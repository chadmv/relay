from __future__ import annotations

import json
from typing import Any, Callable, Optional

import httpx
import pytest
from pydantic import ValidationError as PydanticValidationError

from relay import (
    AuthError,
    Client,
    Conflict,
    Job,
    JobStatus,
    NotFound,
    OverlapPolicy,
    Priority,
    ProtocolError,
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

    The message is only half of it, and the half that was never checked is what
    the CALLER RECEIVES. printTaskLogs (internal/cli/logs.go) has already
    written every row to `out` by the time it returns this error - the error is
    a completeness caveat on output the operator can already see. Assert the
    records survive the raise, or the port keeps the Go wording and drops the
    Go semantics.
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
    assert "4 distinct rows" in message
    assert [r.seq for r in excinfo.value.records] == [1, 2, 3, 4]


def test_task_logs_page_cap_completeness_is_distinct_seqs_not_a_record_count(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The completeness claim above is only sound if it counts DISTINCT rows.

    `len(out)` counts records APPENDED and `total` is server-supplied, so a
    server that serves the same page twice behind an advancing cursor drives
    them equal while half the log was never sent. This handler does exactly
    that: rows 1 and 2 twice, cursor 2 then 4, total 4. Rows 3 and 4 do not
    exist on the wire at any point.

    An implementation that tests `len(out) >= page.total` tells the operator
    every row was collected. That is the original defect - a silently
    incomplete log presented as complete - re-created inside the fix for it.
    """
    monkeypatch.setattr(Client, "_MAX_LOG_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        return httpx.Response(
            200,
            json=_log_page(
                [_log_row(1), _log_row(2)], next_seq=len(calls) * 2, total=4
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.task_logs("abc")

    message = str(excinfo.value)
    assert "every one was collected" not in message
    assert "may be longer" in message
    # Duplicates and all: the client does not know which of them the server
    # meant, so it hands back exactly what it received.
    assert [r.seq for r in excinfo.value.records] == [1, 2, 1, 2]


def test_task_logs_page_cap_hands_back_what_it_collected(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The generic cap arm - the one that fires on a log genuinely longer than
    the cap - must not discard the records either.

    _MAX_LOG_PAGES is PRIVATE, so a caller who hits this has no supported way
    to raise the bound and re-run. Throwing away 2,000,000 collected records
    leaves them with nothing but the message.
    """
    monkeypatch.setattr(Client, "_MAX_LOG_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        seq = len(calls) * 2
        return httpx.Response(
            200,
            json=_log_page([_log_row(seq - 1), _log_row(seq)], next_seq=seq, total=9999),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.task_logs("abc")

    assert "may be longer" in str(excinfo.value)
    assert [r.seq for r in excinfo.value.records] == [1, 2, 3, 4]


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


def test_run_schedule_now_forbidden_raises_auth_error() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(403, json={"error": "forbidden"})

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
