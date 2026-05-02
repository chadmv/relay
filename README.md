# Relay

Relay is a distributed task execution system. You submit a **job** — a named set of shell commands with optional dependencies — and Relay schedules and runs them across a pool of **worker agents** on your network. Results and logs stream back in real time.

Typical use cases include render farms, batch processing pipelines, CI runners, and any workload where you want to spread compute across multiple machines without managing infrastructure yourself.

---

## Architecture

Relay has three components:

| Binary | Role |
|--------|------|
| `relay-server` | Central coordinator — stores jobs in PostgreSQL, serves the REST API and gRPC endpoint, runs the scheduler and the scheduled-job (cron) engine |
| `relay-agent` | Worker node — connects to the server, receives tasks, runs them, streams logs back; can also manage source workspaces (e.g. Perforce stream clients) |
| `relay` | CLI — submit jobs, watch logs, manage workers, reservations, and recurring schedules |

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

**First-time setup — create the initial admin user**

On a fresh install there are no users. Set `RELAY_BOOTSTRAP_ADMIN` and `RELAY_BOOTSTRAP_PASSWORD` to create (or promote) an admin account on startup:

**Linux / macOS**

```sh
RELAY_BOOTSTRAP_ADMIN=admin@example.com \
RELAY_BOOTSTRAP_PASSWORD=changeme \
./bin/relay-server
```

**Windows**

```powershell
$env:RELAY_BOOTSTRAP_ADMIN    = "admin@example.com"
$env:RELAY_BOOTSTRAP_PASSWORD = "changeme"
.\bin\relay-server.exe
```

Both variables are cleared from the process environment immediately after the account is created. On subsequent starts they are not needed — omit them and the server starts normally.

### 3 — Configure the CLI

First, log in as the admin you created in step 2:

**Linux / macOS**

```sh
./bin/relay login
```

**Windows**

```powershell
.\bin\relay.exe login
```

Enter the server URL (default `http://localhost:8080`) and the admin email and password from step 2.

Credentials are saved to:
- Linux/macOS: `~/.relay/config.json`
- Windows: `%APPDATA%\relay\config.json`

This saves a bearer token to your config file so subsequent `relay` commands are authenticated.

### 4 — Enroll and start one or more agents

Before a new agent can connect, an admin must issue it a one-time enrollment token:

```sh
./bin/relay agent enroll --hostname worker-01
# relay-agent token: <token printed here>
```

Set that token as an environment variable before starting the agent for the first time. After enrollment the agent persists a long-lived token in `--state-dir` and the env var is no longer needed.

**Linux / macOS**

```sh
# First boot — provide the enrollment token
RELAY_AGENT_ENROLLMENT_TOKEN=<token> ./bin/relay-agent

# Subsequent starts — long-lived token read from state-dir automatically
./bin/relay-agent

# Explicit coordinator address
./bin/relay-agent --coordinator relay-server.local:9090
```

**Windows**

