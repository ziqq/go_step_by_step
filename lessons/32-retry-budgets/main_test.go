package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func testPolicy() retryPolicy {
	return retryPolicy{maxAttempts: 3, budget: time.Second, initialDelay: 10 * time.Millisecond, maxDelay: 25 * time.Millisecond}
}

func TestInvalidPolicyDoesNoWork(t *testing.T) {
	for _, change := range []func(*retryPolicy){
		func(p *retryPolicy) { p.maxAttempts = 0 },
		func(p *retryPolicy) { p.maxAttempts = -1 },
		func(p *retryPolicy) { p.budget = 0 },
		func(p *retryPolicy) { p.budget = -time.Second },
		func(p *retryPolicy) { p.initialDelay = 0 },
		func(p *retryPolicy) { p.initialDelay = -time.Second },
		func(p *retryPolicy) { p.maxDelay = p.initialDelay - 1 },
	} {
		policy := testPolicy()
		change(&policy)
		err := retryRead(context.Background(), policy,
			func(context.Context) error { t.Fatal("invalid policy started a read"); return nil },
			func(context.Context, time.Duration) error { t.Fatal("invalid policy started a wait"); return nil })
		if !errors.Is(err, ErrInvalidRetryPolicy) {
			t.Fatalf("invalid policy = %v", err)
		}
	}
	read := func(context.Context) error { return nil }
	if !errors.Is(retryRead(context.Background(), testPolicy(), nil, waitContext), ErrInvalidRetryPolicy) ||
		!errors.Is(retryRead(context.Background(), testPolicy(), read, nil), ErrInvalidRetryPolicy) {
		t.Fatal("nil callback was accepted")
	}
}

func TestReadClassificationAndAttemptLimit(t *testing.T) {
	permanent := errors.New("invalid read contract")
	temporary := fmt.Errorf("dependency busy: %w", ErrTemporary)
	for _, tc := range []struct {
		name     string
		results  []error
		attempts int
		want     error
	}{
		{"immediate success", []error{nil}, 3, nil},
		{"temporary then success", []error{temporary, nil}, 3, nil},
		{"exhaustion", []error{temporary, temporary, temporary}, 3, temporary},
		{"one means one total call", []error{temporary}, 1, temporary},
		{"permanent", []error{permanent}, 3, permanent},
		{"permanent after temporary", []error{temporary, permanent}, 3, permanent},
		{"canceled even with temporary marker", []error{errors.Join(ErrTemporary, context.Canceled)}, 3, context.Canceled},
		{"deadline even with temporary marker", []error{errors.Join(ErrTemporary, context.DeadlineExceeded)}, 3, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := testPolicy()
			policy.maxAttempts = tc.attempts
			calls, waits := 0, 0
			err := retryRead(context.Background(), policy, func(context.Context) error {
				if calls >= len(tc.results) {
					t.Fatal("extra read")
				}
				result := tc.results[calls]
				calls++
				return result
			}, func(context.Context, time.Duration) error { waits++; return nil })
			if !errors.Is(err, tc.want) || calls != len(tc.results) || waits != calls-1 {
				t.Fatalf("error=%v calls=%d waits=%d", err, calls, waits)
			}
		})
	}
}

func TestBackoffCapsAndPreservesOneDeadline(t *testing.T) {
	policy := testPolicy()
	policy.maxAttempts = 5
	parent, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "request-32"), 30*time.Second)
	defer cancel()
	var deadline time.Time
	var derived context.Context
	check := func(ctx context.Context) {
		t.Helper()
		got, ok := ctx.Deadline()
		parentDeadline, _ := parent.Deadline()
		if !ok || !got.Before(parentDeadline) || ctx.Value(contextKey{}) != "request-32" {
			t.Fatal("lost bounded deadline or parent value")
		}
		if deadline.IsZero() {
			deadline, derived = got, ctx
		} else if got != deadline {
			t.Fatal("retry reset the overall deadline")
		}
	}
	var delays []time.Duration
	err := retryRead(parent, policy, func(ctx context.Context) error { check(ctx); return ErrTemporary },
		func(ctx context.Context, delay time.Duration) error {
			check(ctx)
			delays = append(delays, delay)
			return nil
		})
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond, 25 * time.Millisecond}
	if !errors.Is(err, ErrTemporary) || !reflect.DeepEqual(delays, want) {
		t.Fatalf("backoff=%v error=%v", delays, err)
	}
	if !errors.Is(derived.Err(), context.Canceled) || parent.Err() != nil {
		t.Fatal("child resources were not canceled or parent was canceled")
	}
}

