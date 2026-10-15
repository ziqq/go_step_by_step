// Package main demonstrates contract tests at a chat history HTTP boundary.
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
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrNotMember = errors.New("user is not an active conversation member")

type Principal struct{ UserID string }
type principalKey struct{}

// withDemoPrincipal simulates identity already verified by trusted middleware.
// It does not authenticate a token or a real user.
func withDemoPrincipal(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, principalKey{}, Principal{UserID: userID})
}

type Message struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Text           string    `json:"text"`
	CreatedAt      time.Time `json:"created_at"`
}

type Cursor struct {
	CreatedAt time.Time `json:"created_at"`
	MessageID int64     `json:"message_id"`
}

type HistoryPage struct {
	Messages []Message
	Next     *Cursor
	HasMore  bool
}

// historyLoader has the argument/result shape of lesson 23's ListPage.
// Its implementation owns membership checks and conversation-scoped storage.
type historyLoader func(context.Context, string, string, *Cursor, int) (HistoryPage, error)

type historyResponse struct {
	Messages   []Message `json:"messages"`
	HasMore    bool      `json:"has_more"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newRouter(load historyLoader) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /conversations/{conversationID}/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		principal, ok := r.Context().Value(principalKey{}).(Principal)
		if !ok || strings.TrimSpace(principal.UserID) == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="chat"`)
			writeJSON(w, http.StatusUnauthorized, errorResponse{"authentication_required", "authentication required"})
			return
		}

		conversationID := strings.TrimSpace(r.PathValue("conversationID"))
		limit, before, err := parsePageQuery(r.URL.RawQuery)
		if err != nil || conversationID == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{"invalid_page", "history page parameters are invalid"})
			return
		}
		if err := r.Context().Err(); err != nil {
			writeLoadError(w, err)
			return
		}
		if load == nil {
			writeLoadError(w, errors.New("history loader is missing"))
			return
		}

		page, err := load(r.Context(), principal.UserID, conversationID, before, limit)
		if contextErr := r.Context().Err(); contextErr != nil {
			writeLoadError(w, contextErr)
			return
		}
		if err != nil {
			writeLoadError(w, err)
			return
		}
		if page.HasMore && (page.Next == nil || len(page.Messages) == 0 ||
			page.Next.CreatedAt.IsZero() || page.Next.MessageID < 1) {
			writeLoadError(w, errors.New("history adapter returned an invalid next position"))
			return
		}
		if page.Messages == nil {
			page.Messages = []Message{}
		}
		response := historyResponse{Messages: page.Messages, HasMore: page.HasMore}
		if page.HasMore {
			response.NextCursor = encodeCursor(*page.Next)
		}
		writeJSON(w, http.StatusOK, response)
	})
	return mux
}

func parsePageQuery(rawQuery string) (int, *Cursor, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0, nil, err
	}
	for name := range query {
		if name != "limit" && name != "before" {
			return 0, nil, errors.New("unsupported history parameter")
		}
	}
	limit := 20
	if values, present := query["limit"]; present {
		if len(values) != 1 {
			return 0, nil, errors.New("limit must be provided once")
		}
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > 50 {
			return 0, nil, errors.New("limit must be between 1 and 50")
		}
	}
	values, present := query["before"]
	if !present {
		return limit, nil, nil
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > 256 {
		return 0, nil, errors.New("before must be one bounded cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil {
		return 0, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var before Cursor
	if err := decoder.Decode(&before); err != nil {
		return 0, nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return 0, nil, errors.New("cursor must contain exactly one JSON value")
	}
	if before.CreatedAt.IsZero() || before.MessageID < 1 {
		return 0, nil, errors.New("cursor position is invalid")
	}
	before.CreatedAt = before.CreatedAt.UTC()
	return limit, &before, nil
}

func encodeCursor(cursor Cursor) string {
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	raw, _ := json.Marshal(cursor) // Cursor has only JSON-supported field types.
	return base64.RawURLEncoding.EncodeToString(raw)
}

func writeLoadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		// A disconnected client has no useful response recipient.
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusGatewayTimeout, errorResponse{"history_timeout", "history request timed out"})
	case errors.Is(err, ErrNotMember):
		writeJSON(w, http.StatusForbidden, errorResponse{"forbidden", "conversation access denied"})
	default:
		writeJSON(w, http.StatusInternalServerError, errorResponse{"history_unavailable", "history could not be loaded"})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	load := func(ctx context.Context, userID, conversationID string, _ *Cursor, _ int) (HistoryPage, error) {
		if err := ctx.Err(); err != nil {
			return HistoryPage{}, err
		}
		if userID != "alice" || conversationID != "chat-1" {
			return HistoryPage{}, ErrNotMember
		}
		return HistoryPage{Messages: []Message{{
			ID: 1, ConversationID: conversationID, Text: "hello",
			CreatedAt: time.Date(2026, time.October, 15, 6, 0, 0, 0, time.UTC),
		}}}, nil
	}
	handler := newRouter(load)
	for _, userID := range []string{"alice", "mallory", ""} {
		request := httptest.NewRequest(http.MethodGet, "/conversations/chat-1/messages", nil)
		if userID != "" {
			request = request.WithContext(withDemoPrincipal(request.Context(), userID))
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		_ = response.Body.Close()
		fmt.Println(response.Status)
	}
}
