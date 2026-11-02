# syntax=docker/dockerfile:1
# check=error=true
# notes-api – Endstand aus „Dockerfiles zum Anfassen", Go 1.27, ohne Artefakt-Stage
ARG GO_VERSION=1.27

# in Produktion: @sha256:… pinnen
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS base
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

FROM base AS test
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && go test ./...

FROM base AS build
ARG TARGETOS TARGETARCH VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/notes-api .

FROM scratch AS final
ARG VERSION=dev REVISION=unknown CREATED=unknown
LABEL org.opencontainers.image.title="notes-api" \
      org.opencontainers.image.description="Notiz-API aus dem Trainingslager" \
      org.opencontainers.image.source="https://example.com/compose-lab/notes" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"
# Ohne --chmod: Mit --chmod bekämen auch die dabei neu angelegten Ordner /etc,
# /etc/ssl und /etc/ssl/certs Modus 444. Ohne x darf User 65532 sie nicht betreten
# und findet weder die CA-Zertifikate noch /etc/resolv.conf (keine DNS-Namen).
COPY --link --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --link --from=build --chmod=555 /out/notes-api /notes-api
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=10s --timeout=2s --start-period=5s --start-interval=1s \
  CMD ["/notes-api", "healthcheck"]
STOPSIGNAL SIGTERM
ENTRYPOINT ["/notes-api"]
