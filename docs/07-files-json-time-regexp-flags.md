# Lesson 7: Files, JSON, time, regular expressions, and flags

## Goal

After this lesson you should be able to:

- read a file and wrap an I/O error with context;
- decode JSON into a typed Go struct;
- parse and compare RFC3339 timestamps;
- compile a regular expression and handle invalid patterns;
- build a small CLI with the standard-library `flag` package.

Backend services constantly cross these boundaries: configuration and fixtures are files, API payloads are JSON, event timestamps are time values, and CLI tools need predictable flags. The example is a small event report that can grow into a log or chat-event tool.

## Before you start

Run the example from the repository root:

```sh
go run ./lessons/07-files-json-time-regexp-flags
```

Expected output:

```text
matched events: 3
1 2026-09-21T07:00:00Z message Hello
2 2026-09-21T07:05:00Z system Connected
3 2026-09-21T07:10:00Z message Need help
```

Try filters:

```sh
go run ./lessons/07-files-json-time-regexp-flags -type message -after 2026-09-21T07:05:00Z -pattern help
```

Expected output:

```text
matched events: 1
3 2026-09-21T07:10:00Z message Need help
```

## 1. Read a file and decode JSON

The fixture is a JSON array. Struct tags map JSON keys to Go fields:

```json
[
  {
    "id": 1,
    "type": "message",
    "timestamp": "2026-09-21T07:00:00Z",
    "user_id": "u-1",
    "message": "Hello"
  }
]
```

The Go model uses a typed time field:

```go
type Event struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	UserID    string    `json:"user_id"`
	Message   string    `json:"message"`
}
```

`time.Time` implements JSON unmarshalling for RFC3339 timestamps. A typed field is safer than carrying a timestamp as an arbitrary string, because comparisons and formatting stay explicit.

`loadEvents` uses `os.ReadFile` and `json.Unmarshal`:

```go
data, err := os.ReadFile(path)
if err != nil {
	return nil, fmt.Errorf("read events: %w", err)
}

var events []Event
if err := json.Unmarshal(data, &events); err != nil {
	return nil, fmt.Errorf("decode events: %w", err)
}
```

The `%w` verb preserves the original error for `errors.Is` and `errors.As`. Add context at the boundary where the failure happens: reading and decoding are different failures and should remain distinguishable in the message.

## 2. Validate data after decoding

Valid JSON is not automatically valid application data. The example checks that every event has a positive ID, a type, and a timestamp:

```go
for _, event := range events {
	if event.ID <= 0 || event.Type == "" || event.Timestamp.IsZero() {
		return nil, fmt.Errorf("%w: id=%d", ErrInvalidEvent, event.ID)
	}
}
```

Keep validation close to the input boundary. The rest of the program can then work with a stronger assumption: loaded events have the minimum fields required by the report.

The example deliberately does not reject an empty `message`: a system event might carry no human message. Validation should follow the contract, not guesses about what data “usually” looks like.

## 3. Parse and compare time values

An empty `-after` flag means there is no lower time bound. Otherwise the value must use RFC3339:

```go
func parseAfter(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	return time.Parse(time.RFC3339, value)
}
```

A zero `time.Time{}` is a useful sentinel for “not configured”; test it with `IsZero`, not with a formatted string. The filter keeps events at or after the boundary and removes events for which `Timestamp.Before(after)` is true.

Time values carry a location and an instant. Prefer parsing explicit offsets such as `Z` or `+04:00`; avoid silently interpreting user input in a machine-local timezone.

## 4. Compile and use regular expressions

Compile user input once before iterating over events:

```go
func compileMessagePattern(value string) (*regexp.Regexp, error) {
	if value == "" {
		return nil, nil
	}

	return regexp.Compile(value)
}
```

An invalid expression is an input error, not a reason to panic. A `nil` pattern means “do not filter by message”. `MatchString` checks whether the expression occurs in the message.

Regular expressions are powerful but should stay bounded in scope. Do not use them where a simple equality or prefix check expresses the contract more clearly.

## 5. Parse flags in a testable function

The standard `flag` package parses command-line options. Keeping parsing and work in `run` makes the CLI testable without launching a process:

```go
func run(args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("events", flag.ContinueOnError)
	flags.SetOutput(errorOutput)

	inputPath := flags.String("input", defaultInputPath, "path to a JSON event file")
	eventType := flags.String("type", "", "keep only events with this type")
	afterValue := flags.String("after", "", "keep events at or after an RFC3339 timestamp")
	patternValue := flags.String("pattern", "", "regular expression matched against the message")

	if err := flags.Parse(args); err != nil {
		return err
	}
	// parse options, load, filter, and write the report
	return nil
}
```

`flag` values are pointers because parsing fills them after the definitions are created. `flag.ContinueOnError` lets `run` return an error to the caller and keeps tests in control of the output writer.

The `main` function is intentionally thin:

```go
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

## 6. Read the example with its tests

The data flow is:

```text
flags -> parse options -> read file -> decode JSON -> validate -> filter -> print
```

The tests cover a valid file, invalid JSON, invalid event data, an empty time filter, an invalid timestamp, combined type/time/regexp filtering, CLI output, and an invalid regular expression. The `run` function receives writers and arguments, so tests do not need to replace global process state.

This shape resembles a backend command or maintenance tool: input parsing is explicit, domain data is typed, and failures carry context.

## Common mistakes

- Reading JSON into `map[string]any` when the payload has a stable contract.
- Forgetting JSON tags when external keys use `snake_case`.
- Comparing timestamp strings instead of parsed `time.Time` values.
- Calling `regexp.Compile` inside the loop for every event.
- Treating invalid user input as a panic.
- Using a local timezone implicitly for a timestamp that has no offset.
- Calling `flag.Parse` in a package initializer, which makes tests and reuse harder.
- Letting `main` contain all business logic, making it difficult to test.
- Treating valid JSON as valid domain data without validation.

## Practice

Implement this single exercise in `lessons/07-files-json-time-regexp-flags/main.go` and add focused tests in `main_test.go`:

1. Add a `-limit` flag. `0` means no limit; a positive value keeps only the first `limit` events after all existing filters. A negative value must return an error.
2. Add a `-user` flag that keeps only events with the matching `UserID`. An empty value disables this filter.
3. Update the example output and tests for the empty, positive, negative, and larger-than-result limit cases.
4. Keep the JSON file unchanged and preserve all existing behavior.

Run the focused checks:

```sh
gofmt -w lessons/07-files-json-time-regexp-flags/main.go lessons/07-files-json-time-regexp-flags/main_test.go
go test ./lessons/07-files-json-time-regexp-flags
go vet ./lessons/07-files-json-time-regexp-flags
```

## Done when

You can explain where file I/O, JSON decoding, validation, time parsing, regexp compilation, and flag parsing belong; why `time.Time` is preferable to a timestamp string for filtering; and how the tests exercise both valid data and rejected input.
