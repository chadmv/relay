---
title: Python SDK follow_job() subscribes to nothing forever on an uppercase or dashless job id
type: bug
status: closed
closed: 2026-08-30
resolution: fixed
created: 2026-08-27
priority: high
source: Spec D9 while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# `follow_job()` subscribes to nothing forever on a non-canonical job id

## Summary
`handleEvents` deliberately does not validate or canonicalise `?job_id=`
(`internal/api/events.go`), and the broker filter is an exact string compare. `follow_job(job_id)`
passes the caller's string verbatim, and the stream sets no read timeout. So an uppercase or
dashless UUID - which `get_job()` accepts, because `parseUUID` is `pgtype.UUID.Scan` and takes
both - yields an open, permanently empty stream.

## Repro / Symptoms
`client.follow_job("7E660488-1234-4321-8888-ABCDEFABCDEF")` against a job that exists. Expected:
frames. Observed: the iterator blocks forever. `client.get_job()` with the same string works.

## Context
Character-for-character the defect `canonicalJobID` closed in the Go CLI on 2026-08-26
([[bug-2026-08-25-relay-logs-prints-nothing-envelope-drift]]).

**The remedy needs care in a way the Go one did not, and this is the whole reason it was declined
rather than folded in.** `canonicalJobID` shares its parse half with the server - it runs argv
through the same `pgtype.UUID.Scan` the server's `parseUUID` uses - so the two cannot drift.
Python has no such shared parser. `uuid.UUID()` accepts spellings `pgtype.UUID.Scan` may not
(`urn:uuid:...`, brace-wrapped), so a naive SDK-side canonicaliser would make the SDK accept MORE
than the server: a client that cheerfully subscribes to a job id the server would have rejected.

Also note `follow_job` was separately found to raise `ValueError` on its first frame in every
released version, so this hang has never actually been reachable by a caller. It becomes reachable
with that fix, which shipped in the slice above.

## Acceptance / Done When
- A non-canonical but server-acceptable job id yields the same frames as its canonical spelling.
- The acceptance surface is pinned in BOTH directions: a spelling the server rejects must not be
  silently canonicalised by the SDK into one it accepts.
- A test covers uppercase, dashless, and at least one spelling `uuid.UUID` takes and the server
  does not.

## Related
- `python/src/relay/client.py` `follow_job`, `_stream_events`
- `internal/api/events.go` `handleEvents`; `internal/events/broker.go` `Publish`
- `internal/cli/logs.go` `canonicalJobID` - the Go fix and its shared-parser argument

## Resolution
Fixed **server-side**, not in the SDK where this item filed it. `internal/api/handleEvents` now routes
`?job_id=` through `canonicalJobIDFilter`, which renders an accepted UUID via `uuidStr` and returns
anything else byte-identical. Four lines of production Go; the Python SDK has a zero production diff.

**The item's central instruction was unsatisfiable as written, and measuring is what showed it.** The
item said a naive SDK canonicaliser would accept MORE than the server. That is true, and it is only
one direction. Measured by execution in both:

- **Server accepts, Python rejects.** `pgtype.UUID.Scan`'s 36-byte branch slices out indexes 8, 13, 18
  and 23 *without examining them*, so `7e660488_1234_4321_8888_abcdefabcdef` and its `:`, space and
  `*` variants all name the same job, and `GET /v1/jobs/{id}` answers 200 for them. No canonicaliser
  built on `uuid.UUID` can reproduce that, so an SDK-side fix is **incomplete**.
- **Python accepts, server rejects.** Brace-wrapped, `urn:uuid:` and stray-dash forms - **unsound**.
  Worse, `+<31 hex>`, `0x<30 hex>` and a PEP 515 `_` inside 32 hex digits are accepted by `int(s, 16)`
  and yield a **different uuid**, so a naive canonicaliser silently subscribes to another job.

Both differences are non-empty, so this item's own "Done When" list could not be satisfied on the SDK
side at all. The server is the only place the acceptance surface is defined by construction, which is
why the fix moved there.

**The guard is the whole correctness argument.** `parseUUID` returns the zero UUID on failure and
`uuidStr` renders that as the empty string - which is the broker's BROADCAST filter. Rendering
unconditionally would promote every typo from "one job, silently empty" to "every job on the cluster".
That is a scope surprise rather than a privilege escalation, since `/v1/events` has no owner gate and
omitting `job_id` is already a full-cluster subscription - but it is still the one way this change
could have been worse than doing nothing.
`TestEvents_JobIDRejectedSpellingsAreNotCanonicalised` asserts SCOPE and dies when the guard is
deleted; `TestCanonicalJobIDFilter` dies too, on the default lane with no container.

Verification: four review lenses, a full green `-tags integration` suite across all 24 packages, the
CLI real-server lane, and a `-race` run over the events tests. The headline test was reproduced RED
at the pre-image, failing on the frame wait with the subscription barrier passing - an empty stream,
which is the defect.

Residuals filed rather than folded in:
[[bug-2026-08-30-empty-job-id-opens-the-broadcast-subscription]] (`follow_job("")` and `relay logs ""`
both open the broadcast subscription - pre-existing, but this slice is the first place `""`-means-
broadcast is written down on the client side) and
[[idea-2026-08-30-parseuuid-formats-the-whole-rejected-input-into-a-discarded-error]].
[[idea-2026-08-26-six-copies-of-the-uuid-render-format]] was amended, not closed: its claim that
server-direction drift is caught by nothing is now refuted by `TestCanonicalJobIDFilter`, and its
argument is stronger than before because the render format is now load-bearing on both sides of the
client/server boundary.
