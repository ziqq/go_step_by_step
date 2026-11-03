// Package main demonstrates a bounded TTL cache with owned byte snapshots.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidCache = errors.New("positive capacity, TTL, and clock are required")
	ErrInvalidKey   = errors.New("tenant, viewer, and conversation must not be blank")
	ErrCacheFull    = errors.New("cache is full")
)

// previewKey identifies one fixed preview representation, not an authorization grant.
type previewKey struct {
	TenantID       string
	ViewerID       string
	ConversationID string
}

func (k previewKey) valid() bool {
	return strings.TrimSpace(k.TenantID) != "" && strings.TrimSpace(k.ViewerID) != "" && strings.TrimSpace(k.ConversationID) != ""
}

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

// previewCache requires its constructor and must not be copied.
// now must be fast, nondecreasing, and must not call back into the cache.
type previewCache struct {
	mu       sync.Mutex
	entries  map[previewKey]cacheEntry
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

func newPreviewCache(capacity int, ttl time.Duration, now func() time.Time) (*previewCache, error) {
	if capacity <= 0 || ttl <= 0 || now == nil {
		return nil, ErrInvalidCache
	}
	return &previewCache{entries: make(map[previewKey]cacheEntry), capacity: capacity, ttl: ttl, now: now}, nil
}

// Put copies data, restarts this key's TTL, and rejects new keys at live capacity.
// Callers must not mutate data concurrently with this call.
func (c *previewCache) Put(key previewKey, data []byte) error {
	if !key.valid() {
		return ErrInvalidKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.capacity {
		return ErrCacheFull
	}
	c.entries[key] = cacheEntry{data: slices.Clone(data), expiresAt: now.Add(c.ttl)}
	return nil
}

// Get returns an independent snapshot and a hit flag without extending TTL.
// Invalid, missing, and expired keys all return (nil, false).
func (c *previewCache) Get(key previewKey) ([]byte, bool) {
	if !key.valid() {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}
	if !c.now().Before(entry.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return slices.Clone(entry.data), true
}

func run(output io.Writer) error {
	now := time.Date(2026, time.November, 3, 0, 0, 0, 0, time.UTC)
	cache, err := newPreviewCache(1, time.Minute, func() time.Time { return now })
	if err != nil {
		return err
	}
	key := previewKey{TenantID: "tenant-1", ViewerID: "viewer-1", ConversationID: "chat-1"}
	input := []byte("hello")
	if err := cache.Put(key, input); err != nil {
		return err
	}
	input[0] = 'X'
	snapshot, hit := cache.Get(key)
	now = now.Add(time.Minute)
	_, afterExpiry := cache.Get(key)
	_, err = fmt.Fprintf(output, "preview=%s; hit=%t; hit at expiry=%t\n", snapshot, hit, afterExpiry)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
