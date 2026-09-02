# Lane T design: upgrade vite and vitest to clear the dev-tooling audit advisories

Date: 2026-09-02
Backlog item: `docs/backlog/feature-2026-06-05-upgrade-vite-vitest.md`
Branch: `claude/web2-t-vite-upgrade`
Gate mode: autonomous. There was no human in the loop to answer one question at a time, so every
question I would have asked is written out in Decisions with the answer I took and the reason, and
every call a human might make the other way is in Escalations.

## Why this is a design and not a version bump

Three of this repo's four frontend gates run through vite: `npm test` (vitest, which resolves the
project's vite), `npm run build` (the production bundle that `//go:embed all:dist` snapshots into
`relay-server`), and `make test-e2e` (Playwright driving that embedded bundle). Vite 8 replaces the
bundler and the transformer underneath all three. The unit suite is the least interesting of the
three: it is the production bundle and the browser harness that can move without any test saying so.

## Current state, verified against the tree

`web/node_modules` is present in this worktree and its `.package-lock.json` exists, but I have no
shell in this session, so I could not run `npm audit`, `npm ls` or `npm test`. Everything below is
read out of `web/package.json` and `web/package-lock.json`, which agree.

Installed today (lockfile):

- `vite` 5.4.21 (declared `^5.4.11`)
- `vitest` 2.1.9 (declared `^2.1.8`)
- `vite-node` 2.1.9, `@vitest/expect` `mocker` `runner` `snapshot` `spy` `utils` all 2.1.9 - every
  one transitive. There is no direct `@vitest/*` entry in `devDependencies`.
- `esbuild` 0.21.5, with 23 `@esbuild/<platform>` optional entries recorded in the lock. `vite` is
  its only requirer in the tree.
- `@vitejs/plugin-react` 4.7.0
- `@tailwindcss/vite` 4.3.0 (declared `^4.0.0`)
- `jsdom` 29.1.1, `typescript` 5.9.3, `@types/node` 26.3.0, `@playwright/test` 1.62.1 (pinned exact)

Config surface:

- `web/vite.config.ts` declares `plugins`, `server.proxy` and `test` only. It contains no
  `rollupOptions`, no `manualChunks`, no `build`, no `optimizeDeps` and no `esbuild` option, so none
  of Vite 8's renamed or removed build options are in use here. Its `test.exclude` is
  `[...configDefaults.exclude, 'e2e/**']`.
- `web/tsconfig.json` has `"types": ["vitest/globals", "@testing-library/jest-dom", "node"]` and
  `"include": ["src", "e2e", "playwright.config.ts"]`. `web/tsconfig.node.json` includes only
  `vite.config.ts`.
- `.github/workflows/web-ci.yml` pins `node-version: 22` and runs, in order, `npm ci`, `npx tsc -b`,
  `npm test`, `npm run build`, `go build ./cmd/relay-server`, Playwright browser install,
  `npm run test:e2e`.

Test surface, counted from the tree:

