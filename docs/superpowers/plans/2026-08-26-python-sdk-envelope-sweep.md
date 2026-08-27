# Python SDK envelope sweep - implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `relay.Client.task_logs()` return a task's COMPLETE log - decode the `{items, next_seq, total}` envelope the server has written since `a90c727`, page it to the end with a verbatim cursor, and raise `ProtocolError` on a server that cannot be paged - plus the five smaller sweep findings the spec accepted (D2-D6, D11, D12).

**Architecture:** Two new pydantic models (`LogPage`, and `seq` on `LogRecord`), one new error class (`ProtocolError`), and one bounded paging loop in `Client.task_logs()` that drives a new `Client.task_logs_page()` sibling. The paging PATTERN copies the SDK's existing `_fetch_all` / `_get_page` split exactly; the TYPE cannot be shared with `Page[T]` because the cursor is an ordered `int` under a different wire key. Every fixture body in the test suite is hand-written from plain dict literals, never dumped from the models under test.

**Tech Stack:** Python 3.9-3.13, pydantic v2, httpx (`httpx.MockTransport`), pytest, ruff, mypy strict.

Spec: `docs/superpowers/specs/2026-08-26-python-sdk-envelope-sweep.md` (commit `db95be2`)
Backlog item: `docs/backlog/bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys.md`

---

## Slice independence declaration

**There is exactly ONE slice and it has ZERO parallelism.**

- This is a **zero-Go, zero-frontend diff.** Every file touched is under `python/` (plus one `git mv` under `docs/backlog/`, which is the conductor's step, not an engineer's). Nothing under `internal/` or `web/` is edited, and reading `internal/api/tasks.go` and `internal/cli/logs.go` as reference is required but they are **read-only**.
- **One agent owns the whole thing.** There is no backend/frontend split to run in parallel in Phase 3, because there is no Go and no TypeScript. Dispatch a single Python-capable engineer (`relay-backend-engineer` is the closest role fit; the work is SDK client code and pytest under TDD). Do not open a second lane.
- **The tasks within the slice are sequential, not independent.** Task N's implementation is what Task N+1's test is written against. Task 4's `task_logs_page()` is called by Task 5's `task_logs()`; Task 6's stops sit inside Task 5's loop. Do not reorder.
- **The suite is RED from the end of Task 1 until the end of Task 5.** That is deliberate and is the measurement the backlog item's acceptance criterion demands. Task 1 changes test files only and its whole purpose is to produce that RED. Do not "fix it early" by writing production code inside Task 1.
- For the conductor's Phase 4 fan-out: the integration-lane brief's "skip on a zero-Go diff" carve-out **does** apply to the Go integration lane. There is no Go here. What the integration agent should do instead is stated in "Verification gates".

---

## What this plan refutes, corrects, or adds to the spec

Read this section before writing code. I read the spec once asking only whether it contradicts itself, contradicts the tree, or prescribes something that does not exist. **Eight things did not survive; six spec claims I specifically tried to break and could not.** Cited by symbol throughout.

### Refuted: three of the spec's test sketches are vacuous mutation kills as written

Memory: *plan-supplied tests are untrusted*. I checked each sketched test against the loop it claims to guard, by hand-executing the mutation it exists to kill.

1. **T6 (stop 2) as specified CANNOT kill its own mutation.** The spec says "Page 2 returns `next_seq` equal to the `since_seq` it was asked for -> `ProtocolError`", with two pages. Delete the `page.next_seq <= since` line and hand-execute: `since` is never updated, the handler keeps answering the same page, and the loop spins to `_MAX_LOG_PAGES` where **stop 3 raises `ProtocolError` too**. A `pytest.raises(ProtocolError)` assertion is GREEN against the mutant, after 10000 requests. **Two corrections, both required:** the handler must serve a normal DRAINED page 3 so the mutant terminates and returns records, and the assertion must be `pytest.raises(ProtocolError, match="did not advance")` so a different stop firing cannot satisfy it.

2. **T7 (stop 3) as specified HANGS under its own mutation.** "A never-draining handler (always a full page, always an advancing `next_seq`)" plus a shrunken cap. Delete the cap check and the loop runs forever; `pyproject.toml` sets `addopts = "-ra"` and the project has no `pytest-timeout` dependency, so nothing stops it. **Correction:** the handler returns a 500 for any request past the cap, so the mutant terminates with `ServerError` - which is not `ProtocolError` - and the test is RED instead of hung.

3. **T9's request-count assertion is vacuous unless page 2 advertises more.** The spec says "Two pages of 200 with `task_logs(limit=250)` -> exactly 250 records, and exactly 2 requests (asserting the request count is what proves the cap short-circuits)". If page 2 reports drained, the loop makes exactly 2 requests with or without the user cap and the count proves nothing. **Correction:** the fixture must be a log LONGER than two pages, so an implementation that trims at the end makes a third request.

### Refuted: one spec rule is right for `LogPage` and wrong for `Job`

4. **The spec's "no benign default" rule must NOT be applied to D3's six `Job` fields.** The spec states the rule for `LogPage` ("A defaulted `next_seq: int = 0` would read a missing key as drained") and then, four sections later, prescribes "six optional fields; all default to absent/zero". Those two sentences are in tension and the resolution is not stated. Here it is: **`Job` is the AUTHORING model as well as the response model.** `Job(name="nightly")` is the README's first example and appears in roughly fifteen existing tests; `Job.add_task` constructs from it. A required `total_tasks` would break every authoring call site and turn the whole suite red for no gain. `LogPage` is response-only and has no authoring caller, so the strict rule costs nothing there. Defaults on `Job`, no defaults on `LogPage`, and the difference is *response-only vs dual-use*, not inconsistency. Task 7 adds a test that pins the authoring path so this cannot be "tidied" later.

### Refuted: two spec omissions that would produce a second idiom

5. **The spec never says whether `task_logs()` calls `task_logs_page()`.** It presents two signatures and a pseudocode loop that builds its own request line. Two independent request builders means T12 (`limit=200` on the first request) and T10 (`since_seq` absent when 0) pin two different pieces of code, and the `?since_seq=` / `?limit=` policy would live in two places. **Decision: `task_logs()` drives `task_logs_page()`.** One request builder, one place the query policy lives. This is also what makes T12's assertion meaningful.

6. **T11 needs a named exception class and it is NOT a `RelayError`.** The spec says only "A body missing `next_seq` raises". `LogPage.model_validate` raises `pydantic_core.ValidationError`, which by the spec's own D8 does not descend from `RelayError`. Stated explicitly so the engineer does not write `pytest.raises(relay.ValidationError)`, watch it fail, and "fix" it by wrapping - which is D8, declined, and would make this diff two features.

### Refuted: one structural claim about stop 2's position

7. **Stop 2 is unreachable on page 1, so the "poisoned page first" rule cannot be satisfied literally for T6.** On the first iteration `since == 0`, and `next_seq == 0` is already consumed by the drained arm above, so `page.next_seq <= since` can only fire on a negative cursor. Page 2 is the earliest position the discriminating input can occupy. The plan states the substitute discriminator (the `match=` on the message, plus a drained page 3) rather than silently ignoring the rule. Every OTHER discriminating page in this plan does go first: T5's empty page is request 1, T7's and T8's misbehaving pages are request 1 onward.

### Refuted: one line citation

8. **The route-table citation has rotted.** The spec says it enumerated from `internal/api/server.go` `Handler()`, "lines 96-207". `Handler()` opens at line **88**. The registrations do start around 96, so the substance holds and only the number is wrong. Every other citation in the spec resolves (see below). Cite by symbol.

### Added beyond the spec, flag for conductor veto

9. **One prose fix the spec declined as code but left false in the README.** `python/README.md` states "All exceptions descend from `relay.RelayError`". D8 establishes that this is false at every `model_validate` site and has been since the SDK shipped, and declines the CODE fix - correctly, it is a cross-cutting wrap needing its own RED. But the plan edits the table that sentence heads, and shipping a known-false contract while editing it is the *wrong prose about correct code* defect this project keeps finding. The fix is to correct the ADVERTISEMENT, which is one sentence: say that decode failures raise `pydantic.ValidationError` and do not descend from `RelayError`. This is in Task 9. If the conductor wants D8 strictly out, delete that sentence from Task 9 and nothing else changes.

### What I tried to break in the spec and could NOT

"The input was correct" is indistinguishable from having checked nothing, so here is what I actually measured rather than assumed:

