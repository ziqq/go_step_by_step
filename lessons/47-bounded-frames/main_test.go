package main

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadFrameAtLimitAndNextFrame(t *testing.T) {
	stream := bytes.NewReader([]byte{0, 0, 0, 2, 'h', 'i', 0, 0, 0, 1, '!'})
	for _, want := range []string{"hi", "!"} {
		payload, err := readFrame(stream, 2)
		if err != nil || string(payload) != want {
			t.Fatalf("payload = %q, error = %v, want %q", payload, err, want)
		}
	}
	if _, err := readFrame(stream, 2); !errors.Is(err, io.EOF) {
		t.Fatalf("end error = %v, want EOF", err)
	}
}

func TestReadFrameRejectsLengthBeforeReadingPayload(t *testing.T) {
	for _, header := range [][]byte{{0, 0, 0, 3}, {255, 255, 255, 255}} {
		stream := bytes.NewReader(append(header, 'x', 'y', 'z'))
		payload, err := readFrame(stream, 2)
		if !errors.Is(err, errFrameTooLarge) || payload != nil || stream.Len() != 3 {
			t.Fatalf("payload = %v, error = %v, unread = %d", payload, err, stream.Len())
		}
	}
}

func TestReadFrameHandlesEmptyAndTruncatedFrames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []byte
		want  error
	}{
		{name: "empty frame", input: []byte{0, 0, 0, 0}},
		{name: "empty stream", want: io.EOF},
		{name: "partial header", input: []byte{0, 0}, want: io.ErrUnexpectedEOF},
		{name: "missing payload", input: []byte{0, 0, 0, 2}, want: io.ErrUnexpectedEOF},
		{name: "partial payload", input: []byte{0, 0, 0, 2, 'h'}, want: io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := readFrame(bytes.NewReader(tc.input), 2)
			if !errors.Is(err, tc.want) || (err != nil && payload != nil) {
				t.Fatalf("payload = %v, error = %v, want %v", payload, err, tc.want)
			}
			if tc.want == nil && len(payload) != 0 {
				t.Fatalf("empty frame = %v", payload)
			}
		})
	}
}

type failingReader struct {
	calls   int
	failure error
}

func (r *failingReader) Read([]byte) (int, error) {
	r.calls++
	return 0, r.failure
}

func TestReadFrameInvalidLimitDoesNotRead(t *testing.T) {
	for _, limit := range []int{0, -1} {
		reader := &failingReader{}
		if _, err := readFrame(reader, limit); !errors.Is(err, errInvalidLimit) || reader.calls != 0 {
			t.Fatalf("limit = %d, error = %v, reads = %d", limit, err, reader.calls)
		}
	}
}

func TestReadFramePreservesReadError(t *testing.T) {
	failure := errors.New("connection failed")
	reader := &failingReader{failure: failure}
	if _, err := readFrame(reader, 8); !errors.Is(err, failure) {
		t.Fatalf("error = %v, want original cause", err)
	}
}
