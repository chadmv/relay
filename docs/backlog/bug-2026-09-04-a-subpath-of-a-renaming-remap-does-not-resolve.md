---
title: A sync path that is a strict subpath of a stream whose view renames a subtree does not resolve
type: bug
status: open
created: 2026-09-04
priority: medium
source: Phase 4 correctness lens on the perforce client-path slice; proven against the branch's own p4d fixture
---

# A sync path that is a strict subpath of a stream whose view renames a subtree does not resolve

## Summary

`toClientPath` is a string rewrite: strip the stream prefix, prepend `//<client>`. That assumes
the client namespace is an identity map of the stream namespace, which is true for a mainline or
development stream and for an `import+` remap (the imported files land at the same client-side
path), and FALSE for a `Remapped:` view that renames a subtree - the one case the client-path
slice exists to serve.

## Repro / Symptoms

Against the repository's own p4d fixture, whose virtual stream carries `Remapped: ... sub/...`:

    p4 -c <client> changes -m1 //<client>/readme.txt#head      -> out=""     (what toClientPath emits)
    p4 -c <client> changes -m1 //<client>/sub/readme.txt#head  -> "Change 3" (where the remap puts it)
    p4 -c <client> changes -m1 //test/virt/readme.txt#head     -> out=""     (the depot form, same)

With `rev: "#head"` the prepare fails with `resolve head for //test/virt/readme.txt: could not
parse ""`. With `@CL`, `@label` or `#N` it fails SILENTLY - see
[[bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero]].

## Context

**Not a regression.** The depot form failed identically before the client-path slice, so this is
an incomplete fix rather than a new defect, and the whole-stream form (`<stream>` or
`<stream>/...`) works because `//<client>/...` names the whole view however it is remapped.
README states the mechanism rather than claiming support, so nothing currently promises otherwise.

The slice's spec rejected "resolve the remap ourselves" on the grounds that it reimplements p4's
view resolution. `toClientPath` IS such a reimplementation - just an unconditional-identity one -
so that argument does not settle this; it only says the reimplementation must not be hand-rolled.

## Proposal

Let p4 do the resolution: `p4 -c <client> where <depot-or-stream-path>` returns the client path
for a path, through the client's own view. One round trip per sync entry, which is the cost to
weigh - a spec with many entries pays it per entry, and `Prepare` already runs per task.

Alternatively detect the non-identity case and refuse it with a clear error rather than emitting a
path that resolves to nothing. That needs the client view read anyway, so it is not obviously
cheaper than doing the translation.

Whichever is chosen, the integration test needs a subpath row: the fixture already has the remap,
so it needs only a file under a subdirectory in `//test/main`.

## Acceptance / Done When

- A strict subpath of a renaming remap either resolves correctly or fails with an error naming the
  cause, proven against p4d.
- The `TestToClientPath` row asserting `//s/x/sub/dir/...` -> `//<client>/sub/dir/...` is revisited:
  it currently encodes the naive rewrite as contract.

## Related

- `internal/agent/source/perforce/perforce.go` (`toClientPath`), `perforce_clientpath_test.go`
- [[idea-2026-09-03-sync-spec-exclusion-paths-design]] - also builds on `toClientPath`
