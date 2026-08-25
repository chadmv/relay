---
title: The mutation harness does not assert where a mutation landed, and has produced false survivals
type: idea
status: open
created: 2026-08-26
priority: low
source: 2026-08-26 worker-delete slice - two false survivals in one session, six recorded instances across the project
---

# The mutation harness does not assert where a mutation landed

## Summary

Mutation testing is load-bearing on this project - it is how several guards were shown to be
decorative and how several "unkillable" claims were refuted. But there is no committed harness. Each
slice writes an ad-hoc script, and those scripts have now lied in at least six recorded instances.

Two of them happened in one session on 2026-08-26, and both were **false survivals** - the most
dangerous direction, because a survival reads as "this code is not covered" and invites someone to
add a test that was never needed, or to accept a guard that is actually load-bearing.

## Repro / Symptoms

Both 2026-08-26 failures had causes a harness can check:

1. **It patched the wrong function.** The script replaced the *first* occurrence of
   `s.sendCancelSignals(cancels)`. That string exists in both `handleDisableWorker` and the new
   `handleDeleteWorker`, so the edit landed in a function the test filter did not exercise. Reported
   SURVIVED. Re-run against the correct site with a line-number check: dies immediately.
2. **A narrowing `-run` filter hid the kill.** The same run used `-run TestDeleteWorker`, so the
   disable test that would have caught the misplaced edit never ran.
3. A third mutation produced a **compile error**, which is not a behavioural kill and must not be
   recorded as one.

Earlier recorded causes, same class: CRLF line endings silently defeating a `sed` (four consecutive
instances), and a mutation applied to a test-file literal rather than to the producer it was meant to
exercise.

## Proposal

A small committed harness under `scripts/`, not a per-slice script. It should:

- Take a **file and line**, not just a string, and **assert the mutation landed there** - print the
  before/after line and fail if the file is unchanged. This alone closes the CRLF class and the
  wrong-occurrence class.
- **Refuse a narrowing `-run` filter by default**, or require an explicit flag that is recorded in
  the result. A mutation's blast radius is not known in advance, which is the point of running it.
- Distinguish **compile error**, **survived**, and **killed by <test>** as three outcomes, never two.
- Require a **control** in every batch - a mutation that must die - and fail the batch if it does not,
  since uniform results mean a broken harness rather than good coverage.
- Restore cleanly, and refuse to run against a dirty tree or a shared worktree (two lanes collided on
  one scratch path earlier in this project's history).

Not proposed: a full mutation-testing framework. The value here is a correct applied-check and honest
outcome accounting, not coverage scoring.

## Acceptance / Done When

- A committed harness exists and the next slice uses it instead of an ad-hoc script.
- A mutation that fails to apply is reported as **not applied**, never as survived.
- A compile error is a distinct outcome from a survival.
- A batch without a control fails.

## Related

- `docs/retros/2026-08-26-worker-delete.md` - the two false survivals
- `docs/retros/2026-08-25-auto-enroll-guards.md`, `docs/retros/2026-08-25-handler-pool-seam.md`
- [[idea-2026-08-25-no-documented-working-local-race-lane]] - the same shape: a tool whose failure
  mode is indistinguishable from a real result
