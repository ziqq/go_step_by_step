// Package main demonstrates PostgreSQL migrations, parameterized queries, and transactions.
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	migrationLockID     int64 = 20260930
	maxDescriptionRunes       = 200
	maxRecentLimit            = 100
)

var (
	ErrInvalidExpense = errors.New("invalid expense")
	ErrInvalidLimit   = errors.New("invalid list limit")
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Expense struct {
	ID          int64
	Description string
	AmountCents int64
	CreatedAt   time.Time
}

type repository struct {
	db *sql.DB
}

type migrationFile struct {
	version int
	path    string
}

var migrations = []migrationFile{
	{version: 1, path: "migrations/001_expenses.sql"},
	{version: 2, path: "migrations/002_expense_events.sql"},
	{version: 3, path: "migrations/003_expenses_recent_index.sql"},
}

func validateExpense(description string, amountCents int64) error {
	description = strings.TrimSpace(description)
	if description == "" || utf8.RuneCountInString(description) > maxDescriptionRunes || amountCents <= 0 {
		return ErrInvalidExpense
	}
	return nil
}

func validateRecentLimit(limit int) error {
	if limit < 1 || limit > maxRecentLimit {
		return ErrInvalidLimit
	}
	return nil
}

func openDatabase(ctx context.Context, databaseURL string) (*sql.DB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL database: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return db, nil
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    integer PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	applied := make(map[int]struct{}, len(migrations))
	rows, err := tx.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate applied migrations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close migration rows: %w", err)
	}

	for _, migration := range migrations {
		if _, ok := applied[migration.version]; ok {
			continue
		}
		statement, err := fs.ReadFile(migrationFS, migration.path)
		if err != nil {
			return fmt.Errorf("read migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, string(statement)); err != nil {
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, migration.version); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func (r *repository) create(ctx context.Context, description string, amountCents int64) (Expense, error) {
	description = strings.TrimSpace(description)
	if err := validateExpense(description, amountCents); err != nil {
		return Expense{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Expense{}, fmt.Errorf("begin create expense: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var expense Expense
	err = tx.QueryRowContext(ctx, `
		INSERT INTO expenses (description, amount_cents)
		VALUES ($1, $2)
		RETURNING id, description, amount_cents, created_at`,
		description,
		amountCents,
	).Scan(&expense.ID, &expense.Description, &expense.AmountCents, &expense.CreatedAt)
	if err != nil {
		return Expense{}, fmt.Errorf("insert expense: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO expense_events (expense_id, event_type)
		VALUES ($1, 'expense.created')`, expense.ID); err != nil {
		return Expense{}, fmt.Errorf("record expense event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Expense{}, fmt.Errorf("commit expense: %w", err)
	}
	return expense, nil
}

func (r *repository) recent(ctx context.Context, limit int) ([]Expense, error) {
	if err := validateRecentLimit(limit); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, description, amount_cents, created_at
		FROM expenses
		ORDER BY created_at DESC, id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent expenses: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]Expense, 0)
	for rows.Next() {
		var expense Expense
		if err := rows.Scan(&expense.ID, &expense.Description, &expense.AmountCents, &expense.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan expense: %w", err)
		}
		items = append(items, expense)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expenses: %w", err)
	}
	return items, nil
}

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := openDatabase(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()

	if err := applyMigrations(ctx, db); err != nil {
		return err
	}

	expenses, err := (&repository{db: db}).recent(ctx, 10)
	if err != nil {
		return err
	}
	if len(expenses) == 0 {
		fmt.Println("No expenses yet")
		return nil
	}
	for _, expense := range expenses {
		fmt.Printf("%d %s %d cents\n", expense.ID, expense.Description, expense.AmountCents)
	}
	return nil
}
