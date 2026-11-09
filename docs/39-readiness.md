# 39. Readiness with a bounded dependency probe

[Русская версия](ru/39-readiness.md) · [Runnable example](../lessons/39-readiness/main.go)

Liveness answers whether the process can serve a probe. Readiness answers whether this instance should receive application traffic. If PostgreSQL is temporarily unavailable, removing an instance from routing can help. Restarting every healthy process during the same database outage can make recovery harder.

Lesson 15 kept readiness in memory. The chat backend's readiness route passes a single request-derived deadline through PostgreSQL and media storage checks. This example adds a limit on concurrent dependency checks and shows how to report drain while HTTP can still answer.

## Run and observe

```sh
go run ./lessons/39-readiness
go test -count=1 -v ./lessons/39-readiness
go test -race -count=20 ./lessons/39-readiness
go vet ./lessons/39-readiness
```

```text
/readyz 204
/readyz 503
/healthz 204
```

The demo invokes the mux in memory. It passes a fake dependency to `newProbes`; a real service can pass the method value `db.PingContext`. The constructor expects a non-nil callback and a positive timeout, supplied by composition code.

## Follow one request

1. `GET /healthz` returns 204 without consulting dependencies. `GET /readyz` returns 204 after success and 503 when draining, busy, canceled, expired, or when the dependency fails. Go's GET route patterns also match HEAD. Probe responses have an empty body and `Cache-Control: no-store`.

2. A capacity-one channel admits one active readiness check. A second request gets 503 immediately. The slot remains occupied until the callback returns and is released on every return path.

3. `context.WithTimeout(parent, timeout)` shares cancellation with the caller and honors an earlier parent deadline. Check `ctx.Err()` after the callback so that a late nil error cannot turn cancellation into success. The timeout is cooperative. A callback that ignores it can hold the slot.

4. Call `beginDrain` before shutting down the HTTP server. Requests starting after it returns skip the dependency. An active check that reaches its final drain check after the flag is set also returns 503. A response already decided before that flag changes may still report 204. Draining neither cancels nor joins existing handlers. Server shutdown and load balancer propagation have their own timing.

## Practice: check a fixed set of dependencies under one budget

Replace the single callback with an ordered slice of callbacks and make the constructor return `(*probes, error)`. Update the demo and existing test setup for the signature while keeping their behavioral checks.

1. Reject an empty list, any nil callback, or a nonpositive timeout. Return a nil probe and a generic configuration error. Own a copy of the slice so that replacing an element in the caller's slice after construction does not change the checks.

2. Run checks sequentially in supplied order. Acquire one slot and create one child context for the entire sequence. Stop at the first error. Check cancellation and drain before each callback and again before returning success. Preserve the status codes and empty responses.

3. Use fakes to test configuration errors, order and shared deadline, caller-slice ownership, failure short-circuiting, and drain or cancellation while the first callback runs. Prove that a second probe cannot enter the dependency sequence while the first owns the slot. Use channels to control order and join any goroutines you start.

Completion: all checks must succeed under the same budget before readiness returns 204. Explain why issuing a fresh timeout for each dependency extends the possible wait, and why removing traffic and draining active requests are separate operations.

## Common mistakes

- Calling the database from liveness or sending the dependency error to the client.
- Releasing the slot as soon as a timer fires while the callback still runs.
- Creating a new timeout from `context.Background()` and losing request cancellation.
- Treating a successful ping as proof that all application operations work.
- Assuming an HTTP 503 instantly stops requests already routed to the process.

References: [Go context](https://pkg.go.dev/context#WithTimeout) · [Kubernetes probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/)
