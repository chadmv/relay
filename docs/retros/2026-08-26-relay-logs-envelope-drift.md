---
date: 2026-08-26
topic: relay-logs-envelope-drift
branch: claude/relay-logs-envelope-drift-175d0f
range: 27b6566..a340ad7
---

# Session Retro: 2026-08-26 - relay logs Envelope Drift

**TL;DR:** The `relay logs` command printed nothing at all, for every job, for three and a half
months. The server changed the shape of its reply in May; the command kept reading the old shape,
the read failed every time, and the code was written to ignore that failure quietly. Tests stayed
green because four fake test servers were still hand-writing the old shape. This session fixed the
reading, made the command print long logs in full instead of stopping after the first 200 lines,
and made a failure say so on the error stream and exit non-zero. Four rounds of review then found
the same "prints nothing" symptom reachable by four more routes - a cancelled job, a server error
the server swallowed, a job id typed in capitals, and a job id that does not exist - so the fix
grew well past the original one-line decode.

## Handoff

`printTaskLogs` (`internal/cli/logs.go`) now decodes `taskLogPage{Items,NextSeq,Total}` and pages
`?since_seq=` (EXCLUSIVE, `WHERE id > $2`, so the cursor is the previous page's `next_seq`
VERBATIM, never `+1`) under **three** stops beyond the server's drained signal: an empty page that
does not report drained, `NextSeq <= since`, and `maxLogPages` (10000, a package var so a test can
shrink it). Each page is written as it arrives, so memory is O(one page) and a failure on page N
leaves 1..N-1 printed; the `Fprintf` return is checked, because an unchecked write reaches this
slice's own symptom through a full disk. `watchJobLogs` returns
`logCompleteness{incompleteTasks, unreconciled}` - a struct, not the count it replaced, because a
count can only describe logs that were ATTEMPTED AND FAILED and was silent about logs never
attempted. `watchOutcomeError` COMPOSES status and completeness rather than ranking them;
`silentError{}` survives only for a non-`done` job with complete output. `doSubmit` shares all of
it: `relay submit` without `--detach` is the second production caller and the spec missed it
entirely.

Four verify rounds each found "prints nothing" by a new door, twice inside code written to close
it. Cancel: `CancelJobTasks` flips tasks to `failed` in one statement and publishes only a `job`
frame, so no task frame ever fires - closed by `reconcileFinalSnapshot`, armed by a **defer** so an
early return added later cannot skip it, and skipped for exactly one of the three things the
subscribe-time snapshot can settle. `handleGetJob` (`internal/api/jobs.go`) discarded
`ListTasksByJob`'s error and answered 200 with `tasks` absent under `omitempty` - the original bug's
exact shape, in the backstop - now a 500, with `jobSnapshotUnusable` rejecting a task-less or
wrong-id body client-side regardless. `canonicalJobID` resolves argv through the same
`pgtype.UUID.Scan` the server's `parseUUID` uses, because `handleEvents` does not canonicalise
`?job_id=` and the broker filter is an exact string compare, so an uppercase or dashless UUID
subscribed to nothing forever. A 404 id is now classified through the shared
`relayclient.ErrorIsTransient` and ends the watch at once. `jobPath`/`jobEventsPath` escape argv -
path and query need different escapers and the difference is not cosmetic.

`internal/store/jobs_status_vocabulary_lockstep_test.go` is new: the existing tasks guard reads
`tasks_status_check` and nothing else, so `jobIsTerminal`'s registration in it had been prose that
could never fire. Writing the real guard surfaced `terminalStatuses` (`internal/mcp/wait.go`) as an
unregistered second slicing site of the same vocabulary. Both guards are `//go:build integration`
and CI runs neither (`go-ci.yml` is `make vet-integration` plus untagged `go test -race`) - a fifth
instance of `idea-2026-08-23-integration-only-guards-ci-never-runs`. `internal/mcp`'s wait loop
also gained a consecutive-not-cumulative failure bound (`maxConsecutiveWaitFailures = 5`).

