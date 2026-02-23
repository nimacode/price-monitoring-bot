.PHONY: build run test clean docker-build docker-up docker-down fmt lint

BINARY_NAME=bot
GO=/usr/local/go/bin/go
GOFLAGS=-v

build:
	$(GO) build $(GOFLAGS) -o bin/$(BINARY_NAME) ./cmd/bot/main.go

run:
	$(GO) run ./cmd/bot/main.go

test:
	$(GO) test -v -race -coverprofile=coverage.out ./...

test-coverage: test
	$(GO) tool cover -html=coverage.out -o coverage.html

clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

fmt:
	$(GO) fmt ./...

lint:
	golangci-lint run ./...

docker-build:
	docker-compose build

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f bot

docker-restart:
	docker-compose restart bot

mongo-shell:
	docker-compose exec mongo mongosh -u $(MONGO_USER) -p $(MONGO_PASSWORD) price_monitor

install:
	$(GO) mod download
	$(GO) mod tidy

update-deps:
	$(GO) get -u ./...
	$(GO) mod tidy

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build          Build the binary"
	@echo "  run            Run the application"
	@echo "  test           Run tests"
	@echo "  test-coverage  Run tests and generate coverage report"
	@echo "  clean          Clean build artifacts"
	@echo "  fmt            Format code"
	@echo "  lint           Run linter"
	@echo "  docker-build   Build Docker image"
	@echo "  docker-up      Start containers"
	@echo "  docker-down    Stop containers"
	@echo "  docker-logs    View bot logs"
	@echo "  docker-restart Restart bot container"
	@echo "  mongo-shell    Open MongoDB shell"
	@echo "  install        Download dependencies"
	@echo "  update-deps    Update all dependencies"
