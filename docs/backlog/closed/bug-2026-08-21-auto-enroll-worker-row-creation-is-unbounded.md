---
title: Auto-enroll worker row creation is unbounded, and the fix belongs on the auto-enroll path rather than on the upsert
type: bug
status: closed
created: 2026-08-21
closed: 2026-08-25
resolution: fixed
priority: medium
source: Section 5.2 of the 2026-08-20-grpc-admission-bounds spec; the half of the source item that slice closed by written decision rather than by code
---

# Auto-enroll worker row creation is unbounded, and the fix belongs on the auto-enroll path rather than on the upsert

## Summary

With `RELAY_ALLOW_AUTO_ENROLL=true`, any host able to reach `:9090` can create **one persistent
`workers` row per distinct hostname it claims**, and nothing bounds the total.

`autoEnrollAndRegister` (`internal/worker/handler.go`) runs, per connection, in one transaction:
`GetWorkerByHostnameForUpdate` -> revoked check -> `UpsertWorkerByHostname` -> `SetWorkerAgentToken`.
`UpsertWorkerByHostname` (`internal/store/query/workers.sql`) is
`INSERT ... ON CONFLICT (hostname) DO UPDATE`, and `reg.Hostname` is caller-supplied and validated
nowhere - the function's own comment says so.

The rows **survive the connection that created them, survive a server restart, and appear in every
`GET /v1/workers` page and every dispatcher scan.** That is unbounded persistent state growth, not
transient contention.

**The nuance that determines where a fix goes, and it is the reason this item is not a one-liner
against the upsert:** the **enrollment-token path does not have this property**. `enrollAndRegister`
calls the same `UpsertWorkerByHostname`, but inside a transaction that also calls
`ConsumeAgentEnrollment` and returns `errEnrollmentNotConsumable` when `rows == 0`, which rolls the
whole transaction back. It also rejects an already-consumed or expired token up front. **One
admin-issued enrollment token buys exactly one row**, and row creation on that path is bounded by
admin issuance. `reconnectAndRegister` creates no rows at all.

So a bound belongs on the **auto-enroll path specifically, never on `UpsertWorkerByHostname`**. A
guard on the shared statement would break the one path that is already correct.

## Repro / Symptoms

With `RELAY_ALLOW_AUTO_ENROLL=true`, open `Connect` streams to `:9090` with no credential and a
`RegisterRequest` naming a fresh hostname each time. Each connection leaves one `workers` row behind.
Observed: `GET /v1/workers` fills with rows for machines that do not exist, the dispatcher scans them
on every pass, and nothing removes them. Disabling auto-enroll afterwards does not clean them up.

## Context

