# Lesson 13: PostgreSQL, SQL, migrations, and transactions

## Goal

Use PostgreSQL from Go through `database/sql`, apply versioned schema migrations, write parameterized queries, scan rows, and keep related writes in one transaction. The example records an expense and its audit event atomically.

The target chat backend uses the same broad pattern: `database/sql` with pgx's stdlib driver, context-aware queries, PostgreSQL placeholders, migrations, and transactions. The pgx release is pinned to v5.7.4 because this course module targets Go 1.22; [its module declares Go 1.21](https://raw.githubusercontent.com/jackc/pgx/v5.7.4/go.mod). The pgx stdlib adapter registers the PostgreSQL driver for `database/sql`. See the [official pgx database/sql guide](https://github.com/jackc/pgx/wiki/Getting-started-with-pgx-through-database-sql).

## Start a local PostgreSQL database

In one terminal, start a disposable local database:

```sh
docker run --rm --name go-course-postgres -e POSTGRES_USER=go -e POSTGRES_PASSWORD=go -e POSTGRES_DB=go_course -p 127.0.0.1:5432:5432 postgres:16
```

In a second terminal, set the connection URL and run the example:

```sh
DATABASE_URL='postgres://go:go@localhost:5432/go_course?sslmode=disable' go run ./lessons/13-postgres
```

On a new database, the program applies migrations and prints:

```text
No expenses yet
```

The example database is local learning data. `go run` creates the migration ledger and the lesson's tables and index in that database.

## Open and verify the database

Import the driver's registration package for its side effect, then open the configured driver name. `sql.Open` validates the driver name and prepares a pool; it does not prove the server is reachable. Call `PingContext` with a deadline during startup.

```go
db, err := sql.Open("pgx", databaseURL)
if err != nil {
	return err
}
if err := db.PingContext(ctx); err != nil {
	_ = db.Close()
	return err
}
```

`*sql.DB` is a concurrency-safe connection pool, not one connection. Set sensible pool limits and close it when the process exits. Pass the request or operation context to every query so cancellation and deadlines reach the database.

## Migrations

The example embeds three small SQL files. It creates `schema_migrations`, takes a PostgreSQL transaction-level advisory lock, checks which versions are applied, executes each pending migration, and records the version in the same transaction. If a migration fails, the transaction rolls back both the schema change and its ledger entry.

The files create:

- `expenses`, including database constraints for non-empty descriptions and positive amounts;
- `expense_events`, linked with a foreign key to its expense;
- an index matching the recent-expense ordering.

Treat applied migrations as immutable. A production migration system should also detect checksum drift and handle concurrent startup carefully; the lesson keeps the ledger small enough to read end to end.

## Parameterized queries and scanning

Use placeholders for values. PostgreSQL uses `$1`, `$2`, and so on. Never insert user input into SQL text with string formatting.

```go
err := tx.QueryRowContext(ctx, `
	INSERT INTO expenses (description, amount_cents)
	VALUES ($1, $2)
	RETURNING id, description, amount_cents, created_at`,
	description,
	amountCents,
).Scan(&expense.ID, &expense.Description, &expense.AmountCents, &expense.CreatedAt)
```

Use `QueryRowContext` when exactly one row is expected and `QueryContext` for multiple rows. For a row iterator, close it and check `rows.Err()` after the loop; errors can arrive during iteration.

## Transactions and database constraints

Creating an expense and its event are one operation. The example begins a transaction, inserts the expense, inserts the event, and commits only when both statements succeed. A deferred rollback is harmless after a successful commit and cleans up every early return.

Validation in Go gives a useful error before the query. Database constraints remain necessary because other code paths or future processes can write to the same tables. Both layers enforce the same essential rules.

## Indexes and query shape

The `recent` query orders by `created_at DESC, id DESC` and limits the result count. The index has the same column order, with `id` as a stable tie-breaker when timestamps match. An index speeds up matching reads but costs disk and makes writes more expensive, so create indexes for real query patterns.

## Tests

Most tests need no database: the package tests validation and confirms that embedded migration files exist in version order. The repository integration test runs only when `POSTGRES_TEST_URL` points to a dedicated disposable PostgreSQL database. It applies migrations, creates an expense and event, reads the expense, and removes its test row.

```sh
go test ./lessons/13-postgres
```

To opt into the integration test, keep the local database running and use:

```sh
POSTGRES_TEST_URL='postgres://go:go@localhost:5432/go_course_test?sslmode=disable' go test ./lessons/13-postgres -run TestPostgresRepositoryIntegration -count=1
```

Use an isolated test database: the test applies schema migrations there. Never point this variable at production or at data you need to preserve.

Run the repository checks:

```sh
go test ./...
go test -race ./...
go vet ./...
```

## Typical mistakes

- treating `sql.Open` as proof of a successful connection;
- forgetting to close `*sql.DB` or query rows;
- building SQL with `fmt.Sprintf` and user-controlled values;
- ignoring `rows.Err()` after scanning;
- committing one half of a multi-step operation;
- relying only on Go validation and omitting database constraints;
- editing an already-applied migration instead of adding a new version;
- adding an index without checking which query it supports;
- running migrations or integration tests against a database that contains important data.

## Practice: fetch one expense by ID

Implement `getByID(ctx context.Context, id int64) (Expense, error)` on the repository in `lessons/13-postgres`.

Requirements:

1. Reject IDs less than or equal to zero with a stable validation error before querying.
2. Use `QueryRowContext` and a `$1` parameter; do not concatenate the ID into SQL.
3. Return a distinct `ErrExpenseNotFound` when `errors.Is(err, sql.ErrNoRows)` is true.
4. Wrap other database errors with context while preserving them for `errors.Is` or `errors.As`.
5. Add unit coverage for invalid IDs and integration coverage for an existing and a missing ID. The integration test must remain opt-in through `POSTGRES_TEST_URL`.
6. Leave the existing migration files unchanged; do not add a schema change for this query.

## Completion criteria

You can explain why `PingContext` follows `sql.Open`, how the migration ledger and advisory lock work, why the expense and event share a transaction, and how the index matches the list query. The opt-in PostgreSQL test verifies the create-and-read path on a disposable database.
