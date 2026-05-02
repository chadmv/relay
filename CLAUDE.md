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

Integration tests use `//go:build integration` and spin up real Postgres containers via testcontainers-go. Docker Desktop must be running. On Windows the `desktop-linux` Docker context is used automatically.

## Environment Variables (relay-server)

| Variable | Default | Purpose |
|---|---|---|
| `RELAY_DATABASE_URL` | `postgres://relay:relay@localhost:5432/relay?sslmode=disable` | Postgres connection string |
| `RELAY_HTTP_ADDR` | `:8080` | HTTP listen address |
| `RELAY_GRPC_ADDR` | `:9090` | gRPC listen address |
| `RELAY_BOOTSTRAP_ADMIN` | — | Email of first admin to create on startup. Cleared from process env after consumption. |
| `RELAY_BOOTSTRAP_PASSWORD` | — | Required when `RELAY_BOOTSTRAP_ADMIN` is set. Cleared from process env after consumption; operators should also unset it from their shell. |
| `RELAY_DB_MAX_CONNS` | `25` | Maximum pgxpool connections to Postgres |
| `RELAY_WORKER_GRACE_WINDOW` | `2m` | How long to wait before requeueing a disconnected worker's tasks |
| `RELAY_CORS_ORIGINS` | _(empty)_ | Comma-separated CORS allowlist for HTTP API (empty = same-origin only, wildcard `*` rejected) |
| `RELAY_LOGIN_RATE_LIMIT` | `10:1m` | Per-IP rate limit for `POST /v1/auth/login` (format `N:duration`) |
| `RELAY_REGISTER_RATE_LIMIT` | `5:1m` | Per-IP rate limit for `POST /v1/auth/register` |
| `RELAY_ALLOW_SELF_REGISTER` | _(unset)_ | When `true`, `POST /v1/auth/register` accepts requests without an `invite_token` and creates a non-admin user directly. Default off; requires server restart to change. |
| `RELAY_AGENT_ENROLLMENT_TOKEN` | — | One-time enrollment credential for a fresh agent host. Read only when `<state-dir>/token` does not exist. |

## Environment Variables (relay CLI)

| Variable | Purpose |
|---|---|
| `RELAY_URL` | Override server URL from config file |
| `RELAY_TOKEN` | Override auth token from config file |

## Environment Variables (relay-agent)

| Variable | Default | Purpose |
|---|---|---|
| `RELAY_AGENT_ENROLLMENT_TOKEN` | — | One-time enrollment credential for a fresh agent host. Read only when `<state-dir>/token` does not exist. |
| `RELAY_WORKSPACE_ROOT` | _(unset)_ | Absolute path under which Perforce workspaces are created. Required to enable workspace management for `source`-bearing tasks. When unset, source tasks are dispatched but no workspace preparation is performed. |
| `RELAY_WORKSPACE_MAX_AGE` | _(unset)_ | Age threshold (e.g. `14d`, `8h`) past which idle workspaces are evicted. Requires `RELAY_WORKSPACE_ROOT`. |
| `RELAY_WORKSPACE_MIN_FREE_GB` | _(unset)_ | Free-disk threshold in GB. When free disk drops below this, LRU workspaces are evicted. Requires `RELAY_WORKSPACE_ROOT`. |
| `RELAY_WORKSPACE_SWEEP_INTERVAL` | `15m` | How often the eviction sweeper runs. Only active when `MAX_AGE` or `MIN_FREE_GB` is set. |

**Source providers:** Relay assumes `p4` is installed and a valid P4 ticket is active on the agent host. Provision P4 tickets out-of-band (e.g. via `p4 login` in your system startup). Relay does not manage P4 credentials. The Perforce integration test (`perforce_integration_test.go`) spins up a `p4d` container via testcontainers-go; it requires Docker and the `p4` CLI on PATH but no external Perforce server.

## Architecture

Relay is a render farm job coordinator with three binaries:

- **`relay-server`** — HTTP REST API (`:8080`) + gRPC server (`:9090`) + scheduler
- **`relay-agent`** — runs on worker nodes; connects to the server via gRPC and executes tasks as subprocesses
- **`relay`** — CLI client

### relay-server internals

`cmd/relay-server/main.go` wires everything together:

