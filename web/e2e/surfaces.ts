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
  // Optional per-surface setup run BEFORE page.goto, for a surface whose state
  // the seeded REST fixtures cannot produce. A function of Page and Seed for the
  // same collection-vs-execution reason as `path` and `ready`.
  //
  // USE THIS SPARINGLY AND JUSTIFY IT AT THE CALL SITE. fixtures.ts's rule is
  // that fixtures go through the REST API so a surface cannot assert about a
  // state production cannot produce. A `prepare` that fabricates data is a
  // deliberate exception and must name (a) why no REST path can produce the
  // state and (b) where the real wire contract for that state is pinned instead.
  prepare?: (page: Page, seed: Seed) => Promise<void>
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

// injectScheduleFailure rewrites the REAL GET /v1/scheduled-jobs response so ONE
// real seeded row carries a last_error, letting the schedules list be measured
// with the FAILING chip present.
//
// WHY IT IS NOT SEEDED THROUGH THE REST API like every other fixture in this
// harness: it cannot be. handleCreateScheduledJob and handlePatchScheduledJob
// both run jobspec.Validate BEFORE storing, so a spec that fails validation
// cannot be written through either. last_error is produced only by schedrunner,
// from a row an earlier release stored under a rule a later release tightened.
// The alternatives are a direct-SQL seed (which needs the pg driver in a .ts
// file, and web/tsconfig.json type-checks e2e/ under strict while pg ships no
// types - ensure-db.mjs is a .mjs file for exactly that reason) or racing the
// 10-second ticker after planting an invalid spec by SQL. Both cost more than
// the property is worth HERE.
//
// WHAT THIS SURFACE IS FOR IS LAYOUT, and the interception leaves layout
// entirely real: a real request to a real server, a real response envelope,
// every other field real, the real router, the real query client, the real
// production CSS bundle. One field's value is fabricated.
//
// WHERE THE REAL CONTRACT IS PINNED INSTEAD, both in CI:
//   - internal/api/scheduled_jobs_response_test.go (untagged, go-ci `test` job):
//     the field names, absent-not-zero, and the row/response arity.
//   - internal/cli/schedules_failure_integration_test.go (go-ci
//     `cli-integration` job): last_error planted in a real database and read
//     back through a real internal/api server over HTTP.
//
// DO NOT "FIX" THIS INTO A REAL-DATA TEST. There is no REST path that can
// produce the state, so the real-data version of this surface does not exist to
// be written.
//
// THE ROUTE STAYS INSTALLED FOR THE PAGE'S LIFETIME, which matters: useSchedules
// sets a refetchInterval, so the list re-fetches every few seconds and an
// interception that fired once would let the chip vanish mid-measurement.
async function injectScheduleFailure(page: Page, scheduleName: string): Promise<void> {
  await page.route(/\/v1\/scheduled-jobs\?/, async (route) => {
    const response = await route.fetch()
    const body = (await response.json()) as { items?: Array<Record<string, unknown>> }
    let hits = 0
    for (const item of body.items ?? []) {
      if (item.name === scheduleName) {
        item.last_error = 'task nightly: retries must be between 0 and 10'
        item.last_error_at = new Date().toISOString()
        hits++
      }
    }
    // FULFILL FIRST, THROW SECOND, and the order is the diagnostic. A silent
    // zero-hit interception would make this surface measure the HEALTHY table
    // while wearing the failing one's name - the empty-table misdiagnosis this
    // whole file exists to avoid, one level up. `ready` gates on the chip, so
    // it catches that on its own; this throw only adds a message naming the
    // cause. Throwing BEFORE the fulfill would leave the request hanging and
    // the page stuck in its loading state, so `ready` would then fail for a
    // second, unrelated-looking reason and hide the first.
    await route.fulfill({ response, json: body })
    if (hits !== 1) {
      throw new Error(
        `injectScheduleFailure matched ${hits} rows named ${JSON.stringify(scheduleName)}, want exactly 1`,
      )
    }
  })
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
      // THE SAME PATH as `jobs` above, in the other view. `prepare` sets the same
      // preference key the shipped view switch writes, so no state is fabricated
      // that production cannot produce; addInitScript is required because the SPA
      // reads the key during its first render, before any test code could run.
      //
      // WHAT THIS SURFACE ESTABLISHES: five lanes do not widen the document,
      // <header> or <main>. WHAT IT CANNOT: whether the lanes are readable, or how
      // much of the row sits clipped behind its own scroller - a
      // scrollWidth <= clientWidth gate cannot tell "fits" from "clipped", and this
      // view is deliberately a scroller (see README, and the same limit spelled out
      // on schedules-failing). The screenshots are the artifact for that.
      name: 'jobs-lanes',
      path: () => '/jobs',
      population: 'populated',
      prepare: async (p) => {
        await p.addInitScript(() => window.localStorage.setItem('relay.jobs.view', 'lanes'))
      },
      ready: async (p, seed) => {
        // Scoped to the Queued lane, not the bare link: a seeded job never leaves
        // `pending`, so a pass here means the populated lane really rendered,
        // rather than an empty lanes view being measured under a populated name.
        //
        // Case-insensitive name: the lane heading is uppercased by CSS, and
        // Chromium reflects text-transform in the accessible name.
        const lane = p.getByRole('region', { name: /^queued$/i })
        await expect(lane.getByRole('link', { name: seed.jobName })).toBeVisible()
      },
    },
    {
      // THE SAME PATH as `jobs` above, in the third view. `prepare` sets the same
      // preference key the shipped view switch writes, so no state is fabricated
      // that production cannot produce; addInitScript is required because the SPA
      // reads the key during its first render, before any test code could run.
      //
      // WHAT THIS SURFACE ESTABLISHES: the timeline does not widen the document,
      // <header> or <main> at three widths. WHAT IT CANNOT: whether a bar is
      // legible, or whether the name column has truncated a job name to nothing.
      // This view is not a horizontal scroller, so the gate's usual blind spot
      // does not apply the way it does to jobs-lanes - but the bar track has
      // hidden overflow, which is a clip of the same kind one level down. The
      // screenshots are the artifact and someone has to open them.
      name: 'jobs-timeline',
      path: () => '/jobs',
      population: 'populated',
      prepare: async (p) => {
        await p.addInitScript(() => window.localStorage.setItem('relay.jobs.view', 'timeline'))
      },
      ready: async (p, seed) => {
        // Scoped to the timeline region, not a bare link: a run where the seeded
        // job is not drawn must fail loudly rather than measure an empty timeline
        // under a populated name.
        //
        // The seeded job is created at seed time and never leaves `pending`, so
        // it falls inside the default 24-hour window and draws as the instant
        // marker.
        //
        // TIMEOUT WIDER THAN THE DEFAULT, ON PURPOSE, KEPT ACROSS THE LIVENESS
        // FIX. useJobTimeline now computes its anchor fresh at each fetch's
        // start (windowBounds(w, Date.now())) rather than from a ticking
        // render, but that anchor is still quantized to ANCHOR_STEP_MS (15s),
        // so a job created within that same window as this test's first fetch
        // can still fall just past the quantized `until` on that first fetch.
        // The refresh that would then pick it up is bounded at one
        // ANCHOR_STEP_MS interval plus however long that refresh's walk takes -
        // this is the same number useJobTimeline.ts documents as the view's
        // whole staleness budget. Re-measured under this fix round (72/72,
        // this surface resolving in under half a second at all three widths)
        // and left the timeout as-is: that run not hitting the race does not
        // retire it, since the underlying quantization this comment describes
        // is unchanged. Reproducible for a real user too - a job submitted
        // seconds ago is documented as "not yet in the window" - so the fix
        // here is patience, not a shorter anchor.
        const region = p.getByRole('region', { name: 'Jobs timeline' })
        await expect(region.getByRole('link', { name: seed.jobName })).toBeVisible({ timeout: 20_000 })
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

    // EMPTY-STATE ONLY: no agent runs, so no worker row exists. Gated on the
    // page's actual empty-state copy (WorkersPage.tsx: "No workers enrolled
    // yet."), not on the <h1> alone - the h1 renders during the pre-fetch
    // skeleton too (measured: it resolved in 35ms, before GET /v1/workers had
    // even returned), so an h1-only gate is an any-state pass and certifies
    // nothing about the empty state it claims to cover.
    {
      name: 'workers',
      path: () => '/workers',
      population: 'empty',
      ready: async (p) => {
        await expect(p.getByText('No workers enrolled yet.')).toBeVisible()
      },
    },

    {
      name: 'schedules',
      path: () => '/schedules',
      population: 'populated',
      // COVERAGE LIMIT, DECLARED. The seeded schedule has never fired, so its
      // last_job_id is absent and the LAST JOB cell renders the hyphen at every
      // width. The POPULATED cell - link, dot and status word, the widest thing
      // that column can hold - is covered by NO browser test, and cannot be in
      // slice 1: producing it needs a scheduler fire, which needs an agent, the
      // same blocker this file already records for /workers. Do not read a pass
      // here as a populated-cell pass.
      ready: async (p, seed) => {
        // exact:true, unlike jobs' equivalent locator above: SchedulesTable.tsx
        // ALSO renders an `aria-label="Edit ${name}"` link per row
        // (SchedulesTable.tsx:104), which JobsTable does not, so the
        // substring-matching default resolves two elements here. Measured, not
        // assumed - the ambiguous form threw a strict-mode violation.
        await expect(p.getByRole('link', { name: seed.scheduleName, exact: true })).toBeVisible()
        // THE STRIP HAS TWO POPULATIONS AND THE WIDTH DIFFERS BETWEEN THEM. Four
        // hyphens until the stats response lands, then four numbers - so a
        // measurement taken on the link alone can be taken against the placeholder
        // strip and report a width the shipped page never has. A digit is the
        // discriminator and works for a zero count too.
        await expect(p.getByTestId('schedules-stat-enabled')).toHaveText(/\d/)
      },
    },
    {
      // THE SAME PATH as `schedules` above, deliberately. The question is what
      // the FAILING chip does to a nine-column grid at a 1080px floor, 620px of
      // fixed track before any fr gets a pixel. The healthy surface above is the
      // CONTROL: if both overflow, the chip is not the cause.
      //
      // WHAT THIS SURFACE CAN AND CANNOT ESTABLISH. Widening SchedulesTable's own
      // MIN_W to 2400px changes NOTHING here. That is not a hole in this
      // surface, it is the documented limit in e2e/README.md - a
      // scrollWidth <= clientWidth gate cannot tell "fits" from "clipped behind
      // a scroller", and Table wraps the whole role="table" subtree in an
      // overflow-x-auto div, so anything that widens the GRID scrolls inside
      // that wrapper instead of widening the document. Do not re-attempt that
      // mutation expecting a red; it cannot go red by construction.
      //
      // What DID go red, and what each one buys:
      //   - a 2400px min-width on the GlassPanel OUTSIDE the scroll wrapper
      //     fails `schedules` AND `schedules-failing` at all three widths with
      //     "document overflows". The gate really does measure this page.
      //   - renaming the chip's text fails ONLY `schedules-failing`, all three
      //     widths, while `schedules` stays green. This surface's gate is
      //     chip-specific and the attribution is clean.
      //   - making injectScheduleFailure match nothing fails only this surface,
      //     and its named error leads the report.
      // So this surface pins that the chip RENDERS in a real browser against a
      // populated table, and that it does not push the page past the document
      // edge. It cannot pin what the chip does INSIDE the table's scroller;
      // nothing in this harness can, until that gap's own backlog item lands.
      name: 'schedules-failing',
      path: () => '/schedules',
      population: 'populated',
      prepare: (p, seed) => injectScheduleFailure(p, seed.scheduleName),
      ready: async (p, seed) => {
        // GATED ON THE CHIP, not merely on the row. A ready() that waited only
        // for the schedule link would pass on a table with no chip in it at all,
        // which is a measurement of the HEALTHY state wearing this surface's
        // name - the "measure the populated state" lesson applied to the
        // specific population this surface claims.
        //
        // exact:true on the link for the same reason the surface above uses it:
        // SchedulesTable also renders an Edit link per row whose accessible name
        // contains the schedule name, so the substring-matching default resolves
        // two elements here.
        const row = p
          .getByRole('row')
          .filter({ has: p.getByRole('link', { name: seed.scheduleName, exact: true }) })
        await expect(row.getByText('FAILING')).toBeVisible()
        // Same reason as the sibling surface above: the strip is measured
        // populated, not in its placeholder state.
        await expect(p.getByTestId('schedules-stat-enabled')).toHaveText(/\d/)
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
        // NOT aria-current="page": that is set by the router on mount, before
        // any of the tab's four queries (GET /v1/jobs/stats, GET
        // /v1/workers/stats, GET /v1/health and GET /v1/config) have
        // returned - measured, it resolved in 53ms under a 2500ms delay on
        // those four endpoints (the eight other populated surfaces took
        // ~2900ms). It certifies navigation, not readiness.
        //
        // SCOPE MATTERS HERE. Under a delay or a 500 scoped to just this
        // tab's own four endpoints, `aria-current="page"` resolves in 53ms /
        // still resolves at all - it says nothing about this page's data.
        // Under a BLANKET `**/v1/**` interception instead, the old
        // `aria-current` gate never resolves either, because
        // `/v1/users/me` (used by every surface's app shell, not just this
        // tab) fails too and the app never renders past its own loading
        // state - so a blanket forced-500 run cannot distinguish this gate
        // from a working one. Measured: blanket 2500ms delay, this gate
        // 5475ms vs `aria-current` 2902ms; tab-scoped 2500ms delay, this gate
        // 2916ms vs `aria-current` 55ms; tab-scoped forced 500, this gate
        // throws at 10011ms (the `expect` timeout) vs `aria-current`
        // resolving in 47ms regardless. Gated instead on the Access panel's
        // Chip (ServerTab.tsx:104-106), which renders only once config.data
        // has actually landed - ErrorStrip renders in its place on a
        // failure, so this locator does not appear at all under a tab-scoped
        // forced 500. Scoped to the "Self-registration" row specifically:
        // the fleet stats grid on this same page has its own StatCell
        // labelled DISABLED (ServerTab.tsx:44), and an unscoped text match
        // resolves to both - measured directly, a strict-mode violation.
        const row = p.getByText('Self-registration').locator('xpath=..')
        await expect(row.getByText(/^(ENABLED|DISABLED)$/)).toBeVisible()
      },
    },
    {
      name: 'profile',
      path: () => '/profile/identity',
      population: 'populated',
      ready: async (p, seed) => {
        // NOT `main h1`: that locator matches the <h1> on every one of these
        // surfaces (13 of them share it), so it would pass equally on a
        // page that redirected somewhere else with an <h1> of its own - it
        // is not specific to this page. This is a SPECIFICITY fix, not a
        // readiness one: with GET /v1/users/me forced to 500, both `main h1`
        // and this gate fail, because AuthProvider redirects to /auth when
        // it cannot load the signed-in user, and neither locator exists on
        // that page - measured directly. Gated instead on the meta strip's
        // own email testid (ProfilePage.tsx:79), whose text is the
        // AuthProvider user's real email and must equal the seeded admin's -
        // a locator that can only pass once this specific page has rendered
        // ITS OWN data, not just any page's.
        await expect(p.getByTestId('meta-email')).toHaveText(seed.adminEmail)
      },
    },
  ]
}
