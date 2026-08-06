# Lesson 1: Data types

[Russian version](ru/01-data-types.md)

## Goal

Learn to choose basic Go types, declare variables, understand zero values, convert values explicitly, and distinguish bytes from Unicode code points.

After this lesson, you will be able to read and change the program in `lessons/01-data-types/main.go` and understand what its tests verify.

## Before you start

From the repository root, run:

```sh
go version
```

It should report Go 1.22 or later. Run every command in this lesson from the repository root, the directory that contains `go.mod`.

## Run the example

```sh
go run ./lessons/01-data-types
go test ./lessons/01-data-types
```

`go run` compiles and runs the program. `go test` runs functions whose names begin with `Test`. A passing test means the actual result matches the expected result.

Expected output from the first command:

```text
name: "Go" (string)
visits: 0 (int), zero value
active: true (bool)
average: 2.3
bytes and runes in "Go язык": 11 and 7
20 C in Fahrenheit: 68.0
```

Do not memorize these lines as magic; the following sections explain every value.

## Basic types

| Type | Use it for |
| --- | --- |
| `bool` | `true` and `false` |
| `string` | immutable UTF-8 text |
| `int` | ordinary integer calculations and indexes |
| `int64` | an integer size required by an API, file size, or database field |
| `float64` | fractional values and scientific calculations |
| `byte` | one byte of binary data or UTF-8 text |
| `rune` | one Unicode code point |

Prefer `int` for ordinary whole numbers. Do not choose a numeric type merely because it "looks safer"; use a specific width when an API, protocol, database, or memory requirement demands it.

`uint` and other unsigned types are not an automatic fit for values that "cannot be negative", such as an age or a count. Subtracting from zero does not fail; it wraps to a large positive number. Use them when an external format or bitwise operation requires them.

```go
age := 27
var price float64 = 19.99
active := true
name := "Mira"
```

`:=` declares and initializes a variable inside a function. `var` is useful when the type must be explicit or a value arrives later.

Go infers `int` from `27` in the first line. The second line explicitly makes `19.99` a `float64`. Write variable names in `mixedCaps`: `userName`, not `user_name`.

## Zero values

Every declared variable already has a value:

```go
var count int       // 0
var ratio float64   // 0
var enabled bool    // false
var title string    // ""
```

This matters in Go: design types so their zero value is useful when possible.

In the runnable example, `var visits int` immediately creates a variable with the value `0`. That is why the program prints `visits: 0` before anything is assigned. A zero value does not mean the variable does not exist, and it is not the same as `nil`; `nil` applies only to some types, covered later.

## Conversions are explicit

Go does not silently mix numeric types:

```go
completed := 7
total := 10
ratio := float64(completed) / float64(total)
```

The conversion is not validation. For example, `int(3.9)` is `3`; choose a rounding rule deliberately when one is needed.

The following does not compile because its operands have different types:

```go
// ratio := completed / float64(total)
```

Another common mistake is integer division, which discards the fractional part. `7 / 3` has type `int` and evaluates to `2`. The `average` function converts both operands to `float64` first, so its result is approximately `2.333...`.

## Reading the example functions

```go
func average(total int, count int) float64 {
	if count == 0 {
		return 0
	}

	return float64(total) / float64(count)
}
```

- `func` begins a function declaration.
- `average` is the function name.
- `total int, count int` are two parameters of type `int`.
- `float64` after the parentheses is the return type.
- `if count == 0` prevents division by zero.
- `return 0` is valid because the untyped constant `0` is representable as `float64`.

`celsiusToFahrenheit` accepts and returns `float64` because temperatures can be fractional. In `celsius*9/5 + 32`, the expression is evaluated left to right; because `celsius` is already `float64`, no fractional part is lost.

## Strings, bytes, and runes

`len` returns bytes, not characters. UTF-8 letters can occupy several bytes.

```go
word := "Go язык"
bytes := len(word)
characters := len([]rune(word))
```

Use `[]byte` for binary data and encoded text. Use `rune` or `[]rune` when the task concerns Unicode code points. Neither automatically equals what a person sees as one visual character; grapheme clusters are a later, specialized subject.

