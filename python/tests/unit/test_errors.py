from __future__ import annotations

import httpx
import pytest

from relay import (
    AuthError,
    Conflict,
    HTTPError,
    NotFound,
    ProtocolError,
    RelayError,
    ServerError,
    ValidationError,
)
from relay.errors import raise_for_response


def _response(status: int, body: object = None) -> httpx.Response:
    request = httpx.Request("GET", "http://example/x")
    if body is None:
        return httpx.Response(status, request=request)
    return httpx.Response(status, request=request, json=body)


def test_2xx_is_noop() -> None:
    raise_for_response(_response(200, {"ok": True}))
    raise_for_response(_response(204))


@pytest.mark.parametrize(
    ("status", "exc"),
    [
        (400, ValidationError),
        (401, AuthError),
        (403, AuthError),
        (404, NotFound),
        (409, Conflict),
        (500, ServerError),
        (502, ServerError),
        (418, HTTPError),
    ],
)
def test_status_maps_to_subclass(status: int, exc: type[Exception]) -> None:
    with pytest.raises(exc):
        raise_for_response(_response(status, {"error": "boom"}))


def test_message_extracted_from_error_body() -> None:
    with pytest.raises(ValidationError, match="bad spec"):
        raise_for_response(_response(400, {"error": "bad spec"}))


def test_message_falls_back_to_text_when_body_not_json() -> None:
    request = httpx.Request("GET", "http://example/x")
    response = httpx.Response(500, request=request, content=b"raw error text")
    with pytest.raises(ServerError, match="raw error text"):
        raise_for_response(response)


def test_response_attached_to_exception() -> None:
    response = _response(404, {"error": "missing"})
    with pytest.raises(NotFound) as exc_info:
        raise_for_response(response)
    assert exc_info.value.response is response


def test_protocol_error_is_a_relay_error() -> None:
    """The three paging stops need a class callers can catch. ServerError is
    wrong (the status was 200) and RelayError itself is untypeable by a caller
    who wants to distinguish this from everything else.
    """
    assert issubclass(ProtocolError, RelayError)
    err = ProtocolError("server cursor did not advance (next_seq 7 after since_seq 7)")
    assert "did not advance" in str(err)


def test_protocol_error_records_is_a_snapshot() -> None:
    """`ProtocolError.__init__` copies with `list(records)`, and the copy was
    unpinned: replacing it with a bare `records` left all 132 tests green,
    because at all four raise sites inside task_logs the list passed in is a
    dying local that nothing else can reach.

    The copy is kept and stated as a guarantee rather than dropped, because
    `ProtocolError` is public and a caller may construct or re-raise one with a
    list of their own. `.records` is then a snapshot: it does not alias, and it
    does not change under the holder when the source list moves on. Its cost is
    one extra pointer array, which on the 2,000,000-row walk the page cap
    bounds is roughly 16 MB against the gigabyte-plus of LogRecords those
    pointers refer to.
    """
    from relay.models import LogRecord

    at = "2026-08-25T00:00:00Z"
    source = [LogRecord(seq=1, stream="stdout", content="a\n", created_at=at)]
    err = ProtocolError("cursor did not advance", records=source)

    source.append(LogRecord(seq=2, stream="stdout", content="b\n", created_at=at))
    source[0] = LogRecord(seq=99, stream="stdout", content="clobbered\n", created_at=at)

    assert err.records is not source
    assert [r.seq for r in err.records] == [1]
