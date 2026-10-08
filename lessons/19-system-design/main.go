// Package main demonstrates an HTTP contract for cursor-paginated messages.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"time"
)

const (
	defaultPageLimit = 20
	maxPageLimit     = 50
)

var (
	ErrConversationRequired = errors.New("conversation ID is required")
	ErrInvalidPageLimit     = errors.New("page limit is invalid")
	ErrInvalidCursor        = errors.New("cursor is invalid")
)

type Message struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversation_id"`
	CreatedAt      time.Time `json:"created_at"`
	Text           string    `json:"text"`
}

type MessagePage struct {
	Messages   []Message `json:"messages"`
	NextCursor string    `json:"next_cursor"`
}

type pageCursor struct {
	ConversationID string    `json:"conversation_id"`
	CreatedAt      time.Time `json:"created_at"`
	ID             int64     `json:"id"`
}

func pageMessages(messages []Message, conversationID, token string, limit int) (MessagePage, error) {
	if conversationID == "" {
		return MessagePage{}, ErrConversationRequired
	}
	if limit < 1 || limit > maxPageLimit {
		return MessagePage{}, ErrInvalidPageLimit
	}

	var cursor *pageCursor
	if token != "" {
		decoded, err := decodeCursor(token)
		if err != nil || decoded.ConversationID != conversationID {
			return MessagePage{}, ErrInvalidCursor
		}
		cursor = &decoded
	}

	filtered := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.ConversationID == conversationID && (cursor == nil || olderThanCursor(message, *cursor)) {
			filtered = append(filtered, message)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		left, right := filtered[i], filtered[j]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID > right.ID
	})

	page := MessagePage{Messages: make([]Message, 0, limit)}
	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}
	page.Messages = append(page.Messages, filtered...)
	if hasMore {
		last := page.Messages[len(page.Messages)-1]
		encoded, err := encodeCursor(pageCursor{
			ConversationID: conversationID,
			CreatedAt:      last.CreatedAt,
			ID:             last.ID,
		})
		if err != nil {
			return MessagePage{}, fmt.Errorf("encode next cursor: %w", err)
		}
		page.NextCursor = encoded
	}
	return page, nil
}

func olderThanCursor(message Message, cursor pageCursor) bool {
	if message.CreatedAt.Equal(cursor.CreatedAt) {
		return message.ID < cursor.ID
	}
	return message.CreatedAt.Before(cursor.CreatedAt)
}

func encodeCursor(cursor pageCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(token string) (pageCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return pageCursor{}, ErrInvalidCursor
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cursor pageCursor
	if err := decoder.Decode(&cursor); err != nil {
		return pageCursor{}, ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return pageCursor{}, ErrInvalidCursor
	}
	if cursor.ConversationID == "" || cursor.CreatedAt.IsZero() || cursor.ID <= 0 {
		return pageCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func newRouter(messages []Message) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /messages", func(w http.ResponseWriter, r *http.Request) {
		limit := defaultPageLimit
		if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid page parameters")
				return
			}
			limit = parsed
		}

		page, err := pageMessages(messages, r.URL.Query().Get("conversation_id"), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid page parameters")
			return
		}
		writeJSON(w, http.StatusOK, page)
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

func fetchPage(client *http.Client, endpoint string, query url.Values) (MessagePage, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return MessagePage{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return MessagePage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return MessagePage{}, fmt.Errorf("GET messages: unexpected status %s", response.Status)
	}

	var page MessagePage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		return MessagePage{}, fmt.Errorf("decode message page: %w", err)
	}
	return page, nil
}

func demoMessages() []Message {
	return []Message{
		{ID: 1, ConversationID: "chat-1", CreatedAt: time.Date(2026, time.January, 5, 10, 0, 0, 0, time.UTC), Text: "earlier"},
		{ID: 2, ConversationID: "chat-1", CreatedAt: time.Date(2026, time.January, 5, 10, 2, 0, 0, time.UTC), Text: "newer, first ID"},
		{ID: 3, ConversationID: "chat-1", CreatedAt: time.Date(2026, time.January, 5, 10, 2, 0, 0, time.UTC), Text: "newer, second ID"},
		{ID: 4, ConversationID: "chat-2", CreatedAt: time.Date(2026, time.January, 5, 10, 3, 0, 0, time.UTC), Text: "another conversation"},
	}
}

func main() {
	server := httptest.NewServer(newRouter(demoMessages()))
	defer server.Close()

	client := server.Client()
	query := url.Values{"conversation_id": {"chat-1"}, "limit": {"2"}}
	first, err := fetchPage(client, server.URL+"/messages", query)
	if err != nil {
		fmt.Println("load first page:", err)
		return
	}
	fmt.Printf("page 1: %d messages, next page=%t\n", len(first.Messages), first.NextCursor != "")

	query.Set("cursor", first.NextCursor)
	second, err := fetchPage(client, server.URL+"/messages", query)
	if err != nil {
		fmt.Println("load second page:", err)
		return
	}
	fmt.Printf("page 2: %d messages, next page=%t\n", len(second.Messages), second.NextCursor != "")
}
