# Lesson 24: Capstone V — history HTTP contract tests

[Russian version](ru/24-history-api-tests.md)

Estimated time: 50–65 minutes: 10 minutes reading, 10 running and tracing the example, then at least 30 minutes on the exercise.

Lesson 23 owns a PostgreSQL page operation: check membership, constrain the conversation, read a stable page, and finish the transaction. This lesson tests the HTTP boundary around that operation. A correct SQL query alone does not establish that a caller receives the right status, headers, or JSON.

## Run the example

From the repository root:

```sh
go run ./lessons/24-history-api-tests
```

```text
200 OK
403 Forbidden
401 Unauthorized
```

The demo sends three in-process requests: an allowed principal, a denied principal, and no principal. It uses a small fake loader, not a PostgreSQL connection. The identity helper simulates trusted authentication middleware; neither a query parameter nor an unverified Authorization header becomes an identity.

## One boundary, two owners

`historyLoader` accepts context, trusted user ID, conversation ID, optional cursor, and page size. Its argument/result shape follows lesson 23's `ListPage`. The HTTP handler validates transport input and formats public responses; the loader owns membership and storage scope. In PostgreSQL, those checks must stay inside the lesson 23 snapshot transaction.

Each lesson remains an independent executable package. This example does not import or wire lesson 23's `main` package. When composing a real application, declare shared types once and connect the actual adapter; do not copy these standalone teaching packages into a deployed service unchanged.

The reference backend uses authenticated command envelopes and a richer `messages.load` contract. This REST exercise teaches boundary tests, not compatibility with that wire protocol.

## Contract of the handled history route

The route is `GET /conversations/{conversationID}/messages`. It accepts only `limit` and `before`. Unsupported query keys, duplicate values, malformed query encoding, and invalid cursors are rejected. Date filters remain the learner's separate exercises in lessons 22–23.

| Input or outcome | Observable behavior | Loader calls |
|---|---|---|
| Missing verified principal | `401`, JSON error, Bearer challenge | 0 |
| Invalid page parameters | `400`, `invalid_page` | 0 |
| Allowed request | `200`, JSON page, `Cache-Control: no-store` | 1 |
| Wrapped `ErrNotMember` | `403`, `forbidden` | 1 |
| Wrapped `context.DeadlineExceeded` | `504`, `history_timeout` | 1 |
| Unexpected loader error | `500`, `history_unavailable`, no internal error text | 1 |
| Request already canceled | No response body or status write | 0 |

Successful pages use `messages: []` rather than `null`. A next cursor is returned only when `has_more` is true. An adapter claiming another page without a usable next position produces a generic `500`. The standard mux handles unknown paths and unsupported methods with its own `404`/`405` responses; the JSON table describes the handled route.

`errors.Is` preserves the classification of wrapped errors. Public JSON has stable `code` and `message` fields; database connection details and SQL text do not belong there. The handler also rechecks the incoming context after loading so a canceled request does not become a successful response.

## How the tests make the contract observable

Use `httptest.NewRequest` and `NewRecorder` to invoke the real handler. The success test records the dependency arguments and a request-owned context value, then inspects status, headers, one JSON document, empty-array behavior, and the next cursor. Rejection tests prove the loader was never called. A fake returning a wrapped error exercises error mapping without requiring a broken database.

These are HTTP contract tests. They do not prove SQL authorization, real transactions, JWT verification, socket behavior, or client disconnect delivery. Keep lesson 23's opt-in PostgreSQL tests as separate evidence.

Inspect `recorder.Result()` after the handler returns and close the response body. Do not compare an entire `http.Response` with `reflect.DeepEqual`: the recorder API does not promise every field is stable. For cancellation, count actual `WriteHeader`/`Write` calls; a recorder's implicit `200` does not mean a response was sent to a disconnected client. See the official [httptest documentation](https://pkg.go.dev/net/http/httptest#ResponseRecorder.Result).

```sh
go test -count=1 -v ./lessons/24-history-api-tests
go test -race -count=1 ./lessons/24-history-api-tests
```

## Exercise — add a per-request history time budget

Extend `newRouter` to accept a positive `time.Duration` budget and return an error for zero or negative values. Keep the current public page and error contracts.

1. After identity and query validation, derive a child context from the incoming request and pass it to the loader. Release the child context's resources on every return path.
2. Preserve an earlier incoming deadline and propagate client cancellation. Do not replace the request context with `context.Background()`.
3. When the budget expires, return the stable `504 history_timeout` error. If the client context is canceled, do not write a response. Check context state before publishing a result, including a loader that returns success after expiry.
4. Add focused tests for configuration rejection, a bounded child deadline, an earlier parent deadline, parent cancellation, budget expiry, late success, and child-context cleanup after an ordinary successful response. Existing identity, query, and error-redaction tests must still pass.

Plan at least 30–40 minutes. The [context documentation](https://pkg.go.dev/context#WithTimeout) describes child deadlines and cleanup; it does not forcibly stop a loader that ignores context. Keep loading synchronous and cooperative rather than launching work that outlives a timed-out handler.

Use a fake that waits for `ctx.Done()` and signals entry through a channel. Avoid arbitrary sleeps and exact millisecond assertions. Guard a blocking test with a generous watchdog so a missing cancellation produces a clear failure rather than a hanging suite.

## Typical mistakes and completion criteria

- Trusting `user_id` or the contents of an unverified Bearer header.
- Checking only `200` while ignoring headers, JSON shape, scope arguments, or forbidden calls.
- Testing only a bare sentinel while production errors are wrapped.
- Claiming a fake's `403` proves membership SQL works.
- Treating all mux errors as JSON or a recorder's default status as a real network response.
- Adding a timeout but forgetting cleanup, losing the parent context, or returning late success.

You are done when the new budget tests cover both success and failure, no unauthenticated/invalid request reaches the loader, and you can explain which guarantees need PostgreSQL or real network tests.

Run the repository checks:

```sh
gofmt -w ./lessons/24-history-api-tests
go test ./...
go test -race ./...
go vet ./...
```
