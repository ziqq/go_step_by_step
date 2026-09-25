# Lesson 11: HTTP, JSON, routing, and validation

## Goal

Build a small HTTP API with the standard library. The example covers method-aware routing with `http.ServeMux`, JSON request and response bodies, input validation, HTTP status codes, and isolated handler tests with `httptest`.

This is the boundary between a Go function and a backend service. The target chat backend follows the same general shape: a router selects a handler, the handler validates an HTTP request, and a response helper writes a stable JSON contract.

## Runnable example

The complete example is in `lessons/11-http-json/main.go`. It starts an in-memory test server, sends one request, and prints the response without binding a real port.

```sh
go run ./lessons/11-http-json
```

```text
201 Created id=1 text=hello from HTTP
```

The in-memory server is convenient for a deterministic example. A real process can use the same handler with `http.Server`:

```go
server := &http.Server{Addr: ":8080", Handler: newRouter(store)}
err := server.ListenAndServe()
```

## Routing with ServeMux

Go 1.22 added method-aware patterns to `http.ServeMux`. The pattern `POST /messages` selects only POST requests for that path, while `GET /messages` selects only GET requests.

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /messages", listMessages(store))
mux.HandleFunc("POST /messages", createMessage(store))
```

When the path exists but the method is unsupported, the standard mux returns `405 Method Not Allowed`. Let the router own that decision instead of duplicating method checks inside every handler.

Later, a pattern such as `GET /messages/{id}` can read the path value with `r.PathValue("id")`. The practice task uses this form.

## JSON is a contract

Decode into a concrete request type. `DisallowUnknownFields` catches misspelled or unexpected fields instead of silently ignoring them.

```go
decoder := json.NewDecoder(r.Body)
decoder.DisallowUnknownFields()
var input createMessageRequest
if err := decoder.Decode(&input); err != nil {
	writeError(w, http.StatusBadRequest, "invalid JSON")
	return
}
```

One successful `Decode` does not prove that the body contains only one JSON value. Try a second decode and require `io.EOF`; otherwise a body containing two JSON objects could be accepted accidentally. Limit request size before decoding untrusted input.

Responses should set `Content-Type` before `WriteHeader`. After the status is written, changing headers does not change the response already sent to the client.

## Validation and status codes

The example trims message text, rejects an empty value, and limits it to 280 Unicode code points. `utf8.RuneCountInString` counts user-visible code points more appropriately than `len`, which counts bytes.

The example uses:

- `201 Created` when a message is stored;
- `200 OK` when messages are listed;
- `400 Bad Request` for malformed JSON or invalid input;
- `405 Method Not Allowed` from the mux for an unsupported method.

Do not return internal error details to an HTTP client. Public error messages should be stable and useful to the caller without exposing database, filesystem, or stack information.

## HTTP tests without a listening port

`httptest.NewRequest` creates a request and `httptest.NewRecorder` captures the response. Call the handler directly:

```go
request := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(`{"text":"hello"}`))
response := httptest.NewRecorder()
newRouter(store).ServeHTTP(response, request)
```

This makes tests fast and deterministic. Test the complete contract: status, content type, JSON shape, normalization, and failure cases. `httptest.NewServer` is useful when you specifically need to exercise an actual HTTP client, as the runnable example does.

## Typical mistakes

- registering a handler for a path but forgetting its method contract;
- accepting unknown JSON fields and hiding client typos;
- accepting trailing JSON after the first decoded value;
- using `len` when the limit is expressed in Unicode characters;
- writing a body before setting the status or content type;
- returning `200` for a newly created resource instead of `201`;
- exposing raw internal errors in a public response;
- testing only the happy path and not malformed JSON, empty input, or oversized input;
- sharing a mutable in-memory store without a mutex when handlers can run concurrently.

## Checks

Run the focused package:

```sh
go test ./lessons/11-http-json
```

Run the full repository checks:

```sh
go test ./...
go test -race ./...
go vet ./...
```

Run the example:

```sh
go run ./lessons/11-http-json
```

## Practice: fetch one message by ID

Extend the API with `GET /messages/{id}` in `lessons/11-http-json`.

Requirements:

1. Parse `id` from `r.PathValue("id")` and reject a non-positive or non-numeric value with `400 Bad Request`.
2. Return the message as JSON with `200 OK` when it exists.
3. Return a stable JSON error with `404 Not Found` when it does not exist.
4. Keep the store safe for concurrent handlers; do not return the store's internal slice or mutate it without its lock.
5. Add focused tests for an existing ID, malformed ID, non-positive ID, and missing ID.
6. Keep the existing POST and list contracts unchanged.

Do not add a third-party router. Use the standard library pattern and `PathValue` introduced in Go 1.22.

## Completion criteria

You can explain when a handler writes `400`, `404`, `405`, and `201`, can describe why a second JSON decode is needed, and can prove the new endpoint with `httptest` and the race detector.
