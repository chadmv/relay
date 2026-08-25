import type { APIRequestContext } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

export interface Seed {
  runId: string
  adminEmail: string
  jobId: string
  jobName: string
  scheduleId: string
  scheduleName: string
  userEmail: string
  inviteEmail: string
  enrollmentHostname: string
  reservationName: string
}

async function post<T>(request: APIRequestContext, token: string, path: string, data: unknown): Promise<T> {
  const res = await request.post(path, { data, headers: { Authorization: `Bearer ${token}` } })
  if (res.status() !== 201) {
    throw new Error(`POST ${path} -> ${res.status()}: ${await res.text()}`)
  }
  return (await res.json()) as T
}

// Fixtures are created through the REST API as the bootstrap admin - the exact
// path the SPA itself uses - and NOT by direct SQL. Direct SQL would bypass
// jobspec.Validate and CreateJobFromSpec (CLAUDE.md, "Single job-spec pipeline"),
// so a fixture could encode a state production cannot produce and the test would
// then assert about a page that cannot exist.
//
// NAMING: every resource carries the run id, and every assertion locates by that
// name. /jobs, /schedules and /admin/* are unscoped global lists, so no spec may
// assert a count over one and no spec may use an nth-row locator - a leftover row
// from an aborted run must not be able to make a locator ambiguous.
export async function seedAll(request: APIRequestContext, token: string, runId: string, adminEmail: string): Promise<Seed> {
  const jobName = `e2e-${runId}-job`
  const job = await post<{ id: string }>(request, token, '/v1/jobs', {
    name: jobName,
    priority: 'normal',
    tasks: [
      { name: 'alpha', command: ['echo', 'alpha'] },
      { name: 'beta', command: ['echo', 'beta'], depends_on: ['alpha'] },
      { name: 'gamma', command: ['echo', 'gamma'], depends_on: ['alpha'] },
    ],
  })

  // The job_spec's own name is deliberately NOT a superstring of scheduleName,
  // so `getByText(scheduleName, { exact: true })` on the detail page cannot be
  // ambiguous.
  const scheduleName = `e2e-${runId}-schedule`
  const schedule = await post<{ id: string }>(request, token, '/v1/scheduled-jobs', {
    name: scheduleName,
    // 24h apart, comfortably above minScheduleInterval = 30s
    // (internal/api/scheduled_jobs.go:17).
    cron_expr: '0 3 * * *',
    timezone: 'UTC',
    overlap_policy: 'skip',
    job_spec: { name: `e2e-${runId}-template`, tasks: [{ name: 'nightly', command: ['echo', 'nightly'] }] },
  })

  const userEmail = `e2e-${runId}-user@relay.test`
  await post(request, token, '/v1/users', {
    email: userEmail,
    name: `E2E ${runId}`,
    password: 'e2e-user-password',
  })

  const inviteEmail = `e2e-${runId}-invite@relay.test`
  await post(request, token, '/v1/invites', { email: inviteEmail, expires_in: '72h' })

  const enrollmentHostname = `e2e-${runId}-agent`
  await post(request, token, '/v1/agent-enrollments', { hostname_hint: enrollmentHostname, ttl_seconds: 3600 })

  // A SELECTOR-only reservation, no worker_ids. handleCreateReservation requires
  // only `name` (internal/api/reservations.go:243-246) and parses an empty
  // worker_ids array without complaint (:266-274), so the reservations TABLE is
  // populated in slice 1 even though no agent runs. Only the create form's
  // WorkerPicker is empty-state - see surfaces.ts.
  const reservationName = `e2e-${runId}-reservation`
  await post(request, token, '/v1/reservations', {
    name: reservationName,
    selector: { pool: `e2e-${runId}` },
  })

  return {
    runId,
    adminEmail,
    jobId: job.id,
    jobName,
    scheduleId: schedule.id,
    scheduleName,
    userEmail,
    inviteEmail,
    enrollmentHostname,
    reservationName,
  }
}

const runDir = join(dirname(fileURLToPath(import.meta.url)), '.run')

export function readSeed(): Seed {
  return JSON.parse(readFileSync(join(runDir, 'seed.json'), 'utf8')) as Seed
}
