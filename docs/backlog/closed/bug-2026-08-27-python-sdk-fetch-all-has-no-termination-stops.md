---
title: Python SDK _fetch_all walks a server-supplied cursor with no termination stops, so a repeating cursor hangs six methods forever
type: bug
status: closed
created: 2026-08-27
priority: high
source: Phase 4 invariants lens while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
closed: 2026-08-29
resolution: fixed
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

## Resolution
Both languages fixed. `Client._fetch_all` and `relayclient.FetchAllPages` each gained an
empty-page stop, a repeated-cursor stop and a page cap, below the caller-`limit`
short-circuit and below the server's own drained signal. The repeated-cursor stop is a seen-SET
rather than a comparison against the previous cursor: an opaque string has no order, so the O(1)
comparison the item's `task_logs` exemplar uses catches only a self-loop, while an `A,B,A,B`
cycle - two replicas behind a load balancer - runs to the page cap. The Go decision was made
explicitly and came out as fix-both: six CLI commands walk a list implicitly via
`resolveWorkerIDIn` with `userLimit=0`, so `--limit` cannot bound them.

The item's diagnosis held on every point. Four of its prescriptions did not, and the largest is
worth recording: "the reasoning applies verbatim next door" is true of the justification and
false of the predicates. A second correction was found in verification - the page cap's "every
one was collected" arm was copied from `task_logs`, where its premise is true because
`GetTaskLogsPage` is `LIMIT $3`, into the list walk, where it is false because every list query
is `LIMIT page_limit + 1` and `buildPage` emits a cursor only when the extra row came back. On a
correct server the list walk can never stop one request short, so the arm could only fire on a
misbehaving server - and it settled completeness using that same server's `total`. Dropped; the
list walks in both languages now report without asserting.

Verification also found that `_fetch_all` was the only walk in the SDK hand-picking a raw
envelope, so five server-controlled shapes crashed outside `RelayError`; the whole body now
decodes through `Page[model]`, as `task_logs_page` already did. Two of those crash sites
(`_quote_cursor`'s `len()`, and `cursor in seen`) were introduced by this slice's own first
round.

23 commits on `claude/python-sdk-fetch-all-termination-c0622b`. Gates: 160 Python unit tests,
ruff and mypy --strict clean, 22 Go packages, `go vet` clean, `-race` green in the golang:1.26
container, Go CLI real-server integration 202/202, Go API 336/336, and all twelve Python list
methods driven against a live server with five of six crossing a real page boundary.

Spec `docs/superpowers/specs/2026-08-28-fetch-all-termination-stops.md`, plan
`docs/superpowers/plans/2026-08-28-fetch-all-termination-stops.md`.

Related items left deliberately open: [[bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained]]
(a MISSING `next_cursor` key still reads as drained; this slice changed only wrong-TYPED values),
[[bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy]] (`pydantic.ValidationError`
still escapes), and [[bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout]]
(the cap bounds requests only - not wall clock, bytes, or memory).
