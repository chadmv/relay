"""relay-sdk: Python client for the relay job-submission API."""

from __future__ import annotations

from ._version import __version__
from .client import Client
from .errors import (
    AuthError,
    Conflict,
    HTTPError,
    NotFound,
    RelayError,
    ServerError,
    TimeoutError,
    ValidationError,
)
from .models import (
    Event,
    EventType,
    Job,
    JobStatus,
    LogRecord,
    OverlapPolicy,
    Priority,
    ScheduledJob,
    Source,
    Sync,
    Task,
    TaskStatus,
)

__all__ = [
    "AuthError",
    "Client",
    "Conflict",
    "Event",
    "EventType",
    "HTTPError",
    "Job",
    "JobStatus",
    "LogRecord",
    "NotFound",
    "OverlapPolicy",
    "Priority",
    "RelayError",
    "ScheduledJob",
    "ServerError",
    "Source",
    "Sync",
    "Task",
    "TaskStatus",
    "TimeoutError",
    "ValidationError",
    "__version__",
]
