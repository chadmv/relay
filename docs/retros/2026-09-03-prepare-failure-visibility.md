---
date: 2026-09-03
topic: prepare-failure-visibility
branch: claude/top-3-roadmap-items-65856f
range: ed3cdc0dcf42a9770e1250e2c8bb67232ad7a65a..5514f5291d67ab29149a8d07ce54df2136037bc8
---

# Session Retro: 2026-09-03 - Prepare-Failure Visibility

**TL;DR:** When a render task failed while checking out its files from Perforce, the task showed
up as failed with a completely empty log - the reason existed only on the worker machine's own
console, if anyone happened to be watching. This session made the reason visible in three places:
the server now saves it as the last line of the task's log, the agent writes what it is doing to
the worker's own log as it goes, and a full disk now says "you are out of disk space" instead of
echoing Perforce's raw error. The work took four rounds of review to finish, and the two most
serious problems found were both introduced by earlier rounds of fixing rather than by the
original code - including one where the fix for a security issue quietly re-opened it through a
different path.

## Handoff

Three ROADMAP Now items shipped as one batch on `claude/top-3-roadmap-items-65856f` (39 commits,
26 files, +4210/-113, nothing merged to `main` - the merge gate is still open):
`bug-2026-09-03-prepare-failure-error-message-is-discarded`,
`feature-2026-09-03-classify-out-of-disk-p4-errors`,
`feature-2026-09-03-agent-task-lifecycle-logging`. None are closed yet; closing is the
`/backlog close` step after merge.

Spec `docs/superpowers/specs/2026-09-03-prepare-failure-visibility.md`, plan
`docs/superpowers/plans/2026-09-03-prepare-failure-visibility.md`, both committed before code
(`2c44e28`, `d742f47`). Two lanes in separate worktrees (`pfv-lane-a` = `internal/worker` +
README, `pfv-lane-b` = `internal/agent`), four review rounds, merged back at each round.

