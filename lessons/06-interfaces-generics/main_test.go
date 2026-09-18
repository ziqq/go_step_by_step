package main

import (
	"reflect"
	"testing"
)

func TestMessageRender(t *testing.T) {
	message := Message{ID: 1, Author: "Anton", Content: "Hello"}

	if got, want := message.Render(), "message #1 from Anton: Hello"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestSystemEventRender(t *testing.T) {
	event := SystemEvent{Kind: "message.created"}

	if got, want := event.Render(), "event: message.created"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestRenderAllPreservesOrder(t *testing.T) {
	items := []Renderable{
		Message{ID: 1, Author: "Anton", Content: "Hello"},
		SystemEvent{Kind: "message.created"},
	}

	want := []string{
		"message #1 from Anton: Hello",
		"event: message.created",
	}

	if got := renderAll(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("renderAll() = %#v, want %#v", got, want)
	}
}

func TestRenderAllEmpty(t *testing.T) {
	if got := renderAll(nil); len(got) != 0 {
		t.Fatalf("renderAll(nil) length = %d, want 0", len(got))
	}
}

func TestRenderAuthor(t *testing.T) {
	tests := []struct {
		name   string
		item   Renderable
		want   string
		wantOK bool
	}{
		{
			name:   "message",
			item:   Message{Author: "Anton"},
			want:   "Anton",
			wantOK: true,
		},
		{
			name:   "system event is not a message",
			item:   SystemEvent{Kind: "message.created"},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotOK := renderAuthor(tt.item)
			if gotOK != tt.wantOK {
				t.Fatalf("renderAuthor() ok = %t, want %t", gotOK, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("renderAuthor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirst(t *testing.T) {
	stringValue, stringOK := first([]string{"queued", "sent"}, "sent")
	if stringValue != "sent" || !stringOK {
		t.Fatalf("first(strings) = %q, %t; want %q, true", stringValue, stringOK, "sent")
	}

	integerValue, integerOK := first([]int{1, 2, 3}, 4)
	if integerValue != 0 || integerOK {
		t.Fatalf("first(missing int) = %d, %t; want 0, false", integerValue, integerOK)
	}

	emptyValue, emptyOK := first([]string(nil), "missing")
	if emptyValue != "" || emptyOK {
		t.Fatalf("first(empty) = %q, %t; want empty string, false", emptyValue, emptyOK)
	}
}
