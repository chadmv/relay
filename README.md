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

On a trusted private network you can instead run the server with `RELAY_ALLOW_AUTO_ENROLL=true` and start the agent with no token at all - skip the `relay agent enroll` step entirely. The agent receives and persists a long-lived token on its first connection, exactly as with token enrollment. **This works for a hostname that has no existing `workers` row.** Auto-enrollment creates workers and never claims them, so a machine being re-provisioned in place - or one that lost its state directory but kept its hostname - is refused. **Revoking alone does not fix that**, and this is the trap worth knowing before you hit it: `relay workers revoke <id>` nulls the credential but keeps the row, so the hostname stays claimed and the token-less agent gets the identical refusal on every restart. The two routes are **sequential, not alternative**: revoke the worker **and then** enroll the agent with an admin-issued enrollment token (`relay agent enroll`), which a revoked worker accepts and which reuses the existing worker with its history intact. The other escape hatch is to **rename the host** - identity is keyed by hostname, so a renamed machine rejoins as a new worker. The third route **frees the claimed hostname outright**: `relay workers delete --yes <id-or-hostname>` removes the worker row, which is the only thing that unclaims a hostname. It is admin-only, destructive, and has no undo - it requeues the worker's assigned tasks, scrubs its id out of every reservation naming it, and refuses while the worker is still connected - so it is the third remedy, not the first. A machine re-provisioned in place under auto-enroll now has a remedy on the token-less path; note that all three remedies need an admin, so an operator who cannot get `relay agent enroll` run for them cannot get `relay workers delete` run for them either.

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
| `RELAY_PUBLIC_URL` | _(empty)_ | Browser-facing base URL of the relay web UI, e.g. `https://relay.example.com`, or `https://ops.example.com/relay` behind a reverse proxy that strips a path prefix. The coordinator renders `RELAY_JOB_URL` and `RELAY_TASK_URL` from it into every task subprocess (see **relay-agent -> Task subprocess environment**). Unset means those two variables are simply absent - relay never guesses an origin, and `RELAY_JOB_ID` / `RELAY_TASK_ID` are injected either way. **An invalid value refuses to boot** rather than warning and disabling: a warn-and-disable typo would be indistinguishable from never having set the variable, and there is no defensible default origin to fall back to. Rejected: any scheme other than `http`/`https`, a missing host (`https://:8080` included - a port is not a host), a non-ASCII host (supply the punycode `xn--` form; the characters browsers fold to `.` before resolving a name make `https://relay.example.com<ideographic full stop>evil.com` read as relay's host), a port outside 1-65535, an authority ending in a bare `:`, userinfo (`https://user:pass@host`), a query string, a fragment, and any embedded whitespace or control character. Surrounding whitespace and trailing slashes are trimmed. The effective value is printed at startup on every boot, which is what an operator has instead of a validator for a value that parses perfectly and names the wrong host. A path prefix here is a string, not a route rewrite: relay serves its SPA from its own routes, so `https://ops.example.com/relay` produces working links only if your proxy actually strips `/relay`. |
| `RELAY_BOOTSTRAP_ADMIN` | _(empty)_ | Email address — creates or promotes this user to admin on startup when no admin exists. Cleared from process env after consumption. |
| `RELAY_BOOTSTRAP_PASSWORD` | _(empty)_ | Required when `RELAY_BOOTSTRAP_ADMIN` is set. Cleared from process env after consumption; operators should also unset it from their shell. |
| `RELAY_DB_MAX_CONNS` | `25` | Maximum PostgreSQL connection pool size |
| `RELAY_WORKER_GRACE_WINDOW` | `2m` | How long to wait before requeueing tasks from a disconnected agent |
| `RELAY_TASKLOG_TRAILING_WINDOW` | `15m` | How long after a task finishes its assigned agent may still append log chunks for it. A *single* agent reconnect attempt is bounded at roughly 105s by three timers (subprocess wait delay, gRPC keepalive, reconnect backoff cap), but the agent retries indefinitely and its send buffer is shared across reconnects, so a chunk buffered across an outage longer than this window is dropped by design - the window is a deliberate trade, not a derived bound. **Set it too small and real trailing output is silently truncated** - a rejected chunk is dropped with no error to the agent and no line in the server log, exactly like a stale-epoch chunk - **the one runtime signal is `task_log_fence.counts.rejected_total` on `GET /v1/server/counters`**, which climbs steadily when this window is too small. An unparseable, zero or negative value keeps the default and logs one warning at startup; so does a positive value under `2m`, which is kept but almost certainly a units mistake. Setting a very large value (`8760h`) restores the pre-`15m` behaviour, in which the window never closes - that is the hole this knob exists to bound: anything holding a worker's agent token could append rows to a task that worker finished for as long as the row existed, and nothing prunes `task_logs`. |
| `RELAY_TASK_WATCHDOG_MARGIN` | `30m` | How long past a task's own `timeout_sec` the coordinator waits before declaring it `timed_out` itself. `timeout_sec` is otherwise enforced only by the agent, so this is what bounds a wedged or uncooperative one. The margin must absorb the whole gap between the agent's deadline firing and the coordinator seeing the terminal update (subprocess kill, proctree cleanup, final log flush, and a gRPC reconnect if the stream dropped - roughly 105s in the worst measured case), which is why the default is generous. **Set it too small and healthy work is killed with no way for the agent to object**, and the task is not retried automatically. `0` disables this arm entirely; write `1s` if you meant "no margin" rather than "no bound". Applies only to tasks with `timeout_sec > 0` that have reported `running`; `started_at` is stamped once per assignment and cannot be reset by any later status report, so this bound is not something a wedged or uncooperative agent can extend. |
| `RELAY_TASK_MAX_ASSIGNMENT` | `24h` | Absolute cap on how long a task may stay assigned to a worker, measured from dispatch. This is the arm that bounds `timeout_sec = 0` tasks (documented as "no deadline") and tasks that never report `running` at all - a task spends its entire workspace sync in `preparing`, and a P4 sync on a 1 TB+ workspace can legitimately run for hours, so the default must exceed the longest honest assignment. **Too small kills healthy long-running work silently.** `0` disables this arm. Setting both watchdog variables to `0` disables the watchdog completely, restoring the behaviour in which a connected agent could hold a task, its worker slot and its job forever - but note that disabling **only this arm** already restores indefinite hold for a large class of tasks, because the execution arm applies solely to tasks with `timeout_sec > 0` that have reported `running`. A `timeout_sec = 0` task, or one still syncing its workspace (in `preparing`), has no other bound. A swept task is marked `timed_out` and is **not** retried automatically - the retry budget is consumed only by agent-reported failures - so recovery is `POST /v1/jobs/{id}/retry`. |
| `RELAY_GRPC_MAX_CONNS` | `1024` | Maximum concurrent agent gRPC connections, all sources combined. Refused connections are accepted and immediately closed at the listener, before the HTTP/2 handshake, any goroutine or any database work; the agent sees `Unavailable` and reconnects with backoff. This is what turns every per-connection bound in the server into a fleet-wide number - at the default, the worst-case caller-driven log burst is 1024 x the per-connection budget. `0` disables the cap, leaving the process file-descriptor limit as the only ceiling. Note the tradeoff a cap introduces: a full budget is a denial ceiling, which is why the per-source cap below exists so that one source cannot fill it alone. These connections share the process file-descriptor table with the HTTP listener and the pgx pool, so this value should stay well below the container's hard `RLIMIT_NOFILE`; 1024 is the classic *soft* default, and what keeps the default safe is that Go raises the soft limit to the hard one at startup. Setting `1` is accepted but warned about - see the per-source row. If you front `:9090` with a proxy that already caps connections, set the caps there and `0` here **and** `RELAY_GRPC_MAX_CONNS_PER_IP=0`, which then also stops relay wrapping the accepted conn at all. Both caps have to be off for that: with either one live the accounting wrapper is what releases the slot, so it stays, and `RELAY_GRPC_MAX_CONNS_PER_IP` defaults to `64` rather than to off. |
| `RELAY_GRPC_MAX_CONNS_PER_IP` | `64` | Maximum concurrent agent gRPC connections from any one source, keyed on the TCP source address - `X-Forwarded-For` is not trusted and there is no proxy assumption. **IPv4 is keyed on the exact address; IPv6 is aggregated to the /64 prefix.** That asymmetry is deliberate and is not the same rule `RELAY_LOGIN_RATE_LIMIT` uses. The smallest IPv6 delegation anybody receives is a /64 and every address in it is free to its holder, so a per-address key would let one host present a thousand distinct "sources" and make this cap unreachable - not weaker, absent - while the refusal summary blamed fleet growth. IPv4 addresses are scarce and already shared through NAT, so aggregating them would collapse unrelated operators onto one bucket instead. **What aggregation buys, and where it stops:** it raises the bar from "one host, one address" to "one host per /64 the attacker holds", and no further. At the defaults each distinct /64 buys 64 slots, 6.25% of `RELAY_GRPC_MAX_CONNS`, so **16 distinct /64s fill the 1024 fleet cap**. Providers routinely delegate a /56 (256 /64s) or a /48 (65536), and cheap VPS estates hand out a /64 per instance, so a larger delegation escapes this cap in proportion to its size. The per-source cap is a bar, not a wall; what bounds the absolute worst case is `RELAY_GRPC_MAX_CONNS`, and what makes that survivable is that a refused peer is closed before any handshake, goroutine or query. **NAT hazard:** a site running more than this many agents behind one NAT gateway, or out of one IPv6 /64, will see agents refused and reconnect-backoff indefinitely; the symptom is agents that never come online while the server logs a once-a-minute refusal summary, and the fix is to raise or disable this value. `GET /v1/server/counters` is what tells that case apart from an attack: a NAT gateway shows few `distinct_sources` each holding many connections, a distributed source pattern shows many sources holding one each, and sixteen `/64`s each at exactly 64 shows the delegation escape described above with nothing refused yet. **Do not set it to `1`** - a reconnecting agent's new connection can arrive before the server has observed the close of its old one, so the agent can be refused its own reconnect. `1` is accepted and used, with a warning at startup. `0` disables. |
| `RELAY_GRPC_MAX_CONN_IDLE` | `60s` | How long an agent gRPC connection that is holding **no stream** may stay open before the server closes it. It can never terminate a connection that is doing its job - a connection holding a stream is not idle no matter how long or how quietly it lives - so a healthy agent is unaffected, and this is **not** a maximum connection age. **Read this value as the attacker's duty cycle, not as a tolerance.** A peer parking connection slots has to re-establish each one once per window and pays nothing else - no credential, no stream, no database work - so at `15m` (the previous default) holding all 1024 slots cost about 1.14 new TCP connections per second, sustained, and a peer that completed the handshake parked a slot 7.5x *longer* than one that said nothing at all. At `60s` a peer that opens **no stream** holds a slot for 60s, about 17 new connections per second to hold all 1024. Raising it multiplies the hold per handshake one for one. A legitimate agent's real window - dial to first stream - is sub-millisecond, so `60s` still leaves four orders of magnitude of headroom. Note what it does **not** cover: a peer that completes TCP and says nothing never reaches transport construction and is bounded by grpc-go's own 120s connection timeout; and a peer that opens a *stream* and then says nothing is not idle by this definition at all, and is bounded by `RELAY_GRPC_REGISTRATION_TIMEOUT` below. **These two compose - the cheapest hold on this port is the sum, not either one.** The registration deadline ends the *stream*, which hands the connection back to this reaper, so a stream-opening peer holds its slot for `RELAY_GRPC_REGISTRATION_TIMEOUT + RELAY_GRPC_MAX_CONN_IDLE`: **90s at the defaults, about 11.4 new connections per second to hold all 1024** - cheaper than the 17/s above and cheaper than grpc-go's 120s for a peer that says nothing at all, which is the figure both numbers have to stay under. The server warns at startup if your two values sum past 120s. A value under `1s` is kept but warned about. `0` disables reaping. |
| `RELAY_GRPC_REGISTRATION_TIMEOUT` | `30s` | How long a peer that has opened a gRPC stream may go without sending its `RegisterRequest` before the server closes the stream. This is the bound on connection *parking*: such a peer never authenticates, so it costs no credential and no database round trip, and it is invisible to `RELAY_GRPC_MAX_CONN_IDLE` because holding a stream is precisely what makes a connection not idle. Without it, a handful of source prefixes can fill `RELAY_GRPC_MAX_CONNS` permanently and every real agent is refused from then on. **This is the one admission knob that cannot be disabled**, because no proxy can enforce an application-layer "send a RegisterRequest within N" on the server's behalf; `0`, a negative value or a typo keeps the default and warns. Closing the stream hands the connection back to `RELAY_GRPC_MAX_CONN_IDLE`, so a peer willing to open a fresh stream once per idle window still holds its slot - now for the price of a periodic round trip rather than for free. Fully closing that needs a maximum connection age, which relay does not set. **This value ADDS to `RELAY_GRPC_MAX_CONN_IDLE`, it does not overlap with it:** a peer that opens a stream and never registers holds its connection slot for the sum of the two, 90s at the defaults. Raising this for a "slow fleet" therefore buys a cheaper parking primitive as well as a longer grace period - at `2m` the composite is 180s, past the 120s grpc-go allows a peer that says nothing at all, which makes opening a stream the *cheapest* way to hold a slot on the port. The server logs a warning at startup when the two values sum past 120s. A legitimate agent sends its `RegisterRequest` immediately after opening the stream, so the honest window is one network round trip; a value under `1s` is kept but warned about, and a very large value restores the old unbounded behaviour. |
| `RELAY_TELEMETRY_WINDOW` | `30m` | Retention window for the in-memory worker utilization ring buffer |
| `RELAY_TELEMETRY_STALE_AFTER` | `30s` | A connected worker with no telemetry received for longer than this is marked `stale`. Should be greater than `RELAY_TELEMETRY_INTERVAL`. |
| `RELAY_CORS_ORIGINS` | _(empty)_ | Comma-separated CORS allowlist for HTTP API (empty = same-origin only, wildcard `*` rejected) |
| `RELAY_LOGIN_RATE_LIMIT` | `10:1m` | Per-IP rate limit for `POST /v1/auth/login` (format `N:duration`) |
| `RELAY_REGISTER_RATE_LIMIT` | `5:1m` | Per-IP rate limit for `POST /v1/auth/register` |
| `RELAY_ALLOW_SELF_REGISTER` | _(unset)_ | When `true`, `POST /v1/auth/register` accepts requests without an `invite_token` and creates a non-admin user directly. Default off; requires server restart to change. |
| `RELAY_ALLOW_AUTO_ENROLL` | `false` | When `true`, agents may register with no enrollment token (token-less auto-enrollment). Intended only for trusted private networks where any host able to reach gRPC is trusted. A long-lived agent token is still issued on join and used for all later reconnects. An existing worker row is never touched at all: a hostname that already has one is refused, whatever that worker's status and whatever its token, so "revoked workers are not revived" is the special case rather than the rule. |
| `RELAY_AUTO_ENROLL_WORKER_CEILING` | `1024` | Refuses token-less auto-enrollment once this many **non-revoked** workers exist. `0` disables the bound; a negative or unparseable value warns and keeps the default. It counts ALL non-revoked workers, not only auto-enrolled ones, because nothing in the schema records which path created a row. The bound is approximate - see the auto-enrollment cost note below. Requires a server restart to change. |

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
3. Start the gRPC server (agent connections), with one stream per connection, the `RELAY_GRPC_MAX_CONNS` / `RELAY_GRPC_MAX_CONNS_PER_IP` admission caps applied at the listener, and a deadline on the first `RegisterRequest`. All four effective bounds are printed unconditionally at startup.
4. Start the task dispatch scheduler, the Postgres LISTEN/NOTIFY trigger, and the stale-task watchdog (which ends assignments that blow `RELAY_TASK_WATCHDOG_MARGIN` or `RELAY_TASK_MAX_ASSIGNMENT`)
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

On first boot the agent requires a one-time enrollment token. After successful enrollment the long-lived token is persisted and used automatically on subsequent starts. If the token is revoked by an admin, the agent exits with an authentication error **the next time it connects**. Revocation does not reach a connection that is already established: nothing re-checks a credential after registration and revoking does not close the stream, so an already-connected agent keeps running the tasks it already holds and keeps writing their task logs and statuses until it disconnects for some other reason - both of those writes are fenced on the task's assignment epoch and its `worker_id`, never on the worker's status. **New dispatches stop at once**, though: revoking sets the worker's status to `revoked`, the dispatcher only selects `online` or `stale` workers, and nothing restores `online` while the stream is live. Relay sets no maximum connection age, so there is no timer that ends the connection either. To end it immediately, disable the worker and confirm it has gone offline. (Deleting the worker is **not** the way to end a live connection either. `DELETE /v1/workers/{id}` is permitted only while the row's status is `offline` or `revoked`, and **those two are not equivalent**: `offline` does mean disconnected, but `revoked` does **not** - as the sentences above say, revoking clears the credential and leaves the stream up. So delete can run against a worker that is still connected and still executing tasks. Because it requeues those tasks for somebody else, it therefore **sends the still-connected agent a cancel for each one**, exactly as `disable --requeue` does, so the subprocess is killed rather than left to duplicate the work; a cancel to an `offline` worker is a no-op. What delete does NOT do is end the connection - the agent's next reconnect fails authentication because its row is gone. When it runs, it does not do what an earlier revision of this paragraph claimed. `tasks.worker_id` is `ON DELETE SET NULL`, so a naive delete would orphan running tasks rather than destroy them; the handler therefore **requeues them first, in the same transaction**, which ends each assignment generation with an epoch bump while `worker_id` still names it. `reservations.worker_ids` is a bare `UUID[]` with no foreign key, so a naive delete would leave reservations naming a phantom; the handler **scrubs the id out** of every reservation naming it. And `agent_enrollments.consumed_by` has no `ON DELETE` action at all, so a naive delete would **fail outright** for any worker ever enrolled with a token; the handler **nulls that link** first, leaving `consumed_at` intact.)

When the server runs with `RELAY_ALLOW_AUTO_ENROLL=true`, an agent with no `token` file and no `RELAY_AGENT_ENROLLMENT_TOKEN` attempts token-less auto-enrollment instead of exiting. If the server does not allow it, the agent exits with an authentication error. It exits the same way, with the same error, in two further cases: the hostname it presents **already has a `workers` row**, and the fleet is **at `RELAY_AUTO_ENROLL_WORKER_CEILING`**. The server returns one opaque refusal for all three, so the agent's own exit log is what names them and prescribes the remedy.

**Disable vs revoke.** *Disabling* a worker (`relay workers disable`) takes it
out of the scheduler's rotation while keeping its token and connection, so it
can be re-enabled instantly with `relay workers enable`. *Revoking* a worker
(`relay workers revoke`) destroys its agent token and forces a fresh enrollment
before it can rejoin. The two are independent: a worker can be both disabled and
revoked, and re-enrollment clears the revoked state but leaves a disabled worker
disabled.

Token-less auto-enrollment is the exception to that rule: whereas a deliberate
token re-enrollment (with a fresh admin-issued enrollment token) clears the
revoked state, auto-enrollment under `RELAY_ALLOW_AUTO_ENROLL` does not revive a
revoked worker - it stays revoked until an admin clears or deletes it. There are
now three routes out, in the order to try them: revoke and then re-enroll with an
admin-issued token, which keeps the row and its history; rename the host, since
identity is keyed by hostname and a renamed machine rejoins as a new worker; or
`relay workers delete --yes <id-or-hostname>`, which removes the row and frees
the hostname for token-less auto-enroll. The third is destructive and cannot be
undone, which is why it is third.

An admin-issued enrollment token still revives a revoked worker, and that is the
recovery route this design points you at. What it no longer does is bind to a
worker whose credential is **live**: rotating a live agent's credential requires
a revoke first. The asymmetry between the two paths is deliberate - revoking
does not delete the row, so refusing every existing row on the enrollment-token
path too would leave no NON-DESTRUCTIVE way back: the enrollment-token path is
the only recovery that preserves the revoked worker's row and its history.
`relay workers delete` exists and would also unstick the hostname, but it
destroys the identity rather than reviving it, so it is not a substitute for the
route this asymmetry protects.

**What auto-enrollment costs, stated plainly.** Under `RELAY_ALLOW_AUTO_ENROLL=true`, any host able to
reach the gRPC port may create **one persistent `workers` row per distinct hostname it claims**, up to
`RELAY_AUTO_ENROLL_WORKER_CEILING`. The hostname is caller-supplied and not validated.

**Takeover is refused.** Auto-enrollment may CREATE a worker and may never CLAIM one: a hostname that
already has a `workers` row is refused, whatever that row's status and whatever its token, so a
token-less caller cannot overwrite an existing worker's agent token, lock its agent out, or inherit its
registry slot, assignments and reservations. The check and the write are a single
`INSERT ... ON CONFLICT (hostname) DO NOTHING`, so there is no window between them and two concurrent
first boots of the same fresh hostname cannot clobber each other either. (See
`docs/superpowers/specs/2026-08-25-auto-enroll-guards.md`.) Revoked workers are no longer a special
case - they are refused because a row exists, not because the row is revoked.

**The refusal discloses nothing beyond the refusal itself.** Every credential failure on the gRPC
registration surface returns the identical `Unauthenticated` status and the identical string, so a
caller cannot tell "this hostname is taken" from "this hostname is revoked" from "your token is
unknown". One oracle is inherent and is not closed: a caller learns a hostname is claimed because
claiming it fails, while an unclaimed one succeeds. Closing that would mean refusing everything.

**NON-REVOKED row growth is bounded; total row growth is not.** Those rows survive the connection that created them, survive a server
restart, and appear in every `GET /v1/workers` page and every dispatcher scan, so the total is capped:
token-less enrollment is refused once `RELAY_AUTO_ENROLL_WORKER_CEILING` (default `1024`, `0` disables)
non-revoked workers exist. **The bound is approximate and the arithmetic is stated rather than
implied:** two concurrent auto-enrolls at `ceiling - 1` both pass the check under read-committed
isolation and both insert, so the honest bound is `ceiling + RELAY_GRPC_MAX_CONNS`, not `ceiling`.
Making it exact would need serializable isolation or an advisory lock on a hot path, for an overshoot
that is a fraction of a percent. `RELAY_GRPC_MAX_CONNS_PER_IP` bounds how many such registrations one source address
can have *in flight at once*; it does not bound how many rows accumulate over time, because the rows
outlive their connections. Row growth is a bounded, recorded decision rather than an oversight: the
flag is off by default and its documented trust model is that any host able to reach gRPC is trusted. The
enrollment-token path does **not** have this property - the worker upsert and the single-use token
consume share one transaction, so one admin-issued token buys exactly one row. If you run auto-enrollment
on a network where that trust does not hold, do not; use enrollment tokens.

**Read the ceiling's promise narrowly.** It bounds the number of **non-revoked** worker rows, which
is what `CountWorkers` measures. It does **not** bound the number of rows in the table, and remedy 1
below is what makes that gap reachable: revoking a row keeps it, so under an active attacker the
operator revokes 1024 junk workers, the attacker creates 1024 more under **new** hostnames - the old
ones stay claimed forever - and the table grows without limit in the revoked bucket while the counted
total sits flat. `relay workers delete` reclaims both the row and the hostname, but **manually and one
row at a time**, so it does not change the shape of that treadmill - and note that deleting an
already-revoked row frees **zero** ceiling budget, because `CountWorkers` already excludes it.
**Nothing reclaims them automatically**: reaping is still not done. Bounding the table itself, and
reaping those rows, is not something this ceiling does or is trying to do; both are tracked separately.

**When the ceiling is reached, in the order to try things.**

0. **Find out whose hostnames they are.** Read the `auto-enrolled worker` lines in the server log:
   they carry the hostname (escaped and length-clipped) *and the remote address*, and they are the only
   attributable signal this system produces. The refusal counters say how many and why; only these say
   **who**. A successful token-less enrollment writes exactly one, permanently, because that hostname
   can never be auto-enrolled again - so the log is a complete list of what was created this way.
1. **Revoke the junk.** The ceiling counts non-revoked workers only, so `relay workers revoke <id>` on
   rows that do not correspond to real machines frees budget immediately, with no restart, and is
   non-destructive. Note what it does not do: the row and its
   hostname remain, so this frees **budget**, never a **hostname**, and it does not shrink the table.
   `relay workers delete` does free the hostname and does shrink the table, and it is deliberately
   **not** a step in this ladder: this ladder is what an operator does in response to
   `fleet_at_ceiling`, a signal an attacker can drive, and deleting 1024 rows under an active
   attacker is the same treadmill as revoking them, only irreversible. It is also worth being precise
   about what it frees - deleting an already-revoked row frees no budget at all, because this count
   already excludes it.
2. **Use enrollment tokens.** The ceiling gates the token-less path only. `relay agent enroll` is never
   refused by it, so machines can still be added with no downtime.
3. **Raise `RELAY_AUTO_ENROLL_WORKER_CEILING`.** This **requires a server restart** - the knob is not
   hot-reloadable. Agents reconnect on backoff and `RELAY_WORKER_GRACE_WINDOW` covers their running
   tasks, so it is a blip rather than data loss.

**Setting the ceiling to `0` is not step 4.** It is deliberately not in the ladder above, because a
climbing `fleet_at_ceiling` count is exactly the signal an attacker filling the budget produces, and
disabling the bound is exactly what that attacker wants - the same shape as the forgeable
`conflicting_total` signal documented under `task_status_fence`. Disable the ceiling only if you have
independently decided the auto-enroll trust model holds on this network, never as a response to
refusals you are still triaging.

An operator whose fleet genuinely exceeds 1024 machines should set this explicitly. The default is
derived from `RELAY_GRPC_MAX_CONNS`, and **the derivation is not airtight**: that knob bounds concurrent
*connections* while this one bounds total non-revoked *rows*, so a farm of 2000 intermittently-connected
machines with 800 online at a time stays under the connection cap and exceeds this ceiling legitimately.

**A first boot that never completes claims the hostname permanently.** `autoEnrollAndRegister` commits
the worker row *and* its freshly minted `agent_token_hash` before the `RegisterResponse` is sent and
before the agent writes the token to its state directory. If the stream dies in that window, or the
agent cannot persist the token (a read-only or full state directory on first boot), the hostname is
claimed with a live credential **the agent never received**. The retry is then refused by auto-enroll
(a row exists) *and* by the enrollment-token path (that row's credential is live), so the machine
cannot rejoin under that hostname until an admin revokes the worker and issues an enrollment token, or
deletes it. This is narrower than the lost-state-directory case and worth naming separately: that one
is a machine that ran successfully once, this one never registered at all. Before the create-only rule
the retry self-healed, because the upsert simply rotated the token; closing the takeover is what makes
this permanent.

**One server-side line is driveable by an unauthenticated caller, and it is not a refusal.** A store fault inside either enrollment transaction is logged (the peer gets an opaque `Internal`; the detail stays on the server). That is deliberate - a store fault is a server condition that must not be silent - but be clear about its bound, because it is weaker than the audit line's. The audit line fires only on success, so it is one line per hostname forever and is additionally capped by the ceiling. The fault line has no per-hostname bound: with auto-enroll on, a caller presenting no credential and a hostname over the ~2704-byte index limit fails deterministically and, since only `Unauthenticated` is terminal for an agent, reconnects on backoff. Neither line is covered by the per-connection ingest budget, which is not allocated until after registration. Bounding hostname length is tracked separately.

**Refusals are counted, never logged.** A refusal is unboundedly repeatable by the same caller with the
same hostname, and the per-connection log limiter is not allocated until *after* registration, so a log
line there would be an unbounded attacker-driven log site. The server keeps a cumulative count split by
cause - hostname already claimed, fleet at the ceiling, and enrollment token naming a live credential -
read through the coordinator's in-process accessor. **These are not yet published on**
`GET /v1/server/counters`; that section is filed as its own backlog item. **Diagnosing a refused agent
starts with the AGENT's log, not the server's**: the server deliberately does not name an
attacker-chosen hostname on a repeatable refusal, so the agent's own exit message - which names its own
hostname and all three causes - is the naming signal. The cost is real and is stated rather than
hidden: a legitimately refused agent produces no server-side line identifying it.

### Environment variables

| Variable | Description |
|----------|-------------|
| `RELAY_AGENT_ENROLLMENT_TOKEN` | One-time enrollment credential issued by an admin (`relay agent enroll`). Required on first boot when no `token` file exists. Cleared from process env immediately after capture. |
| `RELAY_TELEMETRY_INTERVAL` | How often the agent samples host CPU/memory/GPU utilization and reports it to the server. Default `10s`. |
| `RELAY_WORKSPACE_ROOT` | Absolute path under which the agent creates source-controlled workspaces (e.g. Perforce stream clients). Setting this enables the workspace provider; tasks with a `source` field will fail if it is unset. |
| `RELAY_WORKSPACE_MAX_AGE` | Idle workspace age threshold (e.g. `14d`, `8h`). Workspaces unused longer than this are evicted by the sweeper. |
| `RELAY_WORKSPACE_MIN_FREE_GB` | Free-disk threshold in GB. When free disk drops below this, LRU workspaces are evicted until the threshold is met. |
| `RELAY_WORKSPACE_SWEEP_INTERVAL` | How often the sweeper runs. Default `15m`. Only active when `MAX_AGE` or `MIN_FREE_GB` is set. |
| `RELAY_EVICTION_TIMEOUT` | Per-eviction deadline (Go duration, e.g. `45m`, `2h`) bounding the `p4 client -d` call during workspace eviction. Default `30m`. A wedged delete becomes a logged, retryable best-effort skip instead of stalling the sweeper. Does NOT bound the on-disk `os.RemoveAll`. |

### Task subprocess environment

The table above describes the **agent's own** process environment. This section is a different thing: it is what relay adds to the environment of every **task subprocess** the agent spawns.

| Variable | Value | Present when |
|----------|-------|--------------|
| `RELAY_JOB_ID` | The job's UUID | Every dispatch from a relay coordinator. No server configuration needed. |
| `RELAY_TASK_ID` | The task's UUID | Every dispatch from a relay coordinator. No server configuration needed. |
| `RELAY_JOB_URL` | `<RELAY_PUBLIC_URL>/jobs/<job-id>` | `RELAY_PUBLIC_URL` is set on the **server** |
| `RELAY_TASK_URL` | `<RELAY_PUBLIC_URL>/jobs/<job-id>/tasks/<task-id>` | `RELAY_PUBLIC_URL` is set on the **server** |

**A job spec cannot override these four names**, and neither can a workspace provider. The guarantee holds whether or not `RELAY_PUBLIC_URL` is set on the server. That is the point: a step that posts `$RELAY_JOB_URL` into chat is posting a link other people will click, and the guarantee is what makes the value worth trusting.

**Never set-and-empty.** Each name is either absent or carries a non-empty value, so one check is enough and there is no second case for "set but blank":

```sh
if [ -n "$RELAY_JOB_URL" ]; then notify "build running at $RELAY_JOB_URL"; fi
```

**Two limitations.**

- **The strip covers the job spec and the workspace provider, not the agent's own environment.** If `relay-agent` is itself started from a shell that exported `RELAY_JOB_URL`, every task inherits that value wherever relay has no value of its own. The trust boundary this feature defends is the job spec author, not the agent operator, who already chooses the agent binary and owns the machine the subprocess runs on.
- **Windows resolves environment variable names case-insensitively; other platforms do not.** The strip matches that rule rather than a rule of its own, so a job spec key `relay_job_id` is removed on a Windows agent, where it would otherwise be the same variable as `RELAY_JOB_ID`, and is kept everywhere else, where it is a genuinely distinct variable relay has no claim on. Neither is a defect - a distinct variable is not an override - and the guarantee above is stated over these exact names for that reason.

A task that itself submits a relay job runs with its parent's `RELAY_JOB_ID` in scope, so a script that reads the variable after submitting gets the parent's id, not the new one.

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
| `stream` | Yes | Perforce stream path. relay addresses p4 by client path, so the stream's own view - remaps included - is what defines the layout on disk. The part of a sync path that follows the stream is interpreted in the client's layout, which for a remapped stream is not the depot layout, so a narrower subpath resolves through the remap and may not name what the author intended. Workspaces are keyed by stream and reused across tasks. |
| `sync` | Yes | One or more paths to sync. Each entry has `path` (depot path or `...`) and `rev` (`"#head"`, `@CL`, or `@label`). |
| `unshelves` | No | List of pending changelist numbers to unshelve into the workspace before running. Reverted automatically after the task. |
| `workspace_exclusive` | No | If `true`, take an exclusive lock on the workspace (other tasks for the same stream queue). Default `false`. |

**Prepare failures.** When a task's prepare phase fails - a sync error, a bad stream, a missing ticket, or a worker with no workspace provider at all - the provider's error is appended to that task's log, on the **stderr** stream, prefixed `[failed] `. It is readable through `GET /v1/tasks/{id}/logs`, `relay logs` and the SPA's task log view, live and on refresh. On a task with retries left it is followed by the next attempt's output rather than ending the log.

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
relay submit job.json          # submit, then print each task's log as it finishes
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
| `tasks` | Yes | The job's task list. At least one, at most `5000`; a longer list is rejected at submission, and re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs), because that makes the bound retroactive over schedules created by earlier releases. The cap stands in for two costs it is cheaper to bound than to fix: task rows are inserted one at a time, one round trip each, inside a single transaction, and the dispatcher re-reads the whole pending backlog on every tick until it drains. |
| `tasks[].name` | Yes | Unique within the job |
| `tasks[].command` | Yes (or `commands`) | Legacy single-command spelling: one executable and its arguments as an array. Normalized into a one-element `commands` on ingest. Set either this or `commands`, never both. |
| `tasks[].commands` | Yes (or `command`) | Several commands the agent runs SEQUENTIALLY in the same prepared workspace and environment, as an array of argv arrays. At most `500` per task, and at most `25000` across all of a job's tasks; both are rejected at submission and re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs). Past a few hundred, one task per unit is the better model anyway: separate tasks parallelize across the fleet, retry independently, and report per-unit status. Tasks sharing a `source` reuse the same workspace, so splitting does not cost the workspace sharing that motivates `commands`. |
| `tasks[].env` | No | Extra environment variables for this task |
| `tasks[].requires` | No | Worker label selector (task only runs on matching workers) |
| `tasks[].timeout_seconds` | No | Kill task after this many seconds. Max `604800` (7 days); a larger or negative value is rejected at submission, and re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs), because that makes the bound retroactive over schedules created by earlier releases. **Omitted or `0` both mean "no deadline"** - the field is optional and `0` is its second spelling. Independent of `RELAY_TASK_MAX_ASSIGNMENT`, which bounds how long a task may stay ASSIGNED rather than how long it may RUN; a task whose own timeout exceeds that cap is simply swept by the other arm. |
| `tasks[].retries` | No | Retry up to this many times on failure (default `0`, max `10`). A larger or negative value is rejected at submission, and re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs), because that makes the bound retroactive over schedules created by earlier releases. It bounds one per-task multiplier, and the counts it multiplies are bounded separately: `tasks` max `5000`, `tasks[].commands` max `500` per task and `25000` per job. **All of these bound ONE request and none of them bounds repetition** - `POST /v1/jobs` carries no rate limit, so the totals above are per-request figures an authenticated caller may repeat. There is no backoff between a failed task and its redispatch, so a deterministically-failing command burns the whole budget in seconds; for a contended resource use a reservation rather than a large retry count. |
| `tasks[].depends_on` | No | List of task names that must complete before this one starts |
| `tasks[].source` | No | Workspace source spec — agent prepares this before running the task. See [Source workspaces](#source-workspaces). |

When submitted without `--detach`, the CLI prints the job ID and then waits for the job, printing each task's log to stdout once that task finishes. It is the same mechanism as [`relay logs`](#relay-logs) - not a live stream; log content is fetched over REST per finished task - and it has the same exit codes, so a job that finished `done` but whose log could not be fetched in full exits non-zero.

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

Watch a job until it finishes, printing each task's log once that task reaches a
terminal state.

The command subscribes to `/v1/events?job_id=<id>`, which carries job and task
**status** frames only - `task_log` frames require a subscription that names an
explicit `task_id` (see the event table under "Events"). Log content is fetched
over REST from `GET /v1/tasks/{id}/logs` once a task goes terminal, so output
arrives in a burst per finished task rather than live, line by line.

```sh
relay logs <job-id>
```

Output format:

```
[frame-001 stdout] Blender 4.0, blender.org
[frame-001 stdout] Read blend: scene.blend
[frame-001 stderr] Warning: deprecated API call
```

A task's log is paged to the end of what the server had stored **at the moment it
was fetched**, so a log longer than one page is printed in full. That fetch
happens once, as the task goes terminal; its agent may still append for up to
`RELAY_TASKLOG_TRAILING_WINDOW` (default 15m) afterwards, so re-run `relay logs`
on the finished job to pick up late output.

When the job finishes, its task list is re-read once and any task not already
printed is printed then. This is what makes a cancelled job print anything at
all: `relay cancel` marks the job's in-flight tasks failed in a single statement
and emits one event, for the job, so those tasks are never announced
individually. The re-read is also what covers a subscribe-time snapshot that
could not be read at all, since the command then knows nothing about the job
until the stream ends.

The one case that skips it is a job that had **already** finished when the
command started: the snapshot taken at subscribe time is itself the authoritative
read, and every task it listed has already been printed from it.

If a task is still **unfinished** in the final list - the job says it is over and
the task says it is not - its log is printed anyway, since the rows that exist
are worth more than silence, with a note on stderr saying the log is not final
and a non-zero exit. That note is printed whether or not the fetch itself
succeeded; a log can be short for both reasons at once, and the task is still
counted once.

The job id may be given in any spelling the server accepts, including uppercase
hex and the dashless 32-character form.

If a page cannot be fetched, or cannot be written to stdout, `relay logs` prints
a diagnostic on **stderr** naming the task, the last log sequence number it
printed and how much of the log is missing, keeps watching the job's other tasks,
and exits 1:

```
relay: logs for task frame-001 (7e660488-...) are incomplete - stopped after seq 4200 (4200 of 91340 rows): fetching page 22: get task logs failed
error: logs incomplete for 1 of the job's tasks
```

Exit codes: `0` when the job finishes `done`, every task's log printed in full,
and no task was still unfinished when the job ended; `1` otherwise. A failed or
cancelled job whose logs all printed exits 1 with no message - neither command prints the
job's status (stdout carries task log lines, plus the job ID for `relay submit`),
so run `relay get <job-id>` to see it. When the logs are incomplete as well, both
facts are reported rather than one standing in for the other:

```
error: job finished failed; logs incomplete for 1 of the job's tasks
```

---

#### `relay workers list`

> **Note:** order changed to `created_at DESC` (previously alphabetical by name).

```sh
relay workers list
relay workers list --limit 10
relay workers list --json
relay workers list --sort name             # alphabetical
relay workers list --sort -last_seen_at    # most-recently-seen first
relay workers list --revoked               # revoked (decommissioned) workers (admin only)
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

Revoke the long-lived authentication token for a worker (admin only). The agent will not reconnect until re-enrolled. On a *live* connection this is partial: **new dispatches stop immediately** (revoking sets the worker's status to `revoked` and the dispatcher only selects `online` or `stale` workers), but the agent keeps its stream and finishes the tasks it already holds, still writing their logs and statuses, because nothing re-checks the credential after registration and relay sets no maximum connection age.

```sh
relay workers revoke <worker-id>
relay workers revoke <hostname>
```

Note the hostname wart, which is **not** fixed here: a hostname is resolved
against `GET /v1/workers` only, and that endpoint excludes revoked rows, so
`relay workers revoke <hostname>` cannot name a worker that is already revoked.
Revoke is idempotent in intent, so this is harmless - pass the worker UUID if you
need to. `relay workers delete` is the one subcommand that resolves against the
revoked list too, because reaching an already-revoked row is the point of that
verb; whether the other subcommands should is an open question tracked separately.

---

#### `relay workers delete`

Delete a worker row (admin only). **This is the only command that frees a claimed
hostname**, and it is irreversible - there is no undo and relay has no audit log,
so the counts the command prints are the only record of what was destroyed.

`--yes` is **required**. Without it the command prints what it would delete and
exits non-zero, issuing no request at all. It is a flag rather than an
interactive prompt because every destructive path in this CLI is flag-driven and
a prompt breaks scripted use.

```sh
relay workers delete --yes <worker-id>
relay workers delete --yes <hostname>
```

A hostname is resolved against `GET /v1/workers` first and, on a miss, against
`GET /v1/workers/revoked`, so an already-revoked worker can still be named by
hostname - which matters because revoke-then-later-delete is the natural
sequence. That costs a second request only on a miss, and **only for `delete`**:
the other `workers` subcommands resolve against the live list alone.

The delete is **permitted only while the row's status is `offline` or `revoked`**.
A worker that is `online` or `stale` (including a disabled one, since disable does
not close the stream) is refused with 409: disable it and wait for it to go
offline first. **Revoking is not a way around that**, and the 409 deliberately
does not suggest it: revoking only clears the credential, it does not close the
stream, so a revoked worker may still be connected and running tasks. Delete
handles that case rather than pretending it cannot happen - it sends the
still-connected agent a cancel for every task it requeues.

In one transaction it requeues the worker's assigned tasks (they go back to
`pending` with their assignment epoch bumped, so they are re-dispatchable rather
than stranded), nulls the consuming enrollment's `consumed_by` while leaving
`consumed_at` intact, removes the worker's id from every reservation naming it,
and cascades its workspace rows away. It then prints what it destroyed: the
hostname, and four counts.

The fourth count, `attribution_cleared`, is the one worth reading. `tasks.worker_id`
is `ON DELETE SET NULL` for **every** row, but only `dispatched`/`preparing`/`running` tasks are
requeued - so every **finished** task the worker ever ran silently loses its
`worker_id`, and that field is part of the task API. After the delete, "which
machine ran this job" is unanswerable for those rows. Nothing recovers it; the
count exists so at least the scale of the loss is on the record. It is usually the
largest number the command prints.

Two limitations worth knowing. A reservation whose `worker_ids` becomes empty is
**left in place**: it then reserves nothing, and nothing says so, so it must be
removed or re-pointed by hand. And deleting an already-revoked worker frees no
auto-enroll ceiling budget, because that ceiling counts non-revoked rows only -
delete always frees the **hostname**, but frees **budget** only for a worker that
was not already revoked.

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
relay reservations list --sort name         # alphabetical
relay reservations list --sort starts_at    # chronological by start
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

Schedules are owned by the user who created them; non-admins see only their own. Admins can list and operate on all of them. The schedule's owner (or an admin) can use `run-now` to fire a schedule immediately.

**A stored spec is re-validated on every fire, so job-spec rules are retroactive.** A schedule keeps the spec it was created with, and the server validates that spec again on each fire rather than grandfathering it for having been accepted once. A spec that a later release refuses therefore stops producing jobs while the schedule still *looks* healthy: `next_run_at` keeps advancing, nothing disables it, `enabled` stays true, and until 2026-08-28 the only record was one line in the server log. `tasks` (max `5000`), `tasks[].commands` (max `500` per task and `25000` per job), `tasks[].retries` (max `10`) and `tasks[].timeout_seconds` (max `604800`) are the newest rules with this property, so a schedule created before they existed with, say, `"retries": 50` stops firing on upgrade. They are not the first: `scheduled_jobs` shipped 2026-04-22, and both the `source` validator (2026-04-24) and the `priority` allow-list (2026-06-20) landed after it, so a schedule storing `{"priority":"urgent"}` accepted between those dates fails today with the identical symptom.

**A schedule that stops firing now says so.** `GET /v1/scheduled-jobs/{id}` and `GET /v1/scheduled-jobs` both carry two optional fields: `last_error`, the reason the scheduler last failed to produce a job, and `last_error_at`, when it failed. **Absent means healthy** - a schedule that has never failed carries neither key, not an empty string and not `null`. Because the list carries them too, an operator scanning `relay schedules` or the SPA's schedules table can see *which* schedule to suspect without suspecting anything first.

Only permanent failures are recorded: a `job_spec` that will not decode, a `cron_expr` that will not parse, and a spec that fails validation. A transient database fault is logged and not recorded, and it does not overwrite an existing record - a blip is not news about the schedule. `last_error` is **cleared by a successful fire**, and by a `PATCH` that supplies a new `job_spec`, `cron_expr` or `timezone` **and leaves a schedule whose stored `job_spec`, `cron_expr` and `timezone` all validate**. A `PATCH` that changes one of the three while another is still broken keeps the record: the handler validates only what the request supplied, so a record about an input it never looked at is not stale, and erasing it would leave nothing to rewrite it until the next fire. It is **preserved** by a skipped fire (`overlap_policy: skip` with the previous run still active), by a `PATCH` that only renames the schedule or changes `overlap_policy` or `enabled`, and by disabling and re-enabling.

**`last_error` and `last_job_status` answer different questions and can both be present.**
`last_error` is about a fire that produced **no job at all**; `last_job_status` is about
the last job the schedule did manage to produce. The failure statement touches neither
`last_job_id` nor `last_run_at`, so a schedule can carry `last_job_status: "done"` and a
`last_error` at the same time, and the correct reading is "the last job it produced
finished successfully, and the most recent attempt produced no job". That is not a
contradiction. `last_error` is the row-level health signal; `last_job_status` belongs to
the cell that links to the job.

`last_error` is **derived from the schedule's stored configuration and is operator-supplied**: it comes from the stored `job_spec`, or from `cron_expr` and `timezone` when the failure is a `parse cron:` one, and it **may** quote prose the schedule's owner chose - a task name interpolated verbatim into a `task <name>: ...` message, a cron expression echoed back by the parser. Other messages are fixed relay text with nothing operator-chosen in them, and every relay surface labels the whole class the same way rather than string-matching the server's internal branches, so treat any `last_error` as operator-controlled. It is sanitized at the write site - C0 controls and DEL, the C1 range `U+0080`-`U+009F` (which includes the single-byte CSI), and the bidirectional formatting controls (`U+200E`/`U+200F`, `U+202A`-`U+202E`, `U+2066`-`U+2069`) are all replaced with spaces - and truncated to 1 KB on a rune boundary; `run-now` returns the untruncated message. Render it as text, never as markup.

**When a schedule reports a failure:**

1. `POST /v1/scheduled-jobs/{id}/run-now`, or `relay schedules run-now <id>`, or the SPA's **Run now**, to re-check interactively and get the current message in full and untruncated.
2. Repair the stored spec: `PATCH /v1/scheduled-jobs/{id}` with a new `job_spec`, or `relay schedules update <id> --spec FILE`.
3. Disable the schedule if it should not run: `relay schedules update <id> --disable`.

There is no fourth step. In particular there is no "relax the validator" step: the bounds are not environment-configurable by design, because an env-tunable bound would make retroactive schedule invalidation environment-dependent - the same stored spec would fire on one replica's configuration and silently stop on another's.

**To check a schedule you suspect, fire it by hand.** `relay schedules run-now <id>` runs exactly the same validation and answers `400` with the per-task message (`task t: retries must be between 0 and 10`) instead of failing quietly, so it is the way to turn a schedule that has gone silent into a specific reason. Replacing the stored spec is a `PATCH /v1/scheduled-jobs/{id}` with a new `job_spec`, or `relay schedules update <id> --spec FILE`.

**Tasks already in the database are deliberately left alone.** No migration clamps or rejects a `retries` or `timeout_seconds` value that an earlier release already stored on a `tasks` row. The bound applies to job creation from here on; a task that was created with `retries: 2000000000` keeps it and still retries that many times. The same goes for the count bounds: no migration deletes tasks or commands from a job that already exceeds them, and a job created before they existed with 20,000 tasks keeps every one.

---

#### `relay schedules list`

List all scheduled jobs owned by the current user (admins see all).

```sh
relay schedules list
relay schedules list --sort name           # alphabetical
relay schedules list --sort next_run_at    # next-to-fire first
```

Output columns: `ID`, `NAME`, `CRON`, `TZ`, `ENABLED`, `NEXT` (next scheduled run time), `STATE`.

`STATE` is `OK`, or `FAILING` when the scheduler last failed to produce a job from the schedule's stored spec. It is a separate axis from `ENABLED`: a failing schedule is still enabled, because relay does not disable one on its own. Run `relay schedules show <id>` for the reason.

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

Prints the id, name, cron expression, timezone, enabled flag, next run and last run. When the schedule's last fire failed it also prints `Last error`, when it failed, and the `run-now` command that re-checks it. The error text is derived from the schedule's stored configuration - usually its `job_spec`, and its `cron_expr` or `timezone` for a `parse cron:` failure - and is operator-supplied, and the line names its provenance; it is truncated to 1 KB, so use `run-now` for the full message.

---

#### `relay schedules update`

Modify a schedule in place. Only supplied flags are changed.

```sh
relay schedules update <schedule-id> --cron "0 4 * * *"
relay schedules update <schedule-id> --disable
relay schedules update <schedule-id> --enable --tz UTC
relay schedules update <schedule-id> --spec repaired-job.json
```

| Flag | Description |
|------|-------------|
| `--cron EXPR` | New cron expression |
| `--tz ZONE` | New IANA timezone |
| `--spec FILE` | Replace the stored job spec with the contents of FILE (same format as `relay schedules create --spec`). The server validates it and reports any problem verbatim. This is the repair for a schedule whose `STATE` reads `FAILING`. |
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

Fire the schedule immediately, outside of its normal cron cadence (owner or admin).

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
relay admin users list --sort email    # alphabetical by email
relay admin users list --sort name     # alphabetical by name
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

For Claude Code, add it via `claude mcp add relay -- relay mcp` or by editing `~/.claude.json` directly. Restart the client. The relay tools (prefixed `relay_*`) and resources (`relay://server-info`, `relay://recent-jobs`) become available, along with the URI-templated resources `relay://jobs/{id}` and `relay://tasks/{id}` for reading a single job or task by id.

`relay://recent-jobs` is cached so repeated polls within a session do not refetch `GET /v1/jobs?limit=20`. The cache window is controlled by `RELAY_MCP_RESOURCE_CACHE_TTL` (a Go duration, default `10s`); set `RELAY_MCP_RESOURCE_CACHE_TTL=0` to disable caching so every read refetches.

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
| `relay_run_schedule_now` | Fire a schedule immediately (owner or admin). |

Calls that map to admin-only endpoints return a `forbidden` error when invoked by a non-admin token.

`relay_wait_for_job` tolerates a transient failure of its poll (a 5xx, a network
error, a 429) and keeps waiting, giving up only after five consecutive failures
or at the timeout. A failure a later poll cannot outlive - a 404, a 400, an
expired token, a permission the caller does not have - ends the wait immediately.

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
| `GET /v1/invites` | `-created_at` | `created_at`, `expires_at` |
| `GET /v1/auth/tokens` | `-created_at` | `created_at` |

Each key supports both directions, e.g. `?sort=name` (ascending) and `?sort=-name` (descending).

**Examples:**

```
GET /v1/jobs?sort=-priority           # group by priority label (desc; text sort)
GET /v1/workers?sort=name             # alphabetical
GET /v1/jobs?sort=status&limit=10     # group by status, smaller pages
```

**Cursor semantics:** A cursor is valid only for the sort it was issued under. Resending a cursor with a different `?sort=` returns `400 cursor sort key does not match requested sort`. Drop the cursor when changing sort.

**Filter + sort:** `GET /v1/jobs` rejects `?sort=` combined with `?status=` or `?scheduled_job_id=` with `400 sort not supported on filtered list variant`. That rejection is scoped to exactly those two parameters, because each has its own statement with a hard-coded `ORDER BY`. The four filters in [Filtering the jobs list](#filtering-the-jobs-list) - `?q=`, `?mine=`, `?since=`, `?until=` - are threaded into every sort variant and **do** compose with `?sort=`. `GET /v1/scheduled-jobs`'s `?enabled=` and `?q=` and `GET /v1/reservations`'s `?worker_id=` are threaded into every sort variant the same way and also compose with `?sort=`.

**Unknown keys:** `?sort=<key>` where `<key>` is not in the allowlist returns `400 unsupported sort key '<key>'; supported: <list>`.

#### Query-string validation

These rules apply to **every paginated list endpoint** - each of the ones marked "Paginated" in the endpoint tables below - not only to `GET /v1/jobs`. The first two are enforced once, where the query string is parsed, so a new parameter on any endpoint inherits them without doing anything. The arity rule is not automatic: `parsePage` checks only `limit`, `sort` and `cursor`, and every other parameter has to be named in its own endpoint's check.

| Condition | Status | Message |
|-----------|--------|---------|
| the query string is not decodable | `400` | `malformed query string` |
| any value contains a NUL byte | `400` | `query string contains a NUL byte` |
| a parameter the endpoint reads appears more than once | `400` | `query parameter "<name>" must appear at most once` |

All three are decided before any endpoint-specific rule. On `GET /v1/jobs` the one exception runs earlier still: the sort-versus-filter `400` below outranks this endpoint's own arity check, so `?sort=name&status=a&status=b` answers with the sort message.

The repeated-parameter rule covers `limit`, `sort` and `cursor` on every endpoint, plus whichever parameters that endpoint reads itself (`GET /v1/jobs` adds `status`, `scheduled_job_id`, `q`, `mine`, `since` and `until`; `GET /v1/users` adds `email` and `include_archived`). Taking the first value of a repeated parameter silently renders a list that looks authoritative.

#### Filtering the jobs list

`GET /v1/jobs` accepts four optional filters beyond `?status=` and `?scheduled_job_id=`. They AND together, and they compose with `?limit=`, `?cursor=`, `?sort=`, `?status=` and `?scheduled_job_id=`.

For all four, an **empty value is treated as absent**: `?mine=`, `?since=`, `?until=` and `?q=` each mean the same as omitting the parameter, so a cleared form field does not need to be stripped from the query string.

| Parameter | Format | Absent means |
|-----------|--------|--------------|
| `q` | Free text. Case-insensitive substring of either the job `name` or the submitter's `email`. `%` and `_` are **literal characters**, not wildcards. Maximum 200 characters. Whitespace-only is treated as absent. The same `q` contract applies to `GET /v1/scheduled-jobs`, with a third match axis. | No text filter |
| `mine` | `true` / `false` (Go `strconv.ParseBool` spellings: `1`, `t`, `T`, `TRUE`, `true`, `True` and their false counterparts). `true` restricts to jobs you submitted, resolved from your bearer token; `false` means the same as absent. | No owner filter |
| `since` | RFC3339 timestamp. An offset or `Z` is required; fractional seconds are allowed. | Window open at the start |
| `until` | RFC3339 timestamp, same format. | Window open at the end |

`since` and `until` bound `created_at` as a **half-open** interval: `created_at >= since AND created_at < until`. A job created exactly at `since` is included; a job created exactly at `until` is excluded, so consecutive time buckets tile without a job appearing in two of them. Either bound may be given alone, and `until == since` is a legal empty window.

`GET /v1/jobs` lists jobs across the whole farm for any authenticated caller. `mine=true` is a convenience filter over that list, not an authorization boundary; it resolves the owner from your bearer token.

`total` counts every row matching every active filter, so the page footer's denominator always belongs to the same set as the rows.

**Errors.** These are `400` with the body `{"error": "<message>"}`. The query-string rules that apply to every paginated endpoint - malformed input, repeated parameters and NUL bytes - are under [Query-string validation](#query-string-validation).

| Condition | Message |
|-----------|---------|
| `mine` is not a boolean | `invalid mine; expected true or false` |
| `since` is not RFC3339 | `invalid since; expected an RFC3339 timestamp` |
| `until` is not RFC3339 | `invalid until; expected an RFC3339 timestamp` |
| `until` is earlier than `since` | `until is earlier than since` |
| `q` is longer than 200 characters | `q is too long; maximum 200 characters` |
| `q` is not valid UTF-8 | `q is not valid UTF-8` |

**Drop the cursor when a filter changes.** A cursor carries no record of the filters that were active when it was issued and the server does not reject a mismatched one - the same requirement that already applies to `?status=`. Filter correctness is nevertheless cursor-independent: a stale cursor can start a page at a surprising position but can never return a row that fails the current filters.

**Examples:**

```
GET /v1/jobs?q=nightly                                   # name or submitter email contains "nightly"
GET /v1/jobs?mine=true&sort=-priority                    # your jobs, highest priority first
GET /v1/jobs?since=2026-09-01T00:00:00Z&until=2026-09-02T00:00:00Z
GET /v1/jobs?q=etl&status=failed                         # composes with the status filter
```

**`?q=` cost.** Substring containment cannot be index-served, so a `?q=` request walks every candidate row of the active sort's index and, for the count, joins `users` to reach the submitter email. A needle that matches nothing is the worst case: it pays the full walk and returns an empty page. The server applies no rate limit and no statement timeout to this today, so the cost is bounded only by the table size and by how often clients ask. Debouncing at 250 ms or more client-side reduces how many of these a typing user generates; it does not bound what a caller can request.

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
{
  "token": "<hex>",
  "expires_at": "2026-07-16T00:00:00Z",
  "user": {
    "id": "<uuid>",
    "email": "you@example.com",
    "name": "Your Name",
    "is_admin": false,
    "created_at": "2026-07-16T00:00:00Z",
    "archived_at": null
  }
}
```

**POST `/v1/auth/login`** body:

```json
{ "email": "you@example.com", "password": "..." }
```

Returns `201 Created`, with the same body:

```json
{
  "token": "<hex>",
  "expires_at": "2026-07-16T00:00:00Z",
  "user": {
    "id": "<uuid>",
    "email": "you@example.com",
    "name": "Your Name",
    "is_admin": false,
    "created_at": "2026-07-16T00:00:00Z",
    "archived_at": null
  }
}
```

`user` is **exactly** the `GET /v1/users/me` body, built by the same code, so the two can
never disagree. `archived_at` is always `null` on these two endpoints: an archived user is
refused at login and a newly created one is never archived. A client that already has the
login response therefore does not need a `/v1/users/me` round trip to learn who it is.

Tokens are valid for 30 days.

### Session

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/v1/users/me/password` | Change own password (body: `current_password`, `new_password`) |
| `GET` | `/v1/auth/tokens` | List the calling user's own live sessions. Paginated. |
| `DELETE` | `/v1/auth/token` | Revoke the bearer token used on this request |
| `DELETE` | `/v1/auth/tokens` | Revoke every active bearer token for the calling user |

**GET `/v1/auth/tokens`** returns the caller's own tokens only. There is no `user_id`
parameter; the identity is the bearer token, and a `?user_id=` in the query string is
ignored. Items:

```json
{ "id": "<uuid>", "created_at": "2026-08-01T10:00:00Z", "expires_at": "2026-08-31T10:00:00Z", "is_current": true }
```

- `expires_at` is **omitted when the token never expires** (a NULL column). Render the
  absence as `never`, not as a missing value.
- `is_current` is always present on every item. Exactly one row **across the caller's whole
  list** is `true` - the token presented on this request - which means a single page may
  contain none: at `?limit=1` every page but one has zero `true` rows. Do not treat "no
  current row on this page" as an error.
- Only tokens that can currently authenticate are listed. Expired tokens are excluded and
  are not counted in `total`.
- There is no per-session revoke endpoint. `DELETE /v1/auth/tokens` revokes every session
  including the caller's; `PUT /v1/users/me/password` revokes every session except the
  caller's, after which this list contains exactly one row.
- No `last_used_at`, IP, user agent or device is available: no such column exists.

### Users

All user-management endpoints other than `PATCH /v1/users/me` are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/users` | List users (`?email=` filter for exact-match lookup). Optional `?include_archived=true` includes archived users. Paginated. The `?email=` lookup validates `limit`, `sort` and `cursor` like any other list request, even though it returns a fixed one-row envelope. |
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
| `GET` | `/v1/jobs` | List jobs. Optional filters: `?status=`, `?scheduled_job_id=`, `?q=`, `?mine=`, `?since=`, `?until=` - see [Filtering the jobs list](#filtering-the-jobs-list). Paginated. |
| `GET` | `/v1/jobs/{id}` | Get a job |
| `DELETE` | `/v1/jobs/{id}` | Cancel a job (`?force=true` for forced termination, skips pipe drain and workspace cleanup) |
| `POST` | `/v1/jobs/{id}/retry` | Re-run a finished job's tasks. `?task=failed` reopens `failed` **and `timed_out`** tasks; `?task=all` also reopens `done` tasks. `task` is **required** and has no default; absent, empty, repeated or unrecognized values are a 400. Owner or admin (404 on deny). 409 if the job is not `done` or `failed`, if the job was cancelled, if nothing matched the mode, or if a selected task has dependents that already ran. Returns the job plus `tasks_retried` (always >= 1). |
| `GET` | `/v1/jobs/{id}/tasks` | List tasks for a job |

### Tasks

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/tasks/{id}` | Get a task |
| `GET` | `/v1/tasks/{id}/logs` | Get task log entries. Paged by `seq`, forward or backward. See "Task log paging" below. |

**Task log paging.** `GET /v1/tasks/{id}/logs` pages by `seq`, which is the log
row's id. It is ordered but **not contiguous**: it comes from a table-wide
sequence shared by every task, so neither `total` nor arithmetic on `seq` yields
an offset.

| Parameter | Default | Rule |
|---|---|---|
| `limit` | 50 | 1..200. Out of range or unparseable: 400 `limit must be 1..200`. |
| `order` | `asc` | `asc` or `desc`, nothing else. Absent or empty is `asc`. Anything else: 400 `order must be asc or desc`. |
| `since_seq` | 0 | Ascending only. **Exclusive**: returns rows with `seq > since_seq`. Negative or unparseable: 400. Sent with `order=desc`: 400. |
| `before_seq` | none | Descending only. **Exclusive**: returns rows with `seq < before_seq`. Absent with `order=desc` means "the newest page". Less than 1 or unparseable: 400. Sent without `order=desc`: 400. |

The response always carries all four keys, on every page including an empty
one. This is `?order=desc&limit=1` against a 94312-entry log:

```json
{
  "items":    [ { "seq": 41, "stream": "stdout", "content": "...", "created_at": "..." } ],
  "next_seq": 0,
  "prev_seq": 41,
  "total":    94312
}
```

- **`items` is always ASCENDING by `seq`, in both directions.** `order` selects
  WHICH rows the page contains, not the order they appear in it. A descending
  page is the newest `limit` rows, listed oldest-first.
- `next_seq` is the ascending cursor: the last row's `seq`, or `0` when the page
  was short (drained). It is always `0` in a descending response.
- `prev_seq` is the descending cursor: the FIRST row's `seq` (the lowest), or `0`
  when the page was short (the beginning of the log has been reached). It is
  always `0` in an ascending response. A FULL final page still mints a
  non-zero `prev_seq`, and the request that follows it returns an empty page
  with `prev_seq` `0`: stop on a `0` cursor, never on a short page.
- `total` counts the task's log ENTRIES, not lines. An entry is an arbitrary
  chunk of output; one line can straddle two entries.
- **Not every line comes from the subprocess.** The coordinator synthesizes one:
  a task whose prepare failed carries the provider's error as a `stderr` entry
  prefixed `[failed] `. An agent that repeats its terminal status message can
  produce that line more than once - the duplicate status write is refused and
  recorded in `task_status_fence.counts.duplicate_total`, but the log line is
  written each time inside `RELAY_TASKLOG_TRAILING_WINDOW`. **The prefix is a
  readability convention, not provenance.** The task's own agent can send an
  ordinary `stderr` chunk whose content begins `[failed] ` through the same
  fence into the same column, and `task_logs` has no field that separates the
  two - so read it as a label, never as proof the coordinator wrote that line.

Stop when the cursor for your direction is `0`. Do not feed a `0` cursor back:
`before_seq=0` is a 400, not an empty page.

Read the end of a long log with one request, then walk earlier:

```
GET /v1/tasks/{id}/logs?order=desc&limit=200
  -> the newest 200 entries, ascending; prev_seq = the lowest seq in that page

GET /v1/tasks/{id}/logs?order=desc&before_seq=<prev_seq>&limit=200
  -> the 200 entries immediately older, ascending; repeat until prev_seq is 0
```

An unknown task is a `404` before any parameter is validated, so a malformed
`?order=` on a task that does not exist returns `404`, not `400`.

### Workers

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/workers` | List workers. Paginated. Order: created_at DESC (changed from name ASC). Excludes revoked workers. |
| `GET` | `/v1/workers/stats` | Fleet-wide worker counts: `online`, `stale`, `offline`, `disabled`, and `total`. `total` is the sum of those buckets; revoked workers are excluded. Same bearer-auth as `GET /v1/workers`. |
| `GET` | `/v1/workers/revoked` | List revoked (decommissioned) workers for audit, newest revocation first (admin only). Paginated, same `page` envelope as `GET /v1/workers`; each item includes `revoked_at`. Sortable only by `-revoked_at` (the default). |
| `GET` | `/v1/workers/{id}` | Get a worker |
| `PATCH` | `/v1/workers/{id}` | Update name, labels, or max_slots (admin only) |
| `DELETE` | `/v1/workers/{id}` | Delete a worker row (admin only). Permitted only while the row's status is `offline` or `revoked`; `online` or `stale` returns 409. Note `revoked` does not imply disconnected (revoke does not close the stream), so a still-connected agent is sent a cancel for every task this requeues. Requeues the worker's assigned tasks first, scrubs its id out of every reservation, nulls the consuming enrollment's `consumed_by`, and cascades its workspace rows. Returns 200 with the deleted row's identity plus `requeued_tasks`, `reservations_updated`, `enrollments_unlinked` and `attribution_cleared` - the last being the count of FINISHED tasks whose `worker_id` the cascade nulls, which is unrecoverable and usually the largest number. Frees the hostname for re-enrollment; frees ceiling budget only if the worker was not already revoked. |
| `DELETE` | `/v1/workers/{id}/token` | Revoke agent long-lived token (admin only) |
| `POST` | `/v1/workers/{id}/disable` | Stop the scheduler from dispatching new tasks to a worker (admin only); its token and connection are kept. `?requeue=true` also requeues and cancels the worker's active tasks; the default leaves running tasks to finish. |
| `POST` | `/v1/workers/{id}/enable` | Re-enable a disabled worker (admin only). |
| `GET` | `/v1/workers/{id}/workspaces` | List source workspaces on the worker (admin only) |
| `POST` | `/v1/workers/{id}/workspaces/{short_id}/evict` | Request eviction of a workspace (admin only); returns 202 even if the worker is offline |
| `GET` | `/v1/workers/{id}/metrics` | Get the worker's short-term utilization history (CPU, memory, GPU). Returns an empty `samples` array for offline workers or workers with no data yet. 404 if the worker does not exist. Same bearer-auth as `GET /v1/workers/{id}`. |
| `GET` | `/v1/workers/{id}/tasks` | List the tasks currently assigned to a worker (`dispatched`, `preparing` or `running`), newest assignment first. Paginated, standard `page` envelope; `total` is the count of ACTIVE tasks for this worker, which is the same number the dispatcher treats as used slots. Fixed order, sortable only by `-assigned_at` (the default). 404 if the worker does not exist. Same bearer-auth as `GET /v1/workers/{id}`. |

`GET /v1/workers/{id}/tasks` does not return a worker's terminal tasks. `items` and `total` come from two statements, so under concurrent dispatch they can disagree for an instant. The used-slot count can legitimately exceed `max_slots`, because `max_slots` is a dispatcher input rather than a database constraint and lowering it via `PATCH /v1/workers/{id}` requeues nothing.

### Server

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/server/counters` | Process-lifetime counters for the server's own silent controls (admin only). Unlike `/v1/jobs/stats` and `/v1/workers/stats`, which are database censuses readable by any authenticated user, these are in-memory numbers describing adversary activity and internal control state. |

```json
{
  "started_at": "2026-08-21T09:00:00Z",
  "grpc_admission": {
    "counts": { "refused_total": 12, "refused_per_source": 3 },
    "levels": { "live_total": 812, "distinct_sources": 16, "max_per_source": 64 }
  },
  "ingest_log_budget": {
    "counts": {
      "deduped":    { "task_log_persist": 4127, "bad_task_id_log": 0, "bad_task_id_status": 0,
                      "status_get_task": 0, "inventory": 12,
                      "status_retry_write": 0, "status_update_write": 0, "status_fail_dependents": 0,
                      "status_log_persist": 0 },
      "suppressed": { "task_log_persist": 39984, "bad_task_id_log": 0, "bad_task_id_status": 0,
                      "status_get_task": 0, "inventory": 0,
                      "status_retry_write": 0, "status_update_write": 0, "status_fail_dependents": 0,
                      "status_log_persist": 0 }
    }
  },
  "task_log_fence": {
    "counts": { "rejected_total": 0 }
  },
  "task_status_fence": {
    "counts": { "raced_total": 2, "duplicate_total": 118, "conflicting_total": 9 }
  },
  "watchdog": {
    "counts": {
      "swept_total": 41,
      "swept_overflow": 0,
      "swept_by_worker": { "6b1f...": 37, "9c02...": 4 }
    }
  }
}
```

- **`counts` are monotonic since `started_at`; `levels` are current.** Counts only ever increase, so a poller derives rates itself; levels go up and down and are meaningless to sum.
- **A section that is not wired is ABSENT, not zero.** A section of zeros means the control ran and stopped nothing; an absent section means this build or this replica does not have that control wired. `started_at` is always present, because a restart zeroes every counter and a stalled counter would otherwise look identical to a restart.
- **Per replica.** These are in-process numbers about one server. A two-replica deployment splits its connections arbitrarily: read both endpoints and add the `counts`; do **not** add the `levels`, and `max_per_source` in particular does not sum into anything meaningful.
- **Nothing here carries an address, a prefix or a hostname**, and nothing ever will - the refusal path is reachable by any unauthenticated peer. The rule is not "every value is a number": it is that every value is a count or a level unless a specific field has been argued for by SHAPE, in the commit that adds it. Two paths are exempt today: `started_at`, admitted as an RFC 3339 instant, and `watchdog.counts.swept_by_worker`, admitted as a map from server-assigned worker UUIDs to counts and **bounded at 256 keys**, because server-assigned is not the same as server-limited - see the `swept_overflow` bullet below. A field carrying a caller-supplied byte, or a container whose key cardinality a peer can drive *without a hard bound*, is inadmissible whatever its type.
- **Reading `grpc_admission`.** `distinct_sources` with `max_per_source` is what separates the two shapes `refused_total` cannot: a NAT gateway or a busy site is a few sources holding many connections each (`max_per_source` high, `distinct_sources` low), while a distributed source pattern is many sources holding one each. The IPv6 delegation case the per-source cap cannot close - sixteen `/64`s each holding the full 64 - reads as `live_total: 1024, distinct_sources: 16, max_per_source: 64` with **`refused_total` still at zero**, which is precisely the shape the refusal counters are blind to. **What these three numbers cannot do is decompose a mixture.** `max_per_source` is a single maximum, not a distribution, so a busy NAT site *plus* a distributed pattern reads high on both `distinct_sources` and `max_per_source` and the endpoint cannot say how much of each - and that mixture is the realistic case, since an attack lands on a fleet that is already busy. Read the three as a strong signal when one shape dominates and as "something is going on" otherwise; separating the two components needs per-source data, which this payload will never carry.
- **When `live_total` has reached your `RELAY_GRPC_MAX_CONNS`, read `refused_per_source` as a floor rather than a measurement.** The total cap is checked first, so a connection over both caps is counted against `refused_total` only.
- **When both `RELAY_GRPC_MAX_CONNS` and `RELAY_GRPC_MAX_CONNS_PER_IP` are `0`, every `levels` figure reads `0` however many connections are live.** With both caps off the listener does no accounting at all (that is what lets it hand the raw connection to gRPC), so a zero there means "not measured", not "nothing there". The startup line says `total DISABLED, per-source-IP DISABLED` when that is the case.
- **Reading `ingest_log_budget`.** Agents can drive log volume, so the nine per-message log sites on the gRPC receive path - the nine keys below, one per site - are rate-limited per connection; these are the lines that limiter dropped, since `started_at`. **The budget covers those nine sites and no others.** Registration-time lines (auto-enrollment, inventory replace at reconnect) and the teardown line `markWorkerOffline` emits are outside it; the first two because the budget does not exist yet when they run, the third because it fires once per connection teardown and so is bounded by the connection caps rather than by message volume. **`status_retry_write`, `status_update_write` and `status_fail_dependents` are new**: they are `handleTaskStatus`'s three database-error lines, which used to sit outside the budget entirely, so a condition where the *read* succeeds and the *write* fails - a serialization failure, a `statement_timeout`, a connection reset - drove one unbudgeted line per message at whatever rate an agent chose to send, while every number in this section read zero. `status_fail_dependents` is worth its own key because that statement is a recursive CTE, the most expensive on the path and the first to deadlock under contention. **`status_log_persist` is new too**: it is `handleTaskStatus` failing to store a task's prepare-failure cause, and it is separate from `task_log_persist` rather than folded into it because the two share a dedupe key otherwise - whichever site logs first for a `(task, epoch)` silences the other for the window, and the log-path site can arm that key without owning the task. Read them as different incidents: `task_log_persist` is a subprocess chunk that did not persist, `status_log_persist` is the reason a task failed that did not persist. **`deduped`** is the healthy arm: the same failure repeated inside a five-minute window, folded into one line - a large number here next to a quiet log is one task failing over and over, and the number is how many occurrences that single line represents. **`suppressed`** is the loud arm: a line dropped entirely because that connection's token bucket was empty. A non-zero `suppressed` means some connection is producing *distinct* failures faster than six lines per minute, which is either an attack or a misconfiguration; `39984` under `task_log_persist` is the difference between "a flood is in progress" and "the fleet looks quiet". The nine keys name the log site, not the worker: **`ingest_log_budget` has no per-worker split and will not get one**, because keying *these* counters on anything the recv goroutine would have to look up needs a shared map write on the one path that must stay lock-free. That reason is specific to that path and does not generalise - `watchdog` below *is* keyed per worker, because it is written on the scheduler goroutine under its own mutex, on a periodic path that already makes a database round trip.
- **The budget is ONE bucket of tokens per connection, shared by all nine kinds, and one kind's key can be multiplied off the wire - so a connection can suppress its own other eight.** Seven of the nine keys are keyed on nothing the caller supplies. The two persist keys carry a task id and an epoch, because those failures really are per task per generation, but only `task_log_persist` is *multipliable*: `status_log_persist` is reached after the currency gate has already required the epoch to equal the task's own, so a sender cannot vary it. `task_log_persist` has no such gate ahead of it - its key carries the chunk's epoch straight off the wire, and a chunk whose *content* Postgres refuses at Bind (a `\x00` byte, SQLSTATE 22021) fails *before* the fence's `WHERE` is evaluated, so neither the task id nor the epoch has to name anything real. Distinct keys are therefore free: 16 of them drain the burst, and six a minute after that hold the bucket at zero for the life of the connection - during which the other `handleTaskStatus` error lines, including the one reporting a lost prepare-failure cause, return nothing. Measured: 40 such chunks at distinct epochs logged 16 and suppressed 24, with `deduped` still zero, and the very next malformed-task-id status message on the same connection was suppressed while the same message on a fresh connection was not. It is per connection, and it is not silent: the suppression is itself counted here, so **a high `suppressed.task_log_persist` next to a status log that has gone quiet is that shape.**
- **What `ingest_log_budget` does NOT count.** These are *log lines the budget dropped*, not diagnostics lost. A handler that decides not to log without consulting the budget - a status update for a task that no longer exists, a log chunk the fence rejected (counted separately under `task_log_fence`), a status report the status fence rejected (counted separately under `task_status_fence`), or any of the log sites outside the budget listed above - contributes nothing to these numbers. They also have no `levels` half: each budget is per connection and dies with it, so there is no process-wide current figure to report.
- **A non-zero `status_fail_dependents` is a data-integrity signal, not a logging one.** The other keys cost you a diagnostic. This one is different, and the section above says why the key exists without saying what it means: when `FailDependentTasks` fails, the failed task's dependents stay `pending`, nothing retries that cascade, and `GetEligibleTasks` will not dispatch a task whose dependency is not `done` - so those tasks are unreachable forever and the job never reaches a terminal status. Read a non-zero value here, under `deduped` or `suppressed`, as **go find the stuck job**, not as a lost log line.
- **Reading `task_log_fence`.** `rejected_total` counts log chunks the coordinator refused to store since `started_at`. A chunk is refused for one of four reasons and **the payload cannot say which**: the sender is not the task's current assignee, or the task id matches no task at all, or the sender's generation is stale (those three are a zombie or forged sender, and all three are the system working), or the task finished longer ago than `RELAY_TASKLOG_TRAILING_WINDOW` - **which is legitimate, and is the case to suspect first when task output is missing rather than spurious.** The number is one number on purpose: the fence returns no row at all when it refuses, so there is nothing to carry a reason, and recovering one would need a second query on a path budgeted for exactly one. **What it is for:** a count that climbs steadily on a fleet whose jobs look healthy is the signature of a trailing window set too small - a units mistake such as `15s` for `15m` is the likely one - and before this number there was no runtime signal of any kind for that, only silently truncated output. **That signature is forgeable, so confirm the window against its configured value before raising it.** Only `worker_id` is authenticated on an incoming chunk; the task id comes from the wire and nothing rate-limits task-log *chunks* (the ingest budget bounds log *lines*), so any enrolled agent can drive this number up smoothly and indefinitely by naming task ids it does not own. Raising `RELAY_TASKLOG_TRAILING_WINDOW` in response to a forged climb widens the very hole that window exists to bound. The number cannot leak anything back - an agent cannot read this endpoint, and the payload carries no identifiers - so the exposure is griefing and misdirection of the one signal, not disclosure. A count that moves in bursts around requeues, cancellations and worker reconnects is the stale-generation case and is expected. **What it is not:** it does not overlap `ingest_log_budget` in either direction, because the rejection path never consults the log budget; and it never reaches an agent, which still learns nothing about why its chunk was dropped.
- **Reading `task_status_fence`.** These count status reports the coordinator refused to record since `started_at`, split three ways by what the task row said when the handler read it. **`duplicate_total` is the expected healthy baseline and its height depends on your agents' retry behaviour** - a terminal task is deliberately not writable, so an agent that repeats a terminal message it already delivered is refused by design and lands here. Do not treat a rising `duplicate_total` as an incident. **`conflicting_total` is the actionable number**: the row was already final with a *different* outcome than the agent is reporting. The signature to look for is a task the stale-task watchdog stamped `timed_out` whose agent then reports `done` - **a successful task recorded as a timeout**, which is what `RELAY_TASK_WATCHDOG_MARGIN` set too small produces and which had no runtime signal of any kind before this number. Check it against `watchdog.counts.swept_by_worker` and against the sweep summary in the log. **`raced_total`** is the narrow case where something ended the assignment between the handler's read and its write - a cancel, a grace requeue, a job retry, a sibling replica - and is expected to move in bursts around requeues and reconnects.
- **Two of the three are exact; `raced_total` is a floor.** `handleTaskStatus` checks the sender's identity and the assignment's generation *before* either write, so most stale or forged reports are dropped a round trip earlier and never reach a counter - which is what makes `raced_total` an under-count. That same check keeps all three **attributable to the task's own assignee**, so no *unrelated* agent can move them - read that word narrowly, because attributable is not the same as honest and the next bullet is what the distinction costs you. Nothing checks the row's *status* before the write, so `duplicate_total` and `conflicting_total` are complete counts of that refusal.
- **`conflicting_total` and `duplicate_total` are forgeable by the task's own assignee, indefinitely, at one unbudgeted gRPC message per increment.** The identity gate proves the sender *is* the assignee; it does not prove the report is *true*. A terminal transition bumps neither the assignment epoch nor `worker_id`, so an agent that reports `done` at epoch N and then `failed` at epoch N passes both gates legitimately, every time, and each message adds one to `conflicting_total` (or to `duplicate_total`, if it repeats the same outcome). Nothing rate-limits status messages and this path spends no log-budget token, so the climb is smooth, silent and unbounded. **The counter counts reports, not tasks** - one misbehaving assignee on one task can dominate the whole section. And the shape it produces is *exactly* the watchdog-margin signature described above, which is where the attacker's incentive points: **before raising `RELAY_TASK_WATCHDOG_MARGIN`, confirm the value it is actually configured with and cross-check `watchdog.counts.swept_by_worker`** - if the coordinator swept nothing, nothing was recorded as a timeout and the reports contradict a sweep that never happened. Raising the margin on a forged climb widens the unbounded-assignment window the watchdog exists to close. The exposure is griefing and misdirection of the one signal, not disclosure: an agent cannot read this endpoint, and the payload carries no identifiers.
- **What `task_status_fence` does NOT cover.** It is not a census of fence rejections: the dispatcher's own `failClaimedTask` and the watchdog's own sweep write are refused by the same statement and are counted nowhere. It is **not comparable with `task_log_fence`**, which has no equivalent pre-filter - no input moves both numbers and neither explains any part of the other. And **it will not reconcile with `watchdog.counts.swept_total`**, though it is tempting to subtract them: the two are opposite ends of the same event seen from the coordinator and from the agent, and the watchdog also sweeps tasks whose agents are gone and never report at all. There is deliberately **no total** - the three keys partition the rejections, so the sum is yours to compute and is not published beside its own parts.
- **Reading `watchdog`.** `swept_total` counts assignments the coordinator's stale-task watchdog ended since `started_at`, and `swept_by_worker` splits that by worker. **Repeated sweeps against the same worker are the tell that a machine should be disabled**: the watchdog frees the worker's slot the moment it stamps `timed_out`, so a wedged or hostile agent stops holding a fixed set of tasks and starts *draining* queued work and failing it, indefinitely. "Worker X: 37" is that pattern as one number; `GET /v1/workers`, `/v1/workers/stats` and `/v1/workers/{id}/metrics` all show nothing, and the worker's `last_seen_at` stays fresh because its stream is healthy. **The server log is not a heartbeat for this.** The watchdog emits its sweep summary only when a sweep actually ended an assignment or failed a write, so on a healthy fleet it appears **never** - that gate is the whole point, since an ungated line would print "0 task(s) swept" every 60 seconds forever. When it does appear it carries the same cumulative figures, and it names the worst worker only when some worker has more than one sweep; if `swept_overflow` is non-zero it says "worst TRACKED worker" and reports how many sweeps went unattributed, because at the cap the real offender may not be in the map at all. To confirm the watchdog is alive, read the `stale-task watchdog:` line it logs unconditionally at startup, and the presence of this section on this endpoint.
- **`swept_by_worker` is capped at 256 distinct workers, first-come, and `swept_overflow` is how you know.** Worker ids are server-assigned, but their *count* is not server-limited - with `RELAY_ALLOW_AUTO_ENROLL` on, a reachable host creates one persistent worker row per hostname it claims - so an admin-facing document that serialised one key per worker on every request would be unbounded. At capacity a **new** worker's sweeps are added to `swept_overflow` instead; already-tracked workers keep counting, and `swept_total` always equals the sum of the map plus the overflow. **A non-zero `swept_overflow` means per-worker attribution is incomplete and the worst listed worker may not be the worst worker.**
- **What `watchdog` does NOT count.** It counts assignments *the coordinator* ended. An agent that honours its own timeout writes the same `timed_out` status and contributes **nothing** here. That is deliberate: the two mean opposite things about a worker's health, and the table cannot tell them apart, so this counter side-steps the ambiguity rather than resolving it. Telling them apart would need a new terminal status, or a writer column plus a migration on an epoch-fenced write path; if such a column is ever added for another reason, this counter should be replaced by a windowed query, which is better on every other axis. **Per replica**, and more so than the other sections: the watchdog is multi-replica-safe by first-write-wins, so a sweep of worker X may be counted on either replica and neither one's `swept_by_worker` is the whole story.
- **A watchdog disabled by `RELAY_TASK_WATCHDOG_MARGIN=0` and `RELAY_TASK_MAX_ASSIGNMENT=0` still reports its section, with every number zero.** Unlike `grpc_admission` with both caps off, nothing there is false - the watchdog genuinely ended nothing - but the payload cannot tell you it is switched off. The startup line does.
- **This is not a history and not an alert.** No rates, no windows, no persistence, and nobody is paged. A restart zeroes everything, which is what `started_at` is for.

### Reservations

All reservation endpoints are admin-only.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/reservations` | List reservations. Optional filter `?worker_id=<uuid>`. Paginated. |
| `POST` | `/v1/reservations` | Create a reservation |
| `DELETE` | `/v1/reservations/{id}` | Delete a reservation |

**`?worker_id=`** matches reservations whose `worker_ids` array contains that id, and
composes with `?limit=`, `?cursor=` and `?sort=`. All reservation endpoints are
admin-only, so a non-admin cannot use it at all.

An id that names no worker, or a worker no reservation targets, returns an **empty page
with `total: 0`, not a 404**. `reservations.worker_ids` is a bare `UUID[]` with no
foreign key, so a worker id can legitimately outlive its row and this endpoint cannot
authoritatively distinguish "never existed" from "deleted". `?worker_id=` with an empty
value is treated as absent, and a value that is not a UUID returns
`400 invalid worker_id; expected a UUID`. Like `?q=`, the array-containment test is
a scan and is not index-served; add a GIN index on `worker_ids` if the table grows.

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
| `GET` | `/v1/invites` | List every invite in every state (active, expired, redeemed). Paginated. |

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

**GET `/v1/invites`** returns every invite with no status filter, because redeemed and
expired invites are what the admin view exists to show. Items:

```json
{
  "id": "<uuid>",
  "created_at": "2026-08-01T10:00:00Z",
  "expires_at": "2026-08-04T10:00:00Z",
  "created_by": "<uuid>",
  "created_by_email": "admin@example.com",
  "email": "invitee@example.com",
  "used_at": "2026-08-02T09:00:00Z"
}
```

- `email` is **omitted** when the invite is not bound to an address.
- `used_at` is **omitted** when the invite has not been redeemed. Its presence is the
  complete and terminal test for "redeemed".
- No `status` field is returned. Derive the pill client-side: redeemed (`used_at`
  present, checked first), expired (`expires_at <= now`), expiring (`expires_at - now`
  under one hour), otherwise active. A server-asserted status would be stale the moment
  the row is on screen.
- No token, token hash, or token prefix is returned. The raw token exists exactly once,
  in the `POST` response; only its SHA-256 is stored, and the list query never selects it.

### Scheduled Jobs

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/scheduled-jobs` | Create a scheduled job |
| `GET` | `/v1/scheduled-jobs` | List scheduled jobs (own schedules; admins see all). Optional filters `?enabled=` and `?q=` - see [Filtering the schedules list](#filtering-the-schedules-list). Paginated. |
| `GET` | `/v1/scheduled-jobs/stats` | Summary counts for the caller's schedules (fleet-wide for admins). Authenticated, not admin-only. |
| `GET` | `/v1/scheduled-jobs/{id}` | Get a scheduled job |
| `PATCH` | `/v1/scheduled-jobs/{id}` | Update a scheduled job |
| `DELETE` | `/v1/scheduled-jobs/{id}` | Delete a scheduled job |
| `POST` | `/v1/scheduled-jobs/{id}/run-now` | Fire the schedule immediately (owner or admin) |

**`last_job_status`.** Both `GET /v1/scheduled-jobs` and `GET /v1/scheduled-jobs/{id}`
carry `last_job_status`, the status of the job `last_job_id` names, taken verbatim from
the job's own vocabulary rather than restated here. It agrees with `status` on
`GET /v1/jobs/{id}` and is deliberately not the `pending` to `queued` rename that
`GET /v1/jobs/stats` performs.

It is **present exactly when `last_job_id` is present** - the two keys appear together
or neither appears. Absent means the schedule has never had a fire that produced a job.
It never means "unknown" and it never means "healthy": a failed lookup is a `500`, not a
silently absent key.

**A `run-now` job does not move it.** `POST /v1/scheduled-jobs/{id}/run-now` creates a
job carrying `scheduled_job_id` but does not update `last_job_id` or `last_run_at`, so
after an interactive run `last_job_status` still describes the previous scheduled fire.

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

**GET `/v1/scheduled-jobs/stats`** returns a census of the caller's schedules, fleet-wide
for admins and scoped to `owner_id` for everyone else. It requires authentication but
**not** admin.

```json
{
  "enabled": 12,
  "paused": 3,
  "total": 15,
  "failed_runs_24h": 2,
  "failing": 1
}
```

| Field | Definition |
|-------|------------|
| `enabled` | Schedules in scope with `enabled = true`. |
| `paused` | Schedules in scope with `enabled = false`. `paused` is exactly `NOT enabled`; there is no third state and no `paused` column. |
| `total` | `enabled + paused`, computed from the two buckets so the identity always holds. |
| `failed_runs_24h` | Jobs a schedule in scope produced that have `status = failed` and were last updated within 24 hours, **including jobs created by `run-now`** - the count is over jobs carrying a `scheduled_job_id`, however they were started. |
| `failing` | Schedules in scope carrying a `last_error`. **Not windowed.** |

All five keys are always present and all five are non-negative integers; there is no
`omitempty` in this response.

`failed_runs_24h` **excludes `cancelled`**, which is why it is not named `failed_24h`
like the field in `GET /v1/jobs/stats`. A cancelled job is an operator action, not a
schedule fault, and a strip that flags one teaches the operator to ignore the strip. It
is windowed on the job's `updated_at`, the same finish-time proxy `GET /v1/jobs/stats`
uses, so the two "in the last 24 hours" numbers on this product's two summary strips mean
the same thing.

`failing` is separate from `failed_runs_24h` and is counted in a different unit. A spawn
failure recorded in `last_error` never becomes a job, so it is invisible to any count over
jobs, and `last_error` records only the most recent failure, so a schedule that failed 48
times contributes one.

**`/stats` accepts no filters.** It is always the whole in-scope census, unaffected by
`?enabled=` and `?q=`, so `stats.total` equals the list's `total` only when no filter is
active. Read `total` off the list for "N matching" and off `/stats` for the strip.

#### Filtering the schedules list

`GET /v1/scheduled-jobs` accepts two optional filters. They AND together and compose with
`?limit=`, `?cursor=` and `?sort=`.

| Parameter | Format | Absent means |
|-----------|--------|--------------|
| `enabled` | `true` / `false` (Go `strconv.ParseBool` spellings). A genuine tri-state: **`enabled=false` means "only paused schedules"**, not the same as absent, which is where it differs from `?mine=` on the jobs list. An empty value (`?enabled=`) is treated as absent. | No enabled filter |
| `q` | Exactly the rules in [Filtering the jobs list](#filtering-the-jobs-list) - case-insensitive substring, `%` and `_` literal, maximum 200 characters, empty or whitespace-only treated as absent - matched against three axes here: the schedule `name`, the owner's `email`, and the `cron_expr`. | No text filter |

The cron axis matches the stored text verbatim, so `@daily` is found by `daily` and
`0 4 * * *` is found by `0 4`. For a non-admin the email axis can only ever match
all-or-nothing, because every schedule in scope has the same owner.

`total` counts every row matching every active filter, so the page footer's denominator
always belongs to the same set as the rows. It is **not** the same number as
`stats.total`, which is the unfiltered census.

**Errors.** These are `400` with the body `{"error": "<message>"}`. The query-string rules
that apply to every paginated endpoint - malformed input, repeated parameters and NUL
bytes - are under [Query-string validation](#query-string-validation).

| Condition | Message |
|-----------|---------|
| `enabled` is not a boolean | `invalid enabled; expected true or false` |
| `q` is longer than 200 characters | `q is too long; maximum 200 characters` |
| `q` is not valid UTF-8 | `q is not valid UTF-8` |

The two `q` bodies are byte-identical to the jobs list's, and a test asserts that.

**Drop the cursor when a filter changes**, exactly as on the jobs list. A cursor carries
no record of the filters that were active and the server does not reject a mismatched
one; filter correctness is nevertheless cursor-independent, so a stale cursor can start a
page at a surprising position but can never return a row that fails the current filters.

`?q=` costs what it costs on the jobs list, for the same reason and with the same
advice: see [`?q=` cost](#filtering-the-jobs-list).

### Events (Server-Sent Events)

```
GET /v1/events                                  # all job/task/worker status changes
GET /v1/events?job_id=<id>                      # status changes for one job
GET /v1/events?task_id=<id>                     # live log lines for one task
GET /v1/events?job_id=<id>&task_id=<id>         # both, on one connection
```

Authenticated (bearer token), one held connection per subscription.

The server never ends the stream on its own: it holds the connection until the
client disconnects or the subscriber is dropped for falling behind. There is no
terminal event, and a `?task_id=`-only subscription receives no status frames at
all, so add `job_id` if you need to know when to stop tailing.

**Event types**

| Type | Payload | Delivered to |
|---|---|---|
| `job` | `{"id": "...", "status": "..."}` | subscriptions with no `job_id`, or a matching `job_id` |
| `task` | `{"id": "...", "status": "..."}` | subscriptions with no `job_id`, or a matching `job_id` |
| `worker` | `{"id": "...", "status": "..."}` | subscriptions with no `job_id` (worker events carry no job scope) |
| `task_log` | `{"task_id","job_id","seq","stream","content","created_at"}` | **only** subscriptions that named that exact `task_id` |
| `dropped` | `{"reason":"slow_consumer"}` | the one subscription being closed, as its final frame |

Log events are opt-in and per-task: there is no job-wide or cluster-wide log
firehose, so a plain `GET /v1/events` never receives `task_log` frames. A
subscription that supplies only `task_id` receives log frames and no status
events.

`seq`, `stream`, `content` and `created_at` are identical in name and type to
the items returned by `GET /v1/tasks/{id}/logs`, so one client-side type covers
both surfaces. `seq` is a per-task total order.

**Backfilling a task log without a gap or a duplicate - do these in order:**

1. Open the SSE subscription **first** and start buffering `task_log` events.
   The subscription is live by the time the response returns 200.
2. Then page `GET /v1/tasks/{id}/logs?since_seq=0`, repeating with
   `since_seq=<next_seq>` until `next_seq` is `0`. Record the highest `seq` seen
   as `maxSeq`.
3. Render the backfill, then apply the buffered and subsequent events,
   discarding any event whose `seq <= maxSeq`.

Reversing steps 1 and 2 leaves a hole between the last page and the first event.

**Opening at the tail instead.** Step 2 walks the whole history, which on a long
log is many requests for output the reader did not ask for. To show the END of
the log instead, keep step 1 exactly as it is - subscribe FIRST, the ordering is
the whole guarantee - and replace step 2 with a single
`GET /v1/tasks/{id}/logs?order=desc&limit=200`. State the window this gives you
precisely, because it is NOT the same guarantee as the forward walk: you hold
the newest `limit` entries as of the read, plus everything appended after it.
Anything older is reachable only through `?order=desc&before_seq=`. That
includes chunks written between the subscribe and the read: if more than
`limit` newer chunks land in that window, an earlier one is pushed below the
page, and the `seq <= maxSeq` rule then discards it from the replay, so it is
in neither. It is not lost - it is simply older history, fetched on demand like
the rest. Say so in the UI rather than implying the log is complete. See "Task
log paging" under the Tasks endpoints.

**`dropped` and reconnection.** If a subscriber stops reading, the server closes
its 64-slot buffer rather than blocking the producer, and writes one final
`event: dropped` frame. The database remains the source of truth, so recover by
re-running step 2 with `since_seq=<last seq seen>`. There is no `id:` /
`Last-Event-ID` resume.

A `?job_id=&task_id=` subscription is a single channel with a single 64-slot
buffer shared by both event families, so a burst of log lines can drop the whole
connection - including its status frames. Recover by re-backfilling the log
**and** refetching job/task state, not just the log.

`seq` values increase but are **not** contiguous: they come from a table-wide
sequence shared by every task, so any other task logging concurrently consumes
ids. A gap in `seq` is therefore **not** a drop signal - do not re-page on one.
The `dropped` frame and an unexpectedly closed stream are the only drop signals.

**Validation.** `?task_id=` returns `400` for a malformed UUID and `404` for an
unknown task. `?job_id=` is not validated - an unknown or unparseable job id
yields an open but permanently empty stream rather than an error. This
asymmetry is deliberate: `?job_id=` is an existing contract with existing
clients, while an unvalidated `?task_id=` would look identical to "this task
produced no output".

**Normalisation.** The asymmetry above is about REJECTION only. Both parameters
are canonicalised. Any spelling the server accepts - uppercase hex, the dashless
32-character form, and the **36-byte** form with any byte in the four separator
positions - is normalised to the lowercase dashed form the server emits, so
`?job_id=7E660488-1234-4321-8888-ABCDEFABCDEF` follows the same job as the
canonical spelling rather than a filter nothing matches. A spelling the server
does not accept is passed through unchanged and is never widened into one it
does accept.

Two consequences of that grammar, both measured, because "36 characters" and
"the job it names" are each wrong for a case the grammar admits:

- **The length test is over BYTES, not characters.** Replace two hex positions
  with a single two-byte rune and the string is 36 *characters* but 37 *bytes*,
  so it misses the 36-byte branch entirely and is passed through untouched.
  Conversely `?job_id=7e660488%FF1234-4321-8888-abcdefabcdef` decodes to 36
  bytes and *is* canonicalised, though those 36 bytes are not valid UTF-8 and
  so are not a 36-character string in any useful sense. The dashless form above
  is unambiguous because all 32 of its positions must be ASCII hex, so there
  bytes and characters coincide. This paragraph deliberately contains no
  non-ASCII literal: an earlier revision wrote one as a raw Latin-1 byte, which
  made this file invalid UTF-8 AND made the example 36 bytes - accepted, the
  opposite of what the sentence around it claimed.
- **The four separator bytes are discarded UNEXAMINED**, so a 36-byte spelling
  can name a job only up to those four bytes:
  `7e660488a1234b4321c8888dabcdefabcdef` canonicalises to
  `7e660488-1234-4321-8888-abcdefabcdef`, silently dropping the `a`, `b`, `c`
  and `d`. This is not new and not a scoping hole - `GET /v1/jobs/{id}` has
  always resolved that same string to that same job - but it means an accepted
  spelling follows the job the *parser* reads out of it, which is not always the
  one a reader would say it names.

**Single-process caveat.** The broker is in-memory, so events are visible only
to clients connected to the `relay-server` process that owns the relevant
agent's gRPC stream. Behind a load balancer with more than one replica, live
delivery degrades to best-effort while the polling endpoints stay correct. This
already applies to status events; a live log view just makes it more visible.

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
