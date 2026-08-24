---
title: internal/api's counter payload guards are proven against fixtures, not producers, and that package structurally cannot fix it
type: idea
status: open
created: 2026-08-24
priority: medium
source: Phase 4 security lens of the 2026-08-24 silent-drop-observability-slice4 slice, reproduced by mutation
---

# internal/api's counter payload guards are proven against fixtures, not producers

## Summary

`GET /v1/server/counters` is defended by two payload walks and a `counterPayloadAllowList` of
per-path exemptions, whose whole job is to keep a caller-supplied byte out of an admin-facing
document. Every one of those predicates runs against **fake sources whose values are Go literals in
`internal/api/server_counters_test.go`**. None has ever seen a byte the real producer generated, so
the guard proves a property of the test file rather than of the server.

`internal/api` cannot close this itself: the producing packages (`internal/scheduler`,
`internal/worker`, `internal/netlimit`) import `internal/api`, so the test cannot import them back.

## Repro / Symptoms

Measured during slice 4, on the section that has the only map leaf in the payload:

1. Mutate `internal/scheduler`'s producer so every `swept_by_worker` key is
   `"build-agent-07.corp.example\n10.0.0.7"` - a hostname with an injected newline, which is exactly
   the shape the exemption's own `why` string cites as what must never get in.
2. `go test ./internal/api/...` - **green**.
3. `go test ./cmd/relay-server/...` - **green** (before slice 4's fix).

The `jsonOK` predicate that rejects that key never ran, because the only value it ever sees is
`threeDistinctSweeps()`, a literal in the test file.

## Context

Slice 4 closed this **for the `watchdog` section only**, in `cmd/relay-server` - the one package that
can import both the producer and the route
(`TestBuildHTTPServer_TheServedWatchdogKeysAreCanonicalUUIDsUnderTheCap`, which drives 260 real
workers through a real `Watchdog` and asserts key shape and the cap against the decoded HTTP
response). The other three sections have no equivalent in the default lane; `grpc_admission` and the
two ingest sections are reached only by integration-tagged tests.

This is **not** the same gap as [[idea-2026-08-23-integration-only-guards-ci-never-runs]]. Fixing the
CI lane makes the integration-tagged guards run; it does not make `internal/api`'s predicates check
anything but fixtures. The two compose: a section could be green in both lanes and still have never
had its allow-list predicate applied to a producer byte.

## Proposal

Adopt slice 4's shape for the remaining sections: assert the real decoded payload in
`cmd/relay-server`, where both sides are importable, rather than adding more fixture-driven cases in
`internal/api`. Consider whether the per-section assertions can share one helper that takes a wired
`httpServerDeps` and applies `counterPayloadAllowList`'s predicates to the real response - the
predicates are already data, so the helper would be small.

Worth deciding at the same time: whether `counterPayloadAllowList` should live somewhere both
packages can import, so the two copies of the canonical-uuid rule
(`internal/api`'s `canonicalUUIDRe`, `cmd/relay-server`'s `canonicalWorkerKeyRe`) collapse to one.

## Acceptance / Done When

- Every section of the counters payload has at least one assertion in the default lane that applies
  the allow-list's predicates to bytes a real producer generated.
- The security lens's mutation (a producer emitting a non-canonical key) goes RED for every section,
  not only `watchdog`.
- The comments claiming enforcement lives in the predicates say where it actually lives.

## Related

- `internal/api/server_counters_test.go` - `counterPayloadAllowList`, the two payload walks.
- `cmd/relay-server/counters_wiring_test.go` - the pattern to copy.
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - adjacent, and explicitly not the same.
- [[idea-2026-08-24-watchdog-counters-never-driven-by-real-rows]] - the sibling gap one layer down.
