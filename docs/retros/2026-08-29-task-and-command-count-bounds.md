---
date: 2026-08-29
topic: task-and-command-count-bounds
branch: claude/task-command-counts-unbounded-756af5
range: 56d5660..b0a24cc
---

# Session Retro: 2026-08-29 - Task and Command Count Bounds

**TL;DR:** A single well-formed request to relay could ask it to run over a million commands, because
nothing limited how many tasks a job could have or how many commands a task could have. The bug
report asked for two limits, one on each of those. This session found that adding exactly those two
limits would have improved the worst case by *nothing* - the request is already capped at one
megabyte of text, and two limits whose product is larger than what a megabyte can express leave the
megabyte as the only thing actually stopping you. So a third limit was added, on the total across the
whole job, and that one is what takes the worst case from roughly 1.7 million commands down to
25,000. Four independent reviews then found no fault in the new code, but did find seven other ways
to make relay do too much work that these limits do not touch; all seven are now written down as
their own items. The work is on a branch, fully verified, not merged.

## Handoff

Branch `claude/task-command-counts-unbounded-756af5`, **17 commits, NOT pushed and NOT merged** - the
user asked for this retro before the Phase 5 decision. Closes
[[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]] (now in `docs/backlog/closed/`).

`jobspec.Validate` gains `maxTasksPerJob = 5000` (checked at the top, beside the existing
`len(spec.Tasks) == 0`), `maxCommandsPerTask = 500` (in the per-task bounds block, AFTER
`normalizeTaskCommands` so both command spellings are covered), and `maxCommandsPerJob = 25000` (a
`totalCommands` accumulator checked inside the same loop, so a spec far over budget is refused
partway through traversal). All six ingest paths inherit them via the Single job-spec pipeline
invariant.

**The third bound is the whole design finding and it was not in the item's proposal.** Aggregate cost
is `total_commands x (1 + retries)` and is indifferent to distribution, because the retry budget is
per task and every task's commands re-run on every attempt - so one task with 116,000 commands and
232 tasks with 500 each cost the same. `5000 x 500 = 2,500,000` is ~15x more than a 1 MiB body can
express, so with only the two per-axis caps `maxBodyBytes` would have stayed the binding constraint
and the shipped reduction would have been zero. The per-axis caps are still non-redundant:
transaction length and dispatcher backlog (tasks), single-slot concentration (commands per task).

Retroactivity is unchanged in kind from the retries slice and enumerated in the `maxRetries` comment:
five paths re-validate STORED `scheduled_jobs.job_spec`. **A stored spec over 5000 tasks stops firing
on upgrade** - that belongs in the PR body. Also for the PR body: **these bounds are NOT a mitigation
for [[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]]** - they bound counts,
nothing bounds stored spec BYTES, and a 1 MiB spec passes all three trivially.

Gates run by the conductor, not taken from agent reports: 22 packages green untagged (2 no-test),
`go vet` clean tagged and untagged, integration `internal/api` 337/337 (481s), `internal/store`
105/105 (210s), `internal/schedrunner` 26/26, zero container leaks. `-race` green in the `golang:1.26`
container per Phase 3. Every corrected number in the new prose was recomputed by the conductor.

**Next session starts at the Phase 5 merge decision.** `make` is not installed; run every target as
its literal command.

## What Was Built

- **`internal/jobspec/jobspec.go`** (+266) - three constants with their arguments as doc comments,
  three checks, and a rewrite of the `maxRetries` block whose stored-spec enumeration had gone stale.
- **`internal/jobspec/count_bounds_test.go`** (+204) - both directions on all three axes, offender
  first and second, plus the legacy-`command` accumulator case and two refused-before-traversal cases.
- **Four more test files** (+305) - the REST wire test (decoding into `map[string]any` deliberately),
  the stored-spec re-validation coverage, and the `jobcreate` nil-`*store.Queries` pair.
- **Prose corrections across nine files** - six comment sites plus README, and then a second round
  correcting five more claims the first round got wrong or missed.
- **Docs** - spec (458 lines) and plan (1599 lines), committed at their phase boundaries.
- **ROADMAP refresh** - open count 146 -> 152, Now re-led, five refresh blocks at the retention cap.

## Key Decisions

- **Three bounds, not the two the item proposed.** See Handoff. The item was not wrong about its two
  axes; it was wrong that bounding them would reduce anything.
- **Generous values (5000/500/25000), conditional on half 2 being filed.** A count cap low enough to
  be the DoS control also refuses a real 2000-frame animation submitted one task per frame. The
  rate-limit item was therefore filed BEFORE implementation began, not at close, so the condition the
  values rest on was true at the moment they were chosen.
- **Not env-configurable**, for `maxRetries`' reason and two that postdate it: the boot sweep writes
  the validator's message into `last_error` (a per-replica bound makes stored operator-facing text a
  function of which replica booted), and the PATCH clear-decision would clear on a lenient replica's
  verdict that a strict replica re-writes.
- **The count is enumerated, not counted.** "on five paths" was dropped from the comment because the
  same paragraph argues that a number goes stale silently and has no maintainer. An ordinal reference
  elsewhere (`retroactivity site 5`) was replaced by a symbol for the same reason.
