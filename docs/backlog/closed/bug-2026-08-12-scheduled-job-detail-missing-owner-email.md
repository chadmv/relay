---
title: GET /v1/scheduled-jobs/{id} always returns owner_email "" - the detail handler never calls fillOwnerEmails
type: bug
status: closed
created: 2026-08-12
closed: 2026-08-14
resolution: fixed
priority: medium
source: filed from the 2026-08-12-schedule-detail-page slice, which omitted the owner line entirely because of it
---

# GET /v1/scheduled-jobs/{id} always returns owner_email "" - the detail handler never calls fillOwnerEmails

## Summary

`handleGetScheduledJob` (`internal/api/scheduled_jobs.go:508-519`) writes
`toScheduledJobResponse(row)` straight to the wire and never calls `s.fillOwnerEmails`. **Both**
list arms do call it - the admin arm at `:371` and the owner-scoped arm at `:504`. The response
struct's `OwnerEmail` field has no `omitempty` (`:25`), so the key is always present and its value
is always the empty string on the detail endpoint.

The result is a response that lies quietly: `{"owner_email": ""}` is indistinguishable, to a client,
from a schedule whose owner genuinely has no email. Nothing fails, nothing logs, and the same field
on the same struct is correct one endpoint over.

## Repro / Symptoms

```
GET /v1/scheduled-jobs                 -> items[i].owner_email == "ada@studio.dev"
GET /v1/scheduled-jobs/<that same id>  -> owner_email == ""
```

Two responses built from the same `scheduledJobResponse` type, for the same row, disagree.

The user-visible consequence today is an omission rather than a wrong value, and only because the
SPA was written defensively. `ScheduleDetailPage.tsx` renders the owner line **conditionally** on
`schedule.owner_email` being non-empty, so it is currently never rendered. The alternatives were
considered at spec time and rejected on the record: falling back to `owner_id` puts 36 opaque
characters on the identity line, and carrying the value over from the cached list row would make a
deep link or hard refresh behave differently from a click-through, which is worse than a consistent
omission. The rationale and this item's slug are in a code comment at
`web/src/schedules/ScheduleDetailPage.tsx:145-152`.

## Context

Found during spec verification for `docs/superpowers/specs/2026-08-12-schedule-detail-page.md`
(section "Where the backlog item and ROADMAP were wrong or incomplete", point 4), re-confirmed
against the tree in the plan's "Verified backend surface" table, and confirmed a third time while
writing `docs/retros/2026-08-12-schedule-detail-page.md`.

Filed at **medium**, not low, and the reasoning is worth keeping because the first instinct is to
call a missing display field cosmetic:

- **It is a contract defect, not a rendering gap.** A struct field populated by two of three
  handlers is a wrong contract, and the project's standing note is that a wrong contract is a
  defect in its own right - consumers implement against the shape, and no test covers this one.
- **The next consumer will not be defensive.** The SPA omitted the line only because the spec
  caught the asymmetry by reading the handler. The CLI, MCP or any third-party client reading the
  documented response shape will render an empty owner or infer "no owner".
- **The blast radius is one line.** The cost of fixing it is far below the cost of the next person
  rediscovering it.

Not a security issue. `fillOwnerEmails` is called on the owner-scoped list arm too, and
`ownedScheduledJob` (`:147-169`) has already established that the caller is the owner or an admin
before this handler reaches the response - so populating the field discloses nothing to anyone who
could not already read the row.

## Proposal

One call, mirroring the two list arms:

```go
func (s *Server) handleGetScheduledJob(w http.ResponseWriter, r *http.Request) {
	...
	row, ok := s.ownedScheduledJob(w, r, id)
	if !ok {
		return
	}
	resp := toScheduledJobResponse(row)
	s.fillOwnerEmails(r, []scheduledJobResponse{resp}, u.Email)  // shape to be checked
	writeJSON(w, http.StatusOK, resp)
}
```

To be settled at implementation time rather than assumed here:

- **`fillOwnerEmails`' actual signature and slice/pointer semantics.** Both existing call sites
  pass a slice of responses; whether it mutates in place through the slice header determines
  whether the single-row form above works as written or needs a one-element slice read back out.
  Read `:371` and `:504` and follow the shape, do not invent a second helper.
- **Where `u` comes from.** The list arms already have the `AuthUser` in hand; this handler
  currently does not bind one, since `ownedScheduledJob` does the auth internally. Getting the
  caller's email may mean one extra line, not a new lookup.
- **Whether `omitempty` should be added to `OwnerEmail` (`:25`) as well.** Arguably yes on
  principle - an absent key is honest where an empty string is not - but it is a **breaking change
  for every existing consumer of the list endpoints**, which today always receive the key. Decide
  it explicitly; do not slip it in with the fix. If it is added, the shipped TS type
  (`web/src/schedules/api.ts:9`) must become optional in the same change.

Deliberately **not** proposed: a new endpoint, a join in the query, or an `owner` sub-object. The
enrichment helper already exists and already solves this for two callers.

## Acceptance / Done When

- `GET /v1/scheduled-jobs/{id}` returns the owner's real email, proven by an integration test that
  is RED against today's code.
