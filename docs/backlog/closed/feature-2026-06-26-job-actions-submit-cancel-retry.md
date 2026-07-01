---
title: "Job write-actions: submit (New Job), cancel / force-cancel, retry"
type: feature
status: closed
created: 2026-06-26
priority: high
source: ROADMAP web-frontend deep review against design_handoff_relay_holo (2026-06-26)
closed: 2026-07-01
resolution: fixed
---

# Job write-actions: submit (New Job), cancel / force-cancel, retry

## Summary
The SPA currently has no job write-actions. The Holo design shows a "+ New job" button (a
job-spec submit form) on the jobs views and graceful + force cancel on job detail. Wiring these
is core to making the jobs UI operational rather than read-only.

## Context
Surfaced by the 2026-06-26 `/roadmap web-frontend deep` review against `design_handoff_relay_holo/`.
Backend contract in the handoff README plus `reference/screens/{jobs-list,job-detail}.js`.

## Proposal
- **Cancel / force-cancel** - `DELETE /v1/jobs/:id` (`?force=true` for force). Backend exists;
  frontend-only. Lives in the job-detail header.
- **Submit ("+ New job")** - `POST /v1/jobs` with a job-spec body. Backend exists. The spec editor
  (YAML/form) is a non-trivial surface; consider scoping it as its own slice.
- **Retry** - `?task=failed|all`. The handoff lists `POST /v1/jobs/:id/retry`, but that route
  **does not exist yet** - needs a new backend endpoint first. Retry re-opens terminal jobs, which
  reactivates the latent [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]].

## Acceptance / Done When
- Job detail exposes graceful cancel and force-cancel with confirmation, wired to `DELETE /v1/jobs/:id`.
- A "+ New job" submit flow creates a job via `POST /v1/jobs` with validation/error handling.
- Retry is implemented once `POST /v1/jobs/:id/retry` lands; the jobs-stats updated_at proxy bug is
  resolved or explicitly accepted as part of that work.

## Related
- Design: `design_handoff_relay_holo/reference/screens/{jobs-list,job-detail}.js`
- Cancel/force-cancel live in the header of [[idea-2026-06-05-job-detail-page-row-click]]
- Retry depends on a new backend route and ties to [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]]
- Source: `internal/api/jobs.go`, `web/src/jobs/`

## Notes
Cancel + submit are frontend-only (endpoints exist); only retry is backend-blocked. The submit
form (job-spec editor) may warrant splitting into its own item once scoped.

## Resolution
Shipped the cancel slice: graceful cancel + force-cancel on the job-detail header, wired to
DELETE /v1/jobs/{id}[?force=true] (feature commit 37cb190, autopilot iteration 3, 2026-07-01
job-cancel-actions). One useJobActions mutation (force as a call-site arg), three-key
invalidation (['job',id] + ['jobs'] + ['job-stats']), owner-or-admin UI gate, terminal-state
button hiding (done/cancelled), and 409 error surfacing. Full web suite green (266 tests),
production build clean, code review CLEAN with mutation-tested non-vacuous assertions.

The omnibus item is decomposed; the two remaining sub-features are carved to their own items:
- [[feature-2026-07-01-job-submit-new-job-form]] (the "+ New job" submit form, FE-ready).
- [[feature-2026-07-01-job-retry-action]] (retry, blocked on the POST /v1/jobs/{id}/retry route
  in [[feature-2026-06-26-web-enabler-backend-endpoints]] plus the jobs-stats-24h and
  retry-resurrects-cancelled-task bugs).

Design: docs/superpowers/specs/2026-07-01-job-cancel-actions-design.md;
plan: docs/plans/2026-07-01-job-cancel-actions-plan.md.
