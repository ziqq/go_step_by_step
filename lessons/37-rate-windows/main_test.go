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

type fakeClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = c.value.Add(d)
}

func fixture(t *testing.T, capacity, limit int, window time.Duration) (*windowLimiter, *fakeClock, rateKey) {
	t.Helper()
	clock := &fakeClock{} // A zero timestamp is a valid first window start.
	limiter, err := newWindowLimiter(capacity, limit, window, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return limiter, clock, rateKey{"tenant-1", "user-1", "history.read"}
}

func assertAllowed(t *testing.T, limiter *windowLimiter, key rateKey) {
	t.Helper()
	decision, err := limiter.Allow(context.Background(), key)
	if err != nil || !decision.Allowed || decision.RetryAfter != 0 {
		t.Fatalf("Allow(%+v) = %+v, %v; want allowed without delay", key, decision, err)
	}
}

func assertLimited(t *testing.T, limiter *windowLimiter, key rateKey, delay time.Duration) {
	t.Helper()
	decision, err := limiter.Allow(context.Background(), key)
	if err != nil || decision.Allowed || decision.RetryAfter != delay {
		t.Fatalf("Allow(%+v) = %+v, %v; want quota denial, delay=%s", key, decision, err, delay)
	}
}

func assertCapacity(t *testing.T, limiter *windowLimiter, key rateKey) {
	t.Helper()
	decision, err := limiter.Allow(context.Background(), key)
	if !errors.Is(err, ErrCapacity) || decision != (rateDecision{}) {
		t.Fatalf("Allow(%+v) = %+v, %v; want zero decision and ErrCapacity", key, decision, err)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		capacity, limit int
		window          time.Duration
		now             func() time.Time
	}{
		{0, 1, time.Second, time.Now}, {-1, 1, time.Second, time.Now},
		{1, 0, time.Second, time.Now}, {1, -1, time.Second, time.Now},
		{1, 1, 0, time.Now}, {1, 1, -1, time.Now}, {1, 1, time.Second, nil},
	} {
		limiter, err := newWindowLimiter(tc.capacity, tc.limit, tc.window, tc.now)
		if limiter != nil || !errors.Is(err, ErrInvalidLimiter) {
			t.Fatalf("constructor = %v, %v", limiter, err)
		}
	}
}

func TestInvalidAndCanceledCallsDoNotReadClockOrAllocateKeys(t *testing.T) {
	clockCalls := 0
	limiter, err := newWindowLimiter(1, 1, time.Second, func() time.Time { clockCalls++; return time.Time{} })
	if err != nil {
		t.Fatal(err)
	}
	valid := rateKey{"tenant", "user", "history.read"}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, key := range []rateKey{{}, {"", "user", "op"}, {"tenant", "", "op"}, {"tenant", "user", ""}, {" ", "user", "op"}, {"tenant", "\t", "op"}, {"tenant", "user", "\n"}} {
		for _, ctx := range []context.Context{context.Background(), canceled} {
			decision, err := limiter.Allow(ctx, key)
			if decision != (rateDecision{}) || !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("invalid key = %+v, %v", decision, err)
			}
		}
	}
	if decision, err := limiter.Allow(nil, valid); decision != (rateDecision{}) || !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil context = %+v, %v", decision, err)
	}
	if decision, err := limiter.Allow(canceled, valid); decision != (rateDecision{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %+v, %v", decision, err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if decision, err := limiter.Allow(expired, valid); decision != (rateDecision{}) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired = %+v, %v", decision, err)
	}
	if clockCalls != 0 {
		t.Fatalf("invalid/canceled calls read clock %d times", clockCalls)
	}
	assertAllowed(t, limiter, valid)
	assertLimited(t, limiter, valid, time.Second)
}

// The first Err call proves validation completed before waiting for the mutex.
type checkedContext struct {
	context.Context
	once    sync.Once
	checked chan struct{}
}

