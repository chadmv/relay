---
title: reg.Hostname is unvalidated and reaches a unique btree, making a pre-auth log site reachable
type: bug
status: open
created: 2026-08-25
priority: medium
source: 2026-08-25 auto-enroll-guards slice - scoped out of that slice deliberately, and it is what makes the new fault log site pre-auth reachable
---

# reg.Hostname is unvalidated and reaches a unique btree, making a pre-auth log site reachable

## Summary

`reg.Hostname` arrives from the wire, is validated nowhere, and is bounded only by gRPC's 4 MiB
receive limit. `workers.hostname` is `TEXT NOT NULL UNIQUE` (`000001_initial.up.sql:25`), an
unconditional btree whose maximum index entry is about 2704 bytes. A hostname above that, or one
containing invalid UTF-8, makes the insert fail in Postgres rather than conflict.

## Repro / Symptoms

With `RELAY_ALLOW_AUTO_ENROLL=true`, connect with no credential and a hostname over ~2704 bytes.
`InsertWorkerForAutoEnroll` fails deterministically, every time.

Consequences, in order of severity:

- **A pre-authentication log site with no per-hostname bound.** `registrationStoreFault`
  (`internal/worker/handler.go`) logs one line per stream on a store fault. That is bounded by
  `RELAY_GRPC_MAX_CONNS` and the registration timeout, but unlike the auto-enroll audit line beside
  it, it has **no** per-hostname bound and no ceiling behind it - the audit line can only fire once
  per hostname ever. The 2026-08-25 slice disclosed this in README rather than closing it, because
  the fix belongs here.
- The agent retries. `internal/agent/agent.go:108` treats only `codes.Unauthenticated` as terminal,
  so `codes.Internal` reconnects on backoff - the peer does not give up.

The schema-disclosure half is already closed: `registrationStoreFault` returns a constant
`codes.Internal, "registration failed"` and `clipID`s both the hostname and the error text, so the
raw Postgres message no longer reaches the peer.

## Proposal

Bound `reg.Hostname` before it reaches any statement - a length cap well under the btree limit, and a
charset rule. Settle:

- **What the limit is.** RFC 1035 says 253 for a DNS name; relay hostnames are operator-chosen labels
  and need not be resolvable. Pick a number and say why.
- **What a refusal returns.** It must stay indistinguishable from every other credential refusal -
  all eleven sites now pass one `msgAuthFailed` constant, held by an AST guard. A new refusal must
  join that set, not sit beside it.
- **Whether it applies to all three registration paths** or only the token-less one.

## Acceptance / Done When

- An oversized or invalid hostname is refused before any statement is issued, proven RED against
  today's code.
- The refusal is indistinguishable from the others and the AST guard's site count is updated
  deliberately rather than incidentally.
- `registrationStoreFault` is no longer reachable by a caller-chosen hostname, and README's
  disclosure of that reachability is corrected.

## Related

- `internal/worker/handler.go` (`autoEnrollAndRegister`, `enrollAndRegister`, `registrationStoreFault`)
- `internal/store/migrations/000001_initial.up.sql:25`
- `internal/worker/refusal_string_guard_test.go` - the guard a new refusal must join
- `docs/retros/2026-08-25-auto-enroll-guards.md`
