import { expect, test } from '@playwright/test'
import { readSeed, type Seed } from './fixtures'

// WHY THESE TWO TABLES. EnrollmentsTable and InvitesTable have ZERO focusable
// elements in any row (web/src/admin/enrollments/EnrollmentsTable.tsx:49-76,
// web/src/admin/invites/InvitesTable.tsx:59+ render only text, Chips and cells),
// so their clipped right-hand columns are reachable only through the scroll
// wrapper's own tab stop - the tabIndex={0} + role="group" + aria-label at
// web/src/components/holo/Table.tsx:181-193. That fix was chosen SPECIFICALLY
// because the previous behaviour leaned on Chromium's implicit scroller
// focusability, which WebKit does not grant, and it has never been watched
// working by anyone in any engine.
//
// WEBKIT IS NOT SAFARI. Playwright's webkit is a bundled WebKit build. It
// exercises WebKit's focusability semantics, which is what the divergence is
// actually about; it does not exercise Safari's chrome, extensions or platform
// integration. The Known Limitation "Safari has not been opened" is NARROWED by
// this file, not retired.
//
// COVERAGE LIMIT (slice 1): no relay-agent runs, so no worker row exists. This
// file says nothing about WorkersTable's own scroll wrapper.
test.describe('scroll-wrapper keyboard reachability @webkit', () => {
  // NARROW ON PURPOSE. The wrapper only scrolls when its content exceeds it, and
  // both tables' own minimum widths (600-700 or so pixels) fit easily in a
  // 1280px viewport - at the default width every assertion below would be
  // vacuously true.
  //
  // DELIBERATELY NOT SPELLED as an actual Tailwind arbitrary-value class
  // anywhere in this file, comments included. Tailwind v4 scans the WHOLE
  // PROJECT for class-shaped substrings, not just what a JS bundle imports, so
  // writing the real class text here would keep its CSS rule alive regardless
  // of whether the component that is supposed to own it still emits it.
  // Measured directly while investigating this file's own mutation matrix
  // entry: the plan's original M2 candidate (a numeric-literal template
  // interpolation, e.g. `min-w-[${660}px]`) turned out to be constant-folded
  // by esbuild back into the literal string during a production build, so
  // Tailwind's scanner still found it and the mutation never actually
  // reproduced. An object-property lookup (reading the number off a `const`
  // object at the interpolation site, rather than writing it inline) is not
  // folded the same way and genuinely disappears from both the built CSS and
  // JS - but ONLY once this comment stopped independently keeping the class
  // alive through the same whole-project scan.
  test.use({ viewport: { width: 480, height: 900 } })

  // marker is a FUNCTION OF Seed, not a resolved value, for the same
  // collection-vs-execution reason as surfaces.ts's path()/ready(): this array
  // is built at collection time, before setup has written e2e/.run/seed.json.
  // readSeed() is called inside each test body instead, after dependencies
  // guarantee setup has actually run.
  const cases = [
    { path: '/admin/enrollments', group: 'Agent enrollments, scrolls horizontally', marker: (seed: Seed) => seed.enrollmentHostname },
    { path: '/admin/invites', group: 'Invites, scrolls horizontally', marker: (seed: Seed) => seed.inviteEmail },
  ]

  for (const c of cases) {
    test(`${c.path}: a real Tab press reaches the labelled scroll region`, async ({ page }) => {
      const seed = readSeed()
      await page.goto(c.path)
      await expect(page.getByText(c.marker(seed))).toBeVisible()

      // The accessible name is DERIVED from the table's own label
      // (Table.tsx:190), so this locator also pins that it has not drifted.
      const group = page.getByRole('group', { name: c.group })
      await expect(group).toHaveCount(1)

      // REAL key events. web/src/components/holo/Table.test.tsx:317-328 already
      // proves tabindex="0" is in the DOM and proves nothing about keyboard
      // reachability, which is the whole reason this file exists.
      await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
      let reached = false
      for (let i = 0; i < 40 && !reached; i++) {
        await page.keyboard.press('Tab')
        reached = await group.evaluate((el) => el === document.activeElement)
      }
      expect(reached, `Tab never reached "${c.group}" within 40 presses`).toBe(true)
    })

    test(`${c.path}: arrow keys scroll the clipped columns into view`, async ({ page }) => {
      const seed = readSeed()
      await page.goto(c.path)
      await expect(page.getByText(c.marker(seed))).toBeVisible()
      const group = page.getByRole('group', { name: c.group })

      // PRECONDITION, asserted rather than assumed, and it is the only gate in
      // the repo that catches a computed Tailwind class. Tailwind v4 scans source
      // STATICALLY: if a consumer built its min-w-[...] string instead of writing
      // the literal, the DOM class attribute is byte-identical - so every jsdom
      // class-string pin stays green - while the production bundle emits no rule
      // at all, the wrapper never overflows, and the a11y fix silently does
      // nothing.
      const overflow = await group.evaluate((el) => el.scrollWidth - el.clientWidth)
      expect(overflow, 'scroll wrapper is not actually scrollable - did the min-width rule reach the bundle?').toBeGreaterThan(0)

      await group.focus()
      const before = await group.evaluate((el) => el.scrollLeft)
      for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowRight')
      await expect
        .poll(() => group.evaluate((el) => el.scrollLeft), { message: 'ArrowRight did not scroll the focused wrapper' })
        .toBeGreaterThan(before)
    })
  }
})
