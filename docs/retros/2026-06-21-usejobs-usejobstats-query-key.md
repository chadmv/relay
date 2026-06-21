---
date: 2026-06-21
topic: usejobs-usejobstats-query-key-prefix
branch: claude/distracted-allen-9c27c1
pr: "2026-06-21 / usejobs-usejobstats-query-key-prefix"
merge: "2026-06-21 / usejobs-usejobstats-query-key-prefix"
---

# Session Retro: 2026-06-21 - decouple useJobStats query key

**TL;DR:** Closed `bug-2026-06-05-usejobs-usejobstats-query-key-prefix`. `useJobStats` used react-query
key `['jobs', 'stats']`, sharing the `'jobs'` prefix with `useJobs`, so a broad
`invalidateQueries({ queryKey: ['jobs'] })` would also invalidate stats. Re-keyed stats to a distinct
top-level `['job-stats']`. Latent coupling, no current trigger. Autopilot batch, item 4 of 7.

## What Was Built

- `web/src/jobs/useJobStats.ts` - query key `['jobs', 'stats']` -> `['job-stats']`.
- `web/src/jobs/queryKeyDecoupling.test.tsx` (new) - renders both hooks, invalidates `['jobs']`,
  asserts the stats endpoint is NOT refetched while the list IS.

## Key Decisions

- **Behavioral decoupling test over a key-string assertion.** Rather than asserting the literal key
  shape, the test exercises the actual coupling: it counts stats-endpoint fetches across an
  `invalidateQueries(['jobs'])`. Proven RED (2 fetches vs expected 1) against the shared-prefix code -
  it fails for the real reason the bug existed, not a cosmetic mismatch.
- **Grep-confirmed single reference site.** Before changing the key, the engineer grepped all of
  `web/src` for `invalidateQueries`/`setQueryData`/`getQueryData` against the stats key and found none -
  the definition was the only reference, so the one-line change is complete and safe.

## Process Note

- Trivial frontend re-key: verification was the engineer's RED-proven test + full web suite (149) +
  `tsc`, then a conductor diff self-review. No relay-verify fan-out needed.
- `web/dist` left untouched.

## Backlog Triage

- No new items.
