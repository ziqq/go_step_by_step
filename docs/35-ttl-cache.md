# Lesson 35: bound a TTL cache and own its snapshots

[Русская версия](ru/35-ttl-cache.md)

Continue the system-design part of the course by giving cached data explicit scope, expiry, capacity, and ownership. Trace the fake clock, draw the lifetime of an entry, run the boundary tests, then complete the invalidation assignment. Lesson 34's batch-admission exercise remains separate and unsolved.

## A disposable preview, not a source of truth

Open [main.go](../lessons/35-ttl-cache/main.go) and [the tests](../lessons/35-ttl-cache/main_test.go). This standalone fixture caches bytes for one fixed conversation-preview representation. It has no HTTP adapter, database loader, background sweeper, or shared cache service. The demo runs offline with an injected clock and no sleeps.

The key is a struct containing `TenantID`, `ViewerID`, and `ConversationID`. All three must be nonblank; nonblank strings remain exact, including surrounding spaces. A struct avoids ambiguous delimiter concatenation. Each field changes identity independently. If you later cache pages or another representation, its filters, cursor, limit, and representation version also need explicit key fields or a separate cache.

A hit does not authorize a viewer. Verify identity and current conversation access on every request before using a private preview, and apply a policy for revoked access. These key fields prevent accidental collisions in this fixture; they do not implement authorization. Do not store secrets or real private messages in the demo.

The read-only reference backend's `docs/DATA_MODEL.md` names PostgreSQL as the source of truth. Its durable `command_results` protocol in `internal/storage/postgres/command_result.go` serves idempotency and is not replaced by this cache. No reference-backend files are changed.

## State the small API contract

Construct `previewCache` with `newPreviewCache(capacity, ttl, now)`. Capacity and TTL must be positive and the clock non-nil; otherwise return nil and `ErrInvalidCache`. The cache requires construction and must not be copied.

| Operation | Result |
|---|---|
| `Put` with a blank key field | `ErrInvalidKey`, no mutation |
| `Put` for a new key at live capacity | `ErrCacheFull`, no live entry evicted or refreshed |
| `Put` for an existing live key | Replace its bytes and restart only its TTL, including at capacity |
| `Get` for an invalid, missing, or expired key | `(nil, false)` |
| `Get` for a live key | A separate byte snapshot and `true`; TTL is unchanged |

Nil and empty payloads are valid cached values. Inspect the hit flag rather than treating an empty payload as a miss. There is no LRU eviction, negative caching of loader errors, or coalescing of concurrent cache misses.

## One expiry boundary, one clock sample

`Put` samples the clock under the mutex, removes all expired entries, applies the entry-count limit, and stores an expiry of that sample plus TTL. `Get` checks the requested entry under the same mutex and deletes it if expired. An entry is live only strictly before expiry:

```go
if !c.now().Before(entry.expiresAt) {
    delete(c.entries, key)
    return nil, false
}
```

This fragment comes from `Get`; see the full runnable example above. Equality is expired. Reads never renew TTL. Fake-clock tests check one nanosecond before expiry, exact equality, and after it, without waiting for wall-clock timers. An operation observes time at its locked sample; a returned snapshot may naturally become older after return.

In an application pass `time.Now`: Go can retain a monotonic component for in-process duration comparisons. Do not serialize these deadlines or compare them with database expiry timestamps. An injected clock must be fast, nondecreasing, and must not call the cache; it executes while the mutex is held. The demo's captured time changes only sequentially; concurrent tests use a synchronized fake clock.

## Copying is part of ownership

`Put` stores `slices.Clone(data)` and `Get` returns another clone. Passing or returning a slice header alone shares its backing array. Without copying, a caller could modify cached data after `Put` or another caller's result after `Get`, even if every map access holds a mutex. Callers still must not mutate the input concurrently with `Put`; the cache cannot synchronize external ownership violations.

The mutex protects lookup, expiry, capacity, and replacement together. Copying returned bytes lets callers mutate their own snapshots after `Get`. No locks are needed by callers for those separate snapshots. These tests establish the example's behavior, not production throughput.

Capacity bounds stored entry count, not byte size, total process memory, or concurrent request count. `Put` scans up to capacity entries and copies input while holding the mutex; `Get` copies output. An idle cache can retain expired bytes until touched, but never exceeds the entry limit. Choose payload limits and eviction policy from measured requirements before adapting this fixture to real traffic; no byte-budget implementation is included here.

## Run from the repository root

```sh
go run ./lessons/35-ttl-cache
go test -count=1 -v ./lessons/35-ttl-cache
go test -race -count=20 ./lessons/35-ttl-cache
```

Expected output:

```text
preview=hello; hit=true; hit at expiry=false
```

The tests cover invalid configuration and keys, empty cached values, copied input/output, scope isolation, exact expiry, no read renewal, overwrite at capacity, overload refusal, reclaiming expired slots, concurrent admission, concurrent snapshot ownership, and writer failures.

## Practice: invalidate one conversation across viewers

Add `InvalidateConversation(tenantID, conversationID string) (int, error)` without changing the published methods or tests:

1. A blank/whitespace-only argument returns zero and `ErrInvalidKey` without changing anything. Preserve exact nonblank identities.
2. Remove every stored entry matching both tenant and conversation, across all viewers. Return the number physically removed, including matching expired entries still present. No match returns zero and nil. Do not refresh, prune, or remove unrelated entries.
3. Serialize the operation with `Put` and `Get`. An entry ordered before invalidation is removed; a `Put` ordered after it may store a new value. This is not a permanent ban or a distributed consistency guarantee.
4. Add deterministic tests for multiple viewers, another tenant with the same conversation ID, another conversation, repeated invalidation, blank arguments with no mutation, matching expired entries, capacity reuse, and channel-ordered/concurrent invalidation with `Put`/`Get`. Keep the fake clock synchronized and avoid sleeps as ordering evidence.
5. In your explanation, place invalidation after successful database commit and analyze `load old data → commit/invalidate → stale Put`. Explain why this method alone cannot prevent late stale refill or revoke snapshots already returned. Name a possible version/check policy; do not implement a loader, distributed cache, or version protocol in this assignment.

Completion: tests prove the exact removal count, isolation, capacity reuse, and both allowed operation orders. Explain the stale-refill boundary without claiming strong consistency. The new method and its tests remain yours.

## Checks and common mistakes

```sh
gofmt -l lessons/35-ttl-cache/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier SQL integrations require disposable PostgreSQL and `POSTGRES_TEST_URL`; without it they skip. Common mistakes: renewing on reads by accident; accepting equality as live; leaking backing arrays; using the cache key as authorization; counting TTL as immediate memory reclamation; silently evicting live entries; and confusing TTL with durable idempotency or write invalidation.

Sources: [Go time comparisons and monotonic clocks](https://pkg.go.dev/time#hdr-Monotonic_Clocks), [mutex ownership](https://pkg.go.dev/sync#Mutex), [slice backing arrays](https://go.dev/blog/slices-intro), [slice cloning](https://pkg.go.dev/slices#Clone).
