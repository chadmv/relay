# Python SDK Pagination Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every paginated relay REST endpoint reachable from the Python SDK, parsing the `{items, next_cursor, total}` envelope, with auto-pagination, single-page cursor access, `sort` support, and typed models for the four unmodeled resources.

**Architecture:** Two private helpers on `Client` mirror the Go reference (`internal/relayclient`): `_get_page` fetches one envelope into a `Page[T]`; `_fetch_all` walks `?cursor=` until exhausted (or a total `limit`). Each resource exposes `list_*` (auto-paginate → `list[T]`) and `list_*_page` (→ `Page[T]`). A generic `Page[T]` pydantic model plus four new resource models (`Worker`, `Reservation`, `AgentEnrollment`, `User`) cover the wire shapes.

**Tech Stack:** Python 3.9+, pydantic v2, httpx (sync), pytest with `httpx.MockTransport`.

**Spec:** [docs/superpowers/specs/2026-06-03-python-sdk-pagination-design.md](../specs/2026-06-03-python-sdk-pagination-design.md)

---

## Prerequisites

The Python targets assume a venv at `python/.venv`. If it does not exist, bootstrap once:

```bash
cd python && python -m venv .venv && .venv/Scripts/python -m pip install -e ".[dev]"
```

(On macOS/Linux the interpreter is `python/.venv/bin/python`.) Commands below use `python/.venv/Scripts/python.exe` for single-test runs and the `make python-test` / `make python-lint` targets for full runs.

**Every commit message ends with the trailer:** `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` (shown via a second `-m` in each commit step).

## File Structure

- `python/src/relay/models.py` — add `Page[T]` generic envelope and `Worker`/`Reservation`/`AgentEnrollment`/`User` models.
- `python/src/relay/client.py` — add `_PAGE_REQUEST_LIMIT`, `_get_page`, `_fetch_all`, `_job_filters`; rewrite `list_jobs`/`list_schedules`; add ten new list methods.
- `python/src/relay/__init__.py` — export the five new symbols.
- `python/tests/unit/test_models.py` — model + `Page[T]` validation tests.
- `python/tests/unit/test_client.py` — pagination behavior tests; fix two tests that mock bare arrays.
- `python/pyproject.toml`, `python/src/relay/_version.py` — version bump to `0.1.2`.
- `docs/backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md` — `git mv` to `closed/`.

---

## Task 1: New models (`Page[T]`, `Worker`, `Reservation`, `AgentEnrollment`, `User`)

**Files:**
- Modify: `python/src/relay/models.py`
- Modify: `python/src/relay/__init__.py`
- Test: `python/tests/unit/test_models.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_models.py`:

```python
from relay import AgentEnrollment, Page, Reservation, User, Worker
from relay.models import Job


def test_page_validates_items_as_job_model() -> None:
    page = Page[Job].model_validate(
        {
            "items": [{"name": "j", "id": "j1", "status": "pending"}],
            "next_cursor": "c1",
            "total": 5,
        }
    )
    assert isinstance(page.items[0], Job)
    assert page.items[0].id == "j1"
    assert page.next_cursor == "c1"
    assert page.total == 5


def test_page_defaults_empty_cursor_and_zero_total() -> None:
    page = Page[Job].model_validate({"items": []})
    assert page.items == []
    assert page.next_cursor == ""
    assert page.total == 0


def test_worker_parses_and_ignores_unknown_field() -> None:
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
            "labels": {"zone": "us"},
            "status": "online",
            "last_seen_at": "2026-06-03T12:00:00Z",
            "future_field": "ignored",
        }
    )
    assert w.name == "worker-a"
    assert w.cpu_cores == 8
    assert w.labels == {"zone": "us"}
    assert w.disabled_at is None


def test_reservation_parses_worker_ids_and_optional_times() -> None:
    r = Reservation.model_validate(
        {
            "id": "r1",
            "name": "res-a",
            "selector": {"gpu": "true"},
            "worker_ids": ["w1", "w2"],
            "user_id": "u1",
            "project": "proj",
            "ends_at": "2026-06-04T00:00:00Z",
            "created_at": "2026-06-03T12:00:00Z",
        }
    )
    assert r.worker_ids == ["w1", "w2"]
    assert r.starts_at is None
    assert r.ends_at is not None


def test_user_and_agent_enrollment_parse() -> None:
    u = User.model_validate(
        {
            "id": "u1",
            "email": "a@example.com",
            "name": "Alice",
            "is_admin": True,
            "created_at": "2026-06-03T12:00:00Z",
            "archived_at": None,
        }
    )
    assert u.is_admin is True
    assert u.archived_at is None

    e = AgentEnrollment.model_validate(
        {
            "id": "e1",
            "created_at": "2026-06-03T12:00:00Z",
            "expires_at": "2026-06-04T12:00:00Z",
            "created_by": "u1",
            "hostname_hint": "host-x",
        }
    )
    assert e.created_by == "u1"
    assert e.hostname_hint == "host-x"
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_models.py -v`
Expected: FAIL — `ImportError: cannot import name 'Page' from 'relay'` (and the other new names).

