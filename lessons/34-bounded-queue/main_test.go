package main

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvalidConfiguration(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		queue, err := newJobQueue(capacity, func(string) { t.Error("invalid queue ran") })
		if queue != nil || !errors.Is(err, ErrInvalidQueue) {
			t.Fatalf("capacity %d: queue=%v err=%v", capacity, queue, err)
		}
	}
	if queue, err := newJobQueue(1, nil); queue != nil || !errors.Is(err, ErrInvalidQueue) {
		t.Fatalf("nil handler: queue=%v err=%v", queue, err)
	}
}

func TestEmptyQueueClosesAndReturnsSharedSignal(t *testing.T) {
	queue := queueForTest(t, 1, func(string) { t.Error("empty queue ran a job") })
	done := queue.Close()
	waitSignal(t, done)
	if queue.Close() != done {
		t.Fatal("Close returned a different completion signal")
	}
	if err := queue.TrySubmit("late"); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed submission = %v", err)
	}
}

func TestBlankJobsDoNotConsumeCapacity(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	queue := queueForTest(t, 1, func(job string) {
		if job == "active" {
			close(started)
			<-release
		}
	})
	t.Cleanup(func() { close(release) })
	submit(t, queue, "active")
	waitSignal(t, started)
	for _, job := range []string{"", " ", "\t\n"} {
		if err := queue.TrySubmit(job); !errors.Is(err, ErrInvalidJob) {
			t.Fatalf("blank submission %q = %v", job, err)
		}
	}
	submit(t, queue, "waiting")
	if err := queue.TrySubmit("overflow"); !errors.Is(err, ErrFull) {
		t.Fatalf("overflow = %v", err)
	}
	queue.Close()
	if err := queue.TrySubmit(" "); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("validation precedence after Close = %v", err)
	}
}

func TestBoundedQueueRejectsOverloadAndDrainsInOrder(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var processed []string
	var active atomic.Int32
	queue := queueForTest(t, 2, func(job string) {
		if active.Add(1) != 1 {
			t.Error("callbacks overlapped")
		}
		defer active.Add(-1)
		if job == "active" {
			close(started)
			<-release
		}
		processed = append(processed, job)
	})
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	submit(t, queue, "active")
	waitSignal(t, started)
	submit(t, queue, "waiting-1")
	submit(t, queue, "waiting-2")
	if err := queue.TrySubmit("rejected"); !errors.Is(err, ErrFull) {
		t.Fatalf("overflow = %v", err)
	}
	done := queue.Close()
	assertOpen(t, done)
	if err := queue.TrySubmit("late"); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close submission = %v", err)
	}
	once.Do(func() { close(release) })
	waitSignal(t, done)
	if !slices.Equal(processed, []string{"active", "waiting-1", "waiting-2"}) {
		t.Fatalf("processed = %v", processed)
	}
}

func TestDequeuingFreesOneWaitingSlot(t *testing.T) {
	started := []chan struct{}{make(chan struct{}), make(chan struct{})}
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var once [2]sync.Once
	var processed []string
	queue := queueForTest(t, 1, func(job string) {
		switch job {
		case "first":
			close(started[0])
			<-release[0]
		case "second":
			close(started[1])
			<-release[1]
		}
		processed = append(processed, job)
	})
	t.Cleanup(func() {
		for i := range release {
			once[i].Do(func() { close(release[i]) })
		}
	})
	submit(t, queue, "first")
	waitSignal(t, started[0])
	submit(t, queue, "second")
	if err := queue.TrySubmit("third"); !errors.Is(err, ErrFull) {
		t.Fatalf("third before dequeue = %v", err)
	}
	once[0].Do(func() { close(release[0]) })
	waitSignal(t, started[1])
	submit(t, queue, "third")
	done := queue.Close()
	assertOpen(t, done)
	once[1].Do(func() { close(release[1]) })
	waitSignal(t, done)
	if !slices.Equal(processed, []string{"first", "second", "third"}) {
		t.Fatalf("processed = %v", processed)
	}
}

func TestCompletionFollowsCallbackCleanup(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	cleaned := false
	queue := queueForTest(t, 1, func(string) {
		defer func() { <-release; cleaned = true }()
		close(started)
	})
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	submit(t, queue, "held-cleanup")
	waitSignal(t, started)
	done := queue.Close()
	assertOpen(t, done)
	once.Do(func() { close(release) })
	waitSignal(t, done)
	if !cleaned {
		t.Fatal("completion preceded callback cleanup")
	}
}

