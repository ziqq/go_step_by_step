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

func TestNormalizeEdit(t *testing.T) {
	valid := EditDraft{ConversationID: " chat-1 ", MessageID: 1, ExpectedVersion: 1, Text: " hello "}
	got, err := normalizeEdit(valid)
	if err != nil || got.ConversationID != "chat-1" || got.Text != "hello" {
		t.Fatalf("normalizeEdit() = %#v, %v", got, err)
	}
	unicode := valid
	unicode.Text = strings.Repeat("я", 280)
	if _, err := normalizeEdit(unicode); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*EditDraft){
		func(d *EditDraft) { d.ConversationID = "" },
		func(d *EditDraft) { d.ConversationID = strings.Repeat("a", 81) },
		func(d *EditDraft) { d.ConversationID = "chat/1" },
		func(d *EditDraft) { d.MessageID = 0 },
		func(d *EditDraft) { d.MessageID = -1 },
		func(d *EditDraft) { d.ExpectedVersion = 0 },
		func(d *EditDraft) { d.ExpectedVersion = -1 },
		func(d *EditDraft) { d.ExpectedVersion = maxVersion },
		func(d *EditDraft) { d.Text = " " },
		func(d *EditDraft) { d.Text = strings.Repeat("я", 281) },
		func(d *EditDraft) { d.Text = "\xff" },
		func(d *EditDraft) { d.Text = "a\x00b" },
	} {
		invalid := valid
		mutate(&invalid)
		if got, err := normalizeEdit(invalid); got != (EditDraft{}) || !errors.Is(err, ErrInvalidEdit) {
			t.Fatalf("normalizeEdit(%#v) = %#v, %v", invalid, got, err)
		}
	}
}

func TestEditRejectsBeforeDatabaseAccess(t *testing.T) {
	store := &messageStore{}
	draft := EditDraft{ConversationID: "chat-1", MessageID: 1, ExpectedVersion: 1, Text: "hello"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := store.Edit(ctx, draft); got != (Message{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Edit() = %#v, %v", got, err)
	}
	if got, err := store.Edit(context.Background(), EditDraft{}); got != (Message{}) || !errors.Is(err, ErrInvalidEdit) {
		t.Fatalf("invalid Edit() = %#v, %v", got, err)
	}
	if got, err := store.Edit(context.Background(), draft); got != (Message{}) || !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("missing DB Edit() = %#v, %v", got, err)
	}
	if db, err := openDatabase(context.Background(), " "); db != nil || !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("openDatabase(empty) = %v, %v", db, err)
	}
}

