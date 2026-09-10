---
date: 2026-09-10
topic: startup-validation-deadline
branch: claude/roadmap-now-dependencies-581b21
range: ef86c877..fb99ca02
---

# Session Retro: 2026-09-10 - Startup Validation Deadline

**TL;DR:** On boot the server re-checks every scheduled job's stored definition to find ones a later
release would now reject, and nothing limited how long that took before the web API would answer.
This session put a 45-second budget on it. Most of the work was not the timer: a pass that runs out
of budget has checked only part of the set, so it would report "3 invalid" when the real number is
higher and unknown. Deciding exactly what the boot log says in that case came before choosing the
number.

## Handoff

Item 5 of 6 in the roadmap Now batch. Closes
[[feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep]]. Merged as PR #210, three commits plus
spec/plan.

New signature, which other lanes need to know:
`ValidateStoredSpecsOnStartup(ctx context.Context, q *store.Queries, budget time.Duration) (SweepResult, error)`.
`RELAY_STARTUP_VALIDATION_DEADLINE`, default 45s, deliberately above `RELAY_DB_STATEMENT_TIMEOUT`'s
30s so one slow statement cannot eat the pass. No off token; a zero budget is an EXPIRED deadline,
not an absent one, so a zero value cannot restore the unbounded boot.

`SweepResult{Checked, Invalid, Truncated}` puts the counts and the caveat in one value, so no caller
can print a partial count without the field that says it is partial. **`Truncated` comes from the
child context's `Err()`, never from the returned error** - SIGTERM gives `Canceled`, the timer gives
`DeadlineExceeded` - and it is set at both non-nil return sites rather than in a `defer`, because a
`defer` merges them and no mutation could then distinguish the page-query site from the row-loop one.

**Measured throughput, loopback, 2000 rows per regime:** all-healthy-small 85,726 rows/s; all-healthy
fat-spec (19,115 bytes/row) 2,751 rows/s, i.e. ~123,780 rows in 45s; all-broken 802 rows/s, ~36,077
in 45s. The stated re-scope threshold was 10,000 fat-spec rows, so the default did NOT move. These
are loopback numbers on an idle machine; a remote database adds a round trip per page and per UPDATE.

`store.Migrate` remains the one pre-listener step with no duration bound and no knob -
`applyStatementTimeout`'s header records that the statement timeout deliberately does not reach
migrations.

Next entry point: the batch is complete; ROADMAP.md needs a refresh.

## What Was Built

- The budget threaded as a value with `context.WithTimeout` created inside the sweep, plus
  `SweepResult`.
- `cmd/relay-server/startupvalidation_config.go` with the parser, and a second wiring guard in its own
  file.
- Three boot-line shapes: an unconditional bounds line, a completed line, and a three-line truncated
  block.
- Four integration guards: the zero-budget refusal, a mid-pass deadline, a shutdown that does NOT
  advertise the deadline, and the two counts.

## Key Decisions

- **The mechanism was chosen by a guard, not by taste.**
  `TestSchedrunnerStartupSweepIsWiredInOrderByMain` parses `main`'s own statement list and requires
  exactly one call, so the obvious encapsulation - a local helper holding the bounded context - makes
  that count zero. That is also why no bounded context exists in `main`'s scope, where the next five
  statements hand `ctx` to long-lived goroutines.
- **Truncation can only produce false negatives.** The sweep has no clearing sibling, so a recorded
  failure stays trustworthy and only its ABSENCE loses meaning. That replaced the item's per-row
  disclosure criterion, which would have meant writing to every row the deadline declined to check -
  the exact unbounded work being cut.
- **Three lines, not one.** The counts and `THESE ARE FLOORS, NOT TOTALS` sit on the same line so even
  a partial print is honest, and every line carries the same prefix so a grep retrieves the block. An
  embedded newline in one `log.Print` was rejected because `log` prefixes the record, not each line.
- **The remedy ladder is tightening-first**, because the enabled count is grown by any authenticated
  user and is unbounded in owners under `RELAY_ALLOW_SELF_REGISTER` - so "raise the deadline" is the
  remedy that widens the delay a hostile population can drive.
- **No remainder**, argued on the sweep's own terms rather than inherited: a pass resumed after the
  listener would run concurrently with the runner, and `AdvanceScheduledJob` clears `last_error` while
  touching none of the three columns `RecordScheduledJobFailure` fences on, so a row fired in between
  gets its fresh clear stamped over with a stale verdict.

## What Went Wrong and What Changes

