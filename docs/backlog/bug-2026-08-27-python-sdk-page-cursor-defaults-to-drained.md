---
title: Python SDK reads next_cursor with a default, so a renamed or dropped key silently returns page 1
type: bug
status: open
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
