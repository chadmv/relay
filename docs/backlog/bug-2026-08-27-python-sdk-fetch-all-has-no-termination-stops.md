---
title: Python SDK _fetch_all walks a server-supplied cursor with no termination stops, so a repeating cursor hangs six methods forever
type: bug
status: open
created: 2026-08-27
priority: high
source: Phase 4 invariants lens while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# Python SDK `_fetch_all` walks a server-supplied cursor with no termination stops

## Summary
`task_logs()` gained three stops and a page cap because a server-supplied cursor driving a client
loop needs them. `_fetch_all` in the same file drives the identical loop on `next_cursor` and has
none: no empty-page stop, no non-advancing-cursor stop, no page cap.

## Repro / Symptoms
Measured against a mock returning `next_cursor: "SAME"` forever:

```
list_jobs HUNG: 2000 requests and counting
```

Unbounded requests AND unbounded growth of `out`. Six public methods: `list_jobs`,
`list_schedules`, `list_workers`, `list_users`, `list_reservations`, `list_agent_enrollments`.

## Context
The reasoning is already written down, in `python/src/relay/client.py`'s `task_logs` loop: "the
provenance of a value says nothing about who controls its content." It applies verbatim next door.

The Go peer `FetchAllPages` (`internal/relayclient/page.go`) has the identical gap, so this is a
cross-language hole and not a Python oversight. Fixing one language and not the other leaves the
CLI exposed.

`task_logs`'s `_MAX_LOG_PAGES` is the shape to copy, including that it is a class attribute so a
test can shrink it.

## Acceptance / Done When
- A repeating or non-advancing `next_cursor` terminates with a `ProtocolError` naming the cursor.
- A page cap bounds the request count, and the message does not blame the server when the
  envelope's own `total` says every row was collected.
- Each stop is pinned by a mutation that kills exactly one test.
- The Go `FetchAllPages` decision is made explicitly - fixed too, or declined in writing.

## Related
- `python/src/relay/client.py` `_fetch_all`
- `internal/relayclient/page.go` `FetchAllPages`
- [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] - closed; added the stops next door
