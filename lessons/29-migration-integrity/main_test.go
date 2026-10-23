package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestMigrationPlan(t *testing.T) {
	plan, err := loadMigrations()
	if err != nil || len(plan) < 2 {
		t.Fatalf("loadMigrations() = %d migrations, %v", len(plan), err)
	}
	for _, invalid := range [][]migration{
		nil,
		{{version: 0, sql: "SELECT 1"}},
		{{version: 2, sql: "SELECT 1"}},
		{{version: 1, sql: " "}},
		{{version: 1, sql: "SELECT 1"}, {version: 3, sql: "SELECT 2"}},
		{{version: 1, sql: "SELECT 1"}, {version: 1, sql: "SELECT 2"}},
	} {
		if err := validatePlan(invalid); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("validatePlan(%#v) = %v", invalid, err)
		}
	}
}

func TestHistoryMustBeAnExactPrefix(t *testing.T) {
	plan := lessonPlan(t)
	one := appliedMigration{version: 1, checksum: checksum(plan[0].sql)}
	two := appliedMigration{version: 2, checksum: checksum(plan[1].sql)}
	for _, history := range [][]appliedMigration{nil, {one}, {one, two}} {
		start, err := pendingStart(plan, history)
		if err != nil || start != len(history) {
			t.Fatalf("pendingStart(%#v) = %d, %v", history, start, err)
		}
	}
	for _, history := range [][]appliedMigration{
		{two},
		{one, one},
		{{version: 1, checksum: strings.Repeat("0", 64)}},
		{one, two, {version: 3, checksum: strings.Repeat("0", 64)}},
	} {
		if start, err := pendingStart(plan, history); start != 0 || !errors.Is(err, ErrHistoryMismatch) {
			t.Fatalf("invalid history = %d, %v", start, err)
		}
	}
	if checksum("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" ||
		checksum(plan[0].sql) == checksum(plan[0].sql+"\n") {
		t.Fatal("checksum must be SHA-256 of the exact bytes, including whitespace")
	}
}

func TestPreconditionsDoNotNeedADatabase(t *testing.T) {
	plan := lessonPlan(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := applyMigrations(ctx, nil, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled apply = %v", err)
	}
	if err := applyMigrations(context.Background(), nil, nil); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("invalid apply = %v", err)
	}
	if err := applyMigrations(context.Background(), nil, plan); !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("missing database = %v", err)
	}
	if err := run(context.Background(), io.Discard, " "); !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("missing URL = %v", err)
	}
}

