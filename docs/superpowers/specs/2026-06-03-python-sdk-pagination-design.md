# Python SDK list methods: pagination envelope support

**Date:** 2026-06-03
**Status:** Design
**Backlog item:** [bug-2026-05-26-python-sdk-list-pagination-envelope](../../backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md)
**Related:** [2026-05-06-list-endpoint-pagination-design](2026-05-06-list-endpoint-pagination-design.md), [2026-05-26-list-endpoint-sort-design](2026-05-26-list-endpoint-sort-design.md)

## Problem

The Python SDK predates cursor pagination. `list_jobs()` and `list_schedules()` in `python/src/relay/client.py` iterate `response.json()` directly, assuming a bare JSON array. Every paginated endpoint now returns an envelope:

```json
{ "items": [ ... ], "next_cursor": "<opaque>", "total": 1234 }
```

So `[Job.model_validate(item) for item in response.json()]` iterates the dict's **keys** (`"items"`, `"next_cursor"`, `"total"`) and validates the string `"items"` as a `Job`. The production code path is broken.

The SDK is also missing list methods for four paginated endpoints (`workers`, `users`, `reservations`, `agent-enrollments`) and exposes no way to page with a cursor or to pass the now-shipped `?sort=` parameter.

## Goal

Make every paginated REST endpoint reachable from the Python SDK, with:

- A simple "give me everything" call returning `list[T]` (auto-paginating across cursors).
- An explicit single-page call returning a `Page[T]` envelope (`items` / `next_cursor` / `total`) for manual cursor control.
- A `sort` parameter on every list method, passed through to `?sort=` and validated server-side.
- Typed pydantic models for the four currently-unmodeled resources.

## Non-goals

- Async client. The SDK is synchronous (`httpx.Client`); this work stays synchronous.
- Client-side sort/filter validation. The server owns the `?sort=` allowlist; the SDK passes the value through and surfaces the server's 400 as `ValidationError`.
- New filters beyond what each endpoint already accepts (see "Filters" below).
- Changing the Go client/CLI, server, or wire format.

## Paginated endpoints (source of truth)

All return `{items, next_cursor, total}` (`internal/api/pagination.go` `page[T]`).

| Endpoint | Admin-only | SDK method (new or fixed) | Filters accepted by server |
| --- | --- | --- | --- |
| `GET /v1/jobs` | no | `list_jobs` (fix) | `status`, `scheduled_job_id` |
| `GET /v1/scheduled-jobs` | no | `list_schedules` (fix) | none |
| `GET /v1/workers` | no | `list_workers` (new) | none |
| `GET /v1/reservations` | yes | `list_reservations` (new) | none |
| `GET /v1/agent-enrollments` | yes | `list_agent_enrollments` (new) | none |
| `GET /v1/users` | yes | `list_users` (new) | none |

"Filters accepted" was confirmed by reading each handler: only `handleListJobs` parses query filters (`status`, `scheduled_job_id`). The others read only `?limit=/?cursor=/?sort=` via `parsePage`.

## Design

### 1. `Page[T]` envelope model

Add to `python/src/relay/models.py`:

```python
from typing import Generic, TypeVar

T = TypeVar("T")

class Page(BaseModel, Generic[T]):
    """One page of a paginated list response.

    ``next_cursor`` is the empty string on the last page. Pass it back as
    ``cursor=`` to fetch the following page.
    """
    model_config = ConfigDict(extra="ignore")

    items: list[T]
    next_cursor: str = ""
    total: int = 0
```

Pydantic v2 supports generic models via `Generic[T]`; `Page[Job]` validates `items` as `list[Job]`.

### 2. Two private helpers on `Client`

Mirroring the Go split (`PageEnvelope[T]` + `FetchAllPages[T]` in `internal/relayclient/page.go`):

