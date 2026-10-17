VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE   ?= companies-service:$(VERSION)
GOLANGCI_VERSION := $(shell cat .golangci-version)

.PHONY: help
help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/companies ./cmd/companies

.PHONY: run
run: ## Run on the host against `make infra`
	go run ./cmd/companies serve --config configs/config.local.yaml

.PHONY: test
test: ## Run unit tests
	go test -race ./...

.PHONY: test-integration
test-integration: ## Run integration tests (needs Docker)
	go test -race -tags integration -count=1 ./test/integration/...

.PHONY: cover
cover: ## Unit and integration coverage report
	go test -tags integration -count=1 -coverpkg=./internal/... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: lint-install
lint-install: ## Install the pinned golangci-lint
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

.PHONY: docker-build
docker-build: ## Build the production image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

.PHONY: up
up: ## Start the full stack (Postgres, Kafka, migrations, app)
	docker compose up -d --build

.PHONY: infra
infra: ## Start only Postgres and Kafka
	docker compose up -d postgres kafka kafka-init

.PHONY: down
down: ## Stop the stack and remove volumes
	docker compose down -v

.PHONY: logs
logs: ## Follow app logs
	docker compose logs -f app

.PHONY: token
token: ## Print a dev JWT for the local config
	@if command -v go >/dev/null; then go run ./tools/devtoken; \
	else docker build -q --target devtoken -t companies-devtoken . >/dev/null && docker run --rm companies-devtoken; fi

.PHONY: events
events: ## Tail the companies.events topic
	docker compose exec kafka /opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server kafka:9092 --topic companies.events --from-beginning --property print.key=true
