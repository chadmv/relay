from __future__ import annotations

import httpx
import pytest

from relay import (
    AuthError,
    Conflict,
    HTTPError,
    NotFound,
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
