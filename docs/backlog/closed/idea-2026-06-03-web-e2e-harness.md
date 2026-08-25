---
title: Add an end-to-end (Playwright) test harness for the web UI
type: idea
status: closed
created: 2026-06-03
priority: medium
closed: 2026-08-24
resolution: fixed
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

## Resolution

Closed 2026-08-24 by slice 1 of the browser harness (web-e2e-harness).

**`web/e2e/README.md` is the live coverage document. Read that, not this note** - it is maintained with
the harness and this file is now history. Nine open items cross-reference this one as the thing that
would make them verifiable; that README is where they should point.

### What shipped

`web/e2e/` with three specs and 51 tests: full-page screenshots at 320/375/1280 across 13 surfaces,
real `Tab` and arrow-key presses on both scroll wrappers, and the login/logout/deep-link flows.
Chromium runs everything; WebKit runs the keyboard subset, because the accessibility argument turns on
its focusability semantics. It drives the **production-embedded** SPA through a real `relay-server`
against a dedicated `relay_e2e` database, not the Vite dev server - Tailwind v4 scans source
statically, so a no-op fix and a working fix are indistinguishable outside a production bundle.

It also ships `.github/workflows/web-ci.yml`, **the repository's first web CI of any kind**. The spec
went looking for "should e2e run in CI" and found that `npm test`, `tsc -b` and `npm run build` had
never run in CI either - 1116 frontend tests were advisory on every commit. A browser job alone would
have been backwards, so the workflow carries the whole web gate.

Both capability gaps named in this item's 2026-08-14 amendment - the argument that actually justified
it - are delivered. **It found a real defect on its first CI run**: the header nav is clipped at narrow
viewports with no affordance, filed as
[[bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports]]. The 2026-08-13 slice's numeric assertion
was correct; what it could not express is what the result looks like.

### NOT covered - read this before assuming a surface is protected

- **No agent runs**, so nothing that needs a worker is real: `/workers` is empty-state only,
  `/workers/:id` is never visited, no job executes, no task reaches `running`, the SSE task-log stream
  is never opened, and `WorkerPicker` renders only its empty state. Filed as
  [[idea-2026-08-24-e2e-harness-slice-2-agent-in-harness]], which was the condition for closing this.
- **Register / self-registration is not covered**, though this item's own proposal named it. The one
  test server pins `RELAY_ALLOW_SELF_REGISTER=false` so it runs the production-default posture;
  covering `/register` needs a second server or a deliberate change to that. See
  [[idea-2026-08-24-four-surfaces-absent-from-the-e2e-surface-list]].
- **Screenshots are artifacts, not assertions.** Nothing compares them. A visual regression does not
  fail a build; a human reads the CI artifact.
- **WebKit is not Safari.** Narrowed deliberately, not retired.
- **The overflow gate cannot distinguish "fits" from "clipped behind an inner scroller"** - demonstrated
  by mutation with all tests green. [[idea-2026-08-24-layout-overflow-gate-cannot-see-inner-scrollers]].
- The login rate limiter is raised in the harness and therefore unexercised.

### Two things worth carrying forward

The four-month-old framing at the top of this file (auth contract drift, missing post-login redirect)
was **already closed at the unit level** and nobody had noticed: `App.test.tsx` drives a real login
through `PublicOnlyRoute`'s redirect and asserts on text unique to `JobsPage`. The plan asserted the
opposite on the strength of `rg 'PublicOnlyRoute' web/src --glob '*.test.tsx'` returning nothing - a
name grep used as a coverage check for a behavioural property. The engineer caught it by running the
mutation and halted rather than adjusting.

That same shape recurred three more times inside the slice, twice self-inflicted: Tailwind v4 scans the
whole project for class-shaped substrings **including comments**, so the spec's own explanatory prose
kept a CSS rule alive and defeated a mutation, and a class-shaped placeholder shipped an invalid rule
into the production stylesheet.
