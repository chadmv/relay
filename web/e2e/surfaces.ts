import { expect, type Page } from '@playwright/test'
import type { Seed } from './fixtures'

export interface Surface {
  name: string
  // A FUNCTION OF Seed, not a plain string. Playwright COLLECTS every spec file
  // across every project - including chromium/webkit, which depend on setup -
  // before it RUNS any test in any project (that is inherent to building the
  // full test list up front, not a bug in how dependencies are scheduled). A
  // literal seed-derived path here would need seed.json to already exist at
  // collection time, before the setup project that WRITES it has run - a
  // chicken-and-egg ENOENT that only shows up once e2e/.run/ starts clean
  // (measured: local runs stayed green only because a stale seed.json from a
  // previous run persisted; CI's clean checkout caught it immediately).
  path: (seed: Seed) => string
  // What must be on screen before a width is measured. Also a function of Seed
  // for the same collection-vs-execution reason as `path`.
  //
  // NEVER waitForLoadState('networkidle'). The jobs, workers and schedules list
  // hooks all set refetchInterval (web/src/jobs/useJobs.ts:11,
  // web/src/workers/useWorkers.ts:11, web/src/schedules/useSchedules.ts:12,
  // default 3000ms), so the network never goes idle on those pages and the wait
  // would hang for the full test timeout.
  ready: (page: Page, seed: Seed) => Promise<void>
  // DECLARED COVERAGE LIMIT, not a discovered one. Slice 1 runs NO relay-agent,
  // so no worker row can exist. A surface marked 'empty' is covered in its EMPTY
  // STATE ONLY - do not read a pass here as a populated-state pass. Closing this
  // is slice 2 (an agent in the harness).
  population: 'populated' | 'empty'
  // True only for /auth. Every project's storageState carries the seeded
  // admin's token (playwright.config.ts), so a plain page.goto('/auth') lands
  // an already-authenticated session and PublicOnlyRoute redirects straight to
  // /jobs before "Sign in" ever renders - measured directly, not assumed.
  // Consumers (layout.spec.ts) must strip the token before navigating when this
  // is true, the same way auth.spec.ts's file-level test.use() does for the
  // whole file.
  anonymous?: boolean
}

const h1 = (name: string) => async (page: Page) => {
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible()
}

// NO Seed PARAMETER. The list of surfaces (names, titles, count) is static and
// does not depend on any seeded value - only the individual path()/ready()
// closures do, and those are called per-test, after readSeed() has succeeded.
export function surfaces(): Surface[] {
  return [
    // The CONTROL. /auth renders no app shell (web/src/app/PublicOnlyRoute.tsx
    // wraps nothing), so it has never overflowed. Its presence is what makes a
    // header/main finding an attribution rather than a correlation - the
    // 2026-08-13 slice found its fourth cause exactly this way.
    { name: 'auth', path: () => '/auth', population: 'populated', anonymous: true, ready: h1('Sign in') },

    {
      name: 'jobs',
      path: () => '/jobs',
      population: 'populated',
      ready: async (p, seed) => {
        await expect(p.getByRole('link', { name: seed.jobName })).toBeVisible()
      },
    },
    {
      name: 'job-detail',
      path: (seed) => `/jobs/${seed.jobId}`,
      population: 'populated',
      ready: async (p, seed) => {
        await expect(p.getByRole('heading', { name: seed.jobName, level: 1 })).toBeVisible()
      },
    },
    { name: 'job-new', path: () => '/jobs/new', population: 'populated', ready: h1('New job') },

    // EMPTY-STATE ONLY: no agent runs, so no worker row exists.
    { name: 'workers', path: () => '/workers', population: 'empty', ready: h1('Workers') },

    {
      name: 'schedules',
      path: () => '/schedules',
      population: 'populated',
      ready: async (p, seed) => {
        // exact:true, unlike jobs' equivalent locator above: SchedulesTable.tsx
        // ALSO renders an `aria-label="Edit ${name}"` link per row
        // (SchedulesTable.tsx:104), which JobsTable does not, so the
        // substring-matching default resolves two elements here. Measured, not
        // assumed - the ambiguous form threw a strict-mode violation.
        await expect(p.getByRole('link', { name: seed.scheduleName, exact: true })).toBeVisible()
      },
    },
    {
      // ScheduleDetailPage has no <h1> - the name is a <span>
      // (web/src/schedules/ScheduleDetailPage.tsx:108).
      name: 'schedule-detail',
      path: (seed) => `/schedules/${seed.scheduleId}`,
      population: 'populated',
      ready: async (p, seed) => {
        await expect(p.getByText(seed.scheduleName, { exact: true })).toBeVisible()
      },
    },

    {
      name: 'admin-users',
      path: () => '/admin/users',
      population: 'populated',
      ready: async (p, seed) => {
        await expect(p.getByText(seed.userEmail)).toBeVisible()
      },
    },
    {
      name: 'admin-invites',
      path: () => '/admin/invites',
      population: 'populated',
      ready: async (p, seed) => {
        await expect(p.getByText(seed.inviteEmail)).toBeVisible()
      },
    },
    {
      name: 'admin-enrollments',
      path: () => '/admin/enrollments',
      population: 'populated',
      ready: async (p, seed) => {
        await expect(p.getByText(seed.enrollmentHostname)).toBeVisible()
      },
    },
    {
      // Populated - see fixtures.ts on selector-only reservations. The create
      // form's WorkerPicker is the only empty-state part of this page.
      name: 'admin-reservations',
      path: () => '/admin/reservations',
      population: 'populated',
      ready: async (p, seed) => {
        await expect(p.getByText(seed.reservationName)).toBeVisible()
      },
    },
    {
      name: 'admin-server',
      path: () => '/admin/server',
      population: 'populated',
      ready: async (p) => {
        // NavLink sets aria-current="page" on the active tab
        // (web/src/admin/AdminTabs.tsx:19-31), which is a cheaper and more
        // specific readiness signal than any of the tab's own numbers.
        await expect(p.getByRole('link', { name: 'Server' })).toHaveAttribute('aria-current', 'page')
      },
    },
    {
      name: 'profile',
      path: () => '/profile/identity',
      population: 'populated',
      ready: async (p) => {
        await expect(p.locator('main h1')).toBeVisible()
      },
    },
  ]
}
