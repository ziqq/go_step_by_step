# Lesson 38: bounded operation metrics and coherent histograms

[Русская версия](ru/38-bounded-metrics.md)

Lesson 37 distinguishes quota exhaustion from limiter capacity. To observe these decisions, a service needs aggregates with a fixed vocabulary, not one time series per principal. Extend lesson 16's overview with an owned histogram snapshot and explicit export invariants. Lesson 37's HTTP adapter remains separate and unsolved.

## What is being counted

Open [main.go](../lessons/38-bounded-metrics/main.go) and [the focused tests](../lessons/38-bounded-metrics/main_test.go). `operationMetrics` has a useful zero value. Use a pointer and never copy it after use. `Observe(operation, result, elapsed)` records one caller-classified completed operation; it does not infer HTTP status, perform authorization, or enforce admission.

| Dimension | Exact retained values |
|---|---|
| operation | `history.read`, `message.write`, `other` |
| result | `ok`, `rejected`, `failed`, `other` |
| upper duration bound, seconds | `0.01`, `0.1`, `1`, `+Inf` |

Every unknown spelling, including case/whitespace variants, collapses to `other` independently in each dimension. Raw strings are not retained. There are 12 cells. Each exports four cumulative buckets, one sum, and one count: exactly **72 samples** on every export, including zero-valued cells. This is the application series count per target before scraper-added labels. Instances and changing deployment labels can still multiply monitoring storage.

Do not use tenant/user/conversation/request IDs, raw URLs, tokens, or error messages as labels. A hash of a user ID still creates a distinct label value per user. Fixed labels bound retained state; they do not automatically make a management endpoint safe to publish. The read-only reference backend's `internal/metrics/registry.go` exports operation aggregates, and its architecture protects `/metrics` with a separate management boundary. This lesson is its own in-memory fixture, not a replacement for that registry or its access policy.

## Disjoint storage, cumulative exposition

The array stores one disjoint bin per observation. Boundaries are inclusive: zero and exactly 10 ms enter the first bin; 10 ms + 1 ns enters the second. Negative duration returns `ErrInvalidDuration`, changing nothing. The metric name ends in `_seconds`; sum and bucket bounds use seconds, not milliseconds.

Fragment of the cumulative export loop:

```go
var cumulative uint64
for bin, upper := range [...]string{"0.01", "0.1", "1", "+Inf"} {
    cumulative += cell.bins[bin]
    fmt.Fprintf(&body, "%s_bucket{operation=%q,result=%q,le=%q} %d\n",
        name, operation, result, upper, cumulative)
}
```

For each cell, exported buckets never decrease as `le` increases, and `+Inf` equals `_count`. `_sum` is the total elapsed time, not a percentile. An average cannot reveal tail latency; coarse buckets also cannot reconstruct exact percentiles. Choose production buckets from an actual latency objective. This fixture does not provide PromQL, a scraper, distributed tracing, alerts, or persistence.

The integer nanosecond sum is formatted as decimal seconds without float conversion. A monitoring system may store samples as floating-point values and lose low-order precision. Count or sum exhaustion returns `ErrMetricOverflow` before any bin/count/sum changes; zero-duration observations are still possible at an exhausted sum while count has room. This is an explicit teaching policy, not a recommendation to fail business requests when telemetry fails. A caller owns an appropriate telemetry-error policy; never silently report that a rejected observation was recorded.

## One snapshot, then writer I/O

One mutex protects all cells and every part of an observation. `snapshot()` copies nested arrays under that mutex; the returned value owns its data. The exporter serializes exactly that copy after unlocking. It never rereads a newer count while writing older buckets, and a slow writer cannot hold the observation mutex. Buffer allocation, formatting, and writer I/O are outside the critical section.

`WritePrometheus(w)` requires a non-nil `io.Writer`, returns its error, and reports a short write as `io.ErrShortWrite`. An error may follow partial output: do not append a second document to the same response or assume nothing was transmitted. Export never resets or modifies counters. Multiple writers must independently provide any synchronization their output destination requires. The fixed buffer is bounded by the fixed label vocabulary and integer widths; this does not promise constant latency under mutex contention.

