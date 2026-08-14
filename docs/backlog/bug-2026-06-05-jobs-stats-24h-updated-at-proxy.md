---
title: Jobs stats 24h buckets rely on updated_at finish proxy
type: bug
status: open
created: 2026-06-05
priority: low
source: jobs-list-frontend retro (2026-06-05)
---

# Jobs stats 24h buckets rely on updated_at finish proxy

## Summary
The `done_24h`/`failed_24h` buckets in `JobStatusCounts` window on `jobs.updated_at` as a finish-time proxy. Still correct today, but **two claims in the original filing were wrong** and were corrected on 2026-08-13:

- **`updated_at` has two writers, not one.** This item said "the only writer of `updated_at` is `UpdateJobStatus`". `RecomputeJobStatus` stamps `NOW()` unconditionally too, after every task status transition, so `updated_at` means "time of the last task-level event", not "time of the last job-status transition". The proxy survives on a narrower invariant: a job only *has* status `done` or `failed` when its last task event was the one that finished it, and a terminal task is unwritable, so no later task event can move `updated_at` while the job sits in a terminal bucket. The false comment on the statement itself is corrected in `internal/store/query/jobs.sql`.
- **The retry trigger fired and did not reproduce.** This item predicted the proxy would break "if a `POST /v1/jobs/:id/retry` endpoint is added that re-opens terminal jobs". That endpoint shipped on 2026-08-13 and the prediction was measured, not assumed: a retried job leaves both buckets the instant it becomes `running`, and re-enters the appropriate bucket when it finishes again with an `updated_at` equal to that new finish. The only effect is a transient undercount while it re-runs, which self-corrects. Explicitly accepted in writing at the statement.

## Context
Decision recorded in the jobs-list design spec and verified during the session. Jobs have no dedicated `finished_at` column. If retry lands, revisit: either add a real `jobs.finished_at` column set on terminal transition, or window on `MAX(tasks.finished_at)`.

## Related
- `internal/store/query/jobs.sql` (`JobStatusCounts`)
- `docs/superpowers/specs/2026-06-05-web-jobs-list-design.md`
- The "if retry lands" trigger in Context **has now fired and been resolved without a fix**:
  [[feature-2026-08-13-job-retry-endpoint]] shipped on 2026-08-13 and chose the
  "explicitly accepted in writing" arm of its acceptance criterion, because the predicted regression
  did not reproduce (see Summary). This item therefore stays OPEN on its original merits - the
  proxy is still a proxy and there is still no `jobs.finished_at` - but it no longer has a pending
  trigger, and it is no longer a scheduling dependency of anything.
