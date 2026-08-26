from __future__ import annotations

from typing import TYPE_CHECKING, Any, Optional

import httpx

if TYPE_CHECKING:
    from .models import LogRecord


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


class ProtocolError(RelayError):
    """The server answered with well-formed HTTP that is not a usable relay
    response: a page that advertises more rows but carries none, a cursor that
    does not advance, or a log that never reports itself drained.

    Carries no ``.response``, unlike the status-derived errors above: it is
    raised from a walk across several responses, so there is no single
    ``httpx.Response`` that explains it.

    ``records`` is what the abandoned walk had already collected, and it is
    the point of the raise rather than a debugging extra. printTaskLogs
    (internal/cli/logs.go), which :meth:`relay.Client.task_logs` ports, has
    already written every row to its output by the time it returns this error:
    the error is a completeness caveat on rows the operator can already see.
    A Python method that returns a list cannot deliver rows and raise, so it
    delivers them HERE::

        try:
            logs = client.task_logs(task_id)
        except relay.ProtocolError as e:
            logs = e.records   # incomplete, and e says why

    It is ``[]`` when nothing was collected, never ``None``, so a caller need
    not test it before iterating.
    """

    def __init__(
        self, message: str, *, records: Optional[list["LogRecord"]] = None
    ) -> None:
        super().__init__(message)
        self.records: list[LogRecord] = list(records) if records else []


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
