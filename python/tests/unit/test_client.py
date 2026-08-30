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
    ServerError,
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


def test_task_logs_page_escapes_the_task_id_into_one_path_segment() -> None:
    """internal/cli/logs.go:714-723 - the exact function this method ports -
    calls url.PathEscape(taskID), with a comment saying the escape means the
    argument does not rest on its provenance. The port carried the reasoning
    nowhere and interpolated the id raw.

    Two things it bought, both measured against httpx 0.28.1:

      - '../../v1/users' resolved to /v1/users/logs. Same host, bearer token
        attached: the id chooses the ENDPOINT.
      - 'abc?limit=1&x=' resolved to /v1/tasks/abc?limit=200&x=%2Flogs - the
        /logs suffix and the paging params both gone, which is a silently wrong
        request rather than a failure.

    (SSRF and header injection do NOT reach: an absolute URL keeps the client's
    host, and httpx rejects CR/LF in a URL. This is a path-shape defect.)

    The assertions are on raw_path because that is what goes on the wire; a
    check on .path would read the DECODED form and pass against no escape at
    all. %2F survives undecoded, so the traversal never reaches the server as
    a slash.

    quote(safe="") escapes at least as much as url.PathEscape, not exactly as
    much: Go leaves + : @ = & $ alone and Python encodes them. The two agree
    on a UUID, and the difference is in the safe direction, so this is a note
    against a future reader assuming they are interchangeable.
    """
    calls: list[bytes] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(request.url.raw_path)
        return httpx.Response(200, json=_log_page([], next_seq=0, total=0))

    client = _make_client(handler)
    client.task_logs_page("../../v1/users")
    client.task_logs_page("abc?limit=1&x=", limit=25)
    client.task_logs_page("abc", since_seq=10)

    assert calls == [
        b"/v1/tasks/..%2F..%2Fv1%2Fusers/logs",
        b"/v1/tasks/abc%3Flimit%3D1%26x%3D/logs?limit=25",
        b"/v1/tasks/abc/logs?since_seq=10",
    ]


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


def test_task_logs_rejects_a_non_positive_limit() -> None:
    """`limit` is documented as capping the TOTAL number of records returned,
    and the loop implements that with `out[:limit]`. Python slice semantics
    then make a NEGATIVE limit mean something else entirely: limit=-1 on a
    5-row log returned 4 records, silently dropping the LAST one - the newest
    line, which is the one an operator reading a log is usually after.

    limit=0 was merely wasteful: a round trip to return [].

    Both are rejected before the loop, so the assertion that no request was
    made is half the test. The SDK validates locally first everywhere else
    (submit() is the precedent), and a limit the caller cannot have meant
    should not reach the server.
    """
    calls: list[dict[str, str]] = []
    client = _make_client(_serve_logs(_log_rows(1, 5), calls))

    for bad in (0, -1):
        with pytest.raises(ValidationError, match="limit"):
            client.task_logs("abc", limit=bad)
    assert calls == []

    # The boundary is admitted, and it is not a no-op.
    assert [r.seq for r in client.task_logs("abc", limit=1)] == [1]


