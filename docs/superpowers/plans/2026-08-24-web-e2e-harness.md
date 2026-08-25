# Web E2E Browser Harness (slice 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a Playwright browser harness under `web/e2e/` that drives the production-embedded SPA against a real `relay-server` on a dedicated `relay_e2e` database, ship three specs that assert things `npm test` structurally cannot (real key events, real layout widths, real navigation), and put the entire web gate - `tsc -b`, `npm test`, `npm run build`, Playwright - into CI for the first time.

**Architecture:** `make web-build` produces `web/dist`; `go build ./cmd/relay-server` embeds it via `//go:embed all:dist`; Playwright's `webServer` starts that binary on `127.0.0.1:8091` against a database that a pre-step dropped and recreated; a Playwright *setup project* logs in as the bootstrap admin, seeds fixtures over the REST API, and writes a `storageState` file the other two projects inherit. Chromium runs every spec; WebKit runs the `@webkit`-tagged keyboard subset.

**Tech Stack:** `@playwright/test`, `pg` (database provisioning only), `@types/node`, GitHub Actions `services: postgres:16`, the existing Vite/Vitest/TypeScript project in `web/`.

**Spec:** `docs/superpowers/specs/2026-08-24-web-e2e-harness.md`
**Item:** `docs/backlog/idea-2026-06-03-web-e2e-harness.md` (this slice is 1 of 2; do **not** close the item)

---

## Slice independence declaration

**This slice is a single lane. There is no frontend/backend split and no Phase 3 parallelism.**

- **Zero backend work.** No `.go` file is created or modified. No `.sql` file is touched, so no `make generate` step exists anywhere in this plan.
- **Zero application-source work.** No file under `web/src/**` is created or modified. Everything is new files under `web/e2e/`, four config edits, one Makefile target, and one workflow file.
- Owner: **`relay-frontend-engineer`**, start to finish. Dispatch one agent, not two.
- Tasks 1-13 are strictly sequential. The slice has a real bootstrapping chain (deps -> collection -> types -> database -> server -> session -> specs -> wiring -> CI) and no task in it is independent of its predecessor.

---

## Verification against HEAD

Every claim the spec makes that this plan depends on was re-read at HEAD before planning. The spec is an input, not a fact.

### Confirmed

