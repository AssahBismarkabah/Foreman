.PHONY: build run test lint clean

build:
	go build -o bin/foreman ./cmd/foreman

run: build
	./bin/foreman $(ARGS)

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
