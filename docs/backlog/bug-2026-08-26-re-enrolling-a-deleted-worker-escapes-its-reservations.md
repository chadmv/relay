---
title: Re-enrolling a deleted worker mints a new id, so every reservation that fenced it silently stops
type: bug
status: open
created: 2026-08-26
priority: medium
source: 2026-08-26 worker-delete slice - security lens; README covers only the empty-array sub-case
---

# Re-enrolling a deleted worker mints a new id, so every reservation that fenced it silently stops

## Summary

Reservations are an **exclusion** control: `reservedIDs` (`internal/scheduler/dispatch.go:185-191`) is
consulted only as `if reservedIDs[uuidStr(w.ID)] { continue }` (`:221`). A reservation naming worker W
keeps W **out** of general dispatch - an isolation fence.

Delete a fenced machine and re-enroll it on the same hostname and it gets a **new UUID**. It is
therefore not in the reservation, and a machine deliberately fenced out of general dispatch is back in
the general pool running arbitrary tenants' jobs - with no signal to anyone.

## Repro / Symptoms

1. Reservation R names `{W1, W2}`, fencing both out of general dispatch.
2. Admin deletes W1. `RemoveWorkerFromReservations` scrubs its id, leaving R naming `{W2}`.
3. The machine re-enrolls on the same hostname. `UpsertWorkerByHostname` / `InsertWorkerForAutoEnroll`
   both mint a fresh id.
4. R still names `{W2}` and looks perfectly healthy in `GET /v1/reservations`. The re-provisioned
   machine is in general dispatch.

Nothing logs it, nothing counts it, and the reservation is not empty so no existing check notices.

## Context

README currently covers only the sub-case where the array empties: "it then reserves nothing, and
nothing says so." The multi-worker case is worse - it looks healthy - and is uncovered.

The scrub itself is correct and was verified: `array_remove` removes every occurrence, counts the
reservation once, and cannot make a reservation exclude *more* workers. The gap is one step later, at
re-enrollment.

## Proposal

Documentation is the floor:

- Extend README's `relay workers delete` limitations to say that **every** reservation naming the
  deleted worker must be re-pointed by hand after re-enrollment, whether or not its array emptied,
  because the machine returns with a new identity.
- Include the affected reservation ids - not just the count - in the delete's log line, so the
  operator has the list to re-point. `RemoveWorkerFromReservations` would become a `:many` with
  `RETURNING id`.

Worth considering separately, and probably its own item: reservations keyed on hostname or label
rather than uuid would not have this failure mode at all. That is a design change, not a fix.

## Acceptance / Done When

- README states that re-enrollment produces a new identity and that reservations must be re-pointed.
- The delete's log line names the reservations it changed.
- The claim that reservations are exclusion-only is stated where a reader of `reservations.worker_ids`
  will find it, since the failure depends on it.

## Related

- `internal/scheduler/dispatch.go:185-191, 221`, `internal/store/query/reservations.sql`
- `internal/api/workers.go` (`handleDeleteWorker`)
- `docs/retros/2026-08-26-worker-delete.md`
