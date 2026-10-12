package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateRequiresPrincipalBeforeAuthorization(t *testing.T) {
	storeCalls := 0
	authorizationCalls := 0
	handler := newRouter(
		func(context.Context, string, string) (bool, error) {
			authorizationCalls++
			return true, nil
		},
		func(context.Context, string, createMessageRequest) (Message, error) {
			storeCalls++
			return Message{ID: 1}, nil
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(
		`{"conversation_id":"chat-1","text":"hello"}`,
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if got := response.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("missing WWW-Authenticate challenge")
	}
	if authorizationCalls != 0 || storeCalls != 0 {
		t.Fatalf("authorization/store calls = (%d, %d), want (0, 0)", authorizationCalls, storeCalls)
	}
}

func TestCreateDeniesNonMemberBeforeStore(t *testing.T) {
	storeCalls := 0
	handler := newRouter(
		func(_ context.Context, userID, conversationID string) (bool, error) {
			if userID != "mallory" || conversationID != "chat-1" {
				t.Fatalf("membership scope = (%q, %q), want (mallory, chat-1)", userID, conversationID)
			}
			return false, nil
		},
		func(context.Context, string, createMessageRequest) (Message, error) {
			storeCalls++
			return Message{}, nil
		},
	)
	request := authenticatedRequest("mallory", `{"conversation_id":"chat-1","text":"hello"}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertJSONError(t, response, http.StatusForbidden)
	if storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0 for a non-member", storeCalls)
	}
}

func TestCreateUsesPrincipalInsteadOfClientIdentity(t *testing.T) {
	storeCalls := 0
	handler := newRouter(
		func(_ context.Context, userID, _ string) (bool, error) {
			return userID == "alice", nil
		},
		func(context.Context, string, createMessageRequest) (Message, error) {
			storeCalls++
			return Message{}, nil
		},
	)
	request := authenticatedRequest("alice", `{"conversation_id":"chat-1","text":"hello","user_id":"mallory"}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertJSONError(t, response, http.StatusBadRequest)
	if storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0 for a forged identity field", storeCalls)
	}
}

func TestCreateFailsClosedWhenMembershipCheckErrors(t *testing.T) {
	storeCalls := 0
	handler := newRouter(
		func(context.Context, string, string) (bool, error) {
			return false, errors.New("private database detail")
		},
		func(context.Context, string, createMessageRequest) (Message, error) {
			storeCalls++
			return Message{}, nil
		},
	)
	request := authenticatedRequest("alice", `{"conversation_id":"chat-1","text":"hello"}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertJSONError(t, response, http.StatusInternalServerError)
	if strings.Contains(response.Body.String(), "private database detail") {
		t.Fatalf("response leaked authorization error: %s", response.Body)
	}
	if storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0 when authorization fails", storeCalls)
	}
}

func TestCreateWritesAfterMembershipAndUsesVerifiedSender(t *testing.T) {
	store := &MemoryMessageStore{}
	var checkedUser, checkedConversation string
	handler := newRouter(
		func(_ context.Context, userID, conversationID string) (bool, error) {
			checkedUser, checkedConversation = userID, conversationID
			return userID == "alice" && conversationID == "chat-1", nil
		},
		store.create,
	)
	request := authenticatedRequest("alice", `{"conversation_id":" chat-1 ","text":" hello "}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body)
	}
	if checkedUser != "alice" || checkedConversation != "chat-1" {
		t.Fatalf("membership checked (%q, %q), want (alice, chat-1)", checkedUser, checkedConversation)
	}
	var message Message
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	if message.ID != 1 || message.SenderID != "alice" || message.ConversationID != "chat-1" || message.Text != "hello" {
		t.Fatalf("created message = %#v, want normalized message from verified principal", message)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"conversation_id":`},
		{name: "trailing JSON", body: `{"conversation_id":"chat-1","text":"hello"} {}`},
		{name: "missing conversation", body: `{"conversation_id":" ","text":"hello"}`},
		{name: "blank text", body: `{"conversation_id":"chat-1","text":"  "}`},
		{name: "too long in Unicode code points", body: `{"conversation_id":"chat-1","text":"` + strings.Repeat("я", maxMessageRunes+1) + `"}`},
		{name: "oversized body", body: strings.Repeat("x", maxRequestBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storeCalls := 0
			handler := newRouter(
				func(context.Context, string, string) (bool, error) { return true, nil },
				func(context.Context, string, createMessageRequest) (Message, error) {
					storeCalls++
					return Message{}, nil
				},
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, authenticatedRequest("alice", test.body))
			assertJSONError(t, response, http.StatusBadRequest)
			if storeCalls != 0 {
				t.Fatalf("store calls = %d, want 0 for invalid input", storeCalls)
			}
		})
	}
}

func TestCreateAcceptsMaximumUnicodeLength(t *testing.T) {
	handler := newRouter(
		func(context.Context, string, string) (bool, error) { return true, nil },
		func(_ context.Context, senderID string, draft createMessageRequest) (Message, error) {
			return Message{ID: 1, SenderID: senderID, ConversationID: draft.ConversationID, Text: draft.Text}, nil
		},
	)
	body := `{"conversation_id":"chat-1","text":"` + strings.Repeat("я", maxMessageRunes) + `"}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest("alice", body))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body)
	}
}

func TestCreateRejectsWrongMethod(t *testing.T) {
	handler := newRouter(
		func(context.Context, string, string) (bool, error) { return true, nil },
		func(context.Context, string, createMessageRequest) (Message, error) { return Message{}, nil },
	)
	request := authenticatedRequest("alice", "")
	request.Method = http.MethodGet
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func authenticatedRequest(userID, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(body))
	return request.WithContext(withDemoPrincipal(request.Context(), Principal{UserID: userID}))
}

func assertJSONError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("response is not a JSON error: %s", response.Body)
	}
}
