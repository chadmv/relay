---
title: Python SDK reads next_cursor with a default, so a renamed or dropped key silently returns page 1
type: bug
status: closed
closed: 2026-08-29
resolution: fixed
created: 2026-08-27
priority: high
source: Spec D7 while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# Python SDK reads `next_cursor` with a default, so a dropped key reads as "drained"

## Summary
`_get_page` and `_fetch_all` both do `body.get("next_cursor", "")`. An empty cursor means drained,
so a server that renames or drops that key makes `_fetch_all` return page 1 and report success.
This is the exact defect shape of the envelope drift that closed
[[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]], one layer over, on six routes.

## Repro / Symptoms
Measured against a mock omitting `next_cursor` from a multi-page response:

```
B: ['j1'] -> silently 'complete'
C: RAISED builtins.TypeError  | is RelayError: False
D: RAISED KeyError 'items'    | is RelayError: False
```

Case B is the silent one and the dangerous one. C and D show the same path failing with raw
built-in exceptions rather than anything under `RelayError`.

## Context
It is not a live drift TODAY - the key is correct. It is a latent one, and the SDK just spent a
whole slice on what happens when a latent one becomes live while a default hides it.

The fix already exists in the same file: `LogPage` declares `next_seq` and `total` REQUIRED with no
default, for exactly this reason, and `task_logs_page` routes the whole body through the model
rather than hand-picking `body["items"]`. `Page` should do the same.

Declined during that slice because making it strict changes the failure behaviour of twelve methods
across six endpoints and needs its own RED.

## Acceptance / Done When
- `Page` validates the whole envelope through the model; a missing `next_cursor` or `total` raises
  rather than defaulting.
- Reverting to `body.get(...)` while keeping the new fixtures turns a test RED.
- The `TypeError`/`KeyError` escapes are resolved or explicitly deferred to the RelayError item.

## Related
- `python/src/relay/client.py` `_get_page`, `_fetch_all`
- `python/src/relay/models.py` `Page`, `LogPage`
- [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]]

## Resolution
Fixed. `Page.next_cursor` and `Page.total` are now REQUIRED and undefaulted, matching `LogPage`.
An envelope omitting either raises `pydantic.ValidationError` instead of decoding into a page that
reports the list drained.

Three things this item got wrong, corrected at the spec gate rather than carried:

- Its Summary names `body.get("next_cursor", "")` in `_get_page`/`_fetch_all`. That statement did
  not exist at the time of closing - #161 deleted it, and the defect survived one layer down as the
  pydantic field default. The remedy was two field declarations in `models.py`, not a `client.py`
  change.
- Acceptance criterion 3 ("the `TypeError`/`KeyError` escapes are resolved or explicitly deferred")
  was already SATISFIED by #161, not open. Both escapes are pinned by named tests.
- The criterion "reverting to `body.get(...)` turns a test RED" was restated against the real
  design: reverting `next_cursor: str` or `total: int` to its default each turns a DIFFERENT named
  test red, and the fixtures omit exactly one key apiece so the two declarations stay separately
  pinned.

Scope decisions made and recorded: `pydantic.ValidationError` escapes unwrapped rather than being
wrapped locally (the remedy belongs to the single `_read_json` chokepoint in
[[bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy]], whose acceptance criteria
this slice extended); the Go peer is filed as
[[bug-2026-08-29-go-pageenvelope-reads-a-dropped-next-cursor-as-drained]] rather than fixed here,
since `encoding/json` has no required-field mechanism.

Phase 4 found that the premise the whole slice rests on - `internal/api`'s `page[T]` carrying no
`omitempty` - was pinned by NOTHING. Adding the tag left all 21 other Go packages green, and only
the opt-in Python integration lane caught it. `internal/api/pagination_test.go` now carries the
guard on the side that owns the tag, and it kills all three single-tag mutations as well as the
all-three one. Version `0.2.1 -> 0.3.0`: `Page` is exported, so removing the defaults breaks
downstream constructors.
