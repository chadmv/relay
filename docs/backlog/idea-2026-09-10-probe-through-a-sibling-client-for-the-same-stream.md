---
title: Probing exclusion paths through an existing sibling client would close the warm case without minting
type: idea
status: open
created: 2026-09-10
priority: low
source: Route deferred with its premise named, by the workspace exclusion-set ceiling spec (PR #211)
---

# Probing exclusion paths through an existing sibling client would close the warm case without minting

## Summary

The exclusion-set ceiling caps how many p4 client specs one author can cause an agent to create; it
does not stop the first one for a bogus spec from being created at all. The reason is that the
resolve-probe uses a client-form filespec, so a not-in-client-view path is only detectable THROUGH a
client's view - the client must exist before the probe can run.

If the agent already holds a client for the same stream, that client's view might answer the probe
without minting anything.

## Context

Deferred rather than rejected, and the premise is named so it is falsifiable: **it rests on an
untested assumption about p4 view equivalence.** Two clients on the same stream with different
exclusion sets do not have identical views - that is the whole reason they are separate workspaces -
so whether a sibling's view answers the question correctly for a path the new spec excludes is exactly
what nobody has measured.

It would close only the WARM case, where a sibling for that stream already exists. The first prepare
on a stream has no sibling and still mints before it can refuse.

The rejected alternatives are recorded in the spec with their reasons, and one is worth not
re-litigating: rolling back the cold mint on probe failure was refused because the preempt loop also
runs on a warm workspace whose baseline moved, so one typo could destroy a populated multi-terabyte
workspace.

## Proposal

Measure the premise first, against real p4d: create two clients on one stream with different
exclusion sets, and check whether a `files` query through client A correctly answers for a path only
B excludes. **If view equivalence does not hold, close this item rather than designing around it.**

## Acceptance / Done When

- The view-equivalence assumption is measured and recorded either way.
- If it holds, a warm prepare with a bogus exclusion set mints no client spec and no directory.
- If it does not hold, that is written down so the route is not proposed again.

## Related

- `internal/agent/source/perforce/perforce.go` (`Prepare`, the probe and preempt loop),
  `internal/agent/source/perforce/client.go` (`CreateStreamClient`, the client-form filespec)
- [[idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates]] - closed; deferred this
