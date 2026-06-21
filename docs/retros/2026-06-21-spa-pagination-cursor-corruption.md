---
date: 2026-06-21
topic: spa-pagination-cursor-corruption
branch: claude/gifted-meninsky-5fc18a
pr: "2026-06-21 / spa-pagination-cursor-corruption"
merge: "2026-06-21 / spa-pagination-cursor-corruption"
---

# Session Retro: 2026-06-21 - SPA pagination cursor corruption

**TL;DR:** Closed `bug-2026-06-10-spa-pagination-cursor-corruption`. JobsPage and SchedulesPage
use React Query `placeholderData: keepPreviousData`; during a page transition the displayed
`data` is still the previous page, so the next button (gated only by `!data?.next_cursor`) stayed
enabled and a double-next pushed a duplicate/stale cursor onto the stack, stranding a later prev.
Both pages now disable next AND prev while `isPlaceholderData` is true. Autopilot iteration 3 of a
`/autopilot 4` run.

## What Was Built

- `web/src/jobs/JobsPage.tsx` / `web/src/schedules/SchedulesPage.tsx` - destructure
  `isPlaceholderData` from `useJobs`/`useSchedules` and add `|| isPlaceholderData` to the
  `disabled` of both the next and prev buttons (four expressions total). No hook or API change.
- A non-vacuous regression test per page: a deferred page-2 response held in flight; both buttons
  asserted `toBeDisabled()` during the in-flight window. Proven RED against the unfixed gating
  (prev was `stack.length === 0` only, next was the stale `next_cursor`).

## Key Decisions

- **Gate prev too, not just next.** The backlog focused on double-next, but disabling only next
  leaves the symmetric prev-during-fetch race. Gating both on `isPlaceholderData` means no cursor
  mutation happens whenever the rows on screen do not match the current cursor - the clean
  invariant.
- **Confirmed no per-poll regression.** The worry with gating on `isPlaceholderData` is that the
  3s/10s background poll might flip it true and disable the buttons every interval. It does not:
  the cursor is part of the query key (`['jobs', sort, status, cursor]`), so a same-key background
  refetch keeps its data and never enters placeholder state - only a NEW key (cursor change) does.
  Verified in review and empirically (polls fire during the passing tests without disabling).
- **Did not rebuild the committed `web/dist`.** `vite build` dirtied `web/dist/index.html`, but
  `web/dist` is tracked only from the initial scaffold commit - no subsequent frontend feature PR
  (auth, Workers, Jobs, Schedules) has updated it. It is regenerated at server-build time, so
  committing a rebuilt copy would break that convention and add noise. Reverted it to keep the
  change surgical.

## Backlog Triage

- No new items. Code review found nothing material and confirmed no other paginated list shares
  the pattern (WorkersPage has no cursor pagination; the worker hooks poll a single page).

## Process Note

- Proportionate verification again: a four-expression presentational gating change with strong
  unit coverage got a single focused frontend code-review, not the backend-oriented relay-verify
  fan-out (whose Postgres/p4d integration tester is irrelevant here). The reviewer still added
  value by reasoning out the per-poll-regression question and scanning sibling list pages.
- Watch for build artifacts in frontend diffs: `vite build` / `tsc -b` during verification dirties
  the tracked-but-unmaintained `web/dist`; revert it before assembling the PR.
