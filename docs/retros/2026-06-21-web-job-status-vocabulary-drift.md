---
date: 2026-06-21
topic: web-job-status-vocabulary-drift
branch: claude/gifted-meninsky-5fc18a
pr: "2026-06-21 / web-job-status-vocabulary-drift"
merge: "2026-06-21 / web-job-status-vocabulary-drift"
---

# Session Retro: 2026-06-21 - Web job-status vocabulary drift

**TL;DR:** Closed `bug-2026-06-20-web-job-status-vocabulary-drift`. The SPA `JobStatus` model and
the Jobs "Queued" filter referenced `queued`/`dispatched`/`timed_out` - values `jobs.status` never
holds (migration 000019 locks it to pending/running/done/failed/cancelled). The "Queued" filter
sent `status=queued`, which could never match a job. Narrowed the model and pointed the filter at
`status=pending`. Autopilot iteration 5 (batch 2, item 1 of 4).

## What Was Built

- `web/src/jobs/api.ts` - `JobStatus` narrowed to `pending|running|done|failed|cancelled`.
- `web/src/jobs/status.ts` - removed the dead `dispatched`/`queued`/`timed_out` switch cases
  (each was a fallthrough partner of a retained status, so no retained color changed) and corrected
  the doc comment.
- `web/src/jobs/JobsPage.tsx` - the "Queued" filter chip now sends `status=pending` (label kept).
- `web/src/jobs/JobsTable.tsx` - dropped the dead `j.status === 'timed_out'` comparison.
- Tests: a behavioral RED test (the Queued chip must request `status=pending`, proven RED against the
  old `status=queued`) plus the trimmed `status.test.ts` covering all five real statuses.

## Key Decisions

- **Keep the "Queued" label, fix the value.** Options were relabel to "Pending", remove the chip, or
  keep "Queued" and send `status=pending`. The last is the most consistent: the stats strip already
  labels the pending count "QUEUED", and a pending job is colloquially "queued" (waiting for a
  worker). So the chip stays "Queued" and now actually returns pending jobs.
- **`tsc` as the completeness proof.** Because `JobStatus` is a string-literal union, narrowing it
  turns every surviving `case 'queued'` / `=== 'timed_out'` / `statusColor('dispatched')` into a
  compile error. A clean `tsc -b` is therefore proof that no dead status reference remains anywhere -
  stronger than grep. The behavioral test covers the one runtime concern (the filter value).
- **Left the public `queued` wire field alone.** `JobStats.queued` (counts pending jobs) is an
  intentional API field, explicitly out of scope - not touched.

## Backlog Triage

- No new items. Code review returned no findings.

## Process Note

- Worktree-path discipline applied from iteration 4's lesson: the engineer and reviewer dispatches
  named the worktree path explicitly and instructed all commands to run there; the engineer's commits
  landed on the correct branch and `web/dist` was reverted (not committed) after the build.
- Proportionate verification once more: a type-narrowing + dead-code cleanup where `tsc` proves
  completeness and one behavioral test proves the fix got a single focused frontend code-review, not
  the backend-oriented relay-verify fan-out.
