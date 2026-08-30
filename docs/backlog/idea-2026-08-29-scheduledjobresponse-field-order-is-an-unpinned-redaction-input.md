---
title: scheduledJobResponse's field order is an unpinned input to a redaction claim in the Python SDK's README
type: idea
status: open
created: 2026-08-29
priority: low
source: Phase 4 round-4 verify of the strict-Page-envelope slice (2026-08-29)
---

# `scheduledJobResponse`'s field order is an unpinned input to a redaction claim

## Summary
`python/README.md` now tells a consumer how much of a paged response body can reach a log through an
escaping `pydantic.ValidationError`. The bound it states is partly a fact about a **Go struct's field
declaration order**, and nothing pins that order.

## Context
pydantic truncates `input_value` in `__str__` to a head-and-tail window, ~25 characters each, and
`logging` renders an exception through `traceback` -> `str(exc)`. So `logger.exception(...)` always
emits the last ~24 characters of the input repr, at any body size.

For a `Page[T]` body most of that tail is spent on the envelope trailer (`}], 'total': N}`), leaving
roughly the last 9 characters to reach into the final item's final field. Today that field is
`updated_at`, because `scheduledJobResponse` (`internal/api/scheduled_jobs.go`) declares `job_spec`
seventh and `created_at`/`updated_at` last, and `json.loads` preserves declaration order.

Measured 2026-08-29 on pydantic 2.13.5: moving `job_spec` last exposes 4 characters of a nested
`env` credential. A full credential cannot reach the window through `Page[T]` because the trailer is
always there - but "4 characters of a secret" is still a worse answer than "9 characters of a
timestamp", and the difference is decided by a Go struct nobody is watching.

## Proposal
Two independent options; either closes it.

- **Pin the order** in `internal/api` - a test asserting `created_at`/`updated_at` marshal last in
  `scheduledJobResponse`, next to the existing envelope-tag guard
  (`TestPageEnvelope_AllThreeKeysArePresentOnAZeroValuePage`), so a reordering refactor goes red.
- **Drop the specific-field claim** from `python/README.md` and state only the bound that holds for
  any order (the trailer consumes most of the tail; guard `errors()` regardless).

The second is cheaper and removes a cross-language prose dependency rather than pinning one. That is
worth weighing on its own: this slice spent four verify rounds on prose in one language making
checkable claims about another's source, and the remedy that finally worked was deleting the claim
rather than correcting it.

## Acceptance / Done When
- Either the field order is pinned by a test in the package that owns the struct, or the README no
  longer names a specific field as the one the tail lands on.
- No prose in `python/` states a redaction bound that depends on an unpinned Go declaration order.

## Related
- `internal/api/scheduled_jobs.go` `scheduledJobResponse`
- `python/README.md` - the Errors section's truncation paragraph
- [[bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy]] - the `_read_json`
  chokepoint that must STRIP `input`; if that lands, this claim's subject disappears entirely and
  this item can close as obsolete.
- [[bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained]] - the closed slice that introduced
  the claim.
