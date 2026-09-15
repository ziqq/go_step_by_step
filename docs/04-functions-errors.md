# Lesson 4: Functions, multiple returns, defer, and errors

[Russian version](ru/04-functions-errors.md)

## Goal

Learn to design small functions, return more than one value, represent failure with error values, wrap errors with context, and guarantee cleanup with defer.

These are the basic tools behind backend handlers: a handler validates input, calls a function, checks its result and error, and returns a safe response.

## Before you start

Run these commands from the repository root:

```sh
go run ./lessons/04-functions-errors
go test ./lessons/04-functions-errors
```

Expected output:

```text
status category: server_error
events: [start finish]
average: 7.50
```

## Function declarations

A function declaration has a name, parameters, and an optional result type:

```go
func average(total, count int) (float64, error) {
    if count == 0 {
        return 0, ErrDivisionByZero
    }

    return float64(total) / float64(count), nil
}
```

The parameters total and count both have type int. The function returns two values: a float64 result and an error. nil means that no error occurred.

Go does not use exceptions for ordinary failures. A function reports a failure as an explicit return value, and its caller decides what to do:

```go
result, err := average(15, 2)
if err != nil {
    return
}
fmt.Println(result)
```

Keep the error check close to the call that produced the error. This makes the failure path visible and prevents a bad result from being used accidentally.

## Multiple return values

Multiple returns are useful when an operation can produce both a value and a failure:

```go
category, err := statusCategory(status)
if err != nil {
    return "", err
}
return category, nil
```

The common convention is to return the useful value first and error second. On failure, return the zero value for the useful result and a non-nil error. On success, return the result and nil.

An error is a value that describes a failure. It should be checked, returned, or deliberately handled. Do not ignore an error with an unused variable just to make the code compile.

## Creating and wrapping errors

Use errors.New for a stable error that callers may recognize:

```go
var ErrDivisionByZero = errors.New("division by zero")
```

Use fmt.Errorf with the %w verb to add context while preserving the original error:

```go
return "", fmt.Errorf("%w: %d", ErrInvalidStatus, status)
```

The message now contains the invalid status, but the caller can still identify the category with errors.Is:

```go
if errors.Is(err, ErrInvalidStatus) {
    // return a validation response
}
```

Compare errors by meaning, not by their text. Error text is for people and can change; a sentinel error or a typed error gives code a stable contract.

Use errors.Is for a known category and errors.As when you need to extract a specific error type. We will use errors.As with custom types in a later lesson.

## Guard clauses

Validate preconditions at the beginning of a function and return immediately when one is invalid:

```go
func inspectStatus(status int, log func(string)) (string, error) {
    if log == nil {
        return "", ErrNilLogger
    }

    log("start")
    defer log("finish")
    return statusCategory(status)
}
```

The nil logger is rejected before it is called. The successful and failed paths after that point share the same deferred cleanup.

## defer

defer schedules a function call for the moment the surrounding function returns:

```go
log("start")
defer log("finish")
```

The finish call runs on every return after the defer statement, including a return caused by an error. This makes defer useful for closing files, unlocking a mutex, rolling back a temporary state, or recording the end of an operation.

Deferred calls run in last-in, first-out order:

```go
defer log("first")
defer log("second")
// output at return: second, first
```

Arguments are evaluated when defer is reached, while the call itself waits until return. Keep deferred functions small and make sure they do not hide an important error.

The example uses a callback instead of a logging package so the order can be tested without global state. In real backend code, the callback can be replaced by structured logging or metrics.

## Reading the example

The lesson combines four ideas:

1. average returns a value and an explicit error for division by zero.
2. statusCategory validates an HTTP status and wraps ErrInvalidStatus with the bad value.
3. inspectStatus rejects a nil dependency, records start, and defers finish.
4. main checks every error before using the result.

The switch without an expression in statusCategory is another way to write mutually exclusive conditions. Each case is checked from top to bottom, and the first matching case returns.

## Reading the tests

The tests verify both values and errors. TestAverageReturnsSentinelError checks errors.Is instead of comparing error strings. TestInspectStatusDefersFinish checks the event order on both success and failure paths, proving that defer is not limited to the happy path.

Run the tests verbosely:

```sh
go test -v ./lessons/04-functions-errors
```

Run all repository checks before committing:

```sh
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
```

## Common mistakes

| Mistake | Why it is wrong | Fix |
| --- | --- | --- |
| Ignoring the error return | The result may be invalid or incomplete | Check err immediately after the call |
| Comparing errors by string | Text is not a stable program contract | Use errors.Is or errors.As |
| Returning a useful value with a non-nil error without a contract | Callers may accidentally use the value | Return the useful zero value on failure unless partial data is explicitly documented |
| Calling a dependency before validating it | A nil function or object can panic | Validate preconditions first |
| Assuming defer runs when the process crashes | defer runs on function return, not on an abrupt process exit | Use it for ordinary function cleanup, not crash recovery |
| Deferring a call inside a huge loop | Cleanup waits until the outer function returns and resources accumulate | Move one iteration into a helper function |

## Practice

1. Add parsePort(text string) (int, error). Accept decimal ports from 1 through 65535 and return a meaningful error otherwise. Use strconv.Atoi and wrap validation errors with context.
2. Add safeAverage(values []int) (float64, error). Return ErrEmptyInput for nil or empty input and avoid integer division.
3. Add classifyError(err error) string. Return stable categories for ErrDivisionByZero, ErrInvalidStatus, and an unknown error using errors.Is.
4. Add withEvents(action func() error, log func(string)) error. Record start and finish with defer, and preserve the action error.
5. Extend tests to prove that deferred finish is recorded when action returns an error.

For every function, define its failure contract first. Test the success path, boundary values, and each documented failure. Do not compare error messages when the contract describes an error category.

## Done when

- You can write and call a function returning a value and an error.
- You can explain when to return nil and when to return a non-nil error.
- You can wrap an error with fmt.Errorf and recognize it with errors.Is.
- You can explain why deferred cleanup runs on both success and ordinary error returns.
- You can predict the order of multiple deferred calls.
- go test ./lessons/04-functions-errors passes.
