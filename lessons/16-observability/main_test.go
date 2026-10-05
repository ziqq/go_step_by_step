package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPublicHandlerLogsGeneratedRequestIDAndBoundedRoute(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	metrics := newRequestMetrics()
	handler := newPublicHandler(logger, metrics)

	request := httptest.NewRequest(http.MethodGet, "/reports?token=private-value", nil)
	request.Header.Set("X-Request-ID", "untrusted-client-value")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	requestID := response.Header().Get("X-Request-ID")
	if len(requestID) != 32 || requestID == "untrusted-client-value" {
		t.Fatalf("X-Request-ID = %q, want a generated 32-character ID", requestID)
	}

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatalf("decode request log: %v", err)
	}
	if record["request_id"] != requestID || record["route"] != "reports" || record["status"] != float64(http.StatusOK) {
		t.Fatalf("request log = %#v, missing expected request attributes", record)
	}
	if strings.Contains(logs.String(), "private-value") || strings.Contains(logs.String(), "untrusted-client-value") {
		t.Fatalf("request log contains query data or an untrusted ID: %s", logs.String())
	}

	metricsResponse := httptest.NewRecorder()
	metrics.ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	if !strings.Contains(body, `route="reports",method="GET",status_class="2xx"`) ||
		strings.Contains(body, "private-value") {
		t.Fatalf("metrics exposition = %s", body)
	}
}

func TestPublicHandlerUsesFiniteLabelsForUnknownPathsAndMethods(t *testing.T) {
	metrics := newRequestMetrics()
	handler := newPublicHandler(quietLogger(), metrics)

	request := httptest.NewRequest("CUSTOM-METHOD", "/tenants/tenant-secret", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}

	metricsResponse := httptest.NewRecorder()
	metrics.ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	if !strings.Contains(body, `route="other",method="OTHER",status_class="4xx"`) {
		t.Fatalf("metrics do not contain bounded labels: %s", body)
	}
	if strings.Contains(body, "tenant-secret") || strings.Contains(body, "CUSTOM-METHOD") {
		t.Fatalf("metrics contain an unbounded label: %s", body)
	}
}

func TestInstrumentRecordsIDGenerationFailure(t *testing.T) {
	metrics := newRequestMetrics()
	generateID := func() (string, error) {
		return "", errors.New("random source unavailable")
	}
	called := false
	handler := instrument(quietLogger(), metrics, generateID, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if called {
		t.Fatal("next handler ran after request ID generation failed")
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(response.Body.String(), "internal server error") {
		t.Fatalf("response body = %q, want a stable error", response.Body.String())
	}
}

func TestStatusWriterKeepsFirstStatusAndDefaultsWritesToOK(t *testing.T) {
	response := httptest.NewRecorder()
	tracked := &statusWriter{ResponseWriter: response, status: http.StatusOK}
	_, _ = tracked.Write([]byte("body"))
	tracked.WriteHeader(http.StatusInternalServerError)
	if tracked.status != http.StatusOK || response.Code != http.StatusOK {
		t.Fatalf("recorded/written status = %d/%d, want %d", tracked.status, response.Code, http.StatusOK)
	}
	if tracked.Unwrap() != response {
		t.Fatal("Unwrap() did not return the original ResponseWriter")
	}
}

func TestDebugHandlerIsSeparateFromPublicHandler(t *testing.T) {
	metrics := newRequestMetrics()
	public := newPublicHandler(quietLogger(), metrics)
	debug := newDebugHandler(metrics)

	publicResponse := httptest.NewRecorder()
	public.ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if publicResponse.Code != http.StatusNotFound {
		t.Fatalf("public pprof status = %d, want %d", publicResponse.Code, http.StatusNotFound)
	}

	debugResponse := httptest.NewRecorder()
	debug.ServeHTTP(debugResponse, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if debugResponse.Code != http.StatusOK {
		t.Fatalf("local pprof index status = %d, want %d", debugResponse.Code, http.StatusOK)
	}

	metricsResponse := httptest.NewRecorder()
	debug.ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metricsResponse.Code != http.StatusOK ||
		!strings.Contains(metricsResponse.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics response = %d %q", metricsResponse.Code, metricsResponse.Header().Get("Content-Type"))
	}
}

func TestMetricsAreSafeForConcurrentRequests(t *testing.T) {
	metrics := newRequestMetrics()
	handler := instrument(quietLogger(), metrics, func() (string, error) {
		return "same-test-id", nil
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	const requestCount = 64
	var wait sync.WaitGroup
	for range requestCount {
		wait.Add(1)
		go func() {
			defer wait.Done()
			handler.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/reports", nil),
			)
		}()
	}
	wait.Wait()

	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	want := fmt.Sprintf(`go_course_http_request_duration_seconds_count %d`, requestCount)
	if !strings.Contains(response.Body.String(), want) ||
		!strings.Contains(response.Body.String(), fmt.Sprintf(`status_class="2xx"} %d`, requestCount)) {
		t.Fatalf("metrics after concurrent requests = %s", response.Body.String())
	}
}
