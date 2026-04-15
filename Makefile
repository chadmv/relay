.PHONY: build test test-integration generate clean

# Build all binaries into bin/
build:
	go build -o bin/relay-server ./cmd/relay-server
	go build -o bin/relay-agent  ./cmd/relay-agent
	go build -o bin/relay        ./cmd/relay

# Run all tests (no Docker required)
test:
	go test ./... -timeout 120s

# Run integration tests (requires Docker)
test-integration:
	go test -tags integration ./... -timeout 300s

# Regenerate sqlc store layer and protobuf code
generate:
	sqlc generate
	buf generate

# Remove compiled binaries
clean:
	rm -rf bin/
