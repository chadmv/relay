---
title: A per-owner cap on scheduled_jobs, the only bound on the boot sweep's duration
type: feature
status: closed
closed: 2026-09-04
resolution: fixed
created: 2026-09-04
priority: medium
source: Carries the surviving half 2 of bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener, after its rate-limit half was decided OUT on 2026-09-04
---

# A per-owner cap on scheduled_jobs, the only bound on the boot sweep's duration

## Summary
There is no per-owner limit on how many schedules a user may create. `POST /v1/scheduled-jobs`
is bare `auth(...)`, so an ordinary authenticated user can grow `scheduled_jobs` without bound.

## Context

This is what remains of the boot-sweep item's half 2 after the other half was decided rather
than deferred.

**The rate limit is OUT, decided not deferred.** `docs/superpowers/specs/2026-09-04-authenticated-route-rate-limiting.md`
settled it on two arguments. A creation-RATE limit bounds growth rate, not table SIZE, and it is
size that breaks the boot - so shipping one would let the boot-sweep item record "bounded" for
something that is not bounded. And `schedrunner.fireOne` runs on the runner goroutine and never
touches an HTTP route, so no HTTP rate limit anywhere bounds a schedule's firing.
Reversing that decision is one wrap
line if a reason appears.

**The cap is load-bearing for the paging slice, which is why this item exists as its own file.**
`docs/superpowers/specs/2026-09-04-boot-sweep-keyset-paging.md` keyset-pages
`ValidateStoredSpecsOnStartup`, which bounds the sweep's peak MEMORY to one page. It does not
bound its DURATION: an actor with a million schedules still delays `ListenAndServe` by O(N)
round trips. A cap bounds the sweep's STARTING work set, which is what the paging slice's doc
comment cites this item for. A decision conditioned on "the cap is coming" is unfalsifiable
until the cap is a findable item.

**A cap does not bound the sweep's duration on its own, and that is an OPEN QUESTION on this
item rather than a consequence of it.** HEAD's single unpaged statement read one MVCC snapshot,
so the work set was fixed at N0 the instant the sweep started. The paged sweep takes a fresh
snapshot per page, so a row INSERTed mid-sweep joins the work set whenever its
`gen_random_uuid()` id sorts above the cursor - with probability equal to the unswept fraction
of the key space. An owner sitting at the cap can `DELETE` one schedule and `POST` another
indefinitely, and a count cap bounds N0 alone. The pass still converges, since the unswept
fraction only shrinks, so this is duration amplification and not non-termination - but it is
amplification the cap does not close, and it exists only because the read is paged. What would
actually bound the duration is a wall-clock deadline on the sweep, or a ceiling on total pages.
Decide which, here or in its own item, before this item's acceptance claims a duration bound.

Note the same hazard applies to `handleRunScheduledJobNow`, which is an HTTP route that creates
a job from a stored spec. It is bucketed by the submit rate limit, but that bounds job creation,
not schedule count.

## Proposal

Sketch only; the number is the work.

1. Decide the cap and where it is enforced. A per-owner `COUNT(*)` at create time is the obvious
   shape; note it is a read-then-write across two statements unless it is done in one.
2. Decide what an admin sees and whether admins are exempt. Note that an exempt class of caller
   is a control that does not exist for the people most able to trip it, which is the argument
   the submit rate limit used to refuse an admin exemption.
3. Decide what happens to an owner already over the cap when it lands. Tightening a validator is
   retroactive over stored data, and the readers are hard to find.

## Acceptance / Done When

- A per-owner schedule count is bounded, and the bound is configurable.
- The boot sweep's STARTING work set is bounded as a consequence. Its DURATION is not, until the
  open question above is settled - do not restate the boot-sweep item's first acceptance
  criterion as a duration bound on the strength of this item alone.
- README documents the cap and its configuration.

## Related

- [[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]] - the parent; this is
  its surviving half 2
- [[bug-2026-08-29-post-v1-jobs-is-not-rate-limited]] - where the rate-limit half was decided OUT
- `internal/api/server.go` (`POST /v1/scheduled-jobs`, bare `auth`),
  `internal/api/scheduled_jobs.go` (`handleCreateScheduledJob`),
  `internal/schedrunner/startup_validation.go` (`ValidateStoredSpecsOnStartup`)

## Resolution

Closed by PR #205. `RELAY_MAX_SCHEDULES_PER_OWNER`, default 100, refuses `POST /v1/scheduled-jobs`
with 409 at the cap, enforced in one transaction as lock-then-count-then-insert. Owners already over
the cap are grandfathered.

**This item's prescribed remedy does not work, and that was measured rather than argued.** The item
says the read-then-write is safe "unless it is done in one" statement. Two sessions at cap-1 running
exactly that conditional INSERT both committed - 3 rows against a cap of 2, with B never blocking -
because under READ COMMITTED the subquery evaluates against the statement's own snapshot. Exactness
needs a lock in an EARLIER statement of the same transaction, so one statement is neither necessary
nor sufficient. `FOR NO KEY UPDATE` rather than `FOR UPDATE`, measured both ways against the real
`jobs.submitted_by` foreign key.

Two of this item's other claims were also wrong. `handleRunScheduledJobNow` has no surface here at
all - it inserts into `jobs` and `tasks` and cannot create a `scheduled_jobs` row. And the acceptance
criterion "the STARTING work set is bounded as a consequence" is false unqualified: the bound is
(rows existing at landing) + owners x cap, grandfathering leaves the first term untouched, and the
owner population is itself unbounded under `RELAY_ALLOW_SELF_REGISTER`.

**The DURATION question this item raised is settled OUT, deliberately, and is not claimed here.** A
count cap bounds the starting work set only; the paged sweep takes a fresh snapshot per page, so a
row inserted above the cursor joins mid-pass. What would bound duration is a wall-clock deadline,
filed as [[feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep]] and cited from the sweep
header. A second unbounded pre-listener read found while checking this is filed as
[[bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded]].

Review found the cap failed OPEN on a legal server setting: `pool.Begin` inherited
`default_transaction_isolation`, and under `repeatable read` the count never saw the competitor's
row - 3 rows at a cap of 2, no error and no log line. The transaction now pins READ COMMITTED where
it is relied on. Review also found two lock mutants that survived every test, including moving the
lock below the count; both are killed by a concurrent guard in a lane CI runs.
