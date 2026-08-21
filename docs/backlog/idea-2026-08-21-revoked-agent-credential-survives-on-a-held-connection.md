---
title: A revoked agent credential survives on a held gRPC connection, and the gRPC and SSE halves should agree on one staleness tolerance
type: idea
status: open
created: 2026-08-21
priority: medium
source: Section 5.1 of the 2026-08-20-grpc-admission-bounds spec, which scoped MaxConnectionAge out and named this as its own item
---

# A revoked agent credential survives on a held gRPC connection, and the gRPC and SSE halves should agree on one staleness tolerance

## Summary

Revoking a worker's agent token does not reach a connection that is already established.

`handleDeleteWorkerToken` (`internal/api/agent_enrollments.go`, routed at
`DELETE /v1/workers/{id}/token`) calls `ClearWorkerAgentToken` and returns. That statement is
`SET agent_token_hash = NULL, status = 'revoked', revoked_at = NOW()`. **It does not touch the
`worker.Registry` and does not close the connection's sender.** Nothing re-checks a credential after
registration: authentication happens once, on the first `RegisterRequest`, and every later message on
that stream is fenced on the task's `assignment_epoch` and `worker_id`, never on the worker's status.
`MaxConnectionAge` is deliberately unset, so no timer ends the connection either.

The connected agent therefore keeps running the tasks it already holds, keeps writing their task logs
and statuses, and keeps its registry slot until it disconnects for some other reason.

**What DOES stop immediately, so this item is not overstated:** new dispatches. `ClearWorkerAgentToken`
sets `status = 'revoked'`, `internal/scheduler/dispatch.go` skips any worker that is not `online` or
`stale`, and nothing restores `online` while the stream is live. README states this correctly today.

**This is the same defect [[idea-2026-08-09-sse-revoked-token-keeps-streaming]] describes on the HTTP
side**, on a different transport with a different registry and a different cost, and the two should be
decided together. That is the entire reason this is filed as a sibling rather than folded in.

## Repro / Symptoms

1. Enroll an agent and let it register and pick up a long-running task.
2. As an admin, `DELETE /v1/workers/{id}/token` (or `relay workers revoke`).
3. Observed: the agent's stream stays open. It continues to stream task logs for its held task and
   reports its terminal status normally, and both writes are accepted, because they are fenced on the
   assignment rather than on the worker's status. The registry still holds its sender, so a cancel or
   an evict command dispatched to that worker id still reaches it.
4. Expected, under any reading of the word "revoke": the credential stops working within a stated,
   documented bound.

The operator remedy today is documented in README: disable or delete the worker and confirm it has
gone offline, noting that deleting the row destroys assignments and reservations.

## Context

Named by `docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md` section 5.1 while scoping
`MaxConnectionAge` **out** of the admission slice, with the reasoning that is worth keeping:

- `MaxConnectionAge` **is not an admission control.** It bounds how long an already-admitted connection
  lives. It ended up in the source item's Proposal list because it happens to bound this, which is a
  different defect.
- Its cost is real and the admission slice could not price it as a security decision. A forced GOAWAY
  costs at most one dropped log chunk per reconnect (`runSender` drops the one in-flight message on a
  stream drop and deliberately does not re-enqueue), a `connection_epoch` bump, and an offline/online
  pair on the events broker. It requeues nothing, because `finishRegister` cancels the grace timer far
  inside the 2m window. At a 30m age over a 100-agent fleet that is roughly 3.3 forced reconnects per
  minute, **each able to lose a log chunk**. That is a product decision about log fidelity.
- **Defaulting it off would satisfy nothing.** A knob that ships disabled bounds nothing and would let
  a slice claim a control it does not exercise.

**Filed as an `idea` rather than a `bug`, matching its SSE sibling, and the call is deliberate.** The
behaviour is a conscious consequence of authenticating once on a long-lived stream, it is documented
in README rather than being a silent surprise, and the load-bearing half of revocation (new dispatch)
does work immediately. The reason it is medium rather than low is that "revoke" is advertised as a
control and an operator reasonably reads it as one.

## Proposal

To be argued at spec time, and **the two transports should be decided in one sitting** even if they
ship separately.

**The gRPC side has a mechanism the HTTP side does not, and it should be priced first.**
`worker.Registry` already holds every connected sender and already supports identity-checked removal
(`UnregisterIf`), and `workerSender` already has a `Close`. A `Registry.CloseIfPresent(workerID)`
called immediately after the `ClearWorkerAgentToken` write is precise, immediate, and needs no timer
and no polling. Settle:

