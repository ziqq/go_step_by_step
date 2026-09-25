package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCreateMessage(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/messages",
		bytes.NewBufferString(`{"text":"  hello  "}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newRouter(newMessageStore()).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}

	var got Message
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := (Message{ID: 1, Text: "hello"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestListMessages(t *testing.T) {
	store := newMessageStore()
	router := newRouter(store)
	for _, text := range []string{"first", "second"} {
		request := httptest.NewRequest(
			http.MethodPost,
			"/messages",
			bytes.NewBufferString(`{"text":"`+text+`"}`),
		)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("create status = %d, want %d", response.Code, http.StatusCreated)
		}
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/messages", nil))

	var got []Message
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	want := []Message{{ID: 1, Text: "first"}, {ID: 2, Text: "second"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
}

func TestCreateMessageRejectsInvalidBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"text":`},
		{name: "unknown field", body: `{"text":"hello","author":"ana"}`},
		{name: "trailing JSON", body: `{"text":"hello"}{"text":"again"}`},
		{name: "empty text", body: `{"text":"   "}`},
		{name: "text too long", body: `{"text":"` + strings.Repeat("x", maxMessageLength+1) + `"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(tt.body))
			response := httptest.NewRecorder()
			newRouter(newMessageStore()).ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.Error == "" {
				t.Fatal("error response has empty message")
			}
		})
	}
}

func TestRouterRejectsUnsupportedMethod(t *testing.T) {
	response := httptest.NewRecorder()
	newRouter(newMessageStore()).ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/messages", nil))

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}
