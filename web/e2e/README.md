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

**If `make` is not on PATH** (measured on this host: it is not, and there is no
other copy anywhere on PATH), use the MSYS2 copy directly:
`/c/msys64/usr/bin/make.exe`.

That copy has a second problem beyond PATH, also measured directly: its recipe
subshells do not inherit `OS`, `TEMP`, `TMP` or the Go environment variables
(`GOPATH`, `GOMODCACHE`, `GOCACHE`) from the invoking shell, even when they are
exported there - `make -p` shows them absent from make's own environment
section entirely. Symptoms are specific enough to be worth naming: the
`RELAY_SERVER_BIN` `ifeq ($(OS),Windows_NT)` silently takes the non-Windows
branch (the built binary is missing the `.exe` suffix `webServer` expects), and
`go build` fails with either `module cache not found: neither GOMODCACHE nor
GOPATH is set` or `Access is denied` creating a work directory under
`C:\WINDOWS`. GNU make command-line variable assignments (as opposed to shell
environment prefixes) *are* forwarded to recipe subshells, so the fix is to
pass them that way:

    /c/msys64/usr/bin/make.exe test-e2e \
      OS="$OS" TEMP="$TEMP" TMP="$TMP" \
      GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"

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
- **Never read `readSeed()` (or anything else from `e2e/.run/seed.json`) at
  module scope.** Playwright collects every spec file across every project -
  including chromium/webkit, which `dependencies: ['setup']` on - *before* it
  runs any test in any project. That collection pass happens before the setup
  project's own test has run and written `seed.json`, so a module-scope
  `const seed = readSeed()` throws ENOENT on a clean checkout. This is not
  hypothetical: it shipped in this slice's first commits, passed every local
  run only because a stale `seed.json` from a previous run persisted in
  `e2e/.run/`, and was caught by the first real CI run against a fresh
  checkout. `env.json` (written by `ensure-db.mjs`, which runs before
  Playwright starts at all) has no such hazard and is fine at module scope.
  When a spec needs seed-derived structure at collection time (a per-surface
  test title, for instance), keep the STRUCTURE seed-independent - `surfaces.ts`
  hardcodes surface names, not values - and make the seed-dependent parts
  (`path`, `ready`, `marker`, ...) functions of `Seed` called from inside the
  test body, after execution (not collection) has reached that point. Before
  trusting a green local run of a change here, delete `e2e/.run/` first; a
  stale run directory can hide exactly this class of bug.
