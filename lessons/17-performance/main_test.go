package main

import (
	"fmt"
	"testing"
	"time"
)

func TestCountUnread(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		cursors  []ReadCursor
		userID   string
		want     int
	}{
		{
			name:   "empty input",
			userID: "alex",
			want:   0,
		},
		{
			name: "without a cursor all incoming messages are unread",
			messages: []Message{
				{ConversationID: "chat-1", SenderID: "sam", CreatedAt: at(10)},
				{ConversationID: "chat-1", SenderID: "lee", CreatedAt: at(11)},
			},
			userID: "alex",
			want:   2,
		},
		{
			name: "own messages are not unread",
			messages: []Message{
				{ConversationID: "chat-1", SenderID: "alex", CreatedAt: at(12)},
			},
			userID: "alex",
			want:   0,
		},
		{
			name: "message at cursor time is already read",
			messages: []Message{
				{ConversationID: "chat-1", SenderID: "sam", CreatedAt: at(12)},
			},
			cursors: []ReadCursor{
				{ConversationID: "chat-1", UserID: "alex", ReadAt: at(12)},
			},
			userID: "alex",
			want:   0,
		},
		{
			name: "latest matching cursor wins",
			messages: []Message{
				{ConversationID: "chat-1", SenderID: "sam", CreatedAt: at(13)},
				{ConversationID: "chat-1", SenderID: "sam", CreatedAt: at(15)},
			},
			cursors: []ReadCursor{
				{ConversationID: "chat-1", UserID: "alex", ReadAt: at(10)},
				{ConversationID: "chat-1", UserID: "alex", ReadAt: at(14)},
			},
			userID: "alex",
			want:   1,
		},
		{
			name: "other users and conversations do not advance the cursor",
			messages: []Message{
				{ConversationID: "chat-1", SenderID: "sam", CreatedAt: at(11)},
				{ConversationID: "chat-2", SenderID: "sam", CreatedAt: at(9)},
			},
			cursors: []ReadCursor{
				{ConversationID: "chat-1", UserID: "pat", ReadAt: at(20)},
				{ConversationID: "chat-2", UserID: "alex", ReadAt: at(10)},
			},
			userID: "alex",
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CountUnread(tt.messages, tt.cursors, tt.userID); got != tt.want {
				t.Fatalf("CountUnread() = %d, want %d", got, tt.want)
			}
		})
	}
}

func at(second int) time.Time {
	return time.Unix(int64(second), 0).UTC()
}

var benchmarkUnread int

func BenchmarkCountUnread(b *testing.B) {
	const userID = "viewer"
	const messageCount = 2000
	const cursorCount = 64

	messages := make([]Message, messageCount)
	for i := range messages {
		senderID := "peer"
		if i%3 == 0 {
			senderID = userID
		}
		messages[i] = Message{
			ConversationID: fmt.Sprintf("chat-%02d", i%8),
			SenderID:       senderID,
			CreatedAt:      at(i),
		}
	}

	cursors := make([]ReadCursor, cursorCount)
	for i := range cursors {
		cursors[i] = ReadCursor{
			ConversationID: fmt.Sprintf("chat-%02d", i%8),
			UserID:         userID,
			ReadAt:         at(messageCount / 2),
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkUnread = CountUnread(messages, cursors, userID)
	}
}
