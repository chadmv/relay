---
date: 2026-09-04
topic: agent-subprocess-to-task-logs-e2e
branch: claude/roadmap-now-dependencies-159be0
range: 11ca876..HEAD
---

# Session Retro: 2026-09-04 - Agent Subprocess to task_logs End to End

**TL;DR:** When a task runs on a worker machine, the text it prints has to travel across a network
connection and land in the database, and separately the server has to send the task down that same
connection in the first place. Both halves were tested, but nothing had ever tested them joined up -
so the seam between them was assumed to work rather than shown to work. This session built one test
that runs a real task program end to end and checks both directions at once. Along the way it found
that a claim the test file made about itself was wrong, and that an earlier note saying this kind of
test could never run automatically was also wrong - so the test now runs on every push.

## Handoff

Lane F of the six-item Now batch. Closed `idea-2026-08-25-no-e2e-path-from-agent-subprocess-to-task-logs`,
merged as PR #201 (`dca0035`), item moved to `docs/backlog/closed/`.
`cmd/relay-server/agent_subprocess_e2e_integration_test.go` drives a real `agent.Agent` against a
real `grpc.NewServer` on `127.0.0.1:0`, over a real `worker.Handler` and real Postgres, running the
test binary as its own task subprocess (sentinel in `DispatchTask.Env`, so no `//go:build !windows`
file and no `GOOS` switch). `cmd/relay-server` was added to `make test-pg-integration` and the
`pg-integration` CI job, taking its database from `internal/testsupport/pgdsn`. Measured cost: 13-18s
of a 72-95s lane, against `timeout-minutes: 12`.

**Feed to lane C**, which owns `idea-2026-08-23-integration-only-guards-ci-never-runs`: a review lens
enumerated four more packages whose integration helpers are Postgres-only and could join this lane
the same way - `internal/api`, `internal/mcp`, `internal/scheduler`, `internal/worker`
(`grep -rln testcontainers --include=*_test.go` on those four returns
`internal/api/testhelper_test.go`, `internal/mcp/mcp_integration_test.go`,
`internal/scheduler/dispatch_test.go`, `internal/worker/handler_test.go`). `internal/worker` is the
instance that item's own 2026-08-24 section calls "a fourth instance, the sharpest yet". That is
lane C's scope, deliberately not filed as a separate item.

## What Was Built

- **`cmd/relay-server/agent_subprocess_e2e_integration_test.go`** (new, ~470 lines) - the harness,
  plus a deadline-bounded `waitFor` with its own test via a `fatalT` fake.
- **`Makefile`, `.github/workflows/go-ci.yml`** - `cmd/relay-server` joins `test-pg-integration` and
  the `pg-integration` job.
- **`cmd/relay-server/{bootstrap,grpc_admission_e2e,startup_reconcile}_test.go`** - rewired from
  per-test testcontainers onto the shared `pgdsn` harness.

## Key Decisions

- **Route through the exported `agent.Agent`, not `agent.Runner`.** The item's proposal named
  `Runner`, but `newRunner` is unexported and `provider` is settable only from inside the package.
  `Agent.handleDispatch` builds a real `Runner` per `DispatchTask`, so no new seam was added and the
  RED survived against HEAD.
- **The subprocess is the test binary re-executing itself.** Portable across Windows and Linux with
  no platform-gated file, which matters because the batch's other lane had just documented that
  `go test` on Windows silently skips `//go:build !windows` files.
- **Both directions in ONE test.** A dispatch must reach the runner before any log can come back, so
  splitting them would have duplicated the whole fixture for no extra coverage.
- **Every wait is deadline-bounded with a named message**, written before any test that waits. A
  mutation that hangs is indistinguishable from infrastructure trouble, which would have made the
  eight-mutation battery unreadable.

## What Went Wrong and What Changes

Ledger on the prior retro (`2026-09-04-windows-crosscompile-ci`): "state the constraint, not a
census" **recurred** and is now promoted, see below. "Prove a platform-exclusion guard on the
platform that lacks the coverage" was **not exercised** here (nothing platform-gated shipped) but is
already promoted. "An injected value must not share a literal with its assertion" **recurred** as the
`pgdsn` root failure surfacing again in a second lane - already filed and promoted, no longer
carried. "Route a named claim to the reviewer" **applied**, and it is what produced this lane's
strongest evidence: the review brief named the "`go vet` runs nothing" claim, and the lens proved it
with an `os.Exit(3)` `init()` rather than asserting it.

