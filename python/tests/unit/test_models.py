from __future__ import annotations

import pytest
from pydantic import ValidationError as PydanticValidationError

from relay import (
    Job,
    JobStatus,
    Priority,
    Source,
    Sync,
    TaskStatus,
    ValidationError,
)

# ─── Sync / Source ────────────────────────────────────────────────────────────


def test_sync_requires_double_slash_path() -> None:
    with pytest.raises(PydanticValidationError):
        Sync(path="depot/main", rev="#head")


@pytest.mark.parametrize(
    "rev",
    ["#head", "#42", "@1234", "@my-label", "@release_1.2"],
)
def test_sync_accepts_valid_revs(rev: str) -> None:
    Sync(path="//depot/main/...", rev=rev)


@pytest.mark.parametrize("rev", ["head", "1234", "@", "#-1", "@@123"])
def test_sync_rejects_invalid_revs(rev: str) -> None:
    with pytest.raises(PydanticValidationError):
        Sync(path="//depot/main/...", rev=rev)


def test_source_requires_perforce_type() -> None:
    with pytest.raises(PydanticValidationError):
        Source(type="git", stream="//x", sync=[Sync(path="//x", rev="#head")])


def test_source_stream_must_start_with_slashes() -> None:
    with pytest.raises(PydanticValidationError):
        Source(stream="depot/main", sync=[Sync(path="//depot/main", rev="#head")])


def test_source_sync_must_be_under_stream() -> None:
    with pytest.raises(PydanticValidationError):
        Source(
            stream="//depot/main",
            sync=[Sync(path="//depot/other/...", rev="#head")],
        )


def test_source_accepts_stream_path_and_wildcard() -> None:
    Source(
        stream="//depot/main",
        sync=[
            Sync(path="//depot/main", rev="#head"),
            Sync(path="//depot/main/...", rev="#head"),
            Sync(path="//depot/main/sub", rev="#head"),
        ],
    )


def test_source_unshelves_must_be_positive() -> None:
    with pytest.raises(PydanticValidationError):
        Source(
            stream="//depot/main",
            sync=[Sync(path="//depot/main/...", rev="#head")],
            unshelves=[0],
        )


def test_source_client_template_charset() -> None:
    with pytest.raises(PydanticValidationError):
        Source(
            stream="//depot/main",
            sync=[Sync(path="//depot/main/...", rev="#head")],
            client_template="bad name with spaces",
        )


# ─── Task / Job ───────────────────────────────────────────────────────────────


def test_add_task_with_single_command_normalizes_to_commands() -> None:
    job = Job(name="j")
    task = job.add_task("t", command=["echo", "hi"])
    assert task.commands == [["echo", "hi"]]


def test_add_task_rejects_both_command_and_commands() -> None:
    job = Job(name="j")
    with pytest.raises(ValueError):
        job.add_task("t", commands=[["echo", "hi"]], command=["echo", "hi"])


def test_add_task_requires_at_least_one_command() -> None:
    job = Job(name="j")
    with pytest.raises(ValueError):
        job.add_task("t")


def test_add_task_rejects_empty_argv() -> None:
    job = Job(name="j")
    with pytest.raises(PydanticValidationError):
        job.add_task("t", commands=[[]])


def test_depends_on_accepts_task_or_string() -> None:
    job = Job(name="j")
    a = job.add_task("a", commands=[["echo", "a"]])
    b = job.add_task("b", commands=[["echo", "b"]], depends_on=[a])
    c = job.add_task("c", commands=[["echo", "c"]], depends_on=["a", b])
    assert b.depends_on == ["a"]
    assert c.depends_on == ["a", "b"]


def test_depends_on_rejects_other_types() -> None:
    job = Job(name="j")
    job.add_task("a", commands=[["echo", "a"]])
    with pytest.raises(PydanticValidationError):
        job.add_task("b", commands=[["echo", "b"]], depends_on=[123])  # type: ignore[list-item]


def test_validate_spec_requires_at_least_one_task() -> None:
    with pytest.raises(ValidationError, match="at least one task"):
        Job(name="j").validate_spec()


