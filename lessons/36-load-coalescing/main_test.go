package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type outcome struct {
	value string
	err   error
}

// The timeout is a deadlock guard; channels establish operation ordering.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not complete")
		var zero T
		return zero
	}
}

func fixture(t *testing.T, capacity int) (*loadGroup, loadKey) {
	t.Helper()
	g, err := newLoadGroup(capacity)
	if err != nil {
		t.Fatal(err)
	}
	return g, loadKey{"tenant-1", "viewer-1", "chat-1"}
}

type heldCall struct {
	release func()
	result  chan outcome
}

func hold(t *testing.T, g *loadGroup, key loadKey, value string, loadErr error) heldCall {
	t.Helper()
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	result := make(chan outcome, 1)
	go func() {
		defer close(finished)
		v, err := g.Do(context.Background(), key, func(context.Context) (string, error) { close(entered); <-release; return value, loadErr })
		result <- outcome{v, err}
	}()
	t.Cleanup(func() { unblock(); receive(t, finished) })
	receive(t, entered)
	return heldCall{unblock, result}
}

// Do evaluates Done only after locating an existing call and releasing its mutex.
// Observing this point proves a follower joined; it does not use scheduler sleeps.
type observedContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func join(t *testing.T, g *loadGroup, ctx context.Context, key loadKey) chan outcome {
	t.Helper()
	observed := &observedContext{Context: ctx, waiting: make(chan struct{})}
	result := make(chan outcome, 1)
	go func() {
		value, err := g.Do(observed, key, func(context.Context) (string, error) { t.Error("follower loader executed"); return "wrong", nil })
		result <- outcome{value, err}
	}()
	receive(t, observed.waiting)
	return result
}

func TestInvalidCapacity(t *testing.T) {
	for _, n := range []int{0, -1} {
		g, err := newLoadGroup(n)
		if g != nil || !errors.Is(err, ErrInvalidGroup) {
			t.Fatalf("constructor(%d) = %v, %v", n, g, err)
		}
	}
}

func TestInvalidArgumentsNeverRunLoader(t *testing.T) {
	g, key := fixture(t, 1)
	called := false
	load := func(context.Context) (string, error) { called = true; return "bad", nil }
	for _, invalid := range []loadKey{{}, {"", "viewer", "chat"}, {"tenant", "", "chat"}, {"tenant", "viewer", ""}, {" \t", "viewer", "chat"}, {"tenant", "\n", "chat"}, {"tenant", "viewer", " "}} {
		if value, err := g.Do(context.Background(), invalid, load); value != "" || !errors.Is(err, ErrInvalidLoad) {
			t.Fatalf("invalid key = %q, %v", value, err)
		}
	}
	if _, err := g.Do(nil, key, load); !errors.Is(err, ErrInvalidLoad) {
		t.Fatal(err)
	}
	if _, err := g.Do(context.Background(), key, nil); !errors.Is(err, ErrInvalidLoad) {
		t.Fatal(err)
	}
	if called {
		t.Fatal("invalid call ran loader")
	}
}

func TestCanceledCallerDoesNotLeadOrJoin(t *testing.T) {
	g, key := fixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	load := func(context.Context) (string, error) { t.Error("canceled caller loaded"); return "", nil }
	if _, err := g.Do(ctx, key, load); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	leader := hold(t, g, key, "ok", nil)
	if _, err := g.Do(ctx, key, load); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := g.Do(ctx, loadKey{}, load); !errors.Is(err, ErrInvalidLoad) {
		t.Fatalf("validation precedence = %v", err)
	}
	leader.release()
	if result := receive(t, leader.result); result.err != nil {
		t.Fatal(result.err)
	}
}

