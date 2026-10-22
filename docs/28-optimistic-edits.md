# Lesson 28: optimistic message edits without lost updates

[Русская версия](ru/28-optimistic-edits.md)

Plan 50–70 minutes: 10 minutes reading, 10–15 minutes running the example, and at least 30 minutes on the assignment. Lesson 27's consumer remains your separate, unsolved exercise.

## Two editors, one version

Two clients can read the same message and then submit different edits. An unconditional update lets the later write silently replace the earlier one. Require the version that the client actually read:

```text
Both clients read version 1.
A submits expected_version=1 -> success; stored version becomes 2.
B submits expected_version=1 -> rejection; A's text remains stored.
```

Read [main.go](../lessons/28-optimistic-edits/main.go). The comparison and the mutation belong to **one SQL statement**, not a Go-side read followed by an unconditional write:

```sql
UPDATE lesson28_messages
SET body=$4, version=version+1
WHERE conversation_id=$1 AND id=$2 AND version=$3
RETURNING id, conversation_id, body, version
```

Under PostgreSQL's default Read Committed isolation, a competing update can wait for the row and then recheck its predicate. The old version no longer matches after another writer succeeds. “Optimistic” does not mean lock-free: database row-lock waits can still happen, so pass the request context.

## Run with a disposable PostgreSQL

Use Go, Docker, and `openssl` from the repository root. This instance uses temporary storage and a random password on a random loopback-only port:

```sh
lesson28_password=$(openssl rand -hex 24)
lesson28_container=$(docker run --rm -d \
  --tmpfs /var/lib/postgresql/data \
  -p 127.0.0.1::5432 \
  -e POSTGRES_USER=course -e POSTGRES_DB=course \
  -e POSTGRES_PASSWORD="$lesson28_password" postgres:17-alpine)
docker exec "$lesson28_container" pg_isready -U course -d course
```

If PostgreSQL is not ready, repeat the readiness command before continuing. The mutable image tag is for local practice, not a production image pin. Do not use a shared/production database or print/commit the password-bearing URL.

```sh
lesson28_port=$(docker port "$lesson28_container" 5432/tcp | awk -F: '{print $NF}')
export DATABASE_URL="postgres://course:$lesson28_password@127.0.0.1:$lesson28_port/course?sslmode=disable"
export POSTGRES_TEST_URL="$DATABASE_URL"
go run ./lessons/28-optimistic-edits
go test -count=1 -v ./lessons/28-optimistic-edits
```

Example output; the ID varies. Each run creates a new demo row and edits it once:

```json
{"id":1,"conversation_id":"lesson-chat","text":"edited once","version":2}
```

## Storage contract and limits

`EditDraft` carries conversation scope, a positive message ID, the expected version, and the replacement text. Conversation IDs are trimmed and contain 1–80 ASCII letters, digits, underscores, or hyphens. Trimmed text is valid UTF-8, contains 1–280 Unicode code points, and has no NUL. Expected versions must be between 1 and `2^63-2`; rejecting the upper boundary prevents overflowing PostgreSQL `bigint` on increment.

The successful result comes from `RETURNING`, not a locally guessed version or a second SELECT. Even replacement with identical text increments the version when the precondition matches. A successful write is not a promise that no later writer has changed the row by the time a client reads the response.

`sql.ErrNoRows` becomes `ErrEditRejected` for **all** unmatched predicates: stale version, missing ID, or wrong conversation. This sample deliberately does not query again to distinguish those cases. It returns an empty `Message` on every failure and preserves other database errors through wrapping; an SQL constraint failure is not an edit rejection.

Never fetch the current version and automatically retry stale text with it. That would silently discard the other editor's work. The client must reload and decide whether to merge or replace. This example has no idempotency receipt or special “identical retry” success path. A lost response can still leave the caller uncertain about the write.

