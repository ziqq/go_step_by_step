// Package main demonstrates scoped duplicate-load suppression with owned lifetime.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

var (
	ErrInvalidGroup = errors.New("positive active-key capacity is required")
	ErrInvalidLoad  = errors.New("context, complete key, and loader are required")
	ErrBusy         = errors.New("active-key capacity reached")
	ErrLoadAborted  = errors.New("loader did not return normally")
)

type loadKey struct{ TenantID, ViewerID, ConversationID string }

func (k loadKey) valid() bool {
	return strings.TrimSpace(k.TenantID) != "" && strings.TrimSpace(k.ViewerID) != "" && strings.TrimSpace(k.ConversationID) != ""
}

type loadCall struct {
	done  chan struct{}
	value string
	err   error
}

// loadGroup requires construction and must not be copied.
// Capacity counts active distinct keys, not waiting callers or result bytes.
type loadGroup struct {
	mu       sync.Mutex
	calls    map[loadKey]*loadCall
	capacity int
}

func newLoadGroup(capacity int) (*loadGroup, error) {
	if capacity <= 0 {
		return nil, ErrInvalidGroup
	}
	return &loadGroup{calls: make(map[loadKey]*loadCall), capacity: capacity}, nil
}

// Do runs the first loader synchronously in its caller; duplicates wait for it.
// A follower's canceled context stops only that follower's wait.
// Loader panics propagate; joined callers receive ErrLoadAborted.
// A loader must not reenter the same key in this group.
func (g *loadGroup) Do(ctx context.Context, key loadKey, load func(context.Context) (string, error)) (string, error) {
	if ctx == nil || !key.valid() || load == nil {
		return "", ErrInvalidLoad
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	g.mu.Lock()
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return "", err
	}
	if call, exists := g.calls[key]; exists {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-call.done:
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return call.value, call.err
		}
	}
	if len(g.calls) >= g.capacity {
		g.mu.Unlock()
		return "", ErrBusy
	}
	call := &loadCall{done: make(chan struct{}), err: ErrLoadAborted}
	g.calls[key] = call
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.calls, key)
		close(call.done)
		g.mu.Unlock()
	}()
	call.value, call.err = load(ctx)
	return call.value, call.err
}

func run(output io.Writer) error {
	group, err := newLoadGroup(1)
	if err != nil {
		return err
	}
	key := loadKey{TenantID: "tenant-1", ViewerID: "viewer-1", ConversationID: "chat-1"}
	loads := 0
	load := func(context.Context) (string, error) { loads++; return "hello", nil }
	var values []string
	for range 2 {
		value, err := group.Do(context.Background(), key, load)
		if err != nil {
			return err
		}
		values = append(values, value)
	}
	_, err = fmt.Fprintf(output, "sequential results=%s; loads=%d (completed results are not cached)\n", strings.Join(values, ", "), loads)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
