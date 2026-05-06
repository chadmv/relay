from __future__ import annotations

import pytest

from relay import ValidationError
from relay.events import parse_sse_stream


def _from_text(text: str) -> list:
    return list(parse_sse_stream(text.splitlines()))


def test_single_event_parsed() -> None:
    events = _from_text(
        "event: job\n"
        'data: {"status":"running"}\n'
        "\n"
    )
    assert len(events) == 1
    assert events[0].type == "job"
    assert events[0].data == {"status": "running"}


def test_multiple_events_parsed() -> None:
    events = _from_text(
        "event: job\n"
        'data: {"status":"running"}\n'
        "\n"
        "event: task\n"
        'data: {"status":"done","id":"abc"}\n'
        "\n"
    )
    assert [e.type for e in events] == ["job", "task"]
    assert events[1].data["id"] == "abc"


def test_multi_line_data_rejoined() -> None:
    """Server splits embedded newlines across multiple data: lines."""
    events = _from_text(
        "event: job\n"
        "data: {\n"
        'data:   "status": "running"\n'
        "data: }\n"
        "\n"
    )
    assert events[0].data == {"status": "running"}


def test_comment_lines_ignored() -> None:
    events = _from_text(
        ": heartbeat\n"
        "event: job\n"
        'data: {"status":"running"}\n'
        "\n"
    )
    assert len(events) == 1


def test_trailing_event_without_blank_line_emitted() -> None:
    events = _from_text(
        "event: job\n"
        'data: {"status":"done"}\n'
    )
    assert len(events) == 1


def test_invalid_json_raises_validation_error() -> None:
    with pytest.raises(ValidationError):
        _from_text("event: job\ndata: not json\n\n")


def test_non_object_data_raises() -> None:
    with pytest.raises(ValidationError):
        _from_text('event: job\ndata: 42\n\n')


def test_leading_space_after_colon_stripped() -> None:
    """Per SSE spec a single leading space is part of the framing."""
    events = _from_text('event: job\ndata: {"x":1}\n\n')
    assert events[0].data == {"x": 1}
