# Python SDK task_logs envelope drift, plus a whole-SDK shape sweep

Date: 2026-08-26
Status: design approved (autonomous gate mode)
Backlog item: `docs/backlog/bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys.md`
Owner phase: Phase 1 (spec) -> `relay-planner` -> a Python-capable engineer under TDD
Related: [2026-06-03-python-sdk-pagination-design](2026-06-03-python-sdk-pagination-design.md) (the
predecessor fix that missed this endpoint), [2026-08-26-relay-logs-envelope-drift](2026-08-26-relay-logs-envelope-drift.md)
(the same defect in the Go CLI, shipped last week as PR #155 / `a192532`)

## Problem

`Client.task_logs()` (`python/src/relay/client.py`, the comprehension on line 266) does:

```python
return [LogRecord.model_validate(item) for item in response.json()]
```

`handleGetTaskLogs` (`internal/api/tasks.go:132`) has written `{"items": [...], "next_seq": N,
"total": N}` since 2026-05-08. Iterating a dict in Python yields its KEYS, so the comprehension
walks the three strings `"items"`, `"next_seq"`, `"total"` and validates each as a `LogRecord`.

**This is now executed, not read off the code.** The backlog item's "Not measured" caveat is closed.
Against a `MockTransport` returning the real envelope, `Client.task_logs()` raises:

```
pydantic_core.ValidationError:
  Input should be a valid dictionary or instance of LogRecord
  [type=model_type, input_value='items', input_type=str]
```

`pydantic.ValidationError` is an alias of `pydantic_core.ValidationError`, so the item's predicted
exception class was right. The plan should assert on `input_value='items'` and `type=model_type`,
not just on the exception class, because a bare `pytest.raises(ValidationError)` would also pass
against a fixture that is malformed for some unrelated reason.

**Measurement provenance.** The repro above was executed by the conductor against `python/.venv`
(Python 3.13; baseline `93 passed`). **This spec session had no shell** - the Bash tool is disabled -
so I inherit that one measurement and did not re-run it. Everything else in this document was
verified by reading the tree at `a192532`, and where I could not verify something by reading I say
so in place rather than asserting it.

Three defects compound, exactly as they did in the CLI:

1. **Shape.** The comprehension iterates the envelope's keys.
2. **Completeness.** `next_seq` is ignored, and `task_logs()` sends no `?limit=` at all, so even a
   corrected decode returns at most the server's default page of **50 rows** and silently truncates
   every longer log. `web/src/jobs/api.ts:157` carries a comment about this exact trap ("Always
   sends an explicit limit so the caller is never silently truncated to the server default of 50").
3. **Invisibility.** Unlike the CLI, the Python failure is LOUD - it raises. It survived for a
   different reason, and that reason is in the next section.

## What was verified, and what the backlog item got wrong

The item is correct on the defect itself. Seven of its supporting claims are wrong, stale, or
prescribe something that cannot be done as written.

### Confirmed

- The comprehension and the envelope both resolve. `logEntry` is `{seq, stream, content,
  created_at}`; the envelope keys are `items` / `next_seq` / `total`.
- `since_seq` is **exclusive**: `GetTaskLogsPage` is `WHERE task_id = $1 AND id > $2 ORDER BY id
  LIMIT $3` (`internal/store/query/tasks.sql:682-688`). The cursor is the previous page's `next_seq`
  VERBATIM, never `+1`.
- `next_seq` is the last returned row's id, overwritten with `0` whenever `len(items) < limit`. A
  full page therefore always carries a non-zero cursor, including the page that happens to exhaust
  the table.
- `test_task_logs_parses_records` (`python/tests/unit/test_client.py:184`) hand-writes a bare array.
  It asserts the SDK agrees with its own fixture. Confirmed.
- The predecessor item was closed `fixed` by the 2026-06-03 pagination work, and `task_logs()` was
  not touched by it. Confirmed by reading that spec's file list.

### Refuted or corrected

1. **"Three clients call this endpoint and two are broken" / "`internal/mcp/task_logs.go:46` is the
   third client."** Both are wrong, and this is a uniqueness claim, so it cannot be checked by
   opening the three named. I searched for the SHAPE (`/logs` and `next_seq` across
   `*.{go,py,ts,tsx,js}`) and counted the hits. There are **four** production clients:

   | Client | File | Verdict |
   | --- | --- | --- |
   | Go CLI | `internal/cli/logs.go` | correct since `a192532` (2026-08-26) |
   | MCP | `internal/mcp/task_logs.go` | correct (passthrough into `map[string]any`) |
   | Web SPA | `web/src/jobs/api.ts:128-168` | correct (`TaskLogPage{items,next_seq,total}`) |
   | Python SDK | `python/src/relay/client.py:266` | **broken** |

   The web SPA was missed entirely by the complement search that produced this item. So the true
   statement today is "one of four", not "two of three", and the item is additionally stale by one
   commit because the CLI was fixed the day after it was filed. The correction matters twice over:
   the web client is a third existing, working implementation of this envelope, and the implementer
   should read `web/src/jobs/api.ts` and `internal/cli/logs.go` rather than invent a fourth reading.

2. **"`internal/mcp/task_logs.go` ... is structurally immune to this class."** True of the DECODE
   and false of drift. The same file's jsonschema advertises `since_seq` as "seq >= this value"
   while the SQL is `id > $2` - already filed as
   `bug-2026-08-26-mcp-since-seq-is-advertised-inclusive-and-is-exclusive`. A passthrough client is
   immune to shape drift and fully exposed to SEMANTIC drift. Do not let "passthrough" be read as
   "safe".

3. **"`python/src/relay/client.py:264`"** (Context section). Line 264 at HEAD is the
   `self._http.get(...)`; the defective comprehension is line 266. The Related section's `262-266`
   is right. Cite by symbol.

4. **"the integration lane should already fail against a real server - check whether that lane is
   being run."** Checked. **It is not run anywhere.** `.github/workflows/python.yml` has exactly two
   jobs: `test` (15 matrix cells, all running `pytest tests/unit -v`) and `lint`. There is no
   integration job, and `python/README.md` documents the lane as `RELAY_INTEGRATION=1 pytest
   tests/integration` against a server the developer supplies. So `test_smoke.py:26` has never once
   executed in CI. This is the same mechanism as `idea-2026-08-23-cli-tests-never-hit-real-server`,
   which the item correctly says "applies verbatim" - it now has the CI evidence the item asserted
   without.

5. **"the SDK's list methods already grew cursor paging in `81a3d65`, so follow whatever shape those
   settled on rather than inventing a third."** Accurate diagnosis, and the remedy is impossible as
   literally written. The settled shape is `Page[T]` with `next_cursor: str`. This endpoint's cursor
   is an `int` under a DIFFERENT KEY with different drain semantics, and pydantic v2 does not coerce
   `int` into a `str` field. See "Paging shape" below, where I separate following the TYPE (cannot)
   from following the PATTERN (must).

6. **"The closed item's own acceptance criterion was 'All paginated REST endpoints have a
   corresponding SDK method', and this endpoint was paginated at the time, so the criterion was
   recorded as met while an endpoint in scope was still broken."** The conclusion is right and the
   diagnosis is too kind to the criterion. Read the predecessor spec: its section headed
   "Paginated endpoints (source of truth)" enumerates six endpoints, all of which return `page[T]`
   with `?cursor=`. `/v1/tasks/{id}/logs` is paginated by `?since_seq=` and does not use `page[T]`,
   so it was never in that table. The endpoint was in scope by the PROSE and out of scope by the
   TABLE the spec actually worked from. That changes the remedy: the fix is not "be stricter about
   criteria", it is "enumerate from the ROUTE TABLE, not from a category name". That is precisely
   what the sweep below does, and it is why the sweep is keyed on `internal/api/server.go`'s
   `Handler()` rather than on the word "paginated".

7. **Commit SHAs `81a3d65` and `a90c727`.** Not verified - no shell this session. The SHAPES they
   are cited for are confirmed by reading the tree.

## Goals

1. `task_logs()` returns the task's complete log against a real `relay-server`.
2. `test_task_logs_parses_records` uses the envelope, and reverting the client fix while keeping the
   new fixture turns it RED.
3. Every `Client` method and every `models.py` field is checked against the handler that serves it,
   with the count stated. Findings are either fixed here or named and declined with a reason.

## Non-goals

- Async client. The SDK is synchronous; it stays synchronous.
- Any change to `internal/api/**`, `internal/cli/**`, `internal/mcp/**`, or `web/**`. The wire format
  is correct and three of four clients already speak it. This is a pure client catch-up.
- A generator/iterator paging idiom. Declined explicitly under "Declined doors".

## Paging shape: the decision and its justification

**Decision: a new `LogPage` model and a `task_logs_page()` sibling. `task_logs()` auto-paginates.
`Page[T]` is NOT reused and NOT generalised.**

### Why `Page[T]` cannot carry this cursor

The item says "follow whatever shape those settled on rather than inventing a third". There are two
different things that sentence could mean and only one of them is achievable.

| | `Page[T]` (six list endpoints) | logs envelope |
| --- | --- | --- |
| cursor key on the wire | `next_cursor` | `next_seq` |
| cursor type | opaque `str` | `int` |
| request param | `?cursor=` | `?since_seq=` |
| drained signal | `""` | `0` |
| ordered? | no, opaque | yes, a `BIGSERIAL` id |

Three reasons they cannot share the type, in increasing order of importance:

1. **Wire key.** `next_cursor` and `next_seq` are different keys. Sharing the model needs an alias
   or a hand-populated constructor, so the "shared" model would not actually validate either body
   without per-endpoint glue - which is the glue the split avoids.
2. **Type.** `Page.next_cursor` is `str`. Pydantic v2 does not coerce `int` into `str` in lax mode,
   so `next_seq: 0` does not validate as a `next_cursor`. Widening to `Union[str, int]` would make
   every existing `list_*_page` caller's `next_cursor` type ambiguous for zero gain, and the wire
   key would still differ.
3. **The type is load-bearing, not cosmetic.** The int cursor is ORDERED, and one of the three
   termination stops below (`next_seq <= since`) is only expressible on an ordered cursor. An
   opaque string cursor cannot detect a non-advancing server. Collapsing the two types would either
   drop that stop or push it into a `Page`-shaped model where it is meaningless for six of seven
   consumers.

So: **the two cannot share the type, and they must share the PATTERN.** The pattern is what the
predecessor actually settled and what the item is really asking for:

- a `X()` method that auto-paginates and returns `list[T]`, whose `limit` caps TOTAL rows,
- a `X_page()` sibling that returns one envelope model, whose `limit` is the PAGE SIZE,
- a request page size of `_PAGE_REQUEST_LIMIT = 200` on the auto-paginating form.

`task_logs()` / `task_logs_page()` adopt that pattern exactly, so the SDK grows a third ENDPOINT
family and not a third IDIOM.

### The models

```python
class LogRecord(BaseModel):
    model_config = ConfigDict(extra="ignore")

    seq: int          # NEW - required, see below
    stream: str
    content: str
    created_at: datetime


class LogPage(BaseModel):
    """One page of a task's log, forward-only from ``since_seq``.

    ``next_seq`` is 0 when the server reports the log drained; otherwise it is
    the cursor for the next request, passed VERBATIM as ?since_seq= because the
    server's predicate is `id > $2` (exclusive). Never next_seq + 1: task_logs.id
    is a global BIGSERIAL, so when one task logs alone its ids are contiguous and
    +1 skips the very next row.
    """

    model_config = ConfigDict(extra="ignore")

    items: list[LogRecord]
    next_seq: int
    total: int
```

**`next_seq` and `total` are REQUIRED, with no default.** This is a deliberate departure from
`_get_page`, which does `body.get("next_cursor", "")`. A defaulted `next_seq: int = 0` would read a
missing key as "drained" and silently return page 1 - which is the same shape as the defect this
whole slice exists to fix, rebuilt inside the fix. The handler writes both keys unconditionally
from a map literal, so requiring them costs nothing and fails loudly if that ever changes. Validate
the whole body through `LogPage.model_validate(response.json())` rather than hand-picking keys, so
the model is the pin.

**`LogRecord.seq` is added as REQUIRED**, for three reasons: it is the only way a caller using
`task_logs_page()` can correlate a record with a cursor; the server has sent it since 2026-05-08 and
every other `LogRecord` field is required; and a defaulted `seq: int = 0` is the same
absent-field-benign-default shape flagged in the paragraph above. This is technically a breaking
change for anyone constructing `LogRecord` by hand, which the SDK does not document as a supported
use (it is a response model) and which no test does today except the fixture being replaced.

### Signatures

```python
_MAX_LOG_PAGES = 10000   # class attribute on Client, sibling to _PAGE_REQUEST_LIMIT

def task_logs(self, task_id: str, *, limit: Optional[int] = None) -> list[LogRecord]:
    """Fetch a task's complete log, auto-paginating across pages.

    ``limit`` caps the TOTAL number of records returned (None = all). Each
    request fetches ``_PAGE_REQUEST_LIMIT`` rows.
    """

def task_logs_page(
    self, task_id: str, *, since_seq: int = 0, limit: Optional[int] = None
) -> LogPage:
    """Fetch one page of a task's log.

    ``limit`` is the PAGE SIZE (1-200). Pass the returned ``next_seq`` back as
    ``since_seq=`` to page forward; it is exclusive, so pass it verbatim.
    """
```

`task_logs()` always sends `limit=200`; without it the server default is 50 and a long log is
silently truncated at the first stop. `task_logs_page()` sends `limit` only when given: a caller
paging by hand is told there is more by `next_seq`, so the server default is visible there rather
than silent. The asymmetry is deliberate and belongs in the docstrings.

### The loop, and its three stops beyond the drained signal

`internal/cli/logs.go`'s `printTaskLogs` is the reference. Read it, including its comments, before
implementing. Its three stops all apply here for the same reason: the cursor is server-supplied and
drives a client loop, and the provenance of a value says nothing about who controls its content or
the timing of the writes behind it.

```
since = 0
out = []
for pages in 1..:
    body   = GET /v1/tasks/{id}/logs?since_seq={since}&limit=200
    page   = LogPage.model_validate(body)
    out   += page.items

    if limit is not None and len(out) >= limit:   return out[:limit]     # user cap
    if page.next_seq == 0:                        return out             # server says drained
    if not page.items:                            raise ProtocolError    # stop 1
    if page.next_seq <= since:                    raise ProtocolError    # stop 2
    if pages >= _MAX_LOG_PAGES:                   raise ProtocolError    # stop 3
    since = page.next_seq
```

The order is the CLI's order with the user cap inserted where `_fetch_all` puts it, so the SDK has
one paging idiom rather than two.

- **Break on `next_seq`, never on `len(items) < limit`.** The two agree today; the second re-derives
  a rule the server already applied and desynchronizes the moment the drain rule moves.
- **Stop 1 (empty page not reporting drained)** is unreachable against the real handler, which sets
  `next_seq = 0` whenever `len(items) < limit`. That is exactly why it must RAISE and not return
  quietly: the only server that reaches it is misbehaving, and a quiet return would launder that
  into a completeness claim the client cannot support. Note the common case it must NOT catch: a log
  whose length is an exact multiple of 200 legitimately produces a final empty page, and that page
  reports drained, so the arm above returns first.
- **Stop 2 (`next_seq <= since`)** catches a non-advancing cursor on the second request. Only
  expressible because the cursor is ordered.
- **Stop 3 (`_MAX_LOG_PAGES`)** catches an ever-advancing cursor that never drains, which neither of
  the other two can see. 10000 pages at 200 rows is 2,000,000 rows: a hang bound, not a product
  limit. It is a class attribute so a test can shrink it, matching `_PAGE_REQUEST_LIMIT`.
  **Its message must not blame the server when `page.total > 0 and len(out) >= page.total`**: a log
  of exactly `_MAX_LOG_PAGES * 200` rows drains correctly but its last page is full and so carries a
  non-zero cursor, and the client stops one request short of learning it was done having in fact
  collected every row. The envelope's own `total` settles that case; port the CLI's two-message
  split rather than re-deriving it.

### New error type

```python
class ProtocolError(RelayError):
    """The server answered with well-formed HTTP that is not a usable relay
    response: a page that advertises more rows but carries none, a cursor that
    does not advance, or a log that never reports itself drained."""
```

Exported from `relay`, added to the README error table. All three stops raise it. Without it the
stops would have to raise `ServerError` (wrong: the status was 200) or `RelayError` directly
(untypeable by callers).

### Memory and server cost, stated rather than assumed

`task_logs()` accumulates the whole log in a list. The CLI deliberately does NOT do this - it prints
each page as it arrives, so memory is O(one page) on a multi-hundred-megabyte log. The SDK cannot
match that while returning `list[LogRecord]`, so the difference must be documented, and
`task_logs_page()` plus `limit=` are the two escapes. See "Declined doors" for why a generator is
not the answer here.

**A server-side cost this amplifies, recorded as an observation and not fixed here.**
`handleGetTaskLogs` calls `CountTaskLogs` on EVERY page, and that query is
`SELECT COUNT(*) FROM task_logs WHERE task_id = $1`. With `idx_task_logs_task_id_id` on
`(task_id, id)` it is an index scan over all of that task's rows, so an N-page walk is N full counts
of the same set: auto-paging a 2,000,000-row log costs 10,000 counts of 2,000,000 index entries. The
CLI and the SPA already pay this; the Python auto-pager adds a third payer. The fix is server-side
(compute `total` only when `since_seq == 0`, or drop it to a cheaper estimate) and belongs in its
own item. Proposed below. I did not run `EXPLAIN`, so treat the plan shape as read off the index
definition rather than measured.

## The stale fixture

`test_task_logs_parses_records` must be rewritten so that **reverting the client fix while keeping
the new fixture turns it RED**. Three rules make that true and keep it true:

1. **The fixture emits the envelope.** With `{"items": [...], "next_seq": 0, "total": N}` in front
   of the reverted comprehension, `LogRecord.model_validate("items")` raises
   `pydantic_core.ValidationError`. The test fails. This alone satisfies the criterion, which is why
   `LogRecord.seq` is deliberately not carrying any of the RED weight.
2. **The assertion is positive, never `pytest.raises`.** Assert
   `[(r.seq, r.stream, r.content) for r in logs] == [...]`. A test that asserts an exception is
   green against a client that raises for an unrelated reason.
3. **The fixture must not be built out of the types under test.** Declare the wire body as a plain
   `dict` literal with hand-written keys. Do NOT construct `LogRecord` or `LogPage` and dump them.
   A fixture built from the consumer's own model cannot detect drift in that model, which is the
   exact failure this slice exists to fix. Put that sentence in a comment above the helper, saying
   that de-duplicating the two re-opens the bug. This is the `writeTaskLogPage` / `logRow` lesson
   from the CLI slice, and its plan called it "the single most important correction in this plan".

A single `_log_page(rows, *, next_seq, total)` helper in the test module builds every fixture body,
with hand-written keys, so the fixtures below cannot drift apart the way the CLI's four fake servers
did.

**RED proof procedure, to be run and recorded:** revert `task_logs` to the HEAD comprehension, keep
every new test, run `./.venv/Scripts/python.exe -m pytest tests/unit -q` from `python/`. Record which
tests fail and the exception text. A test that stays green under that revert is not testing the fix.

## THE SWEEP

**Scope of the check: 25 HTTP-performing public methods on `Client`, over 18 distinct route+verb
pairs, plus 3 private HTTP helpers (`_get_page`, `_fetch_all`, `_stream_events`) and 3 non-HTTP
public methods (`close`, `__enter__`, `__exit__`) - 28 public methods total. On the model side, 85
fields across 12 pydantic models in `models.py`; `events.py` declares no models (it produces
`Event`) and its SSE framing is checked as part of `follow_job`.**

Method is the unit of the count because that is what a caller invokes. Routes are given so the
route-table origin is auditable: I enumerated from `internal/api/server.go` `Handler()` (lines
96-207) and matched each SDK call against the handler registered there, rather than from the word
"paginated", which is what let the predecessor miss this endpoint.

### Method table

"Emitted shape" is what the handler actually writes, read at the `writeJSON` call site.

| # | SDK method | Verb + path | Handler | Emitted shape | SDK assumes | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `submit` | POST `/v1/jobs` | `handleCreateJob` | 201, `jobResponse` object (tasks present) | single object | MATCH |
| 2 | `get_job` | GET `/v1/jobs/{id}` | `handleGetJob` | 200, `jobResponse` (tasks present) | single object | MATCH |
| 3 | `list_jobs` | GET `/v1/jobs` | `handleListJobs` | 200, `page[jobResponse]` | `body["items"]` + `next_cursor` | MATCH (lossy, D3) |
| 4 | `list_jobs_page` | GET `/v1/jobs` | `handleListJobs` | 200, `page[jobResponse]` | `Page[Job]` | MATCH (lossy, D3) |
| 5 | `cancel_job` | DELETE `/v1/jobs/{id}` | `handleCancelJob` | 200, `toJobResponse(job, "", nil, nil)` - `tasks` OMITTED under `omitempty` | single object | MATCH (lossy, D11) |
| 6 | `get_tasks` | GET `/v1/jobs/{id}/tasks` | `handleListTasks` | 200, **bare array** of `taskResponse` | bare array | **MATCH** (see lead 1) |
| 7 | `get_task` | GET `/v1/tasks/{id}` | `handleGetTask` | 200, `taskResponse` object | single object | MATCH |
| 8 | `task_logs` | GET `/v1/tasks/{id}/logs` | `handleGetTaskLogs` | 200, `{items, next_seq, total}` | **bare array** | **DRIFT (D1)** |
| 9 | `follow_job` | GET `/v1/events?job_id=` | `handleEvents` | 200, `text/event-stream`, types `job`/`task`/`worker`/`task_log`/`dropped` | SSE frames -> `Event(type, data)` | **DRIFT (D4, D5, D9)** |
| 10 | `wait` | GET `/v1/jobs/{id}` (polls `get_job`) | `handleGetJob` | as #2 | as #2 | MATCH |
| 11 | `create_schedule` | POST `/v1/scheduled-jobs` | `handleCreateScheduledJob` | 201, `scheduledJobResponse` | single object | MATCH |
| 12 | `list_schedules` | GET `/v1/scheduled-jobs` | `handleListScheduledJobs` | 200, `page[scheduledJobResponse]` | envelope | MATCH |
| 13 | `list_schedules_page` | GET `/v1/scheduled-jobs` | `handleListScheduledJobs` | 200, `page[scheduledJobResponse]` | `Page[ScheduledJob]` | MATCH |
| 14 | `get_schedule` | GET `/v1/scheduled-jobs/{id}` | `handleGetScheduledJob` | 200, `items[0]` - one `scheduledJobResponse` object | single object | MATCH |
| 15 | `update_schedule` | PATCH `/v1/scheduled-jobs/{id}` | `handlePatchScheduledJob` | 200, `scheduledJobResponse` | single object | MATCH |
| 16 | `delete_schedule` | DELETE `/v1/scheduled-jobs/{id}` | `handleDeleteScheduledJob` | **204 No Content**, empty body | returns `None`, never calls `.json()` | MATCH |
| 17 | `run_schedule_now` | POST `/v1/scheduled-jobs/{id}/run-now` | `handleRunScheduledJobNow` | **201** Created, `jobResponse` | `Job` via `raise_for_response` (2xx no-op) | MATCH |
| 18 | `list_workers` | GET `/v1/workers` | `handleListWorkers` | 200, `page[workerResponse]` | envelope | MATCH (lossy, D2) |
| 19 | `list_workers_page` | GET `/v1/workers` | `handleListWorkers` | 200, `page[workerResponse]` | `Page[Worker]` | MATCH (lossy, D2) |
| 20 | `list_users` | GET `/v1/users` | `handleListUsers` | 200, `page[userResponse]` | envelope | MATCH |
| 21 | `list_users_page` | GET `/v1/users` | `handleListUsers` | 200, `page[userResponse]` | `Page[User]` | MATCH |
| 22 | `list_reservations` | GET `/v1/reservations` | `handleListReservations` | 200, `page[reservationResponse]` | envelope | MATCH |
| 23 | `list_reservations_page` | GET `/v1/reservations` | `handleListReservations` | 200, `page[reservationResponse]` | `Page[Reservation]` | MATCH |
| 24 | `list_agent_enrollments` | GET `/v1/agent-enrollments` | `handleListAgentEnrollments` | 200, `page[map[string]any]` with `id`/`created_at`/`expires_at`/`created_by`/`hostname_hint?` | envelope | MATCH |
| 25 | `list_agent_enrollments_page` | GET `/v1/agent-enrollments` | `handleListAgentEnrollments` | as above | `Page[AgentEnrollment]` | MATCH |
| h1 | `_get_page` | (all `page[T]` routes) | `page[T]` | `{items, next_cursor, total}` | `body["items"]`, `body.get("next_cursor","")` | MATCH (latent, D7) |
| h2 | `_fetch_all` | (all `page[T]` routes) | `page[T]` | as above | as above | MATCH (latent, D7) |
| h3 | `_stream_events` | GET `/v1/events` | `handleEvents` | SSE | `parse_sse_stream` | MATCH (framing correct) |

**24 of 25 methods match on SHAPE. One method - `task_logs` - is a hard DRIFT that raises.** Five
further findings are lossy, latent, or prose-level and are listed below.

### Model field table

All 12 models use `extra="ignore"`, so an unmodeled server field is silently dropped rather than
raising. That is the right fail direction and it is also why field-level gaps do not show up as
errors.

| Model | Fields | Server source | Verdict |
| --- | --- | --- | --- |
| `Sync` | 2 | `SourceSpec` sync entries | MATCH (authoring-only) |
| `Source` | 6 | `SourceSpec` | MATCH (authoring-only; the server never echoes `source` on `taskResponse`) |
| `Task` | 12 | `taskResponse` (11 keys) | MATCH; `source` is authoring-only and is always `None` from the server |
| `Job` | 10 | `jobResponse` (16 keys) | **6 list-enrichment keys unmodeled (D3)** |
| `LogRecord` | 3 | `logEntry` (4 keys) | `seq` unmodeled - added by this spec |
| `Event` | 2 | SSE frame | MATCH |
| `ScheduledJob` | 13 | `scheduledJobResponse` (14 keys) | `owner_email` unmodeled; harmless, listed for completeness |
| `Page[T]` | 3 | `page[T]` | MATCH |
| `Worker` | 14 | `workerResponse` (15 keys) | **`revoked_at` unmodeled (D2)**; `last_sample_at` is modeled but is only ever emitted by `GET /v1/workers/{id}`, which the SDK does not call |
| `Reservation` | 9 | `reservationResponse` (9 keys) | MATCH |
| `AgentEnrollment` | 5 | `enrollmentRowToMap` (5 keys) | MATCH |
| `User` | 6 | `userResponse` (6 keys) | MATCH |

**JSON-object fields do not need `Optional`.** `Job.labels`, `Task.env`, `Task.requires`,
`Worker.labels` and `Reservation.selector` are all `dict[...] = Field(default_factory=dict)`, which
in pydantic v2 REJECTS an explicit `null`. I checked whether the server can send one: every backing
column is `JSONB NOT NULL DEFAULT '{}'` (`000001_initial.up.sql:32,45,56,57,88`) or
`'[]'` (`000008_task_commands.up.sql:3`), `rawObject` additionally normalizes `null` to `{}` for
`env`/`requires`, and `rawJSON` normalizes empty to `{}`. So the hazard is real in shape and
unreachable in fact. Recording it because it is one schema change away from being reachable, and
because a reader auditing these fields should not have to re-derive that.

### Lead 1 (given, confirmed): `get_tasks()` and the bare array

**Confirmed: `handleListTasks` writes a bare JSON array today**, and `get_tasks()` is correct.
`resp := make([]taskResponse, len(tasks))` then `writeJSON(w, http.StatusOK, resp)` - a `make`d
slice, so an empty task list serializes as `[]` and never `null`.

**Is it stable? Not by construction, and it is the SDK's only bare-array read.** Three things to
record:

- It is the last unpaginated list route the SDK calls. If `/v1/jobs/{id}/tasks` ever grows
  `page[T]` the way its six siblings did, `get_tasks()` breaks in exactly the way `task_logs()` is
  broken now, and the test that would have caught it does not exist.
- It is a DIFFERENT hazard from the `handleGetJob` one the CLI retro names. `jobResponse.Tasks` is
  `json:"tasks,omitempty"`, so a task-less `handleGetJob` body decodes into a silently-empty list -
  that is route #2, not route #6, and it hit the CLI's `reconcileFinalSnapshot`. The Python SDK is
  behind the same door on `get_job()` and `cancel_job()`: a `Job` with `tasks == []` is
  indistinguishable from a job that has no tasks, and every job in the database has at least one by
  construction (`jobspec.Validate` rejects a spec with zero tasks; CLAUDE.md's single job-spec
  pipeline invariant is what makes that a guarantee and not an observation). Recorded as D11.
- The mitigation this spec funds is one test, not a code change: a unit test asserting `get_tasks()`
  parses a bare array, so a future server-side envelope makes the SDK's suite red rather than
  production red. That is the cheapest thing that would have caught the present bug and it costs
  four lines.

### Lead 2 (given): which of the CLI retro's doors is the Python SDK also behind?

The retro found "prints nothing" reachable by several more routes than the decode. Taken one at a
time, confirmed or refuted against `python/src/relay/`:

| Retro door | Python SDK | Decision |
| --- | --- | --- |
| Envelope decode failure | **OPEN** | fixed here (D1) |
| Silent swallow of that failure | **CLOSED, structurally** | refuted, see below |
| One-page truncation (`next_seq` ignored) | **OPEN**, and worse - no `?limit=` at all, so 50 rows | fixed here |
| Cursor `+1` instead of verbatim | not yet written | pinned by test, see T2 |
| Never-draining / non-advancing server | would OPEN once paging exists | three stops |
| No `http.Client.Timeout` | **CLOSED for the SDK-owned client** (`timeout=30.0`); **OPEN when the caller injects `http_client=`** | partially declined, D12 |
| Unbounded response body | **OPEN** (`response.json()` fully buffers) | declined, D13 |
| Non-canonical job id on the SSE filter | **OPEN** - same defect `canonicalJobID` closed | declined + item, D9 |
| Unescaped id interpolated into the request path | **OPEN in shape**, 9 sites | declined + append, D10 |
| 404 retried as transient | **CLOSED** - no retry loop exists; 404 raises `NotFound` | none |
| Unchecked write to the output stream | **N/A** - returns a list | none |
| Cancelled job publishes no task frame | **N/A** for `task_logs`; `wait()` polls | declined, D14 |

**The swallow is refuted for Python, and the refutation matters.** The comprehension raises out of
`task_logs()`. There is no bare `except`, no `contextlib.suppress`, and no default-on-error anywhere
in `client.py`. So the Python instance was NEVER silent - it has been raising `ValidationError` at
every caller for three and a half months. It survived for a completely different reason: the only
test that exercises it hand-writes the old shape, and the only test that would hit a real server
runs in no CI job on earth. Do not carry the CLI's "silence" framing across; the remedy here is the
lane and the fixture, not a louder error path.

### The full findings list

**D1. `task_logs()` iterates the envelope's keys.** Hard drift, raises. **FIXED HERE.**

**D2. `Worker.revoked_at` is unmodeled.** `workerResponse` emits `revoked_at` (`workers.go:32`,
set by `toWorkerResponse` from `w.RevokedAt`). A Python caller cannot see it, and the SDK also has
no method for `GET /v1/workers/revoked`. **FIXED HERE** (add the field); the missing method is
proposed as backlog item 5 below, not fixed here.

**D3. `Job` drops six list-only enrichment fields.** `jobResponse` carries `total_tasks`,
`done_tasks`, `started_at`, `finished_at`, `scheduled_job_id`, `scheduled_job_name` on
`GET /v1/jobs` rows. `Job` models none of them, so `list_jobs()` silently discards the progress and
timing the server computed. **FIXED HERE** (six optional fields; all default to absent/zero on
single-job routes where the server does not populate them).

**D4. `EventType` covers 2 of the 5 published SSE types.** The server emits `job`
(`scheduler/dispatch.go:410`), `task` (three sites), `worker` (`metrics/sweep.go:95`), `task_log`
(`events.TypeTaskLog`), and `dropped` (written directly by `handleEvents:84`). `EventType` has only
`JOB` and `TASK`. `Event.type` is a plain `str`, so nothing raises - the enum is simply an
incomplete slicing of a vocabulary, in the same family as the two Go lockstep guards. **FIXED HERE**
(add `WORKER`, `TASK_LOG`, `DROPPED`; docstring names `dropped` as the "you fell behind, re-backfill"
frame).

**D5. `follow_job()`'s docstring states a contract the server does not implement.** It says the
iterator yields "until the server closes the connection (which it does when the job reaches a
terminal state)". `handleEvents` closes on exactly two conditions: the request context is done, or
the broker drops a slow consumer. It has no notion of job terminality, and `Broker.removeLocked` is
reached only by cancel or by the slow-consumer path. So a Python caller iterating `follow_job()`
blocks forever after the job finishes - and `_stream_events` sets `read=None`, so no timeout ever
fires. A wrong contract in docs is a defect: consumers implement against the prose and no test
covers it. **FIXED HERE** (docstring corrected; the caller is told to break on a terminal `job`
frame, or to use `wait()`, and is told what a `dropped` frame means).

**D6. `python/README.md`'s Client API table lists 15 of 25 methods.** Missing: all six `*_page`
siblings, `list_workers`, `list_users`, `list_reservations`, `list_agent_enrollments`. The
pagination work added them and did not update this table. **FIXED HERE.**

**D7 (latent, NOT fixed here). `_get_page` / `_fetch_all` read the cursor with a default.**
`body.get("next_cursor", "")` means a renamed or dropped key reads as "drained" and `_fetch_all`
returns page 1 silently - the defect shape of this very slice, one layer over. It is not a drift
today: the key is correct. **Declined** because making it strict changes the failure behaviour of
twelve methods across six endpoints and needs its own RED. Item proposed.

**D8 (NOT fixed here). Decode failures escape as non-`RelayError` exceptions.** `body["items"]`
raises a bare `KeyError`, and every `Model.model_validate(...)` raises
`pydantic_core.ValidationError`. `python/README.md` states "All exceptions descend from
`relay.RelayError`". That is false at every one of the SDK's model-validation sites and has been
since the SDK shipped. **Declined**: fixing it is a cross-cutting wrap at every call site, it is not
what makes `task_logs()` broken, and folding it in would make the diff two features. Item proposed.
Note `ProtocolError`, added here, does descend from `RelayError`, so the new code does not add to
the count.

**D9 (NOT fixed here, HIGH). `follow_job()` subscribes to nothing forever on a non-canonical job
id.** `handleEvents` deliberately does not validate or canonicalise `?job_id=`
(`events.go:50-53`), and the broker filter is an exact string compare. `follow_job(job_id)` passes
the caller's string verbatim. So an uppercase or dashless UUID - which `get_job()` accepts, because
`parseUUID` scans both - yields an open, permanently empty stream with `read=None`. This is
character-for-character the defect `canonicalJobID` closed in the CLI on 2026-08-26. **Declined**
for this slice: it is a different method, it needs a UUID canonicaliser in Python plus its own
tests, and it is a hang rather than a wrong answer. Item proposed, priority high.

**D10 (NOT fixed here). Nine f-string id interpolations into request paths, unescaped.**
`get_job`, `cancel_job`, `get_tasks`, `get_task`, `task_logs`, `get_schedule`, `update_schedule`,
`delete_schedule`, `run_schedule_now`. **I could not measure httpx's behaviour this session (no
shell), and I am not asserting an exploit.** What is checkable by reading is that the SDK hands
httpx a fully-assembled URL string, so whether a `?` or `#` inside an id splits into a query or
fragment is httpx's URL-parsing behaviour and not something the SDK controls. This is the same class
as the open item `bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped`, and it
belongs there as a third language rather than in a new item. **Declined**, proposed as an APPEND to
that item, with the note that the appending session must MEASURE httpx's behaviour before choosing a
remedy - a text search cannot establish a behavioural property.

