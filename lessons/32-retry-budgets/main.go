// Package main demonstrates finite retries for an explicitly safe read.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

var (
	ErrTemporary          = errors.New("temporary read failure")
	ErrInvalidRetryPolicy = errors.New("invalid retry policy or callback")
)

type retryPolicy struct {
	maxAttempts  int
	budget       time.Duration
	initialDelay time.Duration
	maxDelay     time.Duration
}

// retryRead requires a repeatable read and callbacks that honor ctx.
// maxAttempts includes the first call; one budget covers calls and waits.
func retryRead(ctx context.Context, policy retryPolicy, read func(context.Context) error, wait func(context.Context, time.Duration) error) error {
	if policy.maxAttempts < 1 || policy.budget <= 0 || policy.initialDelay <= 0 ||
		policy.maxDelay < policy.initialDelay || read == nil || wait == nil {
		return ErrInvalidRetryPolicy
	}
	ctx, cancel := context.WithTimeout(ctx, policy.budget)
	defer cancel()
	delay := policy.initialDelay
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := read(ctx)
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			!errors.Is(err, ErrTemporary) || attempt == policy.maxAttempts {
			return fmt.Errorf("read attempt %d: %w", attempt, err)
		}
		waitErr := wait(ctx, delay)
		if err := ctx.Err(); err != nil {
			return err
		}
		if waitErr != nil {
			return fmt.Errorf("retry wait: %w", waitErr)
		}
		delay = nextDelay(delay, policy.maxDelay)
	}
}

// nextDelay doubles a positive delay up to its cap without integer overflow.
func nextDelay(delay, cap time.Duration) time.Duration {
	if delay >= cap-delay {
		return cap
	}
	return delay * 2
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func run(output io.Writer) error {
	policy := retryPolicy{maxAttempts: 3, budget: time.Second, initialDelay: 10 * time.Millisecond, maxDelay: 40 * time.Millisecond}
	attempts := 0
	err := retryRead(context.Background(), policy, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("synthetic status unavailable: %w", ErrTemporary)
		}
		return nil
	}, waitContext)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "status read succeeded after %d attempts\n", attempts)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