```python
_PAGE_REQUEST_LIMIT = 200  # matches relayclient.PageRequestLimit (server max)

def _get_page(self, path, model, *, params) -> Page:
    self._require_token()
    response = self._http.get(path, params=params)
    raise_for_response(response)
    body = response.json()
    items = [model.model_validate(it) for it in body["items"]]
    return Page(items=items,
                next_cursor=body.get("next_cursor", ""),
                total=body.get("total", 0))

def _fetch_all(self, path, model, *, params, limit) -> list:
    self._require_token()
    params = dict(params)
    params["limit"] = str(self._PAGE_REQUEST_LIMIT)
    out: list = []
    while True:
        response = self._http.get(path, params=params)
        raise_for_response(response)
        body = response.json()
        out.extend(model.model_validate(it) for it in body["items"])
        if limit is not None and len(out) >= limit:
            return out[:limit]
        cursor = body.get("next_cursor", "")
        if not cursor:
            return out
        params["cursor"] = cursor
```

Both read `body["items"]` (the bug fix). `_fetch_all` walks cursors until `next_cursor` is empty or `limit` rows are collected, requesting 200 per page to minimize round-trips - identical semantics to `FetchAllPages`.

### 3. List method pairs

Each resource gets an auto-paginating method returning `list[T]` and a single-page method returning `Page[T]`. Example for jobs:

```python
def list_jobs(self, *, status=None, scheduled_job_id=None,
              sort=None, limit=None) -> list[Job]:
    """Fetch jobs, auto-paginating across all pages.

    ``limit`` caps the TOTAL number of rows returned across pages
    (not the page size). ``sort`` is passed to the server's ?sort=
    and validated there; an unknown key raises ValidationError.
    """
    return self._fetch_all("/v1/jobs", Job,
                           params=self._job_list_params(status, scheduled_job_id, sort),
                           limit=limit)

def list_jobs_page(self, *, status=None, scheduled_job_id=None,
                   sort=None, limit=None, cursor=None) -> Page[Job]:
    """Fetch a single page of jobs.

    ``limit`` is the PAGE SIZE sent to the server (1-200). Use the
    returned ``next_cursor`` as ``cursor=`` to page forward.
    """
    params = self._job_list_params(status, scheduled_job_id, sort)
    if limit is not None:
        params["limit"] = str(limit)
    if cursor is not None:
        params["cursor"] = cursor
    return self._get_page("/v1/jobs", Job, params=params)
```

The other five resources follow the same two-method pattern. Only `list_jobs`/`list_jobs_page` carry `status`/`scheduled_job_id`; the rest take just `sort`/`limit`/`cursor`.

Full method set:

- `list_jobs` / `list_jobs_page`
- `list_schedules` / `list_schedules_page`
- `list_workers` / `list_workers_page`
- `list_users` / `list_users_page`
- `list_reservations` / `list_reservations_page`
- `list_agent_enrollments` / `list_agent_enrollments_page`

`list_schedules()` replaces the current broken implementation and keeps returning `list[ScheduledJob]`.

#### `limit` semantics

Deliberately dual, matching the Go client:

- On `list_*` (auto-paginate): `limit` caps the **total** rows returned across all pages. `None` means "all".
- On `list_*_page` (single page): `limit` is the **page size** sent as `?limit=` (server range 1-200).

Each method's docstring states which meaning applies. This mirrors `FetchAllPages(..., userLimit)` vs. a raw `?limit=` request and avoids inventing a second parameter name.

### 4. Four new typed models

Added to `models.py`, all `extra="ignore"` so future server fields don't break older SDKs. Fields mirror the Go response structs (`workerResponse`, `reservationResponse`, `userResponse`, `enrollmentRowToMap`). Optional fields (`*time.Time` with `omitempty`, nullable columns) become `Optional[...] = None`.