Carved out of [[bug-2026-08-15-grpc-connection-admission-is-unbounded]] by
`docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md` section 5.2, using the permission that
item explicitly granted ("Decide whether this is part of this item or its own; the connection cap and
the row cap are separable"). The connection-admission slice closed its acceptance criterion for this
by writing the cost into README's auto-enrollment section in the source item's own terms, and filed
this to carry the code half.

**What that slice DID bound, stated precisely so this item does not overclaim.** With
`RELAY_GRPC_MAX_CONNS_PER_IP=64`, one source prefix can have at most 64 registrations **in flight at
once**, each requiring a full transaction. It does **not** bound the total, because the rows outlive
their connections. A slow drip from one address defeats the concurrency cap entirely.

**Two honest counter-arguments, which is why this is medium and not high.** `RELAY_ALLOW_AUTO_ENROLL`
is off by default, and its documented trust model is that any host able to reach gRPC is trusted. The
argument for filing it as a bug anyway is the source item's: "any reachable host may join the pool"
does not say "any reachable host may create unbounded rows in the pool", and the rows are persistent
in a way the trust model never contemplated.

**Relationship to the two adjacent open items, checked rather than assumed:**

- [[bug-2026-08-12-auto-enroll-hostname-takeover]] is a **different defect on the same path**: claiming
  an **in-use** hostname seizes an existing worker's identity. Its proposed fix (refuse when the
  existing row's `agent_token_hash` is non-NULL) does **not** bound creation of rows for **new**
  hostnames, which is this item. The two should be looked at in one sitting - both are guards inside
  `autoEnrollAndRegister`, both need the same README paragraph edited - but they are separate
  decisions and one does not close the other. README already leads with takeover as the larger of the
  two costs.
- [[idea-2026-06-04-cidr-allowlist-auto-enroll]] would bound this **as a side effect** by changing the
  trust boundary. It is a different product with a different operator story, and it is already open.
  Do not absorb it here; if the allowlist is what ships, this item closes as "solved by a
  trust-boundary change" and says so.

## Proposal

Three candidate mechanisms, which is precisely why the admission slice declined to pick one inside a
slice about admission. They are three different products and the choice is a product decision.

- **A rate limit on the auto-enroll path.** Throttles a storm; does not stop a slow drip. Cheapest,
  and it composes with `internal/api/ratelimit.go`'s existing per-source shape, though this is the
  gRPC side and would key on the TCP source address the way `netlimit.hostKey` does. Settle whether it
  reuses `hostKey`'s IPv6 `/64` aggregation, which exists precisely so a per-source key is not free to
  defeat.
- **A total-workers ceiling.** Bounds the absolute worst case and nothing else does. It is a **denial
  primitive against legitimate fleet growth** and needs an operator story for hitting it: what the log
  says, what the agent sees, and how an operator raises it without downtime. Do not ship this without
  that story.
- **A CIDR allowlist.** Already [[idea-2026-06-04-cidr-allowlist-auto-enroll]]. Changes the trust
  boundary rather than bounding a resource, which is a stronger and cleaner answer where an operator
  can enumerate their networks, and no answer at all where they cannot.

Whichever lands, settle these:

- **Does anything reap unused auto-enrolled rows?** Nothing prunes `workers` today, and a bound on
  creation without a reaper means the first attacker's rows are permanent. A TTL on a row that has
  never had a successful post-enrollment reconnect may be a better shape than a creation cap, and it
  is the one option that helps the deployments that have **already** been hit.
- **What does a refused auto-enroll tell the caller?** The refusal must not disclose whether the
  hostname exists, matching the constraint [[bug-2026-08-12-auto-enroll-hostname-takeover]] already
  states.
- **Is the log line for a refusal budgeted?** The registration path's log sites are already known to
  sit outside the per-connection log budget
  ([[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]), and a new
  attacker-reachable `log.Printf` there would be a fresh instance of the flood class the 2026-08-15
  slice closed.

## Acceptance / Done When

- Under `RELAY_ALLOW_AUTO_ENROLL=true`, a caller that repeatedly registers with fresh hostnames stops
  creating rows at a stated bound, proven by a test that is RED against today's code.
- The **enrollment-token path is unaffected**, proven by a test: one valid enrollment token still
  creates exactly one row, whatever the auto-enroll bound is doing. `UpsertWorkerByHostname` itself is
  unchanged, or the change is proven not to reach that path.
- `reconnectAndRegister` still creates no rows, and a legitimate agent reconnecting under its own
  hostname is never refused by the new bound.
- The refusal discloses nothing about whether the hostname exists, and its log line is either budgeted
  or explicitly exempt with a comment saying why.
- README's auto-enrollment cost paragraph is updated: the sentence that currently reads "Nothing bounds
  the total" is the exact prose this item falsifies, and it must be corrected rather than left standing.
- If the chosen mechanism is a total ceiling, README documents what an operator sees when the fleet
  hits it and how to raise it.

## Related

- Source: `internal/worker/handler.go` (`autoEnrollAndRegister`, and `enrollAndRegister` for the
  contrast), `internal/store/query/workers.sql` (`UpsertWorkerByHostname`,
  `GetWorkerByHostnameForUpdate`, `SetWorkerAgentToken`)
- Carved out of: [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]
- Different defect, same function, same README paragraph, to be looked at together:
  [[bug-2026-08-12-auto-enroll-hostname-takeover]]
- Would solve this as a side effect, different product:
  [[idea-2026-06-04-cidr-allowlist-auto-enroll]]
- Constrains any new log line on this path:
  [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]
- Design: `docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md` (Non-Goals),
  `docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md` sections 2.7 and 5.2,
  `docs/retros/2026-08-21-grpc-admission-bounds.md`
- README's current written-down decision: the "What auto-enrollment costs, stated plainly" paragraph

## Notes

The finding worth preserving from the spec, because it is the thing an implementer would otherwise get
wrong on the first attempt: **the source item's Repro says "With a single valid agent token (or with
`RELAY_ALLOW_AUTO_ENROLL` on...)", which reads as though both credential paths create rows.** They do
not. The token path's upsert and single-use consume share one transaction, and `rows == 0` rolls the
upsert back. Anyone who fixes this by guarding `UpsertWorkerByHostname` will have penalised the one
path that was already correct, and the test suite as it stands today would probably not object.
</content>

## Resolution

Fixed. `RELAY_AUTO_ENROLL_WORKER_CEILING` (default 1024, `0` disables) refuses token-less
auto-enrollment once that many non-revoked workers exist, checked inside the transaction **before**
the insert so the refusal is free of side effects.

The item offered three mechanisms and called the choice a product decision. The spec picked the
ceiling on the item's own acceptance criterion - "stops creating rows at a **stated bound**" - which
a rate limit cannot give (it loses to a slow drip) and a reaper cannot give (its steady state is
rate x TTL, and rate is unbounded). The ceiling and the reaper are complements, not alternatives;
the reaper is filed separately.

The conductor's lean - that closing the takeover would shrink this attack - was **refuted**: closing
takeover confines the attacker to hostnames with no row, and creating a row for a hostname with no
row IS this attack. The two are independent, exactly as the item said.

The bound is approximate and every site says so rather than claiming an exact cap: two concurrent
auto-enrolls at `ceiling - 1` both pass under read-committed isolation, so the honest bound is
`ceiling + RELAY_GRPC_MAX_CONNS`.

The operator story the item required is in README: revoke junk rows (frees budget immediately, no
restart), use enrollment tokens (never subject to the ceiling), or raise the knob (requires a
restart, said plainly). Review then found the first remedy is a treadmill under active attack -
revoking frees ceiling budget without freeing the hostname, so an attacker refills with new
hostnames and the revoked bucket grows unbounded. README now says that, and "Row growth is bounded"
is corrected to "Non-revoked row growth is bounded".

Refusals are COUNTED, never logged: a refusal is unboundedly repeatable by the same caller and the
per-connection log limiter is not allocated until after registration. The counters live on `Handler`;
publishing them on `GET /v1/server/counters` is filed separately.

See `docs/retros/2026-08-25-auto-enroll-guards.md`.
