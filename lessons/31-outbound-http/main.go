// Package main demonstrates bounded outbound HTTP reads from a trusted endpoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"
)

const maxSnapshotBytes = 1024

var (
	ErrUpstreamStatus   = errors.New("unexpected upstream status")
	ErrResponseTooLarge = errors.New("upstream response exceeds 1024 bytes")
)

type snapshotClient struct {
	http *http.Client
}

func newSnapshotClient() *snapshotClient {
	return &snapshotClient{http: &http.Client{
		Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// fetch accepts only a trusted, configured endpoint, not a URL from HTTP input.
// It returns bytes, not proof of a valid JSON document or a healthy dependency.
func (c *snapshotClient) fetch(ctx context.Context, endpoint string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create snapshot request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request snapshot: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", ErrUpstreamStatus, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSnapshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	if len(body) > maxSnapshotBytes {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func run(output io.Writer) error {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"service\":\"notifications\",\"pending\":3}\n")
	}))
	defer server.Close()
	client := newSnapshotClient()
	defer client.http.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body, err := client.fetch(ctx, server.URL)
	if err != nil {
		return err
	}
	_, err = output.Write(body)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
