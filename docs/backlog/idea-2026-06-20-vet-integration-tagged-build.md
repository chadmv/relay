---
title: Verification flow does not compile integration-tagged files (vet/build -tags integration in CI)
type: idea
status: open
created: 2026-06-20
priority: medium
source: noticed while closing bug-2026-06-19-finishregister-gap-connection-epoch-race (Phase 4 caught a HIGH compile break invisible to unit make test)
---

# Verification flow does not compile integration-tagged files

## Summary
The unit `make test` (`go test ./... -timeout 120s`) does not compile
`//go:build integration` files, so a change to a shared signature can leave an
integration-tagged callsite broken while the default flow stays green. This
session, changing the grace `onExpire` callback signature broke
`cmd/relay-server/startup_reconcile_test.go` (still using `func(string)`); the
break was invisible until the integration build during Phase 4 verify. A signature
or symbol change can ship "done" with a broken integration build that no one
compiled.

## Proposal
Add a fast type-check of the integration-tagged code to the default verification
flow and CI - e.g. a `make vet-integration` target running
`go vet -tags integration ./...` (compiles every package and its integration
tests without running Docker-backed tests). Wire it into CI on every push, since
unlike `make test-integration` it needs no Postgres/p4d containers and is quick.
This catches integration-tagged compile breaks without the cost of running the
full integration suite.

## Notes
Adjacent to [[idea-2026-06-19-race-test-target-perforce-package]] - both close
gaps where the default verification flow misses a failure class (there: data
races without `-race`; here: compile breaks in `-tags integration` code). Could be
folded into the same CI/Makefile change if that idea is picked up.

## Related
- `cmd/relay-server/startup_reconcile_test.go` (the integration-tagged file broken
  this session by the grace `onExpire` signature change).
- `Makefile` (`test`, `test-integration` targets).
- [[idea-2026-06-19-race-test-target-perforce-package]] - adjacent verification-gap idea.
- Retro: `docs/retros/2026-06-20-finishregister-gap-connection-epoch.md`.
