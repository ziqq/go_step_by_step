# Lesson 26: build metadata and packaging

[Русская версия](ru/26-build-provenance.md)

Plan 50–65 minutes: 10 minutes reading, 10–15 minutes running the example, and 30–40 minutes on the assignment.

## From source to an identifiable artifact

Lesson 25 observed HTTP requests. Now identify the executable producing those logs: which version, source revision, and Go compiler built it? This example is a one-shot CLI, not another server and not a replacement for the earlier chat lessons.

Run from the repository root:

```sh
go run ./lessons/26-build-provenance
```

The CLI emits exactly one JSON document and exits. Example output; `go_version` depends on the compiler:

```json
{"version":"dev","revision":"unknown","go_version":"go1.27.2"}
```

The contract is deliberately small: version is an opaque label of 1–64 ASCII letters, digits, dots, underscores, or hyphens; revision is `unknown` for development or 40 lowercase hexadecimal characters for this SHA-1 repository. It is not a semantic-version parser or a universal Git object-ID validator. Invalid metadata or any positional argument causes a nonzero exit; output errors are propagated.

## Compile-time metadata is not runtime configuration

The example starts with string variables:

```go
var version = "dev"
var revision = "unknown"
```

The linker's `-X` flag replaces these values while building. A constant, or a variable initialized by calling a function, is not an equivalent linker target. Changing `APP_VERSION` on an already-built process cannot rewrite its binary.

From a clean, committed checkout, build and run:

```sh
source_revision=$(git rev-parse HEAD)
build_dir=$(mktemp -d)
go build -trimpath -buildvcs=false \
  -ldflags "-X main.version=lesson26 -X main.revision=$source_revision" \
  -o "$build_dir/build-info" ./lessons/26-build-provenance
"$build_dir/build-info"
```

Check `git status --short` first, including untracked files: labeling dirty source with `HEAD` does not prove that commit produced the artifact. For an uncommitted experiment use revision `unknown`.

`-trimpath` removes filesystem paths from the executable. `-buildvcs=false` disables automatic VCS stamping; here the build explicitly supplies a revision because the Docker context excludes `.git`. These flags alone do not establish bit-for-bit reproducibility or trustworthy provenance. A binary can report any label its builder supplied.

## A small, pinned container

The supplied [Dockerfile](../lessons/26-build-provenance/Dockerfile) builds with `CGO_ENABLED=0` in one stage and copies only the executable into `scratch`. Its builder is Go 1.27.2 on Alpine, pinned to an image-index digest; the root module's Go 1.22 language baseline is unchanged. A digest pins content, not future security updates: review and update it deliberately.

Using the revision from the clean checkout above:

```sh
docker build -f lessons/26-build-provenance/Dockerfile \
  --build-arg APP_VERSION=lesson26 \
  --build-arg SOURCE_REVISION="$source_revision" \
  -t go-course-build-info:lesson26 .
docker run --rm go-course-build-info:lesson26
docker image inspect go-course-build-info:lesson26 \
  --format '{{.Config.User}} {{json .Config.Entrypoint}}'
```

The image configuration should show:

```text
65532:65532 ["/build-info"]
```

The process writes its metadata to stdout and exits; there are no ports or health endpoints. A build-time smoke run rejects invalid labels before producing the runtime image. Build arguments are not a place for secrets.

The read-only reference `tetradka/chat/backend/Dockerfile` also separates build and runtime stages and uses a non-root runtime user, but includes media dependencies. Do not transfer this CLI's `scratch` choice blindly to that backend: HTTPS certificates, timezone data, or FFmpeg require their own runtime packaging decisions.

## Verification and common mistakes

```sh
gofmt -l lessons/26-build-provenance/*.go
go test -count=1 -v ./lessons/26-build-provenance
go test ./...
go test -race ./...
go vet ./...
```

The focused tests cover valid and invalid metadata, argument rejection, JSON output, and writer failures. They also compile and execute a real artifact with synthetic test metadata, verify that runtime environment variables cannot replace it, and inspect its Go build settings. This child build needs the Go toolchain and a writable build cache. These tests do not build the Docker image: run the container commands separately.

Common mistakes: testing only a struct rather than the artifact; using a branch name or short SHA as the full revision; assuming a mutable image tag pins content; confusing a reported revision with signed provenance; assuming an artifact cache or upload is an attestation; publishing credentials in build arguments.

## Assignment: verified CI build artifact — not implemented

Extend `.github/workflows/go.yml` with **one artifact job** that depends on the existing `verify` job. Leave the supplied CLI and existing checks intact.

1. Check out source and derive the full revision from the actual checked-out `HEAD`. For pull requests it may be a merge commit; do not substitute an assumed branch SHA.
2. Build this CLI for the runner with `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, a valid non-default version label, and that revision. Use the compiler version selected by the workflow; do not hardcode an expected `go_version`.
3. Execute the resulting binary. Parse its output as JSON and require exactly the expected version, exact checked-out revision (never `unknown`), and a nonempty compiler version. A build command succeeding is not sufficient.
4. Demonstrate a negative check: wrong or missing revision, wrong version, malformed JSON, or missing compiler version must make your checker exit nonzero. Keep the normal workflow green; test failures deliberately as negative cases.
5. Produce a SHA-256 checksum for the binary and upload the binary, the actual metadata JSON, and the checksum as one workflow artifact with seven-day retention. A local download must allow checksum verification.
6. Preserve the existing format/test/race/vet/build checks and `contents: read` permissions. Do not publish images, releases, packages, or secrets; do not add signing or deployment to this exercise.

Completion: show a successful Actions run, the downloaded matching artifact, a verified checksum, and the failing negative-check cases. Explain why these checks detect accidental mismatches but do not authenticate a malicious builder. Artifact upload is not signed attestation. No workflow solution is provided in this lesson.

## Sources

- [Go linker: -X](https://pkg.go.dev/cmd/link)
- [Go build flags](https://pkg.go.dev/cmd/go#hdr-Compile_packages_and_dependencies)
- [Docker multi-stage builds](https://docs.docker.com/build/building/multi-stage/) and [USER](https://docs.docker.com/reference/dockerfile/#user)
- [Pin base-image versions](https://docs.docker.com/build/building/best-practices/#pin-base-image-versions)
- [Workflow artifacts](https://docs.github.com/en/actions/concepts/workflows-and-actions/workflow-artifacts) and [artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations)