**D11 (documented, not code-fixed). A `Job` with `tasks == []` is ambiguous.** `cancel_job()`
always returns one, because `handleCancelJob` calls `toJobResponse(job, "", nil, nil)` and `tasks`
is `omitempty`. `get_job()` returns one whenever `handleGetJob` fails to populate them - which is no
longer possible since that handler started checking `ListTasksByJob`, but the ambiguity in the model
is unchanged. **FIXED HERE as prose**: `cancel_job`'s docstring states that the returned `Job`
carries no task list, so a caller must not read `.tasks` from it.

**D12 (declined). An injected `http_client=` may have no timeout.** The SDK's own client gets
`timeout=30.0`; when the caller passes `http_client=`, the SDK only merges auth headers
(`client.py:78`) and never inspects the timeout. httpx's own default for a bare `httpx.Client()` is
5 s, so the realistic injected client is not unbounded - but a caller who passed `timeout=None`
gets no bound and no warning. **Declined**: the caller who injects a client owns its policy, and
overriding it would be surprising. Named here so the next reader does not have to rediscover that
the constructor's `timeout=` parameter is silently ignored on the injected path - which IS worth a
one-line docstring note, and that note is in scope.

**D13 (declined). No response-body bound.** `response.json()` buffers the whole body. The paging
loop bounds the number of REQUESTS (`_MAX_LOG_PAGES`) but not the size of any one response, so a
hostile or broken server can return an arbitrarily large single page. The Go item
`bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout` covers the identical hole
for `internal/relayclient`. **Declined** for this slice: it applies to all 25 methods, not to
`task_logs`, and the right shape is one `_read_json(response)` chokepoint (the Python analogue of
CLAUDE.md's single JSON entry point invariant), which is its own slice. Proposed as an append to
that item.

**D14 (declined). No `follow_job` reconcile for a cancelled job.** The retro's cancel door
(`CancelJobTasks` publishes only a `job` frame, so no task frame ever fires) applies to any Python
caller who builds a watch loop on `follow_job`. The SDK ships no watch loop - `wait()` polls
`GET /v1/jobs/{id}`, which is immune. **Declined**: there is nothing in the SDK to fix. The
docstring correction under D5 is what tells a caller building their own loop that the stream will
not end.

### Declined doors, stated once more so none is merely omitted

A generator form of `task_logs()` (`iter_task_logs()`) is **declined**. It would give O(one page)
memory, which is the one thing the list form cannot. It is declined because it would make three
paging idioms in one SDK (`list_*`, `list_*_page`, and a generator), because a generator's lazy
error timing is a second surprise on top of the paging stops, and because `task_logs_page()` already
gives a caller who cares about memory full manual control. If a real caller hits the memory wall,
that is an item with a concrete acceptance criterion; today it is speculation.

## Files changed

- `python/src/relay/models.py` - add `LogPage`; add `LogRecord.seq`; add `Worker.revoked_at` (D2);
  add six `Job` enrichment fields (D3); add three `EventType` members (D4).
- `python/src/relay/errors.py` - add `ProtocolError`.
- `python/src/relay/client.py` - rewrite `task_logs`; add `task_logs_page`; add `_MAX_LOG_PAGES`;
  docstring corrections for `follow_job` (D5), `cancel_job` (D11), and `__init__`'s `timeout=` on
  the injected-client path (D12).
- `python/src/relay/__init__.py` - export `LogPage`, `ProtocolError`.
- `python/tests/unit/test_client.py` - replace `test_task_logs_parses_records`; add the paging
  battery; add the `get_tasks` bare-array pin.
- `python/tests/unit/test_models.py` - `LogPage` validation, `LogRecord.seq`, the new `Worker` /
  `Job` / `EventType` fields.
- `python/README.md` - Client API table (D6), error table (`ProtocolError`), a paging note for
  `task_logs`, and the `follow_job` termination correction.
- `python/pyproject.toml` and `python/src/relay/_version.py` - `0.1.2` -> `0.1.3` (both currently
  read `0.1.2`; keep them in lockstep).
- `docs/backlog/bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys.md` - `git mv` to
  `docs/backlog/closed/` via `/backlog close`, not a hand edit.

No file under `internal/` or `web/` is touched.

## Test plan

Unit only, `httpx.MockTransport`, no network. Every fixture body comes from one
`_log_page(rows, *, next_seq, total)` helper built from a plain dict literal with hand-written keys.

**Paging and shape**

1. **T1 (the RED).** One-page envelope, `next_seq: 0`. Assert
   `[(r.seq, r.stream, r.content) for r in logs]` equals the fixture's rows, positionally. Positive
   assertion, not `pytest.raises`.
2. **T2 (cursor verbatim).** Two pages. Page 1 returns rows with seqs `7, 8` and `next_seq: 8`;
   page 2 returns seq `9` and `next_seq: 0`. Assert request 2 carried `since_seq=8`.
   **The seqs must be contiguous and that is load-bearing**: with a gap (say 7 then 20) a
   `since = next_seq + 1` mutation is undetectable, because both cursors land before the next row.
   Comment it as such, or the next author will "clean up" the fixture and silently kill the test.
3. **T3 (mutation position).** In any test with a discriminating page, the discriminating page goes
   FIRST with normal pages after. A bad input placed last cannot detect an early-exit mutation.
4. **T4 (exact-multiple log).** Page 1 full (200 rows) with a non-zero `next_seq`; page 2 empty with
   `next_seq: 0`. Assert exactly 2 requests, 200 records, no duplicates, and no raise. This is the
   case stop 1 must NOT catch, and the second request is not optional: a full final page always
   carries a non-zero cursor, so the client cannot know it is done without asking again.
5. **T5 (stop 1).** Empty `items` with a non-zero `next_seq` -> `ProtocolError`, message names both
   `next_seq` and `since_seq`.
6. **T6 (stop 2).** Page 2 returns `next_seq` equal to the `since_seq` it was asked for ->
   `ProtocolError`.
7. **T7 (stop 3).** A never-draining handler (always a full page, always an advancing `next_seq`).
   Monkeypatch `Client._MAX_LOG_PAGES` to a small value. Assert `ProtocolError` and assert the
   request count equals the cap - a test that only checks the exception cannot tell the cap from a
   different stop firing.
8. **T8 (stop 3's message).** Never-draining handler whose `total` equals the rows already
   collected. Assert the message does NOT claim the log may be longer, and DOES say every reported
   row was collected.
9. **T9 (`limit` caps total).** Two pages of 200 with `task_logs(limit=250)` -> exactly 250 records,
   and exactly 2 requests (asserting the request count is what proves the cap short-circuits rather
   than trimming at the end).
10. **T10 (`task_logs_page`).** Returns a `LogPage` with the fixture's `next_seq` / `total`; asserts
    `since_seq` and `limit` appear as query params when given, and that `since_seq` is absent when 0.
11. **T11 (required keys).** A body missing `next_seq` raises. Same for `total`. This pins the
    no-default decision; delete the requirement and this test goes green.
12. **T12 (explicit limit).** `task_logs()`'s FIRST request carries `limit=200`. Deleting the limit
    silently reintroduces the 50-row truncation and nothing else would notice.
13. **T13 (old shape is loud).** A bare-array body raises rather than returning `[]`. A server
    rollback must not look like an empty log.

**Sweep pins**

14. **T14.** `get_tasks()` parses a bare array of `taskResponse`-shaped dicts. Four lines; it is the
    cheapest guard against the next instance of this bug.
15. **T15.** `Worker` parses `revoked_at`; `Job` parses the six enrichment fields; `EventType` has
    five members with the server's exact string values.

**RED verification, to be recorded in the PR:** with `task_logs` reverted to the HEAD comprehension
and all new tests kept, T1, T2, T4, T9, T12 and T13 must fail, and T1's failure must carry
`type=model_type, input_value='items'`. Any new test that stays GREEN under that revert must be
justified in the plan or deleted.

**Lane note.** The unit suite runs in CI; the integration suite does not run anywhere. So none of
the above puts the SDK in front of the real handler, and the fix's real acceptance
(`test_smoke.py:26`) is unverifiable by CI. Say so in the PR rather than implying coverage. This is
the fourth confirmed instance of `idea-2026-08-23-cli-tests-never-hit-real-server` and the first in
Python; that item gets an append, not a new file.

## Acceptance criteria

- `task_logs()` returns a task's COMPLETE log (not the first 50 rows) against a real
  `relay-server`, and `python/tests/integration/test_smoke.py::test_submit_and_wait` passes when
  that lane is run by hand.
- `test_task_logs_parses_records` uses the envelope, and the recorded RED procedure above shows the
  named tests failing with the named exception when the client fix is reverted.
- The paging loop breaks on `next_seq`, passes the cursor VERBATIM, and carries all three stops,
  each covered by a test that fails when that stop alone is deleted.
- **The sweep is recorded in this document with the count stated: 25 HTTP-performing methods over 18
  route+verb pairs, 28 public methods total, 85 model fields across 12 models.** Six findings fixed
  (D1-D6), eight named and declined with a reason (D7-D14).
- `LogPage` and `ProtocolError` importable from `relay`; `ruff check src tests` and `mypy src` clean
  against the Python 3.9 target (`from __future__ import annotations` everywhere; no 3.10+ union
  syntax at runtime).
- `python/README.md`'s Client API table lists all 25 methods; the error table lists `ProtocolError`.
- Both version files read `0.1.3`.
- Backlog item `git mv`d to `docs/backlog/closed/` via `/backlog close` on the same branch.

## Proposed backlog items

Proposals for human accept, not filings. Each is specific and has an acceptance criterion.

1. **`follow_job()` subscribes to nothing on a non-canonical job id** (high). D9. The Python
   analogue of `canonicalJobID`. Acceptance: an uppercase or dashless UUID reaches the same
   subscription as its canonical form, proven by a test that fails before the fix.
2. **The SDK's pagination helpers read the cursor with a default** (medium). D7. Acceptance:
   `_get_page` / `_fetch_all` validate the envelope through a model with a required `next_cursor`,
   and a body missing that key raises rather than reporting drained.
3. **Decode failures escape as non-`RelayError` exceptions, contradicting the README** (medium). D8.
   Acceptance: every `model_validate` and every envelope key read routes through one helper that
   raises `relay.ValidationError`; the README's claim becomes true.
4. **`handleGetTaskLogs` recomputes `COUNT(*)` on every page** (medium, server-side). Acceptance:
   `total` is computed on the first page only (or the contract changes to say so), measured with
   `EXPLAIN ANALYZE` on a large task before and after.
5. **The SDK has no method for four read routes it plausibly wants** (low). `GET /v1/workers/{id}`
   (the only source of `last_sample_at`, which the SDK already models), `GET /v1/workers/revoked`,
   `GET /v1/jobs/stats`, `GET /v1/workers/stats`. Acceptance: each has a method and a model, or the
   README says why not.

**Appends, not new items** (judgements for the conductor, not filings):

- `bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped` - nine Python sites, a
  third language. The append must say the next session MEASURES httpx's URL handling before choosing
  a remedy.
- `bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout` - the Python SDK has the
  same unbounded-body hole across all 25 methods; the timeout half is already closed for the
  SDK-owned client and open on the injected-client path.
- `idea-2026-08-23-cli-tests-never-hit-real-server` - fourth confirmed instance, first in Python,
  and the strongest yet: `.github/workflows/python.yml` runs the unit suite on 15 matrix cells and
  the integration suite on none. Priority should NOT move on this evidence alone; the item's own
  2026-08-26 append already argues that raising it for point fixes rewards the wrong signal.

## Rollout

Single PR, no server change, no migration, no wire-format change. Pre-1.0 SDK. `task_logs()` keeps
its `list[LogRecord]` return type, so callers using it as documented are unaffected and were
previously getting an exception. The two model changes that could surprise someone are
`LogRecord.seq` becoming required (a response model; hand-construction is not a documented use) and
`EventType` gaining members (additive). Both are called out in the version bump note.
