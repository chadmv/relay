---
title: "`tasks` and `commands` counts are unbounded per-request multipliers on an unrated-limited route"
type: bug
status: open
created: 2026-08-28
priority: medium
source: Security lens of the Phase 4 review of the retry-bounds slice (2026-08-28)
---

# `tasks` and `commands` counts are unbounded per-request multipliers on an unrated-limited route

## Summary

Bounding `retries` removed the largest multiplier a single job spec could name, but it was never the
only one. `len(JobSpec.Tasks)` and `len(TaskSpec.Commands)` are both unbounded, both multiply the
work a single request creates, and `POST /v1/jobs` carries no rate limit - so the leverage the
retry-bounds item described ("one small, well-formed request produces unbounded work") is **reduced
rather than retired**.

## Repro / Symptoms

Measured by the security lens during the retry-bounds review, staying inside the 1 MiB body limit:

- **Commands axis.** ~130,000 commands in one task at roughly 8 bytes per `["true"],` entry, last one
  failing so the task retries. `internal/agent/runner.go` calls `sendStepMarker` once per command
  unconditionally, and each marker emits a `task_logs` chunk. With `retries: 10` that is 11 attempts:
  **~1.43 million `task_logs` rows and ~1.43 million subprocess spawns**, holding one worker slot for
  roughly 20 to 30 minutes.
- **Tasks axis.** ~23,800 minimal tasks per 1 MiB, times 11 attempts, is ~262,000 dispatch cycles -
  spread across every worker slot in the fleet rather than concentrated on one.

Nothing prunes `task_logs`.

## Context

Found by the security lens of the Phase 4 review of
`docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md`, while answering "what does
the worst-case authenticated request still cost after this fix". Deliberately kept out of that slice:
the human's gate decision was to ship the retry-bounds item alone, and bounding two more fields is a
separate product call.

**What was checked and found NOT to be a problem, recorded so nobody re-derives it.** The security
lens examined every other field in the same structs before reporting these two:

- `Source` is constrained by `validateSourceSpec` - type, stream prefix, per-entry rev against four
  regexes, path containment under the stream, positive unshelve changelists, and a client-template
  regex. The `Sync` and `Unshelves` counts are unbounded but each entry is bounded work.
- `DependsOn` is checked against the task set and the graph is cycle-checked by Kahn's algorithm in
  O(V+E). No algorithmic blowup.
- `Labels`, `Env` and `Requires` are unbounded maps but are marshalled once into JSONB. Linear in
  body size, not multipliers.

The only ceiling on any of it is `maxBodyBytes = 1 << 20` in `internal/api/server.go`. `RateLimit` is
applied to register and login only; no other route is wrapped, so repetition is free.

## Proposal

Sketch only. Two independent halves, either shippable alone:

1. **Bound the counts at the single validation point.** `len(spec.Tasks)` and `len(ts.Commands)` in
   `jobspec.Validate`, beside `maxRetries` and `maxTimeoutSeconds`, inheriting all four ingest paths
   for free. Both caps are product decisions and should be argued, not adopted - and note the same
   retroactivity hazard applies: `schedrunner.fireOne` re-validates stored specs on every fire, so a
   bound below an existing stored spec's count stops that schedule firing.
2. **Rate-limit `POST /v1/jobs`.** Independent of the caps and arguably the more general fix, since
   it bounds repetition rather than per-request size. Note `internal/api/ratelimit.go` is per-IP via
   `RemoteAddr` only and does not trust `X-Forwarded-For`, which constrains how useful it is behind a
   proxy.

## Acceptance / Done When

- A spec exceeding whichever count bound lands is rejected at submission with a per-task or per-job
  error naming the limit, proven by a test RED against today's code, and asserted through at least
  one real entry point.
- A spec at exactly the boundary is still accepted.
- If the rate limit lands: a burst above the threshold is refused, and a normal submission rate is
  not, both proven.
- The README rows for `tasks` and `commands` state whatever bound is chosen, and the PR states the
  retroactivity consequence for stored schedules.

## Related

- Source: `internal/jobspec/jobspec.go` (`Validate`, and the two constants added 2026-08-28),
  `internal/api/server.go` (`maxBodyBytes`, and the routes `RateLimit` does and does not wrap),
  `internal/agent/runner.go` (`sendStepMarker`, one `task_logs` chunk per command)
- The slice that bounded the first two fields and measured this residual:
  [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]]
- Compounds with [[bug-2026-08-27-no-backoff-between-a-failed-task-and-its-redispatch]]: with no
  backoff, the 11 attempts run back to back rather than spread over time.
- Invariant in contact: Single job-spec pipeline - a count bound belongs in `jobspec.Validate` for the
  same reason the retries bound did.