- **Every other line citation in the spec resolves at HEAD.** `client.py:266` is the defective comprehension (line 264 is the `self._http.get`, exactly as the spec says the backlog item got wrong). `test_client.py:184` is `test_task_logs_parses_records`. `internal/api/tasks.go:132` is the `writeJSON` of the envelope. `internal/store/query/tasks.sql:682-688` is `GetTaskLogsPage` and it reads `WHERE task_id = $1 AND id > $2 ORDER BY id LIMIT $3` verbatim. `internal/api/workers.go:32` is `RevokedAt *time.Time` with `json:"revoked_at,omitempty"`. `internal/api/events.go:50-53` is the "?job_id= is deliberately NOT validated" comment. `handleEvents:84` is the `event: dropped` frame. `client.py:78` is the injected-client header merge. `web/src/jobs/api.ts:128-168` is `TaskLogPage` and its explicit-limit comment.
- **The counts are exact.** I counted independently: 12 pydantic models in `models.py` and **85 fields** across them (Sync 2, Source 6, Task 12, Job 10, LogRecord 3, Event 2, ScheduledJob 13, Page 3, Worker 14, Reservation 9, AgentEnrollment 5, User 6). **25 HTTP-performing public `Client` methods**, plus `close` / `__enter__` / `__exit__` = 28 public. **`python/README.md`'s Client API table names 15** of them. All four numbers match the spec. **Do not "update" the 85 or the 25 after this slice lands** - they describe the tree that was swept, and re-stating them post-fix destroys the record.
- **The drain rule is what the spec says it is.** `handleGetTaskLogs` sets `nextSeq = l.ID` in the row loop and then `if int32(len(items)) < limit { nextSeq = 0 }`. So a FULL page always carries a non-zero cursor, including the page that exhausts the table - which is the entire justification for T4's second request and for stop 3's two-message split.
- **The CI claim holds.** `.github/workflows/python.yml` has exactly two jobs, `test` (3 OS x 5 Python = 15 cells, all `pytest tests/unit -v`) and `lint` (`ruff check src tests`, `mypy src`). There is no integration job. `python/tests/integration/test_smoke.py:26` calls `client.task_logs(tasks[0].id)` and has never run in CI.
- **D4's five event types are all real.** `"job"` (`internal/api/jobs.go` x2, `internal/scheduler/dispatch.go`), `"task"` (`dispatch.go` x2, `internal/worker/handler.go`), `"worker"` (`internal/metrics/sweep.go`, `internal/worker/handler.go`), `events.TypeTaskLog = "task_log"` (`internal/events/broker.go`), and `"dropped"` written as a raw frame by `handleEvents`. Five, not four, not six.
- **D5 holds.** `handleEvents`'s `for`/`select` has exactly two exits: `r.Context().Done()` and the broker closing the channel. There is no job-terminality condition anywhere in the function. `follow_job`'s docstring is wrong, and so is the README's `follow_job(id)` row ("until the job is terminal") - **the spec's Files-changed list catches the README half; make sure both are fixed.**
- **`models.py` has `from __future__ import annotations` at line 1.** The 3.9 hazard the conductor flagged does not bite: `list[LogRecord]` is safe in an annotation there, and `Task.commands: list[list[str]]` is the existing proof (the 3.9 CI cell is green today).

---

## Critical files

| File | Role |
|---|---|
| `python/src/relay/client.py` | The defect (`task_logs`, line 266 at HEAD) and most of the fix. `_PAGE_REQUEST_LIMIT` at line 55 is the sibling `_MAX_LOG_PAGES` copies. `_fetch_all` (line 138) is the paging PATTERN to follow. |
| `python/src/relay/models.py` | `LogRecord` (line 347), `EventType` (line 50), `Worker` (line 405), `Job` (line 232). Add `LogPage`. Has `from __future__ import annotations` at line 1, so `list[LogRecord]` annotations are 3.9-safe. |
| `python/src/relay/errors.py` | Add `ProtocolError`. `RelayError` is the base; `TimeoutError` (line 60) is the message-only precedent. |
| `python/src/relay/__init__.py` | Export `LogPage` and `ProtocolError`; both import blocks and `__all__` are alphabetically sorted - keep them so. |
| `python/tests/unit/test_client.py` | The stale fixture (`test_task_logs_parses_records`, line 184). `_make_client` (line 22) and `_page_response` (line 50) are the existing helper precedents. |
| `python/tests/unit/test_models.py` | Model-level pins for `LogPage`, `LogRecord.seq`, `Worker.revoked_at`, `Job` enrichment, `EventType`. |
| `python/tests/unit/test_errors.py` | `ProtocolError` inheritance pin. |
| `python/tests/unit/test_packaging.py` | **New file.** README-table and version-lockstep guards. |
| `python/README.md` | Client API table (15 -> 27 rows), error table, `follow_job` contract, a `task_logs` paging note. |
| `python/pyproject.toml`, `python/src/relay/_version.py` | `0.1.2` -> `0.1.3`, in lockstep. |
| `internal/api/tasks.go` | **READ-ONLY REFERENCE.** `handleGetTaskLogs` + `logEntry` are the contract the test simulator reproduces. Do not edit. |
| `internal/cli/logs.go` | **READ-ONLY REFERENCE.** `printTaskLogs` is the paging loop being ported, including its two-message page-cap split. Read its comments. Do not edit. |
| `web/src/jobs/api.ts` | **READ-ONLY REFERENCE.** `TaskLogPage` + `fetchTaskLogPage`, a third working implementation of this envelope. Do not edit. |

---

## The stop-to-test kill table

The three stops are separately mutable. Deleting any ONE must turn a distinct test RED. This table is the contract; Task 6's verification step walks it.

| # | Mutation (delete or change exactly this) | Test that goes RED | Why the kill is not vacuous |
|---|---|---|---|
| 1 | `if page.next_seq == 0: return out` | `test_task_logs_pages_to_the_end_with_verbatim_cursor` | Without it the walk never returns normally; the honest simulator's next page is empty and reports drained, so **stop 1** fires and the test raises instead of returning 450 records. |
| 2 | Swap the drained arm and stop 1 (order mutation) | `test_task_logs_exact_page_multiple_is_not_a_protocol_error` | A 200-row log's final page is legitimately empty AND reports drained. Checking `not page.items` first raises `ProtocolError` against a correct server. |
| 3 | `if not page.items: raise ProtocolError` (stop 1) | `test_task_logs_raises_on_empty_page_that_is_not_drained` | The poisoned page is request **1**, with a normal drained page 2 after it, so the mutant TERMINATES returning `[]` instead of raising. Poison-last could not detect an early-exit mutation; poison-with-nothing-after would hang. |
| 4 | `if page.next_seq <= since: raise ProtocolError` (stop 2) | `test_task_logs_raises_when_cursor_does_not_advance` | Drained page 3 makes the mutant terminate and return records. `match="did not advance"` is what stops **stop 3** from satisfying the assertion after 10000 spins - without it this kill is vacuous (see refutation 1). |
| 5 | `if pages >= self._MAX_LOG_PAGES: raise ProtocolError` (stop 3) | `test_task_logs_raises_at_the_page_cap` | The handler 500s past the cap, so the mutant raises `ServerError` (not `ProtocolError`) instead of hanging. `assert len(calls) == 3` distinguishes the cap from a different stop firing. |
| 6 | Delete the `page.total` branch of stop 3's message | `test_task_logs_page_cap_message_does_not_blame_the_server_when_total_is_reached` | Asserts the message does NOT contain "may be longer" and DOES contain "every one was collected". |
| 7 | `since = page.next_seq` -> `page.next_seq + 1` | `test_task_logs_pages_to_the_end_with_verbatim_cursor` | Seqs are CONTIGUOUS 1..450, so `+1` drops row 201 and `-1` returns row 200 twice; plus a direct `calls[1]["since_seq"] == "200"` assertion on the wire. |
| 8 | Move `return out[:limit]` below the loop | `test_task_logs_limit_caps_total_records` | The fixture is 450 rows, so trimming at the end makes THREE requests; the test asserts two. |
| 9 | Delete `limit=self._PAGE_REQUEST_LIMIT` from the request | `test_task_logs_sends_explicit_page_limit` | Asserts `calls[0]["limit"] == "200"` on the wire. A record-count assertion cannot catch this: the server default of 50 still returns every row of a short fixture, just in more requests. |
| 10 | `seq: int` -> `seq: int = 0` on `LogRecord` | `test_log_record_requires_seq` | A row without `seq` must raise, not silently become row zero. |
| 11 | `next_seq: int` -> `next_seq: int = 0` on `LogPage` (same for `total`) | `test_log_page_requires_next_seq_and_total` | A missing key would read as "drained" and silently return page 1 - the defect this slice exists to fix, rebuilt inside the fix. |
| 12 | Revert `task_logs` to the HEAD comprehension (the headline) | `test_task_logs_parses_records` + 8 others | See Task 11, "The RED proof procedure". |

---

## Task 1: The fixture tells the truth, and that is the RED

This task changes **test files only**. No production file is touched. Its purpose is to produce and record the measurement the backlog item's acceptance criterion demands: the new fixture in front of the unchanged production decode is RED, and RED for the right reason.

**Files:**
- Modify: `python/tests/unit/test_client.py` (add helpers; replace `test_task_logs_parses_records` at line 184)

- [ ] **Step 1: Add the fixture helpers to `python/tests/unit/test_client.py`**

Insert immediately after `_page_response` (which ends at line 55), before the `# --- Auth & wiring ---` header:

```python
# ─── task-log fixtures ────────────────────────────────────────────────────────
#
# These build the wire bodies GET /v1/tasks/{id}/logs returns (handleGetTaskLogs,
# internal/api/tasks.go) from plain dict literals with HAND-WRITTEN keys.
#
# They are deliberately NOT built by dumping LogRecord or LogPage. A fixture
# built out of the model under test cannot detect drift in that model: if
# LogPage's field names were wrong, the fixture would emit the same wrong keys
# and every test in this file would stay green against an SDK that cannot talk
# to the real server. That is precisely the failure this file is being changed
# to fix - the old test_task_logs_parses_records hand-wrote a bare array and so
# asserted the SDK agreed with itself for three and a half months. Do NOT
# "de-duplicate" these helpers against the models; doing so re-opens the bug.


def _log_row(seq: int, *, stream: str = "stdout", content: str = "") -> dict[str, Any]:
    """One row, shaped like api.logEntry: {seq, stream, content, created_at}."""
    return {
        "seq": seq,
        "stream": stream,
        "content": content or f"line {seq}\n",
        "created_at": "2026-05-06T12:00:00Z",
    }


def _log_rows(first: int, last: int) -> list[dict[str, Any]]:
    """Rows with CONTIGUOUS seqs, inclusive of both ends.

    The contiguity is load-bearing. since_seq is exclusive server-side
    (GetTaskLogsPage is `WHERE task_id = $1 AND id > $2`), so the cursor is the
    previous page's next_seq verbatim. With contiguous ids a `since = next_seq
    + 1` mutation skips exactly one row and a `- 1` returns one twice, and the
    seq-list assertions below catch both. With a GAP in the ids (7 then 20)
    both mutations land harmlessly between rows and are undetectable. Do not
    "tidy" these into sparse ids.
    """
    return [_log_row(seq) for seq in range(first, last + 1)]


def _log_page(rows: list[dict[str, Any]], *, next_seq: int, total: int) -> dict[str, Any]:
    """The envelope: {items, next_seq, total}, keys hand-written."""
    return {"items": rows, "next_seq": next_seq, "total": total}
```

