package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func sampleDraft() draft {
	return draft{TenantID: 1, ConversationID: 9, ActorID: 7, Body: "hello"}
}

func TestSendRejectsBeforeDatabase(t *testing.T) {
	for _, change := range []func(*draft){
		func(d *draft) { d.TenantID = 0 },
		func(d *draft) { d.ConversationID = -1 },
		func(d *draft) { d.ActorID = 0 },
		func(d *draft) { d.Body = "" },
		func(d *draft) { d.Body = " \t" },
		func(d *draft) { d.Body = strings.Repeat("🙂", 281) },
		func(d *draft) { d.Body = "a\x00b" },
		func(d *draft) { d.Body = "\xff" },
	} {
		d := sampleDraft()
		change(&d)
		if id, err := (store{}).Send(context.Background(), d); id != 0 || !errors.Is(err, errInvalid) {
			t.Fatalf("invalid draft: ID=%d error=%v", id, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if id, err := (store{}).Send(ctx, draft{}); id != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation precedence: ID=%d error=%v", id, err)
	}
	if id, err := (store{}).Send(context.Background(), sampleDraft()); id != 0 || !errors.Is(err, errDatabase) {
		t.Fatalf("missing database: ID=%d error=%v", id, err)
	}
}

func TestPostgresMembership(t *testing.T) {
	t.Run("active member preserves exact input", func(t *testing.T) {
		ctx, db := testDatabase(t)
		seedMember(t, ctx, db)
		d := sampleDraft()
		d.Body = " hello ' world "
		id, err := (store{db}).Send(ctx, d)
		if err != nil || id <= 0 {
			t.Fatalf("send: ID=%d error=%v", id, err)
		}
		var stored draft
		if err := db.QueryRowContext(ctx, `SELECT tenant_id, conversation_id, actor_id, body
   FROM lesson53_messages WHERE id=$1`, id).Scan(&stored.TenantID, &stored.ConversationID, &stored.ActorID, &stored.Body); err != nil || stored != d {
			t.Fatalf("stored=%+v error=%v", stored, err)
		}
		d.Body = strings.Repeat("🙂", 280)
		if _, err := (store{db}).Send(ctx, d); err != nil {
			t.Fatal(err)
		}
		assertRows(t, ctx, db, 2)
	})
	t.Run("missing inactive and other scopes are denied", func(t *testing.T) {
		ctx, db := testDatabase(t)
		seedMember(t, ctx, db)
		for _, d := range []draft{
			{TenantID: 2, ConversationID: 9, ActorID: 7, Body: "hello"},
			{TenantID: 1, ConversationID: 10, ActorID: 7, Body: "hello"},
			{TenantID: 1, ConversationID: 9, ActorID: 8, Body: "hello"},
		} {
			assertDenied(t, ctx, db, d)
		}
		mustExec(t, ctx, db, "UPDATE lesson53_members SET active=false")
		assertDenied(t, ctx, db, sampleDraft())
		mustExec(t, ctx, db, "DELETE FROM lesson53_members")
		assertDenied(t, ctx, db, sampleDraft())
		assertRows(t, ctx, db, 0)
	})
	t.Run("revocation wins before the locking read", func(t *testing.T) {
		ctx, db := testDatabase(t)
		seedMember(t, ctx, db)
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, "UPDATE lesson53_members SET active=false"); err != nil {
			t.Fatal(err)
		}
		sent := launch(t, cancel, func() outcome {
			id, err := (store{db}).Send(ctx, sampleDraft())
			return outcome{id, err}
		})
		waitForLock(t, ctx, db, "%SELECT actor_id FROM lesson53_members%")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		o := <-sent
		if o.id != 0 || !errors.Is(o.err, errForbidden) {
			t.Fatalf("send after revocation: %+v", o)
		}
		assertRows(t, ctx, db, 0)
	})
	t.Run("send holds membership until its commit", func(t *testing.T) {
		ctx, db := testDatabase(t)
		seedMember(t, ctx, db)
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		gate, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer gate.Rollback()
		// This gate pauses Send at INSERT, after it has acquired membership.
		if _, err := gate.ExecContext(ctx, "LOCK TABLE lesson53_messages IN SHARE MODE"); err != nil {
			t.Fatal(err)
		}
		sent := launch(t, cancel, func() outcome {
			id, err := (store{db}).Send(ctx, sampleDraft())
			return outcome{id, err}
		})
		waitForLock(t, ctx, db, "%INSERT INTO lesson53_messages%")
		revoked := launch(t, cancel, func() outcome {
			_, err := db.ExecContext(ctx, "UPDATE lesson53_members SET active=false")
			return outcome{err: err}
		})
		waitForLock(t, ctx, db, "%UPDATE lesson53_members SET active=false%")
		assertRows(t, ctx, db, 0)
		if err := gate.Commit(); err != nil {
			t.Fatal(err)
		}
		sendResult, revokeResult := <-sent, <-revoked
		if sendResult.err != nil || sendResult.id <= 0 || revokeResult.err != nil {
			t.Fatalf("send=%+v revoke=%+v", sendResult, revokeResult)
		}
		assertDenied(t, ctx, db, sampleDraft())
		assertRows(t, ctx, db, 1)
	})
	t.Run("cancellation interrupts a membership lock wait", func(t *testing.T) {
		ctx, db := testDatabase(t)
		seedMember(t, ctx, db)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, "UPDATE lesson53_members SET active=false"); err != nil {
			t.Fatal(err)
		}
		sendCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		sent := launch(t, cancel, func() outcome {
			id, err := (store{db}).Send(sendCtx, sampleDraft())
			return outcome{id, err}
		})
		waitCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		waitForLock(t, waitCtx, db, "%SELECT actor_id FROM lesson53_members%")
		cancel()
		o := <-sent
		if o.id != 0 || !errors.Is(o.err, context.Canceled) {
			t.Fatalf("canceled send: %+v", o)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		assertRows(t, ctx, db, 0)
		if _, err := (store{db}).Send(ctx, sampleDraft()); err != nil {
			t.Fatalf("send after rollback: %v", err)
		}
	})
	for _, failure := range []struct{ name, setup, code string }{
		{"insert failure", `ALTER TABLE lesson53_messages ADD CHECK (body <> 'hello')`, "23514"},
		{"commit failure", `CREATE TABLE lesson53_commit_targets (id bigint PRIMARY KEY);
   ALTER TABLE lesson53_messages ADD FOREIGN KEY (actor_id) REFERENCES lesson53_commit_targets(id)
   DEFERRABLE INITIALLY DEFERRED`, "23503"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			ctx, db := testDatabase(t)
			seedMember(t, ctx, db)
			mustExec(t, ctx, db, failure.setup)
			id, err := (store{db}).Send(ctx, sampleDraft())
			var pgErr *pgconn.PgError
			if id != 0 || !errors.As(err, &pgErr) || pgErr.Code != failure.code {
				t.Fatalf("ID=%d error=%v, want SQLSTATE %s", id, err, failure.code)
			}
			assertRows(t, ctx, db, 0)
			// A rollback also releases the membership lock for later revocation.
			unlocked, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			mustExec(t, unlocked, db, "UPDATE lesson53_members SET active=false")
		})
	}
}

