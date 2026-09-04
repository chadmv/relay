from __future__ import annotations

import re
from collections.abc import Callable
from datetime import datetime
from enum import Enum
from typing import Annotated, Any, Generic, Optional, TypeVar, Union

from pydantic import (
    BaseModel,
    BeforeValidator,
    ConfigDict,
    Field,
    field_validator,
    model_validator,
)

# ─── null-valued jsonb fields ─────────────────────────────────────────────────


def _empty_on_null(empty: Callable[[], Any]) -> BeforeValidator:
    """Coerce a wire ``null`` to the field's empty value, leaving absence alone.

    ``Field(default_factory=dict)`` covers a MISSING key. It does nothing for a
    key that is present and ``null``, which is a different wire shape and one
    the server legitimately sends for some jsonb fields (``Job.labels`` and
    ``Reservation.selector`` arrive as ``null`` today); the SDK must not raise
    on a document the server legitimately produces. The remaining annotated
    fields have no live null path today and are insurance against a server
    change.

    A BEFORE validator, so the declared type stays ``dict``/``list`` rather
    than becoming Optional: a caller never has to test these for None, which
    is the whole point of the empty default they already carried.
    """

    def _coerce(value: Any) -> Any:
        return empty() if value is None else value

    return BeforeValidator(_coerce)


_NullIsEmptyDict = _empty_on_null(dict)
_NullIsEmptyList = _empty_on_null(list)


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
    """Constants for comparing against Task.status, not a census of what the server stores.

    Task.status is Optional[str] precisely so an unknown value parses, so a member
    here is a convenience for callers writing comparisons and carries no claim that
    the server can produce it.
    """

    PENDING = "pending"
    QUEUED = "queued"
    BLOCKED = "blocked"
    DISPATCHED = "dispatched"
    PREPARING = "preparing"
    RUNNING = "running"
    DONE = "done"
    FAILED = "failed"
    CANCELLED = "cancelled"
    TIMED_OUT = "timed_out"


class OverlapPolicy(str, Enum):
    SKIP = "skip"
    ALLOW = "allow"


class EventType(str, Enum):
    """The event types the server publishes on GET /v1/events.

    ``Event.type`` is a plain ``str`` so an unknown future type still parses;
    this enum is the vocabulary the server emits TODAY, for comparison and
    autocomplete.

    ``DROPPED`` is not a resource event. The server writes it directly when
    the broker drops a subscriber for falling behind, and its meaning is "you
    missed frames": anything published in the gap is gone, so re-read the job
    with :meth:`relay.Client.get_job` and resume each task's log with
    ``task_logs_page(task_id, since_seq=<last seq seen>)``. Not ``task_logs``,
    which takes no ``since_seq``.
    """

    JOB = "job"
    TASK = "task"
    WORKER = "worker"
    TASK_LOG = "task_log"
    DROPPED = "dropped"


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
    commands: Annotated[list[list[str]], _NullIsEmptyList] = Field(default_factory=list)
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
    labels: Annotated[dict[str, str], _NullIsEmptyDict] = Field(default_factory=dict)
    tasks: list[Task] = Field(default_factory=list)

    # Response-only fields
    id: Optional[str] = None
    status: Optional[str] = None
    submitted_by: Optional[str] = None
    submitted_by_email: Optional[str] = None
    created_at: Optional[datetime] = None
    updated_at: Optional[datetime] = None

    # List-only enrichment (GET /v1/jobs rows). The server computes these from
    # the job's tasks and its scheduled-job source, and does not populate them
    # on single-job routes. They are DEFAULTED because Job is the authoring
    # model too - Job(name="nightly") must keep working. Do not "make these
    # consistent" with Page/LogPage's required envelope fields: what makes
    # these defaulted is who BUILDS a Job, not what the values mean.
    total_tasks: int = 0
    done_tasks: int = 0
    started_at: Optional[datetime] = None
    finished_at: Optional[datetime] = None
    scheduled_job_id: Optional[str] = None
    scheduled_job_name: Optional[str] = None

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
    """One line of a task's log, as served by GET /v1/tasks/{id}/logs.

    ``seq`` is the row's global ``task_logs.id``. It is REQUIRED and has no
    default for three reasons: it is the only way a caller using
    :meth:`relay.Client.task_logs_page` can correlate a record with a cursor,
    every other field here is required, and a defaulted ``seq: int = 0`` reads
    a missing key as row zero.
    """

    model_config = ConfigDict(extra="ignore")

    seq: int
    stream: str
    content: str
    created_at: datetime