func TestSameKeyFollowersShareOneLoadAtCapacity(t *testing.T) {
	g, key := fixture(t, 1)
	leader := hold(t, g, key, "hello", nil)
	var followers []chan outcome
	for range 12 {
		followers = append(followers, join(t, g, context.Background(), key))
	}
	other := key
	other.ConversationID = "other"
	if _, err := g.Do(context.Background(), other, func(context.Context) (string, error) { t.Error("overload ran loader"); return "", nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("capacity = %v", err)
	}
	leader.release()
	for _, ch := range append(followers, leader.result) {
		result := receive(t, ch)
		if result.value != "hello" || result.err != nil {
			t.Fatalf("result = %+v", result)
		}
	}
}

func TestEmptyValuesAndWrappedErrorsAreShared(t *testing.T) {
	want := errors.New("store unavailable")
	for _, tc := range []struct {
		name, value string
		err         error
	}{
		{"empty success", "", nil}, {"wrapped failure", "partial", fmt.Errorf("load: %w", want)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, key := fixture(t, 1)
			leader := hold(t, g, key, tc.value, tc.err)
			follower := join(t, g, context.Background(), key)
			leader.release()
			for _, ch := range []chan outcome{leader.result, follower} {
				result := receive(t, ch)
				if result.value != tc.value || result.err != tc.err {
					t.Fatalf("result = %+v", result)
				}
				if tc.err != nil && !errors.Is(result.err, want) {
					t.Fatal("lost wrapped error")
				}
			}
		})
	}
}

func TestFollowerCancellationDoesNotCancelLeaderOrFreeKey(t *testing.T) {
	g, key := fixture(t, 1)
	leader := hold(t, g, key, "still running", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := join(t, g, ctx, key)
	cancel()
	if result := receive(t, canceled); result.value != "" || !errors.Is(result.err, context.Canceled) {
		t.Fatalf("canceled follower = %+v", result)
	}
	other := key
	other.ViewerID = "other"
	if _, err := g.Do(context.Background(), other, func(context.Context) (string, error) { return "wrong", nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("slot freed before loader returned: %v", err)
	}
	live := join(t, g, context.Background(), key)
	leader.release()
	for _, ch := range []chan outcome{live, leader.result} {
		result := receive(t, ch)
		if result.value != "still running" || result.err != nil {
			t.Fatalf("result = %+v", result)
		}
	}
}

func TestLeaderOwnsContextAndPublishesCooperativeCancellation(t *testing.T) {
	g, key := fixture(t, 1)
	type marker struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), marker{}, "leader"))
	defer cancel()
	result := make(chan outcome, 1)
	entered, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		value, err := g.Do(ctx, key, func(got context.Context) (string, error) {
			if got != ctx || got.Value(marker{}) != "leader" {
				t.Error("loader context changed")
			}
			close(entered)
			<-got.Done()
			return "", fmt.Errorf("store: %w", got.Err())
		})
		result <- outcome{value, err}
	}()
	t.Cleanup(func() { cancel(); receive(t, finished) })
	receive(t, entered)
	follower := join(t, g, context.Background(), key)
	cancel()
	for _, ch := range []chan outcome{result, follower} {
		r := receive(t, ch)
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("cancellation = %+v", r)
		}
	}
	value, err := g.Do(context.Background(), key, func(context.Context) (string, error) { return "retry", nil })
	if err != nil || value != "retry" {
		t.Fatalf("retry = %q, %v", value, err)
	}
}

func TestDifferentKeysAndFreedSlotsProgressIndependently(t *testing.T) {
	g, key := fixture(t, 2)
	first := hold(t, g, key, "first", nil)
	secondKey := key
	secondKey.ConversationID = "second"
	second := hold(t, g, secondKey, "second", nil)
	second.release()
	if r := receive(t, second.result); r.value != "second" || r.err != nil {
		t.Fatalf("second = %+v", r)
	}
	thirdKey := key
	thirdKey.ConversationID = "third"
	value, err := g.Do(context.Background(), thirdKey, func(context.Context) (string, error) { return "third", nil })
	if value != "third" || err != nil {
		t.Fatalf("third = %q, %v", value, err)
	}
	first.release()
	receive(t, first.result)
}