| # | Claim | Evidence at HEAD |
|---|---|---|
| V1 | Vitest would collect `web/e2e/**/*.spec.ts`. | `web/vite.config.ts:13-18` sets `test.environment/globals/setupFiles/css` and **no** `include` or `exclude`. Vitest 2's default `include` is `['**/*.{test,spec}.?(c|m)[jt]s?(x)']` and its default `exclude` covers `node_modules`, `dist`, `cypress` and `*.config.*` - **never `e2e/`**. Vitest's root is the config dir (`web/`), so `web/e2e/auth.spec.ts` is inside it. CONFIRMED. |
| V2 | `tsc -b` leaves `web/e2e/` untype-checked. | `web/tsconfig.json:19` is `"include": ["src"]`. `web/tsconfig.node.json:14` is `"include": ["vite.config.ts"]`. Nothing else is compiled - `playwright.config.ts` at `web/` would also be invisible. CONFIRMED, and **broader than the spec states**: the config file is uncovered too. |
| V3 | Login is rate-limited 10/min on `RemoteAddr` only. | `cmd/relay-server/main.go:164` (`RELAY_LOGIN_RATE_LIMIT` default `"10:1m"`), `internal/api/ratelimit.go:66-72` (`clientIP` reads `r.RemoteAddr`, never `X-Forwarded-For`). CONFIRMED. |
| V4 | `GET /v1/health` is public and returns `{"status":"ok"}`. | `internal/api/server.go:96`, `internal/api/health.go:5-7`. It is registered on the mux with no `auth(...)` wrapper. CONFIRMED. |
| V5 | Bootstrap is a no-op when any admin exists, so a reused database yields a silent 401. | `cmd/relay-server/bootstrap.go:16-23` returns before it ever looks at the email. CONFIRMED - this is why Task 4 refuses to run against a database named `relay`. |
| V6 | There is no web CI. | `.github/workflows/` contains exactly `go-ci.yml`, `python.yml`, `release.yml`. `go-ci.yml:26-34` runs `make vet-integration` and `go test -race ./...` and nothing else. CONFIRMED. |
| V7 | `web/dist/index.html` is one tracked 7-line placeholder; everything else under `web/dist/` is ignored. | `.gitignore:6-8` (`web/dist/` + `!web/dist/index.html`); `web/dist/index.html` is 7 lines reading "The Relay web UI has not been built." CONFIRMED. |
| V8 | The SPA stores its bearer token at `localStorage['relay.token']`, and seeding it is sufficient to be logged in. | `web/src/lib/token.ts:1-13`; `web/src/auth/AuthProvider.tsx:124-139` hydrates from `/users/me` on mount when `getToken()` is non-null. CONFIRMED - `storageState` alone authenticates a page. |
| V9 | The SPA is same-origin with the API. | `web/src/lib/api.ts:73,155,159` fetch relative `/v1${path}`. `internal/api/server.go:204-206` mounts `StaticHandler` at `/`. CONFIRMED. |
| V10 | `make web-build` is `cd web && npm run build` = `tsc -b && vite build`. | `Makefile:8-9`, `web/package.json:8`. CONFIRMED. |
| V11 | Every stable selector the three specs need already exists. | Login: `<label htmlFor>` via `web/src/components/Field.tsx:30-35`, ids `email`/`password` (`LoginScreen.tsx:40-57`). Landing: `<h1>Jobs</h1>` (`JobsPage.tsx:78`). Logout: the `UserMenu` toggle's accessible name is the email (`UserMenu.tsx:174-185`) and the item is `<button>Log out</button>` (`:229-241`). Scroll wrapper: `role="group"` + `aria-label={`${label}, scrolls horizontally`}` (`components/holo/Table.tsx:190`), with `label="Agent enrollments"` (`EnrollmentsTable.tsx:41`) and `label="Invites"` (`InvitesTable.tsx:51`). Row links by name: `JobsTable.tsx:49`, `SchedulesTable.tsx:59-64`. CONFIRMED - **zero application source changes are achievable.** |

### Refuted

| # | The claim | What HEAD actually shows, and what this plan does |
|---|---|---|
| **R-A** | Spec section 4: "`web/e2e/global-setup.ts`: create `relay_e2e`, start-and-wait handled by `webServer`". | **Impossible as written.** `webServer` is a Playwright *plugin* and plugin setup runs before `globalSetup`; the harness must not bet on that ordering either way. Worse, `relay-server` calls `store.Migrate` at `cmd/relay-server/main.go:51` and `log.Fatalf`s on failure - **before** `srv.ListenAndServe()` at `:290-295`. So if the database does not exist when `webServer` launches, the process dies, `/v1/health` never listens, and Playwright fails with a 120s timeout and no useful message. **Fix:** database creation moves out of Playwright entirely into `web/e2e/ensure-db.mjs`, run by the `test:e2e` npm script *before* `playwright test`. Seeding moves from `globalSetup` to a **setup project** (`dependencies: ['setup']`), which provably runs after `webServer` is healthy and reports as a named test instead of an opaque runner crash. |
| **R-B** | Spec D8: `reuseExistingServer: !process.env.CI`. | **Unsafe once the database is recreated per run.** Task 4 drops and recreates `relay_e2e`; a reused server is holding a pool against a database that no longer exists. And if the reused listener is a *developer's* stack, the suite runs green against dev data with a dev admin - exactly the failure D9's port choice exists to prevent, only reachable through a different door. **Fix:** `reuseExistingServer: false`, unconditionally. Cost is ~1s: the binary is prebuilt and migrations run on an empty database. |
| **R-C** | Spec D10: "`/admin/reservations` (whose `WorkerPicker` needs workers) [is] covered in [its] empty state only". | **Over-stated.** `handleCreateReservation` requires only `name` (`internal/api/reservations.go:243-246`) and parses an empty `worker_ids` array without complaint (`:266-274`), so a selector-only reservation is a valid 201 (`:299`). The reservations **table** is populated in slice 1. Only the create form's `WorkerPicker` is empty-state. This plan seeds a reservation. |
| **R-D** | Spec D7: adding `"e2e"` to `tsconfig.json`'s `include` is "the one-line form". | **Three changes minimum, not one.** `web/tsconfig.json:17` pins `"types": ["vitest/globals", "@testing-library/jest-dom"]`, which *disables* automatic inclusion of every other `@types` package. `@types/node` is not a devDependency (`web/package.json:21-35`). The harness needs `process.env`, `node:fs`, `node:path`, `node:url`. So the minimum is: install `@types/node`, add `"node"` to `types`, and extend `include` with `"e2e"` **and `"playwright.config.ts"`** (V2 - the config file is uncovered too). Task 3 measures whether widening `types` breaks `src` and carries a fully specified fallback. |
| **R-E** | Spec D12's enumerated file list. | Incomplete. It omits `web/.gitignore` (Playwright writes `test-results/`, `playwright-report/`, and this plan's `e2e/.run/`, none of which are ignored today - `web/.gitignore` has 7 lines and covers none of them) and omits the `pg`/`@types/node` devDependencies R-A and R-D require. |
| **R-F** | Spec D2's premise that the login rate limit is a live constraint on the harness. | Measured, not assumed: this suite performs **exactly three** logins per run (setup project, `auth.spec.ts`'s login test, `auth.spec.ts`'s logout test - the last two are real UI logins). Three is under the `10:1m` default, so the limit **does not bite at HEAD**. The spec's `RELAY_LOGIN_RATE_LIMIT=1000:1m` is kept as insurance against the fourth, but the config comment must say it is insurance and must state the cost (the harness never exercises the limiter). |

### Two hazards nobody's list had

- **`test.exclude` REPLACES the vitest defaults, it does not extend them.** Writing `exclude: ['e2e/**']` deletes `**/node_modules/**` from the exclude set, and vitest then walks `web/node_modules` collecting every `*.spec.js` it finds there. The correct form spreads `configDefaults.exclude` first. Acceptance criterion 7 ("collected file count unchanged from HEAD") is what catches this, which is why Task 2 records the baseline count *before* touching anything.
- **`PublicOnlyRoute` has no test at HEAD.** `rg 'PublicOnlyRoute' web/src --glob '*.test.tsx'` returns nothing; `web/src/app/` has `ProtectedRoute.test.tsx` and `AdminRoute.test.tsx` and no third file. So the post-login landing route at `web/src/app/PublicOnlyRoute.tsx:9` can be changed to any path and the entire 1100-test suite stays green. The 2026-06-03 item's original complaint - "the missing post-login redirect (no app-level navigation test existed)" - **is still literally true**, two and a half months later. `auth.spec.ts` is the first gate that would catch it.
  **2026-08-24, superseded:** the `rg` grep above checks the component's NAME, not its BEHAVIOUR. `web/src/App.test.tsx:19-38` ("a successful login lands the user on the jobs page") already drives the real `<App/>` tree through this exact redirect - see the correction now in `web/e2e/auth.spec.ts:42`. This browser test is still worth keeping (independent confirmation through a real compiled server), just not for the coverage-gap reason originally written here.

---

## File structure

**Created**

| Path | Responsibility |
|---|---|
| `web/e2e/ensure-db.mjs` | Drops and recreates the dedicated database; writes `e2e/.run/env.json`, the single source of truth for the run's DSN, addresses, admin credentials and run id. Runs before Playwright. Plain `.mjs` because it must run with no TypeScript transform. |
| `web/playwright.config.ts` | Projects (`setup`, `chromium`, `webkit`), `webServer`, serialization, reporters, artifacts. Reads `e2e/.run/env.json`. |
| `web/e2e/global.setup.ts` | The `setup` project's only test: log in as the bootstrap admin, call `seedAll`, write `e2e/.run/state.json` (storageState) and `e2e/.run/seed.json`. |
| `web/e2e/fixtures.ts` | Typed REST seeding helpers + the `Seed` type + `readSeed()`. Every resource name carries the run id. |
| `web/e2e/surfaces.ts` | The single enumerated surface list: path, readiness predicate, and a declared `population` flag recording D10's coverage limit per surface. |
| `web/e2e/keyboard.spec.ts` | `@webkit`. Real `Tab` and arrow presses against the two zero-focusable tables' scroll wrappers. |
| `web/e2e/layout.spec.ts` | `documentElement`/`header`/`main` widths at 320px and 375px on every surface, plus a full-page screenshot per surface per width. |
| `web/e2e/auth.spec.ts` | Login lands on `/jobs`; logged-out deep link redirects; logout clears `relay.token`. |
| `web/e2e/README.md` | How to run it on Windows and in CI, and the declared coverage limits. |
| `.github/workflows/web-ci.yml` | The whole web gate, blocking on every PR. |

**Modified**

| Path | Change |
|---|---|
| `web/package.json:6-12,21-35` | Two scripts; three devDependencies. |
| `web/vite.config.ts:13-18` | `test.exclude` = defaults + `e2e/**`. |
| `web/tsconfig.json:17,19` | `types` gains `"node"`; `include` gains `"e2e"` and `"playwright.config.ts"`. |
| `web/.gitignore` | Four ignore lines. |
| `Makefile:1` and a new target | `RELAY_SERVER_BIN` and `test-e2e`. |

**Never touched:** anything under `web/src/`, any `.go` file, any `.sql` file, `web/dist/index.html` (a build dirties it; every task that builds restores it).

---

## Windows-versus-CI divergence

Windows is the dev host. The Makefile is used by CI-equivalent local runs and assumes an sh-like shell (`Makefile:57` is `rm -rf bin/`), so **run `make` from Git Bash on Windows, not from PowerShell or cmd.** PowerShell is fine for the raw `npm`/`go`/`docker` commands.

| Concern | Windows dev host | `ubuntu-latest` CI |
|---|---|---|
| Postgres | The `relay-postgres` container `scripts/dev.ps1:84-119` manages: `postgres:16`, user/password `relay`/`relay`, database `relay`, host port 5432. Start it with `docker start relay-postgres`, or run `scripts/dev.ps1` once to create it. | `services: postgres:16` with the identical user/password/port. |
| Connection string | `postgres://relay:relay@127.0.0.1:5432/relay_e2e?sslmode=disable` | **Identical.** Use `127.0.0.1`, never `localhost` - on Windows `localhost` can resolve to `::1` first and the published Docker port may not answer there. |
| Server binary | `bin/relay-server.exe` | `bin/relay-server` |
| Binary name selection | `RELAY_SERVER_BIN` in the Makefile (`ifeq ($(OS),Windows_NT)`); `process.platform === 'win32'` in `playwright.config.ts`. The two must agree. | same |
| Browser install | `cd web && npx playwright install chromium webkit` (once) | `npx playwright install --with-deps chromium webkit` every run - `--with-deps` installs apt packages that the `~/.cache/ms-playwright` cache does **not** contain, so it runs even on a cache hit. |
| Entry point | `make test-e2e` (Git Bash) | the workflow's ordered steps |
| `web/dist/index.html` | Dirtied by the build; `make test-e2e` restores it on exit, pass or fail. If you ran `npm run test:e2e` directly, run `git checkout -- web/dist/index.html` yourself. | Irrelevant - fresh checkout, nothing is committed. |
| Screenshots | Written to `web/test-results/`; open them locally. | Uploaded by `actions/upload-artifact`. |

---

## Tasks

### Task 1: Dependencies

**Files:**
- Modify: `web/package.json:6-12` (scripts), `web/package.json:21-35` (devDependencies)
- Modify: `web/package-lock.json` (generated)
- Modify: `web/.gitignore`

- [ ] **Step 1: Record the pre-change baseline**

Run from `web/`:

```bash
npm test 2>&1 | tail -20
```

Write down the exact "Test Files N passed (N)" and "Tests M passed (M)" numbers. Acceptance criterion 7 compares against them.

- [ ] **Step 2: Install the three devDependencies**

Run from `web/`:

```bash
npm install --save-dev --save-exact @playwright/test
npm install --save-dev @types/node pg
```

`--save-exact` for Playwright only: the browser binaries `npx playwright install` downloads are keyed to the package version, and a caret range would let CI and a dev host disagree about which WebKit build the `@webkit` project ran on. `@types/node` and `pg` are ordinary caret ranges.

Record the resolved Playwright version - CI's cache key uses it, read out of `node_modules` rather than hardcoded, so the two can never drift.

- [ ] **Step 3: Add the two scripts**

In `web/package.json`, the `scripts` block becomes:

```json
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview",
    "test": "vitest run",
    "test:watch": "vitest",
    "test:e2e": "node e2e/ensure-db.mjs && playwright test",
    "test:e2e:ui": "node e2e/ensure-db.mjs && playwright test --ui"
  },
```

`ensure-db.mjs` runs first, in both scripts, because `relay-server` dies on a missing database before it ever listens (R-A). `&&` works in both `cmd.exe` (npm's default shell on Windows) and `sh`.

- [ ] **Step 4: Ignore the harness's outputs**

`web/.gitignore` becomes:

```
node_modules
dist
!dist/index.html
*.log
vite.config.js
vite.config.d.ts
test-results/
playwright-report/
blob-report/
e2e/.run/
```

(`*.tsbuildinfo` was already there; keep it - the line order above preserves every existing entry.)

- [ ] **Step 5: Verify nothing regressed**

Run from `web/`:

```bash
npm test 2>&1 | tail -20
```

Expected: the identical numbers from Step 1. Installing dependencies must not move the suite.

- [ ] **Step 6: Commit**

```bash
git add web/package.json web/package-lock.json web/.gitignore
git commit -m "chore(web): add playwright, @types/node and pg devDependencies"
```

---

### Task 2: Stop Vitest from collecting the Playwright specs

The trap first, before there is anything to trap. This task's RED is a real spec file being run in jsdom.

**Files:**
- Create: `web/e2e/auth.spec.ts` (skeleton; filled in at Task 8)
- Modify: `web/vite.config.ts:1-19`

- [ ] **Step 1: Write the failing state - a real spec file in `web/e2e/`**

Create `web/e2e/auth.spec.ts`:

```ts
import { expect, test } from '@playwright/test'

test('placeholder - replaced in Task 8', async ({ page }) => {
  await page.goto('/auth')
  await expect(page).toHaveURL(/\/auth$/)
})
```

- [ ] **Step 2: Run the unit suite and watch it break**

Run from `web/`:

```bash
npm test 2>&1 | tail -30
```

Expected: FAIL. Vitest collects `e2e/auth.spec.ts` (its default `include` is `**/*.{test,spec}.?(c|m)[jt]s?(x)` and its default `exclude` never mentions `e2e/` - `web/vite.config.ts:13-18` overrides neither), runs it in jsdom, and errors on the imported Playwright `test`, which has no runner. The collected file count is baseline + 1.

- [ ] **Step 3: Exclude the directory - spreading the defaults, not replacing them**

`web/vite.config.ts` becomes:

```ts
/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import { configDefaults } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/v1': 'http://localhost:8080',
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    css: true,
    // The Playwright specs are *.spec.ts inside vitest's root, so vitest's
    // default include matches them and its default exclude does not - they would
    // be collected and run in jsdom, where `page` does not exist.
    //
    // configDefaults.exclude is SPREAD, never replaced. Setting `exclude` at all
    // overwrites vitest's defaults wholesale, and dropping `**/node_modules/**`
    // makes vitest walk web/node_modules collecting every *.spec.js it finds.
    // The guard on that is acceptance criterion 7: `npm test`'s collected file
    // count must be unchanged from HEAD, which a node_modules walk explodes.
    exclude: [...configDefaults.exclude, 'e2e/**'],
  },
})
```

- [ ] **Step 4: Run the unit suite and watch it pass**

Run from `web/`:

```bash
npm test 2>&1 | tail -20
```

Expected: PASS, with the **exact** file and test counts from Task 1 Step 1. Not "about the same" - identical. A higher number means `configDefaults` was not spread.

- [ ] **Step 5: Commit**

```bash
git add web/vite.config.ts web/e2e/auth.spec.ts
git commit -m "test(web): keep vitest out of the e2e directory"
```

---

### Task 3: Type-check the harness

**Files:**
- Create: `web/e2e/typecheck-canary.ts` (temporary; deleted in Step 5)
- Modify: `web/tsconfig.json:17,19`

- [ ] **Step 1: Write the canary that proves the current blind spot**

Create `web/e2e/typecheck-canary.ts`:

```ts
// Temporary. Proves `tsc -b` reaches web/e2e/. Deleted in this same task.
export const wrong: number = 'this is a string'
```

- [ ] **Step 2: Run the type check and watch it *not* fail*

Run from `web/`:

```bash
npx tsc -b
echo "exit=$?"
```

Expected: `exit=0`, no diagnostics. **That is the defect.** `web/tsconfig.json:19` is `"include": ["src"]` and `web/tsconfig.node.json:14` is `"include": ["vite.config.ts"]`, so neither `web/e2e/` nor `web/playwright.config.ts` is compiled by anything.

- [ ] **Step 3: Widen the project**

In `web/tsconfig.json`, change exactly two keys:

```json
    "types": ["vitest/globals", "@testing-library/jest-dom", "node"]
