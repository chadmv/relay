---
date: 2026-08-24
topic: web-e2e-harness
slice: idea-2026-06-03-web-e2e-harness, slice 1 of 2 (the item ROADMAP "Now" had passed over for six consecutive backend iterations; filed four months ago)
branch: claude/pr-merge-session-d3977d
range: origin/main..HEAD (web tooling and CI only; zero application source - no file under web/src/, no .go file, no .sql, no proto, no generated file; green, not yet merged)
pr: web-e2e-harness - not yet opened; reference this work by date and slug, never by a predicted number
closes: idea-2026-06-03-web-e2e-harness (RECOMMENDED - see "Close or not" below; conditional on filing slice 2 first)
filed-this-slice: bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports
---

# Session Retro: 2026-08-24 - the slice set out to decide whether e2e should run in CI and found there was no web CI at all, then hit the same instrument mismatch four times

**TL;DR:** `web/e2e/` ships a Playwright harness driving the **production-embedded** SPA
(`make web-build` -> `//go:embed` -> `relay-server` -> `webui.Handler()`) against a real Postgres on a
dedicated `relay_e2e` database, seeded over the REST API as the bootstrap admin. Three specs: real
`Tab` and arrow key events on the two zero-focusable tables' scroll wrappers (Chromium **and**
WebKit), narrow-viewport width measurement with full-page screenshots as artifacts, and the original
auth smoke. 51 tests. And `.github/workflows/web-ci.yml`, **the repository's first web CI of any
kind**, which carries `tsc -b`, `npm test` and `npm run build` as well as the browser lane. Zero
application source changed. The harness found a real defect on its first CI run.

This slice's durable rule is not about the harness at all:

> **Match the instrument to the claim.** A name grep answers "is this identifier written down"; it
> never answers "is this behaviour covered". A jsdom assertion answers "is this attribute in the DOM";
> it never answers "can a keyboard reach it". A class-string pin answers "does the component emit this
> text"; it never answers "did a CSS rule reach the bundle". Every one of those pairs looks like the
> same question and the second half is the one anybody actually cares about. **The tell is a
> conclusion one category broader than the evidence**, and it appeared four times in this one slice -
> once fatally enough that the engineer stopped mid-slice and refused to proceed, which was correct.

---

## The headline: an `rg` for a component name was used as a coverage check, and the engineer stopped

The plan's mutation matrix row M3 rested on one line:

> `rg 'PublicOnlyRoute' web/src --glob '*.test.tsx'` returns nothing; `web/src/app/` has
> `ProtectedRoute.test.tsx` and `AdminRoute.test.tsx` and no third file. So the post-login landing
> route at `web/src/app/PublicOnlyRoute.tsx:9` can be changed to any path and the entire 1100-test
> suite stays green.

