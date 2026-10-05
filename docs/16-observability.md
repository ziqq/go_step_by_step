# Lesson 16: Observability with logs, metrics, traces, and profiles

## Goal

Make a running HTTP service easier to understand when something goes wrong. This example uses Go's structured logger, a small bounded-cardinality metrics endpoint, and the built-in pprof profiles and execution trace.

These signals answer different questions:

- **Logs** describe individual events and include a generated request ID for correlation.
- **Metrics** summarize behavior over time, such as request counts and total duration.
- **Distributed traces** connect spans across service boundaries. A request ID alone is not a trace.
- **Profiles** show where a process spends CPU, memory, or time waiting. A Go execution trace explains scheduler and goroutine activity inside one process.

The sample has no external telemetry dependency. For distributed tracing in a real service, use OpenTelemetry instrumentation; see the [official Go documentation](https://opentelemetry.io/docs/languages/go/) and [otelhttp](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp). Check a selected release's Go requirement against this repository's go.mod before adding it.

## Run the service

The public API listens on :8080. Metrics and profiling use a separate listener bound only to 127.0.0.1:6060.

```sh
go run ./lessons/16-observability
```

Make a request and inspect its response header:

```sh
curl -i http://127.0.0.1:8080/reports
```

The response includes a server-generated X-Request-ID. The same ID appears in the JSON log record. Client-supplied IDs are not trusted, and query strings are not logged.

Read metrics from the local diagnostics listener:

```sh
curl http://127.0.0.1:6060/metrics
```

Example output:

```text
# TYPE go_course_http_requests_total counter
go_course_http_requests_total{route="reports",method="GET",status_class="2xx"} 1
# TYPE go_course_http_request_duration_seconds summary
go_course_http_request_duration_seconds_count 1
go_course_http_request_duration_seconds_sum 0.000021000
```

Labels come from fixed route, method, and status-class values. Do not label metrics with raw URLs, tenant IDs, user IDs, or request IDs: each distinct label set creates another time series.

## Inspect profiles and a Go execution trace

Use the local heap profile with go tool pprof:

```sh
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
```

Capture a short runtime execution trace, then open it with go tool trace:

```sh
curl -o trace.out 'http://127.0.0.1:6060/debug/pprof/trace?seconds=5'
go tool trace trace.out
```

The pprof trace is a Go runtime execution trace, not a distributed trace with spans propagated to PostgreSQL or another service. For cross-service causality, use OpenTelemetry and propagate trace context. The [Go net/http/pprof documentation](https://pkg.go.dev/net/http/pprof) describes the endpoints and profile tools.

The diagnostics mux is not mounted on the public API. The server binds it to loopback because profiles can reveal process details and consume resources. In production, expose diagnostics only through a deliberately protected management network; do not publish port 6060 to the internet.

## How the instrumentation works

The JSON slog handler emits parseable records. Middleware generates a random request ID, replaces any client-supplied value, adds it to the response and logger, then records a bounded route category, method, status class, and elapsed time.

The response-writer wrapper tracks status and implements Unwrap so http.ResponseController can reach the original writer. It is intended for ordinary buffered handlers. A wrapper can hide optional interfaces such as http.Flusher from direct type assertions; do not copy this middleware unchanged around streaming or WebSocket handlers. Use instrumentation designed for those handlers and test the interfaces they require.

The metrics handler snapshots its counter map under a read lock. Its labels are normalized to a fixed set to keep cardinality bounded. The in-memory counters reset when the process restarts; a production system needs a scraper/exporter and operational retention outside the process.

The debug mux registers pprof handlers explicitly instead of importing net/http/pprof for side effects on http.DefaultServeMux. Keeping separate listeners and muxes makes the public/private boundary visible in code.

## Checks

```sh
gofmt -w ./lessons/16-observability
go test ./lessons/16-observability
go test -race ./lessons/16-observability
go test ./...
go test -race ./...
go vet ./...
```

Focused tests check generated correlation IDs, status and duration metrics, bounded labels for unknown routes and methods, metrics concurrency, ID-generation failures, and that pprof is reachable only through the separate diagnostics handler.

## Typical mistakes

- treating a correlation ID as a distributed trace;
- adding unbounded values such as raw paths, tenant IDs, or request IDs to metric labels;
- logging query strings, credentials, tokens, or personal data;
- exposing pprof on the public listener or assuming it is harmless because it is read-only;
- assuming a custom response-writer wrapper preserves streaming and WebSocket interfaces;
- relying on in-memory counters as durable monitoring data;
- using a runtime execution trace to infer cross-service causality.

## Practice: add a distributed trace to report generation

Instrument GET /reports and one outbound HTTP dependency with OpenTelemetry. Use an in-memory span exporter in tests; no collector or external service is required.

Requirements:

1. Create a server span for report generation and a child client span for the outbound request. Pass the request context through every call.
2. Propagate W3C trace context to the test upstream and verify the child span shares the server trace ID.
3. Record dependency failures and canceled requests without logging secrets or using user-controlled values as span names.
4. Shut down the tracer provider with a bounded context so buffered spans are flushed.
5. Check the repository's Go version when selecting OpenTelemetry module versions; justify a toolchain-baseline change rather than silently raising it.
6. Add tests for a successful call, an upstream error, cancellation, and trace-context propagation.

## Completion criteria

You can distinguish logs, metrics, distributed spans, and Go runtime profiles; explain why metric labels must have bounded cardinality; capture a local heap profile and runtime trace; and show that the pprof listener is separate from the public API. The example tests pass with the race detector, and the new exercise remains yours to implement.
