package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	errInvalidLimit  = errors.New("frame limit must be positive")
	errFrameTooLarge = errors.New("frame exceeds byte limit")
)

// readFrame checks the declared byte count before allocating the payload.
func readFrame(reader io.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errInvalidLimit
	}
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, fmt.Errorf("read frame length: %w", err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if uint64(size) > uint64(maxBytes) {
		return nil, fmt.Errorf("%w: declared %d, allowed %d", errFrameTooLarge, size, maxBytes)
	}

	payload := make([]byte, int(size))
	if _, err := io.ReadFull(reader, payload); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("read frame payload: %w", err)
	}
	return payload, nil
}

func main() {
	// Two-byte payload prefixed by a four-byte unsigned big-endian length.
	stream := bytes.NewReader([]byte{0, 0, 0, 2, 'h', 'i'})
	payload, err := readFrame(stream, 8)
	if err != nil {
		panic(err)
	}
	fmt.Printf("received %d bytes: %s\n", len(payload), payload)
}