1. Runs postgres migrations via `store.Migrate()`
2. Creates shared dependencies: `*pgxpool.Pool`, `*store.Queries`, `*events.Broker`, `*worker.Registry`, `*scheduler.Dispatcher`, `*scheduler.NotifyListener` (Postgres LISTEN/NOTIFY trigger for the dispatcher)
3. Creates `*worker.GraceRegistry` and seeds it from any workers with active DB tasks at startup
4. Parses `RELAY_CORS_ORIGINS` and rate-limit env vars — fatal on bad values
5. Starts gRPC server, the dispatcher loop, the NotifyListener goroutine, an hourly enrollment-janitor goroutine (`runEnrollmentJanitor` → `q.DeleteExpiredAgentEnrollments`), and the HTTP server
6. `dispatcher.Trigger` is passed as a callback into the API server, gRPC handler, and grace registry to avoid import cycles
7. Calls `schedrunner.ReconcileOnStartup()` to advance `scheduled_jobs.next_run_at` past any missed triggers during downtime (never-catch-up policy), then starts the `schedrunner.Runner` goroutine (10s ticker) which fires eligible schedules by creating fresh `Job` rows via the store directly

**`internal/api/`** — HTTP handlers. `server.go` registers all routes. Handler methods live in separate files by resource (`auth.go`, `jobs.go`, `tasks.go`, `workers.go`, etc.). The `BearerAuth` middleware validates tokens and injects `AuthUser` into the request context; `AdminOnly` chains after it for admin-only routes. `agent_enrollments.go` adds three admin-only routes: `POST /v1/agent-enrollments` (create enrollment token), `GET /v1/agent-enrollments` (list active enrollments), `DELETE /v1/workers/{id}/token` (revoke agent long-lived token). `cors.go` provides `ParseCORSOrigins` and the `CORS` middleware (fail-closed; wildcard `*` rejected; never emits `Allow-Credentials`). `ratelimit.go` provides `ParseRateLimit` and the `RateLimit` middleware (sliding-window, per-IP via `RemoteAddr`; `X-Forwarded-For` is not trusted). `users.go` exposes `GET /v1/users` (admin-only) for listing accounts; supports `?email=<exact>` filter for direct lookup; uses `GetUserByEmailPublic` so the password hash is never read on the public path; never returns `password_hash`. Also `PATCH /v1/users/me` (any authenticated user) and `PATCH /v1/users/{id}` (admin-only) update the display name, validating that the trimmed name is non-empty and returning the same `userResponse` shape. Also `POST /v1/users` (admin-only) for direct user provisioning, accepting `{email, name?, password, is_admin?}` and returning the same `userResponse` shape (no session token). The `POST /v1/auth/register` handler has a self-serve branch: when `Server.AllowSelfRegister` is true and `invite_token` is empty, it creates a non-admin user directly without an invite.

**`internal/store/`** — sqlc-generated store layer (pgx/v5). SQL queries live in `internal/store/query/*.sql`. After editing any `.sql` file, run `make generate` to regenerate `*.sql.go` and `models.go`. Never edit generated files directly. `store.Queries` accepts any `DBTX` (pool or transaction); use `q.WithTx(tx)` for transactions.

**`internal/scheduler/`** — `Dispatcher` polls for eligible tasks and dispatches them to available workers via `worker.Registry`. Triggered by `Dispatcher.Trigger()` after job submission and task completion.

**`internal/schedrunner/`** — Scheduled-jobs engine. `cron.go` parses cron expressions (standard 5-field, `@hourly`/`@daily`, `@every <dur>`) with IANA timezone support via `github.com/robfig/cron/v3`. `runner.go` implements `Runner.Run()` (10s ticker) and `ReconcileOnStartup()`. `internal/api/scheduled_jobs.go` adds six HTTP endpoints (`POST/GET/PATCH/DELETE /v1/scheduled-jobs`, `POST /v1/scheduled-jobs/{id}/run-now`) and `internal/api/job_spec.go` exports `JobSpec`, `TaskSpec`, `ValidateJobSpec`, and `CreateJobFromSpec` for reuse. The `schedrunner` package does NOT import `internal/api` to avoid a cycle; it calls store functions directly for job creation.

**`internal/events/`** — `Broker` fans out state-change events to SSE subscribers. Events carry a `JobID` filter; `""` = broadcast to all.

**`internal/worker/`** — `Registry` holds in-memory connected gRPC streams (worker ID → sender). `Handler` implements `AgentServiceServer`. `Connect()` dispatches authentication in `authenticateAndRegister()`:
- `enrollAndRegister()` — validates enrollment token hash, checks consumed/expiry, upserts worker by hostname, atomically consumes token, generates and stores long-lived agent token hash
- `reconnectAndRegister()` — hashes the presented agent token, looks up worker via `GetWorkerByAgentTokenHash`
- `finishRegister()` — common path: marks worker online, cancels grace timer, reconciles agent's running-task list against DB, sends `RegisterResponse` (includes new agent token on first enroll), registers sender in registry, triggers dispatcher

