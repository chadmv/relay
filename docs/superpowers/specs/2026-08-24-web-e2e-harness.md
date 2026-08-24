---
title: An end-to-end browser harness for the web SPA
date: 2026-08-24
status: draft (unattended run - every fork decided by the TPM lane; human review pending)
closes: idea-2026-06-03-web-e2e-harness (slice 1 of 2)
theme: dev-tooling / web-frontend
---

# An end-to-end browser harness for the web SPA

**TL;DR.** Add `@playwright/test` under `web/e2e/`, serving the **production-embedded** SPA
(`make web-build` -> `relay-server` -> `webui.Handler()`) against a real Postgres on a dedicated
`relay_e2e` database, seeded through the **REST API** as the bootstrap admin. Run it **in CI**, in a
new `web-ci.yml` that also puts the existing 1100-test Vitest suite into CI **for the first time**.
Chromium runs everything; **WebKit** runs the keyboard subset, because that is the only place the
Chromium-versus-WebKit divergence the shipped code depends on can be exercised. Slice 1 ships three
spec files - real key events, narrow-viewport measurement with screenshots as artifacts, and the
original auth smoke - and changes **zero application source**.

---

## 0. How this spec was produced, and what that costs

This is an unattended autopilot run. The `superpowers:brainstorming` flow's human gates - the visual
companion offer, one-question-at-a-time clarification, per-section approval, and the written-spec
review gate - had no human to answer them. They were executed as **self-decided forks with the
reasoning logged**, which is what the run instruction directs. Section 3 is the substitute: every
question this lane would have asked appears there as a decision with its alternatives and the
argument that settled it.

Two consequences worth naming rather than hiding:

- **The human review gate is not satisfied, only deferred.** Every decision below is reversible at
  plan time and several are flagged as the reviewer's to overrule (D3, D4, D9).
- Where judgment did not clearly resolve a fork, the rule applied was **most conservative and most
  reversible**, and each such case says so. D6 (screenshots as artifacts, not assertions) and D12
  (`test.fixme` over deletion) are the two places it decided the outcome.

---

## 1. Problem

`web/` has 152 test files and roughly 1100 assertions, and **jsdom performs no layout**. Every
`offsetWidth`, `scrollWidth` and `getBoundingClientRect()` in that suite returns 0, so every layout
assertion is a structural guard or a class-string pin. The project's substitute has been an ad-hoc
human-driven browser lane in Phase 4, used when the Go diff is empty. It works, and across three
sessions it has failed to obtain the same two things:

1. **No screenshot, ever.** Every browser session in this arc measured with `elementFromPoint`,
   `getBoundingClientRect` and `scrollWidth` instead of looking. The 2026-08-13 narrow-viewport slice
   shipped **two design decisions taken with no hi-fi reference** (the nav scrolls rather than
   collapsing; the scrollbar under it is deliberately visible) and **nobody has seen either
   rendered**. `docSW <= clientW` says a page does not overflow and says nothing about whether the
   result reads as anything.
2. **No real key events.** The lane cannot send a `Tab` press. `EnrollmentsTable` and `InvitesTable`
   have zero focusable elements in any row, so their clipped right-hand columns are reachable only
   through the scroll wrapper's own tab stop. That wrapper's `tabIndex={0}` + `role="group"` +
   `aria-label` fix was chosen **specifically** because the previous behaviour leaned on Chromium's
   implicit scroller focusability, which WebKit does not grant - and the fix has never been watched
   working, in any engine, by anyone. It is pinned by a jsdom attribute assertion, which proves the
   attribute exists and proves nothing about keyboard reachability.

The prioritization datum: in that slice, **seven of eight verification points were obtainable only by
a human driving a real browser, and none is protected against regression by `npm test`.** The slice
fixed an app-wide rendering defect whose entire acceptance criterion the automated gate cannot
express.

The item's original 2026-06-03 framing (auth contract drift, missing post-login redirect) is stale
rather than wrong. It is the cheap suite. The amendment is the valuable one, and this spec sequences
accordingly.

**A second problem surfaced during verification and changes the shape of the answer:** there is no
web CI at all. See F1 below.

---

## 2. Verified against HEAD (`7e17664`)

Nothing below is taken from the item, from ROADMAP, or from memory without a read.

### Confirmed

