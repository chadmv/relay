---
title: Schedule detail shows one next fire; the hi-fi previews five
type: idea
status: open
created: 2026-08-12
priority: low
source: scoped out of the 2026-08-12-schedule-detail-page slice (decision 1), with the constraint that made it a separate slice
---

# Schedule detail shows one next fire; the hi-fi previews five

## Summary

The hi-fi's Next fires panel lists five upcoming fire times, the first highlighted
(`design_handoff_relay_holo/hifi3-holo-pages.jsx:1814-1828`, mapping over a `NEXT_FIRES` array).
The shipped panel shows **one**: the server's own `next_run_at`
(`web/src/schedules/ScheduleDetailPage.tsx:231-244`).

The gap is not laziness and it is not a missing component. `web/package.json`'s runtime
dependencies are exactly six - `@fontsource-variable/jetbrains-mono`,
`@fontsource-variable/space-grotesk`, `@tanstack/react-query`, `react`, `react-dom`,
`react-router-dom` - and **none of them parses cron**. Entries 2..N cannot be computed in the
browser without adding one.

## Context

The constraint that makes this its own slice, stated so it does not have to be rediscovered:

**Any client-side preview is a second implementation of the scheduler's grammar, and it has to
agree with the first one.** The server parses with `robfig/cron/v3` configured as
`cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor`
(`internal/schedrunner/cron.go:14-16`), which accepts standard 5-field cron **plus** descriptors
(`@hourly`, `@daily`, ...) **plus** `@every <duration>`, and evaluates them in an IANA location
loaded by name (`cron.go:33-36`, any name `time.LoadLocation` resolves - not a fixed list).
Agreement has to hold across all three of those, plus DST transitions in the loaded zone, plus the
`ValidateMinInterval` floor of 30s (`internal/api/scheduled_jobs.go:17`, `cron.go:46-61`).

A JS cron library that handles 5-field expressions but not `@every 90m`, or that computes
`@daily` in UTC when the schedule says `America/New_York`, produces a panel that is confidently
wrong. **A preview that silently disagrees with the scheduler is worse than one honest value**,
because the entire purpose of the panel is to let a user trust an expression they just typed. A
single value that is definitionally correct beats five that are probably correct.

Note also that the shipped single value is **not** a degraded placeholder. `PATCH` recomputes
`next_run_at` server-side and returns it in the 200 body
(`internal/api/scheduled_jobs.go:584-596`), so after a cron or timezone save the panel shows the
authoritative first fire of the edit just made. The feature gap is "four more rows", not "any
feedback at all".

## Proposal

Two routes. **The server-side one is recommended**; both are written out so the trade is visible.

**Option A (recommended): compute it server-side with the authoritative parser.**

Add a preview to the existing detail response rather than a new endpoint - e.g. a `next_fires`
array on `scheduledJobResponse` (`internal/api/scheduled_jobs.go:19-34`), or a `?preview=N` query
parameter on `GET /v1/scheduled-jobs/{id}` if always computing it is unwanted. The values come
from repeated `sched.Next(...)` calls on the already-parsed `Schedule`
(`internal/schedrunner/cron.go`), so there is exactly one grammar in the system and disagreement
is structurally impossible. The frontend then renders N rows with no new dependency and no new
logic.

Design questions for that spec:

- **Where it is computed.** `ParseSchedule` already runs inside the PATCH handler; the GET handler
  does not currently parse at all. Parsing on every GET is cheap but non-zero, and the detail page
  polls at 10s per open tab - bound it (small N, hard cap) and decide whether it rides the default
  response or sits behind a parameter.
- **N, and whether the list endpoint gets it too.** The list renders a countdown per row today; N
  fires per row across a 50-row page is a different cost profile from one detail row. Probably
  detail-only.
- **DST and zone edges are the interesting test cases**, not the happy path. A schedule at
  `0 2 * * *` in `America/New_York` across a spring-forward boundary is the assertion worth
  writing; it is also exactly the case a JS reimplementation would get wrong.
- **Whether a human gloss rides along.** The hi-fi also renders an `explainCron(...)` string
  (`hifi3-holo-pages.jsx:1758`) - "every day at 2:00 AM". That is a cron parser by another name,
  so it was scoped out of the page slice for the same reason and folds into this item if it is
  ever wanted. A server-computed gloss has the same single-grammar property.

**Option B: add a JS cron library.** Cheaper to start, and permanently owned. It must be evaluated
against `@every <duration>`, all descriptors `cron.Descriptor` accepts, arbitrary IANA zones, and
DST - and it inherits a standing obligation to be re-checked whenever `robfig/cron/v3` is
upgraded. Adding the seventh runtime dependency to `web/` for a display nicety is the wrong trade
unless the library demonstrably matches on all four axes.

Deliberately **not** proposed: client-side cron *validation*. The server is the validator of record
and its 400 message is already rendered verbatim; a client pre-check is the same second
implementation with a worse failure mode (it rejects expressions the server would accept).

## Acceptance / Done When

- The Next fire panel renders N upcoming fires, the first visually distinguished, matching
  `hifi3-holo-pages.jsx:1814-1828`.
- Every value is produced by the same parser the scheduler uses, or - if Option B - a test proves
  agreement with the Go implementation on a shared fixture set covering 5-field cron, at least two
  descriptors, `@every <dur>`, and a DST boundary in a non-UTC zone.
- A disabled schedule still shows the "paused - no fires queued" state rather than a stale list.
- After a cron or timezone save, the whole list reflects the **new** expression, not the old one
  (today's single value already does, via the PATCH response - do not regress it).
- The comment naming this item at `web/src/schedules/ScheduleDetailPage.tsx:221-230` is removed.

## Related

- Design record: `docs/superpowers/specs/2026-08-12-schedule-detail-page.md` (decision 1, and the
  "Scoped out" table), `docs/retros/2026-08-12-schedule-detail-page.md`
- Source: `web/src/schedules/ScheduleDetailPage.tsx:221-244` (the one-entry panel and the comment
  naming this item), `internal/schedrunner/cron.go:14-16,33-61`,
  `internal/api/scheduled_jobs.go:19-34,584-596`, `web/package.json:13-20`
- Design: `design_handoff_relay_holo/hifi3-holo-pages.jsx:1808-1829` (the panel),
  `:1758` (`explainCron`, folded in above)
- Same page, other gaps: [[bug-2026-08-12-scheduled-job-detail-missing-owner-email]],
  [[idea-2026-08-12-schedule-job-spec-editor]]
- Same list surface: [[idea-2026-06-05-schedules-filter-search]],
  [[idea-2026-06-05-schedules-stats-endpoint]]
- If Option A is taken, it is a backend enabler for a frontend gap and belongs with
  [[feature-2026-06-26-web-enabler-backend-endpoints]]

## Notes

The line worth carrying into whatever spec picks this up: **the panel's product is trust, so
correctness is the feature and the row count is the packaging.** That is why one value shipped
rather than five approximate ones, and it is the reason to prefer the server-side option even
though it is the larger change.
