// Package main demonstrates a PostgreSQL-backed, conversation-scoped history store.
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

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	migrationLockID int64 = 20261014
	maxPageSize           = 50
)

var (
	ErrNotMember       = errors.New("user is not an active conversation member")
	ErrInvalidPageSize = errors.New("invalid history page size")
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Message struct {
	ID             int64
	ConversationID string
	Text           string
	CreatedAt      time.Time
}

type Cursor struct {
	CreatedAt time.Time
	MessageID int64
}

type HistoryPage struct {
	Messages []Message
	Next     *Cursor
	HasMore  bool
}

type postgresHistoryStore struct {
	db *sql.DB
}

type migration struct {
	version int
	path    string
}

var migrations = []migration{
	{version: 1, path: "migrations/001_memberships.sql"},
	{version: 2, path: "migrations/002_messages.sql"},
	{version: 3, path: "migrations/003_messages_history_index.sql"},
}

func validatePageSize(limit int) error {
	if limit < 1 || limit > maxPageSize {
		return ErrInvalidPageSize
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
		return fmt.Errorf("begin history migrations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock history migrations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS lesson23_schema_migrations (
			version integer PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	for _, item := range migrations {
		var applied bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM lesson23_schema_migrations WHERE version=$1)`, item.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check history migration %d: %w", item.version, err)
		}
		if applied {
			continue
		}

		statement, err := fs.ReadFile(migrationFS, item.path)
		if err != nil {
			return fmt.Errorf("read history migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, string(statement)); err != nil {
			return fmt.Errorf("apply history migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO lesson23_schema_migrations (version) VALUES ($1)`, item.version,
		); err != nil {
			return fmt.Errorf("record history migration %d: %w", item.version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit history migrations: %w", err)
	}
	return nil
}

func (s *postgresHistoryStore) ListPage(ctx context.Context, userID, conversationID string, before *Cursor, limit int) (HistoryPage, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(conversationID) == "" {
		return HistoryPage{}, errors.New("user and conversation IDs are required")
	}
	if err := validatePageSize(limit); err != nil {
		return HistoryPage{}, err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return HistoryPage{}, fmt.Errorf("begin history snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var allowed bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM lesson23_memberships
			WHERE conversation_id=$1 AND user_id=$2 AND active=true
		)`, conversationID, userID).Scan(&allowed)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("check active conversation membership: %w", err)
	}
	if !allowed {
		return HistoryPage{}, ErrNotMember
	}

	query, args := historyQuery(conversationID, before, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("query conversation history: %w", err)
	}
	messages := make([]Message, 0, limit+1)
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.ConversationID, &message.Text, &message.CreatedAt); err != nil {
			_ = rows.Close()
			return HistoryPage{}, fmt.Errorf("scan conversation message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return HistoryPage{}, fmt.Errorf("iterate conversation messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return HistoryPage{}, fmt.Errorf("close conversation message rows: %w", err)
	}

	page := HistoryPage{Messages: messages}
	if len(messages) > limit {
		page.HasMore = true
		page.Messages = messages[:limit]
		last := page.Messages[len(page.Messages)-1]
		page.Next = &Cursor{CreatedAt: last.CreatedAt, MessageID: last.ID}
	}
	if err := tx.Commit(); err != nil {
		return HistoryPage{}, fmt.Errorf("finish history snapshot: %w", err)
	}
	return page, nil
}

func historyQuery(conversationID string, before *Cursor, fetchLimit int) (string, []any) {
	if before == nil {
		return `
			SELECT id, conversation_id, body, created_at
			FROM lesson23_messages
			WHERE conversation_id=$1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`, []any{conversationID, fetchLimit}
	}
	return `
		SELECT id, conversation_id, body, created_at
		FROM lesson23_messages
		WHERE conversation_id=$1
		  AND (created_at, id) < ($2, $3)
		ORDER BY created_at DESC, id DESC
		LIMIT $4`, []any{conversationID, before.CreatedAt.UTC(), before.MessageID, fetchLimit}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
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

	userID := envOrDefault("DEMO_USER_ID", "lesson-user")
	conversationID := envOrDefault("DEMO_CONVERSATION_ID", "lesson-chat")
	page, err := (&postgresHistoryStore{db: db}).ListPage(ctx, userID, conversationID, nil, 20)
	if errors.Is(err, ErrNotMember) {
		fmt.Println("PostgreSQL schema ready; no active demo membership")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("loaded %d messages; has_more=%t\n", len(page.Messages), page.HasMore)
	for _, message := range page.Messages {
		fmt.Printf("%d %s\n", message.ID, message.Text)
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