Closes `bug-2026-08-25-relay-logs-prints-nothing-envelope-drift` (`docs/backlog/closed/`).
`internal/cli/logs_test.go` went from 8 tests to **42**, 1,996 lines, all routed through one
`writeTaskLogPage` simulator whose `logRow` type has hand-written tags deliberately independent of
the production `taskLogEntry`. Next session starts at ROADMAP Now - **which is stale**: it still
leads with this item at a `docs/backlog/` path that no longer resolves.

### Still open

- **The `Run:` closures of `LogsCommand` and `SubmitCommand` are unexercised.** No test constructs
  either `Command`, so the `os.Stdout, os.Stderr` argument pair added this slice can be transposed
  at `logs.go` or `jobs.go` with the whole package green. `admin_output_test.go`'s
  `captureStdStreams` is the precedent and its own doc comment states the reason. Item below.
- **`printTaskLogs` writes `Content` raw with `%s` and treats a chunk as a line.** Deliberately out
  of scope (spec "Rendering: out of scope, and why"), and it is a low-privilege log-forgery surface,
  not only a cosmetic one. Item below. Note the closed item's Resolution already says this "is filed
  separately" - that sentence becomes true when the conductor commits the item, and is false until
  then.
- **`internal/relayclient` bounds no response body and sets no `http.Client.Timeout`.** Item below.
- **Six byte-identical production copies of the UUID render format**, now including
  `canonicalJobID`. Drift in the CLI direction is caught by a fixture literal; drift in the SERVER
  direction is caught by nothing, because `internal/api.uuidStr` is unexported. Item below.
- **Unescaped model-supplied ids in `internal/mcp`.** Six `fmt.Sprintf` interpolations
  (`cancel.go`, `jobs.go`, `tasks.go` x2, `task_logs.go`, `wait.go`) while `resources.go` in the
  same package escapes and comments on why. Folded into the CLI escaping item below; `cancel.go` is
  destructive and the id comes from a model.
- **A residual hole in `onSubscribed`, disclosed in place.** A TRANSIENT snapshot read that fails
  twice falls through to the stream and waits, which is correct for a running job and wrong for one
  that finished before the subscription. Closing it needs the snapshot re-read while the stream is
  live, which this shape cannot express.
- **`-race` and the integration lane were not run in this pass.** See Verification.

## What Was Built

- **`internal/cli/logs.go`** - the whole fix. `taskLogPage`/`taskLogEntry`, the three-stop paging
  loop, `logProgress` with `ofTotal()`, `logCompleteness`, `watchOutcomeError`,
  `jobSnapshotUnusable`, `canonicalJobID`, `jobPath`/`jobEventsPath`, `reconcileFinalSnapshot`, and
  the defer that arms it. Most of the diff is comment, and the comments carry the arguments that no
  test can: why the cursor is verbatim, why the reconcile is skipped for exactly one of three
  outcomes, why the cap message must not blame the server on an exact-multiple log.
- **`internal/cli/jobs.go`** - `doSubmit`/`SubmitCommand` take `errOut` and share
  `watchOutcomeError`. "The two commands wording the same outcome differently is its own defect."
- **`internal/api/jobs.go`** - `handleGetJob` checks `ListTasksByJob`. Plus
  `jobs_task_list_read_integration_test.go`.
