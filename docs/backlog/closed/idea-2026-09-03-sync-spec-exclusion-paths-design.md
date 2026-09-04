---
title: Design sync-spec exclusion paths without poisoning the shared workspace's have-list
type: idea
status: closed
created: 2026-09-03
priority: medium
source: SDNM fork divergence analysis (relay_updates.md, PR-2), evaluated 2026-09-03
blocked_by: [bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync]
closed: 2026-09-04
resolution: fixed
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

## Resolution
The spec is written: `docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md`. It settles
all seven questions this item posed and refutes six of its own premises against the tree.

The first acceptance bullet is met. **The second one is not, and it was never a property a
document could have** - "no task can observe a workspace missing files it asked for" is a
property of shipped code. It moves intact to
[[feature-2026-09-04-implement-sync-spec-exclusion-paths]], along with the acceptance test the
spec designs and the mutation that must kill it. This item is closed on its own stated scope:
"A design item; run it through the brainstorming flow and write the spec before any code."

The framing correction that drove the spec: this item presents folding the exclusion set into
`SourceKey` as one mechanism among three. It is not a mechanism, it is a precondition every
candidate needs, because a have-list preempt writes the shared client's have-list and a
view-based exclusion writes the shared client's spec, which `CreateStreamClient` rewrites on
every `Prepare`.

Six premises of this item refuted, each checked against the symbol:

1. `BaselineHash` does NOT already cover exclusions. It builds `entry{path, rev}` and hashes
   nothing else, so an exclusion field hashes identically to its absence.
2. The dispatcher's warm bias keys on nothing shared. `selectWorker` compares `ws.SourceKey` to
   `taskSrc.Stream` directly; there is no key function to confirm, one must be created.
3. `exclude: true` is not self-describing for the clients this item cites. The SPA source builder
   does not exist yet, and the Python SDK's `Sync` carries `ConfigDict(extra="forbid")`, so the
   field is a hard SDK rejection where a `-` prefix would pass through `path: str` untouched.
4. A WARN line on preempt failure is refused. A warning inside a multi-hour sync log is read
   after the volume has already filled; preempt failure fails the prepare instead.
5. Extending the out-of-disk remedy to mention exclusions is refused as written, because under
   this mechanism adding an exclusion creates a second full-size workspace.
6. The replacement for the fossil include-ordering rule is weaker than needed. Every exclusion
   must be covered by exactly one include at one revision, because the preempt's revision is
   defined by the covering include.

The spec also states the strongest argument against the whole design rather than burying it: a
mixed-exclusion stream on one agent ALWAYS costs more disk than today, and the overflow converts
to sweeper eviction churn that this repository cannot measure. Five follow-on items were filed.
