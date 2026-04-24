# Stage 1: Build
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o axe-gateway ./cmd/gateway

# Stage 2: Runtime
FROM alpine:3.20

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/axe-gateway .

EXPOSE 9090

ENTRYPOINT ["./axe-gateway"]
