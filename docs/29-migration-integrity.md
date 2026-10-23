# Lesson 29: migration integrity and forward-only schema changes

[Русская версия](ru/29-migration-integrity.md)

Plan 50–65 minutes: 10 minutes reading, 10–15 minutes running the example, and at least 30 minutes on the assignment. Lesson 28's HTTP adapter and Lesson 27's consumer remain separate, unsolved exercises.

## From bootstrap SQL to an upgrade protocol

`CREATE TABLE IF NOT EXISTS` does not upgrade an existing table. Lesson 13 introduced versioned migrations; here we add exact-byte checksums, strict history validation, and real failure/concurrency tests. Read [main.go](../lessons/29-migration-integrity/main.go) and the two [embedded SQL files](../lessons/29-migration-integrity/migrations).

Migration 1 creates `lesson29_messages`. Migration 2 adds the positive `version` column used by optimistic edits, giving existing rows version 1 without replacing their text. These are independent lesson tables: this CLI does not upgrade Lesson 28 or the reference backend.

The local plan is explicitly ordered from 1 without gaps. The database ledger must be an exact prefix of that plan:

```text
plan:    [1:checksum_a, 2:checksum_b]
history: []                       -> apply 1 and 2
history: [1:checksum_a]           -> apply 2
history: [1:checksum_a, 2:checksum_b] -> validate; no repeated DDL
history: [1:changed_checksum]     -> reject before pending DDL
history: [2:checksum_b]           -> reject the missing prefix
history: [1:checksum_a, 2:checksum_b, 3:...] -> reject this older binary
```

Do not “repair” a mismatch by overwriting the stored checksum or deleting ledger rows. Stop, inspect the deployed binary and database, and reconcile through a reviewed operational procedure. An old binary is not allowed to silently downgrade the schema.

## Hash bytes, not SQL meaning

The checksum function hashes the complete embedded file, including comments and trailing newlines:

```go
func checksum(statement string) string {
    sum := sha256.Sum256([]byte(statement))
    return hex.EncodeToString(sum[:])
}
```

The plan's SQL is trusted source code inside the binary, not HTTP input. Do not add a SQL upload endpoint or interpolate user-provided identifiers. Version and checksum ledger values are bound parameters.

A checksum detects changed migration bytes relative to the ledger. It does **not** authenticate a malicious builder, protect a ledger an attacker can rewrite, or compare the live table definition with the intended schema. Manual `ALTER TABLE` changes are outside this detector. Use permissions, review, backups, and separate schema-drift tooling for those risks.

## One transaction, one cooperating lock

The runner performs this sequence on one `sql.Tx`:

```text
validate plan -> BeginTx(Read Committed)
  -> acquire transaction-level advisory lock
  -> create/read ledger -> validate the whole applied prefix
  -> execute pending SQL + insert its checksum
  -> Commit -> report success
```

The lock is taken **before** creating the ledger. Concurrent `CREATE TABLE IF NOT EXISTS` alone is not our synchronization protocol. All cooperating runners must use the same lock key and database. This example's key serializes Lesson 29 runners even across their private test schemas; it does not block non-cooperating migration tools by itself.

Read Committed gives subsequent statements a fresh view after the runner waits for the lock. A transaction-level lock is released on transaction completion; a session-level lock is a different lifetime and is not used here. Keep every migration operation on `tx`, not `db`, and close query rows before executing more statements on the transaction's connection.

All pending migrations and their ledger records share **one batch transaction**. If statement 2 fails, statement 1 from this batch also rolls back. If version 1 was committed by an earlier run, it remains committed when a later upgrade fails. A deferred constraint can fail at `Commit`, so executing all SQL is not sufficient proof of success.

Pass a bounded context through `BeginTx`, lock acquisition, and queries. A caller can stop waiting for another runner. As with other database writes, a lost commit response can leave the outcome uncertain; reconnect and inspect history before deciding the next step, rather than claiming every transport error proves rollback.

## Run on a disposable database

From the repository root, use Go, Docker, and `openssl`. Start only a temporary instance with a random password and a random loopback-only port:

```sh
lesson29_password=$(openssl rand -hex 24)
lesson29_container=$(docker run --rm -d \
  --tmpfs /var/lib/postgresql/data \
  -p 127.0.0.1::5432 \
  -e POSTGRES_USER=course -e POSTGRES_DB=course \
  -e POSTGRES_PASSWORD="$lesson29_password" postgres:17-alpine)
docker exec "$lesson29_container" pg_isready -U course -d course
```

If it is not ready, repeat the readiness command before continuing. The mutable image tag is for local practice, not a production image pin. Never use a production/shared database, print the password-bearing URL, or commit credentials.