- **The implementing agent ended its turn narrating a wait instead of returning a result.** Its final
  message was "I'll continue once the monitor notifies me that the run has finished." All nine
  commits were already on the branch and the battery was complete; only the report was missing. This
  is the third recorded instance of the same shape.
  -> **What changes:** no new rule - the existing one already says never to let a subagent's last
  step be the gate a merge decision depends on. What was missing was the recovery: with no
  SendMessage available in this build, the conductor reconstructed state from `git log`, the commit
  messages and the tree, and re-ran the gates itself. Reconstructing from commits worked because the
  agent had been briefed to record each battery result in a commit message rather than only in its
  report. **(CARRIED, already in [[feedback_verify_tree_not_subagent_claims]])**

- **"Leave the machine as you found it" reddened a concurrent gate.** The agent found `relay-postgres`
  stopped, started it for its own run, and stopped it again on finishing - correct by the letter of
  the off-limits rule. It happened mid-way through the conductor's `pg-integration` run, which failed
  with `failed to receive message: unexpected EOF`. Diagnosed by container exit code 0 with a clean
  "database system is shut down", which distinguishes a deliberate stop from a crash or an OOM.
  -> **What changes:** when lanes run concurrently, "restore shared infrastructure to the state you
  found it in" is itself a mutation of shared state, and it must be forbidden explicitly, not just
  implied. Brief concurrent agents to LEAVE started infrastructure running and say the conductor owns
  its lifecycle. Diagnose a mid-run infrastructure red by exit code and shutdown log before treating
  it as a code failure.

- **A review lens's supporting evidence was wrong while its conclusion was right, and the conductor
  relayed the evidence before checking it.** The lens said `TestAgent_dispatchAndReceiveLogs` is
  "untagged, default-lane"; it carries `//go:build integration` and `go test -run` on it reports
  `no tests to run`. The finding itself held - that test does cross the wire in both directions, so
  the file's "ONLY TEST IN THE REPO" claim was false either way - and the implementer caught the
  error during the fix round.
  -> **What changes:** a lens's verdict and the evidence it cites are two claims, and only the
  verdict has been through the lens's own reasoning. Before repeating a specific, checkable
  supporting fact (a build tag, a line count, which lane runs a test), run the one command that
  confirms it. This is cheap for exactly the facts most likely to be wrong.

- **A correction regenerated a fresh completeness claim, in the same commit that removed one.** The
  backlog edit correctly DELETED "or a real gRPC agent" from "what this does not close", then changed
  "stays open for those" to "stays open for **that**" - newly asserting p4d was the sole remaining
  category, when four more packages qualify and one of them is the instance the item itself calls its
  sharpest. Caught in review; now reads "The item stays open."
  -> **What changes:** already the rule. Worth recording that the defect appeared in the very edit
  whose purpose was to remove its sibling, which is the strongest argument yet for deletion over
  rewording: the rewording step is where the new claim gets authored.
  **(CARRIED, already in [[reference_correcting_a_uniqueness_claim]])**

- **A test file's own header made three false claims about other files, two of them falsified by this
  same branch.** "THE ONLY TEST IN THE REPO THAT CROSSES THE WIRE IN BOTH DIRECTIONS"; "unlike this
  package's three older helpers" (a later commit on this branch rewired all three); "unlike the other
  guards in this package, it does NOT need Docker" (false in both modes after the rewire).
  -> **What changes:** when a slice rewires the things its own comments describe, re-read every
  comment the slice wrote EARLIER in the same branch before opening the PR. A comment written at
  commit 2 describes a tree that commit 7 has changed, and nothing re-reads it.
  (promoted to [[reference_commands_that_look_redundant_cover_disjoint_sets]], whose rule - state the
  constraint, not a census - is the same one these three violate)

## Files Most Touched

- `cmd/relay-server/agent_subprocess_e2e_integration_test.go` (+470, new) - the harness, the bounded
  `waitFor` and its `fatalT` test, the helper subprocess.
- `docs/superpowers/plans/2026-09-04-agent-subprocess-to-task-logs-e2e-plan.md` (+1238, new).
- `Makefile` (+22/-11) - `cmd/relay-server` added to `test-pg-integration`, with the reason reduced
  to what is checked.
- `cmd/relay-server/{bootstrap,grpc_admission_e2e,startup_reconcile}_test.go` (+83/-24) - rewired onto
  `pgdsn`.
- `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` (+12/-4) - the "real gRPC
  agent" clause deleted, with the correction appended as its own section.
- `.github/workflows/go-ci.yml`, `CLAUDE.md` (+15/-9) - the lane's package list and its rationale.
