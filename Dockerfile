# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25.4

FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dota-inhouse ./cmd/server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/dota-inhouse /app/dota-inhouse
COPY web /app/web

RUN mkdir -p /data

ENV PORT=8080 \
    DATABASE_PATH=/data/inhouse.db \
    LOG_PATH=/data/inhouse.log

EXPOSE 8080

CMD ["/app/dota-inhouse"]
