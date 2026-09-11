# Lesson 3: Arrays, slices, maps, and range

[Russian version](ru/03-collections.md)

## Goal

Learn how Go stores groups of values, iterate with range, grow slices with append, and count or look up values with maps.

Collections are everywhere in a backend: a page of messages is a slice, a set of supported methods can be represented by a map, and a response often contains a list of records.

## Before you start

Run these commands from the repository root:

```sh
go run ./lessons/03-collections
go test ./lessons/03-collections
```

Expected output:

```text
status count: 5
status sum: 1829
unique methods: [messages.send messages.load]
status frequencies: map[200:1 201:1 429:2 500:1]
```

The order of map entries is not guaranteed. The example output shows one possible order.

## Arrays

An array has a fixed length that is part of its type:

```go
var limits [3]int
values := [3]int{10, 20, 30}
```

The types [3]int and [4]int are different types. Arrays are values: assigning an array copies all of its elements. Use arrays when the size is genuinely fixed and part of the domain.

Most application code uses slices instead. A slice is a small descriptor pointing to an underlying array, with a length and a capacity.

## Slices

Create a slice with a literal:

```go
statuses := []int{200, 404, 500}
```

The length is the number of elements. The capacity is how many elements can fit in the current backing array before Go allocates another one:

```go
len(statuses)
cap(statuses)
```

Use an index to read or update an element:

```go
statuses[0] = 201
first := statuses[0]
```

Indexes start at zero. Reading outside the range causes a panic, so validate indexes when they come from an external request.

A nil slice has length and capacity zero and can be ranged over safely:

```go
var values []int
fmt.Println(len(values)) // 0
```

append returns the resulting slice. Always assign the result because append may allocate a new backing array:

```go
values = append(values, 10)
values = append(values, 20, 30)
```

The distinction between nil and empty slices can matter when encoding JSON. A nil slice commonly encodes as null, while a non-nil empty slice commonly encodes as []. Choose the representation required by the API contract.

## range

The range form iterates over a collection:

```go
for index, status := range statuses {
    fmt.Println(index, status)
}
```

For a slice or array, range provides the index and a copy of the element. Use the blank identifier when the index is not needed:

```go
for _, status := range statuses {
    if status >= 500 {
        // handle a server error
    }
}
```

The value is a copy. Changing status inside the loop does not update the slice. To update elements, use the index:

```go
for index := range statuses {
    statuses[index]++
}
```

The same syntax can iterate over a map, but map iteration order is deliberately unspecified:

```go
for method, count := range methodCounts {
    fmt.Println(method, count)
}
```

Never make API output or tests depend on the order of map iteration. Sort keys when deterministic output is required; sorting will be covered later with the standard library.

## Maps

A map stores key-value pairs:

```go
counts := make(map[int]int)
counts[200]++
counts[500] = 2
```

The zero value of a map is nil. Reading a missing key from a nil map is safe and returns the value type's zero value, but assigning to a nil map panics. Initialize a map with make or a literal before writing to it.

Use the comma-ok form to distinguish a missing key from a stored zero value:

```go
count, exists := counts[404]
if !exists {
    count = 0
}
```

The delete function removes a key. Deleting a missing key is safe:

```go
delete(counts, 404)
```

The map in countStatuses uses a useful zero-value property. When the first status is seen, counts[status] is initially zero, so incrementing it creates the value one.

## Sets with map

Go has no built-in set type. A map with empty struct values is a common representation:

```go
seen := make(map[string]struct{})
seen["messages.send"] = struct{}{}
```

The empty struct occupies no storage for its value. The map in uniqueMethods remembers which command methods have already been added while the slice preserves their first-seen order.

Do not use a map as a set when you also need stable display order. Keep a slice for order and a map for fast membership checks, as the example does.

## Reading the example

The example models a small batch of backend observations:

1. statuses is a slice because its length changes with each batch.
2. sum uses range to process every status.
3. uniqueMethods combines a map for membership with a slice for stable order.
4. countStatuses uses a map to aggregate occurrences by status code.

This is the same basic shape used when a backend processes a page of records, groups events by a key, or removes duplicate command methods.

## Reading the tests

The tests cover nil and empty slices, negative values, duplicate entries, first-occurrence order, and map counts. reflect.DeepEqual is used only to compare collection values in tests; it does not change how the production functions work.

Run the tests verbosely:

```sh
go test -v ./lessons/03-collections
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
| Treating an array and a slice as the same | Their types, copying behavior, and length rules differ | Use an array for fixed size and a slice for a variable collection |
| Ignoring the result of append | append may return a slice backed by a new array | Assign the result back to the slice |
| Writing to a nil map | A nil map can be read but cannot receive assignments | Initialize it with make or a literal |
| Assuming map order | Map iteration order is unspecified | Sort keys or preserve order in a separate slice |
| Modifying the range value | The value variable is a copy of the element | Use the index to update the original slice |
| Returning internal storage accidentally | Callers can mutate a slice that was meant to be private | Copy data at an ownership boundary when necessary |

## Practice

1. Add contains(values []string, wanted string) bool. Return true when wanted appears in the slice. Test nil, empty, first, middle, and missing values.
2. Add filterStatuses(statuses []int, minimum int) []int. Return a new slice containing statuses greater than or equal to minimum. Do not mutate the input slice.
3. Add mergeCounts(left, right map[string]int) map[string]int. Sum values with the same key and leave both input maps unchanged.
4. Add average(values []int) float64. Return 0 for nil or empty input and avoid integer division.
5. Add sortedStatusCodes(counts map[int]int) []int as a preview of deterministic map output. You may use sort.Ints from the standard library.

For every new function, write tests before changing main. Include nil or empty collections, duplicate values, and boundary cases where the contract allows them.

## Done when

- You can explain the difference between an array, a slice, and a map.
- You know why append must be assigned back to the slice.
- You can safely write to a map and distinguish a missing key with comma-ok.
- You can explain why map iteration order must not be used as API or test order.
- You can update slice elements through a range index.
- go test ./lessons/03-collections passes.
