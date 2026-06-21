---
date: 2026-06-20
topic: sweeper-wedges-dirty-delete
branch: claude/blissful-brown-c7780a
pr: "2026-06-20 / sweeper-wedges-dirty-delete"
merge: "2026-06-20 / sweeper-wedges-dirty-delete"
---

# Session Retro: 2026-06-20 - Workspace sweeper wedges on a dirty delete

**TL;DR:** Closed `bug-2026-06-10-sweeper-wedges-dirty-delete`. The Perforce workspace
sweeper deleted the p4 client then the directory; when `RemoveAll` failed, the entry was
marked `DirtyDelete` and kept, but the next sweep retried `DeleteClient` on the
now-nonexistent client, failed, and aborted the entire pass - so disk pressure was never
relieved again. Fixed by skipping `DeleteClient` on a dirty entry and continuing past
per-entry failures.

## What Was Built

- `internal/agent/source/perforce/sweeper.go`: `evict()` skips `Client.DeleteClient` when
  `w.DirtyDelete` is set (using the existing flag rather than string-matching the p4
  error); `SweepOnce` logs and `continue`s past a per-entry `evict` failure in both the age
  pass and the pressure pass instead of returning, so one bad entry no longer aborts the
  pass. The `FreeDiskGB` error stays a hard return.
- Deterministic fake-runner unit tests: a dirty entry never calls `DeleteClient`; a failing
  entry does not abort the pass (a later healthy entry is still evicted). Existing p4d
  integration suite still green.

## Key Decisions

- **Skip-on-DirtyDelete over error-string matching:** `DeleteClient` returns the raw p4
  runner error with no sentinel, and the `DirtyDelete` flag (set only after a successful
  client delete) already carries exactly the right meaning ("client gone, only the
  directory remains"). Using the flag is deterministic and testable; string-matching p4
  stderr would be brittle across p4 versions/locales.
- **Split the related cleanup out:** the backlog item also flagged `Provider.EvictWorkspace`
  building an ad-hoc Sweeper without `OnEvictedCB` (per-task state leak) plus a lock TOCTOU.
  Verified the *background* sweeper already wires `OnEvictedCB`, so the leak is confined to
  `EvictWorkspace`, and the TOCTOU needs a lock-design decision - so it was filed as its own
  item rather than folded in, keeping this fix surgical.

## Backlog Triage

- Filed [[bug-2026-06-20-evictworkspace-state-leak-toctou]] (bug, medium) for the split-out
  `EvictWorkspace` `OnEvictedCB` leak + lock TOCTOU.
- No other items; code review came back clean with no findings.
