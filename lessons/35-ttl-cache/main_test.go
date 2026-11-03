package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu   sync.Mutex
	time time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.time
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(d)
}

func fixture(t *testing.T, capacity int) (*previewCache, *testClock, previewKey) {
	t.Helper()
	clock := &testClock{time: time.Date(2026, time.November, 3, 0, 0, 0, 0, time.UTC)}
	cache, err := newPreviewCache(capacity, time.Minute, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return cache, clock, previewKey{"tenant-1", "viewer-1", "chat-1"}
}

func put(t *testing.T, c *previewCache, key previewKey, data string) {
	t.Helper()
	if err := c.Put(key, []byte(data)); err != nil {
		t.Fatal(err)
	}
}

func wantSnapshot(t *testing.T, c *previewCache, key previewKey, want string) {
	t.Helper()
	data, hit := c.Get(key)
	if !hit || string(data) != want {
		t.Fatalf("Get(%+v) = %q, %t; want %q, true", key, data, hit, want)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		capacity int
		ttl      time.Duration
		now      func() time.Time
	}{
		{"zero capacity", 0, time.Minute, time.Now},
		{"negative capacity", -1, time.Minute, time.Now},
		{"zero TTL", 1, 0, time.Now},
		{"negative TTL", 1, -time.Second, time.Now},
		{"nil clock", 1, time.Minute, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := newPreviewCache(tc.capacity, tc.ttl, tc.now)
			if c != nil || !errors.Is(err, ErrInvalidCache) {
				t.Fatalf("constructor = %v, %v", c, err)
			}
		})
	}
}

func TestInvalidKeysLeaveStoredValueUnchanged(t *testing.T) {
	c, _, key := fixture(t, 1)
	put(t, c, key, "keep")
	for _, invalid := range []previewKey{
		{}, {"", "viewer", "chat"}, {"tenant", "", "chat"}, {"tenant", "viewer", ""},
		{" \t", "viewer", "chat"}, {"tenant", "\n", "chat"}, {"tenant", "viewer", " \t"},
	} {
		if err := c.Put(invalid, []byte("bad")); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("invalid Put = %v", err)
		}
		if data, hit := c.Get(invalid); data != nil || hit {
			t.Fatalf("invalid Get = %q, %t", data, hit)
		}
	}
	wantSnapshot(t, c, key, "keep")
}

func TestMissingAndEmptyValuesUseHitFlag(t *testing.T) {
	c, _, key := fixture(t, 1)
	if data, hit := c.Get(key); data != nil || hit {
		t.Fatalf("missing = %q, %t", data, hit)
	}
	for _, data := range [][]byte{nil, {}} {
		if err := c.Put(key, data); err != nil {
			t.Fatal(err)
		}
		got, hit := c.Get(key)
		if !hit || len(got) != 0 {
			t.Fatalf("empty stored value = %q, %t", got, hit)
		}
	}
}

func TestInputAndEachOutputHaveIndependentBytes(t *testing.T) {
	c, _, key := fixture(t, 1)
	input := []byte("hello")
	if err := c.Put(key, input); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	first, hit := c.Get(key)
	if !hit || string(first) != "hello" {
		t.Fatalf("input alias: %q, %t", first, hit)
	}
	second, _ := c.Get(key)
	first[1] = 'Y'
	if string(second) != "hello" {
		t.Fatalf("outputs alias: %q", second)
	}
	wantSnapshot(t, c, key, "hello")
}

func TestExactKeysIsolateTenantViewerAndConversation(t *testing.T) {
	c, _, key := fixture(t, 7)
	keys := []previewKey{key, {"tenant-2", key.ViewerID, key.ConversationID},
		{key.TenantID, "viewer-2", key.ConversationID}, {key.TenantID, key.ViewerID, "chat-2"},
		{" tenant-1 ", key.ViewerID, key.ConversationID}, {"a:b", "c", "d"}, {"a", "b:c", "d"}}
	for i, k := range keys {
		put(t, c, k, fmt.Sprint(i))
	}
	for i, k := range keys {
		wantSnapshot(t, c, k, fmt.Sprint(i))
	}
}

func TestExpiryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		hit     bool
	}{
		{"before", time.Minute - time.Nanosecond, true}, {"exact", time.Minute, false}, {"after", time.Minute + time.Nanosecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, clock, key := fixture(t, 1)
			put(t, c, key, "value")
			clock.Advance(tc.elapsed)
			data, hit := c.Get(key)
			if hit != tc.hit {
				t.Fatalf("hit = %t, want %t", hit, tc.hit)
			}
			if !hit && data != nil {
				t.Fatalf("expired data = %q", data)
			}
		})
	}
}