func TestExactScopeFieldsDoNotJoinAnotherLoad(t *testing.T) {
	g, key := fixture(t, 2)
	leader := hold(t, g, key, "primary", nil)
	for _, distinct := range []loadKey{{"tenant-2", key.ViewerID, key.ConversationID}, {key.TenantID, "viewer-2", key.ConversationID}, {key.TenantID, key.ViewerID, "chat-2"}, {" tenant-1 ", key.ViewerID, key.ConversationID}} {
		result := make(chan outcome, 1)
		go func() {
			value, err := g.Do(context.Background(), distinct, func(context.Context) (string, error) { return "separate", nil })
			result <- outcome{value, err}
		}()
		if r := receive(t, result); r.value != "separate" || r.err != nil {
			t.Fatalf("distinct = %+v", r)
		}
	}
	leader.release()
	receive(t, leader.result)
}

func TestCompletionDoesNotCacheSuccessOrFailure(t *testing.T) {
	g, key := fixture(t, 1)
	want := errors.New("first failed")
	if _, err := g.Do(context.Background(), key, func(context.Context) (string, error) { return "", want }); err != want {
		t.Fatal(err)
	}
	for _, expected := range []string{"second", "third"} {
		value, err := g.Do(context.Background(), key, func(context.Context) (string, error) { return expected, nil })
		if value != expected || err != nil {
			t.Fatalf("next call = %q, %v", value, err)
		}
	}
}

func TestLoaderCanUseDifferentKeyWithoutHoldingGroupMutex(t *testing.T) {
	g, key := fixture(t, 2)
	other := key
	other.ConversationID = "nested"
	result := make(chan outcome, 1)
	go func() {
		value, err := g.Do(context.Background(), key, func(ctx context.Context) (string, error) {
			return g.Do(ctx, other, func(context.Context) (string, error) { return "nested", nil })
		})
		result <- outcome{value, err}
	}()
	if r := receive(t, result); r.value != "nested" || r.err != nil {
		t.Fatalf("nested = %+v", r)
	}
}

func TestConcurrentDistinctKeysRespectCapacity(t *testing.T) {
	const callers, capacity = 24, 3
	g, key := fixture(t, capacity)
	start, release := make(chan struct{}), make(chan struct{})
	entered, results := make(chan struct{}, callers), make(chan outcome, callers)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var wg sync.WaitGroup
	t.Cleanup(func() { unblock(); wg.Wait() })
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			k := key
			k.ConversationID = fmt.Sprint(i)
			value, err := g.Do(context.Background(), k, func(context.Context) (string, error) { entered <- struct{}{}; <-release; return "accepted", nil })
			results <- outcome{value, err}
		}()
	}
	close(start)
	for range callers - capacity {
		if r := receive(t, results); !errors.Is(r.err, ErrBusy) {
			t.Fatalf("expected rejection, got %+v", r)
		}
	}
	for range capacity {
		receive(t, entered)
	}
	unblock()
	for range capacity {
		if r := receive(t, results); r.value != "accepted" || r.err != nil {
			t.Fatalf("accepted = %+v", r)
		}
	}
	wg.Wait()
}

func TestDemo(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want := "sequential results=hello, hello; loads=2 (completed results are not cached)\n"
	if out.String() != want {
		t.Fatalf("output=%q", out.String())
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
func TestDemoWriterError(t *testing.T) {
	want := errors.New("write failed")
	if err := run(failingWriter{want}); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestLoaderPanicPropagatesAndReleasesFollowersAndCapacity(t *testing.T) {
	g, key := fixture(t, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		_, _ = g.Do(context.Background(), key, func(context.Context) (string, error) { close(entered); <-release; panic("loader panic") })
	}()
	t.Cleanup(unblock)
	receive(t, entered)
	follower := join(t, g, context.Background(), key)
	unblock()
	if got := receive(t, panicked); got != "loader panic" {
		t.Fatalf("panic=%v", got)
	}
	if got := receive(t, follower); got.value != "" || !errors.Is(got.err, ErrLoadAborted) {
		t.Fatalf("follower=%+v", got)
	}
	value, err := g.Do(context.Background(), key, func(context.Context) (string, error) { return "after panic", nil })
	if value != "after panic" || err != nil {
		t.Fatalf("after panic=%q,%v", value, err)
	}
}
