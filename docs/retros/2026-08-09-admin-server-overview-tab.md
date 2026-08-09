---
date: 2026-08-09
topic: admin-server-overview-tab
branch: claude/pr-merging-session-3f03bb
range: d1c6b09..HEAD
---

# Session Retro: 2026-08-09 - admin-server-overview-tab

**TL;DR:** The admin console's fourth tab: `/admin/server`, a read-only operational overview rendering
nine fleet-wide counts, the self-registration policy flag and an HTTP reachability pill. Frontend-only,
zero Go changes; web suite 710 -> 761 tests; review returned **0 high** / 5 medium / ~13 low, all five
mediums and eight cheap lows fixed in the same iteration. This is the smallest of the four built tabs
by surface - no mutations, no pagination, no dialogs, no forms - and its entire design problem was
*what not to render*: the hi-fi is dominated by a `VERSION` / `BUILD` / `DB` / `UPTIME` strip and a
13-row environment-variable table, none of which any endpoint backs. It ships on the four endpoints
that exist and states its own omissions in a footnote. What and why are recorded in the closed item's
Resolution (`docs/backlog/closed/feature-2026-08-08-admin-server-overview-tab.md`); this retro records
what is worth carrying forward, which this time is mostly a **cluster of test-vacuity findings** -
three of the five mediums were tests that measured nothing.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-09-admin-server-overview-tab.md`, **plan**
  `docs/superpowers/plans/2026-08-09-admin-server-overview-tab.md` (10 sequential tasks, one frontend
  engineer; nothing to fan out, since every task's test imports the module the previous task created).
- `web/src/admin/server/` - `api.ts` (`getHealth`, `getServerConfig`), `useServerHealth.ts`,
  `useServerConfig.ts`, `ErrorStrip.tsx`, `HealthPill.tsx`, `StatSection.tsx`, `ServerTab.tsx`, each
  with a colocated test file.
- One `ADMIN_TABS` entry in `web/src/admin/tabs.ts`, plus the two shipped shell test files that entry
  forces.
- A comment-only correction in `web/src/admin/AdminPage.tsx`: its header comment said the unbacked
  facts strip "belongs to the future Server/overview tab". That is now a decision rather than a
  deferral, and the comment says so and points at the allowlist-endpoint item.
- Nothing outside `web/src/admin/`. `web/src/jobs/`, `web/src/workers/`, `web/src/lib/` and
  `web/src/components/` were read-only for the slice.

## Key Decisions

- **Omit rather than fabricate, and file the enabler.** The `VERSION` / `BUILD` / `DB` / `UPTIME` strip
  and the whole env table are absent because no route returns a version, a commit, a Go version, a
  process start time, a Postgres version, or any `RELAY_*` value. The security half of that argument is
  the stronger one: an endpoint that dumps effective config and redacts what looks secret is a
  **deny-list**, and deny-lists for secrets fail open on the next variable someone adds. Proposed as
  `feature-2026-08-09-server-info-allowlist-endpoint` - admin-gated, hand-written allowlist, values
  absent by construction rather than redacted.
- **Reuse `useJobStats` / `useWorkerStats` across module boundaries, do not clone.** Mounting the tab
  creates an *observer* on the existing `['job-stats']` and `['workers','stats']` cache entries rather
  than a second client for either endpoint. The interval is passed explicitly (10s, strictly less load
  than the shipped 3s dashboards) so a future change to those hooks' defaults cannot silently change
  this tab.
- **Per-region degradation is the acceptance criterion.** Four independent queries, and no query's
  failure may unmount another's data. A section that errors with no data swaps its grid for an error
  strip in place; a section that errors *with* data keeps the numbers and marks them stale; a failed
  `/v1/config` renders **no chip in either state**, because a fabricated `DISABLED` would misreport a
  security-relevant policy.
- **The pill reports what the server said, not what a 200 implies.** Four states, including a
  report-it-verbatim branch that is unreachable in production today, so a future non-`ok` status shows
  up instead of rendering as `HEALTHY`.
- **No page-level refresh, no sparkline, no count badge.** `KpiStat` accepts `progress` and nothing
  here is a ratio out of a meaningful max - `online / total` is tempting and misleading, since a fleet
  that is 100% disabled would render a full bar.

## Problems Encountered

1. **`handleHealth` performs no database check, which is what the pill's copy had to be built
   around.** Found at spec time by reading `internal/api/health.go`, not assumed from the endpoint's
   name. A `DB` fact and a health pill both sourced from `/v1/health` would have been two different
   kinds of lie: one fabricated, one over-claiming. So `HEALTHY` is scoped in the footnote to "the
   listener answered and nothing more", and the **realistic outage became a required test**: health
   `ok` while both stats endpoints 500 is Postgres down with the server up, and the correct rendering
   is a green pill beside two degraded sections. That test exists precisely so it fails loudly if
   someone later derives the pill from the stat queries to make it look smarter. The general habit
   worth naming: when a status surface's copy depends on what a probe actually probes, read the probe.
2. **Two label corrections that only reading the SQL produces.** `failed_24h` filters
   `status IN ('failed','cancelled')`, so a cell labelled "Failed" would be wrong - it reads
   `FAILED OR CANCELLED · 24H`, with a test asserting the bare `FAILED · 24H` is absent. And every
   worker bucket, `total` included, excludes revoked workers, which the TOTAL cell carries as its
   sub-line so an admin never reconciles it against the decommissioned list by hand. Neither is
   visible from the JSON response; both are properties of the query behind it.
3. **The spec named a cache key that does not exist, and the planner caught it by reading the code.**
   The spec's polling table said `['worker-stats']`; the shipped hook caches under `['workers','stats']`.
   The plan called it out explicitly, kept the spec's substantive point (this tab creates an observer,
   not a new entry), and instructed the engineer not to "fix" the key or assert the wrong one anywhere.
   Worth recording as the pipeline working: a plan phase that reads code rather than transcribing the
   spec catches the spec's factual errors before they become assertions.
4. **Plan-supplied test bodies were vacuous again, and the engineer caught it.** The degraded-matrix
   tests spread `...handlers()` and then a `fail(path)` override, but the success handler ordering in
   `server.use` meant the intended failure never took effect in some cases. The engineer reordered so
   the failing handler actually wins. Third-plus instance of the standing lesson: **plan-supplied
   tests are untrusted**, and "it matches the plan" is not verification.
5. **Three of the five mediums were tests that measured nothing.** This is the iteration's real theme,
   so each is worth stating concretely:
   - **A cadence test that asserted a constant instead of the wiring.** The health-poll test asserted
     `HEALTH_POLL_MS === 30_000` and waited 250ms. A hook that passed a literal `3000` to
     `refetchInterval` while leaving the exported constant at `30_000` would have passed. Fixed by
     reading `refetchInterval` off the query cache entry's own options, so the assertion is about what
     the observer is actually wired with.
   - **`POLL_MS` was entirely untested.** The whole argument for passing the interval explicitly is
     that falling back to the reused hooks' 3000ms default would triple the shared dashboards' polling
     load - and nothing checked it. The tab now has a test that reads `refetchInterval` off both
     `['job-stats']` and `['workers','stats']` cache entries and asserts `10_000`. A decision defended
     at length in a comment, with no assertion behind it, is a decision one refactor from being lost.
   - **A case-sensitive forbidden-content sweep defeated by title case.** The "no version / build /
     uptime / env content" check matched `container.innerHTML` against uppercase literals, so a leak
     rendered as `Build 1.2.3` would have passed - and the check was simultaneously at risk of being
     satisfied (or tripped) by the footnote's own legitimate prose, which mentions those very words.
     Fixed by cloning the container, removing the footnote node, uppercasing the HTML, and adding a
     **permanent counter-proof test** that injects a lowercase `leaked build 1.2.3` outside the
     footnote and confirms the same technique catches it.
6. **The one behavior medium: the `HealthPill` applied "a lapsed claim is not a claim" to itself but
   not to the numbers beside it.** The pill deliberately lets its error branch win over stale data,
   with a comment explaining that a liveness claim backed by a 30s-old response that has since started
   failing is not a claim. The stat sections, three files away, showed indefinitely-old numbers under a
   flat `stale · last update failed` line with **no age at all** - so a section whose polls had been
   failing for an hour looked identical to one that dropped a single request. Fixed by threading the
   query's `dataUpdatedAt` into `StatSection` and rendering the age
   (`stale · last update failed · 4m ago`), announced via `role="status"` / `aria-live="polite"` so the
   fresh-to-stale transition reaches assistive tech. The lesson is the shape, not the fix: **a
   principle stated in one component's comment does not propagate to its siblings.** When you write
   "an old value is not a current value" as a reason, go look for every other old value on the same
   screen.
7. **The fifth medium was a security finding in the backlog item this slice filed, not in the code.**
   `feature-2026-08-09-server-info-allowlist-endpoint`'s exclusion list omitted
   `RELAY_BOOTSTRAP_PASSWORD`, and its acceptance criterion was written as "assert the excluded keys
   are absent" - which is **deny-list shaped, against the item's own prose arguing for an allowlist**.
   An absence check over a hand-listed set passes unchanged the day someone adds a new secret env var
   and forgets it. Corrected to require **key-set equality against the allowlist constant**, plus
   value-level assertions that each secret-bearing variable's actual value never appears anywhere in
   the response. `cmd/relay-server/main.go:75` only `Unsetenv`s the bootstrap password inside the
   `RELAY_BOOTSTRAP_ADMIN` branch, so it can persist in the process env, which makes the exclusion
   load-bearing rather than redundant. Two things follow: a backlog item is a **reviewable artifact**,
   and the deny-list-versus-allowlist argument has to be carried into the acceptance criteria or it
   only ever lived in the prose.
8. **Process, positive for once: Phase 4 ran the documented pipeline.** The previous batch reported
   five consecutive iterations deviating from the documented `relay-verify` workflow. That workflow has
   since been dropped in favour of a conductor-run `/code-review` fanned out to parallel agents, and
   that is exactly what happened here - conductor review plus three `relay-code-reviewer` lenses
   (invariants, correctness, security). The integration lane was skipped on the explicit and correct
   ground that the diff contains zero Go.

## Findings Triage

- **0 high.** Fourth consecutive tab with none. The pattern from the previous batch holds: findings
  track novelty rather than diff size, and this module's novelty was concentrated in the degradation
  logic and the test design, which is exactly where the mediums landed.
- **5 medium, all fixed with tests**: three test-vacuity (Problem #5), one stale-data-without-age
  (Problem #6), one in the freshly filed backlog item (Problem #7).
- **~13 low**, of which **8 cheap ones were fixed the same iteration**: `aria-label`ed Retry buttons
  (`Retry jobs stats` / `Retry fleet stats` / `Retry access config`, so three identical buttons are
  distinguishable), `role="alert"` on the error strip, an `errorMessage()` floor so a thrown value with
  no message cannot render an empty strip, and the aria-live staleness announcement among them. The
  rest were accepted as minor or deferred below.

## Known Limitations

- **The tab is deliberately sparse next to the hi-fi.** No version, build, uptime, database or
  environment content anywhere, enforced by an absence sweep with a counter-proof. The footnote is what
  closes the gap for a reader comparing the page to the mock; the enabler is
  `feature-2026-08-09-server-info-allowlist-endpoint`.
- **The admin gate here is placement, not protection.** `/v1/jobs/stats` and `/v1/workers/stats` are
  `auth(...)`-only and `/v1/config` and `/v1/health` are public, so every number on this admin-gated
  page is already visible to any authenticated user and two of them to anyone who can reach the port.
  This slice widens no auth surface at all - and the corollary matters for the follow-up: `GET
  /v1/server/info` is the first server-facts route whose payload genuinely needs `admin(...)`, so it
  must not be modelled on these four.
- **The 24h buckets window on `jobs.updated_at`**, a finish-time proxy rather than a real finish
  timestamp (`bug-2026-06-05-jobs-stats-24h-updated-at-proxy`). Accurate today, because the only writer
  is `UpdateJobStatus` and no route re-opens a terminal job, and it silently degrades the day a job
  retry endpoint lands. Displayed, with the footnote framing the page as indicative rather than an
  audit source.
- **`JobStatusCounts` and `WorkerStatusCounts` are unfiltered aggregates over the whole table**, i.e. a
  sequential scan per call. Pre-existing, and this tab's 10s poll is strictly lighter than the shipped
  3s dashboards, so it introduces no new worst case. A partial index or materialized counter was
  **proposed and deliberately not filed**: no measurement exists, and filing an unmeasured performance
  item invites a speculative fix.
- **Status and staleness derive from the browser clock.** The staleness age comes from
  `dataUpdatedAt`, which is client-side, so a badly skewed client mislabels the age. The server exposes
  nothing to prefer.
- **`RegisterScreen` still fetches `/v1/config` inline**, so there are now two clients for that
  endpoint. Deliberate in the spec (different semantics: a one-shot fail-closed `false` on the sign-up
  path versus a cached query with a visible error state here) and deliberately deferred by the
  conductor at review, but see Deferred Findings.

## Deferred Findings

Review-noted, not fixed, recorded here so they are not rediscovered:

1. **`RegisterScreen` versus `useServerConfig`.** With `useServerConfig` shipped, the inline
   `apiFetch<ConfigResponse>('/config')` in a raw `useEffect` at `web/src/auth/RegisterScreen.tsx:21-25`
   is a second client for one endpoint. It also has **no cancellation guard** - the effect's `.then`
   calls `setSelfRegister` unconditionally on whatever resolves, which is the generation-ordering
   invariant's shape (nothing bumps a generation, nothing checks one). Pre-existing and benign in
   practice today, since the screen is unlikely to remount mid-flight and the failure mode is a stale
   policy flag rather than a security decision - the server enforces the invite requirement regardless.
   Filed, see below.
2. **`StatusDot` versus `HealthPill`'s hand-rolled dot.** `HealthPill` renders its own
   `<span aria-hidden>●</span>` rather than using the shared `components/holo/StatusDot`, which is
   liveness-tone-shaped and does not currently take an arbitrary tone. Two hand-rolled dots is thin
   evidence for a generalization; proposed, not filed.
3. **Test `QueryClient`s use `retry: false` while production uses `retry: 1`.** Every error-path test in
   this module (and in the sibling tabs) constructs a client with retries off, so no test exercises the
   one real retry the app performs. This is a **suite-wide convention**, not this diff's problem, and
   changing it would slow every error test. Recorded, not filed - if it is ever worth addressing it is a
   single deliberate decision about the shared test harness, not a per-module fix.

## Improvement Goals

Carried forward, briefly:

- **Treat the plan as an untrusted source of test design** - **honored, and it paid** (Problem #4). The
  engineer caught the handler-ordering vacuity rather than shipping green-but-empty tests.
- **Verify a backlog item's technical claims against the code during spec** - **honored** (Problems #1
  and #2), and this time the same skepticism was owed *downstream* too: the spec's own cache key was
  wrong and the plan caught it (Problem #3). The habit generalizes to every artifact in the chain, not
  just the backlog item at the front of it.
- **Pair every absence assertion with a positive control, in the representation the real failure would
  take** - **half honored.** The forbidden-content sweep already used `innerHTML` rather than
  `textContent`, inheriting the previous iteration's fix, but it was case-sensitive and had no control
  at all until review added one (Problem #5). The control is now a permanent test, not a one-off
  mutation.
- **An overlay owns its own error surface** - **n/a**, no overlay. The regional analogue was honored
  though: each degraded region owns its own strip and its own Retry, and the page has no shared error
  box that could swallow them.
- **Coverage shape: name the test for every rejection** - **honored, and it is the slice's acceptance
  criterion**: every single-query failure, the two-query realistic outage, the all-four failure, the
  post-success stale case, and the Retry-restores-the-grid path each have a named test.
- **Rewriting a shared test file is coverage-losing** - **honored.** `AdminPage.test.tsx` and
  `AdminTabs.test.tsx` were appended to; the existing `renders no unbacked server-facts strip` test was
  left exactly as it was and still passes.
- **Confirm which design-fidelity layer is authoritative** - **honored**, and it is what forced the
  scope decision: the hi-fi was read closely enough to establish that ~90% of it is unbacked.
- **Independently re-verify the tree and re-run the green gate** - **honored**, on the settled tree.
- **Give the playbook an explicit unattended Phase 4 path** - **honored at last** (Problem #8), by
  deleting the unrunnable workflow rather than by documenting the deviation.
- **Teardown ends the generation first / a per-event guarantee is not a bound / diagnose a red gate /
  a wrong contract in docs is a defect / calling the clear function is not evidence** - **n/a.** No
  async lifecycle in this module, no recovery loop, no red gate, no Go, no secret. The generation
  invariant *is* implicated in the deferred `RegisterScreen` finding, one module over.

New from this iteration:

- **A cadence assertion must read the wiring, not the constant.** `expect(POLL_MS).toBe(30_000)` proves
  the constant, not that anything uses it. Read `refetchInterval` off the query cache entry. **Candidate
  for durable memory** - it is a concrete, reusable instance of the standing "a green test can be
  vacuous" rule and it generalizes to any exported tuning constant.
- **A principle written in one component's comment does not propagate to its siblings.** The pill's "a
  lapsed claim is not a claim" reasoning was correct, argued in a comment, and not applied to the
  numbers rendered inches away (Problem #6). When you write down *why* a piece of state is unsafe to
  present, sweep the same screen for other state with the same property. **Candidate for durable
  memory.**
- **A backlog item is a reviewable artifact, and its acceptance criteria carry its argument.** The
  security medium was entirely inside a doc this slice wrote: the prose argued allowlist, the
  acceptance criterion tested deny-list (Problem #7). **Candidate for a one-line amendment to the
  existing backlog-proposal note**, not a new one: when an item's rationale is "the safe form is X",
  the acceptance criterion has to be falsifiable against not-X.
- **Case sensitivity is part of a probe's reach.** The previous batch promoted "a `textContent` sweep
  is blind to attributes"; this iteration adds that an exact-case sweep is blind to `Build`. Belongs as
  a sentence in that same note rather than as a new one.

## Files Most Touched

- `web/src/admin/server/ServerTab.tsx` - the composition point and the only place the four queries meet.
  Carries the `POLL_MS` reasoning, the never-fabricate-a-default comment on the access panel, the
  `errorMessage()` floor, and the footnote whose exact wording two tests assert.
- `web/src/admin/server/ServerTab.test.tsx` - the happy path with distinct per-field values (so a
  swapped-field regression fails), the whole degraded matrix, the clone-and-strip forbidden-content
  sweep plus its counter-proof, and the `POLL_MS` wiring test. Both of the iteration's test-vacuity
  fixes in the tab live here.
- `web/src/admin/server/StatSection.tsx` - the three body states (loading em dashes that keep the grid
  from reflowing, stale-with-age, degraded-in-place) and the `dataUpdatedAt` thread from Problem #6.
  The comments carry why blanking good numbers on a dropped poll is the worse failure.
- `web/src/admin/server/HealthPill.tsx` - `deriveHealthPill` as a pure function so all four states are
  testable without rendering, the report-verbatim branch, and the comment forbidding a future
  "smarter" pill derived from the stat queries.
- `web/src/admin/server/useServerHealth.ts` + `.test.tsx` - the 30s probe, and the cadence test rewritten
  to read the observer's actual `refetchInterval`.
- `web/src/admin/server/useServerConfig.ts` + `.test.tsx` - `staleTime: Infinity` with the reason
  (startup-only env value; changing it restarts the process serving the SPA), and a no-refetch test that
  waits past a duration long enough to actually catch one rather than a token 50ms.
- `web/src/admin/server/ErrorStrip.tsx` - one degraded body for three regions, `role="alert"`, and the
  per-region `aria-label` that makes three identical Retry buttons distinguishable.
- `web/src/admin/tabs.ts` + `AdminPage.tsx` + `AdminPage.test.tsx` + `AdminTabs.test.tsx` - the
  one-line registry entry, the corrected shell comment, and the shipped test files it forces, all
  edited additively.

## Verification

- Full web suite green: **761 tests, up from 710** (710 -> 753 on the implementation, -> 761 with the
  review fixes). Production build green (`tsc -b && vite build`), with `git checkout -- web/dist/`
  before the change set was assembled.
- Both re-run by the conductor on the settled tree rather than trusted from the implementer's report.
- Change set confirmed to be entirely under `web/src/admin/`: no Go file, no `.sql`, no `.proto`, no
  migration, no `web/dist`.
- Code review: conductor `/code-review` fed to three parallel `relay-code-reviewer` lenses (invariants,
  correctness, security). 0 high, 5 medium (all fixed), ~13 low (8 fixed). The integration lane was
  skipped on a zero-Go diff.
- No Go changed, so none of the six Invariants was in play and no `make test` / `make test-integration`
  run was required. The frontend analogues were respected: every request goes through `apiFetch`, no
  component calls `fetch` directly, no shared primitive was modified, and the generation-ordering
  invariant has nothing to bite on - every request here is a TanStack query whose cancellation TanStack
  owns, which is itself a reason to resist any future hand-rolled "live" upgrade of this tab.
