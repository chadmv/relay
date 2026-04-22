# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build all three binaries into bin/
make build

# Unit tests (no Docker required)
make test

# Integration tests (requires Docker Desktop running; -p 1 prevents parallel container conflicts)
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
| `RELAY_BOOTSTRAP_ADMIN` | — | Email of first admin to create on startup |
| `RELAY_BOOTSTRAP_PASSWORD` | — | Required when `RELAY_BOOTSTRAP_ADMIN` is set |
| `RELAY_DB_MAX_CONNS` | `25` | Maximum pgxpool connections to Postgres |
| `RELAY_WORKER_GRACE_WINDOW` | `2m` | How long to wait before requeueing a disconnected worker's tasks |
| `RELAY_CORS_ORIGINS` | _(empty)_ | Comma-separated CORS allowlist for HTTP API (empty = same-origin only, wildcard `*` rejected) |

CLI reads `RELAY_URL` and `RELAY_TOKEN` as overrides for the config file values.

## Architecture

Relay is a render farm job coordinator with three binaries:

- **`relay-server`** — HTTP REST API (`:8080`) + gRPC server (`:9090`) + scheduler
- **`relay-agent`** — runs on worker nodes; connects to the server via gRPC and executes tasks as subprocesses
- **`relay`** — CLI client

### relay-server internals

`cmd/relay-server/main.go` wires everything together:

1. Runs postgres migrations via `store.Migrate()`
2. Creates shared dependencies: `*pgxpool.Pool`, `*store.Queries`, `*events.Broker`, `*worker.Registry`, `*scheduler.Dispatcher`
3. Starts gRPC server (serves `worker.Handler`), the scheduler loop, and the HTTP server
4. `dispatcher.Trigger` is passed as a callback into both the API server and the gRPC handler to avoid an api→scheduler import cycle

**`internal/api/`** — HTTP handlers. `server.go` registers all routes. Handler methods live in separate files by resource (`auth.go`, `jobs.go`, `tasks.go`, `workers.go`, etc.). The `BearerAuth` middleware validates tokens and injects `AuthUser` into the request context; `AdminOnly` chains after it for admin-only routes.

**`internal/store/`** — sqlc-generated store layer (pgx/v5). SQL queries live in `internal/store/query/*.sql`. After editing any `.sql` file, run `make generate` to regenerate `*.sql.go` and `models.go`. Never edit generated files directly. `store.Queries` accepts any `DBTX` (pool or transaction); use `q.WithTx(tx)` for transactions.

**`internal/scheduler/`** — `Dispatcher` polls for eligible tasks and dispatches them to available workers via `worker.Registry`. Triggered by `Dispatcher.Trigger()` after job submission and task completion.

**`internal/events/`** — `Broker` fans out state-change events to SSE subscribers. Events carry a `JobID` filter; `""` = broadcast to all.

**`internal/worker/`** — `Registry` holds in-memory connected gRPC streams (worker ID → sender). `Handler` implements `AgentServiceServer`, handles the `Connect` stream, persists workers, and receives task status updates.

### relay-agent internals

`internal/agent/Agent` maintains one gRPC stream to the coordinator. A single send goroutine owns all writes to the stream (gRPC streams are not concurrent-send-safe); messages are queued on a buffered `sendCh` (capacity 64). The agent reconnects with exponential backoff. `internal/agent/Runner` executes each task as a subprocess and streams stdout/stderr back. Hardware capabilities are detected at startup (`internal/agent/capabilities.go`; GPU detection is NVIDIA-only via `nvidia-smi`). mDNS discovery (`internal/discovery/`) lets agents find the server automatically on the local network.

### relay CLI internals

No cobra/viper — uses stdlib `flag`. Each subcommand is a `cli.Command` struct; `cli.Dispatch()` dispatches by name. Config is stored at `~/.relay/config.json` (Linux/Mac) or `%APPDATA%\relay\config.json` (Windows).

## Key Design Decisions

**Token format:** 32 random bytes → hex-encode → SHA-256(hex) → hex-encode → store hash in DB. The raw hex is returned to the client and never stored.

**Password hashing:** bcrypt cost 12. The `bcryptCost` package variable in `internal/api/auth.go` is overridden to `bcrypt.MinCost` in integration tests via `SetBcryptCostForTest()` (exported from `internal/api/export_test.go` under the `integration` build tag).

**Email enumeration prevention:** `handleLogin` always calls `bcrypt.CompareHashAndPassword` even when the email is not found, using a pre-computed dummy hash (`getDummyHash()` via `sync.Once`).

**Testability overrides:** Several package-level `var` functions can be swapped in tests without build tags:
- `internal/cli`: `saveConfigFn`, `configFilePathFn`, `readPasswordFn`
- `internal/api` (integration only): `bcryptCost` via `SetBcryptCostForTest()`

**Task DAG:** Tasks within a job form a dependency DAG (`task_dependencies` table). The scheduler uses a recursive CTE (`FailDependentTasks`) for transitive cascade on failure.

**Database:** migrations are embedded in the binary and run automatically on startup. Migration files live in `internal/store/migrations/` and use the `golang-migrate` format (`000N_name.up.sql` / `000N_name.down.sql`).

## Session Continuity

At the start of each session, read the most recent file in `docs/retros/` for context on prior work.
