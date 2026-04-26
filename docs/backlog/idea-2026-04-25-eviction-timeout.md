---
title: Per-eviction timeout for EvictWorkspace?
type: idea
status: open
created: 2026-04-25
source: 2026-04-25 perforce-workspace-management retro — Open Questions
---

# Per-eviction timeout for EvictWorkspace?

## Summary
The eviction goroutine in `agent.go` uses `a.runCtx` (correctly), but `EvictWorkspace` itself blocks on `p4 client -d` and `rm -rf`. Should there be a per-eviction timeout?
