package main

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func TestSelectProtocol(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   string
	}{
		{"single", []string{"chat.v1"}, "chat.v1"},
		{"list", []string{"other.v1, chat.v1"}, "chat.v1"},
		{"multiple lines", []string{"other.v1", "chat.v1"}, "chat.v1"},
		{"optional whitespace", []string{" \tchat.v1\t "}, "chat.v1"},
		{"token punctuation", []string{"!#$%&'*+-.^_`|~, chat.v1"}, "chat.v1"},
		{"case distinct tokens", []string{"CHAT.V1, chat.v1"}, "chat.v1"},
		{"missing", nil, ""},
		{"empty", []string{""}, ""},
		{"whitespace", []string{" \t"}, ""},
		{"unsupported", []string{"chat.v2"}, ""},
		{"wrong case", []string{"Chat.v1"}, ""},
		{"suffix", []string{"chat.v1.extra"}, ""},
		{"duplicate", []string{"chat.v1, chat.v1"}, ""},
		{"duplicate across lines", []string{"chat.v1", "chat.v1"}, ""},
		{"unsupported duplicate", []string{"chat.v1, other, other"}, ""},
		{"leading comma", []string{",chat.v1"}, ""},
		{"trailing comma", []string{"chat.v1,"}, ""},
		{"empty middle", []string{"chat.v1,,other"}, ""},
		{"empty line after match", []string{"chat.v1", ""}, ""},
		{"slash after match", []string{"chat.v1, bad/token"}, ""},
		{"quoted", []string{"\"chat.v1\""}, ""},
		{"internal space", []string{"chat.v1, bad token"}, ""},
		{"newline", []string{"chat.v1\n"}, ""},
		{"unicode whitespace", []string{"\u00a0chat.v1"}, ""},
		{"unicode token", []string{"chat.v1, чат"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := append([]string(nil), tc.values...)
			got, err := selectProtocol(tc.values)
			if got != tc.want || (tc.want == "" && !errors.Is(err, errProtocol)) || (tc.want != "" && err != nil) {
				t.Fatalf("got (%q, %v), want %q", got, err, tc.want)
			}
			if !reflect.DeepEqual(before, tc.values) {
				t.Fatal("changed caller header values")
			}
		})
	}
}

func TestAllHeaderLinesAreValidated(t *testing.T) {
	header := make(http.Header)
	header.Add("Sec-WebSocket-Protocol", "chat.v1")
	header.Add("Sec-WebSocket-Protocol", "bad/token")
	if selected, err := selectProtocol(header.Values("Sec-WebSocket-Protocol")); selected != "" || !errors.Is(err, errProtocol) {
		t.Fatalf("accepted malformed later header: %q, %v", selected, err)
	}
}

func TestTokenBytes(t *testing.T) {
	const punctuation = "!#$%&'*+-.^_`|~"
	for b := 0; b < 256; b++ {
		want := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
		for _, allowed := range []byte(punctuation) {
			want = want || b == int(allowed)
		}
		if got := validToken(string([]byte{byte(b)})); got != want {
			t.Fatalf("byte %d: got %v, want %v", b, got, want)
		}
	}
}
