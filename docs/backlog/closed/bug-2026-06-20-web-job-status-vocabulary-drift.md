---
title: SPA models job statuses the backend never emits (queued/dispatched/timed_out)
type: bug
status: closed
created: 2026-06-20
closed: 2026-06-21
resolution: fixed
priority: medium
source: status-vocabulary-drift retro (2026-06-20)
---

# SPA models job statuses the backend never emits (queued/dispatched/timed_out)

## Summary
The frontend `JobStatus` type and the Jobs page status filter model `queued`,
`dispatched`, and `timed_out` as job statuses. As of migration
`000019_status_vocabulary_checks`, `jobs.status` is constraint-locked to
`pending`/`running`/`done`/`failed`/`cancelled` and never holds any of those three
values. So the SPA carries status cases the backend cannot produce, and the
"Queued" filter sends `status=queued`, which can never match a job.

`dispatched`/`timed_out` are real `tasks.status` values but NOT `jobs.status`
values - the two vocabularies were deliberately kept distinct in 000019.

## Context
- `web/src/jobs/api.ts:3-11` - `JobStatus` union includes `'queued' | 'dispatched' | 'timed_out'`.
- `web/src/jobs/status.ts:11-26` - `statusColor` switch has cases for `dispatched`, `queued`, `timed_out`.
- `web/src/jobs/JobsPage.tsx:12` - the "Queued" filter sends `status: 'queued'`.

The `JobStatusCounts` API still exposes a public `queued` JSON field (now counting
`pending` jobs) for backward compatibility; that wire name is intentional and is
NOT what this item is about. This item is the SPA's own `JobStatus` model and
status filter referencing values `jobs.status` cannot hold.

## Proposal
Narrow `JobStatus` to the real job vocabulary (`pending`/`running`/`done`/`failed`/
`cancelled`), drop the dead `dispatched`/`queued`/`timed_out` switch cases, and make
the "Queued" filter send `status=pending` (or relabel/remove it) so it actually
matches jobs. Confirm the stats card that reads the public `queued` count is
unaffected, since that field is a separate concern.

## Related
- `web/src/jobs/api.ts:3-11`
- `web/src/jobs/status.ts:11-26`
- `web/src/jobs/JobsPage.tsx:12`
- `internal/store/migrations/000019_status_vocabulary_checks.up.sql` (`jobs_status_check`)

## Resolution
Fixed 2026-06-21 (web-job-status-vocabulary-drift). The SPA `JobStatus` union was narrowed to the
real `jobs.status` vocabulary `pending|running|done|failed|cancelled` (migration 000019
`jobs_status_check`), removing the dead `queued|dispatched|timed_out` values. The `statusColor`
switch dropped those three dead cases (each was a fallthrough partner of a retained status, so no
retained color changed: running=accent, pending=warn, failed=err, done=ok, cancelled=fg-mute via
default) and its doc comment was corrected; `JobsTable` dropped its dead `j.status === 'timed_out'`
comparison. The Jobs "Queued" filter chip now sends `status=pending` (label kept "Queued",
consistent with the stats strip that already labels the pending count "QUEUED"), so it actually
matches jobs instead of sending the never-matching `status=queued`. `JobStats`/`getJobStats`/the
stats strip and their intentional public `queued` wire field were left untouched (out of scope).
A behavioral RED test asserts the Queued chip requests `status=pending`; `tsc` passing is the
completeness proof that no dead status reference remains anywhere in the SPA. Code review returned
no findings.
