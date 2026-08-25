---
title: Handler.pool has no seam, so the default lane structurally cannot drive a successful worker registration
type: idea
status: closed
created: 2026-08-24
closed: 2026-08-25
resolution: fixed
priority: medium
source: 2026-08-24 finishregister-strand slice - the mechanical cause of that slice's three-round guard problem
---

# Handler.pool has no seam, so the default lane cannot drive a successful worker registration

## Summary

`applyInventory` opens a transaction on the concrete `*pgxpool.Pool` **unconditionally** - no
`h.pool == nil` guard, no short-circuit on an empty inventory. It sits between
`reconcileRunningTasks` and the `RegisterResponse` send inside `finishRegister`, so a fixture built on
the `store.DBTX` interface panics there and **everything below it is unreachable without a real
Postgres**.

The consequence is not one missing test. It is that the entire success path of worker registration -
`registry.Register`, the ownership handoff, `Metrics.Activate`, the online broker event - can only be
exercised behind `//go:build integration`, which CI does not run.

## Context

Measured cost, 2026-08-24: the finishregister-strand slice needed the default lane to pin one line
(`handedOff = true`, which decides which of two deferred releases owns the worker generation). With no
behavioural witness available it paid for a large `go/parser` guard instead - and that guard was
**evaded twice** before it held, each time by a construct that is nil in one context and real in the
other (`h.Metrics != nil`, then `h.pool != nil`). A behavioural test would have caught both on the
first run.

The plan for that slice said to file this when a slice needed it. It did.

## Proposal

Give `Handler` a seam for the pool. Options, cheapest first:

- Narrow the field to an interface carrying the one method `applyInventory` needs, the way
  `terminalTailStore` and `failClaimedStore` were narrowed in `internal/scheduler`. That is the
  established pattern here and it leaves production wiring identical.
- Or inject the inventory step as a function value defaulting to today's behaviour.

**Do not "fix" this by returning early on an empty inventory.** That changes behaviour - an agent
legitimately reporting zero devices would stop clearing stale rows - and `applyInventory` already has
an open defect of its own. The point is a test seam, not a behaviour change.

## Acceptance / Done When

- A default-lane test drives `finishRegister` to a successful return without Postgres.
- The success path's observable effects - sender registered, worker online, metrics activated - are
  asserted somewhere CI executes.
- The handoff guard is then reduced to whatever a behavioural test cannot cover, or retired if it
  covers nothing extra. Do not delete it before the behavioural test exists.

## Related

- `internal/worker/handler.go` - `applyInventory`, `finishRegister`
- `internal/worker/handler_handoff_guard_test.go` - the guard bought instead of this seam
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the lane problem this is the mechanical cause of
- [[bug-2026-08-23-failed-finishregister-strands-worker-online]] - the slice that paid the cost

## Resolution

Fixed. `Handler.pool` is now a one-method `txBeginner` interface, and the default lane drives a
successful worker registration without Postgres for the first time.

All three Acceptance criteria met:

- `TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration` drives `finishRegister`
  to a successful return with no Postgres, through `Connect` rather than directly - so it observes
  BOTH releases and asserts the generation is released exactly once across the connection's life.
- The success path's observable effects are asserted in the lane CI runs: sender registered, worker
  online, metrics activated, dispatch triggered, RegisterResponse actually sent, epoch carried.
- The structural guard is reduced, not retired. Five clauses (G3, G6, G7, G12, G15) were deleted only
  after their behavioural replacements were green, each deletion re-proved by mutation in three
  independent isolated trees. The clauses that survive are the ones no runtime test can see: source
  position, the deferred closure's shape, and the flag's write set.

The mutant that motivated the item - deleting `handedOff = true`, which previously left all 21
packages green - now reddens the default lane four ways.

Two things the item did not anticipate, both recorded in the spec:

- The pool seam alone does not reach a successful `finishRegister`. `GetActiveTasksForWorker` is a
  sqlc `:many` and the existing fake returned a nil `pgx.Rows` with a nil error, panicking one frame
  short of the pool. An `emptyRows` fake was required too.
- One interface covers all three `h.pool` sites, not just `applyInventory`'s - so
  `enrollAndRegister` and `autoEnrollAndRegister` are now fakeable as well. That is the obvious next
  consumer and is filed separately.

`applyInventory`'s behaviour is unchanged, and the forbidden "return early on empty inventory" fix is
now blocked by a test rather than by a comment.

See `docs/retros/2026-08-25-handler-pool-seam.md`.
