package main

import (
	"reflect"
	"testing"
)

func TestMessageSummary(t *testing.T) {
	message := Message{Author: "Anton", Content: "Hello"}

	if got, want := message.Summary(), "Anton: Hello"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
}

func TestChatAddMessageMutatesReceiver(t *testing.T) {
	chat := Chat{Name: "support"}
	want := Message{ID: 1, Author: "Anton", Content: "Hello"}

	chat.AddMessage(want)

	if got := chat.Messages; !reflect.DeepEqual(got, []Message{want}) {
		t.Fatalf("Messages = %#v, want %#v", got, []Message{want})
	}
}

func TestChatLastMessage(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		want     Message
		wantOK   bool
	}{
		{
			name:   "empty chat",
			wantOK: false,
		},
		{
			name: "returns the last message",
			messages: []Message{
				{ID: 1, Author: "Anton", Content: "Hello"},
				{ID: 2, Author: "Bot", Content: "Welcome"},
			},
			want:   Message{ID: 2, Author: "Bot", Content: "Welcome"},
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat := Chat{Messages: tt.messages}
			got, gotOK := chat.LastMessage()

			if gotOK != tt.wantOK {
				t.Fatalf("LastMessage() ok = %t, want %t", gotOK, tt.wantOK)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("LastMessage() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEventPromotesEmbeddedID(t *testing.T) {
	event := Event{Identified: Identified{ID: 42}, Kind: "message.created"}

	if got, want := event.ID, 42; got != want {
		t.Fatalf("event.ID = %d, want %d", got, want)
	}
}