- **162 test files**, all under `web/src`, matching `*.test.ts` / `*.test.tsx`. There are no
  `*.spec.*` files under `src` and no test files at the `web/` root. The four `*.spec.ts` files under
  `web/e2e` are Playwright's and are what `'e2e/**'` excludes. A corroborating count: exactly 165
  files outside `node_modules` import from `vitest` or `vite`, which is the 162 plus
  `src/test/setup.ts`, `src/test/secretLeaks.ts` and `vite.config.ts`.
- **1231 top-of-line `it(` / `test(` declarations**, plus one `test.each` with three cases in
  `workers/WorkerEditForm.test.tsx` that the search's pattern cannot match, giving a static
  expectation of **1234 tests**. No `.skip`, `.only`, `.todo` or `.fails` anywhere under `src`.

That last number is a text search, and a text search cannot establish what vitest collects. It is
here as a cross-check on the recorded baseline, not as the criterion. The criterion is AC1.

## Target versions, and the registry evidence for each

Fetched from `registry.npmjs.org` today, not recalled:

| Package | Now | Target | Evidence |
| --- | --- | --- | --- |
| `vite` | 5.4.21 | `^8.2.2` | `vite/latest` is 8.2.2; `engines.node` is `^20.19.0 \|\| >=22.12.0` |
| `vitest` | 2.1.9 | `^4.1.11` | `vitest/latest` is 4.1.11; peer `vite` is `^6.0.0 \|\| ^7.0.0 \|\| ^8.0.0`; peer `@types/node` is `^20.0.0 \|\| ^22.0.0 \|\| >=24.0.0`; `engines.node` is `^20.0.0 \|\| ^22.0.0 \|\| >=24.0.0` |
| `@vitejs/plugin-react` | 4.7.0 | `^6.1.1` | `latest` is 6.1.1; its peer `vite` is `^8.0.0` and nothing else, so 4.7.0 cannot stay |
| `@tailwindcss/vite` | 4.3.0 | unchanged | 4.3.0's own peer `vite` is already `^5.2.0 \|\| ^6 \|\| ^7 \|\| ^8` |

So the item's headline is confirmed on both counts: 8 is vite's real current major and 4 is
vitest's. The exact targets are vite 8.2.2, vitest 4.1.11, plugin-react 6.1.1.

Two structural facts that decide whether the advisories actually clear:

- `vite@8.2.2`'s `dependencies` are `postcss`, `rolldown`, `picomatch`, `tinyglobby`, `lightningcss`.
  **`esbuild` is not among them**; it is an optional peer at `^0.27.0 || ^0.28.0`, and npm does not
  install optional peers. Since vite 5 is the only thing requiring esbuild in the current lock,
  esbuild should leave the tree entirely rather than move to a patched version.
- `vitest@4.1.11`'s `dependencies` contain **no `vite-node`** and no `esbuild`. `@vitest/mocker`
  survives at 4.1.11. So two of the five advisory rows in the item disappear as packages rather than
  as versions.

`@vitejs/plugin-react@6.1.1` depends only on `@rolldown/pluginutils`; its other three peers
(`oxc-transform-react`, `@rolldown/plugin-babel`, `babel-plugin-react-compiler`) are all marked
optional in `peerDependenciesMeta`, so a plain install pulls none of them.

## What in the backlog item I refuted or corrected

1. **"152 files / 1116 tests" is stale.** It predates the 2026-09-02 web SPA batch. The tree today
   has 162 test files and a static expectation of 1234 tests. Requirement 1 of the addendum is still
   right in substance; only its numbers are wrong, and AC1 replaces them with a measured baseline
   rather than another written-down constant that will go stale the same way.
2. **"plus any required `@vitejs/plugin-react` / `@vitest/*` bumps" is half wrong.** There is no
   direct `@vitest/*` dependency to bump - all six are transitive and follow `vitest`. The
   `@vitejs/plugin-react` half is not optional as the phrasing suggests: plugin-react 6's vite peer
   is `^8.0.0` exclusively, so this is a required two-major bump, 4 to 6, not a maybe.
3. **Correction, not a refutation, on the addendum's requirement 1.** Vitest 4's migration guide has
   a breaking change headed "Simplified `exclude`": vitest now excludes only `node_modules` and
   `.git` by default, where vitest 2 also excluded `dist`, `cypress`, config files and more. The
   spread stays load-bearing and stays correct - `configDefaults` is still exported from
   `vitest/config` in 4.1.11, and the node_modules guard the comment describes is still the thing the
   spread preserves - but the comment's implied claim about the breadth of the defaults narrows, and
   `web/dist` and `web/test-results` are no longer excluded by default. Rewriting that comment
   regenerates its claims, so it gets re-derived against vitest 4, not edited around.
4. **The item's exposure argument holds.** The critical vitest advisory needs the Vitest UI server
   listening, and no script starts it: `test` is `vitest run`, `test:watch` is `vitest`, and
   `test:e2e:ui` is `playwright test --ui`, which is Playwright's UI and unrelated. Nothing else is
   refuted.

## Approaches considered

**A. Minimal three-package bump, everything else pinned by the lock (recommended).** Edit exactly
three version ranges in `web/package.json`, let npm resolve, do not touch jsdom, testing-library,
typescript, msw or `@tailwindcss/vite`. The diff is then attributable: if the e2e suite moves, the
cause is in the vite/vitest majors and nowhere else.

**B. Bump everything to latest while the file is open.** Cheaper in calendar terms, and it is how the
tree drifts back to green audit fastest. Rejected: `@testing-library/react` and `jsdom` sit directly
under the 162 files that this migration is trying to hold constant, and a single moved assertion
would then have two candidate causes. The whole value of AC1 is that the count is attributable.

**C. `npm audit fix --force`.** Rejected outright. It picks versions by advisory graph, not by the
peer constraints above, and it would leave `@vitejs/plugin-react` at 4.7.0 against a vite 8 peer of
`^8.0.0` or resolve it to something nobody chose. The item itself already argues against this.

## The change set

- `web/package.json`: `vite` to `^8.2.2`, `vitest` to `^4.1.11`, `@vitejs/plugin-react` to `^6.1.1`.
  No other dependency line moves, and no `engines` field is added (see D3).
- `web/package-lock.json`: regenerated by `npm install` in `web/`. This is a large mechanical diff -
  esbuild and its 23 platform entries and `vite-node` leave; `rolldown`, `lightningcss` and their own
  platform-specific native packages arrive. All platform variants must be present in the lock, the
  way the 23 `@esbuild/*` entries are today, or `npm ci` on ubuntu CI fails against a lock generated
  on Windows.
- `web/vite.config.ts`: keep the `test` block where it is; keep `[...configDefaults.exclude, 'e2e/**']`;
  re-derive the comment above it against vitest 4's actual defaults. Add further explicit excludes
  only if AC1 measures a change, and if so, only the minimum that restores 162.
- `web/tsconfig.json`: expected unchanged. `vitest@4.1.11` still publishes a `./globals` export
  pointing at `globals.d.ts`, so `"vitest/globals"` in `types` still resolves. AC5 re-measures the
  addendum's "node adds nothing" claim rather than inheriting it.
- No source file under `web/src` should need to change. If one does, that is a finding, not a chore:
  say which vitest 4 breaking change forced it and record it in the commit message.

## Risk and failure modes

**The production bundle is where this migration can go wrong silently.** Vite 8 swaps Rollup and
esbuild for Rolldown and Oxc, and makes Lightning CSS the default minifier. Bundle contents, chunk
names and emitted CSS all change. Every unit test in this repo runs in jsdom, which does no layout
and never sees the built CSS, so `npm test` is structurally incapable of noticing. The only
instrument that can is `make test-e2e`, which serves the embedded production bundle, plus the
screenshots it uploads. This is exactly the reason the addendum's requirement 3 exists, and it is the
single most important verification step in this spec.

**The CSS minifier change lands on Tailwind v4 output.** `@tailwindcss/vite` generates the
stylesheet; Lightning CSS now minifies it and does more syntax lowering than esbuild did. Small size
changes are expected and are not a defect. A missing rule is. `web/e2e/layout.spec.ts` and
`header-nav.spec.ts` are the assertions that would catch a layout consequence, and the screenshots
are the artifact a human has to open, per `web/e2e/README.md`.

**Browser targets rise.** Vite 8's default target moves to roughly Chrome 111 / Edge 111 /
Firefox 114 / Safari 16.4. Playwright 1.62's bundled Chromium and WebKit are both far newer, so the
harness cannot detect a real-world regression here. This is a stated limitation of the verification,
not a claim that nothing changed for end users.

**Security lens.** The advisories are dev-only and the item's reasoning for that is correct: vite,
vitest and esbuild never ship, because the SPA is built to static assets and embedded. Read against
the actual lock rather than assumed: esbuild's own postinstall script leaves with esbuild; rolldown
arrives as prebuilt-binary packages with no install script of its own; and lightningcss is not new to
this change at all - `@tailwindcss/vite` already pulled it before this diff - the upgrade adds a
second lightningcss copy (vite's own, nested under `vite/node_modules`) beside the one already there.
That second copy, and rolldown's own per-platform binary packages, are still a supply-chain surface
change worth naming - more native binary packages fetched at `npm ci` time in CI and on every
developer machine - accepted here because the alternative is staying on a vite major whose dev server
has a published cross-origin read advisory. Nothing about relay's own threat model - the epoch fence,
the identity-checked teardown, the single job-spec pipeline - is touched by this change; no Go file
moves.

**No invariant is in scope.** This lane writes no Go, no SQL and no React. If a proposed fix here
starts editing `web/src`, that is a signal the migration has found a real behavioural change and it
should be surfaced, not absorbed.

## Decisions

**D1. Which packages move?**
Options: (a) the three named above only; (b) those plus `@tailwindcss/vite` to 4.3.3; (c) everything
to latest.
Taken: (a). `@tailwindcss/vite` 4.3.0 already declares a vite `^8` peer, so there is nothing to fix,
and its declared range `^4.0.0` means a future `npm install` will float it anyway - but not as part
of this diff. Do not run a blanket `npm update`; edit the three lines and let npm resolve.

**D2. Do jsdom, `@testing-library/*` or typescript move?**
Options: (a) hold all fixed; (b) bump jsdom and testing-library to current; (c) bump typescript too.
Taken: (a), hold everything fixed. These three sit directly under the 162 test files whose collected
count is the migration's headline acceptance criterion, and moving them would give any change in that
count two candidate causes. The compatibility facts support holding: vitest 4 declares `jsdom` as a
peer at `*`, and `@types/node` 26.3.0 satisfies vitest 4's `>=24.0.0` peer, so nothing forces a move.
If a testing-library incompatibility surfaces during implementation, bump the single package that
demands it and say in the commit message which failure demanded it - do not take the opportunity to
sweep the rest.

**D3. Does the Node floor change as part of this item?**
Options: (a) out of scope, the sibling item owns it; (b) raise `node-version` in `web-ci.yml`;
(c) add an `engines.node` field to `web/package.json`.
Taken: (a), out of scope, with one verification obligation. Vite 8 requires
`^20.19.0 || >=22.12.0` and vitest 4 requires `^20 || ^22 || >=24`. CI's `node-version: 22` resolves
to the latest 22.x, which is well past 22.12, so both are satisfied and no CI edit is required. The
related `idea-2026-08-24-web-ci-node-20-actions-and-unverified-node-version` covers the deprecated
action majors and the `@types/node`-versus-runtime mismatch; note for whoever picks it up that vitest
4's `@types/node` peer of `>=24.0.0` now sanctions the installed 26.3.0 against a Node 22 runtime, so
that item's "match downward" proposal has acquired a constraint it did not have when it was filed.
No `engines` field is added: npm already prints EBADENGINE for vite's own declared range, so a root
field would be a second copy of a number rather than a new check. The obligation is AC8 - a
developer or runner on Node 20.x below 20.19, or 22.0 through 22.11, breaks on this change, and the
verification must record the Node version it ran on.

**D4. Does the vitest config move to its own `vitest.config.ts`?**
Options: (a) keep `test` in `vite.config.ts`; (b) split it out, which is what vitest 4's docs lean
toward.
Taken: (a). Splitting is a defensible piece of housekeeping and it is not this diff's job; it would
move the `test.exclude` block that AC2 exists to protect, into a new file, in the same commit that
changes what its defaults mean. If someone wants the split, it is a separate item.

**D5. What happens to the `test.exclude` comment?**
Options: (a) leave it; (b) rewrite it; (c) re-derive it against vitest 4 and add explicit excludes
only if measured necessary.
Taken: (c). The comment currently asserts a breadth of vitest defaults that vitest 4 has narrowed, so
leaving it is shipping a false claim, and rewriting from the old text risks authoring a fresh one.
Re-derive from vitest 4's own documented default exclude, keep the citation of the acceptance
criterion that guards it, and let AC1 decide whether `dist/**` needs adding.

## Verification plan

Environment requirements the engineer must confirm before starting, and must state if unmet:

- **Postgres.** `make test-e2e` needs a Postgres reachable at `postgres://relay:relay@127.0.0.1:5432`
  - the `relay-postgres` container `scripts/dev.ps1` manages. `docker start relay-postgres`, or run
  `scripts/dev.ps1` once to create it.
- **Playwright browsers.** `cd web && npx playwright install chromium webkit`, once.
- **make on PATH.** Per `web/e2e/README.md`, `make` is not on PATH on this host; use
  `/c/msys64/usr/bin/make.exe` and forward `OS`, `TEMP`, `TMP`, `GOPATH`, `GOMODCACHE` and `GOCACHE`
  as command-line variable assignments, or the `.exe` suffix branch and the Go build both fail in
  ways that look like something else.
- **Go and node**, for the embed build and the SPA build.

Sequence:

1. **Before touching anything**, at HEAD: run `npm test` in `web/` and record the summary line
   verbatim - the "Test Files N passed (N)" and "Tests M passed (M)" numbers. This is the baseline
   AC1 compares against. If it cannot be run, this lane stops here and says so; every other criterion
   leans on it.
2. Also at HEAD: run `npm audit` and record the output verbatim, so the "0 vulnerabilities" claim
   after the change has a measured before. I could not run it in this session, so the item's "5
   vulnerabilities (4 moderate, 1 critical)" is inherited, not verified.
3. Edit the three version ranges, `npm install`, inspect the lock diff for the expected departures
   (`esbuild`, `@esbuild/*`, `vite-node`) and arrivals (`rolldown`, `lightningcss` and their platform
   packages).
4. `npx tsc -b`, `npm test`, `npm run build`, `npm audit`, `npm ls esbuild`, then the full
   `make test-e2e`.
5. Open the Playwright screenshots. They are artifacts, not assertions; nobody else will.

## Acceptance criteria

1. `npm test` collects **exactly the same number of test files and the same number of tests** as the
   baseline recorded in step 1, and all pass. The baseline is a measured number from this branch's
   HEAD, not the 162/1234 estimate in this spec and not the item's 152/1116. If the estimate and the
   baseline disagree, the baseline wins and the discrepancy is worth one sentence in the commit
   message.
2. `web/vite.config.ts` still spreads `configDefaults.exclude` rather than replacing it, and the
   comment above it describes vitest 4's actual defaults rather than vitest 2's.
3. `npm run build` succeeds, and `relay-server` rebuilt after it serves the SPA - which is what
   `make test-e2e` proves end to end, since a stale or placeholder embed makes every spec fail.
4. `make test-e2e` is green on both the `chromium` and `webkit` projects, and a human has opened the
   `layout.spec.ts` screenshots for the run.
5. `npx tsc -b` is clean with `web/tsconfig.json`'s `include` still covering `src`, `e2e` and
   `playwright.config.ts`, and `"node"` still in `types`. The addendum's claim that `"node"` widens
   nothing is **re-measured**, not inherited: remove it, observe whether `tsc -b` still passes, put it
   back, and record which way it went in the commit message.
6. `npm audit` reports 0 vulnerabilities, or the remainder is enumerated with a reason each one is
   accepted. `npm ls esbuild` reports esbuild absent from the tree.
7. `web/package.json` moved exactly three lines: `vite`, `vitest`, `@vitejs/plugin-react`. No other
   dependency version changed. Any additional change is named and justified against the specific
   failure that demanded it.
8. The verification records the Node version it ran on, and that version satisfies both
   `^20.19.0 || >=22.12.0` (vite 8) and `^20 || ^22 || >=24` (vitest 4).
9. `web-ci` is green on the PR, with `npm ci` succeeding on ubuntu against a lockfile generated on
   Windows.
10. The working tree carries no rebuilt `web/dist` (see Constraints).
11. `docs/backlog/feature-2026-06-05-upgrade-vite-vitest.md` is closed with `/backlog close`, which
    `git mv`s it to `docs/backlog/closed/`. Flipping `status` in place is not closing it.

## Constraints

**`web/dist` must not be committed.** `.gitignore` ignores `web/dist/` with a single exception for
`web/dist/index.html`, which is a tracked placeholder. Every build in this lane's verification
overwrites that one tracked file with an index referencing hashed assets nobody else has - and this
lane runs more builds than most, since the bundler itself changed. `make test-e2e` restores it on
exit, pass or fail, but a bare `npm run build` does not. **Before assembling the PR, run
`git checkout -- web/dist/` and confirm the diff contains no `web/dist` entry.** `web/dist` is not
maintained per PR; it is stale in `main` by design.

**CRLF and encoding.** `package-lock.json` is a large machine-generated file on a CRLF repo. After
the regeneration, check the diffstat against the change intended and confirm `git ls-files --eol`
reads `i/lf` for every touched path.

## Escalations

Calls a human might reasonably make the other way:

- **Do it in two steps.** vite 5 to 7 with vitest 3 first, then 8 and 4. This halves the size of any
  single unexplained e2e change, at the cost of two migrations and two lockfile churns, and it does
  not clear the advisories on its own. I chose one step because the peer graph is clean: vitest 4
  accepts vite 8 directly and plugin-react 6 requires it, so the intermediate stop buys bisection
  granularity rather than compatibility.
- **Take the node floor now.** Bundling the sibling CI item means one workflow edit instead of two,
  and this lane has to run `web-ci` anyway. I kept it out because bumping three action majors is a
  change whose correctness cannot be established from the same evidence as a dependency bump, and a
  wrong action major breaks CI for every branch.
- **Accept a bundle-size increase without investigation.** Lightning CSS lowers syntax more
  aggressively than esbuild did, so the CSS may grow. I have not set a size budget and would not
  block on one, but a human may want a number recorded either way.
- **Split the vitest config out (D4) while the file is open.** Reasonable housekeeping; I declined
  because it moves the exact block AC2 guards, in the same commit that changes its semantics.
- **Decide the migration is not worth it.** The exposure is dev-only, the critical advisory needs a
  server no script starts, and Vite 8 is a bundler swap. A human could reasonably defer. The
  counter-argument is that the surface only grows: the e2e harness already had to be carried rather
  than preceded, per the item's own 2026-08-24 addendum.
