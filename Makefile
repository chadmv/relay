.PHONY: build test test-integration generate clean python-test python-test-integration python-lint web-install web-build web-dev

# Install web dependencies
web-install:
	cd web && npm ci

# Build the web UI into web/dist (embedded by relay-server)
web-build:
	cd web && npm run build

# Run the Vite dev server (proxies /v1 to :8080)
web-dev:
	cd web && npm run dev

# Build all binaries into bin/ (web UI is embedded into relay-server)
build: web-build
	go build -o bin/relay-server ./cmd/relay-server
	go build -o bin/relay-agent  ./cmd/relay-agent
	go build -o bin/relay        ./cmd/relay

# Run all tests (no Docker required)
test:
	go test ./... -timeout 120s

# Run integration tests (requires Docker); -p 1 prevents parallel container conflicts on Windows
test-integration:
	go test -tags integration -p 1 ./... -timeout 300s

# Regenerate sqlc store layer and protobuf code
generate:
	sqlc generate
	buf generate

# Remove compiled binaries
clean:
	rm -rf bin/

# ─── Python SDK ──────────────────────────────────────────────────────────────
# Targets assume a venv at python/.venv. Bootstrap with:
#   cd python && python -m venv .venv && .venv/Scripts/python -m pip install -e ".[dev]"

PYTHON_VENV := python/.venv/Scripts/python.exe
ifeq ($(OS),Windows_NT)
PYTHON_VENV := python/.venv/Scripts/python.exe
else
PYTHON_VENV := python/.venv/bin/python
endif

# Run Python SDK unit tests (no relay-server required)
python-test:
	$(PYTHON_VENV) -m pytest python/tests/unit -v

# Run Python SDK integration tests against a running relay-server.
# Requires RELAY_URL and RELAY_TOKEN to be set, and at least one online agent.
python-test-integration:
	RELAY_INTEGRATION=1 $(PYTHON_VENV) -m pytest python/tests/integration -v

# Run linters and type checks on the Python SDK
python-lint:
	$(PYTHON_VENV) -m ruff check python/src python/tests
	$(PYTHON_VENV) -m mypy python/src
