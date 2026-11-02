// Package main demonstrates bounded in-memory admission and draining one worker.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

var (
	ErrInvalidQueue = errors.New("positive capacity and handler are required")
	ErrInvalidJob   = errors.New("job must not be blank")
	ErrFull         = errors.New("queue is full")
	ErrClosed       = errors.New("queue is closed")
)

// jobQueue must be constructed with newJobQueue and must not be copied.
// Capacity counts waiting jobs; one more job may be executing.
type jobQueue struct {
	mu     sync.Mutex
	closed bool
	jobs   chan string
	done   chan struct{}
}

func newJobQueue(capacity int, handle func(string)) (*jobQueue, error) {
	if capacity <= 0 || handle == nil {
		return nil, ErrInvalidQueue
	}
	q := &jobQueue{jobs: make(chan string, capacity), done: make(chan struct{})}
	go func() {
		for job := range q.jobs {
			handle(job)
		}
		close(q.done)
	}()
	return q, nil
}

// TrySubmit either accepts one job or reports rejection without waiting for space.
// It can wait for the admission mutex; nil means acceptance, not delivery.
func (q *jobQueue) TrySubmit(job string) error {
	if strings.TrimSpace(job) == "" {
		return ErrInvalidJob
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	select {
	case q.jobs <- job:
		return nil
	default:
		return ErrFull
	}
}

// Close rejects new work and returns the same signal to every caller.
// The worker drains accepted jobs and closes the signal after all callbacks return.
func (q *jobQueue) Close() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.jobs)
	}
	return q.done
}

func run(output io.Writer) error {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var processed []string
	queue, err := newJobQueue(1, func(job string) {
		if job == "job-1" {
			close(started)
			<-release
		}
		processed = append(processed, job)
	})
	if err != nil {
		return err
	}
	defer func() {
		releaseOnce.Do(func() { close(release) })
		<-queue.Close()
	}()
	if err := queue.TrySubmit("job-1"); err != nil {
		return err
	}
	<-started
	if err := queue.TrySubmit("job-2"); err != nil {
		return err
	}
	if err := queue.TrySubmit("job-3"); !errors.Is(err, ErrFull) {
		return fmt.Errorf("overload rejection: %v", err)
	}
	done := queue.Close()
	if err := queue.TrySubmit("job-4"); !errors.Is(err, ErrClosed) {
		return fmt.Errorf("closed admission: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	<-done
	_, err = fmt.Fprintf(output, "processed: %s; overload rejected; queue drained\n", strings.Join(processed, ", "))
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