func TestPostgresOptimisticEditIntegration(t *testing.T) {
	t.Run("matching version returns stored update", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		before := seedMessage(t, ctx, db)
		text := "'; DROP TABLE lesson28_messages; --"
		got, err := (&messageStore{db: db}).Edit(ctx, EditDraft{
			ConversationID: before.ConversationID, MessageID: before.ID, ExpectedVersion: 1, Text: text,
		})
		if err != nil || got.ID != before.ID || got.ConversationID != before.ConversationID || got.Text != text || got.Version != 2 {
			t.Fatalf("Edit() = %#v, %v", got, err)
		}
		assertStored(t, ctx, db, got)
	})

	t.Run("stale missing and wrong scope all reject without writing", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		before := seedMessage(t, ctx, db)
		store := &messageStore{db: db}
		current, err := store.Edit(ctx, EditDraft{
			ConversationID: before.ConversationID, MessageID: before.ID, ExpectedVersion: 1, Text: "first edit",
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, draft := range []EditDraft{
			{ConversationID: before.ConversationID, MessageID: before.ID, ExpectedVersion: 1, Text: "stale"},
			{ConversationID: "another-chat", MessageID: before.ID, ExpectedVersion: 2, Text: "wrong scope"},
			{ConversationID: before.ConversationID, MessageID: before.ID + 1000, ExpectedVersion: 2, Text: "missing"},
		} {
			got, err := store.Edit(ctx, draft)
			if got != (Message{}) || !errors.Is(err, ErrEditRejected) {
				t.Fatalf("unmatched Edit() = %#v, %v", got, err)
			}
			assertStored(t, ctx, db, current)
		}
	})

	t.Run("two concurrent edits have exactly one winner", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		before := seedMessage(t, ctx, db)
		store := &messageStore{db: db}
		type result struct {
			message Message
			err     error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		for _, text := range []string{"edit A", "edit B"} {
			go func(text string) {
				<-start
				got, err := store.Edit(ctx, EditDraft{
					ConversationID: before.ConversationID, MessageID: before.ID, ExpectedVersion: 1, Text: text,
				})
				results <- result{got, err}
			}(text)
		}
		close(start)
		var winner Message
		successes, rejections := 0, 0
		for i := 0; i < 2; i++ {
			result := <-results
			if result.err == nil {
				successes++
				winner = result.message
			} else if errors.Is(result.err, ErrEditRejected) && result.message == (Message{}) {
				rejections++
			} else {
				t.Fatalf("concurrent Edit() = %#v, %v", result.message, result.err)
			}
		}
		if successes != 1 || rejections != 1 || winner.Version != 2 ||
			(winner.Text != "edit A" && winner.Text != "edit B") {
			t.Fatalf("successes=%d rejections=%d winner=%#v", successes, rejections, winner)
		}
		assertStored(t, ctx, db, winner)
	})

	t.Run("same text is still a versioned write", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		before := seedMessage(t, ctx, db)
		got, err := (&messageStore{db: db}).Edit(ctx, EditDraft{
			ConversationID: before.ConversationID, MessageID: before.ID, ExpectedVersion: 1, Text: before.Text,
		})
		if err != nil || got.Version != 2 || got.Text != before.Text {
			t.Fatalf("same-text Edit() = %#v, %v", got, err)
		}
		assertStored(t, ctx, db, got)
	})

	t.Run("constraint failures are not precondition rejections", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		before := seedMessage(t, ctx, db)
		if _, err := db.ExecContext(ctx, `
			ALTER TABLE lesson28_messages ADD CONSTRAINT reject_blocked CHECK (body <> 'blocked')`); err != nil {
			t.Fatal(err)
		}
		got, err := (&messageStore{db: db}).Edit(ctx, EditDraft{
			ConversationID: before.ConversationID, MessageID: before.ID, ExpectedVersion: 1, Text: "blocked",
		})
		var postgresError *pgconn.PgError
		if got != (Message{}) || !errors.As(err, &postgresError) || postgresError.Code != "23514" || errors.Is(err, ErrEditRejected) {
			t.Fatalf("constraint Edit() = %#v, %v", got, err)
		}
		assertStored(t, ctx, db, before)
	})

	t.Run("validation and cancellation leave the row unchanged", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		before := seedMessage(t, ctx, db)
		store := &messageStore{db: db}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		draft := EditDraft{ConversationID: before.ConversationID, MessageID: before.ID, ExpectedVersion: 1, Text: "new"}
		if _, err := store.Edit(canceled, draft); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Edit() error = %v", err)
		}
		draft.ExpectedVersion = maxVersion
		if _, err := store.Edit(ctx, draft); !errors.Is(err, ErrInvalidEdit) {
			t.Fatalf("overflow-prone Edit() error = %v", err)
		}
		assertStored(t, ctx, db, before)
	})
}

func seedMessage(t *testing.T, ctx context.Context, db *sql.DB) Message {
	t.Helper()
	message := Message{ConversationID: "chat-1", Text: "original"}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO lesson28_messages (conversation_id, body) VALUES ($1, $2)
		RETURNING id, version`, message.ConversationID, message.Text,
	).Scan(&message.ID, &message.Version); err != nil {
		t.Fatal(err)
	}
	return message
}

func assertStored(t *testing.T, ctx context.Context, db *sql.DB, want Message) {
	t.Helper()
	var got Message
	if err := db.QueryRowContext(ctx, `
		SELECT id, conversation_id, body, version FROM lesson28_messages WHERE id=$1`, want.ID,
	).Scan(&got.ID, &got.ConversationID, &got.Text, &got.Version); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("stored message = %#v, want %#v", got, want)
	}
}

// The random schema belongs to this subtest; learner tables are never truncated.
func integrationDatabase(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set POSTGRES_TEST_URL to a disposable PostgreSQL database for optimistic-edit integration tests")
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
		t.Fatalf("connect to test PostgreSQL: %v", err)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	schema := pgx.Identifier{"lesson28_test_" + hex.EncodeToString(suffix[:])}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	scoped := config.Copy()
	scoped.RuntimeParams["search_path"] = strings.Trim(schema, `"`)
	db := stdlib.OpenDB(*scoped)
	db.SetMaxOpenConns(5)
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean up owned test schema: %v", err)
		}
		_ = admin.Close()
	})
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}
