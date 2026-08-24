---
title: With both gRPC caps disabled the counters payload asserts "this control ran and stopped nothing" where the truth is "this control measured nothing"
type: idea
status: open
created: 2026-08-21
priority: low
source: Phase 4 refutation (b) of the 2026-08-21-silent-drop-observability slice 1; a residual disclosed in three places and deliberately not closed
---

# With both gRPC caps disabled the counters payload asserts "ran and stopped nothing" where the truth is "measured nothing"

## Summary

`GET /v1/server/counters` fixes a contract with exactly two states per section, and the distinction is
the whole point of the endpoint:

- **A section of zeros** means "this control ran and stopped nothing."
- **An ABSENT section** means "this build or this replica does not have that control wired."

There is a third state and the payload cannot express it. When **both** gRPC connection caps are
disabled (`RELAY_GRPC_MAX_CONNS=0` and `RELAY_GRPC_MAX_CONNS_PER_IP=0`), `netlimit.Listener.Accept`
returns the accepted connection **unwrapped** and never calls `admit`, so no accounting happens at all.
`main` still wraps the listener unconditionally - it must, because
`TestServerCountersIsWiredByMain` requires exactly one unconditional assignment on the reachability
chain - so the `grpc_admission` section is **present**, and every field of its `levels` half reads `0`
with any number of live connections.

By the payload's own contract that is an **affirmative false statement**, not an ambiguity: the
endpoint says the admission control ran and held nothing, where the truth is that it measured nothing.
An operator reading `live_total: 0` on a busy server has been told something wrong by a surface whose
entire purpose is to stop silent controls from lying by omission. **This is the endpoint's own subject
one layer down.**

## Repro / Symptoms

1. Start `relay-server` with `RELAY_GRPC_MAX_CONNS=0` and `RELAY_GRPC_MAX_CONNS_PER_IP=0` - a
   configuration README explicitly supports, for an operator who caps connections at a proxy instead
   and wants grpc-go's `conn.(*net.TCPConn)` assertion to succeed.
2. Connect any number of agents.
3. `GET /v1/server/counters` as an admin.
4. Observed:

   ```json
   "grpc_admission": {
     "counts": { "refused_total": 0, "refused_per_source": 0 },
     "levels": { "live_total": 0, "distinct_sources": 0, "max_per_source": 0 }
   }
   ```

   which is byte-identical to a wired, enabled, genuinely idle listener.

The startup line does say `total DISABLED, per-source-IP DISABLED`, so the information exists in the
process. It is simply not on the surface built for it, and an operator polling an endpoint is not
reading a startup line from three weeks ago.

## Context

**This was found, argued and deliberately not closed during slice 1**, and both halves of that decision
should be preserved by anything that closes this item.

The engineer **declined** to make the section absent in this configuration, and that call was correct:
making `absent` mean "not wired **OR** wired-but-disabled" would degrade the vocabulary **permanently,
for every future section**, in order to disambiguate one configuration of one section. Three more
sections are queued behind this contract (`ingest_log_budget`, `task_log_fence`, `watchdog`), and each
would inherit a weakened `absent`. **Do not close this item by making the section absent.**

The disclosure route was taken instead, in three places:

- `netlimit.Listener.Stats`'s doc comment ("WHEN BOTH CAPS ARE DISABLED, EVERY LEVEL READS ZERO NO
  MATTER HOW MANY CONNECTIONS ARE LIVE ... A zero here therefore means 'not measured', not 'nothing
  there'"), pinned by the pre-existing `TestLimitListener_ZeroDisables`, which asserts
  `Stats{} == l.Stats()` after admitting 200 connections with both caps off and goes RED if anybody
  "fixes" this by accounting on the disabled path.
- `internal/api/server_counters.go`'s "WHAT THIS ENDPOINT DOES NOT BUY" block.
- README's Server counters subsection.

`server_counters.go` also records why the obvious fix was rejected: closing it in the payload needs
either **a boolean** (banned by the counts-only rule, which admits counts and levels and nothing else)
or **the configured caps as extra fields** - and `max_per_source` as an observed maximum sitting next
to `max_per_source` as a configured cap is **a naming trap** nobody should ship.

That is a rejection of two specific shapes, not a proof that no shape works. Which is why this is an
item rather than a permanent Known Limitation.

## Proposal

To be argued at spec time rather than adopted as written. **The bar is: express the third state without
weakening `absent` and without a name collision.**

- **A distinctly-named configured-limits object is the obvious candidate.** Something like
  `"grpc_admission": { "counts": {...}, "levels": {...}, "limits": { "max_total": 0, "max_per_source": 0 } }`,
  where `0` already means "disabled" in relay's own env vocabulary and the operator can therefore read
  the all-zero `levels` correctly. `limits` is a third classification alongside `counts` and `levels`,
  which is a **contract expansion for all four sections** and must be decided as one - that is the real
  cost, and it is why this was not done inside slice 1.
- **Check the naming trap explicitly.** `limits.max_per_source` next to `levels.max_per_source` is the
  exact collision `server_counters.go` warns about. Either the nesting makes it unambiguous enough (the
  JSON path differs, the field name does not) or the configured field needs a different name
  (`per_source_cap`?). Decide this rather than discovering it in review.
