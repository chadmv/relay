---
title: worker_workspaces.source_key is agent-supplied, unbounded, and inside a primary key
type: idea
status: closed
created: 2026-09-04
priority: medium
source: Recommended by the sync-spec exclusion paths design (2026-09-04)
closed: 2026-09-10
resolution: fixed
---

# worker_workspaces.source_key is agent-supplied, unbounded, and inside a primary key

## Summary
`applyInventoryUpdate` and the registration-time bulk ingest pass the agent's `source_key`
straight to `UpsertWorkerWorkspace`, and the column sits inside the table's primary key. Nothing
bounds its length.

## Context
Found while specifying sync-spec exclusion paths
(`docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md`, section 6.1). That design
deliberately chose a short fixed-width key format partly to stay clear of this, which means the
underlying absence of a bound stays untested rather than being fixed.

This is the same shape as the unvalidated hostname reaching a unique btree: a value that arrives
over the wire and lands in an index, where an over-long value fails the index rather than
conflicting.

## Related
- [[bug-2026-08-25-hostname-is-unvalidated-and-reaches-a-unique-index]] - the same shape, already filed
- [[feature-2026-09-04-implement-sync-spec-exclusion-paths]] - the design that routes around this
- `internal/worker/` (`applyInventoryUpdate`), `internal/store/query/` (`UpsertWorkerWorkspace`)

## Resolution

Fixed in PR #209, squashed as `ef86c877`, which also closed
[[bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory]].

`inventoryUpsertParams` in `internal/worker/inventory_params.go` is the sole production route to
`store.UpsertWorkerWorkspaceParams`, verified by a shape search across the tree with exactly one
deliberate test bypass. It bounds in BYTES, not runes, so `octet_length()` in the CHECK and `len()` in
Go are the same measure: `source_type` 64, `source_key` 512, `short_id` 128, `baseline_hash` 128, plus
a NUL refusal and a `last_used_at` storability check. Migration `000024` adds four matching
`CHECK ... NOT VALID` constraints.

**This item's title scopes the fix narrower than its own diagnosis.** A second index nobody named,
`worker_workspaces_lookup_idx (source_type, source_key, baseline_hash)`, is left failable by bounding
`source_key` alone - so the quantity that must fit one btree entry is the SUM of three columns, not any
one of them. All four agent-supplied columns are enumerated and bounded.

**The harm model implied by the item is backwards, and correcting it decided the design.** A bad row
already failed the batch, and that IS the defect: returning from `applyInventory`'s closure rolls the
transaction back, `ReplaceWorkerInventory`'s DELETE with it, so the worker keeps its previous rows. The
real harm is a permanent self-sustaining desync - an agent reporting the same bad row every registration
never updates its inventory again while the dispatcher keeps warm-scoring it. So a refused row is
dropped and the batch commits.

**`sourcekey.go`'s "KEEP IT SHORT" comment reads as a bound it is not.** Its guard pins the composite
form's OVERHEAD at 20 bytes, not the key's length, and `validateSourceSpec` bounds no length on `stream`
or any sync path - so the bound was not implied elsewhere, as this item's Context suggests.

**"Same shape as the hostname item" is half right, and the differing half decides the mechanism.**
Hostname is pre-auth and its refusal must not become an oracle. This value arrives from an already
authenticated worker and every statement is scoped to a `workerID` resolved at registration and never
read off the wire, so the blast radius is the sender's own rows. That is what makes a silent drop
acceptable here and would not on a shared table. Shared rule, different mechanism.

**Measured, and it refuted the plan's own instrument.** The btree entry limit is 2704 bytes on
PostgreSQL 16.13, confirming a figure this item's sibling had quoted and never verified. The plan
prescribed reading the limit off the error from a 12800-byte value, but that fails at `INDEX_SIZE_MASK`
(8191) during index-tuple formation, BEFORE the btree page check - overstating headroom threefold. The
sum 64+512+128 = 704 leaves about 3.8x. No p4 environment was reachable, so the escalation rule
(revisit if a real stream exceeds 300 bytes) did not fire and 512 is recorded as anchored top-down only.

The migration is `NOT VALID` deliberately: a validated CHECK scans existing rows and FAILS the startup
migration, so one planted over-long row would be a server that will not boot after upgrade - a control
whose deployment the attacker can deny in advance. The legacy violating set drains itself because every
reconnect runs `ReplaceWorkerInventory`. The DELETE arm deliberately has no bound, since it binds these
values as comparison keys rather than index tuples, and bounding it would put a row stored before the
bound existed out of reach of the agent's own per-row delete.

Review found the suite did not prove the case it exists to prove: the at-bound test maxed only
`source_key`, so it proved 526 bytes fit rather than 704. All four columns are now maxed in one row and
read back. The values are generated incompressible, because `strings.Repeat` of one character is
TOAST-compressible - measured, with a three-column row at 3000 bytes inserting fine from repeated text
and failing from incompressible text.

Refused rows are counted into `InventoryRowRejections`, which is deliberately not published; see
[[idea-2026-09-10-publish-inventory-row-rejection-counter]], filed because the comment deferring to it
named no findable item.
