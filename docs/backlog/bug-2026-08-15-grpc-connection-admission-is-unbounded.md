---
title: gRPC connection and stream admission is unbounded, so every per-connection bound is a soft bound
type: bug
status: open
created: 2026-08-15
priority: medium
source: Phase 4 security lens of the 2026-08-15-tasklog-err-limiter-keying slice; the residual that slice named rather than implied
---

# gRPC connection and stream admission is unbounded, so every per-connection bound is a soft bound

## Summary

`cmd/relay-server/main.go` constructs the agent gRPC server with **exactly one option**:

```go
grpcSrv := grpc.NewServer(
    grpc.KeepaliveParams(keepalive.ServerParameters{
        Time:    30 * time.Second,
        Timeout: 10 * time.Second,
    }),
)
```

That leaves every admission control at its grpc-go default:

- **`MaxConcurrentStreams` is unset**, so the default of `math.MaxUint32` applies. One TCP connection
  may open ~4 billion concurrent `Connect` streams.
- **No `keepalive.EnforcementPolicy`**, so a client may ping as often as it likes without being
  terminated, and `PermitWithoutStream` is not decided either way.
- **No `MaxConnectionAge` / `MaxConnectionAgeGrace` / `MaxConnectionIdle`**, so a connection - including
  one whose credential was revoked after it connected - lives until it or the process dies.
- **No unary or stream interceptor**, so there is no place a per-peer connection count, a concurrency
  cap or a rejection metric could live today.

The consequence is that **"per connection" is not a bound**. The 2026-08-15 slice replaced an
attacker-keyed log limiter with a per-connection token bucket, which is the correct shape and is
genuinely four to five orders of magnitude better on lines-per-attacker-CPU. But its whole security
claim is scoped to one connection, and a principal can open as many as it likes.

## Repro / Symptoms

With a single valid agent token (or with `RELAY_ALLOW_AUTO_ENROLL` on and no credential at all), open
N concurrent `Connect` streams to `:9090`. Each gets its own `ingestLogLimiter` with a full burst of 16
and a 6-lines-per-minute refill, so total caller-driven log volume scales linearly in N with no ceiling
anywhere. The same multiplication applies to every other per-connection resource: recv goroutines,
in-flight pool statements, and registry churn.

**The sharper case, and the reason this is not merely the log item's residual.** With auto-enroll on, a
connect storm with **varying hostnames** creates one `workers` row per hostname via
`UpsertWorkerByHostname`, which is `INSERT ... ON CONFLICT (hostname) DO UPDATE`. That is **unbounded
persistent state growth**, not transient contention: the rows survive the connections, survive a
restart, and land in every `GET /v1/workers` page and every dispatcher scan. It is strictly worse than
the log flood the adjacent slice closed.

## Context

Found by the Phase 4 security lens while sizing the win of the log-budget slice honestly. The lens's
formulation is the one to keep: **"per connection" is only a bound if connections are bounded.**

This is pre-existing and is not introduced by that slice - it is simply the first time anything in this
repo made a security claim that rests on connection count. It will not be the last: the recv-loop rate
limiter that keeps getting deferred has the same dependency, and so does any future per-connection
quota.

Two honest counter-arguments that keep this at medium rather than high:

- The agent gRPC port's documented trust model is network reachability, and an attacker who can reach
  `:9090` with a valid token already gets task dispatch. Auto-enroll is off by default.
- grpc-go's defaults are what most services ship with, and nothing here is a *vulnerability* in the
  narrow sense. It is a missing control that several other controls now silently depend on.

The argument for filing it as a **bug** rather than an idea is the `workers`-row growth: that exceeds
what the trust model grants. "Any reachable host may join the pool" does not say "any reachable host may
create unbounded rows in the pool".

## Proposal

Settle these before implementing; the numbers matter more than the mechanism.

