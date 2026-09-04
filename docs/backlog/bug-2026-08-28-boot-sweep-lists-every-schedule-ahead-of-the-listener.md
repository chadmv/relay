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
`ValidateStoredSpecsOnStartup` re-validates every ENABLED schedule synchronously during boot,
before `srv.ListenAndServe()`. Its read is keyset-paged through
`ListEnabledScheduledJobsPage`, so peak memory and per-statement work are one page: the
ALLOCATION half of this item is closed. **What survives is the DURATION.** The sweep issues
O(N) sequential round trips ahead of the listener - one page read per `sweepPageSize` enabled
rows plus one `UPDATE` per BROKEN row - and nothing bounds N. There is no per-user cap on
schedule creation, so an ordinary authenticated user can grow `scheduled_jobs` until the boot
takes long enough to matter, and the HTTP API an operator would use to delete the offending
schedules is exactly what never comes up.

**Paging added a second term to the same exposure.** The unpaged statement read ONE MVCC
snapshot, so its work set was fixed at N0 the instant the sweep started and no concurrent writer
could grow it. Every page is now its own snapshot, so a row INSERTed mid-sweep joins the work
set whenever its `gen_random_uuid()` id sorts above the cursor - with probability equal to the
unswept fraction of the key space. A per-owner count cap bounds N0 and not this: an owner at the
cap can `DELETE` one schedule and `POST` another indefinitely. The pass still converges, since
the unswept fraction only shrinks, so this is duration amplification and not non-termination.

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
newly created schedules do not satisfy.

Note the amplification: the pass issues one sequential `UPDATE` per BROKEN row, and "most rows
broken" is precisely the scenario the sweep exists for - the release that lands a new validation
rule. So the worst case for latency coincides with the case it was built to serve.

## Repro / Symptoms
- Authenticated non-admin creates many schedules with large `job_spec` bodies (each bounded only
  by `maxBodyBytes`, 1 MiB).
- Restart the server. Boot holds one page of `store.ScheduledJob` at a time, but issues one
  round trip per page and one `UPDATE` per broken row before the listener starts, so total wall
  clock is proportional to the enabled count.
- Keep creating schedules WHILE the sweep runs. Each page is a fresh snapshot, so rows landing
  above the cursor extend the same pass.
- Under a readiness probe the outcome is a probe timeout and a restart loop, with no HTTP
  surface to repair it from.

## Proposal
Two independent halves, either shippable alone:

1. Keyset-page the sweep - `WHERE enabled AND id > $1 ORDER BY id LIMIT $2`, loop until short.
   The statement already has `ORDER BY id`, so this costs nothing else. Consider also checking
   `ctx.Err()` in the loop body: on a shutdown mid-sweep every remaining BROKEN row logs its own
   `context canceled` line. `validateStoredRow` does no I/O, so healthy rows run silently at
   memory speed; only broken rows reach `RecordScheduledJobFailure`. The count still matters,
   because the sweep's worst case is "most rows broken" - the release that lands a new
   retroactive rule, which is the scenario the sweep exists for.
2. A per-owner schedule cap. Filed separately: [[feature-2026-09-04-per-owner-schedule-cap]].

   **A rate limit on `POST /v1/scheduled-jobs` is DECIDED OUT for this item**, on two arguments,
   both checked against the tree:
   - A creation-rate limit bounds growth RATE, not table SIZE, and size is what breaks the boot.
     `RateLimit` is a per-IP token bucket over a window: it changes how long an actor needs to
     reach N schedules and puts no ceiling on N. The sweep's cost is a function of N alone.
     Shipping it and recording "bounded" here would be a control that reads as a fix for a
     property it does not have.
   - No HTTP rate limit anywhere bounds a schedule's FIRING. `fireOne` has exactly one caller,
     `Runner.TickOnce`, reached from `Runner.Run` on the goroutine `cmd/relay-server` starts. It
     never touches an HTTP route. (`handleRunScheduledJobNow` IS an HTTP route that creates a job
     from a stored spec, but it is a separate path with its own `jobspec.Validate` call, not
     `fireOne`. Whether run-now wants a rate limit is a different question on a different route.)

Moving the sweep off the boot path into a goroutine is a third option. The no-lock argument this
paragraph used to rest on was already retired: `cmd/relay-server`'s own comment records that "no
lock is needed for a pass that runs while nothing else is running" is false the moment a second
replica exists, and what makes the sweep safe is `RecordScheduledJobFailure`'s content fence,
which is placement-independent. The real reasons to defer async placement are that it changes when
a boot reports ready and that it puts the sweep's UPDATEs in contention with `TickOnce`'s row
locks. Neither is a correctness objection; both are behaviour changes wanting their own slice.

## Acceptance / Done When
- The boot sweep's MEMORY and PER-STATEMENT cost is proportional to a page, not to the table.
  **Met.** Its DURATION is not, and this item must not read as if the exposure is closed: paging
  converted an unbounded ALLOCATION into an unbounded DURATION. An actor with a million enabled
  schedules still delays the boot by O(N) round trips before `ListenAndServe`, and the HTTP API an
  operator would use to delete them still comes up last. The per-owner cap, which is not here,
  bounds the STARTING work set; bounding the DURATION additionally needs a deadline or a
  total-pages ceiling on the sweep, recorded as an open question on
  [[feature-2026-09-04-per-owner-schedule-cap]].
- The sweep's doc comment states the property it actually has. **Already met at the time this
  slice was scoped** - the `THAT IS NARROWER THAN` paragraph was accurate before any of this
  landed. A criterion green before the change pins nothing, so the slice did not treat it as work.
  What it created instead was the obligation to keep it true: five prose sites were rewritten
  because paging or the `ctx.Err()` return falsified them.
- A decision is recorded on the quota and rate-limit half, even if it is "not now". **Met for the
  rate limit**, which is decided OUT above - decided, not deferred. The quota survives as the
  separately filed per-owner cap.

## Related
- `internal/store/query/scheduled_jobs.sql` (`ListEnabledScheduledJobsPage`, named
  `ListEnabledScheduledJobs` when this item was filed),
  `internal/schedrunner/startup_validation.go`, `cmd/relay-server/main.go`, `internal/api/server.go`
- [[bug-2026-08-23-unfireable-schedule-is-invisible]] - the slice that added the sweep
- [[bug-2026-08-23-http-listener-has-no-admission-bounds]] - adjacent, but about the request path;
  this one is about the boot path, which no admission bound on the listener can protect
- [[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]] - the same missing
  rate limit on the same family of routes, from the request side
