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
        return httpx.Response(200, json=[])

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
        return httpx.Response(200, json=[])

    client = _make_client(handler)
    client.list_jobs(status=JobStatus.RUNNING)
    assert captured["query"] == {"status": "running"}


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
