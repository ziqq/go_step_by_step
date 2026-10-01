package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := newHandler()
	if err != nil {
		t.Fatalf("newHandler(): %v", err)
	}
	return handler
}

func reportRequest(handler http.Handler, token, tenantID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/tenants/"+tenantID+"/reports", nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestReportsHandlerReturnsTenantScopedJSON(t *testing.T) {
	response := reportRequest(newTestHandler(t), "viewer-demo-token", "acme")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	var body struct {
		TenantID string `json:"tenant_id"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.TenantID != "acme" || body.Message == "" {
		t.Fatalf("response = %#v", body)
	}
}

func TestAuthenticationRejectsMissingInvalidAndMalformedCredentials(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*http.Request)
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{
			name: "invalid token", wantStatus: http.StatusUnauthorized,
			configure: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer unknown-token")
			},
		},
		{
			name: "unsupported scheme", wantStatus: http.StatusUnauthorized,
			configure: func(request *http.Request) {
				request.Header.Set("Authorization", "Basic user:password")
			},
		},
		{
			name: "duplicate header", wantStatus: http.StatusBadRequest,
			configure: func(request *http.Request) {
				request.Header.Add("Authorization", "Bearer viewer-demo-token")
				request.Header.Add("Authorization", "Bearer writer-demo-token")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTestHandler(t)
			request := httptest.NewRequest(http.MethodGet, "/tenants/acme/reports", nil)
			if tt.configure != nil {
				tt.configure(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if !strings.Contains(response.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Fatalf("WWW-Authenticate = %q, want Bearer challenge", response.Header().Get("WWW-Authenticate"))
			}
			if tt.name == "unsupported scheme" && strings.Contains(response.Header().Get("WWW-Authenticate"), "error=") {
				t.Fatalf("unsupported scheme challenge has an error code: %q", response.Header().Get("WWW-Authenticate"))
			}
			if strings.Contains(response.Body.String(), "viewer-demo-token") ||
				strings.Contains(response.Body.String(), "password") {
				t.Fatalf("error response leaked credentials: %s", response.Body)
			}
		})
	}
}

func TestAuthorizationRejectsAnotherTenantAndDisallowedRole(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		tenant string
	}{
		{name: "another tenant", token: "other-tenant-demo-token", tenant: "acme"},
		{name: "disallowed role", token: "disabled-demo-token", tenant: "acme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := reportRequest(newTestHandler(t), tt.token, tt.tenant)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
		})
	}
}

func TestRateLimitReturnsRetryAfter(t *testing.T) {
	handler := newTestHandler(t)
	for range 2 {
		response := reportRequest(handler, "viewer-demo-token", "acme")
		if response.Code != http.StatusOK {
			t.Fatalf("allowed request status = %d, want %d", response.Code, http.StatusOK)
		}
	}
	response := reportRequest(handler, "viewer-demo-token", "acme")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("limited request status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	seconds, err := strconv.Atoi(response.Header().Get("Retry-After"))
	if err != nil || seconds < 1 || seconds > 60 {
		t.Fatalf("Retry-After = %q, want an integer from 1 through 60", response.Header().Get("Retry-After"))
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	limiter, err := newRateLimiter(1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	if allowed, _ := limiter.allow("user"); !allowed {
		t.Fatal("first request was denied")
	}
	if allowed, retryAfter := limiter.allow("user"); allowed || retryAfter <= 0 {
		t.Fatalf("second request = (%v, %v), want denied with retry delay", allowed, retryAfter)
	}
	now = now.Add(time.Minute)
	if allowed, _ := limiter.allow("user"); !allowed {
		t.Fatal("request after the window was denied")
	}
}

func TestRateLimiterIsSafeUnderConcurrentRequests(t *testing.T) {
	const limit = 10
	limiter, err := newRateLimiter(limit, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Int32
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if ok, _ := limiter.allow("same-user"); ok {
				allowed.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := allowed.Load(); got != limit {
		t.Fatalf("allowed requests = %d, want %d", got, limit)
	}
}

func TestNewRateLimiterRejectsInvalidConfiguration(t *testing.T) {
	for _, tt := range []struct {
		limit  int
		window time.Duration
	}{
		{limit: 0, window: time.Minute},
		{limit: 1, window: 0},
	} {
		if _, err := newRateLimiter(tt.limit, tt.window); err != ErrInvalidRateLimit {
			t.Errorf("newRateLimiter(%d, %s) error = %v, want ErrInvalidRateLimit", tt.limit, tt.window, err)
		}
	}
}
