.PHONY: build test test-integration test-cli-integration test-race vet-integration generate clean python-test python-test-integration python-lint web-install web-build web-dev test-e2e

# `go build -o` writes exactly the name it is given, and Windows shells will not
# execute an extensionless file. web/playwright.config.ts picks the same two names
# from process.platform - the two must agree.
ifeq ($(OS),Windows_NT)
RELAY_SERVER_BIN := bin/relay-server.exe
else
RELAY_SERVER_BIN := bin/relay-server
endif

# Install web dependencies
web-install:
	cd web && npm ci

# Build the web UI into web/dist (embedded by relay-server)
web-build:
	cd web && npm run build

# Run the Vite dev server (proxies /v1 to :8080)
web-dev:
	cd web && npm run dev

# Run the browser end-to-end suite (Playwright) against the PRODUCTION-EMBEDDED
# SPA - not the Vite dev server. Tailwind v4 scans source statically, so a
# computed class string emits no CSS and a no-op fix is indistinguishable from a
# working one to every other test in this repo; only a production bundle can tell
# them apart. web/embed.go's SPA fallback is server code with its own semantics,
# and embedded serving is same-origin where the dev server is :5173 proxying
# :8080.
#
# Requires: node, go, and a Postgres reachable at
# postgres://relay:relay@127.0.0.1:5432 - the relay-postgres container
# scripts/dev.ps1 already manages. Install the browsers once with:
#   cd web && npx playwright install chromium webkit
#
# THE BUILD ORDER IS LOAD-BEARING. web/embed.go snapshots web/dist at COMPILE
# time (//go:embed all:dist), so relay-server must be rebuilt AFTER web-build or
# it serves the previous bundle - or, from a clean checkout, the 7-line "has not
# been built" placeholder, which makes every spec fail with no #root and no
# obvious cause.
#
# web/dist/index.html is a TRACKED placeholder while everything else under
# web/dist/ is gitignored (.gitignore:6-8), so a build replaces exactly one
# tracked file with an index referencing hashed assets nobody else has.
# Restoring it here, pass or fail, makes that a step instead of a rule people
# have to remember. Uses sh syntax, like `clean` above - run make from Git Bash
# on Windows, not from cmd.
test-e2e: web-build
	go build -o $(RELAY_SERVER_BIN) ./cmd/relay-server
	cd web && npm run test:e2e; rc=$$?; cd .. && git checkout -- web/dist/index.html; exit $$rc

# Build all binaries into bin/ (web UI is embedded into relay-server)
build: web-build
	go build -o bin/relay-server ./cmd/relay-server
	go build -o bin/relay-agent  ./cmd/relay-agent
	go build -o bin/relay        ./cmd/relay

# Run all tests (no Docker required)
test:
	go test ./... -timeout 120s

# Run integration tests (requires Docker); -p 1 prevents parallel container conflicts on Windows.
# The timeout is deliberately generous: every integration test spins up its own
# real Postgres container, so internal/api alone runs ~320-340s.
test-integration:
	go test -tags integration -p 1 ./... -timeout 900s

# Run the CLI real-server integration lane. Every test in it drives a live
# internal/api server over HTTP against a real Postgres, so a response-shape
# drift in a handler reddens here instead of staying invisible to
# internal/cli's httptest fixtures.
#
# Two modes, selected by RELAY_TEST_DATABASE_URL:
#   unset - one Postgres testcontainer per test (needs Docker), like every
#           other integration package in this repo.
#   set   - one freshly CREATEd database per test on the supplied server, e.g.
#           postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable (the
#           relay-postgres container scripts/dev.ps1 already manages). This is
#           what .github/workflows/go-ci.yml's cli-integration job uses, and the
#           command there is this same target so the two cannot drift.
#
# -p 1 is NOT needed: the pattern names one package. The 480s Go timeout is
# deliberately distinct from the 10-minute job timeout in CI so a Go panic and a
# GitHub job kill name themselves instead of looking identical.
test-cli-integration:
	go test -tags integration ./internal/cli/... -timeout 480s

# Type-check (compile) the integration-tagged code without running it. Catches
# shared-signature breaks in //go:build integration files that the unit `test`
# target never compiles. Needs no Postgres/p4d containers.
vet-integration:
	go vet -tags integration ./...

# Run tests under the race detector (unit tests only - no Docker). Catches
# concurrency regressions across the agent send goroutine and Runner, the
# worker/grace registries, the scheduler, and the perforce registry race guard.
# internal/agent is included: its former Windows-only proctree race
# (internal/agent/proctree_windows.go, docs/backlog/closed/bug-2026-06-20-agent-proctree-windows-race.md)
# is fixed - the proctree is now assigned synchronously after cmd.Start instead
# of from a goroutine that polled cmd.Process.
# NOTE (Windows): -race needs cgo with a working gcc. The default Strawberry Perl
# gcc fails (exit status 0xc0000139); use MSYS2 mingw64 via
# CC=/c/msys64/mingw64/bin/gcc.exe (with its bin on PATH). Linux/CI is unaffected.
test-race:
	go test -race ./... -timeout 180s

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
