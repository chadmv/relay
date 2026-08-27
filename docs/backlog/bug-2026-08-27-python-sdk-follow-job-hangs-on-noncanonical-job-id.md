---
title: Python SDK follow_job() subscribes to nothing forever on an uppercase or dashless job id
type: bug
status: open
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
