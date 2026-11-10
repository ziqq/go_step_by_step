# 40. Drain HTTP and workers before closing shared resources

[Русская версия](ru/40-shutdown-order.md) · [Runnable example](../lessons/40-shutdown-order/main.go)

A chat handler and a background worker can share the same PostgreSQL pool. Closing the pool when the listener stops can break work the service already accepted. The owner needs evidence that both groups have stopped using it.

Lesson 12 introduced HTTP shutdown. Lesson 39 showed how to withdraw readiness. The chat backend uses one shutdown deadline for HTTP and lifecycle work, and treats incomplete drain as a separate exit path. Here we isolate the rule that protects a shared resource.

## Run the example

```sh
go run ./lessons/40-shutdown-order
go test -count=1 -v ./lessons/40-shutdown-order
go test -race -count=20 ./lessons/40-shutdown-order
go vet ./lessons/40-shutdown-order
```

```text
readiness off
worker stopped
store closed
```

The demo uses an HTTP server without a listener, one simulated worker, and a fake store. The tests control the boundaries with callbacks and channels.

## Follow the shutdown

1. The service owner calls `shutdownService` once with a fresh context and one deadline. The signal that starts shutdown may already be canceled, so deriving the drain context from that signal would exhaust its budget immediately.

2. Withdraw readiness and signal workers to stop accepting work. The stop callback returns promptly. Its completion channel closes only once workers stop using the shared resource.

3. Call `http.Server.Shutdown` with the same context used to wait for workers. HTTP drain can proceed while workers stop. After both complete, check the context again before closing the resource. A `select` can choose the completion channel when cancellation is also ready.

4. If HTTP drain fails or the budget expires before cleanup begins, return an error and leave the resource open. The owner still needs a policy for ending the process safely. An `io.Closer` has no context parameter, so the deadline bounds cooperative drain and waiting, not the duration of `Close`.

`Serve` returning `http.ErrServerClosed` tells the owner that serving has stopped. The owner still needs to wait for `Shutdown` itself. Upgraded connections such as WebSockets need their own lifecycle tracking. [Go Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown)

## Practice: close several resources in reverse order

Change the final cleanup step to accept a list of acquired resources in acquisition order. The owner calls the coordinator once and supplies non-nil closers. Adapt the demo and test setup while preserving the current drain behavior.

1. Close resources in reverse acquisition order after successful HTTP drain and worker completion. An empty list is valid.

2. Once cleanup starts, attempt every close even after a failure or context cancellation. Use `errors.Join` to preserve every close error so callers can find each cause with `errors.Is`.

3. Test empty and multiple resources, reverse order, two independent errors, cancellation triggered by the first closer, and zero closes after failed HTTP drain or incomplete worker join.

Completion. Explain why cancellation before cleanup can leave resources open, why cancellation after cleanup begins cannot undo a close, and why stopping a listener is insufficient evidence that a shared pool can be closed.

## Common mistakes

- Deferring the pool close in a scope that returns as soon as the listener stops.
- Creating a fresh timeout for each wait and accidentally extending the total budget.
- Treating a worker stop signal as proof that it has finished.
- Assuming `http.Server.Shutdown` joins upgraded connections or that a context interrupts an arbitrary `Close`.
