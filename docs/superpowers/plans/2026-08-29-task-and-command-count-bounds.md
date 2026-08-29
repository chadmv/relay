# Task and command count bounds - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound `len(JobSpec.Tasks)`, `len(TaskSpec.Commands)` and the job-wide command total in `jobspec.Validate`, so that no single request can name an unbounded number of subprocess spawns or `task_logs` rows.

**Architecture:** Three constants and three comparisons inside `jobspec.Validate`, which the Single job-spec pipeline invariant then propagates to every ingest path for free. Two of the three are per-axis caps; the third is a running total accumulated inside the existing per-task loop, and it is the only one of the three that moves the aggregate worst case. No new package, no migration, no generated code, no proto change, no SQL change.

**Tech Stack:** Go 1.26, testify, testcontainers-go (integration lane), Postgres 16. No `make generate` step anywhere in this plan - nothing under `internal/store/query/` is touched.

**Spec:** `docs/superpowers/specs/2026-08-29-task-and-command-count-bounds.md` (including its `## Gate decision (conductor, autonomous mode, 2026-08-29)` section, whose three rulings are settled)
**Backlog item this closes:** `docs/backlog/bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers.md` (half 1 only; half 2, rate-limiting `POST /v1/jobs`, is a separate item the conductor files before closing)
**Predecessor slice, whose shape this deliberately repeats:** `docs/superpowers/plans/2026-08-27-retry-bounds-and-budget-predicate.md`

---

## Slice independence declaration

**This is a single-slice, single-PR, single-session plan. It has no stages and must NOT be handed to `/backlog phases`.** Every task below is a few minutes of work and the whole thing is one PR.

**Frontend/backend: backend plus docs, with exactly one comment-only line-block in one `.ts` file.** This was checked rather than assumed:

- `web/src/jobs/specTemplate.ts`'s `validateSpecText` **is** a client-side job-spec pre-check, and it **already duplicates the lower end of the very range this slice bounds** (`obj.tasks.length === 0`). It has no upper bound and must not gain one. Task 7 edits its doc comment only - no behaviour, no test, no `web/dist`, no `npm` command, no frontend build.
- `web/src/jobs/NewJobPage.tsx` enumerates the accepted spec field names in prose and makes **no claim about their ranges**, so nothing there becomes false.
- `python/src/relay/models.py` carries `retries: int = 0` and `timeout_seconds: Optional[int]` with no bounds and no prose; it has no `tasks`/`commands` count claim at all. Unchanged.
- Nothing under `web/` or `python/` renders or asserts a task/command count bound.

**There is therefore no frontend slice to run in parallel with, and Phase 3 must be a single backend lane.** Do not dispatch `relay-frontend-engineer` for Task 7's one comment; it is three sentences in a file no test reads, and splitting it across two agents costs a second commit and a second review for nothing. If the conductor prefers strict role separation anyway, the `.ts` hunk is the last step of Task 7 and can be lifted out wholesale.

**The tasks are sequential and each leaves the tree green.** Tasks 1-3 are three RED/GREEN cycles on the same two files; Tasks 4-5 are proofs that depend on all three constants existing; Tasks 6-8 are prose, README and the battery.

---

## What I refuted in the spec

The spec is substantially correct and its arithmetic holds. I re-derived every number that lands in a doc comment or a README row, and checked every prescribed edit target by symbol. Eight findings.

### F1. The spec asks for a test that no input can distinguish, and calls it a guard

The spec's Testing section requires: *"The per-task command bound applies to the legacy `command` spelling too. A spec using `command` cannot exceed the bound, so the assertion is that a legacy spec at one command is still accepted after the check is added - a guard against a check placed before `normalizeTaskCommands`."*

