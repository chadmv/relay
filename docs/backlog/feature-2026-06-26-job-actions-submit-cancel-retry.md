---
title: "Job write-actions: submit (New Job), cancel / force-cancel, retry"
type: feature
status: open
created: 2026-06-26
priority: high
source: ROADMAP web-frontend deep review against design_handoff_relay_holo (2026-06-26)
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
