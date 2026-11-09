package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"
)

type probes struct {
	ping     func(context.Context) error
	timeout  time.Duration
	slot     chan struct{}
	draining atomic.Bool
}

func newProbes(ping func(context.Context) error, timeout time.Duration) *probes {
	return &probes{ping: ping, timeout: timeout, slot: make(chan struct{}, 1)}
}

func (p *probes) beginDrain() {
	p.draining.Store(true)
}

func (p *probes) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		probeStatus(w, http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", p.ready)
	return mux
}

func (p *probes) ready(w http.ResponseWriter, r *http.Request) {
	probeStatus(w, p.readinessStatus(r.Context()))
}

func (p *probes) readinessStatus(parent context.Context) int {
	if p.draining.Load() || parent.Err() != nil {
		return http.StatusServiceUnavailable
	}
	select {
	case p.slot <- struct{}{}:
		defer func() { <-p.slot }()
	default:
		return http.StatusServiceUnavailable
	}

	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	if p.draining.Load() || ctx.Err() != nil {
		return http.StatusServiceUnavailable
	}
	if err := p.ping(ctx); err != nil || ctx.Err() != nil || p.draining.Load() {
		return http.StatusServiceUnavailable
	}
	return http.StatusNoContent
}

func probeStatus(w http.ResponseWriter, status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
}

func main() {
	p := newProbes(func(context.Context) error { return nil }, 250*time.Millisecond)
	handler := p.routes()
	show := func(path string) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		fmt.Printf("%s %d\n", path, response.Code)
	}
	show("/readyz")
	p.beginDrain()
	show("/readyz")
	show("/healthz")
}