class LogPage(BaseModel):
    """One page of a task's log, forward-only from ``since_seq``.

    ``next_seq`` is 0 when the server reports the log drained; otherwise it is
    the cursor for the next request, passed VERBATIM as ``?since_seq=`` - the
    cursor is exclusive already. Never ``next_seq + 1``, which skips the very
    next row when one task logs alone.

    ``next_seq`` and ``total`` are REQUIRED and undefaulted, and so are
    :class:`Page`'s ``next_cursor`` and ``total``, for the same reason. A
    defaulted ``next_seq: int = 0`` would read a MISSING key as "drained" and
    silently return page 1, because 0 is this walk's end-of-log signal. An
    absent key with a benign default is not a missing value. It is a
    FABRICATED one, and for a cursor the fabricated value is "there is nothing
    more".

    Read that as a rule about PAGE-ENVELOPE fields, not about every default in
    this file: :class:`Job`'s list-only enrichment fields stay defaulted
    because ``Job`` is the model a caller CONSTRUCTS, and container fields
    such as ``Worker.labels`` stay defaulted because an empty dict is the
    honest reading of an absent map, with no control flow derived from it. A
    cursor is neither - it is the loop's stop condition, which is why it gets
    no default at all.
    """

    model_config = ConfigDict(extra="ignore")

    items: list[LogRecord]
    next_seq: int
    total: int


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
    job_spec: Annotated[dict[str, Any], _NullIsEmptyDict]
    overlap_policy: str
    enabled: bool
    next_run_at: datetime
    last_run_at: Optional[datetime] = None
    last_job_id: Optional[str] = None
    # Why the scheduler last failed to produce a job from this schedule, and
    # when. ABSENT MEANS HEALTHY: the server omits both keys entirely and
    # never sends "" or null.
    #
    # A CONSUMER'S HEALTHY TEST IS `not sj.last_error`. `is None` answers the
    # narrower question of whether the SERVER said anything at all, which is
    # what test_scheduled_job_failure_fields_are_none_when_the_server_
    # omits_them pins; it is not the question an application is asking.
    #
    # The two only differ on "", which this SDK deliberately does not coerce to
    # None (see test_scheduled_job_empty_last_error_is_not_coerced_to_none):
    # absent, empty and present are three states and collapsing two of them is
    # the exact defect these fields exist to report, so the SDK reports what it
    # received and leaves the partition to the caller.
    #
    # last_error is derived from the schedule's stored configuration - its
    # job_spec, or its cron_expr and timezone for a "parse cron: ..." failure -
    # and is OPERATOR-SUPPLIED: it MAY quote prose the schedule's owner chose,
    # such as a task name interpolated verbatim. Some messages are fixed server
    # text instead; treat the whole string as untrusted either way, because a
    # client cannot tell the two apart without string-matching the server's own
    # messages. It is sanitized and truncated to 1 KB server-side;
    # run_scheduled_job_now() returns the untruncated message.
    last_error: Optional[str] = None
    last_error_at: Optional[datetime] = None
    created_at: datetime
    updated_at: datetime


# ─── Pagination & resource models ──────────────────────────────────────────────

T = TypeVar("T")


class Page(BaseModel, Generic[T]):
    """One page of a paginated list response.

    ``next_cursor`` is the empty string on the last page; pass it back as
    ``cursor=`` to fetch the next page. ``total`` is the server's count of
    all matching rows, not just this page.

    All three fields are REQUIRED and undefaulted, and ``next_cursor`` is the
    one that matters. The empty string is this SDK's drained signal, so a
    defaulted ``next_cursor: str = ""`` would read an ABSENT key as "the list
    ended": :meth:`relay.Client.list_jobs` would return page 1, raise nothing,
    and no caller could tell a 200-row prefix from a complete 200-row list.
    ``total`` is the milder half - not a control-flow signal, but a number the
    ``*_page`` methods hand back for a caller to render, where a silent 0 is a
    wrong number rather than a missing one.

    Requiring them costs nothing against a correct server. That property is
    pinned on the Go side: TestPageEnvelope_AllThreeKeysArePresentOnAZeroValuePage
    in internal/api/pagination_test.go marshals a zero-value ``page[T]`` and
    asserts all three keys are present.

    Not every endpoint uses this envelope: ``GET /v1/jobs/{id}/tasks`` returns
    a bare JSON array, which is why :meth:`relay.Client.get_tasks` is outside
    everything argued here - see the note in python/README.md.

    ``extra="ignore"`` stays. Strictness here is about the ABSENCE of a
    contract field, not the presence of an unknown one - opposite directions.
    A model that rejected new envelope fields could not talk to a newer server.
    """

    model_config = ConfigDict(extra="ignore")

    items: list[T]
    next_cursor: str
    total: int


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
    labels: Annotated[dict[str, Any], _NullIsEmptyDict] = Field(default_factory=dict)
    status: str
    last_seen_at: Optional[datetime] = None
    last_sample_at: Optional[datetime] = None
    disabled_at: Optional[datetime] = None
    revoked_at: Optional[datetime] = None


class Reservation(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: str
    name: str
    selector: Annotated[dict[str, Any], _NullIsEmptyDict] = Field(default_factory=dict)
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
