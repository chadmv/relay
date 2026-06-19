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

## Invariants

Cross-cutting rules that new code must not bypass. Every high-severity finding in the 2026-06-10 codebase review was a path that sidestepped an invariant already enforced elsewhere - check changes against these:

- **Epoch fence.** Every write to `tasks.status` or `task_logs` must either fence on `assignment_epoch` (match the caller's epoch) or end the assignment (bump it, as `ClaimTaskForWorker` and `RequeueWorkerTasks` do). Never call an epoch-fenced query with a zero-value epoch, and never return a task to `pending` without bumping the epoch.
- **Single job-spec pipeline.** All job-spec ingestion (REST API, CLI, MCP, schedrunner) goes through `jobspec.Validate` and `CreateJobFromSpec`. Never define parallel spec structs or task-creation paths; if a field is added to `jobspec.TaskSpec`, every consumer gets it for free only if they share the types.
- **One bounded sender per gRPC stream.** All writes to a stream go through its single send goroutine (agent: `sendCh`; server: `workerSender`). Sends from other goroutines must be bounded - a peer that stops reading must never block a dispatcher or HTTP handler indefinitely.
- **Identity-checked teardown.** Connection cleanup must only tear down state it owns: verify the registered sender or handle is yours before unregistering a worker, marking it offline, or arming a grace timer. A stale connection's defers must not clobber a fresh registration.
- **No interior pointers across locks.** Shared registries return value copies from getters; mutation happens through methods that hold the lock, never on pointers that escaped it.
- **Single JSON entry point.** HTTP request bodies are read only via `readJSON` in `internal/api/server.go`; request-size limits and decode policy live there, not at call sites.

## Behavior

Behavioral guidelines to reduce common LLM coding mistakes.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.