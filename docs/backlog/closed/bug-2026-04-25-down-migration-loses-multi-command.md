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

## Resolution

Closed `wontfix` on 2026-04-26. Down-migration data fidelity for a feature
being rolled back is not worth the engineering cost. Behavior is documented
in `internal/store/migrations/000008_task_commands.down.sql` (multi-command
rows fail loudly during down-migration). If a multi-command row ever needs
to survive a downgrade, revisit then.
