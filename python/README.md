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
| `list_jobs(status=, scheduled_job_id=, sort=, limit=)` | GET `/v1/jobs`, auto-paginating. `limit` caps the TOTAL jobs returned. |
| `list_jobs_page(..., cursor=)` | One page of `/v1/jobs` as a `Page[Job]`. Here `limit` is the PAGE SIZE (1-200). |
| `cancel_job(id, force=False)` | DELETE `/v1/jobs/{id}` - graceful by default; `force=True` requests an immediate kill on the agent. The returned `Job` carries **no** task list; call `get_tasks` for that. |
| `get_tasks(job_id)` | GET `/v1/jobs/{id}/tasks`. |
| `get_task(id)` | GET `/v1/tasks/{id}`. |
| `task_logs(id, limit=)` | GET `/v1/tasks/{id}/logs`, auto-paginating to the end of the log. `limit` caps the TOTAL records. See the note below. |
| `task_logs_page(id, since_seq=, limit=)` | One page of a task's log as a `LogPage`. Pass the returned `next_seq` back as `since_seq=` **verbatim** - the cursor is exclusive. |
| `follow_job(id)` | Iterator over SSE `Event` objects. The server does **not** end the stream when the job finishes - see Following a job. |
| `wait(id, timeout=None, poll_interval=1.0)` | Block (polling `GET /v1/jobs/{id}`) until the job is terminal. |
| `create_schedule(...)` | POST `/v1/scheduled-jobs`. |
| `list_schedules(sort=, limit=)` | GET `/v1/scheduled-jobs`, auto-paginating. |
| `list_schedules_page(sort=, limit=, cursor=)` | One page as a `Page[ScheduledJob]`. |
| `get_schedule(id)` / `update_schedule(id, ...)` / `delete_schedule(id)` | GET / PATCH / DELETE `/v1/scheduled-jobs/{id}`. |
| `run_schedule_now(id)` | POST `/v1/scheduled-jobs/{id}/run-now`. Owner or admin only. |
| `list_workers(sort=, limit=)` / `list_workers_page(sort=, limit=, cursor=)` | GET `/v1/workers`. |
| `list_users(sort=, limit=)` / `list_users_page(sort=, limit=, cursor=)` | GET `/v1/users`. Admin-only. |
| `list_reservations(sort=, limit=)` / `list_reservations_page(sort=, limit=, cursor=)` | GET `/v1/reservations`. Admin-only. |
| `list_agent_enrollments(sort=, limit=)` / `list_agent_enrollments_page(sort=, limit=, cursor=)` | GET `/v1/agent-enrollments`. Admin-only. |
| `close()` | Close the SDK-owned HTTP client. A no-op when you passed `http_client=`. |

### Reading a task's log

`task_logs(id)` walks every page and returns the whole log as a list. It always
requests 200 rows per page; without an explicit limit the server's default is 50
and a long log would be silently truncated. It accumulates the whole log in
memory - `relay logs` does not, because it prints each page as it arrives, and
the SDK cannot do that while returning a list. On a very large log, page by hand:

```python
since = 0
while True:
    page = client.task_logs_page(task_id, since_seq=since, limit=200)
    for record in page.items:
        print(record.content, end="")
    if page.next_seq == 0:      # the server says the log is drained
        break
    since = page.next_seq       # VERBATIM - the cursor is exclusive
```

A server that cannot be paged raises `ProtocolError`: an empty page that still
advertises more rows, a cursor that does not advance, or a log that never
reports itself drained within 10000 pages.

## Following a job

`follow_job(id)` yields `Event` objects as the server publishes them. **The
server does not end the stream when the job reaches a terminal state.**
`handleEvents` closes on exactly two conditions - the client goes away, or the
broker drops a subscriber for falling behind - and it has no notion of job
terminality. The SDK sets no read timeout on the stream, so a caller that
simply iterates to exhaustion blocks forever after the job is done. Break out
yourself:

```python
terminal = {relay.JobStatus.DONE, relay.JobStatus.FAILED, relay.JobStatus.CANCELLED}
for event in client.follow_job(job_id):
    if event.type == relay.EventType.DROPPED:
        # The broker dropped this subscriber for falling behind. Frames
        # published in the gap are gone; re-read the job and the task logs.
        break
    if event.type == relay.EventType.JOB and event.data.get("status") in terminal:
        break
```

Or use `wait(id)`, which polls `GET /v1/jobs/{id}` and returns the terminal
`Job`. Polling is immune to both of the above.

## Errors

Errors raised by the SDK's own request handling descend from `relay.RelayError`.
Response DECODING is the exception: a body that does not match the model raises
`pydantic.ValidationError`, which does **not** descend from `RelayError`. That
gap is known and tracked separately.

| Class | When |
|---|---|
| `ValidationError` | Local Pydantic failure or server 400 |
| `AuthError` | Missing token, 401, or 403 |
| `NotFound` | 404 |
| `Conflict` | 409 (e.g. cancelling a terminal job) |
| `ServerError` | 5xx |
| `HTTPError` | Any other unexpected status |
| `ProtocolError` | A 200 that is not a usable relay response: an empty page advertising more rows, a cursor that does not advance, or a log that never reports itself drained |
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