type contextKey struct{}

func TestEarlierParentDeadlineIsPreserved(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	policy := testPolicy()
	policy.budget = time.Hour
	deadline, _ := parent.Deadline()
	err := retryRead(parent, policy, func(ctx context.Context) error {
		if got, _ := ctx.Deadline(); got != deadline {
			t.Fatal("child extended parent deadline")
		}
		return nil
	}, waitContext)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPreCanceledReadDoesNoWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := retryRead(ctx, testPolicy(), func(context.Context) error { t.Fatal("started canceled read"); return nil },
		func(context.Context, time.Duration) error { t.Fatal("started canceled wait"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCancellationDuringReadOrWait(t *testing.T) {
	for _, duringWait := range []bool{false, true} {
		t.Run(fmt.Sprint(duringWait), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{})
			finished := make(chan error, 1)
			policy := testPolicy()
			policy.budget = 30 * time.Second
			policy.initialDelay, policy.maxDelay = time.Hour, time.Hour
			go func() {
				calls := 0
				finished <- retryRead(ctx, policy, func(ctx context.Context) error {
					calls++
					if calls != 1 {
						return errors.New("unexpected extra call")
					}
					if !duringWait {
						close(entered)
						<-ctx.Done()
						return ctx.Err()
					}
					return ErrTemporary
				}, func(ctx context.Context, delay time.Duration) error { close(entered); return waitContext(ctx, delay) })
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("did not reach cancellation barrier")
			}
			cancel()
			if err := awaitRetry(t, finished); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestBudgetExpiresDuringReadOrWait(t *testing.T) {
	for _, duringWait := range []bool{false, true} {
		t.Run(fmt.Sprint(duringWait), func(t *testing.T) {
			policy := testPolicy()
			policy.budget = 20 * time.Millisecond
			policy.initialDelay, policy.maxDelay = time.Hour, time.Hour
			finished := make(chan error, 1)
			go func() {
				finished <- retryRead(context.Background(), policy, func(ctx context.Context) error {
					if duringWait {
						return ErrTemporary
					}
					<-ctx.Done()
					return ctx.Err()
				}, waitContext)
			}()
			if err := awaitRetry(t, finished); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestObservedCancellationWinsOverCallbackResults(t *testing.T) {
	for _, inWait := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := retryRead(ctx, testPolicy(), func(context.Context) error {
			calls++
			if inWait {
				return ErrTemporary
			}
			cancel()
			return nil
		}, func(context.Context, time.Duration) error { cancel(); return nil })
		cancel()
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("callback cancellation = %v calls=%d", err, calls)
		}
	}
}

func TestWaitFailureStopsAndPreservesCause(t *testing.T) {
	want := errors.New("fixture wait failed")
	calls := 0
	err := retryRead(context.Background(), testPolicy(), func(context.Context) error { calls++; return ErrTemporary },
		func(context.Context, time.Duration) error { return want })
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("wait error=%v calls=%d", err, calls)
	}
}

func TestBackoffDoesNotOverflow(t *testing.T) {
	const maximum = time.Duration(1<<63 - 1)
	for _, tc := range []struct{ delay, cap, want time.Duration }{
		{1, 1, 1}, {2, 5, 4}, {3, 5, 5}, {maximum/2 + 1, maximum, maximum}, {maximum, maximum, maximum},
	} {
		if got := nextDelay(tc.delay, tc.cap); got != tc.want {
			t.Fatalf("nextDelay(%v, %v) = %v", tc.delay, tc.cap, got)
		}
	}
}

func TestWaitContextZeroDelayAndPreCancellation(t *testing.T) {
	if err := waitContext(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestIndependentConcurrentReads(t *testing.T) {
	policy := testPolicy()
	var work sync.WaitGroup
	for i := 0; i < 12; i++ {
		work.Add(1)
		go func() {
			defer work.Done()
			calls := 0
			err := retryRead(context.Background(), policy, func(context.Context) error {
				calls++
				if calls == 1 {
					return ErrTemporary
				}
				return nil
			}, func(context.Context, time.Duration) error { return nil })
			if err != nil || calls != 2 {
				t.Errorf("independent read = %v calls=%d", err, calls)
			}
		}()
	}
	work.Wait()
}

func TestDemo(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil || output.String() != "status read succeeded after 3 attempts\n" {
		t.Fatalf("demo=%q error=%v", output.String(), err)
	}
}

func awaitRetry(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not finish")
		return nil
	}
}
