# 50. Negotiate a session subprotocol

[Русская версия](ru/50-session-subprotocol.md) · [Runnable example](../lessons/50-session-subprotocol/main.go)

Lesson 49 checked browser Origin. Before dispatching the JSON commands from lesson 48, the peers also need a shared application protocol. This example requires the illustrative token `chat.v1`. It is separate from WebSocket wire version 13, authentication, and conversation authorization. The reference chat backend uses an upgrader and command envelopes; this lesson explores an additional version-negotiation policy rather than claiming that the reference already uses this token.

## Run and trace

```sh
go run ./lessons/50-session-subprotocol
go test -count=1 -v ./lessons/50-session-subprotocol
go test -race ./lessons/50-session-subprotocol
go vet ./lessons/50-session-subprotocol
```

```text
chat.v1
subprotocol rejected
subprotocol rejected
```

The program runs a pure selection function. It opens no socket and does not perform an HTTP upgrade.

## Follow the selection

1. Pass all values from `r.Header.Values("Sec-WebSocket-Protocol")`. Request offers may span several header lines and comma-separated values. Split each line, trim only surrounding ASCII space and tab, and validate every token. A token is nonempty ASCII letters, digits, or the punctuation listed in `validToken`; quoted strings, slashes, controls, and non-ASCII bytes fail.

2. Track exact tokens in a local map. Duplicates across or within lines fail. Token values are case-sensitive. Header names are case-insensitive, which does not make `Chat.v1` equal to `chat.v1`. This parser deliberately rejects empty comma elements instead of skipping them.

3. Finish validation before selecting `chat.v1`. A malformed later offer invalidates the whole request even when an earlier token matches. Valid unsupported tokens may coexist with the supported token. No match or absent offers fail because this application's endpoint requires a subprotocol.

4. In a real endpoint, reject before upgrade on error and configure the upgrader to emit exactly the selected offered token on success. Keep authentication, Origin, and membership checks. Let the library validate the rest of the handshake and own the 101 response. Do not echo the whole client header or manually generate an upgrade in this example. Configure HTTP header-size limits at the server boundary.

RFC 6455 defines request offers, selection from those offers, and the distinction between request and response header multiplicity. Its general protocol allows no selected subprotocol; requiring one here is an explicit application policy. See [RFC 6455 sections 4.1, 4.2.2, and 11.3.4](https://www.rfc-editor.org/rfc/rfc6455).

## Practice · Validate the server selection

Add `validateSelection(offered []string, response []string) error` on the client side. `offered` contains separate token names from the client's own offer; `response` contains all response header values. Do not implement a WebSocket client or change server selection.

1. Validate the complete `offered` slice: require a nonempty list of unique valid tokens with no surrounding whitespace. Accept a response only when it contains exactly one header value which, after trimming ASCII space and tab, is one valid token present in `offered` with exact case. The response is a single selection, not a comma-separated offer list. Return `errProtocol` on every rejection and nil on acceptance. This application requires a selection even though the general WebSocket protocol allows its absence.

2. Test an offered selection, a valid selected token on either side of the offer list, surrounding response space/tab, absent response, empty response, multiple lines including identical ones, comma-separated values, unoffered token, wrong case, malformed token, and invalid/duplicate/empty offers. Verify neither input slice changes. Preserve the original tests.

3. Run focused and root checks. Explain why a compatible JSON shape cannot excuse an unoffered selection, why the client must reject before accepting application messages, and which parts of a full handshake remain the WebSocket library's responsibility.

```sh
gofmt -w lessons/50-session-subprotocol/main.go lessons/50-session-subprotocol/main_test.go
go test ./...
go test -race ./...
go vet ./...
```

## Common mistakes

- Returning as soon as a supported token appears skips malformed later input.

- Using Header.Get loses later stored header values.

- Lowercasing token values silently changes the application contract.

- Echoing all offers produces a response list instead of one selection.

- Confusing `chat.v1` with wire version 13 or treating it as authenticated identity.

## Completion criteria

Trace all three demo results. Complete the client-selection exercise with positive and negative tests while retaining the server contract. Explain request lists versus one response selection, the application's required-selection policy, and the boundary between protocol compatibility and authorization.
