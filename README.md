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
| `RELAY_TELEMETRY_WINDOW` | `30m` | Retention window for the in-memory worker utilization ring buffer |
| `RELAY_TELEMETRY_STALE_AFTER` | `30s` | A connected worker with no telemetry received for longer than this is marked `stale`. Should be greater than `RELAY_TELEMETRY_INTERVAL`. |
| `RELAY_CORS_ORIGINS` | _(empty)_ | Comma-separated CORS allowlist for HTTP API (empty = same-origin only, wildcard `*` rejected) |
| `RELAY_LOGIN_RATE_LIMIT` | `10:1m` | Per-IP rate limit for `POST /v1/auth/login` (format `N:duration`) |
| `RELAY_REGISTER_RATE_LIMIT` | `5:1m` | Per-IP rate limit for `POST /v1/auth/register` |
| `RELAY_ALLOW_SELF_REGISTER` | _(unset)_ | When `true`, `POST /v1/auth/register` accepts requests without an `invite_token` and creates a non-admin user directly. Default off; requires server restart to change. |

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

**Disable vs revoke.** *Disabling* a worker (`relay workers disable`) takes it
out of the scheduler's rotation while keeping its token and connection, so it
can be re-enabled instantly with `relay workers enable`. *Revoking* a worker
(`relay workers revoke`) destroys its agent token and forces a fresh enrollment
before it can rejoin. The two are independent: a worker can be both disabled and
revoked, and re-enrollment clears the revoked state but leaves a disabled worker
disabled.

### Environment variables

| Variable | Description |
|----------|-------------|
| `RELAY_AGENT_ENROLLMENT_TOKEN` | One-time enrollment credential issued by an admin (`relay agent enroll`). Required on first boot when no `token` file exists. Cleared from process env immediately after capture. |
| `RELAY_TELEMETRY_INTERVAL` | How often the agent samples host CPU/memory/GPU utilization and reports it to the server. Default `10s`. |
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

`relay login` authenticates existing accounts only. To create a new account, use `relay register` (below).

---

#### `relay register`

Create a new account interactively. The CLI prompts for server URL, email, optional display name, invite token, and password, then saves the resulting bearer token to your config file.

```sh
relay register
```

Use this for first-time non-admin sign-up. Existing accounts should use `relay login`. If `RELAY_ALLOW_SELF_REGISTER=true` on the server, the invite-token prompt may be left blank; otherwise an invite from an admin (`relay invite create`) is required.

---

#### `relay logout`

Revoke the bearer token saved in your config file and clear it locally.

```sh
relay logout         # revoke just this session
relay logout --all   # revoke every active session for your account
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

#### `relay profile update`

Update your own display name.

```sh
relay profile update --name "Your Name"
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
relay list --limit 10          # first 10 jobs
relay list --json              # JSON output
relay list --sort -priority    # group by priority label (desc; text sort)
relay list --sort name         # alphabetical
```

Statuses: `pending`, `running`, `done`, `failed`, `cancelled`

The `--sort` flag against a pre-feature server silently falls back to the default ordering - old servers ignore unknown query parameters.

---

#### `relay get`

Get full details for a job, including all tasks.

```sh
relay get <job-id>
relay get <job-id> --json
```

---

#### `relay cancel`

Cancel a job. Pending and queued tasks are marked failed immediately. For tasks already running on an agent, the agent terminates the entire subprocess tree (the direct child and any descendants), then runs workspace cleanup before reporting the task as failed. A brief pipe-drain budget (5 s) gives the subprocess one last chance to flush stdout/stderr to the relay log.

Use `--force` to skip pipe drain and workspace cleanup so the agent is freed as quickly as possible. This is the right choice when a task is genuinely stuck or you don't care about the last few KB of log output. Forced cancel may leave a Perforce workspace in a partial state; the next sync on that worker treats it as a cold target.

```sh
relay cancel <job-id>           # tree-kill, drain logs, cleanup workspace
relay cancel <job-id> --force   # tree-kill, skip drain, skip cleanup
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

> **Note:** order changed to `created_at DESC` (previously alphabetical by name).