Shipped: `handleTaskStatus` writes `upd.ErrorMessage` through `AppendTaskLog` with the connection's
`workerID` and `int32(upd.Epoch)`, above the retry branch (which bumps the epoch and returns), on
stream `stderr` (migration 000019's CHECK admits only `stdout`/`stderr` - `prepare` is unwritable),
content `"[" + statusStr + "] " + sanitizeAgentErrorMessage(msg) + "\n"`, published before the
status frame. `MaxAgentErrorMessageBytes = 4096`; the sanitiser strips NUL, coerces valid UTF-8,
cuts at a rune boundary. `trailingLogCutoff` and `publishTaskLog` extracted from `handleTaskLog`.
New budgeted log kind `kindStatusLogPersist` (ninth), which is a published JSON key and so reached
`internal/api/server_counters.go` and `cmd/relay-server/counters_wiring_test.go`.
`p4CommandError` in `client.go` keeps args/underlying/stderr separate; `classifiableText` returns
`""` when no `*p4CommandError` is in the chain, so caller-supplied depot paths never reach the
classifier. Five host-log lines in `Runner.Run`, `argv[0]` only, `%q` + `clipArg`
(`maxLoggedArgBytes = 4096`); `[sync]` brackets through `progress` with `handle.Release()` moved
above the failure line.

Gates green on the merge: 22 packages default, `internal/worker` 166.8s, `internal/api` 619.6s,
`cmd/relay-server` 25.4s, `internal/agent` incl. real p4d 36.8s, `-race` in the `golang:1.26`
container (21 packages, zero races), `vet` clean under both tag sets.

Two residuals shipped deliberately and are filed below: `classifyP4Error` still misclassifies when
p4 echoes the offending path into its own stderr (args are excluded, stderr is not), and
`errorMessageLogStream = "stderr"` is guarded only under the integration tag. One existing item was
annotated rather than duplicated:
`bug-2026-08-24-wire-keyed-dedupe-lets-a-peer-suppress-its-own-diagnostics` - the kind split stops
the log path consuming the status path's dedupe ENTRY, but both kinds still draw on one 16-token
BUCKET, so that item's drain still suppresses the new line.

Next session starts at the merge gate: open the PR (recommended - the four rounds' reproductions are
the batch's most valuable record), then `/backlog close` the three items, then ROADMAP refresh.

## What Was Built

- **The coordinator stores the cause.** `handleTaskStatus` had never read `TaskStatusUpdate.ErrorMessage`
  - nothing under `internal/worker/` referenced the field - so a p4 sync failure produced a `failed`
  task with an empty log. It now writes one fenced `task_logs` row and publishes it before the status
  frame, because the CLI's follower and the SPA's tail both stop on the terminal frame.
- **The agent says what it is doing.** Five host-log lines across `Runner.Run`: prepare start, prepare
  failure with cause, the no-provider refusal, each step's start, each step's exit. Program name only.
  The provider's sync lifecycle goes through the `progress` callback into `task_logs` instead, so it is
  visible through the API rather than only on the host.
- **A full disk says so.** A fifth `classifyP4Error` case matching the phrase, with `workspace`-contains-
  `space` negatives as the reason the test exists, and classification narrowed to what a p4 invocation
  actually produced.

## Key Decisions

- **`stderr`, not a new `prepare` stream.** The backlog item prescribed the stream `handleTaskLog`
  stores for `LOG_STREAM_PREPARE` chunks; that resolves to `stdout`, and migration 000019's
  `task_logs_stream_check` admits only `stdout`/`stderr`, so `prepare` is unwritable rather than
  merely unused. Adding it would be a migration plus three consumers. `stderr` renders in the SPA's
  error colour and nothing anywhere filters by stream.
- **The fence-rejection arm counts nothing.** The item prescribed joining `taskLogFenceRejects`, whose
  published meaning is "chunks the fence refused with no Go-side pre-filter". The new site sits behind
  both gates, so folding it in would falsify a documented contract. Nothing new is counted, because
  `AppendTaskLog`'s fence is strictly weaker than the status write that follows - any rejection there
  is already counted in `task_status_fence`.
- **Terminal reports only, and no writability pre-filter.** A non-terminal report leaves status,
  `worker_id` and epoch untouched, so admitting `RUNNING` would be an unbudgeted insert at one message
  per row. A T0-writability predicate was declined deliberately: it would give a counter-labelling
  helper a control job and invert its failure mode, silently dropping a real line when `preparing`
  lands.
- **The residual `classifyP4Error` vector was accepted, not chased.** Excluding the args closes every
  route where the phrase arrives via argv; p4 echoing the path into its own stderr remains, and
  suppressing that by cross-checking args is leaky in principle. The destructive half was closed
  instead: the message no longer tells an operator to raise `RELAY_WORKSPACE_MIN_FREE_GB`, which evicts
  other tenants' warm workspaces and, where it is unset, turns the sweeper on for the first time.

## What Went Wrong and What Changes

Ledger: every entry in the prior retro was promoted, so none are carried. Promoted lessons that
fired this session: [[reference_backstop_recreates_the_defect]] (twice, and it is this session's
headline - see the first entry below); [[feedback_verify_tree_not_subagent_claims]] (every lane
report and every review claim re-checked against the tree; three subagent claims were wrong);
[[reference_verify_the_mutation_applied]] (fired for real, on the conductor - see below);
[[reference_correcting_a_uniqueness_claim]] (rounds 3 and 4 each deleted stale counts and one round
wrote a fresh one); [[feedback_assert_encoding_after_a_programmatic_edit]] (every doc edit checked
for line delta, eol and non-ASCII inventory); [[reference_uniqueness_claim_is_about_the_complement]]
(the ninth log kind falsified six counts across four files, found by searching the shape).
[[feedback_same_finding_across_parallel_lanes]] was not exercised - findings split cleanly by lane.
The `docs/agent-team/README.md` shared-state rule promoted last session **recurred** and is the
second entry below.

- **A security fix's own fallback branch re-opened the hole it closed, and the round that wrote it
  documented a different residual.** Lane B excluded caller-supplied argv from `classifyP4Error` by
  keying on a new `p4CommandError` type - but `classifiableText` fell through to `err.Error()` on the
  outermost error whenever that type was absent, and `ResolveHead` has two returns that are not that
  type, wrapped by `Prepare` with the job's own depot path. Reproduced twice: a spec naming
  `//depot/disk full/...` still reported a full volume, by a route needing no stderr echo at all -
  i.e. a cleaner exploit than the residual the engineer had written down as "what remains".
  -> **What changes:** when a fix works by recognising a type, a tag or a shape, the review question is
  what happens on the branch where recognition FAILS, and the fix brief says so explicitly. A
  documented residual is a claim about the complement and gets the same treatment as any other: the
  author enumerates the routes and measures each, rather than describing the one they found.
  (promoted: extends [[reference_backstop_recreates_the_defect]])

- **The shared-state rule reached engineer briefs and not review-lens briefs, and a reviewer dropped a
  table in the user's dev database.** Last session promoted "anything outside the worktree is
  off-limits" into `docs/agent-team/README.md`, phrased as a Phase 3 note about engineers. Phase 4's
  lenses have the same Bash access; the correctness lens ran `DROP TABLE IF EXISTS task_logs` against
  the live `relay-postgres` dev database instead of a throwaway. It disclosed this itself and restored
  the schema from migrations 000001 + 000018 + 000019 (verified independently: columns, both indexes,
  the CHECK, `schema_migrations` at 22 clean); the rows are gone.
  -> **What changes:** the shared-state paragraph goes in EVERY subagent brief that carries tool
  access, not only Phase 3's - a read-only review lens is read-only with respect to the repo, never
  with respect to the machine. State it as a property of the tools the agent holds, not of the phase
  it is in. (promoted: extends the Phase 3 note in `docs/agent-team/README.md`)

- **The conductor's own mutation reported "survived" without ever being applied.** Verifying a
  reviewer's coverage claim, a `cp` to `$TMPDIR` failed (the variable was empty), `&&` short-circuited
  the Python that would have applied the mutation, and `go test` then ran against clean code and
  printed `ok` - which reads exactly like a survivor. Caught only because the restore script asserted
  the mutant text was present and found `mutant count 0`.
  -> **What changes:** a mutation harness asserts the edit landed by re-reading the file and comparing
  a hash, never by an exit code, and the restore step's assertion runs even when the mutation step
  "succeeded" - the restore is where an unapplied mutation is detectable. This applies to the conductor
  spot-checking a claim, not only to a dispatched battery. (already in
  [[reference_verify_the_mutation_applied]] - stamping so it stops being rediscovered)

- **A guard whose threshold was expressed in terms of the constant it guards could not see the
  constant move.** Round 3 raised `maxLoggedArgBytes` 1024 -> 4096 and left both clip guards at a
  literal `10_000`, so the bound could be doubled again invisibly. The conductor prescribed
  `assert.Less(len(out), 2*maxLoggedArgBytes+512)`, and the engineer measured that this is strictly
  worse - the threshold then scales with the very constant a raise changes, and 5120 and 8192 both
  pass. The same shape appeared independently in Lane A: `TestIngestLogLimiter_EveryKindIsItsOwnDedupeKey`
  asserts `len(l.seen) == len(kinds)` against its own hand-written map, so omitting the ninth kind
  narrowed the census silently rather than reddening.
  -> **What changes:** a guard must not derive its expected value from the thing it guards. When
  writing an assertion, ask what would have to change for it to go red, and if the answer moves the
  threshold too, pin the value separately - two assertions, one for the behaviour and one for the
  constant or the population. (promoted to [[reference_a_guard_must_not_derive_its_expectation_from_its_subject]])

- **Two fixes the conductor prescribed were wrong, and both were caught by an engineer measuring
  rather than implementing.** The `classifyP4Error` fix was briefed as closing all five cases; p4
  echoes paths into its own stderr, so it closes them only where it does not. The clip-guard fix was
  self-defeating as above. Both briefs stated the remedy as an instruction.
  -> **What changes:** a remedy written into a fix brief is a hypothesis, labelled as one, with the
  measurement that would confirm it named alongside - and the brief says to report back rather than
  implement if the measurement disagrees. Both engineers did this unprompted; the brief should not
  have relied on that. (promoted: extends [[reference_accurate_item_wrong_remedy]])

- **Two agents in one session collided on a scratchpad filename, and one collided with the
  conductor on a source file.** Both lane engineers wrote `mut.py` into the shared session scratchpad;
  Lane A's version overwrote Lane B's, which ran it three times against the wrong package before
  noticing. Separately, the conductor mutated `internal/worker/handler.go` while an integration agent
  was doing the same - because the conductor read that agent's "I'll wait for notifications" message
  as a stall and took over, when it had in fact completed and reported in full.
  -> **What changes:** every dispatched agent gets a lane-private scratchpad prefix in its brief, and
  before taking over work from an agent that appears stalled, check for its completion report first -
  an agent that says it is waiting may already have finished, and duplicating its work puts two
  writers on one file. (NOT promoted - carried to the next retro: the scratchpad-collision axis belongs on
  [[feedback_mutation_testing_needs_isolated_tree]] and the do-not-take-over-a-running-agent rule
  wants a `feedback` memory of its own; the playbook now carries the scratchpad half only)

- **Adding one enum value falsified six counts in four files, and the round that added it corrected
  two of them.** The ninth log kind made "eight kinds" wrong in `ingest_log_limiter.go` (twice),
  `ingest_log_limiter_test.go`, `taskstatus_fence_counters_test.go` (twice, one inside a `require`
  failure message), `ingest_log_counters.go` ("these sixteen numbers"), README, and a ROADMAP line
  citing an open backlog item. Round 3 fixed README and the code census; round 4 found the rest.
  -> **What changes:** when a change adds a member to an enumerated set, grep the tree for the old
  cardinal and its spelled-out form before committing, and delete every count found rather than
  incrementing it - a corrected number regenerates the defect at the next member. (already covered by
  [[reference_uniqueness_claim_is_about_the_complement]] and
  [[reference_correcting_a_uniqueness_claim]]; noting the enum-cardinal trigger here rather than
  offering a third memory)

- **A four-round review sequence terminated, and only round 4 established that.** Rounds 2 and 3 each
  found a real defect in the previous round's newest code; round 4 found no correctness or security
  defect, only stale counts and two blunted guards. Without round 4 the batch would have merged on the
  assumption that round 3 was clean, which is exactly the assumption round 3 falsified about round 2.
  -> No process change - the playbook already prescribes this and it worked. Recorded because the
  fourth round is the cheapest round and the easiest to skip when the third comes back looking clean.

## Recommended Backlog Items

Backlog intake, not a priority order - the Handoff names the next entry point.

- See [`bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr`](../backlog/bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr.md) - classifyP4Error still misclassifies when p4 echoes the offending path into its own stderr
- See [`bug-2026-09-03-sendstepmarker-writes-full-argv-to-task-logs`](../backlog/bug-2026-09-03-sendstepmarker-writes-full-argv-to-task-logs.md) - sendStepMarker writes the full argv into task_logs, readable by every authenticated user
- See [`bug-2026-09-03-provider-progress-parks-while-holding-the-workspace`](../backlog/bug-2026-09-03-provider-progress-parks-while-holding-the-workspace.md) - the provider's progress callback parks on an agent-lifetime send while holding a workspace handle
- See [`feature-2026-09-03-sendfinalstatus-carries-no-error-message`](../backlog/feature-2026-09-03-sendfinalstatus-carries-no-error-message.md) - a cmd.Start failure lands as a failed task with an empty log
- See [`idea-2026-09-03-replace-the-readme-log-site-count-with-a-guard`](../backlog/idea-2026-09-03-replace-the-readme-log-site-count-with-a-guard.md) - replace README's hand-maintained log-site and key counts with a test-backed census
- See [`idea-2026-09-03-unshelve-spec-always-resyncs-a-baseline-workspace`](../backlog/idea-2026-09-03-unshelve-spec-always-resyncs-a-baseline-workspace.md) - an unshelve-bearing spec always re-syncs, so the baseline-match fast path is unreachable
- Appended to [`idea-2026-08-23-integration-only-guards-ci-never-runs`](../backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md) - a tenth instance with a new sub-shape: `errorMessageLogStream` survives the untagged lane because the default-lane stub discards the `Stream` argument, so the remedy is to widen the stub rather than to move the test.
- Annotated [`bug-2026-08-24-wire-keyed-dedupe-lets-a-peer-suppress-its-own-diagnostics`](../backlog/bug-2026-08-24-wire-keyed-dedupe-lets-a-peer-suppress-its-own-diagnostics.md) during the session - the kind split stops the log path consuming the status path's dedupe entry, but both still draw on one bucket, so that item's drain still suppresses the new line.

## Files Most Touched

- `internal/worker/handler.go` (+228) - the fenced `AppendTaskLog` write in `handleTaskStatus`, the
  sanitiser and its bound, `trailingLogCutoff` and `publishTaskLog` extracted from `handleTaskLog`.
- `internal/worker/handler_taskstatus_errormessage_integration_test.go` (+432, new) - the eleven
  integration guards: fence, identity, currency, retry survival, ordering, bound, sanitisation.
- `internal/worker/taskstatus_errormessage_test.go` (+283, new) - the untagged half, so the sanitiser
  and the dedupe-key split run in CI's race lane rather than only under Docker.
- `internal/agent/runner_lifecycle_log_test.go` (+249, new) - the `argv[0]` narrowing, the injection
  guard, the `cmd.Start` line, and the clip guards split from the bound guard.
- `internal/agent/source/perforce/perforce_progress_test.go` (+137, new) - the `[sync]` brackets, the
  no-cause-repeated guard, and the release-before-report ordering observed from inside the callback.
- `internal/agent/source/perforce/diagnostics_test.go` (+112) - the disk-full table, the
  `workspace`-contains-`space` negatives, the passthrough arm strengthened from `errors.Is` to
  identity, and every fixture rebuilt through the production constructor.
- `internal/agent/source/perforce/client_error_test.go` (+103, new) - `p4CommandError`'s rendering and
  unwrap contract.
- `internal/worker/ingest_log_limiter.go` (+72/-...) - the ninth kind, the deleted census, and the
  comment reduced to what its named guard actually buys.
- `internal/agent/runner.go` (+55) - the five lifecycle lines, `clipArg`, and the bound.
- `internal/agent/source/perforce/client.go` (+40) - `p4CommandError` and the four wrapped return paths.
- `internal/agent/source/perforce/diagnostics.go` (+30) - `classifiableText` and the disk-full case.