```powershell
# First boot
$env:RELAY_AGENT_ENROLLMENT_TOKEN = "<token>"
.\bin\relay-agent.exe

# Subsequent starts
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
| `RELAY_BOOTSTRAP_ADMIN` | _(empty)_ | Email address — creates or promotes this user to admin on startup when no admin exists. Cleared from process env after consumption. |
| `RELAY_BOOTSTRAP_PASSWORD` | _(empty)_ | Required when `RELAY_BOOTSTRAP_ADMIN` is set. Cleared from process env after consumption; operators should also unset it from their shell. |
| `RELAY_DB_MAX_CONNS` | `25` | Maximum PostgreSQL connection pool size |
| `RELAY_WORKER_GRACE_WINDOW` | `2m` | How long to wait before requeueing tasks from a disconnected agent |
| `RELAY_CORS_ORIGINS` | _(empty)_ | Comma-separated CORS allowlist for HTTP API (empty = same-origin only, wildcard `*` rejected) |
| `RELAY_LOGIN_RATE_LIMIT` | `10:1m` | Per-IP rate limit for `POST /v1/auth/login` (format `N:duration`) |
| `RELAY_REGISTER_RATE_LIMIT` | `5:1m` | Per-IP rate limit for `POST /v1/auth/register` |

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
2. Seed grace timers for any agents that had active tasks when the server last stopped (tasks requeue if the agent does not reconnect within `RELAY_WORKER_GRACE_WINDOW`)
3. Start the gRPC server (agent connections)
4. Start the task dispatch scheduler and Postgres LISTEN/NOTIFY trigger
5. Start an hourly janitor that purges expired enrollment tokens
6. Start the HTTP server (CLI / API traffic)
7. Reconcile scheduled jobs (advance any `next_run_at` that fell in the past while the server was down, then start the scheduler polling loop)

### Database schema

The server creates these tables on first run:

- **users** — accounts with email and optional admin flag
- **api_tokens** — SHA-256-hashed bearer tokens (30-day expiry)
- **workers** — registered agents with hardware capabilities and persisted agent token hash
- **agent_enrollments** — admin-issued one-time enrollment tokens (SHA-256 hashed, TTL-bounded, atomically consumed on first agent connection)
- **jobs** — submitted job records
- **tasks** — individual commands belonging to a job
- **task_dependencies** — DAG edges expressing `depends_on` relationships
- **task_logs** — captured stdout/stderr per task
- **reservations** — admin-managed worker allocations
- **invites** — one-time invite tokens issued by admins; SHA-256 hashed; single-use with optional email binding and expiry
- **scheduled_jobs** — cron-triggered job templates; each row stores the cron expression, timezone, overlap policy, and a `job_spec` JSONB payload fired on schedule
- **worker_workspaces** — server-side inventory of agent-side workspaces (e.g. Perforce stream clients); used by the dispatcher's warm-workspace preference and for admin visibility/eviction

---

## relay-agent

### Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--coordinator <host:port>` | *(mDNS discovery)* | Coordinator address; skips network discovery |
| `--state-dir <path>` | `/var/lib/relay-agent` (Linux) · `%ProgramData%\relay` (Windows) | Directory for persistent state |

The agent writes two files to `--state-dir`:
- `worker-id` — UUID assigned on first registration; reused on reconnect so the server recognises the same machine
- `token` — long-lived authentication token issued by the server on enrollment; written at 0600 permissions

On first boot the agent requires a one-time enrollment token. After successful enrollment the long-lived token is persisted and used automatically on subsequent starts. If the token is revoked by an admin, the agent exits with an authentication error.

### Environment variables

| Variable | Description |
|----------|-------------|
| `RELAY_AGENT_ENROLLMENT_TOKEN` | One-time enrollment credential issued by an admin (`relay agent enroll`). Required on first boot when no `token` file exists. Cleared from process env immediately after capture. |
| `RELAY_WORKSPACE_ROOT` | Absolute path under which the agent creates source-controlled workspaces (e.g. Perforce stream clients). Setting this enables the workspace provider; tasks with a `source` field will fail if it is unset. |
| `RELAY_WORKSPACE_MAX_AGE` | Idle workspace age threshold (e.g. `14d`, `8h`). Workspaces unused longer than this are evicted by the sweeper. |
| `RELAY_WORKSPACE_MIN_FREE_GB` | Free-disk threshold in GB. When free disk drops below this, LRU workspaces are evicted until the threshold is met. |
| `RELAY_WORKSPACE_SWEEP_INTERVAL` | How often the sweeper runs. Default `15m`. Only active when `MAX_AGE` or `MIN_FREE_GB` is set. |

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

### Source workspaces

Tasks can declare an optional `source` spec. When present, the agent prepares a managed workspace (syncs files, applies any shelved changes) before running the task's command, and the working directory passed to the subprocess is the workspace root.

**v1 supports Perforce only.** A worker must have:

- The `p4` CLI on `PATH`.
- A valid P4 ticket — provision via `p4 login` out-of-band; relay does not manage P4 credentials.
- `RELAY_WORKSPACE_ROOT` set to a directory the agent can write to.

