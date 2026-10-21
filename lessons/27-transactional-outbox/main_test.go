package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestNormalizeDraft(t *testing.T) {
	for _, body := range []string{"hello", strings.Repeat("я", 280), "'; DROP TABLE lesson27_messages; --"} {
		got, err := normalizeDraft(MessageDraft{ConversationID: " chat-1 ", Text: " " + body + " "})
		if err != nil || got.ConversationID != "chat-1" || got.Text != body {
			t.Fatalf("normalizeDraft() = %#v, %v", got, err)
		}
	}
	for _, draft := range []MessageDraft{
		{}, {ConversationID: "chat", Text: " "}, {ConversationID: "chat", Text: strings.Repeat("я", 281)},
		{ConversationID: "chat", Text: "\xff"}, {ConversationID: "chat", Text: "a\x00b"},
		{ConversationID: strings.Repeat("a", 81), Text: "hello"},
		{ConversationID: "chat/1", Text: "hello"}, {ConversationID: "chat 1", Text: "hello"},
	} {
		got, err := normalizeDraft(draft)
		if !errors.Is(err, ErrInvalidDraft) || got != (MessageDraft{}) {
			t.Fatalf("normalizeDraft(invalid) = %#v, %v", got, err)
		}
	}
}

func TestCreateRejectsBeforeDatabaseAccess(t *testing.T) {
	store := &outboxStore{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	draft := MessageDraft{ConversationID: "chat-1", Text: "hello"}
	if got, err := store.Create(ctx, draft); !errors.Is(err, context.Canceled) || got != (Created{}) {
		t.Fatalf("canceled Create() = %#v, %v", got, err)
	}
	if got, err := store.Create(context.Background(), MessageDraft{}); !errors.Is(err, ErrInvalidDraft) || got != (Created{}) {
		t.Fatalf("invalid Create() = %#v, %v", got, err)
	}
	if got, err := store.Create(context.Background(), draft); !errors.Is(err, ErrDatabaseRequired) || got != (Created{}) {
		t.Fatalf("missing database Create() = %#v, %v", got, err)
	}
	if db, err := openDatabase(context.Background(), " "); db != nil || !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("openDatabase(empty) = %v, %v", db, err)
	}
}

