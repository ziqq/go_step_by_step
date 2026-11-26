# 52. Reconcile a repeated send with a durable command key

[Русская версия](ru/52-command-idempotency.md) · [Runnable example](../lessons/52-command-idempotency/main.go) · [Schema](../lessons/52-command-idempotency/schema.sql)

Lesson 51 separated operation outcome from reply delivery. A connection can disappear after a write commits. Repeating that send with a fresh key creates another message. This lesson retains an immutable send row that also acts as its receipt: the same tenant, actor, key, conversation, and body return the stored ID. Reusing the key with changed input returns a conflict.

The reference chat dispatcher builds CommandMeta from trusted tenant/user identity, a command ID, method, and request hash. This example studies one operation with small integer IDs and exact stored input. It does not reproduce that backend's full protocol. Authentication and current conversation authorization belong before every Send call, including replays; callers must supply trusted tenant/actor IDs.

## Start a disposable PostgreSQL database

The following password is a public local-demo value. Use only a disposable database. The container keeps data in memory and exposes PostgreSQL on loopback. Run it in one terminal; wait for the ready message. Stop it with Ctrl-C after the checks.

```sh
docker run --rm --name go-lesson52 \
  --tmpfs /var/lib/postgresql/data:rw,size=512m \
  -e POSTGRES_PASSWORD=lesson52_local \
  -p 127.0.0.1:55452:5432 postgres:17-alpine
```

In another terminal, from the repository root:

```sh
export DATABASE_URL='postgres://postgres:lesson52_local@127.0.0.1:55452/postgres?sslmode=disable'
export POSTGRES_TEST_URL="$DATABASE_URL"
go run ./lessons/52-command-idempotency
go run ./lessons/52-command-idempotency
go test -count=1 -v ./lessons/52-command-idempotency
go test -race ./lessons/52-command-idempotency
go vet ./lessons/52-command-idempotency
```

Both demo runs print:

```text
same message: true
changed input rejected: true
```

The first run stores the fixed demo command. Later runs reuse its receipt. The demo bootstraps only lesson52_sends; it is not a migration runner. Integration tests create and remove their own random schemas. Without POSTGRES_TEST_URL those tests explicitly skip, so a green local unit run alone does not verify database behavior.

## Follow the database decision

1. Check context, then validate positive IDs, a nonblank UTF-8 key of at most 64 bytes, and a nonblank UTF-8 body of at most 280 code points. Reject NUL. Keep accepted input unchanged: leading spaces in a nonblank body are meaningful. The key is separate from lesson 51's correlation ID grammar; this storage example accepts punctuation in keys and binds all values as SQL arguments.

2. Attempt one INSERT with a unique constraint on tenant_id, actor_id, command_key. Conversation is input bound to that key, not part of the uniqueness scope. RETURNING id identifies a new row. A conflict does nothing, so Scan returns sql.ErrNoRows. Identity sequences can have gaps; do not infer the number of committed messages from IDs.

```sql
INSERT INTO lesson52_sends
    (tenant_id, actor_id, command_key, conversation_id, body)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, actor_id, command_key) DO NOTHING
RETURNING id;
```

3. After a conflict, run a separate SELECT scoped by the same tenant, actor, and key. Compare the stored conversation and body exactly in Go. Matching input replays the stored message ID with Created false; changed input returns errConflict and a zero result. The insert winner reports Created true. The separate statement matters when an overlapping insertion commits: the conflicting row need not be visible to the original INSERT snapshot. See PostgreSQL's [INSERT documentation](https://www.postgresql.org/docs/17/sql-insert.html) and [Read Committed behavior](https://www.postgresql.org/docs/17/transaction-iso.html#XACT-READ-COMMITTED).

4. The example never updates or deletes retained rows. Their immutable input is the receipt. Editing body, deleting rows, or expiring a receipt invalidates this design's replay guarantee. A real mutable-message system needs a separate immutable receipt and an explicit retention policy. This lesson's schema and contract retain records without cleanup; database-role enforcement and lifecycle migrations are outside this sample.

5. An insert I/O error can conceal a committed result. Return an error without inventing success or claiming rollback. Reconcile with the same scope, key, and input under the application's bounded retry policy. A conflict must never disclose the old body or ID. Created is diagnostic information, not permission to publish an event after the transaction: a crash between send and publication would lose the event. That is the practice below.

## Practice · Commit one outbox event for a new send

Extend this lesson's Send to atomically persist the winning send and one message.created outbox event, combining its durable key with lesson 27's transaction boundary. Keep the result type and existing behavior. Do not add a worker or network publication.

1. Add a lesson-specific outbox table with a unique message_id reference and an event containing the persisted message ID and conversation ID. Run the insert, replay lookup when needed, and new-event insert within one explicit READ COMMITTED transaction. A replay emits no new event. Return Created true only after the new send and event commit successfully. Roll back on any preceding error; commit failure returns a zero result and an error.

2. Preserve exact scope/input matching and the immutable receipt. Matching concurrent calls must return one message ID and leave one send and one event. Mismatched body or conversation returns a conflict without an event. Distinct tenant/actor/key scopes remain independent. Do not solve uncertainty by changing the key or by adding an in-memory deduplication map.

3. Add database tests for a successful new send, replay after deliberately discarding the first reply, simultaneous duplicates, conflicting payloads, outbox insertion failure, and a deferred constraint that fails at commit. Assert both table counts, matching event data, zero results on failure, and successful retry with the same key after a rolled-back transaction. Keep all original tests. Explain why a lost commit acknowledgement may still require reconciliation and why an idempotent write does not guarantee event delivery.

```sh
gofmt -w lessons/52-command-idempotency/main.go lessons/52-command-idempotency/main_test.go
go test ./...
go test -race ./...
go vet ./...
```

## Common mistakes

- SELECT before INSERT without a unique constraint races across processes.

- Putting conversation in the unique key allows one command key to create messages in different conversations.

- Matching only the key silently accepts changed input.

- Retrying with a fresh key after a lost response creates a new operation.

- Publishing an event after an autocommitted send leaves a crash window.

- Removing or editing the immutable receipt turns a replay into a different operation.

## Completion criteria

Run the demo twice against the same disposable database. Show one stored ID for repeated input, rejection of changed input, and isolation between trusted scopes. Complete the atomic-outbox practice without solving it through post-commit publication. Pass all database tests and root checks with POSTGRES_TEST_URL set; distinguish retained write results from response delivery and event delivery.
