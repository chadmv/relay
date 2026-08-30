---
title: follow_job("") and relay logs "" open the server-wide broadcast subscription instead of failing
type: bug
status: open
created: 2026-08-30
priority: medium
source: Phase 4 review of the 2026-08-30 ?job_id= canonicalisation slice; pre-exists that slice, which is only the first place "" -> broadcast is written down client-side
---

# `follow_job("")` and `relay logs ""` open the broadcast subscription

## Summary

Both SDK clients put an empty job id straight into `?job_id=`, and the server cannot distinguish
`?job_id=` from omitting the parameter at all. `handleEvents` does
`r.URL.Query().Get("job_id")` -> `""` -> `events.Filter{JobID: ""}`, which is the broker's
**broadcast** scope. So a method documented as "Stream events for a single job" yields every job's
status events on that server.

## Repro / Symptoms

Measured, both clients:

```
python:  httpx.Request('GET', '/v1/events', params={'job_id': ''})
         -> 'http://x/v1/events?job_id='
go/cli:  "/v1/events?job_id=" + url.QueryEscape("")
         -> "/v1/events?job_id="
```

Server side: `canonicalJobIDFilter("")` returns `""` unchanged (correctly - see its doc comment; the
empty row is the one case where "unchanged" and "broadcast" coincide, and
`TestCanonicalJobIDFilter`'s `passthrough/empty` row pins exactly that), and
`internal/events/broker.go`'s `Publish` status branch delivers to every filter whose `JobID` is
empty and which named no task.

## Context

Two call sites, both pre-existing and both unchanged by the 2026-08-30 canonicalisation slice:

- `python/src/relay/client.py`, `Client.follow_job` -> `_stream_events(job_id)`, which passes
  `params={"job_id": job_id}` with no guard. `_require_token()` is the only precondition.
- `internal/cli/logs.go`, `doLogs`: `len(args) == 0` is refused, but `args[0] == ""` is not, so
  `relay logs ""` reaches `jobEventsPath("")`. The CLI is partly self-limiting - `watchJobLogs`
  subscribes and then takes a snapshot, and `GET /v1/jobs/` gives a fatal that ends the watch - but
  the broadcast subscription is opened first, and the ordering is what makes it not-a-fix.

This is a scope surprise, not a privilege escalation: `/v1/events` is bearer-auth-only with no
per-owner gate, so the caller is handed data the same token could have requested outright by
omitting the parameter. The defect is that the caller did NOT ask for it, and the method's contract
says the opposite.

Filed rather than fixed in that slice because it pre-exists it and is out of its scope. It is filed
NOW because that slice is the first place `""`-means-broadcast is written down on the client side
(`follow_job`'s new docstring and `python/README.md` both describe spelling behaviour for a
non-empty id), so the next reader has prose that stops one word short of this case.

## Proposal

Two candidate guards; decide, do not do both.

- **Refuse in the client.** `follow_job` raises `ValueError` (or the SDK's own error type - check
  `relay.errors` for whether a raw builtin escapes the `RelayError` hierarchy, which is
  [[bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy]]'s subject) on an empty or
  whitespace-only id, before any request; `doLogs` refuses an empty `args[0]` the same way it
  refuses a missing one. Cheap, local, and it keeps the "no client-side canonicalisation, ever"
  decision intact because refusing is not rewriting.
- **Distinguish it server-side.** Treat a PRESENT-but-empty `job_id` as an error while a MISSING one
  stays the broadcast. This is the only option that also covers a hand-written `curl`, and it is a
  change to an existing documented contract (`?job_id=` returns no 4xx today, which
  `TestEvents_TaskIDValidation` pins), so it needs its own decision rather than being folded in.

The first is the recommendation on cost; state which was taken and why the other was not.

## Acceptance / Done When

- A test proves an empty id does NOT reach `?job_id=` on the wire (Python) and does not build a
  request line (CLI), or - if the server option is taken - that a present-but-empty `job_id`
  answers 4xx while an absent one still opens the broadcast.
- The prose that now describes only the non-empty case says what happens for `""`:
  `follow_job`'s docstring, `python/README.md` ("Job id spelling"), and `README.md` under "Events".

## Related

- `python/src/relay/client.py` (`follow_job`, `_stream_events`)
- `internal/cli/logs.go` (`doLogs`, `jobEventsPath`, `watchJobLogs`)
- `internal/api/events.go` (`handleEvents`, `canonicalJobIDFilter`), `internal/events/broker.go`
- [[bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy]]
