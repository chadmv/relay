---
title: Design sync-spec exclusion paths without poisoning the shared workspace's have-list
type: idea
status: open
created: 2026-09-03
priority: medium
source: SDNM fork divergence analysis (relay_updates.md, PR-2), evaluated 2026-09-03
blocked_by: [bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync]
---

# Design sync-spec exclusion paths without poisoning the shared workspace's have-list

## Summary

There is no way to sync a stream while excluding a large subtree, and on real streams the
excluded subtree is the difference between a workspace that fits the volume and one that does not.
A fork of relay implemented exclusions as `-`-prefixed `source.sync[].path` entries, realised with
a have-list preempt (`p4 sync -k` on the excluded path before the real sync). The need is real and
the validation rules are worth keeping. **The mechanism must not be ported as-is**, because it
breaks relay's shared-workspace model silently.

## Context

Workspaces are keyed by stream and shared across tasks (README, "Workspace arbitration"). `p4 sync
-k` writes the client's have-list: the excluded files are recorded as present at the target
revision. A later task on the same workspace that does NOT exclude that path computes a different
baseline and triggers a re-sync, but `p4 sync` skips every file whose have-revision already
matches. The excluded files never arrive, nothing reports it, and the workspace is wrong for every
following task until someone runs a forced sync. The fork's deployment would not see this if every
job on that stream carries the same exclusion.

A second, smaller point: the fork's rule that an exclusion must follow an include is justified by
p4's left-to-right handling of command-line specs, which the have-list mechanism never uses. The
rule itself is right (an exclusion-only spec is meaningless); its stated reason is a fossil of a
design that was not shipped.

`p4 sync` ignores exclusionary filespecs on the command line, and a stream client's view is
derived from the stream spec so it cannot carry ad-hoc exclusion lines. Those two facts are why the
fork reached for the have-list, and any design here has to answer them.

## Proposal

A design item; run it through the brainstorming flow and write the spec before any code. The
questions the spec must settle:

1. **Mechanism.** The most promising shape: make the exclusion set part of the workspace identity,
   so `SourceKey` becomes the stream plus its canonical (sorted, deduplicated) exclusion list and
   differently-excluded tasks never share a client. Within one such workspace the have-list preempt
   is then safe, because every task on it excludes the same paths. Alternatives to weigh: a
   per-task `p4 sync -k` reversal on identity change (fragile), or a non-stream client with an
   explicit view (loses stream semantics). Whatever is chosen, the spec states what a task with a
   different exclusion set sees.
2. **Spec surface.** A `-` prefix on `path` overloads a string and reaches every consumer through
   validation alone. An explicit `exclude: true` on `SyncEntry` is self-describing for the SPA's
   job builder (the Perforce block is carved out to
   [[feature-2026-09-03-perforce-source-builder-in-the-new-job-builder]]) and the Python SDK's
   models, and it keeps `toClientPath` free of prefix handling. Prefer the field; record why.
3. **Validation**, in `jobspec.validateSourceSpec` (the single pipeline: REST, CLI, MCP,
   schedrunner and the SPA all go through it): the excluded path sits under the stream; it carries
   no revision; at least one include exists. Keep the fork's tests, reworded.
4. **Baseline and warm-workspace preference.** If exclusions live in `pf.Sync`, `BaselineHash`
   already covers them; confirm, and confirm the dispatcher's warm-workspace bias keys on the new
   `SourceKey` so a differently-excluded task is not treated as warm.
5. **Operator visibility.** One `[sync] EXCLUDE <path>` line per exclusion in the task log, and a
   `WARN` line on a preempt failure, as the fork does.
6. **Registry migration.** Existing entries keyed by bare stream must remain valid: a task with no
   exclusions keeps today's key.
7. Once shipped, extend [[feature-2026-09-03-classify-out-of-disk-p4-errors]]'s remedy to mention
   exclusions.

## Acceptance / Done When

- A spec exists that names the mechanism, the spec field, the workspace-identity rule and the
  behaviour of a mixed-exclusion pair of tasks on one stream, with the have-list hazard above as a
  test case.
- No task can observe a workspace missing files it asked for because a previous task excluded them.

## Related

- `internal/jobspec/jobspec.go` (`validateSourceSpec`), `internal/agent/source/perforce/perforce.go`
  (`Prepare`, `BaselineHash`, the registry `SourceKey`), `client.go`
- [[bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync]] - supplies `toClientPath`
- [[bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded]] - the other
  per-entry subprocess axis on the same spec
