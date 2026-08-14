---
title: Decide whether owner_email (and peers) should carry omitempty, as one deliberate contract change
type: idea
status: open
created: 2026-08-14
priority: low
source: deferred explicitly during the 2026-08-14-scheduled-job-owner-email slice, which fixed the population bug and refused to ride a breaking contract change along with it
---

# Decide whether owner_email (and peers) should carry omitempty, as one deliberate contract change

## Summary

`scheduledJobResponse.OwnerEmail` has no `omitempty`, so the key is always present and its value is
`""` whenever the owner's email could not be resolved. An absent key is honest; an empty string is
not, because a client cannot distinguish "not resolved" from "this owner genuinely has no email".

The population bug that made this visible is fixed - `GET /v1/scheduled-jobs/{id}` now returns the
real value - so this is no longer urgent. What remains is the contract question the fix deliberately
refused to answer.

## Context

Raised in `bug-2026-08-12-scheduled-job-detail-missing-owner-email`, which named it as one of three
decisions to settle at implementation time and said explicitly: decide it, do not slip it in. It was
decided as **not now**, for a stated reason rather than by omission.

Adding `omitempty` is a **breaking change for every existing consumer of the list endpoints**, which
today always receive the key. It would also force the shipped TS type at `web/src/schedules/api.ts`
optional in the same commit, and any consumer doing `row.owner_email === ''` or reading the key
unconditionally would change behaviour. That is a contract decision with its own blast radius, and
riding it along inside a bug fix would have made a one-line correctness change into a breaking API
change nobody reviewed as one.

`fillOwnerEmails` degrades rather than failing: a store lookup error is logged and leaves
`owner_email` empty, returning 200. So the empty-string case is reachable in production today, which
is precisely what makes the ambiguity real rather than theoretical.

## Proposal

Treat this as one deliberate sweep, not a per-field edit:

- Enumerate every response field in `internal/api/` that is populated by an enrichment step and can
  legitimately be absent, rather than only `OwnerEmail`. `applyJobEnrichment`'s counters are the
  obvious peer and are separately tracked in
  [[bug-2026-08-13-single-job-responses-report-zero-total-tasks]], which proposes `*int32` +
  `omitempty` for exactly this reason - so the two items should be decided together or one should
  absorb the other.
- Decide the house rule once: does this API distinguish "absent" from "empty" at all? Note the
  codebase already does in places - `last_run_at`/`last_job_id` carry `omitempty` and their absence
  is documented as meaningful, with a client comment warning consumers to handle `undefined` and
  never `=== null`.
- If adopted, the TS types and any `=== ''` checks change in the same slice, and the change is
  called out as breaking in the PR rather than buried.

## Acceptance / Done When

- A stated house rule on absent-versus-empty for enriched response fields, recorded somewhere
  durable rather than in a PR description.
- Either `omitempty` is applied consistently across the fields the rule covers, with TS types and
  client checks updated in the same change, or the item is closed `wontfix` with the argument
  written down so it is not re-litigated.
- Whichever way it goes, decided together with
  [[bug-2026-08-13-single-job-responses-report-zero-total-tasks]] rather than in conflict with it.

## Related

- Deferred from: [[bug-2026-08-12-scheduled-job-detail-missing-owner-email]] (closed 2026-08-14)
- Overlapping decision: [[bug-2026-08-13-single-job-responses-report-zero-total-tasks]]
- Source: `internal/api/scheduled_jobs.go` (`scheduledJobResponse.OwnerEmail`, `fillOwnerEmails`),
  `web/src/schedules/api.ts`

## Notes

Filed as an idea rather than a bug: nothing is broken, and the current shape is merely less precise
than it could be. Low priority for the same reason - but worth keeping, because the alternative to
deciding it once is deciding it accidentally, one field at a time, in whichever PR next touches an
enriched response.