type outcome struct {
	id  int64
	err error
}

func launch(t *testing.T, cancel context.CancelFunc, fn func() outcome) <-chan outcome {
	t.Helper()
	result := make(chan outcome, 1)
	done := make(chan struct{})
	go func() { defer close(done); result <- fn() }()
	t.Cleanup(func() { cancel(); <-done })
	return result
}

func waitForLock(t *testing.T, ctx context.Context, db *sql.DB, pattern string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (
   SELECT 1 FROM pg_stat_activity
   WHERE application_name=current_setting('application_name')
   AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		// Pace server observations, not the order of competing transactions.
		select {
		case <-ctx.Done():
			t.Fatal("expected server lock wait: " + pattern)
		case <-ticker.C:
		}
	}
}

func seedMember(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	mustExec(t, ctx, db, `INSERT INTO lesson53_members VALUES (1, 9, 7, true)`)
}
func mustExec(t *testing.T, ctx context.Context, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query); err != nil {
		t.Fatal(err)
	}
}
func assertDenied(t *testing.T, ctx context.Context, db *sql.DB, d draft) {
	t.Helper()
	if id, err := (store{db}).Send(ctx, d); id != 0 || !errors.Is(err, errForbidden) {
		t.Fatalf("denied: ID=%d error=%v", id, err)
	}
}
func assertRows(t *testing.T, ctx context.Context, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM lesson53_messages").Scan(&got); err != nil || got != want {
		t.Fatalf("rows=%d want=%d error=%v", got, want, err)
	}
}

func testDatabase(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_URL")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_URL to a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	admin := stdlib.OpenDB(*config)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	name := "lesson53_test_" + hex.EncodeToString(suffix[:])
	schema := pgx.Identifier{name}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	scoped := config.Copy()
	scoped.RuntimeParams["search_path"] = name
	scoped.RuntimeParams["application_name"] = name
	db := stdlib.OpenDB(*scoped)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		_ = db.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean up owned schema: %v", err)
		}
		_ = admin.Close()
	})
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}
