---
title: Audit log for privileged admin-console actions
type: feature
status: open
updated: 2026-08-12
created: 2026-06-26
priority: low
source: ROADMAP deep-refresh gaps pass (2026-06-26)
---

# Audit log for privileged admin-console actions

## Summary
The Admin console will expose several privileged actions that are recorded nowhere: invite creation,
admin password reset, and worker disable/enable/revoke. This is the broad sibling to the narrower
archive/unarchive audit item - capturing all of these in one audit trail.

**Second constituency added 2026-08-12:** three security iterations have now deferred *detection* to
this item, and the deferrals are written into the code they came from. See "Deferred to this item"
below. That is worth recording on the receiving item rather than only in the deferring comments,
because a pointer is only useful in the direction someone will actually read it.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps pass; filed as a separate broad item (per the user's
choice) rather than widening the narrow archive/unarchive item, so the small item stays shippable and
this one frames the general audit table.

## Deferred to this item (2026-08-12)

The 2026-08-12 epoch-fence hardening family - the task-log assignee fence, the task-status assignee
fence, and the retry/terminality guard - closed three write paths by making rejected writes affect
zero rows. Every one of those iterations chose a **silent drop** on rejection, deliberately and for
the same three reasons: the rejection is the control and is complete on its own; a log line would be
caller-controlled volume on the gRPC recv goroutine with no rate limiter (see
[[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] for what that costs); and there is no sink -
`internal/metrics` is a utilization ring buffer, not a counter registry.

The consequence, stated plainly so this item's scope is not surprised by it later:

- **`handleTaskStatus` suppresses `pgx.ErrNoRows` at both write sites.** The
  `IncrementTaskRetryCount` branch and the `UpdateTaskStatus` branch are each wrapped in
  `if !errors.Is(err, pgx.ErrNoRows) { log.Printf(...) }`, so a status update rejected by the
  identity, currency or terminality fence produces **no log line, no metric, no event and no trace**.
- `handleTaskLog` does the same for a rejected log chunk.
- **Forged, stale and duplicate are deliberately indistinguishable** at these sites, so even if a
  signal existed it would not classify itself.

So a compromised-but-enrolled agent probing the fences - the residual threat model all three specs
converge on - is currently invisible. That is an accepted trade, not an oversight, and the code says
so: the rationale comments at those branches name **this item** as where detection belongs. Whoever
builds the audit table should treat "agent-ingest fence rejections" as a first-class source
alongside the admin actions, or explicitly decide it is out of scope and say where it goes instead -
because today the deferral has nowhere else to land.

Two notes for whoever picks it up:

- The **volume** problem does not disappear by moving the sink. A durable table written from the recv
  goroutine on attacker-controlled input is a worse flood target than a log line, not a better one.
  Any design has to answer aggregation (a counter per worker per interval, not a row per rejection)
  before it answers schema.
- The **classification** problem is genuinely unsolved at the SQL layer: distinguishing forged from
  stale costs either a second round trip or the "zero rows means no side effect" structural guarantee
  the epoch-fence invariant leans on. An audit design that wants to record *which* predicate rejected
  will have to pay for it somewhere, and that cost belongs in the audit spec rather than being
  smuggled into the ingest path.

## Proposal
Introduce an audit table (actor, action, target, timestamp, metadata) and write to it from the
privileged handlers: invite create, admin password reset, worker disable/enable, agent-token/worker
revoke, and user archive/unarchive (folding in the narrow item's scope). Expose a read path for the
Admin Server/overview tab if useful. This subsumes the archive/unarchive item once the table exists.

Decide, as part of that design, whether agent-ingest fence rejections are in scope (see "Deferred to
this item"). They have a different actor model (a worker identity, not a user), a different volume
profile (attacker-driven, unbounded, on a hot goroutine) and a different consumer (a security
reviewer, not an admin auditing a colleague), so "one table, two shapes" is a real risk and the
answer may well be a separate aggregated counter rather than rows in this table.

## Acceptance / Done When
- An audit table exists and the listed privileged actions write to it.
- A documented read path (or at least queryable storage) for admins.
- The narrow archive/unarchive item is closed as covered, or explicitly kept as the first slice.
- The agent-ingest fence-rejection deferral is either satisfied or explicitly re-homed, and the
  rationale comments in `internal/worker/handler.go` that point here are updated to match whichever
  it is.

## Related
- Broad sibling of [[idea-2026-05-06-audit-log-archive-unarchive]]
- Surfaces in [[feature-2026-06-26-admin-console-pages]] (the actions to audit)
- Detection deferred here by: `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md`
  (section 7), `docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md` (section 3.3),
  `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md` (section 3.3)
- Volume constraint on any recv-goroutine sink: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]]
- Source: `internal/api/auth.go:359-420` (password reset), `internal/api/invites.go`,
  `internal/api/workers.go:424-564` (disable/enable), `internal/api/agent_enrollments.go:227-243`
  (token revoke), `internal/worker/handler.go` (`handleTaskStatus`'s two `pgx.ErrNoRows` branches and
  `handleTaskLog`'s, all three of which name this item)

## Notes
Lower priority than the UI build-out; do when an audit table is actually warranted, but tracked so it
is a deliberate decision rather than an omission. The 2026-08-12 addition does not by itself argue
for raising the priority - the deferral is accepted and the exposure it leaves is observability, not
correctness - but it does mean this item now has two independent constituencies, and a future
priority review should weigh both.
</content>
