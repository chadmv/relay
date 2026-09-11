---
date: 2026-09-10
topic: workspace-exclusion-set-ceiling
branch: claude/roadmap-now-dependencies-581b21
range: fb99ca02..fa9971be
---

# Session Retro: 2026-09-10 - Workspace Exclusion Set Ceiling

**TL;DR:** A job author could name any sixteen made-up paths to exclude from a Perforce sync, and each
distinct set made the agent create a new workspace directory and a new permanent client record on the
shared Perforce server - all before anything checked whether those paths name anything at all. This
session capped how many such workspaces one stream can hold per agent, evicting the junk left by
failed attempts first. The whole design rested on one unverified assumption about how Perforce
behaves, so that was measured against a real server before any code was written.

## Handoff

Item 6 of 6 in the roadmap Now batch, and the roadmap's lead Now item. Closes
[[idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates]]. Merged as PR #211,
14 commits plus spec/plan.

Two enforcement points. Agent side: a per-(stream, agent) ceiling over **exclusion-derived keys only**,
in `Prepare`'s cold branch, evict-then-admit. Coordinator side: a per-batch row bound in
`applyInventory` that **truncates** rather than refusing, because refusing would re-create the freeze
PR #209 removed.

`RELAY_WORKSPACE_MAX_EXCLUSION_SETS`, default 4, hard max 64, clamped with a warning. **No value
disables it and `0` is not one** - both neighbouring `RELAY_WORKSPACE_*` variables mean "disabled" at
zero, so a bare comparison against an unset field would have refused every cold exclusion prepare. It
resolves through a `getenv` seam rather than a `Config` field for the same reason.

**The gating measurement, against real p4d 2026.1.** The question was reframed away from exit status
first, on the `PathHasFiles` precedent that p4 exits zero on conditions it also reports on stderr:
can a client spec for a non-existent stream be PERSISTED? Control on a real stream exits 0 with a full
spec; the bogus stream exits **1 with empty stdout** and `Stream '...' doesn't exist.` on stderr;
`client -i` is never reached because no spec exists to pipe; `p4 clients -e` afterwards is empty. The
refusal is at the FIRST of the two calls, so nothing is minted. That is now a permanent guard carrying
the written lane excuse CLAUDE.md requires.

**The honest concurrency bound is `K + concurrent prepares - 1`, not `K`** - the check and the mint are
not under one lock, and the overshoot is bounded by the operator's slot count rather than the author's
input. No test or comment asserts a hard `K`.

Next entry point: the batch is complete; ROADMAP.md needs a refresh.

## What Was Built

- `internal/agent/source/perforce/exclusion_ceiling.go` - the key predicate, the resolver, pure
  candidate ordering, and the gate.
- The gate in `Prepare`'s cold branch only, with a test proving a WARM prepare is not gated.
- `client_stream_existence_integration_test.go` - the premise guard, p4d-backed, human-run with its
  reason stated.
- A batch bound in `applyInventory` with a truncation counter, and a subtest driving a batch that is
  both over-count and carries a bad row.

## Key Decisions

- **Exclusion-derived keys only.** Read literally, "a ceiling on distinct source keys per stream" puts
  the base workspace inside the ceiling, letting an author with junk strings evict the shared warm
  workspace every non-exclusion task on that stream uses - the attacker's best outcome from the
  control. The base is never an eviction candidate, proven with a decoy that sorts first under both
  ordering arms.
- **Empty-`BaselineHash` first, then LRU.** The empty baseline is exactly the residue a failed bogus
  prepare leaves, so the attacker's junk is reclaimed before any legitimate warm workspace.
- **Evict inline, because the sweeper cannot be delegated to.** `Sweeper.Run` returns immediately when
  both `MaxAge` and `MinFreeGB` are zero and is only constructed when one is set, so on a default agent
  nothing reclaims a workspace ever. The item called this "off by default", which understates it.
- **The refusal never names the occupants.** Task logs are readable by any authenticated user, so
  occupant keys would be a cross-tenant disclosure; short ids go to the agent's own log.
- **The client must exist before the probe can run**, so a ceiling is the only practical answer.
  Rollback of the cold mint on probe failure was rejected because the preempt loop also runs on a warm
  workspace whose baseline moved, so one typo would destroy a populated multi-terabyte workspace.

## What Went Wrong and What Changes

Ledger: the prior retro's entries were all promoted, so none are carried. Promoted lessons that
fired: [[reference_ask_the_system_dont_parse_its_prose]] - the reframed gating question is its
sharpest instance; [[reference_a_ceiling_test_must_assert_the_refusal]];
[[reference_a_decoy_goes_before_its_target]] - the base-workspace decoy;
[[reference_verify_a_prescribed_command_exists]]; [[reference_a_kill_must_name_its_guard]];
[[feedback_file_the_item_a_decision_is_conditioned_on]].

