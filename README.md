# Relay

Relay is a distributed task execution system. You submit a **job** — a named set of shell commands with optional dependencies — and Relay schedules and runs them across a pool of **worker agents** on your network. Results and logs stream back in real time.

Typical use cases include render farms, batch processing pipelines, CI runners, and any workload where you want to spread compute across multiple machines without managing infrastructure yourself.

---

## Architecture

Relay has three components:

| Binary | Role |
|--------|------|
| `relay-server` | Central coordinator — stores jobs in PostgreSQL, serves the REST API and gRPC endpoint |
| `relay-agent` | Worker node — connects to the server, receives tasks, runs them, streams logs back |
| `relay` | CLI — submit jobs, watch logs, manage workers and reservations |

```
relay (CLI)
    │  REST + SSE
    ▼
relay-server ──── PostgreSQL
    │  gRPC (bidirectional stream)
    ├──► relay-agent  (machine A)
    ├──► relay-agent  (machine B)
    └──► relay-agent  (machine C)
```

Agents discover the server automatically via **mDNS** (`_relay._tcp.local`) or you can point them at a host directly with a flag.

---

## Quick Start

### Prerequisites

- Go 1.22+
- PostgreSQL 14+
- Docker (for integration tests only)

### Build

**Linux / macOS**

```sh
make build
```

Produces `bin/relay-server`, `bin/relay-agent`, and `bin/relay`.

**Windows**

`make` is not available by default on Windows. Build with `go build` directly:

```powershell
go build -o bin\relay-server.exe .\cmd\relay-server
go build -o bin\relay-agent.exe  .\cmd\relay-agent
go build -o bin\relay.exe        .\cmd\relay
```

