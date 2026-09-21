package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const testEventsJSON = `[
	{"id":1,"type":"message","timestamp":"2026-09-21T07:00:00Z","user_id":"u-1","message":"Hello"},
	{"id":2,"type":"system","timestamp":"2026-09-21T07:05:00Z","user_id":"","message":"Connected"},
	{"id":3,"type":"message","timestamp":"2026-09-21T07:10:00Z","user_id":"u-2","message":"Need help"}
]`

func writeTestEvents(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test events: %v", err)
	}

	return path
}

func TestLoadEvents(t *testing.T) {
	path := writeTestEvents(t, testEventsJSON)

	events, err := loadEvents(path)
	if err != nil {
		t.Fatalf("loadEvents() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("loadEvents() length = %d, want 3", len(events))
	}
	if got, want := events[2].Timestamp, time.Date(2026, 9, 21, 7, 10, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("events[2].Timestamp = %s, want %s", got, want)
	}
}

func TestLoadEventsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr error
	}{
		{name: "invalid JSON", content: "{"},
		{name: "invalid event", content: `[{"id":0,"type":"message","timestamp":"2026-09-21T07:00:00Z"}]`, wantErr: ErrInvalidEvent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestEvents(t, tt.content)
			_, err := loadEvents(path)
			if err == nil {
				t.Fatal("loadEvents() error = nil, want error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("loadEvents() error = %v, want errors.Is(..., %v)", err, tt.wantErr)
			}
		})
	}
}

func TestParseAfter(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Time
		wantErr bool
	}{
		{name: "empty means no lower bound"},
		{name: "RFC3339", value: "2026-09-21T07:05:00Z", want: time.Date(2026, 9, 21, 7, 5, 0, 0, time.UTC)},
		{name: "invalid timestamp", value: "tomorrow", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAfter(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseAfter() error = %v, wantErr %t", err, tt.wantErr)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("parseAfter() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestFilterEvents(t *testing.T) {
	path := writeTestEvents(t, testEventsJSON)
	events, err := loadEvents(path)
	if err != nil {
		t.Fatalf("loadEvents() error = %v", err)
	}
	pattern := regexpMustCompile(t, "help")
	after := time.Date(2026, 9, 21, 7, 5, 0, 0, time.UTC)

	got := filterEvents(events, "message", after, pattern)
	if len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("filterEvents() = %#v, want event 3", got)
	}
}

func TestRunWritesFilteredReport(t *testing.T) {
	path := writeTestEvents(t, testEventsJSON)
	var output bytes.Buffer

	err := run([]string{"-input", path, "-type", "message", "-pattern", "help"}, &output, &output)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	want := "matched events: 1\n3 2026-09-21T07:10:00Z message Need help\n"
	if output.String() != want {
		t.Fatalf("run() output = %q, want %q", output.String(), want)
	}
}

func TestRunRejectsInvalidPattern(t *testing.T) {
	path := writeTestEvents(t, testEventsJSON)
	err := run([]string{"-input", path, "-pattern", "["}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "compile message pattern") {
		t.Fatalf("run() error = %v, want invalid pattern error", err)
	}
}

func regexpMustCompile(t *testing.T, value string) *regexp.Regexp {
	t.Helper()

	pattern, err := regexp.Compile(value)
	if err != nil {
		t.Fatalf("regexp.Compile(%q): %v", value, err)
	}
	return pattern
}
