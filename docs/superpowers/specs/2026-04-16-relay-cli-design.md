# Relay CLI Design

**Date:** 2026-04-16
**Status:** Approved

---

## Overview

`relay` is the client CLI binary for the relay render farm. It wraps the coordinator's REST API, allowing users to submit jobs, tail logs, inspect state, and manage workers and reservations. This is Plan 4 of the relay render farm build.

---

## Architecture

```
cmd/relay/main.go          — parse os.Args, build registry, dispatch
internal/cli/
  command.go               — Command struct, registry, top-level dispatch
  client.go                — HTTP client wrapper (base URL, Bearer auth, JSON helpers)
  config.go                — load/save config file; env var overrides
  login.go                 — relay login
  jobs.go                  — relay submit / list / get / cancel
  logs.go                  — relay logs
  workers.go               — relay workers list / get
  reservations.go          — relay reservations list / create / delete
```

### Command Interface

```go
type Command struct {
    Name  string
    Usage string
    Run   func(ctx context.Context, args []string, cfg *Config) error
}
```

`main.go` builds a flat registry of top-level commands. Nested subcommands (`workers list`, `reservations create`) are dispatched inside their own `Run` func with a local `flag.FlagSet` — no recursive registry needed at this scale.

### HTTP Client

`client.go` wraps `*http.Client` with `base URL` and `Authorization: Bearer <token>` baked in. All command `Run` funcs receive a `*Client` constructed from `*Config`. Commands never build raw HTTP requests directly.

---

## Config & Auth

**Config file locations:**
- Linux/macOS: `~/.relay/config.json`
- Windows: `%APPDATA%\relay\config.json`

**Schema:**
```json
{ "server_url": "http://localhost:8080", "token": "<rawHex>" }
```

**Resolution order** (later overrides earlier):
1. Config file
2. `RELAY_URL` env var (overrides `server_url`)
3. `RELAY_TOKEN` env var (overrides `token`)

Config is loaded once at startup in `main.go` and passed to every command. Commands that require auth fail with `"no token configured — run 'relay login' first"` if the token is empty.

### `relay login` Flow

1. Prompt for Server URL (pre-filled from current config if present)
2. Prompt for Email
3. `POST /v1/auth/token` → `{ token, expires_at }`
4. Write `server_url` + `token` to config file (create file and parent dirs if needed)
5. Print `"Logged in. Token expires <date>."`

`relay config` subcommand is out of scope for v1. `relay login` is the only mechanism for writing the config file.

---

## Subcommands

| Command | HTTP | Notes |
|---|---|---|
| `relay login` | `POST /v1/auth/token` | Interactive prompts, writes config |
| `relay submit <job.json>` | `POST /v1/jobs` | Blocks + streams logs by default; `--detach` prints job ID and exits |
| `relay list [--status <s>]` | `GET /v1/jobs` | Tabular: ID, name, status, created |
| `relay get <job-id>` | `GET /v1/jobs/{id}` | Job detail + task table |
| `relay cancel <job-id>` | `DELETE /v1/jobs/{id}` | Prints confirmation or error |
| `relay logs <job-id>` | `GET /v1/events?job_id=<id>` | Tails SSE; exits on terminal job state |
| `relay workers list` | `GET /v1/workers` | Tabular: ID, status, labels |
| `relay workers get <id>` | `GET /v1/workers/{id}` | Detail view |
| `relay reservations list` | `GET /v1/reservations` | Tabular |
| `relay reservations create <res.json>` | `POST /v1/reservations` | JSON file input |
| `relay reservations delete <id>` | `DELETE /v1/reservations/{id}` | Prints confirmation |

### Job Submission Format

Input is a JSON file passed as the sole positional argument:

```json
{
  "name": "my-render",
  "priority": "normal",
  "labels": { "project": "film-x" },
  "tasks": [
    {
      "name": "render-frame-001",
      "command": ["blender", "-b", "scene.blend", "-f", "1"],
      "env": { "BLENDER_USER_SCRIPTS": "/opt/scripts" },
      "requires": { "gpu": "true" },
      "timeout_seconds": 3600,
      "retries": 1,
      "depends_on": []
    }
  ]
}
```

### `relay submit` Blocking Mode

After `POST /v1/jobs` succeeds, the CLI:
1. Opens `GET /v1/events?job_id=<id>` SSE stream
2. Prints each log chunk prefixed: `[task-name stdout]` or `[task-name stderr]`
3. Exits code 0 when job status event carries `done`
4. Exits code 1 when job status carries `failed` or `cancelled`
5. If SSE stream drops before a terminal state: prints `"connection lost — job <id> may still be running"`, exits code 1

`--detach` skips steps 1–5 and prints only the job ID.

### Output

- Default: plain text tables, no color, no external dependency
- `--json` flag on any read command (`list`, `get`, `workers list`, etc.) emits raw JSON for scripting

---

## Error Handling

| Condition | Behavior |
|---|---|
| HTTP 4xx | Print `error` field from response body; exit 1 |
| HTTP 5xx | Print `"server error (<code>) — try again"`; exit 1 |
| Network error | Print raw error message; exit 1 |
| SSE stream drop (submit blocking) | Print `"connection lost — job <id> may still be running"`; exit 1 |
| Missing token | Print `"no token configured — run 'relay login' first"`; exit 1 |
| Bad JSON input file | Print `"invalid JSON: <err>"`; exit 1 |

---

## Testing

- Each `Command.Run` receives a `*Client` whose underlying `http.Client` is pointed at an `httptest.Server` in tests — no mocks, no interfaces needed.
- `config.go` unit tests cover: file only, env only, env overrides file, missing file (returns zero-value config, no error).
- `relay submit` blocking mode: fake SSE server emits a `done` job status event; test asserts exit code 0 and correct log prefix output.
- No integration tests (consistent with rest of codebase — those require Docker).

---

## Known v1 Limitations

- No `relay config set` command — config file can only be written via `relay login`
- No shell completion
- No color output
- `relay workers get` has no PATCH support in the CLI (admin update is server-side only in v1)
