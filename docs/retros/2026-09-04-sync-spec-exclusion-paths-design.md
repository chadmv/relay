---
date: 2026-09-04
topic: sync-spec-exclusion-paths-design
branch: claude/lane-syncspec-design
range: 191c310e67b6c5b53bdf43f9909413c3f1ccdea4..1cf0b99fb039386c925fdf1cba6d4159e1671f66
---

# Session Retro: 2026-09-04 - Sync-Spec Exclusion Paths Design

**TL;DR:** Perforce workspaces on this project are shared between tasks, and there is currently no
way for a job to say "sync this stream but skip that huge subtree" - which on real streams is the
difference between fitting on the disk and not. A production fork of relay built that feature and
it works for them, but porting it as-is would silently corrupt the shared workspace for anyone who
does not use the same exclusions. This session produced the design for doing it safely. No code.
The most useful output was not the chosen mechanism but the honest accounting underneath it: the
design costs MORE disk than today in the common mixed case, and the repository cannot measure how
much.

## Handoff

Lane B of a six-item batch over ROADMAP.md's Now section, and the last item of the fork-upstreaming
batch. Closed [[idea-2026-09-03-sync-spec-exclusion-paths-design]]. Three commits: the spec
`12d6969`, five follow-on items `7a4cd69`, the close `1cf0b99`. Docs only, no Go or web files.

The spec is `docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md`, 470 lines, settling
all seven questions the item posed.

**Mechanism:** the exclusion set joins the workspace identity, so differently-excluded tasks never
share a client, and inside such a workspace the exclusion is realised with a `p4 sync -k` have-list
preempt at the resolved revision of the include that covers it.

**The framing correction that drove everything else.** The item presents key-splitting as one
mechanism among three. It is not a mechanism, it is a **precondition every candidate needs**: a
have-list preempt writes the shared client's have-list, and a view-based exclusion writes the
shared client's spec, which `CreateStreamClient` rewrites on every `Prepare`. Once that is seen,
the real question is only what happens INSIDE a workspace whose exclusion set is constant, and
there the preempt beats the view route because the view route rests on two p4 premises nothing in
this repository has ever captured.

**Closed on one acceptance bullet, not two, and deliberately.** Bullet 1 (a spec exists) is met.
Bullet 2 ("no task can observe a workspace missing files it asked for") was never a property a
document could have; it moves intact to [[feature-2026-09-04-implement-sync-spec-exclusion-paths]]
with the acceptance test and the mutation that must kill it. The item's own Proposal scoped it this
way.

Next reader: `docs/backlog/bug-2026-09-04-a-subpath-of-a-renaming-remap-does-not-resolve` and
`bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero` both meet this design inside
`toClientPath`, and the implementation gains correctness for free when they land.

## What Was Built

A spec. Its most load-bearing sections:

- **Section 3.4, the disk trade**, written as the strongest argument AGAINST the design rather
  than buried. An agent running `k` exclusion sets for one stream holds `k` workspaces totalling
  `sum(S - X_i)`. With one excluding family and one non-excluding family that is `2S - X`, and
  since at least one include must survive an exclusion, `X < S` always. **So a mixed-exclusion
  stream on one agent always costs more disk than today.** The feature pays only where the
  exclusion is uniform for that stream on that agent - which is exactly the fork's deployment
  shape, and exactly why the fork never observed the poisoning this design exists to prevent.
- **The acceptance test, with its order as the load-bearing detail.**
  `TestPerforce_E2E_AnExcludingTaskDoesNotStripFilesFromAnUnexcludingPeer` must run the EXCLUDING
  task first. Run the unexcluding one first and its full sync leaves the files on disk, hiding the
  defect. The mutation that must kill it: making `SourceKey` ignore exclusions.

## Key Decisions

- **Explicit `exclude: true` field over a `-` prefix on `path`** - but not for the reason the item
  gives, and with a cost the item does not mention: the field fails OPEN on version skew, because
  `readJSON` does not set `DisallowUnknownFields` and protobuf drops unknown fields, where a `-`
  prefix would fail closed through `toClientPath`. Closed by an `optional bool
  supports_sync_exclusions` capability mirroring `supports_workspaces`.
- **A short fixed-width key format** (`x1|<hex16>|<stream>`) rather than raw concatenation, chosen
  against Postgres's btree index-row limit inside `worker_workspaces`'s primary key and against
  wrecking the `tabwriter` column in `doWorkersWorkspaces`.
- **Preempt failure fails the prepare**, rather than the fork's WARN line. A warning inside a
  multi-hour sync log is read after the volume has already filled.
- **State the disk cost as UNQUANTIFIED rather than estimating it.** `InventoryEntry` carries no
  size and `Sweeper.Run` discards `SweepOnce`'s evicted ids, so neither term of the trade is
  measurable from this repository. An estimate would have been a number nobody could check.

## What Went Wrong and What Changes

**Six of the item's premises were false, and four of them were phrased as instructions to
"confirm".** That phrasing is the trap worth recording. An item that says "confirm `BaselineHash`
already covers exclusions" pre-commits the reader to a yes; the honest reading is that it is an
open question, and here the answer was no. `BaselineHash` builds `entry{path, rev}` and hashes
nothing else, so an exclusion field would hash identically to its absence and two
differently-excluded tasks would look same-baseline. Likewise "confirm the dispatcher's warm bias
keys on the new `SourceKey`" - `selectWorker` compares `ws.SourceKey` to `taskSrc.Stream`
directly, so there was no key function to confirm and one has to be created.

**The item asserted a benefit for two clients, and both were wrong in opposite directions.** The
SPA source builder it cites does not exist yet. And the Python SDK is worse than neutral: `Sync`
carries `model_config = ConfigDict(extra="forbid")`, so `exclude` is a hard SDK rejection where a
`-` prefix would pass through `path: str` untouched. The design still chooses the field, but on a
different argument. **A benefit claimed for a downstream consumer needs that consumer opened.**

**A live drift was found incidentally, in the language nobody was looking at.** Checking the SDK
for the `extra="forbid"` question surfaced that `_CLIENT_TEMPLATE_RE` (`^[A-Za-z0-9_.-]+$`) has
diverged from Go's `clientTmplRe` (`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`): Go was tightened to refuse a
leading hyphen because `CreateStreamClient` places the value straight after `-t`, and the SDK
pattern still allows one. Filed. This is the "the unguarded copy is in another language" shape,
and it was found by reading the other copy for an unrelated reason - which is the only way it ever
gets found while nothing enumerates the pair.

**The conductor verified four of the agent's claims against the tree before accepting the spec,
and should have; all four held.** Recording this because the alternative - accepting a docs-only
deliverable on the agent's own report - is tempting precisely when there is no test suite to
disagree with it. A spec has no lane. The tree is the only check it gets.

## Recommended Backlog Items

All five were filed on this branch:

- `feature-2026-09-04-implement-sync-spec-exclusion-paths` - the code, carrying acceptance bullet 2
- `idea-2026-09-04-source-sync-has-no-entry-count-bound` - the third per-entry subprocess axis on
  this spec, and the only one of the three with no bound at all
- `idea-2026-09-04-no-workspace-size-or-eviction-instrumentation` - what would make section 3.4's
  trade measurable
- `bug-2026-09-04-python-client-template-regex-has-drifted-from-go`
- `idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key`

## Files Most Touched

- `docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md` - the deliverable
- `docs/backlog/` - five added, one closed
