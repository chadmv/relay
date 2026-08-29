---
title: "`POST /v1/jobs` is not rate-limited, so per-request bounds cap size but not repetition"
type: bug
status: open
created: 2026-08-29
priority: medium
source: Half 2 of bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers, split out at its spec gate (2026-08-29)
---

# `POST /v1/jobs` is not rate-limited, so per-request bounds cap size but not repetition

## Summary

`internal/api/server.go` wraps exactly two routes in `RateLimit`: `POST /v1/auth/register` and
`POST /v1/auth/login`. Every other route, including `POST /v1/jobs` and `POST /v1/scheduled-jobs`, is
unwrapped, so an authenticated caller may repeat a submission at whatever rate the network allows.

This is the half of
[[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]] that bounds **repetition**. The
other half - bounding `len(JobSpec.Tasks)`, `len(TaskSpec.Commands)` and the job-wide command total in
`jobspec.Validate` - was shipped alone by the human's scope call, and it bounds only what ONE request
can ask for.

## Why this is filed rather than folded in

**The count-bounds slice's chosen numbers are conditional on this item existing.** Its spec argued
explicitly that count caps cannot be the DoS control at any value that does not also refuse real work
(a 2000-frame animation submitted one task per frame is a legitimate submission), so it chose the
generous set - `maxTasksPerJob = 5000`, `maxCommandsPerTask = 500`, `maxCommandsPerJob = 25000` - on
the stated premise that this item follows. If this item is closed as wontfix rather than implemented,
the tighter set (2000 / 200 / 10,000) becomes the better call and those constants should be revisited.
That dependency runs in this direction only: the caps do not depend on this code, they depend on this
decision.

## Repro / Symptoms

After the count-bounds slice, one authenticated 1 MiB `POST /v1/jobs` still buys roughly 275,000
subprocess spawns and 275,000 `task_logs` rows (25,000 commands x 11 attempts at `retries: 10`), and
nothing prunes `task_logs`. Repeat the request N times and every figure multiplies by N with no
server-side ceiling anywhere.

## Context

- `internal/api/ratelimit.go` is **per-IP via `RemoteAddr` only and deliberately does not trust
  `X-Forwarded-For`**. Behind a proxy every request presents the proxy's address, so a naive per-IP
  limit on an authenticated route either does nothing (all callers share one bucket that is sized for
  the fleet) or takes the whole fleet down together. This is the central design problem, not a footnote:
  the useful key for an authenticated route is probably the authenticated user, not the IP, and that is
  a different mechanism from the one that exists.
- The existing limiter is sized by `RegisterLimitN`/`RegisterLimitWin` and `LoginLimitN`/`LoginLimitWin`,
  so the configuration shape for a third bucket already exists.
- A per-user quota is a different product question from a per-user rate limit (a burst ceiling versus a
  budget over time). Decide which is being built before building it.

## Proposal

Sketch only - the keying decision above should be settled first, and it is a product call.

1. Decide the key: authenticated user id, IP, or both. Note that the anti-enumeration reasoning that
   makes `RemoteAddr` the right key for `login` does not transfer to an authenticated route.
2. Decide burst-ceiling versus budget-over-time, and whether `POST /v1/scheduled-jobs` needs the same
   treatment (a schedule is a repeating submission by construction, so a rate limit on creation bounds
   something different from what it bounds on `/v1/jobs`).
3. Wire it in `internal/api/server.go` beside the two existing wraps.

## Acceptance / Done When

- A burst above the threshold is refused and a normal submission rate is not, both proven.
- The chosen key is argued in a doc comment, including why the `RemoteAddr`-only reasoning from
  `ratelimit.go` does or does not apply to an authenticated route.
- If the key is not the authenticated user, the proxy behaviour is stated: what one shared source
  address does to unrelated callers.
- README documents the limit and its configuration.
- The count-bounds constants in `internal/jobspec/jobspec.go` are re-read against this outcome, per
  "Why this is filed rather than folded in" above.

## Related

- Parent: [[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]] (half 1, the counts)
- Source: `internal/api/server.go` (the two wrapped routes), `internal/api/ratelimit.go`
- Compounds with [[bug-2026-08-27-no-backoff-between-a-failed-task-and-its-redispatch]]
