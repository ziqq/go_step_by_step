# 41. Bound and drain long-lived sessions

[Русская версия](ru/41-session-drain.md) · [Runnable example](../lessons/41-session-drain/main.go)

A chat connection can stay open long after its HTTP upgrade. The connection owner must keep its slot until the session and its cleanup finish. Go's [Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown) leaves hijacked connections such as WebSockets to the application. The chat backend tracks the lifetime of the full WebSocket handler so that shared resources can stay open while it exits.

## Run the example

```sh
go run ./lessons/41-session-drain
go test -count=1 -v ./lessons/41-session-drain
go test -race -count=20 ./lessons/41-session-drain
go vet ./lessons/41-session-drain
```

```text
session stopped
all sessions drained
```

The example simulates a session with one goroutine. It uses a process-wide slot limit and two channels to model the lifecycle independently of the transport.

## Follow one slot

1. Reserve a slot before starting work that will own the connection. Admission returns `errSessionLimit` immediately when all slots are occupied. A successful call hands the caller a release function. Call it on setup failure as well as normal exit, after any cleanup that still uses shared resources.

2. `beginDrain` seals admission under the same mutex that protects the count. An acquire racing with drain either owns a counted slot or returns `errDraining`. Once admission is sealed, `errDraining` takes precedence even if the registry is full.

3. Closing `stopping` broadcasts a request to stop. Session owners observe it and finish their own work. In an actual WebSocket handler, a blocked read may need a transport-level deadline or a connection close before cleanup can finish.

4. The registry closes `done` when drain starts on an empty registry or when the last counted session releases its slot during drain. Repeated calls to `beginDrain` return the same channel, and each release function uses `sync.Once` so that duplicate calls cannot free another owner's slot.

## Practice · Bound one caller's wait for drain

Add `waitDrained(ctx context.Context) error`. The shutdown coordinator calls `beginDrain` and uses its existing shutdown context to wait. Keep the current admission, stop signal, and release contracts.

1. Wait for `done` or for the context to end. Waiting is allowed before drain starts and must leave admission open in that case. A live caller returns nil when drain completes.

2. Return `ctx.Err()` if the context is already done at entry or when the caller observes drain completion. Test an already-canceled context against an already-drained registry so the result cannot depend on which ready branch a `select` picks. A context canceled after a successful observation may race with the return.

3. Canceling one caller's wait leaves every session and other waiters alone. Write a test that cancels one wait while a session still holds a slot, then releases that session and checks that a second caller with a live context succeeds. Also test an already-expired deadline with `errors.Is`.

4. Explain how the coordinator should respond if waiting times out while a session can still use the store. Keep transport-specific termination policy outside the registry.

Use channels to coordinate test goroutines and join them on exit. Use deadlines as guards against a stuck test. Avoid sleeps to infer when an operation started. The existing tests cover the base registry and should still pass alongside your new tests.

## Common mistakes

- Checking `draining` and incrementing `active` in separate critical sections admits work after drain has already completed.
- Releasing a slot as soon as `stopping` closes can signal completion while session cleanup still uses the database.
- Closing shared channels from a canceled waiter lets one caller affect the entire service lifecycle.
- A timeout for waiting does not interrupt a blocked transport operation on its own.

## Completion criteria

The example prints both lines in order. The original and new tests pass under the race detector. Two callers can wait independently, and cancellation returns the context error without changing admission, session ownership, or the shared completion signal.
