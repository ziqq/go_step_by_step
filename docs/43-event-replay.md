# 43. Replay durable chat events with a cursor

[Русская версия](ru/43-event-replay.md) · [Runnable example](../lessons/43-event-replay/main.go)

After reconnecting, a client can ask for durable events after its last applied ID. The reference chat backend loads events for the authenticated tenant and user and advances a session cursor after each successful enqueue. This lesson isolates the replay loop with a fake store and a callback. Lesson 27 covers persisting events, and lesson 42 covers sending transient hints.

## Run the example

```sh
go run ./lessons/43-event-replay
go test -count=1 -v ./lessons/43-event-replay
go test -race -count=20 ./lessons/43-event-replay
go vet ./lessons/43-event-replay
```

```text
4 message created
7 message edited
cursor: 7
```

The demo starts at ID 2 and requests one event per page. It delivers 4 and 7, then reads one empty page. Change the initial cursor to 4 and observe which event remains.

## Follow the cursor

1. The loader reads the authorized stream with an exclusive cursor. A SQL implementation can filter by trusted scope and use `event_id > cursor`, `ORDER BY event_id ASC` and `LIMIT`. IDs can have gaps. The loader must expose a stable order: once the cursor passes an ID, an event with that ID cannot become visible for the first time later. An increasing sequence alone does not guarantee this under concurrent commits.

2. The loop validates an entire page before calling the delivery callback. A page above the limit, a stale ID, a duplicate or a descending pair is a loader error. The loader fills each page up to the requested size whenever enough events are available. A short or empty page ends this pass. Events appended later can be loaded on the next pass.

3. The cursor advances only when the callback returns nil. On a read error, delivery error or canceled context, return the last accepted ID. The store and delivery callback receive the same context and are responsible for honoring it during blocking work.

4. Local queue acceptance and client application are separate milestones. A client reconnects with its own last applied cursor. If a write reached the client but its acknowledgment was lost, the client may see the same ID again and should apply it idempotently.

## Practice · Limit the number of pages per replay pass

Add a positive `maxPages` argument and a `needsAnotherPass` boolean return value. Keep cursor, validation and error behavior.

1. Count each successful load, including an empty page. A short or empty page ends the pass with false. When the last allowed page is full and every event was accepted, return true with the updated cursor. True means another read is needed to know whether anything remains. Stay within the budget even when the final available page exactly fills it.

2. Return false on invalid input, cancellation, or any load or delivery error. Preserve the last accepted cursor. Reject a nonpositive budget before invoking either callback. Check cancellation before reporting a successful pass.

3. Test a full page at the budget boundary, then resume and read an empty page. Test a short final page at the boundary, invalid budget, and failure partway through the last allowed page. Assert the exact load cursors, number of calls, returned progress, and error identity.

## Common mistakes

- An inclusive cursor loads the last event again.
- Advancing to the end of a page before delivery can skip events after a writer failure.
- Stopping at a numeric gap can miss later events.
- Bounding page size alone still permits a long pass when the backlog is large or keeps growing.

## Completion criteria

The example prints the three expected lines. The original and added tests pass with the race detector. A pass respects its page budget, and consecutive passes resume at the last accepted ID.
