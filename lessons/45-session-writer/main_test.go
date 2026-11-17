package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestWriterDrainsEventsInOrderOnClose(t *testing.T) {
	events := make(chan string, 2)
	events <- "first"
	events <- "second"
	close(events)

	var written []frame
	err := runWriter(context.Background(), events, nil, time.Second, func(_ context.Context, next frame) error {
		written = append(written, next)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []frame{{kind: "event", body: "first"}, {kind: "event", body: "second"}}
	if !reflect.DeepEqual(written, want) {
		t.Fatalf("writes = %+v, want %+v", written, want)
	}
}

func TestWriterSendsPingAndStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	ticks <- time.Time{}

	calls := 0
	err := runWriter(ctx, nil, ticks, time.Second, func(_ context.Context, next frame) error {
		calls++
		if next != (frame{kind: "ping"}) {
			t.Fatalf("frame = %+v, want ping", next)
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("error = %v, calls = %d", err, calls)
	}
}

func TestWriterClosedTicksDoNotSendPing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ticks := make(chan time.Time)
	close(ticks)

	err := runWriter(ctx, nil, ticks, time.Second, func(_ context.Context, next frame) error {
		t.Fatalf("closed ticker yielded a frame: %+v", next)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want session timeout", err)
	}
}

func TestWriterTimeoutStopsBeforeNextEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events := make(chan string, 2)
	events <- "first"
	events <- "second"
	close(events)

	calls := 0
	var childDeadline time.Time
	err := runWriter(ctx, events, nil, 20*time.Millisecond, func(writeCtx context.Context, _ frame) error {
		calls++
		childDeadline, _ = writeCtx.Deadline()
		<-writeCtx.Done()
		return writeCtx.Err()
	})
	parentDeadline, _ := ctx.Deadline()
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 || childDeadline.IsZero() || !childDeadline.Before(parentDeadline) {
		t.Fatalf("error = %v, writes = %d, write deadline = %v, session deadline = %v", err, calls, childDeadline, parentDeadline)
	}
}

func TestWriterPreservesSinkError(t *testing.T) {
	ticks := make(chan time.Time, 1)
	ticks <- time.Time{}
	failure := errors.New("connection closed")
	err := runWriter(context.Background(), nil, ticks, time.Second, func(context.Context, frame) error {
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want wrapped sink error", err)
	}
}
