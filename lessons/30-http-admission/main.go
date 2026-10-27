// Package main demonstrates fail-fast admission for synchronous HTTP handlers.
package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
)

var ErrInvalidLimit = errors.New("a positive concurrency limit and a handler are required")

// limitConcurrency shares one budget across requests to the returned handler.
// A slot belongs to ServeHTTP until it returns or unwinds; there is no queue.
func limitConcurrency(max int, next http.Handler) (http.Handler, error) {
	if max <= 0 || next == nil {
		return nil, ErrInvalidLimit
	}
	slots := make(chan struct{}, max)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			admissionError(w, "request_canceled", false)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			// Cancellation can race with acquisition; check again before work.
			if r.Context().Err() != nil {
				admissionError(w, "request_canceled", false)
				return
			}
			next.ServeHTTP(w, r)
		default:
			admissionError(w, "overloaded", true)
		}
	}), nil
}

func admissionError(w http.ResponseWriter, code string, retry bool) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if retry {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintf(w, "{\"error\":%q}\n", code)
}

func run(output io.Writer) error {
	entered := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	handler, err := limitConcurrency(1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		first.Do(func() {
			close(entered)
			<-release
		})
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		return err
	}
	initial := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(initial, httptest.NewRequest(http.MethodGet, "/work", nil))
	}()
	<-entered
	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, httptest.NewRequest(http.MethodGet, "/work", nil))
	close(release)
	<-done
	after := httptest.NewRecorder()
	handler.ServeHTTP(after, httptest.NewRequest(http.MethodGet, "/work", nil))
	_, err = fmt.Fprintf(output, "busy: %d, retry-after: %s\nfirst: %d\nafter release: %d\n",
		busy.Code, busy.Header().Get("Retry-After"), initial.Code, after.Code)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