func TestPostgresMigrationIntegrity(t *testing.T) {
	t.Run("upgrade preserves data and rerun skips applied DDL", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		plan := lessonPlan(t)
		mustApply(t, ctx, db, plan[:1])
		if _, err := db.ExecContext(ctx, `INSERT INTO lesson29_messages (conversation_id, body) VALUES ('chat-1', 'keep me')`); err != nil {
			t.Fatal(err)
		}
		mustApply(t, ctx, db, plan)
		var body string
		var version int64
		if err := db.QueryRowContext(ctx, `SELECT body, version FROM lesson29_messages`).Scan(&body, &version); err != nil {
			t.Fatal(err)
		}
		if body != "keep me" || version != 1 {
			t.Fatalf("upgraded row = %q, version %d", body, version)
		}
		var appliedAt time.Time
		if err := db.QueryRowContext(ctx, `SELECT applied_at FROM lesson29_schema_migrations WHERE version=2`).Scan(&appliedAt); err != nil {
			t.Fatal(err)
		}
		mustApply(t, ctx, db, plan)
		assertLedgerCount(t, ctx, db, 2)
		var unchanged time.Time
		if err := db.QueryRowContext(ctx, `SELECT applied_at FROM lesson29_schema_migrations WHERE version=2`).Scan(&unchanged); err != nil || !unchanged.Equal(appliedAt) {
			t.Fatalf("rerun changed applied_at: %v, %v", unchanged, err)
		}
	})

	t.Run("changed applied SQL blocks pending migration", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		plan := lessonPlan(t)
		mustApply(t, ctx, db, plan[:1])
		plan[0].sql += "\n-- changed after application"
		if err := applyMigrations(ctx, db, plan); !errors.Is(err, ErrHistoryMismatch) {
			t.Fatalf("changed migration = %v", err)
		}
		assertLedgerCount(t, ctx, db, 1)
		assertVersionColumn(t, ctx, db, false)
	})

	t.Run("unknown newer version and missing history fail closed", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		plan := lessonPlan(t)
		mustApply(t, ctx, db, plan)
		if err := applyMigrations(ctx, db, plan[:1]); !errors.Is(err, ErrHistoryMismatch) {
			t.Fatalf("older binary = %v", err)
		}
		assertLedgerCount(t, ctx, db, 2)
		if _, err := db.ExecContext(ctx, `DELETE FROM lesson29_schema_migrations WHERE version=1`); err != nil {
			t.Fatal(err)
		}
		if err := applyMigrations(ctx, db, plan); !errors.Is(err, ErrHistoryMismatch) {
			t.Fatalf("history gap = %v", err)
		}
		assertLedgerCount(t, ctx, db, 1)
		assertVersionColumn(t, ctx, db, true)
	})

	for _, failure := range []struct {
		name string
		sql  string
	}{
		{"statement failure", "SELECT 1 / 0;"},
		{"commit failure", `CREATE TABLE lesson29_guard (
			message_id bigint REFERENCES lesson29_messages(id) DEFERRABLE INITIALLY DEFERRED
		); INSERT INTO lesson29_guard VALUES (999);`},
	} {
		t.Run(failure.name+" rolls back fresh batch", func(t *testing.T) {
			ctx, db := integrationDatabase(t)
			plan := lessonPlan(t)
			plan[1].sql += "\n" + failure.sql
			if err := applyMigrations(ctx, db, plan); err == nil || errors.Is(err, ErrHistoryMismatch) {
				t.Fatalf("broken migration = %v", err)
			}
			for _, table := range []string{"lesson29_messages", "lesson29_schema_migrations", "lesson29_guard"} {
				var name sql.NullString
				if err := db.QueryRowContext(ctx, `SELECT to_regclass($1)::text`, table).Scan(&name); err != nil || name.Valid {
					t.Fatalf("rolled-back table %s = %v, %v", table, name, err)
				}
			}
			mustApply(t, ctx, db, lessonPlan(t))
		})
	}

	t.Run("failed upgrade preserves committed prefix", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		plan := lessonPlan(t)
		mustApply(t, ctx, db, plan[:1])
		plan[1].sql += "\nSELECT 1 / 0;"
		if err := applyMigrations(ctx, db, plan); err == nil {
			t.Fatal("broken upgrade succeeded")
		}
		assertLedgerCount(t, ctx, db, 1)
		assertVersionColumn(t, ctx, db, false)
		mustApply(t, ctx, db, lessonPlan(t))
	})

	t.Run("two cooperating runners both succeed", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		plan := lessonPlan(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				results <- applyMigrations(ctx, db, plan)
			}()
		}
		close(start)
		for i := 0; i < 2; i++ {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		assertLedgerCount(t, ctx, db, 2)
		assertVersionColumn(t, ctx, db, true)
	})

	t.Run("deadline while lock is held leaves no DDL", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		holder, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
			t.Fatal(err)
		}
		limited, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		if err := applyMigrations(limited, db, lessonPlan(t)); err == nil || limited.Err() == nil {
			t.Fatalf("blocked migration = %v, context = %v", err, limited.Err())
		}
		var ledger sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT to_regclass('lesson29_schema_migrations')::text`).Scan(&ledger); err != nil || ledger.Valid {
			t.Fatalf("ledger after cancellation = %v, %v", ledger, err)
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		mustApply(t, ctx, db, lessonPlan(t))
	})
}

func lessonPlan(t *testing.T) []migration {
	t.Helper()
	plan, err := loadMigrations()
	if err != nil || len(plan) < 2 {
		t.Fatalf("load published baseline: %v", err)
	}
	// Keep baseline tests unchanged when the learner adds migration 3 and its tests.
	return plan[:2]
}

func mustApply(t *testing.T, ctx context.Context, db *sql.DB, plan []migration) {
	t.Helper()
	if err := applyMigrations(ctx, db, plan); err != nil {
		t.Fatal(err)
	}
}

func assertLedgerCount(t *testing.T, ctx context.Context, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM lesson29_schema_migrations`).Scan(&got); err != nil || got != want {
		t.Fatalf("ledger count = %d, want %d: %v", got, want, err)
	}
}

func assertVersionColumn(t *testing.T, ctx context.Context, db *sql.DB, want bool) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='lesson29_messages' AND column_name='version'
	)`).Scan(&exists); err != nil || exists != want {
		t.Fatalf("version column exists = %v, want %v: %v", exists, want, err)
	}
}

// Each test owns one random schema inside a disposable test database.
func integrationDatabase(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set POSTGRES_TEST_URL to a disposable PostgreSQL database for migration integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	admin := stdlib.OpenDB(*config)
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatal("test PostgreSQL is not ready")
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	schema := "lesson29_test_" + hex.EncodeToString(suffix[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	scoped := config.Copy()
	scoped.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*scoped)
	db.SetMaxOpenConns(5)
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("clean up owned schema: %v", err)
		}
		_ = admin.Close()
	})
	return ctx, db
}