**The grep is true and the conclusion is false.** `web/src/App.test.tsx:19-38` ("a successful login
lands the user on the jobs page") drives the real `<App/>` tree through that exact redirect and
asserts on the OVERVIEW eyebrow unique to `JobsPage.tsx:77`. It never names `PublicOnlyRoute`,
because a behavioural test of a route has no reason to. Mutating `to="/jobs"` to `to="/schedules"`
turns `npm test` **RED** (2 failed, 1 passed), not green.

The engineer ran the mutation, got the opposite of the documented expectation, and **halted rather
than shipping around it**. That is the single best process outcome in this slice. The plan's step
said "expected: PASS"; a lane optimizing for a green checkbox would have written that discrepancy off
as an environment quirk. Instead M3 was replaced with a genuinely browser-only mutation - deleting
`s.StaticHandler = d.static` from `cmd/relay-server/http_server.go:155`, the one line wiring
`webui.Handler()`'s SPA fallback into `api.Server`'s mux. Both halves of that seam have unit coverage
in isolation (`web/embed_test.go`'s `TestHandler_ServesIndexForUnknownRoute`,
`internal/api/static_test.go`'s `TestServer_StaticHandlerServesNonAPIPaths` against a *synthetic*
static handler) and **nothing exercises the wiring between them**. Deleting the assignment leaves
`npm test`, `tsc -b` and `go test ./...` all green while every test in `auth.spec.ts` goes red.

Note what makes the replacement better and not merely different: the original mutation's browser
column was still accurate. The browser lane really would have caught M3. What was wrong was the
*claim of exclusivity* - "and nothing else catches it" - and that is the claim the slice was being
sold on.

**This is the same shape CLAUDE.md already names for uniqueness claims** ("a uniqueness claim is a
claim about the complement - it cannot be checked by opening its subject, only by searching for the
shape"), arriving from the other direction: here somebody *did* search, and searched for the wrong
shape. The rule generalizes to "the instrument must be able to be wrong in the direction that
matters", and a name grep cannot be wrong in the direction of a behavioural test that does not name
its subject.

---

## The spec refuted the premise of its own central fork

The brief handed this lane one question: should the browser suite run in CI, given
`idea-2026-08-23-integration-only-guards-ci-never-runs` argues that a guard behind a gate CI never
runs is decorative? The spec's verification pass answered a different question, because
`.github/workflows/` contains exactly `go-ci.yml`, `python.yml` and `release.yml`, and a
case-insensitive grep for `npm`, `node` or `web` across all three returns **zero matches**.

**`npm test`, `tsc -b` and `npm run build` had never run in CI.** 1116 frontend tests across 152
files, twelve merged frontend slices, and every one of them advisory. The fork was not "should e2e be
in CI" but "the web lane has no CI at all", and adding a Playwright-only job while the unit suite
stayed unenforced would have been backwards.

So the slice ships the whole web gate with the browser tier on top, and the larger of its two
coverage gains is the boring one. The PR body has to say so or reviewers will read this as an
e2e-only change.

Worth recording as a pattern rather than an anecdote: **the spec's job was to decide a fork, and it
dissolved the fork instead.** Twenty-second consecutive iteration in which planning-phase
verification changed something material before code was written.

---

## The plan then refuted six of the spec's claims, including that its central design cannot work

The spec is an input, not a fact, and the plan treated it that way:

- **R-A, the important one.** The spec's `globalSetup` design - "create `relay_e2e`, start-and-wait
  handled by `webServer`" - is **impossible as written**, and not because of plugin ordering.
  `relay-server` runs `store.Migrate` at `cmd/relay-server/main.go:51` and `log.Fatalf`s on failure,
  long before `srv.ListenAndServe()` at `:290`. If the database does not exist when `webServer`
  launches, the process dies, `/v1/health` never listens, and Playwright fails with a 120s timeout
  and no useful diagnostic. Creating the database from inside Playwright is *always* too late,
  whatever the ordering happens to be. Database provisioning moved out into `e2e/ensure-db.mjs`, run
  by the npm script before Playwright starts at all.
- **R-B.** `reuseExistingServer: !process.env.CI` is unsafe once the database is recreated per run: a
  reused server holds a pool against a database that no longer exists, and if the reused listener is
  a **developer's** stack, the suite runs green against dev data with a dev admin. Now `false`,
  unconditionally.
- **R-D.** "Add `"e2e"` to `include`, the one-line form" is three changes minimum.
  `tsconfig.json:17` pins `"types"`, which *disables* automatic `@types` inclusion; `@types/node` was
  not a dependency; and `playwright.config.ts` itself was outside every project too, which the spec
  had not noticed.
- **R-C, R-E, R-F.** The reservations table is populated in slice 1 after all (a selector-only
  reservation is a valid 201); the spec's file list omitted `web/.gitignore` and two devDependencies;
  and the login rate limit was *measured* rather than assumed - exactly three logins per run, under
  the `10:1m` default, so the raise is insurance and the comment says so.

---

## The same instrument mismatch recurred three more times inside the slice, twice self-inflicted

This is why the headline is a rule and not a story about one grep.

1. **The replacement M2 was defeated by the test file's own comment.** The mutation is meant to prove
   that a *computed* Tailwind class emits no CSS while the rendered `class` attribute stays
   byte-identical. It kept failing to reproduce. Cause: `@tailwindcss/vite` builds its Scanner over
   the Vite root with `{base: viteRoot, pattern: '**/*'}` and reads **source files on disk** - every
   file, comments included, not just what a JS bundle imports. `keyboard.spec.ts`'s own explanatory
   prose spelled the real utility class, which kept that CSS rule alive no matter what the component
   did. **The test's documentation was load-bearing input to the thing it documented.** Confirmed by
   re-running with the class text absent from the comment: RED, 6 tests, the same failure set as the
   shipped form.
2. **The same mechanism shipped invalid CSS to production.** A class-*shaped* placeholder written in
   prose (`.min-w-[...]`) is not a real utility, so Tailwind emitted its own bogus rule into the
   production bundle. Confirmed by an A/B build. Fixed by describing classes in prose instead of
   spelling them, and the rule is now written into `web/e2e/README.md`.
3. **`readSeed()` at module scope passed locally for the wrong reason.** Playwright collects every
   spec file across every project - including the ones that `dependencies: ['setup']` on - *before*
   it runs any test in any project, so a module-scope read of `seed.json` runs before the setup
   project has written it. Every local run was green because a **stale `seed.json` from a previous
   run persisted** in `e2e/.run/`. CI's clean checkout caught the ENOENT on the first try. The remedy
   is structural: `surfaces.ts` keeps the *structure* seed-independent (surface names are hardcoded)
   and makes `path`, `ready` and `marker` functions of `Seed`, called from inside test bodies.

Items 1 and 2 are the same fact biting in opposite directions, and both are silent. A real class name
in prose keeps a dead rule alive, so deleting the last real usage is not caught by anything. A fake
class-shaped string generates a rule that should not exist. Neither is visible to `npm test`,
`tsc -b`, or any human reading a diff.

**Item 3 is the "green for the wrong reason" family, and the mechanism is worth naming**: a run
directory that persists between runs makes every ordering bug in it invisible. `web/e2e/README.md`
now says to delete `e2e/.run/` before trusting a green local run of a change in that directory.

---

## Review found the engineer's explanation of its own bug was wrong, in the same direction as the bug

The first fix for the M2 failure carried a comment saying esbuild constant-folded the interpolated
template back into a literal string, so the mutation never reached the bundle.

The observation about esbuild is true. The conclusion is exactly backwards. **Tailwind's Scanner
never reads the emitted bundle**; it reads source files on disk. Whether esbuild folds anything is
irrelevant to whether the CSS rule survives. So the comment offered a mechanism that cannot produce
the effect, in a file whose entire purpose is to be the one place in the repo that understands this
distinction, and it would have taught the next reader the opposite of the truth.

**Seventeenth consecutive iteration for "wrong prose about correct code".** The variant here is worse
than the usual: the prose was not stale, it was freshly written by the person who had just measured
the phenomenon, and it explained the measurement with a mechanism from the wrong layer. The corrected
comment in `keyboard.spec.ts:28-48` now states both facts and which one is load-bearing.

---

## Two demonstrated coverage misses in the harness's own gate

The harness was reviewed as a product, which is the right treatment for a thing whose whole value is
that it catches what other gates cannot.

**1. `scrollWidth <= clientWidth` cannot distinguish "fits" from "clipped".** Replacing `AdminTabs`'
`flex-wrap` with `max-w-full overflow-x-auto` puts five pills into an unlabelled horizontal scroller
with no keyboard affordance, and **all 38 tests passed**. The document does not overflow; the content
overflows into its own wrapper. This is a real hole in the new gate, not a hypothetical, and it is
the same shape as the defect the harness found on its first run (below). Recorded in
`web/e2e/README.md` and recommended as an item.

**2. Nothing above 375px was measured.** `WIDTHS` was `[320, 375]`, so every `md:` and `lg:` rule in
the app - `ServerTab.tsx`'s `md:grid-cols-2` is a real one - was unevaluated, and a hostile
`md:min-w-[2000px]` would have passed. 1280 was added, restoring a check the 2026-08-13 slice itself
ran by hand and never wrote down. The suite went 38 tests to 51.

Both misses share a root: **a narrow harness is easy to mistake for a broad one**, because the tests
are named after surfaces rather than after the properties they check. Thirteen surfaces sounds like
coverage of thirteen pages. It is coverage of one property on thirteen pages.

---

## The harness found a real defect on its first CI run, and somebody looked at it

`bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports`. At 375px the header renders
`RELAY | Jobs | Workers | S…` and then the user chip; at 320px "Workers" is cut mid-word. The header
is a horizontal scroll container so the items are technically reachable, and nothing on screen says
so. A user on a phone sees an app with two pages.

Three things make this the slice's justification rather than a bonus:

- **Every number is correct.** `documentElement.scrollWidth <= clientWidth` holds. The 2026-08-13
  slice's assertion was right; what it could not express is what the result looks like. This is
  coverage-miss 1 above, occurring in production rather than in a mutation.
- **The screenshots were downloaded and opened.** This is the first time anyone has seen this SPA
  rendered at 320 or 375, across three sessions that all measured with `getBoundingClientRect` and
  `scrollWidth` instead of looking. D6's whole design (artifacts, not pixel baselines) rests on a
  process commitment that a human opens them, and R2 named it as the one risk with no technical
  mitigation. The commitment was honored on the first run.
- **The item does not prescribe the fix.** It lists three options (scroll affordance, collapsed menu,
  prioritized truncation), says the design question is genuinely open because the hi-fi is silent on
  narrow viewports, and requires the acceptance criterion to be visual as well as numeric now that it
  can be. That is the right shape for a defect found by looking at something for the first time.

---

## Close or not: recommend CLOSE, conditional on filing slice 2 first

**Recommendation: close `idea-2026-06-03-web-e2e-harness`, and file slice 2 (agent-in-harness) as its
own item in the same commit.** This contradicts the plan, which says twice not to close it. The plan
made that call at plan time; it is worth re-deciding against the shipped result, and the plan's own
supporting claims about the item were superseded twice during the slice.

**Why close:**

1. **The item's dominant argument is its 2026-08-14 amendment, and both of its two named gaps are
   fully delivered.** The amendment is explicit that the original 2026-06-03 framing is stale, names
   exactly two capability gaps measured across three sessions (no screenshot, no real key events),
   and says "Both gaps are exactly what Playwright provides as table stakes: `page.screenshot()`,
   real `page.keyboard.press('Tab')`, and multiple browser engines including WebKit". All three
   shipped. The amendment also says, in as many words, that **no separate item was filed for the
   limitation because "this item is its remedy"**. The remedy exists and has already produced a
   finding.
2. **The verb is "stand up a harness".** It is stood up, it runs in CI, it is documented, and it is
   reproducible on a Windows dev host. Holding the file open for register flows and worker pages
   re-opens the four-month-old framing the item's own amendment calls stale.
3. **The remainder is not "the rest of this item", it is three separable questions** already
   enumerated in the spec's section 8: an agent in the harness, visual baselines on a pinned image,
   axe. Keeping one file open as an umbrella for three is what makes `/backlog list` unreadable, and
   this project's convention is one item per question.

**The honest counter-argument, and its mitigation:** nine open items cross-reference
`[[idea-2026-06-03-web-e2e-harness]]` as the thing that would make them verifiable
(`native-dialog-element-reconsideration`, `table-minwidth-magnitude-is-unchecked`,
`table-visual-harmonization`, `sign-out-in-one-tab`, `web-suite-waitfor-flakiness`,
`cli-tests-never-hit-real-server`, `integration-only-guards-ci-never-runs`,
`document-z-index-layering-scale`, plus the new header-nav bug). Closing it makes nine wikilinks
point into `docs/backlog/closed/`. That is acceptable **only if the resolution note routes those
readers somewhere live**, which is the first requirement below.

### What the resolution note must record

- **Point at `web/e2e/README.md` as the live document**, not just at this retro. Nine open items lean
  on this file; the reader arriving from one of them needs the harness's current capabilities and
  limits, which are maintained there and not here.
- **No `relay-agent` runs, so no worker row can exist.** `/workers` is covered in its empty state
  only; `/workers/:id` is never visited at all (the stronger gap, and the page the 2026-08-13 retro
  flagged as having under 15px of headroom); no job executes; no task reaches `running`; SSE
  task-log tailing is not exercised; `/admin/reservations` is populated but its create form's
  `WorkerPicker` is empty-state. **This is slice 2 and it must be filed before the close.**
- **Register and self-registration flows are NOT covered**, and they are named in the item's own
  2026-06-03 proposal. Deliberately out: `RELAY_ALLOW_SELF_REGISTER=true` on the one test server
  would mean the harness never runs the **default** posture, which is the one production runs.
  Covering them needs a second server project.
- **Four surfaces reachable today are absent from `surfaces.ts`** - `/jobs/:id/tasks/:taskId`,
  `/profile/password`, `/profile/sessions` (all three need no agent and no config change), and
  `/register`.
- **Screenshots are artifacts, not assertions.** No pixel baselines, so a visual regression is caught
  only if a human opens the artifact. That is a process commitment, not a gate.
- **Playwright's `webkit` is a bundled WebKit build, not Safari.** The Known Limitation "Safari has
  not been opened" is **narrowed, not retired**. Do not let a future reader cite this harness as
  Safari coverage.
- **The rate limiter is not exercised** (`RELAY_LOGIN_RATE_LIMIT=1000:1m` on the test server), and
  Firefox and axe are out with reasons in the spec's section 5.
- **`scrollWidth <= clientWidth` cannot distinguish "fits" from "clipped behind an inner scroller"** -
  demonstrated, and both an open bug and a recommended item.

---

## Process shape

- Full brainstorming flow, executed unattended. The spec's section 0 names what that costs: the
  visual-companion offer, one-question-at-a-time clarification, per-section approval and the
  written-spec review gate had no human to answer them, so each fork was self-decided **with the
  reasoning logged**, and the rule applied where judgment did not clearly resolve was "most
  conservative and most reversible". D6 and D12 were decided that way. Three decisions are flagged in
  section 9 as the reviewer's to overrule.
- Single lane, `relay-frontend-engineer`, thirteen sequential tasks. No Phase 3 parallelism, because
  the slice is a bootstrapping chain (deps -> collection -> types -> database -> server -> session ->
  specs -> wiring -> CI) with no independent task in it.
- **The engineer halted mid-slice** on the M3 discrepancy rather than proceeding. Then the same shape
  recurred three more times, two of them self-inflicted inside the fixes.
- The plan carries dated **SUPERSEDED** annotations rather than edits, at five sites (the
  `PublicOnlyRoute` hazard note, Task 8 Step 3, Task 10 Step 3, mutation-matrix rows M2 and M3, and
  follow-on 6). That is the right treatment: a reader who remembers the original claim needs to see
  it refuted, not vanished.
- Gates: `npm test` 152 files / 1116 tests unchanged from baseline (proving `test.exclude` spreads
  `configDefaults` rather than replacing them); `tsc -b` clean; `make test-e2e` 51 passed in ~23s
  local, ~36s in CI; zero flakes across 5 clean runs plus load and parallelism stress; both CI jobs
  green.

---

## What Was Built

- **`web/e2e/ensure-db.mjs`** - drops and recreates `relay_e2e` and writes `e2e/.run/env.json`, the
  single source of truth for the run's DSN, addresses, admin credentials and run id. Runs outside
  Playwright, before it starts, because `relay-server` `log.Fatalf`s on migrate before it listens.
  Refuses to drop `relay`, `postgres` or `template1`, and requires a simple identifier - which is
  also what makes the quoted `DROP` safe. Plain `.mjs` so it needs no transform and `pg` needs no
  types.
- **`web/playwright.config.ts`** - three projects (`setup`, `chromium`, `webkit` grepping `@webkit`),
  `fullyParallel: false`, `workers: 1`, `retries: 0`, `webServer` starting the prebuilt binary gated
  on `/v1/health`, `reuseExistingServer: false`. Two comments carry measurements rather than
  assertions: serialization's three reasons are labelled **precautionary rather than load-bearing at
  HEAD** (`workers: 4` passed clean across four full runs), and the env block pins
  `RELAY_ALLOW_AUTO_ENROLL`, `RELAY_ALLOW_SELF_REGISTER` and `RELAY_CORS_ORIGINS` **explicitly to the
  safe values rather than leaving them unset**, because Playwright merges `process.env` into
  `webServer.env` and a developer's exported shell variable would otherwise silently flip the test
  server to a permissive posture while the config and README kept claiming the default.
- **`web/e2e/global.setup.ts`** - a setup *project*, not `globalSetup`, so it provably runs after
  `webServer` is healthy and a seeding failure reports as one named red test. Logs in once over HTTP,
  seeds, writes `storageState` **by hand** (the SPA's only credential is
  `localStorage['relay.token']`, so no browser round trip is needed), and carries a named message on
  the 401 that is this lane's most likely single failure.
- **`web/e2e/fixtures.ts`** - REST seeding as the bootstrap admin, never direct SQL, because direct
  SQL bypasses `jobspec.Validate` and `CreateJobFromSpec` and could encode states production cannot
  produce. Every resource name carries a per-run id.
- **`web/e2e/surfaces.ts`** - 13 surfaces, each with `path(seed)`, `ready(page, seed)`, a declared
  `population` flag and an `anonymous` flag for the `/auth` control. Four readiness predicates were
  strengthened during review after being *measured* to certify nothing: the `/workers` `h1` resolved
  in 35ms before `GET /v1/workers` returned (an any-state gate on a page whose whole claim is the
  empty state); `/admin/server`'s `aria-current="page"` resolved in 53ms under a 2500ms API delay and
  **still resolved with every API forced to 500**; `/profile`'s `main h1` matches on every surface;
  and two locators were ambiguous under strict mode.
- **`web/e2e/auth.spec.ts`** - login lands on `/jobs`, logged-out deep link redirects, logout clears
  `relay.token` (asserted as absence from the real store). Runs anonymous file-wide, because
  `DELETE /v1/auth/token` destroys the caller's own token and the suite is serialized, so logging out
  with the shared token would silently unauthenticate every later spec.
- **`web/e2e/layout.spec.ts`** - 13 surfaces x 3 widths, `document`/`header`/`main` measured
  separately, per-surface JSON attachments and a full-page PNG on every run pass or fail.
- **`web/e2e/keyboard.spec.ts`** (`@webkit`) - real `Tab` and `ArrowRight` presses at a 480px
  viewport. `assertScrollable` asserts overflow **> 100, not > 0**, and the number is measured: with
  the min-width rule missing, fixed-pixel columns and cell padding alone still produce 51px
  (enrollments) and 32px (invites), which `> 0` cannot tell apart from a working rule; with the rule
  applied the same surfaces measure 222px and 302px. It is also ordered **before** the row-marker
  wait, because a missing rule collapses the column the marker lives in and both tests used to die on
  a generic hidden-element timeout that never mentions min-width.
- **`.github/workflows/web-ci.yml`** - `npm ci` -> `tsc -b` -> `npm test` -> `npm run build` ->
  `go build ./cmd/relay-server` (after the SPA build, because the embed is a compile-time snapshot) ->
  cached browser install -> `npm run test:e2e` -> artifact upload with `if: always()`. Blocking on
  every PR. The browser install calls `./node_modules/.bin/playwright` rather than `npx playwright`,
  with the reason written down: `npx` silently falls back to fetching from the registry, which would
  run an unpinned `apt-get install` as root if this step were ever reordered or made conditional.
- **`Makefile`** - `test-e2e`, plus `RELAY_SERVER_BIN` with the `.exe` suffix on Windows, which
  `playwright.config.ts` must agree with. Restores the tracked `web/dist/index.html` placeholder on
  exit **pass or fail**, propagating the exit code: a rule that runs beats a rule that is documented.
- **`web/e2e/README.md`** - how to run it, what it does not cover, and five house rules. Includes a
  measured Windows section (no `make` on PATH; the MSYS2 copy does not forward `OS`, `TEMP` or the Go
  environment to recipe subshells, so `RELAY_SERVER_BIN` silently takes the non-Windows branch).
- **Config edits:** `vite.config.ts`'s `test.exclude` spreading `configDefaults.exclude` (setting
  `exclude` at all replaces vitest's defaults wholesale, and dropping `**/node_modules/**` makes
  vitest walk `web/node_modules` collecting every `*.spec.js` in it); `tsconfig.json` gaining `"node"`
  in `types` and `"e2e"` plus `"playwright.config.ts"` in `include`; `web/.gitignore` gaining four
  lines including `e2e/.run/`.

## Key Decisions

- **The production-embedded build, not the Vite dev server.** Three arguments in the order that
  decided it: Tailwind v4 scans source statically so a computed class emits no CSS and only a
  production bundle can tell a no-op fix from a working one; `webui.Handler()`'s SPA fallback is
  server code with its own semantics that a dev-server harness cannot reach; and embedded serving is
  same-origin where the dev server is `:5173` proxying `:8080`. The slice's own M2 and its
  replacement M3 both landed inside argument 1 and argument 2 respectively, which is unusually direct
  confirmation of a design decision by the work that followed it.
- **A dedicated `relay_e2e` database, non-negotiable.** `bootstrapAdmin` returns before it looks at
  the email when **any** admin exists, so pointing the harness at a developer's `relay` database
  leaves `RELAY_BOOTSTRAP_PASSWORD` unconsumed and every login returns 401 with no diagnostic
  anywhere. Enforced by a refusal in `ensure-db.mjs`, not by a note in a doc.
- **No test-only fixture endpoint**, rejected on threat model: a fixture endpoint is production
  attack surface added for tests, and gating it on a tag, a flag or an admin check are all ways to
  ship a hole one misconfiguration from live. This project has already spent a full slice bounding
  what an unauthenticated peer can do to the gRPC port.
- **Screenshots as artifacts, not pixel baselines.** Cross-platform rasterization makes baselines
  either permanently red or permanently regenerated, and both are the decorative outcome the CI
  decision exists to prevent. Doing it properly needs a pinned container image and is its own slice.
- **WebKit in slice 1, Firefox out with a reason.** The a11y argument the item makes turns
  specifically on Chromium granting implicit scroller focusability where WebKit does not, and that is
  why the shipped `Table.tsx` carries an explicit `tabIndex={0}`. Pay for the engine where the
  argument is. No finding in this project turns on Gecko.
- **Zero application source, enforced as an acceptance criterion.** A harness that lands entangled
  with a product fix is two changes in one review and neither gets reviewed properly. The header-nav
  defect the harness found was **filed, not fixed**, which is that policy working as intended.
- **`retries: 0`.** A retry that passes hides a flake. The escalation is fix or delete, never retry
  and never `continue-on-error`.
- **This lands before the vite/vitest upgrade**, inverting that item's stated preference, because the
  upgrade changes the build that produces the shipped bundle and a real-browser gate is what makes it
  safe to take.

## Findings Triage

- **1 HIGH, self-inflicted and caught by the engineer: a name grep used as a coverage check.** M3's
  premise was false; `App.test.tsx` already drives that redirect. The slice halted and replaced the
  mutation with a genuinely browser-only one. The mutation matrix's conclusion survives for M1 and
  M2 and no longer holds for M3, and the plan says so in place.
- **1 HIGH, coverage: nothing above 375px was measured.** Every `md:`/`lg:` rule was unevaluated;
  1280 added; suite 38 -> 51.
- **1 HIGH, coverage: the overflow gate cannot see an inner scroller.** Demonstrated by mutation (all
  38 tests green), and the same shape as the defect found in production. Documented; recommended as
  an item.
- **1 MEDIUM, shipped to production: an invalid `.min-w-[...]` CSS rule** generated from a
  class-shaped placeholder in a comment, because Tailwind scans prose.
- **1 MEDIUM: the engineer's explanation of its own bug was wrong**, citing esbuild constant-folding
  for an effect that depends only on the source scan. Seventeenth consecutive iteration for the
  wrong-prose class.
- **1 MEDIUM, green for the wrong reason: `readSeed()` at module scope**, masked locally by a stale
  `seed.json` and caught by CI's clean checkout on the first run.
- **4 readiness predicates measured to certify nothing** and replaced, two of them proven inert
  against forced API failures.
- **6 spec claims refuted by the plan**, one of them ("`globalSetup` creates the database") a design
  that cannot work at all.
- **1 real defect found by the harness on its first CI run**, filed rather than fixed.

## What Remains Open

- **[[bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports]]** - filed this slice, medium. The
  design question is genuinely open; the hi-fi is silent on narrow viewports.
- **Slice 2, an agent in the harness.** The larger half of the item's value and the whole of the
  declared coverage limit. **Recommended as an item below, and it must be filed before the parent
  item closes.**
- **The overflow gate's inner-scroller blind spot.** Recommended as an item below. It is *not* a
  duplicate of the header-nav bug: that bug is one instance and its acceptance criterion is
  nav-specific, while this is a property of the gate that applies to every scrollable container.
- **Four surfaces reachable today are absent from `surfaces.ts`.** Recommended as an item below.
- **The web CI workflow's actions are the last Node-20 majors**, while `go-ci.yml` is already on
  Node-24 actions. Recommended as an item below.
- **`web-ci.yml` pins `node-version: 22` while `web/package.json` carries `@types/node ^26.3.0`.**
  `tsc -b` therefore type-checks against an API surface several majors ahead of the runtime CI
  executes. Folded into the same item.
- **Nobody has run this on a second machine.** The Windows path is documented from one host's
  measurements, including a `make` that is not on PATH and an MSYS2 copy with environment-forwarding
  quirks. Not proposed as an item - the README records it and the next person to run it locally is
  the test.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **twenty-second
  iteration**. The spec dissolved its own central fork; the plan refuted six spec claims.
- **A backlog proposal is not a contract** - honored. The item's proposal named register flows and
  "or `make web-dev`"; both were declined with reasons rather than followed.
- **Each stage treats the previous stage's output as untrusted** - honored in all three directions,
  and this is the slice where it mattered most: **the engineer refuted the plan mid-execution and
  stopped**, which is the strongest form this goal has taken in this batch.
- **Plan-supplied tests are untrusted** - honored, and extended to plan-supplied *mutations*. Two of
  the three headline mutation rows were wrong; both were caught by running them.
- **A mutation proof must leave a test behind** - honored: `assertScrollable`'s `> 100` threshold,
  the 1280 width, and the `anonymous` flag all survive as permanent artifacts of measurements taken
  during review.
- **A test can be green because of the bug** - honored twice: the stale `seed.json`, and four
  readiness predicates that passed on any state.
- **Wrong prose about correct code is the dominant defect class** - **seventeenth consecutive
  iteration**, with a new variant (freshly written, correct observation, wrong layer).
- **State a coverage limit rather than implying it** - honored heavily. `surfaces.ts` carries a
  per-surface `population` field, `web/e2e/README.md` has a "what it does not cover" section naming
  seven distinct limits, and the WebKit-is-not-Safari narrowing is written in three places.
- **Say "declined, and here is the price"** - honored at five sites (pixel baselines, register flows,
  Firefox, axe, the rate limiter).
- **Backlog housekeeping is required scope** - the close and the `git mv` belong to the conductor,
  via `/backlog close`, never a hand-edited `status:`.

New from this iteration:

- **Match the instrument to the claim.** The durable rule at the top. A name grep, a jsdom attribute
  assertion and a class-string pin each answer a strictly narrower question than the one being
  argued, and the tell is a conclusion one category broader than the evidence. **Candidate for
  durable memory.**
- **Prose in a scanned tree is compiled input.** Tailwind v4's whole-project scan means a comment can
  keep a CSS rule alive, or emit a bogus one. This is the first time in this project that
  documentation has been *load-bearing input* to the artifact it documents. **Recommended as a
  CLAUDE.md amendment below.**
- **A run directory that persists between runs hides every ordering bug in it.** Delete the scratch
  state before trusting a green local run; a fresh CI checkout is the only environment that tells the
  truth by default.
- **Name tests after the property, not the surface.** Thirteen surface-named tests read as coverage
  of thirteen pages and are coverage of one property on thirteen pages. Both coverage misses in this
  slice were invisible for that reason.
- **An unattended brainstorming run should record the forks it decided rather than pretending they
  were asked.** The spec's section 0 and section 9 are a good template: what the human gate would
  have asked, what was decided, on what rule, and which decisions the reviewer should expect to
  overrule.

## Files Most Touched

- `web/e2e/keyboard.spec.ts:28-48` - the Tailwind whole-project-scan explanation, the corrected
  esbuild claim, and why the mutation kept failing to reproduce. This is where the next person to
  write a Tailwind-related test will land.
- `web/e2e/keyboard.spec.ts:61-92` - `assertScrollable`: why the threshold is 100 and not 0, with
  both measured pairs, and why the call is ordered before the row-marker wait.
- `web/e2e/surfaces.ts:6-37` - the collection-versus-execution rule that made `path` and `ready`
  functions of `Seed`, with the stale-`seed.json` measurement.
- `web/e2e/surfaces.ts:73-86`, `:146-167`, `:168-182` - the three readiness predicates that were
  measured to certify nothing, each with the timing that proved it.
- `web/playwright.config.ts:40-62` - serialization argued as precautionary rather than load-bearing,
  with `workers: 4` measured green.
- `web/playwright.config.ts:155-167` - the `process.env` merge order, and why the safe posture is
  pinned rather than left unset.
- `web/e2e/README.md:100-150` - the inner-scroller blind spot and the prose-is-compiled-input rule.
  The two most transferable things in the slice.
- `.github/workflows/web-ci.yml:85-99` - the `npx` fallback argument on the browser install step.
- `docs/superpowers/plans/2026-08-24-web-e2e-harness.md` - the six refutations, and five dated
  SUPERSEDED annotations left in place rather than edited away.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No
  `git log`, no `git diff`, no test run. Every claim below that could be checked by reading was
  checked against the worktree.
- **Verified by reading:** the spec in full; the plan in full (1969 lines, including all five
  SUPERSEDED annotations and the mutation matrix); `web/e2e/keyboard.spec.ts`,
  `web/e2e/layout.spec.ts`, `web/e2e/auth.spec.ts`, `web/e2e/surfaces.ts`, `web/e2e/README.md` and
  `web/playwright.config.ts` in full; `.github/workflows/web-ci.yml` and `.github/workflows/go-ci.yml`
  in full; `Makefile` in full; `web/package.json`, `web/.gitignore`, `web/src/admin/AdminTabs.tsx`;
  the item and its 2026-08-14 amendment in full; the newly filed header-nav bug in full;
  `idea-2026-08-14-table-minwidth-magnitude-is-unchecked` in full; and the ROADMAP entries for this
  item and for the vite/vitest upgrade.
- **Confirmed against files, not inferred:** that the suite is **51 tests** (13 surfaces x 3 widths =
  39 layout, plus 3 auth, plus 4 keyboard on chromium, plus 1 setup, plus the same 4 keyboard tests
  on webkit) and that the "38 tests stayed green" figure for the AdminTabs mutation is the same
  arithmetic at the earlier two-width configuration, so the two numbers are consistent rather than
  contradictory; that `WIDTHS = [320, 375, 1280]`; that `surfaces()` returns 13 entries and takes no
  `Seed` parameter; that `assertScrollable` asserts `> 100`; that `AdminTabs.tsx:17` still carries
  `flex flex-wrap`, so the coverage-miss mutation was reverted; that no literal `min-w-[NNNpx]`
  utility remains anywhere under `web/e2e/` (the one surviving occurrence is inside a template-literal
  example in a comment, `` `min-w-[${660}px]` ``, which is not a class-shaped literal); that
  `web/.gitignore` covers `e2e/.run/`, `test-results/`, `playwright-report/` and `blob-report/`; that
  `web-ci.yml` uses `setup-node@v4`, `actions/cache@v4` and `upload-artifact@v4` while `go-ci.yml`
  uses `checkout@v5` and `setup-go@v6`; that `web-ci.yml` pins `node-version: 22` while
  `package.json` carries `@types/node ^26.3.0`; that `@playwright/test` is pinned exactly to
  `1.62.1`; that the Makefile's `test-e2e` restores `web/dist/index.html` and propagates `rc`; and
  that nine open backlog items cross-reference `[[idea-2026-06-03-web-e2e-harness]]`.
- **Reported by the implementing and verifying lanes, not re-run here:** all test results (`npm test`
  152/1116 unchanged, `tsc -b` clean, 51 passed in ~23s local and ~36s in CI, zero flakes across 5
  clean runs plus load and parallelism stress, both CI jobs green); every mutation result, including
  the 38-tests-green AdminTabs scroller and the `md:min-w-[2000px]` pass at the old widths; the
  measured overflow figures (51/32 without the rule, 222/302 with it); the readiness-predicate
  timings (35ms, 53ms); the `workers: 4` parallel runs; the A/B build confirming the bogus CSS rule;
  the commit set; and the fact that the screenshots were downloaded and opened.
- **Not verified:** all test results, the commit count, the diff stat, and the change set as `git`
  sees it. Each is attributed above.
- **One thing the conductor should confirm before the PR:** that `git diff --stat origin/main...HEAD`
  shows **zero files under `web/src/`** and zero `.go` files. Three mutation exercises and one
  deliberate CI failure edited `web/src/` during the slice, and acceptance criterion 9 requires the
  net to be zero. I confirmed `AdminTabs.tsx` and `EnrollmentsTable.tsx` read as unmutated, but a
  three-file spot check is not the same as the diff.
- **No PR number appears anywhere in this retro or in the proposed items**, by instruction. The work
  is referenced by date and slug.
- **Outstanding and belonging to the conductor:** the item filings below, the `/backlog close` run
  with its `git mv` into `docs/backlog/closed/` (conditional on filing slice 2 first), the four
  amendments, the CLAUDE.md decision, the diff check above, the final gates, all commits, and a
  ROADMAP refresh.

## CLAUDE.md verdict

**One amendment earned. One candidate declined, with reasons. Plus one trivial addition that should
just be taken.**

### Take: `make test-e2e` in the Commands block

The Commands block lists `make build`, `make test`, `make test-integration` and `make generate`. A
harness no future agent knows exists is a harness that rots. One line, plus the prerequisite:

> ```bash
> # Browser end-to-end suite (Playwright; requires Docker Postgres, Node, and
> # `cd web && npx playwright install chromium webkit` once). Run from Git Bash on
> # Windows. See web/e2e/README.md for what it does and does not cover.
> make test-e2e
> ```

### Take: the Tailwind scan, as a bullet under "Key Design Decisions"

That section holds mechanism facts whose consequences bite (token format, bcrypt cost, testability
overrides). This is exactly that shape, it is checkable against a diff, and it is currently written
down only in `web/e2e/README.md`, which a backend engineer editing a Tailwind class will never open.
Proposed wording:

> **Tailwind v4 scans the whole project, so prose is compiled input.** `@tailwindcss/vite` builds its
> Scanner over the Vite root (`{base: viteRoot, pattern: '**/*'}`) and reads **source files on disk** -
> every file, comments and markdown included - never the emitted JS bundle. Three consequences, all
> silent. (1) A class string a component **computes** rather than spells (`` `min-w-[${SIZES.w}px]` ``)
> emits **no CSS rule at all** while the rendered `class` attribute stays byte-identical, so every
> jsdom class-string pin stays green and the styling does nothing. (2) Writing a real utility name in
> a comment or a doc keeps that rule alive after the last real usage is deleted, so nothing notices
> the deletion - and it can silently defeat a test that is trying to prove the opposite, which this
> repo has measured. (3) Writing a class-**shaped** string that is not a real utility emits its own
> bogus rule into the production bundle - this repo has shipped one. **Describe a class in prose; do
> not spell it.** The only gate that can observe any of this is the browser lane (`make test-e2e`),
> because it is the only one that measures the production bundle.

If you want it shorter, sentence (1) is the load-bearing half - it is the one that lets a
non-functional fix look identical to a working one. (2) and (3) are the cost of writing the rule
down carelessly, and they are what this slice actually paid.

### Decline: a general "check behaviour by executing behaviour" rule

It is a true and important lesson, it is the headline of this retro, and it should **not** go in
CLAUDE.md.

- **Every other bullet in that file is checkable against a diff.** A reviewer can look at a change
  and say "this write is not epoch-fenced" or "this defines a parallel spec struct". Nobody can look
  at a diff and say "this conclusion was one category broader than its evidence". A rule that cannot
  be applied at review time is the kind the brief warns about, and the file is already long enough
  that a decorative bullet costs the readable ones attention.
- **It partly collides with an existing memory entry.**
  `reference_wrong_prose_is_the_dominant_defect` says "probe the claim and grep its literal wording,
  don't reason about where it lives" - which points toward grep, where this lesson points away from
  it. The two reconcile (grep to *find* a claim, execute to *test* it), but a CLAUDE.md bullet that
  needs a reconciliation footnote is worse than no bullet.
- **The system already caught all four instances**, and caught the worst one by the engineer halting
  mid-slice. The evidence says the process works; adding a rule would be treating a success as a
  failure.

Recommend it as **durable memory instead**, as `reference_match_the_instrument_to_the_claim`, sibling
to the existing `reference_cadence_test_must_assert_wiring` and `reference_test_green_because_of_the_bug`
entries. That is where the project's verification-methodology lessons already live and where they get
read at the right moment.

## Recommended Backlog Items

Proposals only - the conductor files via `/backlog`, and the human gives final accept. Every factual
claim below was verified by reading the worktree in this pass.

**1. Slice 2: run a `relay-agent` inside the browser harness**
- type: `idea`, priority: `medium`
- **File this BEFORE closing `idea-2026-06-03-web-e2e-harness`.** It is the declared coverage limit of
  slice 1 and the larger half of the parent item's value; closing the parent without it loses the
  scope.
- Slice 1 runs no agent, so no `workers` row can exist. Concretely uncovered: `/workers` is
  empty-state only (gated on the literal copy "No workers enrolled yet."); `/workers/:id` is not in
  `surfaces.ts` at all and is never visited - the stronger gap, and the page the 2026-08-13 retro
  flagged as having under 15px of headroom; `/admin/reservations` is populated but its create form's
  `WorkerPicker` is empty-state; no job executes, so no task reaches `running`,
  `/jobs/:id/tasks/:taskId` renders an empty log, and SSE task-log tailing is not exercised at all.
- Shape: build `relay-agent`, run it against the test server with `RELAY_ALLOW_AUTO_ENROLL` **on for
  a second server project only**, so the default-posture server slice 1 uses keeps running the
  posture production runs. Note the interaction the item must decide: `playwright.config.ts` pins
  `RELAY_ALLOW_AUTO_ENROLL: 'false'` explicitly (not unset) precisely so a developer's exported shell
  variable cannot flip it, so slice 2 must add a project rather than relax that pin.
- Acceptance should require at least one task reaching `running` and one SSE log chunk arriving in a
  real browser, since that is the property no gate in this repo has ever observed.

**2. `web-ci.yml` runs the last Node-20 action majors, and pins a Node version nobody has verified**
- type: `idea`, priority: `medium`
- `.github/workflows/web-ci.yml` uses `actions/setup-node@v4` (:40), `actions/cache@v4` (:80) and
  `actions/upload-artifact@v4` (:111). Those are the last majors on the Node 20 runtime, which
  GitHub is force-running on a newer runtime during its deprecation window - so these three steps are
  already executing on a runtime nobody in this project has verified them against.
  `.github/workflows/go-ci.yml` is already on the Node-24 majors (`actions/checkout@v5`,
  `actions/setup-go@v6`), and `web-ci.yml` uses `checkout@v5` too, so the workflow is internally
  inconsistent as well as behind.
- **Do not hardcode a target version from memory** - read each action's own release notes and bump to
  whatever major carries the current runtime, then confirm the workflow is still green. The
  `upload-artifact` bump is the one with real behaviour changes historically (artifact immutability
  and the one-name-per-run rule), and this workflow uploads under
  `playwright-${{ github.run_id }}` with `if: always()`, so check that path specifically.
- Second, smaller, same file: `web-ci.yml` pins `node-version: 22` (:42) while
  `web/package.json` carries `@types/node: ^26.3.0` (:29). `tsc -b` therefore type-checks the harness
  against an API surface several majors ahead of the runtime CI executes it on, so a Node 26-only API
  would pass the type check and fail at runtime. Either pin `@types/node` to the CI Node major or
  raise `node-version`, and add an `engines` field so the two cannot drift silently again.

**3. `layout.spec.ts`'s overflow gate cannot distinguish "fits" from "clipped behind an inner
scroller"**
- type: `idea`, priority: `medium`
- **Measured, not reasoned.** Replacing `web/src/admin/AdminTabs.tsx:17`'s `flex-wrap` with
  `max-w-full overflow-x-auto` puts five admin pills into an unlabelled horizontal scroller with no
  keyboard affordance, and the entire browser suite stayed green (38 tests at the time; 51 now).
  `documentElement.scrollWidth <= clientWidth` holds, because the content overflows into its own
  wrapper rather than past the document edge. `web/e2e/README.md:100-107` records this.
- **Not a duplicate of `bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports`.** That bug is one
  live instance and its acceptance criterion is nav-specific; this is a property of the gate that
  applies to every `overflow-x-auto` container in the app, of which `Table.tsx`'s scroll wrapper is a
  deliberate and correct one. Fixing the header nav does not close this; closing this would have
  caught the header nav.
- The hard part is the discrimination, and the item should say so rather than prescribing: the app
  has containers that are *supposed* to scroll horizontally (`Table.tsx`'s wrapper, with
  `role="group"` and an `aria-label`) and containers that are not. A candidate rule is that a
  horizontally-scrollable element must either carry an accessible name and a tab stop, or not
  overflow - which is a property the `Table` primitive already satisfies by construction and
  `AdminTabs` would not. Whatever is chosen, prove it RED against the AdminTabs mutation above and
  leave that mutation's discriminating input behind as a permanent test.

**4. Four surfaces reachable today are absent from `web/e2e/surfaces.ts`**
- type: `idea`, priority: `low`
- `surfaces.ts` has 13 entries; `web/e2e/README.md:83-88` names what is missing. **Three need no
  agent and no config change and could be added now:** `/jobs/:id/tasks/:taskId` (the seeded job has
  three tasks, so the route resolves), `/profile/password` and `/profile/sessions`. The fourth,
  `/register`, needs `RELAY_ALLOW_SELF_REGISTER=true`, which means a second server project - group it
  with the register-flows gap rather than with these three. `/workers/:id` belongs to item 1.
- Low priority because each is one entry plus a readiness predicate. Worth filing anyway because the
  gap is invisible: the file reads as an enumeration of the app's surfaces and is an enumeration of
  thirteen of them, and every new surface a future frontend slice ships will default to absent unless
  somebody makes adding one part of the routine.
- Whoever takes it should apply this slice's own lesson to the new predicates: `ready()` must gate on
  something that can only be true once **that** page has rendered **its** data. Four of the original
  thirteen predicates were measured to pass on any state, two of them even with every API forced to
  500.

**5. AMEND (no new file): [[idea-2026-08-23-integration-only-guards-ci-never-runs]]**
- Record the frontend twin found by this slice's verification pass and now half-closed: the Go-side
  complaint is that guards behind `//go:build integration` never run in CI, and the **web** side was
  strictly worse - `npm test`, `tsc -b` and `npm run build` were not tag-gated, they were **entirely
  absent from CI**, for twelve merged frontend slices and 1116 tests. `.github/workflows/web-ci.yml`
  closes that half.
- Record that the Go half is untouched and the item stays open for it, and cross-reference
  `idea-2026-08-24-handler-pool-has-no-seam` as the mechanical cause on that side, so a future reader
  does not mistake this amendment for the whole resolution.

**6. AMEND (no new file): [[feature-2026-06-05-upgrade-vite-vitest]]**
- Record that this slice deliberately inverted the item's stated "ideally before the new test surface
  grows" sequencing, with the argument: the upgrade changes the build that produces the shipped
  bundle, and a real-browser regression gate is what makes that upgrade safe to take. The upgrade now
  inherits one.
- Record the three concrete things its acceptance must now cover, all of which are new since the item
  was written: `web/vite.config.ts`'s `test.exclude` **replaces** vitest's defaults rather than
  extending them, so the `[...configDefaults.exclude, 'e2e/**']` spread must be re-checked under
  vitest 4 or vitest walks `web/node_modules`; `web/tsconfig.json` now carries a widened `types`
  (with `"node"`) and `include` (with `"e2e"` and `"playwright.config.ts"`); and `npm run test:e2e`
  must be part of the upgrade's own gate, not just `npm test`.

**7. AMEND (no new file): [[idea-2026-08-09-native-dialog-element-reconsideration]]**
- **Its written trigger condition has fired.** The item names two triggers - jsdom implements
  `HTMLDialogElement`, **or** the repo gains a real-browser test harness - and cites
  `[[idea-2026-06-03-web-e2e-harness]]` as the second one. That harness landed today.
- Record honestly what firing does and does not unlock, or the trigger becomes noise: the dialog unit
  tests still run in jsdom, so this does not make native `<dialog>` viable by itself. What it unlocks
  is that the *evaluation* is now possible - `showModal()`'s focus trap and top-layer behaviour can
  be exercised in Chromium and WebKit for the first time. Whether that justifies moving dialog
  coverage out of jsdom is the actual question and it is unanswered.
- The same note applies to `[[idea-2026-08-12-document-z-index-layering-scale]]`, which describes
  itself as blocked on "jsdom support or the Playwright harness".

**8. AMEND (no new file): [[idea-2026-08-14-table-minwidth-magnitude-is-unchecked]]**
- The item's "Related" section ends with "Why nothing in `npm test` can see the symptom:
  [[idea-2026-06-03-web-e2e-harness]]". That is now out of date in a way that changes the item's
  option set.
- Record that **two of the ten consumers now have a real magnitude gate**:
  `web/e2e/keyboard.spec.ts`'s `assertScrollable` asserts the scroll wrapper overflows by **more than
  100px** at a 480px viewport on `/admin/enrollments` and `/admin/invites`, with the threshold
  derived from measurement (51px and 32px of residual overflow with the min-width rule missing;
  222px and 302px with it applied). That is precisely a magnitude check, and it is exactly the
  "presence is enforced, magnitude is prose" gap the item was filed about - now closed for two
  tables and open for eight.
- This does **not** displace the item's recommended option A (a dev-only runtime assertion inside
  `Table`), and the amendment should say so: option A reads the values actually passed and covers a
  future consumer that does not exist yet, where the browser lane covers only the tables somebody
  wrote a spec for. But the item's framing that no automated gate can observe the symptom no longer
  holds, and its "Notes" trigger conditions should be re-read in that light.
