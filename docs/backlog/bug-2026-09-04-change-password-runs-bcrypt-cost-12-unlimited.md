---
title: PUT /v1/users/me/password runs a cost-12 bcrypt compare on every request with no rate limit
type: bug
status: open
created: 2026-09-04
priority: medium
source: Security lens of the authenticated-route rate limiting slice (2026-09-04)
---

# PUT /v1/users/me/password runs a cost-12 bcrypt compare on every request with no rate limit

## Summary

`internal/api/server.go` registers `PUT /v1/users/me/password` as bare `auth(...)`. Its handler
`handleChangePassword` runs `bcrypt.CompareHashAndPassword` against the caller's stored hash at
`bcryptCost = 12` on **every** request, before any authorization narrower than "is
authenticated". That is roughly a quarter second of a CPU core per request, bought by a client
that pays almost nothing to send it. One authenticated principal can saturate every core.

## Context

Found by the security lens of the rate-limiting slice
(`docs/superpowers/specs/2026-09-04-authenticated-route-rate-limiting.md`) while enumerating what
that slice did NOT bucket. It was deliberately left out of that bucket rather than folded in -
see Proposal.

**The codebase already recognises bcrypt as the reason a limiter exists.** `POST /v1/auth/login`
is wrapped in `RateLimit` precisely because it runs a bcrypt compare on every attempt, including
the pre-computed dummy-hash path that defeats email enumeration. Its authenticated sibling runs
the same primitive at the same cost with no bound at all.

Per request this is a strictly better denial of service than `POST /v1/jobs`, which that slice
did bound: a job submission's cost is dominated by database inserts the server can batch, while
this one is CPU by construction and cannot be made cheaper without lowering the cost factor,
which is the thing protecting stored passwords.

## Repro / Symptoms

- Authenticate as any ordinary user.
- Issue `PUT /v1/users/me/password` in a loop with a deliberately wrong `current_password`.
- Every request costs a full cost-12 bcrypt compare. The wrong password means the handler
  refuses, so no state changes and nothing rate-limits the loop.

## Proposal

Sketch only; the keying and the window are the work.

**Do not fold this into `RELAY_JOB_SUBMIT_RATE_LIMIT`.** That bucket bounds how much task
execution one principal buys, at a burst sized for job submission. A bcrypt route needs a
different key and a much smaller window, and sharing one bucket would make both bounds wrong -
either the submit ceiling drops to password-change rates, or the password route inherits a
ceiling far above what a human needs.

1. Decide the key. The submit bucket's argument (an authenticated route has a principal, and it
   is the unit the cost belongs to) transfers cleanly here, and unlike retry and run-now there is
   no owner-versus-caller split: the route is self-scoped.
2. Decide the window. A human changes their password at most a few times a year; a bound near 1
   per few seconds refuses nothing real.
3. Consider whether the compare can be skipped for a caller who has already failed recently,
   rather than only refused after it runs.

## Acceptance / Done When

- A burst on this route is refused before the bcrypt compare runs, not after.
- A normal password change is unaffected, proven.
- README documents the bound.

## Related

- [[bug-2026-08-29-post-v1-jobs-is-not-rate-limited]] - the slice that bounded the submit routes
  and enumerated this one as out of scope
- [[bug-2026-08-23-http-listener-has-no-admission-bounds]] - the request-volume control relay
  does not have, which would bound this from the other direction
- `internal/api/server.go` (the route registration), `internal/api/auth.go`
  (`handleChangePassword`, `bcryptCost`)
