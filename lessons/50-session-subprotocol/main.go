package main

import (
	"errors"
	"fmt"
	"strings"
)

var errProtocol = errors.New("subprotocol rejected")

func validToken(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			continue
		}
		return false
	}
	return true
}

// selectProtocol receives all Sec-WebSocket-Protocol request header values.
// This endpoint requires chat.v1; no match is an application-level rejection.
func selectProtocol(values []string) (string, error) {
	seen := make(map[string]bool)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			protocol := strings.Trim(part, " \t")
			if !validToken(protocol) || seen[protocol] {
				return "", errProtocol
			}
			seen[protocol] = true
		}
	}
	// Validate the complete offer before accepting a supported token.
	if !seen["chat.v1"] {
		return "", errProtocol
	}
	return "chat.v1", nil
}

func main() {
	for _, offer := range [][]string{{"other.v1, chat.v1"}, {"chat.v2"}, {"chat.v1, bad/token"}} {
		selected, err := selectProtocol(offer)
		if err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Println(selected)
	}
}
