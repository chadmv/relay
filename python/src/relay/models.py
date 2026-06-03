from __future__ import annotations

import re
from datetime import datetime
from enum import Enum
from typing import Any, Generic, Optional, TypeVar, Union

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


class Priority(str, Enum):
    LOW = "low"
    NORMAL = "normal"
    HIGH = "high"


class JobStatus(str, Enum):
    """Constants for the values the server publishes on Job.status.

    Job.status is typed as str on response models so unknown future values
    parse cleanly; this enum exists for IDE autocomplete and comparison.
    """

    PENDING = "pending"
    RUNNING = "running"
    DONE = "done"
    FAILED = "failed"
    CANCELLED = "cancelled"


class TaskStatus(str, Enum):
    """Constants for the values the server publishes on Task.status."""

    PENDING = "pending"
    QUEUED = "queued"
    BLOCKED = "blocked"
    DISPATCHED = "dispatched"
    RUNNING = "running"
    DONE = "done"
    FAILED = "failed"
    CANCELLED = "cancelled"
    TIMED_OUT = "timed_out"


class OverlapPolicy(str, Enum):
    SKIP = "skip"
    ALLOW = "allow"


class EventType(str, Enum):
    JOB = "job"
    TASK = "task"


# ─── Source specs (Perforce) ──────────────────────────────────────────────────

_REV_PATTERNS = (
    re.compile(r"^#head$"),
    re.compile(r"^@\d+$"),
    re.compile(r"^@[A-Za-z0-9._-]+$"),
    re.compile(r"^#\d+$"),
)
_CLIENT_TEMPLATE_RE = re.compile(r"^[A-Za-z0-9_.-]+$")


class Sync(BaseModel):
    """A single depot path + revision to sync."""

    model_config = ConfigDict(extra="forbid")

    path: str
    rev: str

    @field_validator("path")
    @classmethod
    def _path_starts_with_slashes(cls, v: str) -> str:
        if not v.startswith("//"):
            raise ValueError("path must start with //")
        return v

    @field_validator("rev")
    @classmethod
    def _rev_recognized(cls, v: str) -> str:
        if not any(p.match(v) for p in _REV_PATTERNS):
            raise ValueError(f"invalid rev {v!r} (expected #head, #N, @CL, or @label)")
        return v


class Source(BaseModel):
    """Workspace preparation for a task. Currently only Perforce is supported."""

    model_config = ConfigDict(extra="forbid")

    type: str = "perforce"
    stream: str
    sync: list[Sync]
    unshelves: list[int] = Field(default_factory=list)
    workspace_exclusive: bool = False
    client_template: Optional[str] = None

    @field_validator("type")
    @classmethod
    def _type_supported(cls, v: str) -> str:
        if v != "perforce":
            raise ValueError(f"unsupported source type: {v}")
        return v

    @field_validator("stream")
    @classmethod
    def _stream_well_formed(cls, v: str) -> str:
        if not v:
            raise ValueError("stream is required")
        if not v.startswith("//"):
            raise ValueError("stream must start with //")
        return v

    @field_validator("sync")
    @classmethod
    def _at_least_one_sync(cls, v: list[Sync]) -> list[Sync]:
        if not v:
            raise ValueError("sync must have at least one entry")
        return v

    @field_validator("unshelves")
    @classmethod
    def _unshelves_positive(cls, v: list[int]) -> list[int]:
        for i, cl in enumerate(v):
            if cl <= 0:
                raise ValueError(f"unshelves[{i}]: must be positive")
        return v

    @field_validator("client_template")
    @classmethod
    def _client_template_charset(cls, v: Optional[str]) -> Optional[str]:
        if v is not None and not _CLIENT_TEMPLATE_RE.match(v):
            raise ValueError(f"invalid client_template {v!r}")
        return v

    @model_validator(mode="after")
    def _sync_paths_under_stream(self) -> Source:
        for i, e in enumerate(self.sync):
            if (
                e.path != self.stream
                and e.path != self.stream + "/..."
                and not e.path.startswith(self.stream + "/")
            ):
                raise ValueError(f"sync[{i}].path must be under stream {self.stream}")
        return self


