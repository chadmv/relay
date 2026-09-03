---
date: 2026-09-03
topic: web-batch-two
branch: claude/agitated-bhabha-50c514
range: f2326be79e9028e0b1f9e38479f8457b382a22ee..ed3cdc0dcf42a9770e1250e2c8bb67232ad7a65a
---

# Session Retro: 2026-09-03 - The Second Web Batch

**TL;DR:** The roadmap's web theme still had twenty-two open items at its top after the first batch,
so this session ran all of them as nine streams of work, backend enablers first, then the pages that
needed them, with at most five streams active at once. Every one shipped and was merged: the jobs
page gained a search box, a My jobs toggle and a timeline view over new server-side filters; the
schedules page gained a stats strip, search and a link to each schedule's last job; the worker page
gained its reservations panel; the job page gained a draggable split; the new-job page gained a
structured form that renders into the JSON it submits; tables and dialogs got their accessibility
fixes; and the build tooling moved up two major versions. Review found real defects this time, not
only weak tests: a timeline that could never finish loading on a slow connection, a search whose
pager disabled itself forever after a trailing space, a form whose edits could be lost when two
landed together. Each was found by re-reviewing a fix round's own diff, and each fix round opened a
smaller hole that the next re-review caught.

## Handoff

Nine PRs, all squash-merged to `main`, final `e1a9b7a`: [#177](https://github.com/chadmv/relay/pull/177)
lane T (vite 8, vitest 4, plugin-react 6; `fakeTimers.toFake` pinned to the vitest 2 list; closes
`feature-2026-06-05-upgrade-vite-vitest`), [#178](https://github.com/chadmv/relay/pull/178) lane JB
(`q`/`mine`/`since`/`until` on `GET /v1/jobs`; `parsePage` parses the query once with NUL and
malformed-escape 400s; `rejectRepeatedParams`/`rejectNulBytes` in `internal/api/pagination.go`;
migration 000023 added then dropped; no closes), [#179](https://github.com/chadmv/relay/pull/179) lane TB
(caret `aria-hidden`, TasksTable per-row button with `aria-current`, table names from panel titles,
GlassPanel frames; closes the four `idea-2026-08-09-table-*`/`sort-caret`/`tasks-table-grid-role`
items), [#180](https://github.com/chadmv/relay/pull/180) lane SB (`GET /v1/scheduled-jobs/stats` in one
statement, `last_job_status` via `fillLastJobStatuses`, `?enabled`/`?q` on schedules through the shared
`parseFilterQ` in `internal/api/list_filters.go`, `?worker_id` on reservations, `authResponse.user`; no
closes), [#181](https://github.com/chadmv/relay/pull/181) lane DL (`dialogShellIsSole.guard.test.ts` over
`web/src/test/sourceTree.ts`, which strips comments through the TypeScript parser's comment ranges after
a regex and a lexer each let a producer through; layering scale in `tokens.css`; native dialog closed
wontfix-for-now with a conjunctive trigger; closes the four dialog/layering items),
[#182](https://github.com/chadmv/relay/pull/182) lane SF (SchedulesSummary strip, LAST JOB link with
the status word, chips and search, pager disabled while `qInput.trim() !== q`; closes
`failed-24h-stat`, `last-job-link-status`, `schedules-filter-search`, `schedules-stats-endpoint`),
[#183](https://github.com/chadmv/relay/pull/183) lane MF (`usableUser` guard on non-empty `id`,
`created_at`, `email` and boolean `is_admin`; RegisterScreen on `useServerConfig`; reservations panel on
`?worker_id=`; `useSplitWidth` writes `--relay-split` and `aria-valuenow` to the DOM during a drag and
commits React state once from `finish()`; closes `worker-detail-reservations-panel`,
`login-return-user-object`, `registerscreen-config-fetch-unify`, `job-detail-resizable-split`),
[#184](https://github.com/chadmv/relay/pull/184) lane JF (stable key `['job-timeline', window, q, mine]`,
the queryFn captures its own quantized anchor and returns its bounds, `refetchInterval: ANCHOR_STEP_MS`,
`lastSuccess` ref plus `TimelineState.window`, `cancelQueries` cleanup, `useDebouncedPagingGuard` in
`web/src/lib`, `usePersistedChoice`; closes `job-search-box-q-filter`, `my-jobs-toggle-mine-filter`,
`jobs-timeline-view`, `extract-usepersistedview`), [#185](https://github.com/chadmv/relay/pull/185) lane
FB (`specBuilder.ts`, `SpecBuilderForm` with functional-updater dispatchers and per-batch
`removedThisTick`/`addedThisTick` refs, `React.memo` rows with narrowed props, `job-new-builder` e2e
surface; closes `idea-2026-07-01-job-spec-form-builder` with the Perforce block carved out to
`feature-2026-09-03-perforce-source-builder-in-the-new-job-builder`). Specs and plans are on `main`
under `docs/superpowers/{specs,plans}/2026-09-02-web2-<lane>-*.md`. The fan-in on this branch closed
`idea-2026-05-06-list-endpoint-filters` with its remainder re-filed as
`idea-2026-09-03-list-filters-remainder-status-labels-users-q`, filed twenty-three follow-ups (two high:
`feature-2026-09-03-server-side-bound-for-text-search`, `bug-2026-09-03-react-router-production-advisories`;
four medium), annotated four items, and refreshed ROADMAP.md (Refresh 40; open count 173 -> 175; both
highs enter Now). Lane engineers took two actions outside their worktrees that the user should know
about: the TB engineer dropped and recreated the local `relay` dev database during its layout pass
(`relay_e2e` untouched), and the MF engineer killed a pre-existing `relay-server.exe` from an earlier
session while cleaning up. The e2e harness is now serialized across worktrees with a mkdir-atomic lock
at `%LOCALAPPDATA%\Temp\claude\e2e-lock.d`; the `make test-e2e` recipe needs `OS`, `TEMP`, `TMP` and the
Go cache variables as make command-line assignments (documented in `web/e2e/README.md`; two runs failed
before starting when they were exported instead). Next session starts at ROADMAP.md's Now:
`bug-2026-09-03-react-router-production-advisories` is a lockfile bump, and
`feature-2026-09-03-server-side-bound-for-text-search` shares its keying decision with the rate-limit
lead.

## What Was Built

- **Backend enablers first.** Lane JB put four filters on `GET /v1/jobs` with the query string parsed
  once at `parsePage`, at-most-once and NUL guards on every paged parameter, and three text-count
  statements so the unfiltered count does not pay for a `JOIN users`; an owner index was added, measured
  unused by the list statement, and dropped. Lane SB put the schedules stats in one statement, resolved
  `last_job_status` at list, get, create and PATCH through one helper, shared the `q` parsing with JB
  byte-for-byte on the 400 text, and returned the user object from login.
- **The pages that needed them.** Search, My jobs and a 6h/24h/7d timeline on Jobs; a stats strip, chips,
  search and a LAST JOB link on Schedules; a reservations panel on the worker page; a resizable split on
  the job page; a structured builder on the new-job page that refuses an import it cannot represent
  rather than dropping fields.
- **Guards and primitives.** A tree-walking guard that the dialog shell is the only dialog producer;
  a layering scale; table accessible names derived from panel titles and pinned structurally; a shared
  debounce-window paging guard; a shared persisted-choice hook; the tooling upgrade that everything
  else was built on.

## Key Decisions

- **Dependency order, then a cap of two raised to five.** Lanes T (tooling), JB and SB (server filters
  and stats) ran before the pages that consume them; TB, DL and FB were independent. The user raised
  the parallel cap from two to five mid-session; lanes shared nothing but the e2e harness, which got a
  lock.
- **The umbrella filter item closed at the fan-in, not in a lane**, with its unshipped remainder
  re-filed narrowly so the closed parent does not read as fully delivered.
- **Review agents moved to sonnet while the default model was overloaded.** Fable and Opus returned
  529 for over twenty-five minutes; the scoped re-verifies that ran on sonnet reproduced every finding
  they reported with a scratch test and read library source (TanStack's query core) rather than
  asserting from memory.
- **The timeline hook was redesigned in a fix round rather than patched.** A key that included a
  ticking anchor could never finish a walk slower than one tick; the fix made the key stable, moved the
  anchor into the fetch, and moved refresh onto `refetchInterval`, which the query core continues
  rather than restarts while a fetch is in flight.
- **Three things were filed instead of changed:** the split's `pointercancel` committing the mid-drag
  position (documented as deliberate, decided by nobody), the timeline drawing created-time rather than
  active-time, and the Perforce source block in the builder.

## What Went Wrong and What Changes

Ledger: every entry in the prior retro was promoted except one. The unpromoted one ("a spec rejected a
hi-fi treatment it had not read") was not exercised: lane JF refuted three hi-fi claims by measurement
rather than rejecting a mechanism unread. Promoted lessons that fired this session:
[[feedback_resume_agents_after_a_rate_limit_kill]] (four session-limit kills, every agent resumed from
its transcript against observed worktree state, none re-dispatched fresh);
[[feedback_verify_tree_not_subagent_claims]] (every gate and every close re-checked by the conductor
before each PR; two of the conductor's own resolution notes were found wrong the same way, see below);
[[reference_a_kill_must_name_its_guard]] (every re-verify named the branch its mutation reddened);
[[reference_tailwind_scans_prose]] (a fresh instance in HoloShell's comments, filed);
[[feedback_backlog_proposal_not_contract]] (lane T's inherited audit count of five measured at eleven;
the FB item's dialog framing refuted); [[reference_correcting_a_uniqueness_claim]] (extended below);
[[feedback_relay_the_input_not_just_the_number]] (extended below);
[[feedback_assert_encoding_after_a_programmatic_edit]] (extended below).

- **Every fix round opened a smaller hole, and only the scoped re-verify on the round's own diff found
  it.** JB's NUL fix covered one parameter; DL's comment stripper took a regex, a lexer and finally the
  parser; SB's fixture move lost the owner-scope arm's power; JF's first round redesigned the timeline
  and left the guard comparing raw against trimmed; FB's second round fixed the dispatchers and left the
  three side effects beside them reading the same stale snapshot. Fourteen fix rounds across nine lanes,
  each verified, and the third-round findings were all the first finding's shape at a neighbouring site.
  -> **What changes:** when a finding names a shape (a stale read beside a functional update, a guard on
  one parameter, a comparison of unlike values), the fix brief and the re-verify brief both say "find
  every site with this shape in the file", and a re-verify's opening question is what the NEW code still
  mishandles, never whether the named site is fixed. Promotion: extend
  [[reference_backstop_recreates_the_defect]] with this trigger.

- **A fix round's comment cleanup wrote fresh policy violations, three times.** JF deleted a uniqueness
  claim from a surfaces comment and replaced it with a re-measurement narrative; MF's DOM-direct drag
  comment carried its own render count; FB's render-count test renumbered a "this was 50" to "this was
  20" while editing it. Each was written while fixing prose the policy had just flagged.
  -> **What changes:** a fix round's rewritten comments are authored prose and get the Comments policy
  pass on their added lines before commit, the same as new code; the re-verify brief lists the policy
  explicitly for the comments the round touched. Promotion: extend
  [[reference_correcting_a_uniqueness_claim]] (its "rewriting prose regenerates claims" is the same
  mechanism; the trigger here is a review-driven cleanup).

- **Two commits claimed to fix an escape and neither did.** Writing the six-character escape for an e with an acute accent into a doc: perl
  consumed the backslash, sed changed nothing, the Edit tool decoded the escape into a raw rune, and
  only a quoted-heredoc byte replacement in Python produced the six ASCII bytes. Each layer between the
  intent and the file treated the backslash differently, and `git ls-files --eol` and the diffstat were
  clean throughout.
  -> **What changes:** when an edit must land a backslash escape or a specific non-ASCII byte, verify
  the bytes with `od -c` on the written line before the commit message claims anything, and prefer a
  byte-level write over any tool that interprets escapes. Promotion: extend
  [[feedback_assert_encoding_after_a_programmatic_edit]] with the escape-bearing trigger.

- **An engineer's "green" was measured under an invocation the reviewer's default did not reproduce.**
  FB reported 166 files green; the re-verify's three default `npx vitest run` invocations each timed out
  the same render-count test under parallel contention, and it passed in isolation. The claim and the
  refutation were both true of different invocations.
  -> **What changes:** a suite-green claim in a report names its invocation (command, pool, isolation,
  repeat count), and a reviewer who cannot reproduce green under the default invocation reports that as
  a finding, not as a flake. Promotion: extend [[feedback_relay_the_input_not_just_the_number]].

- **Two of the conductor's resolution notes described the item's proposal, not the shipped code.** The
  My jobs toggle was written up as persisted (it is page state) and the extracted hook was named from
  the item's title (`usePersistedView`; the code says `usePersistedChoice`, shared with the Workers
  page). Both were caught by reading the code a minute later and corrected in a follow-up commit.
  -> **What changes:** a resolution note is written from the diff (grep the symbol, read the state
  declaration), never from the item's title or proposal, and the close commit is not made until the
  note's every noun has been seen in the tree. Promotion: extend [[feedback_backlog_housekeeping]].

- **Two engineers acted on shared local state outside their worktrees.** The TB engineer dropped and
  recreated the local `relay` dev database while setting up a populated page; the MF engineer killed a
  `relay-server.exe` left by an earlier session while cleaning up. Neither was destructive to the
  repo, both were reported, and neither brief had said anything about it.
  -> **What changes:** engineer briefs state that anything outside the worktree (databases other than
  the lane's own e2e database, processes the agent did not start, global npm or Go caches) is
  off-limits without asking, and that any such action is reported in the first line of the report.
  Promotion: `docs/agent-team/README.md`, engineer dispatch checklist.

- **Two lanes fixed the same defect independently on the same day.** SF's and JF's finder passes each
  named the debounce-window cursor race; SF fixed it inline on SchedulesPage and merged, JF extracted a
  shared hook afterwards, and the schedules page now has an item to adopt it. The conductor had both
  finder reports in hand and recorded the overlap without routing it.
  -> **What changes:** when two parallel lanes' reviews name the same finding, assign the fix to one
  lane before either fix round is dispatched, and brief the other to adopt it after that lane merges;
  the fan-in notes are where the overlap is spotted, so read them before dispatching fix rounds, not
  after. Promotion: new feedback memory.

- **Concurrent e2e runs corrupted the shared harness.** Two lanes' `make test-e2e` runs overlapped;
  both drop and recreate `relay_e2e` and bind ports 8091 and 9091, so one run's server answered the
  other's specs and both reported connection refusals.
  -> **What changes:** every engineer brief that may run the browser suite carries the mkdir-atomic
  lock protocol, and `web/e2e/README.md` documents it beside the recipe. Promotion: `web/e2e/README.md`
  (project-specific; already applied to the briefs this session).

- **The default model was overloaded for half an hour and the pipeline stalled on review.** Re-dispatching
  the read-only review agents on sonnet kept the lanes moving; their re-verifies held up, and the
  engineers stayed on the default model.
  -> **What changes:** when review agents die on repeated 529s for more than a few minutes, re-dispatch
  the read-only lenses on sonnet with the same brief rather than waiting; keep engineers on the default
  model unless they are also blocked. Promotion: new feedback memory.

- **The e2e recipe failed twice before running.** Exporting `TEMP` and the Go variables into the
  invoking shell does nothing for the MSYS make's recipe subshells; the README already prescribes
  command-line assignments and the conductor did not read it before the first run.
  -> No process change - the guard exists in `web/e2e/README.md`; read the recipe section before
  invoking it from a new shell.

- **A lane's commits carried the wrong trailer and rewriting history was refused.** `git filter-branch`
  was blocked; the squash merge drops branch trailers, so nothing reached `main` wrong.
  -> No process change - one-off; squash merges make branch trailers cosmetic.

## Recommended Backlog Items

Everything this session surfaced was filed at the fan-in; the list below is the intake record, not a
priority order (ROADMAP.md Refresh 40 orders it).

- See [`feature-2026-09-03-server-side-bound-for-text-search`](../backlog/feature-2026-09-03-server-side-bound-for-text-search.md) - the ~26x database-CPU amplifier behind `?q=` has no server-side bound.
- See [`bug-2026-09-03-react-router-production-advisories`](../backlog/bug-2026-09-03-react-router-production-advisories.md) - five advisories in the served bundle, fix inside the existing range.
- See [`idea-2026-09-03-cursor-carries-no-filter-fingerprint`](../backlog/idea-2026-09-03-cursor-carries-no-filter-fingerprint.md) - a cursor minted under one filter is accepted under another on every list endpoint.
- See [`bug-2026-09-03-scheduled-job-id-branch-ignores-status`](../backlog/bug-2026-09-03-scheduled-job-id-branch-ignores-status.md) - two parameters accepted together, one silently dropped.
- See [`bug-2026-09-03-nul-and-malformed-query-guard-covers-parsepage-only`](../backlog/bug-2026-09-03-nul-and-malformed-query-guard-covers-parsepage-only.md) - five handlers still read the query string directly.
- See [`bug-2026-09-03-run-now-does-not-advance-last-job-id`](../backlog/bug-2026-09-03-run-now-does-not-advance-last-job-id.md) - the LAST JOB cell points at the previous scheduled fire after a manual run.
- See [`idea-2026-09-03-list-filters-remainder-status-labels-users-q`](../backlog/idea-2026-09-03-list-filters-remainder-status-labels-users-q.md) - the unshipped part of the closed umbrella.
- See [`feature-2026-09-03-perforce-source-builder-in-the-new-job-builder`](../backlog/feature-2026-09-03-perforce-source-builder-in-the-new-job-builder.md) - the half of the builder cut from lane FB.
- The seventeen low items (accessibility gaps, the shared focus token, the e2e dialog spec, the orphan
  layer rules, the pointercancel decision, the created-versus-active timeline window, the pg_trgm and
  owner-statement measurements, the client exposure of the filters, the vitest 4 default, two
  comment-policy leftovers, the stats endpoints, the README source table) are listed in ROADMAP.md
  Refresh 40 and placed in their theme sections.

## Files Most Touched

- `internal/store/query/jobs.sql`, `internal/store/query/scheduled_jobs.sql` and their generated
  `.sql.go` - the filtered list, text-count and stats statements (JB, SB).
- `internal/api/pagination.go`, `internal/api/job_filters.go`, `internal/api/list_filters.go` - one
  query parse, the guards, the shared `q` contract.
- `web/src/jobs/JobsPage.tsx`, `useJobTimeline.ts`, `JobsTimeline.tsx`, `timelineGeometry.ts` and
  their tests - the filters and the redesigned timeline (JF).
- `web/src/jobs/specBuilder.ts`, `SpecBuilderForm.tsx`, `TaskRowFields.tsx`, `NewJobPage.builder.test.tsx`
  - the structured builder and its three fix rounds (FB).
- `web/src/test/sourceTree.ts`, `web/src/components/dialog/dialogShellIsSole.guard.test.ts` - the
  parser-based sweep guard (DL).
- `web/src/schedules/SchedulesPage.tsx`, `SchedulesSummary.tsx`, `SchedulesPage.filters.test.tsx` - the
  strip, chips and search (SF).
- `web/src/auth/AuthProvider.tsx`, `web/src/jobs/useSplitWidth.ts`, `web/src/workers/WorkerReservationsPanel.tsx`
  - the login user object, the split, the reservations panel (MF).
- `web/e2e/surfaces.ts`, `web/e2e/README.md`, `web/e2e/keyboard.spec.ts` - two new surfaces, the
  split's browser cases, the counts every lane touched.
- `web/package.json`, `web/package-lock.json`, `web/vitest.config.ts` - the tooling upgrade (T).
- `ROADMAP.md` and twenty-five new files under `docs/backlog/` - the fan-in.
