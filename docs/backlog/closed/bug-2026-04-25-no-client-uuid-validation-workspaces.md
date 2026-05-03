---
title: No client-side UUID validation in workspaces CLI
type: bug
status: closed
resolved: 2026-05-02
created: 2026-04-25
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---

# No client-side UUID validation in workspaces CLI

## Summary
**No client-side UUID validation in `relay workers workspaces`/`evict-workspace`.** The server returns 400 for malformed IDs and the CLI surfaces it, so it's UX-only.
