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
    AgentEnrollment,
    Event,
    EventType,
    Job,
    JobStatus,
    LogRecord,
    OverlapPolicy,
    Page,
    Priority,
    Reservation,
    ScheduledJob,
    Source,
    Sync,
    Task,
    TaskStatus,
    User,
    Worker,
)

__all__ = [
    "AgentEnrollment",
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
    "Page",
    "Priority",
    "RelayError",
    "Reservation",
    "ScheduledJob",
    "ServerError",
    "Source",
    "Sync",
    "Task",
    "TaskStatus",
    "TimeoutError",
    "User",
    "ValidationError",
    "Worker",
    "__version__",
]
