import { expect, test } from '@playwright/test'
import { readSeed } from './fixtures'
import { closeNavPanel, expectDestinationsReachable } from './nav'
import { surfaces } from './surfaces'

// jsdom performs NO layout. Every offsetWidth, scrollWidth and
// getBoundingClientRect() across web/src's 152 test files returns 0, so every
// layout assertion there is a structural guard or a class-string pin. This file
// is the only place in the repo where a width is a real number.
//
// Five qualifiers below are lessons the 2026-08-13 narrow-viewport slice paid
// for, converted into config so nobody re-learns them:
//   - POPULATED tables, because the item was misdiagnosed twice on empty ones.
//   - BOTH 320 and 375, because a fifth cause was found only at 320.
//   - HEADER and MAIN measured SEPARATELY from the document, because the fourth
//     cause was found only that way.
//   - /auth as a CONTROL (surfaces.ts), because it renders no shell and never
//     overflowed, which is what makes the header attribution an attribution.
//   - 1280, restoring a check the 2026-08-13 retro itself ran (a manual
//     no-regression pass at that width) but which never made it into this
//     harness. Every `md:`/`lg:` rule - ServerTab.tsx's `md:grid-cols-2` is a
//     real one - is otherwise unevaluated by this file, since 320 and 375 both
//     sit below every such breakpoint. The added cost is a few seconds of
//     wall clock for a check the project already knew it needed.
const WIDTHS = [320, 375, 1280] as const

// NOT called at module scope. Playwright collects every spec file across every
// project - including chromium/webkit, which DEPEND on setup - before it runs
// any test in any project; that collection pass happens before setup has
// written e2e/.run/seed.json. readSeed() must run per-test, after execution
// (not collection) has reached this point, which dependencies do guarantee.
// Measured, not assumed: a stale seed.json from a previous local run was
// masking exactly this ordering bug until e2e/.run/ was cleaned and CI's fresh
// checkout caught the ENOENT immediately.
for (const width of WIDTHS) {
  test.describe(`narrow viewport ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } })

    for (const s of surfaces()) {
      // The title is conditional because the body is. /auth renders no shell, so
      // there is nothing to navigate and no header to find - and a title claiming
      // reachability over a body that does not assert it is the test-honesty
      // problem this harness exists to avoid. The surface is still measured: it is
      // the control that makes a header finding an attribution.
      const title = s.anonymous
        ? `${s.name} fits the viewport (no shell to navigate)`
        : `${s.name} fits the viewport and keeps every destination reachable`
      test(title, async ({ page }, testInfo) => {
        const seed = readSeed()
        if (s.anonymous) {
          // The stored storageState (playwright.config.ts) carries the seeded
          // admin's token on every project, so /auth would otherwise land
          // already-authenticated and PublicOnlyRoute would redirect it to
          // /jobs before "Sign in" ever rendered - measured, not assumed.
          // Stripped via an init script so it runs before the SPA's first
          // AuthProvider hydration, not after.
          await page.addInitScript(() => window.localStorage.removeItem('relay.token'))
        }
        if (s.prepare) {
          await s.prepare(page, seed)
        }
        const path = s.path(seed)
        await page.goto(path)
        await s.ready(page, seed)

        const m = await page.evaluate(() => {
          const header = document.querySelector('header') as HTMLElement | null
          const main = document.querySelector('main') as HTMLElement | null
          return {
            docScroll: document.documentElement.scrollWidth,
            docClient: document.documentElement.clientWidth,
            headerScroll: header ? header.scrollWidth : null,
            mainScroll: main ? main.scrollWidth : null,
          }
        })

        // Recorded on EVERY run, pass or fail - the numbers are the artifact, not
        // a debugging afterthought collapsed into one boolean.
        await testInfo.attach(`widths-${s.name}-${width}`, {
          body: JSON.stringify({ surface: s.name, path, population: s.population, width, ...m }, null, 2),
          contentType: 'application/json',
        })

        // SCREENSHOTS ARE ARTIFACTS, NOT ASSERTIONS. No toHaveScreenshot, no
        // pixel baselines: rasterization, scrollbar metrics and subpixel layout
        // differ between a Windows dev machine and ubuntu-latest, so a baseline
        // would be either permanently red or permanently regenerated - the
        // decorative outcome this whole workflow exists to avoid. Doing it
        // properly needs a pinned container image and is its own slice.
        //
        // The residual is a PROCESS commitment: an artifact nobody opens is worth
        // nothing. The merge of this slice includes one human pass over these
        // images - specifically over the nav's horizontal scroll and the wrapped
        // tab bars, the two design decisions taken with no hi-fi reference.
        const shot = testInfo.outputPath(`${s.name}-${width}.png`)
        await page.screenshot({ path: shot, fullPage: true })
        await testInfo.attach(`screenshot-${s.name}-${width}`, { path: shot, contentType: 'image/png' })

        expect(m.docScroll, `${path}: document overflows at ${width}px`).toBeLessThanOrEqual(m.docClient)
        if (m.headerScroll !== null) {
          expect(m.headerScroll, `${path}: <header> overflows at ${width}px`).toBeLessThanOrEqual(m.docClient)
        }
        if (m.mainScroll !== null) {
          expect(m.mainScroll, `${path}: <main> overflows at ${width}px`).toBeLessThanOrEqual(m.docClient)
        }

        // ORDERING IS LOAD-BEARING. Everything above measures, attaches and asserts
        // the CLOSED state, which is the state the defect is about. Only now does
        // anything open the menu.
        if (s.anonymous) return

        const opened = await expectDestinationsReachable(page)
        if (!opened) return

        // One extra screenshot on ONE surface: the header is the same component
        // everywhere, and a near-identical PNG per surface would make the human pass
        // worse rather than better. Artifact, not assertion.
        if (s.name === 'jobs') {
          const openShot = testInfo.outputPath(`${s.name}-${width}-nav-open.png`)
          await page.screenshot({ path: openShot, fullPage: true })
          await testInfo.attach(`screenshot-${s.name}-${width}-nav-open`, {
            path: openShot,
            contentType: 'image/png',
          })
        }

        // The panel is a full-bleed dropdown pinned to both viewport edges, so it
        // cannot overflow by construction - which is exactly the kind of claim that
        // needs measuring rather than asserting in prose.
        const open = await page.evaluate(() => ({
          docScroll: document.documentElement.scrollWidth,
          docClient: document.documentElement.clientWidth,
        }))
        await testInfo.attach(`widths-${s.name}-${width}-nav-open`, {
          body: JSON.stringify({ surface: s.name, path, width, ...open }, null, 2),
          contentType: 'application/json',
        })
        expect(
          open.docScroll,
          `${path}: document overflows at ${width}px with the nav panel open`,
        ).toBeLessThanOrEqual(open.docClient)

        await closeNavPanel(page)
      })
    }
  })
}
