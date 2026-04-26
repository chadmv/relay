---
title: Is `last_used_at` accurate enough for the sweeper's age policy?
type: idea
status: open
created: 2026-04-25
source: 2026-04-25 perforce-workspace-management retro — Open Questions
---

# Is `last_used_at` accurate enough for the sweeper's age policy?

## Summary
Is `last_used_at` accurate enough for the sweeper's age policy? It's updated on every `Prepare` but not on every individual `p4` command. A workspace held for 12 hours by a long task shows the same "age" as one used briefly.
