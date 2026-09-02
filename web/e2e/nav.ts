import { expect, type Page } from '@playwright/test'

// The four top-level destinations.
export const DESTINATIONS = ['Jobs', 'Workers', 'Schedules', 'Admin'] as const

// Page bodies render back-links whose accessible names embed these words, and
// Playwright's name matching is case-insensitive substring by default. Exact
// matching plus the banner scope is what keeps these locators on the header's own
// links.
function header(page: Page) {
  return page.getByRole('banner')
}

// At every width, every destination is visible, or reachable through a control that
// is visible. A document-level scrollWidth gate cannot express this: content that
// overflows into its own scroll wrapper reads as zero document overflow and passes.
//
// Returns whether the panel was opened, and LEAVES IT OPEN when it was, so the
// caller can measure the open state before closing it with closeNavPanel.
export async function expectDestinationsReachable(page: Page): Promise<boolean> {
  const h = header(page)
  const toggle = h.getByRole('button', { name: /menu/i })
  let opened = false
  for (const name of DESTINATIONS) {
    const link = h.getByRole('link', { name, exact: true })
    if (await link.isVisible()) {
      // isVisible() is checkVisibility plus a non-empty box, and neither considers
      // clipping by an ancestor scroll container - a link scrolled out of a
      // horizontally scrolling nav still reports visible, which is the exact defect
      // this predicate exists to catch. toBeInViewport goes through
      // IntersectionObserver, which does clip against intermediate scrollers.
      await expect(link, `${name} has a layout box but is clipped out of view`).toBeInViewport()
      continue
    }
    await expect(toggle, `${name} is not visible and no collapse control is either`).toBeVisible()
    if (!opened) {
      await toggle.click()
      opened = true
    }
    await expect(link, `${name} is not reachable through the collapse control`).toBeVisible()
  }
  return opened
}

// Escape rather than a second click on the toggle: it exercises the document
// keydown listener, which is the dismissal route a keyboard user has. The hidden
// assertion is a positive control that the close actually happened - without it a
// no-op here would silently leave the panel open.
export async function closeNavPanel(page: Page): Promise<void> {
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('header-nav-panel')).toBeHidden()
}
