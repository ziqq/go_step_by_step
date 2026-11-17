# 45. One writer and per-write deadlines for a chat session

[Русская версия](ru/45-session-writer.md) · [Runnable example](../lessons/45-session-writer/main.go)

A session can have several producers. Lesson 44 reads durable events, and a ticker tells the session when to send a ping. Give one goroutine ownership of the writes so the producers can hand off work without competing to write to the connection. The reference chat backend has a writer pump that selects between outbound frames and a ping ticker and sets a deadline on each write.

## Run the example

```sh
go run ./lessons/45-session-writer
go test -count=1 -v ./lessons/45-session-writer
go test -race ./lessons/45-session-writer
go vet ./lessons/45-session-writer
```

```text
sent event: message 42
sent ping
```

The example uses an injected tick and a fake writer. It models outbound scheduling only. In a WebSocket session, the writer must set the actual transport deadline or use a write operation that honors cancellation. The reader handles pong responses and can cancel the shared session when the peer stops responding.

## Follow the writer

1. The loop takes one frame at a time. Events on one channel retain their receive order, while a ping occupies its own write. The transport callback finishes before the next write starts.
2. Derive a fresh context from the session context just before each write. Call its cancel function as soon as the write returns. The parent deadline still bounds the child deadline, and cancellation reaches the writer while it waits for a frame or a cooperative write.
3. A write error ends the session. Preserve the cause with %w. A failed write may have sent part of a frame, so reconnect and resume from the last accepted cursor when durable delivery is needed.
4. The event producer closes its channel after the final event. Buffered events remain readable. An exhausted event channel ends the loop. Closing the tick channel disables pings and leaves events active. Canceling a session can discard queued events.

## Practice · Give a pending ping priority

Change the writer so a tick already pending when the loop is ready to choose its next frame is handled before the next event. Keep one writer and the per-write deadline.

1. If a tick arrives during an event write, let that write finish. If the tick is still pending, write a ping before the next event. Preserve event order, the closed-tick behavior, and cancellation before the next write.
2. Write a test that holds the first event write with a channel, queues a tick and a second event while that write is held, then releases it. Use an overall timeout and join the writer goroutine. Verify the order event, ping, event. Add a case where the session is canceled while the first write is held and no further write starts.
3. Explain how the ticker controls ping arrival in the real session. Keep the demo runnable with its injected tick.

## Common mistakes

- Starting a goroutine for each write gives up ownership of ordering and lets slow writes accumulate.
- Reusing one write timeout for the entire session makes healthy later writes inherit an expired deadline.
- Deferring each cancel until the loop returns keeps completed writes' timers alive longer than necessary.
- Adding another select case does not give a ready ping priority over a ready event.

## Completion criteria

The example sends an event followed by a ping. Original and new tests pass with the race detector. A pending ping goes before the second event without interrupting the first write. Cancellation ends the session without starting a later write.
