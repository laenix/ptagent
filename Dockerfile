# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/ptagent-server ./cmd/server
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/ptagent-dispatcher ./cmd/dispatcher

# Frontend build
FROM node:22-alpine AS web-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web/ .
RUN npm run build

# Server image (with frontend)
FROM alpine:3.20 AS server

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /bin/ptagent-server /app/
COPY --from=web-builder /app/web/dist /app/web/dist
COPY configs/ /app/configs/
COPY prompts/ /app/prompts/

EXPOSE 8000

CMD ["/app/ptagent-server", "--addr", ":8000", "--web", "/app/web/dist"]

# Dispatcher image (lightweight, no frontend)
FROM alpine:3.20 AS dispatcher

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /bin/ptagent-dispatcher /app/
COPY configs/ /app/configs/
COPY prompts/ /app/prompts/

CMD ["/app/ptagent-dispatcher", "--config", "/app/configs/dispatch.yaml"]