- **Six residual axes filed rather than fixed**, and one correction appended to an existing item. The
  slice stays about one thing.

## What Went Wrong and What Changes

**Ledger.** Every entry in `2026-08-28-unfireable-schedule-visibility` was promoted, so none is
carried. Promoted lessons that fired this session, as evidence the homes work:
[[feedback_autopilot_squash_merge_resync]]'s squash-orphan extension fired at the first step of this
retro - `git cat-file -e` reported the prior retro's end SHA present while `git merge-base
--is-ancestor` correctly said unreachable, and taking it would have produced a wrong range;
[[feedback_verify_tree_not_subagent_claims]]'s LANE extension fired when the integration lens ended
its turn narrating a running lane instead of returning a result, and the conductor ran the decisive
untagged lane itself; [[reference_tightening_a_validator_is_retroactive]] shaped the whole
retroactivity section; [[feedback_backlog_proposal_not_contract]] produced the session's central
finding; [[reference_wrong_prose_is_the_dominant_defect]] and
[[reference_uniqueness_claim_is_about_the_complement]] both recurred;
[[reference_mutation_battery_needs_green_baseline]] and [[reference_verify_the_mutation_applied]]
governed a 19-row battery with a control. [[feedback_a_green_rerun_bounds_not_retires]] was used
verbatim by the integration lens on the Phase 3 container red.

- **The conductor's own ROADMAP edit corrupted the file from 599 lines to 29,750.** A generic
  section-parser was written to insert numbered entries; two of its mutators returned flat line lists
  where the renumberer expected lists-of-blocks, so every line became an "entry" and the suffix
  duplicated on each call. Nothing detected it - no test covers ROADMAP.md. It was caught by noticing
  a grep hit reported at line 29538 in a file known to be ~600 lines, then reverting one file.
  -> **What changes:** when programmatically restructuring a document, capture the line count before
  and after and assert the delta against the change you intended, in the same script that writes the
  file. And for a one-off edit, prefer exact-anchor string replacement over a general parser: the
  parser has to be right about every section in the file, the anchor only has to be right about the
  line you meant. This is the same instinct CLAUDE.md's CRLF subsection already applies to diffstat,
  generalized from line endings to structure. (promoted to [[feedback_assert_line_delta_on_document_edits]])

- **The conductor put a claim it had "verified" into a subagent brief, and the claim was false.** The
  brief told the fix round that `internal/schedrunner/stored_spec_bounds_test.go` cited a nonexistent
  test. Both halves of the evidence were real - the string was in that file, and `grep -rn "^func
  Test.*StopsFiring"` returned nothing - and the conclusion was still wrong: the hit reads "IT USED TO
  ASSERT A DEFECT ON PURPOSE, under the name ...", a correct historical note about a rename, sitting
  in the very file that defines the new name. Acting on it would have deleted true prose. The engineer
  caught it and said so.
  -> **What changes:** a reference to a symbol that does not exist is not by itself a defect - read
  the sentence around the hit before calling it dangling, because a rename note, a changelog line and
  a "this used to be called X" comment all legitimately name absent symbols. The check is not "does
  the symbol exist" but "does this sentence claim the symbol exists". (promoted to [[reference_absent_symbol_may_be_a_correct_historical_note]])

- **Bounding each axis of a multi-axis cost can reduce the worst case by exactly zero, and it looks
  like progress.** The item named two axes and proposed a cap on each. Both caps were correct, both
  would have been testable, both would have shipped green, and the worst-case aggregate would have
  been unchanged, because their product still exceeded the pre-existing ceiling (`maxBodyBytes`). The
  only reason it surfaced is that the spec agent was explicitly asked whether two per-axis caps were
  sufficient.
  -> **What changes:** when a fix bounds N dimensions of one cost, compute the product of the new
  bounds and compare it to the ceiling that already existed. If the product is larger, the new bounds
  changed the SHAPE of the worst case and not its SIZE, and the reduction being claimed is zero. Ask
  this before writing the tests, because per-axis boundary tests all pass either way. (promoted to [[reference_per_axis_bounds_can_reduce_nothing]])

- **A backlog item filed by the conductor asserted a combinatorial ceiling that the code does not
  enforce.** The dependency-edge item said round trips were bounded by `min(body, V*(V-1))`. But
  `jobspec.Validate` checks each `depends_on` name for membership only and never dedupes, and
  `detectCycle` handles repeats correctly and therefore ACCEPTS them - so a two-task spec repeating
  one name to the body limit issues ~209,700 INSERTs to produce one row, against a stated ceiling of
  2. Found by the security lens, one day after filing. Both proposed fixes had to change.
  -> **What changes:** before writing a `V*(V-1)`-style ceiling, check whether the input is
  deduplicated anywhere on the path. A bound on DISTINCT pairs says nothing about work per ENTRY, and
  `ON CONFLICT DO NOTHING` at the far end makes the two look identical in the result while differing
  by five orders of magnitude in cost. (promoted to [[reference_distinct_pairs_ceiling_ignores_duplicates]])