Or install [GNU Make for Windows](https://gnuwin32.sourceforge.net/packages/make.htm) / use Git Bash / WSL and run `make build` as normal.

> **Cross-compiling** — to build Windows `.exe` files from Linux or macOS:
> ```sh
> GOOS=windows GOARCH=amd64 go build -o bin/relay-server.exe ./cmd/relay-server
> GOOS=windows GOARCH=amd64 go build -o bin/relay-agent.exe  ./cmd/relay-agent
> GOOS=windows GOARCH=amd64 go build -o bin/relay.exe        ./cmd/relay
> ```

### 1 — Start PostgreSQL

**Linux / macOS (Docker)**

```sh
docker run -d \
  --name relay-postgres \
  -e POSTGRES_USER=relay \
  -e POSTGRES_PASSWORD=relay \
  -e POSTGRES_DB=relay \
  -p 5432:5432 \
  postgres:16
```

**Windows (Docker Desktop)**

```powershell
docker run -d `
  --name relay-postgres `
  -e POSTGRES_USER=relay `
  -e POSTGRES_PASSWORD=relay `
  -e POSTGRES_DB=relay `
  -p 5432:5432 `
  postgres:16
```

> **Production use** — the commands above store data inside the container. If the container is deleted and recreated, the data is lost. Add a named volume to persist data across container replacements:
>
> ```sh
> # Linux / macOS
> docker run -d \
>   --name relay-postgres \
>   -e POSTGRES_USER=relay \
>   -e POSTGRES_PASSWORD=relay \
>   -e POSTGRES_DB=relay \
>   -p 5432:5432 \
>   -v relay-pgdata:/var/lib/postgresql/data \
>   postgres:16
> ```
>
> ```powershell
> # Windows
> docker run -d `
>   --name relay-postgres `
>   -e POSTGRES_USER=relay `
>   -e POSTGRES_PASSWORD=relay `
>   -e POSTGRES_DB=relay `
>   -p 5432:5432 `
>   -v relay-pgdata:/var/lib/postgresql/data `
>   postgres:16
> ```
>
> Docker manages the `relay-pgdata` volume internally. Data survives container deletion and is only removed if you explicitly run `docker volume rm relay-pgdata`.

Alternatively, install PostgreSQL natively via the [PostgreSQL Windows installer](https://www.postgresql.org/download/windows/) and create the `relay` database and user manually.

### 2 — Start the server

**Linux / macOS**

```sh
./bin/relay-server
```

**Windows**

```powershell
.\bin\relay-server.exe
```

On first start the server runs all database migrations automatically. Default addresses: HTTP `:8080`, gRPC `:9090`.

### 3 — Start one or more agents

**Linux / macOS**

```sh
# mDNS discovery (same network as server)
./bin/relay-agent

# Explicit coordinator address
./bin/relay-agent --coordinator relay-server.local:9090
```

**Windows**

```powershell
# mDNS discovery
.\bin\relay-agent.exe

# Explicit coordinator address
.\bin\relay-agent.exe --coordinator relay-server.local:9090
```

> **`relay-server.local:9090` explained** — `relay-server.local` is an example mDNS hostname. The `.local` suffix is the standard domain used by mDNS to find machines on a local network by name without a DNS server. Replace `relay-server` with your server machine's actual hostname, or use its IP address directly (e.g. `192.168.1.50:9090`). The `--coordinator` flag accepts any `host:port`.

> **Running the agent on the same machine as the server?** mDNS multicast does not work on the loopback interface, so the agent will fail to discover the server automatically. Use `--coordinator localhost:9090` instead:
>
> ```powershell
> .\bin\relay-agent.exe --coordinator localhost:9090
> ```
>
> ```sh
> ./bin/relay-agent --coordinator localhost:9090
> ```

When the agent connects successfully it prints:
```
connected to coordinator <host>:9090 (worker ID: <uuid>)
```

### 4 — Configure the CLI

**Linux / macOS**

```sh
./bin/relay login
```

**Windows**

```powershell
.\bin\relay.exe login
```

```
Server URL [http://localhost:8080]: (press Enter for default)
Email: you@example.com
```

Credentials are saved to:
- Linux/macOS: `~/.relay/config.json`
- Windows: `%APPDATA%\relay\config.json`

### 5 — Submit a job

**Linux / macOS**

```sh
./bin/relay submit examples/hello-unix.json
```

**Windows**

```powershell
.\bin\relay.exe submit examples\hello-windows.json
```

---

## relay-server

### Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `RELAY_DATABASE_URL` | `postgres://relay:relay@localhost:5432/relay?sslmode=disable` | PostgreSQL connection string |
| `RELAY_HTTP_ADDR` | `:8080` | HTTP server bind address |
| `RELAY_GRPC_ADDR` | `:9090` | gRPC server bind address |
| `RELAY_BOOTSTRAP_ADMIN` | _(empty)_ | Email address — creates or promotes this user to admin on startup when no admin exists |

**Linux / macOS**

```sh
RELAY_DATABASE_URL=postgres://relay:relay@db-host:5432/relay?sslmode=disable \
RELAY_HTTP_ADDR=:8080 \
RELAY_GRPC_ADDR=:9090 \
./bin/relay-server
```

**Windows (PowerShell)**

```powershell
$env:RELAY_DATABASE_URL = "postgres://relay:relay@db-host:5432/relay?sslmode=disable"
$env:RELAY_HTTP_ADDR    = ":8080"
$env:RELAY_GRPC_ADDR    = ":9090"
.\bin\relay-server.exe
```

### Startup sequence

1. Connect to PostgreSQL and run pending migrations
2. Requeue any tasks that were `running` when the server last stopped
3. Start the gRPC server (agent connections)
4. Start the task dispatch scheduler
5. Start the HTTP server (CLI / API traffic)

### Database schema

The server creates these tables on first run:

- **users** — accounts with email and optional admin flag
- **api_tokens** — SHA-256-hashed bearer tokens (30-day expiry)
- **workers** — registered agents with hardware capabilities
- **jobs** — submitted job records
- **tasks** — individual commands belonging to a job
- **task_dependencies** — DAG edges expressing `depends_on` relationships
- **task_logs** — captured stdout/stderr per task
- **reservations** — admin-managed worker allocations
- **invites** — one-time invite tokens issued by admins; SHA-256 hashed; single-use with optional email binding and expiry

---

## relay-agent

### Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--coordinator <host:port>` | *(mDNS discovery)* | Coordinator address; skips network discovery |
| `--state-dir <path>` | `/var/lib/relay-agent` (Linux) · `%ProgramData%\relay` (Windows) | Directory for persistent state |

The agent writes a `worker-id` file to `--state-dir` on first registration. Subsequent restarts reuse the same worker ID so the server recognises it as the same machine.

### Hardware detection

On startup the agent reports to the server:

- CPU core count
- Total RAM (GB)
- GPU count and model (NVIDIA only via `nvidia-smi`; AMD/Intel not detected in v1)
- Operating system
- Hostname

### mDNS discovery

When `--coordinator` is not set, the agent browses the local network for `_relay._tcp.local`. The first IPv4 address that responds is used. If no coordinator is found the agent exits with an error. On IPv6-only networks use `--coordinator` explicitly.

### Reconnection

The agent maintains a persistent gRPC stream to the coordinator. On disconnect it reconnects with exponential backoff starting at 1 s and capping at 60 s.

---

## relay CLI

### Configuration

The CLI reads `~/.relay/config.json` (Linux/Mac) or `%APPDATA%\relay\config.json` (Windows):

```json
{
  "server_url": "http://localhost:8080",
  "token": "<bearer-token>"
}
```

Environment variables override the file:

| Variable | Overrides |
|----------|-----------|
| `RELAY_URL` | `server_url` |
| `RELAY_TOKEN` | `token` |

### Commands

#### `relay login`

Authenticate and save credentials.

```sh
relay login
# Server URL [http://localhost:8080]:
# Email: you@example.com
```

Tokens are valid for 30 days. Re-run `relay login` to refresh.

If the email is not yet registered, the server will require an invite token. The CLI prompts for it automatically:

```
Invite token: <paste token here>
```

---

#### `relay invite create`

Create a one-time invite token (admin only). The token can then be sent to the recipient out-of-band; they supply it when running `relay login` for the first time.

```sh
relay invite create                          # open invite, 72h expiry
relay invite create --email user@example.com # bind to a specific address
relay invite create --expires 24h           # custom expiry
```

The raw token is printed to stdout and is never stored — it cannot be retrieved again.

---

#### `relay submit`

Submit a job from a JSON file.

```sh
relay submit job.json          # submit and tail logs until done
relay submit --detach job.json # submit and print job ID, then exit
```

**Job file format:**

```json
{
  "name": "my-render",
  "priority": "normal",
  "labels": { "project": "film-x" },
  "tasks": [
    {
      "name": "frame-001",
      "command": ["blender", "-b", "scene.blend", "-f", "1"],
      "env": { "SCENE": "scene.blend" },
      "requires": { "gpu": "true" },
      "timeout_seconds": 3600,
      "retries": 2
    },
    {
      "name": "frame-002",
      "command": ["blender", "-b", "scene.blend", "-f", "2"],
      "depends_on": ["frame-001"]
    }
  ]
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Human-readable job name |
| `priority` | No | `normal` (default), `high`, or `low` |
| `labels` | No | Arbitrary key/value metadata |
| `tasks[].name` | Yes | Unique within the job |
| `tasks[].command` | Yes | Executable and arguments as an array |
| `tasks[].env` | No | Extra environment variables for this task |
| `tasks[].requires` | No | Worker label selector (task only runs on matching workers) |
| `tasks[].timeout_seconds` | No | Kill task after this many seconds |
| `tasks[].retries` | No | Retry up to this many times on failure (default 0) |
| `tasks[].depends_on` | No | List of task names that must complete before this one starts |

When submitted without `--detach`, the CLI streams logs to stdout and exits with code 0 when all tasks succeed, or non-zero if any fail.

---

#### `relay list`

List jobs.

```sh
relay list                     # all jobs, table format
relay list --status running    # filter by status
relay list --json              # JSON output
```

Statuses: `pending`, `running`, `done`, `failed`, `cancelled`

---

#### `relay get`

Get full details for a job, including all tasks.

```sh
relay get <job-id>
relay get <job-id> --json
```

---

#### `relay cancel`

Cancel a job. Pending and queued tasks are cancelled immediately; running tasks complete their current execution.

```sh
relay cancel <job-id>
```

---

#### `relay logs`

Stream task logs for a running or completed job via Server-Sent Events.

```sh
relay logs <job-id>
```

Output format:

```
[frame-001 stdout] Blender 4.0, blender.org
[frame-001 stdout] Read blend: scene.blend
[frame-001 stderr] Warning: deprecated API call
```

---

#### `relay workers list`

```sh
relay workers list
relay workers list --json
```

Shows worker ID, name, status, CPU cores, RAM, GPU count, and GPU model.

---

#### `relay workers get`

```sh
relay workers get <worker-id>
relay workers get <worker-id> --json
```

---

#### `relay reservations list`

```sh
relay reservations list
```

---

#### `relay reservations create`

Create a reservation to hold workers for a project or time window (admin only).

```sh
relay reservations create reservation.json
```

**Reservation file format:**

```json
{
  "name": "vfx-sprint",
  "project": "film-x",
  "worker_ids": ["<uuid>", "<uuid>"],
  "selector": { "rack": "gpu-farm" },
  "starts_at": "2026-05-01T09:00:00Z",
  "ends_at": "2026-05-07T18:00:00Z"
}
```

---

#### `relay reservations delete`

```sh
relay reservations delete <reservation-id>
```

---

## REST API

The server exposes a REST API at `http://<host>:8080/v1`. All endpoints except `/health` and `/auth/token` require `Authorization: Bearer <token>`.

### Public

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/health` | Returns `{"status":"ok"}` |
| `POST` | `/v1/auth/token` | Create / retrieve a bearer token |

**POST `/v1/auth/token`** body:

```json
{ "email": "you@example.com", "name": "Your Name", "invite_token": "<raw invite token>" }
```

If the email is known, a new API token is issued immediately. If the email is new, an `invite_token` must be supplied — obtain one from an admin with `relay invite create`. Returns:

```json
{ "token": "<hex>", "expires_at": "2026-07-16T00:00:00Z" }
```

### Jobs

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/jobs` | Submit a job |
| `GET` | `/v1/jobs` | List jobs (`?status=` filter optional) |
| `GET` | `/v1/jobs/{id}` | Get a job |
| `DELETE` | `/v1/jobs/{id}` | Cancel a job |
| `GET` | `/v1/jobs/{id}/tasks` | List tasks for a job |

### Tasks

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/tasks/{id}` | Get a task |
| `GET` | `/v1/tasks/{id}/logs` | Get task log entries |

### Workers

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/workers` | List workers |
| `GET` | `/v1/workers/{id}` | Get a worker |
| `PATCH` | `/v1/workers/{id}` | Update name, labels, or max_slots (admin only) |

### Reservations

All reservation endpoints are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/reservations` | List reservations |
| `POST` | `/v1/reservations` | Create a reservation |
| `DELETE` | `/v1/reservations/{id}` | Delete a reservation |

### Invites

All invite endpoints are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/invites` | Create a one-time invite token |

**POST `/v1/invites`** body:

```json
{ "email": "optional@example.com", "expires_in": "72h" }
```

- `email` — optional; binds the invite to a specific address.
- `expires_in` — optional duration (`"1h"` to `"720h"`); defaults to `"72h"`.

Returns the raw token once:
```json
{ "id": "<uuid>", "token": "<raw token>", "expires_at": "2026-04-19T12:00:00Z" }
```

### Events (Server-Sent Events)

```
GET /v1/events?job_id=<id>
```

Streams job and task status changes as SSE until the job reaches a terminal state. Events have type `job` or `task` and JSON data payloads.

---

## Development

### Run tests

**Linux / macOS**

```sh
make test                # unit tests — no external dependencies
make test-integration    # integration tests — requires Docker
```

**Windows**

```powershell
# Unit tests
go test ./... -timeout 120s

# Integration tests (requires Docker Desktop)
go test -tags integration -p 1 ./... -timeout 300s
```

> Integration tests use [testcontainers-go](https://golang.testcontainers.org/) to spin up a real PostgreSQL container per test. Docker Desktop must be running. The `-p 1` flag is required on Windows to prevent container provider conflicts when multiple packages run in parallel.

### Regenerate code

```sh
make generate
```

**Windows**

```powershell
sqlc generate
buf generate
```

Runs `sqlc generate` (store queries) and `buf generate` (protobuf/gRPC).

### Project layout

```
cmd/
  relay-server/    main.go — server entrypoint
  relay-agent/     main.go — agent entrypoint
  relay/           main.go — CLI entrypoint
internal/
  api/             HTTP handlers and middleware
  agent/           Agent lifecycle, runner, capabilities
  cli/             CLI commands, config, HTTP client
  discovery/       mDNS browse
  events/          SSE broker
  proto/relayv1/   Generated protobuf types
  scheduler/       Task dispatch loop
  store/           sqlc-generated queries, migrations
  worker/          gRPC handler for agent streams
proto/
  relayv1/relay.proto
```

---

## Known limitations (v1)

- Task ordering within a job is by creation time only; priority-based scheduling is not implemented.
- Reservation selectors are informational — only explicit `worker_ids` lists are enforced.
- Cancelling a job does not send cancellation signals to tasks that are already running on agents.
- GPU detection covers NVIDIA only (via `nvidia-smi`).
- No structured logging in relay-agent (errors go to stderr as plain text).
