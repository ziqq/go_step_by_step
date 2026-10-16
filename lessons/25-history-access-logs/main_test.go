package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const historyPattern = "GET /conversations/{conversationID}/messages"

func TestAccessLogOmitsPrivateDataAndPreservesResponse(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	mux := http.NewServeMux()
	mux.HandleFunc(historyPattern, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "private-request-body" {
			t.Fatalf("request body was consumed or changed: %q, %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Demo", "preserved")
		_, _ = io.WriteString(w, "{\"fixture\":\"private-response-body\"}")
	})
	request := httptest.NewRequest(http.MethodGet,
		"/conversations/private-chat-id/messages?token=private-query-token",
		strings.NewReader("private-request-body"),
	)
	request.Header.Set("Authorization", "Bearer private-bearer-token")
	request.Header.Set("Cookie", "session=private-cookie")
	request.Header.Set("X-Request-ID", "private-client-request-id")
	recorder := httptest.NewRecorder()
	accessLog(logger, mux).ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Demo") != "preserved" ||
		response.Header.Get("Content-Type") != "application/json" ||
		recorder.Body.String() != "{\"fixture\":\"private-response-body\"}" {
		t.Fatalf("response was changed: status=%d headers=%v body=%s", response.StatusCode, response.Header, recorder.Body)
	}
	for _, secret := range []string{
		"private-chat-id", "private-query-token", "private-request-body",
		"private-response-body", "private-bearer-token", "private-cookie", "private-client-request-id",
	} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("access record contains synthetic secret %q", secret)
		}
	}
	records := decodeRecords(t, &logs)
	if len(records) != 1 {
		t.Fatalf("records = %d, want one completed request", len(records))
	}
	assertRecord(t, records[0], http.StatusOK, historyPattern, http.MethodGet)
}

func TestAccessLogCapturesTheFirstFinalStatus(t *testing.T) {
	tests := []struct {
		name   string
		handle http.HandlerFunc
		status int
	}{
		{"implicit", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }, http.StatusOK},
		{"explicit", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }, http.StatusForbidden},
		{"empty response", func(http.ResponseWriter, *http.Request) {}, http.StatusOK},
		{"duplicate final", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.WriteHeader(http.StatusInternalServerError)
		}, http.StatusCreated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			mux := http.NewServeMux()
			mux.HandleFunc(historyPattern, test.handle)
			handler := accessLog(slog.New(slog.NewJSONHandler(&logs, nil)), mux)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/conversations/chat/messages", nil))
			response := recorder.Result()
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("response status = %d, want %d", response.StatusCode, test.status)
			}
			records := decodeRecords(t, &logs)
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1", len(records))
			}
			assertRecord(t, records[0], test.status, historyPattern, http.MethodGet)
		})
	}
}

func TestAccessLogNormalizesUnmatchedRoutesAndUnknownMethods(t *testing.T) {
	tests := []struct {
		method, target, label string
		status                int
	}{
		{http.MethodGet, "/unknown/private-path?token=private-query", http.MethodGet, http.StatusNotFound},
		{http.MethodGet, "/unknown/private-path//more", http.MethodGet, http.StatusMovedPermanently},
		{http.MethodDelete, "/conversations/private-chat/messages", http.MethodDelete, http.StatusMethodNotAllowed},
		{"PRIVATE-METHOD", "/conversations/private-chat/messages", "OTHER", http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			var logs bytes.Buffer
			handler := accessLog(slog.New(slog.NewJSONHandler(&logs, nil)), demoMux())
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(test.method, test.target, nil))
			if strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "PRIVATE-METHOD") {
				t.Fatalf("unmatched request data escaped into the access record: %s", &logs)
			}
			records := decodeRecords(t, &logs)
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1", len(records))
			}
			assertRecord(t, records[0], test.status, "unmatched", test.label)
		})
	}
}

func TestStatusWriterDoesNotTreatInformationalHeadersAsFinal(t *testing.T) {
	original := &headerRecorder{header: make(http.Header)}
	tracked := &statusWriter{ResponseWriter: original}
	tracked.WriteHeader(http.StatusEarlyHints)
	tracked.WriteHeader(http.StatusCreated)
	tracked.WriteHeader(http.StatusInternalServerError)
	tracked.WriteHeader(http.StatusEarlyHints)
	if tracked.status != http.StatusCreated ||
		!reflect.DeepEqual(original.statuses, []int{http.StatusEarlyHints, http.StatusCreated}) {
		t.Fatalf("final status=%d forwarded headers=%v", tracked.status, original.statuses)
	}
}

func TestStatusWriterAllowsResponseControllerToUnwrap(t *testing.T) {
	recorder := httptest.NewRecorder()
	tracked := &statusWriter{ResponseWriter: recorder}
	if err := http.NewResponseController(tracked).Flush(); err != nil || !recorder.Flushed {
		t.Fatalf("ResponseController did not reach the original writer: %v", err)
	}
}

func TestAccessLogDoesNotSwallowPanicOrRecordFalseSuccess(t *testing.T) {
	var logs bytes.Buffer
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(http.ResponseWriter, *http.Request) { panic("synthetic panic") })
	handler := accessLog(slog.New(slog.NewJSONHandler(&logs, nil)), mux)
	var caught any
	func() {
		defer func() { caught = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if caught != "synthetic panic" || logs.Len() != 0 {
		t.Fatalf("panic=%v, access records=%s; want propagation and no completed record", caught, &logs)
	}
}

func TestAccessLogConcurrentRecordsRemainCompleteJSON(t *testing.T) {
	var logs bytes.Buffer
	handler := accessLog(slog.New(slog.NewJSONHandler(&logs, nil)), demoMux())
	const requests = 24
	var workers sync.WaitGroup
	for i := 0; i < requests; i++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			target := fmt.Sprintf("/conversations/private-%d/messages?token=synthetic", id)
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
		}(i)
	}
	workers.Wait()
	records := decodeRecords(t, &logs)
	if len(records) != requests {
		t.Fatalf("records = %d, want %d", len(records), requests)
	}
	for _, record := range records {
		assertRecord(t, record, http.StatusOK, historyPattern, http.MethodGet)
	}
}

func decodeRecords(t *testing.T, reader io.Reader) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(reader)
	var records []map[string]any
	for {
		var record map[string]any
		err := decoder.Decode(&record)
		if err == io.EOF {
			return records
		}
		if err != nil {
			t.Fatalf("decode structured access record: %v", err)
		}
		records = append(records, record)
	}
}

func assertRecord(t *testing.T, record map[string]any, status int, route, method string) {
	t.Helper()
	allowed := []string{"time", "level", "msg", "method", "route", "status", "duration_ms"}
	if len(record) != len(allowed) {
		t.Fatalf("record fields = %v, want only %v", record, allowed)
	}
	for _, key := range allowed {
		if _, ok := record[key]; !ok {
			t.Fatalf("record missing %q: %v", key, record)
		}
	}
	duration, ok := record["duration_ms"].(float64)
	if !ok || duration < 0 || record["status"] != float64(status) ||
		record["route"] != route || record["method"] != method ||
		record["level"] != "INFO" || record["msg"] != "http request completed" {
		t.Fatalf("unexpected access record: %v", record)
	}
}

type headerRecorder struct {
	header   http.Header
	statuses []int
}

func (w *headerRecorder) Header() http.Header            { return w.header }
func (w *headerRecorder) Write(body []byte) (int, error) { return len(body), nil }
func (w *headerRecorder) WriteHeader(status int)         { w.statuses = append(w.statuses, status) }
