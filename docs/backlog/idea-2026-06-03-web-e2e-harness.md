---
title: Add an end-to-end (Playwright) test harness for the web UI
type: idea
status: open
created: 2026-06-03
priority: medium
source: web front end auth slice retro
---

# Add an end-to-end (Playwright) test harness for the web UI

## Summary
The auth slice shipped with thorough Vitest + RTL + MSW unit tests, yet two real integration bugs slipped through: the invented auth response contract (mocks mirrored a shape the real backend never returns) and the missing post-login redirect (no app-level navigation test existed). A browser-driven E2E harness exercising the SPA against a real `relay-server` would catch this class of bug.

## Proposal
Stand up Playwright running the built SPA (or `make web-dev`) against a real `relay-server` + Postgres, covering at least: login -> lands on /jobs, logout -> back to /auth, and the register flows. Decide whether to seed via the bootstrap admin or a test fixture. Likely worth doing once a data-bearing page (Workers/Jobs) exists so the E2E surface is meaningful.

## Related
- Retro: `docs/retros/2026-06-03-web-frontend-auth.md`
- `web/` (Vitest unit tests today; no browser E2E)

## Update 2026-08-14: the ad-hoc browser lane has two specific capability gaps, now measured across three sessions

The project's substitute for this item is a human-driven browser lane in Phase 4, used whenever the Go
diff is empty. It works, and it has produced the only end-to-end evidence in several slices. **It has
also now failed to obtain the same two things in three separate sessions**, and the amendment records
which two, because they are the concrete argument for this item that its 2026-06-03 framing (contract
drift, redirects) does not make.

1. **No screenshot and no compositing.** Every browser session in this arc has measured with
   `elementFromPoint`, `getBoundingClientRect`, `scrollWidth` and `scrollHeight` instead of looking.
   That is not always worse - the measurement in `2026-08-12-schedule-detail-page` found an overflow
   a screenshot at the default window size would have missed - but it means **no one has visually
   confirmed a layout**. In the 2026-08-13-narrow-viewport-overflow slice, the header nav's horizontal
   scroll and five wrapped breadcrumb/toolbar rows were proven only as numbers: `docSW <= clientW`
   says the page no longer overflows, and says nothing about whether the result looks like anything.
   That slice shipped **two design decisions taken with no hi-fi reference**, neither of which anyone
   has seen rendered.
2. **No real key events.** The lane cannot send an actual `Tab` press. `EnrollmentsTable` and
   `InvitesTable` have zero focusable elements in any row, so their clipped right-hand columns are
   reachable only through the scroll wrapper's own tab stop. The fix (`tabIndex={0}` plus
   `role="group"` and an `aria-label` on the wrapper) was chosen precisely because the previous
   behaviour relied on Chromium's implicit scroller focusability, which Safari does not grant - and
   **the fix itself was never watched working**, in any browser, by anyone. It is pinned by a jsdom
   attribute assertion, which proves the attribute is present and nothing about keyboard reachability.

Both gaps are exactly what Playwright provides as table stakes: `page.screenshot()`, real
`page.keyboard.press('Tab')`, and multiple browser engines including WebKit for precisely the
Chromium-versus-Safari divergence above.

**No separate backlog item was filed for the limitation**, deliberately: this item is its remedy, and a
second file would split one question. Recorded here instead so the next reader sees the accumulated
evidence rather than a four-month-old framing about auth contracts.

**One more datum for prioritization:** in the 2026-08-13-narrow-viewport-overflow slice, **seven of
eight verification points were obtainable only by a human driving a real browser**, and none of them
is protected against regression by `npm test`. jsdom performs no layout, so `offsetWidth`,
`scrollWidth` and `getBoundingClientRect()` all return 0 and every layout assertion in `web/` is
either a structural guard or a class-string pin. That slice is the strongest argument this item has
accumulated: it fixed an app-wide rendering defect whose entire acceptance criterion the project's
automated gate cannot express.
</content>
