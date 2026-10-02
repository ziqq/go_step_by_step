// Package main demonstrates environment configuration, HTTP probes, and container-friendly shutdown.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type Config struct {
	Environment string
	Port        int
}

type readinessState struct {
	ready atomic.Bool
}

func loadConfig(lookup func(string) (string, bool)) (Config, error) {
	config := Config{Environment: "development", Port: 8080}
	if value, ok := lookup("APP_ENV"); ok {
		config.Environment = strings.TrimSpace(value)
	}
	switch config.Environment {
	case "development", "test", "staging", "production":
	default:
		return Config{}, fmt.Errorf("APP_ENV must be development, test, staging, or production")
	}

	if value, ok := lookup("PORT"); ok {
		port, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("PORT must be an integer from 1 to 65535")
		}
		config.Port = port
	}
	return config, nil
}

func newHandler(readiness *readinessState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
		}{Status: "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !readiness.ready.Load() {
			writeJSON(w, http.StatusServiceUnavailable, struct {
				Status string `json:"status"`
			}{Status: "not_ready"})
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
		}{Status: "ready"})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			writeJSON(w, http.StatusNotFound, struct {
				Error string `json:"error"`
			}{Error: "not_found"})
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Service string `json:"service"`
		}{Service: "go-step-by-step"})
	})
	return mux
}

func run(ctx context.Context, config Config) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", config.Port, err)
	}

	readiness := &readinessState{}
	server := &http.Server{
		Handler:           newHandler(readiness),
		ReadHeaderTimeout: 5 * time.Second,
	}
	readiness.ready.Store(true)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	select {
	case err := <-serveErrors:
		readiness.ready.Store(false)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		readiness.ready.Store(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", err)
		}
		return nil
	}
}

func checkReadiness(ctx context.Context, client *http.Client, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create readiness request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request readiness endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness endpoint returned status %d", response.StatusCode)
	}
	return nil
}

func runHealthcheck(port int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/readyz", port)
	return checkReadiness(ctx, client, endpoint)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func execute() error {
	config, err := loadConfig(os.LookupEnv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("starting environment=%s port=%d", config.Environment, config.Port)
	return run(ctx, config)
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		config, err := loadConfig(os.LookupEnv)
		if err == nil {
			err = runHealthcheck(config.Port)
		}
		if err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		log.Print("usage: service [healthcheck]")
		os.Exit(2)
	}
	if err := execute(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
