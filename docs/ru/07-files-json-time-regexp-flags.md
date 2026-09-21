# Урок 7: файлы, JSON, time, регулярные выражения и флаги

## Цель

После урока вы должны уметь:

- читать файл и добавлять контекст к I/O-ошибке;
- декодировать JSON в типизированную Go-структуру;
- разбирать и сравнивать timestamps в формате RFC3339;
- компилировать регулярное выражение и обрабатывать неверный pattern;
- собирать небольшую CLI-программу на стандартном пакете `flag`.

Backend постоянно пересекает эти границы: конфигурация и fixtures — это файлы, API payloads — JSON, timestamps событий — значения времени, а CLI-инструментам нужны предсказуемые флаги. Пример — небольшой отчёт по событиям, который можно развить в инструмент для логов или chat events.

## Перед началом

Запустите пример из корня репозитория:

```sh
go run ./lessons/07-files-json-time-regexp-flags
```

Ожидаемый вывод:

```text
matched events: 3
1 2026-09-21T07:00:00Z message Hello
2 2026-09-21T07:05:00Z system Connected
3 2026-09-21T07:10:00Z message Need help
```

Попробуйте фильтры:

```sh
go run ./lessons/07-files-json-time-regexp-flags -type message -after 2026-09-21T07:05:00Z -pattern help
```

Ожидаемый вывод:

```text
matched events: 1
3 2026-09-21T07:10:00Z message Need help
```

## 1. Читаем файл и декодируем JSON

Fixture — это массив JSON-объектов. Struct tags сопоставляют JSON-ключи с полями Go:

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

Go-модель использует типизированное поле времени:

```go
type Event struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	UserID    string    `json:"user_id"`
	Message   string    `json:"message"`
}
```

`time.Time` умеет декодировать RFC3339 при работе с JSON. Типизированное поле безопаснее, чем произвольная строка: сравнение и форматирование остаются явными.

`loadEvents` использует `os.ReadFile` и `json.Unmarshal`:

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

Формат `%w` сохраняет исходную ошибку для `errors.Is` и `errors.As`. Добавляйте контекст на границе, где возникает проблема: чтение и декодирование — разные ошибки и должны оставаться различимыми в сообщении.

## 2. Проверяем данные после декодирования

Корректный JSON не обязательно означает корректные данные приложения. Пример проверяет, что у каждого события есть положительный ID, type и timestamp:

```go
for _, event := range events {
	if event.ID <= 0 || event.Type == "" || event.Timestamp.IsZero() {
		return nil, fmt.Errorf("%w: id=%d", ErrInvalidEvent, event.ID)
	}
}
```

Держите validation рядом с границей ввода. После этого остальная программа может полагаться на более сильное условие: загруженные события имеют минимально необходимые для отчёта поля.

Пример намеренно не запрещает пустой `message`: системное событие может не содержать человеческого сообщения. Validation должна следовать контракту, а не догадке о том, какие данные «обычно» приходят.

## 3. Разбираем и сравниваем время

Пустой флаг `-after` означает отсутствие нижней границы. Иначе значение должно быть в RFC3339:

```go
func parseAfter(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	return time.Parse(time.RFC3339, value)
}
```

Нулевое `time.Time{}` — удобный sentinel для состояния «не настроено»; проверяйте его через `IsZero`, а не через отформатированную строку. Фильтр оставляет события на границе и позже, а удаляет те, для которых `Timestamp.Before(after)` равно `true`.

Значения времени содержат location и instant. Предпочитайте явные offset, например `Z` или `+04:00`; не интерпретируйте пользовательский ввод молча в timezone машины.

## 4. Компилируем и используем regexp

Компилируйте пользовательский pattern один раз до обхода событий:

```go
func compileMessagePattern(value string) (*regexp.Regexp, error) {
	if value == "" {
		return nil, nil
	}

	return regexp.Compile(value)
}
```

Неверное выражение — это ошибка входных данных, а не причина для panic. `nil` pattern означает «не фильтровать по message». `MatchString` проверяет, встречается ли выражение в сообщении.

Regular expressions мощные, но область их применения должна быть ограничена. Не используйте regexp там, где контракт яснее выражается обычным сравнением или проверкой prefix.

## 5. Разбираем флаги в тестируемой функции

Стандартный пакет `flag` разбирает параметры командной строки. Если оставить parsing и работу в `run`, CLI можно тестировать без запуска отдельного процесса:

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

Значения `flag` — указатели, потому что parsing заполняет их после объявления. `flag.ContinueOnError` позволяет вернуть ошибку из `run` вызывающему коду и сохранить контроль над output writer в тестах.

`main` намеренно остаётся тонким:

```go
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

## 6. Прочитайте пример вместе с тестами

Поток данных такой:

```text
flags -> parse options -> read file -> decode JSON -> validate -> filter -> print
```

Тесты покрывают корректный файл, неверный JSON, неверные данные события, пустой time filter, неверный timestamp, комбинированную фильтрацию по type/time/regexp, CLI output и неверное регулярное выражение. Функция `run` получает writers и arguments, поэтому тестам не нужно заменять глобальное состояние процесса.

Такая форма похожа на backend command или maintenance tool: parsing входа явен, domain data типизированы, а ошибки содержат контекст.

## Частые ошибки

- Декодировать стабильный payload в `map[string]any`, а не в типизированную структуру.
- Забыть JSON tags, когда внешние ключи используют `snake_case`.
- Сравнивать строки timestamps вместо распарсенных `time.Time`.
- Вызывать `regexp.Compile` внутри цикла для каждого события.
- Превращать неверный пользовательский input в panic.
- Молча использовать timezone машины для timestamp без offset.
- Вызывать `flag.Parse` в initializer пакета, усложняя тестирование и повторное использование.
- Оставлять всю бизнес-логику внутри `main`.
- Считать корректный JSON корректными domain-данными без validation.

## Практика

Реализуйте это единое задание в `lessons/07-files-json-time-regexp-flags/main.go` и добавьте целевые тесты в `main_test.go`:

1. Добавьте флаг `-limit`. `0` означает отсутствие лимита; положительное значение оставляет первые `limit` событий после всех существующих фильтров. Отрицательное значение должно возвращать ошибку.
2. Добавьте флаг `-user`, который оставляет только события с совпадающим `UserID`. Пустое значение выключает фильтр.
3. Обновите вывод примера и тесты для нулевого, положительного, отрицательного лимита и лимита больше числа результатов.
4. JSON-файл не изменяйте и сохраните всё существующее поведение.

Запустите целевые проверки:

```sh
gofmt -w lessons/07-files-json-time-regexp-flags/main.go lessons/07-files-json-time-regexp-flags/main_test.go
go test ./lessons/07-files-json-time-regexp-flags
go vet ./lessons/07-files-json-time-regexp-flags
```

## Готово, когда

Вы можете объяснить, где должны находиться file I/O, JSON decoding, validation, parsing времени, compilation regexp и parsing flags; почему для фильтрации лучше `time.Time`, чем строка timestamp; и как тесты проверяют корректные данные и отвергнутый input.
