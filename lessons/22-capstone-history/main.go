// Package main demonstrates authorized, keyset-paginated message history.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPageSize = 20
	maxPageSize     = 50
	maxCursorBytes  = 256
)

type Principal struct {
	UserID string
}

type principalContextKey struct{}

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

type Message struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Text           string    `json:"text"`
	CreatedAt      time.Time `json:"created_at"`
}

type historyCursor struct {
	CreatedAt time.Time `json:"created_at"`
	MessageID int64     `json:"message_id"`
}

type historyPage struct {
	Messages   []Message `json:"messages"`
	NextCursor string    `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
}

type membershipChecker func(context.Context, string, string) (bool, error)

type messageHistoryStore interface {
	ListBefore(context.Context, string, *historyCursor, int) ([]Message, bool, error)
}

type MemoryMessageStore struct {
	mu       sync.RWMutex
	messages []Message
}

func newMemoryMessageStore(messages ...Message) *MemoryMessageStore {
	return &MemoryMessageStore{messages: append([]Message(nil), messages...)}
}

func (s *MemoryMessageStore) ListBefore(ctx context.Context, conversationID string, before *historyCursor, limit int) ([]Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > maxPageSize {
		return nil, false, errors.New("page size outside allowed range")
	}

	s.mu.RLock()
	candidates := append([]Message(nil), s.messages...)
	s.mu.RUnlock()

	messages := make([]Message, 0, len(candidates))
	for _, message := range candidates {
		if message.ConversationID != conversationID {
			continue
		}
		if before != nil && !isOlder(message, *before) {
			continue
		}
		messages = append(messages, message)
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].CreatedAt.Equal(messages[j].CreatedAt) {
			return messages[i].ID > messages[j].ID
		}
		return messages[i].CreatedAt.After(messages[j].CreatedAt)
	})

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return messages, hasMore, nil
}

func isOlder(message Message, cursor historyCursor) bool {
	if message.CreatedAt.Equal(cursor.CreatedAt) {
		return message.ID < cursor.MessageID
	}
	return message.CreatedAt.Before(cursor.CreatedAt)
}

func newRouter(checkMembership membershipChecker, store messageHistoryStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /conversations/{conversationID}/messages", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="chat"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		conversationID := strings.TrimSpace(r.PathValue("conversationID"))
		if conversationID == "" {
			writeError(w, http.StatusBadRequest, "conversation_id is required")
			return
		}

		limit, err := parsePageSize(r.URL.Query()["limit"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 50")
			return
		}
		before, err := parseCursor(r.URL.Query()["before"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "before cursor is invalid")
			return
		}
		if checkMembership == nil || store == nil {
			writeError(w, http.StatusInternalServerError, "message history unavailable")
			return
		}

		allowed, err := checkMembership(r.Context(), principal.UserID, conversationID)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				writeError(w, http.StatusGatewayTimeout, "authorization timed out")
				return
			}
			writeError(w, http.StatusInternalServerError, "message history could not be authorized")
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "conversation access denied")
			return
		}

		messages, hasMore, err := store.ListBefore(r.Context(), conversationID, before, limit)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				writeError(w, http.StatusGatewayTimeout, "message history timed out")
				return
			}
			writeError(w, http.StatusInternalServerError, "message history could not be loaded")
			return
		}
		if messages == nil {
			messages = []Message{}
		}

		page := historyPage{Messages: messages, HasMore: hasMore}
		if hasMore && len(messages) > 0 {
			page.NextCursor = encodeCursor(messages[len(messages)-1])
		}
		writeJSON(w, http.StatusOK, page)
	})
	return mux
}

func parsePageSize(values []string) (int, error) {
	if len(values) == 0 {
		return defaultPageSize, nil
	}
	if len(values) != 1 {
		return 0, errors.New("limit must be provided once")
	}
	limit, err := strconv.Atoi(values[0])
	if err != nil || limit < 1 || limit > maxPageSize {
		return 0, errors.New("limit outside allowed range")
	}
	return limit, nil
}

func parseCursor(values []string) (*historyCursor, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > maxCursorBytes {
		return nil, errors.New("cursor must be provided once and be bounded")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil {
		return nil, errors.New("decode cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var cursor historyCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, errors.New("parse cursor")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("cursor must contain one JSON value")
	}
	if cursor.CreatedAt.IsZero() || cursor.MessageID < 1 {
		return nil, errors.New("cursor position is invalid")
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return &cursor, nil
}

func encodeCursor(message Message) string {
	cursor := historyCursor{CreatedAt: message.CreatedAt.UTC(), MessageID: message.ID}
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
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
	base := time.Date(2026, time.October, 13, 6, 0, 0, 0, time.UTC)
	store := newMemoryMessageStore(
		Message{ID: 1, ConversationID: "chat-1", Text: "first", CreatedAt: base},
		Message{ID: 2, ConversationID: "chat-1", Text: "second", CreatedAt: base.Add(time.Second)},
		Message{ID: 3, ConversationID: "chat-1", Text: "third", CreatedAt: base.Add(2 * time.Second)},
	)
	checkMembership := func(_ context.Context, userID, conversationID string) (bool, error) {
		return userID == "alice" && conversationID == "chat-1", nil
	}
	request := httptest.NewRequest(http.MethodGet, "/conversations/chat-1/messages?limit=2", nil)
	request = request.WithContext(withDemoPrincipal(request.Context(), Principal{UserID: "alice"}))
	response := httptest.NewRecorder()
	newRouter(checkMembership, store).ServeHTTP(response, request)

	var page historyPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		fmt.Println("response error:", err)
		return
	}
	fmt.Printf("%d %s messages=%d has_more=%t\n", response.Code, http.StatusText(response.Code), len(page.Messages), page.HasMore)
}