def test_task_logs_page_404_raises_not_found() -> None:
    """task_logs_page had no error-path test at all: deleting its
    raise_for_response(...) left all 116 tests green. A 404 here is the
    ordinary case - handleGetTaskLogs 404s on an unknown task id - and without
    the translation it reached LogPage.model_validate as an {"error": ...}
    body and surfaced as a pydantic error about a missing `items`.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(404, json={"error": "task not found"})

    client = _make_client(handler)
    with pytest.raises(NotFound, match="task not found"):
        client.task_logs_page("abc")


def test_task_logs_page_without_a_token_raises_before_the_request(tmp_path: Any) -> None:
    """The other untested gate on the same method: deleting its
    _require_token() also left every test green.
    """
    called = False

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal called
        called = True
        return httpx.Response(200, json=_log_page([], next_seq=0, total=0))

    client = _make_client(handler, token=None, config_path=tmp_path / "x")
    with pytest.raises(AuthError, match="relay login"):
        client.task_logs_page("abc")
    assert called is False


def test_task_logs_raises_on_empty_page_that_is_not_drained() -> None:
    """Stop 1. Unreachable against the real handler, which sets next_seq = 0
    whenever the page is short - which is exactly why it must RAISE and not
    return quietly. The only server that reaches this line is misbehaving, and
    a quiet return would launder that into a completeness claim the client
    cannot support.

    The DISCRIMINATING page is request 2 of 3, never the last. A bad input
    placed LAST cannot detect an early-exit mutation; the normal drained page
    3 after it is what makes a mutant that deletes the stop TERMINATE
    (returning rows 1, 2, 6) rather than spin, so deleting the stop is RED and
    not a hang, and `len(calls) == 2` is what distinguishes the stop firing
    here from the walk simply running to the end.

    Page 1 is a real page rather than the empty one this test used to open
    with, because `.records` is the other half of the contract and `[]` cannot
    tell a preserved envelope from a dropped one: deleting `records=out` at
    this raise site left all 132 tests green. Rows 1 and 2 are collected before
    the stop and must survive it.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) == 1:
            return httpx.Response(
                200, json=_log_page([_log_row(1), _log_row(2)], next_seq=2, total=3)
            )
        if len(calls) == 2:
            return httpx.Response(200, json=_log_page([], next_seq=5, total=3))
        return httpx.Response(200, json=_log_page([_log_row(6)], next_seq=0, total=3))

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="empty page") as excinfo:
        client.task_logs("abc")
    assert len(calls) == 2
    assert [r.seq for r in excinfo.value.records] == [1, 2]


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

    `.records` is pinned here too - deleting `records=out` at this raise site
    left all 132 tests green. The expected value is [7, 7], the SAME ROW TWICE,
    and that is not a fixture accident: `out.extend(page.items)` runs before the
    cursor check, so the page whose cursor did not advance has already been
    appended. This is the one stop where a duplicated tail in `.records` is
    guaranteed rather than merely possible, which is why the docstrings say so.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) <= 2:
            # Asked for since_seq=7 on request 2, answers next_seq=7 again.
            return httpx.Response(200, json=_log_page([_log_row(7)], next_seq=7, total=9))
        return httpx.Response(200, json=_log_page([_log_row(8)], next_seq=0, total=9))

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="did not advance") as excinfo:
        client.task_logs("abc")
    assert len(calls) == 2
    assert [r.seq for r in excinfo.value.records] == [7, 7]


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


# ─── follow_job() ────────────────────────────────────────────────────────────


def test_follow_job_yields_events_and_disables_only_the_read_timeout() -> None:
    """The first test this method has ever had. It shipped unusable: the stream
    built `httpx.Timeout(connect=..., read=None)`, and httpx takes the
    four-explicit-parameters branch only when connect, read, write and pool are
    ALL set - otherwise it raises ValueError. So every caller who iterated
    follow_job() got ValueError on the first frame, and the docstring's central
    claim (that a caller who iterates to exhaustion blocks forever) described
    behaviour no caller could reach.

    The timeout assertion is not decoration: it is the only thing that pins
    WHICH of the four the fix disables. `httpx.Timeout(self._http.timeout)`
    alone parses fine and reinstates the 5 s read timeout the SSE stream must
    not have; the read=None assertion is what kills that.
    """
    captured: dict[str, Any] = {}
    body = (
        'event: job\ndata: {"id": "j1", "status": "running"}\n\n'
        'event: dropped\ndata: {"reason": "slow consumer"}\n\n'
    )

    def handler(request: httpx.Request) -> httpx.Response:
        captured["path"] = request.url.path
        captured["query"] = dict(request.url.params)
        captured["accept"] = request.headers.get("accept", "")
        captured["timeout"] = request.extensions["timeout"]
        return httpx.Response(
            200, text=body, headers={"content-type": "text/event-stream"}
        )

    client = _make_client(handler)
    events = list(client.follow_job("j1"))

    assert [(e.type, e.data) for e in events] == [
        ("job", {"id": "j1", "status": "running"}),
        ("dropped", {"reason": "slow consumer"}),
    ]
    assert captured["path"] == "/v1/events"
    assert captured["query"] == {"job_id": "j1"}
    assert captured["accept"] == "text/event-stream"
    assert captured["timeout"]["read"] is None
    assert captured["timeout"]["connect"] == 5.0  # the client's own, unchanged


def test_follow_job_without_a_token_raises_before_the_request(tmp_path: Any) -> None:
    """follow_job returns a generator, so a _require_token inside the generator
    body would not run until the first next() - and a caller who only builds
    the iterator would see no error at all. It is checked eagerly instead.
    """
    called = False

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal called
        called = True
        return httpx.Response(200, text="")

    client = _make_client(handler, token=None, config_path=tmp_path / "x")
    with pytest.raises(AuthError, match="relay login"):
        client.follow_job("j1")
    assert called is False


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