| # | Claim | Evidence |
|---|---|---|
| C1 | `RELAY_BOOTSTRAP_ADMIN` + `RELAY_BOOTSTRAP_PASSWORD` create or promote an admin at startup, and both are `Unsetenv`'d after use | `cmd/relay-server/main.go:74-88` |
| C2 | Bootstrap is **a no-op if any admin already exists** - it returns before looking at the email | `cmd/relay-server/bootstrap.go:16-23` |
| C3 | Migrations run on startup, so an empty database is a complete starting state | `cmd/relay-server/main.go:49-53` |
| C4 | The SPA is served by the API mux at `/` when `StaticHandler` is set, and `/v1/*` never falls through to it | `internal/api/server.go:204-206`, `web/embed.go:20-45` |
| C5 | `make web-dev` is exactly `cd web && npm run dev` = `vite`, and the dev server proxies `/v1` to `localhost:8080` | `Makefile:12-13`, `web/package.json:7`, `web/vite.config.ts:8-12` |
| C6 | `GET /v1/health` exists and is public; `scripts/dev.ps1` already polls it as its readiness gate | `internal/api/server.go:96`, `scripts/dev.ps1:133-144` |
| C7 | **No browser tooling of any kind is present.** `@playwright/test`, puppeteer and cypress are absent from `web/package.json`, from `web/package-lock.json` (0 occurrences), and from `web/node_modules` | `web/package.json:21-35`; lockfile grep |
| C8 | The SPA stores its bearer token in `localStorage` under `relay.token`, which is what Playwright `storageState` captures | `web/src/lib/token.ts:1-13` |
| C9 | Login is rate-limited **10 requests per minute, keyed on `RemoteAddr` only** (register: 5/min). Every Playwright worker is `127.0.0.1`, so they share one bucket | `cmd/relay-server/main.go:164-171`, `internal/api/ratelimit.go:44-71` |
| C10 | `scripts/dev.ps1` already scaffolds the full combined run - build, Postgres container, bootstrap admin, CLI login, agent, Vite - in separate windows | `scripts/dev.ps1` in full |
| C11 | Self-registration is **off by default** and needs `RELAY_ALLOW_SELF_REGISTER` | `cmd/relay-server/main.go:173-175` |
| C12 | Vitest picks tests up by its default `include` (`**/*.{test,spec}.*`) with no `include`/`exclude` override, and tests are co-located under `web/src/` | `web/vite.config.ts:13-18`; 152 co-located `*.test.tsx` files |
| C13 | `tsc -b` type-checks `web/src` only - `include: ["src"]` | `web/tsconfig.json:19` |
| C14 | CI runs `go test -race ./...` with no tags plus `make vet-integration` | `.github/workflows/go-ci.yml:26-34` |

### Refuted

| # | The claim | What HEAD actually shows |
|---|---|---|
| F1 | Implicit in the run brief and in ROADMAP: the question is whether to add *e2e* to CI | **There is no web CI at all.** `.github/workflows/` contains exactly `go-ci.yml`, `python.yml`, `release.yml`, and a case-insensitive grep for `npm`, `node` or `web` across all three returns **zero matches**. `npm test`, `tsc -b` and `npm run build` have never run in CI. This inverts fork 1 - see D1. |
| F2 | Memory `feedback_web_dist_not_maintained`: "`web/dist` is tracked but stale from the scaffold; a frontend build dirties it" | Directionally right, materially wrong about the size. `.gitignore:6-8` ignores `web/dist/` with a `!web/dist/index.html` negation, and `web/dist/` contains **exactly one file**: a 7-line placeholder reading "The Relay web UI has not been built." So a build dirties **one** tracked file, replacing the placeholder with a real index referencing **gitignored** hashed assets. Committing it would embed an index pointing at assets nobody else has. Narrower hazard, sharper failure mode. |
| F3 | ROADMAP `Now`, on this item: "`make web-dev` + a real relay-server answer the serving one" | Available, and **the wrong target**. Tailwind v4 scans source statically, so the dev server and the production bundle can contain different CSS - the 2026-08-13 slice's highest-value review finding was literally "does this fix emit any CSS in a production build at all". A layout harness on the dev server is structurally unable to ask that. See D3. |
| F4 | ROADMAP item 21 on the vite/vitest upgrade: do it "ideally before the new test surface grows" | Sequencing preference inverted here, with reasons. See D11. |
| F5 | `release.yml` might ship binaries with the placeholder SPA | Not a live concern. `release.yml` is `python-release`, tagged `python-v*`, PyPI only. There is no Go binary release workflow to be wrong. |

### Two findings that were not on anyone's list

- **A reused database silently breaks login.** Because bootstrap returns early when *any* admin
  exists (C2), pointing the harness at a developer's `relay` database leaves
  `RELAY_BOOTSTRAP_PASSWORD` entirely unconsumed, and the login the whole suite depends on returns
  401 with no diagnostic anywhere. This is why D2 mandates a dedicated database name.