- **`internal/store/jobs_status_vocabulary_lockstep_test.go`** - the second lockstep guard, over
  `jobs_status_check`. Its comment enumerates six slicing sites and marks which fail OPEN
  (`jobIsTerminal`, `terminalStatuses`, `handleCancelJob`'s deny-list) versus closed.
- **`internal/mcp/wait.go`** - a transient poll failure no longer destroys `relay_wait_for_job`;
  the bound is consecutive, and the tolerated-failure sleep honours the same deadline and
  cancellation as the poll.
- **`internal/cli/logs_test.go`** - 42 tests. `writeTaskLogPage` + `logRow` + `genRows` (contiguous
  seqs, load-bearing and commented as such), `fakeNeverDrainingServer` (deliberately misbehaving, so
  NOT routed through the correct simulator), and one test per door the verify rounds opened.
- **README** - `relay logs` and `relay submit`, rewritten: not a live stream, per-finished-task
  bursts, paging, the trailing-window caveat, the reconcile, non-canonical ids, the stderr
  diagnostic and exit 1.

### Verification

- **This retro pass had no shell.** Bash is disabled in this session: no `git log`, no `git diff`,
  no test run was executed here. The commit list came from the worktree reflog
  (`.git/worktrees/.../logs/HEAD`) and every claim above that could be checked by reading was
  checked against the tree.
- **Confirmed by reading, not inferred:** the three loop stops and their order; the write check;
  `doSubmit` sharing `watchOutcomeError`; `handleGetJob`'s checked `ListTasksByJob`; both lockstep
  guards' `//go:build integration` tag; `go-ci.yml` running only `vet-integration` and untagged
  `-race`; six production copies of the UUID format; `looksLikeUUID` gating the worker-id
  passthrough; the six unescaped `internal/mcp` interpolations; that no test constructs
  `LogsCommand()` or `SubmitCommand()`.
- **Reported by the implementing and verifying lanes, not re-run here:** all test results, the
  mutation kills, and the diff stat. `-race` and the integration lane were not run in this pass and
  I make no claim about them.
- **Refuted from my own inputs:** see the last two entries of "What Went Wrong and What Changes".

## Key Decisions

- **Break on `next_seq`, never on `len(items) < limit`.** The two agree today; the second
  re-derives a rule the server already applied and desynchronizes the moment the drain rule moves.
- **Canonicalise ARGV, not the snapshot's id.** Adopting the server's spelling fixes the comparison
  and cannot reach `jobEventsPath`, because the subscription is established before any snapshot is
  read. This overrules the conductor's prescribed remedy, and the measurement is the reason.
- **Compose the outcome, do not rank it.** Status and completeness are different facts; a message
  about logs alone invites the reader to conclude the job was fine.
- **Arm the reconcile with a defer, in the same breath as the call that creates the obligation.**
  Deleting a bottom-of-function call is caught by a test; a `return` added above it is not.
- **The fixture's `logRow` is not the CLI's `taskLogEntry`.** A fixture built out of the type under
  test cannot detect drift in that type, which is the exact failure this slice exists to fix.
- **No rendering change.** Byte-identical stdout format is what makes the diff reviewable as a bug
  fix, and the interior-CR question is too small to decide alone - a row is a chunk, not a line.
- **`maxLogPages` is a hang bound, not a product limit**, and its message must not blame the server
  on a log of exactly `maxLogPages * 200` rows, where the envelope's own `total` settles it.

### CLAUDE.md verdict

**No amendment is earned.** The strongest candidate is "a guard's registration is only real if the
guard's instrument reads the thing the site slices" (the `jobIsTerminal` prose registration). It is
correct and it is checkable, and it belongs in durable memory rather than Invariants: Invariants is
for rules new *code* must not bypass, and this is a rule for whoever writes a guard. The second
candidate - the paging loop's three stops - is a worked example of the already-stated "a
server-written value is not out of the caller's reach", and the worked example belongs in the code,
where it is.

One thing to flag and NOT recommend: adding `AppendTaskLog`-style prose about the CLI's status
predicates. `taskIsTerminal`/`jobIsTerminal` are now registered in two live lockstep guards with
their fail directions written out. That is stronger than a CLAUDE.md sentence and it is in the file
the next author opens.

## What Went Wrong and What Changes

**Ledger.** From `2026-08-25-windows-crlf-log-lines`. *A green re-run bounds a red run's frequency*
- not exercised, still unpromoted, carried. *A test whose name asserts more than its body* - not
exercised, still unpromoted, carried. *Read every artifact once for internal contradiction* -
**recurred, three times**, and in a register the rule does not name; entry below. *A spec-designed
test can be structurally incapable of the kill it is paired with* - **recurred**, caught by the
plan this time; entry below. Promoted lessons used and vindicated:
[[reference_verify_the_mutation_applied]] (recurred, twice, on `perl -0` and a `python` multi-line
replace, exactly the CRLF trigger it now carries), [[reference_mutation_battery_needs_green_baseline]]
(a deletion-shaped mutation was a build error and was reported inconclusive, not banked),
[[reference_accurate_item_wrong_remedy]] (the conductor's canonicalisation remedy was accurate about
the diagnosis and wrong about the fix), [[reference_added_a_property_forgot_its_guard]] (the defer's
`err != nil` guard replaced every transport error with generic text, 21 packages green),
[[reference_same_typed_args_transpose_silently]] (found by mutation at `doLogs`/`doSubmit`, and
still open one layer up - see the item below), [[feedback_backlog_proposal_not_contract]] (the item
undercounted the fixtures 1:4 and its CRLF scope note was stale by one commit).

- **(CARRIED) An artifact contradicted itself three times, and every instance was a code comment or
  a printed string rather than a handed-down document.** The cap message told the operator "the log
  may be longer than N rows" while the caller had just prepended "(400 of 400 rows)". A reconcile
  comment leaned on an absolute the same file disproves. A diagnostic said "after it finished" beside
  an error saying the job may still be running. All three were caught by later rounds, not by the
  round that wrote them.
  -> **What changes:** widen the trigger. The internal-contradiction pass is not a document review
  step - run it over the STRINGS AND COMMENTS a commit adds, against the code in the same hunk. The
  rule as written fires when you are reading a spec; these three were written while closing a
  finding, which the 2026-08-25 retro already identified as the least-verified moment in a claim's
  life.

- **(CARRIED, variant) The spec's test helper was built out of the type under test.**
  `writeTaskLogPage(w, r, rows []taskLogEntry)` would have marshalled the CLI's own json tags, so a
  wrong tag makes both sides agree and the suite stays green against a CLI that cannot talk to the
  server - the defect the slice exists to fix, reproduced in its own fixture. Caught by the plan,
  which declared it "the single most important correction in this plan".
  -> **What changes:** a fixture that simulates a producer must not import the consumer's types.
  Declare the wire shape locally with hand-written tags, and say in the comment that de-duplicating
  the two structs re-opens the bug. Offered for promotion as an extension of
  [[reference_guard_never_sees_real_producer]], whose current trigger is an import direction rather
  than a shared type.

- **A registration in a guard is not a guard if the guard's instrument reads something else.**
  `jobIsTerminal` was listed in `TestTasksStatusVocabularyIsExactly`, which reads
  `tasks_status_check` and only that. `jobIsTerminal` slices `jobs.status`. So from the day it was
  registered until the day the second guard was written, a seventh terminal JOB status would have
  shipped with zero signal and `relay logs` would hang on every finished job. Writing the real guard
  immediately surfaced `terminalStatuses` in `internal/mcp/wait.go` as an unregistered second site
  slicing the same vocabulary.
  -> **What changes:** when adding a site to a lockstep guard, read the guard's own query and check
  it reads the constraint the new site slices - a registration is a claim about the INSTRUMENT, not
  about the list. And when a guard's subject turns out to be a second vocabulary, grep for other
  sites slicing it before writing the expected set, because the first guard's list is evidence only
  about the first vocabulary. Promotion candidate: extend
  [[reference_match_the_instrument_to_the_claim]].

- **Three fix rounds each introduced a regression in their own newest code; the fourth did not.** A
  task-less 200 was read as "nothing owed" by the reconcile written to close the omission. A
  non-canonical id hung on the SSE filter after the reconcile made the snapshot authoritative. A 404
  was retried by the retry added for transient failures. Each was found by the NEXT round, on the
  previous round's diff.
  -> **What changes:** after a fix round, the verify lens's primary subject is the fix's own diff,
  not the original defect. State it as the round's opening question: "what does this round's new code
  do with the input that produced the original symptom?" Three for three here, and the fourth round
  is the only evidence that the sequence terminates. Promotion candidate: `docs/agent-team/README.md`
  phase briefs.

- **A test's substring assertion was satisfiable by its own fixture's identifiers.**
  `require.NotContains(errOut, "finished")` was being decided by a job id that contained the word,
  not by the message under test. The shipped fixture id is `job-nostatus`, which is consistent with
  the collision having been found and renamed away.
  -> **What changes:** a `Contains`/`NotContains` assertion needs fixture identifiers disjoint from
  every asserted substring. Pick the ids adversarially - the assertion is on a buffer that the
  fixture also writes into. Promotion candidate: durable memory, as a sibling of
  [[reference_test_green_because_of_the_bug]].

- **An agent lost an uncommitted implementation to `git checkout --`.**
  -> **What changes:** never `git checkout -- <path>` in a lane that has uncommitted work in that
  path; `git stash` or commit first, and read the reflog before any destructive restore. Offered as
  an extension of [[feedback_concurrent_agents_share_one_git_index]], which today covers `git add` +
  bare commit and reset, not restore.

- **Mutation testing found four things reading did not, and one of them was a whole uncovered
  path.** `relay submit`'s non-detach path had zero coverage. `timed_out` appeared exactly once in
  `internal/cli` - inside the predicate itself - so the predicate had no subject. The stdout/stderr
  split transposed cleanly at both `doLogs` and `doSubmit`. The defer's `err != nil` guard replaced
  every transport error with generic text and left 21 packages green.
  -> No process change - the recorded mutation lessons are what produced these, and they worked.
  Recorded here as the evidence that they are working, and as the argument for the harness item
  already open.

## Recommended Backlog Items

Intake, not a priority order - `ROADMAP.md` orders the work and the Handoff names the next entry
point. Six items are drafted and written to `docs/backlog/` uncommitted; two existing items get
appends rather than new files.

- [`bug-2026-08-26-relay-logs-treats-a-chunk-as-a-line-and-forges-lines`](../backlog/bug-2026-08-26-relay-logs-treats-a-chunk-as-a-line-and-forges-lines.md) - a `task_logs` row is a chunk, so the prefix lands on the first line only and a bare `\n` inside one row forges a byte-identical `[audit stdout]` line. Submit rights are the whole exploit.
- [`bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped`](../backlog/bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped.md) - four raw-argv CLI sites plus `evict-workspace`'s `shortID`, and six model-supplied ids in `internal/mcp` including a destructive `DELETE`.
- [`bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout`](../backlog/bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout.md) - `http.Client{}` with no `Timeout` and an unbounded fully-buffered decode; fixing it there bounds every CLI and MCP call at once.
- [`bug-2026-08-26-mcp-since-seq-is-advertised-inclusive-and-is-exclusive`](../backlog/bug-2026-08-26-mcp-since-seq-is-advertised-inclusive-and-is-exclusive.md) - the jsonschema says `seq >=`, the SQL is `id > $2`, so a model paging with `since_seq = last_seq` re-fetches the boundary row forever.
- [`bug-2026-08-26-cli-command-constructor-wiring-is-unpinned`](../backlog/bug-2026-08-26-cli-command-constructor-wiring-is-unpinned.md) - **half closed in this slice.** Nothing constructed `LogsCommand()` or `SubmitCommand()`, so their `os.Stdout, os.Stderr` pair transposed with all 21 packages green; `internal/cli/command_writer_wiring_test.go` closes both, and each transposition is killed by that test and by nothing else in the tree. The item now carries the residue: twelve of seventeen `XCommand()` constructors are built by no test, two of them with a transposable same-typed pair (`AgentCommand`'s `os.Stdout, os.Stderr`, and `MCPCommand`'s `os.Stdin, os.Stdout` - both `*os.File`). `MCPCommand` is the one to read twice: `mcp_test.go` does construct it, so a name-keyed coverage audit scores it covered, but the test errors on config before `srv.Run` is ever reached. Coverage by constructor name is the wrong instrument; the question is whether a test reaches the wiring line.
- [`idea-2026-08-26-six-copies-of-the-uuid-render-format`](../backlog/idea-2026-08-26-six-copies-of-the-uuid-render-format.md) - byte-identical in six production files; drift in the server direction is caught by nothing because `uuidStr` is unexported.

**Appends, not new items** (the conductor applies these; both are judgements, not filings):

- `idea-2026-08-23-cli-tests-never-hit-real-server` - this slice is its second confirmed instance
  and the first one FIXED. `writeTaskLogPage` is the seam a real-server lane would replace, and
  `internal/cli/logs_test.go` went 8 tests -> 42, 1,996 lines, so materially more fixture logic now
  goes unexercised against a real server. **Priority should NOT move again.** It went `low` ->
  `medium` on the strength of two live instances; both are now fixed or filed, the residual risk is
  prospective, and the closing cost is unchanged (testcontainers into a package that has none).
  Raising it to `high` on evidence that the point fixes worked would be rewarding the wrong signal.
- `idea-2026-08-23-integration-only-guards-ci-never-runs` - a fifth instance, and the cleanest yet.
  Both status-vocabulary lockstep guards are `//go:build integration` because they read
  `pg_get_constraintdef` from a live database, so the notifier is absent from `make test` AND from
  CI. That is structural rather than a choice, which is exactly why it belongs on this item and not
  on a new one - and it supplies a remedy the item's menu lacks: parse the migration `.sql` instead
  of the live catalog, which puts the guard in the default lane at the cost of testing the file
  rather than the database.

**Considered and not filed:** an item for the internal-contradiction pass (no acceptance criterion
a slice could close - it is a durable-home lesson, same call as 2026-08-25 and 2026-08-26); an item
for the residual transient-snapshot hole in `onSubscribed` (disclosed in the code where a future
author lands, and closing it is a redesign of the watch loop, not a task).

## Files Most Touched

- `internal/cli/logs.go` - the entire fix. Read `canonicalJobID`'s comment for why argv rather than
  the snapshot, the defer above `StreamEvents` for why the reconcile is armed rather than called,
  and the `pages >= maxLogPages` branch for why the message must not blame the server.
- `internal/cli/logs_test.go` - 1,996 lines, 42 tests. `writeTaskLogPage` and `logRow` are the
  point: the simulator's tags are hand-written and independent of production on purpose.
- `internal/cli/jobs.go` - `doSubmit`, the caller the spec missed, now sharing
  `watchOutcomeError`.
- `internal/api/jobs.go` - `handleGetJob`'s checked `ListTasksByJob` and the comment naming what a
  task-less 200 did to the reconcile built on top of it.
- `internal/store/jobs_status_vocabulary_lockstep_test.go` - new. The comment is the artifact: six
  sites, three of them fail-open, and the reason the first guard could not cover any of them.
- `internal/mcp/wait.go` - the consecutive failure bound, and `terminalStatuses`, now a registered
  slicing site.
- `README.md` - `relay logs` and `relay submit`. The old text claimed live SSE streaming, which the
  command has never done.
- `docs/superpowers/plans/2026-08-26-relay-logs-envelope-drift.md` - "What this plan refutes or
  changes in the spec". Six claims, and #3 (the fixture built from the type under test) is the one
  to read.
