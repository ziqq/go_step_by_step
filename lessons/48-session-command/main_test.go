package main

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeCommandPreservesBody(t *testing.T) {
	got, err := decodeCommand([]byte(" \n" + `{"type":"send","conversation_id":7,"body":" привет "}` + "\t\n"))
	want := sendCommand{Type: "send", ConversationID: 7, Body: " привет "}
	if err != nil || got != want {
		t.Fatalf("command = %#v, error = %v, want %#v", got, err, want)
	}
}

func TestDecodeCommandRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"empty", ""},
		{"truncated", `{"type":"send"`},
		{"array", `[]`},
		{"string", `"send"`},
		{"unknown field", `{"type":"send","conversation_id":7,"body":"hi","user_id":99}`},
		{"wrong field type", `{"type":"send","conversation_id":"7","body":"hi"}`},
		{"fractional ID", `{"type":"send","conversation_id":7.5,"body":"hi"}`},
		{"overflow ID", `{"type":"send","conversation_id":9223372036854775808,"body":"hi"}`},
		{"second object", `{"type":"send","conversation_id":7,"body":"hi"}{}`},
		{"second null", `{"type":"send","conversation_id":7,"body":"hi"} null`},
		{"trailing junk", `{"type":"send","conversation_id":7,"body":"hi"} !`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeCommand([]byte(tc.payload))
			if err == nil || got != (sendCommand{}) {
				t.Fatalf("command = %#v, error = %v, want zero command and error", got, err)
			}
		})
	}
}

func TestDecodeCommandRequiresSendFields(t *testing.T) {
	for _, payload := range []string{
		`null`, `{}`, `{"type":"ping","conversation_id":7,"body":"hi"}`,
		`{"conversation_id":7,"body":"hi"}`, `{"type":"send","body":"hi"}`,
		`{"type":"send","conversation_id":0,"body":"hi"}`,
		`{"type":"send","conversation_id":-1,"body":"hi"}`,
		`{"type":"send","conversation_id":7}`, `{"type":"send","conversation_id":7,"body":null}`,
		`{"type":"send","conversation_id":7,"body":" \t\n"}`,
	} {
		got, err := decodeCommand([]byte(payload))
		if !errors.Is(err, errInvalidCommand) || got != (sendCommand{}) {
			t.Fatalf("input = %s, command = %#v, error = %v", payload, got, err)
		}
	}
}

func TestDecodeCommandPreservesTypedErrors(t *testing.T) {
	_, err := decodeCommand([]byte(`{"type":4}`))
	var typeError *json.UnmarshalTypeError
	if !errors.As(err, &typeError) {
		t.Fatalf("error = %v, want UnmarshalTypeError", err)
	}
	_, err = decodeCommand([]byte(`{"type":!}`))
	var syntaxError *json.SyntaxError
	if !errors.As(err, &syntaxError) {
		t.Fatalf("error = %v, want SyntaxError", err)
	}
	_, err = decodeCommand([]byte(`{"type":"send","conversation_id":7,"body":"hi"} null`))
	if !errors.Is(err, errTrailingJSON) {
		t.Fatalf("error = %v, want trailing JSON error", err)
	}
}
