# Lesson 18: Reliability under failure

## Goal

Learn how deadlines, bounded retries, idempotency, and backpressure fit together. The runnable example is a small in-memory job queue with a fixed capacity. It demonstrates admission control; it is intentionally not a durable queue or a complete worker system.

Plan at least 30 minutes for the practice: implement the retry behavior, add failure-focused tests, and inspect how cancellation and idempotency interact.

## Run the example and tests

```sh
go run ./lessons/18-reliability
go test ./lessons/18-reliability
```

```text
queue full: event-2 rejected
dequeued: event-1 (key=send-42)
```

The queue has a positive, fixed capacity. `TryEnqueue` either adds the job or returns `ErrQueueFull` immediately. `Dequeue` waits for work but returns when its context is canceled or its deadline expires. If the context is already canceled when `Dequeue` starts, it leaves queued jobs untouched. If cancellation races with a job becoming available, either ready branch may win.

## Timeouts and cancellation

A request's context should flow through the work it starts. A child context can give one dependency a shorter timeout than the overall request, but it must not outlive the parent's deadline. Always call the returned cancel function and pass the derived context to the actual operation; creating a timeout context alone does not stop code that ignores it. See the [Go context documentation](https://pkg.go.dev/context) and the official guide to [canceling database operations](https://go.dev/doc/database/cancel-operations).

Queue saturation also needs a time policy. This example rejects immediately. Another API could wait for queue capacity, but that wait must select on `ctx.Done()` so a canceled request cannot block forever. Avoid retrying a full queue in a tight loop: it consumes CPU without creating capacity.

## Retries and idempotency

Retry only failures that are known to be transient, and cap both the number of attempts and the total time spent. A bounded exponential delay, often with jitter, avoids synchronized retry storms. Respect a dependency's contract such as `Retry-After`; do not retry validation or authorization errors as if they were temporary.

A timeout can leave the caller uncertain whether the remote side committed the operation. A retry is safe only when the logical operation is idempotent or the receiver deduplicates it. Reuse the same idempotency key and same request contents for every attempt. The key in this lesson's `Job` is only data: this in-memory queue does not enforce uniqueness or deduplication. Durable idempotency requires the receiver to store a key and result, usually atomically with its state change. “Exactly once” is not guaranteed by a client retry loop.

## Backpressure is not durability

A bounded queue limits memory and makes overload visible. The caller must handle `ErrQueueFull`, for example by returning a retryable response, applying a documented wait policy, or persisting work elsewhere. An in-memory channel loses queued jobs when the process exits. Durable delivery needs persistent storage, claiming/acknowledgment, recovery after a crash, and idempotent handling of redelivery.

The separate chat backend has durable command receipts and a transactional event outbox. This sample intentionally demonstrates only bounded in-process admission; it does not replace those database guarantees.

## Typical mistakes

- deriving a timeout but not passing its context to the blocking operation;
- forgetting to call the cancel function returned by `context.WithTimeout`;
- allowing every retry attempt its own full timeout without respecting the parent's deadline;
- retrying permanent errors, retrying forever, or retrying immediately in a tight loop;
- generating a new idempotency key for a retry and creating a second logical operation;
- claiming that a stable client key provides exactly-once behavior when the receiver never persists or checks it;
- silently discarding a job after `ErrQueueFull`;
- treating a buffered channel as durable storage or closing a queue while producers may still send.

## Practice: make outbound delivery retry-safe

Extend the example with an outbound notification delivery operation and a small worker that consumes queued `Job` values. Keep it local and use `httptest`; no external provider or database is needed.

1. Give each delivery an overall context and each network attempt a shorter timeout. Stop promptly when the caller cancels or the overall deadline expires, and cancel each derived attempt context.
2. Retry only transport failures and explicitly transient HTTP responses, with a maximum attempt count and bounded backoff. Do not retry permanent `4xx` responses. Keep the total wait within the parent context.
3. Send the job's same `IdempotencyKey` and same payload on every attempt. Do not claim duplicate protection unless the receiver stores and enforces that key.
4. Add tests for immediate success, transient failure followed by success, permanent failure without retry, exhausted attempts, cancellation/deadline, and identical key and payload across attempts. Use an `httptest.Server`; avoid real sleeps and machine-specific timing thresholds.
5. Preserve the queue's FIFO, capacity, overflow, and cancellation behavior. Explain what would need to change before queued jobs could survive a process restart.

## Checks

```sh
gofmt -w ./lessons/18-reliability
go test ./lessons/18-reliability
go test -race ./lessons/18-reliability
go test ./...
go test -race ./...
go vet ./...
```

## Completion criteria

You can trace one deadline through a worker to its HTTP call; distinguish transient from permanent failures; show that retries are bounded and retain one logical idempotency key; explain overload behavior when the queue is full; and state why the example queue is not durable. The outbound retry implementation remains yours.
