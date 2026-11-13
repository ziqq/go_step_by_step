package main

import (
	"context"
	"errors"
	"fmt"
)

type event struct {
	id   int64
	body string
}

// loadPage reads events visible to the authorized stream in ascending ID order.
type loadPage func(ctx context.Context, after int64, limit int) ([]event, error)
type deliverEvent func(context.Context, event) error

var (
	errReplayInput = errors.New("invalid replay cursor or page size")
	errReplayPage  = errors.New("invalid event page")
)

// replay returns the last cursor accepted by deliver, even on failure.
func replay(ctx context.Context, after int64, size int, load loadPage, deliver deliverEvent) (int64, error) {
	if after < 0 || size < 1 {
		return after, errReplayInput
	}

	for {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		page, err := load(ctx, after, size)
		if err != nil {
			return after, fmt.Errorf("load after %d: %w", after, err)
		}
		if err := ctx.Err(); err != nil {
			return after, err
		}
		if len(page) > size {
			return after, errReplayPage
		}
		previous := after
		for _, item := range page {
			if item.id <= previous {
				return after, fmt.Errorf("%w: id %d follows %d", errReplayPage, item.id, previous)
			}
			previous = item.id
		}

		for _, item := range page {
			if err := ctx.Err(); err != nil {
				return after, err
			}
			if err := deliver(ctx, item); err != nil {
				return after, fmt.Errorf("deliver event %d: %w", item.id, err)
			}
			after = item.id
		}
		if len(page) < size {
			return after, ctx.Err()
		}
	}
}

func main() {
	history := []event{{2, "already seen"}, {4, "message created"}, {7, "message edited"}}
	load := func(_ context.Context, after int64, limit int) ([]event, error) {
		for i, item := range history {
			if item.id > after {
				return history[i:min(i+limit, len(history))], nil
			}
		}
		return nil, nil
	}
	deliver := func(_ context.Context, item event) error {
		fmt.Println(item.id, item.body)
		return nil
	}

	cursor, err := replay(context.Background(), 2, 1, load, deliver)
	if err != nil {
		panic(err)
	}
	fmt.Println("cursor:", cursor)
}
