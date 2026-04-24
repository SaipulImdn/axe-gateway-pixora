.PHONY: build run clean test lint docker-build docker-up docker-down

APP_NAME := axe-gateway
MAIN_PATH := ./cmd/gateway

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(APP_NAME) $(MAIN_PATH)

run:
	go run $(MAIN_PATH)

clean:
	rm -f $(APP_NAME)

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...

docker-build:
	docker build -t $(APP_NAME) .

docker-up:
	docker compose up -d

docker-down:
	docker compose down

tidy:
	go mod tidy
