---
title: Enrolling with an in-use hostname takes over that worker's identity and locks out the real agent
type: bug
status: open
created: 2026-08-12
priority: medium
source: Phase 4 review of the task-log assignee-fence iteration (2026-08-12)
---

# Enrolling with an in-use hostname takes over that worker's identity and locks out the real agent

## Summary
`UpsertWorkerByHostname` (`internal/store/query/workers.sql:56-68`) is
`INSERT ... ON CONFLICT (hostname) DO UPDATE ... RETURNING id`, so when the hostname already exists
it returns the **existing** worker's id rather than creating a new row. Both enrollment paths then
call `SetWorkerAgentToken` (`workers.sql:70-81`) on that id, which overwrites `agent_token_hash`,
clears `revoked_at`, and resets a `revoked` status to `offline`.

The consequence is that naming an in-use hostname is not "joining the pool as a new worker", it is
**seizing an existing worker's identity**: the legitimate agent's token stops working at its next
reconnect (`GetWorkerByAgentTokenHash` will not find it), and the attacker inherits that worker's
registry slot, its task assignments and its reservations.

`autoEnrollAndRegister` (`internal/worker/handler.go:256-305`) looks like it guards this - it takes
`GetWorkerByHostnameForUpdate` inside the transaction and rejects `status == 'revoked'` - but that is
the only status it rejects. An `online` worker with a live agent and running tasks is not blocked.

## Repro / Symptoms
With `RELAY_ALLOW_AUTO_ENROLL` set, open a `Connect` stream to `:9090` with no credential and a
`RegisterRequest` naming the hostname of a real, currently-connected worker. The server upserts onto
that worker's row, issues a fresh agent token bound to it, and `finishRegister` registers the new
sender under the same worker id. The real agent's next reconnect fails `Unauthenticated`, and it has
no way to distinguish this from an ordinary token revocation.

Two follow-on effects worth calling out:
- `finishRegister` calls `reconcileRunningTasks` (`handler.go:374-415`) with the reported
  `running_tasks`. An attacker that reports the victim's live task at its current epoch keeps that
  task assigned rather than having it requeued - and it is now assigned to a worker identity the
  attacker controls. **It thereafter passes the new `AppendTaskLog` assignee fence legitimately**,
  as the task's genuine assignee.
- Conversely, an attacker that reports nothing gets every one of the victim's active tasks requeued
  in a single connect, which is a cheap availability attack on the fleet.

The **enrollment-token path shares the same reachability, with a twist**: `enrollAndRegister`
(`handler.go:165-233`) calls the same `UpsertWorkerByHostname` and has **no revoked check at all** -
not even the one auto-enroll has. So a holder of a valid, unconsumed, unexpired enrollment token
intended for a new machine can point it at an existing hostname and take over that worker, including
a previously revoked one, since `SetWorkerAgentToken` explicitly revives `revoked` to `offline`.
That path requires an admin-issued one-shot credential, so it is a much narrower exposure, but it
should be considered in the same fix rather than left as a surprise.

## Context
Pre-existing; not introduced by the 2026-08-12 assignee-fence work, which is simply where it was
found. Two of the three Phase 4 review lenses (security and invariants) independently flagged it,
because the shipped spec's threat model (section 5) originally said an auto-enroll attacker picks an
**unused** hostname. That claim is wrong and it understated auto-enroll's exposure; the spec now
carries a dated correction saying so.

The honest counter-argument, which is why this is filed at medium rather than high: auto-enroll is
**off by default**, and its documented trust model is network reachability. The design spec
(`docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md`) explicitly names per-host
allowlisting as a non-goal, and [[idea-2026-06-04-cidr-allowlist-auto-enroll]] records the deferral.
Under a trust model of "anyone who can reach the gRPC port may be a worker", an attacker who can
already register as *some* worker gains task dispatch anyway.

The argument for filing it as a **bug** rather than a documentation item is that the failure mode
exceeds what that trust model grants:

- The trust model says "a new host may join the pool". It does not say "a joining host may
  invalidate an existing host's credential". The token overwrite is a silent, persistent lockout of
  a legitimate agent, and it survives disabling auto-enroll.
