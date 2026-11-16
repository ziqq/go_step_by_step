package main

import (
	"context"
	"fmt"
	"time"
)

// serve runs one initial refresh and then refreshes on a wake signal or a tick.
// The caller owns both channels and the refresh callback owns the cursor.
func serve(ctx context.Context, wake <-chan struct{}, ticks <-chan time.Time, refresh func(context.Context) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := refresh(ctx); err != nil {
			return fmt.Errorf("refresh events: %w", err)
		}

		waiting := true
		for waiting {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case _, open := <-wake:
				if !open {
					wake = nil
				}
				waiting = !open
			case _, open := <-ticks:
				if !open {
					ticks = nil
				}
				waiting = !open
			}
		}
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	ticks := make(chan time.Time, 1)

	cursor := 0
	// Both channels are driven by the demo so the output is deterministic.
	err := serve(ctx, wake, ticks, func(context.Context) error {
		switch cursor {
		case 0:
			fmt.Println("initial read applied event 1")
			cursor = 1
			wake <- struct{}{}
		case 1:
			fmt.Println("wake read applied event 2")
			cursor = 2
			ticks <- time.Time{}
		case 2:
			fmt.Println("periodic read applied event 3")
			cancel()
		}
		return nil
	})
	if err != context.Canceled {
		panic(err)
	}
}