def test_validate_spec_rejects_duplicate_task_names() -> None:
    job = Job(name="j")
    job.add_task("t", commands=[["echo", "1"]])
    job.add_task("t", commands=[["echo", "2"]])
    with pytest.raises(ValidationError, match="duplicate task name"):
        job.validate_spec()


def test_validate_spec_rejects_unknown_depends_on() -> None:
    job = Job(name="j")
    job.add_task("a", commands=[["echo", "a"]])
    job.add_task("b", commands=[["echo", "b"]], depends_on=["missing"])
    with pytest.raises(ValidationError, match="unknown depends_on"):
        job.validate_spec()


def test_validate_spec_rejects_self_dependency() -> None:
    job = Job(name="j")
    job.add_task("a", commands=[["echo", "a"]], depends_on=["a"])
    with pytest.raises(ValidationError, match="cannot depend on itself"):
        job.validate_spec()


def test_to_spec_dict_serializes_minimal_job() -> None:
    job = Job(name="j", priority=Priority.HIGH, labels={"env": "prod"})
    job.add_task("t", commands=[["echo", "hi"]])
    spec = job.to_spec_dict()
    assert spec == {
        "name": "j",
        "priority": "high",
        "labels": {"env": "prod"},
        "tasks": [
            {
                "name": "t",
                "commands": [["echo", "hi"]],
                "env": {},
                "requires": {},
                "timeout_seconds": None,
                "retries": 0,
                "depends_on": [],
            }
        ],
    }


def test_to_spec_dict_includes_source_when_set() -> None:
    job = Job(name="j")
    job.add_task(
        "t",
        commands=[["echo", "hi"]],
        source=Source(
            stream="//depot/main",
            sync=[Sync(path="//depot/main/...", rev="#head")],
            unshelves=[42],
        ),
    )
    spec = job.to_spec_dict()
    src = spec["tasks"][0]["source"]
    assert src["type"] == "perforce"
    assert src["stream"] == "//depot/main"
    assert src["unshelves"] == [42]
    # workspace_exclusive is False by default; with exclude_none=True it stays in
    # because False is not None. That's fine — server accepts it.
    assert "client_template" not in src  # None is excluded


def test_response_status_accepts_unknown_string() -> None:
    """Response status fields are typed as str so future statuses parse."""
    job = Job.model_validate({"name": "j", "priority": "normal", "status": "weird-future-status"})
    assert job.status == "weird-future-status"


def test_status_enum_compares_to_bare_string() -> None:
    """The (str, Enum) mixin means enum values equal their wire strings."""
    assert JobStatus.DONE == "done"
    assert TaskStatus.TIMED_OUT == "timed_out"


def test_priority_enum_input_accepts_string() -> None:
    job = Job(name="j", priority="high")  # type: ignore[arg-type]
    assert job.priority is Priority.HIGH


def test_invalid_priority_rejected() -> None:
    with pytest.raises(PydanticValidationError):
        Job(name="j", priority="wat")  # type: ignore[arg-type]


def test_full_response_round_trip() -> None:
    payload = {
        "id": "11111111-1111-1111-1111-111111111111",
        "name": "j",
        "priority": "normal",
        "status": "running",
        "submitted_by": "22222222-2222-2222-2222-222222222222",
        "submitted_by_email": "u@example.com",
        "labels": {},
        "tasks": [
            {
                "id": "33333333-3333-3333-3333-333333333333",
                "name": "t",
                "status": "running",
                "commands": [["echo", "hi"]],
                "env": {},
                "requires": {},
                "timeout_seconds": None,
                "retries": 0,
                "retry_count": 0,
                "worker_id": "44444444-4444-4444-4444-444444444444",
            }
        ],
        "created_at": "2026-05-06T12:00:00Z",
        "updated_at": "2026-05-06T12:00:00Z",
    }
    job = Job.model_validate(payload)
    assert job.id == "11111111-1111-1111-1111-111111111111"
    assert job.tasks[0].name == "t"
    assert job.tasks[0].worker_id == "44444444-4444-4444-4444-444444444444"
    assert job.status == JobStatus.RUNNING
