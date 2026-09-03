import { expect, type Locator, test } from '@playwright/test'
import { readSeed, TASK_NAMES, type Seed } from './fixtures'

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
  // of whether the component that is supposed to own it still emits it. This
  // is demonstrated, not theoretical: an earlier draft of this file spelled a
  // placeholder as the literal class text, which the scanner matched, and an
  // A/B build showed removing that placeholder was the only CSS delta.
  //
  // The plan's M2 mutation candidate - the pixel number moved into a template
  // interpolation inside the same bracket syntax, rather than written as a
  // plain digit - is not class-shaped text, so the scanner never matches it
  // regardless of what this comment says. Measured
  // directly on this tree, with this comment byte-identical to what you are
  // reading: the mutated form emits no `min-width:660px` / `min-width:740px`
  // rule into the production CSS, and keyboard.spec.ts fails 8 of its 8 tests
  // (4 chromium + 4 webkit) - the same failure set as the shipped
  // object-property form below produces when it is itself mutated back to a
  // bare string literal. An earlier run of this same M2 mutation was reported
  // as not reproducing; that report was wrong, and this comment's text was
  // not the cause (it is not class-shaped either). The actual cause of that
  // earlier false negative is unknown. The likeliest candidate, unverified,
  // is a stale `web/dist` embed: `//go:embed` snapshots the SPA at Go compile
  // time (see README.md), so a build that reused an un-rebuilt binary between
  // planting the mutation and running the suite would silently test the
  // pre-mutation bundle.
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

