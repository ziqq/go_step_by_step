// Package main demonstrates a small JSON HTTP API with validation and tests.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"unicode/utf8"
)

const maxMessageLength = 280

type Message struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type createMessageRequest struct {
	Text string `json:"text"`
}

type messageStore struct {
	mu       sync.RWMutex
	nextID   int
	messages []Message
}

func newMessageStore() *messageStore {
	return &messageStore{nextID: 1}
}

func (s *messageStore) create(text string) Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	message := Message{ID: s.nextID, Text: text}
	s.nextID++
	s.messages = append(s.messages, message)
	return message
}

func (s *messageStore) all() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	messages := make([]Message, len(s.messages))
	copy(messages, s.messages)
	return messages
}

func newRouter(store *messageStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /messages", listMessages(store))
	mux.HandleFunc("POST /messages", createMessage(store))
	return mux
}

func listMessages(store *messageStore) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.all())
	}
}

func createMessage(store *messageStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
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

		text := strings.TrimSpace(input.Text)
		switch {
		case text == "":
			writeError(w, http.StatusBadRequest, "text is required")
		case utf8.RuneCountInString(text) > maxMessageLength:
			writeError(w, http.StatusBadRequest, "text is too long")
		default:
			writeJSON(w, http.StatusCreated, store.create(text))
		}
	}
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
	server := httptest.NewServer(newRouter(newMessageStore()))
	defer server.Close()

	response, err := http.Post(
		server.URL+"/messages",
		"application/json",
		strings.NewReader(`{"text":"hello from HTTP"}`),
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
	fmt.Printf("%s id=%d text=%s\n", response.Status, message.ID, message.Text)
}
