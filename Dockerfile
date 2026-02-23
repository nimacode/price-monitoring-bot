FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /bot ./cmd/bot/main.go

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -g '' appuser

WORKDIR /app

COPY --from=builder /bot /app/bot
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

RUN mkdir -p /app/logs && chown -R appuser:appuser /app

USER appuser

ENV TZ=Asia/Tehran

CMD ["./bot"]
