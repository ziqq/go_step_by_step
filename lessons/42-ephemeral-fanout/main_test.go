package main

import (
	"sync"
	"testing"
	"time"
)

func requireEvent(t *testing.T, queue <-chan string, want string) {
	t.Helper()
	select {
	case got, open := <-queue:
		if !open || got != want {
			t.Fatalf("got (%q, %t), want (%q, true)", got, open, want)
		}
	default:
		t.Fatalf("expected queued event %q", want)
	}
}

func requireEmpty(t *testing.T, queue <-chan string) {
	t.Helper()
	select {
	case got, open := <-queue:
		t.Fatalf("expected open empty queue, got (%q, %t)", got, open)
	default:
	}
}

func requireClosed(t *testing.T, queue <-chan string) {
	t.Helper()
	select {
	case got, open := <-queue:
		if open {
			t.Fatalf("expected closed queue, got event %q", got)
		}
	default:
		t.Fatal("queue is still open")
	}
}

func TestSlowSubscriberKeepsEarlierEvent(t *testing.T) {
	events := newHub()
	slow, stopSlow := events.subscribe("design", 1)
	defer stopSlow()
	fast, stopFast := events.subscribe("design", 1)
	defer stopFast()

	events.publish("design", "first")
	requireEvent(t, fast, "first")

	published := make(chan struct{})
	go func() {
		defer close(published)
		events.publish("design", "second")
	}()
	select {
	case <-published:
	case <-time.After(time.Second):
		// Unblock a publisher that incorrectly used a blocking send.
		<-slow
		<-published
		t.Fatal("publish waited for a slow subscriber")
	}
	requireEvent(t, fast, "second")
	requireEvent(t, slow, "first")
	requireEmpty(t, slow)

	events.publish("design", "third")
	requireEvent(t, slow, "third")
	requireEvent(t, fast, "third")
}

func TestConversationIsolation(t *testing.T) {
	events := newHub()
	design, stopDesign := events.subscribe("design", 2)
	defer stopDesign()
	support, stopSupport := events.subscribe("support", 1)
	defer stopSupport()

	events.publish("missing", "ignored")
	events.publish("design", "first")
	events.publish("design", "second")
	requireEvent(t, design, "first")
	requireEvent(t, design, "second")
	requireEmpty(t, support)
}

func TestUnsubscribeClosesOnlyOwnedQueue(t *testing.T) {
	events := newHub()
	old, stopOld := events.subscribe("design", 1)
	events.publish("design", "queued before unsubscribe")
	stopOld()
	newQueue, stopNew := events.subscribe("design", 1)
	defer stopNew()
	stopOld()

	events.publish("design", "new subscriber only")
	requireEvent(t, old, "queued before unsubscribe")
	requireClosed(t, old)
	requireEvent(t, newQueue, "new subscriber only")
}

func TestPublishRacesWithUnsubscribe(t *testing.T) {
	events := newHub()
	queue, unsubscribe := events.subscribe("design", 1)
	start := make(chan struct{})

	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		<-start
		events.publish("design", "one possible delivery")
	}()
	for range 2 {
		go func() {
			defer workers.Done()
			<-start
			unsubscribe()
		}()
	}
	close(start)
	workers.Wait()

	select {
	case value, open := <-queue:
		if open {
			if value != "one possible delivery" {
				t.Fatalf("unexpected event %q", value)
			}
			requireClosed(t, queue)
		}
	default:
		t.Fatal("unsubscribe did not close the queue")
	}
}
