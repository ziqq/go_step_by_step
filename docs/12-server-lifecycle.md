# Lesson 12: Configuration, structured logs, and graceful shutdown

## Goal

Load and validate process configuration, write structured JSON logs with `log/slog`, return stable HTTP errors, and stop an HTTP server cleanly when it receives an interrupt or termination signal.

These pieces connect the HTTP handler from Lesson 11 to a long-running backend process. The server owns startup and shutdown; handlers own request validation and public response contracts.

## Runnable server

The complete example is in `lessons/12-server-lifecycle/main.go`.

```sh
go run ./lessons/12-server-lifecycle
```

The server listens on `:8080` by default. Open another terminal and request its health endpoint:

```sh
curl -i http://localhost:8080/healthz
```

```text
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{"status":"ok"}
```

The process writes one JSON log record for startup, each request, and shutdown. Press Ctrl+C to send an interrupt signal and observe graceful shutdown.

## Configuration from the environment

The example reads only the settings it needs and validates them once during startup:

| Variable | Default | Meaning |
|---|---|---|
| `APP_ADDR` | `:8080` | TCP host and port |
| `APP_READ_HEADER_TIMEOUT` | `5s` | Maximum time to read request headers |
| `APP_SHUTDOWN_TIMEOUT` | `10s` | Maximum time to wait for active requests during shutdown |

For local development, configuration can be overridden for one process:

```sh
APP_ADDR=127.0.0.1:9090 APP_SHUTDOWN_TIMEOUT=15s go run ./lessons/12-server-lifecycle
```

Reject invalid configuration before opening a listener. A clear startup error is easier to diagnose than a server that starts with an unsafe timeout or an invalid address.

## Structured logging with slog

`slog` stores fields separately from the human-readable message. The example uses `JSONHandler`, so log collectors can query method, path, duration, and address without parsing free-form text.

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
logger.Info("http request completed", "method", r.Method, "path", r.URL.Path)
```

Do not log request bodies, authorization headers, passwords, tokens, or other secrets. Log enough stable context to understand what happened, while keeping private request data out of the log stream.

## Stable HTTP errors

An HTTP error has two parts: a status code for protocol-level handling and a response body that callers can rely on. Keep the public error shape stable and avoid returning internal error strings.

```go
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
```

For this lesson, unknown paths return `404 Not Found` with the same JSON shape every time. In a larger service, map known domain errors to public status codes at the handler boundary and log internal causes separately.
The standard mux returns `405 Method Not Allowed` when a known path is requested with an unsupported method.

## Graceful shutdown lifecycle

The example uses `signal.NotifyContext` to turn Ctrl+C or SIGTERM into context cancellation. On cancellation, `Server.Shutdown` stops accepting new connections and waits for active handlers. The shutdown context is derived from `context.Background()` because the request or signal context is already canceled; it has its own finite timeout.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

if err := server.Shutdown(shutdownCtx); err != nil {
	_ = server.Close()
	return err
}
```

Shutdown is not an unbounded wait. When its deadline expires, the example closes remaining connections and returns an error. This gives deployment systems a predictable upper bound for process termination.

## Testing the lifecycle

Use `httptest` to verify response status and JSON without opening a public port. For shutdown, bind a local listener on an ephemeral port, start one deliberately blocked handler, cancel the server context, then release the handler. The server should let that active request finish before `serve` returns.

This tests behavior rather than merely checking that `Shutdown` was called. The race detector also checks concurrent server and test activity.

## Typical mistakes

- reading environment variables throughout request handling instead of validating once at startup;
- accepting zero or negative timeouts;
- logging secrets or entire request bodies;
- returning raw internal errors to clients;
- using the canceled signal context as the shutdown context, leaving no time to drain requests;
- stopping the process immediately without calling `Server.Shutdown`;
- waiting forever for active requests without a shutdown deadline;
- treating a health endpoint's `200` response as proof that every dependency is ready.

## Checks

Run focused tests:

```sh
go test ./lessons/12-server-lifecycle
```

Run all repository checks:

```sh
go test ./...
go test -race ./...
go vet ./...
```

Run the server and stop it with Ctrl+C:

```sh
go run ./lessons/12-server-lifecycle
```

## Practice: configure the response write timeout

Add `APP_WRITE_TIMEOUT` to the configuration and server in `lessons/12-server-lifecycle`.

Requirements:

1. Default it to `10s` and reject malformed, zero, and negative durations.
2. Apply it to `http.Server.WriteTimeout`.
3. Add table-driven tests for the default, a valid override, and invalid values.
4. Keep the existing defaults and behavior for the other configuration fields.
5. Add a short explanation in a Go comment or in your lesson notes describing how a write timeout differs from the read-header and shutdown timeouts.

Verify the package and repository with the commands above. Do not add a third-party configuration or logging package.

## Completion criteria

You can trace the path from environment settings to a validated server, explain which context controls request work and which one bounds shutdown, and show through a test that active requests finish during graceful shutdown.
