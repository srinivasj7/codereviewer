# syntax=docker/dockerfile:1.7

# Multi-stage build. ARG CMD picks which binary under cmd/<CMD> to build.
# All binaries are built with CGO_ENABLED=1 so the tree-sitter cgo path
# in the indexer is available regardless of the host that builds the image.

ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS builder
ARG CMD
RUN apk add --no-cache git build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=1 GOOS=linux go build -ldflags="-w -s" -o /app/binary ./cmd/${CMD}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
COPY --from=builder /app/binary /app/binary
USER app
ENTRYPOINT ["/app/binary"]
