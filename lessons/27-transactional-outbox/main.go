// Package main demonstrates an atomic message-and-outbox write in PostgreSQL.
package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	ErrDatabaseRequired = errors.New("DATABASE_URL is required")
	ErrInvalidDraft     = errors.New("conversation ID and valid message text are required")
)

//go:embed schema.sql
var schemaSQL string

type MessageDraft struct {
	ConversationID string
	Text           string
}

type Message struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Text           string    `json:"text"`
	CreatedAt      time.Time `json:"created_at"`
}

type Created struct {
	Message Message `json:"message"`
	EventID int64   `json:"event_id"`
}

type messageCreated struct {
	MessageID      int64  `json:"message_id"`
	ConversationID string `json:"conversation_id"`
}

type outboxStore struct {
	db *sql.DB
}

func normalizeDraft(draft MessageDraft) (MessageDraft, error) {
	draft.ConversationID = strings.TrimSpace(draft.ConversationID)
	draft.Text = strings.TrimSpace(draft.Text)
	if len(draft.ConversationID) < 1 || len(draft.ConversationID) > 80 {
		return MessageDraft{}, ErrInvalidDraft
	}
	for _, char := range draft.ConversationID {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_') {
			return MessageDraft{}, ErrInvalidDraft
		}
	}
	if !utf8.ValidString(draft.Text) || strings.ContainsRune(draft.Text, 0) ||
		utf8.RuneCountInString(draft.Text) < 1 || utf8.RuneCountInString(draft.Text) > 280 {
		return MessageDraft{}, ErrInvalidDraft
	}
	return draft, nil
}

func openDatabase(ctx context.Context, databaseURL string) (*sql.DB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, ErrDatabaseRequired
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return db, nil
}

// applySchema bootstraps this lesson only; it is not a versioned migration runner.
func applySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin outbox schema: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("create outbox schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outbox schema: %w", err)
	}
	return nil
}

// Create persists a message and its delivery intent, not delivery itself.
// Authorization and tenant isolation are outside this storage example.
func (s *outboxStore) Create(ctx context.Context, draft MessageDraft) (Created, error) {
	if err := ctx.Err(); err != nil {
		return Created{}, err
	}
	draft, err := normalizeDraft(draft)
	if err != nil {
		return Created{}, err
	}
	if s.db == nil {
		return Created{}, ErrDatabaseRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Created{}, fmt.Errorf("begin message and outbox: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result := Created{Message: Message{ConversationID: draft.ConversationID, Text: draft.Text}}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO lesson27_messages (conversation_id, body)
		VALUES ($1, $2) RETURNING id, created_at`,
		draft.ConversationID, draft.Text,
	).Scan(&result.Message.ID, &result.Message.CreatedAt); err != nil {
		return Created{}, fmt.Errorf("insert message: %w", err)
	}
	payload, err := json.Marshal(messageCreated{
		MessageID: result.Message.ID, ConversationID: result.Message.ConversationID,
	})
	if err != nil {
		return Created{}, fmt.Errorf("encode message event: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO lesson27_outbox (message_id, event_type, payload)
		VALUES ($1, 'message.created', $2::jsonb) RETURNING id`,
		result.Message.ID, string(payload),
	).Scan(&result.EventID); err != nil {
		return Created{}, fmt.Errorf("insert outbox event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Created{}, fmt.Errorf("commit message and outbox: %w", err)
	}
	return result, nil
}

func run(ctx context.Context, output io.Writer, databaseURL string) error {
	db, err := openDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := applySchema(ctx, db); err != nil {
		return err
	}
	result, err := (&outboxStore{db: db}).Create(ctx, MessageDraft{
		ConversationID: "lesson-chat", Text: "hello atomic outbox",
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, os.Stdout, os.Getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
