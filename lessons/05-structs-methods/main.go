package main

import "fmt"

type Message struct {
	ID      int
	Author  string
	Content string
}

func (m Message) Summary() string {
	return fmt.Sprintf("%s: %s", m.Author, m.Content)
}

type Chat struct {
	Name     string
	Messages []Message
}

func (c *Chat) AddMessage(message Message) {
	c.Messages = append(c.Messages, message)
}

func (c Chat) MessageCount() int {
	return len(c.Messages)
}

func (c Chat) LastMessage() (Message, bool) {
	if len(c.Messages) == 0 {
		return Message{}, false
	}

	return c.Messages[len(c.Messages)-1], true
}

type Identified struct {
	ID int
}

type Event struct {
	Identified
	Kind string
}

func main() {
	chat := Chat{Name: "support"}
	chat.AddMessage(Message{ID: 1, Author: "Anton", Content: "Hello"})
	chat.AddMessage(Message{ID: 2, Author: "Bot", Content: "Welcome"})

	last, ok := chat.LastMessage()
	if !ok {
		return
	}

	fmt.Printf("chat: %s, messages: %d\n", chat.Name, chat.MessageCount())
	fmt.Printf("last: #%d %s\n", last.ID, last.Summary())

	event := Event{Identified: Identified{ID: 42}, Kind: "message.created"}
	fmt.Printf("event: #%d %s\n", event.ID, event.Kind)
}
