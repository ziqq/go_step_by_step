# Lesson 33: own the complete worker lifetime

[Русская версия](ru/33-worker-lifecycle.md)

Trace admission, cancellation, and joining separately. Run the channel-barrier tests, draw a concurrent `Start`/`Stop` timeline, then implement the bounded-shutdown assignment. Lesson 32's jitter assignment remains separate and unsolved.

## Cancellation requests a stop; joining confirms completion

Lesson 32 passes a shared deadline through retry attempts and waits. A process also needs to know when its background work has actually returned. A canceled context alone is not that evidence: the callback may still be releasing resources.

Open [main.go](../lessons/33-worker-lifecycle/main.go) and [the focused tests](../lessons/33-worker-lifecycle/main_test.go). The local demo starts one synthetic worker, waits for its start signal, requests cancellation, joins it, and checks that new work is rejected. It needs no network, Docker, or signal handler.

The read-only chat-backend reference has process-lifecycle accounting that closes admission before waiting for active work. This lesson isolates that ownership rule in a small task group. It does not modify the reference backend, the HTTP server from lesson 12, or the queue, outbox, and retry assignments.

## Give one owner a single-use task group

Construct `taskGroup` with `newTaskGroup(parent)`; its zero value is not supported, and it must not be copied. Use a process-lifetime parent for process workers. A request's context is appropriate only when the work belongs to that request.

`Start` rejects a nil callback with `ErrInvalidTask`. While admission remains open, an already-canceled or expired parent produces its context error without starting a callback. Once `Stop` closes admission, valid new callbacks receive `ErrStopping`. Returning nil from `Start` means the callback was accepted and accounted for, not that it has run successfully.

Parent cancellation reaches accepted callbacks, but the owner still calls `Stop` and waits for its signal. There is no hidden watcher that turns parent cancellation into a completed join.

## Count before starting a goroutine

This fragment runs while the admission mutex is held; `Stop` checks and changes the closing state under the same mutex:

```go
g.active.Add(1)
g.mu.Unlock()
go func() {
    defer g.active.Done()
    task(g.ctx)
}()
```

Moving `Add` inside the new goroutine could let a concurrent wait observe zero before the task is registered. The shared admission lock ensures that a task is either counted before closing or rejected after closing. Waiting then operates on a set that cannot gain new members. Each accepted callback has one matching `Done`.

The callback runs outside the mutex. It can request `Stop` itself, but it must return rather than wait for its own group's completion: joining itself would create a dependency cycle. The group never restarts or reuses its wait group for a second lifecycle.

## Stop admission, request cancellation, then join

The first `Stop` closes admission, cancels the child context, and starts one waiter. Every call returns the same receive-only completion channel. Repeated `Stop` calls do not create another waiter or close the channel twice.

Only after every accepted callback returns does the waiter publish completion:

```go
g.active.Wait()
close(g.done)
```

Callback defers finish before its wrapper calls `Done`, so receiving from the closed completion channel also follows callback cleanup. Concurrent `Stop` callers share this evidence; a return from `Stop` alone does not prove every task has observed cancellation or finished.

Admission and execution are different events. A task accepted just before closing may begin after `Stop` returns and receive an already-canceled context. The callback must check that context before work and pass it through blocking calls. Cancellation is cooperative; the group cannot forcibly interrupt arbitrary code. The tests deliberately hold a callback after cancellation to demonstrate this distinction.

This group owns lifetime accounting, not capacity, persistence, retries, or worker-error aggregation. It does not enforce lesson 30's concurrency budget. Callbacks report their own operation errors; panics follow normal Go process behavior. Process-owned resources used by callbacks must remain available until joining or an explicitly chosen process-termination policy.

## HTTP shutdown has its own boundary

`http.Server.Shutdown` manages its HTTP server; it is not a join for arbitrary background goroutines. Hijacked connections such as WebSockets need their own shutdown coordination too. Closing a listener or seeing `ErrServerClosed` is not evidence that all process work is done.

At the process boundary, decide the ordering of HTTP admission, worker cancellation, joins, and shared-resource closure. This fixture demonstrates the worker part only. The assignment limits the caller's wait without pretending to kill an unfinished callback.

## Run from the repository root

```sh
go run ./lessons/33-worker-lifecycle
go test -count=1 -v ./lessons/33-worker-lifecycle
go test -race -count=20 ./lessons/33-worker-lifecycle
```

Expected output:

```text
worker joined; new task rejected
```

The tests cover empty groups, nil callbacks, cleanup before joining, multiple held tasks, parent cancellation/deadlines, child cancellation without canceling the parent, concurrent repeated stops, admission/stop races, a callback requesting stop, and output errors. Channels establish ordering; watchdogs only fail stuck tests. Race-detector success does not prove every possible schedule, operating-system shutdown, or deployed database/WebSocket cleanup.

## Assignment: bounded shutdown waiting — not implemented

Add `Shutdown(ctx context.Context) error` to this lesson's `taskGroup`. Preserve `Start`, `Stop`, and all published tests; do not change earlier lessons or add another worker manager.

1. Always request `Stop`, including when the caller's waiting context is already canceled. The supplied context controls only how long this caller waits; it must not replace the group's worker context or reopen admission.
2. If the shared join signal is already closed when checked, return nil even if the waiting context is canceled. Otherwise wait for joining or caller cancellation/deadline. Return a context error recognizable with `errors.Is` when waiting ends first. If joining and cancellation race during the wait, either result is allowed; nil requires observing completion.
3. A timed-out call must leave the real join signal open while work remains. Do not close it, claim success, force a callback to return, or spawn a new waiter per call. A later `Shutdown` with an active waiting context must still be able to observe successful joining.
4. Add channel-based tests for successful and repeated shutdown, a held cleanup that outlives the waiting deadline, a later successful join, pre-canceled waiting that still stops admission, already-joined precedence, and concurrent callers with different waiting contexts. Release every held test callback during cleanup. Preserve the cancellation/deadline error identity and rejection of post-stop tasks.

Completion: tests demonstrate that one caller can stop waiting while another still waits for the same unfinished work. Explain why a shutdown timeout is a decision point for the process owner, not proof that goroutines or shared resources are safe to discard. The method and its tests remain yours.

## Checks and common mistakes

```sh
gofmt -l lessons/33-worker-lifecycle/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier SQL integration tests require a disposable PostgreSQL instance and `POSTGRES_TEST_URL`; otherwise they skip. Common mistakes: `Add` after goroutine launch; leaving admission open during `Wait`; closing completion on cancellation; waiting for oneself; losing cleanup on a failed test; launching a waiter for every observer; reusing a stopped group; and closing shared resources while accepted callbacks still run.

Sources: [Go WaitGroup accounting](https://pkg.go.dev/sync#WaitGroup.Add), [Go cancellation](https://pkg.go.dev/context#CancelFunc), [HTTP shutdown boundaries](https://pkg.go.dev/net/http#Server.Shutdown).
