---
title: The CLI integration lane's shared-service mode has no reaper, so any abnormal exit leaks a relaytest_ database carrying an admin token
type: bug
status: open
created: 2026-08-27
priority: low
source: 2026-08-27 CLI real-server integration lane slice - Phase 4 invariants and security lenses
---

# The CLI lane's shared-service mode leaks `relaytest_` databases on abnormal exit

## Summary
`internal/cli/pgharness_integration_test.go`'s `newSharedServiceDSN` creates one
`relaytest_<hex>` database per test and drops it in a `t.Cleanup`. That cleanup is the **only**
thing anywhere that removes it. Any abnormal exit - Ctrl-C, the workflow's
`concurrency: cancel-in-progress`, a `timeout-minutes` kill, a `go test -timeout` panic - skips
`t.Cleanup` entirely and leaks the database.

Container mode has no equivalent gap: testcontainers' Ryuk reaper cleans up after an abnormal exit.
Shared-service mode has nothing.

## Repro / Symptoms
Not reproduced deliberately (it needs a kill mid-run). Established by reading the code and by the
asymmetry with container mode. Normal operation is clean: across every verification run in the
closing slice - both modes, a 5x flake check, and three full mutation batteries -
`SELECT count(*) FROM pg_database WHERE datname LIKE 'relaytest_%'` was **0** every time.

## Context
Two things make this worth an item rather than a shrug:

- **`RELAY_TEST_DATABASE_URL` mode is advertised for local use** against the long-lived
  `relay-postgres` container `scripts/dev.ps1` manages, so leaks accumulate across sessions with
  nothing to sweep them and no documented command to do so. In CI the service container dies with
  the job, so CI is unaffected.
- **The leaked databases are not empty.** After `startRelayServer` seeds, each carries
  `admin@cli-lane.test` with `is_admin = true`, a bcrypt-`MinCost` (cost 4) hash of the hardcoded
  string `cli-lane-password`, and a token row with `ExpiresAt` NULL - never expires. Low severity
  because an attacker needs Postgres access already and would then have to point a `relay-server`
  at the orphan, but it is not nothing.

A narrower sibling was fixed in the same slice: the `t.Cleanup` used to be armed *after* the
`CREATE DATABASE`, so a successful create followed by a failing `Close` leaked one during ordinary
operation. It is now armed before the create, which closes the in-process window. This item is the
out-of-process one.

## Proposal
The obvious fix - sweep every `relaytest_%` database at harness startup - **is wrong as stated and
that is why this needs design rather than a one-liner**: two concurrent runs against one server
would drop each other's databases mid-test. Options worth weighing:

- Sweep only databases older than some threshold, which needs a creation timestamp
  (`pg_database` does not carry one; a comment or a naming-embedded timestamp would).
- Sweep only those with no active connections (`pg_stat_activity`), which is racy but bounded.
- Do not sweep; document a manual cleanup command in the Makefile comment and accept the leak.

The third is a legitimate outcome. What is not acceptable is the current silence.

## Acceptance / Done When
- Either an abnormal exit cannot leak, or the Makefile/CLAUDE.md documents the cleanup and says why
  no automatic reaper exists.
- Whatever is chosen does not break two concurrent runs against one Postgres.

## Related
- `internal/cli/pgharness_integration_test.go` (`newSharedServiceDSN`, `testDBPrefix`, `dbNamePattern`)
- `Makefile` (`test-cli-integration`), `.github/workflows/go-ci.yml` (`cli-integration`)
- [[idea-2026-08-23-cli-tests-never-hit-real-server]] - the closed item that built this harness
- [[bug-2026-08-26-integration-lane-times-out-on-docker-teardown]] - the container-mode teardown hazard
