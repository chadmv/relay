---
date: 2026-06-21
topic: default-cancel-abandon-hang
branch: claude/dazzling-chaplygin-1f400f
range: d494f0eba8be3ef2e5e5abe8a38ddd286181217f..HEAD
---

# Session Retro: 2026-06-21 - Default-cancel / Abandon unbounded hang fix

**TL;DR:** Fixed the latent unbounded-hang bug in the agent runner's default `Cancel(false)`
and `Abandon()` paths under a wedged `sendCh` by adding a per-runner `cancelledCh` (mirroring
the existing `forcedCh`), run through the full gated agent-team lifecycle (spec -> plan ->
implement -> verify -> integrate) with RED-vs-GREEN proven on Linux/Docker and a clean `-race`.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-06-21-default-cancel-abandon-hang-design.md` - chose
  Approach A (dedicated `cancelledCh` signal channel) over B (select on the run context),
  with the load-bearing terminal-status gating-predicate table.
- **Plan** `docs/superpowers/plans/2026-06-21-default-cancel-abandon-hang-plan.md` - 6 tasks,
  sequenced so the two-change wedged repro lands last and each test maps to a clean RED->GREEN
  cycle; `-race` in the Linux container made a required gate at the plan gate.
- **Fix** `internal/agent/runner.go` - `cancelledCh chan struct{}` + `cancelledClose sync.Once`,
  allocated in `newRunner`; closed once by both `Cancel(false)` and `Abandon()`; a fourth
  `<-r.cancelledCh` select case in `sendOrAbort` so a parked in-flight log write abandons and
  lets `cmd.Wait()` return; `sendFinalStatus`'s bounded best-effort try-send extended from the
  forced-only gate to any per-task cancel (`r.cancelled.Load()`). Default cancel still runs
  workspace Finalize + sendInventory (Finalize gate keys off `r.forced`, untouched).
- **Tests** `internal/agent/runner_cancel_test.go` - three `//go:build !windows` tests:
  default-cancel wedged-full bound, default-cancel non-wedged terminal-FAILED, abandon
  wedged-full bound.
- **Backlog** closed `bug-2026-06-20-default-cancel-abandon-backpressure-bound-test`; filed
  the pre-existing `bug-2026-06-21-sendinventory-blocking-send-under-wedge` residual.

## Key Decisions

- **Approach A over B.** A dedicated channel closed explicitly by `Cancel`/`Abandon` mirrors
  the proven `forcedCh` mechanism and keeps the abort signal independent of context-teardown
  ordering. B (selecting on `runCtx`) would couple log-drain semantics to context lifetime -
  exactly the implicit coupling that made the original bug hard to see.
- **Unconditional guarded close (deviation from spec's literal wiring).** The plan closes
  `cancelledCh` on the forced path too, guarded by `sync.Once`, rather than "forced ->
  forcedCh only." Behaviorally inert (the forced terminal-send gate and Finalize-skip gate
  both key off `r.forced`, not `cancelledCh`) and a smaller diff. The code reviewer confirmed
  it inert.
- **Gate the bounded terminal send on `r.cancelled.Load()` alone.** Both `Cancel` kinds set
  `r.cancelled`; abandon early-returns before the gate; normal completion never sets it. So a
  single predicate covers forced + default while leaving the normal-completion blocking send
  byte-for-byte unchanged.
- **Proportionate Phase 4.** A unit-level runner concurrency fix with Linux RED/GREEN + clean
  `-race` did not warrant the full `relay-verify` workflow or an integration tester; a focused
  adversarial `relay-code-reviewer` pass was the right-sized verification (found nothing).

## Problems Encountered

- **Sustained model-classifier outage** blocked subagent dispatch for ~15-20 min early in the
  session (read-only tools and the conductor's own edits kept working). Handled by waiting on
  background timers and retrying; the user confirmed when status was restored.
- **`SendMessage` to a paused subagent is not enabled in this context.** The TPM agent paused
  with one clarifying question and returned an agent id, but `SendMessage` errored
  ("not enabled in this context"). Worked around by dispatching a FRESH TPM with every design
  decision pre-resolved in the prompt, so it wrote the spec straight through without stopping.

## Known Limitations

- `sendInventory` in the default-cancel `defer` still uses a blocking parent-context send and
  can park until agent shutdown under a wedged channel. Pre-existing, on the runner's own
  goroutine (does not pin `cmd.Wait()`), out of scope here - filed as
  [`bug-2026-06-21-sendinventory-blocking-send-under-wedge`](../backlog/bug-2026-06-21-sendinventory-blocking-send-under-wedge.md).

## Improvement Goals

- **When `SendMessage` to a paused subagent is unavailable, re-dispatch a fresh agent with the
  decision baked into the prompt** rather than blocking. Cheaper than discovering the tool gap
  mid-flow; pre-resolving foreseeable clarifying questions in the dispatch prompt avoids the
  round-trip entirely. (new this session)
- **During a classifier/model outage, wait on a single longer background timer and retry on
  the completion notification** instead of chaining short sleeps - keeps the loop efficient and
  the user informed. (new this session)

## Files Most Touched

- `internal/agent/runner.go` (+89/-35) - the fix: `cancelledCh`, guarded close, `sendOrAbort`
  4th case, `sendFinalStatus` per-task-cancel bound.
- `internal/agent/runner_cancel_test.go` (+130) - three new `!windows` regression tests.
- `docs/superpowers/plans/2026-06-21-default-cancel-abandon-hang-plan.md` (+730) - the 6-task plan.
- `docs/superpowers/specs/2026-06-21-default-cancel-abandon-hang-design.md` (+314) - the design.
- `docs/backlog/bug-2026-06-21-sendinventory-blocking-send-under-wedge.md` (+61) - residual.
- `docs/backlog/closed/bug-2026-06-20-default-cancel-abandon-backpressure-bound-test.md` - closed.
