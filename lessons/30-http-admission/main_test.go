package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimitConfiguration(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, max := range []int{-1, 0} {
		if handler, err := limitConcurrency(max, next); handler != nil || !errors.Is(err, ErrInvalidLimit) {
			t.Fatalf("limitConcurrency(%d) = %v, %v", max, handler, err)
		}
	}
	if handler, err := limitConcurrency(1, nil); handler != nil || !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("nil handler = %v, %v", handler, err)
	}
}

func TestAdmissionPreservesRequestAndWriter(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/work?cursor=opaque", strings.NewReader("payload"))
	recorder := httptest.NewRecorder()
	handler := mustLimit(t, 1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w != recorder || r != request {
			t.Fatal("admission must pass the original writer and request")
		}
		w.Header().Set("X-Work", "done")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created\n"))
	}))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || recorder.Body.String() != "created\n" ||
		recorder.Header().Get("X-Work") != "done" || recorder.Header().Get("Retry-After") != "" {
		t.Fatalf("downstream response changed: %#v, %q", recorder.Result(), recorder.Body.String())
	}
}

func TestFullBudgetRejectsWithoutWaitingAndCanBeReused(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var active atomic.Int32
	var peak atomic.Int32
	handler := mustLimit(t, 2, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
		}
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	first := serveAsync(handler, context.Background())
	second := serveAsync(handler, context.Background())
	awaitSignal(t, entered)
	awaitSignal(t, entered)
	const burst = 20
	responses := make([]<-chan *httptest.ResponseRecorder, burst)
	for i := range responses {
		responses[i] = serveAsync(handler, context.Background())
	}
	for _, response := range responses {
		assertAdmissionError(t, awaitResponse(t, response), "overloaded", "1")
	}
	if active.Load() != 2 || peak.Load() != 2 {
		t.Fatalf("active=%d, peak=%d; rejected requests must not enter", active.Load(), peak.Load())
	}
	unblock()
	for _, response := range []<-chan *httptest.ResponseRecorder{first, second} {
		if got := awaitResponse(t, response).Code; got != http.StatusNoContent {
			t.Fatalf("admitted status = %d", got)
		}
	}
	if got := awaitResponse(t, serveAsync(handler, context.Background())).Code; got != http.StatusNoContent {
		t.Fatalf("slot not reusable: %d", got)
	}
}

func TestAlreadyCanceledRequestDoesNotConsumeCapacity(t *testing.T) {
	var calls int
	handler := mustLimit(t, 1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/work", nil).WithContext(ctx))
	assertAdmissionError(t, recorder, "request_canceled", "")
	if calls != 0 {
		t.Fatal("already-canceled request reached downstream")
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/work", nil))
	if calls != 1 || recorder.Code != http.StatusNoContent {
		t.Fatal("canceled request leaked capacity")
	}
}

func TestCancellationDoesNotReleaseSlotBeforeHandlerReturns(t *testing.T) {
	entered := make(chan struct{})
	observed := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	handler := mustLimit(t, 1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(observed)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := serveAsync(handler, ctx)
	awaitSignal(t, entered)
	cancel()
	awaitSignal(t, observed)
	assertAdmissionError(t, awaitResponse(t, serveAsync(handler, context.Background())), "overloaded", "1")
	assertAdmissionError(t, awaitResponse(t, serveAsync(handler, ctx)), "request_canceled", "")
	unblock()
	if got := awaitResponse(t, first).Code; got != http.StatusNoContent {
		t.Fatalf("handler must own its response after entry: %d", got)
	}
}

func TestPanicPropagatesButReleasesSlot(t *testing.T) {
	var calls int
	handler := mustLimit(t, 1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			panic("fixture panic")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	func() {
		defer func() {
			if got := recover(); got != "fixture panic" {
				t.Fatalf("panic was changed or swallowed: %v", got)
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/work", nil))
	}()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/work", nil))
	if calls != 2 || recorder.Code != http.StatusNoContent {
		t.Fatal("panic leaked a slot")
	}
}

func TestDemoOutput(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "busy: 503, retry-after: 1\nfirst: 204\nafter release: 204\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func mustLimit(t *testing.T, max int, next http.Handler) http.Handler {
	t.Helper()
	handler, err := limitConcurrency(max, next)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func serveAsync(handler http.Handler, ctx context.Context) <-chan *httptest.ResponseRecorder {
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/work", nil).WithContext(ctx))
		response <- recorder
	}()
	return response
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not reach the test barrier")
	}
}

func awaitResponse(t *testing.T, response <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case result := <-response:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("request blocked instead of completing")
		return nil
	}
}

func assertAdmissionError(t *testing.T, recorder *httptest.ResponseRecorder, code, retryAfter string) {
	t.Helper()
	if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != "{\"error\":\""+code+"\"}\n" ||
		recorder.Header().Get("Retry-After") != retryAfter || recorder.Header().Get("Cache-Control") != "no-store" ||
		recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("admission response = status %d, headers %v, body %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}
