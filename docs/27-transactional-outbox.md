# Lesson 27: one transaction for a message and its outbox event

[Русская версия](ru/27-transactional-outbox.md)

Plan 55–80 minutes: 10–15 minutes reading, 10–15 minutes running the example, and at least 35 minutes on the assignment.

## The failure window

Lesson 26 packaged an identifiable binary. Now examine a runtime failure: saving a message and then enqueueing an event in memory leaves a gap. If the process stops between those operations, the message exists but the event is lost. Reversing the order can announce a message that never commits.

The example stores the message and its delivery intent in the same PostgreSQL transaction. It does **not** send notifications. A later consumer reads committed events; processing and retry policy are separate responsibilities.

## Run against a disposable database

Use Go, Docker, and `openssl`. From the repository root, start an isolated PostgreSQL instance with a random password, a random loopback-only port, and temporary data:

```sh
lesson27_password=$(openssl rand -hex 24)
lesson27_container=$(docker run --rm -d \
  --tmpfs /var/lib/postgresql/data \
  -p 127.0.0.1::5432 \
  -e POSTGRES_USER=course -e POSTGRES_DB=course \
  -e POSTGRES_PASSWORD="$lesson27_password" postgres:17-alpine)
docker exec "$lesson27_container" pg_isready -U course -d course
```

If the last command reports that PostgreSQL is not ready, repeat that readiness command before continuing. The image tag is for local practice, not a pinned production deployment. Never point this example or its tests at a production/shared database. Do not print or commit the connection URL: it contains the temporary password.

```sh
lesson27_port=$(docker port "$lesson27_container" 5432/tcp | awk -F: '{print $NF}')
export DATABASE_URL="postgres://course:$lesson27_password@127.0.0.1:$lesson27_port/course?sslmode=disable"
export POSTGRES_TEST_URL="$DATABASE_URL"
go run ./lessons/27-transactional-outbox
go test -count=1 -v ./lessons/27-transactional-outbox
```

Example output; identifiers and time vary:

```json
{"message":{"id":1,"conversation_id":"lesson-chat","text":"hello atomic outbox","created_at":"2026-10-21T06:30:00Z"},"event_id":1}
```

Each run creates another message. This writer is intentionally **not idempotent**.

## Trace the transaction

Read [main.go](../lessons/27-transactional-outbox/main.go) and [schema.sql](../lessons/27-transactional-outbox/schema.sql).

1. Normalize the draft before database access. Conversation IDs contain 1–80 ASCII letters, digits, underscores, or hyphens. Trimmed text contains 1–280 Unicode code points, valid UTF-8, and no NUL character.
2. Start one `sql.Tx` and defer its rollback.
3. Insert `lesson27_messages` with parameters; `RETURNING` retrieves the database-generated ID and timestamp.
4. Insert `lesson27_outbox` using the **same transaction**. Its `message_id` is a unique foreign key. The JSON payload carries only the message ID and conversation ID; the event type is `message.created`.
5. Commit before returning the message and event ID.

The function returns an empty `Created` value on any error. In particular, generated IDs alone are not success:

```go
if err := tx.Commit(); err != nil {
    return Created{}, fmt.Errorf("commit message and outbox: %w", err)
}
return result, nil
```

After a successful commit, the deferred rollback is harmless. A failed second insert rolls back the message too. Do not replace either transactional call with `db.ExecContext`: that would execute outside this transaction. Sequence values can have gaps after rollback; do not test for gap-free IDs or infer commit order from them.

`applySchema` embeds and bootstraps these two lesson tables. `CREATE TABLE IF NOT EXISTS` is not a migration strategy for upgrading an existing schema; compare Lesson 23's versioned migrations. The sample does not include authentication, tenant isolation, membership validation, an HTTP endpoint, a publisher, or delivery acknowledgments. Combining it with Lesson 21 needs a coherent authorization-and-write contract, not an unprotected route to this store.

The read-only reference `tetradka/chat/backend/internal/storage/postgres/repository.go` also inserts messages and outbox records through one transaction, but additionally handles tenant/chat state, recipients, command results, and push jobs. Its `message.new` event is not this lesson's simplified protocol.

## What the tests prove

Unit tests cover normalization, invalid/oversized text and IDs, missing configuration, and already-canceled requests. SQL-looking message text is data, not executable SQL.

With `POSTGRES_TEST_URL` set, integration tests create a randomly named schema per subtest and remove only that owned schema. They check matching committed rows and payload, an outbox constraint failure after message insertion, a deferred foreign-key failure **at commit**, no rows after validation/cancellation rejection, and eight concurrent independent writes.

The commit-failure test adds a deliberately impossible deferred constraint in its private test schema. It is not a production failure hook. Both tables must remain empty after that known rejection.

Without `POSTGRES_TEST_URL`, the database cases are skipped, not verified. These tests do not simulate a database crash, network loss during commit, or external delivery. A lost commit response can leave the outcome uncertain: do not blindly retry this non-idempotent writer or claim that every commit error proves no write happened.

## Assignment: an idempotent local consumer — not implemented

Build **one consumer** that turns committed outbox events into a per-conversation message count. Keep it inside PostgreSQL for this exercise; do not add a queue, network delivery, polling loop, or goroutine system.

The method you add should have this contract:

```go
func (s *outboxStore) Consume(ctx context.Context, eventID int64) (applied bool, err error)
```

1. Reject nonpositive IDs and report a missing event distinctly. Load the stored event, validate its type and payload against its referenced message, and do not trust a caller-supplied conversation ID.
2. Add a receipt keyed by `(consumer_name, event_id)` for consumer `conversation-count-v1` and a separate per-conversation counter table.
3. Record the receipt and increment the counter atomically. Return `true, nil` only after a successful commit. Redelivery of an already-applied event returns `false, nil` without incrementing again.
4. Concurrent calls for the same event must create one receipt and one increment. Two different events for one conversation must both increment without a lost update.
5. Test first delivery, sequential and concurrent redelivery, distinct events, missing/invalid events, cancellation, and a forced counter-write failure. After the forced failure there must be no receipt; retrying after removing that test constraint must apply once.
6. Preserve all writer tests. Explain why removing receipts can allow duplicates again, and why this local transaction does not guarantee exactly-once email, push, or WebSocket delivery.

No receipt/counter schema or consumer implementation is supplied. Completion means the method and failure-focused integration tests pass against a disposable PostgreSQL instance, and you can explain the transaction boundary without claiming external delivery guarantees.

## Checks and cleanup

With the temporary database still running:

```sh
gofmt -l lessons/27-transactional-outbox/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

`-p 1` runs packages sequentially; assertions are unchanged. When finished, stop **only the container ID saved above**. Its temporary database is intentionally lost:

```sh
docker stop "$lesson27_container"
unset DATABASE_URL POSTGRES_TEST_URL lesson27_password lesson27_port lesson27_container
```

## Typical mistakes and sources

Common mistakes: enqueueing after commit only in memory; mixing `db` and `tx` calls; returning IDs before commit; assuming repeated `Create` calls deduplicate; treating an outbox row as proof of delivery; marking a receipt before its effect commits; testing SQL behavior only with a fake; calling skipped integration tests a pass.

- [Executing transactions in Go](https://go.dev/doc/database/execute-transactions)
- [PostgreSQL 17 transactions](https://www.postgresql.org/docs/17/tutorial-transactions.html)
- [PostgreSQL RETURNING](https://www.postgresql.org/docs/17/dml-returning.html)
