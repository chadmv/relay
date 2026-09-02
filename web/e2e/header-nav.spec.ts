import { expect, test, type Page } from '@playwright/test'

// ONE PAGE, not every surface. The header is the same component on all thirteen
// shell surfaces, and layout.spec.ts already runs the reachability predicate across
// all of them at three widths. What this file adds is what that loop cannot say: at
// which width, exactly, the collapse happens, and whether a real key press can
// drive it.
//
// No fourth entry was added to layout.spec.ts's WIDTHS for the same reason: a
// fourth width there costs fourteen surfaces times two engines for a property one
// page needs.

const DESTINATIONS = ['Jobs', 'Workers', 'Schedules', 'Admin'] as const

// Gated on the page's own <h1> rather than on the header: the header renders as
// soon as AuthProvider reports authenticated, so a header-only gate can resolve
// before the route has finished mounting.
async function gotoJobs(page: Page, width: number) {
  await page.setViewportSize({ width, height: 900 })
  await page.goto('/jobs')
  await expect(page.getByRole('heading', { name: 'Jobs', level: 1 })).toBeVisible()
}

function header(page: Page) {
  return page.getByRole('banner')
}

test.describe('header nav collapse', () => {
  test('at 1280 every destination is inline and the collapse toggle is not exposed', async ({
    page,
  }) => {
    await gotoJobs(page, 1280)
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeVisible()
    }
    // toBeHidden, not toHaveCount(0): the toggle is always in the DOM and is removed
    // from the user's reach by CSS alone. An absence assertion would pass against a
    // component that stopped rendering it at every width.
    await expect(header(page).getByRole('button', { name: /menu/i })).toBeHidden()
  })

  // TWO VIEWPORTS ONE PIXEL APART. This is what makes the pair a test of the md
  // breakpoint rather than of "something collapses somewhere": move the breakpoint
  // and exactly one of these two goes red.
  test('at 768 the nav is still inline', async ({ page }) => {
    await gotoJobs(page, 768)
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeVisible()
    }
    await expect(header(page).getByRole('button', { name: /menu/i })).toBeHidden()
    const m = await page.evaluate(() => ({
      s: document.documentElement.scrollWidth,
      c: document.documentElement.clientWidth,
    }))
    expect(m.s, 'document overflows at 768px').toBeLessThanOrEqual(m.c)
  })

  test('at 767 the nav is collapsed', async ({ page }) => {
    await gotoJobs(page, 767)
    await expect(header(page).getByRole('button', { name: /menu/i })).toBeVisible()
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeHidden()
    }
    const m = await page.evaluate(() => ({
      s: document.documentElement.scrollWidth,
      c: document.documentElement.clientWidth,
    }))
    expect(m.s, 'document overflows at 767px').toBeLessThanOrEqual(m.c)
  })

  test('at 375 the toggle is the only visible nav control', async ({ page }) => {
    await gotoJobs(page, 375)
    await expect(header(page).getByRole('button', { name: /menu/i })).toBeVisible()
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeHidden()
    }
  })
})

// TAGGED so playwright.config.ts's webkit project grep runs this describe in both
// engines. This is the only lane in the repo that can send a real key, and the
// engine divergence it is here for is the one UserMenu documents: WebKit does not
// focus a <button> on click, so a focus-restore contract proven only through a
// click in chromium says nothing about it. Opening via Tab plus Enter puts focus on
// the toggle in BOTH engines by construction.
test.describe('header nav collapse keyboard @webkit', () => {
  async function tabToToggle(page: Page) {
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    const toggle = header(page).getByRole('button', { name: /menu/i })
    let reached = false
    for (let i = 0; i < 20 && !reached; i++) {
      await page.keyboard.press('Tab')
      reached = await toggle.evaluate((el) => el === document.activeElement)
    }
    expect(reached, 'Tab never reached the collapse toggle within 20 presses').toBe(true)
    return toggle
  }

  test('a real Tab press reaches the collapse toggle and Enter opens the panel', async ({
    page,
  }) => {
    await gotoJobs(page, 375)
    const toggle = await tabToToggle(page)
    await expect(page.getByTestId('header-nav-panel')).toBeHidden()

    await page.keyboard.press('Enter')

    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeVisible()
    }
  })

  test('Escape closes the panel and returns focus to the toggle', async ({ page }) => {
    await gotoJobs(page, 375)
    const toggle = await tabToToggle(page)
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('header-nav-panel')).toBeVisible()

    await page.keyboard.press('Escape')

    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByTestId('header-nav-panel')).toBeHidden()
    expect(await toggle.evaluate((el) => el === document.activeElement)).toBe(true)
  })
})
