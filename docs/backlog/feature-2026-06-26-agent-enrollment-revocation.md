---
title: Revoke an unused agent-enrollment token (DELETE /v1/agent-enrollments/{id})
type: feature
status: open
created: 2026-06-26
priority: medium
source: ROADMAP deep-refresh gaps pass (2026-06-26)
---

# Revoke an unused agent-enrollment token (DELETE /v1/agent-enrollments/{id})

## Summary
An admin can create and list agent-enrollment tokens but cannot revoke a still-valid, unconsumed one
(e.g. a leaked token) before it expires. The only delete path is the TTL sweep
`DeleteExpiredAgentEnrollments`. A targeted revoke endpoint is a real admin-safety gap.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps pass. The Admin console's Agent-enrollments tab (just
filed) can create and list enrollments; revoke is the missing third action.

## Proposal
Add `DELETE /v1/agent-enrollments/{id}` (admin-only) plus a `DeleteAgentEnrollment` store query that
removes a single unconsumed enrollment by id. Already-consumed enrollments are immutable history;
deleting an enrollment must not affect the worker it enrolled (that is governed by the worker token,
revoked separately via `DELETE /v1/workers/{id}/token`).

## Acceptance / Done When
- `DELETE /v1/agent-enrollments/{id}` revokes an unconsumed enrollment (admin-only), 404 on unknown id.
- Revoking does not disturb an already-enrolled worker.
- Unit + integration coverage; the Admin enrollments tab can call it.

## Related
- Powers the revoke action in [[feature-2026-06-26-admin-console-pages]] (Agent-enrollments tab)
- Source: `internal/api/server.go:142-143` (POST/GET only), `internal/api/agent_enrollments.go`, `internal/store/query/agent_enrollments.sql:20` (TTL sweep only)
