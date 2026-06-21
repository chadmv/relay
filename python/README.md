# relay-jobs

Python SDK for relay job submission. See full quickstart below.

## Install

```bash
pip install relay-jobs
```

## Quickstart

The SDK reads its configuration from (in order) explicit kwargs, the
environment variables `RELAY_URL` / `RELAY_TOKEN`, and the CLI config file
at `~/.relay/config.json` (or `%APPDATA%\relay\config.json` on Windows).
The simplest path is to run `relay login` once, then import and go:

```python
import relay

job = relay.Job(name="nightly-cook", priority=relay.Priority.HIGH)
cook = job.add_task("cook", commands=[["ue4-cook", "--map", "Main"]], retries=2)
job.add_task("test", commands=[["pytest"]], depends_on=[cook])

with relay.Client() as client:
    submitted = client.submit(job)
    print(f"submitted {submitted.id}")
    final = client.wait(submitted.id, timeout=600)
    if final.status != relay.JobStatus.DONE:
        raise RuntimeError(f"job ended {final.status}")
```

## Authoring

`relay.Job` and `relay.Task` are Pydantic models that mirror the server's
`JobSpec` exactly. You can use the `add_task` builder:

```python
job = relay.Job(name="example")
job.add_task(
    "build",
    commands=[["make", "build"]],
    env={"GOOS": "linux"},
    requires={"gpu": "true"},
    timeout_seconds=3600,
    retries=1,
    source=relay.Source(
        type="perforce",
        stream="//depot/main",
        sync=[relay.Sync(path="//depot/main/...", rev="#head")],
        unshelves=[12345],
    ),
)
```

Or construct from a dict if you already have a JSON spec:

```python
job = relay.Job.model_validate(spec_dict)
```

`depends_on` accepts `Task` instances or names:

```python
a = job.add_task("a", commands=[["echo", "1"]])
b = job.add_task("b", commands=[["echo", "2"]], depends_on=[a])  # or ["a"]
```

## Client API

| Method | Description |
|---|---|
| `submit(job)` | POST `/v1/jobs`. Validates locally, returns the populated `Job`. |
| `get_job(id)` | GET `/v1/jobs/{id}`. |
| `list_jobs(status=, scheduled_job_id=)` | GET `/v1/jobs` with optional filters. |
| `cancel_job(id, force=False)` | DELETE `/v1/jobs/{id}` — graceful by default; `force=True` requests an immediate kill on the agent. |
| `get_tasks(job_id)` | GET `/v1/jobs/{id}/tasks`. |
| `get_task(id)` | GET `/v1/tasks/{id}`. |
| `task_logs(id)` | GET `/v1/tasks/{id}/logs`. |
| `follow_job(id)` | Iterator over SSE `Event` objects until the job is terminal. |
| `wait(id, timeout=None, poll_interval=1.0)` | Block (polling) until the job is terminal. |
| `create_schedule(...)` | POST `/v1/scheduled-jobs`. |
| `list_schedules() / get_schedule(id) / update_schedule(id, ...) / delete_schedule(id)` | Standard CRUD. |
| `run_schedule_now(id)` | POST `/v1/scheduled-jobs/{id}/run-now`. Allowed for the schedule's owner or an admin. |

## Errors

All exceptions descend from `relay.RelayError`:

| Class | When |
|---|---|
| `ValidationError` | Local Pydantic failure or server 400 |
| `AuthError` | Missing token, 401, or 403 |
| `NotFound` | 404 |
| `Conflict` | 409 (e.g. cancelling a terminal job) |
| `ServerError` | 5xx |
| `HTTPError` | Any other unexpected status |
| `TimeoutError` | `wait()` exceeded its wall-clock limit |

The original `httpx.Response` is attached as `.response` on each instance
for debugging.

## Compatibility

- **Python**: 3.9, 3.10, 3.11, 3.12, 3.13.
- **Server**: tested against relay-server `main`. The SDK only consumes
  the existing v1 REST + SSE surface — no server-side changes needed.

## Development

From the `python/` directory:

```bash
python -m venv .venv && .venv/Scripts/python -m pip install -e ".[dev]"
pytest tests/unit
RELAY_INTEGRATION=1 pytest tests/integration   # requires a running relay-server
```

Or run from the repo root via `make python-test` and
`make python-test-integration`.
