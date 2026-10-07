package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestNewQueueRequiresPositiveCapacity(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
	}{
		{name: "zero", capacity: 0},
		{name: "negative", capacity: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, err := NewQueue(tt.capacity)
			if queue != nil || !errors.Is(err, ErrInvalidCapacity) {
				t.Fatalf("NewQueue(%d) = (%v, %v), want (nil, ErrInvalidCapacity)", tt.capacity, queue, err)
			}
		})
	}
}

func TestQueuePreservesFIFOJobsAndKeys(t *testing.T) {
	queue, err := NewQueue(2)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}

	want := []Job{
		{ID: "event-1", IdempotencyKey: "key-1", Payload: "one"},
		{ID: "event-2", IdempotencyKey: "key-2", Payload: "two"},
	}
	for _, job := range want {
		if err := queue.TryEnqueue(job); err != nil {
			t.Fatalf("TryEnqueue(%q) error = %v", job.ID, err)
		}
	}

	for _, expected := range want {
		got, err := queue.Dequeue(context.Background())
		if err != nil {
			t.Fatalf("Dequeue() error = %v", err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("Dequeue() = %#v, want %#v", got, expected)
		}
	}
}

func TestTryEnqueueRejectsOverflowWithoutReplacingQueuedJob(t *testing.T) {
	queue, err := NewQueue(1)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	first := Job{ID: "event-1", IdempotencyKey: "key-1"}
	if err := queue.TryEnqueue(first); err != nil {
		t.Fatalf("TryEnqueue(first) error = %v", err)
	}
	if err := queue.TryEnqueue(Job{ID: "event-2"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("TryEnqueue(second) error = %v, want ErrQueueFull", err)
	}

	got, err := queue.Dequeue(context.Background())
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("Dequeue() = (%#v, %v), want (%#v, nil)", got, err, first)
	}
}

func TestDequeueReturnsDeadlineWhenQueueIsEmpty(t *testing.T) {
	queue, err := NewQueue(1)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()

	job, err := queue.Dequeue(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || job != (Job{}) {
		t.Fatalf("Dequeue() = (%#v, %v), want (zero Job, DeadlineExceeded)", job, err)
	}
}

func TestCanceledDequeueLeavesReadyJobInQueue(t *testing.T) {
	queue, err := NewQueue(1)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	want := Job{ID: "event-1", IdempotencyKey: "key-1"}
	if err := queue.TryEnqueue(want); err != nil {
		t.Fatalf("TryEnqueue() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := queue.Dequeue(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Dequeue() error = %v, want context.Canceled", err)
	}
	got, err := queue.Dequeue(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Dequeue() after cancellation = (%#v, %v), want (%#v, nil)", got, err, want)
	}
}
