import { expect, type Page } from '@playwright/test'

// The four top-level destinations. /profile's three are reached through the user
// chip, which is visible at every width, so they are already behind a visible
// control and are not this file's business.
const DESTINATIONS = ['Jobs', 'Workers', 'Schedules', 'Admin'] as const

// SCOPED TO THE HEADER, and exact. Playwright matches an accessible name
// case-insensitively and by SUBSTRING by default, and four pages render a
// breadcrumb link whose name contains one of these words - so an unscoped locator
// resolves two elements and throws a strict-mode violation on job-detail, job-new,
// workers/:id and schedule-detail.
function header(page: Page) {
  return page.getByRole('banner')
}

// The assertion the backlog item asks for, stated so that it FAILS at HEAD: at
// every width, every destination is visible, or reachable through a control that is
// visible. A scrollWidth <= clientWidth gate cannot express this - content that
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
    if (await link.isVisible()) continue
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
// keydown listener, which is the dismissal route a keyboard user has, and it leaves
// the page as it was found so the caller's next step is not measuring a state it
// created. The hidden assertion is a positive control that the close actually
// happened - without it a no-op here would silently leave the panel open.
export async function closeNavPanel(page: Page): Promise<void> {
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('header-nav-panel')).toBeHidden()
}
