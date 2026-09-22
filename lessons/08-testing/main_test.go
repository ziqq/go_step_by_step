package main

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr error
	}{
		{name: "empty input", input: "   ", want: []string{}},
		{name: "trim lower and deduplicate", input: "Go, backend, go, chat", want: []string{"go", "backend", "chat"}},
		{name: "ignore empty parts", input: "api,, go, ", want: []string{"api", "go"}},
		{name: "reject whitespace inside tag", input: "go lang", wantErr: ErrInvalidTag},
		{name: "reject punctuation", input: "go!", wantErr: ErrInvalidTag},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeTags(tt.input)
			if tt.wantErr != nil {
				if err == nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("normalizeTags() error = %v, want errors.Is(..., %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeTags() error = %v, want nil", err)
			}
			requireTags(t, got, tt.want)
		})
	}
}

func requireTags(t *testing.T, got, want []string) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
}

func TestNormalizeTagsOutputIsValidAndUnique(t *testing.T) {
	tags, err := normalizeTags("Go, Backend, go, chat_api")
	if err != nil {
		t.Fatalf("normalizeTags() error = %v", err)
	}

	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if !regexp.MustCompile("^[a-z0-9][a-z0-9_-]*$").MatchString(tag) {
			t.Fatalf("tag %q does not match the contract", tag)
		}
		if _, exists := seen[tag]; exists {
			t.Fatalf("tag %q is repeated", tag)
		}
		seen[tag] = struct{}{}
	}
}

func BenchmarkNormalizeTags(b *testing.B) {
	input := strings.Repeat("backend,go,chat,backend,api,", 100)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = normalizeTags(input)
	}
}

func FuzzNormalizeTagsNeverPanics(f *testing.F) {
	f.Add("Go,backend,chat")
	f.Add("   ")
	f.Add("go!,backend")

	f.Fuzz(func(t *testing.T, input string) {
		tags, err := normalizeTags(input)
		if err != nil {
			return
		}

		seen := make(map[string]struct{}, len(tags))
		for _, tag := range tags {
			if !tagPattern.MatchString(tag) {
				t.Fatalf("tag %q does not match the contract", tag)
			}
			if _, exists := seen[tag]; exists {
				t.Fatalf("tag %q is repeated", tag)
			}
			seen[tag] = struct{}{}
		}
	})
}
