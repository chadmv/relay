---
title: ListGraceCandidates is a SELECT DISTINCT with no LIMIT on the pre-listener boot path
type: bug
status: open
created: 2026-09-10
priority: low
source: Found by the pre-listener query enumeration in the ReconcileOnStartup paging spec (PR #208), deliberately out of that slice
---

# ListGraceCandidates is a SELECT DISTINCT with no LIMIT on the pre-listener boot path

## Summary

`ListGraceCandidates` is a `SELECT DISTINCT` over `workers` joined to `tasks` with no LIMIT, and it
runs during boot ahead of `srv.ListenAndServe()`. It is the one pre-listener read that no prior item
had named.

## Context

Found by enumerating every statement on the gating path while specifying the reconcile's paging, not
by a symptom. The enumeration is recorded in
`docs/superpowers/specs/2026-09-10-reconcile-on-startup-paging.md`.

**Severity is genuinely lower than its two siblings, and the reason is worth keeping** so nobody
re-prioritises it on shape alone: it selects three narrow columns over a fleet-sized population,
where the schedule statements carried a `job_spec` column bounded only by `maxBodyBytes` at 1 MiB per
row. The bad case here is a large fleet with many tasks in flight at the moment the server stopped,
not a megabyte per row.

It is the seed for the worker grace timers, so it cannot simply be dropped or deferred past the point
where those timers must exist.

## Proposal

Sketch only. Page it the way the two schedule statements now are, or establish that the fleet-sized
bound is acceptable and say so where the statement is read. Either way the decision should be written
down rather than left as the absence of a LIMIT.

## Acceptance / Done When

- The boot's peak memory for this statement does not grow without bound with fleet size, or the bound
  that makes it acceptable is stated at the statement.
- The grace timers are still seeded before anything can requeue a task.

## Related

- `internal/store/query/` (`ListGraceCandidates`), `cmd/relay-server/main.go` (the seeding call)
- [[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]]
- `docs/superpowers/specs/2026-09-10-reconcile-on-startup-paging.md` - the enumeration
