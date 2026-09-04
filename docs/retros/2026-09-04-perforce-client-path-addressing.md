---
date: 2026-09-04
topic: perforce-client-path-addressing
branch: claude/pr-merging-session-65b658
range: a3d0f9a37f61a31a0b9df4c17869e2f9d1950ccf..3e367fc23cb66f77ada6f6e46c4fd20a6a907198
---

# Session Retro: 2026-09-04 - Perforce Client-Path Addressing

**TL;DR:** Relay syncs a Perforce workspace before running a task, and it was asking Perforce for
files by their depot name. For most streams that works, but a "virtual" or remapped stream is a
view onto files that live somewhere else, so the depot name points at nothing and every such task
failed before it started. This session switched relay to ask by the workspace's own name instead,
which resolves for every stream type, and proved it against a real Perforce server. The review was
the valuable part: it found that the reordering this required opens a window where a background
cleanup can delete the workspace out from under the task being set up, and that the fix as
originally written papered over that by half-repairing the damage and carrying on.

## Handoff

Autopilot iteration 2 of 4. Closed
[[bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync]], the head of the fork-upstreaming
batch in ROADMAP.md's Now after the `preparing` slice. Twenty-one commits: spec `e107ddc`, plan
`dfdcdc6`, seven implementation commits `f420e5b..0a092da`, nine review-fix commits `f049d65..e3d3371`,
a prose-only round `02f9167`, then the close and backlog work.

`toClientPath` rewrites a depot-form sync path into `//<client><rel>`, and `ResolveHead` gained
`client` (running `p4 -c <client> changes -m1`). `Prepare` is reordered: `MkdirAll` +
`CreateStreamClient` + `reg.Upsert`/`reg.Save` now run BEFORE head resolution, because head
resolution became a client-scoped call. `resolved`, `syncPaths` and `BaselineHash` stay keyed on the
DEPOT path - forced, not conservative: `scheduler.BaselineHashFromAPISpec` computes the same hash
server-side for warm-workspace affinity scoring and cannot know the agent's client name.

Five review findings changed the shipped design. A sweep can now complete inside the pre-Acquire
window, so `Prepare` REFUSES with a retryable error when its registry entry is gone rather than
half-repairing it (a missing entry proves a sweep completed; `Registry.Remove` has one production
caller, downstream of `DeleteClient` and a successful `RemoveAll`). The unconditional `MkdirAll`
forces a sync when the workspace directory was absent or empty. The warm path reconciles
`WorkspaceEntry.ClientName` and clears `DirtyDelete` in one `Registry.Mutate`. `client_template` is
applied only on a cold workspace, and `CreateStreamClient` now drops `AltRoots`. `ResolveHead` takes
no cwd at all.

Four lenses, two of them driving live p4d containers. Verify: `go test ./...` 22 packages;
`go vet` both tag sets; the p4d lane with real PASS lines for both E2E tests (35.7s + 34.0s, zero
skips); `-race` in the `golang:1.26` Linux container across all packages, zero data races.

Four items filed and one amended - see Recommended Backlog Items. The subpath gap and the
silent-sync class are the two that matter; neither is a regression, and README states the mechanism
rather than claiming support.

Next session starts at ROADMAP.md's Now, whose lead is now the p4 sync progress heartbeat - and note
it edits the same sync call site this slice just changed.

## What Was Built

- **`toClientPath`**, failing closed on a path not under its stream rather than emitting it
  unchanged, which is the defect it exists to fix.
- **`ResolveHead(ctx, client, path)`** running `p4 -c <client> changes -m1`, with no cwd.
- **The `Prepare` reorder**, plus the refusal, the forced re-sync, the warm reconciliation and
  cold-only template application that review added to it.
- **A p4d fixture with a `Type: virtual` child stream carrying a `Remapped:` line**, and
  `TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout`, which asserts the file lands
  in the remapped layout AND not flat. It is the only test that can prove any of this; the fake
  runner echoes whatever it is told.
