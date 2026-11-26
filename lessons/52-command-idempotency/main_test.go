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
	"github.com/jackc/pgx/v5/stdlib"
)

func sampleCommand() command {
	return command{TenantID: 1, ActorID: 7, ConversationID: 9, Key: "send-1", Body: "hello"}
}

func TestSendRejectsBeforeDatabase(t *testing.T) {
	for _, change := range []func(*command){
		func(c *command) { c.TenantID = 0 },
		func(c *command) { c.ActorID = -1 },
		func(c *command) { c.ConversationID = 0 },
		func(c *command) { c.Key = "" },
		func(c *command) { c.Key = " \t" },
		func(c *command) { c.Key = strings.Repeat("x", 65) },
		func(c *command) { c.Key = "a\x00b" },
		func(c *command) { c.Key = "\xff" },
		func(c *command) { c.Body = " " },
		func(c *command) { c.Body = strings.Repeat("я", 281) },
		func(c *command) { c.Body = "a\x00b" },
		func(c *command) { c.Body = "\xff" },
	} {
		c := sampleCommand()
		change(&c)
		if got, err := (store{}).Send(context.Background(), c); got != (result{}) || !errors.Is(err, errInvalid) {
			t.Fatalf("invalid command: result=%+v error=%v", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := (store{}).Send(ctx, command{}); got != (result{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation precedence: %+v %v", got, err)
	}
	if got, err := (store{}).Send(context.Background(), sampleCommand()); got != (result{}) || !errors.Is(err, errDatabase) {
		t.Fatalf("missing database: %+v %v", got, err)
	}
}

func TestPostgresIdempotency(t *testing.T) {
	t.Run("lost reply and new store reuse persisted ID", func(t *testing.T) {
		ctx, db := testDatabase(t)
		c := sampleCommand()
		// Deliberately discard the first response as if transport delivery failed.
		if _, err := (store{db}).Send(ctx, c); err != nil {
			t.Fatal(err)
		}
		got, err := (store{db}).Send(ctx, c)
		var persistedID int64
		if err != nil || got.Created || got.MessageID <= 0 {
			t.Fatalf("replay: %+v %v", got, err)
		}
		if err := db.QueryRowContext(ctx, "SELECT id FROM lesson52_sends").Scan(&persistedID); err != nil || persistedID != got.MessageID {
			t.Fatalf("persisted ID=%d, reply=%+v, error=%v", persistedID, got, err)
		}
		assertRows(t, ctx, db, 1)
	})
	t.Run("payload mismatch returns no result", func(t *testing.T) {
		ctx, db := testDatabase(t)
		c := sampleCommand()
		first, err := (store{db}).Send(ctx, c)
		if err != nil || !first.Created {
			t.Fatalf("first: %+v %v", first, err)
		}
		for _, changed := range []command{
			{TenantID: 1, ActorID: 7, ConversationID: 10, Key: c.Key, Body: c.Body},
			{TenantID: 1, ActorID: 7, ConversationID: 9, Key: c.Key, Body: " hello"},
		} {
			if got, err := (store{db}).Send(ctx, changed); got != (result{}) || !errors.Is(err, errConflict) {
				t.Fatalf("mismatch: %+v %v", got, err)
			}
		}
		again, err := (store{db}).Send(ctx, c)
		if err != nil || again.Created || again.MessageID != first.MessageID {
			t.Fatalf("original changed: %+v %v", again, err)
		}
		assertRows(t, ctx, db, 1)
	})
	t.Run("tenant actor and key isolate commands", func(t *testing.T) {
		ctx, db := testDatabase(t)
		seen := make(map[int64]bool)
		for _, c := range []command{
			sampleCommand(),
			{TenantID: 2, ActorID: 7, ConversationID: 9, Key: "send-1", Body: "hello"},
			{TenantID: 1, ActorID: 8, ConversationID: 9, Key: "send-1", Body: "hello"},
			{TenantID: 1, ActorID: 7, ConversationID: 9, Key: "send-2", Body: "hello"},
		} {
			got, err := (store{db}).Send(ctx, c)
			if err != nil || !got.Created || seen[got.MessageID] {
				t.Fatalf("scope: %+v %v", got, err)
			}
			seen[got.MessageID] = true
		}
		assertRows(t, ctx, db, 4)
	})
	t.Run("concurrent duplicates create one row", func(t *testing.T) {
		ctx, db := testDatabase(t)
		const callers = 16
		type outcome struct {
			result result
			err    error
		}
		outcomes := make(chan outcome, callers)
		start := make(chan struct{})
		for i := 0; i < callers; i++ {
			go func() {
				<-start
				got, err := (store{db}).Send(ctx, sampleCommand())
				outcomes <- outcome{got, err}
			}()
		}
		close(start)
		var id int64
		created := 0
		// Receive every outcome before reporting failures, joining every caller.
		all := make([]outcome, callers)
		for i := range all {
			all[i] = <-outcomes
		}
		for _, o := range all {
			if o.err != nil || o.result.MessageID <= 0 {
				t.Fatalf("concurrent send: %+v %v", o.result, o.err)
			}
			if id == 0 {
				id = o.result.MessageID
			}
			if id != o.result.MessageID {
				t.Fatal("different IDs for one key")
			}
			if o.result.Created {
				created++
			}
		}
		if created != 1 {
			t.Fatalf("created=%d, want 1", created)
		}
		assertRows(t, ctx, db, 1)
	})
	t.Run("replay sees a winner committed during insertion", func(t *testing.T) {
		ctx, db := testDatabase(t)
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		c := sampleCommand()
		var winnerID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO lesson52_sends
			(tenant_id, actor_id, command_key, conversation_id, body)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			c.TenantID, c.ActorID, c.Key, c.ConversationID, c.Body).Scan(&winnerID); err != nil {
			t.Fatal(err)
		}
		type outcome struct {
			result result
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			got, err := (store{db}).Send(ctx, c)
			done <- outcome{got, err}
		}()
		joined := false
		defer func() {
			_ = tx.Rollback()
			cancel()
			if !joined {
				<-done
			}
		}()
		// Observe a real server lock wait before committing the competing row.
		// The timer only paces observation; it does not establish ordering.
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name=current_setting('application_name')
				AND wait_event_type='Lock'
				AND query LIKE '%INSERT INTO lesson52_sends%')`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("competing insertion did not enter a lock wait")
			case <-ticker.C:
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		o := <-done
		joined = true
		if o.err != nil || o.result != (result{MessageID: winnerID}) {
			t.Fatalf("concurrent replay: %+v %v", o.result, o.err)
		}
		assertRows(t, ctx, db, 1)
	})
	t.Run("exact text and SQL arguments", func(t *testing.T) {
		ctx, db := testDatabase(t)
		c := sampleCommand()
		c.Key = "'; DROP TABLE lesson52_sends; --"
		c.Body = " hello ' world "
		if _, err := (store{db}).Send(ctx, c); err != nil {
			t.Fatal(err)
		}
		var key, body string
		if err := db.QueryRowContext(ctx, "SELECT command_key, body FROM lesson52_sends").Scan(&key, &body); err != nil || key != c.Key || body != c.Body {
			t.Fatalf("text changed: %q %q %v", key, body, err)
		}
		c.Key, c.Body = strings.Repeat("x", 64), strings.Repeat("🙂", 280)
		if _, err := (store{db}).Send(ctx, c); err != nil {
			t.Fatal(err)
		}
		assertRows(t, ctx, db, 2)
	})
	t.Run("database error never looks like success", func(t *testing.T) {
		ctx, db := testDatabase(t)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if got, err := (store{db}).Send(ctx, sampleCommand()); got != (result{}) || err == nil {
			t.Fatalf("closed database: %+v %v", got, err)
		}
	})
}

func assertRows(t *testing.T, ctx context.Context, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM lesson52_sends").Scan(&got); err != nil || got != want {
		t.Fatalf("rows=%d, want %d, error=%v", got, want, err)
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
	name := "lesson52_test_" + hex.EncodeToString(suffix[:])
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
