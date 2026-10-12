// Package main demonstrates authorization before a chat message is written.
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

type Principal struct {
	UserID string
}

type principalContextKey struct{}

// withDemoPrincipal only simulates identity propagation in this lesson.
// A deployed service must use its verified authentication middleware instead.
func withDemoPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func principalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		return Principal{}, false
	}
	return principal, true
}

type createMessageRequest struct {
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
}

type Message struct {
	ID             int64     `json:"id"`
	SenderID       string    `json:"sender_id"`
	ConversationID string    `json:"conversation_id"`
	Text           string    `json:"text"`
	CreatedAt      time.Time `json:"created_at"`
}

type membershipChecker func(context.Context, string, string) (bool, error)
type messageCreator func(context.Context, string, createMessageRequest) (Message, error)

type MemoryMessageStore struct {
	mu       sync.Mutex
	nextID   int64
	messages []Message
}

func (s *MemoryMessageStore) create(ctx context.Context, senderID string, draft createMessageRequest) (Message, error) {
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
		SenderID:       senderID,
		ConversationID: draft.ConversationID,
		Text:           draft.Text,
		CreatedAt:      time.Now().UTC(),
	}
	s.messages = append(s.messages, message)
	return message, nil
}

func newRouter(checkMembership membershipChecker, create messageCreator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="chat"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if checkMembership == nil || create == nil {
			writeError(w, http.StatusInternalServerError, "message creation unavailable")
			return
		}

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

		input.ConversationID = strings.TrimSpace(input.ConversationID)
		input.Text = strings.TrimSpace(input.Text)
		if input.ConversationID == "" || input.Text == "" || utf8.RuneCountInString(input.Text) > maxMessageRunes {
			writeError(w, http.StatusBadRequest, "conversation_id and valid text are required")
			return
		}

		allowed, err := checkMembership(r.Context(), principal.UserID, input.ConversationID)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				writeError(w, http.StatusGatewayTimeout, "authorization timed out")
				return
			}
			writeError(w, http.StatusInternalServerError, "message could not be authorized")
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "conversation access denied")
			return
		}

		message, err := create(r.Context(), principal.UserID, input)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				writeError(w, http.StatusGatewayTimeout, "message creation timed out")
				return
			}
			writeError(w, http.StatusInternalServerError, "message could not be created")
			return
		}
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
	store := &MemoryMessageStore{}
	checkMembership := func(_ context.Context, userID, conversationID string) (bool, error) {
		return userID == "alice" && conversationID == "chat-1", nil
	}
	request := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(
		`{"conversation_id":"chat-1","text":"hello from the capstone"}`,
	))
	request = request.WithContext(withDemoPrincipal(request.Context(), Principal{UserID: "alice"}))
	response := httptest.NewRecorder()
	newRouter(checkMembership, store.create).ServeHTTP(response, request)

	var message Message
	if err := json.NewDecoder(response.Body).Decode(&message); err != nil {
		fmt.Println("response error:", err)
		return
	}
	fmt.Printf("%d %s sender=%s message=%d conversation=%s\n", response.Code, http.StatusText(response.Code), message.SenderID, message.ID, message.ConversationID)
}