- **Vitest would run the Playwright specs.** Vitest's default `include` matches `**/*.spec.ts` and
  its default `exclude` does not cover `e2e/` (C12), so `web/e2e/auth.spec.ts` would be collected by
  `npm test` and executed in jsdom, failing confusingly on a missing `page`. This is a one-line fix
  and it is an acceptance criterion (D7), not a plan detail, because omitting it produces a red gate
  that looks like the harness is broken.

---

## 3. Decisions

### D1. CI, not local-only - and the workflow carries the unit suite too

**Options.** (a) Local-only lane, like `make test-integration`. (b) A Playwright-only CI job.
(c) A `web-ci.yml` that runs the whole web gate - `tsc -b`, `npm test`, `npm run build`, then
Playwright - as a blocking check.

**Chosen: (c).**

The brief asks this lane to address a live tension: `idea-2026-08-23-integration-only-guards-ci-never-runs`
argues that guards behind a tag CI never runs are decorative, and the 2026-08-24 finishRegister slice
measured that cost precisely - deleting `handedOff = true` left **all 21 packages green**, and the
substitute was a ~500-line `go/ast` guard **evaded five times**. A local-only browser harness would
be a fifth instance of exactly that pattern, filed under a different theme.

But F1 changes the question. The honest framing is not "should e2e be in CI" - it is **the web lane
has no CI at all**, and adding a Playwright-only job while `npm test` stays unenforced would be
backwards. So the workflow's first value is that the existing 1100 tests, the type check and the
production build start being enforced. The browser lane is the second value, and it is the one that
covers what the first structurally cannot.

Cost, itemized honestly. `ubuntu-latest` provides Postgres as a first-class `services:` block, so
there is no Docker-in-Docker work. `npx playwright install --with-deps chromium webkit` is a cached
step (`~/.cache/ms-playwright`). Estimated wall clock: `npm ci` ~40s, `tsc -b` + `npm test` ~60s,
`npm run build` ~15s, `go build ./cmd/relay-server` ~45s, browser install ~30s cached / ~2min cold,
Playwright run ~90s. Call it **5-7 minutes**, running as a separate workflow concurrent with
`go-ci`, so PR latency rises by roughly zero.

**Reversibility.** Adding a workflow file is reverted by deleting it. If the lane proves flaky, the
fallback is one line (`continue-on-error: true`) - but see R1: that fallback is a last resort, not a
plan, because a non-blocking check is the decorative outcome this decision exists to avoid.

**Local parity is required, not optional.** `make test-e2e` must run the same lane on a Windows dev
machine. A CI-only harness cannot be iterated on, and the reason the browser lane has been ad-hoc for
three sessions is that standing one up costs an afternoon each time.

### D2. Seeding: bootstrap admin for identity, REST API for data, dedicated database, serialized

**Options.** (a) Bootstrap admin + REST API. (b) A test-only fixture endpoint. (c) Direct SQL against
the test database. (d) The `relay` CLI.

**Chosen: (a).**

- **(b) is rejected on threat model.** A fixture endpoint is production attack surface added for
  tests. Gating it on a build tag, an env flag or an admin check are all ways to ship a hole that is
  one misconfiguration from live, and this project has already spent a full slice bounding what an
  unauthenticated peer can do to the gRPC port. Not for test convenience.
- **(c) is rejected on the single-job-spec-pipeline invariant.** Direct SQL bypasses
  `jobspec.Validate` and `CreateJobFromSpec`, so fixtures could encode states production cannot
  produce, and the test would then assert about a page that cannot exist. It also puts a second
  schema consumer beside sqlc.
- **(d) is rejected as a third client to keep working** for no gain; the CLI has its own tests.
- **(a) creates fixtures through the exact path the SPA itself uses**, which is the property that
  makes an e2e fixture trustworthy.

**Sufficiency of the bootstrap admin: confirmed.** It creates the user with `IsAdmin: true`
(`bootstrap.go:39-44`), so `POST /v1/auth/login` yields an admin token that reaches `/admin/*` and
every seeding endpoint. (Bootstrap hashes at `bcrypt.DefaultCost`, not `internal/api`'s 12; the login
compare is correspondingly cheaper and, given the one-login-per-run design below, irrelevant either
way.)

**Dedicated database `relay_e2e`, created and dropped by the lane.** Non-negotiable, for the reason
in section 2: bootstrap is a no-op when any admin exists, so running against a dev database produces
a 401 with no diagnostic. A dedicated database also means a run never mutates a developer's dev data,
and `AdminExists` is false on every run so the bootstrap path is genuinely exercised.

