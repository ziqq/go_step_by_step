# Lesson 34: bound a worker queue and drain accepted jobs

[Русская версия](ru/34-bounded-queue.md)

Trace a held worker, fill its waiting buffer, observe rejection, then close admission and drain. Draw the producer/worker timeline and complete the batch-admission assignment. The bounded-shutdown exercise in lesson 33 remains separate and unsolved.

## Capacity needs an overload contract

Lesson 33 accounts for task lifetime but starts a goroutine for every accepted task. Here, one worker consumes a bounded channel of job IDs. The limit counts waiting jobs: with capacity C, at most C jobs wait and one callback executes. Increasing the buffer absorbs a burst; it does not make a slow callback faster.

Open [main.go](../lessons/34-bounded-queue/main.go) and [the tests](../lessons/34-bounded-queue/main_test.go). The local demo holds its first callback with a channel barrier, fills one waiting slot, rejects an extra job, closes admission, releases the callback, and waits for both accepted jobs. It needs no database, network, or timer-based scheduling.

## One worker, one channel owner

Construct `jobQueue` with `newJobQueue(capacity, handle)`. Positive capacity and a non-nil handler are required; invalid configuration returns nil and `ErrInvalidQueue` before starting a worker. The queue has no supported zero value and must not be copied.

`TrySubmit` rejects an empty or whitespace-only ID with `ErrInvalidJob`, even after closing. Nonblank IDs are preserved exactly. A valid ID receives `ErrClosed` after admission closes; while open, it receives `ErrFull` when the send cannot proceed, or nil when accepted. Duplicate IDs are separate jobs. Nil confirms in-memory acceptance, not callback success or durable delivery.

The following fragment runs after checking the closing flag under the admission mutex:

```go
select {
case q.jobs <- job:
    return nil
default:
    return ErrFull
}
```

The default case avoids waiting for channel space. `TrySubmit` can still wait for the short mutex critical section; it is not a lock-free API or a wall-clock latency guarantee. Full describes that admission attempt: the worker may free a slot immediately afterward. Do not respond to `ErrFull` by silently dropping durable work, spinning, or spawning an unbounded set of waiting goroutines. The caller needs an explicit rejection, retry budget, or persistent handoff policy.

The same mutex protects sending and closing the channel. A closed-state check without that coordination can race with `close` and panic on send. `select` with a default does not make sending to a closed channel safe. The channel remains private, so callers cannot bypass its owner.

## Close admission, then drain

The first `Close` marks the queue closed and closes its jobs channel under the admission mutex. Later calls return the same completion signal. No caller waits for callbacks while holding that mutex. The single worker executes callbacks outside it:

```go
for job := range q.jobs {
    handle(job)
}
close(q.done)
```

Closing the jobs channel preserves already-buffered values. The worker consumes them in send order and publishes completion only after the final callback and its deferred cleanup return. Concurrent producers have no promised wall-clock arrival order; sequential successful submissions preserve their order. There is no callback overlap inside this queue.

A callback may request `Close` but must not wait for its own queue's completion. A stuck callback keeps completion open; this draining policy does not cancel tasks or impose a shutdown deadline. A receive from the closed completion signal synchronizes observation of callback writes. Merely returning from `Close` does not establish that callbacks finished. Panic follows normal Go process behavior; this example does not recover it or interpret callback results.

## Keep the durability boundary visible

Capacity bounds the number of waiting IDs, not their byte size or all memory held by callers. This is an in-memory teaching fixture: process exit loses pending jobs, and calling a handler once here does not establish exactly-once external effects.

Lesson 27 stores durable intent in a transaction. A production dispatcher must keep that intent recoverable until its delivery protocol confirms the effect; a successful enqueue is not that acknowledgment. The read-only chat-backend reference, `internal/push/worker.go`, loads bounded batches from a store and records completion/retry separately. It is not implemented with this lesson's channel. No reference-backend files or earlier exercises are changed.

## Run from the repository root

```sh
go run ./lessons/34-bounded-queue
go test -count=1 -v ./lessons/34-bounded-queue
go test -race -count=20 ./lessons/34-bounded-queue
```

Expected output:

```text
processed: job-1, job-2; overload rejected; queue drained
```

The tests cover invalid configuration/IDs, exact waiting capacity, refusal of excess work, FIFO draining, capacity reuse after dequeue, cleanup before completion, duplicate/value preservation, concurrent close calls, a callback closing its queue, and admission/close races. Barriers establish order; watchdogs fail a stuck test. Repeated race checks do not prove all schedules, crash recovery, delivery acknowledgments, or a production throughput target.

## Assignment: all-or-nothing batch admission — not implemented

Add `TrySubmitBatch(jobs []string) error` to this lesson's `jobQueue`, preserving `TrySubmit`, `Close`, and all published tests. This is one admission exercise; do not add another worker, an unbounded backlog, persistence, or network delivery.

1. Reject an empty/nil batch or any blank ID with `ErrInvalidJob` before accepting anything, including when closed. Validate the entire batch. Preserve each accepted ID exactly and allow duplicates as separate jobs.
2. For a valid batch, return `ErrClosed` if admission is closed. Otherwise accept the entire batch only if it fits the waiting slots available at the admission decision. Insufficient space, including a batch larger than capacity, returns `ErrFull` without accepting any member. Do not wait for space or launch a goroutine per job/caller.
3. Accepted batch members must be consecutive in send order and retain their slice order relative to concurrent single/batch producers. A callback may begin before the method returns. Admission atomicity is not a transaction over callback effects; do not promise simultaneous execution or rollback of effects.
4. Add channel-barrier tests for success, duplicates, nil/empty input, a blank final member, a batch larger than capacity, partial free space with zero partial acceptance, successful admission after dequeue, closed-state/validation precedence, and concurrent batch/single submissions racing with `Close`. Check that only accepted jobs run, in the allowed order, and every accepted job drains once. Release held callbacks during cleanup.

Completion: demonstrate that a rejected batch contributes zero callbacks even after draining, and that accepted batch members cannot be interleaved by another producer. Explain why a loop calling `TrySubmit` cannot alone provide this contract. The method and its tests remain yours.

## Checks and common mistakes

```sh
gofmt -l lessons/34-bounded-queue/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier SQL integrations need a disposable PostgreSQL instance and `POSTGRES_TEST_URL`; without it they skip. Common mistakes: counting only queued jobs as all unfinished work; blocking a send while holding the close mutex; checking the closing flag outside synchronization; having a receiver close a channel while producers send; waiting for one's own callback; treating capacity as a byte budget; losing held test callbacks; and acknowledging durable delivery on enqueue.

Sources: [Go channel buffers and ordering](https://go.dev/ref/spec#Channel_types), [select behavior](https://go.dev/ref/spec#Select_statements), [channel synchronization](https://go.dev/ref/mem#Channel_communication).
