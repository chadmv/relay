import { expect, test, type Page } from '@playwright/test'

// A scrollWidth gate cannot tell "fits" from "clipped behind a scroller", and a
// row of inputs is exactly the shape that clips - layout.spec.ts would report a
// row overflowing into its own horizontal scroller as passing. So this asks the
// question per CONTROL instead: is the row's remove control, and the submit
// control, actually in the viewport.
const WIDTHS = [320, 375, 1280] as const

// A VERTICAL-ONLY scroll. The form is taller than the viewport at every width
// here, so something has to scroll before toBeInViewport means anything - and
// scrollIntoViewIfNeeded would also scroll an ancestor HORIZONTALLY, which is
// the axis under test. Scrolling the window by a vertical delta cannot.
async function scrollVerticallyInto(page: Page, controlName: string) {
  await page.getByRole('button', { name: controlName }).evaluate((el) => {
    const box = el.getBoundingClientRect()
    window.scrollBy(0, box.top - window.innerHeight / 2)
  })
}

for (const width of WIDTHS) {
  test.describe(`job builder ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } })

    test('a task row remove control and the submit control are reachable with two rows', async ({ page }) => {
      await page.goto('/jobs/new')
      await expect(page.getByRole('heading', { name: 'New job', level: 1 })).toBeVisible()
      await page.getByRole('button', { name: 'Add task' }).click()

      const row = page.getByRole('group', { name: 'Task 2' })
      await expect(row.getByRole('textbox', { name: 'Task name' })).toBeVisible()

      for (const name of ['Remove task 2', 'Create job']) {
        await scrollVerticallyInto(page, name)
        const control = page.getByRole('button', { name })
        await expect(control, `${name} is not in the viewport at ${width}px`).toBeInViewport()

        // The horizontal axis on its own, so a failure says which axis moved.
        const box = await control.boundingBox()
        expect(box, `${name} has no box at ${width}px`).not.toBeNull()
        expect(box!.x, `${name} starts left of the viewport at ${width}px`).toBeGreaterThanOrEqual(0)
        expect(
          box!.x + box!.width,
          `${name} extends past the right edge at ${width}px`,
        ).toBeLessThanOrEqual(width)
      }
    })
  })
}