def test_fetch_all_rejects_a_non_positive_limit() -> None:
    """The sibling walk carries test_task_logs_rejects_a_non_positive_limit's
    defect on six public methods.

    `_fetch_all` ends with the same `out[:limit]`, so `list_jobs(limit=-1)`
    silently drops the LAST row - here, the newest job - exactly as
    `task_logs(limit=-1)` did. Validating one and not the other is worse than
    validating neither: the parameter has the same name and the same
    documented meaning on all seven methods, so a caller who learns it is
    checked on task_logs() reasonably assumes it is checked next door.

    The discriminating input is the NEGATIVE limit against a multi-row page,
    because limit=0 is caught by any `limit < 1` guard while limit=-1 is the
    one that returns a plausible-looking short list instead of raising.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        rows = [{"id": f"j{i}", "name": f"job-{i}"} for i in range(1, 6)]
        return httpx.Response(200, json={"items": rows, "next_cursor": "", "total": 5})

    client = _make_client(handler)

    for bad in (0, -1):
        with pytest.raises(ValidationError, match="limit"):
            client.list_jobs(limit=bad)
    assert calls == []


def test_a_bad_limit_does_not_pre_empt_the_missing_token_error(tmp_path: Any) -> None:
    """Precedence, on both walks. The limit guards were added ABOVE
    `_require_token()`, so a token-less client calling `list_jobs(limit=0)`
    reported a ValidationError about the limit while every other method on the
    same client reported AuthError. `task_logs` had the same inversion relative
    to `task_logs_page`, which checks the token itself.

    Missing credentials are the condition the caller has to fix first and the
    one the SDK already advertises with a `relay login` hint; a client that has
    no token cannot make the request whatever the limit says. The two guards
    disagreeing about which comes first is the defect, so the assertion is on
    the CLASS, not on either guard firing.
    """
    called = False

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal called
        called = True
        return httpx.Response(200, json=_page_response([]))

    client = _make_client(handler, token=None, config_path=tmp_path / "x")

    for bad in (0, -1):
        with pytest.raises(AuthError, match="relay login"):
            client.list_jobs(limit=bad)
        with pytest.raises(AuthError, match="relay login"):
            client.task_logs("abc", limit=bad)
    assert called is False


# ─── cursor quoting ──────────────────────────────────────────────────────────


def test_quote_cursor_returns_a_short_cursor_verbatim() -> None:
    """A real relay cursor is base64url of a ~96-byte {t,i,s} JSON, so about 128
    characters. The threshold is 200, so every legitimate cursor is quoted in
    full and an operator can paste it back.
    """
    from relay.client import _quote_cursor

    assert _quote_cursor("eyJ0IjoiMjAyNi0wOC0yOCJ9") == "'eyJ0IjoiMjAyNi0wOC0yOCJ9'"


def test_quote_cursor_bounds_a_long_cursor() -> None:
    """The cursor is SERVER-SUPPLIED and its length is unbounded, so a message
    built from it is unbounded too. The bound must still leave the message
    diagnosable, so the true length is reported alongside the prefix.
    """
    from relay.client import _quote_cursor

    quoted = _quote_cursor("a" * 5000)

    assert len(quoted) < 300
    assert "truncated from 5000 characters" in quoted
    assert "a" * 5000 not in quoted
    assert "a" * 200 in quoted


# ─── _fetch_all termination stops ────────────────────────────────────────────
#
# Every fixture body below is a hand-written dict literal, built by
# _page_response and _job_response. NEVER build one by dumping Page[Job] or Job:
# a fixture encoded through the type under test agrees with the decoder by
# construction, on the envelope keys AND on the item fields, and can detect
# drift in neither direction. Same rule, same reason, as the task-log fixtures
# above.
#
# Every fixture also has a TERMINATOR - an HTTP 500 past the request count the
# correct implementation makes. This project has no pytest-timeout. Without the
# terminator, deleting the stop under test leaves the handler answering forever
# and the test HANGS instead of failing. With it, the mutant raises ServerError,
# which is not ProtocolError, so the test is RED.


def test_fetch_all_raises_on_an_empty_page_that_still_advertises_more() -> None:
    """Stop 1. On a correct server this is unreachable - buildPage
    (internal/api/pagination.go) returns ([], "") for zero rows and emits a
    cursor only when it kept at least one row - which is the point: the loop is
    driven by a value the client does not control, and "no correct server does
    this" is a statement about correct servers.

    Page 1 must be NON-EMPTY. With an empty page 1, `.records` is [] under both
    the correct code and a mutant that drops records=, and the payload assertion
    is vacuous.

    Page 2's cursor must DIFFER from page 1's, or the repeated-cursor stop
    becomes a second possible explanation for the raise and the diagnosis is
    unpinned. The match= is the other half of that.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) == 1:
            return httpx.Response(
                200,
                json=_page_response(
                    [_job_response(id="j1"), _job_response(id="j2")],
                    next_cursor="CUR-ONE",
                    total=99,
                ),
            )
        if len(calls) == 2:
            return httpx.Response(
                200, json=_page_response([], next_cursor="CUR-TWO", total=99)
            )
        return httpx.Response(500, json={"error": "past the stop"})

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="empty page") as excinfo:
        client.list_jobs()

    assert len(calls) == 2
    assert "CUR-TWO" in str(excinfo.value)
    assert [j.id for j in excinfo.value.records] == ["j1", "j2"]


