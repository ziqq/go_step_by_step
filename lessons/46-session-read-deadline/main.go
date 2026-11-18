package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	pongByte = 'P'
	dataByte = 'D'
)

var errUnknownFrame = errors.New("unknown frame")

type deadlineReader interface {
	io.Reader
	SetReadDeadline(time.Time) error
}

func readSession(conn deadlineReader, now func() time.Time, pongTimeout time.Duration, onData func()) error {
	if err := conn.SetReadDeadline(now().Add(pongTimeout)); err != nil {
		return fmt.Errorf("set initial read deadline: %w", err)
	}

	var frame [1]byte
	for {
		_, err := io.ReadFull(conn, frame[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read frame: %w", err)
		}

		switch frame[0] {
		case pongByte:
			if err := conn.SetReadDeadline(now().Add(pongTimeout)); err != nil {
				return fmt.Errorf("renew read deadline: %w", err)
			}
		case dataByte:
			onData()
		default:
			return fmt.Errorf("%w: %q", errUnknownFrame, frame[0])
		}
	}
}

func main() {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()

	sent := make(chan error, 1)
	go func() {
		_, err := peer.Write([]byte{pongByte, dataByte})
		peer.Close()
		sent <- err
	}()

	err := readSession(server, time.Now, 5*time.Second, func() {
		fmt.Println("received application data")
	})
	server.Close()
	if writeErr := <-sent; writeErr != nil {
		panic(writeErr)
	}
	if err != nil {
		panic(err)
	}
	fmt.Println("peer closed")
}
