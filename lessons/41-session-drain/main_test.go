package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func acquireSession(t *testing.T, registry *sessions) func() {
	t.Helper()
	release, err := registry.acquire()
	if err != nil || release == nil {
		t.Fatalf("acquire returned a nil release or error: %v", err)
	}
	t.Cleanup(release)
	return release
}

func assertRejected(t *testing.T, registry *sessions, want error) {
	t.Helper()
	release, err := registry.acquire()
	if release != nil || !errors.Is(err, want) {
		t.Fatalf("acquire: release nil = %v, error = %v, want %v", release == nil, err, want)
	}
}

func assertOpen(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal("signal closed too early")
	default:
	}
}

func assertClosed(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	default:
		t.Fatal("signal is still open")
	}
}

func TestSessionLimitAndSlotReuse(t *testing.T) {
	registry := newSessions(1)
	release := acquireSession(t, registry)
	assertRejected(t, registry, errSessionLimit)

	release()
	acquireSession(t, registry)
	release() // A stale release cannot free a different owner's slot.
	assertRejected(t, registry, errSessionLimit)
	assertOpen(t, registry.stopping)
}

func TestDrainWaitsForLastSession(t *testing.T) {
	registry := newSessions(2)
	first := acquireSession(t, registry)
	last := acquireSession(t, registry)

	done := registry.beginDrain()
	if done != registry.beginDrain() {
		t.Fatal("repeated drain returned a different signal")
	}
	assertClosed(t, registry.stopping)
	assertOpen(t, done)
	assertRejected(t, registry, errDraining)

	var duplicates sync.WaitGroup
	for range 8 {
		duplicates.Add(1)
		go func() {
			defer duplicates.Done()
			first()
		}()
	}
	duplicates.Wait()
	assertOpen(t, done)
	assertRejected(t, registry, errDraining)

	last()
	assertClosed(t, done)
}

func TestDrainEmptyRegistry(t *testing.T) {
	registry := newSessions(1)
	assertClosed(t, registry.beginDrain())
	assertClosed(t, registry.stopping)
	assertRejected(t, registry, errDraining)
}

func TestAcquireRacingWithDrain(t *testing.T) {
	registry := newSessions(1)
	start := make(chan struct{})
	drained := make(chan (<-chan struct{}), 1)
	var workers sync.WaitGroup
	defer workers.Wait()

	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		drained <- registry.beginDrain()
	}()

	close(start)
	release, err := registry.acquire()
	if release != nil {
		defer release()
	}
	select {
	case done := <-drained:
		if err == nil {
			if release == nil {
				t.Fatal("accepted session has no release")
			}
			assertOpen(t, done)
			release()
		} else if release != nil || !errors.Is(err, errDraining) {
			t.Fatalf("unexpected admission result: release nil = %v, error = %v", release == nil, err)
		}
		assertClosed(t, done)
	case <-time.After(time.Second):
		t.Fatal("beginDrain blocked")
	}
}
