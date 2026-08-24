import { defineConfig, devices } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const runDir = join(here, 'e2e', '.run')

interface RunEnv {
  databaseUrl: string
  httpAddr: string
  grpcAddr: string
  adminEmail: string
  adminPassword: string
  runId: string
}

// Written by e2e/ensure-db.mjs, which `npm run test:e2e` runs first. Reading it
// (rather than re-deriving a default) means the DSN the server is handed is
// byte-identical to the database that was just created - there is no second
// default literal to keep in sync.
let runEnv: RunEnv
try {
  runEnv = JSON.parse(readFileSync(join(runDir, 'env.json'), 'utf8')) as RunEnv
} catch {
  throw new Error(
    'e2e/.run/env.json is missing. Run `npm run test:e2e` (which runs e2e/ensure-db.mjs first), not `npx playwright test` directly.',
  )
}

const baseURL = `http://${runEnv.httpAddr}`

// `go build -o` writes exactly the name it is given. The Makefile's
// RELAY_SERVER_BIN picks the .exe suffix on Windows and this must agree with it.
const serverBin = process.platform === 'win32' ? '..\\bin\\relay-server.exe' : '../bin/relay-server'

export default defineConfig({
  testDir: './e2e',

  // SERIALIZED, and not a performance default for someone to tune away later.
  // Three independent reasons, any one of which is sufficient:
  //   1. One relay-server over one Postgres is a shared mutable store with no
  //      per-test namespace.
  //   2. /jobs, /schedules and /admin/* are UNSCOPED global lists, so a parallel
  //      test's fixtures appear in another test's table and any count assertion
  //      or nth-row locator becomes a race.
  //   3. POST /v1/auth/login is rate limited per RemoteAddr only
  //      (internal/api/ratelimit.go:66-72) and every worker is 127.0.0.1.
  // Same reasoning as `-p 1` on `make test-integration`.
  fullyParallel: false,
  workers: 1,

  // Deliberately zero. A retry that passes hides a flake. The escalation for a
  // flaky spec here is to fix it or delete it - never to retry past it and never
  // to quietly set continue-on-error in the workflow.
  retries: 0,

  forbidOnly: !!process.env.CI,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  outputDir: './test-results',
  reporter: process.env.CI
    ? [['github'], ['html', { open: 'never' }]]
    : [['list'], ['html', { open: 'never' }]],

  use: {
    baseURL,
    trace: 'retain-on-failure',
    video: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },

  projects: [
    {
      // A SETUP PROJECT, not globalSetup. It provably runs after webServer is
      // healthy (nothing runs before webServer), and a seeding failure reports as
      // one named red test instead of an opaque runner crash.
      name: 'setup',
      testMatch: /global\.setup\.ts/,
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'chromium',
      dependencies: ['setup'],
      use: { ...devices['Desktop Chrome'], storageState: join(runDir, 'state.json') },
    },
    {
      // Playwright's `webkit` is a BUNDLED WebKit build. It is NOT Safari.
      //
      // It exercises WebKit's focusability semantics - which is precisely why
      // web/src/components/holo/Table.tsx:181-193 carries an explicit
      // tabIndex={0} instead of leaning on Chromium's implicit scroller
      // focusability - and it does NOT exercise Safari's chrome, its extensions
      // or its platform integration. The Known Limitation "Safari has not been
      // opened" is NARROWED by this project, not retired. Do not cite this lane
      // as Safari coverage.
      //
      // Only the @webkit subset runs here: a React SPA's list rendering does not
      // diverge across engines, so running everything twice triples wall clock
      // and flake surface for near-zero marginal information. Pay for the engine
      // where the argument is.
      name: 'webkit',
      grep: /@webkit/,
      dependencies: ['setup'],
      use: { ...devices['Desktop Safari'], storageState: join(runDir, 'state.json') },
    },
  ],

  webServer: {
    command: serverBin,
    url: `${baseURL}/v1/health`,
    // GET /v1/health is public (internal/api/server.go:96, health.go:5-7) and it
    // is a strictly-after-migrations signal: main.go runs store.Migrate at :51
    // and only calls ListenAndServe at :290, so the port does not answer at all
    // until the schema is complete. There is no half-ready window to poll into.

    // NEVER reuse. e2e/ensure-db.mjs has just dropped and recreated the database,
    // so an already-running server on this port holds a pool against a database
    // that no longer exists - and if that listener is a DEVELOPER's stack, the
    // suite runs green against dev data with a dev admin, which is wrong and very
    // hard to see. Restarting costs ~1s: the binary is prebuilt and migrations
    // run on an empty database.
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: 'pipe',
    stderr: 'pipe',
    env: {
      RELAY_DATABASE_URL: runEnv.databaseUrl,
      RELAY_HTTP_ADDR: runEnv.httpAddr,
      RELAY_GRPC_ADDR: runEnv.grpcAddr,
      RELAY_BOOTSTRAP_ADMIN: runEnv.adminEmail,
      RELAY_BOOTSTRAP_PASSWORD: runEnv.adminPassword,
      // MEASURED: this suite performs exactly THREE logins per run (the setup
      // project, and auth.spec.ts's login and logout tests, both of which sign in
      // through the UI). Three is under the 10:1m default, so the limiter does not
      // bite at HEAD. This raise is insurance against the fourth: a 429 presents
      // as a mysterious failure rather than as a limit.
      // STATED COST: the harness therefore does not exercise the rate limiter at
      // all. That is deliberate - it has its own Go tests and is not a
      // browser-observable behaviour. Browser coverage of the 429 path would need
      // a second server project with the default limits, which the non-default
      // ports above keep cheap.
      RELAY_LOGIN_RATE_LIMIT: '1000:1m',
      // Left UNSET on purpose: RELAY_ALLOW_AUTO_ENROLL and
      // RELAY_ALLOW_SELF_REGISTER. The test server runs the DEFAULT (safer)
      // posture, which is the one production runs. No agent connects in slice 1.
    },
  },
})
