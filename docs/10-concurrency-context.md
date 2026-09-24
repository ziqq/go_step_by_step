# Lesson 10: Concurrency and context

## Goal

Learn how to run independent work in goroutines, exchange values through channels, wait with `sync.WaitGroup`, and stop work through `context.Context`. The example is a small worker pool that parses log-like records.

The same boundaries appear in a backend: an HTTP request creates a context, a service starts bounded work, and cancellation prevents workers from continuing after the client or parent operation is gone.

## Runnable example

The complete example is in `lessons/10-concurrency-context/main.go`.

```sh
go run ./lessons/10-concurrency-context
```

```text
0 info connected
1 warn slow request
2 error database unavailable
```

`analyze` accepts a context, a list of jobs, and a worker count. Workers receive indexed jobs from `jobs`, parse them, and send results through `results`. Results can arrive in any order, so the input index is retained and the final slice is sorted.

## Context is a cancellation contract

Pass context as the first argument. Do not store it in a struct, and do not use `context.Background()` inside a function that already received a context.

```go
func Handle(ctx context.Context, request Request) error {
	return service.Process(ctx, request)
}
```

`ctx.Done()` is a channel that becomes readable when the context is canceled or its deadline expires. A `select` lets a goroutine choose between useful work and cancellation:

```go
select {
case job := <-jobs:
	return process(job)
case <-ctx.Done():
	return ctx.Err()
}
```

Cancellation does not forcibly kill a goroutine. Each goroutine must observe the signal and return. This is why every potentially blocking send and receive in the example has a cancellation branch.

## Goroutines and channels

A goroutine is a lightweight concurrent function. A channel transfers ownership of values between goroutines. Establish who closes each channel:

- the sender that owns a channel closes it after sending all values;
- receivers do not close channels they did not create;
- `WaitGroup` waits for workers, but it does not cancel them;
- close `results` only after all workers have finished.

The example has one producer, several workers, and one closer for the results channel. This arrangement avoids sending on a closed channel and gives the main goroutine a clear completion signal.

## Why a worker pool

Starting one goroutine for every input is not always appropriate. A worker pool bounds concurrency with a fixed number of workers. In a real backend this helps avoid opening an uncontrolled number of database requests or outbound calls.

The worker count must be validated before starting goroutines. Shared mutable state also needs an ownership rule: use a mutex, or let one goroutine own the map and receive updates through a channel. The exercise below asks you to choose and explain one of these approaches.

## Typical mistakes

- ignoring `ctx.Done()` in a loop, which leaks goroutines after cancellation;
- closing a channel from the receiver side;
- calling `WaitGroup.Add` after a worker can already call `Done`;
- forgetting `Done` on every return path;
- using a shared map from multiple workers without synchronization;
- relying on completion order instead of preserving an explicit index;
- capturing a loop variable incorrectly when starting goroutines;
- assuming the race detector proves every logical property.

The race detector finds many unsynchronized memory accesses, but it does not prove that cancellation, ordering, or error handling matches the intended contract.

## Checks

Run the focused package:

```sh
go test ./lessons/10-concurrency-context
```

Run the full repository checks:

```sh
go test ./...
go test -race ./...
go vet ./...
```

The race-enabled run is especially important for concurrent code. Run it repeatedly if you change the synchronization or shared-state design.

## Practice: count levels concurrently

Implement `CountLevels` in `lessons/10-concurrency-context/main.go`:

```go
func CountLevels(ctx context.Context, lines []string, workers int) (map[string]int, error)
```

Requirements:

1. Reuse `parseLine` and the same worker-pool limits as `analyze`.
2. Count normalized levels such as `info`, `warn`, and `error`.
3. Return an error for an invalid worker count, an invalid line, or a canceled context.
4. Do not introduce a data race. You may use a mutex around the map or a single collector goroutine that owns it; document the ownership decision in a short comment.
5. Return an empty, non-nil map for empty input.
6. Add tests for normal counts, empty input, invalid input, invalid worker count, and cancellation.

Do not change the behavior of `analyze`. Your completion evidence is:

```sh
go test ./lessons/10-concurrency-context
go test -race ./lessons/10-concurrency-context
```

## Completion criteria

You can explain why `WaitGroup` and `context` solve different problems, identify which goroutine owns each channel, and show that `CountLevels` remains race-free when run with several workers.
