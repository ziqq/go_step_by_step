package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type historyStoreFunc func(context.Context, string, *historyCursor, int) ([]Message, bool, error)

func (f historyStoreFunc) ListBefore(ctx context.Context, conversationID string, before *historyCursor, limit int) ([]Message, bool, error) {
	return f(ctx, conversationID, before, limit)
}

func TestHistoryPaginatesWithoutDuplicatesWhenTimestampsTie(t *testing.T) {
	base := time.Date(2026, time.October, 13, 6, 0, 0, 0, time.UTC)
	store := newMemoryMessageStore(
		Message{ID: 1, ConversationID: "chat-1", Text: "oldest", CreatedAt: base},
		Message{ID: 2, ConversationID: "chat-1", Text: "tie-a", CreatedAt: base.Add(time.Second)},
		Message{ID: 3, ConversationID: "chat-1", Text: "tie-b", CreatedAt: base.Add(time.Second)},
		Message{ID: 4, ConversationID: "chat-1", Text: "newest", CreatedAt: base.Add(2 * time.Second)},
		Message{ID: 5, ConversationID: "chat-2", Text: "other chat", CreatedAt: base.Add(3 * time.Second)},
	)
	membershipCalls := 0
	handler := newRouter(func(_ context.Context, userID, conversationID string) (bool, error) {
		membershipCalls++
		return userID == "alice" && conversationID == "chat-1", nil
	}, store)

	first := serveHistory(handler, "alice", "/conversations/chat-1/messages?limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d; body=%s", first.Code, http.StatusOK, first.Body)
	}
	firstPage := decodePage(t, first)
	if !firstPage.HasMore || firstPage.NextCursor == "" || ids(firstPage.Messages) != "4,3" {
		t.Fatalf("first page = %#v, want IDs 4,3 and a next cursor", firstPage)
	}

	secondURL := "/conversations/chat-1/messages?limit=2&before=" + url.QueryEscape(firstPage.NextCursor)
	second := serveHistory(handler, "alice", secondURL)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d; body=%s", second.Code, http.StatusOK, second.Body)
	}
	secondPage := decodePage(t, second)
	if secondPage.HasMore || secondPage.NextCursor != "" || ids(secondPage.Messages) != "2,1" {
		t.Fatalf("second page = %#v, want IDs 2,1 and no next cursor", secondPage)
	}
	if membershipCalls != 2 {
		t.Fatalf("membership calls = %d, want one check per page", membershipCalls)
	}
}

func TestHistoryRequiresMembershipBeforeReading(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		allowed    bool
		wantStatus int
	}{
		{name: "missing principal", wantStatus: http.StatusUnauthorized},
		{name: "not a member", userID: "mallory", wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			membershipCalls, storeCalls := 0, 0
			handler := newRouter(func(context.Context, string, string) (bool, error) {
				membershipCalls++
				return test.allowed, nil
			}, historyStoreFunc(func(context.Context, string, *historyCursor, int) ([]Message, bool, error) {
				storeCalls++
				return nil, false, nil
			}))
			response := serveHistory(handler, test.userID, "/conversations/chat-1/messages")
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body)
			}
			if storeCalls != 0 {
				t.Fatalf("store calls = %d, want 0 for denied request", storeCalls)
			}
			if test.userID == "" && membershipCalls != 0 {
				t.Fatalf("membership calls = %d, want 0 without a principal", membershipCalls)
			}
		})
	}
}

func TestHistoryRejectsInvalidPageParametersBeforeDependencies(t *testing.T) {
	invalidURLs := []string{
		"/conversations/chat-1/messages?limit=0",
		"/conversations/chat-1/messages?limit=51",
		"/conversations/chat-1/messages?limit=nope",
		"/conversations/chat-1/messages?limit=1&limit=2",
		"/conversations/chat-1/messages?before=not%21base64",
		"/conversations/chat-1/messages?before=",
	}
	for _, requestURL := range invalidURLs {
		t.Run(requestURL, func(t *testing.T) {
			membershipCalls, storeCalls := 0, 0
			handler := newRouter(func(context.Context, string, string) (bool, error) {
				membershipCalls++
				return true, nil
			}, historyStoreFunc(func(context.Context, string, *historyCursor, int) ([]Message, bool, error) {
				storeCalls++
				return nil, false, nil
			}))
			response := serveHistory(handler, "alice", requestURL)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body)
			}
			if membershipCalls != 0 || storeCalls != 0 {
				t.Fatalf("dependency calls = (%d, %d), want (0, 0) for invalid parameters", membershipCalls, storeCalls)
			}
		})
	}
}

func TestParseCursorRejectsMalformedValues(t *testing.T) {
	validTime := time.Date(2026, time.October, 13, 6, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	invalidTokens := []string{
		"not-base64!",
		base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"` + validTime + `","message_id":0}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"` + validTime + `","message_id":1,"extra":true}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"` + validTime + `","message_id":1} {}`)),
		strings.Repeat("a", maxCursorBytes+1),
	}
	for _, token := range invalidTokens {
		t.Run(fmt.Sprintf("token-%d", len(token)), func(t *testing.T) {
			if _, err := parseCursor([]string{token}); err == nil {
				t.Fatal("parseCursor() unexpectedly accepted invalid cursor")
			}
		})
	}
}

func TestHistoryHidesDependencyErrors(t *testing.T) {
	secret := "private database connection details"
	handler := newRouter(func(context.Context, string, string) (bool, error) {
		return false, errors.New(secret)
	}, historyStoreFunc(func(context.Context, string, *historyCursor, int) ([]Message, bool, error) {
		t.Fatal("store called after membership dependency failed")
		return nil, false, nil
	}))
	response := serveHistory(handler, "alice", "/conversations/chat-1/messages")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), secret) {
		t.Fatalf("response leaked dependency error: %s", response.Body)
	}
}

func TestHistoryUsesConversationScopeAndReturnsEmptyArray(t *testing.T) {
	store := newMemoryMessageStore(Message{
		ID: 1, ConversationID: "chat-2", Text: "not visible", CreatedAt: time.Now().UTC(),
	})
	handler := newRouter(func(context.Context, string, string) (bool, error) { return true, nil }, store)
	response := serveHistory(handler, "alice", "/conversations/chat-1/messages")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	page := decodePage(t, response)
	if page.Messages == nil || len(page.Messages) != 0 || page.HasMore || page.NextCursor != "" {
		t.Fatalf("empty page = %#v, want empty messages array and no cursor", page)
	}
}

func serveHistory(handler http.Handler, userID, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if userID != "" {
		request = request.WithContext(withDemoPrincipal(request.Context(), Principal{UserID: userID}))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodePage(t *testing.T, response *httptest.ResponseRecorder) historyPage {
	t.Helper()
	var page historyPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v; body=%s", err, response.Body)
	}
	return page
}

func ids(messages []Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, fmt.Sprint(message.ID))
	}
	return strings.Join(parts, ",")
}