**Isolation: serialize. `workers: 1`, `fullyParallel: false`.** Three independent reasons, any one of
which is sufficient:
1. One `relay-server` over one Postgres is a shared mutable store with no per-test namespace.
2. `/jobs`, `/schedules` and `/admin/*` are **unscoped global lists**, so a parallel test's fixtures
   appear in another test's table. Any count assertion or nth-row locator becomes a race.
3. C9: the login rate limit is 10/min keyed on `RemoteAddr`, and every worker is `127.0.0.1`.

This is the same reasoning as `-p 1` on `make test-integration`, and the spec states it in the config
comment so nobody "optimizes" it later without meeting the three arguments.

**Two mitigations so serialization does not become slow:**
- **One login per run.** A Playwright `globalSetup` logs in once over HTTP, seeds all fixtures, and
  saves `storageState` (which captures `localStorage`, per C8). Every spec except `auth.spec.ts`
  starts already authenticated. Login count per run: 2.
- **Raise the limits on the test instance anyway** (`RELAY_LOGIN_RATE_LIMIT=1000:1m`,
  `RELAY_REGISTER_RATE_LIMIT=1000:1m`), because `storageState` alone breaks the moment someone adds a
  second real-login spec, and a rate-limit failure presents as a mysterious 429 rather than as a
  limit. **Stated cost:** the harness therefore does not exercise the rate limiter. That is
  deliberate; the limiter has its own Go tests and is not a browser-observable behaviour.

**Fixture naming.** Every seeded resource carries a per-run unique suffix (`e2e-<runid>-<n>`), and
every assertion locates by that name - never by row index, never by a count over the whole table. A
leftover row from an aborted run must not be able to make a locator ambiguous.

### D3. Serve the production-embedded build, not the Vite dev server

**Chosen: `make web-build` -> `go build ./cmd/relay-server` -> run it, single origin.**

Three arguments, in the order that decided it:

1. **Tailwind v4 scans source statically.** A computed class string emits no CSS. The 2026-08-13
   retro records that a review lane built to a scratch `outDir` and found a comment's prose
   placeholder compiled into invalid CSS in the bundle - hard proof that the bundle and the source
   are different artifacts. **A no-op fix and a working fix are indistinguishable to every test in
   this repo**, and only a production build can tell them apart. That is the single most valuable
   question this harness can answer, and the dev server is structurally unable to ask it.
2. **`webui.Handler()`'s SPA fallback is server code with its own semantics.** It rewrites unknown
   non-`/v1` paths to `index.html` and 404s `/v1/*` (C4). Vite has its own history fallback. A
   deep-link regression in `web/embed.go` is invisible to a dev-server harness, and deep links are
   the auth spec's third test.
3. **Same origin.** Embedded serving is same-origin; the dev server is `:5173` proxying to `:8080`.
   CORS is fail-closed in this codebase and origin-sensitive behaviour differs between the two.

**Cost, stated:** an SPA rebuild (~15s) plus a Go build (~45s) per cold run. This lane is not for
inner-loop iteration; `make web-dev` remains what humans use. `reuseExistingServer` keeps repeated
local runs fast.

**The `web/dist` hazard, as corrected by F2.** The build overwrites exactly one tracked file,
`web/dist/index.html`, replacing the "not been built" placeholder with an index referencing
gitignored hashed assets. In CI this is harmless (fresh checkout, nothing committed). Locally it is a
trap, and the standing remedy has been a rule people remember. **Decision: make it a step instead.**
`make test-e2e` restores `web/dist/index.html` on exit, pass or fail. A rule that runs beats a rule
that is documented.

### D4. Chromium runs everything; WebKit runs the keyboard subset; Firefox is out

**Chosen: two projects. `chromium` (all specs) and `webkit` (specs tagged `@webkit`, which in slice 1
is `keyboard.spec.ts` only).**

The item's a11y argument turns specifically on Chromium granting implicit scroller focusability where
WebKit does not, and that divergence is **the stated reason the shipped code carries an explicit
`tabIndex={0}`**. If that argument is load-bearing - and it is, it is why a shipped fix looks the way
it does - WebKit is not optional. So it is in slice 1.

Running the whole suite on every engine is rejected: it triples wall clock and triples flake surface
for near-zero marginal information, because a React SPA's list rendering does not diverge across
engines. Pay for the engine where the argument is, not everywhere.