- **A review lens reported a compound claim as verified after checking only part of it.** The
  invariants lens was asked to check the sentence "The CLI, the SPA and the Python SDK post JSON and
  hold no parallel validation". It found the Python half false, reported that as its medium finding,
  and stated the CLI and SPA halves as "true" - the conductor then repeated "verified" in the fix
  brief. The SPA half is also false: `web/src/jobs/specTemplate.ts` runs a real client-side pre-check
  on the `/jobs/new` path. Found by the engineer in the fix round, one layer later.
  -> **What changes:** a sentence making N claims about N subsystems needs N checks, and "verified"
  without naming which subsystems were opened is not a verification. When asking an agent to verify a
  compound claim, ask it to report per clause; when repeating someone's verification, repeat the
  scope with it. (extends [[feedback_sweep_count_needs_its_axis]] from "name the axis a count is over"
  to "name the clauses a verification covered".) (promoted there)

- **A design decision was made conditional on an item that did not exist yet.** The generous cap
  values rest on the argument "these are not the DoS control, the rate limit is" - which is only
  honest if the rate-limit item is actually going to exist. The planner flagged exactly this: if the
  conductor did not file it, the spec's own argument collapsed and the tighter set was correct.
  -> **What changes:** when a decision's justification names future work, file that work before the
  decision ships, not at close. An argument resting on an unfiled item is unfalsifiable at the moment
  it is made and quietly false afterwards; filing first makes the condition checkable by the next
  reader. (promoted to [[feedback_file_the_item_a_decision_is_conditioned_on]])

## Recommended Backlog Items

Everything this session surfaced was filed as it was found; nothing is pending. Order carries no
meaning - `ROADMAP.md` orders the work.

- See [`bug-2026-08-29-post-v1-jobs-is-not-rate-limited`](../backlog/bug-2026-08-29-post-v1-jobs-is-not-rate-limited.md) - half 2; the cap values are conditional on it
- See [`bug-2026-08-29-createjobfromspec-inserts-one-dependency-edge-per-round-trip`](../backlog/bug-2026-08-29-createjobfromspec-inserts-one-dependency-edge-per-round-trip.md) - one round trip per `depends_on` entry, worst case a two-task spec
- See [`bug-2026-08-29-mcp-labels-the-last-error-excerpt-but-not-the-job-spec`](../backlog/bug-2026-08-29-mcp-labels-the-last-error-excerpt-but-not-the-job-spec.md) - the labelling teaches a model the wrong rule
- See [`bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded`](../backlog/bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded.md) - 4.5x the density of the axis just capped
- See [`bug-2026-08-29-perforce-workspace-admission-is-quadratic-under-the-mutex`](../backlog/bug-2026-08-29-perforce-workspace-admission-is-quadratic-under-the-mutex.md) - missing `break`, and a held set that never shrinks
- See [`bug-2026-08-29-a-schedule-outlives-its-owners-credentials`](../backlog/bug-2026-08-29-a-schedule-outlives-its-owners-credentials.md) - survives the remedy an operator reaches for first
- See [`bug-2026-08-29-cli-and-spa-label-last-error-as-coming-from-the-job-spec`](../backlog/bug-2026-08-29-cli-and-spa-label-last-error-as-coming-from-the-job-spec.md) - output, not prose, so the prose-only round stopped
- See [`bug-2026-08-23-dispatch-cycle-unbounded-per-tick`](../backlog/bug-2026-08-23-dispatch-cycle-unbounded-per-tick.md) - amended, not filed: it named source JSON and `reservedIDs` but never `requires`, which is re-parsed per (task x worker) per tick, forever

## Files Most Touched

| File | Why |
|---|---|
| `docs/superpowers/plans/2026-08-29-task-and-command-count-bounds.md` (+1599) | The plan; refuted eight things in the spec, two of which changed the work |
| `docs/superpowers/specs/2026-08-29-task-and-command-count-bounds.md` (+458) | The spec; refuted four things in the backlog item and produced the three-bound argument |
| `internal/jobspec/jobspec.go` (+266/-27) | The three constants, the three checks, and the rewritten `maxRetries` enumeration |
| `internal/jobspec/count_bounds_test.go` (+204) | Both directions on all three axes; 12 mutants applied against it, 12 killed |
| `ROADMAP.md` (+104/-73) | Refresh 32; also the file the conductor corrupted and restored |
| `internal/api/jobs_spec_bounds_integration_test.go` (+96) | The REST wire proof, decoding into `map[string]any` on purpose |
| `internal/schedrunner/stored_spec_count_bounds_test.go` (+80) | The stored-spec re-validation paths, untagged so CI actually runs them |
| `internal/jobcreate/jobcreate_validate_test.go` (+71/-9) | The nil-`*store.Queries` pair; the mutant's failure mode is a panic, contained by a `recover` |
| `internal/mcp/untrusted.go` (+30/-6) | The provenance string, false for two whole message classes |
| `README.md` (+11/-3) | Two rows added (`tasks`, `tasks[].commands`); the "themselves unlimited" clause removed |