- **Whatever is added is still counts, levels or configuration - never a boolean, never a string.**
  Configured caps are server-side integers from env parsing, not caller-supplied bytes, so they do not
  need a `counterPayloadExemption`. Confirm that against `TestCounterPayloadCarriesNoIdentifiers`'s
  unsigned-integer rule before writing it: if a new field needs an exemption, the design is wrong.
- **Consider doing nothing, and say so if so.** The disclosure is already in three places and the
  configuration is a deliberate operator choice made by somebody who knows they disabled the caps. The
  honest counter-argument: the person reading the endpoint during an incident is frequently not the
  person who set the env vars.
- **Do not close this by making the section absent** (see Context), and do not close it by accounting
  on the disabled path - `TestLimitListener_ZeroDisables` will say so, and the unwrapped-conn branch
  exists precisely so grpc-go's `*net.TCPConn` assertion succeeds.

## Acceptance / Done When

- An operator reading `GET /v1/server/counters` alone can distinguish "the admission control is live
  and holding nothing" from "the admission control is disabled and measuring nothing", without
  consulting a startup log line or the process environment.
- `absent` still means exactly "not wired on this build or this replica", unchanged, for every section.
- `TestLimitListener_ZeroDisables` still passes with no assertion changed - the disabled path still does
  no accounting.
- No new field carries a caller-supplied byte, and no new `counterPayloadExemption` is required.
- Whatever classification is added (`limits` or otherwise) is decided for **all four sections** at once,
  the way `counts`/`levels` was, so no later slice reshapes a shipped payload.
- The three existing disclosures (`netlimit.Stats`'s comment, `server_counters.go`'s comment, README)
  are updated or removed rather than left contradicting the new behaviour.

## Related

- Source: `internal/netlimit/listener.go` (`Accept`'s both-caps-disabled branch, and `Stats`'s doc
  comment), `internal/netlimit/listener_test.go` (`TestLimitListener_ZeroDisables`, the guard),
  `internal/api/server_counters.go` ("WHAT THIS ENDPOINT DOES NOT BUY", and the rejection of both
  candidate fixes), `README.md` (the Server counters subsection)
- The contract this must not weaken: `docs/superpowers/specs/2026-08-21-silent-drop-observability.md`
  section 9 (absent-not-zero, decision D11) and section 6.3 (the counts-only rule)
- The slice that found and disclosed it:
  `docs/superpowers/plans/2026-08-21-silent-drop-observability-slice1.md` (R5),
  `docs/retros/2026-08-21-silent-drop-observability-slice1.md`
- The three sections that will inherit whatever this decides:
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]],
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]],
  [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]]
- The item slice 1 closed: [[idea-2026-08-21-netlimit-occupancy-is-unobservable]]

## Notes

Filed at **low** priority deliberately. Nothing is broken, the configuration is uncommon, the
information exists at startup, and the residual is disclosed in three places. The reason it is an item
rather than a Known Limitation is the shape rather than the severity: **this is the endpoint's own
subject applied to itself.** A surface built because "fewer signals than normal is indistinguishable
from healthy" has a configuration in which it reports zeros that are indistinguishable from healthy.
That deserves to be tracked where somebody will trip over it, not only where somebody already reading
the right doc comment will find it.

**The sequencing argument, which matters more than the priority:** the fix is a contract expansion
across all four sections, so it is much cheaper before slices 2-4 ship than after. If it is going to be
done at all, doing it as part of slice 2 costs almost nothing extra; doing it after slice 4 means
reshaping a payload with four populated sections.

## 2026-08-24: slice 4 shipped and DEFERRED this item deliberately. Its sequencing argument is now worth one section, not three.

Slice 4 (`silent-drop-observability-slice4`) considered folding this in - the roadmap said it "rides
cheapest here" - and declined, on two grounds recorded in
`docs/superpowers/plans/2026-08-24-silent-drop-observability-slice4.md`:

1. **The cheap window has largely closed.** The argument for doing it during slice 2 was that a
   `limits` classification touches every section, so it is cheaper before the sections exist. With
   three of four already shipped, the marginal saving of doing it during slice 4 rather than after is
   **one section**, not three.
2. **The watchdog does not reproduce the defect's sharp form.** `netlimit` publishing
   `live_total: 0` on a busy server is an *affirmative false statement*. Every number a disabled
   watchdog publishes is *literally true* - it swept nothing because it ran and found nothing, or
   because it is disabled and the distinction is invisible. Weaker, so it is a second instance rather
   than a second sharp case.

**Slice 4 paid one forward cost to keep this deferral cheap**: the `watchdog` section ships
**counts-only, with no `levels` half at all**. That was forced by the same reasoning this item makes
- the only `levels` candidates were `swept_workers_max` (a compile-time constant, which would have to
MOVE when a `limits` half is added, breaking a published payload) and `swept_workers_tracked` (a
restatement of `len(swept_by_worker)`). So adding `limits` to this section later is **purely
additive, with zero field moves**. Check the other three sections for constants sitting in a `levels`
half before designing the migration; that is where the breaking changes will be.

Note also that this item's own framing has a second instance now: with both gRPC caps disabled the
payload cannot say "not measured", and a disabled watchdog cannot either. Two subsystems, one
missing vocabulary.