func (c *checkedContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case result := <-channel:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not complete")
		var zero T
		return zero
	}
}

func TestCancellationWhileWaitingForMutexDoesNotChargeQuota(t *testing.T) {
	limiter, _, key := fixture(t, 1, 1, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	observed := &checkedContext{Context: ctx, checked: make(chan struct{})}
	result := make(chan error, 1)
	finished := make(chan struct{})
	limiter.mu.Lock()
	var once sync.Once
	release := func() { once.Do(limiter.mu.Unlock) }
	t.Cleanup(func() { cancel(); release(); receive(t, finished) })
	go func() {
		defer close(finished)
		decision, err := limiter.Allow(observed, key)
		if decision != (rateDecision{}) {
			t.Errorf("canceled decision = %+v", decision)
		}
		result <- err
	}()
	receive(t, observed.checked)
	cancel()
	release()
	if err := receive(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting caller = %v", err)
	}
	assertAllowed(t, limiter, key)
}

func TestQuotaWindowBoundaryAndDenialsDoNotExtendIt(t *testing.T) {
	limiter, clock, key := fixture(t, 1, 2, time.Second)
	assertAllowed(t, limiter, key)
	assertAllowed(t, limiter, key)
	assertLimited(t, limiter, key, time.Second)
	clock.Advance(250 * time.Millisecond)
	assertLimited(t, limiter, key, 750*time.Millisecond)
	clock.Advance(750*time.Millisecond - time.Nanosecond)
	assertLimited(t, limiter, key, time.Nanosecond)
	clock.Advance(time.Nanosecond)
	assertAllowed(t, limiter, key) // Reset an existing key even at full capacity.
	assertAllowed(t, limiter, key)
	assertLimited(t, limiter, key, time.Second)
}

func TestAdmittedCallsDoNotSlideTheWindow(t *testing.T) {
	limiter, clock, key := fixture(t, 1, 2, time.Second)
	assertAllowed(t, limiter, key)
	clock.Advance(900 * time.Millisecond)
	assertAllowed(t, limiter, key)
	assertLimited(t, limiter, key, 100*time.Millisecond)
	clock.Advance(100 * time.Millisecond)
	assertAllowed(t, limiter, key)
}

func TestCapacityDenialPreservesExistingQuotaAndExpiry(t *testing.T) {
	limiter, clock, key := fixture(t, 1, 2, time.Second)
	other := key
	other.UserID = "other"
	assertAllowed(t, limiter, key)
	clock.Advance(200 * time.Millisecond)
	assertCapacity(t, limiter, other)
	assertAllowed(t, limiter, key)
	assertLimited(t, limiter, key, 800*time.Millisecond)
	clock.Advance(800 * time.Millisecond)
	assertAllowed(t, limiter, other) // Reclaim without reading the expired key.
	assertAllowed(t, limiter, other)
	assertLimited(t, limiter, other, time.Second)
	assertCapacity(t, limiter, key)
}

func TestCleanupKeepsLiveKeysAndTheirOriginalWindows(t *testing.T) {
	limiter, clock, a := fixture(t, 2, 1, time.Second)
	b, c := a, a
	b.UserID, c.UserID = "b", "c"
	assertAllowed(t, limiter, a)
	clock.Advance(500 * time.Millisecond)
	assertAllowed(t, limiter, b)
	clock.Advance(500 * time.Millisecond)
	assertAllowed(t, limiter, c)
	assertLimited(t, limiter, b, 500*time.Millisecond)
	assertLimited(t, limiter, c, time.Second)
	assertCapacity(t, limiter, a)
}

func TestExactTenantUserAndOperationKeysRemainIsolated(t *testing.T) {
	limiter, _, key := fixture(t, 8, 1, time.Minute)
	variants := []rateKey{key, {"other", key.UserID, key.Operation}, {key.TenantID, "other", key.Operation}, {key.TenantID, key.UserID, "messages.send"}, {" tenant-1", key.UserID, key.Operation}, {key.TenantID, "user-1 ", key.Operation}, {key.TenantID, key.UserID, "history.read "}}
	for _, k := range variants {
		assertAllowed(t, limiter, k)
	}
	for _, k := range variants {
		assertLimited(t, limiter, k, time.Minute)
	}
	// Struct keys do not collide when fields contain a delimiter.
	separate, _, _ := fixture(t, 2, 1, time.Second)
	assertAllowed(t, separate, rateKey{"a\x00b", "c", "op"})
	assertAllowed(t, separate, rateKey{"a", "b\x00c", "op"})
}

func TestLongWindowDelayDoesNotOverflow(t *testing.T) {
	const longest = time.Duration(1<<63 - 1)
	limiter, clock, key := fixture(t, 1, 1, longest)
	assertAllowed(t, limiter, key)
	assertLimited(t, limiter, key, longest)
	clock.Advance(longest - 1)
	assertLimited(t, limiter, key, time.Nanosecond)
	clock.Advance(time.Nanosecond)
	assertAllowed(t, limiter, key)
}

func TestConcurrentSameKeyCannotExceedQuota(t *testing.T) {
	limiter, _, key := fixture(t, 1, 7, time.Second)
	start := make(chan struct{})
	outcomes := make(chan rateDecision, 64)
	var callers sync.WaitGroup
	for range 64 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			decision, err := limiter.Allow(context.Background(), key)
			if err != nil {
				t.Errorf("Allow = %v", err)
			}
			outcomes <- decision
		}()
	}
	close(start)
	callers.Wait()
	close(outcomes)
	admitted := 0
	for d := range outcomes {
		if d.Allowed {
			admitted++
			if d.RetryAfter != 0 {
				t.Error("allowed call has delay")
			}
		} else if d.RetryAfter != time.Second {
			t.Errorf("quota delay = %s", d.RetryAfter)
		}
	}
	if admitted != 7 {
		t.Fatalf("admitted %d; want 7", admitted)
	}
}

