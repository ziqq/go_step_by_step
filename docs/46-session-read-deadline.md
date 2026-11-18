# 46. Renew the session read deadline on pong

[Русская версия](ru/46-session-read-deadline.md) · [Runnable example](../lessons/46-session-read-deadline/main.go)

Lesson 45 sends pings through a single writer. The reference chat backend sets an initial read deadline and renews it in the pong handler. This lets a blocked read return when a peer stops answering. The example uses one-byte frames on an in-memory connection: P stands for pong and D for application data.

## Run the example

```sh
go run ./lessons/46-session-read-deadline
go test -count=1 -v ./lessons/46-session-read-deadline
go test -race ./lessons/46-session-read-deadline
go vet ./lessons/46-session-read-deadline
```

```text
received application data
peer closed
```

## Follow the reader

1. Set the first deadline before starting the read loop. A silent peer can otherwise hold a goroutine indefinitely.
2. On pong, set the deadline to the current time plus the allowed silence. Ordinary application data leaves that time unchanged, so traffic alone cannot substitute for the heartbeat.
3. Stop if setting a deadline fails. A timeout from the connection remains detectable with errors.Is and os.ErrDeadlineExceeded after it is wrapped.
4. The loop owns reads. The session owner can close the connection to unblock a read during shutdown. A read deadline covers transport I/O. Application handlers need their own budget for work that can block.

## Practice · Cap the session lifetime

Add a positive maxLifetime argument to readSession. The absolute cutoff is the session start plus maxLifetime.

1. Set the initial read deadline to the earlier of the heartbeat deadline and the cutoff. For every pong, cap the renewed deadline at the same cutoff.
2. Reject a zero or negative maxLifetime before touching the connection. If the sampled time is at or past the cutoff when handling a pong, return an error detectable as os.ErrDeadlineExceeded and leave the deadline unchanged.
3. With an injected clock and recording reader, test the initial cap, a renewal that hits the cap and a pong exactly at the cutoff. Test both invalid lifetimes and keep the silent connection test.

## Common mistakes

- Renewing after every read lets ordinary traffic mask missed heartbeats.
- Adding maxLifetime to the time of each pong moves the cutoff indefinitely.
- Treating a timeout as clean EOF loses the reason the session ended.
- Running an unbounded application callback in the read loop can delay the next read even with a transport deadline.

## Completion criteria

The example still prints both lines. The tests pass with the race detector. No deadline falls after the fixed cutoff, invalid lifetimes cause no I/O, and a pong at the cutoff reports a timeout.
