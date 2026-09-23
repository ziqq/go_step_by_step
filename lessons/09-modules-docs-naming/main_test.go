package main

import (
	"reflect"
	"testing"
)

func TestCountByTypeNormalizesAndIgnoresEmptyTypes(t *testing.T) {
	events := []Event{
		{Type: "Message"},
		{Type: " message "},
		{Type: "system"},
		{Type: " "},
	}

	want := map[string]int{"message": 2, "system": 1}
	if got := CountByType(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("CountByType() = %#v, want %#v", got, want)
	}
}

func TestCountByTypeEmptyReturnsEmptyMap(t *testing.T) {
	got := CountByType(nil)
	if got == nil {
		t.Fatal("CountByType(nil) returned nil map, want empty map")
	}
	if len(got) != 0 {
		t.Fatalf("CountByType(nil) length = %d, want 0", len(got))
	}
}

func TestFormatSummarySortsTypes(t *testing.T) {
	events := []Event{
		{Type: "system"},
		{Type: "message"},
		{Type: "message"},
	}

	if got, want := FormatSummary(events), "events=3 types=message:2,system:1"; got != want {
		t.Fatalf("FormatSummary() = %q, want %q", got, want)
	}
}

func TestFormatSummaryEmpty(t *testing.T) {
	if got, want := FormatSummary(nil), "events=0 types="; got != want {
		t.Fatalf("FormatSummary(nil) = %q, want %q", got, want)
	}
}
