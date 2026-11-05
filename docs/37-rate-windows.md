# Lesson 37: bound scoped rate windows and retained keys

[Русская версия](ru/37-rate-windows.md)

Lesson 30 bounds simultaneous work; lesson 36 combines duplicate active reads. Neither bounds how often a principal starts requests over time. Trace a fixed window, run the deterministic expiry/capacity tests, and write an HTTP adapter that distinguishes quota exhaustion from unavailable limiter state. Lesson 36's `Close` assignment remains separate and unsolved.

## One fixed policy, exact scoped keys

Open [main.go](../lessons/37-rate-windows/main.go) and [the tests](../lessons/37-rate-windows/main_test.go). Construct `newWindowLimiter(capacity, limit, window, now)` with positive values and a non-nil clock; invalid configuration returns nil and `ErrInvalidLimiter`. Construct before use, never copy, and keep its policy unchanged. One limiter applies the same limit and window to every key. Different policies require separate instances.

`rateKey` contains exact tenant, user, and server-selected operation strings. Whitespace-only fields are invalid, but nonblank identities are not trimmed or merged. A struct avoids delimiter collisions. Identity must come from verified server state; this limiter does not authenticate or authorize. Do not key it from arbitrary `X-User-ID`, `X-Tenant-ID`, or forwarded-IP headers.

The reference chat backend selects a tenant/user/method key in `internal/command/dispatcher.go`, and has a process-local limiter in `internal/ratelimit/limiter.go`. Its operations/deployment docs distinguish local limits from ingress-wide quotas. This lesson uses its own immutable-policy fixture; it neither changes nor claims to duplicate the backend protocol. The reference stays read-only.

## Admission contract

| `Allow(ctx, key)` situation | Decision and error |
|---|---|
| Nil context or blank key field | Zero decision and `ErrInvalidRequest`, before cancellation/state checks |
| Context error observed before admission | Zero decision and that error; no clock read, allocation, or charge |
| New key with a free/reclaimed slot | Allowed, zero delay; charge one and start a full window |
| Existing live key below its quota | Allowed, zero delay; charge one without moving expiry |
| Existing live key at quota | Denied, positive remaining delay, nil error; do not increment or extend |
| Expired existing key, including at full capacity | Allowed; reuse its slot and start a full window at the current time |
| New key while every retained slot is live | Zero decision and `ErrCapacity`; no claimed retry estimate |

Every allowed call consumes quota even if later work fails, panics, or is canceled. There is no release/refund operation. `RetryAfter` describes this key's current quota window; it does not reserve future service or promise capacity at retry time. An unknown key at capacity has no known personal quota window, so that failure is not reported as a quota violation.

One mutex serializes the clock read, lookup, count, cleanup, and insertion. Check context before locking and again after acquiring it. Cancellation observed at the second check wins before clock/state work; cancellation that happens later cannot retroactively undo admission. The short mutex may contend: this example does not wait for quota reset or create a request queue, but it does not promise lock-free admission.

## Exact expiry and bounded retained state

The first allowed request starts `[now, now + window)`. Subsequent requests do not slide the window. Expiry is inclusive: equality starts a fresh window, with no carryover of unused allowance. Zero timestamps are valid; do not treat `time.Time{}` as an uninitialized stored entry.

Fragment of the live-window branch, while holding the mutex:

```go
if current.used >= l.limit {
    return rateDecision{RetryAfter: current.expiresAt.Sub(now)}, nil
}
current.used++
l.entries[key] = current
return rateDecision{Allowed: true}, nil
```

The runnable file handles lookup, exact expiry, validation, and cleanup. When a new key meets full capacity, remove expired entries using the same sampled time, preserve every live entry, then recheck capacity. An expired existing key already owns a slot and can reset directly. There are at most `capacity` entries, including expired ones that remain until reuse/cleanup. There is no sweeper goroutine. The full-map scan is O(capacity) under the mutex; key count is bounded, key-string bytes and request concurrency are not. Use bounded trusted IDs and the separate concurrency/dependency budgets when adapting a service.

The injected clock must be fast, nondecreasing, and non-reentrant because it runs under the mutex. Use `time.Now` for process-local duration measurement; its monotonic component survives `Add`, comparison, and subtraction within the process. Tests use a synchronized fake clock and advance it explicitly; they never sleep to establish expiry. Do not serialize timestamps and assume their monotonic component survives. Clock rollback is outside this fixture's contract.

