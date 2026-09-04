---
title: p4 sync reports "file(s) not in client view" on stderr and exits zero, so a task syncs nothing and reports success
type: bug
status: open
created: 2026-09-04
priority: high
source: Phase 4 security and correctness lenses on the perforce client-path slice; measured against a live p4d
---

# p4 sync reports "file(s) not in client view" on stderr and exits zero, so a task syncs nothing and reports success

## Summary

`execRunner.Stream` captures stderr into a buffer that is read ONLY inside
`newP4CommandError`, i.e. only when `cmd.Wait()` returns non-nil. `p4 sync` exits **zero**
when a path matches nothing, so the whole "nothing matched" family is discarded: the
operator's progress log reads `[sync] starting: 1 path(s)` then `[sync] complete`, `Prepare`
returns a handle, the baseline is recorded as satisfied, and the task's commands run in an
empty workspace. The next task with the same spec then skips the sync entirely.

## Repro / Symptoms

Measured against a live p4d, on a stream whose view remaps a subtree:

    p4 -c <client> sync -q --parallel=4 //<client>/readme.txt@2
    stderr: //<client>/readme.txt@2 - file(s) not in client view.
    exit=0

    p4 -c <client> sync -q --parallel=4 //<client>/sub/...@2 //<client>/readme.txt@2
    stderr: //<client>/sub/...@2 - no file(s) at that changelist number.
    stderr: //<client>/readme.txt@2 - file(s) not in client view.
    exit=0                                <- with NOTHING synced

The same family covers `- no such file(s).` and `- no file(s) at that changelist number.`

## Context

Not introduced by the client-path slice - the depot form behaved identically - but that slice
made it load-bearing, because addressing through the client view is what turns a resolution
failure into a match failure. `#head` revs fail closed by a different route (`p4 changes -m1`
exits 0 with EMPTY output, which `ResolveHead` turns into `could not parse ""`), so the silent
path needs a spec pinned with `@CL`, `@label` or `#N` - all three of which
`jobspec.validateSourceSpec` accepts.

For a render farm the failure mode is a green task that built nothing, which the pipeline has
no way to detect.

## Proposal

Decide between two shapes; the second is narrower and probably right:

- After `cmd.Wait()` returns nil in `SyncStream`, scan the captured stderr and fail the prepare
  when any line matches p4's "nothing matched" family. Cheap, but it is a phrase match, which
  is the shape [[bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr]] is about.
- Assert positively that each sync spec resolved to at least one file, via `p4 -ztag sync -n`
  or a `p4 -c <client> files -m1 <spec>` probe before the sync. Structured output, no phrase
  matching, and it states the property the operator cares about.

Either way, decide what happens on a DELIBERATELY empty path - a spec whose subtree is legitimately
empty at that changelist is not obviously an error, and refusing it would be a new failure for
specs that work today.

## Acceptance / Done When

- A sync whose paths match nothing fails the prepare rather than recording a satisfied baseline.
- Proven against p4d with a path that matches nothing, asserting the task does not report success.
- The deliberately-empty case is decided in writing, either way.

## Related

- `internal/agent/source/perforce/client.go` (`execRunner.Stream`, `SyncStream`)
- [[bug-2026-09-04-a-subpath-of-a-renaming-remap-does-not-resolve]] - the case that surfaced it
- [[bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr]]
