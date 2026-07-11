.PHONY: build run test lint ci clean

build:
	go build -o bin/foreman ./cmd/foreman

run: build
	./bin/foreman $(ARGS)

test:
	go test ./...

lint:
	golangci-lint run ./...

ci: lint test
	go build ./...

clean:
	rm -rf bin/