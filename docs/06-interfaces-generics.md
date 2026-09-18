# Lesson 6: Interfaces, type assertions, generics, and package boundaries

## Goal

After this lesson you should be able to:

- define a small interface at the place where it is consumed;
- understand that Go interfaces are satisfied implicitly;
- use a type assertion with the comma-ok form;
- write a small generic function with a type constraint;
- recognize when a new package creates a real boundary instead of just adding folders.

A backend often works with several concrete types through one narrow behavior: messages, system events, storage records, or HTTP responses can all expose a small operation. Interfaces make that boundary explicit without forcing inheritance.

## Before you start

Run the example and its tests from the repository root:

```sh
go run ./lessons/06-interfaces-generics
go test ./lessons/06-interfaces-generics
```

Expected output:

```text
message #1 from Anton: Hello
event: message.created
message author: Anton
first state: sent
```

## 1. Interfaces describe behavior

An interface contains method signatures, not data. `Renderable` says only that a value can render itself:

```go
type Renderable interface {
	Render() string
}
```

`Message` and `SystemEvent` satisfy the interface because they both have a `Render() string` method. No explicit declaration is needed:

```go
type Message struct {
	ID      int
	Author  string
	Content string
}

func (m Message) Render() string {
	return fmt.Sprintf("message #%d from %s: %s", m.ID, m.Author, m.Content)
}

type SystemEvent struct {
	Kind string
}

func (e SystemEvent) Render() string {
	return fmt.Sprintf("event: %s", e.Kind)
}
```

The compile-time assertions in the example make this relationship visible and fail early if a method is renamed or its signature changes:

```go
var _ Renderable = Message{}
var _ Renderable = SystemEvent{}
```

## 2. Use the smallest interface at the consumer boundary

A slice of `Renderable` values can contain different concrete types:

```go
func renderAll(items []Renderable) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Render())
	}

	return result
}

items := []Renderable{
	Message{ID: 1, Author: "Anton", Content: "Hello"},
	SystemEvent{Kind: "message.created"},
}
```

The caller does not need to know how each value renders. This is useful in backend code when a handler depends on one capability but should not depend on a concrete storage or transport implementation.

Keep interfaces small. An interface with one or two methods is often easier to implement, fake, and test than a large interface that tries to describe an entire subsystem. Define the interface where the consumer needs it when that keeps ownership clear.

## 3. Type assertions recover a concrete value

An interface value contains a dynamic concrete value and its type. A type assertion asks whether the dynamic value has a specific type. The comma-ok form is safe:

```go
func renderAuthor(item Renderable) (string, bool) {
	message, ok := item.(Message)
	if !ok {
		return "", false
	}

	return message.Author, true
}
```

Calling `renderAuthor` with a `SystemEvent` returns `false` instead of panicking. A direct assertion without `, ok` panics when the type is different:

```go
message := item.(Message)
```

Use a type switch when several concrete types require different behavior:

```go
switch value := item.(type) {
case Message:
	fmt.Println(value.Author)
case SystemEvent:
	fmt.Println(value.Kind)
default:
	fmt.Println("unknown renderable")
}
```

A value receiver means both `Message` and `*Message` implement `Renderable`, but their dynamic types are still different. An assertion for `Message` does not match a `*Message`. Assert the type you actually expect, or avoid inspecting the concrete type when the interface method is enough.

## 4. Generics make reusable algorithms type-safe

The `first` function works for any comparable type. The constraint `comparable` allows `==` and `!=`:

```go
func first[T comparable](values []T, want T) (T, bool) {
	for _, value := range values {
		if value == want {
			return value, true
		}
	}

	var zero T
	return zero, false
}
```

The compiler infers `T` from the arguments:

```go
state, ok := first([]string{"queued", "sent", "failed"}, "sent")
id, found := first([]int{10, 20, 30}, 20)
```

Generics are useful when the algorithm is the same and only the element type changes. They are not a reason to replace every interface: an interface describes behavior shared by different types, while a type parameter preserves the concrete element type across a reusable algorithm.

The zero value of `T` is obtained with `var zero T`. Returning it together with `false` avoids inventing a special value that might be valid input.

## 5. Package boundaries are ownership boundaries

Start with one package while the example is small. Add a package when it owns a coherent responsibility and has a useful API for another package. A package boundary should answer three questions:

- Which types and functions are public?
- Which details must remain private?
- Who owns the interface contract?

A possible backend shape might look like this:

```text
cmd/api/main.go
internal/render/message.go
internal/render/event.go
internal/httpapi/handler.go
```

Do not create `pkg`, `repository`, `service`, or `factory` directories only because they sound architectural. A package is justified by a stable responsibility, not by a preferred folder tree. Keep implementation details unexported until another package genuinely needs them.

## 6. Read the example with its tests

The example uses one interface for heterogeneous rendering, one safe type assertion for the exceptional case where the author is needed, and one generic algorithm for searching comparable values. The tests cover normal rendering, order preservation, empty input, a failed type assertion, strings, integers, and a missing value.

The interface is intentionally small. If a future HTTP handler only needs `Render`, it can depend on `Renderable` rather than on `Message`, `SystemEvent`, or a large service interface.

## Common mistakes

- Adding an explicit `implements` keyword: Go does not have one.
- Defining a large interface before a consumer needs it.
- Using a direct type assertion when the dynamic type may be different.
- Confusing an interface value's static type with its dynamic concrete type.
- Using generics where a small interface would express behavior more clearly.
- Returning a magic zero or sentinel value without a separate success flag.
- Creating packages only to move files, without a responsibility or ownership boundary.
- Exporting every type and function from a new package by default.

## Practice

Implement the following in `lessons/06-interfaces-generics/main.go` and add focused tests in `main_test.go`.

1. Add `FileAttachment` with a `Name` and `Size` field. Make it satisfy `Renderable` with output such as `file: report.pdf (2048 bytes)`, then add it to `main` and test it.
2. Add a generic `count[T comparable](values []T, want T) int` function. Test an empty slice, a missing value, and repeated values for both strings and integers.
3. Add `renderKind(item Renderable) string` using a type switch. Return `message` for `Message`, `system_event` for `SystemEvent`, and `unknown` for another type that implements `Renderable`.
4. Keep the interfaces small and do not add a package only for this exercise. In a short comment, state which future backend consumer would own `Renderable`.
5. Keep all existing tests green and add tests for every new behavior, including the negative and empty cases.

Run the focused checks:

```sh
gofmt -w lessons/06-interfaces-generics/main.go lessons/06-interfaces-generics/main_test.go
go test ./lessons/06-interfaces-generics
go vet ./lessons/06-interfaces-generics
```

## Done when

You can explain why `Message` satisfies `Renderable` without an explicit declaration, why `renderAuthor` returns `false` for a `SystemEvent`, why `first` needs `comparable`, and why a new package should own a responsibility rather than merely hold files.
