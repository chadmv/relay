---
title: Down migration loses information for multi-command rows
type: bug
status: open
created: 2026-04-25
source: 2026-04-25 multi-command-tasks retro — Known Limitations
---

# Down migration loses information for multi-command rows

## Summary
Down migration loses information for multi-command rows. The migration script is honest about this — `command TEXT[]` simply can't represent `[[a],[b]]`. Acceptable for a rollback path that should be rare; documented in the file.
