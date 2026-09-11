---
title: Settle the worker hostname unique index in one pass - validation and the delete-reclaim race
type: feature
status: open
created: 2026-09-10
priority: medium
source: Roadmap Refresh 48 suggested scoping the pair as one piece of work; both were already in Now and both touch one index
---

# Settle the worker hostname unique index in one pass - validation and the delete-reclaim race

## Summary

Two open bugs concern the same unique index on `workers.hostname`, from the two directions a value can
reach it: one is that the value itself is unvalidated and unbounded, the other is that the index
releases a name at an attacker-observable instant when a worker is deleted. This item owns both so
they are scoped, decided and shipped together rather than as two passes over one index.

It adds no new defect. Its two children carry the diagnoses; what lives here is the shared decision
and the reason the two must agree.

## Context

**Why one piece of work rather than two.** The children are not duplicates and neither blocks the
other - either is shippable alone - so no `blocked_by` edge is warranted between them. What makes them
one body of work is that they must settle on the SAME answer about one index, and the cost of
disagreeing is paid twice: two migrations or constraint changes, two passes over an index that is on
the pre-auth registration path, and two chances for the validation rule and the reclaim rule to
contradict each other.

**The precedent they were waiting on now exists, and that is what makes this schedulable.** Roadmap
Refresh 47 paired the validation child with `worker_workspaces.source_key` and said the two should
settle on one answer. That slice shipped in [#209](https://github.com/chadmv/relay/pull/209) and
settled it:

- Bound in **BYTES**, not runes, so the Go check and any database `CHECK` are the same measure.
- **Refuse, never transform.** The value is an identity; truncating or stripping a byte collapses two
  distinct entities onto one key.
- **Drop the offending row, do not fail the batch**, where a batch exists.
- Enforce at a **single constructor** that is the sole route to the params the statement binds.
- Migration as `CHECK ... NOT VALID`, because a validated CHECK scans existing rows and fails a
  startup migration, which lets planted data deny the control's own deployment.

**The one axis that precedent does NOT cover is the reason this needs its own thinking.**
`source_key` arrives from an already-authenticated agent, so its refusal is scoped to the sender's own
rows and a silent drop is acceptable. `hostname` arrives **pre-authentication**. A refusal there is
reachable by anyone who can open a connection, so it must join `msgAuthFailed` and must not become an
enumeration oracle - the refusal cannot reveal whether a hostname is taken, bounded, or malformed.
That is a different design question with the same mechanism underneath.

**The delete-reclaim child interacts with whatever validation chooses.** `autoEnrollAndRegister` takes
no row lock, so its `ON CONFLICT DO NOTHING` insert waits on the deleting transaction at the unique
index and succeeds the instant the delete commits. If validation adds a constraint or changes how the
insert conflicts, the race's shape changes with it - so the two cannot be reasoned about separately
even though they can be shipped separately.

## Proposal

Sketch only; the children carry the diagnoses.

Decide, in this order, because each answer constrains the next:

1. **What the bound is and where it is enforced**, following the `source_key` precedent unless there is
   a reason not to - and say the reason if so.
2. **What a pre-auth refusal discloses.** This is the new question. The answer has to hold for a
   malformed hostname, an over-long one, and one already claimed, or it is an oracle.
3. **Whether the delete-reclaim window is closed or documented.** The delete child notes that the two
   safe sequences already exist, so documentation-first is a legitimate outcome - but it should be
   decided after 1 and 2, not before.

## Acceptance / Done When

- Both children are closed, or any child left open says explicitly which of the three decisions above
  covered it and why it was not closed.
- The validation answer is stated against the `source_key` precedent: the same where it is the same,
  and an explicit reason wherever it differs.
- A pre-auth refusal does not distinguish malformed from over-long from already-claimed, and a guard
  pins that.
- Only one pass is made over the unique index.

## Related

- `internal/worker/handler.go` (`autoEnrollAndRegister`, `enrollAndRegister`),
  `internal/store/migrations/` (the `workers.hostname` unique index)
- [[idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key]] - closed; the
  precedent the validation answer follows, and the one axis it does not cover
- [[bug-2026-08-13-cursor-value-kind-not-validated]] - an empty hostname is how an agent forces the
  cursor defect there, so validation here narrows that item's reachable input