// WHY A BROWSER FOR THIS. The jsdom tests next to TasksTable prove the attributes
// are in the DOM and prove nothing about whether a real Tab press reaches the
// control or whether a real Enter activates it. Only a real key press can say
// the per-row tab stop survived.
//
// THE TAG IS LOAD-BEARING. playwright.config.ts gives the webkit project
// grep: /@webkit/, so an untagged describe runs in chromium only. WebKit's
// focusability semantics are the reason the other describe in this file exists, so
// this one pays for the second engine too.
//
// DEFAULT VIEWPORT. The other describe's test.use is scoped to that describe; this
// one deliberately measures the surface at the size the screenshots use.
test.describe('job-detail task selection @webkit', () => {
  test('Enter on a task control moves the sole aria-current mark, and no row is aria-selected', async ({ page }) => {
    const seed = readSeed()
    await page.goto(`/jobs/${seed.jobId}`)
    await expect(page.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()

    const table = page.getByRole('table', { name: 'Tasks' })
    await expect(table).toHaveCount(1)
    // Asserted in the browser as well as in jsdom (see TasksTable.tsx for why
    // there is no aria-selected) because this is the advertisement the lane
    // exists to remove.
    await expect(table.locator('[aria-selected]')).toHaveCount(0)

    const current = table.locator('[aria-current="true"]')
    await expect(current).toHaveCount(1)
    const startingName = ((await current.textContent()) ?? '').trim()

    // startingName must actually be one of the seeded tasks. `.find()` below
    // returns a truthy value for ANY input string unless it equals all three
    // seeded names at once - impossible for a single string - so it cannot
    // report seed drift on its own; this is the check that can.
    expect(TASK_NAMES, `the marked task was ${JSON.stringify(startingName)}, which is not one of the seeded tasks`).toContain(startingName)

    // Target a task that is NOT already marked. A test that re-selects the default
    // selection passes against a mark hardcoded onto the first row.
    const target = TASK_NAMES.find((n) => n !== startingName)
    const targetButton = table.getByRole('button', { name: target as string, exact: true })
    await expect(targetButton).toHaveCount(1)

    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    let reached = false
    for (let i = 0; i < 80 && !reached; i++) {
      await page.keyboard.press('Tab')
      reached = await targetButton.evaluate((el) => el === document.activeElement)
    }
    expect(reached, `Tab never reached the "${target}" task control within 80 presses`).toBe(true)

    await page.keyboard.press('Enter')

    const after = table.locator('[aria-current="true"]')
    await expect(after).toHaveCount(1)
    await expect(after).toHaveText(target as string)
    await expect(table.locator('[aria-selected]')).toHaveCount(0)
  })

  // COMPUTED STYLE, NOT A SCREENSHOT DIFF. getComputedStyle reads what the
  // cascade actually resolved the Tailwind classes to on THIS element, in a
  // real layout engine (jsdom does none) - it discriminates the two states
  // that matter here directly: a positive-or-default outline-offset (painted
  // outside the border box, where the ancestor's `truncate` clip removes it)
  // from a negative one (painted inside, where the clip cannot reach it).
  test('the focused task control gets a negative-offset outline, not the (clipped) browser default', async ({ page }) => {
    const seed = readSeed()
    await page.goto(`/jobs/${seed.jobId}`)
    await expect(page.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()

    const table = page.getByRole('table', { name: 'Tasks' })
    const target = TASK_NAMES[0]
    const targetButton = table.getByRole('button', { name: target, exact: true })
    await expect(targetButton).toHaveCount(1)

    // A REAL Tab press, not .focus(): a programmatic focus() is not guaranteed
    // to put an element into :focus-visible in every engine, and
    // :focus-visible is exactly what the focus-visible: Tailwind variant gates
    // on. Same loop as the sibling test above.
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    let reached = false
    for (let i = 0; i < 80 && !reached; i++) {
      await page.keyboard.press('Tab')
      reached = await targetButton.evaluate((el) => el === document.activeElement)
    }
    expect(reached, `Tab never reached the "${target}" task control within 80 presses`).toBe(true)

    const outline = await targetButton.evaluate((el) => {
      const s = getComputedStyle(el)
      return {
        style: s.outlineStyle,
        offset: s.outlineOffset,
        width: s.outlineWidth,
        color: s.outlineColor,
        focusVisible: el.matches(':focus-visible'),
      }
    })
    // Both an offset check AND a width/colour check: stripping either the
    // width utility or the colour utility alone left the offset check green
    // before - the offset survives even at the browser's own default width
    // and colour.
    expect(outline.focusVisible, 'element is focused but not :focus-visible').toBe(true)
    expect(outline.style, 'outline-style is none while focused - no ring at all').not.toBe('none')
    expect(parseFloat(outline.offset), `outline-offset was "${outline.offset}", not negative`).toBeLessThan(0)
    expect(outline.width, `outline-width was "${outline.width}", not 2px`).toBe('2px')
    // The exact accent RGB, not merely "not transparent": with no explicit
    // outline-color the two engines disagree on the fallback (chromium:
    // rgb(16, 16, 16); webkit: rgb(237, 233, 254), the page's own fg colour) -
    // both are opaque, so a "not transparent" check would pass against either
    // fallback and miss the missing focus-visible variant that sets the ring's
    // colour. --color-accent (#3dd0f7) resolves to rgb(61, 208, 247) in both
    // engines when that variant is present.
    expect(outline.color, `outline-color was "${outline.color}", not the accent colour`).toBe('rgb(61, 208, 247)')
  })
})

// WHY A BROWSER FOR THIS. jsdom performs no layout: every getBoundingClientRect
// is zero and no stylesheet is loaded, so it can say the ARIA value moved and can
// say nothing about whether a column moved with it. These four are the only
// assertions in this feature that need a layout engine.
//
// THE TAG IS LOAD-BEARING, as in the describes above: playwright.config.ts gives
// the webkit project grep: /@webkit/, so an untagged describe runs in chromium
// only.
test.describe('job-detail split resizer @webkit', () => {
  const SEPARATOR = 'Resize the pipeline and task detail panes'
  // The left pane's own scroll wrapper. Its accessible name is DERIVED from the
  // table's label, so this locator also pins that the name has not drifted.
  const LEFT_GROUP = 'Tasks, scrolls horizontally'

  test('a key press moves a real column', async ({ page }) => {
    const seed = readSeed()
    await page.goto(`/jobs/${seed.jobId}`)
    await expect(page.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()

    const sep = page.getByRole('separator', { name: SEPARATOR })
    await expect(sep).toHaveCount(1)
    const left = page.getByRole('group', { name: LEFT_GROUP })
    await expect(left).toHaveCount(1)
    const before = await left.evaluate((el) => el.getBoundingClientRect().width)

    // A REAL Tab press, not .focus(), matching this file's convention: only a
    // real press can say the tab stop is reachable.
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    let reached = false
    for (let i = 0; i < 120 && !reached; i++) {
      await page.keyboard.press('Tab')
      reached = await sep.evaluate((el) => el === document.activeElement)
    }
    expect(reached, `Tab never reached the split separator within 120 presses`).toBe(true)

    for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowRight')

    // BOTH halves. Updating the ARIA value without applying the width, or the
    // reverse, passes exactly one of these.
    await expect(sep).toHaveAttribute('aria-valuenow', '65')
    await expect
      .poll(() => left.evaluate((el) => el.getBoundingClientRect().width), {
        message: 'aria-valuenow moved but the tasks column did not',
      })
      .toBeGreaterThan(before + 20)
  })

  // COMPUTED STYLE, NOT A SCREENSHOT DIFF. Same shape as the task-selection
  // describe's own outline test: getComputedStyle reads what the cascade
  // actually resolved on THIS element in a real layout engine, discriminating
  // a positive-or-default outline (the browser's own, often clipped by an
  // ancestor) from the app's accent one painted inside the box.
  test('the focused separator gets the accent outline, not the browser default', async ({ page }) => {
    const seed = readSeed()
    await page.goto(`/jobs/${seed.jobId}`)
    await expect(page.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()

    const sep = page.getByRole('separator', { name: SEPARATOR })
    await expect(sep).toHaveCount(1)

    // A REAL Tab press, not .focus(): a programmatic focus() is not guaranteed
    // to put an element into :focus-visible in every engine, and
    // :focus-visible is exactly what the focus-visible: variant gates on.
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    let reached = false
    for (let i = 0; i < 120 && !reached; i++) {
      await page.keyboard.press('Tab')
      reached = await sep.evaluate((el) => el === document.activeElement)
    }
    expect(reached, `Tab never reached the split separator within 120 presses`).toBe(true)

    const outline = await sep.evaluate((el) => {
      const s = getComputedStyle(el)
      return {
        style: s.outlineStyle,
        offset: s.outlineOffset,
        width: s.outlineWidth,
        color: s.outlineColor,
        focusVisible: el.matches(':focus-visible'),
      }
    })
    expect(outline.focusVisible, 'element is focused but not :focus-visible').toBe(true)
    expect(outline.style, 'outline-style is none while focused - no ring at all').not.toBe('none')
    expect(parseFloat(outline.offset), `outline-offset was "${outline.offset}", not negative`).toBeLessThan(0)
    expect(outline.width, `outline-width was "${outline.width}", not 2px`).toBe('2px')
    // The exact accent RGB, not merely "not transparent" - see the sibling
    // outline test above for why a not-transparent check would pass on either
    // engine's own unstyled fallback and miss a dropped colour variant.
    expect(outline.color, `outline-color was "${outline.color}", not the accent colour`).toBe('rgb(61, 208, 247)')
  })

  test('a real drag moves and clamps the split', async ({ page }) => {
    const seed = readSeed()
    await page.goto(`/jobs/${seed.jobId}`)
    await expect(page.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()

    const sep = page.getByRole('separator', { name: SEPARATOR })
    const left = page.getByRole('group', { name: LEFT_GROUP })
    const before = await left.evaluate((el) => el.getBoundingClientRect().width)

    const box = await sep.boundingBox()
    expect(box, 'the separator has no box - is it hidden at this viewport?').not.toBeNull()
    const cx = box!.x + box!.width / 2
    const cy = box!.y + box!.height / 2

    await page.mouse.move(cx, cy)
    await page.mouse.down()
    await page.mouse.move(cx + 120, cy, { steps: 10 })
    await page.mouse.up()

    await expect
      .poll(() => left.evaluate((el) => el.getBoundingClientRect().width), {
        message: 'a rightward drag did not grow the left pane',
      })
      .toBeGreaterThan(before + 20)

    // Far past the edge: the clamp holds on the POINTER path, which is a
    // different code path from the key path above.
    const box2 = await sep.boundingBox()
    await page.mouse.move(box2!.x + box2!.width / 2, box2!.y + box2!.height / 2)
    await page.mouse.down()
    await page.mouse.move(-500, box2!.y + box2!.height / 2, { steps: 10 })
    await page.mouse.up()

    await expect(sep).toHaveAttribute('aria-valuenow', '30')
    const clamped = await left.evaluate((el) => el.getBoundingClientRect().width)
    expect(clamped).toBeLessThan(before)
  })

  test('the split survives a reload', async ({ page }) => {
    const seed = readSeed()
    await page.goto(`/jobs/${seed.jobId}`)
    await expect(page.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()

    const sep = page.getByRole('separator', { name: SEPARATOR })
    await sep.focus()
    for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowLeft')
    await expect(sep).toHaveAttribute('aria-valuenow', '45')

    await page.reload()
    await expect(page.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()
    await expect(page.getByRole('separator', { name: SEPARATOR })).toHaveAttribute(
      'aria-valuenow',
      '45',
    )
  })
})

// A SEPARATE DESCRIBE because test.use is per describe. Below the breakpoint the
// panes stack, so a separator would resize nothing and would be a dead tab stop.
// No jsdom test can see this: no stylesheet is loaded there, so the element is in
// the accessibility tree at every width.
test.describe('job-detail split at a narrow viewport @webkit', () => {
  test.use({ viewport: { width: 375, height: 900 } })

  test('no separator where there is no split', async ({ page }) => {
    const seed = readSeed()
    await page.goto(`/jobs/${seed.jobId}`)
    // Positive controls: the page rendered, and it rendered its own data. A bare
    // count-of-zero passes equally on a page that failed to load.
    await expect(page.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()
    await expect(page.getByRole('table', { name: 'Tasks' })).toHaveCount(1)
    await expect(page.getByRole('separator')).toHaveCount(0)
  })
})
