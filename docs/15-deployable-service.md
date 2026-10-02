# Lesson 15: Docker, runtime configuration, health checks, and CI

## Goal

Package a small Go HTTP service as a non-root container, configure it at runtime, distinguish liveness from readiness, and run repeatable checks in GitHub Actions.

The example connects environment configuration and the HTTP lifecycle from Lesson 12 to deployment. Its `/readyz` state is deliberately simple; a real API should become ready only after required dependencies such as PostgreSQL pass a bounded `PingContext` check. The target chat backend follows a multi-stage image build and runs the application as a non-root user.

The Dockerfile separates compilation from runtime and copies only the static binary into `scratch`. Its Docker health check executes the same binary's `healthcheck` mode, so the image needs no shell or `curl`. Docker health reports whether a container is healthy; it does not by itself restart an unhealthy container. See [Docker multi-stage builds](https://docs.docker.com/build/building/multi-stage/), the [Dockerfile `USER` and `HEALTHCHECK` reference](https://docs.docker.com/reference/dockerfile/), and [GitHub's Go build-and-test guidance](https://docs.github.com/en/actions/tutorials/build-and-test-code/go).

## Build and run the container

Build from the repository root so the Dockerfile can copy the module files and this lesson's source. The `.dockerignore` keeps Git metadata and local environment files out of the build context.

```sh
docker build --pull -f lessons/15-deployable-service/Dockerfile -t go-course-service:lesson15 .
```

Run the image while overriding its production defaults for local development:

```sh
docker run --rm --publish 8080:8080 --env APP_ENV=development --env PORT=8080 go-course-service:lesson15
```

In another terminal, check the service and its readiness endpoint:

```sh
curl -i http://localhost:8080/readyz
```

```text
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{"status":"ready"}
```

Inspect Docker's health status while the container is running:

```sh
docker inspect --format '{{.State.Health.Status}}' $(docker ps -q --filter ancestor=go-course-service:lesson15)
```

Stop the container with Ctrl+C. The Go process handles SIGTERM, marks itself unready, and gives active HTTP handlers up to ten seconds to finish.

## Image stages and runtime identity

`lessons/15-deployable-service/Dockerfile` has two stages:

1. `build` uses the Go toolchain to compile a static Linux binary with `CGO_ENABLED=0`.
2. `runtime` starts from an empty `scratch` image, copies only that binary, sets production defaults, and uses numeric UID/GID `65532` rather than root.

The build context is the repository root. `.dockerignore` excludes `.git`, local `.env` files, test binaries, and temporary build directories. Never copy credentials into the image or pass them as build arguments. Supply runtime secrets through the deployment platform's secret mechanism. Keep the Go builder image version current with the project's supported toolchain; for stronger supply-chain reproducibility, pin base images by digest.

The container listens on port 8080 by default. `EXPOSE` documents the port but does not publish it; `docker run --publish` creates the host mapping. Runtime environment values override the Dockerfile's `ENV` defaults.

## Runtime configuration and health contracts

| Variable | Default | Accepted values |
|---|---|---|
| `APP_ENV` | `development` locally, `production` in the image | `development`, `test`, `staging`, `production` |
| `PORT` | `8080` | integer from 1 through 65535 |

The service validates these settings once at startup and logs the selected environment and port. Do not create different source builds for development and production merely to change configuration.

- `GET /healthz` is a liveness probe: a 200 means the HTTP process can answer.
- `GET /readyz` is a readiness probe: a 200 means this instance should receive traffic; 503 means it should not.

The sample marks itself ready after creating its listener and clears readiness before shutdown. It has no external dependency. In a database-backed service, readiness should include a bounded, context-aware dependency check, while liveness should not fail just because PostgreSQL is temporarily unavailable. During shutdown, remove readiness before draining active handlers.

Docker's `HEALTHCHECK` invokes `/service healthcheck`, which makes a two-second request to `127.0.0.1:$PORT/readyz`. This is useful for local inspection and some deployment environments, but Kubernetes and other orchestrators may use their own probe configuration. An unhealthy status is a signal for the orchestrator; do not assume Docker automatically restarts the container.

## Continuous integration

`.github/workflows/go.yml` runs on pushes and pull requests targeting `main`. It grants only `contents: read`, selects the Go version from `go.mod`, and checks formatting, tests, the race detector, `go vet`, and compilation. It does not publish images or require registry credentials.

The local equivalents are:

```sh
gofmt -w ./lessons/15-deployable-service
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

The focused tests use `httptest`; they do not require Docker. A successful Go test run does not prove that the container image builds, so run the Docker build command separately when a Docker daemon is available.

## Typical mistakes

- shipping the compiler, source tree, or package manager in the runtime image;
- running the service as root or assuming `EXPOSE` publishes a host port;
- baking `.env` files or secrets into an image layer;
- using a liveness probe as a dependency check and restarting a healthy process during a database outage;
- returning ready before required startup dependencies are available;
- forgetting to make a service unready before graceful shutdown;
- assuming a Docker `unhealthy` status automatically restarts a container;
- testing only `go build` in CI and omitting unit tests, race detection, or static analysis;
- claiming Docker build verification from Go tests alone.

## Practice: add a container smoke job to CI

Extend `.github/workflows/go.yml` with a job that builds and runs this image after the Go verification job succeeds.

Requirements:

1. Build the image from the repository root using the lesson's Dockerfile; do not push it to a registry or add credentials.
2. Run a temporary container with a non-default host port and explicit runtime `APP_ENV`/`PORT` overrides.
3. Poll `/healthz` and `/readyz` with a finite deadline. Fail the job if either endpoint does not return 200, and print container logs when startup fails.
4. Ensure the container is stopped even if a smoke assertion fails. Do not weaken or skip the existing Go checks.
5. Keep the job compatible with the non-root, shell-free runtime image; use host-side tools for HTTP requests rather than assuming `curl` exists inside the container.

## Completion criteria

You can explain the build and runtime stages, how runtime configuration differs from image build arguments, what liveness and readiness mean, and what the CI workflow actually proves. The focused tests pass, the Docker image builds and reports healthy, and the new CI smoke job checks both endpoints without publishing the image.
