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
| `task_logs(id, limit=)` | GET `/v1/tasks/{id}/logs`, auto-paginating to the end of the log. `limit` caps the TOTAL records and must be >= 1. See the note below. |
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

On every auto-paginating method above - the seven that take a total-capping
`limit=`, which is `task_logs` plus the six `list_*` - **`limit` must be >= 1**.
A `limit` of 0 or below raises `ValidationError` locally, before any request.
It is rejected rather than passed on because those walks end in `out[:limit]`,
and Python slice semantics would turn a negative limit into "everything but
the last N rows": a plausible-looking short list rather than an error. This
does not apply to the `*_page` siblings, where `limit` is the page size and
travels to the server, which answers 400 for anything outside 1-200.

Every one of those seven walks is driven by a cursor the **server** chooses, and
the provenance of a value says nothing about who controls its content. So each
has three stops beyond the server's own drained signal, and a server that trips
one raises `ProtocolError`: a page that carries no rows while still advertising
more, a cursor that does not advance, and a client-side cap of 10000 requests.

The middle stop is **two different mechanisms**, and which one you get depends on
the walk. The six `list_*` methods page on an opaque base64 cursor that carries
no order, so they keep a **set** of every cursor already requested: both a repeat
and a two-cycle (`A,B,A,B`, which two replicas behind a load balancer produce)
stop, and the message says the server "repeated a cursor this walk had already
requested". `task_logs` pages on an integer `next_seq` and so requires **strict
advance** - `next_seq <= since_seq` stops - with the message "server cursor did
not advance (next_seq N after since_seq M)". For integers, monotonicity already
subsumes repetition, so the outcomes coincide and the mechanisms do not. Do not
"unify" `task_logs` onto a set on the strength of this paragraph.

The cap bounds **requests** and nothing else - not wall clock, not response
bytes, not the memory of one response, for the reasons measured under "Reading a
task's log" below. The rows collected before a walk was abandoned are on the
exception:

```python
try:
    jobs = client.list_jobs()
except relay.ProtocolError as e:
    jobs = e.records          # never None; [] if nothing was collected
    print(f"partial list ({len(jobs)} jobs): {e}")
```

A list with **no matching rows is not an error** - it answers `items: []` with an
empty cursor, which is the drained signal, and `list_jobs()` returns `[]`.

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
reports itself drained within 10000 pages. That page cap bounds the number of
REQUESTS and nothing else. Not wall clock: httpx's read timeout is per socket
read, not per request, so a server that answers steadily enough need not trip
it (measured: one request completed in 14.3 s under a 0.5 s read timeout), and
that is per page. Not bytes: gzip is decoded unbounded (measured: 89 KiB on
the wire, 31 MB in memory). Not memory: a full 2,000,000-row walk retains well
over a gigabyte.

Only the third of those is bounded by anything the SDK offers, and that is
`limit=`. **httpx has no total-time and no response-size setting**, so
`Client(timeout=)` cannot close the other two: it sets httpx's four
per-operation timeouts (`connect`, `read`, `write`, `pool`) and a per-read
bound is exactly what the 14.3 s measurement defeats. Closing the wall-clock
or byte axis takes a caller-supplied `httpx.BaseTransport` wrapper passed as
`http_client=`, or a deadline enforced outside the SDK.

The records collected before the walk was abandoned are on the exception, so a
caller keeps the partial log and the reason it is partial:

```python
try:
    logs = client.task_logs(task_id)
except relay.ProtocolError as e:
    logs = e.records          # never None; [] if nothing was collected
    print(f"partial log ({len(logs)} records): {e}")
```

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
        # published in the gap are gone; re-read the job with get_job(), and
        # resume each task's log with task_logs_page(id, since_seq=last_seq).
        # Not task_logs(), which has no since_seq and restarts at row 0.
        break
    if event.type == relay.EventType.JOB and event.data.get("status") in terminal:
        break
```

Or use `wait(id)`, which polls `GET /v1/jobs/{id}` and returns the terminal
`Job`. Polling is immune to both of the above.

## Errors

Errors raised by the SDK's own request handling descend from `relay.RelayError`.
Response DECODING is the exception, and it escapes in two ways, neither of
which descends from `RelayError`. Two is the whole list only for the bodies that
go through a model in ONE PIECE - every paged envelope now does, which is what
`_fetch_all` did not do until it was routed through `Page[model]`. A decode that
hand-picks fields out of the raw result and uses them untyped adds `TypeError`
and `KeyError`, and the claim has to be scoped because one such decode remains.

Counted across `relay/`, twelve call sites read `response.json()`. Ten hand the
whole body to a model. Two do not, and only one of them widens this list:

- `get_tasks(job_id)` iterates the raw result
  (`[Task.model_validate(item) for item in response.json()]`), so a 200 whose
  body is not a JSON array raises `TypeError` - exactly the escape class the
  paragraph above says the paged envelopes no longer have. `handleListTasks`
  builds `make([]taskResponse, len(tasks))`, so a correct server never sends
  one; that is a statement about the server, not about the decode, and it is
  tracked separately.
- `_extract_message` in `errors.py` reads `payload.get("error")`, but every step
  is guarded - `try/except ValueError` around the parse, `isinstance` on both
  the payload and the field - and it falls back to `response.text`. It cannot
  raise, so it adds nothing here.

The two escapes are:

- a body that is well-formed JSON but does not match the model raises
  `pydantic.ValidationError`;
- a body that JSON itself cannot read raises a plain `ValueError` first, before
  any model is reached - `json.JSONDecodeError` for a 200 whose body is not
  JSON at all (an ingress or proxy returning an HTML error page), and
  `ValueError` for an integer over CPython's 4300-digit limit.

That gap is known and tracked separately.

| Class | When |
|---|---|
| `ValidationError` | Local Pydantic failure or server 400 |
| `AuthError` | Missing token, 401, or 403 |
| `NotFound` | 404 |
| `Conflict` | 409 (e.g. cancelling a terminal job) |
| `ServerError` | 5xx |
| `HTTPError` | Any other unexpected status |
| `ProtocolError` | A 200 that is not a usable relay response, raised by **any** cursor walk - `task_logs` and all six `list_*` methods: an empty page advertising more rows, a cursor the walk already requested, or a walk that never reports itself drained within the client's page cap. Carries `.records` (whatever that walk collected - log records from `task_logs`, resource models from `list_*`) instead of `.response` |
| `TimeoutError` | `wait()` exceeded its wall-clock limit |

`.response` carries the originating `httpx.Response`, but only where there was
one, so it has three states and not two:

- **The response**, whenever the error came from a server reply - every row
  above raised from a status.
- **`None`**, on the rows that also have LOCAL raise sites. `ValidationError`
  is raised locally for a spec that fails `validate_spec()` or a `limit` below
  1; `AuthError` is raised locally when no token is configured. Both reach the
  caller before any request is made.
- **Absent entirely** on `ProtocolError` and `TimeoutError` - they have no
  `.response` attribute at all, so reading it is an `AttributeError`, not
  `None`. `ProtocolError` carries `.records` in its place.

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