The string `"Go язык"` has three ASCII bytes (`G`, `o`, and a space) and four Cyrillic letters that each use two bytes. Therefore, `len` returns $3 + 4 \cdot 2 = 11$. Converting to `[]rune` produces seven code points: `G`, `o`, a space, `я`, `з`, `ы`, and `к`.

## Formatting output

`fmt.Printf` prints text from a format string. It has two kinds of arguments:

```go
fmt.Printf("format", valuesToInsert...)
```

`%` starts a **format verb**. It is not `$`: `$d` has no special meaning to `Printf`. Each verb usually consumes the next value after the format string, from left to right.

```go
fmt.Printf("name: %q (%T)\n", name, name)
```

The first `name` is inserted into `%q` and prints as `"Go"`; the second `name` is inserted into `%T` and prints as `string`.

The example uses:

| Format | Meaning |
| --- | --- |
| `%q` | a quoted string |
| `%T` | a value's type |
| `%d` | an integer |
| `%t` | a boolean |
| `%f` | a floating-point value; six digits after the decimal by default |
| `%.1f` | a floating-point value with one digit after the decimal point |
| `%s` | a string without quotes |
| `%v` | a value in its default representation |
| `%%` | a percent sign |

In `%.1f`:

- `%` starts the format verb;
- `.` begins the precision setting;
- `1` requests one digit after the decimal point;
- `f` selects floating-point formatting.

Thus, `fmt.Printf("%.1f\n", 7.0/3.0)` prints `2.3`, while `%f` would print `2.333333`.

For example, `fmt.Printf("%T\n", name)` prints `string`. `\n` starts a new line. If a verb does not match its value type, `fmt` does not stop the program but prints a diagnostic such as `%!d(string=Go)`. This means the format and supplied value do not match.

## Reading the tests

`TestAverage` in `main_test.go` holds a table of cases. Each case defines the input (`total`, `count`) and expected value (`want`). The loop runs each as a separate subtest with `t.Run`.

```go
got := average(test.total, test.count)
if got != test.want {
	t.Fatalf("average(...) = %v, want %v", got, test.want)
}
```

It computes `got`, then compares it with `want`. When they differ, `t.Fatalf` stops that subtest and reports both values. Run only these tests verbosely with:

```sh
go test -v ./lessons/01-data-types
```

## Common mistakes

| Mistake | Why it is wrong | Fix |
| --- | --- | --- |
| `7 / 3` for an average | It is integer division and produces `2` | Convert operands to `float64` before division |
| Dividing by `count` without checking zero | Integer division by zero ends the program with a panic | Return an error or documented value; this lesson returns `0` |
| Using `len` to count Cyrillic letters | `len` counts bytes | Use `len([]rune(text))` for code points |
| Expecting `int(3.9)` to round | Conversion discards the fractional part | Choose and apply a rounding rule explicitly later with `math` |

## Practice workflow

1. Add the function to `main.go`.
2. Add its test to `main_test.go`.
3. Run `go test ./lessons/01-data-types`.
4. Only after the test passes, change `main` if you want console output.

## Practice

1. Add `Percentage(completed, total int) float64`. For a zero `total`, return `0`. Write table-driven tests.
2. Add `IsASCII(text string) bool` by inspecting bytes. Test ASCII and Cyrillic input.
3. Write `KilometersToMiles(km float64) float64`; document the conversion constant and test a value with a tolerance.
4. Explain in a comment why `len("Привет")` differs from `len([]rune("Привет"))`.
5. Extend `main` to print the type of every value using `%T`.

For `Percentage`, test at least three cases: a whole percentage, a fractional percentage, and a zero `total`. For `IsASCII`, expect `true` for `"Go 1.22"` and `false` for `"Привет"`. Do not look for a solution before attempting it: tests should describe the required behavior.

## Done when

- You can explain the type and zero value of every variable in the example.
- `go test ./lessons/01-data-types` passes.
- You can explain why conversions are explicit and when to use `rune` instead of `byte`.