// Package main demonstrates closing task admission before cancellation and join.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	ErrStopping    = errors.New("task group is stopping")
	ErrInvalidTask = errors.New("task callback is required")
)

// taskGroup must be constructed with newTaskGroup and must not be copied.
type taskGroup struct {
	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

func newTaskGroup(parent context.Context) *taskGroup {
	ctx, cancel := context.WithCancel(parent)
	return &taskGroup{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// Start accounts for an accepted callback before launching its goroutine.
// An accepted callback can begin after Stop and must honor the supplied context.
func (g *taskGroup) Start(task func(context.Context)) error {
	if task == nil {
		return ErrInvalidTask
	}
	g.mu.Lock()
	if g.stopping {
		g.mu.Unlock()
		return ErrStopping
	}
	if err := g.ctx.Err(); err != nil {
		g.mu.Unlock()
		return err
	}
	g.active.Add(1)
	g.mu.Unlock()
	go func() {
		defer g.active.Done()
		task(g.ctx)
	}()
	return nil
}

// Stop closes admission, requests cancellation, and returns a shared join signal.
// Only the first call starts a waiter; it never waits for callbacks inline.
func (g *taskGroup) Stop() <-chan struct{} {
	g.mu.Lock()
	if g.stopping {
		g.mu.Unlock()
		return g.done
	}
	g.stopping = true
	g.mu.Unlock()
	g.cancel()
	go func() {
		g.active.Wait()
		close(g.done)
	}()
	return g.done
}

func run(output io.Writer) error {
	group := newTaskGroup(context.Background())
	defer func() { <-group.Stop() }()
	started := make(chan struct{})
	if err := group.Start(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	}); err != nil {
		return err
	}
	<-started
	<-group.Stop()
	err := group.Start(func(context.Context) {})
	if !errors.Is(err, ErrStopping) {
		return fmt.Errorf("post-stop admission: %v", err)
	}
	_, err = fmt.Fprintln(output, "worker joined; new task rejected")
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
