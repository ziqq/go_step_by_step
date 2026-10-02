# Урок 15: Docker, runtime-конфигурация, health checks и CI

## Цель

Упаковать небольшой Go HTTP-сервис в non-root контейнер, передавать настройки при запуске, различать liveness и readiness и выполнять повторяемые проверки в GitHub Actions.

Пример связывает конфигурацию окружения и жизненный цикл HTTP из урока 12 с развертыванием. Состояние `/readyz` намеренно простое; реальный API должен становиться готовым только после успешной проверки обязательных зависимостей, например ограниченного по времени `PingContext` для PostgreSQL. Целевой chat backend использует multi-stage сборку и запускает приложение от непривилегированного пользователя.

Dockerfile отделяет компиляцию от runtime и копирует в `scratch` только статический бинарный файл. Встроенный Docker health check запускает режим `healthcheck` того же бинарного файла, поэтому образу не нужны shell или `curl`. Docker сообщает health-статус контейнера, но сам по себе не перезапускает unhealthy-контейнер. См. [документацию Docker о multi-stage builds](https://docs.docker.com/build/building/multi-stage/), [справочник Dockerfile для `USER` и `HEALTHCHECK`](https://docs.docker.com/reference/dockerfile/) и [рекомендации GitHub по сборке и тестированию Go](https://docs.github.com/en/actions/tutorials/build-and-test-code/go).

## Собери и запусти контейнер

Собирай из корня репозитория: Dockerfile копирует файлы модуля и код этого урока. `.dockerignore` исключает метаданные Git и локальные environment-файлы из build context.

```sh
docker build --pull -f lessons/15-deployable-service/Dockerfile -t go-course-service:lesson15 .
```

Запусти образ, переопределив production-настройки для локальной разработки:

```sh
docker run --rm --publish 8080:8080 --env APP_ENV=development --env PORT=8080 go-course-service:lesson15
```

В другом терминале проверь сервис и готовность:

```sh
curl -i http://localhost:8080/readyz
```

```text
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{"status":"ready"}
```

Пока контейнер работает, Docker health-статус можно посмотреть так:

```sh
docker inspect --format '{{.State.Health.Status}}' $(docker ps -q --filter ancestor=go-course-service:lesson15)
```

Останови контейнер через Ctrl+C. Go-процесс обрабатывает SIGTERM, снимает readiness и дает активным HTTP-обработчикам до десяти секунд на завершение.

## Слои образа и runtime-пользователь

`lessons/15-deployable-service/Dockerfile` состоит из двух этапов:

1. `build` использует Go toolchain для компиляции статического Linux-бинарного файла с `CGO_ENABLED=0`.
2. `runtime` начинается с пустого образа `scratch`, копирует только бинарный файл, задает production defaults и запускается с числовыми UID/GID `65532`, а не от root.

Контекст сборки — корень репозитория. `.dockerignore` исключает `.git`, локальные `.env`-файлы, тестовые бинарники и временные каталоги сборки. Никогда не копируй credentials в образ и не передавай их через build arguments. Передавай runtime-секреты через механизм секретов платформы развертывания. Поддерживай версию Go builder-образа в соответствии с toolchain проекта; для большей воспроизводимости цепочки поставки закрепляй base images по digest.

По умолчанию контейнер слушает порт 8080. `EXPOSE` документирует порт, но не публикует его; host mapping создаёт `docker run --publish`. Runtime-переменные окружения заменяют значения `ENV` из Dockerfile.

## Runtime-конфигурация и health-контракты

| Переменная | Значение по умолчанию | Допустимые значения |
|---|---|---|
| `APP_ENV` | локально `development`, в образе `production` | `development`, `test`, `staging`, `production` |
| `PORT` | `8080` | целое число от 1 до 65535 |

Сервис проверяет настройки один раз при запуске и логирует выбранные окружение и порт. Не создавай отдельные сборки исходников в dev и production только ради изменения конфигурации.

- `GET /healthz` — liveness probe: статус 200 означает, что HTTP-процесс отвечает.
- `GET /readyz` — readiness probe: статус 200 означает, что экземпляру можно направлять трафик; 503 — что пока не следует.

Пример помечает себя готовым после создания listener и снимает readiness перед остановкой. Внешних зависимостей у него нет. В сервисе с базой readiness должна включать ограниченную по времени проверку зависимости с context, а liveness не должна падать только из-за временной недоступности PostgreSQL. При остановке сначала сними readiness, затем заверши активные обработчики.

Docker `HEALTHCHECK` запускает `/service healthcheck`, который за две секунды запрашивает `127.0.0.1:$PORT/readyz`. Это полезно для локальной проверки и некоторых платформ, но Kubernetes и другие оркестраторы могут задавать собственные probes. Статус unhealthy — сигнал для оркестратора; не считай, что Docker автоматически перезапустит контейнер.

## Непрерывная интеграция

`.github/workflows/go.yml` запускается при push и pull request в `main`. Workflow имеет только право `contents: read`, выбирает версию Go из `go.mod` и проверяет форматирование, тесты, race detector, `go vet` и компиляцию. Он не публикует образы и не требует credentials реестра.

Эквивалентные локальные команды:

```sh
gofmt -w ./lessons/15-deployable-service
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Целевые тесты используют `httptest` и не требуют Docker. Успешный Go-тест не доказывает, что образ собирается, поэтому отдельно выполни Docker build, если доступен Docker daemon.

## Типичные ошибки

- включить компилятор, исходники или пакетный менеджер в runtime-образ;
- запускать сервис от root или считать, что `EXPOSE` публикует host-порт;
- встраивать `.env` или секреты в слой образа;
- использовать liveness как проверку зависимости и перезапускать здоровый процесс при сбое базы;
- возвращать ready до доступности обязательных зависимостей;
- забыть снять readiness перед graceful shutdown;
- считать, что Docker автоматически перезапустит контейнер со статусом unhealthy;
- проверять в CI только `go build`, не запуская тесты, race detector или static analysis;
- считать Go-тесты доказательством успешной сборки Docker-образа.

## Практика: добавь container smoke job в CI

Расширь `.github/workflows/go.yml`: после успешной Go-проверки добавь job, который собирает и запускает этот образ.

Требования:

1. Собирай образ из корня репозитория существующим Dockerfile; не отправляй его в registry и не добавляй credentials.
2. Запускай временный контейнер с нестандартным host-портом и явным runtime override для `APP_ENV`/`PORT`.
3. Ожидай `/healthz` и `/readyz` ограниченное время. Заверши job ошибкой, если любой endpoint не вернул 200; при ошибке старта выведи логи контейнера.
4. Гарантируй остановку контейнера даже при провале smoke-проверки. Не ослабляй и не пропускай существующие Go-проверки.
5. Учитывай, что runtime-образ не содержит root-пользователя и shell; используй host-инструменты для HTTP-запросов, не предполагая, что внутри контейнера есть `curl`.

## Критерии завершения

Ты можешь объяснить build и runtime этапы, отличие runtime-конфигурации от build arguments, назначение liveness и readiness и реальные гарантии CI workflow. Целевые тесты проходят, Docker-образ собирается и становится healthy, а новый smoke job проверяет оба endpoint без публикации образа.
