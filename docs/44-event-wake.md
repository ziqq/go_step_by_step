# 44. Wake signals and periodic reads of durable events

[Русская версия](ru/44-event-wake.md) · [Runnable example](../lessons/44-event-wake/main.go)

Lesson 43 reads durable events after a cursor. A connected session also needs to decide when to read again. The reference chat backend subscribes to a wake signal before loading events, then waits for another signal, a periodic tick, or cancellation. The signal says that the store may have new data. The store supplies the events and their order.

## Run the example

```sh
go run ./lessons/44-event-wake
go test -count=1 -v ./lessons/44-event-wake
go test -race ./lessons/44-event-wake
go vet ./lessons/44-event-wake
```

```text
initial read applied event 1
wake read applied event 2
periodic read applied event 3
```

The demo injects both signals in sequence so the output is repeatable. A service can pass a ticker's channel and stop the ticker when the session ends.

## Follow the loop

1. Subscribe before the initial read. An event can be committed while that read is in progress. A buffered wake signal can remain available until the loop starts waiting.
2. Refresh immediately, then refresh after a wake or tick. The callback owns the last accepted cursor and runs serially. Several notifications can represent one refresh. A periodic tick prompts a read even when a notification was lost.
3. A closed channel is always ready. Disable that case by assigning nil to the local channel variable after detecting closure. If both inputs close, the loop waits for context cancellation.
4. Stop on a refresh failure and preserve its cause. Check cancellation before each refresh and pass the context to the callback so any blocked I/O can also stop.

## Practice · Coalesce pending wake signals

Add a nonblocking helper that signals a wake channel of capacity one. The caller owns the channel and keeps it open until all producers have stopped.

1. Call the helper after a successful event commit. A pending signal already means a refresh is needed. Repeated calls while the channel is full must return promptly and leave exactly one signal queued.
2. Verify repeated and concurrent calls with no reader. Drain the one pending signal and verify a subsequent call queues a fresh signal. Use a timeout to catch a blocked producer and join all test goroutines.
3. In the demo, replace the direct wake send with calls to the helper. Keep the periodic read and describe how it covers a missing notification. Explain why one shared channel consumed by several sessions would wake only one receiver per signal.

## Common mistakes

- Waiting for a signal before the first read delays existing events until a notification or tick arrives.
- Sending before a commit can wake a reader before the event becomes visible.
- Treating a closed channel as a new wake can cause a tight loop.
- Using an unbounded queue for wake signals lets repeated notifications accumulate during a slow refresh.

## Completion criteria

The example prints all three reads in order. The original and new tests pass with the race detector. Bursts leave one pending wake, a later signal can queue again, and periodic reads still work.
