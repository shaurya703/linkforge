.PHONY: help up down logs build run test test-unit test-integration fmt vet tidy loadtest seed

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

up: ## Start the full stack (app + postgres + redis), build if needed
	docker compose up --build

down: ## Stop the stack and remove volumes
	docker compose down -v

logs: ## Tail app logs
	docker compose logs -f app

build: ## Build the binary locally
	go build -o bin/linkforge ./cmd/server

run: ## Run the binary locally (expects local postgres + redis)
	go run ./cmd/server

test: test-unit ## Alias for unit tests

test-unit: ## Run fast unit tests (no external deps)
	go test -race -count=1 ./internal/shortener/... ./internal/ratelimit/...

test-integration: ## Run integration tests (requires a running Docker daemon)
	go test -race -count=1 -tags=integration ./internal/integration/...

fmt: ## Format code
	go fmt ./...

vet: ## Static checks
	go vet ./...

tidy: ## Tidy modules
	go mod tidy

seed: ## Insert a few sample links against a running server (BASE_URL overridable)
	./scripts/seed.sh

loadtest: ## Run the k6 load test against a running server
	k6 run loadtest/redirect.js