**`source` field shape (in a task):**

```json
{
  "name": "render-shot-001",
  "command": ["blender", "-b", "scene.blend", "-f", "1"],
  "source": {
    "type": "perforce",
    "stream": "//depot/film-x/main",
    "sync": [
      { "path": "//depot/film-x/main/...", "rev": "#head" }
    ],
    "unshelves": [12345],
    "workspace_exclusive": false
  }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `type` | Yes | Source provider — `"perforce"` is the only v1 value. |
| `stream` | Yes | Perforce stream path. Workspaces are keyed by stream and reused across tasks. |
| `sync` | Yes | One or more paths to sync. Each entry has `path` (depot path or `...`) and `rev` (`"#head"`, `@CL`, or `@label`). |
| `unshelves` | No | List of pending changelist numbers to unshelve into the workspace before running. Reverted automatically after the task. |
| `workspace_exclusive` | No | If `true`, take an exclusive lock on the workspace (other tasks for the same stream queue). Default `false`. |

**Workspace arbitration.** Multiple tasks targeting the same stream on the same worker share the workspace under a three-rule policy: tasks with the *same baseline* run concurrently; tasks needing additional but disjoint sync paths join additively; everything else serializes. Tasks with `workspace_exclusive: true` always serialize.

**Warm-workspace preference.** The dispatcher prefers workers that already have a synced workspace for the task's stream — even if a colder worker has more free slots. The preference is a soft bias, not a hard pin: if no warm worker is free, a cold worker is used.

**Eviction.** Workspaces persist between tasks. The sweeper goroutine evicts:
- Workspaces idle longer than `RELAY_WORKSPACE_MAX_AGE`.
- Oldest workspaces (LRU) when free disk drops below `RELAY_WORKSPACE_MIN_FREE_GB`.

Active workspaces (held by a running task) are never evicted. Admins can also evict on demand via `relay workers evict-workspace`.

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

#### `relay passwd`

Change your password (requires your current password).

```sh
relay passwd
# Current password:
# New password:
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

#### `relay agent enroll`

Issue a one-time enrollment token for a new agent (admin only). The token is printed to stdout; expiry metadata goes to stderr for easy script capture.

```sh
relay agent enroll                           # open enrollment, 24h expiry
relay agent enroll --hostname worker-01      # informational hostname hint
relay agent enroll --ttl 1h                  # custom expiry
```