Firefox is excluded **with a reason rather than silently**: no open finding, retro or item in this
project turns on Gecko behaviour. Adding it later is one config entry.

**An honesty constraint that must survive into the code.** Playwright's `webkit` is a bundled WebKit
build, **not Safari**. It exercises WebKit's focusability semantics, which is what the divergence is
actually about; it does not exercise Safari's chrome, its extensions, or its platform integration.
The Known Limitation "Safari has not been opened" is therefore **narrowed, not retired**, and the
`webkit` project's config comment must say so. Claiming otherwise would be exactly the class of wrong
prose about correct code this project has now recorded for sixteen consecutive iterations.

### D5. Slice one ships the harness plus three specs, in value order

A harness with no tests in it is not worth shipping, and the item lists two different suites. They
are sequenced by what no existing gate can express:

1. **`keyboard.spec.ts` - real key events.** First, because it retires a named Known Limitation and
   because a named shipped fix currently depends on an unobserved behaviour. Tab-order walk on
   `/admin/enrollments` and `/admin/invites` proving the scroll wrapper takes focus from a real `Tab`
   press, that its accessible name is announced (`role="group"` + `aria-label`), and that arrow keys
   then scroll the clipped right-hand columns into view. Chromium **and** WebKit.
2. **`layout.spec.ts` - the narrow-viewport regression, as measurement plus screenshots.**
   `documentElement.scrollWidth <= clientWidth` at **320px and 375px**, on every shell surface, **with
   populated tables**, plus per-element `HEADER`/`MAIN` widths, plus `/auth` as the control. Every one
   of those four qualifiers is a lesson the 2026-08-13 slice paid for: the item was misdiagnosed twice
   because it measured empty tables and default views; the fourth cause was found only because `MAIN`
   was measured separately from the document; a fifth was found only because someone measured at 320
   as well as 375; and the header attribution is an attribution rather than a correlation only because
   `/auth` renders no shell and never overflowed. This spec converts those four into config, so the
   next person cannot re-learn them.
3. **`auth.spec.ts` - the original 2026-06-03 framing, kept small.** Login lands on `/jobs`; logout
   returns to `/auth` and `relay.token` is gone from `localStorage`; a deep link to `/jobs/:id` while
   logged out redirects to `/auth`. Cheap, and it is the smoke test that tells you the harness itself
   is wired before the other two are believed.

### D6. Screenshots are artifacts, not assertions

**Options.** (a) `page.screenshot()` uploaded as a build artifact. (b) `toHaveScreenshot()` pixel
baselines. (c) Both.

**Chosen: (a). This is a conservative-and-reversible call and it decided the fork.**

The item's headline complaint is "**no one has looked at this**". The remedy for that is an artifact
a human can open, not an automated pixel diff. Pixel baselines rendered on a Windows dev machine and
on `ubuntu-latest` differ on font rasterization, scrollbar metrics and subpixel layout; getting them
wrong produces either a permanently red gate or a permanently `--update-snapshots`-ed one, and both
are the decorative outcome D1 exists to prevent. Doing it properly requires pinning rendering to a
container image, which is its own slice.

So: `page.screenshot()` for every surface at every width on **every run, pass or fail**, uploaded via
`actions/upload-artifact`, plus Playwright's built-in trace and video on failure. Baselines are a
named follow-on (section 8).

**The residual is a process commitment, not a technical one, and it must be said plainly:** an
artifact nobody opens is worth nothing. The merge of this slice should include one human pass over
the screenshots - specifically over the nav's horizontal scroll and the wrapped tab bars, which are
the two design decisions taken with no hi-fi reference and which the retro flags as the reviewer's to
overrule.

### D7. `web/e2e/`, inside the existing npm project

**Options.** (a) `web/e2e/` in the existing project. (b) A separate root-level `e2e/` package.

**Chosen: (a).** One `npm ci`, one `node_modules`, one place a frontend engineer looks. The cost of
(b) is a second lockfile and a second tooling upgrade surface for a directory of at most a dozen
files.

Two consequences that are **acceptance criteria, not plan details**, because omitting either
produces a red gate that looks like the harness is broken:

- `web/vite.config.ts` gains `test.exclude` covering `e2e/**`, or `npm test` collects the Playwright
  specs and runs them in jsdom (section 2, second unlisted finding).
- `web/tsconfig.json`'s `include` gains `"e2e"`, or the harness is never type-checked - and `tsc -b`
  is the enforcement mechanism this project chose over source scanning when it made `Table`'s
  `minWidth` a required prop. If vitest's ambient globals collide with Playwright's imported
  `test`/`expect`, the fallback is a `tsconfig.e2e.json` project reference; prefer the one-line form
  first.

