package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingStore struct {
	calls  int
	draft  MessageDraft
	ctx    context.Context
	result Message
	err    error
}

func (s *recordingStore) Create(ctx context.Context, draft MessageDraft) (Message, error) {
	s.calls++
	s.ctx = ctx
	s.draft = draft
	return s.result, s.err
}

func TestNewMessageServiceRequiresStore(t *testing.T) {
	service, err := NewMessageService(nil)
	if service != nil || !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("NewMessageService(nil) = (%v, %v), want (nil, ErrStoreRequired)", service, err)
	}
}

func TestMessageServiceNormalizesAndDelegates(t *testing.T) {
	wantMessage := Message{
		ID:             17,
		ConversationID: "chat-1",
		Text:           "hello",
		CreatedAt:      time.Date(2026, time.October, 9, 6, 30, 0, 0, time.UTC),
	}
	store := &recordingStore{result: wantMessage}
	service, err := NewMessageService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), struct{}{}, "request")

	got, err := service.Create(ctx, MessageDraft{ConversationID: " chat-1 ", Text: "  hello  "})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantMessage) {
		t.Fatalf("Create() = %#v, want %#v", got, wantMessage)
	}
	if store.calls != 1 || store.ctx != ctx {
		t.Fatalf("store calls/context = (%d, %v), want (1, original context)", store.calls, store.ctx)
	}
	if want := (MessageDraft{ConversationID: "chat-1", Text: "hello"}); store.draft != want {
		t.Fatalf("stored draft = %#v, want %#v", store.draft, want)
	}
}

func TestMessageServiceRejectsInvalidDraftBeforeStore(t *testing.T) {
	tests := []struct {
		name  string
		draft MessageDraft
	}{
		{name: "missing conversation", draft: MessageDraft{Text: "hello"}},
		{name: "blank text", draft: MessageDraft{ConversationID: "chat-1", Text: " \t "}},
		{name: "too many code points", draft: MessageDraft{ConversationID: "chat-1", Text: strings.Repeat("я", maxMessageRunes+1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &recordingStore{}
			service, err := NewMessageService(store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Create(context.Background(), test.draft); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("Create() error = %v, want ErrInvalidMessage", err)
			}
			if store.calls != 0 {
				t.Fatalf("store calls = %d, want 0 for invalid input", store.calls)
			}
		})
	}
}

func TestMessageServiceDoesNotCallStoreAfterCancellation(t *testing.T) {
	store := &recordingStore{}
	service, err := NewMessageService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := service.Create(ctx, MessageDraft{ConversationID: "chat-1", Text: "hello"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v, want context.Canceled", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0 after cancellation", store.calls)
	}
}

func TestMessageServiceWrapsStoreError(t *testing.T) {
	wantErr := errors.New("database unavailable")
	service, err := NewMessageService(&recordingStore{err: wantErr})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), MessageDraft{ConversationID: "chat-1", Text: "hello"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Create() error = %v, want wrapped database error", err)
	}
}

func TestMemoryMessageStoreAllocatesUniqueIDsConcurrently(t *testing.T) {
	const count = 32
	store := &MemoryMessageStore{}
	ids := make(chan int64, count)
	errCh := make(chan error, count)
	var workers sync.WaitGroup
	for index := range count {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			message, err := store.Create(context.Background(), MessageDraft{
				ConversationID: "chat-1",
				Text:           fmt.Sprintf("message-%d", index),
			})
			if err != nil {
				errCh <- err
				return
			}
			ids <- message.ID
		}(index)
	}
	workers.Wait()
	close(ids)
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	seen := make(map[int64]bool, count)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate message ID %d", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("unique IDs = %d, want %d", len(seen), count)
	}
}

func TestCreateMessageHandlerReturnsCreatedMessage(t *testing.T) {
	service, err := NewMessageService(&MemoryMessageStore{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(
		`{"conversation_id":" chat-1 ","text":" hello "}`,
	))
	response := httptest.NewRecorder()
	newRouter(service).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body)
	}
	if got := response.Header().Get("Location"); got != "/messages/1" {
		t.Fatalf("Location = %q, want /messages/1", got)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var message Message
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	if message.ID != 1 || message.ConversationID != "chat-1" || message.Text != "hello" || message.CreatedAt.IsZero() {
		t.Fatalf("response = %#v, want stored message with generated ID and timestamp", message)
	}
}

func TestCreateMessageHandlerRejectsInvalidBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"text":`},
		{name: "unknown field", body: `{"conversation_id":"chat-1","text":"hello","extra":true}`},
		{name: "trailing JSON", body: `{"conversation_id":"chat-1","text":"hello"} {}`},
		{name: "invalid message", body: `{"conversation_id":" ","text":"hello"}`},
		{name: "oversized body", body: strings.Repeat("x", maxRequestBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewMessageService(&MemoryMessageStore{})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			newRouter(service).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body)
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Fatalf("response is not stable JSON error: %s", response.Body)
			}
		})
	}
}

func TestCreateMessageHandlerHidesStoreError(t *testing.T) {
	service, err := NewMessageService(&recordingStore{err: errors.New("secret database detail")})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(
		`{"conversation_id":"chat-1","text":"hello"}`,
	))
	response := httptest.NewRecorder()
	newRouter(service).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] != "message could not be created" {
		t.Fatalf("error response = %s, want stable generic message", response.Body)
	}
	if strings.Contains(response.Body.String(), "secret database detail") {
		t.Fatalf("response leaked internal error: %s", response.Body)
	}
}

func TestCreateMessageHandlerRejectsWrongMethod(t *testing.T) {
	service, err := NewMessageService(&MemoryMessageStore{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/messages", nil)
	response := httptest.NewRecorder()
	newRouter(service).ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}
