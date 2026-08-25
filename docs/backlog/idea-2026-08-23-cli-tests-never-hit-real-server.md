---
title: internal/cli tests hand-write server responses, so a response-shape drift in internal/api is invisible to any CLI test
type: idea
status: open
created: 2026-08-23
priority: medium
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
- `relay workers delete --yes` is exercised end to end against a real server, including the refusal
  paths (409 on a connected worker, 404 on a missing one). It is the first irreversible command, so
  it goes first regardless of which resource area is tackled first.

## Measured, 2026-08-26 (absorbed from a duplicate item)
Filed separately by the worker-delete slice's integration lens, then folded in here. The runtime is
the tell, and it is a cheaper check than reading the test files:

```
grep -rl "go:build integration" internal/cli/   -> no matches
grep -rl testcontainers internal/cli            -> no matches
ok  relay/internal/cli  1.046s
```

Under `-tags integration -p 1`, `internal/cli` finishes in **1.0s** while `internal/store` takes
257s and `internal/worker` 175s. Nothing containerized runs in this package, and nothing ever has.

**The stakes moved with `relay workers delete`.** It is the first irreversible operation in the
product, and its end-to-end coverage is "does the CLI construct the request and parse the response
the fake returns". Four tests, all against a hand-written literal. A drift in the real handler's
status codes, response shape, or error strings is invisible to every one of them - which is no
longer a hypothetical, because that is exactly what happened to `relay logs`.

## Confirmed live, 2026-08-25
This is no longer hypothetical. `relay logs` prints nothing for every job, on every task, and has
done since `a90c727` (2026-05-08) moved `GET /v1/tasks/{id}/logs` to a pagination envelope while the
CLI kept decoding a bare array. The whole suite was green throughout, for precisely the reason this
item states: the fixture at `internal/cli/logs_test.go:47` hand-writes the shape the server stopped
sending, so `TestLogs*` asserts the CLI agrees with the fixture rather than with the handler.

Three and a half months of silent breakage on a user-facing subcommand. Two points the original
filing did not anticipate:

- **The same fixture pattern hides the same drift in the Python SDK** (`task_logs()`, with its own
  stale bare-array fixture at `python/tests/unit/test_client.py:184`). The gap is not CLI-specific -
  it is "every non-web client hand-writes its own idea of the contract". A fix scoped to
  `internal/cli` leaves the second client uncovered.
- **A previous fix aimed at this defect class already missed an endpoint.**
  [[bug-2026-05-26-python-sdk-list-pagination-envelope]] was closed `fixed` on 2026-06-03 with the
  acceptance criterion "All paginated REST endpoints have a corresponding SDK method", three and a
  half weeks after the logs endpoint became paginated. Per-method repair without a lane does not
  converge.

Raised `low` -> `medium` on 2026-08-25. The original priority was set before any confirmed
instance existed; the measured cost is now one wholly broken subcommand plus one broken SDK
method, both attributable to this gap.

## Related
- [[bug-2026-08-25-relay-logs-prints-nothing-envelope-drift]] and
  [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] - the two confirmed instances
- Absorbed 2026-08-25: `idea-2026-08-26-cli-has-no-integration-coverage`, closed as a duplicate.
  Its measurement and its `workers delete` stakes are folded in above; its own Proposal asked for
  exactly that outcome. Sources it cited: `internal/cli/workers_delete_test.go`,
  `internal/cli/workers.go`, `docs/retros/2026-08-26-worker-delete.md`.
- `internal/cli/admin_test.go:16`, `internal/cli/admin_users_test.go`, `internal/cli/admin_output_test.go`
- [[bug-2026-08-25-relay-logs-prints-nothing-envelope-drift]] and
  [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] - the two confirmed instances
- [[idea-2026-06-03-web-e2e-harness]] - the same verification gap for the SPA client
- [[bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped]] - open CLI-output item a real-server lane would exercise for real