## Run from the repository root

```sh
go run ./lessons/38-bounded-metrics
go test -count=1 -v ./lessons/38-bounded-metrics
go test -race -count=20 ./lessons/38-bounded-metrics
```

The demo exports a complete 74-line document: two metadata lines and 72 samples. This is only the `history.read` / `ok` excerpt; the other categories are also present:

```text
go_course_operation_duration_seconds_bucket{operation="history.read",result="ok",le="0.01"} 2
go_course_operation_duration_seconds_bucket{operation="history.read",result="ok",le="0.1"} 3
go_course_operation_duration_seconds_bucket{operation="history.read",result="ok",le="1"} 3
go_course_operation_duration_seconds_bucket{operation="history.read",result="ok",le="+Inf"} 4
go_course_operation_duration_seconds_sum{operation="history.read",result="ok"} 1.020000002
go_course_operation_duration_seconds_count{operation="history.read",result="ok"} 4
```

The tests cover exact and adjacent nanosecond boundaries, maximum duration, unknown/private inputs, independent categories, invalid input, count/sum overflow, owned snapshots, deterministic complete exposition, writer failures, and concurrent observation/export consistency. A reentrant test writer records a new observation, proving output happens after unlock and uses the earlier snapshot. No scheduling sleep establishes correctness.

## Practice: add a diagnostics-only HTTP adapter

Add `newMetricsHandler(m *operationMetrics) (http.Handler, error)` and `ErrInvalidHandler`; keep the collector and its tests unchanged. Implement one adapter, without opening a listener:

1. A nil collector returns nil and `ErrInvalidHandler`. For any request whose context is already done, return before method dispatch with no header/body writes and no observation. Use a write-counting spy; a recorder's default status alone cannot prove no writes.

2. Support GET and HEAD. Return 200 with `Content-Type: text/plain; version=0.0.4; charset=utf-8` and `Cache-Control: no-store`. GET returns the complete histogram document from one snapshot. HEAD returns those headers and zero body bytes; do not call the body writer, even with an empty slice.

3. Other methods return 405 with `Allow: GET, HEAD` and generic `method not allowed`. Do not echo the path, query, headers, identifiers, or errors. The method/context rules are this exercise's explicit policy.

4. Scraping GET/HEAD, rejecting a method, and observing an already-canceled request must never change operation metrics. Do not wrap the adapter in operation instrumentation or invent a `metrics.scrape` business operation. Do not reset counters after a scrape.

5. Use `httptest` for GET, HEAD, method refusal, canceled contexts, complete fixed labels, repeated reads, and concurrent observation/scraping. Check cumulative buckets and `+Inf == count` within each returned document, response headers, no private input reflection, and unchanged observations after diagnostics-only traffic. Keep writer/network failure claims limited to what the tests demonstrate.

Completion: explain the 72-series bound, cumulative versus disjoint bins, snapshot consistency, and why counters reset on restart. The adapter and its tests remain yours. Mount it only behind an existing protected diagnostics boundary when composing a real service; this assignment does not implement authentication, expose a public endpoint, or solve lesson 37.

## Checks and common mistakes

```sh
gofmt -l lessons/38-bounded-metrics/*.go
go test -p 1 -count=1 ./...
go test -race -p 1 -count=1 ./...
go vet ./...
```

Earlier SQL integrations need disposable PostgreSQL and `POSTGRES_TEST_URL`; without it they skip. Common mistakes: retaining raw labels in a map, hashing private IDs into labels, exporting disjoint bins as cumulative buckets, comparing a bucket and count from different snapshots, holding a lock during I/O, mixing seconds and milliseconds, ignoring short writes, instrumenting the scrape recursively, or presenting local counters as durable monitoring.

Sources: [Prometheus labels and units](https://prometheus.io/docs/practices/naming/), [text exposition and histogram invariants](https://prometheus.io/docs/instrumenting/exposition_formats/), [histograms and summaries](https://prometheus.io/docs/practices/histograms/).
