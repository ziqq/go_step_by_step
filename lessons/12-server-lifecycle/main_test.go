package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	values := map[string]string{
		"APP_ADDR":                "127.0.0.1:9090",
		"APP_READ_HEADER_TIMEOUT": "2s",
		"APP_SHUTDOWN_TIMEOUT":    "3s",
	}
	config, err := loadConfig(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.Address != "127.0.0.1:9090" {
		t.Fatalf("Address = %q", config.Address)
	}
	if config.ReadHeaderTimeout != 2*time.Second || config.ShutdownTimeout != 3*time.Second {
		t.Fatalf("timeouts = %#v", config)
	}

	defaults, err := loadConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("loadConfig() defaults error = %v", err)
	}
	if defaults.Address != ":8080" || defaults.ReadHeaderTimeout != 5*time.Second || defaults.ShutdownTimeout != 10*time.Second {
		t.Fatalf("defaults = %#v", defaults)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "address", key: "APP_ADDR", value: "localhost"},
		{name: "port", key: "APP_ADDR", value: ":70000"},
		{name: "read timeout", key: "APP_READ_HEADER_TIMEOUT", value: "soon"},
		{name: "non-positive timeout", key: "APP_SHUTDOWN_TIMEOUT", value: "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(func(key string) (string, bool) {
				if key == tt.key {
					return tt.value, true
				}
				return "", false
			})
			if err == nil {
				t.Fatalf("loadConfig() accepted %s=%q", tt.key, tt.value)
			}
		})
	}
}

func TestHealthEndpointReturnsJSON(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	response := httptest.NewRecorder()
	newHandler(logger).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status body = %q, want ok", body.Status)
	}
}

func TestUnknownRouteReturnsStableJSONError(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	response := httptest.NewRecorder()
	newHandler(logger).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	var body apiError
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "not_found" || body.Message == "" {
		t.Fatalf("error body = %#v", body)
	}
}

func TestHealthEndpointRejectsUnsupportedMethod(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	response := httptest.NewRecorder()
	newHandler(logger).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestRequestLoggerUsesStructuredFields(t *testing.T) {
	var output strings.Builder
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := requestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	for _, field := range []string{`"method":"GET"`, `"path":"/healthz"`, `"msg":"http request completed"`} {
		if !strings.Contains(output.String(), field) {
			t.Errorf("structured log %q does not contain %s", output.String(), field)
		}
	}
}

func TestServeGracefullyWaitsForActiveRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	config := Config{ReadHeaderTimeout: time.Second, ShutdownTimeout: 3 * time.Second}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- serve(ctx, listener, handler, config, logger)
	}()

	clientResult := make(chan *http.Response, 1)
	clientError := make(chan error, 1)
	go func() {
		response, requestErr := (&http.Client{Timeout: 4 * time.Second}).Get("http://" + listener.Addr().String())
		if requestErr != nil {
			clientError <- requestErr
			return
		}
		clientResult <- response
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		close(release)
		t.Fatal("handler did not start")
	}
	cancel()
	close(release)

	select {
	case response := <-clientResult:
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("response status = %d, want %d", response.StatusCode, http.StatusOK)
		}
	case err := <-clientError:
		t.Fatalf("HTTP request: %v", err)
	case <-time.After(4 * time.Second):
		t.Fatal("active request did not finish")
	}

	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("serve() error = %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("server did not shut down")
	}
}
