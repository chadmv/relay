// Provisions the dedicated e2e database and writes e2e/.run/env.json, the single
// source of truth for this run's DSN, addresses, admin credentials and run id.
// playwright.config.ts and e2e/global.setup.ts both READ that file rather than
// re-deriving defaults, so there is no second literal that can drift.
//
// WHY THIS RUNS OUTSIDE PLAYWRIGHT. Playwright starts `webServer` as a plugin,
// and relay-server runs migrations and log.Fatalf's on failure at
// cmd/relay-server/main.go:51 - long BEFORE srv.ListenAndServe() at :290. If the
// database does not exist when webServer launches, the process dies, /v1/health
// never listens, and the run fails as a 120s timeout with no diagnostic. Creating
// the database from a globalSetup or a setup project is therefore always too
// late, whatever the plugin ordering happens to be. The test:e2e npm script runs
// this first instead.
//
// WHY .mjs AND NOT .ts. It must run with no transform step, before Playwright's
// TypeScript loader exists. Keeping it JS also means `pg` needs no @types.
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import pg from 'pg'

const here = dirname(fileURLToPath(import.meta.url))
const runDir = join(here, '.run')

// Windows note: 127.0.0.1, never `localhost` - localhost can resolve to ::1
// first and the published Docker port may not answer there. Same string in CI,
// where `services: postgres:16` publishes the identical user/password/port.
const DEFAULT_URL = 'postgres://relay:relay@127.0.0.1:5432/relay_e2e?sslmode=disable'
const databaseUrl = process.env.RELAY_E2E_DATABASE_URL ?? DEFAULT_URL

const target = new URL(databaseUrl)
const dbName = decodeURIComponent(target.pathname.replace(/^\//, ''))

// A DEDICATED database is non-negotiable, and this is the guard rather than a
// note in a doc. bootstrapAdmin returns early when ANY admin already exists
// (cmd/relay-server/bootstrap.go:20-23), so RELAY_BOOTSTRAP_PASSWORD is never
// consumed against a developer's `relay` database and every login in the suite
// returns 401 with no diagnostic anywhere. The identifier check is also what
// makes the quoted DROP below safe.
if (!/^[A-Za-z0-9_]+$/.test(dbName)) {
  throw new Error(`RELAY_E2E_DATABASE_URL must name a simple database identifier, got ${JSON.stringify(dbName)}`)
}
if (dbName === 'relay' || dbName === 'postgres' || dbName === 'template1') {
  throw new Error(`refusing to drop ${dbName}: RELAY_E2E_DATABASE_URL must name a dedicated e2e database`)
}

const adminUrl = new URL(databaseUrl)
adminUrl.pathname = '/postgres'

const client = new pg.Client({ connectionString: adminUrl.toString() })
await client.connect()
try {
  // Drop at the START, not at the end: a run that crashed leaves state behind,
  // and a clean slate at the start is the only thing that guarantees
  // AdminExists is false so the bootstrap path is genuinely exercised. It also
  // leaves the database around afterwards for post-mortem inspection.
  // WITH (FORCE) terminates leftover connections (PG13+; both environments run
  // postgres:16).
  await client.query(`DROP DATABASE IF EXISTS "${dbName}" WITH (FORCE)`)
  await client.query(`CREATE DATABASE "${dbName}"`)
} finally {
  await client.end()
}

const env = {
  databaseUrl,
  // Loopback, and non-default ports. `make test-e2e` must not collide with a
  // developer's scripts/dev.ps1 stack on :8080/:9090.
  httpAddr: process.env.RELAY_E2E_HTTP_ADDR ?? '127.0.0.1:8091',
  grpcAddr: process.env.RELAY_E2E_GRPC_ADDR ?? '127.0.0.1:9091',
  adminEmail: 'e2e-admin@relay.test',
  adminPassword: 'e2e-password-not-a-secret',
  runId: Date.now().toString(36),
}
mkdirSync(runDir, { recursive: true })
writeFileSync(join(runDir, 'env.json'), JSON.stringify(env, null, 2))
console.log(`[e2e] recreated database ${dbName}; run id ${env.runId}`)