### D8. Postgres is the lane's dependency, not Playwright's job to start

Playwright's `webServer` starts **`relay-server` only**, with `url: http://127.0.0.1:8091/v1/health`
(C6, and see D9 for the port) and `reuseExistingServer: !process.env.CI`. Postgres comes from a
`services:` block in CI and from the `relay-postgres` container `scripts/dev.ps1` already manages
locally.

Rationale: Playwright is not a container orchestrator, and making it one produces a **third**
implementation of "bring up the relay stack" beside `scripts/dev.ps1` and the testcontainers
integration lane. Two is already one too many; the harness should consume that scaffolding, not
duplicate it.

### D9. Non-default ports

`RELAY_HTTP_ADDR=:8091`, `RELAY_GRPC_ADDR=:9091`. Reason: `make test-e2e` must not collide with a
developer's running `scripts/dev.ps1` stack on `:8080`/`:9090`, and - more importantly -
`reuseExistingServer` must not silently attach to the **dev** server and run the suite against dev
data with a dev admin. That failure would be green, wrong, and very hard to see. Cheap insurance.

`RELAY_ALLOW_AUTO_ENROLL` is deliberately left **unset**, so the test server runs the default
(safer) posture. No agent connects in slice 1 (D10).

### D10. Coverage limits are declared, not discovered

No `relay-agent` runs in slice 1, so **no worker row can exist**. Concretely:

- `/workers`, `/workers/:id` and `/admin/reservations` (whose `WorkerPicker` needs workers) are
  covered in their **empty state only**.
- No job executes, so no task reaches `running`, so `/jobs/:id/tasks/:taskId` renders an empty log
  and SSE tailing is not exercised.

This is precisely the "measurement taken on the convenient fixture" trap the 2026-08-13 retro named,
and the correct handling is to **disclose it as a coverage limit in the spec files themselves**, not
to let a future reader mistake an empty-state pass for a populated-state pass. Closing it is slice 2
(section 8).

### D11. This lands before the vite/vitest upgrade, inverting that item's stated preference

`feature-2026-06-05-upgrade-vite-vitest` (vite 5 -> 8, vitest 2 -> 4, 5 npm audit advisories, 1
critical) carries a ROADMAP note saying to do it "ideally before the new test surface grows" (F4).
Inverted here, with the argument:

- The surfaces barely overlap. Playwright has its **own** runner and its **own** browsers; it consumes
  the **built** `dist`, not vite's API. The only shared files are `package.json` and one `exclude` key
  in `vite.config.ts`.
- The dependency runs the other way. The upgrade changes the build that produces the bundle the SPA
  ships. A real-browser regression gate is exactly what makes that upgrade safe to take, and today
  there is none. Doing the upgrade first means taking it with no browser evidence at all.

Recorded so this is a decision rather than a collision.

### D12. Slice one changes zero application source

No `web/src/**`, no Go. New and modified files: `web/playwright.config.ts`, `web/e2e/**`,
`web/package.json` (devDependency + two scripts), `web/vite.config.ts` (one `exclude`),
`web/tsconfig.json` (one `include`), `Makefile` (one target), `.github/workflows/web-ci.yml`, and this
spec.

**If a spec goes red on a pre-existing defect** - and `layout.spec.ts` at 320px on populated tables
plausibly will - the policy is:

- Fix it in this slice **only** if it is a one-line class string of the kind the 2026-08-13 slice
  already shipped ten of.
- Otherwise commit the spec as `test.fixme()` **citing a filed backlog item id in the annotation**,
  so the gate stays green, the debt is named, and the test is not deleted.
- **A `test.fixme` with no item id is not acceptable** and should fail review. That is the guard
  against this mechanism becoming a way to hide findings.

Rationale: a harness that lands entangled with a product fix is two changes in one review, and
neither gets reviewed properly.

---

## 4. What slice one ships

**Harness**
- `@playwright/test` pinned exactly in `web/package.json` devDependencies; scripts `test:e2e` and
  `test:e2e:ui`.
- `web/playwright.config.ts`: `testDir: './e2e'`, `fullyParallel: false`, `workers: 1`, `retries: 0`
  (see R1), two projects (`chromium` all, `webkit` grep `@webkit`), `webServer` starting the built
  `relay-server` gated on `/v1/health`, `use.storageState` from global setup, trace and video on
  failure, screenshot artifacts always.
- `web/e2e/global-setup.ts`: create `relay_e2e`, start-and-wait handled by `webServer`, log in once,
  seed fixtures over REST, write `storageState`.
