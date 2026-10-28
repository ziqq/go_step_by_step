package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResponseByteBudget(t *testing.T) {
	for _, size := range []int{0, maxSnapshotBytes, maxSnapshotBytes + 1} {
		for _, chunked := range []bool{false, true} {
			t.Run(fmtCase(size, chunked), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" {
						t.Error("unexpected method or Accept header")
					}
					if chunked {
						w.(http.Flusher).Flush()
					}
					_, _ = io.WriteString(w, strings.Repeat("x", size))
				}))
				defer server.Close()
				client := testClient(t)
				body, err := client.fetch(context.Background(), server.URL)
				if size > maxSnapshotBytes {
					if body != nil || !errors.Is(err, ErrResponseTooLarge) {
						t.Fatalf("oversize = %q, %v", body, err)
					}
				} else if err != nil || string(body) != strings.Repeat("x", size) {
					t.Fatalf("bounded response = %q, %v", body, err)
				}
			})
		}
	}
}

func fmtCase(size int, chunked bool) string {
	return strconv.Itoa(size) + "_chunked_" + strconv.FormatBool(chunked)
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var visits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		visits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	body, err := testClient(t).fetch(context.Background(), redirect.URL)
	if body != nil || !errors.Is(err, ErrUpstreamStatus) || visits.Load() != 0 {
		t.Fatalf("redirect = %q, %v; target visits=%d", body, err, visits.Load())
	}
}

func TestCancellationBeforeHeadersAndDuringBody(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		t.Run(strconv.FormatBool(flushHeaders), func(t *testing.T) {
			entered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if flushHeaders {
					_, _ = io.WriteString(w, "partial")
					w.(http.Flusher).Flush()
				}
				close(entered)
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := testClient(t)
			client.http.Timeout = 3 * time.Second
			result := fetchAsync(client, ctx, server.URL)
			awaitSignal(t, entered)
			cancel()
			got := awaitResult(t, result)
			if got.body != nil || !errors.Is(got.err, context.Canceled) {
				t.Fatalf("cancellation = %q, %v", got.body, got.err)
			}
		})
	}
}

func TestClientTimeoutIncludesBody(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := testClient(t)
	client.http.Timeout = 150 * time.Millisecond
	result := fetchAsync(client, context.Background(), server.URL)
	awaitSignal(t, entered)
	got := awaitResult(t, result)
	if got.body != nil || !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("body timeout = %q, %v", got.body, got.err)
	}
}

func TestBodyBudgetAppliesAfterAutomaticGzipDecoding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = io.WriteString(writer, strings.Repeat("x", maxSnapshotBytes+1))
		_ = writer.Close()
	}))
	defer server.Close()
	body, err := testClient(t).fetch(context.Background(), server.URL)
	if body != nil || !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("decoded response = %q, %v", body, err)
	}
}

func TestBodiesCloseAndErrorsNeverReturnPartialData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reader io.Reader
		want   error
	}{
		{"success", 200, strings.NewReader("ok"), nil},
		{"status", 503, strings.NewReader("private upstream diagnostic"), ErrUpstreamStatus},
		{"no content is not a snapshot", 204, strings.NewReader(""), ErrUpstreamStatus},
		{"oversize", 200, strings.NewReader(strings.Repeat("x", 10000)), ErrResponseTooLarge},
		{"read failure", 200, &brokenReader{}, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedBody{Reader: tc.reader}
			client := testClient(t)
			client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: body, Header: make(http.Header)}, nil
			})
			data, err := client.fetch(context.Background(), "https://dependency.invalid/snapshot")
			if !errors.Is(err, tc.want) || !body.closed || body.read > maxSnapshotBytes+1 {
				t.Fatalf("error=%v, closed=%v, read=%d", err, body.closed, body.read)
			}
			if tc.want != nil && data != nil {
				t.Fatalf("failure returned partial data: %q", data)
			}
			if tc.status != 200 && (body.read != 0 || strings.Contains(err.Error(), "private")) {
				t.Fatal("status rejection read or exposed the upstream body")
			}
		})
	}
}

func TestPreCanceledAndMalformedRequestsDoNotReachTransport(t *testing.T) {
	client := testClient(t)
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached transport")
		return nil, errors.New("unreachable")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if body, err := client.fetch(ctx, "https://dependency.invalid"); body != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled = %q, %v", body, err)
	}
	if body, err := client.fetch(context.Background(), "://bad-url"); body != nil || err == nil {
		t.Fatalf("malformed URL = %q, %v", body, err)
	}
}

func TestTransportFailureIsWrapped(t *testing.T) {
	want := errors.New("fixture connection failure")
	client := testClient(t)
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, want })
	if body, err := client.fetch(context.Background(), "https://dependency.invalid"); body != nil || !errors.Is(err, want) {
		t.Fatalf("transport failure = %q, %v", body, err)
	}
}

func TestClientCanBeReusedConcurrently(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer server.Close()
	client := testClient(t)
	var work sync.WaitGroup
	for i := 0; i < 12; i++ {
		work.Add(1)
		go func() {
			defer work.Done()
			body, err := client.fetch(context.Background(), server.URL)
			if err != nil || string(body) != "ok" {
				t.Errorf("concurrent fetch = %q, %v", body, err)
			}
		}()
	}
	work.Wait()
}

func TestDemo(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "{\"service\":\"notifications\",\"pending\":3}\n" {
		t.Fatalf("output = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *trackedBody) Close() error { b.closed = true; return nil }

type brokenReader struct{}

func (*brokenReader) Read(p []byte) (int, error) { return copy(p, "partial"), io.ErrUnexpectedEOF }

type fetchResult struct {
	body []byte
	err  error
}

func fetchAsync(client *snapshotClient, ctx context.Context, endpoint string) <-chan fetchResult {
	result := make(chan fetchResult, 1)
	go func() { body, err := client.fetch(ctx, endpoint); result <- fetchResult{body, err} }()
	return result
}
func testClient(t *testing.T) *snapshotClient {
	t.Helper()
	client := newSnapshotClient()
	t.Cleanup(client.http.CloseIdleConnections)
	return client
}
func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not reach barrier")
	}
}
func awaitResult(t *testing.T, result <-chan fetchResult) fetchResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
		return fetchResult{}
	}
}
