# Session Retro: 2026-05-02 — p4 Binary & Ticket Diagnostics

## What Was Built

Closed backlog item [`bug-2026-04-25-p4-binary-assumed-authenticated`](../backlog/closed/bug-2026-04-25-p4-binary-assumed-authenticated.md): the Perforce source provider previously made no attempt to verify that the `p4` CLI was installed or that a valid ticket existed, surfacing failures as raw `p4` stderr buried in generic error wrappers.

Two complementary diagnostics were added:

- **`(*perforce.Provider).Preflight(ctx) error`** — checks `exec.LookPath("p4")` at agent startup (when `RELAY_WORKSPACE_ROOT` is set). Returns a `ErrP4BinaryMissing` sentinel on failure. `cmd/relay-agent/main.go` calls it immediately after `perforce.New`; on failure it logs once (`relay-agent: workspace provider disabled: ...`) and continues with `provider = nil` so non-source tasks still execute.

- **`classifyP4Error(err) error`** (`internal/agent/source/perforce/diagnostics.go`) — rewraps four common stderr patterns (binary missing, P4PASSWD invalid, session expired, connect failed) with operator-facing guidance messages. Applied at all ten `Client.*` call sites in `perforce.go` (nine originally specified, plus `PendingChangesByDescPrefix` caught by the final code reviewer). Original error preserved via `%w`.

No credential management was added. Relay still expects operators to provision P4 tickets out-of-band via `p4 login`. This closes only the diagnostics gap.

Full brainstorm → spec → plan → worktree → subagent-driven-development cycle. Implementation lives on `feat/p4-diagnostics` pending the merge/PR decision.

## Key Decisions

**Diagnostics-only, no `p4 login` call.** The `CLAUDE.md` and existing specs explicitly say Relay does not manage P4 credentials. The brainstorm confirmed this remains a non-goal. Startup preflight is offline (LookPath only, no server contact) to keep agent boot fast and network-independent.

**Soft fail on missing `p4`.** When `p4` is absent, the agent logs and degrades (`provider = nil`) rather than exiting. Non-source tasks are still a valid workload; a hard exit would be over-strict for operators who set `RELAY_WORKSPACE_ROOT` on a mixed-use worker.

**`lookPath` package-level var for testability.** Follows the existing pattern from `internal/cli` (`saveConfigFn`, `readPasswordFn`) rather than injecting through the Config struct or using build-tag tricks. Keeps production code clean and test overrides local to `perforce` package.

**`PendingChangesByDescPrefix` wrap added post-spec.** The original spec table listed 9 call sites and explicitly excluded `PendingChangesByDescPrefix` (its error flows to `progress()`, not a hard failure). The final code reviewer correctly identified this as still operator-visible (the `[recover]` progress line) and flagged it as a blocker. The fix was one line; a targeted test was added.

## Problems Encountered

**Plan listed 9 sites but the spec and reviewer agreed the 10th should be wrapped.** The spec stated "PendingChangesByDescPrefix is intentionally excluded." The final reviewer overruled this — correctly — because the error still surfaces in task logs and is exactly the category of stderr this change exists to improve. The spec and plan were wrong; the reviewer caught it. The blocker was resolved in one fix commit before merge.

**Worktree shell `cd` confusion.** Early bash commands to `cd .worktrees/p4-diagnostics` failed (relative path, Windows shell). After switching to `cd D:/dev/relay/.worktrees/p4-diagnostics`, subsequent bash commands ran in the worktree rather than the main repo — causing `git log` to show worktree-branch commits rather than master commits. This was a minor tracking inconvenience, not a correctness issue.

## What We Did Well

**Final code reviewer caught the spec omission.** The two-stage per-task review (spec compliance + quality) passed cleanly for all five tasks. The final whole-branch reviewer found the one remaining gap (`PendingChangesByDescPrefix`) that was absent from both spec and plan. The review layer did its job.

**Haiku for mechanical tasks, Sonnet for reviews.** The implementation tasks were simple (1–2 files, clear spec) and haiku handled them without blockers. Review tasks used sonnet. No wasted escalations to opus.

**Quality fixup caught in-loop.** The code quality review on Task 1 found two improvements (precedence comment, `errors.Is` passthrough assertion). These were addressed in the same commit loop before Task 2 started — no deferred cleanup.

**TDD discipline held throughout.** Each task: write failing test, run to confirm failure, implement, run to confirm pass. No task skipped the failing-test step.

## What We Did Not Do Well

**Spec excluded `PendingChangesByDescPrefix` incorrectly.** The plan's rationale ("flows to `progress()`, not a return") is accurate as a data-flow observation but wrong as a diagnostic justification. Operator-visible output in task logs is exactly what this change targets, whether it reaches them via `return` or `progress()`. The spec author (this session) wrote a note that should have been "wrap it" instead of "skip it." The reviewer's instinct was better.

**Commit range ambiguity in retro.** Because the retro is written before the merge decision, the commit range below reflects the worktree tip. The next retro will start from the merged commit (or PR merge commit), not from this hash. This is minor bookkeeping but worth noting.

## Improvement Goals

- **When excluding a call site from a classifier pass, document the reason explicitly** in terms of whether the error is operator-visible — not just in terms of data-flow mechanics. "flows to progress()" is insufficient justification if progress lines appear in task logs.

## Files Most Touched

- `internal/agent/source/perforce/diagnostics.go` (new) — `classifyP4Error` helper, four recognized patterns
- `internal/agent/source/perforce/diagnostics_test.go` (new) — 7-row table-driven test
- `internal/agent/source/perforce/perforce_preflight_test.go` (new) — `TestPreflight_BinaryPresent` / `TestPreflight_BinaryMissing`
- `internal/agent/source/perforce/perforce.go` — `ErrP4BinaryMissing`, `lookPath` var, `Preflight` method, 10 call-site wraps
- `internal/agent/source/perforce/perforce_test.go` — `TestProvider_Prepare_ClassifiesAuthError` + `TestProvider_Prepare_ClassifiesRecoverError`
- `cmd/relay-agent/main.go` — `Preflight` call gates provider assignment and sweeper wiring
- `docs/superpowers/specs/2026-05-02-p4-binary-diagnostics-design.md` — full design doc
- `docs/superpowers/plans/2026-05-02-p4-binary-diagnostics.md` — 5-task implementation plan

## Commit Range

d872926..0932d18
