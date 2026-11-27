# 53. Keep membership valid until a message commits

[Русская версия](ru/53-membership-lock.md) · [Runnable example](../lessons/53-membership-lock/main.go) · [Schema](../lessons/53-membership-lock/schema.sql)

Lesson 52 requires current authorization even when a command is repeated. A plain membership SELECT followed by a later INSERT has a gap: another transaction can revoke access between them. Here one READ COMMITTED transaction locks the active membership row, writes one message, and commits before releasing the lock.

The reference chat backend checks membership inside a message transaction and uses row locks for mutable chat state. This smaller example isolates one membership rule; it is not a reproduction or audit of that backend. Tenant and actor IDs must come from verified authentication. A conversation ID alone is not authority. Other policies, HTTP replies, idempotency, and outbox delivery remain separate responsibilities. In particular, this lesson does not solve lesson 52's outbox practice.

## Run with disposable PostgreSQL

Use only a disposable database. The password below is a public local-demo value. Run the loopback-only container in one terminal and wait for readiness; stop it with Ctrl-C after checking the lesson.

```sh
docker run --rm --name go-lesson53 \
  --tmpfs /var/lib/postgresql/data:rw,size=512m \
  -e POSTGRES_PASSWORD=lesson53_local \
  -p 127.0.0.1:55453:5432 postgres:17-alpine
```

From the repository root in another terminal:

```sh
export DATABASE_URL='postgres://postgres:lesson53_local@127.0.0.1:55453/postgres?sslmode=disable'
export POSTGRES_TEST_URL="$DATABASE_URL"
go run ./lessons/53-membership-lock
go test -count=1 -v ./lessons/53-membership-lock
go test -race ./lessons/53-membership-lock
go vet ./lessons/53-membership-lock
```

Expected output:

```text
authorized write committed: true
write after revocation denied: true
```

The demo bootstraps lesson-specific tables, activates its fixed demo membership, saves one message, and revokes that membership before trying another send. Each demo run saves another message: there is no command key here. Tests use their own random schemas. Database tests skip without POSTGRES_TEST_URL; a passing unit-only run is not database evidence.

## Hold the decision through the write

1. Check context first, then positive tenant/conversation/actor IDs and nonblank UTF-8 body of at most 280 code points. Reject NUL and preserve accepted text exactly. Invalid input and a missing database return ID zero. Start an explicit READ COMMITTED transaction, and defer rollback for every early return.

2. Select one active membership by all three IDs and acquire FOR SHARE. No matching active row returns errForbidden with ID zero; missing, inactive, and another scope use the same error. Other database failures remain errors, not permission denials.

```sql
SELECT actor_id FROM lesson53_members
WHERE tenant_id=$1 AND conversation_id=$2 AND actor_id=$3 AND active
FOR SHARE;
```

3. Insert through tx, not through db. Keep the transaction short and perform no network calls while holding the lock. Return the generated message ID only after Commit succeeds. Insert or commit failure returns zero. An uncertain commit acknowledgement can hide a committed write; do not retry this non-idempotent operation blindly.

4. If Send holds the shared row lock first, an UPDATE that revokes membership waits until Send commits or rolls back. If revocation holds the conflicting lock first, the locking read waits, then rechecks the updated row after revocation commits. Since active is now false, Send is denied. A rolled-back revocation does not remove access. The order is determined by conflicting lock acquisition, not by which HTTP request started first.

5. FOR SHARE permits other shared membership checks but blocks an UPDATE of active. FOR KEY SHARE is too weak for that non-key update. A foreign key proves existence, not active authorization. This schema deliberately has no membership foreign key on historical messages: revoking or deleting membership must not erase history. Review PostgreSQL's [row-lock modes](https://www.postgresql.org/docs/17/explicit-locking.html#LOCKING-ROWS) and [READ COMMITTED rechecks](https://www.postgresql.org/docs/17/transaction-iso.html#XACT-READ-COMMITTED).

The integration tests observe real server lock waits using pg_stat_activity, rather than guessing ordering with sleeps. One test holds revocation first. Another temporarily blocks the message table to pause Send after its membership read, then proves revocation must wait for that Send. This table lock is a test gate, not the production locking strategy. Every test goroutine is joined. Tests also cover context cancellation, exact text, scope isolation, insertion failure, and a deferred constraint failure at commit.

## Practice · Bound lock waiting per transaction

Add SendWithLockWait(ctx context.Context, d draft, waitMillis int) (int64, error) and errBusy. Preserve Send and its original tests. Keep the membership rule and commit boundary; add no retries, goroutines, NOWAIT, or SKIP LOCKED.

1. Use the same context and draft validation order as Send. Then require waitMillis in 1..5000, returning errInvalid and ID zero otherwise, before touching the database. Give this operation a transaction-local PostgreSQL lock_timeout using a bound value. Do not set a session-wide value on db: a pool can run the write on another connection or leak settings to a later borrower. The timeout limits each lock acquisition, not the whole transaction; callers still need a context deadline.

2. On SQLSTATE 55P03, roll back and return ID zero with an error matching errBusy through errors.Is. Keep cancellation/deadline errors identifiable, and do not relabel forbidden access, insertion errors, or other SQLSTATE values as busy. Return an ID only after commit. No setting may survive commit or rollback into a reused connection.

3. Add database tests with a real held membership lock, a wait budget that expires, a shorter context deadline, successful acquisition after release, an inactive membership, and an unrelated SQL error. Join all goroutines. Use a separate blocker connection and a one-connection operation pool to prove lock_timeout is restored to its previous value after both successful commit and rollback. Assert zero failed IDs, table counts, successful later use, and the original authorization ordering. Do not rely on exact elapsed milliseconds. Explain why a lock timeout is not proof that a request can be retried safely.

```sh
gofmt -w lessons/53-membership-lock/main.go lessons/53-membership-lock/main_test.go
go test ./...
go test -race ./...
go vet ./...
```

## Common mistakes

- Checking membership before opening the transaction leaves a revocation gap.

- Calling db.QueryRowContext after a locking SELECT on tx releases the logical connection between authorization and the write.

- FOR KEY SHARE does not block an ordinary update of the active flag.

- Treating a SQL failure as forbidden hides operational failures; returning the tentative ID before commit invents success.

- Holding the lock during a remote call delays revocation and consumes a pool connection.

- A context deadline bounds waiting, but neither cancellation nor a lost reply proves that a write did not commit.

## Completion criteria

Show that a scoped active member can write, unrelated or revoked membership cannot, and both concurrent lock orders produce the documented result. Pass the real database tests and root checks with POSTGRES_TEST_URL set. Complete the bounded-lock-wait practice and demonstrate that pooled connections retain no modified timeout setting.
