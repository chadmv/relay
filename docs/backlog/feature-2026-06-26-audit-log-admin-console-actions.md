---
title: Audit log for privileged admin-console actions
type: feature
status: open
created: 2026-06-26
priority: low
source: ROADMAP deep-refresh gaps pass (2026-06-26)
---

# Audit log for privileged admin-console actions

## Summary
The Admin console will expose several privileged actions that are recorded nowhere: invite creation,
admin password reset, and worker disable/enable/revoke. This is the broad sibling to the narrower
archive/unarchive audit item - capturing all of these in one audit trail.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps pass; filed as a separate broad item (per the user's
choice) rather than widening the narrow archive/unarchive item, so the small item stays shippable and
this one frames the general audit table.

## Proposal
Introduce an audit table (actor, action, target, timestamp, metadata) and write to it from the
privileged handlers: invite create, admin password reset, worker disable/enable, agent-token/worker
revoke, and user archive/unarchive (folding in the narrow item's scope). Expose a read path for the
Admin Server/overview tab if useful. This subsumes the archive/unarchive item once the table exists.

## Acceptance / Done When
- An audit table exists and the listed privileged actions write to it.
- A documented read path (or at least queryable storage) for admins.
- The narrow archive/unarchive item is closed as covered, or explicitly kept as the first slice.

## Related
- Broad sibling of [[idea-2026-05-06-audit-log-archive-unarchive]]
- Surfaces in [[feature-2026-06-26-admin-console-pages]] (the actions to audit)
- Source: `internal/api/auth.go:359-420` (password reset), `internal/api/invites.go`, `internal/api/workers.go:424-564` (disable/enable), `internal/api/agent_enrollments.go:227-243` (token revoke)

## Notes
Lower priority than the UI build-out; do when an audit table is actually warranted, but tracked so it
is a deliberate decision rather than an omission.