- **Guards for three properties the design leaned on and nothing pinned**: that the client spec is
  re-read on a warm Prepare, that the registration guard is conditional, and that the cold-path
  registration reaches disk.

## Key Decisions

- **Refuse, do not repair.** The alternative was restoring all three artifacts a sweep destroys.
  Refusing is less code, provably correct from a single-caller argument, and produces an accurate
  early failure instead of a confusing late one.
- **`BaselineHash` is not changed.** It is a cross-process contract, and folding the template into
  it would re-sync every warm workspace in the fleet once.
- **The subpath gap is filed, not fixed.** `toClientPath` is a string rewrite and cannot resolve a
  view that renames a subtree. It is not a regression - the depot form failed identically - so
  redesigning it mid-slice would have been unreviewed scope. README states the mechanism instead of
  claiming support.
- **The template is applied cold-only**, accepting that a repaired client loses its template fields,
  because re-applying it warm reopens a last-writer-wins race between two `ModeShared` peers.

## What Went Wrong and What Changes

Ledger: the previous retro's entries were all promoted or already-stamped, so none are carried.
Promoted lessons that fired: [[reference_the_unguarded_copy_is_in_another_language]] was written
last iteration and fired immediately here in a different medium - the README sentence the branch
added was the one new false claim in the whole slice;
[[feedback_verify_tree_not_subagent_claims]] (every report re-checked; the fix round's own diff was
re-reviewed and yielded two more findings); [[reference_verify_the_mutation_applied]] (every lens
and the engineer ran a control set alongside their mutations);
[[reference_wrong_prose_is_the_dominant_defect]] (again the dominant finding class);
[[reference_a_guard_must_not_derive_its_expectation_from_its_subject]] (the `toClientPath` table's
`want` values are literals, deliberately);
[[reference_uniqueness_claim_is_about_the_complement]] (three separate complement-claims were
deleted this round). [[feedback_same_finding_across_parallel_lanes]] was exercised: the swept-
workspace defect was reproduced independently by two lenses, and one engineer owned the fix.

- **The fix the item prescribed opened a race the item never mentioned, and only a fresh derivation
  found it.** Moving registration above `ws.Acquire` leaves the workspace with zero holders for the
  whole window, so a sweep can complete AND release inside it and the post-Acquire re-check then
  reads clear. At HEAD this was impossible: the registration sat after `Acquire`, so a holder always
  existed and every eviction failed its holder check. The spec found it by re-deriving the ordering
  argument from scratch rather than porting the item's.
  -> **What changes:** when a change REORDERS an acquisition, re-derive the exclusion argument from
  the new order rather than checking that the old argument still reads true. The old argument is
  about a sequence that no longer exists, so it cannot be falsified by inspection - only by
  rebuilding it. (promoted to [[reference_reordering_an_acquisition_invalidates_its_exclusion_argument]])

- **A partial repair is worse than no repair, because it silences the symptom that would have
  reported the damage.** The first fix restored the registry row a sweep had removed and carried on -
  leaving `Prepare` returning a handle whose directory and p4 client were both deleted. Two lenses
  reproduced it the same way: by adding one `require.DirExists` to the shipped test, which then
  failed. The test could not see it because the fake runner never touches the filesystem.
  -> **What changes:** when a fix repairs damaged state, enumerate everything the damaging path
  destroys and either restore all of it or refuse. A repair that restores a strict subset converts a
  loud failure into a quiet wrong one, and the test that covers the repair will assert only the
  subset the author was thinking about. (promoted to [[reference_repair_all_of_it_or_refuse]])

- **Making a call unconditional turned a loud failure into a silent success.** `MkdirAll` moved from
  cold-only to every-Prepare, so a surviving registry row whose directory was gone got an EMPTY
  directory recreated, matched its baseline, skipped the sync, and ran the task in an empty tree
  reporting success. Before, the missing directory made the command fail to start.
  -> **What changes:** when a guarded call becomes unconditional, ask what the guard was preventing
  on the path that now reaches it - not just what the call now enables. Here the guard was the only
  thing making a corrupt state observable. (promoted: extends
  [[reference_backstop_recreates_the_defect]])

