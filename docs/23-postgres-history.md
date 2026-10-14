# Lesson 23: Capstone IV — persist scoped history in PostgreSQL

Estimated time: 45–60 minutes.

Lesson 22 introduced a stable `(created_at, id)` cursor and checked membership before reading. This step moves the same page contract to PostgreSQL. The adapter checks active membership and reads the page inside one read-only `REPEATABLE READ` transaction, and every message query is still constrained to the requested conversation.

The example uses the repository's pinned pgx stdlib driver with `database/sql`. A transaction gives the membership check and page query the same per-request snapshot; it does not freeze history across separate page requests. See Go's [`database/sql` transaction options](https://pkg.go.dev/database/sql#TxOptions), PostgreSQL's [transaction isolation documentation](https://www.postgresql.org/docs/current/transaction-iso.html), and the [pgx stdlib guide](https://github.com/jackc/pgx/wiki/Getting-started-with-pgx-through-database-sql).

## Start a disposable local database

```sh
docker run --rm --name go-course-history -e POSTGRES_USER=go -e POSTGRES_PASSWORD=go -e POSTGRES_DB=go_course -p 127.0.0.1:5434:5432 postgres:16
```

Run the example from a second terminal. It applies this lesson's versioned migrations, then tries to read the default demo conversation. A fresh database has no membership yet:

```sh
DATABASE_URL='postgres://go:go@localhost:5434/go_course?sslmode=disable' go run ./lessons/23-postgres-history
```

```text
PostgreSQL schema ready; no active demo membership
```

For a different local database, set `DEMO_USER_ID` and `DEMO_CONVERSATION_ID` to the IDs whose history you want to read. The demo only reads; it does not seed or delete conversation data.

## How the adapter works

The migrations create a small membership table, a message table, and an index on `(conversation_id, created_at DESC, id DESC)`. Migration versions are recorded transactionally under an advisory lock. Applied migration files should be treated as immutable.

`ListPage` validates the caller's IDs and page size, opens a read-only repeatable-read transaction, checks for an active membership, and only then queries messages. The page query always binds `conversation_id` as `$1`; when a cursor is present, PostgreSQL applies the exclusive tuple predicate `(created_at, id) < ($2, $3)`. User input is passed as query arguments, never formatted into SQL. PostgreSQL compares row-constructor fields from left to right; see its [row comparison rules](https://www.postgresql.org/docs/current/functions-comparisons.html#ROW-WISE-COMPARISON).

The query fetches at most `limit + 1` rows. The extra row sets `has_more`; it is not returned. The next cursor is the final returned row, so the next page starts strictly before that tuple. Rows are closed and checked before committing the read transaction. Database details are wrapped for logs/callers; a handler should map `ErrNotMember` to `403` and keep unexpected database text out of public responses.

The transaction provides one snapshot for membership and page data in a single request. It does not make page 1 and page 2 a single snapshot, and it is not a substitute for a durable history revision protocol. The real chat backend's `messages.load` contract has additional rules beyond this lesson's REST/store sample.

## Tests

Unit tests verify page-size boundaries, bound SQL arguments (including a hostile conversation ID), tuple-cursor SQL, and embedded migration order. The PostgreSQL integration test creates uniquely scoped fixture rows, verifies a two-page traversal including a timestamp tie, checks non-member denial, and removes only its own rows. It skips when `POSTGRES_TEST_URL` is unset.

Use a disposable test database separate from any database containing data you need to preserve. The test applies this lesson's schema migrations there; cleanup removes fixture rows but intentionally leaves the migration schema.

```sh
go test ./lessons/23-postgres-history
```

```sh
POSTGRES_TEST_URL='postgres://go:go@localhost:5434/go_course_test?sslmode=disable' go test ./lessons/23-postgres-history -run TestPostgresHistoryIntegration -count=1
```

Run all repository checks:

```sh
gofmt -w ./lessons/23-postgres-history
go test ./...
go test -race ./...
go vet ./...
```

## Exercise — add creation-time filters to the PostgreSQL query

Extend `ListPage` with optional inclusive `created_from` and exclusive `created_to` bounds:

1. Validate the range before opening a transaction; reject malformed or non-increasing bounds with a stable error.
2. Bind the timestamps as SQL arguments. Keep the conversation predicate, membership check, tuple cursor, and stable ordering intact.
3. Apply the same bounds on every cursor page. A caller following `Next` must carry the same filter values forward.
4. Add unit tests for query construction and invalid bounds, plus opt-in integration coverage for both boundaries and a two-page traversal. Keep integration fixtures uniquely scoped and clean up only their own rows.

Plan at least 40 minutes. Completion means the existing page traversal still passes, filtered pages do not cross conversation scope, and the integration test is explicitly reported as skipped if no test database is configured.

## Typical mistakes

- interpolating a conversation ID or cursor into SQL text;
- doing the membership check on one connection and reading messages outside the transaction;
- forgetting that `database/sql` transactions pin the operations to one connection;
- using timestamp alone as a cursor when timestamps can tie;
- dropping the conversation predicate from the cursor query;
- returning the extra `limit + 1` row to the client;
- committing while result rows are still open or ignoring `rows.Err()`;
- pointing opt-in integration tests at production or valuable local data;
- claiming the transaction creates a snapshot shared by multiple HTTP requests.

## Completion criteria

You can trace the membership check and page query through one context-aware transaction, explain the composite index and exclusive cursor predicate, and distinguish a per-request database snapshot from a cross-page history revision.
