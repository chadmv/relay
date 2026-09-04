---
title: Replace README's hand-maintained log-site and key counts with a test-backed census
type: idea
status: open
created: 2026-09-03
priority: low
source: round-4 re-verify of the prepare-failure-visibility batch, 2026-09-03
---

# Replace README's hand-maintained log-site count with a guard

## Summary

README's ingest-log-budget bullet states the number of per-message log sites and the number of
budget keys, and builds an argument on the arithmetic ("so a connection can suppress its own
other N"). The numbers are correct at HEAD - verified 2026-09-03: nine `lim.allow(logKey{...})`
sites in `internal/worker/handler.go` and nine kinds - but they are hand-maintained, and the next
kind falsifies a paragraph rather than a digit.

## Context

This is the residual left deliberately by the batch that created the ninth kind. That batch's own
history is the argument for fixing it structurally rather than by re-counting:

- Adding one kind falsified six counts across four files (`ingest_log_limiter.go` twice,
  `ingest_log_limiter_test.go`, `taskstatus_fence_counters_test.go` twice - one inside a `require`
  failure message - `ingest_log_counters.go`, README, and a ROADMAP line citing a backlog item).
- Two rounds fixed some of them, and round 3 corrected README's count while leaving the code's,
  so the two disagreed for a commit.
- The in-code census was deleted rather than corrected, per CLAUDE.md's ban on counts of other
  code. README was corrected instead, because its bullet's argument is arithmetic and could not
  simply drop the number.

## Proposal

Add a guard that asserts the count of `lim.allow(logKey{...})` call sites in
`internal/worker/handler.go` equals `kindCount - 1`, in the same shape as the existing
`TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished` (which parses the package). Then
rewrite README's bullet to cite that test by name instead of stating either number, and restate
the suppression argument as a property rather than as arithmetic.

Note what the existing guard does **not** establish, since the new one must not inherit the gap:
it asserts `literalKinds` is a subset of `declared`, never the converse, and it is blind to an
*unbudgeted* `log.Printf`, which has no `logKey` literal to inspect.

## Acceptance / Done When

- Adding a tenth kind, or a tenth budgeted site, reddens a test rather than silently falsifying
  prose.
- README's bullet contains no cardinal that has to be maintained by hand.

## Related

- `internal/worker/ingest_log_limiter.go`, `internal/worker/ingest_log_counters_test.go`
- `README.md` - the `ingest_log_budget` bullet
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the guard must land in a lane CI runs