```sh
relay workers list
relay workers list --limit 10
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

#### `relay workers disable <id-or-hostname> [--requeue]`

Disable a worker (admin only). A disabled worker keeps its agent token and gRPC
connection but receives no new task dispatches. By default running tasks are
left to finish (drain); pass `--requeue` to requeue the worker's active tasks
immediately and cancel their subprocesses on the agent. The positional argument
may be a worker UUID or a hostname.

```sh
relay workers disable <worker-id>
relay workers disable <hostname>
relay workers disable <worker-id> --requeue
```

---

#### `relay workers enable <id-or-hostname>`

Re-enable a disabled worker (admin only). Takes effect immediately. The
positional argument may be a worker UUID or a hostname.

```sh
relay workers enable <worker-id>
relay workers enable <hostname>
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

### Admin commands

The `relay admin` subcommand group bundles operations that require an admin token.

#### `relay admin users list`

> **Note:** order changed to `created_at DESC` (previously `created_at ASC`). Output includes a `Total: N` header line.

List every user in the system.

```sh
relay admin users list
relay admin users list --include-archived
relay admin users list --limit 25
```

Output columns: `ID`, `EMAIL`, `NAME`, `ADMIN`, `CREATED`. Pass `--include-archived` to include archived users in the output. Pass `--limit N` to control page size (default 50, max 200).

---

#### `relay admin users get`

Look up a single user by email.

```sh
relay admin users get user@example.com
```

---

#### `relay admin users create`

Create a user account directly, bypassing the invite flow. The password is read from a prompt.

```sh
relay admin users create --email user@example.com --name "Some User"
relay admin users create --email admin@example.com --admin
```

| Flag | Required | Description |
|------|----------|-------------|
| `--email` | Yes | Email address |
| `--name` | No | Display name (defaults to email) |
| `--admin` | No | Create the user as an admin |

---

#### `relay admin users update`

Update a user's display name. The positional argument may be either an email or a UUID.

```sh
relay admin users update user@example.com --name "New Name"
```

---

#### `relay admin users archive`

Soft-delete a user (admin only). The user can no longer log in and all of their API tokens are revoked. The account record is retained and can be restored with `relay admin users unarchive`. The positional argument may be either an email or a UUID.

```sh
relay admin users archive user@example.com
```

---

#### `relay admin users unarchive`

Restore an archived user (admin only). The account is re-activated but previously revoked tokens are not restored — the user will need to log in again. The positional argument may be either an email or a UUID.

```sh
relay admin users unarchive user@example.com
```

---

#### `relay admin passwd`

Reset another user's password (admin only). Prompts for the new password twice. **All of the target user's sessions are revoked** — they will need to log in again.

```sh
relay admin passwd user@example.com
```

---

## MCP integration

Relay ships an [MCP](https://modelcontextprotocol.io) server as the `relay mcp` subcommand. Connecting your MCP client (Claude Desktop, Claude Code, etc.) gives the model a curated set of tools for managing your relay deployment as the user you logged in with via `relay login`.

### Prerequisites

Run `relay login` once. The MCP server reads the saved bearer token from `~/.relay/config.json` (Linux/macOS) or `%APPDATA%\relay\config.json` (Windows). Environment overrides `RELAY_URL` / `RELAY_TOKEN` are honored.

### Configure your client

Add an entry to your MCP client's config file. For Claude Desktop on Windows the file is `%APPDATA%\Claude\claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "relay": {
      "command": "relay",
      "args": ["mcp"]
    }
  }
}
```

For Claude Code, add it via `claude mcp add relay -- relay mcp` or by editing `~/.claude.json` directly. Restart the client. The relay tools (prefixed `relay_*`) and resources (`relay://server-info`, `relay://recent-jobs`) become available.

### Tools (v1)

Read tools (any logged-in user):

