// Package main demonstrates scoped rate windows with bounded retained state.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidLimiter = errors.New("positive capacity, limit, window, and clock are required")
	ErrInvalidRequest = errors.New("context, tenant, user, and operation are required")
	ErrCapacity       = errors.New("rate-limit key capacity reached")
)

// rateKey uses trusted identity and a server-selected operation, not client headers.
type rateKey struct{ TenantID, UserID, Operation string }

func (k rateKey) valid() bool {
	return strings.TrimSpace(k.TenantID) != "" && strings.TrimSpace(k.UserID) != "" && strings.TrimSpace(k.Operation) != ""
}

type rateDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type rateWindow struct {
	expiresAt time.Time
	used      int
}

// windowLimiter requires construction and must not be copied.
// One immutable policy applies to every key. now must be fast, nondecreasing,
// and must not call back into the limiter; it is called under the mutex.
type windowLimiter struct {
	mu              sync.Mutex
	entries         map[rateKey]rateWindow
	capacity, limit int
	window          time.Duration
	now             func() time.Time
}

func newWindowLimiter(capacity, limit int, window time.Duration, now func() time.Time) (*windowLimiter, error) {
	if capacity <= 0 || limit <= 0 || window <= 0 || now == nil {
		return nil, ErrInvalidLimiter
	}
	return &windowLimiter{entries: make(map[rateKey]rateWindow), capacity: capacity, limit: limit, window: window, now: now}, nil
}

// Allow charges one admitted request even if later work fails or is canceled.
// Quota denial returns a positive delay and nil error. Capacity denial is a
// separate error with no claimed retry delay. Neither waits or extends a window.
func (l *windowLimiter) Allow(ctx context.Context, key rateKey) (rateDecision, error) {
	if ctx == nil || !key.valid() {
		return rateDecision{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return rateDecision{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return rateDecision{}, err
	}

	now := l.now()
	current, exists := l.entries[key]
	if exists && now.Before(current.expiresAt) {
		if current.used >= l.limit {
			return rateDecision{RetryAfter: current.expiresAt.Sub(now)}, nil
		}
		current.used++
		l.entries[key] = current
		return rateDecision{Allowed: true}, nil
	}

	// An expired existing key already owns a slot, including at full capacity.
	if !exists && len(l.entries) >= l.capacity {
		for savedKey, saved := range l.entries {
			if !now.Before(saved.expiresAt) {
				delete(l.entries, savedKey)
			}
		}
		if len(l.entries) >= l.capacity {
			return rateDecision{}, ErrCapacity
		}
	}
	l.entries[key] = rateWindow{expiresAt: now.Add(l.window), used: 1}
	return rateDecision{Allowed: true}, nil
}

func run(output io.Writer) error {
	now := time.Date(2026, time.November, 5, 0, 0, 0, 0, time.UTC)
	limiter, err := newWindowLimiter(2, 2, time.Second, func() time.Time { return now })
	if err != nil {
		return err
	}
	key := rateKey{"tenant-1", "user-1", "history.read"}
	for attempt := 1; attempt <= 4; attempt++ {
		if attempt == 4 {
			now = now.Add(time.Second)
		}
		decision, err := limiter.Allow(context.Background(), key)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "attempt=%d allowed=%t retry_after=%s\n", attempt, decision.Allowed, decision.RetryAfter); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
