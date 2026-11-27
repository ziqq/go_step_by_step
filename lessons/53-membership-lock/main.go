// Package main keeps a membership decision valid until a message commits.
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
	errInvalid   = errors.New("invalid message")
	errForbidden = errors.New("active membership required")
	errDatabase  = errors.New("database required")
)

type draft struct {
	TenantID, ConversationID, ActorID int64
	Body                              string
}

type store struct{ db *sql.DB }

// Send requires tenant/actor IDs from verified authentication, not request JSON.
// This lesson authorizes one write; it does not deduplicate retries.
func (s store) Send(ctx context.Context, d draft) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if d.TenantID <= 0 || d.ConversationID <= 0 || d.ActorID <= 0 ||
		len(d.Body) > 280*utf8.UTFMax || !utf8.ValidString(d.Body) ||
		utf8.RuneCountInString(d.Body) > 280 || strings.TrimSpace(d.Body) == "" || strings.ContainsRune(d.Body, 0) {
		return 0, errInvalid
	}
	if s.db == nil {
		return 0, errDatabase
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("begin authorized send: %w", err)
	}
	defer tx.Rollback()
	var memberID int64
	err = tx.QueryRowContext(ctx, `
		SELECT actor_id FROM lesson53_members
		WHERE tenant_id=$1 AND conversation_id=$2 AND actor_id=$3 AND active
		FOR SHARE`, d.TenantID, d.ConversationID, d.ActorID).Scan(&memberID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errForbidden
	}
	if err != nil {
		return 0, fmt.Errorf("lock membership: %w", err)
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO lesson53_messages (tenant_id, conversation_id, actor_id, body)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		d.TenantID, d.ConversationID, d.ActorID, d.Body).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert authorized message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		// An I/O error can hide a committed write. Do not retry blindly.
		return 0, fmt.Errorf("commit authorized send: %w", err)
	}
	return id, nil
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
	// The demo owns this fixed membership in a disposable database.
	if _, err := db.ExecContext(ctx, `INSERT INTO lesson53_members
		(tenant_id, conversation_id, actor_id, active) VALUES (1, 9, 7, true)
		ON CONFLICT (tenant_id, conversation_id, actor_id) DO UPDATE SET active=true`); err != nil {
		return err
	}
	d := draft{TenantID: 1, ConversationID: 9, ActorID: 7, Body: "hello"}
	id, err := (store{db}).Send(ctx, d)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `UPDATE lesson53_members SET active=false
		WHERE tenant_id=1 AND conversation_id=9 AND actor_id=7`); err != nil {
		return err
	}
	deniedID, err := (store{db}).Send(ctx, d)
	if !errors.Is(err, errForbidden) || deniedID != 0 {
		return fmt.Errorf("expected denied send, got ID %d and error %v", deniedID, err)
	}
	_, err = fmt.Fprintln(out, "authorized write committed:", id > 0)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "write after revocation denied:", deniedID == 0)
	return err
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, os.Getenv("DATABASE_URL"), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "lesson demo failed; check disposable database configuration")
		os.Exit(1)
	}
}
