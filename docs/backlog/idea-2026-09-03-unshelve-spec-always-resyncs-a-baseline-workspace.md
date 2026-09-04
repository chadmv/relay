---
title: An unshelve-bearing spec always re-syncs, so the baseline-match fast path is unreachable for it
type: idea
status: open
created: 2026-09-03
priority: low
source: Lane B of the prepare-failure-visibility batch, found when new progress lines made a silent re-sync audible, 2026-09-03
---

# An unshelve-bearing spec always re-syncs

## Summary

`tryAdmit` sets `needsExclusive := req.WorkspaceExclusive || len(req.Unshelves) > 0`, and an
exclusive admission makes `needsSync` unconditionally true in `Provider.Prepare`. So for any spec
carrying `unshelves`, the baseline-match arm - the fast path that skips the sync when the workspace
is already at the requested baseline - is **unreachable**. Whether that is correct is undecided;
this item is to decide it.

## Context

Found by accident, and the way it was found is the interesting part. `TestPerforce_E2E_SyncAndUnshelve`
asserted `require.Empty(progress2, "second Prepare with same baseline should not trigger re-sync")`.
That assertion could never have observed the property it named: the spec under test carries an
unshelve, so `needsSync` was always true and a sync always ran. It passed only because `p4 sync -q`
prints nothing on an up-to-date single-file workspace.

Adding `[sync] starting` / `[sync] complete` progress brackets made the previously silent re-sync
audible and turned the test red. It was replaced with the observable truth (one start bracket, one
complete bracket, no `[recover]` line, and a total-output bound), which is strictly stronger - but
that replacement documents the behaviour rather than deciding whether the behaviour is right.

## Proposal

Decide, and record the reason either way:

- **If the re-sync is necessary** - because an unshelve mutates the workspace and the baseline
  hash no longer describes it - say so where `needsExclusive` is set, and the fast path's
  unreachability for these specs stops being a surprise.
- **If it is not** - the workspace was reverted at `Finalize`, so a baseline match may still be
  valid - then the exclusive-mode decision and the sync decision should be separated, since they
  are currently one flag answering two questions.

The cost of getting this wrong in the current direction is a full re-sync per unshelve-bearing
task on an already-warm workspace, which on a large stream is the expensive case.

## Acceptance / Done When

- The coupling between `needsExclusive` and `needsSync` is either justified in place or separated.
- Whichever way it goes, a test observes the sync actually happening or not - not the absence of
  output from a `-q` invocation.

## Related

- `internal/agent/source/perforce/workspace.go` - `tryAdmit`, `needsExclusive`
- `internal/agent/source/perforce/perforce.go` - `Prepare`, `needsSync`, the baseline-match arm
- `internal/agent/source/perforce/perforce_integration_test.go` - `TestPerforce_E2E_SyncAndUnshelve`
- [[bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded]] - the other unshelve cost
