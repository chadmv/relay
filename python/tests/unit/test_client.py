from __future__ import annotations

import json
from typing import Any, Callable, Optional

import httpx
import pytest

from relay import (
    AuthError,
    Client,
    Conflict,
    Job,
    JobStatus,
    NotFound,
    OverlapPolicy,
    Priority,
    ValidationError,
)


def _make_client(
    handler: Callable[[httpx.Request], httpx.Response],
    *,
    token: Optional[str] = "test-token",
    config_path: Optional[Any] = None,
) -> Client:
    transport = httpx.MockTransport(handler)
    http = httpx.Client(transport=transport, base_url="http://test")
    return Client(token=token, config_path=config_path, http_client=http)


def _job_response(**overrides: Any) -> dict[str, Any]:
    base: dict[str, Any] = {
        "id": "11111111-1111-1111-1111-111111111111",
        "name": "j",
        "priority": "normal",
        "status": "pending",
        "submitted_by": "22222222-2222-2222-2222-222222222222",
        "submitted_by_email": "u@example.com",
        "labels": {},
        "tasks": [],
        "created_at": "2026-05-06T12:00:00Z",
        "updated_at": "2026-05-06T12:00:00Z",
    }
    base.update(overrides)
    return base


def _page_response(items: list[Any], *, next_cursor: str = "", total: Optional[int] = None) -> dict[str, Any]:
    return {
        "items": items,
        "next_cursor": next_cursor,
        "total": len(items) if total is None else total,
    }


# ─── Auth & wiring ────────────────────────────────────────────────────────────


def test_no_token_raises_auth_error_on_method_call(tmp_path: Any) -> None:
    # config_path points at a missing file, no env, no kwarg
    client = _make_client(lambda r: httpx.Response(200), token=None, config_path=tmp_path / "x")
    with pytest.raises(AuthError, match="relay login"):
        client.list_jobs()


def test_authorization_header_sent(monkeypatch: pytest.MonkeyPatch) -> None:
    captured: dict[str, str] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["auth"] = request.headers.get("authorization", "")
        return httpx.Response(200, json=_page_response([]))

    client = _make_client(handler, token="secret-token")
    client.list_jobs()
    assert captured["auth"] == "Bearer secret-token"


# ─── submit() ────────────────────────────────────────────────────────────────


def test_submit_posts_spec_and_parses_response() -> None:
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["method"] = request.method
        captured["path"] = request.url.path
        captured["body"] = json.loads(request.content)
        return httpx.Response(201, json=_job_response(id="aaa", status="pending"))

    client = _make_client(handler)
    job = Job(name="j", priority=Priority.HIGH)
    job.add_task("t", commands=[["echo", "hi"]])
    result = client.submit(job)

    assert captured["method"] == "POST"
    assert captured["path"] == "/v1/jobs"
    assert captured["body"]["name"] == "j"
    assert captured["body"]["priority"] == "high"
    assert captured["body"]["tasks"][0]["commands"] == [["echo", "hi"]]
    assert result.id == "aaa"
    assert result.status == JobStatus.PENDING


def test_submit_validates_locally_before_request() -> None:
    """A spec with no tasks must fail before the HTTP call is made."""
    called = False

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal called
        called = True
        return httpx.Response(201, json=_job_response())

    client = _make_client(handler)
    with pytest.raises(ValidationError, match="at least one task"):
        client.submit(Job(name="j"))
    assert called is False


def test_submit_surfaces_server_400() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(400, json={"error": "duplicate task name: t"})

    client = _make_client(handler)
    job = Job(name="j")
    job.add_task("t", commands=[["echo", "1"]])
    job.add_task("t2", commands=[["echo", "2"]])
    with pytest.raises(ValidationError, match="duplicate task name"):
        client.submit(job)


# ─── jobs CRUD ───────────────────────────────────────────────────────────────


def test_get_job_404_raises_not_found() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(404, json={"error": "job not found"})

    client = _make_client(handler)
    with pytest.raises(NotFound):
        client.get_job("missing")


def test_list_jobs_passes_status_filter() -> None:
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["query"] = dict(request.url.params)
        return httpx.Response(200, json=_page_response([]))

    client = _make_client(handler)
    client.list_jobs(status=JobStatus.RUNNING)
    assert captured["query"]["status"] == "running"


def test_cancel_job_force_query_param() -> None:
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["method"] = request.method
        captured["query"] = dict(request.url.params)
        return httpx.Response(200, json=_job_response(status="cancelled"))

    client = _make_client(handler)
    result = client.cancel_job("abc", force=True)
    assert captured["method"] == "DELETE"
    assert captured["query"] == {"force": "true"}
    assert result.status == JobStatus.CANCELLED


def test_cancel_job_409_raises_conflict() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(409, json={"error": "already terminal"})

    client = _make_client(handler)
    with pytest.raises(Conflict):
        client.cancel_job("abc")


# ─── tasks / logs ────────────────────────────────────────────────────────────


def test_task_logs_parses_records() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json=[
                {"stream": "stdout", "content": "hi\n", "created_at": "2026-05-06T12:00:00Z"},
                {"stream": "stderr", "content": "warn\n", "created_at": "2026-05-06T12:00:01Z"},
            ],
        )

    client = _make_client(handler)
    logs = client.task_logs("abc")
    assert [log.stream for log in logs] == ["stdout", "stderr"]


# ─── wait() ──────────────────────────────────────────────────────────────────


def test_wait_returns_when_terminal_seen() -> None:
    statuses = iter(["pending", "running", "done"])

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_job_response(status=next(statuses)))

    client = _make_client(handler)
    final = client.wait("abc", poll_interval=0)
    assert final.status == JobStatus.DONE


def test_wait_times_out() -> None:
    from relay import TimeoutError as RelayTimeoutError

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=_job_response(status="running"))

    client = _make_client(handler)
    with pytest.raises(RelayTimeoutError):
        client.wait("abc", timeout=0.05, poll_interval=0.01)


# ─── scheduled jobs ──────────────────────────────────────────────────────────


def test_create_schedule_serializes_job_spec() -> None:
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["body"] = json.loads(request.content)
        return httpx.Response(
            201,
            json={
                "id": "55555555-5555-5555-5555-555555555555",
                "name": "hourly",
                "owner_id": "22222222-2222-2222-2222-222222222222",
                "cron_expr": "@hourly",
                "timezone": "UTC",
                "job_spec": captured["body"]["job_spec"],
                "overlap_policy": "skip",
                "enabled": True,
                "next_run_at": "2026-05-06T13:00:00Z",
                "created_at": "2026-05-06T12:00:00Z",
                "updated_at": "2026-05-06T12:00:00Z",
            },
        )

    client = _make_client(handler)
    job = Job(name="j")
    job.add_task("t", commands=[["echo", "hi"]])
    sched = client.create_schedule(
        name="hourly",
        cron_expr="@hourly",
        job_spec=job,
        overlap_policy=OverlapPolicy.SKIP,
    )
    assert captured["body"]["cron_expr"] == "@hourly"
    assert captured["body"]["overlap_policy"] == "skip"
    assert captured["body"]["job_spec"]["name"] == "j"
    assert sched.id == "55555555-5555-5555-5555-555555555555"


def test_run_schedule_now_admin_403_raises_auth_error() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(403, json={"error": "admin only"})

    client = _make_client(handler)
    with pytest.raises(AuthError):
        client.run_schedule_now("abc")


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
