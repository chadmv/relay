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
