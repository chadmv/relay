---
title: The startup validation sweep lists every enabled schedule unbounded, ahead of the HTTP listener
type: bug
status: open
created: 2026-08-28
priority: medium
source: Phase 4 security, invariants and correctness lenses of the unfireable-schedule-visibility slice (2026-08-28)
---

# The startup validation sweep lists every enabled schedule unbounded, ahead of the HTTP listener

## Summary
`ListEnabledScheduledJobs` is `SELECT * FROM scheduled_jobs WHERE enabled ORDER BY id` with no
LIMIT, and `ValidateStoredSpecsOnStartup` consumes it into one slice synchronously during boot,
before `srv.ListenAndServe()`. `SELECT *` carries every row's `job_spec`. Because there is no
per-user cap on schedule creation and no rate limit on `POST /v1/scheduled-jobs`, an ordinary
authenticated user can grow that table until the server cannot finish booting - and the HTTP API
an operator would use to delete the offending schedules is exactly what never comes up.

## Context
Found independently by three Phase 4 review lenses on the slice that added the sweep
([[bug-2026-08-23-unfireable-schedule-is-invisible]]). Deferred from that PR because the fix has
two halves with different scopes: paging the sweep is small and local, while a schedule quota and
a rate limit on the create route are a separate policy decision.

The sweep's own doc comment says "IT MUST NOT BE ABLE TO STOP THE SERVER BOOTING, and it is
written so that it cannot". That is true of the property it was written for - a per-row record
error is logged and the loop continues - but it is a different property from the one the sentence
claims, and the unbounded read in front of the loop is not covered by it. Worth correcting the
comment even if the paging is not done, since a wrong contract in prose is a defect on this
project.

Every other read of `job_spec` in the tree is bounded: `handleListScheduledJobs` is paged,
`ListEligibleScheduledJobs` has `LIMIT $1` at `BatchLimit = 100`, and
`ListOverdueScheduledJobsForCatchup` is unbounded but filtered by `next_run_at < NOW()`, which
newly created schedules do not satisfy. `ListEnabledScheduledJobs` is the first statement that
materializes the whole enabled set with specs attached.

Note the amplification: the pass issues one sequential `UPDATE` per BROKEN row, and "most rows
broken" is precisely the scenario the sweep exists for - the release that lands a new validation
rule. So the worst case for latency coincides with the case it was built to serve.

## Repro / Symptoms
- Authenticated non-admin creates many schedules with large `job_spec` bodies (each bounded only
  by `maxBodyBytes`, 1 MiB).
- Restart the server. Boot allocates one `store.ScheduledJob` per enabled row, all at once,
  before the listener starts.
- Under a readiness probe the outcome is a crash loop, with no HTTP surface to repair it from.

## Proposal
Two independent halves, either shippable alone:

1. Keyset-page the sweep - `WHERE enabled AND id > $1 ORDER BY id LIMIT $2`, loop until short.
   The statement already has `ORDER BY id`, so this costs nothing else. Consider also checking
   `ctx.Err()` in the loop body: on a shutdown mid-sweep every remaining row currently logs its
   own `context canceled` line.
2. A per-owner schedule cap and a rate limit on `POST /v1/scheduled-jobs`. `RateLimit` is
   currently wired only to register and login; the create route is bare `auth(...)`.

Moving the sweep off the boot path into a goroutine is a third option, but it INTERACTS with the
sweep's fence: the current no-lock argument depends on the sweep completing before the runner
starts, so making it async requires the row-generation predicate to be in place first.

## Acceptance / Done When
- The boot sweep's memory and query cost is proportional to a page, not to the table.
- The sweep's doc comment states the property it actually has.
- A decision is recorded on the quota and rate-limit half, even if it is "not now".

## Related
- `internal/store/query/scheduled_jobs.sql` (`ListEnabledScheduledJobs`),
  `internal/schedrunner/startup_validation.go`, `cmd/relay-server/main.go`, `internal/api/server.go`
- [[bug-2026-08-23-unfireable-schedule-is-invisible]] - the slice that added the sweep
- [[bug-2026-08-23-http-listener-has-no-admission-bounds]] - adjacent, but about the request path;
  this one is about the boot path, which no admission bound on the listener can protect
- [[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]] - the same missing
  rate limit on the same family of routes, from the request side
