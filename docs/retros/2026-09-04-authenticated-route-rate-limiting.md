---
date: 2026-09-04
topic: authenticated-route-rate-limiting
branch: claude/lane-jobs-ratelimit
range: 38725ba..d476f66
---

# Session Retro: 2026-09-04 - Authenticated-Route Rate Limiting

**TL;DR:** Anyone with a valid login could submit jobs to the render farm as fast as their network
allowed, and nothing stopped them. This session added a per-user speed limit on the three routes
that actually buy work. The design question was not "how fast" but "how do you identify who is
asking" - the existing limiter counts by network address, which is wrong the moment a whole studio
shares one office connection. The most useful part of the session was the review: three separate
mutations of the shipped code left every test passing, including one that turns the whole limiter
off in production.

## Handoff

Lane D of a six-item batch over ROADMAP.md's Now section, and the keystone two other items were
waiting on. Closed [[bug-2026-08-29-post-v1-jobs-is-not-rate-limited]]. Seventeen commits: spec
`21fde15`, plan `a9396d5`, twelve implementation commits `6aa2314..e598b4d`, a three-commit fix
round `7b47b1d..d476f66`.

`RELAY_JOB_SUBMIT_RATE_LIMIT`, default `120:10s`, one shared bucket over `POST /v1/jobs`,
`POST /v1/jobs/{id}/retry` and `POST /v1/scheduled-jobs/{id}/run-now`. Key is the authenticated
user id, mounted inside the auth chain because `BearerAuth` is what puts `AuthUser` in the
context.

**The item named two routes and the important one was neither.** `retry` re-runs an existing job's
tasks from a **zero-byte** request with no validation, which is the cheapest repetition primitive in
the API, and `run-now` is a second job-creation path through `CreateJobFromSpec`. One bucket, not
three, because the quantity bounded is execution bought per principal and alternating routes must
not triple the ceiling.

**`POST /v1/scheduled-jobs` is OUT, decided rather than deferred.** A creation-rate limit bounds
growth rate, not table size, and size is what breaks the boot sweep - so shipping it would let that
item record "bounded" for something that is not. The surviving half is
[[feature-2026-09-04-per-owner-schedule-cap]].

**The three count constants stand unchanged** (5000 / 500 / 25000). The count-bounds slice
conditioned the tighter set on this item being closed WONTFIX; it was implemented, so the premise
holds. Outcome "unchanged" is the required step, not a no-op.

Verify: 22 packages default, `-race` green across 24 packages in a Linux container, the full
`internal/api` integration lane green in 700s, CLI lane green. One item filed. `ROADMAP.md` still
says `RateLimit` wraps register and login and nothing else; the batch's closing refresh owns that.

## What Was Built

- **`userRateLimitKey`**, returning `(string, bool)` and failing closed. The two-value return is
  the point: `rl.allow(userRateLimitKey(u))` does not compile, so an unidentified principal cannot
  be bucketed under `""` by omission.
- **`UserRateLimit`**, a second instance of the existing `rateLimiter` with a per-principal key.
- **A property nobody had written down**, added in the fix round: because the 401 returns *before*
  `rl.allow`, an unidentified caller creates no map key at all. That is exactly the defect the
  token-keyed design was rejected for, and the shipped ordering delivers it structurally.

## Key Decisions

- **The user id, not the address.** On `login` there is no principal and the address is the only
  identifier that exists before one does; on an authenticated route there is one, resolved
  server-side from a hash lookup and never read off the wire. The address key fails both ways at
  once: one office egress collapses a studio into one bucket, one user with two machines gets two.
- **Disclose the composition cost rather than hide it.** The limiter runs after `BearerAuth`, so
  every refused request still costs one token lookup. It bounds repetition of expensive work, not
  request volume.
- **No admin exemption.** An admin's runaway script buys the same execution as anyone's, and an
  exempt class is a control that does not exist for the people most able to trip it.