# ─── Task ────────────────────────────────────────────────────────────────────


class Task(BaseModel):
    """A unit of work. Used both for authoring (input) and as the response
    model returned by the server. Response-only fields (``id``, ``status``,
    ``retry_count``, ``worker_id``) are optional and unset when authoring.
    """

    model_config = ConfigDict(extra="ignore", populate_by_name=True)

    # Authoring fields
    name: str
    commands: list[list[str]] = Field(default_factory=list)
    env: dict[str, str] = Field(default_factory=dict)
    requires: dict[str, str] = Field(default_factory=dict)
    timeout_seconds: Optional[int] = None
    retries: int = 0
    depends_on: list[str] = Field(default_factory=list)
    source: Optional[Source] = None

    # Response-only fields
    id: Optional[str] = None
    status: Optional[str] = None
    retry_count: Optional[int] = None
    worker_id: Optional[str] = None

    @field_validator("name")
    @classmethod
    def _name_required(cls, v: str) -> str:
        if not v:
            raise ValueError("task name is required")
        return v

    @field_validator("commands")
    @classmethod
    def _commands_argv_nonempty(cls, v: list[list[str]]) -> list[list[str]]:
        for i, argv in enumerate(v):
            if not argv:
                raise ValueError(f"commands[{i}]: argv must not be empty")
        return v

    @field_validator("depends_on", mode="before")
    @classmethod
    def _coerce_depends_on(cls, v: Any) -> Any:
        """Accept Task instances as shorthand for their names."""
        if v is None:
            return []
        if isinstance(v, (list, tuple)):
            out: list[str] = []
            for item in v:
                if isinstance(item, Task):
                    out.append(item.name)
                elif isinstance(item, str):
                    out.append(item)
                else:
                    raise ValueError(
                        f"depends_on entries must be Task or str, got {type(item).__name__}"
                    )
            return out
        return v

    def to_spec_dict(self) -> dict[str, Any]:
        """Serialize only the authoring (server-facing) fields."""
        d: dict[str, Any] = {
            "name": self.name,
            "commands": self.commands,
            "env": self.env,
            "requires": self.requires,
            "timeout_seconds": self.timeout_seconds,
            "retries": self.retries,
            "depends_on": self.depends_on,
        }
        if self.source is not None:
            d["source"] = self.source.model_dump(exclude_none=True)
        return d


# ─── Job ─────────────────────────────────────────────────────────────────────


