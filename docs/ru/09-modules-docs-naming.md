# Урок 9: modules, dependencies, документация и имена

## Цель

После урока вы должны уметь:

- читать module path и версию Go из go.mod;
- понимать, почему dependencies входят в module metadata;
- документировать exported identifiers в стиле go doc;
- выбирать idiomatic names для exported и unexported Go-кода;
- сохранять маленький API пакета детерминированным и тестируемым.

Пример считает типы событий. Он использует только standard library, но те же правила modules и packages применяются, когда backend получает внешнюю dependency.

## Перед началом

Запустите пример и посмотрите документацию пакета:

```sh
go run ./lessons/09-modules-docs-naming
go test ./lessons/09-modules-docs-naming
go doc ./lessons/09-modules-docs-naming
```

Ожидаемый вывод:

```text
events=3 types=message:2,system:1
```

## 1. Module — единица управления dependencies

В корне repository находится go.mod:

```text
module github.com/ziqq/go_step_by_step

go 1.22
```

Module path — это import prefix для packages repository. Директива go задаёт базовый language/toolchain. Когда добавляется external dependency, её module path и version записываются в go.mod, а checksums обычно попадают в go.sum.

Полезные read-only команды:

```sh
go list -m
go list -m all
go mod graph
```

Не запускайте go get только для того, чтобы пример выглядел «современнее». Добавляйте dependency, только когда standard library или существующий код не решают задачу разумно. После осознанного изменения dependency запускайте go mod tidy и проверяйте оба module-файла.

## 2. Используйте standard library, когда её достаточно

Пример импортирует fmt, sort и strings. Для подсчёта, нормализации и сортировки third-party package не нужен:

```go
import (
	"fmt"
	"sort"
	"strings"
)
```

Dependency имеет стоимость поддержки и security-риск. Правило не в том, чтобы «никогда не использовать dependencies»: dependency должна решать реальную проблему, быть зафиксирована в module metadata и быть понятной на review.

## 3. Документируйте exported identifiers

Exported names начинаются с uppercase. Их comments начинаются с имени identifier:

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

Package comment в doc.go описывает package:

```go
// Package main demonstrates module metadata, package documentation, and naming.
package main
```

Запустите go doc, чтобы увидеть public surface. Documentation — часть API contract: описывайте поведение, empty input, ordering, errors и важные assumptions владения.

Unexported helpers не обязаны иметь public API comments, но их names и code всё равно должны быть понятными. Не экспортируйте функцию только для того, чтобы тест получил к ней доступ.

## 4. Выбирайте idiomatic names

Предпочитайте короткие и точные names:

```go
type Event struct {
	Type    string
	Message string
}

func normalizeType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
```

Основные правила:

- используйте ID, HTTP, JSON и URL для initialisms;
- в local names пишите userID, а не userId;
- используйте FormatSummary, а не GetFormattedSummary;
- для маленького type подойдёт короткий receiver вроде e;
- не повторяйте имя package без необходимости;
- экспортируйте только нужные names, детали оставляйте private.

Name должен объяснять смысл значения. Короткое имя idiomatic только при небольшом scope и очевидном значении.

## 5. Сохраняйте output детерминированным

Порядок итерации map не является contract. CountByType возвращает map для lookup по type. FormatSummary сортирует keys перед построением text:

```go
types := make([]string, 0, len(counts))
for eventType := range counts {
	types = append(types, eventType)
}
sort.Strings(types)
```

Детерминированный output делает CLI behavior, tests, logs и diffs стабильными. Никогда не полагайтесь на случайный порядок обхода map.

## 6. Прочитайте пример вместе с тестами

У package небольшой public surface: Event, CountByType и FormatSummary. normalizeType — implementation detail. Тесты покрывают normalization, ignored empty types, contract non-nil empty map, sorted output и empty input.

Новой third-party dependency нет. Package comment и comments exported names можно проверить через go doc, а tests показывают поведение, которое обещает documentation.

## Частые ошибки

- Редактировать go.mod вручную и не проверять dependency graph.
- Добавлять dependency, когда достаточно standard library.
- Экспортировать helpers только ради tests.
- Писать comments, которые не начинаются с exported identifier.
- Использовать Id, Http, Json или Url вместо Go initialisms.
- Превращать map data в user-visible text без определения ordering.
- Делать output зависимым от порядка итерации map.
- Использовать vague names вроде data, item или doThing вне маленького scope.
- Считать documentation украшением, а не API contract.

## Практика

Реализуйте это единое задание в lessons/09-modules-docs-naming/main.go и добавьте целевые tests в main_test.go:

1. Добавьте FilterByType(events []Event, eventType string) []Event. Нормализуйте запрошенный type, сохраняйте исходный order событий и возвращайте пустой non-nil срез при отсутствии совпадений.
2. Добавьте Go doc comment для exported function, описав empty input и ordering.
3. Добавьте tests для совпавшего type, другого casing и spaces, отсутствия совпадений и empty input.
4. Проверьте package через go doc и убедитесь, что public names и comments понятны. Не добавляйте dependency и не меняйте go.mod.
5. Сохраните детерминированность FormatSummary и зелёными все существующие tests.

Запустите целевые проверки:

```sh
gofmt -w lessons/09-modules-docs-naming/*.go
go test ./lessons/09-modules-docs-naming
go vet ./lessons/09-modules-docs-naming
go doc ./lessons/09-modules-docs-naming
```

## Готово, когда

Вы можете объяснить, чем владеет go.mod, когда оправдана dependency, почему comments exported names начинаются с имени identifier, почему Go использует initialisms ID и HTTP и почему map-backed output нужно сортировать перед превращением в user-visible text.
