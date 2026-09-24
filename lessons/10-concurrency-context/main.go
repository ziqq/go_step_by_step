package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var ErrInvalidRecord = errors.New("invalid record")

type Record struct {
	Index   int
	Level   string
	Message string
}

type indexedLine struct {
	index int
	value string
}

type parseResult struct {
	index  int
	record Record
	err    error
}

func parseLine(line string) (Record, error) {
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return Record{}, fmt.Errorf("%w: %q", ErrInvalidRecord, line)
	}

	return Record{
		Level:   strings.ToLower(strings.TrimSpace(parts[0])),
		Message: strings.TrimSpace(parts[1]),
	}, nil
}

func analyze(ctx context.Context, lines []string, workers int) ([]Record, error) {
	if workers <= 0 {
		return nil, fmt.Errorf("workers must be positive")
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan indexedLine)
	results := make(chan parseResult)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for {
			select {
			case <-workCtx.Done():
				return
			case item, ok := <-jobs:
				if !ok {
					return
				}

				record, err := parseLine(item.value)
				record.Index = item.index
				select {
				case results <- parseResult{index: item.index, record: record, err: err}:
				case <-workCtx.Done():
					return
				}
			}
		}
	}

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}

	go func() {
		defer close(jobs)
		for index, line := range lines {
			select {
			case jobs <- indexedLine{index: index, value: line}:
			case <-workCtx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	records := make([]Record, 0, len(lines))
	var firstErr error
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
				cancel()
			}
			continue
		}
		records = append(records, result.record)
	}

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Index < records[j].Index
	})
	return records, nil
}

func main() {
	lines := []string{
		"info|connected",
		"warn|slow request",
		"error|database unavailable",
	}

	records, err := analyze(context.Background(), lines, 2)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	for _, record := range records {
		fmt.Printf("%d %s %s\n", record.Index, record.Level, record.Message)
	}
}