| Tool | Purpose |
|---|---|
| `relay_whoami` | Identity of the calling user. |
| `relay_list_jobs` | Cursor-paginated list of jobs. |
| `relay_get_job` | Fetch one job. |
| `relay_list_tasks` | Tasks for a job. |
| `relay_get_task` | Fetch one task. |
| `relay_get_task_logs` | Page of log lines (`since_seq`/`limit`). |
| `relay_list_workers` / `relay_get_worker` | Worker inventory. |
| `relay_list_schedules` / `relay_get_schedule` | Scheduled jobs. |
| `relay_list_reservations` | Worker reservations (admin-only). |

Write tools (any logged-in user):

| Tool | Purpose |
|---|---|
| `relay_submit_job` | Submit a job from an inline `job_spec`. |
| `relay_cancel_job` | Cancel a job (no force in v1). |
| `relay_wait_for_job` | Block until terminal or timeout (default 60s, max 300s). |
| `relay_create_schedule` / `relay_update_schedule` / `relay_delete_schedule` | Schedule CRUD. |
| `relay_run_schedule_now` | Fire a schedule immediately (admin-only). |

Calls that map to admin-only endpoints return a `forbidden` error when invoked by a non-admin token.

The four list tools (`relay_list_jobs`, `relay_list_workers`, `relay_list_schedules`, `relay_list_reservations`) accept an optional `sort` parameter; see [Configurable sort order](#configurable-sort-order) for the per-endpoint allowlist.

### Deferred to a later release

Worker mutations (revoke token, evict workspace), agent enrollment, invite creation, all user mutations (create/update/archive/passwd), force-cancel, password reset, and reservation create/delete. Multi-user remote MCP (HTTP transport) is also out of scope for v1.

---

## REST API

The server exposes a REST API at `http://<host>:8080/v1`. All endpoints except `/health`, `/auth/login`, and `/auth/register` require `Authorization: Bearer <token>`.

### Pagination

List endpoints that can return large result sets support cursor-based pagination via two query parameters:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `limit` | 50 | Rows per page. Range: 1–200. Out-of-range → 400. |
| `cursor` | _(none)_ | Opaque cursor from the previous page's `next_cursor` field. Absent on the first request. |

All paginated endpoints return this envelope:

```json
{
  "items": [ ... ],
  "next_cursor": "eyJ0IjoiMjAyNi0wNC0xNlQxMDowMDowMFoiLCJpIjoiYWJjZCJ9",
  "total": 274
}
```

- `items` — the rows for this page (up to `limit` rows)
- `next_cursor` — opaque base64 token for the next page; empty string `""` means this is the last page
- `total` — server-side count of all matching rows (consistent across pages)

**Clients must treat `next_cursor` as opaque.** Its format is server-internal and may change without notice.

Paginated endpoints sort by `created_at DESC, id DESC`.

**Ordering notes:**
- `GET /v1/workers` changed from alphabetical-by-name to `created_at DESC, id DESC`.
- `GET /v1/users` changed from `created_at ASC` to `created_at DESC`.

#### Configurable sort order

Each list endpoint accepts an optional `?sort=<key>` query parameter to override the default ordering. Prefix the key with `-` for descending order; absent dash means ascending. Absent `?sort=` keeps the default `created_at DESC, id DESC`.

| Endpoint | Default | Allowed keys |
|----------|---------|--------------|
| `GET /v1/jobs` | `-created_at` | `created_at`, `name`, `priority`, `status`, `updated_at` |
| `GET /v1/workers` | `-created_at` | `created_at`, `name`, `status`, `last_seen_at` |
| `GET /v1/users` | `-created_at` | `created_at`, `name`, `email` |
| `GET /v1/scheduled-jobs` | `-created_at` | `created_at`, `name`, `next_run_at`, `updated_at` |
| `GET /v1/reservations` | `-created_at` | `created_at`, `name`, `starts_at`, `ends_at` |
| `GET /v1/agent-enrollments` | `-created_at` | `created_at`, `expires_at` |

Each key supports both directions, e.g. `?sort=name` (ascending) and `?sort=-name` (descending).

**Examples:**

```
GET /v1/jobs?sort=-priority           # group by priority label (desc; text sort)
GET /v1/workers?sort=name             # alphabetical
GET /v1/jobs?sort=status&limit=10     # group by status, smaller pages
```

**Cursor semantics:** A cursor is valid only for the sort it was issued under. Resending a cursor with a different `?sort=` returns `400 cursor sort key does not match requested sort`. Drop the cursor when changing sort.

**Filter + sort:** `GET /v1/jobs` rejects `?sort=` combined with `?status=` or `?scheduled_job_id=` with `400 sort not supported on filtered list variant`. Other endpoints' filters do not currently combine with sort.

**Unknown keys:** `?sort=<key>` where `<key>` is not in the allowlist returns `400 unsupported sort key '<key>'; supported: <list>`.

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

### Session

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/v1/users/me/password` | Change own password (body: `current_password`, `new_password`) |
| `DELETE` | `/v1/auth/token` | Revoke the bearer token used on this request |
| `DELETE` | `/v1/auth/tokens` | Revoke every active bearer token for the calling user |

### Users

All user-management endpoints other than `PATCH /v1/users/me` are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/users` | List users (`?email=` filter for exact-match lookup). Optional `?include_archived=true` includes archived users. Paginated. |
| `POST` | `/v1/users` | Create a user (body: `email`, `password`, optional `name`, optional `is_admin`) |
| `POST` | `/v1/users/password-reset` | Reset a user's password (body: `email`, `new_password`); revokes all of their sessions |
| `PATCH` | `/v1/users/me` | Update own profile (body: `name`) |
| `PATCH` | `/v1/users/{id}` | Update a user (body: `name`) |
| `POST` | `/v1/users/{id}/archive` | Archive (soft-delete) a user. Revokes all of their API tokens. |
| `POST` | `/v1/users/{id}/unarchive` | Restore an archived user. Old tokens stay revoked. |

### Jobs

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/jobs` | Submit a job |
| `GET` | `/v1/jobs` | List jobs (`?status=` and `?scheduled_job_id=` filters optional). Paginated. |
| `GET` | `/v1/jobs/{id}` | Get a job |
| `DELETE` | `/v1/jobs/{id}` | Cancel a job (`?force=true` for forced termination, skips pipe drain and workspace cleanup) |
| `GET` | `/v1/jobs/{id}/tasks` | List tasks for a job |

### Tasks

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/tasks/{id}` | Get a task |
| `GET` | `/v1/tasks/{id}/logs` | Get task log entries |

### Workers

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/workers` | List workers. Paginated. Order: created_at DESC (changed from name ASC). |
| `GET` | `/v1/workers/{id}` | Get a worker |
| `PATCH` | `/v1/workers/{id}` | Update name, labels, or max_slots (admin only) |
| `DELETE` | `/v1/workers/{id}/token` | Revoke agent long-lived token (admin only) |
| `POST` | `/v1/workers/{id}/disable` | Stop the scheduler from dispatching new tasks to a worker (admin only); its token and connection are kept. `?requeue=true` also requeues and cancels the worker's active tasks; the default leaves running tasks to finish. |
| `POST` | `/v1/workers/{id}/enable` | Re-enable a disabled worker (admin only). |
| `GET` | `/v1/workers/{id}/workspaces` | List source workspaces on the worker (admin only) |
| `POST` | `/v1/workers/{id}/workspaces/{short_id}/evict` | Request eviction of a workspace (admin only); returns 202 even if the worker is offline |
| `GET` | `/v1/workers/{id}/metrics` | Get the worker's short-term utilization history (CPU, memory, GPU). Returns an empty `samples` array for offline workers or workers with no data yet. 404 if the worker does not exist. Same bearer-auth as `GET /v1/workers/{id}`. |

### Reservations

All reservation endpoints are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/reservations` | List reservations. Paginated. |
| `POST` | `/v1/reservations` | Create a reservation |
| `DELETE` | `/v1/reservations/{id}` | Delete a reservation |

### Agent Enrollments

All agent-enrollment endpoints are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/agent-enrollments` | Create a one-time enrollment token |
| `GET` | `/v1/agent-enrollments` | List active (unexpired, unconsumed) enrollments. Paginated. |

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
| `GET` | `/v1/scheduled-jobs` | List scheduled jobs (own schedules; admins see all). Paginated. |
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
