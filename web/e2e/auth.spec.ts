import { expect, test, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { readSeed } from './fixtures'

const runDir = join(dirname(fileURLToPath(import.meta.url)), '.run')
// env.json is written by e2e/ensure-db.mjs, which runs BEFORE Playwright even
// starts - not by the setup project - so it is safe to read at module (i.e.
// collection-time) scope. seed.json is different: it is written by the setup
// project's own test, and Playwright collects every spec file across every
// project - including this one, which depends on setup - before it runs any
// test in any project. readSeed() must therefore stay INSIDE the one test body
// that needs it below, not up here.
const runEnv = JSON.parse(readFileSync(join(runDir, 'env.json'), 'utf8')) as {
  adminEmail: string
  adminPassword: string
}

// The whole file runs ANONYMOUS: it is about the unauthenticated state, and the
// logout test in particular MUST own its own token. DELETE /v1/auth/token is
// handleLogoutCurrent (internal/api/server.go:116) and destroys the caller's own
// token, so logging out with the token in .run/state.json would silently
// unauthenticate every spec that runs after this one. The suite is SERIALIZED, so
// that is an ordering landmine, not a hypothetical.
test.use({ storageState: { cookies: [], origins: [] } })

async function signIn(page: Page) {
  await page.goto('/auth')
  // Case-insensitive regexes, NOT the literals 'Email'/'Password': the <label>
  // carries Tailwind's `uppercase` (web/src/components/Field.tsx:30-35), and
  // Playwright's accessible-name computation applies text-transform, so the
  // accessible name is "EMAIL" while the source says "Email".
  await page.getByLabel(/^email$/i).fill(runEnv.adminEmail)
  await page.getByLabel(/^password$/i).fill(runEnv.adminPassword)
  await page.getByRole('button', { name: /sign in/i }).click()
}

test('a real login lands on /jobs', async ({ page }) => {
  await signIn(page)

  // CORRECTION: an earlier draft of this comment claimed PublicOnlyRoute
  // (web/src/app/PublicOnlyRoute.tsx:9) had zero test coverage, based on `rg
  // PublicOnlyRoute web/src --glob '*.test.tsx'` returning nothing. That grep
  // checks for the component's NAME, not the BEHAVIOUR - src/App.test.tsx:19-38
  // ("a successful login lands the user on the jobs page") already drives the
  // real <App/> tree through this exact redirect and asserts JobsPage's
  // OVERVIEW eyebrow (confirmed unique to JobsPage.tsx:77), all without ever
  // naming PublicOnlyRoute. Measured directly: mutating the redirect target to
  // /schedules turns App.test.tsx RED, not green. So this assertion does not
  // close a coverage gap - it is independent confirmation, through a real
  // browser and a real compiled server, of something jsdom already catches.
  await expect(page).toHaveURL(/\/jobs$/)
  await expect(page.getByRole('heading', { name: 'Jobs', level: 1 })).toBeVisible()
})

test('a deep link while logged out redirects to /auth', async ({ page }) => {
  // A FULL page load at a client-only route, unlike anything jsdom/MSW can do
  // (MSW mocks fetch; it never issues a real GET that a real server answers).
  // This exercises the SEAM a unit test cannot reach even though its two halves
  // both have unit coverage in isolation: web/embed_test.go's
  // TestHandler_ServesIndexForUnknownRoute proves webui.Handler()'s SPA
  // fallback in isolation, and internal/api/static_test.go's
  // TestServer_StaticHandlerServesNonAPIPaths proves api.Server's mux routes to
  // a *synthetic* static handler - but nothing exercises the WIRING between
  // them, `s.StaticHandler = d.static` in cmd/relay-server/http_server.go:155,
  // through a real running binary. Measured: deleting that one assignment
  // leaves npm test, tsc -b AND go test ./... all green (the surrounding
  // comment in http_server.go already says as much - "is likewise green
  // everywhere") while every test in this file goes red, because /auth itself
  // is also a path only the SPA fallback can answer. This is the one mutation
  // in this file that is genuinely browser-only.
  const seed = readSeed()
  await page.goto(`/jobs/${seed.jobId}`)
  await expect(page).toHaveURL(/\/auth$/)
  await expect(page.getByRole('heading', { name: 'Sign in', level: 1 })).toBeVisible()
})

test('logout returns to /auth and clears relay.token', async ({ page }) => {
  await signIn(page)
  await expect(page.getByRole('heading', { name: 'Jobs', level: 1 })).toBeVisible()

  // The UserMenu toggle's accessible name is the signed-in email
  // (web/src/shell/UserMenu.tsx:174-185). getByRole name matching is
  // case-insensitive substring by default, which is what makes the toggle's own
  // `uppercase` class harmless here.
  await page.getByRole('button', { name: runEnv.adminEmail }).click()
  await page.getByRole('button', { name: 'Log out' }).click()

  await expect(page).toHaveURL(/\/auth$/)
  // Assert ABSENCE from the actual store, not that a clear function was called.
  // The key is web/src/lib/token.ts:1.
  await expect
    .poll(() => page.evaluate(() => window.localStorage.getItem('relay.token')))
    .toBeNull()
  // The destination claims focus, so a keyboard user who signs out does not land
  // on <body> with their next Tab starting from the top of the document. Polled
  // rather than read once: focus lands on the commit that mounts the form, which
  // is after the URL settles. jsdom cannot answer this - it has no browser event
  // loop and no real navigation.
  await expect
    .poll(() => page.evaluate(() => document.activeElement?.id ?? null))
    .toBe('email')
})
