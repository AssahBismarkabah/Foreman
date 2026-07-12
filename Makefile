# Foreman Makefile
#
# Start development database:
#   make up
#   make wait-db    (waits for PostgreSQL health check)
#   make test       (run all tests)
#
# Include NATS (optional):
#   make up-nats
#
# Clean up:
#   make down       (stop containers)
#   make reset      (stop + delete volumes)

# Load .env if present (silently ignore if missing)
ifneq (,$(wildcard .env))
    include .env
    export
endif

.PHONY: build run test lint ci clean
.PHONY: up up-nats down reset logs ps wait-db

VERSION ?= dev
BUILD_FLAGS = -ldflags="-w -s -X main.version=$(VERSION)" -trimpath

# --- Build ---

build:
	go build $(BUILD_FLAGS) -o bin/foreman ./cmd/foreman

# Build for Linux (useful when building from macOS for Docker)
build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(BUILD_FLAGS) -o bin/foreman-linux-amd64 ./cmd/foreman

# --- Docker ---

# Build the Docker image locally
docker:
	docker build \
		-t foreman:$(VERSION) \
		-t foreman:latest \
		--build-arg VERSION=$(VERSION) \
		.

# --- Release (local) ---

# Build binaries for all platforms
release: clean
	@echo "Building foreman $(VERSION)..."
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(BUILD_FLAGS) -o bin/foreman-linux-amd64 ./cmd/foreman
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build $(BUILD_FLAGS) -o bin/foreman-darwin-amd64 ./cmd/foreman
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(BUILD_FLAGS) -o bin/foreman-darwin-arm64 ./cmd/foreman
	@echo "Done:"
	@ls -lh bin/

run: build
	./bin/foreman $(ARGS)

# --- Test ---

# Run all tests (unit + integration). Integration tests require compose services.
test:
	go test -count=1 -p 1 ./...

# Run unit tests only (skips integration/Docker/PostgreSQL tests)
test-unit:
	go test -count=1 -short ./...

# --- Lint ---

lint:
	golangci-lint run ./...

# --- CI (lint + build + test) ---

ci: lint build test

# --- Clean ---

clean:
	rm -rf bin/

# --- Docker Compose ---

up:
	docker compose up -d

up-nats:
	docker compose --profile nats up -d

down:
	docker compose down

reset: down
	docker compose down -v

logs:
	docker compose logs -f

ps:
	docker compose ps

wait-db:
	@echo "Waiting for PostgreSQL to be ready..."
	@for i in $$(seq 1 20); do \
		if docker compose exec -T postgres pg_isready -U ${FOREMAN_PG_USER:-foreman} > /dev/null 2>&1; then \
			echo "PostgreSQL is ready."; \
			exit 0; \
		fi; \
		printf "  waiting... (%d/20)\r" $$i; \
		sleep 1; \
	done; \
	echo "\nPostgreSQL did not become ready in time."; \
	exit 1