func TestConcurrentDistinctKeysCannotExceedCapacity(t *testing.T) {
	limiter, _, base := fixture(t, 3, 1, time.Second)
	start := make(chan struct{})
	accepted := make(chan rateKey, 24)
	var callers sync.WaitGroup
	for i := range 24 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			key := base
			key.UserID = fmt.Sprint(i)
			d, err := limiter.Allow(context.Background(), key)
			if err == nil && d.Allowed && d.RetryAfter == 0 {
				accepted <- key
				return
			}
			if !errors.Is(err, ErrCapacity) || d != (rateDecision{}) {
				t.Errorf("unexpected result %+v, %v", d, err)
			}
		}()
	}
	close(start)
	callers.Wait()
	close(accepted)
	count := 0
	for key := range accepted {
		count++
		assertLimited(t, limiter, key, time.Second)
	}
	if count != 3 {
		t.Fatalf("admitted keys=%d; want 3", count)
	}
}

func TestIndependentInstancesHaveIndependentBudgets(t *testing.T) {
	a, _, key := fixture(t, 1, 1, time.Second)
	b, _, _ := fixture(t, 1, 1, time.Second)
	assertAllowed(t, a, key)
	assertAllowed(t, b, key)
	assertLimited(t, a, key, time.Second)
	assertLimited(t, b, key, time.Second)
}

func TestDemo(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil {
		t.Fatal(err)
	}
	const want = "attempt=1 allowed=true retry_after=0s\nattempt=2 allowed=true retry_after=0s\nattempt=3 allowed=false retry_after=1s\nattempt=4 allowed=true retry_after=0s\n"
	if output.String() != want {
		t.Fatalf("output=%q; want %q", output.String(), want)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestDemoPreservesWriterError(t *testing.T) {
	failure := errors.New("writer unavailable")
	if err := run(failingWriter{failure}); !errors.Is(err, failure) {
		t.Fatalf("run = %v", err)
	}
}
