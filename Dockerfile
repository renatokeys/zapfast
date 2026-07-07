# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILDDATE=unknown

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-trimpath

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build \
        -ldflags="-s -w \
            -X 'main.version=${VERSION}' \
            -X 'main.commit=${COMMIT}' \
            -X 'main.buildDate=${BUILDDATE}'" \
        -o /out/zapfast ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILDDATE=unknown

LABEL org.opencontainers.image.title="zapfast" \
      org.opencontainers.image.description="WhatsApp HTTP API forked from wuzapi (MIT)" \
      org.opencontainers.image.source="https://github.com/renatokeys/zapfast" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILDDATE}"

COPY --from=builder /out/zapfast /app/zapfast
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/app/zapfast"]
CMD ["--address=0.0.0.0", "--port=8080", "--logtype=json"]