- The code already asserts intent to gate hostname reuse. `autoEnrollAndRegister` takes a `FOR
  UPDATE` lock specifically to inspect the existing row's status before upserting. The guard exists
  and covers one case out of the interesting ones, which reads as an oversight rather than a
  decision.
- The lockout is indistinguishable, from the agent side, from an ordinary revocation - see
  `docs/retros/2026-06-05-agent-stale-token-diagnostics.md` for the diagnostics that path already
  needed.

## Proposal
Refuse enrollment when a worker row for that hostname already holds a credential. Concretely: in
`autoEnrollAndRegister`, after the existing `GetWorkerByHostnameForUpdate`, reject when
`existing.AgentTokenHash` is non-NULL, with a distinct gRPC status and a server log line naming the
hostname and the remote address. That leaves the legitimate cases working - a brand new host (no
row), and a re-enrolling host whose token was cleared by revocation (`ClearWorkerAgentToken` sets
`agent_token_hash = NULL`) - while closing takeover of a worker that currently has a usable
credential.

Before implementing, settle these:

- **Does that break legitimate re-enroll?** An agent that lost its state directory but kept its
  hostname would today silently re-enroll and keep its identity; under this change it would be
  refused until an admin revokes the old worker. That is a real operational regression and needs a
  deliberate answer - probably "yes, and revoke-then-re-enroll is the supported route", but it must
  be documented in README and surfaced in the agent's error message, not discovered in the field.
- **The enrollment-token path.** Same `UpsertWorkerByHostname`, no revoked check. Decide whether an
  enrollment token may bind to an existing hostname at all. Note it consumes a one-shot
  admin-issued credential, so the answer may legitimately differ from the auto-enroll answer.
- **Check `GetWorkerByHostnameForUpdate` covers the race.** The auto-enroll lookup and upsert are
  already in one transaction with `FOR UPDATE`; confirm any new predicate sits inside it, and that
  the enrollment path gets equivalent treatment rather than a check outside its transaction.

## Acceptance / Done When
- Auto-enroll naming a hostname whose worker row has a non-null `agent_token_hash` is refused, and
  the existing worker's `agent_token_hash` is unchanged afterwards, proven by a test that is RED
  against today's code.
- A revoked worker's hostname still re-enrolls (`agent_token_hash` is NULL after revocation), so
  the legitimate recovery path is not broken.
- A brand new hostname still auto-enrolls.
- The enrollment-token path's behavior is either matched or deliberately documented, with a test
  pinning whichever was chosen.
- The refusal is logged with hostname and remote address; the refused caller gets no information
  about whether the hostname exists beyond the refusal itself.
- README's auto-enroll section states the new rule and the revoke-then-re-enroll recovery.

## Related
- Adjacent and deferred from the same design: [[idea-2026-06-04-cidr-allowlist-auto-enroll]]
- Source: `internal/worker/handler.go` (`autoEnrollAndRegister`, `enrollAndRegister`,
  `finishRegister`, `reconcileRunningTasks`), `internal/store/query/workers.sql`
  (`UpsertWorkerByHostname`, `SetWorkerAgentToken`, `GetWorkerByHostnameForUpdate`,
  `ClearWorkerAgentToken`, `GetWorkerByAgentTokenHash`)
- Design: `docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md` (Non-Goals),
  `docs/retros/2026-06-04-auto-enroll-mode.md`
- Threat-model correction that surfaced this:
  `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` section 5
- Adjacent: [[bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero]] - an attacker holding a
  seized identity passes both epoch fences legitimately, so identity takeover is upstream of every
  per-task check

## Notes
The interaction with the just-shipped assignee fence is the reason this is worth writing down rather
than leaving as folklore. That fence is a real and complete fix for cross-task forgery by an identity
the attacker does not control, and it is explicitly **not** a defense against an attacker who
controls the assignee identity itself. Auto-enroll's trust model is the only control on the latter,
which makes tightening it the load-bearing half of the pair rather than a nice-to-have.

If the decision goes the other way and this is accepted as within auto-enroll's stated trust model,
then it should still land as a documentation change: the trust model needs to say out loud that
enabling auto-enroll means any reachable host can seize any worker identity by hostname, because
that is a materially stronger statement than "any reachable host can become a worker".
