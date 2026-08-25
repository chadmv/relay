---
title: The enrollment refusal counters exist but no operator can read them
type: idea
status: open
created: 2026-08-25
priority: medium
source: 2026-08-25 auto-enroll-guards slice - de-scoped at plan time by the conductor
---

# The enrollment refusal counters exist but no operator can read them

## Summary

`Handler.EnrollmentRefusals()` returns a three-way split of enrollment refusals - hostname claimed,
fleet at ceiling, credential live - and nothing outside tests calls it. Refusals are deliberately
never logged, so a refused enrollment currently produces **zero** observable server-side output.

## Context

De-scoped deliberately at plan time, not an oversight. `internal/api/server_counters.go` is 574 lines
and its test 1355; a sixth section touches a const, an accessor, an `api.CounterSources` field, a
response struct with json tags, `counterPayloadLeaves`, and the section list in that test - comparable
to or larger than the guards the slice existed to ship. The reduced form satisfies every acceptance
criterion in both closed items, and README:435 already tells the reader the section is deferred.

## Proposal

Add an `enrollment` section to `GET /v1/server/counters` following the five existing sections.

**Carry two things the slice measured, or the payload ships a wrong contract:**

- **The `fleet_at_ceiling` aliasing.** The ceiling is checked before the hostname insert (deliberately,
  so a refusal stays side-effect-free), so at capacity *every* refusal is recorded as
  `fleet_at_ceiling` including claimed-hostname retries - `hostname_claimed_total` goes to zero
  exactly when an operator is triaging. This was observed empirically while writing the end-to-end
  ceiling test, not reasoned about. Say it where the signal is READ.
- **The section is `enrollment`, not `auto_enroll`.** `credential_live_total` fires only on the
  enrollment-TOKEN path. The Go type was renamed for this reason during the slice; do not let the
  JSON section reintroduce the error.

## Acceptance / Done When

- The three counters are published, admin-only, alongside the existing sections.
- The payload guards cover the new section (note `idea-2026-08-24-counter-payload-guards-check-fixtures-not-producers`:
  those predicates are proven against fixtures rather than producers).
- README's "not yet published" sentence is corrected, and the aliasing caveat lands where the numbers
  are documented.

## Related

- `internal/worker/enrollment_refusal_counters.go`, `internal/api/server_counters.go`
- [[idea-2026-08-24-counter-payload-guards-check-fixtures-not-producers]]
- `docs/superpowers/specs/2026-08-25-auto-enroll-guards.md` section 7
