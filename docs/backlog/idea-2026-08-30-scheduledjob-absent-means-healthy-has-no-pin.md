---
title: ScheduledJob's absent-means-healthy failure-key omission has no Go-side pin
type: idea
status: open
created: 2026-08-30
source: comment-policy retrofit; the models.py docstring carried the claim with no guard
---

# ScheduledJob's absent-means-healthy failure-key omission has no Go-side pin

## Summary
The Python SDK's `ScheduledJob` reads absent `last_fire_error`/failure keys as healthy, on the
contract that `scheduledJobResponse` omits both keys entirely (`omitempty`) when there is no
failure. Nothing on the Go side pins that omission - the analogue of
`TestPageEnvelope_AllThreeKeysArePresentOnAZeroValuePage`, in the opposite direction: that test
pins presence of keys a client requires, this would pin absence of keys a client reads meaning
into. A Go change that starts emitting an empty-string failure key would flip healthy schedules
to failed-looking in the SDK with all Go packages green.

## Context
The 2026-08-30 comment retrofit condensed the models.py docstring that carried this contract; per
the new CLAUDE.md comment policy a cross-language claim should be pinned by a named guard instead.

## Acceptance / Done When
- A Go test marshals a healthy `scheduledJobResponse` and asserts the failure keys are absent from
  the JSON, cited from the models.py docstring in place of the prose contract.

## Related
- [[idea-2026-08-29-scheduledjobresponse-field-order-is-an-unpinned-redaction-input]]
- [[idea-2026-08-14-owner-email-omitempty-contract]]
- python/src/relay/models.py (ScheduledJob), internal/api/scheduled_jobs.go