- **Identity-checked teardown applies.** A close must only tear down the sender it means to. Between
  the DB write and the close, a reconnect could legitimately register a *new* sender for that worker
  id - though with the token now NULL that reconnect will fail `GetWorkerByAgentTokenHash`, which is
  worth confirming rather than assuming. Whatever the answer, `CloseIfPresent` must not clobber a
  registration it does not own, and CLAUDE.md's rule is the governing one.
- **It is per replica.** A multi-replica deployment has the agent connected to exactly one server, and
  the revoking HTTP request may land on another. So the registry close is a **fast path**, not a bound:
  something must still make the guarantee fleet-wide. That is what makes the staleness-tolerance
  question unavoidable rather than optional.
- **The other revocation paths.** `handleDisableWorker` deliberately keeps the connection (disable is
  reversible and is not revocation) and must stay that way. Deleting a worker, and any future path that
  clears a token, need the same treatment or the fix is again a property of one handler.

**Then the tolerance, which is the part both items share.** Options, cheapest first, and the answer
should be **one number stated once** rather than two mechanisms picked independently:

- **Re-validate periodically** on each held connection or stream. Bounded staleness, one query per
  interval per connection. This is the option that works across replicas.
- **Cap connection lifetime** (`MaxConnectionAge` on gRPC; a lifetime cap on SSE). Simple, no
  per-connection auth query, and it churns every consumer. On the gRPC side it costs a log chunk per
  reconnect, priced above.
- **A revocation signal.** Most precise, most work, and it needs care not to couple token lifecycle to
  the events broker.

## Acceptance / Done When

- A worker whose agent token is revoked mid-connection stops being able to write task logs and statuses
  within a stated, documented bound, proven by an integration test that is RED against today's code.
- The bound is a **single stated tolerance** that both this and
  [[idea-2026-08-09-sse-revoked-token-keeps-streaming]] are measured against, or the decision that the
  two transports get different numbers is written down with its reason.
- Whatever closes the connection is **identity-checked**: it can never tear down a sender registered by
  a different connection, proven by a test that races a revoke against a reconnect.
- The per-replica versus fleet-wide semantics of the fast path are documented. A registry close is not
  a bound on its own.
- `handleDisableWorker` still keeps the connection open, proven by a test, so the fix does not quietly
  turn disable into revoke.
- README's revoked-agent paragraph is updated. Its current text is precise about today's behaviour
  ("Revocation does not reach a connection that is already established ... Relay sets no maximum
  connection age") and is exactly the prose this work falsifies.
- No new lock, goroutine, queue or round trip on the gRPC recv path.

## Related

- Source: `internal/api/agent_enrollments.go` (`handleDeleteWorkerToken`),
  `internal/store/query/workers.sql` (`ClearWorkerAgentToken`, `GetWorkerByAgentTokenHash`),
  `internal/worker/registry.go` (`Register`, `UnregisterIf`, and the absence of any close),
  `internal/worker/sender.go` (`workerSender.Close`), `internal/worker/handler.go`
  (`authenticateAndRegister`, which runs exactly once), `internal/api/workers.go`
  (`handleDisableWorker`, which must NOT change)
- **The same defect on the other transport, to be decided together:**
  [[idea-2026-08-09-sse-revoked-token-keeps-streaming]]
- The slice that scoped `MaxConnectionAge` out and named this:
  `docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md` section 5.1,
  `docs/retros/2026-08-21-grpc-admission-bounds.md`
- Adjacent, on identity over a held stream: [[bug-2026-08-12-auto-enroll-hostname-takeover]]
- README's current written-down behaviour: the relay-agent section's revocation paragraph

## Notes

The spec that named this recommended filing it as `bug-2026-08-20-revoked-credential-survives-on-a-held-connection`
covering both transports and "superseding or absorbing" the SSE item. **Filed as an `idea` and as a
sibling instead, and both departures are deliberate:**

- **`idea`, not `bug`**, because the admission slice subsequently shipped a README paragraph making
  this documented behaviour with a stated operator remedy, and because its sibling on the HTTP side is
  an idea. A pair of items describing one defect should not disagree about whether it is a defect.
- **Sibling, not absorption**, because the SSE item's acceptance criteria, source files, revocation
  paths and candidate mechanisms are all different, and absorbing it would silently widen an open
  item's Done-When - the test this project applied on 2026-08-15 and again on 2026-08-20. The
  unification requirement lives here, as an acceptance criterion, which gets the same outcome without
  editing the other item's scope.

The rule this pair exists to record: **authenticating once at connect makes every later message's
authorization a statement about the past.** The epoch and assignee fences on `task_logs` and
`tasks.status` are correct and complete for what they check - they establish that the caller is the
task's current assignee - and they are structurally incapable of noticing that the assignee's
credential was destroyed thirty seconds ago. Nothing on a long-lived connection re-asks the question
the middleware asked once.
</content>
