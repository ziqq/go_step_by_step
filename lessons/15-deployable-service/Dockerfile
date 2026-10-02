# syntax=docker/dockerfile:1
ARG GO_IMAGE=golang:1.27.1-alpine

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY lessons/15-deployable-service ./lessons/15-deployable-service
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service ./lessons/15-deployable-service

FROM scratch AS runtime
COPY --from=build /out/service /service
ENV APP_ENV=production PORT=8080
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD ["/service", "healthcheck"]
ENTRYPOINT ["/service"]