One SQL statement is sufficient for this one-row mutation. If an edit must also append an outbox event, those changes need one explicit transaction as in Lesson 27; sending an event afterward only in memory recreates the failure window.

The example omits authentication, authorship checks, tenant isolation, deletion state, outbox delivery, and an HTTP server. The conversation predicate is not authorization. The embedded [schema.sql](../lessons/28-optimistic-edits/schema.sql) bootstraps a lesson table, not versioned production migrations.

The read-only reference `tetradka/chat/backend/internal/storage/postgres/message_lifecycle.go` also checks an expected version during editing. Its actual flow additionally checks membership/author/deletion state, maintains history and events, and has different retry semantics. Do not treat this lesson as a replacement for that protocol.

## What the tests cover

Unit tests exercise normalization, malformed/oversized text, invalid IDs/versions, overflow protection, missing database configuration, and already-canceled contexts.

Integration tests use a random private schema per subtest. They verify the stored result, preservation after stale/missing/wrong-scope rejection, exactly one winner among two edits using version 1, same-text version increments, constraint-error classification, and no changes after validation or cancellation rejection. SQL-looking text remains a bound parameter.

Without `POSTGRES_TEST_URL` the SQL cases are skipped. Such a run is not evidence of PostgreSQL concurrency behavior. The tests do not simulate network loss during commit or test authorization.

## Assignment: a version-aware HTTP edit adapter — not implemented

Add one local `net/http` adapter and test it with `httptest`. Do not change the writer's semantics or implement Lesson 27's consumer.

1. Handle `PATCH /conversations/{conversation_id}/messages/{message_id}`. Take scope/ID from the route, never the JSON body. Accept exactly one JSON object with `text` and integer `expected_version`; reject unknown fields, trailing JSON, missing/invalid values, and bodies over 1 MiB.
2. Pass the request context to a small consumer-side editor interface with the existing `Edit(context.Context, EditDraft) (Message, error)` contract. Use a deterministic fake for adapter tests; the existing SQL tests cover the real store.
3. Return `200` with the stored `Message` JSON on success, `400` for invalid input, `413` for the oversized body, `409` with `{"error":"edit rejected"}` for `ErrEditRejected`, and a generic `500` JSON error for unexpected storage failures. These are this application's statuses, not a full HTTP `If-Match` implementation.
4. Do not retry on `ErrEditRejected`, look up a newer version, expose raw SQL errors, or return another conversation's message. Unmatched scope and missing IDs remain the same `409` rejection, not a follow-up existence lookup.
5. Test successful editing, rejection without a second store call, malformed/unknown/trailing/oversized bodies, invalid route IDs and versions, request-context forwarding, and redaction of an internal error marker. Verify that invalid requests never reach the editor.
6. Keep it local: no public server, authentication shortcut, ETag implementation, queue, or new dependency. Describe the trusted-authentication boundary needed before exposing this route in a real service.

Completion: the adapter's focused tests and all existing checks pass; rejected edits do not overwrite data; the HTTP implementation remains yours.

## Checks and cleanup

While the temporary database is running:

```sh
gofmt -l lessons/28-optimistic-edits/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Stop only the temporary container ID saved above; its data is intentionally discarded:

```sh
docker stop "$lesson28_container"
unset DATABASE_URL POSTGRES_TEST_URL lesson28_password lesson28_port lesson28_container
```

## Typical mistakes and sources

Common mistakes: checking a version only in Go; removing the version or scope predicate; assigning the caller's arbitrary new version; confusing a stale edit with every SQL failure; automatically retrying with a newer version; claiming a scoped update proves authorization; treating skipped SQL tests as database evidence.

- [PostgreSQL Read Committed behavior](https://www.postgresql.org/docs/17/transaction-iso.html)
- [UPDATE RETURNING](https://www.postgresql.org/docs/17/dml-returning.html)
- [Go QueryRowContext and ErrNoRows](https://pkg.go.dev/database/sql#DB.QueryRowContext)
