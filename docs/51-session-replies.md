# 51. Safe replies to session commands

[Русская версия](ru/51-session-replies.md) · [Runnable example](../lessons/51-session-replies/main.go)

After frame, command, and subprotocol validation, processing produces a result or an internal error. The client needs a stable code. This example maps errors to a small JSON reply without serializing their text. The reference backend queues dispatcher results for one writer; this lesson isolates the public reply boundary, with no socket or database write.

## Run and trace

```sh
go run ./lessons/51-session-replies
go test -count=1 -v ./lessons/51-session-replies
go test -race ./lessons/51-session-replies
go vet ./lessons/51-session-replies
```

```json
{"id":"req-1","code":"ok"}
```

```json
{"id":"req-1","code":"forbidden"}
```

```json
{"id":"req-1","code":"internal"}
```

## Follow the reply

1. replyFor receives an already validated correlation ID and the outcome of one operation. The ID links a response to a request; it is not a principal, permission, or idempotency key. JSON contains only id and code. Field order is shown for readability; clients must not depend on JSON key order.

2. nil maps to ok. errors.Is recognizes wrapped sentinel errors. Policy precedence is invalid_command → forbidden → deadline_exceeded → canceled → internal. For errors.Join, the first matching condition wins regardless of child order. This is an explicit example contract. An unrelated error with the same text remains internal.

3. Unknown errors always map to internal. The mapper never calls Error or includes SQL, tenant, stack, or input-message details. A test error whose Error method panics checks that this boundary does not format private errors. Real internal logs need their own sensitive-data redaction policy.

4. The mapper does not retry an operation, cancel context, or write to a connection. Even canceled or deadline_exceeded does not prove a write rolled back: it might have completed before the error was observed. A lost reply does not imply no effect. Idempotency and commit confirmation remain separate contracts. The writer from lesson 45 sends a reply if the transport is still available.

[errors.Is](https://pkg.go.dev/errors#Is) · [context errors](https://pkg.go.dev/context#pkg-variables)

## Practice · Validate correlation before execution

Add execute(ctx context.Context, id string, run func(context.Context) error) reply as a synchronous boundary before handler invocation. The example does not implement this exercise.

1. Validate the ID first: 1–64 bytes, ASCII letters, digits, hyphen, and underscore only. An invalid ID returns an empty id and code invalid_request_id; never reflect the invalid input or call run. Do not truncate or normalize IDs.

2. For a valid ID, check ctx.Err first; an already canceled or expired context returns through replyFor without run. Next, a nil callback returns internal. Otherwise call run exactly once with the same context and map its result through replyFor. Do not spawn a goroutine, retry, recover panic, or replace the result with a later context check. A nonnil ctx is a caller precondition.

3. Test lengths 0, 1, 64, and 65, every allowed character class, spaces, Unicode, and controls; invalid ID takes precedence over cancellation and nil callback. Cover canceled and expired contexts, nil callback, identity and values of the passed context, exactly one call on success and error, and a callback which cancels context then returns nil: the reply stays ok. Preserve original tests and show invalid IDs are never reflected. Explain why correlation does not provide deduplication and why canceled does not authorize a blind write retry.

```sh
gofmt -w lessons/51-session-replies/main.go lessons/51-session-replies/main_test.go
go test ./...
go test -race ./...
go vet ./...
```

## Common mistakes

- Sending err.Error or selecting a code by error text.

- Replacing a completed callback's success with canceled.

- Publishing an unchecked ID or treating it as identity.

- Claiming a pure mapper guarantees reply delivery.

## Completion criteria

Trace all three results, implement execute without a provided solution, and preserve the original tests. Demonstrate check precedence, context propagation, exactly one callback invocation, and no internal details in JSON. Explain operation outcome versus reply delivery.
