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
slice writes an ad-hoc script, and those scripts have now lied in at least ten recorded instances
(see Notes; the count is maintained there, not here).

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
- The applied-check is a diff against a saved copy taken immediately before the test runs,
  not a string count performed once at batch start - so an edit invalidated in between is
  reported as not applied.
- A batch without a control fails.

## Related

- `docs/retros/2026-08-26-worker-delete.md` - the two false survivals
- `docs/retros/2026-08-25-auto-enroll-guards.md`, `docs/retros/2026-08-25-handler-pool-seam.md`
- [[idea-2026-08-25-no-documented-working-local-race-lane]] - the same shape: a tool whose failure
  mode is indistinguishable from a real result

## Notes

**2026-09-01, per-task-identity-env-vars slice - a tenth instance, and the mechanism is new.** Every
instance above is an anchor that NEVER matched: CRLF against a `sed`, a mangled backslash, a `$` that
does not match before `\r\n`, a unique string in the wrong function. This one **matched when it was
written and went stale afterwards.**

A fix round's battery ran a `git stash` round-trip partway through, to check `gofmt` at HEAD. The
round-trip re-materialised the working copy with CRLF, so the harness's LF-only anchors stopped
matching from that point on and every subsequent mutation silently no-opped. It reported three
mutants SURVIVING against a guard that had just been written specifically to kill them - the
false-survival direction this item names, on the worst possible subject. The engineer caught it from
its own applied-check and re-ran; the conductor then re-ran two of the three independently in an
isolated tree with the application verified by file diff, and both died.

**What this changes about the Proposal, and it is not covered by the current wording.** "Assert the
mutation landed there" is stated as a property of the edit. It has to be a property of the edit
**at the moment the test runs**. An `assert count(anchor) == 1` performed once at batch start is
necessary and insufficient in exactly the way the wrong-occurrence check was: anything that touches
the working tree between the check and the run invalidates it, and `git stash`, `git checkout`, a
formatter, and a second agent in a shared worktree all qualify. The check that survives this is a
DIFF against a saved copy immediately before the run, not a string count performed earlier - and the
same diff should gate the restore, rather than assuming the write-back landed.

This also sharpens the item's own argument for a committed harness over per-slice scripts. Each of
the ten instances was a different script making a different mistake; the CRLF ones were fixed one at
a time, in one script at a time, and the fix never propagated because there was nothing to propagate
it into.

**2026-08-25, windows-crlf-log-lines slice - three more instances, and the scope is wider than this
item states.** Three independent actors in one slice each lost mutations to a silently-unapplied
edit, all reporting the false-survival direction this item names:

- The correctness review lens lost four mutations to `sed`/`perl` line anchors not matching `\r\n`.
- The backend engineer lost several to backslash sequences mangled through shell heredocs before
  reaching Python - a different mechanism from CRLF, identical symptom.
- The conductor lost one to `perl -0pi -e 's/^status: open$/.../m'`, where `$` does not match before
  `\r\n`.

**The conductor's instance is the one that matters for scoping.** It was not a mutation at all - it
was `/backlog close` stamping frontmatter during ordinary housekeeping. The file was silently left
unchanged and the close would have committed an item still marked `status: open`, caught only by the
skill's own verify step. So the failure mode is not "the mutation harness does not assert where it
landed"; it is **"any line-anchored byte edit on this CRLF tree can silently no-op"**, and mutation
testing is merely where it does the most damage, because there the silence is indistinguishable from
a result.

That argues the harness this item proposes should expose its applied-check as something callable
outside mutation runs, or that the rule belongs somewhere every scripted edit sees it rather than
only in a mutation harness. Worth deciding before building.

Counting instances: this item recorded six across the project; these three bring it to nine, and one
of them is outside the harness's scope entirely.

## Related

- [[bug-2026-08-25-windows-crlf-log-lines-render-blank]] - the slice these three came from, itself a
  CRLF bug, which is a coincidence worth enjoying but not reading anything into
