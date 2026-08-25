---
title: Deleting a worker frees its hostname to whoever claims it first, at an attacker-observable instant
type: bug
status: open
created: 2026-08-26
priority: medium
source: 2026-08-26 worker-delete slice - security lens; scoped out deliberately as documentation
---

# Deleting a worker frees its hostname to whoever claims it first

## Summary

The 2026-08-25 guards closed hostname takeover by refusing auto-enrollment for **any** hostname that
already has a `workers` row. `relay workers delete` removes the row, so the hostname becomes claimable
again - that is the feature. The gap is that the operator does not control who claims it, and under
`RELAY_ALLOW_AUTO_ENROLL=true` an attacker can be **parked waiting** for the instant it frees.

## Repro / Symptoms

`autoEnrollAndRegister` (`internal/worker/handler.go`) takes no row lock; it runs
`InsertWorkerForAutoEnroll ... ON CONFLICT (hostname) DO NOTHING`. Against a hostname whose row is
being deleted by an **uncommitted** transaction, Postgres makes that INSERT wait on the deleting xact
at the unique index and then succeed the moment the delete commits.

So a peer already looping registration attempts on the target hostname does not merely race the
legitimate re-provision - it is first in line. And it gets a free signal that the moment arrived: its
`msgAuthFailed` refusals stop, which is the inherent oracle the refusal path already documents.

There is no rate limit on gRPC registration - `internal/api/ratelimit.go` covers login and register
only; the gRPC side has `RELAY_GRPC_MAX_CONNS` and the registration timeout, neither of which slows a
patient attacker.

The operator's motivation makes it sharper: README now presents delete as the remedy for "a machine
re-provisioned in place under auto-enroll", so the intended use is precisely to free a hostname for a
specific machine to re-take.

## Context

Not novel in kind - README already prices auto-enroll as "any host able to reach the gRPC port may
create one persistent `workers` row per distinct hostname it claims". What is new is that a
**specific, previously-claimed, in-use** hostname becomes claimable on admin command, at an instant
the attacker can detect.

Scoped out of the worker-delete slice deliberately, as documentation rather than code, so the
mechanism could be described accurately rather than guessed at.

## Proposal

Documentation first, because the safe sequences already exist:

- Say in README's `relay workers delete` section that under auto-enroll the freed hostname is
  immediately claimable by any peer reaching the gRPC port, and that the delete-then-reboot window is
  a race the operator does not control.
- Give the two safe sequences: re-provision with `relay agent enroll` (an admin-issued token binds
  the identity without depending on winning the race), or perform the delete with auto-enroll off.

Only if that proves insufficient: a short server-side hold on a just-deleted hostname, or a
delete-and-reissue that mints an enrollment token bound to the freed hostname in one step. Both are
larger and neither should be reached for before the doc change is tried.

## Acceptance / Done When

- README states the race, its window, and the two sequences that avoid it.
- The claim is checked against the code rather than reasoned - specifically that the auto-enroll
  INSERT waits on the deleting transaction rather than failing fast.

## Related

- `internal/worker/handler.go` (`autoEnrollAndRegister`), `internal/api/workers.go` (`handleDeleteWorker`)
- [[bug-2026-08-12-auto-enroll-hostname-takeover]] (closed) - the guard delete reopens by design
- [[idea-2026-06-04-cidr-allowlist-auto-enroll]] - would close it as a side effect
- `docs/retros/2026-08-26-worker-delete.md`
