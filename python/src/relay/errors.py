from __future__ import annotations

from typing import Any, Optional

import httpx


class RelayError(Exception):
    """Base for every error raised by the SDK."""


class ValidationError(RelayError):
    """Either local Pydantic validation or a 400 from the server."""

    def __init__(self, message: str, response: Optional[httpx.Response] = None) -> None:
        super().__init__(message)
        self.response = response


class AuthError(RelayError):
    """401 or 403 from the server, or missing credentials locally."""

    def __init__(self, message: str, response: Optional[httpx.Response] = None) -> None:
        super().__init__(message)
        self.response = response


class NotFound(RelayError):
    """404 from the server."""

    def __init__(self, message: str, response: Optional[httpx.Response] = None) -> None:
        super().__init__(message)
        self.response = response


class Conflict(RelayError):
    """409 from the server (e.g. cancelling a terminal job)."""

    def __init__(self, message: str, response: Optional[httpx.Response] = None) -> None:
        super().__init__(message)
        self.response = response


class ServerError(RelayError):
    """5xx from the server."""

    def __init__(self, message: str, response: Optional[httpx.Response] = None) -> None:
        super().__init__(message)
        self.response = response


class HTTPError(RelayError):
    """Any other unexpected HTTP status."""

    def __init__(self, message: str, response: Optional[httpx.Response] = None) -> None:
        super().__init__(message)
        self.response = response


class TimeoutError(RelayError):
    """Wall-clock timeout from wait()/follow_job()."""


def raise_for_response(response: httpx.Response) -> None:
    """Translate a non-2xx response into the appropriate RelayError subclass.

    No-op for 2xx. The error message comes from the server's JSON {"error": ...}
    body when present, falling back to the raw text.
    """
    if response.is_success:
        return

    message = _extract_message(response)
    status = response.status_code
    if status == 400:
        raise ValidationError(message, response)
    if status in (401, 403):
        raise AuthError(message, response)
    if status == 404:
        raise NotFound(message, response)
    if status == 409:
        raise Conflict(message, response)
    if 500 <= status < 600:
        raise ServerError(message, response)
    raise HTTPError(f"HTTP {status}: {message}", response)


def _extract_message(response: httpx.Response) -> str:
    try:
        payload: Any = response.json()
    except ValueError:
        return response.text or f"HTTP {response.status_code}"
    if isinstance(payload, dict):
        err = payload.get("error")
        if isinstance(err, str):
            return err
    return response.text or f"HTTP {response.status_code}"