func TestReadingDoesNotExtendTTL(t *testing.T) {
	c, clock, key := fixture(t, 1)
	put(t, c, key, "value")
	clock.Advance(59 * time.Second)
	wantSnapshot(t, c, key, "value")
	clock.Advance(time.Second)
	if _, hit := c.Get(key); hit {
		t.Fatal("read extended TTL")
	}
}

func TestOverwriteAtCapacityRestartsOnlyItsOwnTTL(t *testing.T) {
	c, clock, key := fixture(t, 2)
	other := previewKey{key.TenantID, key.ViewerID, "chat-2"}
	put(t, c, key, "old")
	put(t, c, other, "other")
	clock.Advance(30 * time.Second)
	put(t, c, key, "new")
	clock.Advance(30 * time.Second)
	wantSnapshot(t, c, key, "new")
	if _, hit := c.Get(other); hit {
		t.Fatal("overwrite refreshed another key")
	}
	clock.Advance(30 * time.Second)
	if _, hit := c.Get(key); hit {
		t.Fatal("overwrite never expired")
	}
}

func TestCapacityRefusalPreservesLiveEntryAndExpiry(t *testing.T) {
	c, clock, key := fixture(t, 1)
	other := previewKey{key.TenantID, key.ViewerID, "chat-2"}
	put(t, c, key, "keep")
	clock.Advance(59 * time.Second)
	if err := c.Put(other, []byte("reject")); !errors.Is(err, ErrCacheFull) {
		t.Fatalf("full Put = %v", err)
	}
	wantSnapshot(t, c, key, "keep")
	if _, hit := c.Get(other); hit {
		t.Fatal("rejected value stored")
	}
	clock.Advance(time.Second)
	if _, hit := c.Get(key); hit {
		t.Fatal("rejection refreshed stored entry")
	}
}

func TestPutReclaimsExpiredEntriesWithoutPriorReads(t *testing.T) {
	c, clock, key := fixture(t, 2)
	other := previewKey{key.TenantID, "viewer-2", key.ConversationID}
	put(t, c, key, "expired-1")
	put(t, c, other, "expired-2")
	clock.Advance(time.Minute)
	third := previewKey{key.TenantID, key.ViewerID, "chat-3"}
	fourth := previewKey{key.TenantID, key.ViewerID, "chat-4"}
	put(t, c, third, "fresh-3")
	put(t, c, fourth, "fresh-4")
	wantSnapshot(t, c, third, "fresh-3")
	wantSnapshot(t, c, fourth, "fresh-4")
	for _, k := range []previewKey{key, other} {
		if _, hit := c.Get(k); hit {
			t.Fatal("expired key survived")
		}
	}
}

func TestConcurrentDistinctWritersRespectCapacity(t *testing.T) {
	const producers, capacity = 32, 8
	c, _, key := fixture(t, capacity)
	start := make(chan struct{})
	results := make([]error, producers)
	var wg sync.WaitGroup
	for i := range producers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			k := key
			k.ViewerID = fmt.Sprint(i)
			results[i] = c.Put(k, []byte(k.ViewerID))
		}()
	}
	close(start)
	wg.Wait()
	accepted := 0
	for i, err := range results {
		k := key
		k.ViewerID = fmt.Sprint(i)
		if err == nil {
			accepted++
			wantSnapshot(t, c, k, k.ViewerID)
		} else {
			if !errors.Is(err, ErrCacheFull) {
				t.Fatalf("Put = %v", err)
			}
			if _, hit := c.Get(k); hit {
				t.Fatal("rejected concurrent write stored")
			}
		}
	}
	if accepted != capacity {
		t.Fatalf("accepted %d, want %d", accepted, capacity)
	}
}

func TestConcurrentReadersAndWritersOwnTheirSnapshots(t *testing.T) {
	c, _, key := fixture(t, 1)
	put(t, c, key, strings.Repeat("a", 32))
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value := bytes.Repeat([]byte{byte('a' + i)}, 32)
			for range 40 {
				if err := c.Put(key, value); err != nil {
					t.Error(err)
					return
				}
				got, hit := c.Get(key)
				if !hit || len(got) != 32 {
					t.Errorf("Get = %q, %t", got, hit)
					return
				}
				for _, b := range got {
					if b != got[0] {
						t.Error("torn snapshot")
						return
					}
				}
				got[0] = '!'
			}
		}()
	}
	wg.Wait()
}

func TestDemo(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "preview=hello; hit=true; hit at expiry=false\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestDemoPreservesWriterError(t *testing.T) {
	want := errors.New("write failed")
	if err := run(failingWriter{want}); !errors.Is(err, want) {
		t.Fatalf("run = %v", err)
	}
}