- [ ] **Step 3: Add the models to `models.py`**

In `python/src/relay/models.py`, change the typing import line:

```python
from typing import Any, Generic, Optional, TypeVar, Union
```

Append at the end of the file:

```python
# ─── Pagination & resource models ──────────────────────────────────────────────

T = TypeVar("T")


class Page(BaseModel, Generic[T]):
    """One page of a paginated list response.

    ``next_cursor`` is the empty string on the last page; pass it back as
    ``cursor=`` to fetch the next page. ``total`` is the server's count of
    all matching rows, not just this page.
    """

    model_config = ConfigDict(extra="ignore")

    items: list[T]
    next_cursor: str = ""
    total: int = 0


class Worker(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    name: str
    hostname: str
    cpu_cores: int
    ram_gb: int
    gpu_count: int
    gpu_model: str
    os: str
    max_slots: int
    labels: dict[str, Any] = Field(default_factory=dict)
    status: str
    last_seen_at: Optional[datetime] = None
    last_sample_at: Optional[datetime] = None
    disabled_at: Optional[datetime] = None


class Reservation(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    name: str
    selector: dict[str, Any] = Field(default_factory=dict)
    worker_ids: list[str] = Field(default_factory=list)
    user_id: str
    project: Optional[str] = None
    starts_at: Optional[datetime] = None
    ends_at: Optional[datetime] = None
    created_at: datetime


class AgentEnrollment(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    created_at: datetime
    expires_at: datetime
    created_by: str
    hostname_hint: Optional[str] = None


class User(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    email: str
    name: str
    is_admin: bool
    created_at: datetime
    archived_at: Optional[datetime] = None
```

- [ ] **Step 4: Export the new symbols from `__init__.py`**

In `python/src/relay/__init__.py`, update the `from .models import (...)` block and `__all__` to include the new names (keep alphabetical ordering):

```python
from .models import (
    AgentEnrollment,
    Event,
    EventType,
    Job,
    JobStatus,
    LogRecord,
    OverlapPolicy,
    Page,
    Priority,
    Reservation,
    ScheduledJob,
    Source,
    Sync,
    Task,
    TaskStatus,
    User,
    Worker,
)
```

Add `"AgentEnrollment"`, `"Page"`, `"Reservation"`, `"User"`, `"Worker"` to `__all__` (alphabetical).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_models.py -v`
Expected: PASS (all model tests green).

- [ ] **Step 6: Commit**

```bash
git add python/src/relay/models.py python/src/relay/__init__.py python/tests/unit/test_models.py
git commit -m "python: add Page envelope and Worker/Reservation/AgentEnrollment/User models" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Pagination helpers + fix `list_jobs` (core regression)

