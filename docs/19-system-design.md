# Lesson 19: System design — contracts, pagination, caching, and workers

Estimated time: 35–50 minutes.

This lesson combines topics from the production part of the course into one small, testable design. The runnable example implements a message-list HTTP contract with keyset pagination. It is an educational REST example, not a claim that the reference chat backend exposes this exact route: its current message-list flow uses authenticated commands and opaque cursors. Treat that backend as a read-only source of realistic constraints.

## Run the example

```sh
go run ./lessons/19-system-design
go test ./lessons/19-system-design
```

The example sorts messages by `created_at DESC, id DESC`. The ID is a stable tie-breaker when timestamps match. It reads one extra row to decide whether another page exists, then creates a cursor from the final row returned. The cursor is URL-safe Base64 around JSON for readability in this exercise; it is not encrypted or signed, and it is not an authorization credential.

For a database-backed endpoint, apply the same comparison in SQL and use a matching index. PostgreSQL notes that pagination without a predictable `ORDER BY` can return inconsistent subsets, and that skipped `OFFSET` rows still need to be computed. See [LIMIT and OFFSET](https://www.postgresql.org/docs/current/queries-limit.html).

## Contract to preserve

`GET /messages` accepts `conversation_id`, optional `limit` (default 20, range 1–50), and optional `cursor`. A successful response contains `messages` and `next_cursor`; the cursor is empty on the last page. Missing conversation IDs, malformed cursors, cursors for another conversation, and out-of-range limits return a JSON `400` error. The method is GET only.

In a real service, validate the caller's authorization for the conversation on every request, including requests with a valid cursor. Bind cursor state to the query scope and ordering; do not treat an opaque token as proof of access. Keep the error shape and ordering stable so clients can safely advance through pages.

## Caching private responses

If you cache a user-specific message list, the cache key must include every input that changes the representation: authenticated principal or authorization scope, conversation, filters, cursor, limit, and representation version. A cache keyed only by URL can leak one user's data to another. For private data, choose an explicit policy such as `Cache-Control: private, no-cache` when private storage with revalidation is acceptable; use `no-store` when it is not. `private` prevents shared-cache storage, while `no-cache` requires validation before reuse. See [RFC 9111, HTTP Caching](https://www.rfc-editor.org/rfc/rfc9111.html).

An ETag can identify a representation. A client sends it later in `If-None-Match`; when the selected representation has not changed, answer `304 Not Modified` without a body. The validator must change when any represented field changes, not merely when the page's row count changes. Conditional request semantics are defined by [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html).

Cache invalidation is part of the write contract: a new message can change the first page and its cursor, while a read-state update can change fields even when membership and order stay the same. Decide which events invalidate or refresh each affected key. Avoid caching authenticated content in a shared cache unless the authorization model explicitly supports it.

## Background work and service evolution

Do not start an unbounded detached goroutine from a request handler for durable work. If a database transaction must also trigger delivery, persist an outbox record in the same transaction; a worker can claim it, retry with bounds, and make processing idempotent. This connects to Lesson 18's queue and backpressure exercise. Define what happens after repeated failure, how duplicate delivery is handled, and which observable signal tells an operator that work is stuck.

Prefer additive API evolution: optional response fields and new optional query parameters are usually easier for old clients than changed meanings, ordering, or error codes. Keep cursor decoding compatible for the lifetime of issued cursors, or version the cursor format and define an expiry. Breaking changes need an explicit migration path and client/server rollout plan.

## Exercise — conditional caching

Implement conditional caching for the example endpoint without changing the pagination contract:

1. Generate an ETag for the exact response representation and return it with a private-cache policy.
2. When `If-None-Match` matches, return `304 Not Modified` with no response body; a changed representation must produce a different validator.
3. Add focused tests for a cache hit, a changed message, and isolation between two conversations or authorization scopes. Include at least one case where a represented field changes but the number of messages does not.
4. Write a short design note describing which writes invalidate the cache and whether invalidation is synchronous or delivered through an idempotent outbox worker.

Do not implement a shared cache or background worker in this exercise. First make the validator and HTTP contract correct. Run the focused tests, then the full repository checks. Completion means the tests cover both `200` and `304`, the `304` body is empty, and the design note names the cache key and invalidation events.

## Common mistakes

- Paginating by timestamp alone and skipping rows that share a timestamp.
- Using `OFFSET` for deep pages without considering query cost or concurrent inserts.
- Reusing a cursor with a different conversation, filter, or ordering.
- Treating Base64 as encryption, integrity protection, or authorization.
- Omitting identity or query scope from a private response cache key.
- Returning a `304` with a body or keeping the same ETag after represented data changes.
- Making a worker retry forever, process duplicates non-idempotently, or lose work between commit and enqueue.

## Checks and completion

```sh
gofmt -w lessons/19-system-design/*.go
go test ./lessons/19-system-design
go test ./...
go test -race ./...
go vet ./...
```

You are done when you can explain the stable sort key, cursor scope, cache key, ETag validation, invalidation path, and the reason a durable background task needs an outbox or equivalent atomic handoff.