- **`120:10s` against a measurement.** A 200-iteration `relay submit` loop ran at 33.8/s and
  36.7/s. The human chose the value knowing a scripted loop is refused partway and takes about 17s
  if it retries. That is the burst a ceiling exists to bound, and README says so.

## What Went Wrong and What Changes

**Three mutations of the shipped code left every test green, and each was found by running it
rather than reading it.**

- Mutating `main.go`'s `httpServerDeps` literal to zero left the **entire `cmd/relay-server` lane
  green**. The bucket can be silently off in production. The named-field trade removed the
  transposition hazard at the `api.New` boundary and **not** inside main's own literal, and a
  comment claiming the pair is guarded was true only for the assignment one layer down. The
  boundary is now stated instead of a guard being bolted on, because stating it is the honest
  repair and the general fix has its own item.
- Setting `Retry-After` to zero **survived every rate-limit test**: the assertions were `NotEmpty`
  or `secs >= 1`, and `0+1 == 1` satisfies both. A limiter telling every refused caller "one
  second" under a ten-second window passed.
- Relaxing `&&` to `||` in the enable guard survived, because the only off-state test set *both*
  fields to zero while the field doc promises "zero on EITHER".

**The plan's own test designs were wrong in three places, and only running them showed it.** A
mutation predicted RED survived (the second 401 arm is reachable but cannot answer differently, so
it pinned nothing). A control predicted GREEN killed a test for the wrong reason - `key = u.Email`
collapsed alice and bob because every `AuthUser` literal leaves `Email` at `""`, a degenerate
fixture value rather than the property. And deleting `next.ServeHTTP` survived, because
`httptest.NewRecorder` defaults `Code` to 200, so three allowed-path assertions were satisfied by
a middleware that writes nothing.

**A prose repair stopped halfway, which is worse than not starting.** An earlier commit fixed
"neither route is rate limited" in the test file and left the copy on `jobOwnerOr404` itself - the
one a maintainer reads before changing the gate order. It was missed because the sentence wraps a
line, so a single-line grep returns nothing. The fix round re-ran the sweep with a multiline
pattern over a wider shape (8 hits, 8 files) and checked each against the router rather than
against the review's list; exactly one was stale.

**Three of the conductor's fix prescriptions were wrong and all three were refused with
evidence.** A prescribed `Retry-After` band of `[55, 60]` was wrong at the top - seconds truncate
and then increment, so a full window renders `window + 1`, and the engineer's first run returned
61. A demand for a new test pinning `ErrorIsTransient`'s 429 classification was refused because
that row already exists and the mutation proving it was run; a duplicate would have killed nothing.
And a citation pointed at a backlog file that lives on another lane's branch and was not visible
from this worktree. **A fix brief is a set of claims, and prescribing beyond what you checked
manufactures work at best and a wrong edit at worst.**

**A false claim reached three documents before anyone checked it.** "One `@every 1s` schedule is an
uncapped job engine" appeared in the spec, then the README, then a backlog item the conductor
wrote. `minScheduleInterval = 30 * time.Second` refuses it on both create and PATCH, and README
already said so 800 lines further down. Three independent review lenses found it. It was deleted
rather than corrected, per this project's rule - and note the correction that was tempting would
have been wrong in a second way, because the real unbounded axis is the *number* of schedules, not
one schedule's interval.

## Recommended Backlog Items

- [[bug-2026-09-04-change-password-runs-bcrypt-cost-12-unlimited]] - filed. `PUT
  /v1/users/me/password` runs a cost-12 bcrypt compare on every request with no bound, before any
  authorization narrower than "is authenticated". Per request that is a strictly better denial of
  service than the route this slice bounds, and `POST /v1/auth/login` is rate-limited *precisely
  because* it runs a bcrypt compare.

## Files Most Touched

- `internal/api/ratelimit.go` - `userRateLimitKey`, `UserRateLimit`
- `internal/api/server.go` - the three wraps and the enable guard
- `cmd/relay-server/{main,http_server}.go` - config threading via named fields
- `README.md`, `internal/jobspec/jobspec.go`, `internal/api/jobs.go` - prose this slice falsified
