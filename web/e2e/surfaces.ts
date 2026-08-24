import { expect, type Page } from '@playwright/test'
import type { Seed } from './fixtures'

export interface Surface {
  name: string
  path: string
  // What must be on screen before a width is measured.
  //
  // NEVER waitForLoadState('networkidle'). The jobs, workers and schedules list
  // hooks all set refetchInterval (web/src/jobs/useJobs.ts:11,
  // web/src/workers/useWorkers.ts:11, web/src/schedules/useSchedules.ts:12,
  // default 3000ms), so the network never goes idle on those pages and the wait
  // would hang for the full test timeout.
  ready: (page: Page) => Promise<void>
  // DECLARED COVERAGE LIMIT, not a discovered one. Slice 1 runs NO relay-agent,
  // so no worker row can exist. A surface marked 'empty' is covered in its EMPTY
  // STATE ONLY - do not read a pass here as a populated-state pass. Closing this
  // is slice 2 (an agent in the harness).
  population: 'populated' | 'empty'
}

const h1 = (name: string) => async (page: Page) => {
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible()
}

export function surfaces(seed: Seed): Surface[] {
  return [
    // The CONTROL. /auth renders no app shell (web/src/app/PublicOnlyRoute.tsx
    // wraps nothing), so it has never overflowed. Its presence is what makes a
    // header/main finding an attribution rather than a correlation - the
    // 2026-08-13 slice found its fourth cause exactly this way.
    { name: 'auth', path: '/auth', population: 'populated', ready: h1('Sign in') },

    {
      name: 'jobs',
      path: '/jobs',
      population: 'populated',
      ready: async (p) => {
        await expect(p.getByRole('link', { name: seed.jobName })).toBeVisible()
      },
    },
    { name: 'job-detail', path: `/jobs/${seed.jobId}`, population: 'populated', ready: h1(seed.jobName) },
    { name: 'job-new', path: '/jobs/new', population: 'populated', ready: h1('New job') },

    // EMPTY-STATE ONLY: no agent runs, so no worker row exists.
    { name: 'workers', path: '/workers', population: 'empty', ready: h1('Workers') },

    {
      name: 'schedules',
      path: '/schedules',
      population: 'populated',
      ready: async (p) => {
        await expect(p.getByRole('link', { name: seed.scheduleName })).toBeVisible()
      },
    },
    {
      // ScheduleDetailPage has no <h1> - the name is a <span>
      // (web/src/schedules/ScheduleDetailPage.tsx:108).
      name: 'schedule-detail',
      path: `/schedules/${seed.scheduleId}`,
      population: 'populated',
      ready: async (p) => {
        await expect(p.getByText(seed.scheduleName, { exact: true })).toBeVisible()
      },
    },

    {
      name: 'admin-users',
      path: '/admin/users',
      population: 'populated',
      ready: async (p) => {
        await expect(p.getByText(seed.userEmail)).toBeVisible()
      },
    },
    {
      name: 'admin-invites',
      path: '/admin/invites',
      population: 'populated',
      ready: async (p) => {
        await expect(p.getByText(seed.inviteEmail)).toBeVisible()
      },
    },
    {
      name: 'admin-enrollments',
      path: '/admin/enrollments',
      population: 'populated',
      ready: async (p) => {
        await expect(p.getByText(seed.enrollmentHostname)).toBeVisible()
      },
    },
    {
      // Populated - see fixtures.ts on selector-only reservations. The create
      // form's WorkerPicker is the only empty-state part of this page.
      name: 'admin-reservations',
      path: '/admin/reservations',
      population: 'populated',
      ready: async (p) => {
        await expect(p.getByText(seed.reservationName)).toBeVisible()
      },
    },
    {
      name: 'admin-server',
      path: '/admin/server',
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
      path: '/profile/identity',
      population: 'populated',
      ready: async (p) => {
        await expect(p.locator('main h1')).toBeVisible()
      },
    },
  ]
}
