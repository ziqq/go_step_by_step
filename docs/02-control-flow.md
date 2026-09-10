# Lesson 2: Operators and control flow

[Russian version](ru/02-control-flow.md)

## Goal

Learn to combine values with operators, make decisions with if, repeat work with for, return early, and understand the scope of variables.

The examples use HTTP status codes because backend code constantly decides whether a response succeeded, should be retried, or should stop the operation.

## Before you start

Run these commands from the repository root:

    go run ./lessons/02-control-flow
    go test ./lessons/02-control-flow

Expected output:

    status 503: retry
    retry delay for attempt 3: 4s
    sum of first 5 numbers: 15

## Operators

Arithmetic operators calculate values:

    total := 7 + 3
    difference := 7 - 3
    product := 7 * 3
    quotient := 7 / 3
    remainder := 7 % 3

When both operands are integers, division performs integer division and 7 / 3 is 2. The remainder operator returns 1 for 7 % 3.

Comparison operators return a bool:

    status >= 200
    status < 300
    status == 429
    status != 404

Logical operators combine boolean expressions:

| Operator | Meaning |
| --- | --- |
| logical AND (&&) | both expressions must be true |
| logical OR (||) | at least one expression must be true |
| ! | invert a boolean value |

In isSuccessStatus, both boundaries are required. A response with status 200 is successful, but 300 is not:

    return status >= 200 && status < 300

Go evaluates && and || from left to right and stops as soon as the result is known. This is called short-circuit evaluation. Keep the order in mind when the second expression depends on the first one.

## Conditions and early returns

An if executes its body only when the condition is true:

    if attempt <= 0 {
        return 0
    }

The example returns early for invalid attempts. This keeps the main calculation free of a nested else block.

The optional initializer can declare a value whose scope lasts until the end of that if statement:

    if success := isSuccessStatus(status); success {
        return "success"
    }

The variable success exists only inside this if. It cannot be used after the closing brace. Small scopes make it harder to accidentally reuse a temporary value in an unrelated branch.

Use else if when branches are mutually exclusive:

    if status >= 500 {
        return "server error"
    } else if status >= 400 {
        return "client error"
    } else {
        return "other"
    }

Do not add else after an unconditional return unless it makes a short example easier to read. In production code, guard clauses are usually clearer.

## Loops with for

Go has one loop keyword: for.

The three-part form looks similar to a traditional for loop:

    for i := 1; i < attempt; i++ {
        delay *= 2
    }

It has an initializer (i := 1), a condition (i < attempt), and a post statement (i++). The variable i is scoped to the loop.

The loop in retryDelaySeconds doubles the delay once for every retry after the first attempt:

| Attempt | Delay |
| ---: | ---: |
| 1 | 1 second |
| 2 | 2 seconds |
| 3 | 4 seconds |
| 4 | 8 seconds |

Go also supports a condition-only loop:

    for condition {
        // repeated work
    }

Use break to stop a loop and continue to skip the rest of the current iteration. We will use these with arrays, slices, and maps in the next lesson.

## Scope and shadowing

Scope is the part of the program where a name can be used. A function parameter is available inside its function. A variable declared inside an if or for is available only in that block.

    func example(value int) int {
        if value > 0 {
            result := value * 2
            return result
        }

        // result does not exist here.
        return 0
    }

Be careful with :=: it declares a new variable when at least one name on the left is new in the current scope. Accidentally creating a new variable instead of updating an outer one is called shadowing. The compiler catches some unused variables, but not every shadowing mistake.

Prefer short scopes and names that describe their local purpose. This will matter later when handlers validate requests and return errors.

## Reading the example

requestDecision separates three outcomes:

1. A 2xx response is successful.
2. 408, 429, and 5xx responses may be retried.
3. Other responses should stop the operation.

retryDelaySeconds uses a loop instead of a hard-coded table. This is a small example of turning a repeated rule into code. Real services also need a maximum delay, jitter, and a cancellation-aware wait; those topics belong to the backend and reliability sections later in the course.

## Reading the tests

The tests use table-driven cases. Each case gives an input and an expected result. Boundary values such as 200, 299, 500, and 599 are important because range conditions often fail at their edges.

Run the tests verbosely:

    go test -v ./lessons/02-control-flow

Run all repository checks before committing:

    go fmt ./...
    go test ./...
    go test -race ./...
    go vet ./...

## Common mistakes

| Mistake | Why it is wrong | Fix |
| --- | --- | --- |
| Writing status >= 200 OR status < 300 | Almost every integer satisfies one side of the expression | Use logical AND for an inclusive range with two boundaries |
| Forgetting integer division | 7 / 3 produces 2, not a fraction | Convert an operand to float64 when fractional output is required |
| Starting a loop at the wrong value | It can skip the first attempt or add one delay too many | Write expected values for attempts 1, 2, and 3 first |
| Using a variable outside its block | A name declared in an if or for has limited scope | Move the declaration outward only when the value is needed afterward |
| Retrying every error | Permanent errors such as 404 or 403 should not be retried blindly | Define an explicit retry policy |

## Practice

1. Add isValidPort(port int) bool. Return true only for ports from 1 through 65535. Test both boundaries and invalid values.
2. Add clamp(value, minimum, maximum int) int. Return the nearest boundary when value is outside the range. Document the assumption that minimum <= maximum.
3. Add isEven(value int) bool using the remainder operator. Include a negative even number in the tests.
4. Add countDown(start int) []int that returns values from start down to 0. Do not solve it by writing each value manually; use a loop. The slice return type is a preview of the next lesson.
5. In a comment, explain the scope of success in requestDecision and why it cannot be used after the first if block.

For every new function, write the tests before changing main. Include normal values, boundaries, and at least one invalid or empty case where the contract allows it.

## Done when

- You can explain the difference between && and || for a range check.
- You can write a for loop with an initializer, condition, and post statement.
- You can predict which variables are visible inside and outside an if or for block.
- You can explain why 408, 429, and 5xx may be retryable while 403 and 404 are not automatically retryable.
- go test ./lessons/02-control-flow passes.