Ledger: the prior retro's entries were all promoted, so none are carried. Promoted lessons that
fired: [[reference_same_typed_args_transpose_silently]] - the headline entry below;
[[reference_verify_the_mutation_applied]], which caught a whole technique failing silently;
[[reference_a_kill_must_name_its_guard]]; [[reference_lossy_aggregate_discloses_where_read]];
[[reference_relay_the_input_not_just_the_number]]; [[feedback_commit_heredoc_shell]].

- **`go test -overlay` cannot mutate `main.go`, and the first run reported "ok" with the mutant
  plainly present.** `parseMainBodyAndPkgName` calls `parser.ParseFile`, which is ordinary file I/O
  the build overlay never intercepts. A silently-unapplied mutation reports "survived".
  -> **What changes:** a mutation aimed at a test that READS source with `go/parser` or `go/ast` must
  run in a real tree copied outside the repo, never through `-overlay`. Before trusting any overlay
  battery, confirm the overlay is live by introducing a deliberate compile error and checking the
  error names the overlay path. (promoted to project CLAUDE.md)
- **The spec forbade a wiring guard for a reason that did not apply, and the gap was real.** Its
  instruction was that a criterion green before the change pins nothing - true in general, but an
  assertion on the call's THIRD argument is RED at HEAD when the call has two. `main` holds at least
  four `time.Duration` locals, every one compiles in that position, and
  `watchdog_config_test.go` already records that exactly this swap COMPILES and left the package green
  when measured. Verified independently: swapping `staleAfter` in makes the new guard fail.
  -> **What changes:** when a change adds a parameter to a call in a file that an AST guard already
  reads, check whether an assertion about the NEW parameter is red at HEAD before accepting
  "that criterion is already green". The general rule does not cover a position that did not exist.
- **Two assertions that looked positional were not.** `Contains(lines[0], "45s")` and
  `Contains(lines[0], "47.123s")` were described as catching a budget/elapsed transposition; under the
  swap BOTH values are still present in the line, so both assertions stay green. Measured both ways.
  -> **What changes:** an assertion that two values appear is not an assertion that they appear in the
  right ROLES. Anchor each to the clause that gives it meaning, not to the line.
- **Three "expected kill" cells named an assertion a `require` earlier in the test aborts before
  reaching**, so those content assertions were unproven.
  -> **What changes:** when recording which assertion a mutation kills, run it and read the failure.
  If the kill lands on an earlier `require`, the named assertion needs its own mutant - one that keeps
  the earlier invariant and changes only what that assertion reads.
- **Two planned mutations did not compile, and a compile error is not a kill.**
  -> **What changes:** a mutation must leave the package building. If deleting a statement orphans a
  variable or an import, rewrite the mutant into the shape the real regression would take.
- **A stale-prose search could not find the site it was told to find.** The claim straddled a comment
  line break, so a single-line `rg` returned only dated records and zero code hits.
  -> **What changes:** when searching for a prose claim in code comments, use a multiline pattern with
  flexible whitespace. A claim worth searching for is long enough to wrap.
- **A survivor that only `go vet` catches.** `ctx, _ = context.WithTimeout(...)` is caught by
  `lostcancel`, and the test suite alone does not catch it - measured, with both packages reporting
  `ok`.
  -> **What changes:** when a mutation survives the whole suite, check whether a vet analyser owns it
  before calling it uncovered, and record which tool is the guard.

## Recommended Backlog Items

Backlog intake, not a priority order.

- [idea] **A resumed validation pass after the listener needs a fence the tree does not have.**
  `AdvanceScheduledJob` clears `last_error` while touching none of the three columns
  `RecordScheduledJobFailure` fences on, so a continuation running beside the runner can stamp a stale
  verdict over a fresh clear. **The item must not prescribe the fence** - that is the design question.
- [bug] **`store.Migrate` has no duration bound and no knob on the pre-listener path.**
  `applyStatementTimeout` deliberately does not reach migrations, so a `CREATE INDEX` on a large table
  delays the listener without limit. Pre-existing; this slice bounds the sweep beside it.
- [idea] **`GET /v1/scheduled-jobs/stats`' `failing` key is a live count documented as "Not
  windowed".** It now has a second cause of under-reporting, and it is the one numeric reader an
  operator is most likely to trust.

## Files Most Touched

- `internal/schedrunner/startup_validation.go` - the budget, `SweepResult`, the two `Truncated` sites.
- `internal/schedrunner/startup_validation_deadline_integration_test.go` - new, four guards.
- `cmd/relay-server/startupvalidation_config.go` and its test - the parser and the three line shapes.
- `cmd/relay-server/startupvalidation_wiring_test.go` - new; the positional guard the spec forbade.
- `cmd/relay-server/main.go` - the call site and the two prints.
- `internal/schedrunner/runner.go` - one stale trailing clause corrected.
- `README.md` - startup sequence item 5, the env var row, and the truncation caveat where the count is
  read.
