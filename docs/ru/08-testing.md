# Урок 8: table-driven tests, benchmarks, fuzzing и coverage

## Цель

После урока вы должны уметь:

- организовывать cases в таблицу и запускать каждый как subtest;
- выносить повторяющуюся проверку в test helper с `t.Helper`;
- писать benchmark и смотреть allocations;
- добавлять fuzz-тест для инварианта;
- использовать coverage для поиска пропущенных путей, а не как цель качества.

Пример нормализует разделённые запятыми chat tags. Он намеренно небольшой, чтобы внимание осталось на инструментах тестирования backend parsers и validators.

## Перед началом

Запустите пример и тесты из корня репозитория:

```sh
go run ./lessons/08-testing
go test ./lessons/08-testing
```

Ожидаемый вывод:

```text
normalized tags: go, backend, chat
```

## 1. Table-driven tests

Таблица — это срез именованных входов и ожидаемых результатов. Один тест может покрыть обычные, граничные и ошибочные значения:

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

Конкретное имя case делает ошибку понятной: `invalid punctuation` полезнее, чем `case 3`. Держите таблицу рядом с тестом, чтобы контракт было легко проверить на review.

## 2. Subtests изолируют cases

Запускайте каждую строку таблицы через `t.Run`:

```go
for _, tt := range tests {
	t.Run(tt.name, func(t *testing.T) {
		got, err := normalizeTags(tt.input)
		// compare got and err with tt.want and tt.wantErr
	})
}
```

У subtest есть собственное имя, и его можно выбрать через `-run`:

```sh
go test ./lessons/08-testing -run 'TestNormalizeTags/invalid'
```

В этом уроке subtests последовательные. Добавляйте `t.Parallel` только после понимания общего состояния и владения данными.

## 3. Helpers проясняют повторяющиеся assertions

`requireTags` сравнивает срезы и отмечает себя как helper:

```go
func requireTags(t *testing.T, got, want []string) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
}
```

`t.Helper` заставляет test runner показывать место вызова helper при ошибке. Helper должен улучшать читаемость, а не прятать проверяемое поведение.

## 4. Benchmarks измеряют повторяющуюся операцию

Benchmark использует `*testing.B` и повторяет работу `b.N` раз:

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

Запустите его с данными об allocations:

```sh
go test ./lessons/08-testing -run '^$' -bench '^BenchmarkNormalizeTags$' -benchmem
```

Setup должен находиться вне измеряемого цикла. Сравнивайте benchmark с baseline и реалистичным input; один запуск не является универсальным утверждением о производительности.

## 5. Fuzzing проверяет инвариант

Fuzz-тест начинает с seed cases, а затем Go мутирует input:

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

Контракт — это инвариант, а не один точный output для каждой строки: успешный результат содержит корректные уникальные tags, а произвольный input не вызывает panic.

Запустите ограниченную fuzz-сессию:

```sh
go test ./lessons/08-testing -run '^$' -fuzz '^FuzzNormalizeTagsNeverPanics$' -fuzztime=5s
```

Go может создавать corpus-файлы в `testdata/fuzz`. Перед commit проверяйте их как тестовые входы.

## 6. Coverage — это обратная связь

Coverage показывает, какие statements или branches были выполнены тестами:

```sh
go test ./lessons/08-testing -cover
go test ./lessons/08-testing -coverprofile=/tmp/go-step-by-step-lesson-08.cover
go tool cover -func=/tmp/go-step-by-step-lesson-08.cover
```

Coverage не доказывает полезность assertions. Используйте его для поиска путей вроде invalid и empty input, а затем пишите тесты по контракту.

## 7. Прочитайте пример вместе с тестами

`normalizeTags`:

- обрезает пробелы и приводит tags к lowercase;
- игнорирует пустые части между запятыми;
- отклоняет punctuation и пробелы внутри tag;
- сохраняет порядок первого появления;
- удаляет повторы;
- возвращает пустой, но non-nil срез для blank input.

Тесты выражают правила таблицей и subtests, переиспользуют helper, benchmark-ят реалистичный повторяющийся input и проверяют успешный инвариант через fuzzing. Такой стиль переносится на backend parsing, validation, filters и request transformations.

## Частые ошибки

- Проверять только happy path без именованных invalid cases.
- Использовать helper без вызова `t.Helper`.
- Включать setup в benchmark loop.
- Запускать fuzzing без ясного инварианта.
- Запускать неограниченный fuzzing в scheduled task.
- Гнаться за 100% coverage вместо проверки качества assertions.
- Коммитить fuzz corpus без проверки его назначения.

## Практика

Реализуйте это единое задание в `lessons/08-testing/main.go` и добавьте целевые тесты в `main_test.go`:

1. Добавьте `normalizeUserIDs(input string) ([]string, error)`. Разделяйте input по запятым, обрезайте пробелы, сохраняйте порядок первого появления, отклоняйте пустой ID и удаляйте повторы.
2. Используйте table-driven tests с subtests для blank input, одного ID, пробелов, duplicates и invalid empty parts.
3. Вынесите сравнение срезов user IDs в helper и отметьте его через `t.Helper`.
4. Добавьте benchmark с повторяющимся input и fuzz-тест успешного инварианта.
5. Запустите coverage и добавьте хотя бы один тест для каждой ветки invalid input. Поведение tags не изменяйте.

Запустите целевые проверки:

```sh
gofmt -w lessons/08-testing/main.go lessons/08-testing/main_test.go
go test ./lessons/08-testing
go test ./lessons/08-testing -run '^$' -bench . -benchmem
go test ./lessons/08-testing -cover
go vet ./lessons/08-testing
```

## Готово, когда

Вы можете объяснить, почему table-driven subtest проще расширять, чем дублированные тесты, что меняет `t.Helper` в отчёте об ошибке, почему benchmark setup должен быть вне измеряемого цикла, какой инвариант проверяет fuzzing и почему coverage описывает выполнение, а не корректность.
