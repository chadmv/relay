import { expect, test } from '@playwright/test'

test('placeholder - replaced in Task 8', async ({ page }) => {
  await page.goto('/auth')
  await expect(page).toHaveURL(/\/auth$/)
})