```sh
lesson29_port=$(docker port "$lesson29_container" 5432/tcp | awk -F: '{print $NF}')
export DATABASE_URL="postgres://course:$lesson29_password@127.0.0.1:$lesson29_port/course?sslmode=disable"
export POSTGRES_TEST_URL="$DATABASE_URL"
go run ./lessons/29-migration-integrity
go run ./lessons/29-migration-integrity
go test -count=1 -v ./lessons/29-migration-integrity
```

Both CLI runs print the same result; the second validates existing history without replaying applied DDL:

```json
{"schema_version":2}
```

This is a local migration CLI, not an HTTP server or a production deployment system. It has no down migrations, automatic checksum repair, backup/restore workflow, tenant routing, or application rollout orchestration.

## Limits before production

The runner is deliberately for a small set of transactional SQL steps. Migration files must not contain their own `BEGIN`, `COMMIT`, or `ROLLBACK`; the runner owns the transaction. This is a source-review rule, not a SQL-parser guarantee. `CREATE INDEX CONCURRENTLY` cannot run inside this transaction. Do not remove the transaction just to make such a file work: nontransactional migrations require a separate recovery design.

DDL can acquire strong locks and disrupt a running application. A successful local migration is not evidence of zero downtime, bounded production lock duration, or compatibility with old application instances. Large backfills and live index creation are separate operational work. In a real chat backend, coordinate schema changes with the deployed reader/writer versions; a checksum does not solve that compatibility contract.

## What the tests prove

Unit tests validate ordered plans, exact-byte SHA-256, strict prefix matching, unknown/newer versions, missing configuration, and cancellation before database access.

PostgreSQL tests create a random private schema per subtest and remove only that schema. They cover preservation of an existing message during upgrade, unchanged ledger timestamps on rerun, changed applied SQL, missing/newer history, statement and deferred-commit failure rollback, preservation of an earlier committed prefix, two cooperating runners, and a bounded deadline while another transaction holds the lock. The lock test uses a real deadline, not a guessed scheduling sleep.

Without `POSTGRES_TEST_URL` SQL tests are **skipped**. That is not evidence of migrations or concurrency working in PostgreSQL. Tests do not simulate network loss during commit or a rolling production deployment.

## Assignment: add one compatible soft-delete migration — not implemented

Add migration 3 for this lesson only, leaving both published SQL files unchanged.

1. Add nullable `deleted_at` timestamps to messages. Existing rows must retain their IDs, conversation, text, creation time, and version; their deletion timestamp starts as NULL.
2. Add a partial index for active-message history, scoped by `conversation_id` and ordered by `created_at DESC, id DESC`. It should exclude soft-deleted rows. This tiny local exercise uses an ordinary transactional index; do not claim it is an online production migration.
3. Extend the explicit embedded manifest with version 3. Do not modify the checksum algorithm, existing ledger entries, or mismatch policy. Do not implement soft-delete HTTP handlers or the earlier exercises.
4. Add tests using the **complete** `loadMigrations()` plan: fresh install, upgrade from versions 1 and 2 with existing rows, NULL defaults/data preservation, index columns/predicate, unchanged rerun, and concurrent runners. The existing helper intentionally pins the original two-step baseline; keep those tests and add your new cases separately.
5. Simulate a failed candidate migration in a test-only plan and prove that neither a partial schema change nor its ledger entry survives. Also show that changing an already-applied SQL file remains a rejection, not an automatic repair. Do not edit the published SQL fixtures to simulate failure.

Completion: old and new tests pass against a real disposable PostgreSQL database; version 3 upgrades existing data without rewriting history; explain what a deployment and rollback plan would still need. The new migration and its tests remain yours.

## Checks and cleanup

While the temporary database is running:

```sh
gofmt -l lessons/29-migration-integrity/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Stop only the instance created above; its temporary learning data is discarded:

```sh
docker stop "$lesson29_container"
unset DATABASE_URL POSTGRES_TEST_URL lesson29_password lesson29_port lesson29_container
```

Typical mistakes: changing old SQL to silence a mismatch; hashing normalized SQL while storing a different file; validating only the latest version; creating the ledger before the lock; mixing `db` and `tx`; forgetting to close rows or check `Commit`; assuming checksums prove live-schema integrity; using an ordinary index as proof of zero-downtime migration.

Sources: [Go database/sql transactions](https://pkg.go.dev/database/sql#DB.BeginTx), [PostgreSQL advisory locks](https://www.postgresql.org/docs/17/explicit-locking.html#ADVISORY-LOCKS), [CREATE INDEX transaction restrictions](https://www.postgresql.org/docs/17/sql-createindex.html).
