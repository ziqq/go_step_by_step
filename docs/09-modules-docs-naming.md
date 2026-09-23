# Lesson 9: Modules, dependencies, documentation, and naming

## Goal

After this lesson you should be able to:

- read the module path and Go version from go.mod;
- understand why dependencies belong in module metadata;
- document exported identifiers in the style expected by go doc;
- choose idiomatic names for exported and unexported Go code;
- keep a small package API deterministic and easy to test.

The example counts event types. It uses only the standard library, but the same module and package rules apply when a backend adds an external dependency.

## Before you start

Run the example and inspect its package documentation:

```sh
go run ./lessons/09-modules-docs-naming
go test ./lessons/09-modules-docs-naming
go doc ./lessons/09-modules-docs-naming
```

Expected output:

```text
events=3 types=message:2,system:1
```

## 1. A module is the unit of dependency management

The repository root contains go.mod:

```text
module github.com/ziqq/go_step_by_step

go 1.22
```

The module path is the import prefix for packages in the repository. The go directive describes the language/toolchain baseline. When an external dependency is added, its module path and version are recorded in go.mod and checksums are normally recorded in go.sum.

Useful read-only commands:

```sh
go list -m
go list -m all
go mod graph
```

Do not run go get just to make an example look more modern. Add a dependency only when the standard library or existing code cannot reasonably provide the needed behavior. After an intentional dependency change, run go mod tidy and review both module files.

## 2. Prefer the standard library when it is enough

The example imports fmt, sort, and strings. No third-party package is needed for counting, normalizing, or sorting:

```go
import (
	"fmt"
	"sort"
	"strings"
)
```

A dependency has a maintenance and security cost. The rule is not “never use dependencies”; make the dependency solve a real problem, pin it in module metadata, and keep the use visible to reviewers.

## 3. Document exported identifiers

Exported names start with an uppercase letter. Their comments start with the identifier name:

```go
// CountByType counts valid event types and ignores events without a type.
func CountByType(events []Event) map[string]int {
	// implementation
	return map[string]int{}
}

// FormatSummary returns a deterministic, human-readable event summary.
func FormatSummary(events []Event) string {
	// implementation
	return ""
}
```

A package comment in doc.go describes the package:

```go
// Package main demonstrates module metadata, package documentation, and naming.
package main
```

Run go doc to see the public surface. Documentation is part of an API contract: explain behavior, empty input, ordering, errors, and important ownership assumptions.

Unexported helpers do not need public API comments, but their names and code should still be clear. Do not export a function only to make a test reach it.

## 4. Choose idiomatic names

Prefer short, precise names:

```go
type Event struct {
	Type    string
	Message string
}

func normalizeType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
```

Common naming rules:

- use ID, HTTP, JSON, and URL for initialisms;
- use userID, not userId, in local names;
- use FormatSummary, not GetFormattedSummary;
- use a short receiver such as e for a small type;
- avoid names that repeat the package name;
- keep exported names necessary and unexported details private.

Names should tell the reader what a value means. A short name is idiomatic only when its scope is small and its meaning is obvious.

## 5. Keep output deterministic

Map iteration order is not a contract. CountByType returns a map because callers need lookup by type. FormatSummary sorts the keys before building text:

```go
types := make([]string, 0, len(counts))
for eventType := range counts {
	types = append(types, eventType)
}
sort.Strings(types)
```

Deterministic output makes CLI behavior, tests, logs, and diffs stable. Never rely on the order in which a map happens to be iterated.

## 6. Read the example with its tests

The package has a small public surface: Event, CountByType, and FormatSummary. normalizeType is an implementation detail. Tests cover normalization, ignored empty types, the non-nil empty map contract, sorted output, and empty input.

The module has no new third-party dependency. The package comment and exported comments can be inspected with go doc, while the tests show the behavior that documentation promises.

## Common mistakes

- Editing go.mod by hand without checking the dependency graph.
- Adding a dependency when the standard library is sufficient.
- Exporting helpers only for tests.
- Writing comments that do not start with the exported identifier.
- Using Id, Http, Json, or Url instead of Go initialisms.
- Returning map data directly as user-visible text without defining ordering.
- Making output depend on map iteration order.
- Using vague names such as data, item, or doThing outside a tiny scope.
- Treating documentation as decoration instead of an API contract.

## Practice

Implement this single exercise in lessons/09-modules-docs-naming/main.go and add focused tests in main_test.go:

1. Add FilterByType(events []Event, eventType string) []Event. It must normalize the requested type, preserve the original event order, and return an empty non-nil slice when nothing matches.
2. Add a Go doc comment for the exported function that describes empty input and ordering.
3. Add tests for a matching type, different casing and spaces, no matches, and empty input.
4. Inspect the package with go doc and verify that the public names and comments are clear. Do not add a dependency or modify go.mod.
5. Keep FormatSummary deterministic and all existing tests green.

Run the focused checks:

```sh
gofmt -w lessons/09-modules-docs-naming/*.go
go test ./lessons/09-modules-docs-naming
go vet ./lessons/09-modules-docs-naming
go doc ./lessons/09-modules-docs-naming
```

## Done when

You can explain what go.mod owns, when a dependency is justified, why exported comments start with the identifier name, why Go uses ID and HTTP initialisms, and why map-backed output must be sorted before it becomes user-visible text.