### relay-agent internals

`internal/agent/Agent` maintains one gRPC stream to the coordinator. A single send goroutine owns all writes to the stream (gRPC streams are not concurrent-send-safe); messages are queued on a buffered `sendCh` (capacity 64). The agent reconnects with exponential backoff. `internal/agent/Runner` executes each task as a subprocess and streams stdout/stderr back. Hardware capabilities are detected at startup (`internal/agent/capabilities.go`; GPU detection is NVIDIA-only via `nvidia-smi`). mDNS discovery (`internal/discovery/`) lets agents find the server automatically on the local network. `internal/agent/credentials.go` manages the `Credentials` struct: `LoadCredentials(stateDir)` reads `<stateDir>/token` (missing = no error); `SetEnrollmentToken` captures `RELAY_AGENT_ENROLLMENT_TOKEN` in memory then clears the env var; `Persist(agentToken)` writes the long-lived token to disk at 0600 perms and clears the in-memory enrollment token. On first boot the enrollment token is used; subsequent reconnects use the persisted agent token; revoked tokens cause the agent to exit with a log message.

### relay CLI internals

No cobra/viper — uses stdlib `flag`. Each subcommand is a `cli.Command` struct; `cli.Dispatch()` dispatches by name. Config is stored at `~/.relay/config.json` (Linux/Mac) or `%APPDATA%\relay\config.json` (Windows). Relevant subcommands for agent management:
- `relay agent enroll [--hostname HINT] [--ttl DURATION]` — creates an enrollment token (admin only); token to stdout, metadata to stderr for easy script capture
- `relay workers revoke <id-or-hostname>` — calls `DELETE /v1/workers/{id}/token`; accepts UUID or hostname (resolved via worker list)
- `relay schedules create --name NAME --cron EXPR [--tz ZONE] [--overlap skip|allow] --spec FILE.json` — create a recurring schedule; owner is the calling user
- `relay schedules list` / `show <id>` / `update <id> [...]` / `delete <id>` / `run-now <id>` — manage schedules; non-admins see only their own
- `relay admin users list` / `relay admin users get <email>` — admin-only user listing/lookup; calls `GET /v1/users`
- `relay admin users create --email <email> [--name <name>] [--admin]` — admin-only direct user provisioning; password read interactively; calls `POST /v1/users`
- `relay profile update --name "<name>"` — update your own display name; calls `PATCH /v1/users/me`
- `relay admin users update <email-or-id> --name "<name>"` — admin-only override of another user's display name; resolves email→UUID via `GET /v1/users?email=` when the positional isn't a UUID

## Key Design Decisions

**Token format:** 32 random bytes → hex-encode → SHA-256 of the hex string → hex-encode the digest → store hash in DB. The raw hex is returned to the client and never stored. All hashing goes through `internal/tokenhash.Hash`; never inline the SHA-256 call at a new site.

**Password hashing:** bcrypt cost 12. The `bcryptCost` package variable in `internal/api/auth.go` is overridden to `bcrypt.MinCost` in integration tests via `SetBcryptCostForTest()` (exported from `internal/api/export_test.go` under the `integration` build tag).

**Email enumeration prevention:** `handleLogin` always calls `bcrypt.CompareHashAndPassword` even when the email is not found, using a pre-computed dummy hash (`getDummyHash()` via `sync.Once`).

**Testability overrides:** Several package-level `var` functions can be swapped in tests without build tags:
- `internal/cli`: `saveConfigFn`, `configFilePathFn`, `readPasswordFn`
- `internal/api` (integration only): `bcryptCost` via `SetBcryptCostForTest()`

**Task DAG:** Tasks within a job form a dependency DAG (`task_dependencies` table). The scheduler uses a recursive CTE (`FailDependentTasks`) for transitive cascade on failure.

**Database:** migrations are embedded in the binary and run automatically on startup. Migration files live in `internal/store/migrations/` and use the `golang-migrate` format (`000N_name.up.sql` / `000N_name.down.sql`).

## Session Continuity

At the start of each session, read the most recent file in `docs/retros/` for context on prior work.

## Backlog

At the start of each session, if `docs/backlog/` exists, list the open backlog files (`find docs/backlog -maxdepth 1 -name "*.md" 2>/dev/null`) and surface a one-line summary: count by type, plus the titles of any `priority: high` items. Do not read full files unless asked.
