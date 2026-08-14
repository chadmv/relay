# Retro: GET /v1/scheduled-jobs/{id} returns the owner's real email

**Date:** 2026-08-14
**Slice:** 2026-08-14-scheduled-job-owner-email
**Closes:** `bug-2026-08-12-scheduled-job-detail-missing-owner-email`
**Shape:** 13 lines of Go, two integration tests, comment corrections in four frontend files.

## What shipped

`handleGetScheduledJob` never called `s.fillOwnerEmails`, so `GET /v1/scheduled-jobs/{id}` always
returned `owner_email: ""` while both list arms returned the real value. Same struct, same row, two
endpoints disagreeing. It now builds a one-element slice, calls the helper, and writes that element
back out - mirroring the list arms rather than inventing a third shape.

## The item's Proposal contained the exact bug its prose warned about

This is the finding worth carrying forward. The item was unusually careful: it flagged three
questions as deferred, told the implementer to read the two working call sites and "follow the
shape, do not invent a second helper", and warned that a one-directional test would pass against a
handler that hardcodes the caller's email.

Its own code sketch then passed `[]scheduledJobResponse{resp}` - a copy - into `fillOwnerEmails`
and wrote `resp`. `fillOwnerEmails` mutates its elements in place, so the enrichment would have
been discarded. Implemented literally, the sketch produces an endpoint that still returns `""`
while looking fixed, and the only test that would have caught it is the one the item itself said to
write.

The sketch did carry `// shape to be checked`, and the implementer checked it. That comment is the
thing that worked. **A code sketch in a backlog item is a hypothesis with syntax highlighting**, and
the habit of marking which parts are unverified is what made this a non-event rather than a shipped
regression.

## The vacuous-test warning was tested, not trusted

The item predicted that an owner-fetches-own-schedule test alone would pass against a handler that
just stamps the caller's email onto every response. That prediction was verified by mutation rather
than assumed: dropping `&& row.OwnerID == u.ID` reddened only the admin-sees-the-owner's-email test
while the owner test stayed green. So the discriminating direction is confirmed necessary, and the
suite would not have caught the wrong implementation without it.

Reviewing re-executed that mutation independently and reproduced it, and also ran the full revert
(helper call removed entirely), which reddens both - the two tests bracket the failure modes.

## A frontend test was green because of the bug

`web/src/schedules/api.test.ts` asserted `expect(s.owner_email).toBe('')`, with a comment carefully
explaining that the value is always empty on this endpoint and citing the handler. That assertion
was correct, well-documented, and pinning the defect - it would have failed the moment anyone fixed
the bug.

It now asserts a real value passes through, which is strictly stronger: a client that dropped the
field fails now and passed before. Three further files carried comments making the same
now-false claim and were corrected in the same change; a sweep confirmed no comment anywhere still
claims the field is always empty here.

The general shape: **a test that documents a defect in prose and asserts it in code is a test that
will resist its own fix.** Worth noticing when writing one - the honest form is a test named for the
bug, not a passthrough test whose expected value happens to be the bug's output.

## The three deferred decisions

All settled with evidence rather than assumption:

1. **Signature.** `fillOwnerEmails` mutates in place; one-element slice in, `items[0]` out.
2. **Caller identity.** `UserFromCtx(r.Context())`, the same source `ownedScheduledJob` already
   uses - one line, no new lookup. Review confirmed the `row.OwnerID == u.ID` comparison cannot
   degenerate: `scheduled_jobs.owner_id` is `NOT NULL REFERENCES users(id)`, and the identical
   comparison already gates authorization one function up.
3. **`omitempty`: deliberately not added.** Breaking for every list consumer, and it would force the
   TS type optional in the same commit. Filed as
   [[idea-2026-08-14-owner-email-omitempty-contract]] so it is decided once, together with
   [[bug-2026-08-13-single-job-responses-report-zero-total-tasks]], rather than accidentally one
   field at a time.

## The generalization was acted on, and came back empty

The item's closing note - an enrichment step applied at some call sites and not others is a defect
the type system cannot see - was actionable, so it was actioned. A repo-wide sweep for
`fill*`/`enrich*` plus `resolve|attach|hydrate|populate|decorate|augment|annotate` found exactly two
response-shaped helpers: `fillOwnerEmails` and `applyJobEnrichment`. The latter is already tracked.

Nothing new to file is a real result, and cheaper to record than to rediscover. The class is now
closed with a named search rather than an intuition.

## Process notes

- **Scaled down deliberately.** No separate spec or plan doc, and one combined review lens instead
  of a four-way fan-out, matching the project's convention for trivial no-logic changes. The item
  was complete enough to be the contract. Recording the deviation so the scaling is a decision
  rather than a drift.
- Two low prose findings from review were fixed inline rather than routed back: `fillOwnerEmails`'
  doc still described a two-caller world that stopped being true with this change, and "mutates
  through the slice header" was loose (the header is passed by value; the backing array is shared).
- One citation discrepancy: `OwnerEmail` is at `:23`, not `:25`, and the wrong line was repeated in
  the item and three frontend files. The replacements cite by symbol, per the rule this batch
  adopted after two self-stranded citations.

## Verification

`go build`, `go vet`, `go test ./...` green; integration `./internal/api/...` green; frontend
1068/1068 across 144 files. List-arm tests untouched (`git diff --numstat` shows zero deletions).