- `web/e2e/fixtures.ts`: typed REST seeding helpers (jobs, schedules, users, invites, enrollments),
  every name carrying the per-run unique suffix.
- `web/e2e/surfaces.ts`: the single enumerated surface list with, per surface, its path, whether it is
  populated or empty-state-only in this slice, and why.

**Specs**
- `web/e2e/keyboard.spec.ts` (`@webkit`) - real `Tab` and arrow presses against the two zero-focusable
  tables' scroll wrappers.
- `web/e2e/layout.spec.ts` - `docSW <= clientW` at 320 and 375 on every surface, per-element
  `HEADER`/`MAIN` widths recorded, `/auth` as control, `page.screenshot()` per surface per width.
- `web/e2e/auth.spec.ts` - login redirect, logout redirect plus token clearance, logged-out deep link.

**Wiring**
- `Makefile`: `test-e2e` (build web, build server, run, restore `web/dist/index.html` on exit).
- `.github/workflows/web-ci.yml`: `npm ci` -> `tsc -b` -> `npm test` -> `npm run build` ->
  `go build ./cmd/relay-server` -> browser install (cached) -> `npm run test:e2e`, with a
  `services: postgres:16` block and an artifact upload. Blocking on every PR.
- One-time additions of `exclude`/`include` per D7.

---

## 5. What slice one explicitly does NOT ship

Each with the reason, because an unargued omission reads later as an oversight.

| Not shipped | Why |
|---|---|
| **Pixel-diff visual baselines** (`toHaveScreenshot`) | D6. Cross-platform rasterization makes them either permanently red or permanently regenerated. Needs a pinned container image; own slice. |
| **Register / self-registration flows** | Needs `RELAY_ALLOW_SELF_REGISTER=true` (C11). Configuring the one test server for it means the harness never tests the **default** posture, which is the one production runs. Deferred to a second server project or a second slice. |
| **Anything requiring a live worker** | `/workers`, `/workers/:id`, `/admin/reservations` populated; job execution; SSE task-log tailing. Needs `relay-agent` over gRPC plus auto-enroll plus subprocess management. That is slice 2. |
| **Firefox** | D4. No finding in this project turns on Gecko. One config entry when one does. |
| **Automated a11y scanning (axe)** | Attractive and a different kind of work: it produces a finding list, not a regression gate, and it changes the dependency set. Own slice, after this one exists to host it. |
| **Mobile emulation, touch, device descriptors** | The narrow-viewport work is a viewport-width question, and 320/375 covers it. Touch is a separate product question nobody has asked. |
| **Replacing the ad-hoc Phase 4 browser lane** | The lane stays. This harness covers regressions; exploratory measurement of a *new* defect is still a human driving a browser, and the 2026-08-13 baseline pass is the model. |
| **Perforce / p4d anything** | Out of scope by an order of magnitude. |
| **Any change under `web/src/` or any Go change** | D12. |

---

## 6. Acceptance criteria

1. `make test-e2e` runs green from a clean checkout on a Windows dev machine, with prerequisites of
   Go, Node and a running Docker Postgres only, and exits non-zero if any spec fails.
2. `.github/workflows/web-ci.yml` runs on every pull request as a **blocking** check and executes, in
   order: `npm ci`, `tsc -b`, `npm test`, `npm run build`, `go build ./cmd/relay-server`, browser
   install, `npm run test:e2e`. **The existing Vitest suite runs in CI for the first time**, and the
   PR body says so, because it is the larger of the two coverage gains.
3. `keyboard.spec.ts` sends real `Tab` and arrow key presses, passes on **both** `chromium` and
   `webkit`, and its assertions concern focus and scroll position - never an attribute's presence,
   which is what the existing jsdom pin already does and what this spec exists to improve on.
4. The Known Limitation "no real `Tab` press has ever been sent" is retired. The Known Limitation
   "Safari has not been opened" is **restated precisely** (WebKit engine yes, Safari no), not claimed
   closed.
5. `layout.spec.ts` asserts `documentElement.scrollWidth <= clientWidth` at 320px and 375px on every
   surface in `surfaces.ts`, with **populated** tables wherever D10 says population is possible;
   records per-element `HEADER` and `MAIN` widths; and includes `/auth` as a control that must also
   pass.
6. Screenshots for every surface at every width are uploaded as a build artifact on every run, pass or
   fail. A human opens them once before merge and either accepts or overrules the two
   no-hi-fi-reference design decisions.
