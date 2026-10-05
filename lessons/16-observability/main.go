// Package main demonstrates structured request logs, bounded HTTP metrics, and local Go profiling.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type metricLabel struct {
	route       string
	method      string
	statusClass string
}

type requestMetrics struct {
	mu            sync.RWMutex
	requests      map[metricLabel]uint64
	durationNanos atomic.Uint64
}

func newRequestMetrics() *requestMetrics {
	return &requestMetrics{requests: make(map[metricLabel]uint64)}
}

func (m *requestMetrics) observe(label metricLabel, duration time.Duration) {
	m.mu.Lock()
	m.requests[label]++
	m.mu.Unlock()
	if duration > 0 {
		m.durationNanos.Add(uint64(duration))
	}
}

func (m *requestMetrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.RLock()
	snapshot := make(map[metricLabel]uint64, len(m.requests))
	var total uint64
	for label, count := range m.requests {
		snapshot[label] = count
		total += count
	}
	m.mu.RUnlock()

	labels := make([]metricLabel, 0, len(snapshot))
	for label := range snapshot {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		left, right := labels[i], labels[j]
		if left.route != right.route {
			return left.route < right.route
		}
		if left.method != right.method {
			return left.method < right.method
		}
		return left.statusClass < right.statusClass
	})

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintln(w, "# HELP go_course_http_requests_total Number of completed HTTP requests.")
	_, _ = fmt.Fprintln(w, "# TYPE go_course_http_requests_total counter")
	for _, label := range labels {
		_, _ = fmt.Fprintf(
			w,
			"go_course_http_requests_total{route=%q,method=%q,status_class=%q} %d\n",
			label.route,
			label.method,
			label.statusClass,
			snapshot[label],
		)
	}
	_, _ = fmt.Fprintln(w, "# HELP go_course_http_request_duration_seconds Duration of completed HTTP requests.")
	_, _ = fmt.Fprintln(w, "# TYPE go_course_http_request_duration_seconds summary")
	_, _ = fmt.Fprintf(w, "go_course_http_request_duration_seconds_count %d\n", total)
	_, _ = fmt.Fprintf(
		w,
		"go_course_http_request_duration_seconds_sum %.9f\n",
		float64(m.durationNanos.Load())/float64(time.Second),
	)
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func newRequestID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate request ID: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}

func instrument(
	logger *slog.Logger,
	metrics *requestMetrics,
	generateID func() (string, error),
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID, err := generateID()
		if err != nil {
			metrics.observe(metricLabel{
				route:       routeLabel(r.URL.Path),
				method:      methodLabel(r.Method),
				statusClass: "5xx",
			}, time.Since(started))
			logger.ErrorContext(r.Context(), "request instrumentation failed", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("X-Request-ID", requestID)
		requestLogger := logger.With("request_id", requestID)
		tracked := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(tracked, r)

		duration := time.Since(started)
		route := routeLabel(r.URL.Path)
		statusClass := fmt.Sprintf("%dxx", tracked.status/100)
		metrics.observe(metricLabel{
			route:       route,
			method:      methodLabel(r.Method),
			statusClass: statusClass,
		}, duration)
		requestLogger.InfoContext(
			r.Context(),
			"http request completed",
			"method", methodLabel(r.Method),
			"route", route,
			"status", tracked.status,
			"duration_ms", float64(duration)/float64(time.Millisecond),
		)
	})
}

func routeLabel(path string) string {
	switch path {
	case "/":
		return "home"
	case "/reports":
		return "reports"
	default:
		return "other"
	}
}

func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost:
		return method
	default:
		return "OTHER"
	}
}

func newPublicHandler(logger *slog.Logger, metrics *requestMetrics) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Go service is running\n")
	})
	mux.HandleFunc("GET /reports", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, "{\"reports\":[]}\n")
	})
	return instrument(logger, metrics, newRequestID, mux)
}

func newDebugHandler(metrics *requestMetrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	for _, profile := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle("GET /debug/pprof/"+profile, pprof.Handler(profile))
	}
	return mux
}

func run(ctx context.Context) error {
	publicListener, err := net.Listen("tcp", ":8080")
	if err != nil {
		return fmt.Errorf("listen for public HTTP on :8080: %w", err)
	}
	debugListener, err := net.Listen("tcp", "127.0.0.1:6060")
	if err != nil {
		_ = publicListener.Close()
		return fmt.Errorf("listen for local diagnostics on 127.0.0.1:6060: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	metrics := newRequestMetrics()
	servers := []struct {
		name     string
		server   *http.Server
		listener net.Listener
	}{
		{
			name:     "public",
			server:   &http.Server{Handler: newPublicHandler(logger, metrics), ReadHeaderTimeout: 5 * time.Second},
			listener: publicListener,
		},
		{
			name:     "diagnostics",
			server:   &http.Server{Handler: newDebugHandler(metrics), ReadHeaderTimeout: 5 * time.Second},
			listener: debugListener,
		},
	}
	serveErrors := make(chan error, len(servers))
	for _, item := range servers {
		go func(item struct {
			name     string
			server   *http.Server
			listener net.Listener
		}) {
			if err := item.server.Serve(item.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErrors <- fmt.Errorf("%s server: %w", item.name, err)
			}
		}(item)
	}

	var runErr error
	select {
	case runErr = <-serveErrors:
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, item := range servers {
		if err := item.server.Shutdown(shutdownCtx); err != nil {
			_ = item.server.Close()
			runErr = errors.Join(runErr, fmt.Errorf("shut down %s server: %w", item.name, err))
		}
	}
	return runErr
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("service stopped with an error", "error", err)
		os.Exit(1)
	}
}
