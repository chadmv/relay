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

// ALLOW-LIST, not a deny-list. `relay` and `postgres`/`template1` were the
// previous three-entry deny-list; the server already refuses to bootstrap
// against a database with an existing admin (relay -> 401, no diagnostic) and
// Postgres itself refuses to drop the template databases, so `relay` was the
// only entry doing any work, and a deny-list's failure mode is "everything not
// named" - `relay_e2e_prod` or any other name a typo or a copy-pasted DSN might
// carry would sail through the old check and get dropped. This regex subsumes
// the old "simple identifier" shape check too - one check, not two.
const ALLOWED_DB_NAME = /^relay_e2e(_[A-Za-z0-9]+)?$/
if (!ALLOWED_DB_NAME.test(dbName)) {
  throw new Error(
    `refusing to drop ${JSON.stringify(dbName)}: RELAY_E2E_DATABASE_URL must name a dedicated e2e database matching ${ALLOWED_DB_NAME}`,
  )
}

// LOOPBACK, unless explicitly overridden. A name check alone still lets
// `relay_e2e` on a remote host - `prod-db.internal`, say - through: this drops
// it WITH (FORCE), which terminates whatever live sessions that database has,
// on the strength of nothing but a hostname nobody validated. Gated behind an
// explicit opt-in so remote use, if ever wanted, shows up in a diff rather than
// silently working.
const allowRemote = process.env.RELAY_E2E_ALLOW_REMOTE === '1'
const LOOPBACK_HOSTS = new Set(['127.0.0.1', 'localhost', '::1', '[::1]'])
function assertLoopback(hostname, label) {
  if (allowRemote) return
  if (!LOOPBACK_HOSTS.has(hostname)) {
    throw new Error(
      `${label} must be loopback (127.0.0.1, localhost or ::1), got ${JSON.stringify(hostname)}. Set RELAY_E2E_ALLOW_REMOTE=1 to override.`,
    )
  }
}
assertLoopback(target.hostname, 'RELAY_E2E_DATABASE_URL host')

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

// host:port -> host, stripping IPv6 brackets if present ("[::1]:8091" -> "[::1]").
function hostOf(addr) {
  const i = addr.lastIndexOf(':')
  return i === -1 ? addr : addr.slice(0, i)
}

const httpAddr = process.env.RELAY_E2E_HTTP_ADDR ?? '127.0.0.1:8091'
const grpcAddr = process.env.RELAY_E2E_GRPC_ADDR ?? '127.0.0.1:9091'
// Same loopback requirement as the database above, and for the same reason:
// the admin credentials this run boots the server with are a committed,
// publicly-known password (env.adminPassword below), and that is only safe
// BECAUSE the listener is loopback-only for the run's duration. An unvalidated
// RELAY_E2E_HTTP_ADDR=0.0.0.0:8091 would publish an admin API onto the LAN
// with that password, turning a documented convention into an enforced one.
assertLoopback(hostOf(httpAddr), 'RELAY_E2E_HTTP_ADDR host')
assertLoopback(hostOf(grpcAddr), 'RELAY_E2E_GRPC_ADDR host')

const env = {
  databaseUrl,
  // Loopback, and non-default ports. `make test-e2e` must not collide with a
  // developer's scripts/dev.ps1 stack on :8080/:9090.
  httpAddr,
  grpcAddr,
  adminEmail: 'e2e-admin@relay.test',
  adminPassword: 'e2e-password-not-a-secret',
  runId: Date.now().toString(36),
}
mkdirSync(runDir, { recursive: true })
writeFileSync(join(runDir, 'env.json'), JSON.stringify(env, null, 2))
console.log(`[e2e] recreated database ${dbName}; run id ${env.runId}`)