7. `npm test`'s collected file count is **unchanged from HEAD** (proving the `exclude` works), and a
   deliberately introduced type error inside `web/e2e/` fails `tsc -b` (proving the `include` works).
   Both are mutation-style proofs and both must be demonstrated, not asserted.
8. Every fixture is located by its per-run unique name. No spec asserts a count over an unscoped list
   and no spec uses an nth-row locator.
9. `git diff --stat` on the final branch shows **zero files under `web/src/`**, zero `.go` files, and
   `web/dist/index.html` unchanged.
10. Any `test.fixme` cites a filed backlog item id. Any coverage limit from D10 is written into the
    spec file that has it, not only into this document.

---

## 7. Risks

**R1. Flake, and the temptation to paper over it.** Mitigations are structural: serialized workers,
health-gated server start, unique fixture names, no count-over-list assertions, a dedicated database
per run. **`retries: 0` everywhere**, deliberately - a retry that passes hides a flake, and this
project's culture is explicitly hostile to green-for-the-wrong-reason ("diagnose a red gate; measure
'pre-existing' both ways"). The escalation for a flaky spec is to fix it or delete it, never to retry
past it and never to quietly set `continue-on-error`.

**R2. The screenshots go unread.** D6's whole value depends on a human opening the artifact. This is
the one risk with no technical mitigation. Named here so that if it happens, the follow-on is the
pinned-container visual-baseline slice rather than pretending the artifact was the point.

**R3. A third stack-boot implementation.** `scripts/dev.ps1`, the testcontainers integration lane, and
now Playwright's `webServer` all know how to bring up relay. D8 minimizes it (Playwright starts one
process and nothing else) but does not eliminate it. Watch for drift in the env-var set.

**R4. CI cost and cold browser installs.** ~5-7 min with a warm cache, up to ~9 cold. Runs concurrent
with `go-ci`, so PR latency is roughly unchanged. If it becomes a problem the first lever is dropping
`webkit` from the default trigger to a nightly, **not** dropping the browser lane.

**R5. `layout.spec.ts` goes red on merge day.** Likely, and it is the point. Policy is D12: one-line
class-string fixes in slice; anything larger becomes a `test.fixme` with a filed item id.

**R6. Raising the rate limits means the harness never exercises them.** Accepted and stated (D2). If
a future slice wants browser coverage of the 429 path it needs a second server project with the
default limits, which is why D9's port choice keeps a second instance cheap.

**R7. The vite/vitest upgrade will touch these files.** D11 sequences this first; the upgrade slice
inherits a browser gate, which is the payoff, but it must re-run `test:e2e` as part of its own
acceptance and should expect the `exclude`/`include` keys to need re-checking under vitest 4.

**R8. WebKit is not Safari.** D4. The failure mode is a future reader citing this harness as Safari
coverage. Mitigated only by the comment saying so, in the config and in the spec file.

---

## 8. Follow-on work (proposals, not filings)

The TPM lane never auto-files. These are proposed for the conductor and the human:

1. **Slice 2: an agent in the harness.** Run `relay-agent` against the test server with auto-enroll,
   so `/workers`, `/workers/:id`, `/admin/reservations`, job execution and SSE task-log tailing become
   testable. Closes D10's declared coverage limit. This is the larger half of the original item's
   value and it is deliberately not slice 1.
2. **Visual regression baselines on a pinned container image.** Converts D6's artifacts into a gate.
   Needs a fixed rendering environment; blocked on nothing except a decision to pay for it.
3. **Axe integration** on the surface list `surfaces.ts` already enumerates.
4. **Amend `idea-2026-08-23-integration-only-guards-ci-never-runs`** with F1: its Go-side complaint
   has a strictly worse frontend twin, since the web suite is not merely tag-gated but **entirely
   absent from CI**, and this slice closes that half.
5. **A note in `README.md` or `CLAUDE.md`** on how to run the lane, once it exists.

---

## 9. Open questions for the human

Flagged because they are judgment calls a human may reverse, and each is cheap to reverse:

1. **D6** - artifacts rather than pixel baselines. If you want baselines now, that is a container-image
   decision and it belongs in the plan, not bolted on later.
2. **D4** - WebKit in slice 1. It costs ~1-2 min of CI. If that is too much, the keyboard spec runs
   Chromium-only and the a11y argument the item makes stays unverified; say which you prefer.
3. **D11** - taking this before the vite/vitest upgrade, against that item's stated preference.
4. **The two no-hi-fi-reference design decisions** from 2026-08-13 (the nav scrolls; its scrollbar is
   visible). This harness will produce the first images of both. They are yours to overrule, and both
   revert by deleting class strings.
