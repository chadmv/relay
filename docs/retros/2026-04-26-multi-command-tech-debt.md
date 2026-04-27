# Session Retro: 2026-04-26 — Multi-Command Tech-Debt Cleanup

## What Was Built

Three follow-up items from the 2026-04-25 multi-command-tasks retro, shipped as a single PR (three implementation commits) on the worktree branch `claude/reverent-panini-059ccf`.

**1. Deprecated `DispatchTask.command` proto field removed (`2cd0a5d`).** The `repeated string command = 3 [deprecated = true]` field was replaced with `reserved 3; reserved "command";` in `proto/relayv1/relay.proto`. `buf generate` was run to regenerate `internal/proto/relayv1/relay.pb.go`; the `Command []string` field and `GetCommand()` accessor on the generated struct are gone. The JSON `command:` field on `TaskSpec` (public back-compat for stored job specs) was intentionally left untouched.

**2. Structured `step_index`/`step_total` on `TaskLogChunk` (`c459104`).** Added `int32 step_index = 5` and `int32 step_total = 6` to the `TaskLogChunk` proto message. `step_index` is 1-indexed; 0 means "not part of a numbered step" (e.g., PREPARE-phase output). `step_total` is set on every per-step chunk so SSE consumers joining mid-stream can render "step N of M" without prior context. Single-command tasks emit `step_index=1, step_total=1`. The synthetic `=== relay step N/M ===` text marker line is retained (belt-and-suspenders). Agent runner changes: `sendStepMarker` and `pipeLog` both gained `int32` step parameters; the per-step loop in `Run` introduces `step`/`stepTotal` locals threaded into both pipe goroutines. `makePrepareProgressFn` is untouched. TDD: tests extended/added before implementation. Three tests in `runner_multistep_test.go`.

**3. Down-migration backlog item closed as `wontfix` (`61e9bb2`).** `docs/backlog/bug-2026-04-25-down-migration-loses-multi-command.md` moved to `docs/backlog/closed/` with a `## Resolution` section. The migration script's behavior (loud failure on multi-command rows during rollback) is the correct trade-off and is already documented in the migration file.

**4. All three backlog items closed (`fae74f2`).** In addition to the down-migration item, `bug-2026-04-25-deprecated-command-proto-field.md` and `bug-2026-04-25-synthetic-step-markers-unstructured.md` were moved to `docs/backlog/closed/` with frontmatter and Resolution sections. (Caught by the final cross-task code review; not included in the original plan.)

## Key Decisions

**Down-migration item → wontfix.** The bug entry itself called the behavior "acceptable for a rollback path that should be rare." No engineering value in restoring JSONB fidelity through a `TEXT[]` downgrade column; the loud-failure behavior is the correct safe default.

**`reserved` over silent field deletion.** Removing `command = 3` without a reservation would allow a future developer to accidentally reuse field number 3, breaking wire compatibility with any old serialized messages or agents. `reserved 3; reserved "command";` costs two lines and makes accidental reuse a compile-time proto error.

**Self-describing chunks (`step_index` + `step_total` on every chunk).** Carrying `step_total` on every chunk redundantly is worth it: SSE consumers join the stream at an arbitrary point and need to render "step 2 of 3" without buffering all prior chunks. If only the first chunk of a step carried `step_total`, consumers would have to buffer or guess.

**Retain the text marker for one release.** Existing `relay logs` output and any log-tailing tool would break if the text marker were removed immediately. Belt-and-suspenders approach: add structured fields now, remove the text in a future release once consumers have migrated.

**Subagent-driven execution via brainstorm → spec → plan → dispatch pipeline.** All three tasks dispatched as independent subagents with spec-compliance review then code-quality review after each. Worked cleanly; the final cross-task review added value by catching a gap the per-task reviews missed.

## Problems Encountered

**Plan omitted closing the two code-change backlog items.** The plan explicitly handled closing the down-migration item (Task 3), but the commit messages for Tasks 1 and 2 said "Closes the ... backlog item" without the corresponding file moves. The final cross-task code review caught it and flagged it as an Important issue. Required an additional commit (`fae74f2`) to close both items properly.

**`git mv` conflict from writing to `closed/` first.** When the two backlog items were written directly to `docs/backlog/closed/` via the Write tool and then `git mv` was attempted, git reported "destination exists." Needed `git rm` on the originals followed by `git add` on the new files instead. Small friction; cost one failed command.

**Task 3 implementer left `status: open` in frontmatter.** The initial closure commit (`61e9bb2`) moved the file and appended the Resolution section correctly, but left the YAML frontmatter at `status: open` without the `closed:` date. Caught by comparing against existing closed items; fixed in a follow-up commit (`a38195a`).

## Known Limitations

- See [`idea-2026-04-26-remove-synthetic-step-marker`](../backlog/idea-2026-04-26-remove-synthetic-step-marker.md) — Remove synthetic step marker text line once consumers use step_index/step_total

## What We Did Well

- **Brainstorming resolved scope ambiguity cleanly.** Three items went in; only two needed code changes (bug #2 was correctly scoped as wontfix in the brainstorm, saving implementation work).
- **TDD discipline maintained for Task 2.** Tests written, run to confirm failure, then implementation made them green. The spec reviewer verified TDD ordering from the diff.
- **Final cross-task review caught a real gap.** Per-task spec and quality reviews approved each task individually; the final review caught the open-backlog inconsistency that none of the individual reviewers flagged. Worth the extra invocation.
- **`reserved` directive used correctly.** Not just deleting the field — reserving the number and name is the protobuf best practice and prevents future accidents.

## What We Did Not Do Well

- **Plan didn't include backlog housekeeping for code-change tasks.** When a commit message says "Closes the X backlog item," the plan should include a step to move that file to `closed/`. This is a recurring omission pattern (similar to the per-task review vs. final review gap). Future plans for bug fixes should always include a "close the corresponding backlog item" step.
- **Used Write instead of `git mv` for backlog file moves.** Writing the closed files directly then trying to `git mv` caused a conflict. The correct pattern is: `git mv` first, then edit the moved file in place. Alternatively, write directly to the closed path and use `git rm` + `git add`, but `git mv` is simpler and tracks the rename cleanly.

## Improvement Goals

- When drafting a plan for a task that closes a backlog item, include an explicit step to move the corresponding `docs/backlog/*.md` file to `docs/backlog/closed/` in the same commit.
- Always use `git mv <source> <dest>` to move backlog files, then edit the moved file. Avoid writing the destination file first.

## Files Most Touched

- `proto/relayv1/relay.proto` — field 3 reserved; fields 5 and 6 added to `TaskLogChunk`
- `internal/proto/relayv1/relay.pb.go` — regenerated twice (proto changes)
- `internal/agent/runner.go` — `sendStepMarker`, `pipeLog`, loop call sites updated for `int32` step parameters
- `internal/agent/runner_multistep_test.go` — two existing tests extended with structural assertions; `TestRunner_SingleCommandReportsStepOneOfOne` added
- `docs/backlog/closed/` — three items closed with Resolution sections (`deprecated-command-proto-field`, `synthetic-step-markers-unstructured`, `down-migration-loses-multi-command`)
- `docs/superpowers/specs/2026-04-26-multi-command-tech-debt-design.md` — design spec (new)
- `docs/superpowers/plans/2026-04-26-multi-command-tech-debt.md` — implementation plan (new)

## Commit Range

`d18e1ee..fae74f2`
