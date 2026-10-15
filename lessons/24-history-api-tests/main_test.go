package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHistoryHTTPForwardsTrustedIdentityScopeCursorAndContext(t *testing.T) {
	type markerKey struct{}
	createdAt := time.Date(2026, time.October, 15, 6, 0, 0, 0, time.UTC)
	before := Cursor{CreatedAt: createdAt, MessageID: 42}
	next := Cursor{CreatedAt: createdAt.Add(-time.Second), MessageID: 41}
	calls := 0
	load := func(ctx context.Context, userID, conversationID string, cursor *Cursor, limit int) (HistoryPage, error) {
		calls++
		if userID != "alice" || conversationID != "chat-1" || limit != 2 {
			t.Fatalf("load arguments = (%q, %q, %d)", userID, conversationID, limit)
		}
		if cursor == nil || !cursor.CreatedAt.Equal(before.CreatedAt) || cursor.MessageID != before.MessageID {
			t.Fatalf("cursor = %#v, want %#v", cursor, before)
		}
		if ctx.Value(markerKey{}) != "request-owned" {
			t.Fatal("request context was replaced")
		}
		return HistoryPage{
			Messages: []Message{{ID: 41, ConversationID: conversationID, Text: "visible", CreatedAt: next.CreatedAt}},
			Next:     &next, HasMore: true,
		}, nil
	}
	request := historyRequest("alice", "/conversations/chat-1/messages?limit=2&before="+url.QueryEscape(encodeCursor(before)))
	request = request.WithContext(context.WithValue(request.Context(), markerKey{}, "request-owned"))
	recorder := httptest.NewRecorder()
	newRouter(load).ServeHTTP(recorder, request)
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
	response := assertJSONResponse(t, recorder, http.StatusOK)
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("authenticated history response is missing no-store")
	}
	var page historyResponse
	decodeOneJSON(t, response.Body, &page)
	if len(page.Messages) != 1 || page.Messages[0].Text != "visible" || !page.HasMore {
		t.Fatalf("page = %#v", page)
	}
	_, decoded, err := parsePageQuery("before=" + url.QueryEscape(page.NextCursor))
	if err != nil || decoded == nil || decoded.MessageID != next.MessageID || !decoded.CreatedAt.Equal(next.CreatedAt) {
		t.Fatalf("next position = %#v, %v; want %#v", decoded, err, next)
	}
}

func TestHistoryHTTPRejectsRequestsBeforeLoading(t *testing.T) {
	badCursor := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	tests := []struct {
		name, userID, query string
		wantStatus          int
	}{
		{"missing principal", "", "", http.StatusUnauthorized},
		{"query identity is not authentication", "", "?user_id=alice", http.StatusUnauthorized},
		{"identity override", "alice", "?user_id=mallory", http.StatusBadRequest},
		{"zero limit", "alice", "?limit=0", http.StatusBadRequest},
		{"negative limit", "alice", "?limit=-1", http.StatusBadRequest},
		{"large limit", "alice", "?limit=51", http.StatusBadRequest},
		{"empty limit", "alice", "?limit=", http.StatusBadRequest},
		{"non-integer limit", "alice", "?limit=no", http.StatusBadRequest},
		{"duplicate limit", "alice", "?limit=1&limit=2", http.StatusBadRequest},
		{"malformed query", "alice", "?limit=2;limit=3", http.StatusBadRequest},
		{"empty cursor", "alice", "?before=", http.StatusBadRequest},
		{"bad base64", "alice", "?before=not-base64!", http.StatusBadRequest},
		{"oversized cursor", "alice", "?before=" + strings.Repeat("a", 257), http.StatusBadRequest},
		{"duplicate cursor", "alice", "?before=a&before=b", http.StatusBadRequest},
		{"bad cursor position", "alice", "?before=" + badCursor(`{"created_at":"2026-10-15T06:00:00Z","message_id":0}`), http.StatusBadRequest},
		{"extra cursor field", "alice", "?before=" + badCursor(`{"created_at":"2026-10-15T06:00:00Z","message_id":1,"user_id":"alice"}`), http.StatusBadRequest},
		{"trailing cursor JSON", "alice", "?before=" + badCursor(`{"created_at":"2026-10-15T06:00:00Z","message_id":1} {}`), http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			handler := newRouter(func(context.Context, string, string, *Cursor, int) (HistoryPage, error) {
				calls++
				return HistoryPage{}, nil
			})
			request := historyRequest(test.userID, "/conversations/chat-1/messages"+test.query)
			request.Header.Set("Authorization", "Bearer unverified-client-text")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			response := assertJSONResponse(t, recorder, test.wantStatus)
			if calls != 0 {
				t.Fatalf("loader calls = %d, want 0 for rejected request", calls)
			}
			if test.wantStatus == http.StatusUnauthorized && response.Header.Get("WWW-Authenticate") == "" {
				t.Fatal("missing Bearer challenge")
			}
			var problem errorResponse
			decodeOneJSON(t, response.Body, &problem)
			if problem.Code == "" || problem.Message == "" {
				t.Fatalf("empty public error = %#v", problem)
			}
		})
	}
}

