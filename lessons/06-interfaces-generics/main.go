package main

import "fmt"

type Renderable interface {
	Render() string
}

type Message struct {
	ID      int
	Author  string
	Content string
}

func (m Message) Render() string {
	return fmt.Sprintf("message #%d from %s: %s", m.ID, m.Author, m.Content)
}

type SystemEvent struct {
	Kind string
}

func (e SystemEvent) Render() string {
	return fmt.Sprintf("event: %s", e.Kind)
}

var _ Renderable = Message{}
var _ Renderable = SystemEvent{}

func renderAll(items []Renderable) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Render())
	}

	return result
}

func renderAuthor(item Renderable) (string, bool) {
	message, ok := item.(Message)
	if !ok {
		return "", false
	}

	return message.Author, true
}

func first[T comparable](values []T, want T) (T, bool) {
	for _, value := range values {
		if value == want {
			return value, true
		}
	}

	var zero T
	return zero, false
}

func main() {
	items := []Renderable{
		Message{ID: 1, Author: "Anton", Content: "Hello"},
		SystemEvent{Kind: "message.created"},
	}

	for _, line := range renderAll(items) {
		fmt.Println(line)
	}

	if author, ok := renderAuthor(items[0]); ok {
		fmt.Printf("message author: %s\n", author)
	}

	if state, ok := first([]string{"queued", "sent", "failed"}, "sent"); ok {
		fmt.Printf("first state: %s\n", state)
	}
}
