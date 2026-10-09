# Lesson 20: Capstone I — compose a message-writing service

Estimated time: 40–60 minutes.

This is the first vertical slice of the capstone. It connects an HTTP handler to an application service and a small store interface, then wires an in-memory adapter at the composition root. The focus is ownership and data flow, not adding layers for their own sake: the store interface exists because the service needs a storage boundary that can later be implemented by PostgreSQL.

The example is a learning REST API, not a copy of the reference chat backend. The latter uses authenticated command envelopes and performs authorization against conversation membership. This sample does not implement authentication or authorization; do not expose it as a real chat endpoint.

## Run the example

```sh
go run ./lessons/20-capstone
go test ./lessons/20-capstone
```

```text
201 Created id=1 conversation=chat-1 text=hello from the capstone
```

The request path is:

```text
HTTP request -> handler validates JSON -> MessageService validates the use case -> MessageStore persists -> HTTP response
```

The handler does not know how IDs are generated or where a message is stored. `MessageService` depends on the small `MessageStore` interface it consumes. `main` chooses the in-memory implementation. A future PostgreSQL adapter can replace it without changing the request contract.

## What each part owns

- The handler owns HTTP concerns: decoding one bounded JSON value, mapping known input errors to `400`, hiding storage details behind a stable `500`, and writing `201 Created`.
- The service owns application-level normalization and validation: a conversation ID is required, text is trimmed, and the message is limited to 280 Unicode code points.
- The store owns persistence details and returns the saved message. The in-memory adapter uses a mutex because HTTP handlers may run concurrently.
- `main` is the composition root: construct the adapter, pass it into the service, and pass the service into the router.

The interface is intentionally defined next to its consumer. There is no separate repository layer, factory graph, or interface per concrete type.

Go 1.22 method-aware `ServeMux` patterns are used for `POST /messages`; the module pins Go 1.22. The standard-library router returns `405 Method Not Allowed` when a path is registered for another method. See the [Go 1.22 routing overview](https://go.dev/blog/routing-enhancements). The handler uses `http.MaxBytesReader` so the JSON decoder cannot consume an unbounded request body; see [`net/http.MaxBytesReader`](https://pkg.go.dev/net/http#MaxBytesReader).

## Contract and checks

The endpoint accepts one JSON object with `conversation_id` and `text`. Unknown fields, malformed JSON, a second JSON value, an oversized body, a missing conversation ID, and blank or overlong text are rejected with a stable JSON `400`. A successful creation returns the stored message (including its generated ID and UTC creation time) as JSON with `201 Created` and a `Location` header. An unexpected store error returns a generic `500`; internal error text is not exposed. Unsupported methods return `405`.

The tests cover service validation and delegation, concurrent ID allocation in the in-memory adapter, the HTTP success contract, invalid JSON/body cases, method handling, and safe error mapping. They do not claim that a database adapter, authentication, authorization, or a production deployment exists yet.

```sh
gofmt -w ./lessons/20-capstone
go test ./lessons/20-capstone
go test -race ./lessons/20-capstone
```

## Exercise — replace the adapter with PostgreSQL

Implement a PostgreSQL-backed `MessageStore` while keeping the service and HTTP contract unchanged:

1. Add a migration for a `messages` table with a generated ID, conversation ID, normalized text, and creation time. Add an index suitable for reading a conversation's newest messages in stable order.
2. Implement `Create` with `pgx` and pass the caller's context to the database operation. Return the database-generated ID and timestamp.
3. Read the database URL from runtime environment configuration; do not commit credentials or bake them into a binary or Docker image.
4. Wire the PostgreSQL adapter in the production composition root while retaining the in-memory adapter for tests or the example.
5. Add integration coverage using `POSTGRES_TEST_URL`. It must verify persisted values and generated fields, and clean up its own test data. When the variable is absent, skip only the integration test with a clear reason.
6. Keep handler tests independent of PostgreSQL and prove the service remains storage-agnostic.

Do not add authentication, an outbox, retries, or a new router in this step. First preserve this vertical slice and its error contract while changing only the storage adapter. Plan at least 40 minutes; completion means the focused unit tests still pass and the opt-in integration test proves one successful round trip against PostgreSQL.

## Typical mistakes

- letting an HTTP handler construct a database pool or execute SQL directly;
- defining an interface with methods the service does not use;
- returning the raw database error to the client;
- omitting the request context from the storage call;
- using an unbounded decoder or accepting trailing JSON;
- sharing a mutable in-memory store without synchronization;
- claiming this example is production-ready even though authentication, authorization, durable storage, and deployment are absent.

## Completion criteria

You can trace the request through the handler, service, interface, and selected adapter; explain why the interface belongs to its consumer; identify which layer owns validation and error mapping; and state what must change before the example is safe to expose as a real chat service.
