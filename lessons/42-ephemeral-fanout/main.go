package main

import (
	"fmt"
	"sync"
)

// hub distributes transient events within a conversation.
type hub struct {
	mu     sync.Mutex
	topics map[string]map[chan string]struct{}
}

func newHub() *hub {
	return &hub{topics: make(map[string]map[chan string]struct{})}
}

// subscribe requires a positive queue capacity. The hub owns the channel.
func (h *hub) subscribe(conversation string, capacity int) (<-chan string, func()) {
	if capacity <= 0 {
		panic("subscriber queue capacity must be positive")
	}
	queue := make(chan string, capacity)

	h.mu.Lock()
	if h.topics[conversation] == nil {
		h.topics[conversation] = make(map[chan string]struct{})
	}
	h.topics[conversation][queue] = struct{}{}
	h.mu.Unlock()

	return queue, func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		subscribers := h.topics[conversation]
		if _, exists := subscribers[queue]; !exists {
			return
		}
		delete(subscribers, queue)
		if len(subscribers) == 0 {
			delete(h.topics, conversation)
		}
		close(queue)
	}
}

func (h *hub) publish(conversation, event string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for queue := range h.topics[conversation] {
		select {
		case queue <- event:
		default:
			// A slow subscriber can miss transient events.
		}
	}
}

func main() {
	events := newHub()
	slow, unsubscribeSlow := events.subscribe("design", 1)
	defer unsubscribeSlow()
	fast, unsubscribeFast := events.subscribe("design", 1)
	defer unsubscribeFast()

	events.publish("design", "typing started")
	fmt.Println("fast:", <-fast)

	events.publish("design", "typing stopped")
	fmt.Println("fast:", <-fast)
	fmt.Println("slow:", <-slow)
}