**Files:**
- Modify: `python/src/relay/client.py`
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Add the test envelope helper and fix the two bare-array tests**

In `python/tests/unit/test_client.py`, add this helper near `_job_response` (after it):

```python
def _page_response(items: list[Any], *, next_cursor: str = "", total: Optional[int] = None) -> dict[str, Any]:
    return {
        "items": items,
        "next_cursor": next_cursor,
        "total": len(items) if total is None else total,
    }
```

Change `test_authorization_header_sent`: replace `return httpx.Response(200, json=[])` with `return httpx.Response(200, json=_page_response([]))`.

Change `test_list_jobs_passes_status_filter`: replace `return httpx.Response(200, json=[])` with `return httpx.Response(200, json=_page_response([]))`, and change the assertion (auto-pagination now also sends `limit=200`) to:

```python
    client.list_jobs(status=JobStatus.RUNNING)
    assert captured["query"]["status"] == "running"
```

- [ ] **Step 2: Write the failing pagination tests**

Append to `python/tests/unit/test_client.py`:

```python
# ─── pagination ──────────────────────────────────────────────────────────────


def test_list_jobs_parses_envelope_items() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_page_response([_job_response(id="j1")], total=1))

    client = _make_client(handler)
    jobs = client.list_jobs()
    assert [j.id for j in jobs] == ["j1"]


def test_list_jobs_walks_all_pages() -> None:
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if "cursor" not in request.url.params:
            return httpx.Response(200, json=_page_response([_job_response(id="j1")], next_cursor="c1", total=2))
        return httpx.Response(200, json=_page_response([_job_response(id="j2")], total=2))

    client = _make_client(handler)
    jobs = client.list_jobs()
    assert [j.id for j in jobs] == ["j1", "j2"]
    assert "cursor" not in calls[0]
    assert calls[0]["limit"] == "200"
    assert calls[1]["cursor"] == "c1"


def test_list_jobs_limit_caps_total() -> None:
    page1 = [_job_response(id=f"a{i}") for i in range(200)]
    page2 = [_job_response(id=f"b{i}") for i in range(200)]

    def handler(request: httpx.Request) -> httpx.Response:
        if "cursor" not in request.url.params:
            return httpx.Response(200, json=_page_response(page1, next_cursor="c1", total=400))
        return httpx.Response(200, json=_page_response(page2, total=400))

    client = _make_client(handler)
    jobs = client.list_jobs(limit=250)
    assert len(jobs) == 250


def test_list_jobs_page_returns_envelope() -> None:
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["query"] = dict(request.url.params)
        return httpx.Response(200, json=_page_response([_job_response(id="j1")], next_cursor="nextc", total=7))

    client = _make_client(handler)
    page = client.list_jobs_page(limit=50, cursor="start")
    assert [j.id for j in page.items] == ["j1"]
    assert page.next_cursor == "nextc"
    assert page.total == 7
    assert captured["query"]["limit"] == "50"
    assert captured["query"]["cursor"] == "start"


def test_list_jobs_sort_passed_through() -> None:
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["query"] = dict(request.url.params)
        return httpx.Response(200, json=_page_response([]))

    client = _make_client(handler)
    client.list_jobs(sort="-name")
    assert captured["query"]["sort"] == "-name"


def test_list_jobs_bad_sort_raises_validation_error() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            400,
            json={"error": "unsupported sort key 'bogus' for /v1/jobs; supported: created_at, name"},
        )

    client = _make_client(handler)
    with pytest.raises(ValidationError, match="unsupported sort key"):
        client.list_jobs(sort="bogus")
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -v`
Expected: FAIL — the new tests error (e.g. `TypeError` iterating dict keys / `AttributeError` no `list_jobs_page`), and the two edited tests fail until the implementation reads `items`.

- [ ] **Step 4: Implement helpers and rewrite the jobs methods in `client.py`**

