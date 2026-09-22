# Lesson 8: Table tests, benchmarks, fuzzing, and coverage

## Goal

After this lesson you should be able to:

- organize cases as a table and run each case as a subtest;
- extract a readable test helper with `t.Helper`;
- write a benchmark and inspect allocations;
- add a fuzz test for an invariant;
- use coverage to find missing paths without treating it as a quality target.

The example normalizes comma-separated chat tags. It is deliberately small so the focus stays on the testing tools used by backend parsers and validators.

## Before you start

Run the example and tests from the repository root:

```sh
go run ./lessons/08-testing
go test ./lessons/08-testing
```

Expected output:

```text
normalized tags: go, backend, chat
```

## 1. Table-driven tests

A table is a slice of named inputs and expected outcomes. One test can cover normal, boundary, and invalid values:

```go
tests := []struct {
	name    string
	input   string
	want    []string
	wantErr error
}{
	{name: "empty input", input: "   ", want: []string{}},
	{name: "deduplicate", input: "Go, go", want: []string{"go"}},
	{name: "invalid punctuation", input: "go!", wantErr: ErrInvalidTag},
}
```

Specific case names make failures useful: `invalid punctuation` explains more than `case 3`. Keep the table close to the test so the contract is easy to review.

## 2. Subtests isolate each case

Run each table row with `t.Run`:

```go
for _, tt := range tests {
	t.Run(tt.name, func(t *testing.T) {
		got, err := normalizeTags(tt.input)
		// compare got and err with tt.want and tt.wantErr
	})
}
```

A subtest has its own name and can be selected with `-run`:

```sh
go test ./lessons/08-testing -run 'TestNormalizeTags/invalid'
```

This lesson keeps subtests sequential. Add `t.Parallel` only after understanding shared state and data ownership.

## 3. Helpers clarify repeated assertions

`requireTags` compares slices and marks itself as a helper:

```go
func requireTags(t *testing.T, got, want []string) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
}
```

`t.Helper` makes the test runner point at the caller when the helper fails. A helper should improve readability, not hide the behavior being asserted.

## 4. Benchmarks measure a repeated operation

A benchmark uses `*testing.B` and repeats work `b.N` times:

```go
func BenchmarkNormalizeTags(b *testing.B) {
	input := strings.Repeat("backend,go,chat,backend,api,", 100)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = normalizeTags(input)
	}
}
```

Run it with allocation data:

```sh
go test ./lessons/08-testing -run '^$' -bench '^BenchmarkNormalizeTags$' -benchmem
```

Keep setup outside the timed loop. Compare a benchmark with a baseline and a representative input; one run is not a universal performance claim.

## 5. Fuzzing checks an invariant

A fuzz test starts with seed cases and lets Go mutate the input:

```go
func FuzzNormalizeTagsNeverPanics(f *testing.F) {
	f.Add("Go,backend,chat")
	f.Add("   ")

	f.Fuzz(func(t *testing.T, input string) {
		tags, err := normalizeTags(input)
		if err != nil {
			return
		}
		// every successful result must satisfy the tag invariant
	})
}
```

The contract is an invariant, not one exact output for every string: successful results contain valid unique tags, and arbitrary input does not panic.

Run a bounded fuzz session:

```sh
go test ./lessons/08-testing -run '^$' -fuzz '^FuzzNormalizeTagsNeverPanics$' -fuzztime=5s
```

Go may create corpus files under `testdata/fuzz`. Review them as test inputs before committing.

## 6. Coverage is feedback

Coverage shows which statements or branches tests executed:

```sh
go test ./lessons/08-testing -cover
go test ./lessons/08-testing -coverprofile=/tmp/go-step-by-step-lesson-08.cover
go tool cover -func=/tmp/go-step-by-step-lesson-08.cover
```

Coverage does not prove that assertions are useful. Use it to find paths such as invalid and empty input, then write tests for the contract.

## 7. Read the example with its tests

`normalizeTags`:

- trims and lowercases tags;
- ignores empty comma-separated parts;
- rejects punctuation and whitespace inside a tag;
- preserves first-seen order;
- removes duplicates;
- returns an empty, non-nil slice for blank input.

The tests express these rules with a table and subtests, reuse a helper, benchmark realistic repeated input, and check the successful-result invariant with fuzzing. This style transfers directly to backend parsing, validation, filtering, and request transformations.

## Common mistakes

- Testing only the happy path without named invalid cases.
- Using a helper without calling `t.Helper`.
- Including setup inside a benchmark loop.
- Fuzzing without a clear invariant.
- Running unbounded fuzzing in a scheduled task.
- Chasing 100% coverage instead of checking assertion quality.
- Committing fuzz corpus files without reviewing their purpose.

## Practice

Implement this single exercise in `lessons/08-testing/main.go` and add focused tests in `main_test.go`:

1. Add `normalizeUserIDs(input string) ([]string, error)`. Split on commas, trim spaces, preserve first-seen order, reject an empty ID, and remove duplicates.
2. Use table-driven tests with subtests for blank input, one ID, spaces, duplicates, and invalid empty parts.
3. Extract a helper that compares user ID slices and marks itself with `t.Helper`.
4. Add a benchmark with repeated input and a fuzz test for the successful-result invariant.
5. Run coverage and add at least one test for every invalid-input branch. Do not change tag behavior.

Run the focused checks:

```sh
gofmt -w lessons/08-testing/main.go lessons/08-testing/main_test.go
go test ./lessons/08-testing
go test ./lessons/08-testing -run '^$' -bench . -benchmem
go test ./lessons/08-testing -cover
go vet ./lessons/08-testing
```

## Done when

You can explain why a table-driven subtest is easier to extend than duplicated tests, what `t.Helper` changes in a failure report, why benchmark setup belongs outside the timed loop, which invariant fuzzing checks, and why coverage describes execution rather than correctness.
