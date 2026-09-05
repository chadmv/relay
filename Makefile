.PHONY: build test test-integration test-cli-integration test-pg-integration test-api-integration test-race vet-integration generate clean python-test python-test-integration python-lint web-install web-build web-dev test-e2e

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
# real Postgres container, so internal/api alone runs around 750s (measured, a
# clean completed run under -timeout 1800s - somewhat above CLAUDE.md's older
# ~9.5 minute figure, itself derived from a killed 600s run's variance band
# rather than a completion). An earlier version of this comment said
# ~320-340s; that was stale for the package's size at the time it was written.
#
# Deliberately NOT -count=1: Go's test cache DOES key on env vars a test
# actually reads via os.Getenv during that run (pgdsn.NewEmptyDSN reads
# RELAY_TEST_DATABASE_URL), so a real mode change here is not a
# cache-invalidation gap. Measured directly: with a shared GOCACHE, changing
# RELAY_TEST_DATABASE_URL between two runs of TestIntegration_HarnessDSNIsMigratedAndEmpty
# reliably busts the cache, and the unchanged-env rerun in between reports
# "(cached)" as expected. A prior version of this comment claimed the cache
# does not key on env vars at all; that was wrong (see test-cli-integration's
# own comment below for what the cache key actually covers - it does not
# include whether a live TCP connection succeeded, which is a different
# claim). This target is also not what CI runs for ./internal/cli/... -
# .github/workflows/go-ci.yml never calls test-integration - so the
# CI-Postgres-config scenario the old comment invoked cannot apply here
# either. Adding -count=1 back would only cost real time: repeated local runs
# would stop hitting the cache for every untouched package under ./... at the
# integration tag.
#
# Residual axis, not addressed by the reasoning above: test-cli-integration's
# own justification for -count=1 ("the cache key says nothing about whether a
# live TCP connection succeeded") applies verbatim to THIS target too when run
# in shared-service mode (RELAY_TEST_DATABASE_URL set) - pgdsn.NewEmptyDSN
# reads that env var, so a value CHANGE busts the cache as this comment
# measured, but a change to the SHARED SERVER ITSELF (its Postgres version,
# its health check, its container config) with RELAY_TEST_DATABASE_URL left
# unchanged would not. Left as is: this target's normal (unset) mode is a
# fresh testcontainer per test, which has no equivalent gap.
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
#
# -count=1 disables Go's test result cache. Without it, CI's actions/setup-go
# cache: true restores GOCACHE across runs, and the cache key covers the test
# binary, its args, and observed env vars - nothing about whether a live TCP
# connection to Postgres actually succeeded. A PR that edits only the
# services.postgres block or the health check in go-ci.yml leaves the Go
# inputs byte-identical, so without -count=1 this target can report
# "ok (cached)" in well under a second while never having contacted the
# database it names.
test-cli-integration:
	go test -tags integration -count=1 ./internal/cli/... -timeout 480s

# Run the Postgres-only integration lanes, plus internal/agent (see below):
# internal/store, internal/schedrunner, internal/testsupport/pgdsn's own
# database-touching self-test, cmd/relay-server, internal/worker,
# internal/scheduler and internal/mcp - the packages
# .github/workflows/go-ci.yml's pg-integration job runs. All seven Postgres
# users take their database from internal/testsupport/pgdsn, the same harness
# test-cli-integration uses, so the same two modes (unset: one testcontainer
# per test; RELAY_TEST_DATABASE_URL set: one CREATEd database per test on a
# shared server) apply here too.
#
# cmd/relay-server is here because its integration lane, like the Postgres
# users above, takes its database from internal/testsupport/pgdsn. Its gRPC
# servers listen on 127.0.0.1:0 in-process, its agent
# (agent_subprocess_e2e_integration_test.go) is an in-process agent.Agent
# rather than a built binary, and the subprocess it runs is the test binary
# itself.
#
# ./internal/agent NEEDS NO DATABASE AT ALL - it is here because this is the
# job that runs integration-tagged tests on an ubuntu runner, not because it
# needs the service. Its two tagged files drive an in-process fake gRPC
# coordinator and real subprocesses; neither touches Postgres. Do not "fix"
# this by moving it to its own job over an apparent inconsistency.
#
# THE PACKAGE PATH IS ./internal/agent, NEVER ./internal/agent/..., AND THAT
# IS LOAD-BEARING. The /... form also matches internal/agent/source/perforce,
# whose tests t.Skip when the p4 client or Docker is unreachable - both true
# on a GitHub runner - so the wrong pattern makes this job report green having
# silently run zero of that package's tests. Confirm with
# `go test -tags integration -list '.*' ./internal/agent` (no perforce test
# names) before changing this line.
#
# -count=1 for the same reason test-cli-integration's comment gives: the test
# cache says nothing about whether a live TCP connection to Postgres
# succeeded.
#
# No -p 1, in shared-service mode: every test gets its own freshly created
# database, so cross-package parallelism has no PER-DATABASE state to
# corrupt - unlike make test-integration's -p 1, which exists for parallel
# CONTAINER conflicts, a hazard this mode does not create. This reasoning
# does NOT cover the default (container) mode, where this target's packages
# churn their own testcontainers concurrently; that hazard is
# make test-integration's -p 1 to address, not this target's.
test-pg-integration:
	go test -tags integration -count=1 ./internal/store/... ./internal/schedrunner/... ./internal/testsupport/... ./cmd/relay-server/... ./internal/worker/... ./internal/scheduler/... ./internal/mcp/... ./internal/agent -timeout 600s

# Run internal/api's integration lane on its own job/target rather than
# folding it into test-pg-integration above: internal/api alone runs about
# 1.8x that target's combined acquisition count, so adding it there would not
# fit test-pg-integration's own timeout argument, it would replace it - and
# two jobs on separate runners make the wall clock the max rather than the
# sum, the same reason cli-integration and pg-integration are already
# separate jobs rather than steps in one.
#
# -timeout 1800s, NOT 600s: this target must also work in container mode for
# a developer with Docker and no RELAY_TEST_DATABASE_URL, where this package
# alone runs around 750s measured (see the comment on test-integration above)
# with a variance band wide enough that 600s has already killed a run of it,
# reporting FAIL with no --- FAIL line beneath it. See
# internal/api/testhelper_test.go for the harness this shares with
# every other integration lane in this repo.
test-api-integration:
	go test -tags integration -count=1 ./internal/api/... -timeout 1800s

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
