---
title: Audit log for archive/unarchive actions
type: idea
status: open
created: 2026-05-06
source: user-archive feature retro
---

# Audit log for archive/unarchive actions

## Summary
No audit log. Archive and unarchive actions are not recorded anywhere; if an audit table is added later, these actions should write to it.

## Context
The archive/unarchive feature (migration 000010, `POST /v1/users/{id}/archive` and `/unarchive`) was added in the 2026-05-06 session. No audit table exists in the current schema. When one is introduced, the archive and unarchive handlers in `internal/api/users.go` (`handleAdminArchiveUser`, `handleAdminUnarchiveUser`) are the two write sites to hook in.

## Proposal
When an audit table is created, add writes to it inside the existing archive transaction (already `pool.BeginTx`) and in the unarchive handler. Record: actor user ID, target user ID, action (`archive`/`unarchive`), timestamp.

## Related
- `internal/api/users.go` — `handleAdminArchiveUser`, `handleAdminUnarchiveUser`
- `docs/retros/2026-05-06-user-archive.md`
