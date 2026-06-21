.PHONY: build test test-integration test-race vet-integration generate clean python-test python-test-integration python-lint web-install web-build web-dev

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

# Type-check (compile) the integration-tagged code without running it. Catches
# shared-signature breaks in //go:build integration files that the unit `test`
# target never compiles. Needs no Postgres/p4d containers.
vet-integration:
	go vet -tags integration ./...

# Run tests under the race detector (unit tests only - no Docker). Catches
# concurrency regressions across the worker/grace registries, the scheduler, and
# the perforce registry race guard.
# SCOPE: excludes relay/internal/agent because its Windows-only proctree code
# (internal/agent/proctree_windows.go) has a pre-existing data race surfaced when
# this target was first run under -race (docs/backlog/bug-2026-06-20-agent-proctree-windows-race.md). The
# race is //go:build windows so Linux never compiles it (proctree_unix.go is the
# clean Linux build), and the integration tester proved `go test -race ./...` is
# fully green on Linux including internal/agent. CI runs on Linux and therefore
# invokes the full `go test -race ./...` set directly (see
# .github/workflows/go-ci.yml) rather than this target, for complete coverage of
# the agent send goroutine and Runner. This target stays descoped only so the
# same command is green for local Windows devs until the proctree race is fixed;
# re-include relay/internal/agent here once it is.
# NOTE (Windows): -race needs cgo with a working gcc. The default Strawberry Perl
# gcc fails (exit status 0xc0000139); use MSYS2 mingw64 via
# CC=/c/msys64/mingw64/bin/gcc.exe (with its bin on PATH). Linux/CI is unaffected.
test-race:
	go test -race -timeout 180s $(shell go list ./... | grep -v '^relay/internal/agent$$')

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
