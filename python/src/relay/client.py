from __future__ import annotations

import time
from collections.abc import Iterator
from pathlib import Path
from typing import Any, Optional, TypeVar, Union, cast

import httpx
from pydantic import BaseModel

from .config import Config, resolve_config
from .errors import (
    AuthError,
    ValidationError,
    raise_for_response,
)
from .errors import (
    TimeoutError as RelayTimeoutError,
)
from .events import parse_sse_stream
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

_TERMINAL_JOB_STATUSES = frozenset(
    {JobStatus.DONE.value, JobStatus.FAILED.value, JobStatus.CANCELLED.value}
)

M = TypeVar("M", bound=BaseModel)


class Client:
    """Synchronous client for the relay REST API.

    Configuration is resolved at construction in this order: explicit kwargs,
    environment (``RELAY_URL`` / ``RELAY_TOKEN``), then the CLI config file
    at ``~/.relay/config.json`` (or ``%APPDATA%\\relay\\config.json`` on
    Windows). If no token is found, methods that require authentication
    raise :class:`AuthError` with a hint pointing at ``relay login``.
    """

    # Per-request page size used when auto-paginating. Matches the server's
    # max limit and relayclient.PageRequestLimit so we minimize round-trips.
    _PAGE_REQUEST_LIMIT = 200

    def __init__(
        self,
        *,
        url: Optional[str] = None,
        token: Optional[str] = None,
        config_path: Optional[Path] = None,
        timeout: float = 30.0,
        http_client: Optional[httpx.Client] = None,
    ) -> None:
        cfg: Config = resolve_config(url=url, token=token, config_path=config_path)
        if not cfg.server_url:
            cfg.server_url = "http://localhost:8080"
        self._config = cfg
        self._owns_client = http_client is None
        if http_client is None:
            http_client = httpx.Client(
                base_url=cfg.server_url,
                timeout=timeout,
                headers=self._auth_headers(cfg.token, required=False),
            )
        else:
            http_client.headers.update(self._auth_headers(cfg.token, required=False))
        self._http = http_client

    @staticmethod
    def _auth_headers(token: str, *, required: bool = True) -> dict[str, str]:
        headers = {"Accept": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        elif required:
            raise AuthError(
                "no relay token configured; run `relay login` or set RELAY_TOKEN"
            )
        return headers

    def _require_token(self) -> None:
        if not self._config.token:
            raise AuthError(
                "no relay token configured; run `relay login` or set RELAY_TOKEN"
            )

    def close(self) -> None:
        if self._owns_client:
            self._http.close()

    def __enter__(self) -> Client:
        return self

    def __exit__(self, *_exc: object) -> None:
        self.close()

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

    # ─── Jobs ─────────────────────────────────────────────────────────────

    def submit(self, job: Job) -> Job:
        """Submit a Job. Validates locally, then POSTs to ``/v1/jobs`` and
        returns the server's response as a populated :class:`Job`.
        """
        self._require_token()
        job.validate_spec()
        response = self._http.post("/v1/jobs", json=job.to_spec_dict())
        raise_for_response(response)
        return Job.model_validate(response.json())

    def get_job(self, job_id: str) -> Job:
        self._require_token()
        response = self._http.get(f"/v1/jobs/{job_id}")
        raise_for_response(response)
        return Job.model_validate(response.json())

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

    def cancel_job(self, job_id: str, *, force: bool = False) -> Job:
        self._require_token()
        params = {"force": "true"} if force else {}
        response = self._http.delete(f"/v1/jobs/{job_id}", params=params)
        raise_for_response(response)
        return Job.model_validate(response.json())

    # ─── Tasks ────────────────────────────────────────────────────────────

    def get_tasks(self, job_id: str) -> list[Task]:
        self._require_token()
        response = self._http.get(f"/v1/jobs/{job_id}/tasks")
        raise_for_response(response)
        return [Task.model_validate(item) for item in response.json()]

    def get_task(self, task_id: str) -> Task:
        self._require_token()
        response = self._http.get(f"/v1/tasks/{task_id}")
        raise_for_response(response)
        return Task.model_validate(response.json())

    def task_logs(self, task_id: str) -> list[LogRecord]:
        self._require_token()
        response = self._http.get(f"/v1/tasks/{task_id}/logs")
        raise_for_response(response)
        return [LogRecord.model_validate(item) for item in response.json()]

    # ─── Following progress ───────────────────────────────────────────────

    def follow_job(self, job_id: str) -> Iterator[Event]:
        """Stream events for a single job over SSE. Yields :class:`Event`
        objects until the server closes the connection (which it does when
        the job reaches a terminal state) or the caller breaks out.

        The underlying HTTP connection is closed on generator exit.
        """
        self._require_token()
        return self._stream_events(job_id)

    def _stream_events(self, job_id: str) -> Iterator[Event]:
        with self._http.stream(
            "GET",
            "/v1/events",
            params={"job_id": job_id},
            headers={"Accept": "text/event-stream"},
            timeout=httpx.Timeout(connect=self._http.timeout.connect, read=None),
        ) as response:
            raise_for_response(response)
            yield from parse_sse_stream(response.iter_lines())

    def wait(
        self,
        job_id: str,
        *,
        timeout: Optional[float] = None,
        poll_interval: float = 1.0,
    ) -> Job:
        """Block until the job reaches a terminal state, then return it.

        Polls ``GET /v1/jobs/{id}`` every ``poll_interval`` seconds. Polling
        is preferred over SSE here for simplicity and correctness: SSE has
        no replay, so a stream that drops would silently never report
        completion. ``timeout`` is wall-clock; raises :class:`TimeoutError`.
        """
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            job = self.get_job(job_id)
            if job.status in _TERMINAL_JOB_STATUSES:
                return job
            if deadline is not None and time.monotonic() >= deadline:
                raise RelayTimeoutError(
                    f"wait timed out after {timeout}s; job status was {job.status!r}"
                )
            time.sleep(poll_interval)

    # ─── Scheduled jobs ───────────────────────────────────────────────────

    def create_schedule(
        self,
        *,
        name: str,
        cron_expr: str,
        job_spec: Union[Job, dict[str, Any]],
        timezone: str = "UTC",
        overlap_policy: Union[str, OverlapPolicy] = OverlapPolicy.SKIP,
        enabled: Optional[bool] = None,
    ) -> ScheduledJob:
        self._require_token()
        if isinstance(job_spec, Job):
            job_spec.validate_spec()
            spec_dict = job_spec.to_spec_dict()
        elif isinstance(job_spec, dict):
            spec_dict = job_spec
        else:
            raise ValidationError(
                f"job_spec must be Job or dict, got {type(job_spec).__name__}"
            )
        body: dict[str, Any] = {
            "name": name,
            "cron_expr": cron_expr,
            "timezone": timezone,
            "overlap_policy": (
                overlap_policy.value
                if isinstance(overlap_policy, OverlapPolicy)
                else overlap_policy
            ),
            "job_spec": spec_dict,
        }
        if enabled is not None:
            body["enabled"] = enabled
        response = self._http.post("/v1/scheduled-jobs", json=body)
        raise_for_response(response)
        return ScheduledJob.model_validate(response.json())

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

    def get_schedule(self, schedule_id: str) -> ScheduledJob:
        self._require_token()
        response = self._http.get(f"/v1/scheduled-jobs/{schedule_id}")
        raise_for_response(response)
        return ScheduledJob.model_validate(response.json())

    def update_schedule(
        self,
        schedule_id: str,
        *,
        name: Optional[str] = None,
        cron_expr: Optional[str] = None,
        timezone: Optional[str] = None,
        overlap_policy: Optional[Union[str, OverlapPolicy]] = None,
        enabled: Optional[bool] = None,
        job_spec: Optional[Union[Job, dict[str, Any]]] = None,
    ) -> ScheduledJob:
        self._require_token()
        body: dict[str, Any] = {}
        if name is not None:
            body["name"] = name
        if cron_expr is not None:
            body["cron_expr"] = cron_expr
        if timezone is not None:
            body["timezone"] = timezone
        if overlap_policy is not None:
            body["overlap_policy"] = (
                overlap_policy.value
                if isinstance(overlap_policy, OverlapPolicy)
                else overlap_policy
            )
        if enabled is not None:
            body["enabled"] = enabled
        if job_spec is not None:
            if isinstance(job_spec, Job):
                job_spec.validate_spec()
                body["job_spec"] = job_spec.to_spec_dict()
            else:
                body["job_spec"] = job_spec
        response = self._http.patch(f"/v1/scheduled-jobs/{schedule_id}", json=body)
        raise_for_response(response)
        return ScheduledJob.model_validate(response.json())

    def delete_schedule(self, schedule_id: str) -> None:
        self._require_token()
        response = self._http.delete(f"/v1/scheduled-jobs/{schedule_id}")
        raise_for_response(response)

    def run_schedule_now(self, schedule_id: str) -> Job:
        """Fire a schedule immediately. Admin-only on the server; non-admin
        callers get :class:`AuthError`.
        """
        self._require_token()
        response = self._http.post(f"/v1/scheduled-jobs/{schedule_id}/run-now")
        raise_for_response(response)
        return Job.model_validate(response.json())

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
        """Fetch a single page of reservations (admin-only). ``limit`` is the page size (1-200)."""
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
        """Fetch a single page of agent enrollments (admin-only). ``limit`` is the page size (1-200)."""
        return self._get_page(
            "/v1/agent-enrollments", AgentEnrollment, sort=sort, limit=limit, cursor=cursor
        )
