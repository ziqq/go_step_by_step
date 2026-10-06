# Lesson 17: Measure before optimizing

## Goal

Use benchmarks, allocation reports, CPU and memory profiles, and a bounded local load test to make performance work evidence-based. The example counts unread messages for one user across conversations, a workload similar to producing chat-list summaries in a backend.

The current `CountUnread` is intentionally straightforward: for each incoming message, it scans the read cursors and selects the latest cursor for that user and conversation. That makes a useful baseline, not a recommended final implementation.

## Run the example and tests

```sh
go run ./lessons/17-performance
go test ./lessons/17-performance
```

```text
unread messages: 1
```

The contract is deliberately small: a user's own messages are never unread; a message is unread only when it is newer than that user's latest matching cursor; a message at the cursor time is already read; and a missing cursor means incoming messages are unread. Cursors for other users or conversations must not affect the result.

## Benchmark the baseline

```sh
go test -run '^$' -bench '^BenchmarkCountUnread$' -benchmem -count=5 ./lessons/17-performance
```

The benchmark builds its input before timing starts. `ns/op` is elapsed time per operation; `B/op` and `allocs/op` report memory allocated during an operation. This function currently creates no per-call allocations, but its repeated cursor scans still cost CPU as the input grows. Do not assert a machine-specific time threshold in a unit test.

Record the exact command, Go version, input sizes, and baseline output before changing the implementation. Run the same benchmark again after optimizing it. Compare repeated runs on the same machine and under similar load; one unusually fast run is not proof of improvement. Correctness tests remain the guard against changing unread semantics.

## Find where time and memory go

Capture CPU and allocation profiles in separate runs. Keeping the test executable and profiles in a temporary directory avoids adding generated files to the repository.

```sh
profile_dir="$(mktemp -d)"
go test -run '^$' -bench '^BenchmarkCountUnread$' -benchtime=5s -cpuprofile "$profile_dir/cpu.pprof" -o "$profile_dir/performance.test" ./lessons/17-performance
go tool pprof -top "$profile_dir/performance.test" "$profile_dir/cpu.pprof"
```

```sh
go test -run '^$' -bench '^BenchmarkCountUnread$' -benchtime=5s -memprofile "$profile_dir/heap.pprof" -o "$profile_dir/performance.test" ./lessons/17-performance
go tool pprof -sample_index=alloc_space -top "$profile_dir/performance.test" "$profile_dir/heap.pprof"
go tool pprof -sample_index=inuse_space -top "$profile_dir/performance.test" "$profile_dir/heap.pprof"
```

CPU profiles point to functions consuming sampled CPU time. `alloc_space` highlights cumulative allocation volume; `inuse_space` highlights memory still live at the profile snapshot. A profile is evidence about the measured workload, not a complete description of production. Profiling adds overhead, and different profile types can interfere with one another, so collect them separately when comparing results. The [Go diagnostics guide](https://go.dev/doc/diagnostics), [`testing.B`](https://pkg.go.dev/testing#B), and [Go pprof article](https://go.dev/blog/pprof) explain the tools in more detail.

## Benchmarking is not load testing

A microbenchmark calls one function with an in-memory input. It is good for comparing that unit of work, but it does not include HTTP routing, database latency, connection pools, queueing, or network behavior. A concurrent benchmark can reveal contention in a function but still is not a production load test.

For a service-level experiment, run only against a local test server or an explicitly approved staging environment. Use fixed input data, a bounded duration and concurrency, and record request count, errors, and latency percentiles. Never send an experiment to production or a shared service without explicit approval. A result from this lesson's function benchmark must not be presented as end-to-end API capacity.

## Typical mistakes

- optimizing code based on intuition without first capturing a repeatable baseline;
- changing semantics to gain speed, or comparing benchmark runs with different inputs;
- timing test-data setup as if it were part of the function under test;
- trusting a single run or a hard-coded `ns/op` gate across different machines;
- treating `B/op` as the same thing as retained heap, or reading a profile without checking its sample type;
- collecting multiple profiles together and interpreting profiling overhead as application behavior;
- treating a microbenchmark or `httptest` benchmark as proof of production throughput;
- running a load test against a public or shared service without authorization and strict limits.

## Practice: index read cursors and measure the result

Improve `CountUnread` so that it does not scan every cursor for every message. Build an index of the latest cursor for each `(user, conversation)` pair, then count unread messages while preserving the existing function contract.

1. Keep all existing tests passing. Add focused cases if your implementation exposes a bug in the contract; do not weaken or delete the current checks.
2. Capture the provided benchmark's baseline before changing the algorithm. After the change, run the same command, input size, and Go version; report the old and new `ns/op`, `B/op`, and `allocs/op`.
3. Capture and inspect CPU and memory profiles both before and after. Explain which function or operation changed and what the profiles can and cannot establish.
4. For a small service-level experiment, wrap the function in a local HTTP handler and issue requests through a local test server with a fixed worker count and time limit. Record total requests, failures, and p50/p95 latency. Keep this separate from the microbenchmark, and do not add brittle latency thresholds to unit tests.
5. Run the race detector and vet after the change. Keep generated profiles and benchmark artifacts outside the repository.

## Completion criteria

You can explain the baseline complexity and the cursor index's tradeoff; preserve the unread-message contract; show repeatable before/after benchmark results; distinguish allocated from retained memory; use a profile to investigate a measured workload; and explain why an in-process benchmark does not establish API capacity. The assignment's implementation remains yours.