- [ ] **Step 2: Replace `test_task_logs_parses_records`**

Delete the existing function at lines 184-196 and put this in its place:

```python
def test_task_logs_parses_records() -> None:
    """The headline. The server has written {items, next_seq, total} since
    2026-05-08 (a90c727); the SDK iterated the dict, which in Python yields its
    KEYS, and validated the strings "items"/"next_seq"/"total" as LogRecords.

    The assertion is POSITIVE and positional, never pytest.raises: a test that
    asserts an exception is green against a client that raises for an unrelated
    reason.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json=_log_page(
                [
                    _log_row(7, stream="stdout", content="hi\n"),
                    _log_row(8, stream="stderr", content="warn\n"),
                ],
                next_seq=0,
                total=2,
            ),
        )

    client = _make_client(handler)
    logs = client.task_logs("abc")
    assert [(r.seq, r.stream, r.content) for r in logs] == [
        (7, "stdout", "hi\n"),
        (8, "stderr", "warn\n"),
    ]
```

- [ ] **Step 3: Run it and RECORD the RED - this is a deliverable, not a checkbox**

From `python/`:

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py::test_task_logs_parses_records -q
```

Expected: **FAIL**, and the failure must be `pydantic_core._pydantic_core.ValidationError` raised out of `LogRecord.model_validate`, carrying:

```
Input should be a valid dictionary or instance of LogRecord
[type=model_type, input_value='items', input_type=str]
```

**Check the failure text before moving on.** If it is instead `AttributeError: 'LogRecord' object has no attribute 'seq'`, the RED came from the test's own forward reference to a field that does not exist yet rather than from the production defect, and the measurement is worthless. It should not be - `model_validate` raises before any attribute is read - but *verify it, do not assume it*. Paste the exact text into the task report.

- [ ] **Step 4: Commit the RED**

```bash
git add python/tests/unit/test_client.py
git commit -m "test(python): task_logs fixture emits the real envelope (RED)"
```

---

## Task 2: `LogRecord.seq` and the `LogPage` model

**Files:**
- Modify: `python/src/relay/models.py` (`LogRecord`, line 347), `python/src/relay/__init__.py`
- Test: `python/tests/unit/test_models.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_models.py`, and extend its `from relay import (...)` block to include `LogPage` and `LogRecord` (keep it alphabetically sorted: `..., Job, JobStatus, LogPage, LogRecord, Page, Priority, ...`):

```python
# ─── LogRecord / LogPage ──────────────────────────────────────────────────────


def test_log_record_parses_a_server_row() -> None:
    r = LogRecord.model_validate(
        {
            "seq": 42,
            "stream": "stdout",
            "content": "hello\n",
            "created_at": "2026-05-06T12:00:00Z",
        }
    )
    assert r.seq == 42
    assert r.stream == "stdout"


def test_log_record_requires_seq() -> None:
    """seq has NO default. It is the only thing that correlates a record with a
    LogPage.next_seq cursor, the server has emitted it since 2026-05-08, and a
    defaulted `seq: int = 0` would read a missing key as row zero - the same
    absent-field-benign-default shape as a defaulted next_seq.
    """
    with pytest.raises(PydanticValidationError):
        LogRecord.model_validate(
            {"stream": "stdout", "content": "hi\n", "created_at": "2026-05-06T12:00:00Z"}
        )


def test_log_page_parses_the_envelope() -> None:
    page = LogPage.model_validate(
        {
            "items": [
                {
                    "seq": 7,
                    "stream": "stdout",
                    "content": "hi\n",
                    "created_at": "2026-05-06T12:00:00Z",
                }
            ],
            "next_seq": 7,
            "total": 3,
            "future_field": "ignored",
        }
    )
    assert [r.seq for r in page.items] == [7]
    assert page.next_seq == 7
    assert page.total == 3


@pytest.mark.parametrize("missing", ["next_seq", "total"])
def test_log_page_requires_next_seq_and_total(missing: str) -> None:
    """A deliberate departure from _get_page's body.get("next_cursor", "").

    A defaulted next_seq: int = 0 would read a missing key as "drained" and
    silently return page 1 - which is the same shape as the defect this whole
    slice exists to fix, rebuilt inside the fix. The handler writes both keys
    unconditionally from a map literal, so requiring them costs nothing.
    """
    body = {"items": [], "next_seq": 5, "total": 9}
    del body[missing]
    with pytest.raises(PydanticValidationError):
        LogPage.model_validate(body)
```

- [ ] **Step 2: Run them to verify they fail**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_models.py -q -k "log_record or log_page"
```

Expected: FAIL. `ImportError: cannot import name 'LogPage' from 'relay'` at collection, which reds the whole module.

- [ ] **Step 3: Implement**

In `python/src/relay/models.py`, replace `LogRecord` (lines 347-352) with:

```python
class LogRecord(BaseModel):
    """One line of a task's log, as served by GET /v1/tasks/{id}/logs.

    ``seq`` is the row's global ``task_logs.id``. It is REQUIRED and has no
    default for three reasons: it is the only way a caller using
    :meth:`relay.Client.task_logs_page` can correlate a record with a cursor,
    every other field here is required, and a defaulted ``seq: int = 0`` reads
    a missing key as row zero.
    """

    model_config = ConfigDict(extra="ignore")

    seq: int
    stream: str
    content: str
    created_at: datetime


class LogPage(BaseModel):
    """One page of a task's log, forward-only from ``since_seq``.

    ``next_seq`` is 0 when the server reports the log drained; otherwise it is
    the cursor for the next request, passed VERBATIM as ``?since_seq=`` because
    the server's predicate is ``id > $2`` (exclusive - see GetTaskLogsPage in
    internal/store/query/tasks.sql). Never ``next_seq + 1``: ``task_logs.id`` is
    a global BIGSERIAL, so when one task logs alone its ids are contiguous and
    +1 skips the very next row.

    ``next_seq`` and ``total`` are REQUIRED, unlike :class:`Page`'s cursor,
    which is read with ``body.get("next_cursor", "")``. A defaulted
    ``next_seq: int = 0`` would read a missing key as "drained" and silently
    return page 1 - the same shape as the defect this model exists to fix.
    """

    model_config = ConfigDict(extra="ignore")

    items: list[LogRecord]
    next_seq: int
    total: int
```

- [ ] **Step 4: Export both from the package**

In `python/src/relay/__init__.py`, add `LogPage` to the `from .models import (...)` block (between `JobStatus` and `LogRecord`) and to `__all__` (between `"JobStatus"` and `"LogRecord"`). Both lists are alphabetically sorted; keep them so.

- [ ] **Step 5: Run to verify they pass**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_models.py -q
```

Expected: PASS, all model tests.

- [ ] **Step 6: Commit**

```bash
git add python/src/relay/models.py python/src/relay/__init__.py python/tests/unit/test_models.py
git commit -m "feat(python): LogPage model and required LogRecord.seq"
```

---

## Task 3: `ProtocolError`

**Files:**
- Modify: `python/src/relay/errors.py`, `python/src/relay/__init__.py`
- Test: `python/tests/unit/test_errors.py`

- [ ] **Step 1: Write the failing test**

Append to `python/tests/unit/test_errors.py`, and add `ProtocolError` and `RelayError` to its `from relay import (...)` block (sorted: `AuthError, Conflict, HTTPError, NotFound, ProtocolError, RelayError, ServerError, ValidationError`):

```python
def test_protocol_error_is_a_relay_error() -> None:
    """The three paging stops need a class callers can catch. ServerError is
    wrong (the status was 200) and RelayError itself is untypeable by a caller
    who wants to distinguish this from everything else.
    """
    assert issubclass(ProtocolError, RelayError)
    err = ProtocolError("server cursor did not advance (next_seq 7 after since_seq 7)")
    assert "did not advance" in str(err)
```

- [ ] **Step 2: Run it to verify it fails**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_errors.py -q
```

Expected: FAIL at collection, `ImportError: cannot import name 'ProtocolError' from 'relay'`.

- [ ] **Step 3: Implement**

In `python/src/relay/errors.py`, insert after `HTTPError` (which ends at line 57) and before `TimeoutError`:

```python
class ProtocolError(RelayError):
    """The server answered with well-formed HTTP that is not a usable relay
    response: a page that advertises more rows but carries none, a cursor that
    does not advance, or a log that never reports itself drained.

    Message-only, like :class:`TimeoutError` and unlike the status-derived
    errors above: it is raised from a walk across several responses, so there
    is no single ``httpx.Response`` that explains it.
    """
```

