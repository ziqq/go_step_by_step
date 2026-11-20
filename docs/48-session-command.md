# 48. Decode one session command before dispatch

[Русская версия](ru/48-session-command.md) · [Runnable example](../lessons/48-session-command/main.go)

Lesson 47 bounds the frame payload. Now turn those bytes into a command before passing it to application code. The reference chat backend reads a hello frame and validates it before creating a device session. This lesson uses a small send command to study that boundary.

## Run and inspect

```sh
go run ./lessons/48-session-command
go test -count=1 -v ./lessons/48-session-command
go test -race ./lessons/48-session-command
go vet ./lessons/48-session-command
```

```text
send to conversation 7: hello
```

The input is one complete payload whose size has already been checked by its transport owner.

```json
{"type":"send","conversation_id":7,"body":"hello"}
```

## Follow the checks

1. Create a fresh decoder and destination for each payload. DisallowUnknownFields rejects fields outside the struct. The command contains client input only. The authenticated session supplies the principal, and application code checks conversation membership before writing.

2. Decode into a pointer so top-level null can be rejected explicitly. Require type send, a positive int64 conversation ID, and a body containing a non-whitespace character. Keep the original body after checking it so validation preserves the sender's spacing.

3. Decode again and require io.EOF. Trailing whitespace is valid. A second value, including null, and malformed trailing bytes fail. Return a zero command on every error so partially populated fields cannot reach dispatch.

4. Wrap decoder errors with %w. The tests use errors.As for syntax and type errors and errors.Is for semantic and extra-value errors. Map these to a stable public protocol error at the connection boundary. Avoid returning parser details or the payload in public errors or logs.

The standard decoder accepts repeated object keys and matches struct fields without regard to case. DisallowUnknownFields alone leaves those behaviors enabled. The practice below tightens field-name handling. [encoding/json documentation](https://pkg.go.dev/encoding/json)

## Practice · Require unique canonical command keys

Extend decodeCommand so every top-level key appears at most once and has exactly one of the decoded names type, conversation_id, and body. Complete this as one change to the input contract.

1. Inspect object keys before decoding into sendCommand. Compare decoded JSON strings so type and its escaped spelling \u0074ype refer to the same key. A single escaped spelling of a canonical name is valid. Reject case variants such as Type and CONVERSATION_ID. A duplicate is invalid even when its values are equal.

2. Preserve the existing accepted command, whitespace handling, semantic checks, second-value rejection, zero-on-error result, and wrapped decoder causes. Keep missing-field and null validation. Choose a distinguishable error for duplicate or noncanonical keys. Perform all checks before returning a command to dispatch.

3. Add tests for duplicate type, conversation_id, and body, both equal and conflicting values, an escaped duplicate, an escaped canonical singleton, case aliases, and a valid command with reordered fields. Keep malformed JSON, unknown fields, nested values of the wrong type, trailing values, and original tests covered. A map populated after decoding has already lost repeated keys.

4. Run focused tests, then the root checks below. Explain which layer owns byte limits, command validation, trusted identity, and conversation authorization. Explain why a successfully parsed command still needs authorization before a side effect.

```sh
gofmt -w lessons/48-session-command/main.go lessons/48-session-command/main_test.go
go test ./...
go test -race ./...
go vet ./...
```

## Common mistakes

- Calling Decode once accepts an additional value after the command.

- Reusing a destination across frames can carry fields omitted by the next payload.

- Returning a partially decoded command lets an error path look usable.

- Assuming unknown-field rejection also rejects duplicate keys hides an ambiguity.

- Taking a user_id from the payload confuses client input with authenticated identity.

## Completion criteria

Run the demo and trace each check. Complete the unique-key exercise and its positive and negative tests. Both original and new tests must pass, including the race detector. Explain the transport, parser, and authorization boundaries in your own words.
