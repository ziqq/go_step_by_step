package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
)

const defaultInputPath = "lessons/07-files-json-time-regexp-flags/events.json"

var ErrInvalidEvent = errors.New("invalid event")

type Event struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	UserID    string    `json:"user_id"`
	Message   string    `json:"message"`
}

func loadEvents(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}

	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}

	for _, event := range events {
		if event.ID <= 0 || event.Type == "" || event.Timestamp.IsZero() {
			return nil, fmt.Errorf("%w: id=%d", ErrInvalidEvent, event.ID)
		}
	}

	return events, nil
}

func parseAfter(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	after, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse after timestamp: %w", err)
	}

	return after, nil
}

func compileMessagePattern(value string) (*regexp.Regexp, error) {
	if value == "" {
		return nil, nil
	}

	pattern, err := regexp.Compile(value)
	if err != nil {
		return nil, fmt.Errorf("compile message pattern: %w", err)
	}

	return pattern, nil
}

func filterEvents(events []Event, eventType string, after time.Time, messagePattern *regexp.Regexp) []Event {
	filtered := make([]Event, 0, len(events))
	for _, event := range events {
		if eventType != "" && event.Type != eventType {
			continue
		}
		if !after.IsZero() && event.Timestamp.Before(after) {
			continue
		}
		if messagePattern != nil && !messagePattern.MatchString(event.Message) {
			continue
		}

		filtered = append(filtered, event)
	}

	return filtered
}

func run(args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("events", flag.ContinueOnError)
	flags.SetOutput(errorOutput)

	inputPath := flags.String("input", defaultInputPath, "path to a JSON event file")
	eventType := flags.String("type", "", "keep only events with this type")
	afterValue := flags.String("after", "", "keep events at or after an RFC3339 timestamp")
	patternValue := flags.String("pattern", "", "regular expression matched against the message")

	if err := flags.Parse(args); err != nil {
		return err
	}

	after, err := parseAfter(*afterValue)
	if err != nil {
		return err
	}
	pattern, err := compileMessagePattern(*patternValue)
	if err != nil {
		return err
	}
	events, err := loadEvents(*inputPath)
	if err != nil {
		return err
	}

	filtered := filterEvents(events, *eventType, after, pattern)
	fmt.Fprintf(output, "matched events: %d\n", len(filtered))
	for _, event := range filtered {
		fmt.Fprintf(output, "%d %s %s %s\n", event.ID, event.Timestamp.Format(time.RFC3339), event.Type, event.Message)
	}

	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