In `python/src/relay/__init__.py`, add `ProtocolError` to the `from .errors import (...)` block (between `NotFound` and `RelayError`) and to `__all__` (between `"Priority"` and `"RelayError"`).

- [ ] **Step 4: Run to verify it passes**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_errors.py -q
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add python/src/relay/errors.py python/src/relay/__init__.py python/tests/unit/test_errors.py
git commit -m "feat(python): ProtocolError for unusable 200 responses"
```

---

## Task 4: `task_logs_page()`

One page, one envelope. **`task_logs()` is NOT touched in this task and stays broken** - `test_task_logs_parses_records` is still RED at the end of it, and that is expected. It is Task 5's GREEN.

**Files:**
- Modify: `python/src/relay/client.py` (imports; a new method inserted immediately before `task_logs` at line 262)
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the failing tests**

Append to the `# --- tasks / logs ---` section of `python/tests/unit/test_client.py`, after `test_task_logs_parses_records`:

```python
def test_task_logs_page_returns_one_envelope_and_sends_its_params() -> None:
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        return httpx.Response(200, json=_log_page([_log_row(11)], next_seq=11, total=42))

    client = _make_client(handler)
    page = client.task_logs_page("abc", since_seq=10, limit=25)
    assert [r.seq for r in page.items] == [11]
    assert page.next_seq == 11
    assert page.total == 42
    assert calls[0]["since_seq"] == "10"
    assert calls[0]["limit"] == "25"

    # since_seq=0 means "from the beginning" and is not sent. limit is sent
    # only when the caller gives one, so a hand-pager sees the server's default
    # of 50 and is told there is more by next_seq. task_logs() is the opposite -
    # it ALWAYS sends limit=200, because there the truncation would be silent.
    # The asymmetry is deliberate.
    client.task_logs_page("abc")
    assert "since_seq" not in calls[1]
    assert "limit" not in calls[1]


def test_task_logs_page_raises_on_a_bare_array_body() -> None:
    """A server rollback to the pre-2026-05-08 bare array must be LOUD, not an
    empty log. The whole body goes through LogPage.model_validate, so the model
    is the pin - the client never hand-picks keys.

    pydantic's ValidationError does NOT descend from relay.RelayError. That is a
    known, separately-tracked gap (the README says otherwise and is corrected in
    this slice); do not "fix" it here by wrapping.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=[_log_row(1), _log_row(2)])

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.task_logs_page("abc")
```

Add one import to `test_client.py`. Ruff's isort puts straight imports before from-imports within the third-party block, so the block becomes exactly:

```python
import httpx
import pytest
from pydantic import ValidationError as PydanticValidationError

from relay import (
```

(This mirrors `test_models.py`, which already has `import pytest` followed by the same `from pydantic import ...` line.)

- [ ] **Step 2: Run them to verify they fail**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py -q -k "task_logs_page"
```

Expected: FAIL, 2 tests, `AttributeError: 'Client' object has no attribute 'task_logs_page'`.

- [ ] **Step 3: Implement**

In `python/src/relay/client.py`, add `LogPage` to the `from .models import (...)` block (between `JobStatus` and `LogRecord`) and `ProtocolError` to the `from .errors import (...)` block (between `AuthError` and `ValidationError` - uppercase names sort before the lowercase `raise_for_response`).

Insert this method **immediately before** the existing `task_logs` (line 262). Do not touch `task_logs` itself yet:

```python
    def task_logs_page(
        self, task_id: str, *, since_seq: int = 0, limit: Optional[int] = None
    ) -> LogPage:
        """Fetch one page of a task's log.

        ``limit`` is the PAGE SIZE (1-200); omitted, the server's default of 50
        applies, which is visible here because ``next_seq`` tells the caller
        there is more. Pass the returned ``next_seq`` back as ``since_seq=`` to
        page forward - VERBATIM, never ``+ 1``: the server's predicate is
        ``id > $2``, so the cursor is exclusive already.
        """
        self._require_token()
        params: dict[str, str] = {}
        if since_seq:
            params["since_seq"] = str(since_seq)
        if limit is not None:
            params["limit"] = str(limit)
        response = self._http.get(f"/v1/tasks/{task_id}/logs", params=params)
        raise_for_response(response)
        # The WHOLE body goes through the model, never a hand-picked
        # body["items"]. The model is the pin: a missing next_seq or total
        # raises here rather than reading as "drained".
        return LogPage.model_validate(response.json())
```

- [ ] **Step 4: Run to verify they pass**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py -q -k "task_logs_page"
```

Expected: PASS, 2 tests. Running the whole file still shows `test_task_logs_parses_records` FAILING - expected, and closed by Task 5.

- [ ] **Step 5: Commit**

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "feat(python): task_logs_page returns one log envelope"
```

---

## Task 5: `task_logs()` auto-paginates

This is the GREEN for Task 1's RED.

**Files:**
- Modify: `python/src/relay/client.py` (`task_logs`; `_MAX_LOG_PAGES` beside `_PAGE_REQUEST_LIMIT` at line 55)
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Add the honest server simulator to the test module**

Insert into `python/tests/unit/test_client.py` immediately after `_log_page`:

```python
def _serve_logs(
    rows: list[dict[str, Any]], calls: list[dict[str, str]]
) -> Callable[[httpx.Request], httpx.Response]:
    """A behavioural simulator of handleGetTaskLogs (internal/api/tasks.go).

    Four behaviours are load-bearing and each is asserted by a test below:

      - ?since_seq is EXCLUSIVE - rows with seq > since_seq, because the SQL is
        `WHERE task_id = $1 AND id > $2` (GetTaskLogsPage,
        internal/store/query/tasks.sql).
      - ?limit defaults to 50, and a value outside 1..200 is a 400. The handler
        REJECTS, it does not clamp.
      - next_seq is the last returned row's seq, zeroed whenever the page is
        SHORT (len(items) < limit). So a full final page still carries a
        non-zero cursor and one more request is needed to learn the log ended.
      - total is the full row count, independent of the page.

    Tests of the client's misbehaving-server stops do NOT use this - by
    construction it cannot misbehave - and hand-write their own handler.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        params = dict(request.url.params)
        calls.append(params)

        limit = 50
        if "limit" in params:
            if not params["limit"].isdigit() or not 1 <= int(params["limit"]) <= 200:
                return httpx.Response(400, json={"error": "limit must be 1..200"})
            limit = int(params["limit"])

        since = 0
        if "since_seq" in params:
            if not params["since_seq"].isdigit():
                return httpx.Response(
                    400, json={"error": "since_seq must be a non-negative integer"}
                )
            since = int(params["since_seq"])

        page = [r for r in rows if r["seq"] > since][:limit]
        next_seq = 0 if len(page) < limit else page[-1]["seq"]
        return httpx.Response(200, json=_log_page(page, next_seq=next_seq, total=len(rows)))

    return handler
```

- [ ] **Step 2: Write the failing tests**

Append to the `# --- tasks / logs ---` section:

```python
def test_task_logs_pages_to_the_end_with_verbatim_cursor() -> None:
    """450 rows at limit=200 is two full pages plus one short page.

    The seq list is the strongest assertion available: it proves no row was
    dropped AND none was returned twice. Because the seqs are contiguous, a
    `since = next_seq + 1` mutation drops row 201 and a `- 1` returns row 200
    twice, so both die here. The explicit since_seq assertions pin the same
    property on the wire, where it cannot be argued about.
    """
    calls: list[dict[str, str]] = []
    client = _make_client(_serve_logs(_log_rows(1, 450), calls))

    logs = client.task_logs("abc")

    assert [r.seq for r in logs] == list(range(1, 451))
    assert len(calls) == 3
    assert "since_seq" not in calls[0]
    assert calls[1]["since_seq"] == "200"  # verbatim: next_seq, never next_seq + 1
    assert calls[2]["since_seq"] == "400"


def test_task_logs_sends_explicit_page_limit() -> None:
    """Without an explicit limit the server default is 50 and a long log is
    silently truncated at the first stop. Nothing else in this file would
    notice, because the simulator still returns every row of a short fixture -
    just in more requests. The wire assertion is the only kill.
    """
    calls: list[dict[str, str]] = []
    client = _make_client(_serve_logs(_log_rows(1, 3), calls))

    client.task_logs("abc")

    assert calls[0]["limit"] == "200"


def test_task_logs_exact_page_multiple_is_not_a_protocol_error() -> None:
    """A log whose length is an exact multiple of the page size legitimately
    produces a final EMPTY page - and that page reports drained, so the drained
    arm returns before stop 1 can see it. This is the case stop 1 must NOT
    catch, and it is the guard on the ORDER of those two arms.

    The second request is not optional: a full final page always carries a
    non-zero cursor, so the client cannot know it is done without asking again.
    """
    calls: list[dict[str, str]] = []
    client = _make_client(_serve_logs(_log_rows(1, 200), calls))

    logs = client.task_logs("abc")

    assert [r.seq for r in logs] == list(range(1, 201))
    assert len(calls) == 2
    assert calls[1]["since_seq"] == "200"


def test_task_logs_limit_caps_total_records() -> None:
    """limit caps the TOTAL records, and it short-circuits: the fixture is 450
    rows, so an implementation that walks the whole log and trims at the end
    makes THREE requests. The request count is what proves the short-circuit;
    the record count alone cannot.
    """
    calls: list[dict[str, str]] = []
    client = _make_client(_serve_logs(_log_rows(1, 450), calls))

    logs = client.task_logs("abc", limit=250)

    assert [r.seq for r in logs] == list(range(1, 251))
    assert len(calls) == 2
```

- [ ] **Step 3: Run them to verify they fail**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py -q -k "task_logs"
```

Expected: 5 FAILED, 2 passed (the two `task_logs_page` tests from Task 4 are green and are matched by this `-k`). Specifically:

- `test_task_logs_parses_records`, `..._pages_to_the_end_with_verbatim_cursor`, `..._sends_explicit_page_limit` and `..._exact_page_multiple_is_not_a_protocol_error` fail with `pydantic_core.ValidationError`, `type=model_type, input_value='items'` - the HEAD comprehension iterating the envelope's keys.
- `test_task_logs_limit_caps_total_records` fails EARLIER, with `TypeError: task_logs() got an unexpected keyword argument 'limit'`. It never reaches the decode.

- [ ] **Step 4: Implement**

In `python/src/relay/client.py`, add beside `_PAGE_REQUEST_LIMIT` (line 55):

```python
    # Bounds the log paging loop against a server whose next_seq keeps advancing
    # but which never reports the log as drained. 10000 pages at
    # _PAGE_REQUEST_LIMIT rows is 2,000,000 rows: a hang bound, not a product
    # limit. A CLASS attribute, like _PAGE_REQUEST_LIMIT, so a test can shrink
    # it - which means the loop must read it off `self`, not off a module global.
    _MAX_LOG_PAGES = 10000
```

Replace `task_logs` with this. The three stops are Task 6; this version has the drained arm and the user cap only:

```python
    def task_logs(self, task_id: str, *, limit: Optional[int] = None) -> list[LogRecord]:
        """Fetch a task's complete log, auto-paginating across pages.

        ``limit`` caps the TOTAL number of records returned (None = all). Each
        request fetches ``_PAGE_REQUEST_LIMIT`` rows; without that explicit
        limit the server's default is 50 and a long log is silently truncated.

        This accumulates the whole log in memory. ``relay logs`` does not - it
        prints each page as it arrives - and this cannot, while returning a
        list. On a very large log use :meth:`task_logs_page` (O(one page)) or
        pass ``limit=``.
        """
        out: list[LogRecord] = []
        since = 0
        pages = 0
        while True:
            pages += 1
            page = self.task_logs_page(
                task_id, since_seq=since, limit=self._PAGE_REQUEST_LIMIT
            )
            out.extend(page.items)
            if limit is not None and len(out) >= limit:
                return out[:limit]
            # Break on next_seq, never on len(items) < limit: the two agree
            # today, but the second re-derives a rule the server already applied
            # and desynchronizes the moment the drain rule moves.
            if page.next_seq == 0:
                return out
            since = page.next_seq
```

- [ ] **Step 5: Run to verify they pass**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py -q
```

Expected: PASS, whole file. `test_task_logs_parses_records` is now GREEN - the headline RED from Task 1 is closed.

- [ ] **Step 6: Commit**

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "fix(python): task_logs decodes the envelope and pages to the end"
```

---

## Task 6: The three stops

Each stop is separately mutable and each must have a test that dies alone when it is deleted. The kill table above is the contract.

**Files:**
- Modify: `python/src/relay/client.py` (`task_logs`)
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the failing tests**

Append to the `# --- tasks / logs ---` section. Add `ProtocolError` to the `from relay import (...)` block at the top of the file, between `Priority` and `ValidationError` (`Client` is already imported there).

```python
def test_task_logs_raises_on_empty_page_that_is_not_drained() -> None:
    """Stop 1. Unreachable against the real handler, which sets next_seq = 0
    whenever the page is short - which is exactly why it must RAISE and not
    return quietly. The only server that reaches this line is misbehaving, and
    a quiet return would launder that into a completeness claim the client
    cannot support.

    The DISCRIMINATING page is request 1, with a normal page after it. A bad
    input placed LAST cannot detect an early-exit mutation; and the normal page
    2 is what makes the mutant TERMINATE (returning []) rather than spin, so
    deleting the stop is RED and not a hang.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) == 1:
            return httpx.Response(200, json=_log_page([], next_seq=5, total=3))
        return httpx.Response(200, json=_log_page([_log_row(6)], next_seq=0, total=3))

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="empty page"):
        client.task_logs("abc")
    assert len(calls) == 1


