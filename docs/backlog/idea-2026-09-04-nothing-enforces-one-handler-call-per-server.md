---
title: Nothing enforces one Handler() call per api.Server, and every limiter built there is a fresh budget
type: idea
status: open
created: 2026-09-04
priority: low
source: Invariants and correctness lenses of the change-password rate-limit slice (2026-09-04)
---

# Nothing enforces one Handler() call per api.Server, and every limiter built there is a fresh budget

## Summary

`api.Server.Handler()` builds the job-submit, password-change, login and register limiters inline.
Each call allocates a fresh bucket for each of them and starts a `gcLoop` goroutine that nothing
ever stops, so a second `Handler()` call on the same `Server` doubles every one of those budgets
and leaks four goroutines. Nothing in the type or the tests enforces the once-per-`Server` rule the
doc comment states.

The search limiter is the carve-out and the contrast: `searchRateLimiter` memoizes behind
`searchLimiterOnce sync.Once`, so its uniqueness is **structural**, and
`TestSearchLimiter_IsConstructedOncePerServer` pins it.

## Context

Production is immune today, for a reason worth recording because it is not obvious:
`buildHTTPServer` returns `*http.Server`, so `main` never holds the `api.Server` and cannot call
`Handler()` a second time. Verified 2026-09-04: exactly one non-test `Server.Handler()` call site
exists repo-wide, and exactly one non-test `api.New(` call site.

Measured, on a `Server` with both the password and search limiters armed: eleven `Handler()` calls
produced a goroutine delta of exactly +10 while `searchRateLimiter()` returned the identical
pointer every time.

**This is a `Server`-level property, not a property of any one limiter.** It was first noticed while
reviewing the password bucket, and the tempting fix - a comment on that bucket saying its uniqueness
is conventional - would advertise a property three of its neighbours share and none of them state.
That is the shape of claim this project deletes rather than adds, which is why this is filed instead.

Tests already rely on the current behaviour in both directions: `internal/api`'s integration helper
`registerAndLogin` calls `srv.Handler()` per invocation, which is safe only because the limits on
those servers are zero, and a slice that arms a limiter in that lane has to bind one handler per
test or its assertions go silently vacuous.

## Proposal

Sketch only. Options, cheapest first:

1. Memoize the whole handler behind a `sync.Once` on `Server`, the way `searchLimiterOnce` already
   does for its limiter. Makes the rule structural for every limiter at once.
2. Memoize each user-keyed limiter individually, matching the existing search pattern.
3. Leave the behaviour and add a test that pins it, so a second call is a documented fact rather
   than a trap.

Note any fix must not break the `registerAndLogin` pattern, or must fix that too.

## Acceptance / Done When

- A second `Handler()` call on one `Server` cannot silently double a budget, or a test states that
  it does and why that is acceptable.
- The `Handler` doc comment's rule is enforced by something other than convention, or it is
  rewritten to describe what is actually guaranteed.
- The `gcLoop` goroutine leak per extra call is addressed or recorded.

## Related

- `internal/api/server.go` (`Handler`, the four inline limiter constructions, `searchLimiterOnce`),
  `internal/api/search_ratelimit.go` (`searchRateLimiter`, the memoized carve-out)
- `internal/api/auth_integration_test.go` (`registerAndLogin`, which calls `Handler()` per use)
- [[bug-2026-09-04-userratelimit-panics-on-a-zero-limit]] - the other exported-limiter defect from
  the same review
