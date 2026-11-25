package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

var (
	errInvalid   = errors.New("invalid command")
	errForbidden = errors.New("access denied")
)

type reply struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

// replyFor receives an already validated correlation ID, never an identity.
func replyFor(id string, err error) reply {
	code := "internal"
	switch {
	case err == nil:
		code = "ok"
	case errors.Is(err, errInvalid):
		code = "invalid_command"
	case errors.Is(err, errForbidden):
		code = "forbidden"
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	}
	return reply{ID: id, Code: code}
}

func main() {
	encoder := json.NewEncoder(os.Stdout)
	for _, err := range []error{nil, fmt.Errorf("membership lookup: %w", errForbidden), errors.New("private database detail")} {
		if err := encoder.Encode(replyFor("req-1", err)); err != nil {
			fmt.Fprintln(os.Stderr, "encode reply:", err)
			os.Exit(1)
		}
	}
}
