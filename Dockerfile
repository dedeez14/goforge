# syntax=docker/dockerfile:1.7
#
# Multi-stage build producing a tiny, static binary in a distroless image.
# - Final image is ~10MB and runs as a non-root user.
# - Build-cache friendly: deps are downloaded before source is copied.

FROM golang:1.25-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

FROM deps AS build
ARG VERSION=dev
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/api /app/api
COPY migrations /app/migrations
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/api"]
