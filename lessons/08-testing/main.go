package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var ErrInvalidTag = errors.New("invalid tag")

var tagPattern = regexp.MustCompile("^[a-z0-9][a-z0-9_-]*$")

func normalizeTags(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return []string{}, nil
	}

	tags := make([]string, 0)
	seen := make(map[string]struct{})
	for _, part := range strings.Split(input, ",") {
		tag := strings.ToLower(strings.TrimSpace(part))
		if tag == "" {
			continue
		}
		if !tagPattern.MatchString(tag) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidTag, part)
		}
		if _, exists := seen[tag]; exists {
			continue
		}

		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}

	return tags, nil
}

func main() {
	tags, err := normalizeTags("Go, backend, go, chat")
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("normalized tags: %s\n", strings.Join(tags, ", "))
}
