package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateExpense(t *testing.T) {
	tests := []struct {
		name        string
		description string
		amountCents int64
		wantErr     bool
	}{
		{name: "valid expense", description: "coffee", amountCents: 350},
		{name: "empty description", description: "  ", amountCents: 350, wantErr: true},
		{name: "description too long", description: strings.Repeat("я", maxDescriptionRunes+1), amountCents: 350, wantErr: true},
		{name: "zero amount", description: "coffee", amountCents: 0, wantErr: true},
		{name: "negative amount", description: "coffee", amountCents: -1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExpense(tt.description, tt.amountCents)
			if tt.wantErr && !errors.Is(err, ErrInvalidExpense) {
				t.Fatalf("validateExpense() error = %v, want ErrInvalidExpense", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateExpense() error = %v", err)
			}
		})
	}
}

func TestValidateRecentLimit(t *testing.T) {
	for _, tt := range []struct {
		limit   int
		wantErr bool
	}{
		{limit: 1},
		{limit: maxRecentLimit},
		{limit: 0, wantErr: true},
		{limit: -1, wantErr: true},
		{limit: maxRecentLimit + 1, wantErr: true},
	} {
		err := validateRecentLimit(tt.limit)
		if tt.wantErr && !errors.Is(err, ErrInvalidLimit) {
			t.Errorf("validateRecentLimit(%d) error = %v, want ErrInvalidLimit", tt.limit, err)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("validateRecentLimit(%d) error = %v", tt.limit, err)
		}
	}
}

func TestMigrationFilesArePresentAndOrdered(t *testing.T) {
	if len(migrations) == 0 {
		t.Fatal("no migrations configured")
	}
	previousVersion := 0
	for _, migration := range migrations {
		if migration.version <= previousVersion {
			t.Fatalf("migration versions are not increasing at %d", migration.version)
		}
		previousVersion = migration.version

		contents, err := migrationFS.ReadFile(migration.path)
		if err != nil {
			t.Fatalf("read migration %q: %v", migration.path, err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			t.Errorf("migration %q is empty", migration.path)
		}
	}
}

func TestPostgresRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set POSTGRES_TEST_URL to run PostgreSQL integration coverage")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := openDatabase(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := applyMigrations(ctx, db); err != nil {
		t.Fatalf("applyMigrations(): %v", err)
	}

	description := fmt.Sprintf("integration expense %d", time.Now().UnixNano())
	store := &repository{db: db}
	expense, err := store.create(ctx, description, 725)
	if err != nil {
		t.Fatalf("create(): %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM expenses WHERE id=$1`, expense.ID)
	})

	var eventCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM expense_events WHERE expense_id=$1`, expense.ID).Scan(&eventCount); err != nil {
		t.Fatalf("read expense event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("event count = %d, want 1", eventCount)
	}

	recent, err := store.recent(ctx, 10)
	if err != nil {
		t.Fatalf("recent(): %v", err)
	}
	found := false
	for _, item := range recent {
		if item.ID == expense.ID {
			found = item.Description == description && item.AmountCents == 725
			break
		}
	}
	if !found {
		t.Fatalf("recent expenses do not contain created expense %#v", expense)
	}
}