def test_task_logs_raises_when_cursor_does_not_advance() -> None:
    """Stop 2, and it is only expressible because this cursor is ORDERED - an
    opaque string cursor cannot detect a non-advancing server.

    Two things here are load-bearing and both were missing from the spec's
    sketch. The discriminating page is request 2 BY CONSTRUCTION: on request 1
    `since` is 0 and `next_seq == 0` is already consumed by the drained arm, so
    stop 2 is unreachable there. And page 3 is a normal DRAINED page, so
    deleting stop 2 makes the walk terminate and return records - without it the
    mutant would spin to the page cap and raise ProtocolError from stop 3, and
    a bare pytest.raises(ProtocolError) would pass against the mutant. The
    match= is the other half of that same guard.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) <= 2:
            # Asked for since_seq=7 on request 2, answers next_seq=7 again.
            return httpx.Response(200, json=_log_page([_log_row(7)], next_seq=7, total=9))
        return httpx.Response(200, json=_log_page([_log_row(8)], next_seq=0, total=9))

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="did not advance"):
        client.task_logs("abc")
    assert len(calls) == 2


def test_task_logs_raises_at_the_page_cap(monkeypatch: pytest.MonkeyPatch) -> None:
    """Stop 3, which catches an ever-advancing cursor that never drains -
    something neither of the other two stops can see.

    The 500 past the cap is not decoration. Without it, deleting the cap check
    leaves this handler advancing forever and the test HANGS rather than fails:
    this project has no pytest-timeout. The 500 makes the mutant terminate with
    ServerError, which is not ProtocolError, so the test is RED.

    The request-count assertion is the other half: a test that only checks the
    exception class cannot tell the cap from a different stop firing.
    """
    monkeypatch.setattr(Client, "_MAX_LOG_PAGES", 3)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 3:
            return httpx.Response(500, json={"error": "past the cap"})
        seq = len(calls) * 2
        return httpx.Response(
            200,
            json=_log_page([_log_row(seq - 1), _log_row(seq)], next_seq=seq, total=9999),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="page cap"):
        client.task_logs("abc")
    assert len(calls) == 3


def test_task_logs_page_cap_message_does_not_blame_the_server_when_total_is_reached(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Stop 3's two-message split, ported from printTaskLogs.

    A log of exactly _MAX_LOG_PAGES * 200 rows drains correctly, but its last
    page is full and so carries a non-zero cursor: the client stops one request
    short of learning it was done, having in fact collected every row. The
    envelope's own total settles that case, so the message must not tell the
    operator their log may be longer.
    """
    monkeypatch.setattr(Client, "_MAX_LOG_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        seq = len(calls) * 2
        # total == 4 == the rows this walk will have collected when the cap fires.
        return httpx.Response(
            200,
            json=_log_page([_log_row(seq - 1), _log_row(seq)], next_seq=seq, total=4),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.task_logs("abc")

    message = str(excinfo.value)
    assert "may be longer" not in message
    assert "every one was collected" in message
    assert "4 rows" in message
```

- [ ] **Step 2: Run them to verify they fail**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py -q -k "not_drained or does_not_advance or page_cap"
```

Expected: FAIL, 4 tests.

- `..._raises_on_empty_page_that_is_not_drained`: `Failed: DID NOT RAISE <class 'relay.errors.ProtocolError'>` - the walk returns `[]`.
- `..._raises_when_cursor_does_not_advance`: same, after 3 requests.
- Both cap tests: `relay.errors.ServerError: past the cap`, because with no cap the loop runs past the handler's limit.

- [ ] **Step 3: Implement**

In `python/src/relay/client.py`, replace the body of the `while True:` loop in `task_logs` so it reads exactly:

```python
        while True:
            pages += 1
            page = self.task_logs_page(
                task_id, since_seq=since, limit=self._PAGE_REQUEST_LIMIT
            )
            out.extend(page.items)
            if limit is not None and len(out) >= limit:
                return out[:limit]
            # Break on next_seq, never on len(items) < limit: the two agree
            # today, but the second re-derives a rule the server already applied
            # and desynchronizes the moment the drain rule moves.
            #
            # This arm MUST stay above the empty-page stop below. A log whose
            # length is an exact multiple of the page size legitimately produces
            # a final empty page, and that page reports drained.
            if page.next_seq == 0:
                return out
            # Three stops beyond the server's own drained signal, and all three
            # are needed. The cursor is server-supplied and drives a client
            # loop, and the provenance of a value says nothing about who
            # controls its content or the timing of the writes behind it.
            if not page.items:
                raise ProtocolError(
                    "server returned an empty page without reporting the log as "
                    f"drained (next_seq {page.next_seq} after since_seq {since})"
                )
            if page.next_seq <= since:
                raise ProtocolError(
                    "server cursor did not advance "
                    f"(next_seq {page.next_seq} after since_seq {since})"
                )
            if pages >= self._MAX_LOG_PAGES:
                if page.total > 0 and len(out) >= page.total:
                    # Do not blame the server here. A log of exactly
                    # _MAX_LOG_PAGES * _PAGE_REQUEST_LIMIT rows drains
                    # correctly, but its last page is full and so carries a
                    # non-zero cursor: we stopped one request short of learning
                    # we were done, having collected every row. The envelope's
                    # own total settles it, so do not re-raise the ambiguity.
                    raise ProtocolError(
                        f"truncated after {self._MAX_LOG_PAGES} pages - hit the "
                        f"client's page cap; the server reported {page.total} rows "
                        "and every one was collected, but it had not yet reported "
                        "the log as drained"
                    )
                raise ProtocolError(
                    f"truncated after {self._MAX_LOG_PAGES} pages - hit the client's "
                    "page cap; the log may be longer than "
                    f"{self._MAX_LOG_PAGES * self._PAGE_REQUEST_LIMIT} rows, or the "
                    "server may never report it as drained"
                )
            since = page.next_seq
```

- [ ] **Step 4: Run to verify they pass**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py -q
```

Expected: PASS, whole file.

- [ ] **Step 5: Walk the kill table - this is a deliverable, not a checkbox**

Memory: *a mutation that silently fails to apply reports "survived"*. Do these one at a time, restoring `client.py` between each (`git stash` or re-edit). For each row, record the mutation, the command, and which tests went red.

```bash
# Run after EACH mutation:
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py -q
```

| Kill-table row | Mutation | Expected red |
|---|---|---|
| 3 | delete the `if not page.items:` raise | 1 failed: `..._raises_on_empty_page_that_is_not_drained` |
| 4 | delete the `if page.next_seq <= since:` raise | 1 failed: `..._raises_when_cursor_does_not_advance` |
| 5 | delete the whole `if pages >= self._MAX_LOG_PAGES:` block | 2 failed: `..._raises_at_the_page_cap` and `..._page_cap_message_...`, BOTH with `ServerError` and NOT a hang. **If either hangs, the handler's past-the-cap 500 is missing or wrong - fix the test, do not skip the row.** |
| 2 | move the `if not page.items:` raise ABOVE the `if page.next_seq == 0:` arm | 1 failed: `..._exact_page_multiple_is_not_a_protocol_error` |
| 7 | `since = page.next_seq` -> `since = page.next_seq + 1` | 2 failed: `..._pages_to_the_end_...` (missing row 201 and the `calls[1]["since_seq"] == "200"` assertion) and `..._exact_page_multiple_...` (the `calls[1]` assertion) |
| 8 | move `return out[:limit]` below the loop | 1 failed: `..._limit_caps_total_records`, on `len(calls) == 2` |
| 9 | drop `limit=self._PAGE_REQUEST_LIMIT` from the `task_logs_page` call | 1 failed: `..._sends_explicit_page_limit` |

**If any row reports zero failures, the mutation did not apply or the test is vacuous. Do not proceed; say which row and stop.** A uniform result across rows means the harness is broken, not that coverage is good.

- [ ] **Step 6: Commit**

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "feat(python): three paging stops on task_logs, each separately pinned"
```

---

## Task 7: The sweep's model findings (D2, D3, D4)

**Files:**
- Modify: `python/src/relay/models.py` (`EventType` line 50, `Job` line 232, `Worker` line 405)
- Test: `python/tests/unit/test_models.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_models.py`, and add `EventType` to its `from relay import (...)` block (between `AgentEnrollment` and `Job`):

```python
# ─── sweep findings D2 / D3 / D4 ──────────────────────────────────────────────


def test_worker_parses_revoked_at() -> None:
    """D2. workerResponse has emitted revoked_at since worker revocation
    shipped (internal/api/workers.go, toWorkerResponse); Worker did not model
    it, so extra="ignore" dropped it silently and a Python caller could not see
    that a worker had been revoked.
    """
    w = Worker.model_validate(
        {
            "id": "w1",
            "name": "worker-a",
            "hostname": "host-a",
            "cpu_cores": 8,
            "ram_gb": 32,
            "gpu_count": 1,
            "gpu_model": "RTX",
            "os": "linux",
            "max_slots": 4,
            "labels": {},
            "status": "offline",
            "revoked_at": "2026-08-25T09:00:00Z",
        }
    )
    assert w.revoked_at is not None
    assert w.revoked_at.year == 2026


def test_job_parses_list_enrichment_fields() -> None:
    """D3. jobResponse carries six list-only enrichment keys on GET /v1/jobs
    rows. Job modeled none of them, so list_jobs() silently discarded the
    progress and timing the server had already computed.
    """
    job = Job.model_validate(
        {
            "id": "j1",
            "name": "nightly",
            "priority": "normal",
            "status": "running",
            "labels": {},
            "created_at": "2026-08-25T09:00:00Z",
            "updated_at": "2026-08-25T09:05:00Z",
            "total_tasks": 7,
            "done_tasks": 3,
            "started_at": "2026-08-25T09:01:00Z",
            "finished_at": None,
            "scheduled_job_id": "s1",
            "scheduled_job_name": "nightly-cook",
        }
    )
    assert job.total_tasks == 7
    assert job.done_tasks == 3
    assert job.started_at is not None
    assert job.finished_at is None
    assert job.scheduled_job_id == "s1"
    assert job.scheduled_job_name == "nightly-cook"


def test_job_authoring_does_not_require_enrichment_fields() -> None:
    """The six D3 fields are DEFAULTED, and that is deliberate rather than a
    lapse from the strict no-default rule LogPage follows.

    Job is the AUTHORING model as well as the response model - Job(name=...) is
    the README's first example - so a required total_tasks would break every
    authoring call site. LogPage is response-only and has no authoring caller,
    which is why the strict rule costs nothing there and everything here. Do
    not "make these consistent".
    """
    job = Job(name="nightly")
    assert job.total_tasks == 0
    assert job.done_tasks == 0
    assert job.started_at is None
    assert job.scheduled_job_id is None


def test_event_type_covers_every_type_the_server_publishes() -> None:
    """D4. EventType had JOB and TASK only - an incomplete slicing of a
    five-value vocabulary. The five publish sites, by symbol: "job"
    (internal/api/jobs.go, internal/scheduler/dispatch.go), "task"
    (dispatch.go, internal/worker/handler.go), "worker"
    (internal/metrics/sweep.go, worker/handler.go), events.TypeTaskLog =
    "task_log" (internal/events/broker.go), and "dropped", written as a raw
    frame by handleEvents when the broker drops a slow subscriber.

    Set EQUALITY, not a containment check: this fails both on a member the
    server publishes and the SDK lacks, and on a member the SDK invents.
    """
    assert {e.value for e in EventType} == {
        "job",
        "task",
        "worker",
        "task_log",
        "dropped",
    }
```

- [ ] **Step 2: Run them to verify they fail**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_models.py -q -k "revoked_at or enrichment or event_type"
```

Expected: FAIL, 4 tests. The two `Job` ones fail with `AttributeError: 'Job' object has no attribute 'total_tasks'`; `..._revoked_at` with `AttributeError: 'Worker' object has no attribute 'revoked_at'`; `..._event_type_...` with an `AssertionError` showing `{'job', 'task'}` against the five-value set.

- [ ] **Step 3: Implement**

In `python/src/relay/models.py`, replace `EventType` (lines 50-52):

```python
class EventType(str, Enum):
    """The event types the server publishes on GET /v1/events.

    ``Event.type`` is a plain ``str`` so an unknown future type still parses;
    this enum is the vocabulary the server emits TODAY, for comparison and
    autocomplete.

    ``DROPPED`` is not a resource event. The server writes it directly
    (handleEvents, internal/api/events.go) when the broker drops a subscriber
    for falling behind, and its meaning is "you missed frames": anything
    published in the gap is gone, so re-read the job or re-fetch the task's
    logs from the last seq you saw.
    """

    JOB = "job"
    TASK = "task"
    WORKER = "worker"
    TASK_LOG = "task_log"
    DROPPED = "dropped"
```

In `Job`, after `updated_at` (line 257):

```python
    # List-only enrichment (GET /v1/jobs rows). The server computes these from
    # the job's tasks and its scheduled-job source, and does not populate them
    # on single-job routes. They are DEFAULTED because Job is the authoring
    # model too - Job(name="nightly") must keep working - which is why the
    # strict no-default rule LogPage follows does not apply here.
    total_tasks: int = 0
    done_tasks: int = 0
    started_at: Optional[datetime] = None
    finished_at: Optional[datetime] = None
    scheduled_job_id: Optional[str] = None
    scheduled_job_name: Optional[str] = None
```

In `Worker`, after `disabled_at` (line 421):

```python
    revoked_at: Optional[datetime] = None
```

- [ ] **Step 4: Run to verify they pass**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit -q
```

Expected: PASS, whole unit suite.

- [ ] **Step 5: Commit**

```bash
git add python/src/relay/models.py python/tests/unit/test_models.py
git commit -m "feat(python): model revoked_at, job list enrichment, and the full event vocabulary"
```

---

## Task 8: Pin the bare array before it becomes the next instance of this bug

`get_tasks()` is CORRECT today - `handleListTasks` does `resp := make([]taskResponse, len(tasks))` then `writeJSON(w, 200, resp)`, a bare array with no envelope. This task adds no production code. It is the four lines that would have caught the present bug, on the SDK's only remaining bare-array read.

**This test cannot be RED first, and pretending otherwise would be dishonest.** Its evidence is a mutation, and the mutation is reverted while the test stays.

**Files:**
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the test**

Append to the `# --- tasks / logs ---` section:

```python
def test_get_tasks_parses_a_bare_array() -> None:
    """GET /v1/jobs/{id}/tasks is the SDK's ONLY bare-array read - it is the
    last unpaginated list route the SDK calls, and handleListTasks writes a
    make()d slice so an empty task list serializes as [] and never null.

    It is not stable by construction. Its six paginated siblings all grew
    page[T]; if this route follows them, get_tasks() breaks in exactly the way
    task_logs() was broken, and today no test would notice. These four lines
    make that a red suite instead of a red production.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json=[
                {
                    "id": "t1",
                    "name": "cook",
                    "status": "done",
                    "commands": [["echo", "hi"]],
                    "env": {},
                    "requires": {},
                    "timeout_seconds": None,
                    "retries": 0,
                    "retry_count": 0,
                    "worker_id": None,
                }
            ],
        )

    client = _make_client(handler)
    tasks = client.get_tasks("job-1")
    assert [(t.id, t.name, t.status) for t in tasks] == [("t1", "cook", "done")]
```

- [ ] **Step 2: Run it - expect PASS, then prove it is load-bearing**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_client.py::test_get_tasks_parses_a_bare_array -q
```

Expected: PASS.

Now mutate `get_tasks` in `python/src/relay/client.py` (line 254) to the shape this test exists to catch:

```python
        return [Task.model_validate(item) for item in response.json()["items"]]
```

Re-run. Expected: FAIL with `TypeError: list indices must be integers or slices, not str`. **Record that output.** Then revert the mutation (`git checkout -- python/src/relay/client.py`) and re-run to confirm PASS. The discriminating input survives into the permanent test; the mutation does not.

- [ ] **Step 3: Commit**

```bash
git add python/tests/unit/test_client.py
git commit -m "test(python): pin get_tasks on the bare array, the SDK's last one"
```

---

## Task 9: The prose defects (D5, D6, D11, D12) and their guards

A wrong contract in docs is a defect: consumers implement against the prose and no test covers it. Two of these get a real test.

**Files:**
- Modify: `python/src/relay/client.py` (class docstring lines 44-51, `cancel_job` line 241, `follow_job` lines 270-276)
- Modify: `python/README.md`
- Create: `python/tests/unit/test_packaging.py`

- [ ] **Step 1: Write the failing guard for D6**

Create `python/tests/unit/test_packaging.py`:

```python
from __future__ import annotations

import re
from pathlib import Path

from relay import Client, __version__

_PYTHON_DIR = Path(__file__).resolve().parents[2]


def test_readme_client_api_table_documents_every_public_method() -> None:
    """D6: the table listed 15 of 25 methods. The pagination work added ten
    siblings and did not update it, and nothing noticed for three months.

    The search is scoped to the "## Client API" section on purpose. Searching
    the whole README would match the quickstart's `client.submit(job)` and the
    authoring section's `add_task(...)`, and the guard would then pass against
    an EMPTY table - a guard that cannot see its own subject.
    """
    readme = (_PYTHON_DIR / "README.md").read_text(encoding="utf-8")
    section = readme.split("## Client API", 1)[1].split("\n## ", 1)[0]
    documented = set(re.findall(r"(\w+)\(", section))
    public = {
        name
        for name in dir(Client)
        if not name.startswith("_") and callable(getattr(Client, name))
    }
    assert sorted(public - documented) == []


def test_version_files_are_in_lockstep() -> None:
    """pyproject.toml and _version.py are two hand-maintained copies of one
    number. This is what makes bumping one of them RED.
    """
    pyproject = (_PYTHON_DIR / "pyproject.toml").read_text(encoding="utf-8")
    match = re.search(r'^version = "([^"]+)"', pyproject, re.MULTILINE)
    assert match is not None, "pyproject.toml has no [project] version"
    assert match.group(1) == __version__
```

- [ ] **Step 2: Run it to verify the D6 half fails**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_packaging.py -q
```

Expected: `test_readme_client_api_table_documents_every_public_method` FAILS, listing **twelve** undocumented names: `close`, `list_agent_enrollments`, `list_agent_enrollments_page`, `list_jobs_page`, `list_reservations`, `list_reservations_page`, `list_schedules_page`, `list_users`, `list_users_page`, `list_workers`, `list_workers_page`, `task_logs_page`.

`test_version_files_are_in_lockstep` PASSES (both files read `0.1.2` today). It is a guard for Task 10, not a RED here.

- [ ] **Step 3: Replace the README's Client API table**

In `python/README.md`, replace the whole table under `## Client API` (lines 71-84) with:

```markdown
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
```

- [ ] **Step 4: Add the D5 section and fix the error table**

Insert a new `## Following a job` section immediately after the `### Reading a task's log` block, before `## Errors`:

```markdown
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
```

Then in `## Errors`, replace the lead sentence and add the new row:

```markdown
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
```

- [ ] **Step 5: Fix the three docstrings**

In `python/src/relay/client.py`, append to the `Client` class docstring, after the `relay login` sentence at line 50 (D12):

```
    ``timeout`` applies only to the client the SDK builds for itself. When you
    pass ``http_client=``, that client's own timeout policy is used unchanged
    and ``timeout`` is IGNORED - the caller who injects a client owns its
    policy. httpx's default for a bare ``httpx.Client()`` is 5 s; a client
    built with ``timeout=None`` has no bound at all and the SDK will not add
    one.
```

Give `cancel_job` a docstring (D11):

```python
    def cancel_job(self, job_id: str, *, force: bool = False) -> Job:
        """Cancel a job. Graceful by default; ``force=True`` asks the agent to
        kill the running task immediately.

        The returned :class:`Job` carries NO task list. ``handleCancelJob``
        serializes with ``tasks`` omitted (the field is ``omitempty``), so
        ``job.tasks`` is ``[]`` here for EVERY job - including jobs that have
        tasks, which is all of them, since the server rejects a spec with none.
        Do not read ``.tasks`` off this value; call :meth:`get_tasks` instead.
        """
```

Replace `follow_job`'s docstring (D5):

```python
    def follow_job(self, job_id: str) -> Iterator[Event]:
        """Stream events for a single job over SSE.

        **The server does not end the stream when the job finishes.**
        ``handleEvents`` closes on exactly two conditions - the request context
        is done, or the broker drops this subscriber for falling behind - and
        it has no notion of job terminality. This iterator sets no read
        timeout, so a caller that iterates to exhaustion blocks forever after
        the job is done. Break out on a terminal ``job`` frame yourself, or use
        :meth:`wait`, which polls and is immune.

        A ``dropped`` frame (:attr:`relay.EventType.DROPPED`) means the broker
        dropped this subscriber for falling behind: frames published in the gap
        are gone, and the recovery is to re-read the job and re-fetch the
        task's logs from the last seq seen.

        The underlying HTTP connection is closed on generator exit.
        """
```

- [ ] **Step 6: Run to verify everything passes**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit -q
```

Expected: PASS. If the README guard still fails, its assertion prints exactly which method name is missing from the table - add that row.

- [ ] **Step 7: Commit**

```bash
git add python/src/relay/client.py python/README.md python/tests/unit/test_packaging.py
git commit -m "docs(python): correct the follow_job contract, cancel_job tasks, and the method table"
```

---

## Task 10: Version bump

**Files:**
- Modify: `python/pyproject.toml` (line 7), `python/src/relay/_version.py` (line 1)

- [ ] **Step 1: Bump both files**

`python/src/relay/_version.py`:

```python
__version__ = "0.1.3"
```

`python/pyproject.toml`, line 7:

```toml
version = "0.1.3"
```

- [ ] **Step 2: Verify the lockstep guard is not a tautology**

```bash
./.venv/Scripts/python.exe -m pytest tests/unit/test_packaging.py -q
```

Expected: PASS, 2 tests. Now bump ONLY `_version.py` to `0.1.4`, re-run, and confirm `test_version_files_are_in_lockstep` FAILS with `AssertionError: assert '0.1.3' == '0.1.4'`. Revert to `0.1.3` and re-run. That is the guard's proof; without it the test is a tautology over two files that happen to agree.

- [ ] **Step 3: Commit**

```bash
git add python/pyproject.toml python/src/relay/_version.py
git commit -m "chore(python): 0.1.2 -> 0.1.3"
```

---

## Task 11: The RED proof procedure and final verification

**This task produces the evidence the acceptance criterion asks for. Every command's OUTPUT must be pasted into the report - not summarized, not asserted.**

- [ ] **Step 1: The revert-the-fix RED proof**

The spec's own criterion is: revert the client fix while KEEPING every new test, and the named tests go RED.

Replace ONLY the body of `task_logs` in `python/src/relay/client.py` with the HEAD comprehension. **Leave `_MAX_LOG_PAGES`, `task_logs_page` and the imports in place** - the cap tests monkeypatch the class attribute and would error at setup if it were gone, which is a different red and not the one being measured.

```python
    def task_logs(self, task_id: str) -> list[LogRecord]:
        self._require_token()
        response = self._http.get(f"/v1/tasks/{task_id}/logs")
        raise_for_response(response)
        return [LogRecord.model_validate(item) for item in response.json()]
```

Run the whole suite from `python/`:

```bash
./.venv/Scripts/python.exe -m pytest tests/unit -q
```

**Expected RED set - nine tests. Check the report against this list explicitly:**

| Test | Expected failure |
|---|---|
| `test_task_logs_parses_records` | `pydantic_core.ValidationError`, `type=model_type, input_value='items'` |
| `test_task_logs_pages_to_the_end_with_verbatim_cursor` | same ValidationError |
| `test_task_logs_sends_explicit_page_limit` | same ValidationError |
| `test_task_logs_exact_page_multiple_is_not_a_protocol_error` | same ValidationError |
| `test_task_logs_raises_on_empty_page_that_is_not_drained` | same ValidationError (it propagates out of `pytest.raises(ProtocolError)`, which does not catch it) |
| `test_task_logs_raises_when_cursor_does_not_advance` | same ValidationError |
| `test_task_logs_raises_at_the_page_cap` | same ValidationError |
| `test_task_logs_page_cap_message_does_not_blame_the_server_when_total_is_reached` | same ValidationError |
| `test_task_logs_limit_caps_total_records` | `TypeError: task_logs() got an unexpected keyword argument 'limit'` - it fails EARLIER, before the decode |

The spec named six tests (T1, T2, T4, T9, T12, T13) as the ones that must go red. That list is a floor, not a ceiling. **Any NEW test that stays GREEN under this revert must be named in the report with a justification.** The ones that legitimately stay green are: every `test_models.py` addition (Tasks 2 and 7), `test_protocol_error_is_a_relay_error`, `test_get_tasks_parses_a_bare_array`, both `test_packaging.py` tests, and the two `task_logs_page` tests - none of them has `task_logs` as its subject. Everything else must be red.

Then restore:

```bash
git checkout -- python/src/relay/client.py
```

- [ ] **Step 2: The green suite, against the recorded baseline**

```bash
cd python && ./.venv/Scripts/python.exe -m pytest tests/unit -q
```

Baseline at HEAD was **93 passed**. Expected now: **114 passed**, from `93 + 21`:

- `test_client.py` **+11**: `..._pages_to_the_end_with_verbatim_cursor`, `..._sends_explicit_page_limit`, `..._exact_page_multiple_is_not_a_protocol_error`, `..._limit_caps_total_records`, `..._raises_on_empty_page_that_is_not_drained`, `..._raises_when_cursor_does_not_advance`, `..._raises_at_the_page_cap`, `..._page_cap_message_does_not_blame_the_server_when_total_is_reached`, `test_task_logs_page_returns_one_envelope_and_sends_its_params`, `test_task_logs_page_raises_on_a_bare_array_body`, `test_get_tasks_parses_a_bare_array`. (`test_task_logs_parses_records` is REPLACED, not added.)
- `test_models.py` **+7**: six functions, one of which is parametrized over two missing keys.
- `test_errors.py` **+1**.
- `test_packaging.py` **+2**.

**If your number is not 114, reconcile it before claiming done.** Report the arithmetic, not just the total.

- [ ] **Step 3: The lint gate CI runs**

```bash
cd python && ./.venv/Scripts/python.exe -m ruff check src tests
```

Expected: `All checks passed!`. Note `tests/**` ignores only `B` and `SIM`; `E`, `F`, `I` (import sorting) and `RUF` still apply to test files.

- [ ] **Step 4: The type gate CI runs**

```bash
cd python && ./.venv/Scripts/python.exe -m mypy src
```

Expected: `Success: no issues found in 7 source files`. Config is `strict = true` with `python_version = "3.9"`.

- [ ] **Step 5: State the 3.9 compatibility check explicitly**

The local venv is 3.13; **CI's floor is 3.9 and that cell cannot be run locally.** Confirm by reading, and say so in the report rather than implying coverage:

- No `X | Y` union syntax anywhere in the diff (3.10+).
- `list[LogRecord]` and `dict[str, str]` appear only in annotations, and every module in `src/relay/` opens with `from __future__ import annotations` - verify `models.py` and `client.py` both still do.
- No 3.10+ stdlib is imported.

- [ ] **Step 6: State what is NOT covered**

Put this in the PR body verbatim, because implying coverage here is the mechanism that let this bug live for three and a half months:

> None of the above puts the SDK in front of the real handler.
> `.github/workflows/python.yml` runs `pytest tests/unit` on 15 matrix cells and
> the integration suite on none, so
> `python/tests/integration/test_smoke.py::test_submit_and_wait` - the test that
> actually calls `task_logs()` against a live server, and this fix's real
> acceptance - has never executed in CI and did not execute here. This is the
> fourth confirmed instance of `idea-2026-08-23-cli-tests-never-hit-real-server`
> and the first in Python.

If a `relay-server` and an agent happen to be available, run the lane by hand and report the result:

```bash
cd python && RELAY_INTEGRATION=1 ./.venv/Scripts/python.exe -m pytest tests/integration -q
```

- [ ] **Step 7: Confirm the working tree is exactly the expected file set**

```bash
git status --porcelain
```

Expected, and nothing else:

```
 M python/README.md
 M python/pyproject.toml
 M python/src/relay/__init__.py
 M python/src/relay/_version.py
 M python/src/relay/client.py
 M python/src/relay/errors.py
 M python/src/relay/models.py
 M python/tests/unit/test_client.py
 M python/tests/unit/test_errors.py
 M python/tests/unit/test_models.py
?? python/tests/unit/test_packaging.py
```

`python/.venv/` is gitignored (`python/.gitignore`) and must not appear. **No file under `internal/`, `web/`, or `cmd/` may appear.** If one does, stop and report it.

---

## Verification gates (for the conductor's Phase 4)

- **Go lanes do not apply.** Zero Go in the diff, so `make test`, `make test-race` and `make test-integration` are unaffected and running them proves nothing about this change. The integration-tester's "skip on a zero-Go diff" carve-out applies. What the integration agent should do INSTEAD is exercise the Python integration lane by hand if a server is available, and otherwise state plainly that it did not run - see Task 11 Step 6.
- **The gates that DO apply** are the three CI runs: `pytest tests/unit`, `ruff check src tests`, `mypy src`. All three must be pasted.
- **The mutation walk in Task 6 Step 5 is a gate, not a nicety.** Three stops that all raise the same exception class are trivially confusable; the walk is what proves they are separately pinned. Confirm the report contains one line per kill-table row.

---

## Not in this plan, and why

Six of the spec's findings are **declined for this slice** and are to be FILED as backlog items by the conductor. Filing is not the engineer's job.

**D9 (HIGH) - `follow_job()` subscribes to nothing forever on a non-canonical job id.** This is the highest-severity finding in the sweep and it is deliberately OUT. Two reasons. First, the backlog item's acceptance criterion is a sweep that NAMES its findings with a count, not one that repairs all of them - repairing D9 here would be a second feature riding on the envelope diff. Second, and more important, **D9's remedy has an acceptance surface of its own that deserves its own RED.** A Python canonicaliser would be built on `uuid.UUID()`, which accepts spellings the server's `pgtype.UUID.Scan` may not - `urn:uuid:` prefixes and brace-wrapped forms among them. So a naive SDK-side canonicaliser could make the SDK accept MORE than the server does, turning a hang into a wrong-endpoint request; and note that `canonicalJobID` (`internal/cli/logs.go`) deliberately shares the PARSE half with the server precisely so that what counts as a uuid cannot drift, which a Python implementation structurally cannot do. That wrinkle is a design question, not a rider. **The filer must inherit it:** the item's acceptance criterion has to cover BOTH directions - an uppercase or dashless UUID reaches the same subscription as its canonical form, AND a spelling `pgtype.UUID.Scan` rejects is not silently accepted by the SDK.

**D7 - `_get_page` / `_fetch_all` read the cursor with `body.get("next_cursor", "")`.** A renamed or dropped key reads as "drained" and `_fetch_all` returns page 1 silently: this slice's defect shape, one layer over. Not a drift today - the key is correct. Declined because making it strict changes the failure behaviour of twelve methods across six endpoints and needs its own RED.

**D8 - decode failures escape as non-`RelayError` exceptions.** Declined as a CODE fix: it is a cross-cutting wrap at every `model_validate` and every envelope key read, and folding it in makes this diff two features. **The one-sentence README correction IS in scope** (Task 9) - correcting a false advertisement is not the same work as changing the behaviour it describes.

**D10 - nine f-string id interpolations into request paths, unescaped.** Belongs as an APPEND to the existing `bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped`, as a third language, not a new item. The append must say the next session MEASURES httpx's URL handling before choosing a remedy - a text search cannot establish a behavioural property, and no shell measured it in the spec session.

**D13 - no response-body bound.** `response.json()` buffers the whole body; the paging loop bounds the number of REQUESTS but not the size of any one response. Applies to all 25 methods, not to `task_logs`. Append to `bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout`. The right shape is one `_read_json(response)` chokepoint - the Python analogue of CLAUDE.md's single-JSON-entry-point invariant.

**D14 - no `follow_job` reconcile for a cancelled job.** Nothing in the SDK to fix: `wait()` polls and is immune, and the SDK ships no watch loop. The docstring correction under D5 is what tells a caller building their own loop that the stream will not end.

Also for the conductor, from the spec's proposals section: the four missing read routes (`GET /v1/workers/{id}`, `/v1/workers/revoked`, `/v1/jobs/stats`, `/v1/workers/stats`), and the server-side `CountTaskLogs`-per-page cost that this auto-pager becomes the third payer of. And `idea-2026-08-23-cli-tests-never-hit-real-server` gets an append (fourth instance, first in Python) - **not a priority bump**; that item's own 2026-08-26 append argues raising it for point fixes rewards the wrong signal.

**One stage, one PR.** This plan does not divide into units meant to span more than one session, so there is nothing for `/backlog phases` to file. The declined findings above are ordinary item filings.

## Closing the backlog item

`docs/backlog/bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys.md` must be closed with `/backlog close bug-2026-08-25-python-sdk-task-logs`, which `git mv`s it into `docs/backlog/closed/`, stamps the frontmatter, appends a `## Resolution` note and commits. **This is the CONDUCTOR's step, not the engineer's** - `/backlog close` is a slash command and subagents cannot invoke it. Do not hand-edit the item's `status` field; that leaves the file in the open directory and `/backlog list` reports it as malformed.
