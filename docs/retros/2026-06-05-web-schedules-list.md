# Session Retro: 2026-06-05 - Web Schedules List Page

## What Was Built

The Schedules slice of the Relay web front end: the `/schedules` list page (replacing the `JobsPlaceholder` route), built against the real `GET /v1/scheduled-jobs` endpoint, plus a small backend addition exposing `owner_email` on the list response.

The page recreates the `HoloSchedules` design from the design handoff as far as the backend allows: eyebrow + title, a page-scoped ENABLED/PAUSED summary with total, a sort dropdown (8 keys), a 9-column table (name with status dot, cron, tz, overlap chip, next run, last run, last job, owner, actions), inline row actions ("Run now" always visible plus enable/disable), cursor-based prev/next pagination via a client-side cursor stack, a 1s local ticker so relative times stay fresh between 10s polls, and loading/error/empty/inline-action-error states.

Delivered through the full superpowers flow: brainstorming (redirected mid-stream to the design handoff as the source of truth) -> spec -> writing-plans -> subagent-driven development (10 tasks, fresh implementer + spec-compliance and code-quality review each) -> final whole-branch review (READY TO MERGE). New: a `schedules/` feature module mirroring `workers/`, a `GetUserEmailsByIDs` store query, and the `owner_email` response field. 26 new front-end tests (84 total, all green), two new backend integration tests, clean production build.

## Key Decisions

- **The design handoff is the source of truth.** The brainstorm started with a scope question, but the user pointed to `design_handoff_relay_holo/` and its `HoloSchedules` prototype. The rest of the design reconciled that prototype against the actual backend, degrading where the backend lacks data.
- **Scope: list page + two safe mutations.** Run-now and enable/disable (both endpoints already existed). Edit, the schedule detail page, filter chips, and text search were deferred. Matches the Workers-slice precedent of "list page only, mutations/detail later."
- **`owner_email` via batch lookup, not query JOIN.** The 16 paginated list-query variants and the `buildPage`/row-key machinery were left untouched; the handler resolves emails after assembling the page (owner-scoped path reuses the caller's email with no DB call; admin path dedupes owner IDs and runs one `GetUserEmailsByIDs`). This avoided rewriting every generated row type for one display field.
- **Degrade gracefully where the backend cannot feed the design.** Dropped the "FAILED 24h" stat (no aggregate), showed the last job as a plain short id (no link/status dot, since the Jobs detail page does not exist and the response carries no job status), and kept the owner column real via the new `owner_email`.
- **Sort via a dropdown, not Workers' clickable headers.** Most schedule columns are not sortable and "Recently run" (`updated_at`) has no column to click; the dropdown is the prototype's choice and the better fit.
- **"Run now" stays visible on paused schedules.** The backend allows running a disabled schedule; hiding the action would remove a valid operation. We intentionally diverged from the prototype, which only showed it when enabled.
- **10s poll + 1s local tick.** Schedules are low-churn vs. workers' 3s; the relative "next run" countdown is derived client-side from the absolute timestamp, so a fast server poll is unnecessary.

## Problems Encountered

- **A code-review false positive.** The Task 3 reviewer flagged a "Critical" mismatch claiming the Go `scheduledJobResponse` had no `owner_email` field and the TS interface should drop it. Verifying directly showed `OwnerEmail string \`json:"owner_email"\`` at `scheduled_jobs.go:22` (added and double-reviewed in Task 2), and the list handler populating it. The reviewer read a stale or wrong version; the code was correct. Verified before acting rather than implementing the suggested (wrong) change.
- **Build artifact and stray temp file polluting the tree.** `npm run build` overwrote the tracked placeholder `web/dist/index.html` (gitignored except that one file via `!dist/index.html`), and a subagent left a junk `*checkjson.go` file with a mangled Windows-temp-path name. Both were restored/removed to keep the tree clean; the build output was deliberately not committed (the repo commits a placeholder and builds at release).
- **Subagent commit attribution.** Implementer subagents (running Sonnet) appended `Co-Authored-By: Claude Sonnet 4.6` to their commits rather than the configured Opus co-author line. Harmless, but worth noting the per-subagent attribution drift.

## Known Limitations

- See [`bug-2026-06-05-owner-email-lookup-errors`](../backlog/bug-2026-06-05-owner-email-lookup-errors.md) - owner_email lookup errors are swallowed silently
- See [`bug-2026-06-05-formatrelativetime-duplicated`](../backlog/bug-2026-06-05-formatrelativetime-duplicated.md) - formatRelativeTime duplicated across workers and schedules modules

## Open Questions

- See [`idea-2026-06-05-schedule-detail-page`](../backlog/idea-2026-06-05-schedule-detail-page.md) - Schedule detail page and Edit action
- See [`idea-2026-06-05-last-job-link-status`](../backlog/idea-2026-06-05-last-job-link-status.md) - LAST JOB column as a link with run-status dot
- See [`idea-2026-06-05-failed-24h-stat`](../backlog/idea-2026-06-05-failed-24h-stat.md) - FAILED 24h summary stat (needs a failed-runs aggregate)
- See [`idea-2026-06-05-schedules-filter-search`](../backlog/idea-2026-06-05-schedules-filter-search.md) - Server-side filter and search for the Schedules list
- See [`idea-2026-06-05-schedules-stats-endpoint`](../backlog/idea-2026-06-05-schedules-stats-endpoint.md) - Fleet-wide schedules stats endpoint for the page-scoped summary strip

## Improvement Goals

- The Workers-slice contract-verification lesson held again: the final reviewer checked the TS `Schedule` type field-for-field against the Go `scheduledJobResponse`, and no contract bug shipped. Keep doing this every slice.
- When a code-review finding contradicts work that was already verified, re-verify against the live code before acting. The Task 3 false positive would have caused a wrong "fix" if taken at face value.

## Files Most Touched

- `web/src/schedules/SchedulesPage.tsx` (+168) - composition: sort/cursor/pending state, summary strip, sort select, footer pagination, 1s ticker, loading/error/empty/action-error states.
- `web/src/schedules/SchedulesTable.tsx` (+79) - presentational 9-column grid with per-row run-now and enable/disable.
- `web/src/schedules/api.ts` (+53) - `Schedule`/`SchedulesPage`/`ScheduleSort` types and `listSchedules`/`runScheduleNow`/`setScheduleEnabled`.
- `internal/api/scheduled_jobs.go` (+40) - `owner_email` field and the `fillOwnerEmails` batch-lookup helper wired into both list branches.
- `internal/api/scheduled_jobs_owner_email_integration_test.go` (+55) - admin multi-owner and owner-scoped owner_email coverage.
- `internal/store/users.sql.go` (+32) / `internal/store/query/users.sql` (+3) - generated `GetUserEmailsByIDs` batch query.
- `web/src/schedules/format.ts` (+28) - pure helpers: `formatRelativeTime`, `nextRunDisplay`, `shortId`.
- `web/src/schedules/useScheduleActions.ts` (+20) - run-now and enable/disable mutations that invalidate the list.
- `web/src/schedules/useSchedules.ts` (+15) - the polled query hook (10s interval, keepPreviousData, cursor in key).
- `web/src/app/router.tsx` (+2/-1) - mounted `SchedulesPage` at `/schedules`.

## Commit Range

264350bd562270ae9d825f9ac521182b16d37e64..629dcca579e6fdb626f075c6d3f8d2c50556e7ec
