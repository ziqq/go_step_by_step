package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginGate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		origins []string
		allowed bool
	}{
		{"chat", []string{"https://chat.example.test"}, true},
		{"admin", []string{"https://admin.example.test"}, true},
		{"missing", nil, false},
		{"empty", []string{""}, false},
		{"opaque", []string{"null"}, false},
		{"wildcard", []string{"*"}, false},
		{"wrong scheme", []string{"http://chat.example.test"}, false},
		{"wrong port", []string{"https://chat.example.test:8443"}, false},
		{"explicit default port", []string{"https://chat.example.test:443"}, false},
		{"host suffix", []string{"https://chat.example.test.attacker.test"}, false},
		{"user info", []string{"https://chat.example.test@attacker.test"}, false},
		{"path", []string{"https://chat.example.test/"}, false},
		{"query", []string{"https://chat.example.test?x=1"}, false},
		{"two lines", []string{"https://chat.example.test", "https://admin.example.test"}, false},
		{"repeated line", []string{"https://chat.example.test", "https://chat.example.test"}, false},
		{"joined list", []string{"https://chat.example.test, https://admin.example.test"}, false},
		{"space list", []string{"https://chat.example.test https://admin.example.test"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			handler := requireBrowserOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called++
				w.WriteHeader(http.StatusNoContent)
			}))
			r := httptest.NewRequest(http.MethodGet, "https://api.example.test/ws", nil)
			for _, origin := range tc.origins {
				r.Header.Add("Origin", origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			wantStatus, wantCalls := http.StatusForbidden, 0
			if tc.allowed {
				wantStatus, wantCalls = http.StatusNoContent, 1
			}
			if w.Code != wantStatus || called != wantCalls {
				t.Fatalf("status = %d, calls = %d, want %d and %d", w.Code, called, wantStatus, wantCalls)
			}
			if !tc.allowed && w.Body.String() != "origin denied\n" {
				t.Fatalf("public error = %q", w.Body.String())
			}
		})
	}
}

func TestOriginGateDoesNotTrustForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://chat.example.test/ws", nil)
	r.Header.Set("Origin", "https://attacker.test")
	r.Header.Set("X-Forwarded-Host", "chat.example.test")
	r.Header.Set("X-Forwarded-Proto", "https")
	if browserOriginAllowed(r) {
		t.Fatal("request routing headers must not grant origin access")
	}
}