**A plain upper-bound check hoisted above `normalizeTaskCommands` is behaviourally identical to one below it, for every input that exists.** A legacy task has `len(ts.Commands) == 0` before normalization and `1` after; `0 > 500` and `1 > 500` are both false. The proposed test passes under both positions. It kills only the *range*-shaped variant (`len < 1 || len > 500`), which is a different mutation, and the tree already contains three tests that a legacy spec is accepted (`TestValidate_HappyPath`, `api.TestValidateJobSpec_NormalizesLegacyCommand`, and the CLI's own submit fixtures), so writing a fourth adds nothing.

**The half of the ordering that IS testable is the accumulator, and the spec never noticed it.** Above the normalization a legacy task contributes `0` to the running total; below it, `1`. So a spec of 50 tasks x 500 commands (exactly 25,000) plus one **legacy-`command`** task is accepted under the hoisted accumulator and refused under the correct one. That input is in Task 3 as `TestValidate_TheJobWideCommandTotalIsBounded/a_legacy_command_task_counts_toward_the_total`, and it is the only thing anywhere that separates the two positions. Task 8's battery lists the per-task hoist as **SURVIVES BY CONSTRUCTION, documented, not tested** rather than pretending otherwise.

### F2. The spec's argument for "per-task check first within the bounds block" is broader than what it proves

The spec says placing the per-task command check first within the bounds block *"guarantees a task that is itself over the per-task cap gets the specific, task-naming message rather than the job-level total message"*. That argument establishes **per-task before the total**, and nothing about **per-task before `retries`**. The stronger placement is still adopted (the two command checks belong together and the accumulator has to follow its own check), but the reason recorded in the code comment is the one that is actually true, not the one the spec wrote.

### F3. The spec's precedence note is a strict undercount

The spec names one precedence consequence of the top-of-function task-count check: a spec over the count *and* carrying an invalid priority now reports the count. **Three more move with it**, because the check is placed above the priority switch and above the whole per-task loop: `task name is required`, `duplicate task name: %s`, and every `normalizeTaskCommands` error. All four are new-bound-versus-old-error, so no existing test can depend on the old order, and I confirmed by reading that nothing in the tree reads these messages positionally. The code comment in Task 1 names all four rather than one.

### F4. Two of the spec's own stated stored-spec sites already have their coverage, and one of them cannot be extended the way the spec suggests

The spec says of the retroactivity paths: *"the cheapest honest version is to extend them, not to duplicate them."* Measured:

- Site 3 (`ValidateStoredSpecsOnStartup`) and site 4 (`handlePatchScheduledJob`'s clear-decision) both reach `jobspec.Validate` through the **same exported symbol**, `schedrunner.ValidateStoredSchedule`, which is a **pure function of three stored values and needs no database**. Task 5 therefore covers both in an **untagged** test in the plain `go test ./...` lane. The spec's model - extend `stored_spec_bounds_test.go`, which is `//go:build integration` - would have put the only count-axis coverage of two call sites behind the Docker gate that `.github/workflows/go-ci.yml` never runs.
- Site 1 (`schedrunner.fireOne`)'s existing test is `TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled`, which is a **fifteen-assertion end-to-end record-and-clear proof**. Adding a count-axis schedule to it would need a second poisoned row and a second clear cycle for a mechanism that is message-agnostic and already proven. Task 5 does **not** touch it. What Task 5 does touch is its `makeOverBudgetSpecJSON` header, which is stale (see F5).

### F5. The stale-comment list in the spec is incomplete by three, and two of the three were falsified by PR #159 rather than by this slice

The spec names two stale sites (`maxRetries`'s doc comment and `makeOverBudgetSpecJSON`'s header) plus the "four ingest paths" phrase. Grepping for the *shape* of the claim rather than for its subject turns up three more:

1. **`internal/api/jobs.go`, `uuidStrHead`'s doc comment:** *"Task count is bounded only against zero (jobspec.Validate)"*. **This slice makes it false.** It is the only sentence in non-doc code that this slice falsifies by itself. The argument around it survives - 5000 task ids is still ~185 KB under `log.Printf`'s global mutex, so the cap is still warranted - but the claim does not.
2. **`internal/api/scheduled_jobs.go`, `handleRunScheduledJobNow`:** *"run-now is the ONLY interactive path that can explain why a schedule stopped producing jobs: schedrunner's fireOne logs one server-side line and advances next_run_at, leaving nothing user-visible behind."* **Falsified by PR #159**, which gave `fireOne`'s failure branch `last_error` / `last_error_at`. `fireOne` now leaves a great deal user-visible.
3. **`internal/api/scheduled_jobs_run_now_bounds_integration_test.go`'s header** repeats the same falsified sentence **and names a test that no longer exists** - `TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard` was renamed by PR #159 to `TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled`. Its line-67 assertion message repeats "the ONLY interactive path" too.

2 and 3 are not this slice's debt. They are corrected here anyway, and Task 6 says so in the commit message, because this slice re-reads exactly that path and extends exactly that test file - leaving a known-false sentence and a dangling test name in a file the reviewer opens is worse than a four-line unrelated correction.

### F6. The correct convention for the ingest-path count is to carry no count at all, and this repo already settled that

The spec (and the conductor's brief) says the phrase "shared by four ingest paths" is stale and that there are six. There are six, and I enumerated them by symbol rather than trusting the number: `api.handleCreateJob`, `api.handleCreateScheduledJob`, `api.handlePatchScheduledJob` (when the request body carries `job_spec`), `mcp.submit`, and `mcp.schedules_write` on **both** create and update.

But `internal/schedrunner/failure.go` already ruled on this exact question, in a paragraph written for exactly this failure mode:

> (A COUNT IS NOT CARRIED HERE ON PURPOSE. "four clients" was written when there were four, and was already an undercount by the time MCP started labelling `last_error` in the same PR. An enumeration goes stale loudly - a reader can check it - where a number goes stale silently and has no maintainer.)

**So the corrected comment must ENUMERATE and must not say "six".** Writing "six" would repeat the defect in a fresher spelling, and the next ingest path added would make it wrong with nothing to notice. Task 6 replaces the count with the enumeration.

### F7. One spec number is optimistic, and it is load-bearing for nothing

The spec argues 25,000 is above the legitimate maximum with: *"5000 tasks at 3 commands each is 15,000, and at a realistic size that spec is around 750 KB, which only just fits the body limit."* 750 KB / 5000 tasks is 150 bytes for a task carrying **three** real command lines plus a unique name. A realistic three-command task is 250-300 bytes, so that spec is 1.25-1.5 MB and does **not** fit `maxBodyBytes`. The direction of the error strengthens the spec's own conclusion - the legitimate ceiling is *lower* than claimed, so 25,000 is further above it - so nothing changes. **It must not appear in a doc comment or in README.** The constant's comment in Task 3 states the shape without the byte figure.

### F8. The predecessor's test helpers cannot be reused, and reusing them would go green for the wrong reason

`internal/jobspec/jobspec_bounds_test.go`'s `twoTaskSpec` and `offenderSecondSpec` both assign `bad.Command = []string{"echo", "x"}` before returning. A `TaskSpec` that already carries `Commands` and then gets a `Command` assigned is refused by `normalizeTaskCommands` with `set either command or commands, not both` - so every command-count case routed through those helpers would produce an error, look red-then-green, and be testing the command-form rule instead of the bound. The count cases get their own file and their own helpers (Task 2), and `jobspec_bounds_test.go` stays **byte-identical**.

### Checked, not refuted, recorded so nobody re-derives it

- **The spec's arithmetic.** `["true"],` is exactly 9 bytes; 1 MiB / 9 = 116,508, so "~116,000" is right. 116,508 x 11 = 1,281,588 ("1.28 million"). 25,000 x 11 = 275,000. 116,508 / 25,000 = 4.66 ("4.6x"). 2,500,000 / 116,508 = 21.5 ("about 21x"). 116,508 / 500 = 233 ("about 230x"). 25,000 / 500 = 50 ("at least 50 tasks"). A minimal legacy task `{"name":"abc","command":["a"]},` is 31 bytes, so 1 MiB permits ~33,800 and the "6x reduction on the task count" holds conservatively. A realistic ~100-byte task caps a request near 10,000, so 5000 binds at half the realistic ceiling. **All confirmed.**
- **Refutation 1 (`sendStepMarker` is not unconditional).** Confirmed at the symbol: `internal/agent/runner.go`'s loop calls it at the top of the body, *after* the `cl == nil || len(cl.Argv) == 0` guard, and both that guard and a failed `cmd.Start()`/`cmd.Wait()` `break`.
- **`GetEligibleTasks` has no `LIMIT`.** Confirmed by reading `internal/store/query/tasks.sql`.
- **`jobcreate.CreateJobFromSpec` issues one `CreateTaskDependency` round trip per edge** inside the caller's transaction, and one `CreateTask`/`CreateTaskWithSource` per task. Confirmed. Refutation 5's recommendation (a new backlog item, not this slice) stands.
- **`maxCommandsPerJob` implies `tasks <= 25000`.** True because `normalizeTaskCommands` refuses a task with zero commands, so every task contributes at least 1.
- **`internal/schedrunner/failure.go`'s `maxFailureTextBytes` comment** says 1024 is "comfortably above every fixed-format message `jobspec.Validate` emits; the only way to exceed it is an operator-chosen task name of roughly a kilobyte." The three new messages are 40-60 bytes and one of them has no task name at all. **Still true, no edit.**
- **`internal/store/createtask_guard_test.go`'s header** is about `tasks.retries` and `tasks.timeout_seconds` carrying no CHECK. Count bounds have no per-row column, so nothing there is falsified. Considered adding a sentence and rejected as churn: the guard already covers the count bounds for free, since a caller that bypasses `jobcreate` bypasses all five bounds equally.
- **No existing spec fixture anywhere in the repo** (Go tests, `web/e2e`, `python/tests`, README examples, `web/src/jobs/specTemplate.ts`'s `STARTER_TEMPLATE`) has more than a handful of tasks or more than a handful of commands. Nothing existing breaks.
- **`ROADMAP.md`'s refresh-30 entry** describes what the predecessor slice shipped and is a historical log; it is not falsified and it is the conductor's file. No edit.

---

## Critical files

**Modified (production):**

| File | What changes |
|---|---|
| `internal/jobspec/jobspec.go` | Three constants after `maxTimeoutSeconds`; one check after the `len(spec.Tasks) == 0` check; one `totalCommands` declaration beside `nameSet`; two checks at the head of the per-task bounds block. **Plus** the `maxRetries` doc comment's stale third paragraph. |
| `internal/api/jobs.go` | **Comment only.** `uuidStrHead`'s "bounded only against zero". |
| `internal/api/scheduled_jobs.go` | **Comment only.** `handleRunScheduledJobNow`'s "ONLY interactive path ... nothing user-visible". |
| `web/src/jobs/specTemplate.ts` | **Comment only.** `validateSpecText`'s "deeper rules" list, plus a do-not-mirror instruction. No behaviour, no build, no `web/dist`. |
| `README.md` | Two rows ADDED, three rows rewritten, two paragraphs in the Scheduled jobs section rewritten. |

**Modified (tests):**

| File | What changes |
|---|---|
| `internal/api/jobs_spec_bounds_integration_test.go` | One new test function; `fmt` added to the import block. Existing test untouched. |
| `internal/api/scheduled_jobs_run_now_bounds_integration_test.go` | One new leg on the existing bounds test; the falsified header sentence and the dangling test name corrected. |
| `internal/jobcreate/jobcreate_validate_test.go` | One new test function beside the existing one. |
| `internal/schedrunner/stored_spec_bounds_test.go` | **Header comment only** on `makeOverBudgetSpecJSON`. |

**Created (tests):**

| File | Contents |
|---|---|
| `internal/jobspec/count_bounds_test.go` | The three unit-level bound tests and their helpers |
| `internal/schedrunner/stored_spec_count_bounds_test.go` | Untagged `ValidateStoredSchedule` coverage for retroactivity sites 3 and 4 |

**Not touched, deliberately:** `internal/jobspec/jobspec_bounds_test.go` (must stay byte-identical - see F8), anything under `internal/store/query/` (**no `make generate` anywhere in this plan**), `internal/cli/` (see Task 4's lane note), `web/dist/`.

**Read before starting:** `CLAUDE.md`'s Invariants section (the Single job-spec pipeline bullet), the existing `maxRetries` and `maxTimeoutSeconds` comments in `internal/jobspec/jobspec.go` (the new comments must match their register), and the paragraph in `internal/schedrunner/failure.go` that explains why a count is never carried in a comment.

---

## The three constants and the three messages

Fixed for the whole plan. Every test below spells these as **literals**, never as the constant, so a test cannot agree with a moved constant by construction.

| Constant | Value | Message on violation |
|---|---|---|
| `maxTasksPerJob` | `5000` | `at most 5000 tasks are allowed, got 5001` |
| `maxCommandsPerTask` | `500` | `task bad-task: at most 500 commands are allowed, got 501` |
| `maxCommandsPerJob` | `25000` | `at most 25000 commands in total across all tasks are allowed` |

The third has **no `got` clause**, per the conductor's ruling on open question 2: the check fires mid-loop and does not know the final total, and a running figure that varies with task ordering for the same spec is worse than no figure.

---

## Task 1: Bound the task count

**Files:**
- Create: `internal/jobspec/count_bounds_test.go`
- Modify: `internal/jobspec/jobspec.go` (add `maxTasksPerJob` after the `maxTimeoutSeconds` const; add one check inside `Validate` immediately after the `len(spec.Tasks) == 0` block)

- [ ] **Step 1: Write the failing test**

Create `internal/jobspec/count_bounds_test.go`:

```go
package jobspec

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// argvN returns n runnable one-element argv slices.
//
// `["true"]` is deliberate: internal/agent/runner.go emits its per-command step
// marker only for commands it actually EXECUTES - the loop breaks on an empty
// argv and on a failed Start or Wait - so the cheapest entry that costs anything
// is a runnable one. That is the shape all three bounds' arguments are written
// against, and it is why an unrunnable `["a"]` filler would misrepresent them.
func argvN(n int) [][]string {
	out := make([][]string, n)
	for i := range out {
		out[i] = []string{"true"}
	}
	return out
}

// nTaskSpec builds a valid spec of n tasks, each with a unique name and exactly
// ONE command. It exercises the task-count axis alone: n commands in total, one
// per task, so for every n this file uses neither command bound is in play and a
// failure can only have come from the task count.
func nTaskSpec(n int) *JobSpec {
	tasks := make([]TaskSpec, n)
	for i := range tasks {
		tasks[i] = TaskSpec{Name: "t" + strconv.Itoa(i), Command: []string{"true"}}
	}
	return &JobSpec{Name: "counts", Tasks: tasks}
}

// TestValidate_TheTaskCountIsBoundedAtBothEnds pins the upper end of the range
// whose lower end is "at least one task is required". RED at HEAD: Validate reads
// len(spec.Tasks) only against zero, so the 5001-task spec is accepted.
//
// THE NUMBERS ARE LITERALS, NOT maxTasksPerJob, ON PURPOSE. A test that spells
// the constant agrees with the implementation by construction and cannot detect
// the constant moving - which is the single most likely defect in a change that
// is three constants and three comparisons.
func TestValidate_TheTaskCountIsBoundedAtBothEnds(t *testing.T) {
	t.Run("one over the cap is rejected and the message reports the count", func(t *testing.T) {
		require.EqualError(t, Validate(nTaskSpec(5001)),
			"at most 5000 tasks are allowed, got 5001",
			"the message must STATE the limit and REPORT what arrived: a caller who generated the "+
				"spec has to know by how much to chunk it, and a limit-only message tells them nothing "+
				"about their own input")
	})

	t.Run("exactly at the cap is accepted", func(t *testing.T) {
		require.NoError(t, Validate(nTaskSpec(5000)),
			"a spec AT the boundary must still be accepted - this is the leg an off-by-one written "+
				"as >= breaks, and nothing else in the tree catches it")
	})

	t.Run("the count is refused before the per-task loop runs", func(t *testing.T) {
		// A 5001-task spec whose first two tasks share a name. In the current
		// placement the job-level count wins; a count check moved into or below the
		// per-task loop reports "duplicate task name: t0" instead.
		//
		// THE PRECEDENCE IS THE INSTRUMENT, NOT THE POINT. The point is that the
		// spec is refused BEFORE nameSet is allocated at len(spec.Tasks) capacity
		// and before 5001 normalizeTaskCommands calls - which is work this bound
		// exists to avoid doing, and the only observable trace of "before" is which
		// message comes back.
		spec := nTaskSpec(5001)
		spec.Tasks[1].Name = spec.Tasks[0].Name
		require.EqualError(t, Validate(spec), "at most 5000 tasks are allowed, got 5001")
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/jobspec/ -run TestValidate_TheTaskCountIsBoundedAtBothEnds -v
```

Expected: **2 FAIL, 1 PASS.**

```
--- FAIL: TestValidate_TheTaskCountIsBoundedAtBothEnds/one_over_the_cap_is_rejected_and_the_message_reports_the_count
    Error:  An error is expected but got nil.
--- FAIL: TestValidate_TheTaskCountIsBoundedAtBothEnds/the_count_is_refused_before_the_per-task_loop_runs
    Error:  Not equal:
            expected: "at most 5000 tasks are allowed, got 5001"
            actual  : "duplicate task name: t0"
--- PASS: TestValidate_TheTaskCountIsBoundedAtBothEnds/exactly_at_the_cap_is_accepted
```

The file must **compile** at HEAD - it references no symbol this task adds. If you get a compile error, a helper name collided with `internal/jobspec`'s existing test files (`jobspec_test.go`, `jobspec_bounds_test.go`); rename yours rather than touching theirs.

- [ ] **Step 3: Write the implementation**

In `internal/jobspec/jobspec.go`, immediately after `const maxTimeoutSeconds = 604800` and before the `Validate` doc comment, add:

```go
// maxTasksPerJob bounds len(JobSpec.Tasks). A task in relay is a frame, a frame
// chunk, a build step, or one unit of a fan-out, so the realistic high end for a
// single submission is a full animation submitted one task per frame: a 1000 to
// 2000 frame sequence. Chunking frames - the usual practice, because per-task
// dispatch and workspace-prep overhead dominates for fast frames - puts the same
// sequence at a couple of hundred tasks. A build with a few hundred steps and a
// parameter sweep of a few hundred units both land far below. 5000 is 2.5x to 5x
// above that high end, so no submission a user plausibly wants is refused.
//
// IT STILL BINDS. A realistic task with a real command line is around 100 bytes
// of JSON, so maxBodyBytes (1 MiB, internal/api/server.go) already caps a
// realistic request near 10,000 tasks and this cap binds at half of that. Against
// minimal JSON - a short unique name and a one-element argv, on the order of 30
// to 35 bytes - the body permits on the order of 30,000, so this is roughly a 6x
// reduction on the worst case.
//
// DO NOT RAISE THIS WITHOUT A REFUSED REAL SUBMISSION. "The number looks small"
// is not the reason; a job somebody actually wanted to run being rejected is. And
// before raising it, look at the two costs this number stands in for, because
// fixing either is a better answer than a larger cap:
//   - jobcreate.CreateJobFromSpec inserts tasks ONE AT A TIME, one round trip
//     each, inside the caller's transaction. 5000 tasks is 5000 sequential round
//     trips - a slow request. 30,000 is a different thing entirely.
//   - store.GetEligibleTasks has NO LIMIT, and scheduler.Dispatcher.dispatch runs
//     it on every Trigger() and every 30 seconds, so a large pending backlog is
//     re-read in full on every tick until it drains. That is a fleet-wide
//     property, so a per-request cap bounds one request's contribution to it and
//     nothing about repetition.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.spec rows, and it applies identically
// to every bound in this file.
const maxTasksPerJob = 5000
```

Then, inside `Validate`, immediately after the `if len(spec.Tasks) == 0 { ... }` block and before the priority switch, add:

```go
	// The other end of the SAME range, deliberately adjacent to it, so a reader
	// changing either bound sees the other - the same adjacency argument that keeps
	// the retries and timeout_seconds bounds together. It is a JOB-level property,
	// so it has no task name to interpolate and does not belong in the per-task
	// loop. And it refuses the spec BEFORE the work it bounds: before nameSet is
	// allocated at len(spec.Tasks) capacity and before one normalizeTaskCommands
	// call per task.
	//
	// PRECEDENCE CONSEQUENCE, TAKEN DELIBERATELY. A spec that is over this bound
	// AND carries an invalid priority, a nameless task, a duplicate task name or a
	// bad command form now reports the task count, where the older code reported
	// whichever of those came first. No test can depend on the old order, since the
	// bound is new, and nothing reads these messages positionally. The wording
	// mirrors "at least one task is required" so the pair reads as one range.
	if len(spec.Tasks) > maxTasksPerJob {
		return fmt.Errorf("at most %d tasks are allowed, got %d", maxTasksPerJob, len(spec.Tasks))
	}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/jobspec/ -v
go test ./... -timeout 180s
```

Expected: all `internal/jobspec` tests PASS, and the whole default lane stays green. No fixture anywhere in the repo has more than a handful of tasks, so nothing else should move.

- [ ] **Step 5: Commit**

```bash
git add internal/jobspec/jobspec.go internal/jobspec/count_bounds_test.go
git commit -m "feat(jobspec): bound the task count at the single validation point"
```

---

## Task 2: Bound the per-task command count

**Files:**
- Modify: `internal/jobspec/count_bounds_test.go` (add one helper and one test)
- Modify: `internal/jobspec/jobspec.go` (add `maxCommandsPerTask` after `maxTasksPerJob`; add one check at the head of `Validate`'s per-task bounds block)

- [ ] **Step 1: Write the failing test**

Append to `internal/jobspec/count_bounds_test.go`:

```go
// commandCountSpec builds a two-task spec whose OFFENDING task carries n
// commands, placed at index pos (0 or 1). The other task is healthy.
//
// IT IS NOT twoTaskSpec FROM jobspec_bounds_test.go AND MUST NOT BE REPLACED BY
// IT. That helper assigns bad.Command before returning, and a task carrying both
// Command and Commands is refused by normalizeTaskCommands with "set either
// command or commands, not both" - so every case here would go red at HEAD, green
// after the change, and be exercising the command-form rule rather than the
// bound. That is the "a test can be green because of the bug" shape, in its
// helper-reuse spelling.
func commandCountSpec(pos, n int) *JobSpec {
	bad := TaskSpec{Name: "bad-task", Commands: argvN(n)}
	healthy := TaskSpec{Name: "healthy-task", Command: []string{"echo", "y"}}
	tasks := []TaskSpec{bad, healthy}
	if pos == 1 {
		tasks = []TaskSpec{healthy, bad}
	}
	return &JobSpec{Name: "counts", Tasks: tasks}
}

// TestValidate_ThePerTaskCommandCountIsBounded is the concentration control's
// test: the bound on how much sequential work one request can pin to ONE worker
// slot. RED at HEAD: Validate never reads len(ts.Commands) except through
// normalizeTaskCommands' per-entry argv check.
func TestValidate_ThePerTaskCommandCountIsBounded(t *testing.T) {
	const overMsg = "task bad-task: at most 500 commands are allowed, got 501"

	t.Run("one over the cap, offender FIRST", func(t *testing.T) {
		// The offender is first and a healthy task follows it, so the refusal cannot
		// have been produced by "the last task lost" or by an early exit.
		require.EqualError(t, Validate(commandCountSpec(0, 501)), overMsg,
			"the message must NAME the offending task: a caller with a fifty-task spec has to be told "+
				"WHICH task to split")
	})

	t.Run("one over the cap, offender SECOND", func(t *testing.T) {
		// The mirror, and BOTH positions are needed. An offender at index 0 defeats
		// a loop body that never runs; an offender at index 1 defeats a loop body
		// guarded with `i == 0`. jobspec_bounds_test.go records that the second
		// mutant SURVIVED that entire file before its own index-1 cases existed, so
		// this is a measured hazard on this exact function, not a hypothetical.
		require.EqualError(t, Validate(commandCountSpec(1, 501)), overMsg,
			"the bound must apply to EVERY task, and the message must name the SECOND one")
	})

	t.Run("exactly at the cap is accepted", func(t *testing.T) {
		require.NoError(t, Validate(commandCountSpec(0, 500)),
			"a task AT the boundary must still be accepted - the leg a >= breaks")
	})

	t.Run("the per-task bound wins over the job-wide total", func(t *testing.T) {
		// ONE task with 25,001 commands violates BOTH command bounds. The per-task
		// check runs first, so the message must NAME THE TASK. Transpose the two
		// checks and this returns the job-level total message, which would accuse
		// the whole job for what is one task's fault - and would tell the caller to
		// shrink a spec that has only one thing wrong with it.
		require.EqualError(t, Validate(commandCountSpec(0, 25001)),
			"task bad-task: at most 500 commands are allowed, got 25001")
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/jobspec/ -run TestValidate_ThePerTaskCommandCountIsBounded -v
```

Expected: **3 FAIL, 1 PASS.** The three rejection subtests fail with `An error is expected but got nil.`; `exactly at the cap is accepted` passes (there is no bound yet).

- [ ] **Step 3: Write the implementation**

In `internal/jobspec/jobspec.go`, immediately after `const maxTasksPerJob = 5000`, add:

```go
// maxCommandsPerTask bounds len(TaskSpec.Commands) after normalization.
//
// `commands` exists so several steps share ONE prepared workspace and
// environment: sync, build, render, publish, clean up. The realistic shape is
// single digits. The plausible high end is a task that iterates a fixed list
// inside one prepared workspace - export N assets from a scene, bake N maps -
// which is tens. 500 is roughly 20x that.
//
// THIS IS THE CONCENTRATION CONTROL. It bounds how much sequential work a single
// request can pin to a single worker slot: at the bound, one task is 500
// subprocess spawns per attempt and 5500 across a full maxRetries budget.
//
// A USER AT THIS BOUND IS BEING TOLD TO USE THE BETTER MODEL, NOT TOLD NO. Past a
// few hundred, one task per unit is better anyway: separate tasks parallelize
// across the fleet, retry independently and report per-unit status, which is the
// entire point of a task graph. Tasks sharing a `source` reuse the same workspace
// (workspace_exclusive defaults to false), so splitting does not cost the
// workspace sharing that motivates `commands` in the first place.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.spec rows, and it applies identically
// to every bound in this file.
const maxCommandsPerTask = 500
```

Then, inside `Validate`'s per-task loop: move the existing two-line lead-in comment (`// Bounds last in this loop body, so command-form and duplicate-name` / `// errors keep the precedence they have today.` and the `//` blank line that follows it) so that it heads the new check, expand it as shown, and insert the check. The result, from `nameSet[ts.Name] = struct{}{}` down to the existing `if ts.Retries` line, must read:

```go
		nameSet[ts.Name] = struct{}{}
		// Bounds last in this loop body, so command-form and duplicate-name
		// errors keep the precedence they have today. That promise is about
		// precedence WITHIN one iteration. A bound has always been able to preempt
		// a form error on a LATER task - a bad `retries` at index 3 has always
		// outrun a duplicate name at index 90 - and the running total below is one
		// more instance of that rather than a change to it.
		//
		// THE COMMAND CHECK READS THE NORMALIZED VALUE, i.e. it sits AFTER
		// normalizeTaskCommands, which rewrites a legacy single Command into a
		// one-element Commands and clears Command. That covers both spellings by
		// construction. BE HONEST ABOUT WHAT THAT BUYS TODAY: a legacy Command can
		// only ever produce one command, so hoisting THIS check above the
		// normalization would behave identically and NO INPUT DISTINGUISHES THE TWO
		// POSITIONS. The position is correct rather than merely lucky the moment the
		// legacy form gains a second element. The ACCUMULATOR below is the half that
		// IS testable, because above the normalization a legacy task contributes 0.
		//
		// IT COMES BEFORE THE TOTAL so a task that is itself over the per-task cap
		// gets the specific, task-naming message rather than the job-level one. That
		// is the whole ordering argument; it says nothing about the retries and
		// timeout_seconds checks below, whose relative order is unchanged and
		// unimportant.
		if len(ts.Commands) > maxCommandsPerTask {
			return fmt.Errorf("task %s: at most %d commands are allowed, got %d",
				ts.Name, maxCommandsPerTask, len(ts.Commands))
		}
		// A nil TimeoutSeconds is SKIPPED, not defaulted: nil is the documented
```

(everything from `// A nil TimeoutSeconds is SKIPPED` onward is unchanged and stays exactly as it is.)

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/jobspec/ -v
go test ./... -timeout 180s
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jobspec/jobspec.go internal/jobspec/count_bounds_test.go
git commit -m "feat(jobspec): bound the per-task command count"
```

---

## Task 3: Bound the job-wide command total

**This is the constant that does the work.** The two per-axis caps multiply to 2,500,000, roughly 21x more than a 1 MiB body can express with the cheapest runnable entry - so with only those two, the binding constraint on total commands would remain `maxBodyBytes`, exactly as it was before any of the three existed. Delete `maxCommandsPerJob` and only this task's test goes red.

**Files:**
- Modify: `internal/jobspec/count_bounds_test.go` (add one helper and one test)
- Modify: `internal/jobspec/jobspec.go` (add `maxCommandsPerJob`; declare `totalCommands` beside `nameSet`; add the accumulator and its check directly under Task 2's check)

- [ ] **Step 1: Write the failing test**

Append to `internal/jobspec/count_bounds_test.go`:

```go
// totalSpec builds a spec of nTasks tasks, each carrying perTask commands and a
// unique name.
func totalSpec(nTasks, perTask int) *JobSpec {
	tasks := make([]TaskSpec, nTasks)
	for i := range tasks {
		tasks[i] = TaskSpec{Name: "c" + strconv.Itoa(i), Commands: argvN(perTask)}
	}
	return &JobSpec{Name: "counts", Tasks: tasks}
}

// TestValidate_TheJobWideCommandTotalIsBounded is the case NEITHER per-axis cap
// can catch, and it is the entire argument for the third constant. Every task in
// every case below is inside maxCommandsPerTask and every task count is inside
// maxTasksPerJob, so with only the two per-axis bounds in place all four cases
// are accepted and the third constant could be deleted with every other test in
// the tree still green.
//
// RED at HEAD on three of the four.
func TestValidate_TheJobWideCommandTotalIsBounded(t *testing.T) {
	const overMsg = "at most 25000 commands in total across all tasks are allowed"

	t.Run("one over the total, with every task inside its own bound", func(t *testing.T) {
		// 50 x 500 is exactly 25,000; the 51st task's single command crosses it.
		// 50 <= 5000 and 500 <= 500, so neither per-axis cap fires.
		spec := totalSpec(50, 500)
		spec.Tasks = append(spec.Tasks, TaskSpec{Name: "one-more", Commands: argvN(1)})
		require.EqualError(t, Validate(spec), overMsg,
			"NO 'got' CLAUSE, AND THAT IS A DECISION: the check fires the moment the budget is "+
				"exceeded and does not know the final total, so a running figure would be false and "+
				"would vary with task ordering for the same spec")
	})

	t.Run("exactly at the total is accepted", func(t *testing.T) {
		require.NoError(t, Validate(totalSpec(50, 500)),
			"25,000 exactly must still be accepted - the leg a >= breaks")
	})

	t.Run("a legacy command task counts toward the total", func(t *testing.T) {
		// THE DISCRIMINATING INPUT FOR THE ACCUMULATOR'S POSITION, and the only one
		// in the tree. The 51st task uses the legacy `command` spelling, so it
		// carries len(Commands) == 0 until normalizeTaskCommands rewrites it. An
		// accumulator placed ABOVE the normalization adds 0 for it, the total stops
		// at exactly 25,000, and the spec is accepted.
		//
		// The per-task check's position is NOT distinguishable by any input (a
		// legacy Command can only ever produce one command, and 0 > 500 and 1 > 500
		// are both false). This case covers the half that is.
		spec := totalSpec(50, 500)
		spec.Tasks = append(spec.Tasks, TaskSpec{Name: "legacy", Command: []string{"true"}})
		require.EqualError(t, Validate(spec), overMsg)
	})

	t.Run("the total is refused before traversal completes", func(t *testing.T) {
		// 70 tasks x 400 commands is 28,000, and the running total crosses 25,000 at
		// index 62. A DUPLICATE TASK NAME sits at index 65, three tasks past the
		// crossing.
		//
		// Checking inside the loop reports the total. Completing the pass and
		// checking afterwards reports the duplicate. This pins the "fail as soon as
		// it is exceeded" decision, which is what stops a 116,000-command spec being
		// walked in full before it is refused - and the message is the only
		// observable trace of "as soon as".
		spec := totalSpec(70, 400)
		spec.Tasks[65].Name = spec.Tasks[0].Name
		require.EqualError(t, Validate(spec), overMsg)
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/jobspec/ -run TestValidate_TheJobWideCommandTotalIsBounded -v
```

Expected: **3 FAIL, 1 PASS.**

```
--- FAIL: .../one_over_the_total,_with_every_task_inside_its_own_bound
    Error:  An error is expected but got nil.
--- FAIL: .../a_legacy_command_task_counts_toward_the_total
    Error:  An error is expected but got nil.
--- FAIL: .../the_total_is_refused_before_traversal_completes
    Error:  Not equal:
            expected: "at most 25000 commands in total across all tasks are allowed"
            actual  : "duplicate task name: c0"
--- PASS: .../exactly_at_the_total_is_accepted
```

- [ ] **Step 3: Write the implementation**

In `internal/jobspec/jobspec.go`, immediately after `const maxCommandsPerTask = 500`, add:

```go
// maxCommandsPerJob bounds the TOTAL number of commands across all of a job's
// tasks, accumulated during validation. IT IS THE ONE OF THE THREE THAT MOVES THE
// AGGREGATE NUMBER, and the other two do not produce it.
//
// TWO PER-AXIS CAPS WHOSE PRODUCT EXCEEDS WHAT THE BODY LIMIT ALREADY PERMITS
// REDUCE NOTHING IN AGGREGATE; they only change the shape of the worst case. The
// cost that matters - subprocess spawns, and one task_logs row per command from
// the agent's step marker alone, which nothing in this repo prunes - is
// total_commands x (1 + retries), and it does not care how the commands are
// distributed across tasks, because the retry budget is per task and every task's
// commands re-run on every attempt. maxTasksPerJob x maxCommandsPerTask is
// 2,500,000, roughly 21x more than a 1 MiB body can express with the cheapest
// RUNNABLE entry, so with only those two in place the binding constraint on the
// total would remain maxBodyBytes - exactly as it was before any of these three
// existed.
//
// WHY THE ENTRY MUST BE RUNNABLE, since the arithmetic above turns on it:
// internal/agent/runner.go emits its step marker once per command EXECUTED, at
// the top of the loop body and AFTER the empty-argv guard, and a command whose
// Start or Wait fails breaks the loop. So a body full of entries that cannot
// execute costs one failed exec and one marker, not one per entry. The cheapest
// entry that costs anything is `["true"],` at 9 bytes, which puts about 116,000
// of them in 1 MiB. This bound takes that to 25,000 and takes 1.28 million spawns
// to 275,000.
//
// THE PER-AXIS CAPS ARE NOT REDUNDANT ONCE THIS EXISTS. This one implies
// tasks <= 25000, since normalizeTaskCommands refuses a task with zero commands -
// which is weaker than maxTasksPerJob - and it says nothing about concentration,
// since 25,000 commands in one task satisfies it. Each of the three answers a
// different question: how long is the transaction and how big is the dispatcher's
// backlog; how much can one request pin to one worker slot; how much total work
// can one request buy.
//
// 25,000 IS PLACED TO KEEP THE LEGITIMATE SIDE CLEAR, not to make the adversarial
// number small. The legitimate high end is set by the many-tasks shape - thousands
// of tasks at a handful of commands each - rather than by the few-tasks shape,
// which tops out lower. The window between "what a real job needs" and "what 1
// MiB expresses" is only about 8x wide, because a legitimate command is a long
// string and an adversarial one is nine bytes, and a count bound cannot tell them
// apart.
//
// IT IS NOT A DoS CONTROL AND MUST NOT BE TIGHTENED AS IF IT WERE. POST /v1/jobs
// carries no rate limit - internal/api/server.go wraps only register and login in
// RateLimit - so every figure above is per-request and an authenticated caller may
// repeat it at whatever rate the network allows. The control for repetition is a
// rate limit. Tightening this to buy a constant factor against an attack that
// repetition makes unbounded anyway costs a refused real render, which has no
// workaround inside the product.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.spec rows, and it applies identically
// to every bound in this file.
const maxCommandsPerJob = 25000
```

Then, inside `Validate`, change:

```go
	nameSet := make(map[string]struct{}, len(spec.Tasks))
```

to:

```go
	nameSet := make(map[string]struct{}, len(spec.Tasks))
	totalCommands := 0
```

and insert, immediately after Task 2's `if len(ts.Commands) > maxCommandsPerTask { ... }` block and before the `// A nil TimeoutSeconds is SKIPPED` comment:

```go
		totalCommands += len(ts.Commands)
		// CHECKED INSIDE THE LOOP, not after it, so a spec far over the budget is
		// refused partway through traversal rather than after a full pass over
		// 116,000 entries.
		//
		// JOB-LEVEL MESSAGE, NO TASK PREFIX. The budget is a property of the job,
		// and naming whichever task the accumulator happened to cross on would read
		// as an accusation against a task that may be entirely ordinary - the same
		// spec with its tasks in a different order would name a different one.
		//
		// NO "got" CLAUSE, AND THAT IS A DECISION RATHER THAN AN OMISSION. The other
		// two count messages report the offending number because they know it. This
		// one fires the moment the budget is exceeded and therefore does not know
		// the final total; printing the running count as if it were the total would
		// be false, and "got at least N" is honest but varies with task ordering for
		// the same spec while telling the operator nothing they can act on, since
		// the actionable number is the limit. Completing the pass to report an exact
		// total would trade the early refusal for a nicer message; not taken.
		if totalCommands > maxCommandsPerJob {
			return fmt.Errorf("at most %d commands in total across all tasks are allowed", maxCommandsPerJob)
		}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/jobspec/ -v
go test ./... -timeout 180s
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jobspec/jobspec.go internal/jobspec/count_bounds_test.go
git commit -m "feat(jobspec): bound the job-wide command total"
```

---

## Task 4: Prove all three refusals reach the REST wire

**Why this lane, and why NOT the CLI lane.** Per `CLAUDE.md`'s routing rule, an assertion whose truth depends on what the SERVER puts on the wire - status code, `error` key, and the fact that a boundary value is stored and echoed rather than clamped - belongs in the integration lane. The backlog item's acceptance criterion demands "at least one real entry point", and `POST /v1/jobs` is the one every other consumer inherits.

**No CLI case is added, and that is a decision.** `internal/cli/jobs_spec_bounds_integration_test.go`'s `TestIntegration_SubmitSurfacesTheServersOutOfRangeRefusal` already proves the whole path - a `jobspec.Validate` message reaching a human through `relay submit`, as a typed `relayclient.ResponseError` with a 400 that `ErrorIsTransient` reads as permanent. A second message down that identical path adds a Docker-backed test and no new property. The count messages carry no new shape for the client to mishandle: `relayclient.sanitizeServerText` and the CLI's rendering are message-agnostic, and I checked that the job-level message's *absence* of a `task X:` prefix is not assumed anywhere on the read side.

**Why the at-the-bound acceptance for two of the three axes is NOT here.** `jobcreate.CreateJobFromSpec` inserts tasks one at a time, one round trip each, inside one transaction - so a 5000-task `201` would be 5000 sequential INSERTs against a container, which is the exact cost this bound exists to name. Those two acceptances are proven in `internal/jobspec/count_bounds_test.go` where they cost nothing. The one at-the-bound acceptance that is cheap - 500 commands on ONE task, one INSERT - is proven here, and is enough to show the wire does not clamp.

**Files:**
- Modify: `internal/api/jobs_spec_bounds_integration_test.go` (add `fmt` to the import block; append two helpers and one test; the existing test and `postJobSpec` stay byte-identical)

- [ ] **Step 1: Write the test**

Add `"fmt"` to the import block of `internal/api/jobs_spec_bounds_integration_test.go`, then append:

```go
// repeatCommands renders n JSON `["true"]` entries, comma-separated, with no
// trailing comma. Nine bytes each: the cheapest entry an agent will actually
// EXECUTE, which is the entry the bounds' own arithmetic is written against.
func repeatCommands(n int) string {
	return strings.TrimSuffix(strings.Repeat(`["true"],`, n), ",")
}

// repeatTasks renders n minimal one-command tasks with unique names, comma
// separated, with no trailing comma. About 31 bytes each, so 5001 of them is
// roughly 155 KB - comfortably inside maxBodyBytes, which matters: a 413 would
// make this test pass for the wrong reason.
func repeatTasks(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"name":"t%d","command":["true"]}`, i)
	}
	return b.String()
}

// TestCreateJob_TaskAndCommandCountBoundsAreEnforcedOnTheWire is what makes the
// three count bounds more than a library property: the real handler, the real
// router, the real BearerAuth middleware, the real 1 MiB body reader and the real
// response body.
//
// EVERY REFUSAL COMES FIRST and the acceptance last, so a distinctive input is
// never the last thing the test does - an offender placed at the end cannot
// detect an early-exit defect.
//
// EVERY REFUSAL MUST BE A 400, NOT A 413. All three bodies are well under
// maxBodyBytes on purpose; if one of them ever grows past 1 MiB this test starts
// passing because readJSON refused it, which is a different property entirely.
func TestCreateJob_TaskAndCommandCountBoundsAreEnforcedOnTheWire(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Counts", "counts@example.com", false)
	token := createTestToken(t, q, user.ID)

	code, body := postJobSpec(t, srv, token,
		`{"name":"too-many-tasks","tasks":[`+repeatTasks(5001)+`]}`)
	require.Equal(t, http.StatusBadRequest, code,
		"an over-count spec must be refused at submission, before 5001 sequential INSERTs are issued "+
			"inside one transaction")
	assert.Equal(t, "at most 5000 tasks are allowed, got 5001", body["error"],
		"the message must reach the wire verbatim, limit AND count: a caller who generated the spec "+
			"has to know by how much to chunk it")

	// The offending task is FIRST and a healthy task follows it, so the message
	// cannot be produced by "the last task lost".
	code, body = postJobSpec(t, srv, token,
		`{"name":"too-many-commands","tasks":[`+
			`{"name":"bad-task","commands":[`+repeatCommands(501)+`]},`+
			`{"name":"healthy-task","command":["echo","y"]}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "task bad-task: at most 500 commands are allowed, got 501", body["error"],
		"the per-task message must reach the wire verbatim: a caller with a fifty-task spec has to be "+
			"told WHICH task is wrong")

	// 50 tasks x 500 is exactly 25,000; one more command crosses it. Every task is
	// inside maxCommandsPerTask and the count is inside maxTasksPerJob, so this is
	// the refusal NEITHER per-axis bound can produce - the one that would be
	// missing if this slice had shipped two constants instead of three.
	var over strings.Builder
	over.WriteString(`{"name":"too-many-in-total","tasks":[`)
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&over, `{"name":"c%d","commands":[%s]},`, i, repeatCommands(500))
	}
	over.WriteString(`{"name":"one-more","commands":[["true"]]}]}`)
	code, body = postJobSpec(t, srv, token, over.String())
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "at most 25000 commands in total across all tasks are allowed", body["error"],
		"the job-level message carries NO task prefix on purpose: the budget is a property of the "+
			"job, and naming whichever task the accumulator crossed on would accuse an ordinary task")

	// POSITIVE CONTROL, at the exact per-task boundary, on the same server. Without
	// it a handler that had started refusing every spec would pass all three
	// assertions above. It is also the leg that goes red if maxCommandsPerTask
	// moves down by one.
	code, body = postJobSpec(t, srv, token,
		`{"name":"at-the-boundary","tasks":[{"name":"t","commands":[`+repeatCommands(500)+`]}]}`)
	require.Equal(t, http.StatusCreated, code, "a spec AT the boundary must still be created")

	tasks, ok := body["tasks"].([]any)
	require.True(t, ok, "the created job must carry its tasks")
	require.Len(t, tasks, 1)
	task, ok := tasks[0].(map[string]any)
	require.True(t, ok)
	cmds, ok := task["commands"].([]any)
	require.True(t, ok, "the created task must echo its commands")
	assert.Len(t, cmds, 500,
		"the boundary value must be STORED and echoed, not truncated: validation refuses, it never "+
			"edits, and a truncating validator would pass the 201 assertion above and still be wrong")
}
```

- [ ] **Step 2: Apply MUT-C1 and verify it applied**

In `internal/jobspec/jobspec.go`, comment out all three count checks added in Tasks 1-3 (the `if len(spec.Tasks) > maxTasksPerJob` block, the `if len(ts.Commands) > maxCommandsPerTask` block, and the `if totalCommands > maxCommandsPerJob` block - leave `totalCommands += ...` in place so the file still compiles without an unused-variable error, or comment that out too and comment out the `totalCommands := 0` declaration with it). This returns `Validate` to its HEAD behaviour on all three axes, which is what makes it a legitimate substitute for a pre-implementation RED. Then, from Git Bash at the worktree root:

```bash
git --no-pager diff --stat internal/jobspec/jobspec.go
git --no-pager diff -- internal/jobspec/jobspec.go
go build ./...
```

Expected: a non-empty stat line, a diff showing all three blocks commented out, and a clean build. **If the stat is empty the mutation did not apply and any result below is meaningless** - CRLF has silently broken four mutations in a row on this repo.

- [ ] **Step 3: Run the test to verify it fails**

Run (needs Docker):

```bash
go test -tags integration -p 1 ./internal/api/ -run TestCreateJob_TaskAndCommandCountBoundsAreEnforcedOnTheWire -v -timeout 600s
```

Expected: FAIL at the first assertion, with

```
Error:  Not equal:
        expected: 400
        actual  : 201
Messages: an over-count spec must be refused at submission, before 5001 sequential INSERTs ...
```

**The 201 will be slow** - it is 5001 sequential INSERTs, which is the cost the bound exists to prevent. That slowness is itself the finding; do not raise the timeout above 600s to make it comfortable.

- [ ] **Step 4: Restore, then run the test to verify it passes**

```bash
git checkout -- internal/jobspec/jobspec.go
git --no-pager diff --stat internal/jobspec/jobspec.go   # MUST be empty
go test -tags integration -p 1 ./internal/api/ -run 'TestCreateJob_(TaskAndCommandCount|RetriesAndTimeout)BoundsAreEnforcedOnTheWire' -v -timeout 600s
```

Expected: both PASS - the pre-existing retries/timeout test is run alongside to confirm the shared `postJobSpec` helper and the import edit broke nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/api/jobs_spec_bounds_integration_test.go
git commit -m "test(api): POST /v1/jobs refuses over-count task and command specs"
```

---

## Task 5: Cover the retroactivity paths

**`jobspec.Validate` is retroactive over stored `scheduled_jobs.spec` rows: a stored spec over any of the three new bounds stops firing on the deploy that carries them.** The spec enumerates five sites that re-validate stored data. This task covers the three that have no count-axis coverage, in the cheapest honest lane for each, and leaves the two that already have message-agnostic proofs alone.

| Site | Symbol | Covered how |
|---|---|---|
| 1 | `schedrunner.fireOne` (direct `jobspec.Validate`, hoisted by PR #159) | **Already proven**, message-agnostically, by `TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled`. Not touched (see F4). Its `makeOverBudgetSpecJSON` header is corrected in Task 6. |
| 2 | `api.handleRunScheduledJobNow` (direct `ValidateJobSpec`) | One new leg on the existing `TestRunScheduledJobNow_AStoredOverBoundSpecIsRefusedAsPermanent` - same server, same user, same token, no new container. |
| 3 | `schedrunner.ValidateStoredSpecsOnStartup` -> `ValidateStoredSchedule` | New **untagged** test, plain lane. |
| 4 | `api.handlePatchScheduledJob`'s clear-decision -> `ValidateStoredSchedule` | Same untagged test - both sites funnel through the one exported symbol. |
| 5 | `jobcreate.CreateJobFromSpec` | New untagged test beside the existing one, with a nil `*store.Queries`. |

**Files:**
- Create: `internal/schedrunner/stored_spec_count_bounds_test.go`
- Modify: `internal/jobcreate/jobcreate_validate_test.go` (append one test; add `fmt` to the imports)
- Modify: `internal/api/scheduled_jobs_run_now_bounds_integration_test.go` (append one leg to the existing bounds test)

- [ ] **Step 1: Write the untagged `ValidateStoredSchedule` test**

Create `internal/schedrunner/stored_spec_count_bounds_test.go`:

```go
package schedrunner_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"relay/internal/schedrunner"

	"github.com/stretchr/testify/require"
)

// storedSpecJSON marshals a job spec of nTasks tasks with perTask commands each,
// the way a release before these bounds would have stored it in
// scheduled_jobs.job_spec.
func storedSpecJSON(t *testing.T, nTasks, perTask int) []byte {
	t.Helper()
	tasks := make([]map[string]any, nTasks)
	for i := range tasks {
		cmds := make([][]string, perTask)
		for j := range cmds {
			cmds[j] = []string{"true"}
		}
		tasks[i] = map[string]any{"name": "t" + strconv.Itoa(i), "commands": cmds}
	}
	b, err := json.Marshal(map[string]any{"name": "legacy", "tasks": tasks})
	require.NoError(t, err)
	return b
}

// TestValidateStoredSchedule_RefusesAStoredSpecOverACountBound covers the two
// STORED-SPEC paths that reach jobspec.Validate through this one exported symbol:
// schedrunner.ValidateStoredSpecsOnStartup (the boot sweep, which writes the
// returned message into scheduled_jobs.last_error over every ENABLED schedule)
// and internal/api's handlePatchScheduledJob clear-decision (which clears the
// failure record if and only if this returns nil on the EFFECTIVE row).
//
// IT IS UNTAGGED ON PURPOSE. ValidateStoredSchedule is a pure function of three
// stored values, so the retroactivity fact - a spec an older release accepted
// stops validating - needs no Postgres. Putting it behind the integration tag
// would put the only count-axis coverage of two call sites in the lane
// .github/workflows/go-ci.yml never runs.
//
// WHAT IT DOES NOT PROVE, stated so nobody reads more into it: that either caller
// is wired to this function. That is proven message-agnostically by
// TestValidateStoredSpecsOnStartup and by the PATCH clear-decision tests in
// internal/api, both of which use the retries message and neither of which cares
// which rule produced it.
func TestValidateStoredSchedule_RefusesAStoredSpecOverACountBound(t *testing.T) {
	cases := []struct {
		name string
		spec []byte
		want string
	}{
		{"over the task count", storedSpecJSON(t, 5001, 1),
			"at most 5000 tasks are allowed, got 5001"},
		{"over the per-task command count", storedSpecJSON(t, 1, 501),
			"task t0: at most 500 commands are allowed, got 501"},
		{"over the job-wide command total", storedSpecJSON(t, 51, 500),
			"at most 25000 commands in total across all tasks are allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.EqualError(t,
				schedrunner.ValidateStoredSchedule(tc.spec, "@hourly", "UTC"), tc.want,
				"a stored spec over a count bound must be refused with the bound's OWN message: it is "+
					"what the boot sweep writes into last_error and what run-now answers with, and a "+
					"generic string is what makes a silently-stopped schedule unexplainable")
		})
	}

	// CONTROL, on the same function and after the three refusals: a stored spec at
	// the per-task and job-total bounds exactly must still validate. Without it a
	// ValidateStoredSchedule that had started refusing everything - a broken cron
	// parser, a changed permanent() vocabulary - would pass all three cases above.
	require.NoError(t,
		schedrunner.ValidateStoredSchedule(storedSpecJSON(t, 50, 500), "@hourly", "UTC"),
		"control: 50 tasks x 500 commands is exactly at maxCommandsPerTask and exactly at "+
			"maxCommandsPerJob, and must still be fireable")
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/schedrunner/ -run TestValidateStoredSchedule_RefusesAStoredSpecOverACountBound -v
```

Expected at the tree as of Task 3: **PASS.** This test is green on arrival, because the constants already landed in Tasks 1-3. Its RED is established by MUT-C1 in Step 3, exactly as the predecessor slice established Task 5's guard.

- [ ] **Step 3: Apply MUT-C1, verify it applied, and observe the RED**

Comment out the three count checks in `internal/jobspec/jobspec.go` as in Task 4 Step 2. Then:

```bash
git --no-pager diff --stat internal/jobspec/jobspec.go   # MUST be non-empty
go test ./internal/schedrunner/ -run TestValidateStoredSchedule_RefusesAStoredSpecOverACountBound -v
```

Expected: **all three subtests FAIL** with `An error is expected but got nil.`, and the control still passes. Then restore:

```bash
git checkout -- internal/jobspec/jobspec.go
git --no-pager diff --stat internal/jobspec/jobspec.go   # MUST be empty
```

- [ ] **Step 4: Write the `jobcreate` test**

Add `"fmt"` to the import block of `internal/jobcreate/jobcreate_validate_test.go` and append:

```go
// TestCreateJobFromSpec_RefusesAnOverCountSpecBeforeTouchingTheDatabase is the
// count-axis sibling of the test above and exists for the same reason: this call
// is the entire enforcement for any caller that does not validate first.
// schedrunner.fireOne is such a caller for its SECOND reach at Validate, and
// CreateJobFromSpec is retroactivity site 5 - the one whose every error collapses
// into "create job: %w", which is exactly why fireOne and handleRunScheduledJobNow
// both validate ahead of it.
//
// THE SUBJECT IS THE JOB-WIDE TOTAL rather than either per-axis bound, because it
// is the bound with no per-row analogue anywhere: tasks.retries and
// tasks.timeout_seconds are columns a CHECK constraint could in principle guard,
// and a per-JOB command total is not expressible as a row CHECK at all. If any
// bound is going to be enforced only here, it is this one.
//
// THE NIL *store.Queries IS THE POINT, NOT A SHORTCUT. Validate runs before any
// field of q is read, so a correct implementation never dereferences it. Delete
// the Validate call and the very next statements reach q.CreateJob on a nil
// receiver, which panics - so the mutant fails LOUDLY here instead of passing
// silently. The recover contains it so the mutant does not take down every other
// test in this package's binary; the property is established by the three
// assertions, not by the panic.
func TestCreateJobFromSpec_RefusesAnOverCountSpecBeforeTouchingTheDatabase(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CreateJobFromSpec panicked instead of refusing the spec: %v\n"+
				"q is a nil *store.Queries, so this is what reaching a query on it looks like: "+
				"jobspec.Validate is no longer running before the first database call, which is the "+
				"entire enforcement of the count bounds for a caller that does not validate first.", r)
		}
	}()

	// 51 tasks x 500 commands is 25,500. Every task is inside maxCommandsPerTask
	// and the count is inside maxTasksPerJob, so only the job-wide total refuses
	// this.
	tasks := make([]jobspec.TaskSpec, 51)
	for i := range tasks {
		cmds := make([][]string, 500)
		for j := range cmds {
			cmds[j] = []string{"true"}
		}
		tasks[i] = jobspec.TaskSpec{Name: fmt.Sprintf("t%d", i), Commands: cmds}
	}

	job, created, err := jobcreate.CreateJobFromSpec(
		context.Background(), nil, jobspec.JobSpec{Name: "over-count", Tasks: tasks},
		pgtype.UUID{}, pgtype.UUID{})

	require.EqualError(t, err, "at most 25000 commands in total across all tasks are allowed",
		"CreateJobFromSpec must refuse an over-count spec itself: no CHECK constraint can express a "+
			"per-job total, so this call is the entire enforcement for any caller that does not "+
			"validate first - schedrunner.fireOne is one such caller")
	require.Empty(t, created, "a refused spec must insert no tasks")
	require.False(t, job.ID.Valid, "a refused spec must insert no job")
}
```

- [ ] **Step 5: Run it, prove its RED with MUT-C1, and restore**

```bash
go test ./internal/jobcreate/ -v
```

Expected: PASS (green on arrival). Then apply MUT-C1, verify the stat is non-empty, and re-run:

```bash
go test ./internal/jobcreate/ -run TestCreateJobFromSpec_RefusesAnOverCountSpec -v
```

Expected: FAIL with `An error is expected but got nil.` (**not** the panic message - with MUT-C1 the two `retries`/`timeout` checks are also commented out but `Validate` still runs and returns nil, so `CreateJobFromSpec` proceeds to `q.CreateJob` on a nil receiver and the recover fires). Either failure mode is a legitimate RED; record which you saw. Then:

```bash
git checkout -- internal/jobspec/jobspec.go
git --no-pager diff --stat internal/jobspec/jobspec.go   # MUST be empty
go test ./internal/jobcreate/ -v
```

Expected: PASS.

- [ ] **Step 6: Add the run-now leg**

In `internal/api/scheduled_jobs_run_now_bounds_integration_test.go`, inside `TestRunScheduledJobNow_AStoredOverBoundSpecIsRefusedAsPermanent`, **immediately before** the `// POSITIVE CONTROL on the same server` block (so the refusals stay ahead of the acceptance), insert:

```go
	// THE COUNT AXIS, on the same server. A stored spec whose per-task command
	// count an older release accepted is refused by the same 400 and the same
	// verbatim message, and this is the leg that proves the count bounds inherit
	// run-now's permanence rather than merely POST /v1/jobs'.
	//
	// 501 entries at nine bytes is about 4.6 KB of stored JSONB - deliberately the
	// cheapest of the three count axes to express, since the property under test is
	// the status code and the message, not the size of the row.
	var cmds strings.Builder
	for i := 0; i < 501; i++ {
		if i > 0 {
			cmds.WriteString(",")
		}
		cmds.WriteString(`["true"]`)
	}
	overCount := `{"name":"legacy","tasks":[` +
		`{"name":"healthy-task","command":["echo","y"]},` +
		`{"name":"bad-task","commands":[` + cmds.String() + `]}]}`
	countSched, err := q.CreateScheduledJob(context.Background(), store.CreateScheduledJobParams{
		Name: "legacy-commands", OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: []byte(overCount), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	code, body = postRunNow(t, srv, token, uuidString(countSched.ID))
	require.Equal(t, http.StatusBadRequest, code,
		"a stored spec over a COUNT bound is as permanent as one over a value bound: "+
			"relayclient.ErrorIsTransient reads 5xx as transient and a polling caller would never stop")
	assert.Equal(t, "task bad-task: at most 500 commands are allowed, got 501", body["error"],
		"the per-task message must reach the operator verbatim - run-now is how they turn a schedule "+
			"that has gone silent into a specific reason")
```

Add `"strings"` to that file's import block.

- [ ] **Step 7: Prove the leg's RED, restore, and run the file green**

Apply MUT-C1, verify the stat is non-empty, then:

```bash
go test -tags integration -p 1 ./internal/api/ -run TestRunScheduledJobNow_AStoredOverBoundSpecIsRefusedAsPermanent -v -timeout 300s
```

Expected: FAIL on the new leg with `expected: 400 / actual: 201` (the retries leg above it still passes, because MUT-C1 leaves `maxRetries` alone). Then:

```bash
git checkout -- internal/jobspec/jobspec.go
git --no-pager diff --stat internal/jobspec/jobspec.go   # MUST be empty
go test -tags integration -p 1 ./internal/api/ -run TestRunScheduledJobNow -v -timeout 300s
go test ./... -timeout 180s
```

Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/schedrunner/stored_spec_count_bounds_test.go \
        internal/jobcreate/jobcreate_validate_test.go \
        internal/api/scheduled_jobs_run_now_bounds_integration_test.go
git commit -m "test: cover the count bounds on every stored-spec re-validation path"
```

---

## Task 6: Correct every comment the change falsified, and two PR #159 left behind

**This is not commentary.** Wrong prose about correct code is this project's most-recurring defect class, eight iterations running. Each sentence below is given with its exact current wording so it can be found by search rather than by line number.

**Files:**
- Modify: `internal/jobspec/jobspec.go` (the `maxRetries` doc comment's third paragraph)
- Modify: `internal/api/jobs.go` (`uuidStrHead`'s doc comment)
- Modify: `internal/api/scheduled_jobs.go` (`handleRunScheduledJobNow`'s comment)
- Modify: `internal/schedrunner/stored_spec_bounds_test.go` (`makeOverBudgetSpecJSON`'s header)
- Modify: `internal/api/scheduled_jobs_run_now_bounds_integration_test.go` (header and one assertion message)
- Modify: `web/src/jobs/specTemplate.ts` (`validateSpecText`'s doc comment)

- [ ] **Step 1: Correct the `maxRetries` env paragraph**

In `internal/jobspec/jobspec.go`, find the third paragraph of `maxRetries`'s doc comment, which currently begins `// DO NOT MAKE THIS ENV-CONFIGURABLE. Validate runs on STORED scheduled-job specs` and ends `// exactly as the priority set is.` Replace **that whole paragraph** with:

```go
// DO NOT MAKE THIS ENV-CONFIGURABLE, AND THE SAME GOES FOR EVERY BOUND BELOW.
// Validate runs on STORED scheduled_jobs.spec rows on five paths, and the older
// version of this paragraph named two of them and got one of those wrong:
//   - schedrunner.fireOne calls Validate DIRECTLY, hoisted above the overlap
//     check, and then reaches it again inside jobcreate.CreateJobFromSpec. (It
//     used to reach it only through CreateJobFromSpec; that changed and this
//     comment did not.) Its failure branch records the message in last_error.
//   - api.handleRunScheduledJobNow calls it DIRECTLY, ahead of the transaction,
//     and then again inside CreateJobFromSpec. That direct call is the site that
//     decides the status code: it answers a stored spec's failure with 400 and
//     the validator's own message, where CreateJobFromSpec's error collapses into
//     a 500 that relayclient.ErrorIsTransient reads as retryable.
//   - schedrunner.ValidateStoredSpecsOnStartup, at boot, over every ENABLED
//     schedule, through schedrunner.ValidateStoredSchedule.
//   - api.handlePatchScheduledJob's clear-decision, through the same
//     ValidateStoredSchedule, on the EFFECTIVE row.
//   - jobcreate.CreateJobFromSpec itself, reached from the first two with stored
//     data.
//
// An env-tunable bound would therefore make retroactive schedule invalidation
// environment-dependent: the same stored spec fires on one replica's
// configuration and silently stops on another's, and lowering the knob would
// disable schedules with no signal anywhere. Two of the five sites make that
// worse in ways that postdate the original argument. The startup sweep WRITES the
// returned message into last_error, so the recorded failure text would become a
// function of which replica happened to boot, and the number in a stored,
// operator-facing string would stop matching the binary that reads it. And the
// PATCH clear-decision clears the record if and only if the effective row
// validates, so a PATCH served by a lenient replica would clear a record a strict
// replica immediately re-writes, and the operator would watch the failure
// flicker.
//
// A validation vocabulary shared by every ingest path is a property of the
// binary, exactly as the priority set is. THE PATHS ARE ENUMERATED, NOT COUNTED,
// and deliberately so - see internal/schedrunner/failure.go, which settled this
// question: a number goes stale silently and has no maintainer, where an
// enumeration goes stale loudly because a reader can check it. (The older version
// of this sentence said "four ingest paths"; there are api.handleCreateJob,
// api.handleCreateScheduledJob, api.handlePatchScheduledJob when the request body
// carries a job_spec, mcp.submit, and mcp.schedules_write on both create and
// update. The CLI, the SPA and the Python SDK post JSON and hold no parallel
// validation, so they inherit through the API.)
```

- [ ] **Step 2: Correct `uuidStrHead`'s falsified sentence**

In `internal/api/jobs.go`, find:

```go
// Bounded on purpose, as INSURANCE rather than as a fix for a live flood. Task
// count is bounded only against zero (jobspec.Validate) and log.Printf holds a
// global mutex, so an unbounded rendering would let one line hold that mutex for
// hundreds of kilobytes - and both call sites report a condition the code itself
// argues should not happen, which is exactly the kind of line nobody watches.
```

Replace with:

```go
// Bounded on purpose, as INSURANCE rather than as a fix for a live flood. Task
// count is now bounded at BOTH ends by jobspec.Validate - at least one, at most
// maxTasksPerJob - and the upper bound does not retire this cap: 5000 ids is
// still on the order of 185 KB, log.Printf holds a global mutex, and an unbounded
// rendering would let one line hold that mutex for all of it. Both call sites
// report a condition the code itself argues should not happen, which is exactly
// the kind of line nobody watches.
```

- [ ] **Step 3: Correct the two sentences PR #159 falsified**

**(a) `internal/api/scheduled_jobs.go`**, in `handleRunScheduledJobNow`. Find:

```go
	// run-now is the ONLY interactive path that can explain why a schedule
	// stopped producing jobs: schedrunner's fireOne logs one server-side line
	// and advances next_run_at, leaving nothing user-visible behind.
```

Replace with:

```go
	// run-now is the interactive path for this question, and since last_error
	// landed (migration 000022) it is no longer the only surface that carries the
	// answer: fireOne's failure branch records the same message on the row, and
	// GET /v1/scheduled-jobs and its list sibling both serve it. What run-now still
	// has that the recorded value does not is the UNTRUNCATED message and an
	// answer on demand rather than at the next scheduled fire - which is why it
	// stays the documented first step when a schedule reports a failure.
```

**(b) `internal/api/scheduled_jobs_run_now_bounds_integration_test.go`**, in the header of `TestRunScheduledJobNow_AStoredOverBoundSpecIsRefusedAsPermanent`. Find:

```go
// The bounds are retroactive: jobspec.Validate runs on the STORED spec every
// time a schedule fires, so a row written by an older release can now fail
// validation. schedrunner's own path logs one server-side line and advances
// next_run_at, which is the invisibility
// TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard
// pins. run-now is the operator's way to ask the same question and get the
// answer back, and it is reachable from `relay schedules run-now`, the SPA and
// MCP.
```

Replace with:

```go
// The bounds are retroactive: jobspec.Validate runs on the STORED spec every time
// a schedule fires, so a row written by an older release can now fail validation.
// schedrunner's own path records the same message on the row (last_error /
// last_error_at, pinned by
// TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled), so the
// failure is discoverable without suspecting a schedule first. run-now is the way
// to ask the same question ON DEMAND and get the UNTRUNCATED answer back, rather
// than waiting for the next scheduled fire, and it is reachable from
// `relay schedules run-now`, the SPA and MCP.
```

And in the same test's first `assert.Equal` message, find `"...run-now is the ONLY interactive path "+` and change that fragment to `"...run-now is the interactive path "+`. The rest of the message is unchanged.

- [ ] **Step 4: Correct `makeOverBudgetSpecJSON`'s header**

In `internal/schedrunner/stored_spec_bounds_test.go`, find:

```go
// makeOverBudgetSpecJSON is a spec that was VALID when it was stored and is not
// any more. retries: 50 was accepted by every relay release before the
// retry-bounds change; jobspec.Validate now refuses it, and schedrunner
// re-validates the stored spec on EVERY fire because fireOne calls
// jobcreate.CreateJobFromSpec, which calls jobspec.Validate.
```

Replace with:

```go
// makeOverBudgetSpecJSON is a spec that was VALID when it was stored and is not
// any more. retries: 50 was accepted by every relay release before the
// retry-bounds change; jobspec.Validate now refuses it, and schedrunner
// re-validates the stored spec on EVERY fire because fireOne calls
// jobspec.Validate DIRECTLY, hoisted above the overlap check, and then reaches it
// a second time inside jobcreate.CreateJobFromSpec. (This comment used to name
// only the second of those, from before the hoist.)
```

- [ ] **Step 5: Correct the frontend pre-check's comment**

**No behaviour change, no build, no `web/dist`, no npm command.** In `web/src/jobs/specTemplate.ts`, find:

```ts
// Minimal client-side pre-check. Deliberately shallow: valid JSON, a non-empty
// string `name`, and a non-empty `tasks` array. Deeper rules (unique task names,
// command xor commands, dependency cycles, priority enum, source) are left to
// the server (jobspec.Validate) so the two paths cannot drift.
```

Replace with:

```ts
// Minimal client-side pre-check. Deliberately shallow: valid JSON, a non-empty
// string `name`, and a non-empty `tasks` array. Deeper rules (unique task names,
// command xor commands, dependency cycles, priority enum, source, and the upper
// bounds on the task and command counts) are left to the server
// (jobspec.Validate) so the two paths cannot drift.
//
// THE COUNT BOUNDS IN PARTICULAR MUST NOT BE MIRRORED HERE. This function already
// duplicates the LOWER end of the task-count range, and that one is kept because
// it is a typo check on an empty editor rather than a policy. The upper ends are
// policy: a number written here makes the SPA refuse a spec the server would
// accept, or accept one it would refuse, on the first release that moves either -
// and the server is the validator of record.
```

- [ ] **Step 6: Verify no falsified wording survives and nothing compiled differently**

From Git Bash at the worktree root:

```bash
rg -n "ONLY interactive path|nothing user-visible behind|bounded only against zero|four ingest paths|StopsFiringInvisibly" \
   --glob '!docs/**' --glob '!ROADMAP.md' .
```

Expected: **zero hits.** (`docs/` holds historical specs, plans and retros that are entitled to describe the tree as it was; `ROADMAP.md` is the conductor's file and its refresh entries are a dated log, not a live claim.)

Then:

```bash
go build ./...
go vet ./...
go vet -tags integration ./...
go test ./... -timeout 180s
git status --short
```

Expected: all clean, and `git status --short` lists exactly the six files this task modifies and nothing under `web/dist/`.

- [ ] **Step 7: Check line endings before committing**

This step edited six tracked text files programmatically. Per `CLAUDE.md`, `git diff` alone cannot tell you whether CRLF churn happened:

```bash
git --no-pager diff --stat
git ls-files --eol internal/jobspec/jobspec.go internal/api/jobs.go internal/api/scheduled_jobs.go \
  internal/schedrunner/stored_spec_bounds_test.go \
  internal/api/scheduled_jobs_run_now_bounds_integration_test.go web/src/jobs/specTemplate.ts
```

Expected: a diffstat proportional to the comment edits above (tens of lines, not hundreds), and **every path reading `i/lf`**. If a diffstat is wildly larger than the change you intended, or a path is not `i/lf`, revert that file and redo the edit rather than committing.

- [ ] **Step 8: Commit**

```bash
git add internal/jobspec/jobspec.go internal/api/jobs.go internal/api/scheduled_jobs.go \
        internal/schedrunner/stored_spec_bounds_test.go \
        internal/api/scheduled_jobs_run_now_bounds_integration_test.go \
        web/src/jobs/specTemplate.ts
git commit -m "docs: correct the comments the count bounds falsified, plus two PR #159 left stale"
```

---

## Task 7: Document the three bounds in README

**Two rows must be ADDED, not edited.** There is no `tasks` row and no `tasks[].commands` row in README's job-spec field table today - only the legacy singular `tasks[].command`, which the table also marks `Yes` even though a spec using `commands` is perfectly valid. Three existing rows and two paragraphs are rewritten.

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add the `tasks` row**

In the job-spec field table (header `| Field | Required | Description |`, first data row `| \`name\` | Yes | Human-readable job name |`), insert immediately after the `labels` row and before `tasks[].name`:

```
| `tasks` | Yes | The job's task list. At least one, at most `5000`; a longer list is rejected at submission, and re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs), because that makes the bound retroactive over schedules created by earlier releases. The cap stands in for two costs it is cheaper to bound than to fix: task rows are inserted one at a time, one round trip each, inside a single transaction, and the dispatcher re-reads the whole pending backlog on every tick until it drains. |
```

- [ ] **Step 2: Correct the `tasks[].command` row and add `tasks[].commands`**

Replace:

```
| `tasks[].command` | Yes | Executable and arguments as an array |
```

with these two rows:

```
| `tasks[].command` | Yes (or `commands`) | Legacy single-command spelling: one executable and its arguments as an array. Normalized into a one-element `commands` on ingest. Set either this or `commands`, never both. |
| `tasks[].commands` | Yes (or `command`) | Several commands the agent runs SEQUENTIALLY in the same prepared workspace and environment, as an array of argv arrays. At most `500` per task, and at most `25000` across all of a job's tasks; both are rejected at submission and re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs). Past a few hundred, one task per unit is the better model anyway: separate tasks parallelize across the fleet, retry independently, and report per-unit status. Tasks sharing a `source` reuse the same workspace, so splitting does not cost the workspace sharing that motivates `commands`. |
```

- [ ] **Step 3: Rewrite the `tasks[].retries` row**

It currently contains the sentence *"The cap bounds this one per-task multiplier and nothing else: a job's `tasks` and a task's `commands` are themselves unlimited, so this is not a bound on how much work one request can ask for."* **This slice makes that false.** Replace the whole row with:

```
| `tasks[].retries` | No | Retry up to this many times on failure (default `0`, max `10`). A larger or negative value is rejected at submission, and re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs), because that makes the bound retroactive over schedules created by earlier releases. It bounds one per-task multiplier, and the counts it multiplies are bounded separately: `tasks` max `5000`, `tasks[].commands` max `500` per task and `25000` per job. **All of these bound ONE request and none of them bounds repetition** - `POST /v1/jobs` carries no rate limit, so the totals above are per-request figures an authenticated caller may repeat. There is no backoff between a failed task and its redispatch, so a deterministically-failing command burns the whole budget in seconds; for a contended resource use a reservation rather than a large retry count. |
```

- [ ] **Step 4: Update the retroactivity paragraph in the Scheduled jobs section**

Find the sentence beginning *"`tasks[].retries` (max `10`) and `tasks[].timeout_seconds` (max `604800`) are the newest rules with this property"* and replace that clause with:

```
`tasks` (max `5000`), `tasks[].commands` (max `500` per task and `25000` per job), `tasks[].retries` (max `10`) and `tasks[].timeout_seconds` (max `604800`) are the newest rules with this property
```

The rest of that paragraph - the `"retries": 50` example and the `source`/`priority` precedents - is unchanged and stays correct.

- [ ] **Step 5: Extend the "already in the database" paragraph**

Find the paragraph beginning *"**Tasks already in the database are deliberately left alone.**"* and append one sentence to it:

```
The same goes for the count bounds: no migration deletes tasks or commands from a job that already exceeds them, and a job created before they existed with 20,000 tasks keeps every one.
```

- [ ] **Step 6: Verify the rendered table and the contract it now states**

```bash
rg -n '^\| `tasks(\[\]\.(command|commands|retries|timeout_seconds))?` \|' README.md
rg -n 'themselves unlimited' README.md
```

Expected: **five hits** from the first command (`tasks`, `tasks[].command`, `tasks[].commands`, `tasks[].retries`, `tasks[].timeout_seconds`), each a single table row with three `|`-delimited cells - a stray newline inside a cell breaks the table silently. **Zero hits** from the second.

Then read all five rows back against `internal/jobspec/jobspec.go`: every number, every "rejected at submission" claim, and the "at least one, at most 5000" range must be true of the code as written. **A wrong contract in docs is a defect in this project's terms** - consumers implement against this prose and no test covers it.

- [ ] **Step 7: Check line endings, then commit**

```bash
git --no-pager diff --stat README.md
git ls-files --eol README.md
```

Expected: a diffstat on the order of a dozen changed lines, and `i/lf`. A three-figure insertion count means the file was reclassified as binary by a CRLF accident - revert and redo. Then:

```bash
git add README.md
git commit -m "docs: state the task and command count bounds in the job-spec table"
```

---

## Task 8: Mutation battery and gates

**Files:** none modified permanently. Every mutation is applied, measured, and reverted with `git checkout --`.

**The rules for every row.** Apply the mutation; run `git --no-pager diff --stat internal/jobspec/jobspec.go` and confirm it is **non-empty**; run the named test; record killed/survived; `git checkout -- internal/jobspec/jobspec.go`; confirm the stat is now empty. A mutation that silently fails to apply reports as "survived", and CRLF has broken four in a row on this repo, which is why row 0 exists.

- [ ] **Step 1: Run the battery**

Unless noted, the command is:

```bash
go test ./internal/jobspec/ -v
```

| # | Mutation | Must kill | Notes |
|---|---|---|---|
| 0 | **CONTROL, RUN FIRST.** `maxTasksPerJob = 5000` -> `= 1` | `TestValidate_TheTaskCountIsBoundedAtBothEnds/exactly_at_the_cap_is_accepted` | If nothing dies, the harness is broken - stop and fix that before reading any row below |
| 1 | `maxTasksPerJob` 5000 -> 5001 | `.../one_over_the_cap_is_rejected_and_the_message_reports_the_count` | Raising a cap breaks a REJECTION case. Do not invert this. |
| 2 | `maxTasksPerJob` 5000 -> 4999 | `.../exactly_at_the_cap_is_accepted` | Lowering a cap breaks an ACCEPTANCE case |
| 3 | `len(spec.Tasks) > maxTasksPerJob` -> `>=` | `.../exactly_at_the_cap_is_accepted` | The off-by-one the at-the-bound case exists for |
| 4 | Move the task-count check below the per-task loop | `.../the_count_is_refused_before_the_per-task_loop_runs` | Reports `duplicate task name: t0` instead |
| 5 | `maxCommandsPerTask` 500 -> 501 | `TestValidate_ThePerTaskCommandCountIsBounded/one_over_the_cap,_offender_FIRST` **and** `/one_over_the_cap,_offender_SECOND` | |
| 6 | `maxCommandsPerTask` 500 -> 499 | `.../exactly_at_the_cap_is_accepted` | And Task 4's 201 leg, and its `assert.Len(cmds, 500)` |
| 7 | `len(ts.Commands) > maxCommandsPerTask` -> `>=` | `.../exactly_at_the_cap_is_accepted` | |
| 8 | Guard the per-task check with `if i == 0 && ...` | `.../one_over_the_cap,_offender_SECOND` | The mutant that SURVIVED `jobspec_bounds_test.go`'s entire file before its index-1 cases existed. Confirm the offender-FIRST case still passes, or the mutation did something else. |
| 9 | Drop `task %s: ` from the per-task message (and its arg) | Both `one_over_the_cap` cases, and Task 4's wire assertion | Pins the per-task naming, which a `require.Error`-only test would miss |
| 10 | `maxCommandsPerJob` 25000 -> 25001 | `TestValidate_TheJobWideCommandTotalIsBounded/one_over_the_total,_with_every_task_inside_its_own_bound` and `/a_legacy_command_task_counts_toward_the_total` | |
| 11 | `maxCommandsPerJob` 25000 -> 24999 | `.../exactly_at_the_total_is_accepted` | |
| 12 | `totalCommands > maxCommandsPerJob` -> `>=` | `.../exactly_at_the_total_is_accepted` | |
| 13 | **Delete the accumulator increment** `totalCommands += len(ts.Commands)` (replace with `_ = ts`) | All three rejection subtests of `TestValidate_TheJobWideCommandTotalIsBounded` | The mutant that makes the third constant decorative |
| 14 | **Move the accumulator ABOVE `normalizeTaskCommands(ts)`** (with the check) | `.../a_legacy_command_task_counts_toward_the_total` ONLY | Confirm `one_over_the_total` still passes - if it also dies, the mutation moved more than the accumulator |
| 15 | **Move the total check out of the loop**, to just after it | `.../the_total_is_refused_before_traversal_completes` | Reports `duplicate task name: c0` instead |
| 16 | **Transpose** the per-task check and the accumulator+total check | `TestValidate_ThePerTaskCommandCountIsBounded/the_per-task_bound_wins_over_the_job-wide_total` | Returns the job-level message for a single 25,001-command task |
| 17 | Delete the whole `maxCommandsPerJob` block (constant, declaration, accumulator, check) | `TestValidate_TheJobWideCommandTotalIsBounded` (3 subtests) and Task 4's total leg | **The proof that the third constant is not redundant.** Every other test in the tree must stay GREEN under this mutation - if anything else dies, one of the other two bounds is being tested through this one. |
| 18 | **NOT A MUTATION - RECORD AS "SURVIVES BY CONSTRUCTION".** Move the per-task command check above `normalizeTaskCommands`, leaving the accumulator where it is | nothing | A legacy `Command` can only produce one command, so `0 > 500` and `1 > 500` are both false and no input distinguishes the positions. Documented in the code comment; do not invent a test that pretends to kill it. See the plan's F1. |

Rows 6, 9, 10 and 17 additionally have an integration-lane consequence. Confirm those separately with:

```bash
go test -tags integration -p 1 ./internal/api/ -run TestCreateJob_TaskAndCommandCountBoundsAreEnforcedOnTheWire -v -timeout 600s
```

- [ ] **Step 2: Confirm the tree is clean**

```bash
git status --short
git --no-pager diff --stat
```

Expected: **both empty.** If anything is listed, a mutation was not reverted. Do not proceed until it is.

- [ ] **Step 3: Run the gates**

```bash
make test
make test-integration
make test-cli-integration
go vet -tags integration ./...
```

Then the race lane, through the Linux container (the native Windows lane is unreliable and, separately, silently skips every `//go:build !windows` file):

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

Expected: all green. **If `-race` does not run, say so plainly in the PR body rather than substituting `-count=N`** - repetition raises confidence in flakiness, not in race-freedom, and this slice adds no concurrency at all, so an honest "did not run" costs nothing.

`make test-cli-integration` is run even though this slice adds no CLI test, because the CLI lane drives a live `internal/api` server and every one of its job-submitting fixtures now passes through three new checks. That is exactly the "run the lane when you change what existing tests use as scaffolding" case.

- [ ] **Step 4: Record the battery and the upgrade note in the PR body**

The PR body must carry, at minimum:

1. The table above with a measured result per row, including row 0's control and row 18's documented non-mutation.
2. **The retroactivity/upgrade note**, verbatim in substance: *operators with stored `scheduled_jobs` rows should check for a `job_spec` over any of the three new count bounds before upgrading.* A stored spec over any of them stops firing on the deploy that carries them. **This slice ships with the visibility already in place**, which the predecessor did not have: `schedrunner.ValidateStoredSpecsOnStartup` records the failure on every enabled schedule at boot rather than at its next scheduled fire, `GET /v1/scheduled-jobs` and its list sibling serve `last_error`, and `POST /v1/scheduled-jobs/{id}/run-now` answers 400 with the untruncated message. Say plainly that a real stored spec over these bounds is much less likely than `retries: 50` was - a schedule over 5000 tasks is a `job_spec` above 150 KB - but do not assert that nobody is affected.
3. A query an operator can run before upgrading:

```sql
SELECT s.id, s.name,
       jsonb_array_length(s.job_spec->'tasks')                                  AS task_count,
       (SELECT max(jsonb_array_length(t->'commands'))
          FROM jsonb_array_elements(s.job_spec->'tasks') AS t)                  AS max_commands_per_task,
       (SELECT coalesce(sum(coalesce(jsonb_array_length(t->'commands'), 1)), 0)
          FROM jsonb_array_elements(s.job_spec->'tasks') AS t)                  AS total_commands
FROM scheduled_jobs s
WHERE jsonb_typeof(s.job_spec->'tasks') = 'array'
  AND (jsonb_array_length(s.job_spec->'tasks') > 5000
    OR (SELECT max(jsonb_array_length(t->'commands'))
          FROM jsonb_array_elements(s.job_spec->'tasks') AS t) > 500
    OR (SELECT coalesce(sum(coalesce(jsonb_array_length(t->'commands'), 1)), 0)
          FROM jsonb_array_elements(s.job_spec->'tasks') AS t) > 25000);
```

(The `coalesce(..., 1)` is the legacy `command` spelling: a task with `command` and no `commands` contributes exactly one, which is what `normalizeTaskCommands` makes it.)

4. **The residual, stated in the same register the predecessor used.** The counts are bounded; unbounded work is not impossible. The worst case one authenticated 1 MiB `POST /v1/jobs` still buys is 275,000 subprocess spawns (25,000 commands x 11 attempts), 275,000 `task_logs` rows from step markers alone before any command output with nothing in the repo pruning them, 5000 sequential INSERT round trips inside one transaction plus up to roughly 250,000 more if the spec is dense in `depends_on` edges (which this slice does not bound), and a pending backlog of up to 5000 tasks re-read in full by `GetEligibleTasks` on every dispatcher tick until it drains. Down from roughly 116,000 commands and 1.28 million spawns: a 4.6x reduction on the aggregate and a 6x reduction on the task count. **`POST /v1/jobs` remains unrate-limited**, so every figure is per-request and repeatable; half 2 of the source item is the control for that and this slice does not substitute for it.
5. A statement of whether `-race` ran.

---

## Conductor steps after the last task

Outside an engineer's scope, recorded so they are not forgotten:

- **File the rate-limit item before closing the source item.** The gate decision's ruling 1 conditions the generous cap set on half 2 being *deferred, not abandoned*: rate-limiting `POST /v1/jobs` (and a per-user quota on `POST /v1/scheduled-jobs`, which has neither). Note in it that `internal/api/ratelimit.go` is per-IP via `RemoteAddr` only and does not trust `X-Forwarded-For`, which constrains how useful it is behind a proxy. **If this item is not filed, the spec's own argument for 5000/500/25000 does not hold and the tight set (2000/200/10,000) is the better call.**
- **File the dependency-edge item.** Gate ruling 3, confirmed: `jobcreate.CreateJobFromSpec` issues one `CreateTaskDependency` round trip per edge inside the caller's transaction, edges are quadratic in the task count, and `maxTasksPerJob` does not meaningfully bound it - the `V*(V-1)` ceiling at 5000 tasks is far above what 1 MiB expresses, so the body limit stays the only binding constraint. Roughly 250,000 sequential INSERTs on one pool connection is reachable. It is `jobcreate`'s insert strategy (batch them, or bound total edges), not validation.
- **Widen `docs/backlog/idea-2026-08-28-mcp-tool-schema-does-not-advertise-the-job-spec-bounds.md`** from two bounds to five, and note in the PR that it was widened. **Do not fix it in this slice** - its unresolved question (shared-type tags versus an MCP mirror, against the Single job-spec pipeline invariant) is a real design decision.
- **Close the source item** with `/backlog close bug-2026-08-28-task-and-command-counts`, and only for half 1. The `git mv` into `docs/backlog/closed/` is required scope, never a hand-edit of `status:`. If half 2's item is filed as a separate open item, say so in the resolution note.
- **Run `/roadmap`** after the close rather than hand-editing `ROADMAP.md`.
- **Phase 6 proposals** worth considering after the retro:
  - *A structural guard on the ingest-path enumeration.* This slice corrected a "four ingest paths" claim that had been wrong since it was written, and F6's fix is an enumeration with no test behind it. A guard that parses the module for `jobspec.Validate` / `ValidateJobSpec` / `ValidateStoredSchedule` call sites and compares the set against a written-down list would turn the enumeration into a checked claim - the same instrument `internal/store/createtask_guard_test.go` already applies to `CreateTask`'s callers, and this is its second confirmed instance. A uniqueness or completeness claim about call sites is exactly what a text-search-free AST walk can hold.
  - *The two PR #159 stale sentences* (F5, items 2 and 3) were invisible to that slice's own Phase 4 because they lived in a file it did not diff and named a test it renamed. A guard that fails when a comment names a `func Test...` identifier that no longer exists in the module would have caught the dangling name mechanically. Cheap, and this repo's comments name tests constantly.
