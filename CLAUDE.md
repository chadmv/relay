# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build all three binaries into bin/
make build

# Unit tests (no Docker required)
make test

# Integration tests (requires Docker Desktop running and the `p4` CLI on PATH;
# spins up Postgres and p4d containers; -p 1 prevents parallel container conflicts)
make test-integration

# Regenerate sqlc store layer and protobuf bindings after editing .sql or .proto files
make generate

# Run a single test
go test ./internal/api/... -run TestRegister_HappyPath -v -timeout 30s

# Run integration tests for one package
go test -tags integration -p 1 ./internal/api/... -run TestRegister -v -timeout 120s
```

Integration tests use `//go:build integration` and spin up real Postgres containers via testcontainers-go. On Windows the `desktop-linux` Docker context is used automatically.

## Architecture

Three binaries: **`relay-server`** (HTTP `:8080` + gRPC `:9090` + scheduler), **`relay-agent`** (worker; gRPC stream to server; runs tasks as subprocesses), **`relay`** (CLI client). Full architecture, env vars, REST API, and CLI reference live in [README.md](README.md).

Code map:

- `cmd/{relay-server,relay-agent,relay}/main.go` — entrypoints; `relay-server` wires `*pgxpool.Pool` → `*store.Queries` → `*events.Broker` → `*worker.Registry` → `*scheduler.Dispatcher` and starts the schedrunner loop.
- `internal/api/` — HTTP handlers, one file per resource (`auth.go`, `jobs.go`, `tasks.go`, `workers.go`, `users.go`, `agent_enrollments.go`, `scheduled_jobs.go`, …). `BearerAuth` middleware injects `AuthUser` into context; `AdminOnly` chains after it. `cors.go` is fail-closed (wildcard `*` rejected). `ratelimit.go` is per-IP via `RemoteAddr` only — `X-Forwarded-For` is not trusted. `job_spec.go` exports `JobSpec`/`ValidateJobSpec`/`CreateJobFromSpec` for reuse by `schedrunner`.
- `internal/store/` — sqlc-generated. SQL lives in `internal/store/query/*.sql`; run `make generate` after edits. **Never edit `*.sql.go` or `models.go` directly.** `store.Queries` accepts any `DBTX` (pool or transaction); use `q.WithTx(tx)` for transactions.
- `internal/scheduler/` — `Dispatcher` polls eligible tasks and dispatches via `worker.Registry`. Wake it with `Dispatcher.Trigger()` (passed as a callback to avoid import cycles). `NotifyListener` consumes Postgres `LISTEN/NOTIFY` to trigger across processes.
- `internal/schedrunner/` — Cron engine for `scheduled_jobs` (5-field cron, `@hourly`/`@daily`, `@every <dur>`, IANA TZ via `robfig/cron/v3`). 10 s ticker; `ReconcileOnStartup()` advances missed `next_run_at` (never-catch-up). **Does NOT import `internal/api`** — calls store directly to avoid a cycle.
- `internal/worker/` — `Registry` (in-memory worker ID → gRPC sender) plus `Handler.Connect()` which dispatches to `enrollAndRegister` (first boot, consumes enrollment token) or `reconnectAndRegister` (long-lived agent token), then `finishRegister`. `GraceRegistry` defers requeue of a disconnected worker's tasks until `RELAY_WORKER_GRACE_WINDOW`.
- `internal/agent/` — `Agent` maintains one gRPC stream; **a single send goroutine owns all writes** (gRPC streams are not concurrent-send-safe; messages queue on a 64-cap `sendCh`). `Runner` runs each task as a subprocess and streams stdout/stderr back. Hardware caps detected at startup (`capabilities.go`; GPU is NVIDIA-only via `nvidia-smi`). `credentials.go` reads/persists the long-lived token at `<state-dir>/token` (0600).
- `internal/events/` — SSE `Broker`. Events carry a `JobID` filter; `""` = broadcast.
- `internal/cli/` — stdlib `flag`, no cobra. Each subcommand is a `cli.Command`; `cli.Dispatch()` routes by name. Config at `~/.relay/config.json` or `%APPDATA%\relay\config.json`.
- `internal/discovery/` — mDNS browse for `_relay._tcp.local`.

## Key Design Decisions

**Token format.** 32 random bytes → hex-encode → SHA-256 of the hex → hex-encode the digest → store hash. Raw hex returned to the client and never stored. **All hashing goes through `internal/tokenhash.Hash` — never inline `sha256.Sum256` at a new site.**

**Password hashing.** bcrypt cost 12. The `bcryptCost` package var in `internal/api/auth.go` is overridden to `bcrypt.MinCost` in integration tests via `SetBcryptCostForTest()` (exported from `internal/api/export_test.go` under `//go:build integration`).

**Email enumeration prevention.** `handleLogin` always calls `bcrypt.CompareHashAndPassword`, even on unknown emails, against a pre-computed dummy hash (`getDummyHash()` via `sync.Once`).

**Testability overrides** (no build tags). `internal/cli` exposes `saveConfigFn`, `configFilePathFn`, `readPasswordFn` as package vars for swapping in tests.

**Task DAG.** `task_dependencies` table; `FailDependentTasks` recursive CTE for transitive cascade on failure.

**Database.** Migrations are embedded in the binary and run on startup. Files in `internal/store/migrations/` use `golang-migrate` format (`000N_name.up.sql` / `000N_name.down.sql`).

**Source providers.** Relay assumes `p4` is installed and a valid P4 ticket is active on the agent. Provision tickets out-of-band (`p4 login`); relay does not manage P4 credentials. The Perforce integration test spins up a `p4d` container via testcontainers-go.

## Session Continuity

At the start of each session, read the most recent file in `docs/retros/` for context on prior work.

## Backlog

At the start of each session, if `docs/backlog/` exists, list the open backlog files (`find docs/backlog -maxdepth 1 -name "*.md" 2>/dev/null`) and surface a one-line summary: count by type, plus the titles of any `priority: high` items. Do not read full files unless asked.
