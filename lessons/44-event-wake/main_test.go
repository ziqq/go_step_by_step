package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServeRefreshesInitiallyOnWakeAndOnTick(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wake := make(chan struct{}, 1)
	ticks := make(chan time.Time, 1)
	reads := 0
	err := serve(ctx, wake, ticks, func(got context.Context) error {
		if got != ctx {
			t.Fatal("refresh received a different context")
		}
		reads++
		switch reads {
		case 1:
			wake <- struct{}{}
		case 2:
			if len(wake) != 0 {
				t.Fatal("second read did not consume the wake signal")
			}
			close(wake)
			ticks <- time.Time{}
		case 3:
			if len(ticks) != 0 {
				t.Fatal("third read did not consume the tick")
			}
			cancel()
		default:
			t.Fatalf("unexpected refresh %d", reads)
		}
		return nil
	})
	if reads != 3 || !errors.Is(err, context.Canceled) {
		t.Fatalf("reads = %d, error = %v", reads, err)
	}
}

func TestServeClosedInputsDoNotRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wake := make(chan struct{})
	ticks := make(chan time.Time)
	close(wake)
	close(ticks)
	reads := 0
	err := serve(ctx, wake, ticks, func(context.Context) error {
		reads++
		if reads > 1 {
			t.Fatal("a closed input triggered a refresh")
		}
		return nil
	})
	if reads != 1 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reads = %d, error = %v", reads, err)
	}
}

func TestServeReturnsRefreshError(t *testing.T) {
	failure := errors.New("store unavailable")
	err := serve(context.Background(), nil, nil, func(context.Context) error {
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want %v", err, failure)
	}
}

func TestServeAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := serve(ctx, nil, nil, func(context.Context) error {
		t.Fatal("refresh ran with a canceled context")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
