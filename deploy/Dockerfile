# syntax=docker/dockerfile:1.6

FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/pingoworker ./cmd/ping_worker.go && \
    CGO_ENABLED=0 go build -o /out/telegram-bot ./cmd/bot.go

# worker image
FROM gcr.io/distroless/base AS worker
WORKDIR /app
COPY --from=builder /out/pingoworker /app/pingoworker
ENV GIN_MODE=release
ENTRYPOINT ["/app/pingoworker"]

# bot image
FROM gcr.io/distroless/base AS bot
WORKDIR /app
COPY --from=builder /out/telegram-bot /app/telegram-bot
ENTRYPOINT ["/app/telegram-bot"]