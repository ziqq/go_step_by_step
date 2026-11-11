package main

import (
	"errors"
	"fmt"
	"sync"
)

var (
	errSessionLimit = errors.New("session limit reached")
	errDraining     = errors.New("sessions are draining")
)

type sessions struct {
	mu       sync.Mutex
	limit    int
	active   int
	draining bool
	stopping chan struct{}
	done     chan struct{}
}

// newSessions requires a positive capacity.
func newSessions(limit int) *sessions {
	if limit <= 0 {
		panic("session capacity must be positive")
	}
	return &sessions{
		limit:    limit,
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// acquire reserves a slot until the session owner calls the returned function.
func (s *sessions) acquire() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.draining {
		return nil, errDraining
	}
	if s.active == s.limit {
		return nil, errSessionLimit
	}
	s.active++

	var once sync.Once
	return func() {
		once.Do(s.release)
	}, nil
}

func (s *sessions) release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.active--
	if s.draining && s.active == 0 {
		close(s.done)
	}
}

// beginDrain seals admission and asks existing sessions to stop.
func (s *sessions) beginDrain() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.draining {
		s.draining = true
		close(s.stopping)
		if s.active == 0 {
			close(s.done)
		}
	}
	return s.done
}

func main() {
	registry := newSessions(1)
	release, err := registry.acquire()
	if err != nil {
		panic(err)
	}

	go func() {
		defer release()
		<-registry.stopping
		fmt.Println("session stopped")
	}()

	<-registry.beginDrain()
	fmt.Println("all sessions drained")
}
