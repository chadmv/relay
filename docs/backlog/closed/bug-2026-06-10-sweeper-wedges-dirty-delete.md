---
title: A dirty delete permanently wedges the workspace sweeper
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
resolution: fixed
priority: medium
source: full-codebase review (2026-06-10)
---

# A dirty delete permanently wedges the workspace sweeper

## Summary
`evict` deletes the p4 client first, then the directory. If `RemoveAll` fails, the entry is marked `DirtyDelete` and kept. On the next sweep the same entry (oldest-first) is retried: `DeleteClient` now fails because the client no longer exists, `evict` returns the error, and `SweepOnce` aborts the entire pass. Every subsequent sweep dies at the same entry, so no workspace is ever evicted again and disk pressure is never relieved.

## Proposal
- Treat "client doesn't exist" as success in `evict` (or skip `DeleteClient` when `w.DirtyDelete`).
- Continue past per-entry failures instead of aborting the pass:

```go
if err := s.evict(ctx, reg, w); err != nil {
    log.Printf("sweeper: evict %s: %v", w.ShortID, err)
    continue
}
```

## Related
- `internal/agent/source/perforce/sweeper.go:77-85, 110-127`
- Related cleanup: `Provider.EvictWorkspace` builds an ad-hoc Sweeper without `OnEvictedCB`, so in-memory `syncedPaths` survive eviction, and its `locked` check has a TOCTOU window against a concurrent Prepare (`perforce.go:267-283`). Split into [[bug-2026-06-20-evictworkspace-state-leak-toctou]].

## Resolution
fixed - `evict()` now skips `Client.DeleteClient` when `w.DirtyDelete` is set (the flag is
only ever set after the client was already deleted, so only the directory remains; uses the
existing flag rather than string-matching the p4 error). `SweepOnce` now logs and continues
past a per-entry `evict` failure in both the age pass and the pressure pass, so one bad
entry no longer aborts the whole pass; the `FreeDiskGB` error stays a hard return. Covered
by deterministic fake-runner unit tests (dirty entry skips DeleteClient; a failing entry
does not abort the pass) plus the existing p4d integration suite. The related
EvictWorkspace `OnEvictedCB` leak + lock TOCTOU was split into
[[bug-2026-06-20-evictworkspace-state-leak-toctou]] to keep this fix surgical. Plan:
`docs/superpowers/plans/2026-06-20-sweeper-wedges-dirty-delete.md`.
