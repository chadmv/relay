---
title: Python SDK decode failures escape the RelayError hierarchy that the README says is exhaustive
type: bug
status: open
created: 2026-08-27
priority: medium
source: Spec D8 plus the Phase 4 security lens, while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# SDK decode failures escape the `RelayError` hierarchy

## Summary
`python/README.md` states that all exceptions descend from `relay.RelayError`. Three classes of
server-driven failure do not, at twelve `response.json()` call sites plus every
`model_validate`. A caller who writes `except RelayError` around SDK calls is not covered.

## Repro / Symptoms
Measured against mock 200 responses:

```
not JSON (an ingress HTML error page) -> json.decoder.JSONDecodeError   RelayError: False
204 no content                        -> json.decoder.JSONDecodeError   RelayError: False
integer over 4300 digits              -> builtins.ValueError            RelayError: False
body shape mismatch                   -> pydantic_core.ValidationError  RelayError: False
bare list where an object is expected -> builtins.TypeError             RelayError: False
missing "items" key                   -> builtins.KeyError              RelayError: False
```

The non-JSON case is the realistic one: a proxy or ingress returning an HTML error page with a 200,
which is the same "the client shows nothing useful" family the envelope-drift bug came from.

## Context
The README's error section has been corrected to name `pydantic.ValidationError`,
`json.JSONDecodeError` and `ValueError` as escaping, so the documentation is no longer wrong. This
item is the code half: making them catchable.

Declined during that slice because it is a cross-cutting wrap at every call site and folding it in
would have made the diff two features.

`ProtocolError`, added in that slice, does descend from `RelayError`, so the new code did not add to
the count.

## Proposal
One `_read_json(response)` chokepoint, the Python analogue of CLAUDE.md's single JSON entry point
invariant, wrapping decode and validation failures in a `RelayError` subclass while preserving the
original as `__cause__`. That also gives the natural home for a response-size bound - see the
related item, which measured a 343x gzip amplification against this same unbounded `.json()`.

## Acceptance / Done When
- Every documented SDK entry point raises only `RelayError` subclasses for a malformed or
  undecodable server response.
- The original exception is reachable via `__cause__`.
- The README's error table matches, and a test pins that a non-JSON 200 is catchable as
  `RelayError`.
- **The wrapper STRIPS `input` rather than passing it through**, and a test pins that. Measured
  against pydantic 2.13.5 on 2026-08-29: a `type=missing` error is raised at the MODEL level, so
  its `["input"]` in `e.errors()` is the ENTIRE decoded page rather than the offending field, and
  `errors()`/`json()` do not truncate it
  (only `str(e)` does, to a ~50-char head and tail). A `/v1/schedules` page carries each schedule's
  full `job_spec`, per-task `env` maps included, and `list_schedules()` walks pages of them - so the
  natural `logger.error("decode failed: %s", e.errors())` written against the README's "catch
  `pydantic.ValidationError` explicitly" instruction ships those values to wherever the logs go.
  Do NOT write the pin against `logger.exception(...)`: measured the same day, that vector does NOT
  leak a schedules page, because `logging` renders through `traceback` and so inherits `str`'s
  truncation. A test pinning `logger.exception` would have been green before any fix, and vacuous.
  The thing to guard is `errors()`, not a call site. This
  chokepoint is the one place that can fix it for every call site; `__cause__` must not silently
  re-expose what the wrapper strips.
- **DECIDE whether a validation failure mid-walk carries the rows already collected**, either way,
  and say so in the README. Today it does not: when `_get_page` raises inside `_fetch_all`, `out`
  (pages 1..N-1) is discarded - and the target set is narrower than it looks. The loop's own three
  termination stops preserve it via `ProtocolError(..., records=out)`, and the README's error table
  advertises `.records` as "whatever that walk collected". But `raise_for_response` sits inside
  `_get_page` on the SAME call path, so an HTTP error mid-walk discards `out` too, exactly as the
  validation failure does - the six `list_*` docstrings say so already.
  `pydantic.ValidationError` is therefore NOT the anomaly: it behaves like the `RelayError`s and
  unlike only the loop's own `ProtocolError` stops. Count carefully if you restate this - the loop
  has three logical STOPS but four `raise ProtocolError(..., records=out)` STATEMENTS, since the
  page-cap stop raises from two branches. The decision is over three PARTIES whose partial-walk
  behaviour must agree (the ProtocolError stops, the HTTP error, the validation failure), so "make
  it match everything else" is the wrong target. A 249-page walk that fails on page 250 loses ~49,800 rows with no
  cursor to resume from. A local `try/except` at the `_get_page` call site is the WRONG fix and
  `client.py`'s comment there says why - it would make `_get_page` and `task_logs_page` raise
  different types for one defect shape. The chokepoint is where the two can be made to agree, so
  this item inherits the question and must answer it rather than pass it on.

## Related
- `python/src/relay/` - twelve `response.json()` sites: eleven in `client.py`, one in
  `errors.py` (`_extract_message`). A `grep -c` of `client.py` alone answers twelve because
  `client.py`'s own comment names the literal `response.json()` and matches itself.
- `python/README.md` - Errors section
- [[bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout]] - the same chokepoint
  would carry the byte bound
- [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]]
