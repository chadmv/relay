---
date: 2026-06-25
topic: sendinventory-wedge-escape
branch: claude/happy-mendel-18687f
pr: "#83 follow-on (residual #3)"
---

# Session Retro: 2026-06-25 - sendInventory wedge-escape

**TL;DR:** Closed the last residual of the 2026-06-21 default-cancel/Abandon hang family.
`sendInventory` in the agent runner's prepare `defer` used a blocking parent-context send and
could park until agent shutdown under a wedged `sendCh`. Switched it to a room-first bounded
best-effort try-send when `r.cancelled.Load() || r.abandoned.Load()`, mirroring
`sendFinalStatus`'s cancelled branch; normal completion keeps the blocking `r.send`. Three
`//go:build !windows` tests proven RED-vs-GREEN on Linux/Docker, full `internal/agent` suite +
`go vet` clean, code review found zero findings. Parent fix was PR #83.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-06-25-sendinventory-wedge-escape-design.md`.
- **Plan** `docs/plans/2026-06-25-sendinventory-wedge-escape-plan.md`.
- **Fix** `internal/agent/runner.go` (commit 8d816a0) - `sendInventory` gains the
  `r.cancelled.Load() || r.abandoned.Load()` bounded try-send branch; normal-completion path
  unchanged.
- **Tests** `internal/agent/runner_cancel_test.go` (commit f72690c) - three `!windows`
  regression tests, RED-vs-GREEN on Linux/Docker.
- **Backlog** closed
  `docs/backlog/closed/bug-2026-06-21-sendinventory-blocking-send-under-wedge.md`.

## What Went Well

- **Clean continuation of an established pattern.** The fix reuses the exact bounded try-send
  shape already proven in `sendFinalStatus`, so the change is small, the safety argument is
  the same one already reviewed (server is authoritative; Finalize reconciled locally; the
  entry recomputes on next workspace use), and the diff is easy to reason about.
- **Right-sized verification.** A unit-level runner concurrency fix with Linux RED/GREEN plus a
  focused adversarial code-review pass was the proportionate gate - no full `relay-verify`
  workflow, no integration tester. Review found nothing.
- **The residual was already filed and scoped.** Last session's retro named this exact bug and
  filed the backlog item, so this session was a straight execute against a known target rather
  than re-discovery.

## Notable

- This is the predicate symmetry the family was converging on: both deferred-cleanup sends
  (`sendFinalStatus`, `sendInventory`) now gate identically - bounded try-send on a per-task
  cancel/abandon, blocking send on normal completion - so a wedged `sendCh` can no longer pin
  the runner's teardown to agent-shutdown.

## Remaining Residuals

None in the cancel/abandon/forced cleanup send-park family. Audited every `sendCh` send in
`internal/agent/runner.go`:

- `sendInventory` (deferred cleanup) - fixed this session.
- `sendFinalStatus` - already bounded on `r.cancelled` (PR #83 / earlier).
- `sendOrAbort` (chunkWriter copy loop) - escapes on all of `ctx.Done`, `forcedCh`,
  `cancelledCh`.
- The blocking `r.send` calls (PREPARING / PREPARE_FAILED / RUNNING / step markers / prepare
  progress) are on the live forward-execution path, not in a cancel/abandon `defer`; none can
  be reached after a per-task cancel signal in a way that would require an escape.
- `handle.Finalize(r.ctx)` is workspace I/O, not a `sendCh` send.

The only deferred `sendCh` send was `sendInventory`; with it fixed, no other
deferred-send-on-parent-context park remains.
