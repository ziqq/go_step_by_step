// Package main demonstrates bounded queue admission and context-aware reads.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidCapacity = errors.New("queue capacity must be positive")
	ErrQueueFull       = errors.New("queue is full")
)

type Job struct {
	ID             string
	IdempotencyKey string
	Payload        string
}

type Queue struct {
	jobs chan Job
}

func NewQueue(capacity int) (*Queue, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Queue{jobs: make(chan Job, capacity)}, nil
}

// TryEnqueue either places a job in the bounded queue or reports overload
// immediately. Callers must decide whether to reject, defer, or retry it.
func (q *Queue) TryEnqueue(job Job) error {
	select {
	case q.jobs <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// Dequeue waits for a job until the context is canceled or its deadline expires.
func (q *Queue) Dequeue(ctx context.Context) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}

	select {
	case job := <-q.jobs:
		return job, nil
	case <-ctx.Done():
		return Job{}, ctx.Err()
	}
}

func main() {
	queue, err := NewQueue(1)
	if err != nil {
		fmt.Println("create queue:", err)
		return
	}

	first := Job{ID: "event-1", IdempotencyKey: "send-42", Payload: "message.created"}
	second := Job{ID: "event-2", IdempotencyKey: "send-43", Payload: "message.created"}
	if err := queue.TryEnqueue(first); err != nil {
		fmt.Println("enqueue first job:", err)
		return
	}
	if err := queue.TryEnqueue(second); errors.Is(err, ErrQueueFull) {
		fmt.Printf("queue full: %s rejected\n", second.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job, err := queue.Dequeue(ctx)
	if err != nil {
		fmt.Println("dequeue:", err)
		return
	}
	fmt.Printf("dequeued: %s (key=%s)\n", job.ID, job.IdempotencyKey)
}
