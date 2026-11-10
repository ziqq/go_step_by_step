package main

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"
)

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func TestShutdownOrdersAdmissionDrainAndClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	close(done)
	var steps []string
	err := shutdownService(ctx,
		func() { steps = append(steps, "unready") },
		func() <-chan struct{} {
			steps = append(steps, "stop workers")
			return done
		},
		func(got context.Context) error {
			if got != ctx {
				t.Fatal("HTTP drain received a different context")
			}
			steps = append(steps, "drain HTTP")
			return nil
		},
		closeFunc(func() error {
			steps = append(steps, "close store")
			return nil
		}),
	)
	want := []string{"unready", "stop workers", "drain HTTP", "close store"}
	if err != nil || !slices.Equal(steps, want) {
		t.Fatalf("steps=%v err=%v; want %v and nil", steps, err, want)
	}
}

func TestHTTPFailureLeavesResourceOpen(t *testing.T) {
	drainErr := errors.New("listener failed")
	err := shutdownService(context.Background(), func() {},
		func() <-chan struct{} { return make(chan struct{}) },
		func(context.Context) error { return drainErr },
		closeFunc(func() error { t.Fatal("closed after HTTP failure"); return nil }),
	)
	if !errors.Is(err, drainErr) {
		t.Fatalf("error=%v, want %v", err, drainErr)
	}
}

func TestCloseWaitsForWorkersAfterHTTPDrains(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done, drained := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- shutdownService(ctx, func() {}, func() <-chan struct{} { return done },
			func(context.Context) error { close(drained); return nil },
			closeFunc(func() error {
				select {
				case <-done:
					return nil
				default:
					return errors.New("closed before workers finished")
				}
			}),
		)
	}()
	select {
	case <-drained:
	case <-ctx.Done():
		t.Error("HTTP did not drain")
	}
	close(done)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestCancellationKeepsResourceOpen(t *testing.T) {
	for _, joined := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "joined"}[joined], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			if joined {
				close(done)
			}
			err := shutdownService(ctx, func() {}, func() <-chan struct{} { return done },
				func(context.Context) error { cancel(); return nil },
				closeFunc(func() error { t.Fatal("closed after cancellation"); return nil }),
			)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want context.Canceled", err)
			}
		})
	}
}

func TestCloseFailurePreservesCause(t *testing.T) {
	done := make(chan struct{})
	close(done)
	err := shutdownService(context.Background(), func() {},
		func() <-chan struct{} { return done },
		func(context.Context) error { return nil },
		closeFunc(func() error { return io.ErrClosedPipe }),
	)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("error=%v, want io.ErrClosedPipe", err)
	}
}
