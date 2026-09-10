---
title: InventoryRowRejections exists but no operator can read it
type: idea
status: open
created: 2026-09-10
priority: medium
source: Review of the worker-workspace inventory bounds slice, which deferred the section and named an item that did not exist
---

# InventoryRowRejections exists but no operator can read it

## Summary

`Handler.InventoryRowRejections()` counts workspace-inventory rows `inventoryUpsertParams`
refused - over a column's byte bound, carrying a NUL, or with a `last_used_at` that cannot be
stored - and nothing outside tests calls it. A refusal is deliberately never logged either:
`handleInventoryUpdate` returns on `errUnstorableInventoryRow` **before** it reaches
`lim.allow`, and `applyInventory` drops the row and continues. So a refused row currently
produces **zero** observable server-side output.

## Context

Deferred at spec time rather than overlooked, and the field's own comment says so - "NOT YET ON
GET /v1/server/counters - the section is deliberately deferred to its own item." **This is that
item.** It is filed because the comment named it and it did not exist: a decision conditioned on
future work is unfalsifiable until the work is findable.

The deferral had a second reason worth recording: the slice ran beside three sibling lanes, and
publishing would have pulled `internal/api` and `cmd/relay-server` into a lane that otherwise
touched neither.

**The consequence is bounded but real.** A refused row means that workspace is absent from the
coordinator's inventory, so the dispatcher's warm scoring never prefers the agent that actually
holds it and tasks on that stream always dispatch cold. README's `stream` row documents that
consequence; nothing reports that it has happened.

**Read the accessor's own warning before designing the section.** An authenticated agent moves
this number at will, one increment per inventory entry per message, and it is attributable to
"some agent" and no further. The remedy is to find which agent is sending malformed inventory -
never to raise a bound, which is exactly what an agent driving the number would want. That
sentence has to survive into wherever the number is READ, not only where it is computed.

## Proposal

Add the counter to `GET /v1/server/counters`, following the five existing sections.

Settle two things the shape raises:

- **Whether it shares a section with the enrollment counters or gets its own.** Both are
  worker-supplied refusal counts deferred for the same reason; see the sibling item.
- **What the section says about attributability.** A per-process total that any agent can drive
  is a triage signal, not an alert threshold, and the payload should not invite an operator to
  treat it as one.

## Acceptance / Done When

- The counter is published, admin-only, alongside the existing sections.
- The payload guards cover the new section - note the guards there are proven against fixtures
  rather than producers.
- The "NOT YET ON GET /v1/server/counters" comment on `inventoryRowRejects` is deleted once it
  is false.
- The remedy sentence - find the agent, never raise the bound - lands where the number is read.

## Related

- `internal/worker/handler.go` (`inventoryRowRejects`, `InventoryRowRejections`,
  `handleInventoryUpdate`), `internal/worker/inventory_params.go`, `internal/api/server_counters.go`
- [[idea-2026-08-25-publish-enrollment-refusal-counters]] - the same shape, deferred the same way;
  the two should settle on one answer about sections
- [[idea-2026-08-24-counter-payload-guards-check-fixtures-not-producers]]
- `docs/superpowers/specs/2026-09-10-worker-workspace-source-key-length-bound.md`
