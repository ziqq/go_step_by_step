package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func lookupFrom(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		want    Config
		wantErr bool
	}{
		{name: "defaults", want: Config{Environment: "development", Port: 8080}},
		{
			name:   "production override",
			values: map[string]string{"APP_ENV": "production", "PORT": "9090"},
			want:   Config{Environment: "production", Port: 9090},
		},
		{name: "unknown environment", values: map[string]string{"APP_ENV": "prod"}, wantErr: true},
		{name: "non-numeric port", values: map[string]string{"PORT": "http"}, wantErr: true},
		{name: "zero port", values: map[string]string{"PORT": "0"}, wantErr: true},
		{name: "port too large", values: map[string]string{"PORT": "65536"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadConfig(lookupFrom(tt.values))
			if tt.wantErr {
				if err == nil {
					t.Fatal("loadConfig() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadConfig() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("loadConfig() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestHealthEndpoints(t *testing.T) {
	readiness := &readinessState{}
	handler := newHandler(readiness)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("liveness response = %d %s", response.Code, response.Body)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}

	readiness.ready.Store(true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready response = %d %s", response.Code, response.Body)
	}
}

func TestCheckReadiness(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "ready", statusCode: http.StatusOK},
		{name: "not ready", statusCode: http.StatusServiceUnavailable, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			err := checkReadiness(context.Background(), server.Client(), server.URL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkReadiness() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