This is a fixed-window policy, not a sliding-window or token-bucket rate guarantee. Requests can cluster around expiry. Separate processes/instances have independent budgets, and restarting loses history. The tests demonstrate that limitation rather than claiming fleet-wide protection. Do not replace durable command idempotency or authorization with this counter.

## Run from the repository root

```sh
go run ./lessons/37-rate-windows
go test -count=1 -v ./lessons/37-rate-windows
go test -race -count=20 ./lessons/37-rate-windows
```

Expected output (attempt 4 advances the fake clock exactly one second):

```text
attempt=1 allowed=true retry_after=0s
attempt=2 allowed=true retry_after=0s
attempt=3 allowed=false retry_after=1s
attempt=4 allowed=true retry_after=0s
```

The tests cover invalid/canceled calls, cancellation while waiting for the mutex, one-nanosecond expiry boundaries, non-sliding windows, capacity refusal, cleanup without a prior read, exact scope/delimiter isolation, maximum-duration arithmetic, concurrent quota/capacity admission, independent instances, and output failures. Channel barriers order concurrent work; the test timeout only guards against deadlock.

## Practice: adapt the decision to private history HTTP

Add `limitHistory(l *windowLimiter, next http.Handler) (http.Handler, error)` and `ErrInvalidHandler`. Add these private identity fixtures for the assignment; tests inject them as previously verified server context, without implementing authentication:

```go
type principal struct { TenantID, UserID string }
type principalKey struct{}
```

Keep the limiter and its tests unchanged. Implement one adapter with this contract:

1. A nil limiter or nil handler returns nil and `ErrInvalidHandler`. Before identity lookup, a request whose context is already done returns without writing headers/body or calling the limiter/next. The same no-write rule applies if `Allow` returns a context cancellation/deadline error.
2. Read a **value** of type `principal` from `r.Context().Value(principalKey{})`. Missing/wrong-type/blank identity returns 401, `WWW-Authenticate: Bearer`, and generic `authentication required`; it must not consume a key. Use exact trusted identity plus the constant operation `history.read`. Ignore client identity/operation headers and query values. The downstream handler still owns current authorization.
3. Quota denial returns 429 with `Retry-After` as decimal whole seconds, rounded up and at least one. Convert without overflowing at the maximum `time.Duration`; include 1 ns, an exact second, a fractional second, and the maximum duration in your tests. Use generic `rate limit exceeded` without identifiers. Do not sleep, retry inline, or call next.
4. `ErrCapacity` returns 503 and generic `limiter unavailable`, with **no** `Retry-After`: this fixture has no safe availability estimate. Do not claim the new identity exceeded a quota. Other unexpected limiter errors return generic 500. Error responses must not expose keys, tokens, paths, or internal errors.
5. For allowed calls invoke next exactly once with the original request and writer. Preserve downstream headers/status/body and context; do not refund quota after downstream failure or cancellation after admission. Use `httptest` and a write-counting spy for no-write cancellation checks. Test missing/forged identity, scope isolation, 429 versus 503, exact reset at full capacity, next not called on refusal, concurrent requests, and downstream response preservation.

Completion: explain rate versus concurrency, quota versus capacity, safe delay rounding, and why one local counter does not enforce a fleet-wide quota. The adapter, rounding helper if needed, and their tests remain yours. Do not add a real token verifier, Redis, proxy policy, or lesson 36's `Close` here.

## Checks and common mistakes

```sh
gofmt -l lessons/37-rate-windows/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier SQL integrations require disposable PostgreSQL and `POSTGRES_TEST_URL`; without it they skip. Common mistakes: unbounded identity maps; evicting a live key to admit another; resetting a live key on every request; blocking an expired existing key at full capacity; treating state capacity as a personal quota; trusting client identity; delaying inside a handler; integer overflow during delay rounding; and advertising local limits as distributed guarantees.

Sources: [Go monotonic clocks](https://pkg.go.dev/time#hdr-Monotonic_Clocks), [429 semantics](https://www.rfc-editor.org/rfc/rfc6585.html#section-4), [Retry-After syntax](https://www.rfc-editor.org/rfc/rfc9110.html#name-retry-after). HTTP mappings and cancellation precedence above are this exercise's explicit policy.
