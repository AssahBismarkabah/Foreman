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

# --- Build ---

build:
	go build -o bin/foreman ./cmd/foreman

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
