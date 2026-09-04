---
title: A per-owner cap on scheduled_jobs, the only bound on the boot sweep's duration
type: feature
status: open
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
touches an HTTP route, so no HTTP rate limit anywhere bounds a schedule's firing: one
`@every 1s` schedule is an uncapped job engine regardless. Reversing that decision is one wrap
line if a reason appears.

**The cap is load-bearing for the paging slice, which is why this item exists as its own file.**
`docs/superpowers/specs/2026-09-04-boot-sweep-keyset-paging.md` keyset-pages
`ValidateStoredSpecsOnStartup`, which bounds the sweep's peak MEMORY to one page. It does not
bound its DURATION: an actor with a million schedules still delays `ListenAndServe` by O(N)
round trips. Only a cap on N closes that, so the paging slice's doc comment cites this item by
filename. A decision conditioned on "the cap is coming" is unfalsifiable until the cap is a
findable item.

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
- The boot sweep's DURATION is bounded as a consequence, and the boot-sweep item's first
  acceptance criterion can be restated to say so.
- README documents the cap and its configuration.

## Related

- [[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]] - the parent; this is
  its surviving half 2
- [[bug-2026-08-29-post-v1-jobs-is-not-rate-limited]] - where the rate-limit half was decided OUT
- `internal/api/server.go` (`POST /v1/scheduled-jobs`, bare `auth`),
  `internal/api/scheduled_jobs.go` (`handleCreateScheduledJob`),
  `internal/schedrunner/startup_validation.go` (`ValidateStoredSpecsOnStartup`)