func TestHistoryHTTPMapsWrappedErrorsWithoutLeakingDetails(t *testing.T) {
	const secret = "private database DSN and query details"
	tests := []struct {
		name, code, message string
		err                 error
		status              int
	}{
		{"denied", "forbidden", "conversation access denied", fmt.Errorf("%s: %w", secret, ErrNotMember), http.StatusForbidden},
		{"deadline", "history_timeout", "history request timed out", fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), http.StatusGatewayTimeout},
		{"database failure", "history_unavailable", "history could not be loaded", errors.New(secret), http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			handler := newRouter(func(context.Context, string, string, *Cursor, int) (HistoryPage, error) {
				calls++
				return HistoryPage{}, test.err
			})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, historyRequest("alice", "/conversations/chat-1/messages"))
			response := assertJSONResponse(t, recorder, test.status)
			if calls != 1 || strings.Contains(recorder.Body.String(), secret) {
				t.Fatalf("calls=%d, body=%s", calls, recorder.Body)
			}
			var problem errorResponse
			decodeOneJSON(t, response.Body, &problem)
			if problem.Code != test.code || problem.Message != test.message {
				t.Fatalf("public error = %#v, want (%q, %q)", problem, test.code, test.message)
			}
		})
	}
}

func TestHistoryHTTPDefaultPageAndEmptyJSONArray(t *testing.T) {
	handler := newRouter(func(_ context.Context, _, _ string, before *Cursor, limit int) (HistoryPage, error) {
		if before != nil || limit != 20 {
			t.Fatalf("default page = (%#v, %d), want (nil, 20)", before, limit)
		}
		return HistoryPage{}, nil
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, historyRequest("alice", "/conversations/chat-1/messages"))
	response := assertJSONResponse(t, recorder, http.StatusOK)
	var page historyResponse
	decodeOneJSON(t, response.Body, &page)
	if page.Messages == nil || len(page.Messages) != 0 || page.HasMore || page.NextCursor != "" {
		t.Fatalf("empty page = %#v, want [] and no next cursor", page)
	}
}

func TestHistoryHTTPHandlesMissingOrBrokenAdapter(t *testing.T) {
	for name, loader := range map[string]historyLoader{
		"missing": nil,
		"invalid next page": func(context.Context, string, string, *Cursor, int) (HistoryPage, error) {
			return HistoryPage{HasMore: true}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			newRouter(loader).ServeHTTP(recorder, historyRequest("alice", "/conversations/chat-1/messages"))
			response := assertJSONResponse(t, recorder, http.StatusInternalServerError)
			var problem errorResponse
			decodeOneJSON(t, response.Body, &problem)
			if problem.Code != "history_unavailable" {
				t.Fatalf("public error = %#v", problem)
			}
		})
	}
}

func TestHistoryHTTPDoesNotWriteAfterRequestCancellation(t *testing.T) {
	for _, cancelBeforeLoad := range []bool{true, false} {
		t.Run(fmt.Sprintf("before=%t", cancelBeforeLoad), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			handler := newRouter(func(ctx context.Context, _, _ string, _ *Cursor, _ int) (HistoryPage, error) {
				calls++
				cancel()
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("loader did not receive request cancellation")
				}
				return HistoryPage{}, nil
			})
			if cancelBeforeLoad {
				cancel()
			}
			request := historyRequest("alice", "/conversations/chat-1/messages")
			request = request.WithContext(withDemoPrincipal(ctx, "alice"))
			writer := &countingWriter{ResponseRecorder: httptest.NewRecorder()}
			handler.ServeHTTP(writer, request)
			if writer.writes != 0 {
				t.Fatalf("response writes = %d, want 0 after cancellation", writer.writes)
			}
			if (cancelBeforeLoad && calls != 0) || (!cancelBeforeLoad && calls != 1) {
				t.Fatalf("loader calls = %d, cancelBeforeLoad=%t", calls, cancelBeforeLoad)
			}
		})
	}
}

func TestHistoryHTTPMuxRejectsOtherMethodsAndPaths(t *testing.T) {
	handler := newRouter(func(context.Context, string, string, *Cursor, int) (HistoryPage, error) {
		t.Fatal("loader called for unsupported route")
		return HistoryPage{}, nil
	})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, "/conversations/chat-1/messages", http.StatusMethodNotAllowed},
		{http.MethodGet, "/unknown", http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != test.status {
			t.Fatalf("%s %s status = %d, want %d", test.method, test.path, recorder.Code, test.status)
		}
	}
}

func historyRequest(userID, target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if userID != "" {
		request = request.WithContext(withDemoPrincipal(request.Context(), userID))
	}
	return request
}

func assertJSONResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int) *http.Response {
	t.Helper()
	response := recorder.Result()
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != status || response.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("response = %d %q, want %d JSON; body=%s", response.StatusCode, response.Header.Get("Content-Type"), status, recorder.Body)
	}
	return response
}

func decodeOneJSON(t *testing.T, body io.Reader, output any) {
	t.Helper()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("response contains trailing JSON or invalid content: %v", err)
	}
}

type countingWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *countingWriter) WriteHeader(status int) {
	w.writes++
	w.ResponseRecorder.WriteHeader(status)
}

func (w *countingWriter) Write(body []byte) (int, error) {
	w.writes++
	return w.ResponseRecorder.Write(body)
}
