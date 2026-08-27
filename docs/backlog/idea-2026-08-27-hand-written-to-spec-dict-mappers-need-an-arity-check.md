---
title: Job.to_spec_dict and Task.to_spec_dict hand-write their keys, so an authoring field added later is silently never sent
type: idea
status: open
created: 2026-08-27
priority: low
source: Phase 4 invariants and correctness lenses, while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# The `to_spec_dict` mappers need an arity check

## Summary
`Job.to_spec_dict()` hand-writes four keys out of sixteen model fields. `Task.to_spec_dict()` does
the same. Both are correct today - the omitted fields are all response-only - but nothing pins the
partition, so an AUTHORING field added to either model tomorrow is silently dropped from the request
body with the whole suite green.

## Repro / Symptoms
Measured after the slice above added six response-only fields to `Job`:

```
Job.model_fields : 16
to_spec_dict keys: ['labels', 'name', 'priority', 'tasks']
DROPPED (12)     : created_at, done_tasks, finished_at, id, scheduled_job_id,
                   scheduled_job_name, started_at, status, submitted_by,
                   submitted_by_email, total_tasks, updated_at
```

`grep -rn "model_fields" python/tests/` returns nothing. `test_to_spec_dict_serializes_minimal_job`
pins the OUTPUT dict exactly, so it stays green when a new field is dropped rather than going red.

## Context
This is the "a hand-written copy between two types needs an arity check" shape. The blind spot did
not appear in that slice - it widened, from six fields to twelve - and the widening is what makes it
worth pinning now rather than later.

The allow-list itself is the right design: it is what stops `scheduled_job_id` and the other
server-computed fields from being posted back to `POST /v1/jobs`. The gap is only that nothing
forces a decision when a field is added.

## Proposal
Name the two sets in `models.py` and assert `set(Job.model_fields) == _AUTHORING | _RESPONSE_ONLY`,
so adding a field to `Job` fails until it is classified. Same for `Task`.

## Acceptance / Done When
- Adding a field to `Job` or `Task` without classifying it turns a test RED.
- The RED names the unclassified field.

## Related
- `python/src/relay/models.py` `Job.to_spec_dict`, `Task.to_spec_dict`
- [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]]