In `python/src/relay/client.py`, update imports. Add `cast` to the typing import:

```python
from typing import Any, Optional, TypeVar, Union, cast
```

Add a pydantic import below the httpx import:

```python
from pydantic import BaseModel
```

Extend the models import to include the new names:

```python
from .models import (
    AgentEnrollment,
    Event,
    Job,
    JobStatus,
    LogRecord,
    OverlapPolicy,
    Page,
    Reservation,
    ScheduledJob,
    Task,
    User,
    Worker,
)
```

Add a module-level TypeVar after the imports (near `_TERMINAL_JOB_STATUSES`):

```python
M = TypeVar("M", bound=BaseModel)
```

Inside `class Client`, add the page-size constant near the top of the class body (e.g. just after the docstring, before `__init__`):

```python
    # Per-request page size used when auto-paginating. Matches the server's
    # max limit and relayclient.PageRequestLimit so we minimize round-trips.
    _PAGE_REQUEST_LIMIT = 200
```

Add the two helpers (place them just before the `# ─── Jobs ───` section):

```python
    # ─── Pagination helpers ───────────────────────────────────────────────

    def _get_page(
        self,
        path: str,
        model: type[M],
        *,
        params: Optional[dict[str, str]] = None,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[M]:
        """Fetch a single page envelope and validate each item into ``model``."""
        self._require_token()
        p: dict[str, str] = dict(params or {})
        if sort is not None:
            p["sort"] = sort
        if limit is not None:
            p["limit"] = str(limit)
        if cursor is not None:
            p["cursor"] = cursor
        response = self._http.get(path, params=p)
        raise_for_response(response)
        body = response.json()
        items = [model.model_validate(item) for item in body["items"]]
        return cast(
            "Page[M]",
            Page(items=items, next_cursor=body.get("next_cursor", ""), total=body.get("total", 0)),
        )

    def _fetch_all(
        self,
        path: str,
        model: type[M],
        *,
        params: Optional[dict[str, str]] = None,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> list[M]:
        """Walk ?cursor= until next_cursor is empty, or ``limit`` rows collected.

        ``limit`` caps the TOTAL rows returned across pages (None = all). Each
        request fetches ``_PAGE_REQUEST_LIMIT`` rows.
        """
        self._require_token()
        p: dict[str, str] = dict(params or {})
        if sort is not None:
            p["sort"] = sort
        p["limit"] = str(self._PAGE_REQUEST_LIMIT)
        out: list[M] = []
        cursor: Optional[str] = None
        while True:
            if cursor:
                p["cursor"] = cursor
            response = self._http.get(path, params=p)
            raise_for_response(response)
            body = response.json()
            out.extend(model.model_validate(item) for item in body["items"])
            if limit is not None and len(out) >= limit:
                return out[:limit]
            cursor = body.get("next_cursor", "")
            if not cursor:
                return out
```

Replace the existing `list_jobs` method with the rewritten pair plus the filter builder:

```python
    def list_jobs(
        self,
        *,
        status: Optional[Union[str, JobStatus]] = None,
        scheduled_job_id: Optional[str] = None,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> list[Job]:
        """List jobs, auto-paginating across all pages.

        ``limit`` caps the TOTAL number of jobs returned (None = all).
        ``sort`` is forwarded to ?sort= and validated server-side; an
        unknown key raises :class:`ValidationError`.
        """
        return self._fetch_all(
            "/v1/jobs", Job,
            params=self._job_filters(status, scheduled_job_id), sort=sort, limit=limit,
        )

    def list_jobs_page(
        self,
        *,
        status: Optional[Union[str, JobStatus]] = None,
        scheduled_job_id: Optional[str] = None,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[Job]:
        """Fetch a single page of jobs.

        ``limit`` is the PAGE SIZE (1-200). Use the returned ``next_cursor``
        as ``cursor=`` to page forward.
        """
        return self._get_page(
            "/v1/jobs", Job,
            params=self._job_filters(status, scheduled_job_id),
            sort=sort, limit=limit, cursor=cursor,
        )

    @staticmethod
    def _job_filters(
        status: Optional[Union[str, JobStatus]],
        scheduled_job_id: Optional[str],
    ) -> dict[str, str]:
        params: dict[str, str] = {}
        if status is not None:
            params["status"] = status.value if isinstance(status, JobStatus) else status
        if scheduled_job_id is not None:
            params["scheduled_job_id"] = scheduled_job_id
        return params
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -v`
Expected: PASS (new pagination tests and the two edited tests green).

