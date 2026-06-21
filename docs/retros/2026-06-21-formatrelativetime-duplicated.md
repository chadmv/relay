---
date: 2026-06-21
topic: formatrelativetime-duplicated
branch: claude/distracted-allen-9c27c1
pr: "2026-06-21 / formatrelativetime-duplicated"
merge: "2026-06-21 / formatrelativetime-duplicated"
---

# Session Retro: 2026-06-21 - extract shared formatRelativeTime

**TL;DR:** Closed `bug-2026-06-05-formatrelativetime-duplicated`. `formatRelativeTime` was duplicated
verbatim between `web/src/workers/liveness.ts` and `web/src/schedules/format.ts`. Extracted the single
implementation into `web/src/lib/time.ts`; both modules re-export it so every existing importer keeps
its path. Behavior unchanged. Autopilot batch, item 3 of 7.

## What Was Built

- `web/src/lib/time.ts` (new) - the single `formatRelativeTime` implementation.
- `web/src/lib/time.test.ts` (new) - covers seconds/minutes/hours/days buckets and future-clamps-to-0.
- `web/src/workers/liveness.ts`, `web/src/schedules/format.ts` - local definitions removed, replaced by
  `export { formatRelativeTime } from '../lib/time'`.

## Key Decisions

- **Confirm-identical-before-merge gate.** The engineer's first step was to verify the two bodies were
  character-for-character identical (same clamp, thresholds, output format) before extracting - merging
  two subtly-different functions would silently change one call site's output. They were identical, so
  the extraction is a pure DRY move.
- **Re-export over rewrite-all-importers.** Seven files import `formatRelativeTime` from `./liveness` or
  `./format`. Re-exporting from both original modules keeps the diff surgical (logic single-sourced in
  `lib/time.ts`, import paths untouched) instead of editing seven importers. The duplication that
  mattered - the implementation - is gone; the two one-line re-exports are compatibility shims.

## Process Note

- Trivial DRY extraction with no behavior change: verification was the engineer's full web suite
  (148 tests) + `tsc --noEmit`, then a conductor diff self-review, rather than the relay-verify fan-out.
- `web/dist` (the stale committed scaffold) was left untouched; the diff is only `web/src`.

## Backlog Triage

- No new items.
