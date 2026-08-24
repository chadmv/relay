---
title: internal/cli tests hand-write server responses, so a response-shape drift in internal/api is invisible to any CLI test
type: idea
status: open
created: 2026-08-23
priority: low
source: 2026-08-23 deep roadmap refresh - integration-tester lens finding
---

# internal/cli tests hand-write server responses, so a response-shape drift in internal/api is invisible to any CLI test

## Summary
`internal/cli` has zero integration-tagged tests (`grep -rl "go:build integration" internal/cli/`
returns nothing), and every CLI test drives an `httptest.NewServer(http.HandlerFunc(...))` that
hand-writes the JSON response literal rather than calling into `internal/api`'s real handlers
(`internal/cli/admin_test.go:16`, `admin_users_test.go`, `admin_output_test.go`, and siblings). A
handler response-shape change can silently break every `relay` subcommand with the whole suite
green.

## Context
This is precisely the "invented contract" bug class that motivated
[[idea-2026-06-03-web-e2e-harness]] - mocks mirrored a shape the real backend never returns - just
unaddressed for the CLI binary. Naturally sequenced after or alongside the harness item: driving
the real binary against a real server is the same fix shape, for a different client.

## Proposal
One integration-tagged CLI lane that boots a real `relay-server` (the api package's testcontainer
helpers exist) and drives a representative subcommand per resource through the real handlers.
Full per-flag coverage stays in the fast `httptest` tests; the integration lane exists to catch
shape drift, not flag logic.

## Acceptance / Done When
- At least one integration-tagged test per CLI resource area (workers, jobs, schedules, admin)
  exercises the real server end to end.
- A deliberately introduced response-shape change in `internal/api` reddens the CLI lane (proven
  once, as the discriminating mutation).

## Related
- `internal/cli/admin_test.go:16`, `internal/cli/admin_users_test.go`, `internal/cli/admin_output_test.go`
- [[idea-2026-06-03-web-e2e-harness]] - the same verification gap for the SPA client
- [[bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped]] - open CLI-output item a real-server lane would exercise for real