func TestValuesAndDuplicateIDsArePreserved(t *testing.T) {
	var processed []string
	queue := queueForTest(t, 4, func(job string) { processed = append(processed, job) })
	want := []string{" spaced ID ", "duplicate", "duplicate", "job-4"}
	for _, job := range want {
		submit(t, queue, job)
	}
	waitSignal(t, queue.Close())
	if !slices.Equal(processed, want) {
		t.Fatalf("processed=%q want=%q", processed, want)
	}
}

func TestConcurrentCloseDoesNotWaitInline(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	queue := queueForTest(t, 1, func(string) { close(started); <-release })
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	submit(t, queue, "held")
	waitSignal(t, started)
	const callers = 24
	signals := make(chan (<-chan struct{}), callers)
	for i := 0; i < callers; i++ {
		go func() { signals <- queue.Close() }()
	}
	done := queue.Close()
	for i := 0; i < callers; i++ {
		select {
		case signal := <-signals:
			if signal != done {
				t.Fatal("Close returned another signal")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Close waited for callback")
		}
	}
	assertOpen(t, done)
	once.Do(func() { close(release) })
	waitSignal(t, done)
}

func TestCallbackCanCloseWithoutJoiningItself(t *testing.T) {
	ready := make(chan struct{})
	signals := make(chan (<-chan struct{}), 1)
	var queue *jobQueue
	queue = queueForTest(t, 1, func(string) { <-ready; signals <- queue.Close() })
	submit(t, queue, "close-from-worker")
	close(ready)
	select {
	case done := <-signals:
		waitSignal(t, done)
	case <-time.After(5 * time.Second):
		t.Fatal("callback could not close queue")
	}
}

func TestConcurrentAdmissionAndCloseAccountForAcceptedJobs(t *testing.T) {
	for round := 0; round < 12; round++ {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			started, release, gate := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			processed := make(map[string]int)
			queue := queueForTest(t, 4, func(job string) {
				if job == "seed" {
					close(started)
					<-release
				}
				processed[job]++
			})
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			submit(t, queue, "seed")
			waitSignal(t, started)
			const producers = 32
			type result struct {
				job string
				err error
			}
			results := make(chan result, producers)
			var actors sync.WaitGroup
			for i := 0; i < producers; i++ {
				actors.Add(1)
				go func() {
					defer actors.Done()
					<-gate
					job := fmt.Sprintf("job-%d", i)
					results <- result{job, queue.TrySubmit(job)}
				}()
			}
			actors.Add(1)
			go func() { defer actors.Done(); <-gate; queue.Close() }()
			close(gate)
			actorsDone := make(chan struct{})
			go func() { actors.Wait(); close(actorsDone) }()
			waitSignal(t, actorsDone)
			accepted := map[string]int{"seed": 1}
			for i := 0; i < producers; i++ {
				got := <-results
				if got.err == nil {
					accepted[got.job] = 1
				} else if !errors.Is(got.err, ErrFull) && !errors.Is(got.err, ErrClosed) {
					t.Errorf("submission %q = %v", got.job, got.err)
				}
			}
			done := queue.Close()
			assertOpen(t, done)
			once.Do(func() { close(release) })
			waitSignal(t, done)
			if len(processed) != len(accepted) {
				t.Fatalf("processed=%v accepted=%v", processed, accepted)
			}
			for job, count := range accepted {
				if processed[job] != count {
					t.Errorf("job %q: processed=%d want=%d", job, processed[job], count)
				}
			}
		})
	}
}

func TestDemo(t *testing.T) {
	var output bytes.Buffer
	err := run(&output)
	if err != nil || output.String() != "processed: job-1, job-2; overload rejected; queue drained\n" {
		t.Fatalf("demo=%q error=%v", output.String(), err)
	}
}

func TestDemoPreservesOutputError(t *testing.T) {
	want := errors.New("fixture output failure")
	if err := run(failingWriter{want}); !errors.Is(err, want) {
		t.Fatalf("output error = %v", err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func queueForTest(t *testing.T, capacity int, handle func(string)) *jobQueue {
	t.Helper()
	queue, err := newJobQueue(capacity, handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitSignal(t, queue.Close()) })
	return queue
}

func submit(t *testing.T, queue *jobQueue, job string) {
	t.Helper()
	if err := queue.TrySubmit(job); err != nil {
		t.Fatalf("submit %q: %v", job, err)
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("queue barrier did not complete")
	}
}

func assertOpen(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal("completion closed while callback was held")
	default:
	}
}