- **Both directions asserted**: an admin fetching another user's schedule sees that owner's email,
  and the owner fetching their own sees their own. A one-directional test passes against a handler
  that hardcodes the caller's email.
- The list endpoints' existing behaviour is unchanged, proven by their existing tests passing with
  no edit.
- The `omitempty` question is answered explicitly in the change's description, either way.
- `web/src/schedules/ScheduleDetailPage.tsx` renders the owner line, and the comment at `:145-152`
  naming this item is removed in the same change or in an immediate follow-up. Note the page's
  existing tests already assert **both** directions of the conditional (empty renders nothing,
  non-empty renders the line), so the frontend needs no new test - only the removal of the
  now-false comment.

## Related

- Source: `internal/api/scheduled_jobs.go:508-519` (the handler), `:371` and `:504` (the two call
  sites that get it right), `:25` (`OwnerEmail`, no `omitempty`), `:147-169` (`ownedScheduledJob`,
  the auth that already ran)
- Consumer that works around it: `web/src/schedules/ScheduleDetailPage.tsx:145-177`,
  `web/src/schedules/api.ts:56-69` (the client comment documents the asymmetry)
- Design record: `docs/superpowers/specs/2026-08-12-schedule-detail-page.md` (decision 8),
  `docs/retros/2026-08-12-schedule-detail-page.md`
- Same page, different gap: [[idea-2026-08-12-schedule-next-fires-preview]],
  [[idea-2026-08-12-schedule-job-spec-editor]]
- Adjacent backend-enabler collection: [[feature-2026-06-26-web-enabler-backend-endpoints]] - if
  that item is picked up as a batch, this belongs in it.

## Notes

The useful generalization: **an enrichment step applied at some call sites and not others is a
defect the type system cannot see.** `scheduledJobResponse` compiles identically whether
`fillOwnerEmails` ran or not, so the only way to catch this class is to ask, for every response
struct with an enriched field, which handlers build it. Worth a grep for other
`fill*`/`enrich*` helpers with an uneven call-site distribution while this one is open -
`applyJobEnrichment` in `internal/api/jobs.go` is the obvious next one to check.

## Resolution

Fixed in the 2026-08-14-scheduled-job-owner-email slice. `handleGetScheduledJob` builds a
one-element slice, calls `fillOwnerEmails` and writes that element back out, mirroring both list
arms rather than inventing a third shape.

**The item's own Proposal sketch was wrong as written**, and the bug it contained is the one the
item warned about in prose. It passed `[]scheduledJobResponse{resp}` - a copy - and then wrote
`resp`, so the enrichment would have been discarded and the endpoint would have kept returning
`""` while looking fixed. `fillOwnerEmails` mutates its elements in place, so the enriched element
must be read back out of the slice.

The three deferred decisions, each settled with evidence:

1. **Signature and semantics.** `fillOwnerEmails(r, items []scheduledJobResponse, selfEmail string)`
   mutates in place. One-element slice in, `items[0]` out.
2. **Where the caller comes from.** `UserFromCtx(r.Context())` - the same source `ownedScheduledJob`
   already uses, so no new lookup. `selfEmail` is passed only when `row.OwnerID == u.ID`, which
   mirrors the list arms' self-short-circuit and avoids a query on the common self-fetch. Review
   confirmed the comparison cannot degenerate: `scheduled_jobs.owner_id` is `NOT NULL REFERENCES
   users(id)`, and the identical comparison already gates authorization one function up.
3. **`omitempty` on `OwnerEmail`: deliberately NOT added.** It is a breaking change for every
   existing consumer of the list endpoints, which today always receive the key, and it would force
   the shipped TS type optional in the same commit. That is a contract decision deserving its own
   item, not a rider on a bug fix.

**Both directions are asserted, and the discriminating one was proven necessary.** An admin
fetching another user's schedule sees that owner's email; the owner fetching their own sees their
own. Dropping `&& row.OwnerID == u.ID` reddens only the admin test while the owner test stays
green - so the one-directional test the item warned about is confirmed vacuous on its own. The
list arms' existing tests were untouched (`git diff --numstat` shows zero deletions).

**A frontend test was pinning the defect.** `web/src/schedules/api.test.ts` asserted
`expect(s.owner_email).toBe('')` with a comment explaining the value is always empty here. That
assertion was green *because of* the bug; it now asserts a real value passes through, which is
strictly stronger. Three further files carried comments making the same now-false claim and were
corrected in the same change.

One citation discrepancy: `OwnerEmail` is at `scheduled_jobs.go:23`, not `:25` - the wrong line was
repeated in this item and in three frontend files. The corrected comments cite by symbol instead,
per the rule this batch adopted after two self-stranded citations.

The item's generalization - an enrichment step applied at some call sites and not others is a
defect the type system cannot see - was acted on: a repo-wide sweep for `fill*`/`enrich*` and for
`resolve|attach|hydrate|populate|decorate|augment|annotate` found only two response-shaped helpers.
`applyJobEnrichment` is the other, and it is already tracked as
[[bug-2026-08-13-single-job-responses-report-zero-total-tasks]]. Nothing new to file.
