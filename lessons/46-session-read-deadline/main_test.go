package main

import (
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

type recordingReader struct {
	io.Reader
	deadlines []time.Time
	failAt    int
	failure   error
}

func (r *recordingReader) SetReadDeadline(deadline time.Time) error {
	r.deadlines = append(r.deadlines, deadline)
	if len(r.deadlines) == r.failAt {
		return r.failure
	}
	return nil
}

func TestReadSessionRenewsOnlyOnPong(t *testing.T) {
	conn := &recordingReader{Reader: strings.NewReader("DPDDPD")}
	start := time.Unix(100, 0)
	now := start
	dataCount := 0
	err := readSession(conn, func() time.Time { return now }, 10*time.Second, func() {
		dataCount++
		now = now.Add(time.Second)
	})
	if err != nil || dataCount != 4 {
		t.Fatalf("error = %v, data count = %d", err, dataCount)
	}
	want := []time.Time{start.Add(10 * time.Second), start.Add(11 * time.Second), start.Add(13 * time.Second)}
	if !slices.Equal(conn.deadlines, want) {
		t.Fatalf("deadlines = %v, want %v", conn.deadlines, want)
	}
}

func TestReadSessionTimesOutWhilePeerStaysSilent(t *testing.T) {
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()

	err := readSession(conn, time.Now, 0, func() {
		t.Fatal("silent peer cannot deliver data")
	})
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestReadSessionRejectsUnknownFrame(t *testing.T) {
	conn := &recordingReader{Reader: strings.NewReader("XP")}
	err := readSession(conn, time.Now, time.Second, func() {
		t.Fatal("unexpected data callback")
	})
	if !errors.Is(err, errUnknownFrame) || len(conn.deadlines) != 1 {
		t.Fatalf("error = %v, deadlines = %v", err, conn.deadlines)
	}
}

func TestReadSessionStopsOnDeadlineError(t *testing.T) {
	failure := errors.New("cannot set deadline")
	for _, tc := range []struct {
		name   string
		failAt int
		unread int
	}{
		{name: "initial", failAt: 1, unread: 2},
		{name: "renewal", failAt: 2, unread: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.NewReader("PD")
			conn := &recordingReader{Reader: input, failAt: tc.failAt, failure: failure}
			err := readSession(conn, time.Now, time.Second, func() {
				t.Fatal("read continued after deadline error")
			})
			if !errors.Is(err, failure) || input.Len() != tc.unread {
				t.Fatalf("error = %v, unread bytes = %d, want %d", err, input.Len(), tc.unread)
			}
		})
	}
}