def test_fetch_all_zero_matching_rows_is_not_an_error() -> None:
    """The drained return MUST stay above the empty-page stop.

    A list with no matching rows answers `items: []` with `next_cursor: ""` -
    that IS the legitimate empty page here, and it reports itself drained.
    Testing emptiness first turns list_jobs() against an empty jobs table into a
    ProtocolError. That inversion is a one-line mutation (M4).
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        return httpx.Response(200, json=_page_response([], total=0))

    client = _make_client(handler)
    assert client.list_jobs() == []
    assert len(calls) == 1


def test_fetch_all_raises_when_the_server_repeats_a_cursor() -> None:
    """Stop 2, self-loop. The repro from the backlog item: a server answering
    the same cursor forever drove 2000 requests and counting.

    The cursor here is an opaque base64 string with no order, so "did not
    advance" cannot be a comparison the way task_logs' `next_seq <= since` is.
    The stop is membership: this walk already requested this cursor.

    Membership is tested BEFORE the cursor is recorded, so a self-loop fires on
    request 2 - hence `len(calls) == 2`, not 3.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 3:
            return httpx.Response(500, json={"error": "past the stop"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{len(calls)}")], next_cursor="CUR-SAME", total=99
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="already requested") as excinfo:
        client.list_jobs()

    assert len(calls) == 2
    assert "CUR-SAME" in str(excinfo.value)
    assert [j.id for j in excinfo.value.records] == ["j1", "j2"]


def test_fetch_all_raises_on_a_two_cycle_of_cursors() -> None:
    """THIS is the test that discriminates a seen-SET from a comparison against
    the immediately previous cursor.

    Under previous-cursor-only, A,B,A,B never fires: it runs to the page cap,
    10000 requests and up to 2,000,000 retained rows later. That is not an
    exotic adversarial construction - two replicas behind a load balancer with
    different data, or a caching proxy alternating two cached bodies, produce
    exactly this.

    The set fires on request 3, when A comes round again.
    """
    calls: list[dict[str, str]] = []
    cursors = ["CUR-A", "CUR-B", "CUR-A"]

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > len(cursors):
            return httpx.Response(500, json={"error": "past the stop"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{len(calls)}")],
                next_cursor=cursors[len(calls) - 1],
                total=99,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="already requested") as excinfo:
        client.list_jobs()

    assert len(calls) == 3
    assert "CUR-A" in str(excinfo.value)


def test_fetch_all_truncates_an_over_long_cursor_in_its_message() -> None:
    """The message quotes a value the SERVER chose, so the message's length is
    the server's to choose unless the client bounds it.

    This is the WIRING half of the _quote_cursor tests: asserting the helper
    truncates proves nothing about the code that builds the message.

    Note this fixture is also a self-loop, so deleting the repeated-cursor stop
    (M1) reddens this test too. That is expected and recorded in the mutation
    table; it is not a sign the truncation is unpinned - M8 kills this test
    while leaving the stop intact.
    """
    huge = "z" * 5000
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 3:
            return httpx.Response(500, json={"error": "past the stop"})
        return httpx.Response(
            200,
            json=_page_response([_job_response(id="j1")], next_cursor=huge, total=99),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert len(message) < 1000
    assert "truncated from 5000 characters" in message
    assert huge not in message


def test_fetch_all_truncates_an_over_long_cursor_in_the_empty_page_message() -> None:
    """The SECOND raise site that interpolates a cursor, and it was unpinned.

    _fetch_all quotes a server-chosen cursor in two messages, and the test above
    reaches only one of them: its fixture is a self-loop, so it always arrives at
    the repeated-cursor message. Measured, replacing _quote_cursor(cursor) with
    cursor!r at the EMPTY-PAGE site alone survived the entire suite.

    The bound belongs to the raise site, not to the helper - a helper that
    truncates proves nothing about a caller that does not use it - so each site
    needs its own fixture. This one reaches the empty-page stop: page 1 is
    non-empty with a short cursor, page 2 is empty with a 5000-character one.

    The two cursors differ, so the repeated-cursor stop is not a second possible
    explanation for the raise; the match= is the other half of that.
    """
    huge = "q" * 5000
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) == 1:
            return httpx.Response(
                200,
                json=_page_response(
                    [_job_response(id="j1")], next_cursor="CUR-SHORT", total=99
                ),
            )
        if len(calls) == 2:
            return httpx.Response(
                200, json=_page_response([], next_cursor=huge, total=99)
            )
        return httpx.Response(500, json={"error": "past the stop"})

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="empty page") as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert len(calls) == 2
    assert len(message) < 1000
    assert "truncated from 5000 characters" in message
    assert huge not in message
    assert [j.id for j in excinfo.value.records] == ["j1"]


def test_fetch_all_raises_at_the_page_cap(monkeypatch: pytest.MonkeyPatch) -> None:
    """Stop 3, which catches an ever-advancing, never-repeating cursor that
    never drains - something neither of the other two stops can see.

    _MAX_LIST_PAGES is a CLASS attribute so this monkeypatch works, which means
    the loop must read it off `self` and never off a module global.

    The request-count assertion is not decoration: a test that only checks the
    exception class cannot tell the cap from a different stop firing, and it
    cannot see an off-by-one in the cap's own predicate.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 3)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 3:
            return httpx.Response(500, json={"error": "past the cap"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{len(calls)}")],
                next_cursor=f"CUR-{len(calls)}",
                total=9999,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="page cap"):
        client.list_jobs()
    assert len(calls) == 3


def test_fetch_all_page_cap_quotes_the_last_pages_total_not_the_first(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The cap message says "the server's last page reported N", and N really
    has to come off the LAST page.

    Every other cap fixture in this file sends a CONSTANT total on every page,
    so not one of them can tell which page the number was read from. Measured:
    an SDK that captures page 1's total and interpolates that instead leaves all
    159 of them green. The three totals here differ per page for exactly that
    reason - do NOT flatten them back to a constant.

    Python's claim is the STRONGER of the two SDKs' and so the one that needed
    pinning. internal/relayclient/page.go says "the server's first page
    reported", because FetchAllPages returns the first page's total as its own
    second return value and the message is describing that number; this walk
    returns no total at all, reads it fresh on every page, and says so.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 3)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 3:
            return httpx.Response(500, json={"error": "past the cap"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{len(calls)}")],
                next_cursor=f"CUR-{len(calls)}",
                total=6 + len(calls),
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert len(calls) == 3
    assert "3 rows were collected (3 distinct row ids)" in message
    assert "the server's last page reported 9" in message
    assert "reported 7" not in message
    assert "reported 8" not in message
    assert [j.id for j in excinfo.value.records] == ["j1", "j2", "j3"]


def test_fetch_all_page_cap_makes_no_completeness_claim_when_total_is_reached(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Stop 3's message asserts NEITHER possibility: it does not blame the
    server, and it does not claim every row was collected. It REPORTS the two
    numbers it has, which is not the same thing.

    It used to split on completeness whenever `distinct >= total`, justified by
    "a list of exactly _MAX_LIST_PAGES * _PAGE_REQUEST_LIMIT rows drains
    correctly, but its last page is full and so carries a cursor". That premise
    is true for task_logs and FALSE for a list walk, and the asymmetry is on the
    wire:

      - GetTaskLogsPage (internal/store/query/tasks.sql) is `LIMIT $3` - no
        over-fetch - and handleGetTaskLogs (internal/api/tasks.go) sets
        next_seq = 0 only when `len(items) < limit`. A FULL last log page does
        carry a non-zero cursor, so the log walk really does stop one request
        short. The premise holds there.
      - Every list query is `LIMIT sqlc.arg(page_limit)::int + 1`, and every
        list handler goes through buildPage (internal/api/pagination.go), which
        does `hasMore := int32(len(rows)) > limit` and returns an empty cursor
        when that is false. A list page carries a cursor only when a row
        genuinely exists BEYOND it, so a list that is an exact multiple of the
        page size drains at its last full page and never reaches the cap.

    So on a list, reaching the cap means the server IS misbehaving - and the
    removed arm then settled completeness using `total`, a number that same
    misbehaving actor supplies. A server that keeps advancing cursors and
    reports total: 1000 on a five-million-row list got the SDK to tell the
    operator every row was collected on a walk 0.02% complete.

    This fixture is the exact input that used to trigger the claim: distinct ==
    total == 4. The Go side reached the same conclusion first and says so at
    internal/relayclient/page.go.

    The outcome assertion is not optional. The sibling's version of this test
    was green BECAUSE OF a different bug until `.records` was asserted.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        n = len(calls) * 2
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{n - 1}"), _job_response(id=f"j{n}")],
                next_cursor=f"CUR-{len(calls)}",
                total=4,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert "every one was collected" not in message
    assert "completeness" not in message
    assert "4 rows were collected (4 distinct row ids)" in message
    assert "the server's last page reported 4" in message
    assert [j.id for j in excinfo.value.records] == ["j1", "j2", "j3", "j4"]


def test_fetch_all_page_cap_reports_distinct_ids_alongside_the_row_count(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The message reports rows APPENDED and DISTINCT ids as two separate
    numbers, because they are two different measurements and their disagreement
    is the whole diagnostic value of the second one.

    A server that re-serves a page behind an ADVANCING cursor drives len(out) up
    while sending nothing new - and the repeated-cursor stop cannot see it,
    because the cursor genuinely advances. This handler does exactly that: ids
    j1 and j2 twice, cursors CUR-1 then CUR-2, total 4. Rows 3 and 4 do not
    exist on the wire at any point, and an operator reading "4 rows collected"
    alone would never know.

    Pinning both numbers in ONE substring is what makes this discriminating: a
    mutant that reports len(out) as the distinct count says "(4 distinct row
    ids)" and dies here.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id="j1"), _job_response(id="j2")],
                next_cursor=f"CUR-{len(calls)}",
                total=4,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert "every one was collected" not in message
    assert "completeness" not in message
    assert "4 rows were collected (2 distinct row ids)" in message
    # Duplicates and all: the client does not know which of them the server
    # meant, so it hands back exactly what it received.
    assert [j.id for j in excinfo.value.records] == ["j1", "j2", "j1", "j2"]


def test_fetch_all_page_cap_says_so_when_no_row_carried_an_id(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The third message arm, and it exists for list_jobs SPECIFICALLY.

    Of the six models _fetch_all walks, five declare `id: str` - required and
    undefaulted - so a row missing `id` fails inside model_validate long before
    the cap arm runs, and this arm is unreachable for them BY CONSTRUCTION.
    `Job` declares `id: Optional[str] = None`, because Job is the authoring
    model too and Job(name="nightly") must keep working. So list_jobs is the one
    method that can reach here, which is why this fixture is a jobs walk.

    The message must NOT print "0 distinct row ids": that is a computed-looking
    number standing in for a measurement that did not happen. It says no
    distinct-row count can be given, and why.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        n = len(calls) * 2
        # Hand-written rows that deliberately carry NO "id" key. Job's only
        # required field is `name`.
        return httpx.Response(
            200,
            json=_page_response(
                [{"name": f"n{n - 1}"}, {"name": f"n{n}"}],
                next_cursor=f"CUR-{len(calls)}",
                total=4,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert "carry no id" in message
    assert "0 distinct" not in message
    assert "every one was collected" not in message
    assert "completeness" not in message
    assert "4 rows were collected" in message
    assert [j.name for j in excinfo.value.records] == ["n1", "n2", "n3", "n4"]


def test_fetch_all_limit_satisfied_on_page_two_by_a_page_that_repeats_a_cursor() -> None:
    """The `limit` short-circuit stays ABOVE every stop.

    A caller who asked for 3 rows and has 3 rows has been served. Turning that
    into an error because the page that completed the order also repeated a
    cursor would make a correct result depend on a defect the caller never
    observes.

    The discriminating case is narrower than it looks and no existing test
    covers it. Neither the cursor-repeat stop nor the page cap can fire on
    request 1 - there is no previous cursor, and pages == 1 < cap - so a walk
    satisfied on page 1 proves nothing about the ordering. `limit` must be
    satisfied on page 2 OR LATER, by a page that also trips a stop. That is why
    both pages return cursor CUR-A.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the stop"})
        n = len(calls) * 2
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{n - 1}"), _job_response(id=f"j{n}")],
                next_cursor="CUR-A",
                total=99,
            ),
        )

    client = _make_client(handler)
    jobs = client.list_jobs(limit=3)

    assert [j.id for j in jobs] == ["j1", "j2", "j3"]
    assert len(calls) == 2


