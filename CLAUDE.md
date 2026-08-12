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
- `internal/store/` — sqlc-generated. SQL lives in `internal/store/query/*.sql`; run `make generate` after edits (sqlc emits LF; on this CRLF repo it rewrites line endings across all generated files - after generating, run `git diff --ignore-all-space` and keep only the real content change, reverting LF-only hunks with `git checkout -- <file>`). **Never edit `*.sql.go` or `models.go` directly.** `store.Queries` accepts any `DBTX` (pool or transaction); use `q.WithTx(tx)` for transactions.
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

Cross-cutting rules that new code must not bypass. Every high-severity finding in the 2026-06-10 codebase review was a path that sidestepped an invariant already enforced elsewhere - check changes against these.

Most are stated in backend terms because that is where they were first codified, but the reasoning is **not backend-specific**. The generation-ordering rule below was rediscovered as a frontend bug on 2026-08-09 precisely because it was phrased only as a database concern, so it was invisible to whoever wrote the equivalent async lifecycle in `web/`. When you are working in `web/`, read these for the shape, not just the nouns.

- **End the generation before releasing the resource.** Wherever a generation, epoch, or token guards whether an async continuation is still current, bump it *first* and release the resource *second*. Releasing first leaves the dying resource's own callbacks running while they still look current, so they pass the staleness guard and clobber the state the teardown just set. This is the general form of the epoch fence below; the frontend instance is an `AbortController` in an effect (`web/src/jobs/useTaskLogStream.ts`), where aborting an SSE stream without bumping the run generation let the dying connection's rejection overwrite an `error` status with `reconnecting` and retry a 404 the code documented as non-transient. When reviewing, search for `abort()`, `close()`, `cancel()`, and unregister calls, and ask what the released thing's own handlers will do on the next tick.
- **Epoch fence.** Every write to `tasks.status` or `task_logs` must either fence on `assignment_epoch` (match the caller's epoch) or end the assignment (bump it, as `ClaimTaskForWorker` and `RequeueWorkerTasks` do). Never call an epoch-fenced query with a zero-value epoch, and never return a task to `pending` without bumping the epoch. Gate any side effect on the fence having actually matched - `AppendTaskLog` returns `pgx.ErrNoRows` for a stale chunk, and `handleTaskLog` must drop it *before* publishing, or a zombie agent's output appears in a live view and then vanishes on refresh. `AppendTaskLog` additionally fences on `tasks.worker_id` matching the connection's authenticated worker - the epoch establishes currency, not identity, and the comparison must stay NULL-rejecting so a zero-value worker id fails closed.
- **Single job-spec pipeline.** All job-spec ingestion (REST API, CLI, MCP, schedrunner) goes through `jobspec.Validate` and `CreateJobFromSpec`. Never define parallel spec structs or task-creation paths; if a field is added to `jobspec.TaskSpec`, every consumer gets it for free only if they share the types.
- **One bounded sender per gRPC stream.** All writes to a stream go through its single send goroutine (agent: `sendCh`; server: `workerSender`). Sends from other goroutines must be bounded - a peer that stops reading must never block a dispatcher or HTTP handler indefinitely.
- **Identity-checked teardown.** Connection cleanup must only tear down state it owns: verify the registered sender or handle is yours before unregistering a worker, marking it offline, or arming a grace timer. A stale connection's defers must not clobber a fresh registration.
- **No interior pointers across locks.** Shared registries return value copies from getters; mutation happens through methods that hold the lock, never on pointers that escaped it.
- **Single JSON entry point.** HTTP request bodies are read only via `readJSON` in `internal/api/server.go`; request-size limits and decode policy live there, not at call sites.

## Subagent team and routing

Development is supported by a team of role-specialized subagents in `.claude/agents/`
(`relay-tpm`, `relay-planner`, `relay-backend-engineer`, `relay-frontend-engineer`,
`relay-integration-tester`, `relay-code-reviewer`, plus the built-in `Explore` for
read-only discovery). The main interactive session is the conductor: it orchestrates them
through a phased `spec -> plan -> implement -> verify -> integrate -> retro` lifecycle. See
[docs/agent-team/README.md](docs/agent-team/README.md) for the roster, the phase pipeline,
gate modes, and when to dispatch which agent. The design spec behind the team is
`docs/superpowers/specs/2026-06-18-agent-team-design.md`.

**Doc-only vs code agents.** `relay-tpm`, `relay-planner`, and `relay-code-reviewer` never
touch source - they produce specs, plans, and review findings respectively. Committing the
spec doc (Phase 1) and the plan doc (Phase 2) is always the conductor's step, since those
agents hold no git access; both commits land at the phase boundary before any code is
written. `relay-backend-engineer`, `relay-frontend-engineer`, and `relay-integration-tester`
edit code (and tests) under TDD and the Invariants above.

**Verification.** Phase 4 is a conductor-run `/code-review` followed by a parallel fan-out of four
agents dispatched in one message: `relay-code-reviewer` on three separate lenses (invariants,
correctness, security) plus `relay-integration-tester`. No Workflow, so no opt-in to obtain. The
agents cannot run `/code-review` themselves - it is a slash command, not a skill - so the conductor
runs it and feeds the output in as prior findings, which each agent then confirms or refutes with its
own evidence before adding its own passes. Confirmed findings route back to the owning engineer, then
re-verify until clean. See [docs/agent-team/README.md](docs/agent-team/README.md) for the lens briefs.

**Closing backlog items.** Always close a backlog item with the `/backlog close <fragment>`
command, never by hand-editing the item's `status` field in place. The command runs the full
close sequence the skill enforces: it `git mv`s the file from `docs/backlog/` into
`docs/backlog/closed/`, stamps `status: closed` plus `closed:`/`resolution:` frontmatter,
appends a `## Resolution` note, and commits. The `git mv` to `docs/backlog/closed/` is required
scope when a task closes a backlog item, not optional cleanup. Flipping `status` alone leaves
the file in the open directory and skips these steps, which `/backlog list` then reports as a
malformed open item.