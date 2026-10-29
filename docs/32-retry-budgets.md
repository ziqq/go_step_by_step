# Lesson 32: retries share one time budget

[Русская версия](ru/32-retry-budgets.md)

Trace the retry loop, run the failure cases, then implement the jitter assignment. The JSON adapter from lesson 31 remains your separate, unsolved assignment.

## A retry is more work for a dependency

Lesson 31 bounds one HTTP read. Repeating it can still multiply work or reset a deadline forever. This lesson isolates retry scheduling from networking so you can test its contract without a provider, Docker, or an HTTP adapter. Open [main.go](../lessons/32-retry-budgets/main.go) and [the tests](../lessons/32-retry-budgets/main_test.go).

The demo simulates a safe status read: two temporary failures followed by success. It does not send a notification, enqueue work, decode lesson 31's JSON, or implement lesson 18's delivery worker. The read-only reference chat backend also separates retryable delivery failures from terminal ones and caps scheduled retries. Its durable worker is a different lifecycle; we do not copy or modify it.

## Attempts include the first call

`maxAttempts: 3` means at most three calls, not three retries after an initial call. The first call has no preceding delay. Wait only when another attempt is allowed: never after success, a permanent error, or the final failure.

The configured delays are positive and `maxDelay >= initialDelay`; the overall budget must also be positive. Invalid policy or nil callbacks returns `ErrInvalidRetryPolicy` before work starts. The `read` and `wait` function parameters are small test seams, not interfaces or a retry framework.

The operation marks only an explicitly temporary failure with a sentinel:

```go
return fmt.Errorf("synthetic status unavailable: %w", ErrTemporary)
```

The loop checks `errors.Is`. Unmarked errors stop immediately. Cancellation and deadline errors remain terminal even when joined with the temporary marker. Exhaustion wraps the final read error, preserving `errors.Is`; a wait failure also stops and preserves its cause. Do not classify errors by matching their text or blindly mark all HTTP errors temporary.

## One deadline for calls and pauses

The timeout is created once, outside the loop:

```go
ctx, cancel := context.WithTimeout(ctx, policy.budget)
defer cancel()
```

Every read and wait receives that derived context. An earlier parent deadline remains earlier, parent values survive, and returning releases the child context without canceling the parent. Creating a fresh full budget inside every iteration would extend the logical operation.

Check cancellation before each call and after callbacks. If cancellation is already observed, do not accept success or launch another read. Cancellation racing after the final check can still coincide with a successful return: this is cooperative cancellation, not an atomic ordering guarantee. Callbacks run synchronously and **must honor context**; a callback that blocks forever cannot be interrupted by this loop. Spawning it into a goroutine would hide an abandoned operation rather than stop it.

## Backoff must be bounded and cancelable

For five attempts, an initial delay of 10 ms and cap of 25 ms produces four waits:

```text
10ms, 20ms, 25ms, 25ms
```

`nextDelay` saturates before doubling can overflow `time.Duration`. Its private precondition is a positive delay no greater than the cap. `waitContext` selects between a fresh timer and `ctx.Done()`, then stops the timer. It never resets or reuses a timer channel, so no draining protocol is needed. A one-hour planned pause can end as soon as the shared context expires; `time.Sleep` would not react to cancellation.

Deterministic backoff makes this example inspectable, but many callers could retry together. The assignment adds full jitter. Neither mechanism is a global load limiter: admission from lesson 30 and a service-wide retry policy still matter. Put retry ownership at one deliberate boundary; independently retrying at several layers multiplies calls.

## Keep write semantics out of a read retry loop

The function name does not prove an operation is repeatable. The caller must establish that contract before using it. A timeout cannot prove that a remote write did not commit. Do not attach this example to message creation, push delivery, or a database commit and claim exactly-once behavior. A stable client key alone is not server-side deduplication.

An HTTP integration must separately classify documented status/transport failures, close each response body, preserve per-attempt byte/time limits, and follow the provider's `Retry-After` policy. This fixture implements none of those mappings and does not solve lesson 31's decoder. Avoid logging raw provider errors or payloads while adding retry diagnostics.

## Run and inspect

```sh
go run ./lessons/32-retry-budgets
go test -count=1 -v ./lessons/32-retry-budgets
go test -race -count=20 ./lessons/32-retry-budgets
```

Expected output:

```text
status read succeeded after 3 attempts
```

Tests establish attempt counts, final-error identity, capped delays without sleeps, one shared deadline, parent propagation, child cleanup, invalid configuration, pre-cancellation, cancellation during a read or real timer wait, a real overall timeout, overflow boundaries, and independent concurrent calls. Channels establish cancellation order; five-second watchdogs only detect stuck tests. They do not measure deployed recovery latency or prove behavior of callbacks that ignore context.

## Assignment: full jitter with a controllable source — not implemented

Add a separate `retryReadWithJitter` beside the existing function; keep its contract and existing tests intact. Do not modify earlier lessons or add an HTTP client or worker.

1. Accept the same inputs plus a `sample` callback receiving the current exponential ceiling. Full jitter chooses a duration from zero through that ceiling, inclusive; a real source would sample uniformly. Inject the source so tests can choose exact values without statistical assertions. A real random generator is not required for this assignment.
2. Call the source exactly once for each eligible retry, after checking cancellation. Do not sample before the first read, after success/permanent/context errors, or after the last allowed call. A nil source must fail before any read. A negative or above-ceiling sample must return an identifiable error and start neither the wait nor the next read.
3. Pass the chosen delay to the cancelable waiter, including zero. Advance the exponential **ceiling**, not the sampled value: choosing zero must not collapse later ceilings to zero. Keep the cap, total deadline, terminal error classification, and attempt limit unchanged.
4. Add deterministic table tests for samples of zero, an interior value, the ceiling, and invalid negative/too-large values. Assert the ceilings and waits with recording callbacks; cover exhaustion, no sampling on terminal paths, cancellation after sampling, deadline expiry, and independent concurrent calls. Preserve `errors.Is` behavior. Do not use wall-clock performance thresholds or shared mutable random state.

Completion: test evidence distinguishes the retry ceiling from the chosen wait and proves that jitter cannot create an extra attempt or extend the shared budget. Explain why bounded jitter neither proves a safe write nor caps total fleet traffic. The jitter adapter and its tests remain yours.

## Checks and common mistakes

```sh
gofmt -l lessons/32-retry-budgets/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier SQL tests require a disposable PostgreSQL instance and `POSTGRES_TEST_URL`; otherwise they skip. Typical mistakes: counting only retries; sleeping after the last error; resetting the deadline; retrying cancellation or validation errors; multiplying retries across layers; overflowing delay arithmetic; treating jitter as admission control; and allowing a zero sample to reset exponential growth.

Sources: [Go context deadlines and cleanup](https://pkg.go.dev/context#WithTimeout), [Go timers](https://pkg.go.dev/time#NewTimer), [AWS on retry load, backoff, and jitter](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/).