# ─── _fetch_all envelope typing ──────────────────────────────────────────────
#
# _page_response cannot express any of these bodies: it is typed
# `next_cursor: str, total: Optional[int]`, which is exactly why nothing in this
# file had ever varied the TYPE of an envelope field. The envelope is chosen by
# the SERVER, so its field TYPES are the server's to choose in the same sense
# its values are - the argument the termination stops already rest on, applied
# one level up. These bodies are therefore hand-written inline.
#
# The contract asserted is pydantic's ValidationError, NOT a relay error.
# ValidationError escaping the RelayError hierarchy is a separate, tracked
# defect (bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy);
# what these pin is that a wrong-typed envelope reaches ONE documented decoding
# failure instead of a raw TypeError from deep inside the loop.


def test_fetch_all_discards_collected_rows_when_a_mid_walk_page_is_an_http_error() -> None:
    """The counterexample the six list_* docstrings used to promise away.

    They said "a walk that cannot be completed raises ProtocolError with the
    rows collected so far on .records" without qualification. This walk cannot
    be completed and raises ServerError, which carries no .records at all - and
    page 1's two rows are gone. The claim is now scoped to the client's own
    termination stops, and this pins the other side of that scope.

    It also locks the shape against a plausible future "improvement": wrapping a
    mid-walk transport or HTTP failure in ProtocolError so the partial rows
    survive would be a real design change, not a bug fix, and it would silently
    change what an existing caller's `except` clause catches.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) == 1:
            return httpx.Response(
                200,
                json=_page_response(
                    [_job_response(id="j1"), _job_response(id="j2")],
                    next_cursor="CUR-ONE",
                    total=99,
                ),
            )
        return httpx.Response(500, json={"error": "boom"})

    client = _make_client(handler)
    with pytest.raises(ServerError) as excinfo:
        client.list_jobs()

    assert len(calls) == 2
    assert not hasattr(excinfo.value, "records")


def test_fetch_all_discards_collected_rows_when_a_mid_walk_page_cannot_decode() -> None:
    """The other half, and the likelier one in practice: ordinary server/SDK
    version skew, where a row stops matching the model mid-walk.

    pydantic.ValidationError does not descend from RelayError - that is the
    tracked bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy
    - and it carries no records either. Page 1's rows are discarded, which is
    exactly the case the old docstring promised .records for.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) == 1:
            return httpx.Response(
                200,
                json=_page_response(
                    [_job_response(id="j1")], next_cursor="CUR-ONE", total=99
                ),
            )
        # `name` is Job's one required field, so a row without it cannot decode.
        return httpx.Response(
            200, json=_page_response([{"id": "j2"}], next_cursor="", total=99)
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError) as excinfo:
        client.list_jobs()

    assert len(calls) == 2
    assert not hasattr(excinfo.value, "records")