- **`grpc.MaxConcurrentStreams(n)`.** An agent uses exactly one stream per connection, so a small `n`
  (single digits) is correct and costs a legitimate agent nothing. This is the cheapest single line in
  the item.
- **`grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: ..., PermitWithoutStream: false})`.**
  Decide `MinTime` against the agent's own keepalive settings in `internal/agent/`, or a legitimate
  agent gets its connection torn down.
- **`MaxConnectionAge` + `MaxConnectionAgeGrace`.** Forces periodic re-authentication, which also
  bounds how long a revoked credential's live connection survives - note
  [[idea-2026-08-09-sse-revoked-token-keeps-streaming]] is the same shape on the HTTP side. Weigh
  against the reconnect churn: `GraceRegistry` and `reconcileRunningTasks` already handle reconnects, so
  the cost is measurable rather than hypothetical.
- **A per-peer connection cap in a stream interceptor**, keyed on the authenticated worker after
  registration and on remote address before it. This is the only one that needs new state, and it is the
  one that actually bounds the fleet-wide multiplication. Consider whether it belongs here at all versus
  at a reverse proxy - relay does not currently assume one, and `ratelimit.go` deliberately does not
  trust `X-Forwarded-For`, so a proxy assumption would be new.
- **Separately, cap auto-enroll row creation.** Even with a connection cap, a slow storm creates rows.
  Options: a rate limit on the auto-enroll path specifically, a total-workers ceiling, or a CIDR
  allowlist ([[idea-2026-06-04-cidr-allowlist-auto-enroll]] already carries that deferral). Decide
  whether this is part of this item or its own; the connection cap and the row cap are separable.

Whatever lands, **the per-connection bounds that depend on it should cite it in source**, so the next
person reading `ingestLogLimiter`'s comment finds the connection bound rather than re-deriving that it
is missing.

## Acceptance / Done When

- `MaxConcurrentStreams` is set to a small explicit value, with a comment stating that an agent uses one
  stream, proven by a test that opens more than the limit and observes the refusal.
- A keepalive enforcement policy is set, with its `MinTime` derived from the agent's configured
  keepalive rather than picked, and a test that a legitimate agent's cadence is not terminated by it.
- Either a per-peer connection cap exists, or the decision that it belongs at a proxy is written down in
  README with the assumption it introduces.
- Auto-enroll row creation is bounded, or the decision to leave it unbounded is written into the
  auto-enroll trust model in README, in the same terms this item uses.
- `ingestLogLimiter`'s doc comment cites whatever bound lands, so "per connection" stops being an
  unqualified claim.

## Related

- Source: `cmd/relay-server/main.go` (the `grpc.NewServer` call), `internal/worker/handler.go`
  (`Connect`, `autoEnrollAndRegister`), `internal/store/query/workers.sql` (`UpsertWorkerByHostname`)
- The slice whose security claim depends on this:
  `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md` section 4 ("what is already
  bounded"), `docs/retros/2026-08-15-tasklog-err-limiter-keying.md` ("the honest scope")
- Adjacent, same trust model: [[bug-2026-08-12-auto-enroll-hostname-takeover]],
  [[idea-2026-06-04-cidr-allowlist-auto-enroll]]
- Same shape on the HTTP side: [[idea-2026-08-09-sse-revoked-token-keeps-streaming]]
- Blocks the honest version of: the deferred recv-loop rate limiter (no item yet; deferred by both the
  2026-08-12 and 2026-08-15 tasklog specs)

## Notes

The generalizable rule this item exists to record: **a bound stated per unit is only a bound if the unit
is bounded.** This is not specific to gRPC. Any future per-worker, per-user or per-token quota inherits
the same question, and the honest place to answer it is at admission, once, rather than in each quota's
doc comment.

Filed deliberately with the residual stated rather than the fix assumed. The single-line
`MaxConcurrentStreams` change is tempting to do opportunistically, and it would be a real improvement -
but it does not close the multiplication, which is per *connection*, not per stream. Do not let the
cheap half close the item.
