# Multi-stage build shared by every binary in this repository.
#
# Select which one to build with the package path:
#   --build-arg CMD_PATH=.            the API server (module root)
#   --build-arg CMD_PATH=./collector  the Binance ingestion worker
#   --build-arg CMD_PATH=./backtest   the backtest CLI

FROM golang:1.23-alpine AS builder

WORKDIR /src

# Dependencies first so a code-only change does not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG CMD_PATH=.
# CGO_ENABLED=0 produces a static binary, which is what the distroless static
# base image can run. -trimpath keeps build paths out of the binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/app \
        ${CMD_PATH}

# ---------------------------------------------------------------------------
# migrate — applies the goose migrations, then exits
# ---------------------------------------------------------------------------
#
# goose is compiled here from source rather than pulled as a separate image,
# and it is deliberately kept out of go.mod: its dialect drivers drag in
# ClickHouse, MSSQL, YDB and Docker — 86 modules for a tool the application
# never imports. Building it in our own builder gets the pinned version with
# none of that weight in the app.

FROM golang:1.23-alpine AS goose-builder

ARG GOOSE_VERSION=v3.24.3
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION}

FROM gcr.io/distroless/static-debian12:nonroot AS migrate

COPY --from=goose-builder /go/bin/goose /goose
# The migration SQL has to travel in the image; goose reads it at run time.
COPY --from=builder /src/migrations /migrations

USER nonroot:nonroot

# Configuration comes through GOOSE_DRIVER, GOOSE_DBSTRING and
# GOOSE_MIGRATION_DIR. The distroless image has no shell, so a command line
# containing "$DATABASE_URL" would be passed through literally; environment
# variables are the only way to parameterise this without adding one.
ENTRYPOINT ["/goose"]
CMD ["up"]

# ---------------------------------------------------------------------------
# app — the API, collector and backtest binaries
# ---------------------------------------------------------------------------
#
# Last, so a plain `docker build` with no --target still produces an
# application image rather than the migration tool.

FROM gcr.io/distroless/static-debian12:nonroot AS app

# Certificates and passwd come with the distroless base; nothing else is needed
# by a static Go binary.
COPY --from=builder /out/app /app

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/app"]
