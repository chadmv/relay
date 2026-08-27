---
title: internal/cli tests hand-write server responses, so a response-shape drift in internal/api is invisible to any CLI test
type: idea
status: closed
created: 2026-08-23
closed: 2026-08-27
resolution: fixed
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

## Second instance, measured 2026-08-26

`bug-2026-08-25-relay-logs-prints-nothing-envelope-drift` is now **fixed**, and fixing it is the
sharpest measurement this item has. The point fix replaced four hand-written bare-array literals
with one `writeTaskLogPage` helper that simulates `handleGetTaskLogs`, with json tags deliberately
independent of the production types so a wrong tag cannot make both sides agree. That is a strictly
better single point of failure and it is still a simulator: `internal/cli/logs_test.go` grew from 8
tests to 42 and about 1,996 lines, so substantially MORE fixture logic now goes unexercised against
a real server than before the fix.

`writeTaskLogPage` is the seam a real-server lane would replace. It is the single place that shapes
the fake envelope, so pointing the existing `httptest.NewServer` fixtures at a live `relay-server`
plus a Postgres container keeps the assertions and swaps the seam, rather than a rewrite.

**Priority should NOT move again.** It went `low` -> `medium` on two live instances, and both are
now fixed or filed. The residual risk is prospective, and the closing cost is unchanged: `internal/cli`
has zero `//go:build integration` files and no testcontainers usage, so a real-server lane means
introducing that dependency into a package that has none. That is an infrastructure lift, correctly
scoped as its own item rather than folded into a bugfix.

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

## Update 2026-08-27 - instance in a second language, and the lane earned its keep on first contact

`python/tests/integration/` has **never executed in CI**. `.github/workflows/python.yml` has two
jobs, `test` (which runs `pytest tests/unit` across 15 matrix cells) and `lint`. Neither touches
`tests/integration`, so `test_smoke.py::test_submit_and_wait` - the only test that calls
`task_logs()` against a real server - had never run anywhere.

That is exactly why [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] survived three
and a half months AND survived a previous fix aimed at the same file and the same defect class.

**The strongest evidence yet for this item: the lane was stood up by hand during that fix, and it
immediately found a bug that four separate reading-based review lenses had passed.** `Job.labels`
arrives on the wire as `null` (a handler marshals a Go nil map when the field is omitted) while the
SDK modelled it as a required dict, so `list_jobs()` raised for any job submitted without labels.
No amount of code reading found it, because the SDK-vs-handler sweep that slice ran checked each
response's CONTAINER shape and never each FIELD's nullability. Only a real server sends a real
`null`. It also found an unrelated live server bug
([[bug-2026-08-27-scheduled-jobs-null-job-spec-bypasses-required-guard]]).

Standing it up needs a Postgres service plus a built `relay-server` in the workflow, and for full
value a `relay-agent` too - the paging boundary test that proved the cursor is passed verbatim
needed a task that actually produced 1221 log rows, which only a real agent does.

Add to Related: `python/tests/integration/`, `.github/workflows/python.yml`.

## Resolution

Closed by the 2026-08-27 CLI real-server integration lane slice (spec + plan in
`docs/superpowers/`; commits `2273656..b55438f`).

`internal/cli` now has 18 `//go:build integration` tests that drive the real `internal/api`
handlers over real HTTP against a real Postgres, covering all four Acceptance areas -
workers (including `delete --yes` across its **four**-deep refusal ladder, 403/400/404/409;
the item said two), jobs, schedules and admin - plus logs, which was not one of the four but
had the confirmed live breakage.

`.github/workflows/go-ci.yml` gains a `cli-integration` job that actually runs the lane, via
a `services: postgres` block rather than testcontainers. **This half was not in the item's
Proposal and is what makes the rest matter**: `go-ci.yml` ran `go test -race ./...` with no
tags, so an integration-tagged lane would have landed in a dead zone
([[idea-2026-08-23-integration-only-guards-ci-never-runs]] is the standing record of that gap;
this slice does not close it, but supplies the mechanism that could).

The harness (`newIntegrationDSN`) has two modes selected by `RELAY_TEST_DATABASE_URL`: a
testcontainer per test when unset (every other integration package's model, zero-config for
developers), and one freshly `CREATE`d `relaytest_<hex>` database per test on a supplied
server when set. Measured: ~40s vs ~12s for the same 18 tests. CI uses the second.

**The lane found two live user-facing bugs before any synthetic mutation was written**, which
is stronger evidence than the discriminating mutation the Acceptance asked for, because nobody
authored them to be caught:

- `taskResp` decoded `json:"command"` (`[]string`) while `toTaskResponse` has emitted
  `json:"commands"` (`[][]string`) since migration `000008_task_commands`, so
  `relay get --json` printed `"command":null` and carried no task definitions for ~3 months.
- `relay list/get --json` silently dropped 7 more server fields (`labels`, `retry_count`, and
  the five populated list-enrichment fields). `--json` is a lossy re-encode through a
  hand-written mirror, not a passthrough.

The discriminating mutation was proven anyway and then some: M0 control, M1 (envelope -> bare
array), M2 (`next_seq` -> `nextSeq`) all redden the lane while `CLI-DEFAULT` stays green - the
half the item's Acceptance omitted, and the half that proves the lane is not redundant with the
89 existing `httptest` fixtures. All 27 `jobResponse`/`taskResponse` json-tag renames are now
killed; before the arity guards, 9 survived, one of which made `relay list` print
`0001-01-01 00:00` for every job on the farm with all tests green.

### What the item got wrong

Verification refuted five of its claims. The sharpest: "the residual risk is prospective" was
false - both bugs above were live at HEAD when it was written. Also: "the api package's
testcontainer helpers exist" (they are `package api_test`, unimportable); "409 on a connected
worker, 404 on a missing one" (the ladder is four deep, and 403 fires before both); "for full
value a `relay-agent` too" (that measurement came from the Python lane; `GetTaskLogsPage` has
no fence, so direct row insertion suffices); and "pointing the existing fixtures at a live
server keeps the assertions and swaps the seam" (most of `logs_test.go`'s 42 tests assert
behaviour a real server cannot be made to produce, so the division is per-test, not per-file).

### Not closed by this slice

The lane sits **beside** the fast `httptest` tests rather than replacing them, per the item's
own division, and CLAUDE.md now carries the routing rule. 51 vacuous fixture bodies remain -
see [[idea-2026-08-27-cli-default-lane-fixtures-encode-through-their-own-decoder]]. Counting
them took three attempts (19, 29, 42, finally 51) because each used a text search for a
structural property; the warning is recorded with the count.