- **The slice's own fix did not cover the general case, and the branch said it did.** `toClientPath`
  assumes the client namespace mirrors the stream namespace, which is false exactly for a renaming
  remap - the case the slice exists to serve. Proven against the branch's own p4d fixture. The code
  was no worse than before; the README sentence the branch ADDED was the new defect, and it was
  unqualified.
  -> **What changes:** when a fix is partial, the documentation written in the same commit is where
  the partiality becomes a lie. State the mechanism, never the coverage - a mechanism sentence stays
  true as the coverage grows, and a coverage claim needs re-auditing every time anything changes.
  (promoted to [[reference_document_the_mechanism_not_the_coverage]])

- **A prescribed remedy would have authored a fresh false claim, and the engineer refused it.** The
  re-verify asked for a comment attributing the handle-ordering property to "the test that pins it".
  No test pins it - the named test asserts only the empty cwd. Writing the attribution would have
  been exactly the defect the prose round existed to remove.
  -> **What changes:** a review finding that prescribes NEW prose is prescribing a claim, and the
  implementer verifies it like any other. State the finding as what is wrong; leave what to write to
  whoever can check it. (already in [[reference_accurate_item_wrong_remedy]] - stamping the
  prose-prescription trigger)

- **A test that HANGS under mutation is a guard that cannot be trusted.** Removing the registration
  guard made the sweeper-claim tests deadlock for the full timeout rather than fail, because the
  mutant refreshed `LastUsedAt` so the sweeper's age pass selected nothing and the test's gate never
  fired. It now fails in 20s with a message naming that cause.
  -> **What changes:** when a test waits on a gate another component must open, give the wait a
  bounded failure that names what did not happen. A hang under mutation is indistinguishable from
  infrastructure trouble, and it is an expensive way to learn a guard works. (promoted to
  [[reference_a_gated_test_needs_a_bounded_failure]])

## Recommended Backlog Items

Backlog intake, not a priority order - ROADMAP.md orders the work.

- See [`bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero`](../backlog/bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero.md) - p4 exits zero when a sync path matches nothing and relay discards stderr on exit zero, so the task syncs nothing and reports success
- See [`bug-2026-09-04-a-subpath-of-a-renaming-remap-does-not-resolve`](../backlog/bug-2026-09-04-a-subpath-of-a-renaming-remap-does-not-resolve.md) - `toClientPath` is a string rewrite, not a view resolution
- See [`bug-2026-09-04-a-repaired-p4-client-loses-its-template-fields`](../backlog/bug-2026-09-04-a-repaired-p4-client-loses-its-template-fields.md) - a repaired client is rebuilt from p4 defaults
- See [`bug-2026-09-04-a-warm-workspace-in-active-use-ages-out`](../backlog/bug-2026-09-04-a-warm-workspace-in-active-use-ages-out.md) - a warm workspace reused at its baseline never refreshes `last_used_at`
- See [`bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr`](../backlog/bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr.md) - amended: its stated repro cannot fire, a second channel was found, and one of its two remedies does not close it

## Files Most Touched

- `internal/agent/source/perforce/perforce.go` - `toClientPath`, the reorder, the refusal, the
  forced re-sync and the warm reconciliation.
- `internal/agent/source/perforce/perforce_warm_test.go` - the warm-path guards, all new.
- `internal/agent/source/perforce/client.go` - `ResolveHead`'s signature, and `AltRoots` dropped
  from the client spec.
- `internal/agent/source/perforce/testdata/p4d/entrypoint.sh` - the virtual stream with the remap,
  which is what makes any of this provable.
- `internal/agent/source/perforce/perforce_remap_integration_test.go` - the only proof.
- `internal/agent/source/perforce/sweeper_claim_test.go` - the swept-workspace interleaving, and its
  bounded failure.
- `internal/agent/source/perforce/perforce_test.go` - the cwd contract, restated to what it checks.
- `internal/jobspec/jobspec.go` - `client_template` anchored against a leading hyphen.
- `README.md` - the mechanism, not the coverage.