- [ ] **Step 6: Commit**

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "python: parse pagination envelope in list_jobs; add _get_page/_fetch_all and list_jobs_page" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Fix `list_schedules` + add `list_schedules_page`

**Files:**
- Modify: `python/src/relay/client.py`
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_client.py`:

```python
def _schedule_response(**overrides: Any) -> dict[str, Any]:
    base: dict[str, Any] = {
        "id": "55555555-5555-5555-5555-555555555555",
        "name": "hourly",
        "owner_id": "22222222-2222-2222-2222-222222222222",
        "cron_expr": "@hourly",
        "timezone": "UTC",
        "job_spec": {"name": "j", "tasks": []},
        "overlap_policy": "skip",
        "enabled": True,
        "next_run_at": "2026-06-03T13:00:00Z",
        "created_at": "2026-06-03T12:00:00Z",
        "updated_at": "2026-06-03T12:00:00Z",
    }
    base.update(overrides)
    return base


def test_list_schedules_parses_envelope_items() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_page_response([_schedule_response(id="s1")], total=1))

    client = _make_client(handler)
    scheds = client.list_schedules()
    assert [s.id for s in scheds] == ["s1"]


def test_list_schedules_page_returns_envelope() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_page_response([_schedule_response(id="s1")], next_cursor="c2", total=3))

    client = _make_client(handler)
    page = client.list_schedules_page(sort="name")
    assert [s.id for s in page.items] == ["s1"]
    assert page.next_cursor == "c2"
    assert page.total == 3
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k schedule -v`
Expected: FAIL — `test_list_schedules_parses_envelope_items` errors iterating dict keys; `list_schedules_page` does not exist.

- [ ] **Step 3: Rewrite `list_schedules` and add `list_schedules_page`**

In `python/src/relay/client.py`, replace the existing `list_schedules` method with:

```python
    def list_schedules(
        self,
        *,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> list[ScheduledJob]:
        """List scheduled jobs, auto-paginating across all pages.

        ``limit`` caps the TOTAL rows returned (None = all). ``sort`` is
        validated server-side; an unknown key raises :class:`ValidationError`.
        """
        return self._fetch_all("/v1/scheduled-jobs", ScheduledJob, sort=sort, limit=limit)

    def list_schedules_page(
        self,
        *,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[ScheduledJob]:
        """Fetch a single page of scheduled jobs. ``limit`` is the page size (1-200)."""
        return self._get_page(
            "/v1/scheduled-jobs", ScheduledJob, sort=sort, limit=limit, cursor=cursor
        )
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k schedule -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "python: parse pagination envelope in list_schedules; add list_schedules_page" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: New list methods (workers, users, reservations, agent enrollments)

**Files:**
- Modify: `python/src/relay/client.py`
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_client.py`:

```python
# ─── new resource list methods ───────────────────────────────────────────────


def test_list_workers_parses_model_and_paginates() -> None:
    calls: list[dict[str, str]] = []
    worker = {
        "id": "w1", "name": "worker-a", "hostname": "host-a", "cpu_cores": 8,
        "ram_gb": 32, "gpu_count": 1, "gpu_model": "RTX", "os": "linux",
        "max_slots": 4, "labels": {"zone": "us"}, "status": "online",
        "last_seen_at": "2026-06-03T12:00:00Z",
    }

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        return httpx.Response(200, json=_page_response([worker], total=1))

    client = _make_client(handler)
    workers = client.list_workers()
    assert workers[0].name == "worker-a"
    assert workers[0].cpu_cores == 8
    assert workers[0].labels == {"zone": "us"}
    assert calls[0]["limit"] == "200"


def test_list_workers_page_returns_envelope() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_page_response([], next_cursor="wc", total=9))

    client = _make_client(handler)
    page = client.list_workers_page(limit=10)
    assert page.items == []
    assert page.next_cursor == "wc"
    assert page.total == 9


def test_list_users_parses_model() -> None:
    user = {
        "id": "u1", "email": "a@example.com", "name": "Alice",
        "is_admin": True, "created_at": "2026-06-03T12:00:00Z", "archived_at": None,
    }

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_page_response([user], total=1))

    client = _make_client(handler)
    users = client.list_users()
    assert users[0].email == "a@example.com"
    assert users[0].is_admin is True


