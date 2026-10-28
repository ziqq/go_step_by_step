# Lesson 31: outbound HTTP time and byte budgets

[Русская версия](ru/31-outbound-http.md)

Plan at least 40 minutes, including at least 30 minutes on the JSON-contract exercise. Lesson 30's shared-route admission assignment remains unsolved and unchanged.

## A slot does not bound the work inside it

Lesson 30 limits simultaneous handler work. A dependency that never finishes can still occupy every slot. A huge response can also consume memory even if only one request is active. Add two independent budgets: elapsed time and response bytes.

Open [main.go](../lessons/31-outbound-http/main.go) and [the focused tests](../lessons/31-outbound-http/main_test.go). This small standard-library client reads a synthetic notification-service snapshot; the demo starts its own loopback `httptest` server, performs a GET, and exits. It is not a public proxy or a deployment.

The read-only reference chat backend applies similar timeout, redirect, and response-size boundaries to external provider calls. We use that principle without copying provider credentials or changing the reference project. Lesson 19's simple client stays as published; this is an independent example, not its silent rewrite.

## Reuse one client; preserve the caller's context

The constructor gives the client a one-second timeout and refuses redirect following:

```go
client := &http.Client{
    Timeout: time.Second,
    CheckRedirect: func(*http.Request, []*http.Request) error {
        return http.ErrUseLastResponse
    },
}
```

Reuse this client concurrently; configure it before use, not while requests are running. Each call builds a fresh request with `http.NewRequestWithContext`. The caller's cancellation/deadline remains attached to that request. A pre-canceled context returns before transport access.

With Go's standard transport, the client timeout covers obtaining and reading the response, not only receiving headers. A shorter caller deadline still applies. A custom `RoundTripper` must honor context itself; an arbitrary blocking `Reader` is not made cancelable by a byte limiter. These are bounded-call mechanisms, not real-time scheduling guarantees.

## Headers are not a completed operation

`Do` returning a response does not prove a complete body has arrived. The code immediately defers `Body.Close`, accepts only status `200`, and then reads through a byte boundary:

```go
body, err := io.ReadAll(io.LimitReader(response.Body, maxSnapshotBytes+1))
if err != nil {
    return nil, fmt.Errorf("read snapshot: %w", err)
}
if len(body) > maxSnapshotBytes {
    return nil, ErrResponseTooLarge
}
```

Here the limit is 1024 bytes. Reading only 1024 would silently accept the first 1024 bytes of a larger response; the extra byte distinguishes an exact fit from overflow. Count actual bytes even when `Content-Length` is absent. An empty body is allowed by this transport reader; valid JSON is a separate assignment.

Read errors return no partial payload. Error wrapping preserves `errors.Is` for cancellation and deadlines. Status rejection contains only the numeric status, never the dependency's error body. Other internal errors may contain the endpoint URL: do not return or log raw errors/URLs containing credentials or sensitive query parameters.

The deferred close runs on success, status rejection, overflow, and body-read failure. It is not an unbounded drain: rejected bodies are not fully consumed merely to encourage connection reuse. Reuse may be lost on those paths. With the default transport's automatic gzip decoding, the limit applies to decoded body bytes; it is not a bound on total process memory, headers, compressed traffic, CPU, or active calls. Combine it with admission rather than claiming this client solves all resource limits.

## Redirect and endpoint policy

`ErrUseLastResponse` keeps the redirect response available without sending the next request. Our status check then rejects `3xx` and closes its body. No redirect destination is visited by this policy.

This is **not SSRF protection for an arbitrary initial URL**. The initial endpoint must come from trusted service configuration, not an HTTP query parameter or a submitted message. The demo uses local HTTP solely for tests; use HTTPS with normal certificate verification for a real remote provider. Do not disable TLS verification to make a request succeed.

There is no application retry loop. Standard transport behavior may retry certain eligible requests internally, so do not describe this as an exactly-once network protocol. GET response fetching does not establish idempotency for message-writing POST requests or settle an uncertain database commit. There are no cookies, provider tokens, proxy endpoints, or production readiness decisions in this fixture.

## Run from the repository root

```sh
go run ./lessons/31-outbound-http
go test -count=1 -v ./lessons/31-outbound-http
go test -race -count=20 ./lessons/31-outbound-http
```

Expected synthetic response:

```json
{"service":"notifications","pending":3}
```

Tests cover exact byte limits and overflow with fixed/chunked responses, redirect refusal with zero target visits, cancellation before headers and during a body, a timeout after headers, automatic gzip decoding, body closure, read/transport failures, malformed URLs, pre-cancellation, and concurrent client reuse. Channel barriers establish cancellation order; watchdogs only fail stuck tests. The suite does not prove production latency, TLS/proxy/DNS behavior, or resilience of a deployed provider.

## Assignment: validate the bounded snapshot — not implemented

Add one typed adapter on top of this lesson's reader. Do not add an HTTP server or modify previous lessons.

1. A valid snapshot is one JSON object with exactly two required fields: `service` equals `"notifications"`; `pending` is an integer greater than or equal to zero. Zero is valid and must be distinguished from a missing field. Accept ordinary JSON whitespace and field order changes.
2. Reject malformed JSON, arrays, top-level `null`, missing/null fields, unknown fields, invalid service values, negative/fractional/string counts, and a second JSON document or non-whitespace trailing content. Do not claim the standard decoder automatically rejects duplicate keys or enforces exact key casing; duplicate-key and casing policies are outside this exercise.
3. Return the zero result on every failure. Transport errors must remain discoverable with `errors.Is`, including `ErrResponseTooLarge`, `ErrUpstreamStatus`, cancellation, and deadlines. The adapter must not retry, follow redirects, or replace the caller's context.
4. Add table-driven decoder tests and local-server adapter tests for valid zero/nonzero counts, invalid documents, oversized input, non-200 status, and cancellation. Keep the published transport tests unchanged. The adapter and its tests remain yours.

Completion: callers receive a validated typed snapshot or a zero result plus an error; no partial result is treated as healthy. Explain why `200`, a small body, and a successful first decode alone are insufficient to accept the dependency contract. This fixture does not decide readiness for a real chat backend.

## Checks and typical mistakes

```sh
gofmt -l lessons/31-outbound-http/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier SQL integration tests need a disposable PostgreSQL instance and `POSTGRES_TEST_URL`; otherwise they skip. This lesson itself needs no Docker or external service.

Typical mistakes: a zero client timeout; discarding the parent context; trusting `Content-Length`; treating truncation as success; forgetting to close rejected responses; unlimited draining; accepting a redirect or an arbitrary user URL; leaking upstream diagnostics; mutating a shared client during requests; confusing transport success with JSON validation; blindly retrying writes.

Sources: [HTTP client lifetime and timeout](https://pkg.go.dev/net/http#Client), [request context](https://pkg.go.dev/net/http#NewRequestWithContext), [redirect refusal](https://pkg.go.dev/net/http#ErrUseLastResponse), [byte-limited reading](https://pkg.go.dev/io#LimitReader).
