---
title: A repaired p4 client is rebuilt from defaults, silently losing every client_template field
type: bug
status: open
created: 2026-09-04
priority: low
source: Phase 4 re-verify lens on the perforce client-path slice, confirmed by the implementing engineer
---

# A repaired p4 client is rebuilt from defaults, silently losing every client_template field

## Summary

`CreateStreamClient` runs on every `Prepare`, which is what repairs a workspace whose p4 client
spec was deleted while its registry row survived. The caller-supplied `client_template` is applied
only on the COLD path, so the repair runs `p4 client -o -S <stream> <name>` with no `-t`. When the
client really is gone, that returns a fresh DEFAULT spec plus the stream view - so `Options`,
`SubmitOptions`, `LineEnd` and anything else the template contributed are silently dropped, and
the workspace is then re-synced through the degraded client.

## Context

Cold-only template application is deliberate and should stay: workspaces are keyed on the stream
alone and `BaselineHash` does not include the template, so two tasks with different templates hash
identically, are admitted `ModeShared`, and would otherwise flip the client spec against each other
with one of them possibly mid-sync.

So the fix is not "re-apply `-t` on the warm path" - that reopens the flip. It is a keying or
hashing question: either record the template on `WorkspaceEntry` and re-apply it on repair only,
or make the template part of what distinguishes one workspace from another.

`BaselineHash` is a cross-process contract - `scheduler.BaselineHashFromAPISpec` computes the same
function server-side for warm-workspace affinity scoring - so changing it re-syncs every warm
workspace in the fleet once. That cost is the decision, not the code.

## Proposal

Record the template on `WorkspaceEntry` and pass it to `CreateStreamClient` when the workspace is
cold OR when the client is being repaired. Detecting "being repaired" needs a signal the code does
not have today; the cheapest one is probably the `DirtyDelete` flag plus a client-existence probe.

## Acceptance / Done When

- A workspace whose client spec was deleted is repaired with its template fields intact.
- Two concurrent tasks with different templates cannot flip a shared client's spec.
- A test references `client_template` end to end; none did before the slice that filed this.

## Related

- `internal/agent/source/perforce/perforce.go` (`Prepare`), `client.go` (`CreateStreamClient`)
- `internal/agent/source/perforce/perforce_warm_test.go`