```

```json
  "include": ["src", "e2e", "playwright.config.ts"],
```

`"node"` is required, not cosmetic: an explicit `types` array disables TypeScript's automatic `@types/*` inclusion, and the harness uses `process.env`, `node:fs`, `node:path` and `node:url`. `playwright.config.ts` is listed by name because `include: ["e2e"]` alone leaves it uncompiled, which V2 found.

- [ ] **Step 4: Run the type check and watch it fail on the canary**

Run from `web/`:

```bash
npx tsc -b
```

Expected: FAIL with `e2e/typecheck-canary.ts(2,14): error TS2322: Type 'string' is not assignable to type 'number'.`

**Then read the rest of the output.** If there are *also* new errors under `src/**` - widening `types` with `"node"` puts `NodeJS.Timeout`, `Buffer` and friends into `src`'s scope - stop and apply Fallback B below instead. (A scan of HEAD says this is unlikely: every timer handle in `web/src` is either untyped or `ReturnType<typeof setTimeout>` - `useTaskLogStream.ts:128,141,142`, `useDebouncedValue.ts:10`, `useNow.ts:11` - and no file uses `process`. Measure anyway.)

<details>
<summary><b>Fallback B - a separate project reference (use only if Step 4 shows new `src/**` errors)</b></summary>

Revert `web/tsconfig.json`'s `types` and `include` to HEAD, and change only its `references`:

```json
  "references": [{ "path": "./tsconfig.node.json" }, { "path": "./tsconfig.e2e.json" }]
```

Create `web/tsconfig.e2e.json`:

```json
{
  "extends": "./tsconfig.json",
  "compilerOptions": {
    "composite": true,
    "noEmit": false,
    "emitDeclarationOnly": true,
    "outDir": "./node_modules/.tmp/e2e-types",
    "types": ["node"]
  },
  "include": ["e2e", "playwright.config.ts"]
}
```

`composite: true` is mandatory for a referenced project. `emitDeclarationOnly` + `outDir` inside `node_modules` is the same idiom `tsconfig.node.json:9,12` already uses, redirected so no `.d.ts` files land beside the specs. `tsc -b` on the default project builds its references first, so acceptance criterion 7 still holds verbatim.
</details>

- [ ] **Step 5: Delete the canary and confirm green**

```bash
rm web/e2e/typecheck-canary.ts
cd web && npx tsc -b && echo "exit=$?"
```

Expected: `exit=0`. The permanent guard is not the canary - it is that `web/e2e/**` and `playwright.config.ts` are now inside the project `npm run build` type-checks, so a type error in any spec breaks the build.

- [ ] **Step 6: Commit**

```bash
git add web/tsconfig.json
git commit -m "build(web): type-check the e2e directory and the playwright config"
```

---

### Task 4: Provision the dedicated database

**Files:**
- Create: `web/e2e/ensure-db.mjs`

- [ ] **Step 1: Prove the failing state**

With the `relay-postgres` container running (`docker start relay-postgres`), run from `web/`:

```bash
node -e "process.exit(0)"   # sanity: node works
node e2e/ensure-db.mjs
```

Expected: FAIL - `Cannot find module '<...>/e2e/ensure-db.mjs'`. Nothing provisions `relay_e2e` today, and `store.Migrate` (`cmd/relay-server/main.go:51`) `log.Fatalf`s against a database that does not exist.

- [ ] **Step 2: Write it**

Create `web/e2e/ensure-db.mjs`:

```js
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
```

- [ ] **Step 3: Run it and watch it pass**

From `web/`:

```bash
node e2e/ensure-db.mjs
cat e2e/.run/env.json
```

Expected: `[e2e] recreated database relay_e2e; run id <base36>` and a JSON file with six keys. Run it twice in a row - the second run must also succeed, which is what proves the `DROP ... WITH (FORCE)` path works.

- [ ] **Step 4: Prove the refusal guard fires**

```bash
RELAY_E2E_DATABASE_URL='postgres://relay:relay@127.0.0.1:5432/relay?sslmode=disable' node e2e/ensure-db.mjs
```

Expected: FAIL with `refusing to drop relay: RELAY_E2E_DATABASE_URL must name a dedicated e2e database`, and the developer's `relay` database untouched (`docker exec relay-postgres psql -U relay -d relay -c '\dt'` still lists the schema).

- [ ] **Step 5: Commit**

```bash
git add web/e2e/ensure-db.mjs
git commit -m "test(web): provision a dedicated relay_e2e database for the browser lane"
```

---

### Task 5: The Playwright config and a server that actually boots

**Files:**
- Create: `web/playwright.config.ts`

- [ ] **Step 1: Run Playwright with no config and watch it fail**

From `web/`:

```bash
npx playwright test
```

Expected: FAIL - no `playwright.config.ts`, so Playwright either finds no tests or errors on a missing config. Either way, nothing is wired.

- [ ] **Step 2: Write the config**

Create `web/playwright.config.ts`:

```ts
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
```

- [ ] **Step 3: Build the server and run the placeholder spec**

From the repo root (Git Bash on Windows):

```bash
make web-build
go build -o bin/relay-server.exe ./cmd/relay-server   # drop .exe on Linux
cd web && npm run test:e2e
```

Expected: the `setup` project fails (`global.setup.ts` does not exist yet, so `testMatch` finds no tests and Playwright errors on the unmet dependency) **but the web server line reports the server started and `/v1/health` answered.** That is what this task proves. If instead you see `Timed out waiting 120000ms from config.webServer`, read the piped stderr - a `migrate:` fatal means `ensure-db.mjs` did not run.

- [ ] **Step 4: Restore the tracked placeholder**

```bash
cd .. && git checkout -- web/dist/index.html && git status --porcelain web/dist
```

Expected: no output. `make web-build` overwrites this one tracked file (`.gitignore:6-8` ignores everything else under `web/dist/`) with an index referencing gitignored hashed assets; committing it would embed an index pointing at assets nobody else has.

- [ ] **Step 5: Commit**

```bash
git add web/playwright.config.ts
git commit -m "test(web): playwright config serving the production-embedded SPA"
```

---

### Task 6: Fixtures and the setup project

**Files:**
- Create: `web/e2e/fixtures.ts`
- Create: `web/e2e/global.setup.ts`

- [ ] **Step 1: Write the failing state - a setup project with no test file**

Already failing from Task 5 Step 3. Confirm the exact message from `web/`:

```bash
npm run test:e2e 2>&1 | head -20
```

Expected: an error naming the `setup` project (no tests matched `/global\.setup\.ts/`).

- [ ] **Step 2: Write the fixtures module**

Create `web/e2e/fixtures.ts`:

```ts
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
```

- [ ] **Step 3: Write the setup project**

Create `web/e2e/global.setup.ts`:

```ts
import { expect, test as setup } from '@playwright/test'
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { seedAll } from './fixtures'

const runDir = join(dirname(fileURLToPath(import.meta.url)), '.run')
const runEnv = JSON.parse(readFileSync(join(runDir, 'env.json'), 'utf8')) as {
  adminEmail: string
  adminPassword: string
  runId: string
}

setup('log in and seed fixtures', async ({ request, baseURL }) => {
  const res = await request.post('/v1/auth/login', {
    data: { email: runEnv.adminEmail, password: runEnv.adminPassword },
  })
  // A 401 here almost always means the database was NOT freshly created:
  // bootstrapAdmin returns before it looks at the email when ANY admin already
  // exists (cmd/relay-server/bootstrap.go:20-23), so RELAY_BOOTSTRAP_PASSWORD is
  // never consumed and there is no matching credential. That is the single
  // failure this whole lane is most likely to hit, so it gets a named message.
  expect(res.status(), 'bootstrap admin login (401 => the database was not empty)').toBe(201)
  const { token } = (await res.json()) as { token: string }
  expect(token).toBeTruthy()

  const seed = await seedAll(request, token, runEnv.runId, runEnv.adminEmail)

  // storageState is written by hand rather than via a browser round trip: the
  // SPA's only credential is localStorage['relay.token'] (web/src/lib/token.ts:1)
  // and AuthProvider hydrates from /users/me on mount whenever that key is
  // present (web/src/auth/AuthProvider.tsx:124-139). No browser needed.
  writeFileSync(
    join(runDir, 'state.json'),
    JSON.stringify({
      cookies: [],
      origins: [
        {
          origin: new URL(baseURL!).origin,
          localStorage: [{ name: 'relay.token', value: token }],
        },
      ],
    }),
  )
  writeFileSync(join(runDir, 'seed.json'), JSON.stringify(seed, null, 2))
})
```

- [ ] **Step 4: Run and watch setup pass**

From `web/`:

```bash
npm run test:e2e 2>&1 | tail -30
cat e2e/.run/seed.json
```

Expected: `1 passed` for `[setup] > global.setup.ts:... log in and seed fixtures`, then the placeholder `auth.spec.ts` from Task 2 also passing on chromium. `seed.json` has ten keys with `e2e-<runid>-` names.

- [ ] **Step 5: Prove the fixtures are real**

```bash
curl -s http://127.0.0.1:8091/v1/health
```

(The server is stopped by now; instead verify by eye in the previous step's `seed.json` that `jobId` and `scheduleId` are UUIDs, not empty strings.)

- [ ] **Step 6: Restore and commit**

```bash
cd .. && git checkout -- web/dist/index.html
git add web/e2e/fixtures.ts web/e2e/global.setup.ts
git commit -m "test(web): seed e2e fixtures over REST as the bootstrap admin"
```

---

### Task 7: The surface list

**Files:**
- Create: `web/e2e/surfaces.ts`

- [ ] **Step 1: Write it**

Create `web/e2e/surfaces.ts`:

```ts
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
```

- [ ] **Step 2: Type-check it**

From `web/`:

```bash
npx tsc -b && echo "exit=$?"
```

Expected: `exit=0`. This is Task 3's guard doing its job on real harness code.

- [ ] **Step 3: Commit**

```bash
git add web/e2e/surfaces.ts
git commit -m "test(web): enumerate the e2e surface list with declared coverage limits"
```

---

### Task 8: `auth.spec.ts`

**Files:**
- Modify: `web/e2e/auth.spec.ts` (replacing the Task 2 placeholder in full)

- [ ] **Step 1: Write the real spec**

Replace `web/e2e/auth.spec.ts` entirely with:

```ts
import { expect, test, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { readSeed } from './fixtures'

const runDir = join(dirname(fileURLToPath(import.meta.url)), '.run')
const runEnv = JSON.parse(readFileSync(join(runDir, 'env.json'), 'utf8')) as {
  adminEmail: string
  adminPassword: string
}
const seed = readSeed()

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

  // web/src/app/PublicOnlyRoute.tsx:9 has NO unit test at HEAD - `rg
  // PublicOnlyRoute web/src --glob '*.test.tsx'` returns nothing, and web/src/app/
  // holds only ProtectedRoute.test.tsx and AdminRoute.test.tsx. The landing route
  // can be changed to any path and the whole 1100-test suite stays green. This is
  // the 2026-06-03 item's original complaint ("the missing post-login redirect: no
  // app-level navigation test existed"), still literally true, and this assertion
  // is the first gate in the repo that would catch it.
  //
  // 2026-08-24, SUPERSEDED: the rg grep above checks the component's NAME, not
  // its BEHAVIOUR - web/src/App.test.tsx:19-38 already drives this exact
  // redirect. See the shipped correction at web/e2e/auth.spec.ts:42.
  await expect(page).toHaveURL(/\/jobs$/)
  await expect(page.getByRole('heading', { name: 'Jobs', level: 1 })).toBeVisible()
})

test('a deep link while logged out redirects to /auth', async ({ page }) => {
  // A FULL page load at a client-only route. This also exercises
  // webui.Handler()'s SPA fallback (web/embed.go:32-42), which rewrites unknown
  // non-/v1 paths to index.html - server code with its own semantics that a Vite
  // dev-server harness could never reach.
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
})
```

- [ ] **Step 2: Run only this spec**

From `web/`:

```bash
npm run test:e2e -- auth.spec.ts --project=chromium
```

Expected: `3 passed` plus the `setup` project's 1.

- [ ] **Step 3: Prove it can fail - the landing-route mutation**

Edit `web/src/app/PublicOnlyRoute.tsx:9`, changing `to="/jobs"` to `to="/schedules"`. Then:

```bash
npm test 2>&1 | tail -5                                  # from web/
```

Expected: **PASS** - the whole unit suite is green, because `PublicOnlyRoute` has no test.

**2026-08-24, SUPERSEDED:** this is wrong. `web/src/App.test.tsx:19-38` drives `<App/>` through this exact redirect; confirmed directly by re-running this exact mutation against the current tree - `npm test` goes **RED** (2 failed, 1 passed), not PASS. See the correction at `web/e2e/auth.spec.ts:42` and the matching note at line 61 above. If re-running this step, expect RED here and treat the browser lane below as independent confirmation, not the only gate. Then:

```bash
cd .. && make web-build && go build -o bin/relay-server.exe ./cmd/relay-server && cd web
npm run test:e2e -- auth.spec.ts --project=chromium
```

Expected: **FAIL** on `a real login lands on /jobs` with `Expected pattern: /\/jobs$/` and `Received string: ".../schedules"`.

- [ ] **Step 4: Revert the mutation and confirm green**

```bash
cd .. && git checkout -- web/src/app/PublicOnlyRoute.tsx
make web-build && go build -o bin/relay-server.exe ./cmd/relay-server
cd web && npm run test:e2e -- auth.spec.ts --project=chromium
```

Expected: `3 passed`. Then `cd .. && git checkout -- web/dist/index.html && git diff --stat -- web/src/` must print nothing.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/auth.spec.ts
git commit -m "test(web): e2e auth smoke - login landing, deep link, logout"
```

---

### Task 9: `layout.spec.ts`

**Files:**
- Create: `web/e2e/layout.spec.ts`

- [ ] **Step 1: Write it**

Create `web/e2e/layout.spec.ts`:

```ts
import { expect, test } from '@playwright/test'
import { readSeed } from './fixtures'
import { surfaces } from './surfaces'

// jsdom performs NO layout. Every offsetWidth, scrollWidth and
// getBoundingClientRect() across web/src's 152 test files returns 0, so every
// layout assertion there is a structural guard or a class-string pin. This file
// is the only place in the repo where a width is a real number.
//
// Four qualifiers below are lessons the 2026-08-13 narrow-viewport slice paid
// for, converted into config so nobody re-learns them:
//   - POPULATED tables, because the item was misdiagnosed twice on empty ones.
//   - BOTH 320 and 375, because a fifth cause was found only at 320.
//   - HEADER and MAIN measured SEPARATELY from the document, because the fourth
//     cause was found only that way.
//   - /auth as a CONTROL (surfaces.ts), because it renders no shell and never
//     overflowed, which is what makes the header attribution an attribution.
const WIDTHS = [320, 375] as const
const seed = readSeed()

for (const width of WIDTHS) {
  test.describe(`narrow viewport ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } })

    for (const s of surfaces(seed)) {
      test(`${s.name} does not overflow horizontally`, async ({ page }, testInfo) => {
        await page.goto(s.path)
        await s.ready(page)

        const m = await page.evaluate(() => {
          const header = document.querySelector('header') as HTMLElement | null
          const main = document.querySelector('main') as HTMLElement | null
          return {
            docScroll: document.documentElement.scrollWidth,
            docClient: document.documentElement.clientWidth,
            headerScroll: header ? header.scrollWidth : null,
            mainScroll: main ? main.scrollWidth : null,
          }
        })

        // Recorded on EVERY run, pass or fail - the numbers are the artifact, not
        // a debugging afterthought collapsed into one boolean.
        await testInfo.attach(`widths-${s.name}-${width}`, {
          body: JSON.stringify({ surface: s.name, path: s.path, population: s.population, width, ...m }, null, 2),
          contentType: 'application/json',
        })

        // SCREENSHOTS ARE ARTIFACTS, NOT ASSERTIONS. No toHaveScreenshot, no
        // pixel baselines: rasterization, scrollbar metrics and subpixel layout
        // differ between a Windows dev machine and ubuntu-latest, so a baseline
        // would be either permanently red or permanently regenerated - the
        // decorative outcome this whole workflow exists to avoid. Doing it
        // properly needs a pinned container image and is its own slice.
        //
        // The residual is a PROCESS commitment: an artifact nobody opens is worth
        // nothing. The merge of this slice includes one human pass over these
        // images - specifically over the nav's horizontal scroll and the wrapped
        // tab bars, the two design decisions taken with no hi-fi reference.
        const shot = testInfo.outputPath(`${s.name}-${width}.png`)
        await page.screenshot({ path: shot, fullPage: true })
        await testInfo.attach(`screenshot-${s.name}-${width}`, { path: shot, contentType: 'image/png' })

        expect(m.docScroll, `${s.path}: document overflows at ${width}px`).toBeLessThanOrEqual(m.docClient)
        if (m.headerScroll !== null) {
          expect(m.headerScroll, `${s.path}: <header> overflows at ${width}px`).toBeLessThanOrEqual(m.docClient)
        }
        if (m.mainScroll !== null) {
          expect(m.mainScroll, `${s.path}: <main> overflows at ${width}px`).toBeLessThanOrEqual(m.docClient)
        }
      })
    }
  })
}
```

- [ ] **Step 2: Run it**

From `web/`:

```bash
npm run test:e2e -- layout.spec.ts --project=chromium
```

Expected: 26 tests (13 surfaces x 2 widths). If any go red on a pre-existing defect, apply the policy in "Handling a pre-existing red" below - do **not** fix product code beyond a one-line class string, and do **not** delete the test.

- [ ] **Step 3: Prove it can fail - the mutation `npm test` cannot catch**

Edit `web/src/admin/AdminTabs.tsx:17`, deleting `flex-wrap` from the class string (leave everything else byte-identical). Then:

```bash
npm test 2>&1 | tail -5                                  # from web/
```

Expected: **PASS, with the same counts as always.** `rg flex-wrap web/src --glob '*.test.tsx'` returns exactly one hit and it is a *comment* (`ProfilePage.test.tsx:59`). Nothing in the 1100-test suite asserts this class. Then:

```bash
cd .. && make web-build && go build -o bin/relay-server.exe ./cmd/relay-server && cd web
npm run test:e2e -- layout.spec.ts --project=chromium
```

Expected: **FAIL** on `admin-users`, `admin-invites`, `admin-enrollments`, `admin-reservations` and `admin-server` at **both** 320 and 375, with `<main> overflows`. Five pills do not fit either viewport - that is exactly why `flex-wrap` is there (`AdminTabs.tsx:11-16` records the browser pass that found it).

**This is the entire justification for the slice.** Open one of the attached PNGs and confirm the tab row is running off the right edge.

- [ ] **Step 4: Revert and confirm green**

```bash
cd .. && git checkout -- web/src/admin/AdminTabs.tsx
make web-build && go build -o bin/relay-server.exe ./cmd/relay-server
cd web && npm run test:e2e -- layout.spec.ts --project=chromium
cd .. && git checkout -- web/dist/index.html && git diff --stat -- web/src/
```

Expected: all green; the final `git diff --stat` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/layout.spec.ts
git commit -m "test(web): measure narrow-viewport widths at 320 and 375 with screenshots"
```

---

### Task 10: `keyboard.spec.ts`

**Files:**
- Create: `web/e2e/keyboard.spec.ts`

- [ ] **Step 1: Write it**

Create `web/e2e/keyboard.spec.ts`:

```ts
import { expect, test } from '@playwright/test'
import { readSeed } from './fixtures'

const seed = readSeed()

// WHY THESE TWO TABLES. EnrollmentsTable and InvitesTable have ZERO focusable
// elements in any row (web/src/admin/enrollments/EnrollmentsTable.tsx:49-76,
// web/src/admin/invites/InvitesTable.tsx:59+ render only text, Chips and cells),
// so their clipped right-hand columns are reachable only through the scroll
// wrapper's own tab stop - the tabIndex={0} + role="group" + aria-label at
// web/src/components/holo/Table.tsx:181-193. That fix was chosen SPECIFICALLY
// because the previous behaviour leaned on Chromium's implicit scroller
// focusability, which WebKit does not grant, and it has never been watched
// working by anyone in any engine.
//
// WEBKIT IS NOT SAFARI. Playwright's webkit is a bundled WebKit build. It
// exercises WebKit's focusability semantics, which is what the divergence is
// actually about; it does not exercise Safari's chrome, extensions or platform
// integration. The Known Limitation "Safari has not been opened" is NARROWED by
// this file, not retired.
//
// COVERAGE LIMIT (slice 1): no relay-agent runs, so no worker row exists. This
// file says nothing about WorkersTable's own scroll wrapper.
test.describe('scroll-wrapper keyboard reachability @webkit', () => {
  // NARROW ON PURPOSE. The wrapper only scrolls when its content exceeds it, and
  // EnrollmentsTable's min-w-[660px] / InvitesTable's min-w-[740px] fit easily in
  // a 1280px viewport - at the default width every assertion below would be
  // vacuously true.
  test.use({ viewport: { width: 480, height: 900 } })

  const cases = [
    { path: '/admin/enrollments', group: 'Agent enrollments, scrolls horizontally', marker: seed.enrollmentHostname },
    { path: '/admin/invites', group: 'Invites, scrolls horizontally', marker: seed.inviteEmail },
  ]

  for (const c of cases) {
    test(`${c.path}: a real Tab press reaches the labelled scroll region`, async ({ page }) => {
      await page.goto(c.path)
      await expect(page.getByText(c.marker)).toBeVisible()

      // The accessible name is DERIVED from the table's own label
      // (Table.tsx:190), so this locator also pins that it has not drifted.
      const group = page.getByRole('group', { name: c.group })
      await expect(group).toHaveCount(1)

      // REAL key events. web/src/components/holo/Table.test.tsx:317-328 already
      // proves tabindex="0" is in the DOM and proves nothing about keyboard
      // reachability, which is the whole reason this file exists.
      await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
      let reached = false
      for (let i = 0; i < 40 && !reached; i++) {
        await page.keyboard.press('Tab')
        reached = await group.evaluate((el) => el === document.activeElement)
      }
      expect(reached, `Tab never reached "${c.group}" within 40 presses`).toBe(true)
    })

    test(`${c.path}: arrow keys scroll the clipped columns into view`, async ({ page }) => {
      await page.goto(c.path)
      await expect(page.getByText(c.marker)).toBeVisible()
      const group = page.getByRole('group', { name: c.group })

      // PRECONDITION, asserted rather than assumed, and it is the only gate in
      // the repo that catches a computed Tailwind class. Tailwind v4 scans source
      // STATICALLY: if a consumer built its min-w-[...] string instead of writing
      // the literal, the DOM class attribute is byte-identical - so every jsdom
      // class-string pin stays green - while the production bundle emits no rule
      // at all, the wrapper never overflows, and the a11y fix silently does
      // nothing.
      const overflow = await group.evaluate((el) => el.scrollWidth - el.clientWidth)
      expect(overflow, 'scroll wrapper is not actually scrollable - did the min-width rule reach the bundle?').toBeGreaterThan(0)

      await group.focus()
      const before = await group.evaluate((el) => el.scrollLeft)
      for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowRight')
      await expect
        .poll(() => group.evaluate((el) => el.scrollLeft), { message: 'ArrowRight did not scroll the focused wrapper' })
        .toBeGreaterThan(before)
    })
  }
})
```

- [ ] **Step 2: Run it on both engines**

From `web/`:

```bash
npm run test:e2e -- keyboard.spec.ts
```

Expected: 8 passing (4 tests x chromium + 4 tests x webkit; the `grep: /@webkit/` on the webkit project matches this describe title, and no other spec carries the tag).

**Documented contingency, and the only one in this plan:** if the two `arrow keys scroll` tests pass on chromium and fail on webkit with `scrollLeft` unchanged, replace the five `ArrowRight` presses with a single `await page.keyboard.press('End')` - both are real key events on a focused scroll container and `End` scrolls to the far edge. Do not weaken the assertion, and do not switch to `group.evaluate(el => el.scrollLeft = 100)`, which would stop testing the keyboard entirely.

- [ ] **Step 3: Prove it can fail - the Tailwind mutation `npm test` cannot catch**

Edit `web/src/admin/enrollments/EnrollmentsTable.tsx:18`, changing

```ts
const MIN_W = 'min-w-[660px]'
```

to

```ts
const MIN_W = `min-w-[${660}px]`
```

The rendered class attribute is byte-identical. Then:

```bash
npm test 2>&1 | tail -5                                  # from web/
npx tsc -b && echo "tsc exit=$?"
```

Expected: **both green.** The unit suite pins class strings, not CSS rules, and the type is still `string`. Then:

```bash
cd .. && make web-build && go build -o bin/relay-server.exe ./cmd/relay-server && cd web
npm run test:e2e -- keyboard.spec.ts --project=chromium
```

Expected: **FAIL** on `/admin/enrollments: arrow keys scroll the clipped columns into view` with `scroll wrapper is not actually scrollable - did the min-width rule reach the bundle?` and `Received: 0`.

**2026-08-24, SUPERSEDED - the expected output above is wrong, confirmed by re-running:**
1. This exact numeric-literal-interpolation form (`` `min-w-[${660}px]` ``) **does** reproduce - measured directly, with no object-property-lookup detour needed: it is not class-shaped text (the `${660}` breaks up what would otherwise be a matching candidate), so `@tailwindcss/vite`'s Scanner never emits the rule for it, and `dist/assets/*.css` shows no `min-width:660px` / `min-width:740px` rule with this mutation applied. An earlier pass reported this form as not reproducing; that report was itself wrong, and the cause of that earlier false negative is unknown (a stale `web/dist` embed - `//go:embed` snapshots at Go compile time - is an unverified suspicion, not a finding).
2. `Received: 0` never occurs. The wrapper's fixed-pixel columns and cell padding (undamaged by this mutation - `COLS` is a plain literal) already produce a *small* residual overflow with the rule missing - measured directly at 51px (enrollments) and 32px (invites) - which the original `toBeGreaterThan(0)` precondition could not tell apart from a working 660px/740px rule. `keyboard.spec.ts` now asserts `toBeGreaterThan(100)` specifically because of this; against this mutation form (applied to both tables) the actual failure reads `Expected: > 100 / Received: 51` (enrollments) or `32` (invites), on both the Tab-press and arrow-keys tests for each table - 8 of `keyboard.spec.ts`'s 8 tests (4 chromium + 4 webkit), not `Received: 0` on the arrow-keys test alone and not the 6-test count an earlier pass reported. See the comment above `assertScrollable` in `web/e2e/keyboard.spec.ts:61-87` for the full measurement.

- [ ] **Step 4: Revert and confirm green**

```bash
cd .. && git checkout -- web/src/admin/enrollments/EnrollmentsTable.tsx
make web-build && go build -o bin/relay-server.exe ./cmd/relay-server
cd web && npm run test:e2e -- keyboard.spec.ts
cd .. && git checkout -- web/dist/index.html && git diff --stat -- web/src/
```

Expected: 8 passing; the final `git diff --stat` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/keyboard.spec.ts
git commit -m "test(web): real Tab and arrow presses on the table scroll wrappers, chromium + webkit"
```

---

### Task 11: `make test-e2e`

**Files:**
- Modify: `Makefile:1` (`.PHONY`), `Makefile:13` (after `web-build`), and a new `RELAY_SERVER_BIN` block

- [ ] **Step 1: Prove the failing state**

From the repo root, in Git Bash:

```bash
make test-e2e
```

Expected: FAIL - `No rule to make target 'test-e2e'`.

- [ ] **Step 2: Add the binary-name variable**

Insert immediately after the `.PHONY` line at `Makefile:1`:

```make
# `go build -o` writes exactly the name it is given, and Windows shells will not
# execute an extensionless file. web/playwright.config.ts picks the same two names
# from process.platform - the two must agree.
ifeq ($(OS),Windows_NT)
RELAY_SERVER_BIN := bin/relay-server.exe
else
RELAY_SERVER_BIN := bin/relay-server
endif
```

and extend the `.PHONY` list with `test-e2e`.

- [ ] **Step 3: Add the target**

Insert after the `web-dev` target (`Makefile:11-13`):

```make
# Run the browser end-to-end suite (Playwright) against the PRODUCTION-EMBEDDED
# SPA - not the Vite dev server. Tailwind v4 scans source statically, so a
# computed class string emits no CSS and a no-op fix is indistinguishable from a
# working one to every other test in this repo; only a production bundle can tell
# them apart. web/embed.go's SPA fallback is server code with its own semantics,
# and embedded serving is same-origin where the dev server is :5173 proxying
# :8080.
#
# Requires: node, go, and a Postgres reachable at
# postgres://relay:relay@127.0.0.1:5432 - the relay-postgres container
# scripts/dev.ps1 already manages. Install the browsers once with:
#   cd web && npx playwright install chromium webkit
#
# THE BUILD ORDER IS LOAD-BEARING. web/embed.go snapshots web/dist at COMPILE
# time (//go:embed all:dist), so relay-server must be rebuilt AFTER web-build or
# it serves the previous bundle - or, from a clean checkout, the 7-line "has not
# been built" placeholder, which makes every spec fail with no #root and no
# obvious cause.
#
# web/dist/index.html is a TRACKED placeholder while everything else under
# web/dist/ is gitignored (.gitignore:6-8), so a build replaces exactly one
# tracked file with an index referencing hashed assets nobody else has.
# Restoring it here, pass or fail, makes that a step instead of a rule people
# have to remember. Uses sh syntax, like `clean` above - run make from Git Bash
# on Windows, not from cmd.
test-e2e: web-build
	go build -o $(RELAY_SERVER_BIN) ./cmd/relay-server
	cd web && npm run test:e2e; rc=$$?; cd .. && git checkout -- web/dist/index.html; exit $$rc
```

- [ ] **Step 4: Run the whole lane end to end**

From the repo root, in Git Bash, with `docker start relay-postgres` done:

```bash
make test-e2e
git status --porcelain
```

Expected: `setup` 1 passed, `chromium` 33 passed (3 auth + 26 layout + 4 keyboard), `webkit` 4 passed. `git status --porcelain` prints nothing - the restore step ran.

- [ ] **Step 5: Prove the restore runs on failure too**

```bash
sed -i 's/min-w-\[660px\]/min-w-[1px]/' web/src/admin/enrollments/EnrollmentsTable.tsx
make test-e2e ; echo "make exit=$?"
git status --porcelain web/dist
git checkout -- web/src/admin/enrollments/EnrollmentsTable.tsx
```

Expected: `make exit=1` (a non-zero exit is propagated, per acceptance criterion 1) and `git status --porcelain web/dist` prints **nothing** - `web/dist/index.html` was restored despite the failure.

- [ ] **Step 6: Commit**

```bash
make web-build >/dev/null 2>&1 || true
git checkout -- web/dist/index.html
git add Makefile
git commit -m "build: add make test-e2e for the browser lane"
```

---

### Task 12: The CI workflow, seen to fail before it is trusted

**Files:**
- Create: `.github/workflows/web-ci.yml`

- [ ] **Step 1: Prove the failing state**

```bash
ls .github/workflows/
grep -ril 'npm\|node\|web' .github/workflows/ | wc -l
```

Expected: exactly `go-ci.yml`, `python.yml`, `release.yml`, and `0`. **`npm test`, `tsc -b` and `npm run build` have never run in CI.** That is the larger of this slice's two coverage gains and the PR body must say so.

- [ ] **Step 2: Write the workflow**

Create `.github/workflows/web-ci.yml`:

```yaml
name: web-ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

concurrency:
  group: web-ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  web:
    name: unit + typecheck + build + browser
    runs-on: ubuntu-latest
    timeout-minutes: 20
    services:
      # Same image, user, password and port as the relay-postgres container
      # scripts/dev.ps1:84-96 manages locally, so
      # postgres://relay:relay@127.0.0.1:5432 is one string in both environments.
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: relay
          POSTGRES_PASSWORD: relay
          POSTGRES_DB: relay
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    steps:
      - uses: actions/checkout@v5

      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: web/package-lock.json

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - name: Install web dependencies
        run: npm ci
        working-directory: web

      # The next three steps have NEVER run in CI before this workflow existed.
      # They are listed separately rather than folded into `npm run build` (which
      # runs tsc -b again) so a failure names which gate broke.
      - name: Type check
        run: npx tsc -b
        working-directory: web

      - name: Unit tests (vitest)
        run: npm test
        working-directory: web

      - name: Production SPA build
        run: npm run build
        working-directory: web

      # MUST come after the SPA build: web/embed.go snapshots web/dist at compile
      # time (//go:embed all:dist). Reordering these two ships the placeholder.
      - name: Build relay-server (embeds web/dist)
        run: go build -o bin/relay-server ./cmd/relay-server

      - name: Resolve Playwright version
        id: pw
        run: echo "version=$(node -p "require('./web/node_modules/@playwright/test/package.json').version")" >> "$GITHUB_OUTPUT"

      - name: Cache Playwright browsers
        uses: actions/cache@v4
        with:
          path: ~/.cache/ms-playwright
          key: ms-playwright-${{ runner.os }}-${{ steps.pw.outputs.version }}

      # Runs even on a cache hit: --with-deps installs apt packages that the
      # ~/.cache/ms-playwright cache does not contain. It is a no-op download when
      # the browsers are already cached.
      - name: Install Playwright browsers
        run: npx playwright install --with-deps chromium webkit
        working-directory: web

      - name: Browser end-to-end (Playwright)
        run: npm run test:e2e
        working-directory: web

      # D6: screenshots are ARTIFACTS, not assertions. layout.spec.ts writes one
      # full-page PNG per surface per width on every run, pass or fail, and the
      # merge of this slice includes one human pass over them. An artifact nobody
      # opens is worth nothing.
      - name: Upload Playwright report and screenshots
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: playwright-${{ github.run_id }}
          path: |
            web/playwright-report/
            web/test-results/
          retention-days: 14
```

- [ ] **Step 3: Push and watch it pass**

```bash
git add .github/workflows/web-ci.yml
git commit -m "ci: run the whole web gate - vitest, tsc, build and playwright"
git push -u origin HEAD
gh run watch
```

Expected: `web-ci` green in roughly 5-9 minutes (cold browser install on the first run).

- [ ] **Step 4: See it fail, deliberately - a workflow that has never been red is not a gate**

```bash
sed -i 's/flex flex-wrap gap-1.5 self-start/flex gap-1.5 self-start/' web/src/admin/AdminTabs.tsx
git commit -am "TEMP: prove web-ci goes red - do not merge"
git push
gh run watch
```

Expected: **RED**, at the `Browser end-to-end (Playwright)` step, on the five `admin-*` layout tests at both widths - and **green** at `Unit tests (vitest)` in the same run, which is the demonstration that the browser lane covers something the unit lane structurally cannot.

Download the artifact and open `admin-users-320.png`. That image is the point of the slice.

- [ ] **Step 5: Revert the deliberate failure**

```bash
git revert --no-edit HEAD
git push
gh run watch
```

Expected: green. Then confirm the tree is clean of it:

```bash
git diff --stat origin/main -- web/src/
```

Expected: **no output.** Acceptance criterion 9 requires zero files under `web/src/` in the final diff, and a revert commit plus its parent nets to zero - but verify rather than assume.

---

### Task 13: Documentation and the final gate

**Files:**
- Create: `web/e2e/README.md`

- [ ] **Step 1: Write the README**

Create `web/e2e/README.md`:

```markdown
# Browser end-to-end harness (slice 1)

Playwright driving the **production-embedded** SPA - `make web-build` -> `web/dist`
-> `//go:embed` -> `relay-server` -> `webui.Handler()` - against a real Postgres on a
dedicated `relay_e2e` database. Not the Vite dev server: Tailwind v4 scans source
statically, so a computed class string emits no CSS and a no-op fix is
indistinguishable from a working one to every other test in this repo.

## Run it on Windows

Prerequisites: Go, Node, Docker Desktop, and the browsers once.

    docker start relay-postgres          # or run scripts/dev.ps1 once to create it
    cd web && npm ci && npx playwright install chromium webkit && cd ..
    make test-e2e                        # from Git Bash, not cmd or PowerShell

`make test-e2e` builds the SPA, rebuilds `relay-server` (the embed is a
compile-time snapshot, so the order matters), runs the suite, and restores the
tracked `web/dist/index.html` placeholder on exit, pass or fail.

To iterate on one spec:

    cd web && npm run test:e2e -- layout.spec.ts --project=chromium
    cd web && npm run test:e2e:ui

Both go through `e2e/ensure-db.mjs`, which drops and recreates the database.
Running `npx playwright test` directly will fail with a message telling you so.
If you ran the npm script directly, restore the placeholder yourself:
`git checkout -- web/dist/index.html`.

## Run it in CI

`.github/workflows/web-ci.yml`, blocking on every PR. It also carries the
`tsc -b`, `npm test` and `npm run build` gates, which had never run in CI before
this workflow existed.

## What it covers, and what it does not

Screenshots are **artifacts, not assertions**. `layout.spec.ts` writes one
full-page PNG per surface per width on every run and CI uploads them. There are
no pixel baselines: cross-platform rasterization would make them either
permanently red or permanently regenerated. Someone has to open them.

**No `relay-agent` runs in slice 1, so no worker row can exist.** `/workers` and
`/workers/:id` are covered in their empty state only, no job executes, no task
reaches `running`, and SSE task-log tailing is not exercised. `surfaces.ts`
records the limit per surface in a `population` field - do not read an
empty-state pass as a populated-state pass. Closing this is slice 2.

**Playwright's `webkit` is a bundled WebKit build, not Safari.** It exercises
WebKit's focusability semantics - the reason `components/holo/Table.tsx` carries
an explicit `tabIndex={0}` - and nothing about Safari's chrome, extensions or
platform integration. Do not cite this harness as Safari coverage.

The **rate limiter is not exercised**: the test server runs
`RELAY_LOGIN_RATE_LIMIT=1000:1m`. Register/self-registration flows are out too -
covering them would mean the one test server never runs the default posture,
which is the one production runs.

## Rules

- `retries: 0` and `workers: 1`, both deliberate. Read the comments in
  `playwright.config.ts` before changing either.
- Locate every fixture by its per-run unique name. No count assertions over
  `/jobs`, `/schedules` or `/admin/*` - they are unscoped global lists. No nth-row
  locators.
- Never `waitForLoadState('networkidle')`: the list hooks poll on a
  `refetchInterval`, so the network never goes idle.
- A `test.fixme` must cite a filed backlog item id in its annotation. One without
  an id should fail review.
```

- [ ] **Step 2: Run the full gate**

From the repo root, Git Bash:

```bash
cd web && npm test && npx tsc -b && npm run build && cd ..
go test ./... -timeout 120s
make test-e2e
git checkout -- web/dist/index.html
git status --porcelain
git diff --stat origin/main -- web/src/
git diff --stat origin/main -- '*.go'
```

Expected, in order: the Task 1 baseline counts (unchanged); `tsc` exit 0; a successful bundle; the Go suite green; the browser suite green; **no output** from all three git commands.

- [ ] **Step 3: Commit**

```bash
git add web/e2e/README.md
git commit -m "docs(web): how to run the browser harness and what it does not cover"
```

- [ ] **Step 4: Open the PR**

The body must state, explicitly, that **the existing Vitest suite runs in CI for the first time** - it is the larger of the two coverage gains and reviewers will otherwise read this as an e2e-only change. Include the two mutation demonstrations (Task 9 Step 3, Task 10 Step 3) and link the artifact from the deliberate-failure run in Task 12 Step 4.

Do **not** close `docs/backlog/idea-2026-06-03-web-e2e-harness.md`. This is slice 1 of 2; the item's amendment is only half satisfied until an agent runs in the harness.

---

## Mutation matrix

Every row was checked against HEAD before being written down. "npm test" means `cd web && npm test`; "go test" means `go test ./...`.

| # | Mutation | Site | `npm test` | `tsc -b` | `go test` | Browser lane |
|---|---|---|---|---|---|---|
| **M1** | Delete `flex-wrap` from the tab-bar class string | `web/src/admin/AdminTabs.tsx:17` | **GREEN** - `rg flex-wrap web/src --glob '*.test.tsx'` returns one hit and it is a comment (`ProfilePage.test.tsx:59`) | GREEN | GREEN | **`layout.spec.ts` RED** on all five `admin-*` surfaces at 320 **and** 375 (`<main> overflows`) |
| **M2**† | `const MIN_W = 'min-w-[660px]'` -> `` const MIN_W = `min-w-[${660}px]` `` | `web/src/admin/enrollments/EnrollmentsTable.tsx:18` | **GREEN** - the rendered class attribute is byte-identical and `Table.test.tsx` pins class strings, not CSS rules | GREEN | GREEN | **`keyboard.spec.ts` RED** on `/admin/enrollments`'s arrow-scroll precondition (`Received: 0`); `/admin/invites` still passes |
| **M3**‡ | `to="/jobs"` -> `to="/schedules"` | `web/src/app/PublicOnlyRoute.tsx:9` | **GREEN** - `PublicOnlyRoute` has no test file at HEAD | GREEN | GREEN | **`auth.spec.ts` RED** on `a real login lands on /jobs` |
| M4 | `tabIndex={0}` -> `tabIndex={-1}` on the scroll wrapper | `web/src/components/holo/Table.tsx:190` | RED (`Table.test.tsx:323`) | GREEN | GREEN | `keyboard.spec.ts` RED on both engines - Tab never reaches the group |
| M5 | Delete the `fs.Stat` fallback branch | `web/embed.go:37-42` | GREEN | GREEN | RED (`web/embed_test.go:10-22`) | `auth.spec.ts` RED on the logged-out deep link |
| M6 | Build `relay-server` **without** `make web-build` first | build order | GREEN | GREEN | GREEN | **All three specs RED** - the placeholder index has no `#root` |

**M1, M2 and M3 are the justification for this slice.** Each is a real defect class this project has actually shipped or currently has no gate for, and each leaves every existing gate - 1100 unit tests, the type checker, and the whole Go suite - green.

**2026-08-24, SUPERSEDED:**
- **† M2's Browser-lane column is wrong; the mutation form shown is correct.** Confirmed by re-running: the numeric-literal-interpolation form shown (`` `min-w-[${660}px]` ``) does reproduce - it is not class-shaped text, so `@tailwindcss/vite`'s Scanner never emits the rule for it, regardless of esbuild's later constant-folding inside the JS bundle (irrelevant either way, since the Scanner reads source files on disk, never the bundle). Applied to both tables, it produces `Expected: > 100 / Received: 51` (enrollments) or `32` (invites) on **both** `keyboard.spec.ts` tests per table - 8 of 8 tests, not `Received: 0` on the arrow-keys test alone. An earlier pass reported this mutation form as not reproducing and proposed an object-property-lookup form as the fix for that; that report was itself wrong and no such detour is needed. The cause of that earlier false negative is unknown - see the Task 9 Step 3 note above for the full measurement.
- **‡ M3's `npm test` column is wrong.** `web/src/App.test.tsx:19-38` already drives this redirect; re-running this exact mutation against the current tree turns `npm test` **RED** (2 failed), not GREEN. The Browser-lane column (`auth.spec.ts` RED) is still accurate and is still independent, real-server confirmation - it is the `npm test` claim specifically that does not hold. See the note at line 61 and the correction shipped at `web/e2e/auth.spec.ts:42`.

The **conclusion these two rows exist to support - that the browser lane catches something the existing gates do not - still holds for M2** (no unit test pins the CSS rule reaching the bundle, only the class string) but **no longer holds for M3**: `App.test.tsx` already covers this redirect, so M3 is not evidence of a gap this slice closes. M1 alone remains an unambiguous, un-superseded justification.

- M1 is the layout class jsdom cannot measure (`offsetWidth`/`scrollWidth` are always 0 there).
- M2 is the Tailwind static-scan class the 2026-08-13 review lane raised as its highest-value finding ("does this fix emit any CSS in a production build at all") and which nothing in the repo has been able to ask since.
- M3 is the **original 2026-06-03 item's own complaint**, still uncovered at HEAD.

Tasks 8, 9 and 10 each require their row to be demonstrated, both directions, before the task commits. Per [[reference_mutation_proof_must_leave_a_test]], the discriminating input survives: the spec files stay in the tree, so the mutation stays detectable forever.

---

## Handling a pre-existing red

`layout.spec.ts` at 320px on populated tables plausibly finds something on merge day. That is the point, and the policy is fixed in advance so it is not negotiated under pressure:

1. **A one-line Tailwind class string** of the kind the 2026-08-13 slice already shipped ten of: fix it in this slice, in its own commit, named in the PR body.
2. **Anything larger:** commit the spec as `test.fixme()` with a filed backlog item id in the annotation. The gate stays green, the debt is named, and the test is not deleted.
3. **A `test.fixme` with no item id is not acceptable and should fail review.** That is the guard against this mechanism becoming a way to hide findings.

A harness that lands entangled with a product fix is two changes in one review and neither gets reviewed properly.

---

## Gate commands

**Local, Windows dev host, Git Bash, from the repo root:**

```bash
docker start relay-postgres
cd web && npm ci && npx playwright install chromium webkit && cd ..

# the full gate
cd web && npm test && npx tsc -b && npm run build && cd ..
go test ./... -timeout 120s
make test-e2e

# the diff gate (acceptance criterion 9)
git checkout -- web/dist/index.html
git status --porcelain                              # expect: no output
git diff --stat origin/main -- web/src/             # expect: no output
git diff --stat origin/main -- '*.go'               # expect: no output
```

**CI:** `.github/workflows/web-ci.yml`, blocking on every PR, in the order `npm ci` -> `npx tsc -b` -> `npm test` -> `npm run build` -> `go build ./cmd/relay-server` -> browser install -> `npm run test:e2e` -> artifact upload.

**Expected counts at the end of the slice:** `setup` 1, `chromium` 33 (3 auth + 26 layout + 4 keyboard), `webkit` 4. Vitest file and test counts identical to the Task 1 Step 1 baseline.

---

## Follow-on work (proposals - the conductor files these, this plan does not)

1. **Slice 2: an agent in the harness.** Run `relay-agent` against the test server with auto-enroll so `/workers`, `/workers/:id`, `/admin/reservations` populated, job execution and SSE task-log tailing become testable. Closes the coverage limit `surfaces.ts` declares. This is the larger half of the item's value and is deliberately not slice 1. **The item `idea-2026-06-03-web-e2e-harness` stays open until this lands.**
2. **Visual regression baselines on a pinned container image.** Converts the screenshots from artifacts into a gate.
3. **Axe integration** over the surface list `surfaces.ts` already enumerates.
4. **Amend `idea-2026-08-23-integration-only-guards-ci-never-runs`** with V6: its Go-side complaint has a strictly worse frontend twin - the web suite was not merely tag-gated but entirely absent from CI - and this slice closes that half.
5. **A note in `README.md` or `CLAUDE.md`** pointing at `web/e2e/README.md`.
6. ~~**File `PublicOnlyRoute` has no unit test** as its own small item if the browser lane is not considered sufficient coverage for `web/src/app/PublicOnlyRoute.tsx:9`.~~ **2026-08-24, SUPERSEDED:** it already has one - `web/src/App.test.tsx:19-38` drives this exact redirect. See the note at line 61.

This plan is **one PR, one session**. It has no multi-session stages, so there is nothing here for `/backlog phases`.
