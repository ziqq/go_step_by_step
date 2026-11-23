package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
)

// browserOriginAllowed checks a browser-only endpoint with a fixed trusted list.
func browserOriginAllowed(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	switch origins[0] {
	case "https://chat.example.test", "https://admin.example.test":
		return true
	default:
		return false
	}
}

func requireBrowserOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !browserOriginAllowed(r) {
			http.Error(w, "origin denied", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	// A 204 stand-in shows whether the gate reached the upgrade boundary.
	handler := requireBrowserOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, origin := range []string{"https://chat.example.test", "https://chat.example.test.attacker.test"} {
		request := httptest.NewRequest(http.MethodGet, "https://api.example.test/ws", nil)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		fmt.Println(response.Code)
	}
}