def test_fetch_all_rejects_a_non_string_next_cursor() -> None:
    """Fires on request 2, with no page cap involved: an int cursor is hashable,
    so it survives the `in seen` membership test on request 2 and reaches
    _quote_cursor, which asks it for a len().
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the decode"})
        return httpx.Response(
            200,
            json={"items": [_job_response(id="j1")], "next_cursor": 12345, "total": 5},
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()


def test_fetch_all_rejects_a_list_next_cursor() -> None:
    """The other half of the cursor-type pair, and it fails EARLIER than the int
    one on the un-fixed code: a list is unhashable, so `cursor in seen` raises
    before _quote_cursor is ever reached. Two crash sites, one shape.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the decode"})
        return httpx.Response(
            200,
            json={"items": [_job_response(id="j1")], "next_cursor": ["x"], "total": 5},
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()


@pytest.mark.parametrize("bad_total", ["many", None, {"n": 1}])
def test_fetch_all_rejects_a_non_integer_total(
    bad_total: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    """`total` is only READ at the page cap on the un-fixed code, so the cap is
    what this fixture drives - that is where the TypeError was measured.

    Routed through the model, `total` is validated on EVERY page, so the fix
    moves the failure forward to request 1. The assertion is deliberately about
    the exception, not the request count: both are correct outcomes and pinning
    the count here would pin the un-fixed code's laziness as a contract.

    "many" is not simply "a string": pydantic coerces a NUMERIC string, so
    `total: "5"` stays acceptable. It is the non-numeric string that must fail.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        return httpx.Response(
            200,
            json={
                "items": [_job_response(id=f"j{len(calls)}")],
                "next_cursor": f"CUR-{len(calls)}",
                "total": bad_total,
            },
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()


def test_fetch_all_rejects_a_null_items() -> None:
    """`items: null` is a live defect that PRE-DATES this slice - the line it
    crashed on is the same subscript that was there before - so it is fixed here
    incidentally, by the same routing, rather than as a regression.

    `Page.items` is required and undefaulted, so null is a ValidationError and
    not an empty page: coercing null to [] would invent a drained-looking page
    out of a body the server never sent.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200, json={"items": None, "next_cursor": "", "total": 0}
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()


def test_fetch_all_rejects_a_missing_items_key() -> None:
    """The absent-key sibling of the null case, and it still isolates `items`:
    the fixture carries `next_cursor` and `total`, so `items` is the only key
    missing and the only thing that can produce the raise.

    All THREE of Page's fields are required and undefaulted. `next_cursor` and
    `total` carried defaults until the strict-envelope slice; their absent-key
    cases are test_fetch_all_rejects_a_missing_next_cursor and
    test_fetch_all_rejects_a_missing_total.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"next_cursor": "", "total": 0})

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()


# ─── envelope ABSENCE ────────────────────────────────────────────────────────
#
# The section above varies the TYPE of an envelope field. These vary its
# PRESENCE, which is the sharper case: a wrong type crashes somewhere loud,
# while an absent key used to decode into a value that looked legitimate.
# `next_cursor`'s default was the empty string and the empty string is
# _fetch_all's drained signal, so a dropped key reported the list finished -
# `list_jobs()` returned page 1 and raised nothing, and no caller could tell a
# 200-row prefix from a complete 200-row list.
#
# _page_response cannot express these bodies: it is typed `next_cursor: str`
# with `total` defaulting to len(items), so it always emits all three keys.
# That is why it survived this change untouched, and why these are hand-written.
#
# Each fixture omits EXACTLY ONE of the two keys and carries the other. A single
# fixture omitting both would make the two field declarations indistinguishable
# - restoring either default alone would leave it green - and the pair would
# look covered while pinning nothing.


def test_fetch_all_rejects_a_missing_next_cursor() -> None:
    """A body with no `next_cursor` key must RAISE, not read as drained.

    `total` is PRESENT and non-zero here on purpose. It is what separates this
    test from test_fetch_all_rejects_a_missing_total: restoring
    `total: int = 0` alone must leave this one GREEN.

    A request-count assertion would not be evidence here and is deliberately
    absent: the correct code raises on request 1 and the old code stopped after
    request 1, so `len(calls) == 1` holds under both. The 500 terminator stays
    per this file's convention - it costs nothing, and it turns a mutant that
    keeps walking into a failure instead of a hang, since the project has no
    pytest-timeout.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 1:
            return httpx.Response(500, json={"error": "past the decode"})
        return httpx.Response(
            200, json={"items": [_job_response(id="j1")], "total": 99}
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()


def test_fetch_all_rejects_a_missing_total() -> None:
    """The other half. `next_cursor` is PRESENT and DRAINED here, so a walk that
    ignored the missing `total` would terminate normally with one row and
    report success - which is exactly what it did before this slice.

    Restoring `next_cursor: str = ""` alone must leave this one GREEN. That is
    what makes the two field declarations separately pinned.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 1:
            return httpx.Response(500, json={"error": "past the decode"})
        return httpx.Response(
            200, json={"items": [_job_response(id="j1")], "next_cursor": ""}
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()


def test_get_page_rejects_a_missing_next_cursor() -> None:
    """The same body through `list_jobs_page` - one request, no walk at all.

    `_get_page` and `_fetch_all` share the model today, so this looks
    redundant, and it is the six one-page methods' only pin. Nothing structural
    forbids a lenient path being added back to `_get_page` - a `.get()` at the
    call site is exactly how this defect was originally written - and a
    `list_jobs_page` caller reads `page.next_cursor` to decide whether to ask
    for more. Wired, not just the helper.

    `len(calls) == 1` documents that the refusal happens after the request, not
    instead of it. It does not discriminate the fix: the un-fixed code also made
    exactly one request and returned a Page.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        return httpx.Response(
            200, json={"items": [_job_response(id="j1")], "total": 99}
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs_page()

    assert len(calls) == 1
