# Lesson 25: Capstone VI — private history access logs

[Russian version](ru/25-history-access-logs.md)

Estimated time: 50–65 minutes: 10 minutes reading, 10 running and inspecting records, then 30–45 minutes on the exercise.

Lesson 24 tested the public HTTP contract. The next capstone step adds operational evidence: which route finished, with what status, and how long it took. Chat history contains private identifiers and message content, so access records use an explicit field allowlist rather than serializing a request.

## Run and inspect

```sh
go run ./lessons/25-history-access-logs
```

The program sends two in-process requests through a demo mux and emits one JSON record per normally completed request. Their statuses are `200` and `404`; the matched route is a fixed template, while the unknown route is `unmatched`. One record looks like this; its time and duration vary:

```json
{"time":"2026-10-16T06:35:00Z","level":"INFO","msg":"http request completed","method":"GET","route":"GET /conversations/{conversationID}/messages","status":200,"duration_ms":0.042}
```

The demo route only returns an empty JSON fixture. It has no authentication, database, or message-loading behavior. Keep lesson 24's identity/error boundary and lesson 23's membership transaction when composing a real service. The target chat backend also uses authenticated command envelopes and WebSockets; this buffered REST logging example does not implement or instrument that whole protocol.

## A deliberately small access record

The logger is Go's standard `log/slog.JSONHandler`. It adds time, level, and message; the middleware adds only these fields:

| Field | Value source |
|---|---|
| `method` | A fixed list of ordinary HTTP methods; everything else becomes `OTHER` |
| `route` | The allowlisted history template; everything else becomes `unmatched` |
| `status` | First final response status, or the normal implicit `200` |
| `duration_ms` | Elapsed handler time measured by the server |

Raw URLs, query strings, conversation/user IDs, headers, cookies, and request/response bodies are not inspected by the logger. Do not enrich the supplied logger with those values elsewhere and expect this middleware to remove them. The test checks the complete field set as well as the absence of synthetic secrets.

The sample accepts a `*http.ServeMux` and obtains a candidate label with `ServeMux.Handler`, then invokes `ServeHTTP` for real dispatch and wildcard extraction. Only the exact configured history template is allowed into a record. This extra lookup keeps the example compatible with the repository's Go 1.22 API baseline. `Request.Pattern` was introduced in [Go 1.23](https://go.dev/doc/go1.23#nethttppkgnethttp). Do not silently raise the module baseline just to use it.

Keep the explicit allowlist when adding a route: mux-generated redirects can return a request-derived redirect path as their pattern. Unknown paths, unsupported methods, and those redirects must not leak an ID through the label. See [ServeMux.Handler](https://pkg.go.dev/net/http#ServeMux.Handler).

## Recording status without changing the response

`statusWriter` delegates headers and body writes to the original writer. The first body write implies `200`; a later final `WriteHeader` cannot replace an already committed status. Informational responses such as `103` do not become the final status. `101` is treated as terminal, but protocol upgrades are outside this sample's scope.

The middleware writes its completed record after the mux returns normally. It does not recover a panic or label one as a successful empty response. In a running Go HTTP server, panic handling belongs to the server/recovery policy and its diagnostic channel. A status record is evidence of handler behavior, not proof that the client received every response byte.

`Unwrap` allows `http.ResponseController` to reach capabilities of the original writer. It does not preserve optional interfaces through direct type assertions. Use this wrapper only for ordinary buffered endpoints; streaming and WebSocket handlers need instrumentation tested for their required interfaces. See [ResponseWriter](https://pkg.go.dev/net/http#ResponseWriter), [ResponseController](https://pkg.go.dev/net/http#ResponseController), and [JSONHandler](https://pkg.go.dev/log/slog#JSONHandler).

## Checks and what they prove

```sh
gofmt -w ./lessons/25-history-access-logs
go test -count=1 -v ./lessons/25-history-access-logs
go test -race -count=1 ./lessons/25-history-access-logs
go test ./...
go test -race ./...
go vet ./...
```

Focused tests cover the field allowlist, synthetic private values in all input/body locations, response preservation, implicit and explicit status, duplicate final headers, informational headers, unknown/redirected routes, unknown methods, controller unwrapping, panic propagation, and complete concurrent JSON records. They do not prove production log retention, collector availability, real network delivery, or privacy of other application logs. PostgreSQL integration remains separate and must be reported as skipped without `POSTGRES_TEST_URL`.

## Exercise — correlate one request without trusting client IDs

Add one server-generated request ID across the response, handler context, and completed access record.

1. Generate 16 random bytes with `crypto/rand` and encode them as 32 lowercase hexadecimal characters. Inject the generator as a function so tests can supply deterministic IDs and failures; do not add an external UUID library.
2. Store the ID under a private context key and provide a small accessor. Derive from the incoming context so its cancellation, deadline, and existing values survive.
3. Set `X-Request-ID` before invoking the mux, pass the ID to the handler via context, and add exactly that value to the access record. Replace any client-provided header; never treat a correlation ID as authentication.
4. On generator failure, return a generic `500`, do not call the mux, and emit a sanitized instrumentation-failure record without the raw error or an invented request ID.
5. Add tests for ID format, header/context/log equality, a forged input header, preserved parent context, generator failure, and independent IDs in concurrent requests. Update the successful-record allowlist only for the new `request_id` field; keep the privacy and response-contract tests meaningful.

Plan 30–45 minutes. Completion means one ID follows one request through all three observations, concurrent requests do not share mutable request state, and no secret/error text appears in records.

## Typical mistakes

- Logging the entire request, raw path, query, or error object for convenience.
- Copying a mux redirect path into a supposedly safe route field.
- Treating JSON encoding alone as redaction.
- Counting an informational or duplicate header as the final status.
- Calling an HTTP status proof of client delivery, or assuming `Unwrap` preserves every interface.
- Reusing a client ID, replacing the request context with `Background`, or storing one request's ID in shared middleware state.
- Presenting a request ID as distributed tracing or as a metric label.