Set the printed token as `RELAY_AGENT_ENROLLMENT_TOKEN` when starting the agent for the first time. The token is consumed on first use and cannot be retrieved again.

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
| `tasks[].source` | No | Workspace source spec — agent prepares this before running the task. See [Source workspaces](#source-workspaces). |

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

#### `relay workers revoke`

Revoke the long-lived authentication token for a worker (admin only). The agent exits immediately with an authentication error and will not reconnect until re-enrolled.

```sh
relay workers revoke <worker-id>
relay workers revoke <hostname>
```

---

#### `relay workers workspaces`

List managed source workspaces present on a worker (admin only).

```sh
relay workers workspaces <worker-id>
relay workers workspaces <worker-id> --json
```

Output columns: `SHORT_ID`, `SOURCE_TYPE`, `SOURCE_KEY`, `BASELINE`, `LAST_USED`. The `SHORT_ID` is the local handle used by `relay workers evict-workspace`.

---

#### `relay workers evict-workspace`

Ask a worker to delete one of its managed workspaces (admin only). The eviction is fire-and-forget — the command returns 202 even if the worker is offline; the agent confirms by sending an inventory update on its next connection.

```sh
relay workers evict-workspace <worker-id> <short-id>
```

Workspaces actively held by a running task cannot be evicted; the agent rejects the request and the workspace remains.

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

### Scheduled jobs

Recurring jobs are defined as **schedules** — a cron expression plus a stored job spec that the server submits as a fresh job on every fire. Schedules support standard 5-field cron, the `@hourly` / `@daily` shorthands, and `@every <duration>` (minimum 30 s). Each schedule has an IANA timezone and an overlap policy (`skip` if the previous run is still active, or `allow`).

The server reconciles `next_run_at` on startup: any firings that fell during downtime are skipped (no catch-up), and the schedule resumes on its next eligible fire. A polling loop ticks every 10 s.

Schedules are owned by the user who created them; non-admins see only their own. Admins can list and operate on all of them, and only admins can use `run-now` to fire a schedule immediately.

---

#### `relay schedules list`

List all scheduled jobs owned by the current user (admins see all).

```sh
relay schedules list
```

Output columns: `ID`, `NAME`, `CRON`, `TZ`, `ENABLED`, `NEXT` (next scheduled run time).

---

#### `relay schedules create`

Create a new scheduled job from a job spec file.

```sh
relay schedules create \
  --name nightly-render \
  --cron "0 2 * * *" \
  --spec job.json \
  --tz America/Los_Angeles \
  --overlap skip
```

| Flag | Default | Description |
|------|---------|-------------|
| `--name NAME` | *(required)* | Human-readable schedule name |
| `--cron EXPR` | *(required)* | Cron expression (5-field, or `@hourly`/`@daily`/`@every 30m`) |
| `--spec FILE` | *(required)* | Path to job spec JSON file (same format as `relay submit`) |
| `--tz ZONE` | `UTC` | IANA timezone (e.g. `America/Los_Angeles`) |
| `--overlap skip\|allow` | `skip` | What to do when the previous run is still active: `skip` skips the new fire; `allow` submits anyway |

The minimum supported interval is 30 seconds.

---

#### `relay schedules show`

Print details for a single schedule.

```sh
relay schedules show <schedule-id>
```

---

#### `relay schedules update`

Modify a schedule in place. Only supplied flags are changed.

```sh
relay schedules update <schedule-id> --cron "0 4 * * *"
relay schedules update <schedule-id> --disable
relay schedules update <schedule-id> --enable --tz UTC
```

| Flag | Description |
|------|-------------|
| `--cron EXPR` | New cron expression |
| `--tz ZONE` | New IANA timezone |
| `--overlap skip\|allow` | New overlap policy |
| `--enable` | Re-enable a disabled schedule |
| `--disable` | Pause the schedule without deleting it |

---

#### `relay schedules delete`

Delete a schedule. Already-submitted jobs are not affected.

```sh
relay schedules delete <schedule-id>
```

---

#### `relay schedules run-now`

Fire the schedule immediately, outside of its normal cron cadence (admin only).

```sh
relay schedules run-now <schedule-id>
```

Prints the ID and initial status of the job that was created.

---

## REST API

The server exposes a REST API at `http://<host>:8080/v1`. All endpoints except `/health` and `/auth/token` require `Authorization: Bearer <token>`.

### Public

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/health` | Returns `{"status":"ok"}` |
| `POST` | `/v1/auth/register` | Register a new account |
| `POST` | `/v1/auth/login` | Log in and receive a bearer token |

**POST `/v1/auth/register`** body:

```json
{ "email": "you@example.com", "name": "Your Name", "password": "...", "invite_token": "<raw invite token>" }
```

`invite_token` is required for new accounts — obtain one from an admin with `relay invite create`. Password must be at least 8 characters. Returns `201 Created`:

```json
{ "token": "<hex>", "expires_at": "2026-07-16T00:00:00Z" }
```

**POST `/v1/auth/login`** body:

```json
{ "email": "you@example.com", "password": "..." }
```

Returns `201 Created`:

```json
{ "token": "<hex>", "expires_at": "2026-07-16T00:00:00Z" }
```

Tokens are valid for 30 days.

### Jobs

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/jobs` | Submit a job |
| `GET` | `/v1/jobs` | List jobs (`?status=` and `?scheduled_job_id=` filters optional) |
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
| `DELETE` | `/v1/workers/{id}/token` | Revoke agent long-lived token (admin only) |
| `GET` | `/v1/workers/{id}/workspaces` | List source workspaces on the worker (admin only) |
| `POST` | `/v1/workers/{id}/workspaces/{short_id}/evict` | Request eviction of a workspace (admin only); returns 202 even if the worker is offline |

### Reservations

All reservation endpoints are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/reservations` | List reservations |
| `POST` | `/v1/reservations` | Create a reservation |
| `DELETE` | `/v1/reservations/{id}` | Delete a reservation |

### Agent Enrollments

All agent-enrollment endpoints are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/agent-enrollments` | Create a one-time enrollment token |
| `GET` | `/v1/agent-enrollments` | List active (unexpired, unconsumed) enrollments |

**POST `/v1/agent-enrollments`** body:

```json
{ "hostname_hint": "worker-01", "ttl": "24h" }
```

Both fields are optional (`ttl` defaults to `24h`). Returns the raw token once:

```json
{ "id": "<uuid>", "token": "<raw token>", "expires_at": "..." }
```

Set the token as `RELAY_AGENT_ENROLLMENT_TOKEN` when starting a new agent. The token is consumed on first use and cannot be retrieved again.

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

### Scheduled Jobs

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/scheduled-jobs` | Create a scheduled job |
| `GET` | `/v1/scheduled-jobs` | List scheduled jobs (own schedules; admins see all) |
| `GET` | `/v1/scheduled-jobs/{id}` | Get a scheduled job |
| `PATCH` | `/v1/scheduled-jobs/{id}` | Update a scheduled job |
| `DELETE` | `/v1/scheduled-jobs/{id}` | Delete a scheduled job |
| `POST` | `/v1/scheduled-jobs/{id}/run-now` | Fire the schedule immediately (admin only) |

**POST `/v1/scheduled-jobs`** body:

```json
{
  "name": "nightly-render",
  "cron_expr": "0 2 * * *",
  "timezone": "America/Los_Angeles",
  "overlap_policy": "skip",
  "job_spec": { ... }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Human-readable schedule name |
| `cron_expr` | Yes | 5-field cron expression, or `@hourly`/`@daily`/`@every <duration>` |
| `timezone` | No | IANA timezone (default `UTC`) |
| `overlap_policy` | No | `skip` (default) or `allow` |
| `job_spec` | Yes | Job definition — same structure as `POST /v1/jobs` body |

**PATCH `/v1/scheduled-jobs/{id}`** — all fields optional, only supplied fields are updated:

```json
{
  "cron_expr": "0 4 * * *",
  "timezone": "UTC",
  "overlap_policy": "allow",
  "enabled": false
}
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

> Integration tests use [testcontainers-go](https://golang.testcontainers.org/) to spin up real PostgreSQL and p4d containers per test. Docker Desktop must be running, and the `p4` CLI must be on PATH (the Perforce test fixture shells out to it). The `-p 1` flag is required on Windows to prevent container provider conflicts when multiple packages run in parallel.

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
  schedrunner/     Scheduled job polling loop and startup reconciliation
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
- GPU detection covers NVIDIA only (via `nvidia-smi`).
- No structured logging in relay-agent (errors go to stderr as plain text).

---

## Transport Security

Relay's HTTP server does not handle TLS directly. When passwords are in use, deploy Relay behind a TLS-terminating reverse proxy to protect credentials in transit.

**Example — Caddy (`Caddyfile`):**

```
relay.internal {
    reverse_proxy localhost:8080
}
```

Caddy automatically provisions a certificate from your internal CA or Let's Encrypt. No changes to Relay's configuration are needed.

**Example — nginx (`/etc/nginx/conf.d/relay.conf`):**

```
server {
    listen 443 ssl;
    server_name relay.internal;
    ssl_certificate     /etc/ssl/certs/relay.crt;
    ssl_certificate_key /etc/ssl/private/relay.key;
    location / {
        proxy_pass http://127.0.0.1:8080;
    }
}
```
