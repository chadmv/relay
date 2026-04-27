---
title: Sweeper still uses an independent `Registry` instance
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---

# Sweeper still uses an independent `Registry` instance

## Summary
**Sweeper still uses an independent `Registry` instance.** Final review noted this design point: the sweeper reads the registry fresh from disk each pass for safety. It works correctly *with* `OnEvictedCB`, but a future refactor could give the sweeper a reference to `p.reg` directly and eliminate the read-then-overwrite race window entirely.

## Resolution
`Provider` now exposes `Registry()` and the `Sweeper` takes an injected `Reg *Registry` field. `SweepOnce` no longer calls `LoadRegistry` per pass; eviction is immediately visible to subsequent `Provider.ListInventory` and `Prepare` calls without depending on `OnEvictedCB`. `InvalidateWorkspace` keeps clearing the per-task workspace cache but no longer touches `p.reg`. New `TestProviderSweeper_CoherentWithoutInvalidateCallback` proves the coherence. Spec: [docs/superpowers/specs/2026-04-26-dispatcher-lifecycle-correctness-design.md](../../superpowers/specs/2026-04-26-dispatcher-lifecycle-correctness-design.md).
