package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPageMessagesUsesStableOrderAndCursor(t *testing.T) {
	messages := demoMessages()
	original := append([]Message(nil), messages...)

	first, err := pageMessages(messages, "chat-1", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []int64{3, 2}, messageIDs(first.Messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("first page IDs = %v, want %v", got, want)
	}
	if first.NextCursor == "" {
		t.Fatal("first page has no next cursor")
	}

	second, err := pageMessages(messages, "chat-1", first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []int64{1}, messageIDs(second.Messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("second page IDs = %v, want %v", got, want)
	}
	if second.NextCursor != "" {
		t.Fatalf("last page next cursor = %q, want empty", second.NextCursor)
	}
	if !reflect.DeepEqual(messages, original) {
		t.Fatal("pageMessages modified its input")
	}
}

func TestPageMessagesRejectsInvalidArguments(t *testing.T) {
	validCursor, err := encodeCursor(pageCursor{
		ConversationID: "chat-1",
		CreatedAt:      time.Date(2026, time.January, 5, 10, 2, 0, 0, time.UTC),
		ID:             2,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		conversationID string
		cursor         string
		limit          int
		wantErr        error
	}{
		{name: "missing conversation", limit: 1, wantErr: ErrConversationRequired},
		{name: "zero limit", conversationID: "chat-1", limit: 0, wantErr: ErrInvalidPageLimit},
		{name: "limit too large", conversationID: "chat-1", limit: maxPageLimit + 1, wantErr: ErrInvalidPageLimit},
		{name: "malformed cursor", conversationID: "chat-1", cursor: "not-base64!", limit: 1, wantErr: ErrInvalidCursor},
		{name: "cursor for another conversation", conversationID: "chat-2", cursor: validCursor, limit: 1, wantErr: ErrInvalidCursor},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := pageMessages(demoMessages(), test.conversationID, test.cursor, test.limit)
			if err != test.wantErr {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestDecodeCursorRejectsInvalidPayloads(t *testing.T) {
	tests := map[string]string{
		"invalid base64": "%%%",
		"invalid JSON":   "e30",
		"unknown field":  "eyJjb252ZXJzYXRpb25faWQiOiJjaGF0LTEiLCJjcmVhdGVkX2F0IjoiMjAyNi0wMS0wNVQxMDowMDowMFoiLCJpZCI6MSwiZXh0cmEiOnRydWV9",
		"zero ID":        "eyJjb252ZXJzYXRpb25faWQiOiJjaGF0LTEiLCJjcmVhdGVkX2F0IjoiMjAyNi0wMS0wNVQxMDowMDowMFoiLCJpZCI6MH0",
		"trailing JSON":  "eyJjb252ZXJzYXRpb25faWQiOiJjaGF0LTEiLCJjcmVhdGVkX2F0IjoiMjAyNi0wMS0wNVQxMDowMDowMFoiLCJpZCI6MX0ge30",
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCursor(token); err != ErrInvalidCursor {
				t.Fatalf("error = %v, want %v", err, ErrInvalidCursor)
			}
		})
	}
}

func TestPageMessagesReturnsEmptyPage(t *testing.T) {
	page, err := pageMessages(nil, "chat-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.Messages == nil || len(page.Messages) != 0 {
		t.Fatalf("messages = %#v, want a non-nil empty slice", page.Messages)
	}
	if page.NextCursor != "" {
		t.Fatalf("next cursor = %q, want empty", page.NextCursor)
	}
}

func TestMessageListHandler(t *testing.T) {
	handler := newRouter(demoMessages())
	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{name: "valid page", method: http.MethodGet, target: "/messages?conversation_id=chat-1&limit=1", wantStatus: http.StatusOK},
		{name: "missing conversation", method: http.MethodGet, target: "/messages", wantStatus: http.StatusBadRequest},
		{name: "invalid limit", method: http.MethodGet, target: "/messages?conversation_id=chat-1&limit=zero", wantStatus: http.StatusBadRequest},
		{name: "wrong method", method: http.MethodPost, target: "/messages", wantStatus: http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body)
			}
			if test.wantStatus == http.StatusOK {
				if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
					t.Fatalf("Content-Type = %q, want application/json", got)
				}
				var page MessagePage
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if len(page.Messages) != 1 || page.NextCursor == "" {
					t.Fatalf("page = %#v, want one message and a next cursor", page)
				}
			} else if test.wantStatus == http.StatusBadRequest {
				var body map[string]string
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == "" {
					t.Fatalf("error response is not stable JSON: %s", response.Body)
				}
			}
		})
	}
}

func TestMessageListHandlerUsesDefaultLimit(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/messages?conversation_id=chat-1", nil)
	response := httptest.NewRecorder()
	newRouter(demoMessages()).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var page MessagePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if got, want := len(page.Messages), 3; got != want {
		t.Fatalf("message count = %d, want %d (default limit %d)", got, want, defaultPageLimit)
	}
}

func TestFetchPageEncodesQueryAndRejectsUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("conversation_id"); got != "chat with spaces" {
			t.Errorf("conversation_id = %q, want encoded query value", got)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	query := url.Values{"conversation_id": {"chat with spaces"}}
	if _, err := fetchPage(server.Client(), server.URL, query); err == nil {
		t.Fatal("fetchPage error = nil, want unexpected-status error")
	}
}

func messageIDs(messages []Message) []int64 {
	ids := make([]int64, len(messages))
	for index, message := range messages {
		ids[index] = message.ID
	}
	return ids
}
