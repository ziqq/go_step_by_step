// Package main demonstrates composing an HTTP handler, application service,
// and replaceable message-store adapter.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxMessageRunes = 280
	maxRequestBytes = 1 << 20
)

var (
	ErrStoreRequired  = errors.New("message store is required")
	ErrInvalidMessage = errors.New("message is invalid")
)

type MessageDraft struct {
	ConversationID string
	Text           string
}

type Message struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Text           string    `json:"text"`
	CreatedAt      time.Time `json:"created_at"`
}

// MessageStore is the storage contract consumed by MessageService.
type MessageStore interface {
	Create(context.Context, MessageDraft) (Message, error)
}

type MessageService struct {
	store MessageStore
}

func NewMessageService(store MessageStore) (*MessageService, error) {
	if store == nil {
		return nil, ErrStoreRequired
	}
	return &MessageService{store: store}, nil
}

func (s *MessageService) Create(ctx context.Context, draft MessageDraft) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}

	draft.ConversationID = strings.TrimSpace(draft.ConversationID)
	draft.Text = strings.TrimSpace(draft.Text)
	if draft.ConversationID == "" || draft.Text == "" || utf8.RuneCountInString(draft.Text) > maxMessageRunes {
		return Message{}, ErrInvalidMessage
	}

	message, err := s.store.Create(ctx, draft)
	if err != nil {
		return Message{}, fmt.Errorf("create message: %w", err)
	}
	return message, nil
}

type MemoryMessageStore struct {
	mu       sync.Mutex
	nextID   int64
	messages []Message
}

func (s *MemoryMessageStore) Create(ctx context.Context, draft MessageDraft) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}

	s.nextID++
	message := Message{
		ID:             s.nextID,
		ConversationID: draft.ConversationID,
		Text:           draft.Text,
		CreatedAt:      time.Now().UTC(),
	}
	s.messages = append(s.messages, message)
	return message, nil
}

type createMessageRequest struct {
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
}

func newRouter(service *MessageService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		decoder.DisallowUnknownFields()

		var input createMessageRequest
		if err := decoder.Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "request body must be one valid JSON object")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeError(w, http.StatusBadRequest, "request body must contain exactly one JSON value")
			return
		}

		message, err := service.Create(r.Context(), MessageDraft{
			ConversationID: input.ConversationID,
			Text:           input.Text,
		})
		if errors.Is(err, ErrInvalidMessage) {
			writeError(w, http.StatusBadRequest, "conversation_id and valid text are required")
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "message creation timed out")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "message could not be created")
			return
		}

		w.Header().Set("Location", fmt.Sprintf("/messages/%d", message.ID))
		writeJSON(w, http.StatusCreated, message)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

func main() {
	service, err := NewMessageService(&MemoryMessageStore{})
	if err != nil {
		fmt.Println("create message service:", err)
		return
	}
	server := httptest.NewServer(newRouter(service))
	defer server.Close()

	response, err := server.Client().Post(
		server.URL+"/messages",
		"application/json",
		strings.NewReader(`{"conversation_id":"chat-1","text":"hello from the capstone"}`),
	)
	if err != nil {
		fmt.Println("request error:", err)
		return
	}
	defer response.Body.Close()

	var message Message
	if err := json.NewDecoder(response.Body).Decode(&message); err != nil {
		fmt.Println("response error:", err)
		return
	}
	fmt.Printf("%s id=%d conversation=%s text=%s\n", response.Status, message.ID, message.ConversationID, message.Text)
}
