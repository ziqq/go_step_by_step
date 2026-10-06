// Package main demonstrates a measurable baseline for unread-message counting.
package main

import (
	"fmt"
	"time"
)

type Message struct {
	ConversationID string
	SenderID       string
	CreatedAt      time.Time
}

type ReadCursor struct {
	ConversationID string
	UserID         string
	ReadAt         time.Time
}

// CountUnread counts incoming messages newer than the user's latest read cursor
// in the same conversation. This intentionally simple baseline scans all
// cursors for every message so that the benchmark has a concrete optimization
// target.
func CountUnread(messages []Message, cursors []ReadCursor, userID string) int {
	unread := 0
	for _, message := range messages {
		if message.SenderID == userID {
			continue
		}

		var latestReadAt time.Time
		foundCursor := false
		for _, cursor := range cursors {
			if cursor.UserID != userID || cursor.ConversationID != message.ConversationID {
				continue
			}
			if !foundCursor || cursor.ReadAt.After(latestReadAt) {
				latestReadAt = cursor.ReadAt
				foundCursor = true
			}
		}

		if !foundCursor || message.CreatedAt.After(latestReadAt) {
			unread++
		}
	}
	return unread
}

func main() {
	userID := "alex"
	messages := []Message{
		{ConversationID: "chat-1", SenderID: "sam", CreatedAt: time.Date(2026, time.January, 5, 10, 1, 0, 0, time.UTC)},
		{ConversationID: "chat-1", SenderID: userID, CreatedAt: time.Date(2026, time.January, 5, 10, 2, 0, 0, time.UTC)},
		{ConversationID: "chat-1", SenderID: "sam", CreatedAt: time.Date(2026, time.January, 5, 10, 3, 0, 0, time.UTC)},
	}
	cursors := []ReadCursor{
		{ConversationID: "chat-1", UserID: userID, ReadAt: time.Date(2026, time.January, 5, 10, 1, 30, 0, time.UTC)},
	}

	fmt.Printf("unread messages: %d\n", CountUnread(messages, cursors, userID))
}