def test_list_reservations_parses_model() -> None:
    reservation = {
        "id": "r1", "name": "res-a", "selector": {"gpu": "true"},
        "worker_ids": ["w1", "w2"], "user_id": "u1", "project": "proj",
        "ends_at": "2026-06-04T00:00:00Z", "created_at": "2026-06-03T12:00:00Z",
    }

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_page_response([reservation], total=1))

    client = _make_client(handler)
    reservations = client.list_reservations()
    assert reservations[0].worker_ids == ["w1", "w2"]
    assert reservations[0].starts_at is None


def test_list_agent_enrollments_parses_model() -> None:
    enrollment = {
        "id": "e1", "created_at": "2026-06-03T12:00:00Z",
        "expires_at": "2026-06-04T12:00:00Z", "created_by": "u1", "hostname_hint": "host-x",
    }

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_page_response([enrollment], total=1))

    client = _make_client(handler)
    enrollments = client.list_agent_enrollments()
    assert enrollments[0].created_by == "u1"
    assert enrollments[0].hostname_hint == "host-x"


def test_list_users_admin_403_raises_auth_error() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(403, json={"error": "admin only"})

    client = _make_client(handler)
    with pytest.raises(AuthError):
        client.list_users()
```

Add `AuthError` to the existing top-of-file `from relay import (...)` block if not already present (it is imported in `test_client.py`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "workers or users or reservations or agent_enrollments" -v`
Expected: FAIL — the `list_workers`/`list_users`/`list_reservations`/`list_agent_enrollments` methods do not exist.

- [ ] **Step 3: Add the eight methods to `client.py`**

In `python/src/relay/client.py`, add a new section after the scheduled-jobs methods (end of the class):

