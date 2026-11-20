package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	errInvalidCommand = errors.New("invalid send command")
	errTrailingJSON   = errors.New("expected one JSON value")
)

type sendCommand struct {
	Type           string `json:"type"`
	ConversationID int64  `json:"conversation_id"`
	Body           string `json:"body"`
}

// decodeCommand receives a complete payload already bounded by the frame reader.
func decodeCommand(payload []byte) (sendCommand, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var command *sendCommand
	if err := decoder.Decode(&command); err != nil {
		return sendCommand{}, fmt.Errorf("decode command: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return sendCommand{}, fmt.Errorf("decode trailing data: %w", err)
		}
		return sendCommand{}, errTrailingJSON
	}
	if command == nil || command.Type != "send" || command.ConversationID <= 0 || strings.TrimSpace(command.Body) == "" {
		return sendCommand{}, errInvalidCommand
	}
	return *command, nil
}

func main() {
	command, err := decodeCommand([]byte(`{"type":"send","conversation_id":7,"body":"hello"}`))
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s to conversation %d: %s\n", command.Type, command.ConversationID, command.Body)
}