func TestPostgresOutboxIntegration(t *testing.T) {
	t.Run("commits matching message and event", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		if err := applySchema(ctx, db); err != nil {
			t.Fatalf("repeat schema bootstrap: %v", err)
		}
		body := "'; DROP TABLE lesson27_messages; --"
		got, err := (&outboxStore{db: db}).Create(ctx, MessageDraft{ConversationID: "chat-1", Text: body})
		if err != nil || got.Message.ID <= 0 || got.EventID <= 0 || got.Message.CreatedAt.IsZero() {
			t.Fatalf("Create() = %#v, %v", got, err)
		}
		var storedBody, conversationID, eventType string
		var messageID int64
		var payload []byte
		if err := db.QueryRowContext(ctx, `
			SELECT m.body, m.conversation_id, e.message_id, e.event_type, e.payload
			FROM lesson27_messages m JOIN lesson27_outbox e ON e.message_id=m.id
			WHERE e.id=$1`, got.EventID,
		).Scan(&storedBody, &conversationID, &messageID, &eventType, &payload); err != nil {
			t.Fatal(err)
		}
		var event messageCreated
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatal(err)
		}
		if storedBody != body || conversationID != "chat-1" || messageID != got.Message.ID ||
			eventType != "message.created" || event.MessageID != got.Message.ID || event.ConversationID != "chat-1" {
			t.Fatalf("stored message/event mismatch: %q %q %d %q %#v", storedBody, conversationID, messageID, eventType, event)
		}
		assertCounts(t, ctx, db, 1)
	})

	t.Run("outbox insert failure rolls message back", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		if _, err := db.ExecContext(ctx, `
			ALTER TABLE lesson27_outbox ADD CONSTRAINT reject_new_event CHECK (false) NOT VALID`); err != nil {
			t.Fatal(err)
		}
		got, err := (&outboxStore{db: db}).Create(ctx, MessageDraft{ConversationID: "chat-1", Text: "hello"})
		assertPostgresFailure(t, got, err, "23514", "insert outbox event")
		assertCounts(t, ctx, db, 0)
	})

	t.Run("deferred constraint fails at commit", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE lesson27_commit_targets (id bigint PRIMARY KEY);
			ALTER TABLE lesson27_outbox ADD CONSTRAINT missing_commit_target
			FOREIGN KEY (message_id) REFERENCES lesson27_commit_targets (id)
			DEFERRABLE INITIALLY DEFERRED`); err != nil {
			t.Fatal(err)
		}
		got, err := (&outboxStore{db: db}).Create(ctx, MessageDraft{ConversationID: "chat-1", Text: "hello"})
		assertPostgresFailure(t, got, err, "23503", "commit message and outbox")
		assertCounts(t, ctx, db, 0)
	})

	t.Run("cancellation and validation leave no rows", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		store := &outboxStore{db: db}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if got, err := store.Create(canceled, MessageDraft{ConversationID: "chat-1", Text: "hello"}); !errors.Is(err, context.Canceled) || got != (Created{}) {
			t.Fatalf("canceled Create() = %#v, %v", got, err)
		}
		if _, err := store.Create(ctx, MessageDraft{}); !errors.Is(err, ErrInvalidDraft) {
			t.Fatalf("invalid Create() error = %v", err)
		}
		assertCounts(t, ctx, db, 0)
	})

	t.Run("concurrent independent writes retain one event each", func(t *testing.T) {
		ctx, db := integrationDatabase(t)
		store := &outboxStore{db: db}
		const writes = 8
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan Created, writes)
		failures := make(chan error, writes)
		for i := 0; i < writes; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				result, err := store.Create(ctx, MessageDraft{ConversationID: "chat-1", Text: "hello"})
				if err != nil {
					failures <- err
					return
				}
				results <- result
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		ids := make(map[int64]bool)
		events := make(map[int64]bool)
		for result := range results {
			if ids[result.Message.ID] || events[result.EventID] {
				t.Errorf("reused identifiers: %#v", result)
			}
			ids[result.Message.ID], events[result.EventID] = true, true
		}
		if len(ids) != writes || len(events) != writes {
			t.Fatalf("successful messages=%d events=%d, want %d", len(ids), len(events), writes)
		}
		assertCounts(t, ctx, db, writes)
	})
}

func assertPostgresFailure(t *testing.T, got Created, err error, code, stage string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if got != (Created{}) || !errors.As(err, &postgresError) || postgresError.Code != code || !strings.Contains(err.Error(), stage) {
		t.Fatalf("Create() = %#v, %v, want zero result and PostgreSQL %s at %s", got, err, code, stage)
	}
}

func assertCounts(t *testing.T, ctx context.Context, db *sql.DB, want int) {
	t.Helper()
	var messages, events, pairs int
	if err := db.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM lesson27_messages),
		       (SELECT count(*) FROM lesson27_outbox),
		       (SELECT count(*) FROM lesson27_messages m JOIN lesson27_outbox e ON e.message_id=m.id)`,
	).Scan(&messages, &events, &pairs); err != nil {
		t.Fatal(err)
	}
	if messages != want || events != want || pairs != want {
		t.Fatalf("messages=%d events=%d pairs=%d, want %d each", messages, events, pairs, want)
	}
}

// Each subtest owns a random schema. No shared learner tables are truncated.
func integrationDatabase(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set POSTGRES_TEST_URL to a disposable PostgreSQL database for outbox integration tests")
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
	schema := pgx.Identifier{"lesson27_test_" + hex.EncodeToString(suffix[:])}.Sanitize()
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
	if err := applySchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}
