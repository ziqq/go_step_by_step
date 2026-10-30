package main

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmptyGroupAndInvalidTask(t *testing.T) {
	group := newTaskGroup(context.Background())
	if err := group.Start(nil); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("nil task = %v", err)
	}
	done := group.Stop()
	awaitSignal(t, done)
	if group.Stop() != done {
		t.Fatal("Stop returned a different join signal")
	}
}

func TestStopCancelsButJoinsOnlyAfterCleanup(t *testing.T) {
	group := newTaskGroup(context.Background())
	started, observed, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer func() { releaseOnce.Do(func() { close(release) }); awaitSignal(t, group.Stop()) }()
	cleaned := false
	if err := group.Start(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(observed)
		<-release
		cleaned = true
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, started)
	done := group.Stop()
	awaitSignal(t, observed)
	assertOpen(t, done)
	if err := group.Start(func(context.Context) { t.Error("rejected task ran") }); !errors.Is(err, ErrStopping) {
		t.Fatalf("post-stop start = %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	awaitSignal(t, done)
	if !cleaned {
		t.Fatal("join completed before callback cleanup")
	}
}

func TestJoinWaitsForEveryAcceptedTask(t *testing.T) {
	group := newTaskGroup(context.Background())
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	returned := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var cleanupOnce [2]sync.Once
	defer func() {
		for i := range release {
			cleanupOnce[i].Do(func() { close(release[i]) })
		}
		awaitSignal(t, group.Stop())
	}()
	for i := range release {
		if err := group.Start(func(context.Context) { <-release[i]; close(returned[i]) }); err != nil {
			t.Fatal(err)
		}
	}
	done := group.Stop()
	cleanupOnce[0].Do(func() { close(release[0]) })
	awaitSignal(t, returned[0])
	assertOpen(t, done)
	cleanupOnce[1].Do(func() { close(release[1]) })
	awaitSignal(t, done)
	awaitSignal(t, returned[1])
}

func TestParentCancellationPropagatesAndRejectsNewWork(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), requestKey{}, "worker-33"))
	defer cancel()
	group := newTaskGroup(parent)
	defer func() { awaitSignal(t, group.Stop()) }()
	started, finished := make(chan struct{}), make(chan struct{})
	if err := group.Start(func(ctx context.Context) {
		if ctx.Value(requestKey{}) != "worker-33" {
			t.Error("parent value was lost")
		}
		close(started)
		<-ctx.Done()
		close(finished)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, started)
	cancel()
	awaitSignal(t, finished)
	if err := group.Start(func(context.Context) { t.Error("canceled-parent task ran") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("start after parent cancellation = %v", err)
	}
}

type requestKey struct{}

func TestStopReleasesChildWithoutCancelingParent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	group := newTaskGroup(parent)
	var child context.Context
	if err := group.Start(func(ctx context.Context) { child = ctx }); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, group.Stop())
	if parent.Err() != nil || !errors.Is(child.Err(), context.Canceled) {
		t.Fatal("Stop canceled parent or failed to release child context")
	}
}

func TestExpiredParentStartsNoCallback(t *testing.T) {
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	group := newTaskGroup(parent)
	defer func() { awaitSignal(t, group.Stop()) }()
	if err := group.Start(func(context.Context) { t.Error("expired-parent task ran") }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("start with expired parent = %v", err)
	}
}

func TestConcurrentStopSharesOneSignalAndDoesNotWaitInline(t *testing.T) {
	group := newTaskGroup(context.Background())
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer func() { releaseOnce.Do(func() { close(release) }); awaitSignal(t, group.Stop()) }()
	if err := group.Start(func(context.Context) { <-release }); err != nil {
		t.Fatal(err)
	}
	const callers = 32
	signals := make(chan (<-chan struct{}), callers)
	for i := 0; i < callers; i++ {
		go func() { signals <- group.Stop() }()
	}
	done := group.Stop()
	for i := 0; i < callers; i++ {
		select {
		case got := <-signals:
			if got != done {
				t.Fatal("concurrent Stop returned another signal")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Stop waited inline for callback completion")
		}
	}
	assertOpen(t, done)
	releaseOnce.Do(func() { close(release) })
	awaitSignal(t, done)
}

func TestConcurrentAdmissionAndStopAccountForAllAcceptedTasks(t *testing.T) {
	for round := 0; round < 12; round++ {
		group := newTaskGroup(context.Background())
		gate := make(chan struct{})
		var admitted, ran atomic.Int32
		var actors sync.WaitGroup
		for i := 0; i < 16; i++ {
			actors.Add(1)
			go func() {
				defer actors.Done()
				<-gate
				err := group.Start(func(ctx context.Context) { ran.Add(1); <-ctx.Done() })
				if err == nil {
					admitted.Add(1)
				} else if !errors.Is(err, ErrStopping) {
					t.Errorf("unexpected admission error: %v", err)
				}
			}()
		}
		actors.Add(1)
		go func() { defer actors.Done(); <-gate; group.Stop() }()
		close(gate)
		actorsDone := make(chan struct{})
		go func() { actors.Wait(); close(actorsDone) }()
		awaitSignal(t, actorsDone)
		awaitSignal(t, group.Stop())
		if ran.Load() != admitted.Load() {
			t.Fatalf("round %d: ran=%d admitted=%d", round, ran.Load(), admitted.Load())
		}
		if err := group.Start(func(context.Context) { t.Error("post-join task ran") }); !errors.Is(err, ErrStopping) {
			t.Fatal(err)
		}
	}
}

func TestCallbackCanRequestStopWithoutJoiningItself(t *testing.T) {
	group := newTaskGroup(context.Background())
	requested := make(chan (<-chan struct{}), 1)
	if err := group.Start(func(context.Context) { requested <- group.Stop() }); err != nil {
		t.Fatal(err)
	}
	select {
	case done := <-requested:
		awaitSignal(t, done)
	case <-time.After(5 * time.Second):
		t.Fatal("callback could not request Stop")
	}
}

func TestDemo(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil || output.String() != "worker joined; new task rejected\n" {
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

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle barrier did not complete")
	}
}

func assertOpen(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal("join signal closed while a task was still held")
	default:
	}
}
