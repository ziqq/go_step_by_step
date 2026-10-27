# Lesson 30: HTTP concurrency admission without an unbounded queue

[Русская версия](ru/30-http-admission.md)

Plan at least 40 minutes: read and run the example, then spend at least 30 minutes on the routing assignment. Lesson 29's soft-delete migration remains a separate exercise; this lesson neither changes its SQL nor supplies its solution.

## Why a database pool is not enough

A small SQL connection pool bounds connections, not all waiting HTTP handlers. If requests arrive faster than work completes, waiting requests can still accumulate. Lesson 18 bounded a queue; here we deliberately have **no application waiting queue**: admit a bounded number of synchronous handlers and reject excess work immediately.

Read [the runnable example](../lessons/30-http-admission/main.go) and [its tests](../lessons/30-http-admission/main_test.go). They use only the standard library and `httptest`; no database or listening port is needed. The example is a transport fixture, not an authenticated chat service.

In a chat backend, history reads, message writes, and media processing compete for resources. The reference backend also uses concurrency admission for expensive media work. This lesson extracts only the small local budgeting idea: it does not reproduce that backend's distributed quotas or change its code.

## One wrapper, one budget

`limitConcurrency(max, next)` rejects nonpositive limits and a nil handler at construction. Construct it once and reuse the returned handler. Constructing one wrapper **per request** gives every request its own empty budget and imposes no shared limit.

The central fragment is:

```go
select {
case slots <- struct{}{}:
    defer func() { <-slots }()
    if r.Context().Err() != nil {
        admissionError(w, "request_canceled", false)
        return
    }
    next.ServeHTTP(w, r)
default:
    admissionError(w, "overloaded", true)
}
```

The complete wrapper also checks cancellation **before** this fragment. Sending a token reserves capacity; deferred receiving returns it. `default` means rejection rather than waiting. Never close this semaphore channel during shutdown: requests may still send to it, and channel closure is not the shutdown protocol.

All requests reaching the same returned wrapper share its channel. Two separately constructed wrappers have independent budgets, even if they both use `max = 2`. Multiple processes multiply local capacity; this is not a cluster-wide or per-user limit. It bounds entries into `next.ServeHTTP`, not accepted TCP connections, goroutines already created by the HTTP server, memory per request, or requests per second.

## Cancellation is cooperative; the slot belongs to the handler

An already-canceled request does not enter downstream work. A second check after acquisition narrows a race with cancellation, but cannot make cancellation and handler entry atomic. After entry, the original request and its context reach downstream unchanged. Queries and other blocking operations must still honor that context.

Do not start `next.ServeHTTP` in a detached goroutine and release the token when the client cancels. The handler may still be running: releasing early would admit more work than the budget allows. Keep the slot until the synchronous call returns or unwinds. The same `defer` runs on a panic; the wrapper does not swallow the panic or invent a successful response.

The original `ResponseWriter` is passed directly, without buffering or replacing optional interfaces. Admission does not create a new timeout, stop a handler that ignores cancellation, implement recovery, or make writes safe after `ServeHTTP` returns. Long-lived streams would hold slots for their whole handler lifetime; do not casually apply this buffered-work budget to SSE/WebSockets or health probes.

## Public response policy

When all slots are occupied, this example returns:

```text
HTTP status: 503
Content-Type: application/json; charset=utf-8
Cache-Control: no-store
Retry-After: 1
Body: {"error":"overloaded"}
```

An observed cancellation before handler entry returns `503` with `{"error":"request_canceled"}` and **no** `Retry-After`. This is the lesson's explicit policy, not a standard status that Go automatically sends for canceled clients. A disconnected client may receive no response at all. After handler entry, the wrapper does not overwrite that handler's response on cancellation.

`Retry-After: 1` is a retry hint, not a reservation or proof capacity will exist in one second. This example has no retrying client. Never blindly replay a message-writing POST after an uncertain result: admission does not replace idempotency or resolve an ambiguous commit. `429` typically represents a different per-client rate policy; don't infer it from this global concurrency counter.

## Run and verify

From the repository root:

```sh
go run ./lessons/30-http-admission
go test -count=1 -v ./lessons/30-http-admission
go test -race -count=20 ./lessons/30-http-admission
```

Expected output:

```text
busy: 503, retry-after: 1
first: 204
after release: 204
```

The first request is deliberately held at a channel barrier, so the second meets a full budget. After release, a later request succeeds. No guessed `Sleep` is needed to create overload.

Tests cover invalid configuration, unchanged request/writer/response, two admitted requests plus a rejected burst, slot reuse, pre-canceled requests with both free and occupied capacity, cancellation during handler work, panic propagation with slot release, and deterministic demo output. Watchdog timers only fail a stuck test; channel signals establish ordering. These are local handler tests, **not** network load, production latency, disconnect, or cluster-capacity evidence.

## Assignment: share one work budget and bypass health — not implemented

Build a local `http.ServeMux` composition in this lesson with this contract. Use synthetic handlers only, not real message storage or authentication.

1. `GET /healthz` returns `204` even when work capacity is exhausted. It must not acquire a work slot. Do not interpret this fixture as database readiness.
2. `GET /conversations/{conversationID}/messages` and `POST /conversations/{conversationID}/messages` share **one** budget of 1. Use the existing wrapper rather than changing its admission policy. Both fixture handlers return `204` after their test barrier opens.
3. With a GET held inside the work handler, a POST must return the existing overload JSON, `503`, and `Retry-After: 1`; repeat with POST holding capacity and GET rejected. An independent limiter per route must fail your tests.
4. Prove health still completes during both saturation cases, rejected work never reaches either fixture, and capacity is reusable after release. Unknown paths return `404`; unsupported methods on known routes return `405`, without consuming a work slot. Decide routing before admission and preserve the request/context.
5. Add independent tests with channels and finite watchdogs, including an already-canceled work request, and run them with the race detector. Keep the published wrapper tests. Do not add a waiting queue, retries, metrics framework, distributed leases, or solve earlier assignments.

Completion: tests demonstrate a single shared budget across both work routes, health bypass, correct routing errors, cancellation, and reusable capacity. Explain why constructing a limiter separately for each route would double the intended aggregate capacity. The composition and its new tests remain yours.

## Final checks and common mistakes

```sh
gofmt -l lessons/30-http-admission/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier PostgreSQL lessons need a disposable database and `POSTGRES_TEST_URL` for their SQL integration tests; without it those tests skip. Lesson 30 itself does not need Docker. Do not confuse a passing unit suite with verified database behavior.

Common mistakes: building the limiter inside each request; using a blocking send and creating an implicit waiting queue; releasing on cancellation while work continues; losing tokens after a panic; closing the channel at shutdown; giving each route an independent budget; limiting health together with expensive work; confusing concurrency with rate or authorization; treating a retry hint as permission to duplicate a write.

Sources: [Go select semantics](https://go.dev/ref/spec#Select_statements), [request context lifetime](https://pkg.go.dev/net/http#Request.Context), [cooperative context cancellation](https://pkg.go.dev/context#Context).
