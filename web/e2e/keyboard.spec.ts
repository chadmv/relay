import { expect, type Locator, test } from '@playwright/test'
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
  // by esbuild back into the literal string INSIDE THE BUNDLE. That fold is
  // irrelevant here: @tailwindcss/vite builds its Scanner over the Vite root
  // ({base: viteRoot, pattern: '**/*'}) and reads SOURCE FILES ON DISK, never
  // the emitted bundle, so the fold does not make the rule reappear. The
  // reason that mutation never reproduced was this comment's own literal
  // text independently keeping the class alive through the same
  // whole-project scan - confirmed by re-running it with the class text
  // absent from the comment, which goes RED (6 tests, the same failure set
  // as the shipped form). An object-property lookup (reading the number off
  // a `const` object at the interpolation site, rather than writing it
  // inline) genuinely disappears from both the built CSS and the source scan
  // - but ONLY once this comment stopped independently keeping the class
  // alive.
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

  // PRECONDITION, asserted rather than assumed, and it is the only gate in the
  // repo that catches a computed Tailwind class. Tailwind v4 scans source
  // STATICALLY: if a consumer built its min-width utility string instead of
  // writing the literal, the DOM class attribute is byte-identical - so every
  // jsdom class-string pin stays green - while the production bundle emits no
  // rule at all, the wrapper barely overflows, and the a11y fix silently does
  // nothing.
  //
  // Called BEFORE the row-marker wait in both tests below, deliberately. The
  // group's width comes from the table header's own column widths (Table.tsx
  // renders the wrapper and header unconditionally; `children` - the row data -
  // is not a precondition for it), so this does not need the marker to be
  // visible first. Ordering it first also matters for the failure mode: when
  // the min-width rule is actually missing, the table's 1.6fr track collapses
  // toward zero width and the marker (inside that column) never becomes
  // visible - so with the marker wait first, both tests used to die on a
  // generic hidden-element timeout that never mentions min-width.
  //
  // The threshold is 100, not 0. Measured directly by mutating both tables'
  // MIN_W into the object-property form described above and re-running: the
  // wrapper is NOT flush with its content even with the rule missing - the
  // fixed-pixel columns and cell padding alone (undamaged by the mutation,
  // since COLS is a plain literal) still produce a small residual overflow of
  // 51px (enrollments) and 32px (invites), which `toBeGreaterThan(0)` cannot
  // tell apart from a real one. With the rule applied, the same two surfaces
  // measured 222px and 302px. 100 sits with comfortable margin on both sides of
  // both pairs.
  async function assertScrollable(group: Locator) {
    await expect(group).toHaveCount(1)
    const overflow = await group.evaluate((el) => el.scrollWidth - el.clientWidth)
    expect(overflow, 'scroll wrapper is not actually scrollable - did the min-width rule reach the bundle?').toBeGreaterThan(100)
  }

  for (const c of cases) {
    test(`${c.path}: a real Tab press reaches the labelled scroll region`, async ({ page }) => {
      const seed = readSeed()
      await page.goto(c.path)

      // The accessible name is DERIVED from the table's own label
      // (Table.tsx:190), so this locator also pins that it has not drifted.
      const group = page.getByRole('group', { name: c.group })
      await assertScrollable(group)

      await expect(page.getByText(c.marker(seed))).toBeVisible()

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
      const group = page.getByRole('group', { name: c.group })
      await assertScrollable(group)

      await expect(page.getByText(c.marker(seed))).toBeVisible()

      await group.focus()
      const before = await group.evaluate((el) => el.scrollLeft)
      for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowRight')
      await expect
        .poll(() => group.evaluate((el) => el.scrollLeft), { message: 'ArrowRight did not scroll the focused wrapper' })
        .toBeGreaterThan(before)
    })
  }
})
