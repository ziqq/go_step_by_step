# Lesson 22: Capstone III — page authorized conversation history

Estimated time: 40–55 minutes.

This step turns an authorized read into a bounded history endpoint. The example checks the verified principal and conversation membership on every page, then returns messages newest-first using a keyset cursor made from `(created_at, message_id)`. The ID is a tie-breaker: timestamps are not guaranteed to be unique.

The target chat backend exposes history as the `messages.load` command and has a richer contract, including message-ID anchors, optional filters, and a history revision. This standalone REST example teaches stable paging and scoping; it is not a copy of that protocol and does not provide a consistent database snapshot across pages.

## Run the example

```sh
go run ./lessons/22-capstone-history
go test ./lessons/22-capstone-history
```

```text
200 OK messages=2 has_more=true
```

The route uses Go 1.22 `ServeMux` method and path wildcards. The wildcard value is read with `Request.PathValue`; see the [Go routing overview](https://go.dev/blog/routing-enhancements). The cursor is encoded with unpadded URL-safe Base64 as documented by [`encoding/base64`](https://pkg.go.dev/encoding/base64#RawURLEncoding).

## Request and page contract

- `GET /conversations/{conversationID}/messages` requires a principal already verified by trusted middleware. `withDemoPrincipal` only simulates that step for this lesson; it is not authentication.
- The optional `limit` defaults to 20 and must be from 1 through 50. Duplicate or malformed values are rejected with `400`.
- The optional `before` cursor marks an exclusive position. Each page is ordered by `created_at DESC, id DESC`; the next page contains rows strictly older than the last returned tuple.
- A cursor is a position, not an authorization credential. Base64 is not encryption or a signature. The handler checks membership again and the store query remains scoped to the requested conversation, even if a client edits a cursor.
- The response contains `messages`, `has_more`, and, when another page exists, `next_cursor`. The in-memory adapter detects the extra row after sorting its local copy; a PostgreSQL adapter can fetch `limit + 1` rows.
- Missing principal is `401`; invalid path/query/cursor is `400`; non-membership is `403`; dependency failures fail closed with a generic `500` or `504` on deadline; success is `200`.

Keyset pagination avoids offset drift when new messages arrive before the current page. A stable unique ordering is essential: ordering by timestamp alone can skip or repeat messages when several share the same timestamp. The in-memory example sorts a copy for clarity; a database implementation should apply the conversation predicate and tuple comparison in SQL, with an index aligned to that scope and order.

## What the tests prove

The tests walk two pages with equal timestamps and assert no duplicates or gaps, verify membership is checked on each page, ensure a denied request never reaches storage, reject bad limits and malformed cursors, keep an empty page as a JSON array, and ensure rows from another conversation are not returned. They do not prove database query plans, cursor integrity, or a cross-page snapshot.

```sh
gofmt -w ./lessons/22-capstone-history
go test ./lessons/22-capstone-history
go test -race ./lessons/22-capstone-history
```

## Exercise — add a creation-time range filter

Extend the history endpoint with optional `created_from` and `created_to` query parameters:

1. Parse both as RFC 3339 timestamps. `created_from` is inclusive and `created_to` is exclusive.
2. Reject malformed values and ranges where `created_from` is not earlier than `created_to` with a stable `400` response.
3. Apply the same range to the first page and every cursor page. Keep the cursor tuple and membership scope unchanged; the client must carry the filters forward when following `next_cursor`.
4. Add focused tests for each boundary, an empty range, malformed input, and a two-page traversal with the same filters. Assert that a denied member still never reaches the store.

Plan at least 35 minutes. Completion means all positive and negative cases pass, page order remains stable for equal timestamps, and the race detector finds no shared-state race.

## Typical mistakes

- paginating only by timestamp and losing rows with equal timestamps;
- using `OFFSET` for a changing message history and assuming later pages stay fixed;
- treating an opaque-looking cursor as proof of access;
- checking membership only on the first page;
- querying by message or timestamp without the conversation scope;
- returning a nil slice as `null` when the API contract requires `[]`;
- embedding filters into a cursor but silently dropping them on the next request;
- claiming that separate page requests share one consistent database snapshot.

## Completion criteria

You can explain the `(created_at, id)` ordering, show why the cursor comparison is exclusive, prove authorization runs for every request, and describe the SQL scope/index needed for a PostgreSQL-backed history reader.
