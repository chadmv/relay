---
title: Sweeper still uses an independent `Registry` instance
type: bug
status: open
created: 2026-04-25
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---

# Sweeper still uses an independent `Registry` instance

## Summary
**Sweeper still uses an independent `Registry` instance.** Final review noted this design point: the sweeper reads the registry fresh from disk each pass for safety. It works correctly *with* `OnEvictedCB`, but a future refactor could give the sweeper a reference to `p.reg` directly and eliminate the read-then-overwrite race window entirely.
