// Package main demonstrates allowlisted access logs for buffered HTTP handlers.
package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"time"
)

const historyRoute = "GET /conversations/{conversationID}/messages"

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		if w.status == 0 {
			w.ResponseWriter.WriteHeader(status)
		}
		return
	}
	if w.status != 0 {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	w.status = status
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

// Unwrap lets ResponseController find capabilities on the original writer.
// It does not preserve optional interfaces through direct type assertions.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func accessLog(logger *slog.Logger, next *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		_, pattern := next.Handler(r)
		route := "unmatched"
		if pattern == historyRoute {
			route = historyRoute
		}
		tracked := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(tracked, r)

		status := tracked.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.InfoContext(r.Context(), "http request completed",
			"method", methodLabel(r.Method),
			"route", route,
			"status", status,
			"duration_ms", float64(time.Since(started))/float64(time.Millisecond),
		)
	})
}

func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

// demoMux is a transport fixture, not an authenticated chat history service.
func demoMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(historyRoute, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, "{\"messages\":[]}\n")
	})
	return mux
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := accessLog(logger, demoMux())
	for _, target := range []string{
		"/conversations/demo-chat/messages?token=synthetic-secret",
		"/unknown/private-id",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
	}
}
