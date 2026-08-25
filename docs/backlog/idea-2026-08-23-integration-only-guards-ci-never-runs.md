---
title: Three of the admission/counters/log-fence security guards run only under the integration tag CI never executes
type: idea
status: open
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - integration-tester lens finding
---

# Three of the admission/counters/log-fence security guards run only under the integration tag CI never executes

## Summary
`.github/workflows/go-ci.yml` runs `make vet-integration` (`go vet -tags integration ./...` -
compiles, never runs) and `go test -race ./...` with no tag, so integration-tagged tests are
excluded from CI's build entirely. Three guards from the #139-#142 batch now depend on that gap
silently: `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll`
(`internal/worker/handler_ingest_budget_integration_test.go:458`) is the sole proof that a
stale-epoch chunk is dropped before `broker.Publish`; `cmd/relay-server/grpc_admission_e2e_integration_test.go`
is the only place main's real wiring runs end to end; and
`internal/api/server_counters_realsocket_integration_test.go` is the only test reading real
sockets back through the real admin route (the non-integration counters test feeds a
`fakeAdmissionSource`).

## Context
The gap itself is a known, accepted tradeoff - the closed
`idea-2026-06-20-vet-integration-tagged-build` item scoped itself away from running the suite in
CI. What changed is that security-property guards now live exclusively behind it, and this
project has already once found the epoch fence's drop-before-publish consequence guarded only in a
lane CI never runs (fixed in slice 3 by building a default-lane harness). This item is the
systematic version of that one-off fix.

## Proposal
Per guard, either add a non-Docker default-lane regression test for the property (the slice-3
precedent shows the harness often exists already), or stand up a Docker-capable CI lane for the
integration suite (cost: p4d image pulls, ~10min). A written per-guard decision is acceptable
where neither is worth it - what is not acceptable is the current silent state.

## Acceptance / Done When
- Each of the three named guards either has a default-lane sibling covering its core property, a
  CI lane that runs it, or a written decision in the test's own comment saying why not.
- The decision generalizes: a rule (in CLAUDE.md or the test-writing docs) states that a
  security-property guard must not be integration-only without a recorded reason.

## Related
- `.github/workflows/go-ci.yml`, `Makefile` (`vet-integration`)
- `docs/backlog/closed/idea-2026-06-20-vet-integration-tagged-build.md` - the accepted-tradeoff record this item revisits
- [[idea-2026-06-03-web-e2e-harness]] - the frontend half of the same verification-story gap

## 2026-08-24: a fourth instance, the sharpest yet, and this item's own remedy menu is incomplete for it

The finishregister-strand slice supplied a measured instance rather than another example.

`.github/workflows/go-ci.yml` runs `go test -race ./...` with no tags. In `internal/worker`, **every
test that drives a SUCCESSFUL worker registration is `//go:build integration`** - `handler_test.go`,
`handler_teardown_test.go`, and the new strand integration test. So the default lane structurally
cannot observe the registration success path at all.

What that cost, concretely: the slice needed to pin one line, `handedOff = true`, which decides which
of two deferred releases owns the worker generation. Deleting it left **all 21 packages green**, and
that mutant makes every successful registration mark its own worker offline, wipe its metrics entry,
and requeue a healthy agent's running tasks. Fleet-wide, on a green CI.

**This item's remedy menu does not cover the case.** The "add a default-lane sibling test" option is
unavailable here: `applyInventory` opens a transaction on the concrete `*pgxpool.Pool` unconditionally,
so a pool-less fixture panics before the success path is reached. The mechanical cause is filed
separately as [[idea-2026-08-24-handler-pool-has-no-seam]] - **the two are not duplicates**. Fixing the
CI lane makes the existing integration guards run; fixing the seam makes a default-lane behavioural
test possible. Either alone leaves the other gap open, and the seam item is the cheaper of the two.

The fallback the slice actually took - a `go/parser` guard in the default lane - is worth recording as
a data point on cost: it was **evaded twice** before it held, each time by a construct that is nil in
one context and real in the other (`h.Metrics != nil`, then `h.pool != nil` nested inside the guarded
branch). A behavioural test would have caught both on the first run. Treat a parser guard as the
expensive fallback it is, not as an equivalent substitute.

## 2026-08-24: the frontend twin was WORSE than the Go one, and it is now half-closed

This item was filed about Go guards behind `//go:build integration`. The frontend had the same disease
in a more advanced form and nobody had named it: **there was no web CI at all**. No workflow referenced
npm, node or `web/`, so `npm test` (1116 tests), `tsc -b` and `npm run build` had never run in CI on any
commit. The Go guards at least ran locally by convention; the frontend suite was advisory everywhere.

`.github/workflows/web-ci.yml` (2026-08-24) closes that half: unit tests, type-check, production build
and the browser suite now run on every PR.

**What this does NOT close, and the distinction matters for scoping:** the Go integration lane still
runs no tags in CI, which is this item's original subject and is untouched. Also note the new workflow
inherits a version-currency problem of its own -
[[idea-2026-08-24-web-ci-node-20-actions-and-unverified-node-version]].

One measurement worth carrying, because it prices the item: during the 2026-08-24 finishRegister slice a
line whose deletion left all 21 Go packages green had to be guarded by a `go/parser` test instead of a
behavioural one, and **that guard was evaded five times** before it held. The cost of a lane CI never
runs is not the missing signal alone; it is the elaborate and fragile substitute built in its place.

## Progress

- 2026-08-25: one named instance removed. `internal/worker`'s successful-registration
  path had every witness behind `//go:build integration`; narrowing `Handler.pool` to a
  one-method `txBeginner` interface put five behavioural tests in the default lane
  (`internal/worker/handler_register_success_test.go`) and let
  `handler_handoff_guard_test.go` shed five clauses. The item stays open - the remaining
  instances are untouched.
