from __future__ import annotations

import json
from collections.abc import Iterable, Iterator

from .errors import ValidationError
from .models import Event


def parse_sse_stream(lines: Iterable[str]) -> Iterator[Event]:
    """Yield :class:`Event` objects from an iterable of decoded SSE lines.

    Implements the subset of the SSE format the relay server emits: ``event:``
    sets the event type, ``data:`` lines are concatenated with newlines, and
    a blank line dispatches the buffered event. The relay server splits
    embedded newlines in ``Event.Data`` across multiple ``data:`` lines, so
    this parser rejoins them before JSON-decoding.
    """
    event_type = ""
    data_lines: list[str] = []

    for raw in lines:
        # Strip a single trailing newline; SSE lines are delimited by LF/CRLF.
        line = raw.rstrip("\r\n")

        if line == "":
            if event_type or data_lines:
                yield _build_event(event_type, data_lines)
            event_type = ""
            data_lines = []
            continue

        if line.startswith(":"):
            # SSE comment.
            continue

        field, _, value = line.partition(":")
        # Per the SSE spec a single leading space after the colon is ignored.
        if value.startswith(" "):
            value = value[1:]

        if field == "event":
            event_type = value
        elif field == "data":
            data_lines.append(value)
        # Other fields (id, retry) are ignored — relay does not use them.

    if event_type or data_lines:
        yield _build_event(event_type, data_lines)


def _build_event(event_type: str, data_lines: list[str]) -> Event:
    payload = "\n".join(data_lines)
    if not payload:
        data: dict[str, object] = {}
    else:
        try:
            decoded = json.loads(payload)
        except ValueError as e:
            raise ValidationError(f"invalid SSE data JSON: {e}") from e
        if not isinstance(decoded, dict):
            raise ValidationError(
                f"SSE data was not a JSON object: {type(decoded).__name__}"
            )
        data = decoded
    return Event(type=event_type, data=data)