- **The design rested on an unverified claim about another program's behaviour, and the item stated it
  as fact.** "A bound moved, not removed" is only true if `p4 client -o -S` refuses a non-existent
  stream. Nothing in the repo had checked it, and the wrong answer would have invalidated the whole
  slice.
  -> **What changes:** when an item's framing depends on how an external tool behaves, make that
  measurement the first task with an explicit STOP branch, and reframe it away from exit status toward
  the state you actually care about. "Does it exit non-zero" and "can the artifact be persisted" are
  different questions, and this project already has a precedent where p4 exits zero and complains on
  stderr.
- **An error alone was necessary and not sufficient, and the test says so.** A create that failed on
  its SECOND call having already saved a spec would satisfy `require.Error` and still leave the
  artifact behind.
  -> **What changes:** when asserting that an operation left nothing behind, assert the absence of the
  artifact, not the presence of an error. And include the positive control - without it, a failure for
  an unrelated reason (a bad ticket, a fixture that never came up) satisfies the assertion.
- **Two of the slice's own new comments over-claimed, and it found them by grepping its own added
  lines.** "The knob only ever LOOSENS a control" is false for a value below the default, and "the only
  state in which this control refuses" is false because an eviction can also fail on a p4 or disk
  fault.
  -> **What changes:** after writing new comments, grep your own added lines for absolute words -
  "only", "never", "always", "every" - and check each against the code you just wrote. This is the
  cheapest pass available and it caught two defects here.
- **A convergence claim was false and the true reason was narrower.** "Converges in one prepare" -
  simulated, 20 entries at ceiling 4 takes five prepares. What the attempt cap actually bounds is one
  prepare's `client -d` calls.
  -> **What changes:** when documenting a bound, state the quantity it bounds rather than the outcome
  you hope for. A work bound and a convergence guarantee are different claims and only one of them was
  true here.
- **The spec's own discriminating input did not discriminate.** It said a suffix-only key predicate
  would miscount `//a|b`'s workspaces against `//a`; that key ends `|//a|b`, which is not `|//a`. The
  real collision runs the other way - a longer stream's key counted against a shorter one when the
  longer ends with `"|"` plus the shorter.
  -> **What changes:** when a spec supplies the input that proves a predicate, work the input through
  the predicate by hand before writing the test. A plausible-looking counterexample in the wrong
  direction produces a test that passes against the defect.
- **A spec assertion was impossible to write.** It said to assert no `client -i` for the new name
  appears in the argv; that argv is two elements with the spec on stdin.
  -> **What changes:** before asserting on a subprocess's arguments, read the call site for which
  arguments actually travel in argv versus stdin. (carried as a specific instance of
  [[reference_capture_the_format_before_parsing_it]])
- **A claim that a sibling slice's comment "remains true" was wrong.** One batch can now move both
  counters; the checkable property is per ENTRY, because truncation runs ahead of the loop so a dropped
  entry never reaches the constructor.
  -> **What changes:** when a slice adds a second writer to a counter a previous slice documented,
  re-read that comment's quantifier. "Per batch" and "per entry" diverge the moment a second
  increment path exists.
- **A known tradeoff is documented rather than hidden:** with more candidates than the ceiling, the
  attempt cap means the last candidates are never tried, so five held-but-one-free candidates at
  ceiling 4 can refuse even though the fifth was evictable.
  -> No process change - this is the bounded-work tradeoff being recorded where it is read.

## Recommended Backlog Items

Backlog intake, not a priority order.

- [idea] **The eviction attempt cap can refuse while an evictable candidate remains untried.** With
  candidates above the ceiling, `attempts := ceiling` stops before the last ones. Deliberate today and
  documented as a work bound; worth revisiting if operators see refusals with free slots.
- [idea] **Probing through an already-existing sibling client for the same stream** would close the warm
  case without minting, but rests on an untested assumption about p4 view equivalence. Named with its
  premise so it is falsifiable.
- [idea] **`applyInventoryUpdate`'s per-message upsert is the inventory table's growth path**, and this
  slice bounds only the batch. `applyInventory` opens with a full DELETE, so bounding `len(inv)` bounds
  one transaction's work rather than the table's size.

## Files Most Touched

- `internal/agent/source/perforce/exclusion_ceiling.go` - new; predicate, resolver, ordering, gate.
- `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go` - new; the refusal, the held-slot
  case, the base-workspace decoy, and the warm-not-gated case.
- `internal/agent/source/perforce/exclusion_ceiling_test.go` - new; the pure ordering and predicate
  tests, including the pipe-in-stream discriminator.
- `internal/agent/source/perforce/client_stream_existence_integration_test.go` - new; the premise guard.
- `internal/agent/source/perforce/sourcekey.go` - the predicate beside the encoding it inverts.
- `internal/agent/source/perforce/perforce.go` - the gate in the cold branch.
- `internal/worker/handler.go`, `inventory_params.go`, `inventory_batch_bound_test.go` - the batch bound
  and its counter.
- `README.md` - three hunks: the appended ceiling sentence, the eviction sentence, and one env-var row.
