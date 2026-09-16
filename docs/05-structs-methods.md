# Lesson 5: Structs, methods, pointers, and composition

## Goal

After this lesson you should be able to:

- group related data in a `struct`;
- choose a value receiver or a pointer receiver for a method;
- use the zero value and a pointer safely;
- compose types by embedding one struct into another;
- test a small domain model.

This is the first step toward backend models such as a chat message, an event, or a user. A struct describes data, while methods keep behavior close to the data it changes or reads.

## Before you start

Run the example and its tests from the repository root:

```sh
go run ./lessons/05-structs-methods
go test ./lessons/05-structs-methods
```

Expected output:

```text
chat: support, messages: 2
last: #2 Bot: Welcome
event: #42 message.created
```

## 1. Structs group related values

A struct is a named collection of fields. The keyed literal is usually the clearest form because each value is next to its field name:

```go
type Message struct {
	ID      int
	Author  string
	Content string
}

message := Message{
	ID:      1,
	Author:  "Anton",
	Content: "Hello",
}
```

Fields that are not specified get their zero values. Therefore `Message{}` is valid and creates an empty message with `ID == 0` and empty strings.

Prefer keyed literals for your own structs. An unkeyed literal depends on field order and becomes fragile when a field is added:

```go
message := Message{1, "Anton", "Hello"}
```

## 2. Methods describe behavior

A method is a function with a receiver. `Summary` has a value receiver, so it reads a copy of the `Message` value and does not change the original:

```go
func (m Message) Summary() string {
	return fmt.Sprintf("%s: %s", m.Author, m.Content)
}
```

Call it with the familiar dot syntax:

```go
summary := message.Summary()
```

Use a value receiver when the method only reads the value, the value is small, and copying it is appropriate. A value receiver also works for both an addressable value and a pointer to that value.

## 3. Pointer receivers mutate the original value

`AddMessage` changes the `Messages` slice inside `Chat`, so it uses a pointer receiver:

```go
func (c *Chat) AddMessage(message Message) {
	c.Messages = append(c.Messages, message)
}

chat := Chat{Name: "support"}
chat.AddMessage(message)
```

The compiler takes the address of the addressable `chat` value for this call. You can also write the pointer explicitly:

```go
chat := &Chat{Name: "support"}
chat.AddMessage(message)
```

The important difference is the receiver:

```go
func (c Chat) ResetName() {
	c.Name = ""
}
```

This changes only a copy and is not useful for resetting the caller's chat. A mutating method needs `*Chat`:

```go
func (c *Chat) ResetName() {
	c.Name = ""
}
```

## 4. Pointers and nil

A pointer stores the address of a value. `&value` takes an address and `*pointer` follows it:

```go
message := Message{ID: 1}
pointer := &message
pointer.ID = 2

fmt.Println(message.ID) // 2
```

The zero value of a pointer is `nil`. Calling a method through a nil pointer can panic if the method dereferences the receiver. Do not create a nil receiver accidentally; validate a pointer before using it when nil is a possible input.

The zero value of a struct is often useful and requires no constructor. This is one of Go's important design habits: make the ordinary zero value safe and meaningful when possible.

## 5. A method can return a value and a success flag

An empty chat has no last message. `LastMessage` returns a zero `Message` and `false` in that case:

```go
func (c Chat) LastMessage() (Message, bool) {
	if len(c.Messages) == 0 {
		return Message{}, false
	}

	return c.Messages[len(c.Messages)-1], true
}
```

The caller must check the flag before using the result:

```go
last, ok := chat.LastMessage()
if !ok {
	return
}

fmt.Println(last.Summary())
```

Returning `(value, bool)` is useful when absence is normal. Later, when we work with failures that need an explanation, we will use `(value, error)` instead.

## 6. Composition and embedding

Go does not use class inheritance. You can compose a larger type from smaller types. Embedding promotes the embedded fields and methods:

```go
type Identified struct {
	ID int
}

type Event struct {
	Identified
	Kind string
}

event := Event{
	Identified: Identified{ID: 42},
	Kind:       "message.created",
}

fmt.Println(event.ID)
```

`event.ID` is shorthand for `event.Identified.ID`. The explicit form is also valid and can be clearer when several embedded types expose similar names. Embedding is composition, not inheritance: `Event` contains an `Identified` value and can expose its fields for convenience.

## 7. Read the example with its tests

The example has three responsibilities:

- `Message` owns message data and formats one summary;
- `Chat` owns a list and provides read and mutation methods;
- `Event` composes a reusable identifier with an event kind.

The tests check behavior rather than implementation details. In particular, `TestChatAddMessageMutatesReceiver` would fail if `AddMessage` accidentally used a value receiver, and the table-driven `LastMessage` test covers both an empty and a populated chat.

## Common mistakes

- Using an unkeyed struct literal for a type you own, then breaking callers when fields change.
- Choosing a value receiver for a method that must mutate the caller's value.
- Forgetting that a pointer can be `nil`.
- Treating embedding as inheritance and expecting subclass behavior.
- Reading the returned `Message` without checking the `bool` from `LastMessage`.
- Adding a constructor automatically when the struct's zero value already works.

## Practice

Implement each task in `lessons/05-structs-methods/main.go` and add focused tests in `main_test.go`.

1. Add `FindMessage(id int) (Message, bool)` to `Chat`. Return the first matching message and `false` when there is no match.
2. Add `RemoveLastMessage() (Message, bool)` to `Chat`. It must remove and return the last message, or return `Message{}` and `false` for an empty chat.
3. Add an `Unread bool` field to `Message` and a pointer-receiver method `MarkRead()` that sets it to `false`.
4. Add `Label() string` to `Event`, returning a string such as `#42 message.created`.
5. Make the example print the label through `event.Label()` and keep all tests green.

Run the focused checks:

```sh
gofmt -w lessons/05-structs-methods/main.go lessons/05-structs-methods/main_test.go
go test ./lessons/05-structs-methods
go vet ./lessons/05-structs-methods
```

## Done when

You are ready for the next lesson when you can explain why `AddMessage` uses `*Chat`, why `Summary` uses `Message`, what `Message{}` contains, and how `event.ID` reaches the embedded `Identified.ID`. Your tests should cover the empty and non-empty cases for every lookup or removal method.
