package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func request(handler http.Handler, method, path string, ctx context.Context) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(method, path, nil).WithContext(ctx))
	return w
}

func assertProbe(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d body=%q headers=%v; want %d, empty body, no-store", w.Code, w.Body, w.Header(), want)
	}
}

func TestProbeRoutes(t *testing.T) {
	var calls int
	p := newProbes(func(context.Context) error {
		calls++
		return errors.New("private dependency detail")
	}, time.Second)
	h := p.routes()
	ctx := context.Background()
	assertProbe(t, request(h, http.MethodGet, "/healthz", ctx), 204)
	if calls != 0 {
		t.Fatal("liveness touched the dependency")
	}
	assertProbe(t, request(h, http.MethodHead, "/readyz", ctx), 503)
	if got := request(h, http.MethodPost, "/readyz", ctx).Code; got != 405 {
		t.Fatalf("POST status=%d, want 405", got)
	}
	if calls != 1 {
		t.Fatalf("ping calls=%d, want 1", calls)
	}
}

func TestReadinessUsesOneRequestBudget(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want, _ := parent.Deadline()
	p := newProbes(func(ctx context.Context) error {
		if got, ok := ctx.Deadline(); !ok || !got.Equal(want) {
			t.Fatalf("deadline=%v, want parent deadline %v", got, want)
		}
		cancel()
		return nil
	}, time.Minute)
	assertProbe(t, request(p.routes(), http.MethodGet, "/readyz", parent), 503)
}

func TestTimeoutAndSlotReuse(t *testing.T) {
	calls := 0
	p := newProbes(func(ctx context.Context) error {
		calls++
		if calls == 1 {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 50*time.Millisecond {
				t.Fatal("missing probe timeout")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				t.Fatal("probe deadline did not fire")
			}
		}
		return nil
	}, 50*time.Millisecond)
	h := p.routes()
	assertProbe(t, request(h, http.MethodGet, "/readyz", context.Background()), 503)
	// Avoid a scheduling deadline on the success request.
	p.timeout = time.Minute
	assertProbe(t, request(h, http.MethodGet, "/readyz", context.Background()), 204)
}

func TestCanceledRequestSkipsPing(t *testing.T) {
	p := newProbes(func(context.Context) error {
		t.Fatal("canceled request called ping")
		return nil
	}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertProbe(t, request(p.routes(), http.MethodGet, "/readyz", ctx), 503)
}

func TestBusyAndDrainingProbes(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p := newProbes(func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}, 5*time.Second)
	h := p.routes()
	ctx := context.Background()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(h, http.MethodGet, "/readyz", ctx) }()
	joined := false
	defer func() {
		close(release)
		if !joined {
			<-done
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first probe did not start")
	}
	assertProbe(t, request(h, http.MethodGet, "/readyz", ctx), 503)
	p.beginDrain()
	p.beginDrain()
	assertProbe(t, request(h, http.MethodGet, "/healthz", ctx), 204)
	assertProbe(t, request(h, http.MethodGet, "/readyz", ctx), 503)
	if calls.Load() != 1 {
		t.Fatalf("ping calls=%d, want 1", calls.Load())
	}
	// Let the dependency report success after drain and inspect the result.
	select {
	case doneResponse := <-done:
		joined = true
		t.Fatalf("probe returned before release: %d", doneResponse.Code)
	default:
	}
	// Use a send so the deferred close still owns channel cleanup.
	select {
	case release <- struct{}{}:
	case <-time.After(2 * time.Second):
		t.Fatal("probe stopped waiting before release")
	}
	w := <-done
	joined = true
	assertProbe(t, w, 503)
}
