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

FROM gcr.io/distroless/static-debian12:nonroot

# Certificates and passwd come with the distroless base; nothing else is needed
# by a static Go binary.
COPY --from=builder /out/app /app

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/app"]
