---
title: UserRateLimit and RateLimit are exported and panic on the first request when limit is zero
type: bug
status: open
created: 2026-09-04
priority: low
source: Security and correctness lenses of the change-password rate-limit slice (2026-09-04)
---

# UserRateLimit and RateLimit are exported and panic on the first request when limit is zero

## Summary

`rateLimiter.allow` (`internal/api/ratelimit.go`) takes its over-limit branch on
`len(hits) >= rl.limit`, which is `0 >= 0` on the very first request of an empty window, and that
branch's first statement is `retry := rl.window - now.Sub(hits[0])`. With `limit == 0` the slice is
empty, so the first request panics with `index out of range [0] with length 0`.

`UserRateLimit` and `RateLimit` are both exported and neither validates its argument. The
precondition is real, unstated, and enforced only by every call site remembering it.

## Repro / Symptoms

Reproduced 2026-09-04 by constructing the limiter directly and issuing one request:

```
runtime error: index out of range [0] with length 0
```

**Through a real `net/http` server the result is NOT a 500.** `conn.serve` recovers the panic, logs
it, and closes the connection without writing a response, so the client sees
`Get "http://...": EOF`. That is worse than a 500 for anyone diagnosing from the client side: there
is no status code to read, and the failure repeats per request rather than crashing the process.

## Context

Found while reviewing the slice that added `RELAY_PASSWORD_CHANGE_RATE_LIMIT`. Not reachable from
the environment: `ParseRateLimit` refuses a zero count and `main` is fatal on the parse error, so
only a Go caller building a `Server` directly can reach it.

**The count is four and the axis matters.** Non-test call sites of the two exported constructors are
in `internal/api/server.go` - the job-submit, password-change, register and login limiters - and all
four guard both fields `> 0` before constructing. `search_ratelimit.go` is a fifth guarded site but
builds a `&rateLimiter{}` directly rather than through either exported function, so it is not
reachable by a caller outside the package and does not belong in that count.

The exposure is the exported signature carrying an unstated precondition. A future in-package
caller, or any consumer if `internal/` ever opens up, gets a dropped connection per request instead
of a compile error or a construction error. `TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff`
already kills the `&&`-to-`||` relaxation at one call site, but a guard at one call site says nothing
about the constructor.

## Proposal

Sketch only. Make the constructor fail closed rather than fail loud: with `limit <= 0` or
`window <= 0`, return the identity middleware (an unarmed bucket) so a misconfigured caller gets no
limiting rather than a broken route, or clamp to a sane minimum. Either removes the precondition
from the signature.

Note the current shipped comment documents `allow`'s `hits[0]` panic as a load-bearing property,
which entrenches the fail-loud-but-uninformative shape. That comment should go with the fix.

## Acceptance / Done When

- Constructing either exported limiter with a non-positive limit or window cannot panic on a
  request.
- A test pins the chosen behaviour for both constructors, not just one call site.
- The comment describing the panic as load-bearing is removed rather than corrected.

## Related

- `internal/api/ratelimit.go` (`allow`, `UserRateLimit`, `RateLimit`), `internal/api/server.go`
  (the four guarded call sites), `internal/api/search_ratelimit.go` (the direct construction)
- [[bug-2026-09-04-change-password-runs-bcrypt-cost-12-unlimited]] - the slice whose review found
  this
