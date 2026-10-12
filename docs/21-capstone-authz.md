# Lesson 21: Capstone II — authorize message writes by conversation membership

Estimated time: 40–55 minutes.

Lesson 20 separated the HTTP handler, application operation, and storage adapter. This step adds a security boundary to that flow: a verified principal may write only to a conversation where the membership policy allows it. Authentication and authorization are different checks. A signed-in user is not automatically allowed to use every conversation.

The target chat backend accepts authenticated command envelopes and resolves active membership in PostgreSQL. This small REST example is for learning the boundary; it is not an implementation of that backend's protocol. It does not parse JWTs or authenticate credentials. `withDemoPrincipal` only simulates a principal that trusted authentication middleware has already verified. Never use the demo helper to authenticate a real request.

## Run the example

```sh
go run ./lessons/21-capstone-authz
go test ./lessons/21-capstone-authz
```

```text
201 Created sender=alice message=1 conversation=chat-1
```

The order matters: require a principal, validate the request shape, ask the membership policy, and only then call the writer. A client-supplied `user_id` is rejected as an unknown JSON field; the sender ID comes from the verified principal context. Go recommends context values only for request-scoped data and an unexported key type to avoid collisions; see the [`context` package guidance](https://pkg.go.dev/context#WithValue).

For protected operations, deny by default and check permission on every request. If the policy service fails, the handler returns a generic server error and does not write. OWASP's [Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) recommends validating permissions on every request and denying by default.

## HTTP and authorization contract

- Missing or invalid server-side principal: `401 Unauthorized`.
- Authenticated principal without membership: `403 Forbidden`.
- Malformed, oversized, unknown-field, or invalid message request: stable JSON `400 Bad Request`.
- Membership dependency failure: generic `500 Internal Server Error` (or `504` for a deadline); do not fall through to the writer.
- Authorized creation: `201 Created`; `sender_id` is derived from the principal, never the request body.
- Unsupported method: `405 Method Not Allowed`.

The example's membership callback is a tiny fixed rule for demonstration. In a real service, query current membership using the verified user ID and requested conversation ID. Keep the check close to the protected operation and pass the authenticated scope into database predicates as appropriate. A middleware authentication check alone does not prove conversation access.

## What the tests prove

Focused tests verify that unauthenticated and non-member requests never call the writer, a forged `user_id` is not accepted, membership errors fail closed, and an allowed member's ID becomes the sender. They also cover invalid JSON/input, the Unicode length boundary, oversized bodies, generic error responses, and method handling. This local test suite does not verify JWT cryptography, PostgreSQL membership queries, or the live chat backend.

```sh
gofmt -w ./lessons/21-capstone-authz
go test ./lessons/21-capstone-authz
go test -race ./lessons/21-capstone-authz
```

## Exercise — add a protected message read

Add `GET /conversations/{conversationID}/messages/{messageID}` to this example:

1. Read the user ID only from the verified principal in request context; never accept it from a path, query, or JSON field.
2. Check membership for every request before invoking the message reader. A denied request must not call the reader.
3. Scope the lookup by both conversation ID and message ID so a valid ID from another conversation cannot be returned.
4. Return `401` without a principal, `403` for a non-member, `404` for a missing message in an authorized conversation, and a stable generic `500` if authorization or storage fails. Do not expose internal errors.
5. Add tests for an allowed read, missing principal, non-member, missing message, mismatched conversation/message IDs, and dependency failure. Assert that denied requests never reach the reader.

Keep the demo identity helper test-only in meaning: do not implement custom JWT parsing. Plan at least 35 minutes. Completion means both positive and negative access cases pass, the database-facing lookup includes the conversation scope, and the race detector reports no shared-state race.

## Typical mistakes

- confusing authentication (who is calling?) with authorization (may this caller access this conversation?);
- taking `sender_id` or `user_id` from untrusted JSON;
- treating a valid JWT as proof of membership;
- checking membership after writing or reading protected data;
- allowing access when the membership dependency times out or returns an error;
- checking membership only on the first page or first request;
- using a global message ID lookup without constraining it to the authorized conversation;
- treating the demo principal helper as production authentication.

## Completion criteria

You can trace how trusted identity reaches the handler, explain why membership is a separate per-resource check, show that denied requests do not reach storage, and state what still needs PostgreSQL-backed membership and real authentication before deployment.
