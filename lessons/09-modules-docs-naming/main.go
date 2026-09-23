package main

import (
	"fmt"
	"sort"
	"strings"
)

type Event struct {
	Type    string
	Message string
}

// CountByType counts valid event types and ignores events without a type.
func CountByType(events []Event) map[string]int {
	counts := make(map[string]int)
	for _, event := range events {
		eventType := normalizeType(event.Type)
		if eventType == "" {
			continue
		}
		counts[eventType]++
	}

	return counts
}

// FormatSummary returns a deterministic, human-readable event summary.
func FormatSummary(events []Event) string {
	counts := CountByType(events)
	types := make([]string, 0, len(counts))
	for eventType := range counts {
		types = append(types, eventType)
	}
	sort.Strings(types)

	parts := make([]string, 0, len(types))
	for _, eventType := range types {
		parts = append(parts, fmt.Sprintf("%s:%d", eventType, counts[eventType]))
	}

	return fmt.Sprintf("events=%d types=%s", len(events), strings.Join(parts, ","))
}

func normalizeType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func main() {
	events := []Event{
		{Type: "Message", Message: "Hello"},
		{Type: "message", Message: "Need help"},
		{Type: "system", Message: "Connected"},
	}

	fmt.Println(FormatSummary(events))
}
