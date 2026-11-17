package main

import (
	"context"
	"fmt"
	"time"
)

type frame struct {
	kind string
	body string
}

// runWriter owns all calls to write. The producer owns closing events.
func runWriter(ctx context.Context, events <-chan string, ticks <-chan time.Time, timeout time.Duration, write func(context.Context, frame) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		var next frame
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-events:
			if !open {
				return ctx.Err()
			}
			next = frame{kind: "event", body: event}
		case _, open := <-ticks:
			if !open {
				ticks = nil
				continue
			}
			next = frame{kind: "ping"}
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, timeout)
		err := write(writeCtx, next)
		cancel()
		if err != nil {
			return fmt.Errorf("write %s frame: %w", next.kind, err)
		}
	}
}

func main() {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	events := make(chan string, 1)
	ticks := make(chan time.Time, 1)
	events <- "message 42"

	err := runWriter(ctx, events, ticks, time.Second, func(_ context.Context, next frame) error {
		switch next.kind {
		case "event":
			fmt.Println("sent event:", next.body)
			ticks <- time.Time{}
		case "ping":
			fmt.Println("sent ping")
			stop()
		}
		return nil
	})
	if err != context.Canceled {
		panic(err)
	}
}
