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
import { parse as parsePgConnectionString } from 'pg-connection-string'

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
// Postgres itself refuses to drop template1 and the other real template
// databases (postgres itself is protected only because the admin client
// below connects TO it, not because it is a template database), so `relay`
// was the only entry doing any real work, and a deny-list's failure mode is
// "everything not named" - a typo or a copy-pasted DSN naming anything else
// would sail through the old check and get dropped.
//
// EXACT MATCH, not a prefix. An earlier version of this allow-list was
// `/^relay_e2e(_[A-Za-z0-9]+)?$/`, meant to leave room for a per-lane suffix
// on a future parallel run. Measured directly: that pattern accepts
// `relay_e2e_prod` too - the exact typo/copy-paste shape this check exists to
// catch - and dropped and recreated it. Nothing in this repo runs parallel
// e2e lanes today (playwright.config.ts pins `workers: 1`, and there is one
// CI job), so there is no real suffix to allow for yet. If parallel lanes are
// added, widen this to the specific run-id shape they actually use, not to an
// open-ended one.
const ALLOWED_DB_NAME = /^relay_e2e$/
if (!ALLOWED_DB_NAME.test(dbName)) {
  throw new Error(
    `refusing to drop ${JSON.stringify(dbName)}: RELAY_E2E_DATABASE_URL must name a dedicated e2e database matching ${ALLOWED_DB_NAME}`,
  )
}

// LOOPBACK, unless explicitly overridden. A name check alone still lets
// `relay_e2e` on a remote host - `prod-db.internal`, say - through: this drops
// it WITH (FORCE), which terminates whatever live sessions that database has,
// on the strength of nothing but a hostname nobody validated. Gated behind an
// explicit opt-in so remote use, if ever wanted, requires someone to type
// `RELAY_E2E_ALLOW_REMOTE=1` in the invoking shell rather than silently
// working - an env var leaves no trace in a diff the way a code change would,
// so the visibility this buys is at the point of use, not in review.
//
// ONE FLAG RELAXES ALL THREE checks below (database, HTTP and gRPC host) -
// `allowRemote` is read once here and short-circuits every `assertLoopback`
// call in this file.
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
// Resolve the host the SAME WAY `pg` itself will, not via `new URL().hostname`.
// `pg-connection-string` (which `pg` uses internally to parse the connection
// string) honors a `host` query parameter over the URL's own hostname -
// node_modules/pg-connection-string/index.js's `if (!config.host)` guard only
// falls back to the URL hostname when no `host` param is present - and `pg`'s
// ConnectionParameters.host, the value actually passed to dns.lookup() when
// opening the socket, is that resolved value. Checking `target.hostname`
// let a `?host=` query parameter smuggle a non-loopback host past this gate
// while `pg` connected to it anyway. Measured directly:
// `postgres://relay:relay@127.0.0.1:5432/relay_e2e?sslmode=disable&host=prod-db.internal`
// passed a `target.hostname`-based check and then attempted to reach
// `prod-db.internal` - it would have run DROP DATABASE ... WITH (FORCE) there
// had it resolved. `port` and `hostaddr` query params don't have the same
// hazard: node-postgres's ConnectionParameters never reads `hostaddr` at all,
// and an overridden `port` alone can't redirect the connection to a different
// network host.
const resolvedDbHost = parsePgConnectionString(databaseUrl).host
assertLoopback(resolvedDbHost, 'RELAY_E2E_DATABASE_URL host (resolved)')

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