```python
    # ─── Workers ──────────────────────────────────────────────────────────

    def list_workers(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[Worker]:
        """List workers, auto-paginating across all pages. ``limit`` caps total rows."""
        return self._fetch_all("/v1/workers", Worker, sort=sort, limit=limit)

    def list_workers_page(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[Worker]:
        """Fetch a single page of workers. ``limit`` is the page size (1-200)."""
        return self._get_page("/v1/workers", Worker, sort=sort, limit=limit, cursor=cursor)

    # ─── Users (admin-only) ───────────────────────────────────────────────

    def list_users(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[User]:
        """List users, auto-paginating. Admin-only: a non-admin token raises AuthError."""
        return self._fetch_all("/v1/users", User, sort=sort, limit=limit)

    def list_users_page(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[User]:
        """Fetch a single page of users (admin-only). ``limit`` is the page size (1-200)."""
        return self._get_page("/v1/users", User, sort=sort, limit=limit, cursor=cursor)

    # ─── Reservations (admin-only) ────────────────────────────────────────

    def list_reservations(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[Reservation]:
        """List reservations, auto-paginating. Admin-only: non-admin raises AuthError."""
        return self._fetch_all("/v1/reservations", Reservation, sort=sort, limit=limit)

    def list_reservations_page(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[Reservation]:
        """Fetch a single page of reservations (admin-only). ``limit`` is the page size."""
        return self._get_page(
            "/v1/reservations", Reservation, sort=sort, limit=limit, cursor=cursor
        )

    # ─── Agent enrollments (admin-only) ───────────────────────────────────

    def list_agent_enrollments(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[AgentEnrollment]:
        """List active agent enrollments, auto-paginating. Admin-only: non-admin raises AuthError."""
        return self._fetch_all("/v1/agent-enrollments", AgentEnrollment, sort=sort, limit=limit)

    def list_agent_enrollments_page(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[AgentEnrollment]:
        """Fetch a single page of agent enrollments (admin-only). ``limit`` is the page size."""
        return self._get_page(
            "/v1/agent-enrollments", AgentEnrollment, sort=sort, limit=limit, cursor=cursor
        )
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "workers or users or reservations or agent_enrollments" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "python: add list_workers/list_users/list_reservations/list_agent_enrollments and _page siblings" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Version bump, backlog close, full verification

**Files:**
- Modify: `python/pyproject.toml:7`
- Modify: `python/src/relay/_version.py:1`
- Move: `docs/backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md`

- [ ] **Step 1: Bump both version locations to `0.1.2`**

In `python/pyproject.toml`, change line 7:

```toml
version = "0.1.2"
```

In `python/src/relay/_version.py`, change line 1:

```python
__version__ = "0.1.2"
```

- [ ] **Step 2: Verify the version is consistent at runtime**

Run: `python/.venv/Scripts/python.exe -c "import relay; print(relay.__version__)"`
Expected: prints `0.1.2`.

- [ ] **Step 3: Run the full unit suite**

Run: `make python-test`
Expected: PASS — all unit tests green (existing + new).

- [ ] **Step 4: Run linters and type checks**

Run: `make python-lint`
Expected: ruff reports no errors; mypy reports `Success: no issues found`.

If mypy flags the `Page` construction in `_get_page`, confirm the `cast("Page[M]", ...)` wrapper is present exactly as written in Task 2 Step 4.

- [ ] **Step 5: Move the backlog item to closed**

```bash
git mv docs/backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md docs/backlog/closed/bug-2026-05-26-python-sdk-list-pagination-envelope.md
```

- [ ] **Step 6: Commit**

```bash
git add python/pyproject.toml python/src/relay/_version.py
git commit -m "python: bump SDK version to 0.1.2 and close pagination backlog item" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Optional: integration verification

The integration smoke test requires a running `relay-server` with at least one agent and is not part of the unit gate. When an environment is available:

Run: `make python-test-integration`
Expected: `test_list_jobs_includes_recent_submission` passes — the real acceptance check that `list_jobs()` reads the `items` envelope against a live paginated server.

---

## Self-Review Notes

- **Spec coverage:** envelope parsing (Task 2), `Page[T]` + `_get_page` (Task 2), `_fetch_all`/auto-paginate (Task 2), `list_jobs_page` (Task 2), `list_schedules` fix + page (Task 3), four new resources with `list_*`/`list_*_page` (Task 4), four new models + `Page` (Task 1), exports (Task 1), `sort` passthrough + bad-sort `ValidationError` (Task 2), admin-403 `AuthError` (Task 4), `limit` dual semantics (Tasks 2-4 docstrings + tests), version bump (Task 5), backlog close (Task 5). All spec sections map to a task.
- **Type consistency:** `_get_page(path, model, *, params, sort, limit, cursor) -> Page[M]` and `_fetch_all(path, model, *, params, sort, limit) -> list[M]` are used with matching argument names in every `list_*` call. `_job_filters` is the only filter builder and is referenced only by the two jobs methods.
- **No placeholders:** every code and test step contains complete, runnable content.
