# Lesson 36: coalesce scoped loads without losing lifetime ownership

[Русская версия](ru/36-load-coalescing.md)

After TTL expiry, many requests can miss together. Trace one leader and two followers, compare follower cancellation with leader cancellation, run the channel-barrier tests, and complete the close-admission assignment. Lesson 35's conversation-invalidation exercise remains separate and unsolved.

## Share a load while it is running

Open [main.go](../lessons/36-load-coalescing/main.go) and [the tests](../lessons/36-load-coalescing/main_test.go). A `loadGroup` keeps one active call per `loadKey`. The first admitted caller is the leader and runs its loader synchronously in its own goroutine. Duplicates wait for that call's result. The group creates no goroutines or completed-result cache.

The key includes exact, nonblank tenant, viewer, and conversation IDs for one fixed representation. Every caller for the same key must mean the same read; a follower's loader is not called. Include all other representation-changing inputs in a real key. Each caller must still pass current authorization before using private results. Do not apply this fixture to mutations or durable delivery.

The reference chat backend's `internal/storage/postgres/command_result.go` serializes durable mutation receipts in PostgreSQL. This lesson neither replaces that protocol nor claims the backend uses this in-memory group. The reference remains read-only.

## Admission and completion contract

`newLoadGroup(capacity)` requires a positive active-key capacity; otherwise it returns nil and `ErrInvalidGroup`. Construct the group before use and never copy it.

| `Do(ctx, key, load)` situation | Result |
|---|---|
| Nil context/loader or blank key field | Empty string and `ErrInvalidLoad` before context/admission checks |
| Valid call whose context is already canceled | Empty string and `ctx.Err()`, with no new load |
| Same key still active | Join it, including at full capacity; ignore this caller's loader |
| New key at capacity | Empty string and `ErrBusy`, no loader execution |
| New key with capacity | Run loader outside the mutex; publish its string and error unchanged |
| Call completed | Remove its key; a later call starts a fresh load after success or failure |

One mutex serializes lookup and active-key admission. It is released before loading or waiting, so independent keys can progress. Capacity counts distinct active loaders, not waiting callers, bytes, HTTP requests, or database connections. Pair it with the request-admission and dependency budgets from lessons 30–32 when adapting a service; this example adds no waiter limit.

## Choose the owner of cancellation

The leader passes its own context to the loader. Cancellation is cooperative: a database/HTTP operation must honor that context and return. A follower may stop waiting independently. Its cancellation neither cancels the leader nor frees the active slot. Even when all followers leave, the leader owns its load until it returns.

Fragment of the follower branch, after releasing the group mutex:

```go
select {
case <-ctx.Done():
    return "", ctx.Err()
case <-call.done:
    if err := ctx.Err(); err != nil {
        return "", err
    }
    return call.value, call.err
}
```

See the runnable file above for validation, registration, and completion. A follower rechecks cancellation after observing completion: an error already visible at that check wins. Cancellation can still happen later. A leader publishes exactly what its loader returns; the group does not overwrite it with a later context error. If the loader ignores cancellation and returns success, followers can receive success. This is an explicit policy, not a guarantee of independent backend lifetime. Detaching a shared load would require a separate owner, budget, and shutdown protocol.

Result fields are written before the completion channel closes. Followers read them after receiving that signal, which publishes those writes. The example shares strings and an error whose value must not be mutated concurrently. Returning mutable slices or maps would require ownership/copy rules such as lesson 35's.

## Release on abnormal exit; do not hide it

A deferred completion removes the key and wakes followers even if the loader panics. The call starts with `ErrLoadAborted`; normal return replaces it with the loader result. On abnormal exit, followers receive empty data and `ErrLoadAborted`. The original panic propagates in the leader's goroutine; this group does not recover or turn the leader's panic into a successful return. An uncaught panic still terminates the process. A blocked loader cannot run its deferred completion until it exits.

A loader must not synchronously reenter this group with the same key: that would wait on itself. Using another key is allowed when capacity permits. The tests hold callbacks with channels, observe a follower's `Done` evaluation after joining, then release/cancel in a known order. Their timeout only guards against deadlocks; sleeps are not scheduling evidence.

## Run from the repository root

```sh
go run ./lessons/36-load-coalescing
go test -count=1 -v ./lessons/36-load-coalescing
go test -race -count=20 ./lessons/36-load-coalescing
```

Expected output:

```text
sequential results=hello, hello; loads=2 (completed results are not cached)
```

The short demo deliberately shows sequential reloading. `TestSameKeyFollowersShareOneLoadAtCapacity` demonstrates overlapping calls sharing a single loader. Also inspect the scope, wrapped-error, cancellation, panic, independent-key, and concurrent-capacity tests; draw their timelines before implementing the assignment.

## Practice: close admission and join active loaders

Add `Close() <-chan struct{}` and `ErrClosed`, preserving existing open-group behavior and tests:

1. The first close atomically stops admission. Repeated/concurrent calls return the same signal without waiting inline or creating a helper goroutine. An empty group completes immediately.
2. `Do` checks validation first, then its context, then the closed state before joining or leading. A valid live-context call after closing returns empty data and `ErrClosed`, even for an existing key. Already admitted followers may still receive their result or cancel normally.
3. Do not cancel accepted loaders. Publish the shared close signal only after every accepted loader has exited, including deferred cleanup/abnormal-exit bookkeeping and publication of its call result. It need not wait for every follower to return from `Do`. Never release an active slot just because a follower left.
4. A loader may call `Close`, but must not wait for its own group's completion. Test empty/repeated close, a held leader with a joined follower, rejection of both new keys and duplicates, validation/cancellation precedence, loader cleanup before completion, and admission racing with close. Include a caller-recovered loader panic while closing. Use channels and join test goroutines during cleanup.
5. Explain why returning from `Close` does not establish completion and why this drain policy needs a separate shutdown wait budget. Do not implement a timeout, forced termination, cache adapter, or lesson 35's invalidation here.

Completion: no post-close load or join is admitted; every pre-close leader is accounted for, and all close callers observe one completion signal. Explain that follower delivery and loader completion are different events. The method and its tests remain yours.

## Checks and common mistakes

```sh
gofmt -l lessons/36-load-coalescing/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier SQL integrations require disposable PostgreSQL and `POSTGRES_TEST_URL`; without it they skip. Common mistakes: treating coalescing as a TTL cache; omitting authorization scope; using different loads for one key; holding the mutex during I/O; canceling the leader when a follower leaves; promising a waiter bound; reading results before completion; and catching a panic without releasing its key.

Sources: [duplicate suppression in Go's singleflight package](https://pkg.go.dev/golang.org/x/sync/singleflight), [context cancellation contract](https://pkg.go.dev/context#Context), [channel publication](https://go.dev/ref/mem#Channel_communication). This teaching implementation has its own admission and lifetime policy; it is not an API-compatible singleflight replacement.
