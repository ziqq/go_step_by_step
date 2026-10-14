package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidatePageSize(t *testing.T) {
	tests := []struct {
		name    string
		limit   int
		wantErr bool
	}{
		{name: "minimum", limit: 1},
		{name: "maximum", limit: maxPageSize},
		{name: "zero", limit: 0, wantErr: true},
		{name: "negative", limit: -1, wantErr: true},
		{name: "above maximum", limit: maxPageSize + 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePageSize(test.limit)
			if test.wantErr && !errors.Is(err, ErrInvalidPageSize) {
				t.Fatalf("validatePageSize(%d) error = %v, want ErrInvalidPageSize", test.limit, err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("validatePageSize(%d) error = %v", test.limit, err)
			}
		})
	}
}

func TestHistoryQueryUsesParametersAndConversationScope(t *testing.T) {
	conversationID := "chat-1' OR true --"
	query, args := historyQuery(conversationID, nil, 8)
	if strings.Contains(query, conversationID) {
		t.Fatal("conversation ID was interpolated into SQL")
	}
	if !strings.Contains(query, "WHERE conversation_id=$1") || !strings.Contains(query, "LIMIT $2") {
		t.Fatalf("latest query is missing its scope or bound limit: %s", query)
	}
	if !reflect.DeepEqual(args, []any{conversationID, 8}) {
		t.Fatalf("latest query args = %#v, want conversation and fetch limit", args)
	}

	createdAt := time.Date(2026, time.October, 14, 6, 0, 0, 0, time.FixedZone("offset", 3*60*60))
	cursor := &Cursor{CreatedAt: createdAt, MessageID: 42}
	query, args = historyQuery(conversationID, cursor, 9)
	if !strings.Contains(query, "(created_at, id) < ($2, $3)") || !strings.Contains(query, "WHERE conversation_id=$1") {
		t.Fatalf("cursor query is missing its exclusive tuple or conversation scope: %s", query)
	}
	wantArgs := []any{conversationID, createdAt.UTC(), int64(42), 9}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("cursor query args = %#v, want %#v", args, wantArgs)
	}
}

func TestMigrationFilesArePresentAndOrdered(t *testing.T) {
	previousVersion := 0
	for _, item := range migrations {
		if item.version <= previousVersion {
			t.Fatalf("migration versions are not increasing at %d", item.version)
		}
		previousVersion = item.version
		contents, err := migrationFS.ReadFile(item.path)
		if err != nil {
			t.Fatalf("read migration %q: %v", item.path, err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			t.Errorf("migration %q is empty", item.path)
		}
	}
}

func TestPostgresHistoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set POSTGRES_TEST_URL to run PostgreSQL history integration coverage")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := openDatabase(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	conversationID := fmt.Sprintf("lesson-23-%d", time.Now().UnixNano())
	userID := "user-" + conversationID
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM lesson23_messages WHERE conversation_id=$1`, conversationID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM lesson23_memberships WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID)
		_ = db.Close()
	})
	if err := applyMigrations(ctx, db); err != nil {
		t.Fatalf("applyMigrations(): %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO lesson23_memberships (conversation_id, user_id, active) VALUES ($1, $2, true)`,
		conversationID, userID,
	); err != nil {
		t.Fatalf("insert test membership: %v", err)
	}

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	var ids []int64
	for _, body := range []string{"first", "second", "third"} {
		var id int64
		if err := db.QueryRowContext(ctx, `
			INSERT INTO lesson23_messages (conversation_id, body, created_at)
			VALUES ($1, $2, $3) RETURNING id`, conversationID, body, createdAt,
		).Scan(&id); err != nil {
			t.Fatalf("insert test message: %v", err)
		}
		ids = append(ids, id)
	}

	store := &postgresHistoryStore{db: db}
	first, err := store.ListPage(ctx, userID, conversationID, nil, 2)
	if err != nil {
		t.Fatalf("ListPage(first): %v", err)
	}
	if len(first.Messages) != 2 || !first.HasMore || first.Next == nil {
		t.Fatalf("first page = %#v, want two messages and a next cursor", first)
	}
	if first.Messages[0].ID != ids[2] || first.Messages[1].ID != ids[1] {
		t.Fatalf("first page IDs = (%d, %d), want (%d, %d)", first.Messages[0].ID, first.Messages[1].ID, ids[2], ids[1])
	}

	second, err := store.ListPage(ctx, userID, conversationID, first.Next, 2)
	if err != nil {
		t.Fatalf("ListPage(second): %v", err)
	}
	if len(second.Messages) != 1 || second.HasMore || second.Messages[0].ID != ids[0] {
		t.Fatalf("second page = %#v, want only the oldest message", second)
	}
	if _, err := store.ListPage(ctx, "not-a-member", conversationID, nil, 2); !errors.Is(err, ErrNotMember) {
		t.Fatalf("ListPage(non-member) error = %v, want ErrNotMember", err)
	}
}
