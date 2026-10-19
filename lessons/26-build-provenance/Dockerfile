ARG GO_IMAGE=golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY lessons/26-build-provenance ./lessons/26-build-provenance
ARG APP_VERSION=dev
ARG SOURCE_REVISION=unknown
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags="-X main.version=${APP_VERSION} -X main.revision=${SOURCE_REVISION}" \
    -o /out/build-info ./lessons/26-build-provenance \
    && /out/build-info > /dev/null

FROM scratch AS runtime
COPY --from=build /out/build-info /build-info
USER 65532:65532
ENTRYPOINT ["/build-info"]
