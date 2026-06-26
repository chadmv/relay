---
title: Per-eviction timeout for EvictWorkspace?
type: idea
status: closed
created: 2026-04-25
closed: 2026-06-25
resolution: fixed
source: 2026-04-25 perforce-workspace-management retro — Open Questions
---

# Per-eviction timeout for EvictWorkspace?

## Summary
The eviction goroutine in `agent.go` uses `a.runCtx` (correctly), but `EvictWorkspace` itself blocks on `p4 client -d` and `rm -rf`. Should there be a per-eviction timeout?

## Resolution
Fixed 2026-06-25. Decision: YES, a per-eviction timeout is warranted - the background sweeper
serializes evictions and parks its ticker inside `SweepOnce`, so one wedged `p4 client -d`
permanently disables disk reclamation for the agent's lifetime (`runCtx` only fires at shutdown).
`Sweeper.evict` (`internal/agent/source/perforce/sweeper.go`) now wraps the `DeleteClient`
(`p4 client -d`) call in `context.WithTimeout`, so the bounded ctx flows to `exec.CommandContext`
and a wedged Perforce call becomes a logged, best-effort, retryable skip. Per maintainer input
(production workspaces can be 1 TB+), the bound is operator-tunable via `RELAY_EVICTION_TIMEOUT`
(a Go duration, mirroring `RELAY_WORKER_GRACE_WINDOW`), defaulting to a generous 30m. Scope is
deliberately limited to the `p4 client -d` subprocess: `os.RemoveAll` of the workspace tree stays
unbounded (uncancelable local I/O that makes progress), so a large `rm -rf` is never interrupted -
and because a p4 timeout returns before `os.RemoveAll`, no DirtyDelete marker is set and the next
sweep retries the full eviction cleanly. Eviction stays best-effort (existing log-and-skip callers
unchanged; the sweep continues past a timeout). Documented in README.md. Unit tests (hang->bounded
return, no-RemoveAll/no-DirtyDelete on timeout, sweep-continues, env parsing + fallback) green on
Windows and Linux/Docker; `go vet` clean; adversarial review found no findings.
