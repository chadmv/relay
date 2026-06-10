---
title: A dirty delete permanently wedges the workspace sweeper
type: bug
status: open
created: 2026-06-10
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
- Related cleanup: `Provider.EvictWorkspace` builds an ad-hoc Sweeper without `OnEvictedCB`, so in-memory `syncedPaths` survive eviction, and its `locked` check has a TOCTOU window against a concurrent Prepare (`perforce.go:267-283`).