```python
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

`Worker.labels` and `Reservation.selector` are JSON objects on the wire (`json.RawMessage`); typed as `dict[str, Any]`.

### 5. Admin-only methods

`list_users`, `list_reservations`, `list_agent_enrollments` hit admin-gated endpoints. Their docstrings note that a non-admin token yields `AuthError` (the server returns 403, which `raise_for_response` maps to `AuthError`). No client-side role check.

### 6. Sort & error handling

`sort` is forwarded verbatim as `?sort=`. The server validates against its per-endpoint `SortSpec`; an unknown key returns HTTP 400, which `raise_for_response` already converts to `ValidationError`. No client-side allowlist to drift out of sync with the server.

### 7. Exports

`python/src/relay/__init__.py` gains: `Page`, `Worker`, `Reservation`, `AgentEnrollment`, `User`.

## Files changed

- `python/src/relay/models.py` — add `Page[T]`, `Worker`, `Reservation`, `AgentEnrollment`, `User`.
- `python/src/relay/client.py` — add `_get_page`, `_fetch_all`, `_PAGE_REQUEST_LIMIT`; rewrite `list_jobs`/`list_schedules`; add the ten new methods and per-resource param builders.
- `python/src/relay/__init__.py` — export new symbols.
- `python/tests/unit/test_client.py` — fix the three tests that mock bare `json=[]`; add pagination tests (below).
- `python/tests/unit/test_models.py` — add validation tests for `Page[T]` and the four new models.
- `python/pyproject.toml` — bump `version` `0.1.1` → `0.1.2`.
- `python/src/relay/_version.py` — bump `__version__` `0.1.0` → `0.1.2`. (These two are currently out of sync — `pyproject.toml` is at `0.1.1`, `_version.py` at `0.1.0`; this re-aligns both to `0.1.2`.)
- `docs/backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md` — `git mv` to `docs/backlog/closed/`.

## Test plan

### Unit (httpx `MockTransport`, no network)

1. **Envelope parsing:** `list_jobs()` against a handler returning `{"items": [job], "next_cursor": "", "total": 1}` returns `[Job]` with the right id (this is the core regression).
2. **Multi-page walk:** handler returns `next_cursor="c1"` on the first call and `""` on the second; assert `list_jobs()` concatenates both pages and that the second request carried `cursor=c1`.
3. **`limit` caps total:** with two pages of 200, `list_jobs(limit=250)` returns exactly 250 and stops.
4. **`list_jobs_page` envelope:** returns a `Page[Job]` with populated `next_cursor` and `total`; `limit`/`cursor` appear as query params.
5. **`sort` passthrough:** `list_jobs(sort="-name")` and `list_jobs_page(sort="name")` send `?sort=`.
6. **Bad sort → `ValidationError`:** handler returns 400 `{"error": "unsupported sort key ..."}`; both forms raise `ValidationError`.
7. **Each new list method:** one happy-path test per resource validating into its model (`Worker`/`User`/`Reservation`/`AgentEnrollment`).
8. **Fix existing tests:** `test_authorization_header_sent` and `test_list_jobs_passes_status_filter` change their mock from `json=[]` to `json={"items": [], "next_cursor": "", "total": 0}`.
9. **Model tests:** `Page[Job].model_validate({...})` yields typed items; each new model validates a representative server payload and tolerates an unknown extra field.

### Integration (`python/tests/integration/test_smoke.py`)

- `test_list_jobs_includes_recent_submission` passes unchanged once `list_jobs` reads `items` — it becomes the real acceptance check against a live paginated server.

## Acceptance criteria

- `test_list_jobs_includes_recent_submission` passes against a current `relay-server`.
- All six paginated REST endpoints have a corresponding SDK `list_*` method, each with a `list_*_page` sibling.
- `list_*` auto-paginates and accepts a total `limit`; `list_*_page` returns a `Page[T]` with `next_cursor`/`total` and accepts `cursor`/`limit`.
- Every list method accepts `sort`, passed through and validated server-side (`ValidationError` on bad key).
- `Worker`, `User`, `Reservation`, `AgentEnrollment`, `Page` are importable from `relay`.
- `make test` (unit) green; new unit tests cover envelope parsing, multi-page walk, `limit`, `sort`, and each new model.
- `python/pyproject.toml` and `python/src/relay/_version.py` both read `0.1.2` (re-aligned from `0.1.1`/`0.1.0`).
- Backlog item moved to `docs/backlog/closed/` on the same branch.

## Rollout

Single PR. No server change, no migration, no wire-format change — this is a pure client catch-up to an envelope the server has returned since the 2026-05-06 pagination work. The SDK is pre-1.0; `list_schedules()` and `list_jobs()` keep their `list[T]` return type, so callers using them as documented are unaffected (and were previously broken anyway).
