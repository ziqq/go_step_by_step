package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestPublicReply(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"success", nil, "ok"},
		{"invalid", errInvalid, "invalid_command"},
		{"forbidden", errForbidden, "forbidden"},
		{"deadline", context.DeadlineExceeded, "deadline_exceeded"},
		{"canceled", context.Canceled, "canceled"},
		{"wrapped", fmt.Errorf("private tenant detail: %w", errForbidden), "forbidden"},
		{"same text different error", errors.New("access denied"), "internal"},
		{"unknown", errors.New("secret connection string"), "internal"},
		{"invalid wins", errors.Join(context.Canceled, errInvalid, errForbidden), "invalid_command"},
		{"forbidden wins", errors.Join(context.DeadlineExceeded, errForbidden), "forbidden"},
		{"deadline wins", errors.Join(context.Canceled, context.DeadlineExceeded), "deadline_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := replyFor("req-1", tc.err)
			if got != (reply{ID: "req-1", Code: tc.code}) {
				t.Fatalf("got %+v", got)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"id":"req-1","code":"` + tc.code + `"}`
			if string(encoded) != want {
				t.Fatalf("public JSON = %s, want %s", encoded, want)
			}
		})
	}
}

type privateError struct{}

func (privateError) Error() string { panic("public mapping must not render private error") }

func TestMappingDoesNotRenderError(t *testing.T) {
	if got := replyFor("req-2", privateError{}); got.Code != "internal" {
		t.Fatalf("got %+v", got)
	}
}
