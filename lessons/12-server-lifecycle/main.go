// Package main demonstrates configuration, structured logging, and HTTP shutdown.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func loadConfig(lookup func(string) (string, bool)) (Config, error) {
	config := Config{
		Address:           ":8080",
		ReadHeaderTimeout: 5 * time.Second,
		ShutdownTimeout:   10 * time.Second,
	}

	if value, ok := lookup("APP_ADDR"); ok {
		config.Address = strings.TrimSpace(value)
	}
	if value, ok := lookup("APP_READ_HEADER_TIMEOUT"); ok {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("APP_READ_HEADER_TIMEOUT must be a positive duration")
		}
		config.ReadHeaderTimeout = parsed
	}
	if value, ok := lookup("APP_SHUTDOWN_TIMEOUT"); ok {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("APP_SHUTDOWN_TIMEOUT must be a positive duration")
		}
		config.ShutdownTimeout = parsed
	}

	_, port, err := net.SplitHostPort(config.Address)
	if err != nil {
		return Config{}, fmt.Errorf("APP_ADDR must be a host:port address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf("APP_ADDR must contain a port from 1 to 65535")
	}

	return config, nil
}

func newHandler(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
		}{Status: "ok"})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, apiError{
			Code:    "not_found",
			Message: "Resource not found",
		})
	})

	return requestLogger(logger, mux)
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(started),
		)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func run(ctx context.Context, config Config, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", config.Address, err)
	}
	logger.Info("http server listening", "address", listener.Addr().String())
	return serve(ctx, listener, newHandler(logger), config, logger)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler, config Config, logger *slog.Logger) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", err)
		}
		logger.Info("http server stopped")
		return nil
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := execute(logger); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func execute(logger *slog.Logger) error {
	config, err := loadConfig(os.LookupEnv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, config, logger)
}