class Job(BaseModel):
    """A relay job. Same class is used for authoring and as the response
    model. Response-only fields are optional and unset when authoring.

    Authoring example::

        job = Job(name="nightly")
        job.add_task("cook", commands=[["ue4-cook"]])
        client.submit(job)
    """

    model_config = ConfigDict(extra="ignore", populate_by_name=True)

    # Authoring fields
    name: str
    priority: Priority = Priority.NORMAL
    labels: dict[str, str] = Field(default_factory=dict)
    tasks: list[Task] = Field(default_factory=list)

    # Response-only fields
    id: Optional[str] = None
    status: Optional[str] = None
    submitted_by: Optional[str] = None
    submitted_by_email: Optional[str] = None
    created_at: Optional[datetime] = None
    updated_at: Optional[datetime] = None

    @field_validator("name")
    @classmethod
    def _name_required(cls, v: str) -> str:
        if not v:
            raise ValueError("name is required")
        return v

    def add_task(
        self,
        name: str,
        commands: Optional[list[list[str]]] = None,
        *,
        command: Optional[list[str]] = None,
        env: Optional[dict[str, str]] = None,
        requires: Optional[dict[str, str]] = None,
        timeout_seconds: Optional[int] = None,
        retries: int = 0,
        depends_on: Optional[list[Union[str, Task]]] = None,
        source: Optional[Source] = None,
    ) -> Task:
        """Append a task to this job and return it.

        Either ``commands`` (list of argv lists) or ``command`` (single argv)
        must be provided. ``depends_on`` may contain Task instances or names.
        """
        if commands is None and command is None:
            raise ValueError("must provide commands= or command=")
        if commands is not None and command is not None:
            raise ValueError("set either command or commands, not both")
        if commands is None:
            assert command is not None  # type narrowing
            commands = [command]

        deps: list[str] = []
        for d in depends_on or []:
            deps.append(d.name if isinstance(d, Task) else d)

        task = Task(
            name=name,
            commands=commands,
            env=env or {},
            requires=requires or {},
            timeout_seconds=timeout_seconds,
            retries=retries,
            depends_on=deps,
            source=source,
        )
        self.tasks.append(task)
        return task

    def validate_spec(self) -> None:
        """Cross-task validation: at least one task, unique names, every
        ``depends_on`` resolves. Mirrors server-side ValidateJobSpec.

        Raises :class:`relay.ValidationError`. Called automatically by
        :meth:`relay.Client.submit`.
        """
        from .errors import ValidationError

        if not self.tasks:
            raise ValidationError("at least one task is required")
        names: set[str] = set()
        for t in self.tasks:
            if t.name in names:
                raise ValidationError(f"duplicate task name: {t.name}")
            names.add(t.name)
            if not t.commands:
                raise ValidationError(f"task {t.name}: commands is required")
        for t in self.tasks:
            for dep in t.depends_on:
                if dep not in names:
                    raise ValidationError(f"task {t.name}: unknown depends_on: {dep}")
                if dep == t.name:
                    raise ValidationError(f"task {t.name}: cannot depend on itself")

    def to_spec_dict(self) -> dict[str, Any]:
        """Serialize the request body for POST /v1/jobs."""
        return {
            "name": self.name,
            "priority": self.priority.value,
            "labels": self.labels,
            "tasks": [t.to_spec_dict() for t in self.tasks],
        }


# ─── Logs and events ─────────────────────────────────────────────────────────


class LogRecord(BaseModel):
    model_config = ConfigDict(extra="ignore")

    stream: str
    content: str
    created_at: datetime


class Event(BaseModel):
    """An SSE event emitted on /v1/events."""

    model_config = ConfigDict(extra="ignore")

    type: str
    data: dict[str, Any]


# ─── Scheduled jobs ──────────────────────────────────────────────────────────


class ScheduledJob(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    name: str
    owner_id: str
    cron_expr: str
    timezone: str
    job_spec: dict[str, Any]
    overlap_policy: str
    enabled: bool
    next_run_at: datetime
    last_run_at: Optional[datetime] = None
    last_job_id: Optional[str] = None
    created_at: datetime
    updated_at: datetime


# ─── Pagination & resource models ──────────────────────────────────────────────

T = TypeVar("T")


class Page(BaseModel, Generic[T]):
    """One page of a paginated list response.

    ``next_cursor`` is the empty string on the last page; pass it back as
    ``cursor=`` to fetch the next page. ``total`` is the server's count of
    all matching rows, not just this page.
    """

    model_config = ConfigDict(extra="ignore")

    items: list[T]
    next_cursor: str = ""
    total: int = 0


class Worker(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    name: str
    hostname: str
    cpu_cores: int
    ram_gb: int
    gpu_count: int
    gpu_model: str
    os: str
    max_slots: int
    labels: dict[str, Any] = Field(default_factory=dict)
    status: str
    last_seen_at: Optional[datetime] = None
    last_sample_at: Optional[datetime] = None
    disabled_at: Optional[datetime] = None


class Reservation(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    name: str
    selector: dict[str, Any] = Field(default_factory=dict)
    worker_ids: list[str] = Field(default_factory=list)
    user_id: str
    project: Optional[str] = None
    starts_at: Optional[datetime] = None
    ends_at: Optional[datetime] = None
    created_at: datetime


class AgentEnrollment(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    created_at: datetime
    expires_at: datetime
    created_by: str
    hostname_hint: Optional[str] = None


class User(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    email: str
    name: str
    is_admin: bool
    created_at: datetime
    archived_at: Optional[datetime] = None
