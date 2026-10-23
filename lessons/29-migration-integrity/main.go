// Package main demonstrates ordered, checksum-checked PostgreSQL migrations.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// All cooperating lesson runners use the same database-wide transaction lock.
const migrationLockID int64 = 290001

var (
	ErrDatabaseRequired = errors.New("DATABASE_URL is required")
	ErrInvalidPlan      = errors.New("migration plan must start at 1 and be contiguous with nonempty SQL")
	ErrHistoryMismatch  = errors.New("applied migration history does not match this binary")
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int64
	sql     string
}

type appliedMigration struct {
	version  int64
	checksum string
}

func checksum(statement string) string {
	sum := sha256.Sum256([]byte(statement))
	return hex.EncodeToString(sum[:])
}

func loadMigrations() ([]migration, error) {
	// An explicit manifest keeps ordering visible; SQL comes only from this binary.
	files := []string{"migrations/001_messages.sql", "migrations/002_message_version.sql"}
	plan := make([]migration, 0, len(files))
	for i, name := range files {
		statement, err := migrationFS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read migration: %w", err)
		}
		plan = append(plan, migration{version: int64(i + 1), sql: string(statement)})
	}
	return plan, validatePlan(plan)
}

func validatePlan(plan []migration) error {
	if len(plan) == 0 {
		return ErrInvalidPlan
	}
	for i, step := range plan {
		if step.version != int64(i+1) || strings.TrimSpace(step.sql) == "" {
			return ErrInvalidPlan
		}
	}
	return nil
}

// pendingStart rejects changed, missing, or unknown applied versions before DDL.
// It assumes the plan was validated and history was read in ascending order.
func pendingStart(plan []migration, history []appliedMigration) (int, error) {
	if len(history) > len(plan) {
		return 0, ErrHistoryMismatch
	}
	for i, applied := range history {
		if applied.version != plan[i].version || applied.checksum != checksum(plan[i].sql) {
			return 0, fmt.Errorf("%w at version %d", ErrHistoryMismatch, applied.version)
		}
	}
	return len(history), nil
}

func applyMigrations(ctx context.Context, db *sql.DB, plan []migration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePlan(plan); err != nil {
		return err
	}
	if db == nil {
		return ErrDatabaseRequired
	}
	// Read Committed obtains a fresh snapshot after waiting for the advisory lock.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS lesson29_schema_migrations (
			version bigint PRIMARY KEY CHECK (version > 0),
			checksum text NOT NULL CHECK (length(checksum) = 64),
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT version, checksum FROM lesson29_schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	var history []appliedMigration
	for rows.Next() {
		var applied appliedMigration
		if err := rows.Scan(&applied.version, &applied.checksum); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan migration history: %w", err)
		}
		history = append(history, applied)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close migration history: %w", closeErr)
	}
	start, err := pendingStart(plan, history)
	if err != nil {
		return err
	}
	for _, step := range plan[start:] {
		if _, err := tx.ExecContext(ctx, step.sql); err != nil {
			return fmt.Errorf("apply migration %d: %w", step.version, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO lesson29_schema_migrations (version, checksum) VALUES ($1, $2)`,
			step.version, checksum(step.sql)); err != nil {
			return fmt.Errorf("record migration %d: %w", step.version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func run(ctx context.Context, output io.Writer, databaseURL string) error {
	if strings.TrimSpace(databaseURL) == "" {
		return ErrDatabaseRequired
	}
	plan, err := loadMigrations()
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open PostgreSQL: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	if err := applyMigrations(ctx, db, plan); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		Version int64 `json:"schema_version"`
	}{Version: plan[len(plan)-1].version})
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, os.Stdout, os.Getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
