# 49. Check browser Origin before WebSocket upgrade

[Русская версия](ru/49-browser-origin.md) · [Runnable example](../lessons/49-browser-origin/main.go)

Before a browser reaches the session reader from lessons 47–48, its HTTP handshake passes an Origin gate. The chat backend has a CheckOrigin callback on its upgrader. This example studies a browser-only endpoint with two fixed trusted origins.

## Run and trace

```sh
go run ./lessons/49-browser-origin
go test -count=1 -v ./lessons/49-browser-origin
go test -race ./lessons/49-browser-origin
go vet ./lessons/49-browser-origin
```

```text
204
403
```

The handler returning 204 is a stand-in for the upgrade boundary. The demo performs HTTP handler calls through httptest. Connect this gate before the real upgrader in an application.

## Follow the gate

1. Read Header.Values("Origin") and require exactly one value. Reject an absent or empty header, null, repeated lines, and lists. This endpoint deliberately supports browser requests with one trusted origin.

2. Compare the complete value with the fixed allowlist. The scheme and port matter. A suffix such as chat.example.test.attacker.test fails. Exact comparison also rejects user info, paths, queries, and an explicit :443 spelling absent from the list.

3. Reject before calling the next handler. Tests verify zero calls on rejection, one call on acceptance, and a stable 403 body that contains no request data. Host and forwarding headers cannot extend the trusted list.

4. Continue authentication and conversation authorization after the gate. A custom client can supply Origin. The reference backend separately supports native clients with no Origin. That requires an explicitly designed client policy alongside authentication. This lesson's browser-only contract rejects missing Origin.

Origin describes the initiating origin. Header.Values exposes all stored values, which lets this gate check their count. The example uses exact serialized strings and performs no normalization. [RFC 6454](https://www.rfc-editor.org/rfc/rfc6454), [Go Header.Values](https://pkg.go.dev/net/http#Header.Values)

## Practice · Compile a configured browser allowlist

Replace the two literals with a constructor that accepts []string and returns a gate or an error. Validate the entire list at startup before making the gate available.

1. Require at least one entry. Define this exercise's configuration format as https:// followed by a lowercase ASCII DNS hostname and an optional non-default decimal port from 1 to 65535. DNS labels contain letters, digits, or internal hyphens, with no empty label or trailing dot. Require at least two labels. Reject whitespace, wildcard, null, user info, path including /, query including an empty ?, fragment, IP literals, uppercase names, :443, and a port with leading zeros. Parse the URL and validate the remaining parts explicitly.

2. Reject duplicate entries and fail the whole constructor when any entry is invalid. Copy accepted values into privately owned state so later changes to the caller's slice cannot change admission. Keep request matching exact and preserve all original rejection rules. Empty configuration must produce an error.

3. Add constructor tests for valid default and non-default ports, every invalid category, duplicates, mixed valid/invalid lists, and mutation of the original slice. Run concurrent requests against the compiled gate with the race detector. Verify rejection never calls the downstream handler. Explain why trusted configuration is compiled once rather than read from an environment variable on each request.

```sh
gofmt -w lessons/49-browser-origin/main.go lessons/49-browser-origin/main_test.go
go test ./...
go test -race ./...
go vet ./...
```

## Common mistakes

- Prefix or suffix matching admits domains outside the intended list.

- Header.Get hides additional stored values from a count check.

- A wildcard or an empty-list fallback silently broadens access.

- Treating a matching Origin as a principal skips authentication.

- Running the gate after upgrade leaves rejection too late for an HTTP 403.

## Completion criteria

Trace the 204 and 403 paths. Complete the configured-list exercise without weakening the original tests. Show that invalid configuration fails atomically, caller mutation cannot affect the gate, and concurrent admission passes the race detector. Explain the difference between the browser-origin decision and user authentication.
