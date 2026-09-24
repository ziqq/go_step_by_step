package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    Record
		wantErr bool
	}{
		{
			name: "normalizes fields",
			line: " WARN | slow request ",
			want: Record{Level: "warn", Message: "slow request"},
		},
		{
			name:    "rejects missing separator",
			line:    "warn slow request",
			wantErr: true,
		},
		{
			name:    "rejects empty message",
			line:    "warn| ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLine(tt.line)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidRecord) {
					t.Fatalf("parseLine() error = %v, want ErrInvalidRecord", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseLine() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseLine() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAnalyzePreservesInputOrder(t *testing.T) {
	lines := []string{
		"info|first",
		"warn|second",
		"error|third",
	}

	got, err := analyze(context.Background(), lines, 2)
	if err != nil {
		t.Fatalf("analyze() error = %v", err)
	}

	want := []Record{
		{Index: 0, Level: "info", Message: "first"},
		{Index: 1, Level: "warn", Message: "second"},
		{Index: 2, Level: "error", Message: "third"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("analyze() = %#v, want %#v", got, want)
	}
}

func TestAnalyzeRejectsInvalidWorkerCount(t *testing.T) {
	for _, workers := range []int{0, -1} {
		t.Run("workers", func(t *testing.T) {
			if _, err := analyze(context.Background(), nil, workers); err == nil {
				t.Fatalf("analyze() with workers=%d returned nil error", workers)
			}
		})
	}
}

func TestAnalyzeReturnsInvalidRecord(t *testing.T) {
	_, err := analyze(context.Background(), []string{"info|ok", "broken"}, 2)
	if !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("analyze() error = %v, want ErrInvalidRecord", err)
	}
}

func TestAnalyzeRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := analyze(ctx, []string{"info|ignored"}, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("analyze() error = %v, want context.Canceled", err)
	}
}

func TestAnalyzeEmptyInput(t *testing.T) {
	got, err := analyze(context.Background(), nil, 1)
	if err != nil {
		t.Fatalf("analyze() error = %v", err)
	}
	if got == nil {
		t.Fatal("analyze() returned nil slice for empty input")
	}
	if len(got) != 0 {
		t.Fatalf("analyze() returned %d records, want zero", len(got))
	}
}
