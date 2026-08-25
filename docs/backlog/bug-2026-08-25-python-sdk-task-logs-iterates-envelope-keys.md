---
title: Python SDK task_logs() iterates the pagination envelope's keys, so it cannot return log records
type: bug
status: open
created: 2026-08-25
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
