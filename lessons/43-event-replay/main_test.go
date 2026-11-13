package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestReplayAcrossPagesWithGaps(t *testing.T) {
	var queries, delivered []int64
	load := func(_ context.Context, after int64, limit int) ([]event, error) {
		if limit != 2 {
			t.Fatalf("limit = %d, want 2", limit)
		}
		queries = append(queries, after)
		switch after {
		case 2:
			return []event{{id: 4}, {id: 9}}, nil
		case 9:
			return []event{{id: 12}}, nil
		default:
			t.Fatalf("unexpected cursor %d", after)
			return nil, nil
		}
	}
	cursor, err := replay(context.Background(), 2, 2, load, func(_ context.Context, item event) error {
		delivered = append(delivered, item.id)
		return nil
	})
	if err != nil || cursor != 12 || !slices.Equal(queries, []int64{2, 9}) ||
		!slices.Equal(delivered, []int64{4, 9, 12}) {
		t.Fatalf("cursor = %d, error = %v, queries = %v, delivered = %v", cursor, err, queries, delivered)
	}
}

func TestReplayRejectsMalformedPageBeforeDelivery(t *testing.T) {
	for _, tt := range []struct {
		name string
		page []event
	}{
		{"oversized", []event{{id: 4}, {id: 5}, {id: 6}}},
		{"stale", []event{{id: 3}}},
		{"duplicate", []event{{id: 4}, {id: 4}}},
		{"descending", []event{{id: 5}, {id: 4}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cursor, err := replay(context.Background(), 3, 2, func(context.Context, int64, int) ([]event, error) {
				return tt.page, nil
			}, func(context.Context, event) error {
				t.Fatal("delivered an event from a malformed page")
				return nil
			})
			if cursor != 3 || !errors.Is(err, errReplayPage) {
				t.Fatalf("cursor = %d, error = %v", cursor, err)
			}
		})
	}
}

func TestReplayResumesAfterDeliveryFailure(t *testing.T) {
	failure := errors.New("writer failed")
	load := func(_ context.Context, after int64, _ int) ([]event, error) {
		switch after {
		case 2:
			return []event{{id: 4}, {id: 7}}, nil
		case 4:
			return []event{{id: 7}}, nil
		default:
			t.Fatalf("unexpected cursor %d", after)
			return nil, nil
		}
	}
	var accepted []int64
	cursor, err := replay(context.Background(), 2, 2, load, func(_ context.Context, item event) error {
		if item.id == 7 {
			return failure
		}
		accepted = append(accepted, item.id)
		return nil
	})
	if cursor != 4 || !errors.Is(err, failure) {
		t.Fatalf("cursor = %d, error = %v", cursor, err)
	}
	cursor, err = replay(context.Background(), cursor, 2, load, func(_ context.Context, item event) error {
		accepted = append(accepted, item.id)
		return nil
	})
	if cursor != 7 || err != nil || !slices.Equal(accepted, []int64{4, 7}) {
		t.Fatalf("cursor = %d, error = %v, accepted = %v", cursor, err, accepted)
	}
}

func TestReplayStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cursor, err := replay(ctx, 2, 2, func(got context.Context, after int64, _ int) ([]event, error) {
		if got != ctx || after != 2 {
			t.Fatal("unexpected load")
		}
		return []event{{id: 4}, {id: 7}}, nil
	}, func(got context.Context, item event) error {
		if got != ctx || item.id != 4 {
			t.Fatal("unexpected delivery after cancellation")
		}
		cancel()
		return nil
	})
	if cursor != 4 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cursor = %d, error = %v", cursor, err)
	}
}

func TestReplayPreservesLoadFailure(t *testing.T) {
	failure := errors.New("store unavailable")
	cursor, err := replay(context.Background(), 9, 2, func(context.Context, int64, int) ([]event, error) {
		return []event{{id: 10}}, failure
	}, func(context.Context, event) error {
		t.Fatal("delivered an event from a failed read")
		return nil
	})
	if cursor != 9 || !errors.Is(err, failure) {
		t.Fatalf("cursor = %d, error = %v", cursor, err)
	}
}
