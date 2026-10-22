// Package main demonstrates version-checked message edits in PostgreSQL.
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

const maxVersion int64 = 1<<63 - 1

var (
	ErrDatabaseRequired = errors.New("DATABASE_URL is required")
	ErrInvalidEdit      = errors.New("valid message scope, text, and expected version are required")
	ErrEditRejected     = errors.New("message edit precondition did not match")
)

//go:embed schema.sql
var schemaSQL string

type Message struct {
	ID             int64  `json:"id"`
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
	Version        int64  `json:"version"`
}

type EditDraft struct {
	ConversationID  string
	MessageID       int64
	ExpectedVersion int64
	Text            string
}

type messageStore struct {
	db *sql.DB
}

func normalizeEdit(draft EditDraft) (EditDraft, error) {
	draft.ConversationID = strings.TrimSpace(draft.ConversationID)
	draft.Text = strings.TrimSpace(draft.Text)
	if len(draft.ConversationID) < 1 || len(draft.ConversationID) > 80 ||
		draft.MessageID <= 0 || draft.ExpectedVersion <= 0 || draft.ExpectedVersion >= maxVersion {
		return EditDraft{}, ErrInvalidEdit
	}
	for _, char := range draft.ConversationID {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_') {
			return EditDraft{}, ErrInvalidEdit
		}
	}
	if !utf8.ValidString(draft.Text) || strings.ContainsRune(draft.Text, 0) ||
		utf8.RuneCountInString(draft.Text) < 1 || utf8.RuneCountInString(draft.Text) > 280 {
		return EditDraft{}, ErrInvalidEdit
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

// Edit is a storage-only example, not authentication or an event publisher.
// All unmatched scope/version predicates share one rejection; no existence lookup follows.
func (s *messageStore) Edit(ctx context.Context, draft EditDraft) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	draft, err := normalizeEdit(draft)
	if err != nil {
		return Message{}, err
	}
	if s.db == nil {
		return Message{}, ErrDatabaseRequired
	}

	var message Message
	err = s.db.QueryRowContext(ctx, `
		UPDATE lesson28_messages
		SET body=$4, version=version+1
		WHERE conversation_id=$1 AND id=$2 AND version=$3
		RETURNING id, conversation_id, body, version`,
		draft.ConversationID, draft.MessageID, draft.ExpectedVersion, draft.Text,
	).Scan(&message.ID, &message.ConversationID, &message.Text, &message.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrEditRejected
	}
	if err != nil {
		return Message{}, fmt.Errorf("edit message: %w", err)
	}
	return message, nil
}

func run(ctx context.Context, output io.Writer, databaseURL string) error {
	db, err := openDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	// Bootstrap a fresh lesson table; schema upgrades need versioned migrations.
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("create lesson schema: %w", err)
	}
	var id int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO lesson28_messages (conversation_id, body)
		VALUES ('lesson-chat', 'original text') RETURNING id`,
	).Scan(&id); err != nil {
		return fmt.Errorf("seed demo message: %w", err)
	}
	message, err := (&messageStore{db: db}).Edit(ctx, EditDraft{
		ConversationID: "lesson-chat", MessageID: id, ExpectedVersion: 1, Text: "edited once",
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(message)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, os.Stdout, os.Getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
