---
title: Surface revoked workers for admin audit or re-enrollment
type: idea
status: closed
created: 2026-06-04
priority: low
source: noticed while excluding revoked workers from the workers-stats endpoint (PR #12)
---

# Surface revoked workers for admin audit or re-enrollment

## Summary
Revoked workers are now invisible in the entire operational view (excluded from GET /v1/workers and its count, and from GET /v1/workers/stats). If an admin ever needs to audit or re-enroll a revoked worker, there is no surface for it. May warrant a dedicated "revoked/decommissioned" view later.

## Context
Came out of the workers-stats endpoint work. To keep the fleet counts consistent, revoked workers were excluded from both the list endpoint and the stats aggregate, so they no longer appear anywhere a user or admin can see. This is the right default for the operational view, but it removes the only place a revoked worker was previously visible.

## Proposal
A dedicated admin-only listing (e.g. `GET /v1/workers?status=revoked` or a separate `/v1/workers/revoked`) and/or a "Decommissioned" tab in the web UI, so revoked workers can be reviewed and potentially re-enrolled rather than being silently dropped from view.

## Related
- `internal/store/query/workers.sql` - where revoked is excluded from the list/count queries.
- `internal/api/workers.go` - `WorkerStatusCounts` / stats handler.
- `docs/retros/2026-06-04-workers-stats-endpoint.md` - Open Questions.
- `docs/superpowers/specs/2026-06-04-workers-stats-endpoint-design.md`
