# 47. Bound frame bytes before allocating a payload

[Русская версия](ru/47-bounded-frames.md) · [Runnable example](../lessons/47-bounded-frames/main.go)

Lesson 46 bounds the time spent reading from a silent peer. An active peer can also declare a huge payload. Check the declared byte count before allocating memory. The reference chat backend configures a WebSocket read limit before processing incoming frames.

This example teaches the check with a small length-prefixed format. Four unsigned big-endian bytes declare the payload length. The limit comes from trusted server configuration. A connection owner combines this reader with a read deadline and closes the connection on a framing error. The WebSocket library handles its own wire format and read limit.

## Run the example

```sh
go run ./lessons/47-bounded-frames
go test -count=1 -v ./lessons/47-bounded-frames
go test -race ./lessons/47-bounded-frames
go vet ./lessons/47-bounded-frames
```

```text
received 2 bytes: hi
```

## Follow the boundary

1. Validate the positive configured limit before reading. Read the fixed header with io.ReadFull because a network Read can return fewer bytes than requested.

2. Decode the unsigned length and compare it with the limit using uint64. Convert to int only after the comparison. The payload fits within the positive int limit on the running architecture.

3. A size exactly at the limit succeeds. An oversized length returns errFrameTooLarge before allocating or consuming payload bytes. The caller closes the session after this error because unread payload bytes remain.

4. Zero bytes form a valid empty frame. EOF before any header byte means the stream ended between frames. A partial header or payload means a truncated frame. Preserve the cause so errors.Is can distinguish these paths.

## Practice · Bound total payload bytes per session

Add a session reader that receives a positive per-frame limit and a positive total payload budget and delivers frames through a callback returning error.

1. Reuse the header validation and payload-reading logic. Before allocating or reading each payload, reject a declared size exceeding either the per-frame limit or the remaining budget. Use a distinct error for exhaustion of the session budget. Frame headers do not count toward the budget.

2. Charge payload bytes once the complete payload has been read, before calling the callback. Stop on callback error and preserve its cause. A payload exactly consuming the remaining budget is allowed. Continue reading headers so EOF can end the stream and empty frames remain valid. A later positive-size payload exceeds the budget.

3. Test several individually valid frames that exceed the total budget, exact budget followed by EOF, an empty frame after exact budget, rejection before consuming the excess payload, both invalid configured limits, truncated input, and callback failure without reading the next header. Explain why byte limits must be combined with deadlines and a frame-rate limit when empty frames are allowed.

## Common mistakes

- Allocating from the declared length before validating it lets the peer control memory use.

- Treating every EOF as a normal disconnect hides a partial frame.

- Continuing after an oversized header interprets payload bytes as another length.

- A per-frame bound alone allows a long session to process an unlimited total payload.

## Completion criteria

The demo prints its expected line and original tests pass with the race detector. The new tests verify both budgets, preserve failure causes, and prove an excess payload remains unread. Document why an empty-frame flood needs an additional control.
