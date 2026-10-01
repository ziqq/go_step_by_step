# Lesson 14: Authentication, authorization, and web security

## Goal

Separate authentication (who made the request?) from authorization (may that identity perform this action?). Add a small HTTP authentication middleware, tenant and role checks, and a concurrency-safe rate limiter.

The demo uses `net/http` and `httptest`. Its fixed token table is only a teaching fixture: it does not implement JWT, issue credentials, or store real users. It resolves a role separately from the token-to-identity lookup. Do not copy the fixture into a deployed service. The target chat backend validates JWTs with a maintained library, pins the accepted algorithm, requires an expiration and subject, and keeps role resolution separate from token parsing in `internal/auth/jwt.go`. Its database queries also scope records by tenant ID.

Bearer tokens are credentials: send them in the `Authorization` header over TLS, never in URLs, and do not log or echo them. RFC 6750 distinguishes an invalid token (normally 401) from a valid token with insufficient privilege (403). The request's principal is request-scoped data, which is an appropriate use of Go's `context` package.

References: [RFC 6750, sections 2 and 3.1](https://www.rfc-editor.org/rfc/rfc6750.html#section-3.1), [Go `context` package](https://pkg.go.dev/context#WithValue), and the read-only target backend reference at `internal/auth/jwt.go`.

## Run the example

Start the local server:

```sh
go run ./lessons/14-auth-security
```

In another terminal, request the `acme` tenant's reports with the demo viewer token:

```sh
curl -i -H 'Authorization: Bearer viewer-demo-token' http://localhost:8080/tenants/acme/reports
```

The response is JSON with status 200. The in-memory rate limiter allows two requests by this principal per minute; a later request returns 429 and a `Retry-After` header. Restarting the process clears its demo tokens and rate-limit state.

Example response body:

```text
{"tenant_id":"acme","message":"reports are available"}
```

## Authentication middleware

Authentication verifies one credential and creates a trusted identity containing a user ID and tenant ID. Authorization resolves the current role separately and combines it with that identity into a principal for the handler. The example rejects missing or unsupported authentication with 401 and a `WWW-Authenticate` challenge, rejects malformed or duplicated Bearer headers with 400, and rejects unknown credentials with 401. Error responses never echo the credential.

Authentication middleware stores the verified identity in the request context; authorization middleware adds a principal after resolving the role. Downstream handlers read these values from context, not from query parameters, request bodies, or client-supplied identity headers. The unexported context-key types avoid collisions with values from other packages.

In a real service, use its configured, maintained token verifier. Validate the permitted signing algorithm, signature, expiry, and required issuer/audience according to the service contract. Do not write a JWT parser or signing scheme yourself. A token's tenant and role claims are not a substitute for checking current membership or permissions when the application requires revocation or immediate role changes.

## Authorization and tenant boundaries

After authentication, the endpoint compares the requested `tenantID` path value with the authenticated principal's tenant and checks that the role is allowed. A mismatch and a disallowed role both return 403 without revealing which check failed.

Repeat tenant scoping at the data boundary too: SQL that reads or changes a tenant-owned row should include the authenticated tenant ID in its parameters and predicates. A correct HTTP check does not make an unscoped database query safe.

## Rate limiting

The fixed-window limiter tracks a count per authenticated tenant/user pair. A mutex protects its map and counter, and a test exercises concurrent calls. When the limit is reached, the response is 429 with a `Retry-After` value in seconds.

This tiny limiter is intentionally process-local and retains map entries. It is not suitable as-is for a public service: multiple instances need a shared policy or gateway, retained keys need bounded cleanup, and unauthenticated endpoints need their own abuse controls. Do not trust `X-Forwarded-For` unless a trusted proxy removes client-supplied values and supplies the canonical address.

## Tests

The focused tests cover successful authenticated access, missing/invalid/malformed credentials, duplicate authorization headers, cross-tenant and role denial, rate-limit responses, window reset, invalid limiter configuration, and concurrent access.

```sh
go test ./lessons/14-auth-security
go test -race ./lessons/14-auth-security
```

Run the repository checks as well:

```sh
gofmt -w ./lessons/14-auth-security
go test ./...
go test -race ./...
go vet ./...
```

## Typical mistakes

- treating authentication as proof that every action is authorized;
- trusting a tenant ID, user ID, or role sent in a request body or header;
- checking a tenant only in the handler and omitting it from the database query;
- returning 401 for an authenticated user who lacks permission instead of 403;
- logging, exposing, or putting bearer tokens in URLs;
- accepting a token's algorithm without an allow-list or implementing JWT cryptography by hand;
- using an unbounded, process-local limiter as if it protects a multi-instance service;
- updating shared limiter state without synchronization;
- trusting forwarded client-IP headers from an untrusted network.

## Practice: create a tenant-scoped message

Add `POST /tenants/{tenantID}/messages` to this lesson's server.

Requirements:

1. Require authentication and allow only the `writer` and `admin` roles for this route.
2. Require the path tenant to match the authenticated principal. Return 401 for missing/invalid authentication, 403 for an authenticated but unauthorized request, 400 for invalid input, and 429 with `Retry-After` when the authenticated principal exceeds the existing limit.
3. Accept exactly one JSON object with a `text` field. Trim it, reject empty text, unknown fields, malformed or trailing JSON, bodies larger than 1 MiB, and text longer than 280 Unicode code points.
4. Store created messages in an in-memory, mutex-protected store with increasing IDs. Each record must include its tenant ID and creator's user ID. Return the created record as JSON with status 201; never include the bearer token in a response.
5. Add focused tests for successful creation, reader denial, cross-tenant access, malformed and invalid bodies, unknown fields, trailing JSON, oversized bodies, the Unicode length boundary, and rate limiting. Keep rate-limit and store tests safe under `go test -race`.

Do not implement token issuance or custom JWT cryptography. The practice is about applying an already-verified principal consistently at the HTTP and data boundaries.

## Completion criteria

You can explain authentication versus authorization, why the principal comes from verified server-side authentication, why tenant scope must reach database queries, and the difference between 401, 403, and 429. The demo tests pass, and your new route has positive and negative tests that pass under the race detector.
