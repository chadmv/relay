---
title: Python SDK task_logs() iterates the pagination envelope's keys, so it cannot return log records
type: bug
status: closed
created: 2026-08-25
closed: 2026-08-27
resolution: fixed
priority: high
source: Complement search while filing the CLI envelope-drift bug, 2026-08-25 - three clients call this endpoint and two are broken
---

# Python SDK task_logs() iterates the pagination envelope's keys, so it cannot return log records

## Summary
`Client.task_logs()` does `[LogRecord.model_validate(item) for item in response.json()]`. The handler
returns a JSON object, not an array, and iterating a dict in Python yields its KEYS - so the
comprehension walks the strings `"items"`, `"next_seq"`, `"total"` and tries to validate each as a
`LogRecord`. The method cannot return logs for any task.

## Repro / Symptoms
`client.task_logs(task_id)` against any current `relay-server`.

Expected: a list of `LogRecord`. Observed: the comprehension iterates three strings.
`LogRecord.model_validate("items")` should raise `pydantic.ValidationError`.

**Not measured** - this environment has no `pydantic` or `httpx` installed, so the failure mode is
read off the code rather than executed. The dict-iteration half is plain Python semantics and is not
in doubt; confirm the exact exception when fixing. Either way the method does not return log records.

`python/tests/integration/test_smoke.py:26` calls it, so the integration lane should already fail
against a real server - check whether that lane is being run.

## Context
`python/src/relay/client.py:264`. The logs endpoint gained its envelope in `a90c727` (2026-05-08).

**This is an incomplete fix, not a fresh bug.** [[bug-2026-05-26-python-sdk-list-pagination-envelope]]
covered exactly this defect class in exactly this file and was closed `fixed` by `81a3d65` on
2026-06-03 - three and a half weeks AFTER the logs endpoint had already moved to an envelope. That
fix updated `list_jobs()` and `list_schedules()`; `task_logs()` was left on the bare-array
assumption. The closed item's own acceptance criterion was "All paginated REST endpoints have a
corresponding SDK method", and this endpoint was paginated at the time, so the criterion was
recorded as met while an endpoint in scope was still broken.

**Why the unit suite is green:** `python/tests/unit/test_client.py:184`
(`test_task_logs_parses_records`) hand-writes a bare-array response, so it asserts the SDK agrees
with its own fixture rather than with the handler. This is the identical fake-drift mechanism that
hides the Go CLI instance, in a second language.

## Proposal
1. Read `response.json()["items"]`.
2. Decide on paging. `next_seq` is available; the SDK's list methods already grew cursor paging in
   `81a3d65`, so follow whatever shape those settled on rather than inventing a third.
3. Fix `test_task_logs_parses_records` to emit the envelope. Leaving the fixture stale reproduces
   the exact condition that let this survive a fix aimed at it.
4. **Sweep the rest of the SDK against the handlers rather than fixing this one method.** The
   lesson of the 2026-06-03 close is that a per-method fix missed a method; enumerate every SDK
   call against its handler's actual response shape and record the count.

## Acceptance / Done When
- `task_logs()` returns the task's `LogRecord`s against a real `relay-server`.
- `test_task_logs_parses_records` uses the envelope shape, and reverting the client fix while
  keeping the new fixture turns it RED.
- Every SDK method has been checked against its handler's emitted shape, with the number checked
  stated - not just this one method repaired.

## Related
- `python/src/relay/client.py:262-266`
- `python/tests/unit/test_client.py:184` (the stale fixture), `python/tests/integration/test_smoke.py:26`
- `internal/api/tasks.go:132` (the envelope), `a90c727` (2026-05-08)
- [[bug-2026-05-26-python-sdk-list-pagination-envelope]] - closed `fixed` 2026-06-03; this endpoint
  was inside its stated scope and was missed
- [[bug-2026-08-25-relay-logs-prints-nothing-envelope-drift]] - same endpoint, same drift, Go CLI
- [[idea-2026-08-23-cli-tests-never-hit-real-server]] - the mechanism, filed for the CLI; it applies
  verbatim to the Python SDK

## Notes
`internal/mcp/task_logs.go:46` is the third client of this endpoint and is **correct** - it decodes
into `map[string]any` and passes the envelope through untouched. Worth noting because it means the
handler change was not universally missed, and because a passthrough client is structurally immune
to this class.

## Resolution

Fixed across 32c1b36..HEAD on branch claude/python-sdk-task-logs-envelope-0d53a7.

`task_logs()` decodes the envelope, auto-pages `?since_seq=` with the cursor passed
VERBATIM, and gained a `task_logs_page()` sibling plus a `LogPage` model whose
`next_seq`/`total` are required so a missing key cannot read as "drained". Three
termination stops beyond the server's drained signal, each separately pinned by a
mutation that kills exactly one test.

All three acceptance criteria are met, the third one literally:

- **Against a real relay-server**: `tests/integration/test_smoke.py::test_submit_and_wait`
  passes with a real `relay-agent` executing the task. A separate 1221-row log paged over
  7 pages with zero gaps and zero duplicates at every page boundary.
- **The fixture distinguishes the fix**: reverting the client body while keeping the new
  envelope fixture turns 9 tests RED, 8 on the original `ValidationError` and 1 earlier.
- **The sweep, with counts**: 25 HTTP-performing methods over 18 route+verb pairs, and
  88 model fields across 11 response models, checked against the handler that serves each.

The sweep found 14 findings (D1-D14); six were fixed here and eight named and declined,
each now filed. It also MISSED one axis, which is the item's own lesson repeating: it
checked each response's CONTAINER shape and never each FIELD's nullability. The live
integration lane caught `Job.labels` arriving as `null` where the model required a dict -
invisible to all four reading-based review lenses. Five `rawJSON` wire fields were then
coerced; two (`Job.labels`, `Reservation.selector`) are confirmed reachable on a live wire,
three are defence in depth.

Four verify rounds. Round 1 found the page cap discarding a provably-complete log - the
original "returns nothing" defect re-created inside the fix's own backstop - and round 2's
obvious remedy (`return out`) was itself refuted, because `len(out)` counts records appended
rather than distinct rows and a duplicate-serving server made the completeness claim false.
Round 3 found `.records` unpinned at two of its four raise sites. Round 4 found the README
prescribing a remedy that does not exist: httpx has no total-time and no response-size
setting, so `Client(timeout=)` cannot bound the two axes it was said to bound.

Separately fixed because this slice shipped new documentation for it: `follow_job()` raised
`ValueError` on its first frame in every released version and had zero tests. It was observed
working against a live server for the first time in the SDK's history.

Version 0.1.2 -> 0.2.0, not 0.1.3: `LogRecord.seq` became required, which a patch bump would
have advertised as no breakage.

