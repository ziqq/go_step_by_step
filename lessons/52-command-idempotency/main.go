package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schemaSQL string

var (
	errInvalid  = errors.New("invalid send")
	errConflict = errors.New("command key reused with different input")
	errDatabase = errors.New("database required")
)

type command struct {
	TenantID, ActorID, ConversationID int64
	Key, Body                         string
}

type result struct {
	MessageID int64
	Created   bool
}

type store struct{ db *sql.DB }

func validText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value) &&
		strings.TrimSpace(value) != "" && !strings.ContainsRune(value, 0)
}

// Send uses trusted tenant/actor IDs supplied by the authenticated caller.
// The caller must authorize every attempt, including replays.
func (s store) Send(ctx context.Context, c command) (result, error) {
	if err := ctx.Err(); err != nil {
		return result{}, err
	}
	if c.TenantID <= 0 || c.ActorID <= 0 || c.ConversationID <= 0 ||
		!validText(c.Key, 64) || !validText(c.Body, 280*utf8.UTFMax) || utf8.RuneCountInString(c.Body) > 280 {
		return result{}, errInvalid
	}
	if s.db == nil {
		return result{}, errDatabase
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO lesson52_sends (tenant_id, actor_id, command_key, conversation_id, body)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, actor_id, command_key) DO NOTHING RETURNING id`,
		c.TenantID, c.ActorID, c.Key, c.ConversationID, c.Body).Scan(&id)
	if err == nil {
		return result{MessageID: id, Created: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		// An I/O error may hide a committed insert. Keep the same key to reconcile.
		return result{}, fmt.Errorf("insert send: %w", err)
	}
	// A separate statement sees a committed winner that the INSERT may not see.
	// No UPDATE or DELETE may change this lesson's retained replay records.
	var conversationID int64
	var body string
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, body FROM lesson52_sends
		WHERE tenant_id=$1 AND actor_id=$2 AND command_key=$3`,
		c.TenantID, c.ActorID, c.Key).Scan(&id, &conversationID, &body); err != nil {
		return result{}, fmt.Errorf("read send receipt: %w", err)
	}
	if conversationID != c.ConversationID || body != c.Body {
		return result{}, errConflict
	}
	return result{MessageID: id}, nil
}

func run(ctx context.Context, databaseURL string, out io.Writer) error {
	if strings.TrimSpace(databaseURL) == "" {
		return errDatabase
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	c := command{TenantID: 1, ActorID: 7, ConversationID: 9, Key: "demo-send-1", Body: "hello"}
	s := store{db: db}
	first, err := s.Send(ctx, c)
	if err != nil {
		return err
	}
	second, err := s.Send(ctx, c)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "same message:", first.MessageID == second.MessageID); err != nil {
		return err
	}
	_, err = s.Send(ctx, command{TenantID: 1, ActorID: 7, ConversationID: 9, Key: c.Key, Body: "changed"})
	_, writeErr := fmt.Fprintln(out, "changed input rejected:", errors.Is(err, errConflict))
	if !errors.Is(err, errConflict) {
		return fmt.Errorf("expected conflict, got %v", err)
	}
	return writeErr
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, os.Getenv("DATABASE_URL"), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "lesson demo failed; check disposable database configuration")
		os.Exit(1)
	}
}
