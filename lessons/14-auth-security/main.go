// Package main demonstrates HTTP authentication, tenant authorization, and rate limiting.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxBearerTokenBytes = 16 << 10

var ErrInvalidRateLimit = errors.New("rate limit and window must be positive")

type Identity struct {
	UserID   string
	TenantID string
}

type Principal struct {
	Identity
	Role string
}

type tokenVerifier func(token string) (Identity, bool)
type roleResolver func(identity Identity) (string, bool)

type identityContextKey struct{}
type principalContextKey struct{}

type rateBucket struct {
	started time.Time
	count   int
}

type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	buckets map[string]rateBucket
}

var demoIdentities = map[string]Identity{
	"viewer-demo-token":       {UserID: "user-1", TenantID: "acme"},
	"writer-demo-token":       {UserID: "user-2", TenantID: "acme"},
	"other-tenant-demo-token": {UserID: "user-3", TenantID: "other"},
	"disabled-demo-token":     {UserID: "user-4", TenantID: "acme"},
}

var demoRoles = map[string]string{
	"user-1": "viewer",
	"user-2": "writer",
	"user-3": "admin",
	"user-4": "disabled",
}

func verifyDemoToken(token string) (Identity, bool) {
	identity, ok := demoIdentities[token]
	return identity, ok
}

func resolveDemoRole(identity Identity) (string, bool) {
	role, ok := demoRoles[identity.UserID]
	return role, ok
}

func authenticate(verify tokenVerifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Values("Authorization")
		if len(authorization) == 0 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="expense-api"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if len(authorization) != 1 {
			writeInvalidAuthorization(w)
			return
		}

		scheme, token, found := strings.Cut(authorization[0], " ")
		if !found || scheme == "" {
			writeInvalidAuthorization(w)
			return
		}
		if !strings.EqualFold(scheme, "Bearer") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="expense-api"`)
			writeError(w, http.StatusUnauthorized, "Bearer authentication required")
			return
		}
		if token == "" || len(token) > maxBearerTokenBytes || strings.ContainsAny(token, " \t\r\n") {
			writeInvalidAuthorization(w)
			return
		}

		identity, ok := verify(token)
		if !ok || identity.UserID == "" || identity.TenantID == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="expense-api", error="invalid_token"`)
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		ctx := context.WithValue(r.Context(), identityContextKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeInvalidAuthorization(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="expense-api", error="invalid_request"`)
	writeError(w, http.StatusBadRequest, "invalid Authorization header")
}

func authorizeTenantAndRoles(resolve roleResolver, roles []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="expense-api"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		if r.PathValue("tenantID") != identity.TenantID {
			writeError(w, http.StatusForbidden, "access denied")
			return
		}

		role, found := resolve(identity)
		roleAllowed := false
		for _, allowedRole := range roles {
			if found && role == allowedRole {
				roleAllowed = true
				break
			}
		}
		if !roleAllowed {
			writeError(w, http.StatusForbidden, "access denied")
			return
		}
		principal := Principal{Identity: identity, Role: role}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func identityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}

func principalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func newRateLimiter(limit int, window time.Duration) (*rateLimiter, error) {
	if limit < 1 || window <= 0 {
		return nil, ErrInvalidRateLimit
	}
	return &rateLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: make(map[string]rateBucket),
	}, nil
}

func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.buckets[key]
	if bucket.started.IsZero() || !now.Before(bucket.started.Add(l.window)) {
		bucket = rateBucket{started: now}
	}
	if bucket.count >= l.limit {
		l.buckets[key] = bucket
		return false, bucket.started.Add(l.window).Sub(now)
	}
	bucket.count++
	l.buckets[key] = bucket
	return true, 0
}

func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="expense-api"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		key := principal.TenantID + "\x00" + principal.UserID
		allowed, retryAfter := l.allow(key)
		if !allowed {
			seconds := int((retryAfter + time.Second - 1) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newHandler() (http.Handler, error) {
	limiter, err := newRateLimiter(2, time.Minute)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	reports := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, _ := principalFromContext(r.Context())
		writeJSON(w, http.StatusOK, struct {
			TenantID string `json:"tenant_id"`
			Message  string `json:"message"`
		}{TenantID: principal.TenantID, Message: "reports are available"})
	})
	protectedReports := authenticate(
		verifyDemoToken,
		authorizeTenantAndRoles(
			resolveDemoRole,
			[]string{"viewer", "writer", "admin"},
			limiter.middleware(reports),
		),
	)
	mux.Handle("GET /tenants/{tenantID}/reports", protectedReports)
	return mux, nil
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
	handler, err := newHandler()
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(":8080", handler))
}
