# Go: from first program to production

[Russian version](ru/COURSE.md)

## How this course works

Each lesson has one small, runnable example, focused tests, and exercises.
Read the lesson, run its code, change it, then complete the exercises before moving on.

Core commands:

```sh
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
```

The course favors the standard library, simple packages, useful zero values, clear errors, and tests. We introduce interfaces only at the consumer boundary when more than one implementation is actually useful. There are no mandatory "layers", repositories, factories, or Clean Architecture diagrams.

## Course map

### 1. Language foundation

1. Data types, variables, constants, zero values, conversions, strings, bytes, and runes.
2. Operators, conditions, loops, and scope.
3. Arrays, slices, maps, and `range`.
4. Functions, multiple return values, `defer`, and errors.
5. Structs, methods, pointers, and composition.
6. Interfaces, type assertions, generics, and package design.

**Project:** a tested command-line expense tracker that stores data in JSON.

### 2. Everyday Go

7. Files, JSON, time, regular expressions, and command-line flags.
8. Table-driven tests, subtests, test helpers, benchmarks, fuzzing, and coverage.
9. Modules, dependencies, documentation, and idiomatic naming.
10. Context, goroutines, channels, `sync`, cancellation, and the race detector.

**Project:** a concurrent command-line log analyzer.

### 3. Backend development

11. HTTP servers with `net/http`, routing, JSON, validation, and `httptest`.
12. Configuration, structured logging, graceful shutdown, and error handling at API boundaries.
13. SQL, PostgreSQL, migrations, transactions, indexes, and query tests.
14. Authentication, authorization, rate limits, and common web-security failures.

**Project:** an HTTP API for the expense tracker.

### 4. Production skills

15. Docker, environment-specific configuration, health checks, and CI.
16. Observability: logs, metrics, tracing, and `pprof`.
17. Performance: benchmarks, allocations, profiles, and load testing.
18. Reliability: timeouts, retries, idempotency, queues, and backpressure.
19. System design: API contracts, caching, background workers, and evolution of a service.

**Capstone:** a production-ready service with PostgreSQL, migrations, API tests, observability, Docker, and CI.

## Project layout as complexity grows

Start with one package. Add a directory only when it owns a coherent responsibility. Commands live in `cmd/<name>`, while code private to the repository can live in `internal/<name>`. Do not create `pkg` by default: move code there only when an external project genuinely needs a stable, reusable API.

The source material follows the official Go documentation, A Tour of Go, Go by Example, and the test-first practice from Learn Go with Tests. Use current Go documentation for language changes; Effective Go remains useful for idioms but predates modules and generics.