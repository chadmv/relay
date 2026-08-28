# Retry bounds and the retry-budget predicate - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound `retries` and `timeout_seconds` in `jobspec.Validate`, and give `IncrementTaskRetryCount` the `retry_count < retries` precondition it has been missing, without moving the retry budget out of Go.

**Architecture:** Two independent one-line behaviour changes plus the tests neither half has. Half A adds two constants and two comparisons inside `jobspec.Validate`'s existing per-task loop, which the Single job-spec pipeline invariant then propagates to REST, CLI, MCP and schedrunner for free. Half B adds a fourth predicate to one sqlc statement and leaves `handleTaskStatus`'s Go gate byte-for-byte, because that gate is what keeps a routine budget exhaustion from being counted as a `raced` fence rejection and from returning before `UpdateTaskStatus`.

**Tech Stack:** Go 1.26, sqlc, pgx/v5, testify, testcontainers-go (integration lanes), Postgres 16.

**Spec:** `docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md`
**Backlog item closed:** `docs/backlog/bug-2026-08-12-retries-unvalidated-and-budget-only-in-go.md`

---

## Slice independence declaration

**This is a single-slice, single-PR, single-session plan. It has no stages and must not be handed to `/backlog phases`.**

**Frontend/backend: this is 100% backend. There is no frontend component at all.** The conductor's reading is confirmed, and it was checked rather than assumed:

- `web/src/jobs/api.ts` declares `retries: number` on the task type and `web/src/jobs/{TasksTable,TaskLogPage}.tsx` render `retry_count/retries`. **None of them states a bound**, so none becomes wrong. The one prose surface, `web/src/jobs/NewJobPage.tsx`, lists the accepted spec field names and makes no claim about their ranges.
- The spec's Invariants section forbids a mirroring check in `web/src/jobs/`, in `internal/api`, or in the Python SDK. The server stays the validator of record.
- `python/src/relay/models.py` has `retries: int = 0` with no bound and no prose. Unchanged.
- `internal/mcp/wait.go` has a `timeout_seconds` field with its own `max 300` bound. **This is a different field** - it is `relay_wait_for_job`'s own poll timeout, not `TaskSpec.TimeoutSeconds` - and nothing about it changes. Do not "harmonise" the two.

**Half A and Half B are independent of each other in the code** (different packages, no shared symbol) and could in principle be parallelised. **Do not parallelise them here.** Task 10's prose sweep touches files both halves reason about (`internal/api/server_counters.go`, `internal/worker/taskstatus_fence_counters.go`), and Task 12's mutation battery has to run against both halves in one tree. The task order below is sequential and each task leaves the tree green.

---

## What I refuted in the spec

Four findings. Two are wrong instructions that would have wasted an engineer's time or produced a false "mutation survived" conclusion; one is a duplicate test; one is a real breakage the spec, the backlog item and the brief all missed.

### F1. The spec's mutation battery names the wrong test for two of its six rows, and one of them is inverted

**Row 3, "`<` to `<=` in either -> T-B4".** Wrong for the SQL half, and wrong in a way that reads as a survived mutation. Under `AND retry_count <= retries` the Go gate still short-circuits: a `retries = 2` task with `retry_count = 2` fails `2 < 2` in Go and never calls the statement, so T-B4's outcome is byte-identical and the mutation looks like it did nothing. The SQL `<=` mutation is observable **only at the store layer**, where T-B1 calls the statement directly at `retry_count == retries`. Corrected in this plan: SQL `<=` -> **T-B1**; Go `<=` -> **T-B3 and T-B4**. Row 3 must be split into two rows.

**Row 4, "`maxRetries` 10 -> 11, and 10 -> 9 -> T-A2 and T-A1 respectively".** Inverted. `maxRetries = 11` accepts `retries: 11`, which is a **T-A1** rejection case, so T-A1 goes red. `maxRetries = 9` rejects `retries: 10`, which is a **T-A2** acceptance case, so T-A2 goes red. Row 5 (`maxTimeoutSeconds` off by one in each direction) has the same shape and the spec leaves it ambiguous; it is spelled out per-direction in Task 12.

### F2. T-B2 already exists at HEAD

The spec's T-B2 ("Same row at `retry_count = 0`: the call succeeds, `retry_count` is 1, status is `pending`, `worker_id` is NULL, and `assignment_epoch` is N+1") is, assertion for assertion, Case 1 of `TestIncrementTaskRetryCount_StatusEpochAndAssigneeGuarded` in `internal/store/store_test.go`, with `assigned_at` additionally covered by the `IncrementTaskRetryCount` subtest of `TestAssignedAtIsClearedWhereverWorkerIDIs`. Writing it a third time would be a duplicate and would cost another Postgres container.

It is folded into T-B1 as that test's **first leg**, where it is needed as setup regardless - the only way to reach `retry_count == retries` through production statements is to spend the budget first - and where it also serves the "positive control first, so a suite where the statement stopped working cannot look like a suite of successful rejections" convention this repo already uses at that exact test.

### F3. The spec's "T-B3 carries Q5's new job" overstates T-B3's uniqueness

The spec's R2 argument is correct about the consequence but leaves the impression that deleting the Go gate would otherwise go unnoticed. Measured against the tree: once the SQL predicate lands, deleting `task.RetryCount < task.Retries` also reddens `TestHandleTaskStatus_RejectsFailedFromANonAssigneeAndDoesNotCascade` (its positive control drives a `retries = 0` task to `failed`; with the gate gone the branch is entered, `0 < 0` fails in SQL, and the task never reaches `failed`) and several of its neighbours in the same file.

That does not make T-B3 unnecessary - it makes its **expected collateral** something the engineer must be told about in advance, or a battery run will look like a botched mutation. What T-B3 adds that nothing else in the tree has is (a) a subject at a genuinely **exhausted** budget rather than a zero budget, and (b) the `TaskStatusFenceRejections()` assertion, which is the only thing anywhere that pins the counter consequence Q5 identified. Task 12 lists the expected collateral by name.

### F4. The predicate breaks an existing integration test, in the one lane CI never runs

Neither the spec nor the item checked what the new predicate does to existing tests. It breaks one and silently hollows out another, both in `internal/store/retry_job_tasks_integration_test.go`, because `retryFixture.pending` creates tasks with `Retries: 0`:

- **`TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne` goes RED.** It seeds task `b` through `f.inStatus(t, "t-b", "running")` and then calls `IncrementTaskRetryCount` with `require.NoError`. With `retries = 0` and `retry_count = 0`, `0 < 0` is false, the statement returns `pgx.ErrNoRows`, and the `require.NoError` fails. Everything downstream of it in that test (the "two rows must start at different epochs" and "retry_count must reset to 0" assertions) then rests on a fixture that never happened.
- **`TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected` becomes vacuous.** Its retry leg asserts `ErrNoRows` and its comment says the rejection comes from the epoch and the worker predicate, "Both are required; neither is decoration." With `retries = 0` the **budget** predicate alone rejects it, so the assertion would stay green with both of the predicates it claims to test removed.

`.github/workflows/go-ci.yml` never calls `make test-integration` (recorded in `ROADMAP.md`'s Now section), so neither failure is visible to CI. Task 6 repairs both, ahead of the predicate, by giving the fixture a retry budget. **Do not skip Task 6 and do not discover this by running only `make test`.**

### Not refuted, checked and recorded so nobody re-derives it

- `TestTaskStatusWritableSetMatchesTheSQLAllowList` (`internal/worker/taskstatus_fence_counters_test.go`) parses `tasks.sql`, strips every `--` line, and requires **exactly one** `status IN (...)` clause in each statement's executable text. `AND retry_count < retries` adds no such clause and every word added to the doc block is stripped, so the guard is unaffected. It is still run explicitly in Task 7.
- `internal/store/query/jobs.sql`'s `JobStatusCounts` comment says `UpdateTaskStatus` and `IncrementTaskRetryCount` "both carry `status IN ('pending','dispatched','running')`". Still true. No edit.
- `internal/worker/handler.go`'s identity-gate comment says a forged terminal "reaches `IncrementTaskRetryCount`, which rejects on its own `worker_id` predicate". Still true. No edit.
- No job spec anywhere in the repo (Go tests, `web/e2e`, `python/tests`, README examples) uses `retries > 10`, a negative `retries`, or a `timeout_seconds` outside `[0, 604800]`. Half A breaks no existing test.
- The spec cites `internal/api/jobs.go:984` and `:1002` for the operator retry path. Those are line citations and they rot; the symbols are `SelectRetryableTaskIDs` and `RetryJobTasks`, both called from `handleRetryJob`.

---

## Critical files

**Modified (production):**

| File | What changes |
|---|---|
| `internal/jobspec/jobspec.go` | Two constants (`maxRetries`, `maxTimeoutSeconds`) and two comparisons inside `Validate`'s first per-task loop |
| `internal/store/query/tasks.sql` | `IncrementTaskRetryCount` gains `AND retry_count < retries`; its doc block goes three predicates -> four; `UpdateTaskStatus`'s cross-reference sentence is corrected |
| `internal/store/tasks.sql.go` | **GENERATED. Never hand-edit.** Produced by `make generate` in Task 7 |
| `internal/worker/handler.go` | **Comment only.** No code change at the retry gate |
| `internal/api/server_counters.go` | Comment only: the "IDENTICAL three predicates" sentence |
| `internal/worker/taskstatus_fence_counters.go` | Comment only: "WHICH OF THE THREE SQL PREDICATES" |
| `README.md` | Two rows of the job-spec field table |

**Modified (tests):**

| File | What changes |
|---|---|
| `internal/store/retry_job_tasks_integration_test.go` | `retryFixture.pending` gets `Retries: 1` (see F4) |
| `internal/store/store_test.go` | Two stale comments on `TestIncrementTaskRetryCount_StatusEpochAndAssigneeGuarded` |
| `internal/store/incrementtaskretrycount_guard_test.go` | One stale comment |

**Created (tests):**

| File | Contents |
|---|---|
| `internal/jobspec/jobspec_bounds_test.go` | T-A1, T-A2 |
| `internal/api/jobs_spec_bounds_integration_test.go` | T-A3 |
| `internal/cli/jobs_spec_bounds_integration_test.go` | T-A4 |
| `internal/schedrunner/stored_spec_bounds_test.go` | T-A6 |
| `internal/store/createtask_guard_test.go` | T-A5 |
| `internal/store/increment_task_retry_count_budget_integration_test.go` | T-B1 (with T-B2 folded in as its first leg) |
| `internal/worker/handler_retry_budget_integration_test.go` | T-B3, T-B4 |

**Read before starting:** `CLAUDE.md`'s Invariants section (the epoch fence and the Single job-spec pipeline bullets), and the doc block on `IncrementTaskRetryCount` in `internal/store/query/tasks.sql`.

---

## Mutation instruments

Four named edits, each used to force a RED, each reverted immediately. **Every one of them must be verified to have applied before its test is run** - a mutation that silently fails to apply reports as "survived", and CRLF has broken four in a row on this repo.

The verification is always the same two commands, run in Git Bash from the worktree root:

```bash
git --no-pager diff --stat <file>          # MUST be non-empty
git --no-pager diff -- <file>              # MUST show the intended edit
```

and the restore is always:

```bash
git checkout -- <file>
git --no-pager diff --stat <file>          # MUST now be empty
```

**MUT-A1 - the validator is not there.** In `internal/jobspec/jobspec.go`, comment out both bound checks inside `Validate`'s first per-task loop (the `if ts.Retries < 0 ...` block and the `if ts.TimeoutSeconds != nil ...` block). This returns `Validate` to its HEAD behaviour exactly, which is what makes it a legitimate substitute for a pre-implementation RED.

**MUT-A2 - a planted `CreateTask` call site.** Add one line to a non-test Go file outside `internal/jobcreate`.

**MUT-B1 - the Go gate is gone.** In `internal/worker/handler.go`, change `if terminal && task.RetryCount < task.Retries {` to `if terminal {`.

**MUT-B2 - the Go gate is off by one.** In `internal/worker/handler.go`, change `task.RetryCount < task.Retries` to `task.RetryCount <= task.Retries`.

---

## Task 1: Bound `retries` and `timeout_seconds` in `jobspec.Validate`

**Files:**
- Create: `internal/jobspec/jobspec_bounds_test.go`
- Modify: `internal/jobspec/jobspec.go` (add two constants after the `var (...)` regexp block; add two checks inside `Validate`'s first per-task loop, after `nameSet[ts.Name] = struct{}{}`)

- [ ] **Step 1: Write the failing test**

Create `internal/jobspec/jobspec_bounds_test.go`:

```go
package jobspec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// i32 returns a pointer to v. TaskSpec.TimeoutSeconds is a *int32 because nil is
// the documented "no deadline", so every timeout case here has to spell the
// pointer out.
func i32(v int32) *int32 { return &v }

// twoTaskSpec builds a valid two-task spec with the OFFENDING task FIRST and a
// healthy task second.
//
// THE ORDER IS THE POINT, not tidiness. Validate returns the first problem it
// finds, so a bad task placed LAST cannot distinguish "the bound rejected it"
// from "the loop exited early for some unrelated reason". Putting it first also
// forces the error to NAME the offending task, which is what the assertions
// below check rather than merely that an error occurred.
func twoTaskSpec(bad TaskSpec) *JobSpec {
	bad.Name = "bad-task"
	bad.Command = []string{"echo", "x"}
	return &JobSpec{
		Name: "bounds",
		Tasks: []TaskSpec{
			bad,
			{Name: "healthy-task", Command: []string{"echo", "y"}},
		},
	}
}

// TestValidate_RetriesAndTimeoutOutOfRangeAreRejected is the half-A rejection
// table. RED at HEAD: Validate reads neither field.
//
// The assertion is on the WHOLE message, not on a substring, because the
// per-task naming is the property the backlog item asks for - a caller with a
// fifty-task spec has to be told which task is wrong.
func TestValidate_RetriesAndTimeoutOutOfRangeAreRejected(t *testing.T) {
	const retriesMsg = "task bad-task: retries must be between 0 and 10"
	const timeoutMsg = "task bad-task: timeout_seconds must be between 0 and 604800 (0 or omitted means no deadline)"

	cases := []struct {
		name string
		task TaskSpec
		want string
	}{
		{"retries one over the cap", TaskSpec{Retries: 11}, retriesMsg},
		{"retries negative", TaskSpec{Retries: -1}, retriesMsg},
		{"retries at the item's own repro value", TaskSpec{Retries: 2000000000}, retriesMsg},
		{"timeout one over the cap", TaskSpec{TimeoutSeconds: i32(604801)}, timeoutMsg},
		{"timeout negative", TaskSpec{TimeoutSeconds: i32(-1)}, timeoutMsg},
		{"timeout at int32 max", TaskSpec{TimeoutSeconds: i32(2147483647)}, timeoutMsg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(twoTaskSpec(tc.task))
			require.Error(t, err,
				"an out-of-range value must be refused at the single validation point, which REST, CLI, "+
					"MCP and schedrunner all inherit")
			require.Equal(t, tc.want, err.Error(),
				"the message must NAME the offending task and STATE the range, matching this file's "+
					"existing per-task error style")
		})
	}
}

// TestValidate_BoundaryValuesAreAccepted is the positive control, and it is what
// an off-by-one in either constant breaks. An off-by-one is the most likely
// defect in this whole change and nothing else catches it.
//
// The nil case is called out by the backlog item by name: TimeoutSeconds is a
// *int32 and nil is the documented "no deadline". 0 is its second, equally valid
// spelling and stays accepted - rejecting it would break stored specs for no
// benefit.
func TestValidate_BoundaryValuesAreAccepted(t *testing.T) {
	cases := []struct {
		name string
		task TaskSpec
	}{
		{"retries at zero, the default", TaskSpec{Retries: 0}},
		{"retries exactly at the cap", TaskSpec{Retries: 10}},
		{"timeout_seconds exactly zero", TaskSpec{TimeoutSeconds: i32(0)}},
		{"timeout_seconds exactly at the cap", TaskSpec{TimeoutSeconds: i32(604800)}},
		{"timeout_seconds omitted entirely", TaskSpec{}},
		{"both at their caps on one task", TaskSpec{Retries: 10, TimeoutSeconds: i32(604800)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, Validate(twoTaskSpec(tc.task)),
				"a spec AT the boundary must still be accepted")
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/jobspec/ -run 'TestValidate_(RetriesAndTimeoutOutOfRangeAreRejected|BoundaryValuesAreAccepted)' -v
```

Expected: **6 FAIL, 6 PASS.** Every subtest of `TestValidate_RetriesAndTimeoutOutOfRangeAreRejected` fails with

```
Error:  An error is expected but got nil.
Test:   TestValidate_RetriesAndTimeoutOutOfRangeAreRejected/retries_one_over_the_cap
Messages: an out-of-range value must be refused at the single validation point, ...
```

Every subtest of `TestValidate_BoundaryValuesAreAccepted` passes. **If you see a compile error instead, the test file is wrong, not the code** - `i32` or `twoTaskSpec` may be colliding with an existing symbol in package `jobspec`; rename yours.

- [ ] **Step 3: Write the implementation**

In `internal/jobspec/jobspec.go`, immediately after the closing `)` of the `var (...)` block that declares `revHeadRe` ... `clientTmplRe`, add:

```go
// maxRetries bounds TaskSpec.Retries. Chosen for a render and task farm: the
// failures a retry actually rescues are the ones that clear BETWEEN dispatches -
// a flaky network mount, a p4 sync that hit a transient server error, one
// unhealthy node in a fleet - and eleven attempts covers all of them.
//
// DO NOT RAISE THIS FOR A CONTENDED-RESOURCE CASE. A saturated license server is
// the argument that will be made, and it argues the other way: there is no
// backoff anywhere between a retry and its next dispatch, because
// IncrementTaskRetryCount returns the task to `pending` and handleTaskStatus
// immediately calls NotifyTaskSubmitted, which wakes the dispatcher. N retries
// against a saturated pool are therefore N immediate failures inside a few
// seconds; a larger N buys no waiting, only a faster burn. The instrument for
// contention is a reservation, not a retry count.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. Validate runs on STORED scheduled-job specs
// at fire time (schedrunner.fireOne -> jobcreate.CreateJobFromSpec -> Validate),
// so an env-tunable bound would make retroactive schedule invalidation
// environment-dependent: the same stored spec fires on one replica's
// configuration and silently stops on another's, and lowering the knob would
// disable schedules with no signal anywhere. A validation vocabulary shared by
// four ingest paths is a property of the binary, exactly as the priority set is.
const maxRetries = 10

// maxTimeoutSeconds bounds TaskSpec.TimeoutSeconds. Seven days: comfortably
// above the outer edge of a plausible relay task (a full P4 sync of a workspace
// that can exceed 1 TB, followed by a heavy bake, cook or render, is plausibly
// 24 to 72 hours) and far below the ~68 years int32's maximum buys today.
//
// THIS IS NOT RELAY_TASK_MAX_ASSIGNMENT AND MUST NOT BE COUPLED TO IT. The two
// are independent bounds. timeout_seconds is the TASK's own execution deadline,
// enforced by the agent (newRunner, internal/agent/runner.go) and by the
// watchdog's execution arm (ListOverdueAssignedTasks);
// RELAY_TASK_MAX_ASSIGNMENT is the COORDINATOR's absolute assignment bound and
// sweeps the task regardless of this value. A task whose own timeout exceeds the
// absolute cap is simply swept by the other arm. Seven days is deliberately
// ABOVE that knob's 24h default so the independence is visible in the numbers - a
// cap chosen below it would read as agreement and be maintained as if it were
// one. Do not derive this from that env var at runtime.
const maxTimeoutSeconds = 604800
```

Then, inside `Validate`, in the first per-task loop, immediately after `nameSet[ts.Name] = struct{}{}` and before the loop's closing brace, add:

```go
		// Bounds last in this loop body, so command-form and duplicate-name
		// errors keep the precedence they have today.
		//
		// A nil TimeoutSeconds is SKIPPED, not defaulted: nil is the documented
		// "no deadline" and 0 is its second, equally valid spelling. Negatives
		// are rejected rather than documented as a third synonym, because
		// today's equivalence is an accident of two independent sites agreeing
		// (newRunner sets a deadline only `if timeoutSec > 0`;
		// ListOverdueAssignedTasks's execution arm requires
		// `timeout_seconds IS NOT NULL AND timeout_seconds > 0`) and a third
		// consumer is already close - overdueReason computes
		// time.Duration(*t.TimeoutSeconds)*time.Second, which for a negative
		// value yields a negative duration and a nonsense operator string.
		if ts.Retries < 0 || ts.Retries > maxRetries {
			return fmt.Errorf("task %s: retries must be between 0 and %d", ts.Name, maxRetries)
		}
		if ts.TimeoutSeconds != nil && (*ts.TimeoutSeconds < 0 || *ts.TimeoutSeconds > maxTimeoutSeconds) {
			return fmt.Errorf("task %s: timeout_seconds must be between 0 and %d (0 or omitted means no deadline)",
				ts.Name, maxTimeoutSeconds)
		}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./internal/jobspec/ -v
go test ./... -timeout 120s
```

Expected: all `internal/jobspec` tests PASS (12 new subtests plus the 15 that were already there), and the whole default lane stays green. No existing spec in the repo carries an out-of-range value, so nothing else should move.

- [ ] **Step 5: Commit**

```bash
git add internal/jobspec/jobspec.go internal/jobspec/jobspec_bounds_test.go
git commit -m "feat(jobspec): bound retries and timeout_seconds at the single validation point"
```

---

## Task 2: Prove the refusal reaches the REST wire (T-A3)

**Why this lane.** The backlog item is explicit that a `jobspec` unit test alone leaves the rejection "merely a library property". `POST /v1/jobs` is the entry point every other consumer inherits - the SPA, the Python SDK, MCP and the CLI all speak it - and the assertion here is about **what the server puts on the wire**: the status code, the `error` key, and the fact that a boundary value is STORED and echoed rather than clamped. Per `CLAUDE.md`'s routing rule that is an integration-lane assertion, and `internal/api/api_test.go` is already `//go:build integration` with the harness (`newTestServer`, `createTestUser`, `createTestToken`) this needs.

**Files:**
- Create: `internal/api/jobs_spec_bounds_integration_test.go`

- [ ] **Step 1: Write the test**

Create `internal/api/jobs_spec_bounds_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postJobSpec submits body to POST /v1/jobs as token and returns the status code
// and the decoded response object.
//
// The response is decoded into map[string]any DELIBERATELY. Decoding into
// internal/api's own jobResponse would agree with the handler by construction on
// every key and every type, which is the vacuous-fixture shape CLAUDE.md's
// "Where a CLI test goes" rule warns about, in its server-side form.
func postJobSpec(t *testing.T, srv *api.Server, token, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var out map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out),
		"every response on this route is JSON, including the 400")
	return rec.Code, out
}

// TestCreateJob_RetriesAndTimeoutBoundsAreEnforcedOnTheWire is what makes the
// half-A bound more than a library property: the real handler, the real router,
// the real BearerAuth middleware and the real response body.
//
// THE REFUSALS COME FIRST and the acceptance last, so a poisoned input is never
// the last thing the test does - a distinctive input placed at the end cannot
// detect an early-exit defect.
func TestCreateJob_RetriesAndTimeoutBoundsAreEnforcedOnTheWire(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Bounds", "bounds@example.com", false)
	token := createTestToken(t, q, user.ID)

	// The offending task is FIRST and a healthy task follows it, so the error
	// cannot be produced by "the last task lost".
	code, body := postJobSpec(t, srv, token, `{
		"name": "over-budget",
		"tasks": [
			{"name": "bad-task", "command": ["echo", "x"], "retries": 11},
			{"name": "healthy-task", "command": ["echo", "y"]}
		]}`)
	require.Equal(t, http.StatusBadRequest, code,
		"an out-of-range retries must be refused at submission, not stored and discovered at dispatch")
	assert.Equal(t, "task bad-task: retries must be between 0 and 10", body["error"],
		"the per-task message must reach the wire verbatim: a caller with a fifty-task spec has to be "+
			"told WHICH task is wrong")

	code, body = postJobSpec(t, srv, token, `{
		"name": "over-deadline",
		"tasks": [{"name": "bad-task", "command": ["echo", "x"], "timeout_seconds": 604801}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t,
		"task bad-task: timeout_seconds must be between 0 and 604800 (0 or omitted means no deadline)",
		body["error"])

	// POSITIVE CONTROL, at BOTH exact boundaries, on the same server. Without it
	// a handler that had started refusing every spec would pass both assertions
	// above. It is also the leg that goes red if either constant moves down by
	// one.
	code, body = postJobSpec(t, srv, token, `{
		"name": "at-the-boundary",
		"tasks": [{"name": "t", "command": ["echo", "x"], "retries": 10, "timeout_seconds": 604800}]}`)
	require.Equal(t, http.StatusCreated, code,
		"a spec AT the boundary must still be created")

	tasks, ok := body["tasks"].([]any)
	require.True(t, ok, "the created job must carry its tasks")
	require.Len(t, tasks, 1)
	task, ok := tasks[0].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 10, task["retries"],
		"the boundary value must be STORED and echoed, not clamped: validation refuses, it never edits")
	assert.EqualValues(t, 604800, task["timeout_seconds"],
		"same for the deadline - a clamping validator would pass the 201 assertion above and still be wrong")
}
```

- [ ] **Step 2: Apply MUT-A1 and verify it applied**

In `internal/jobspec/jobspec.go`, comment out both bound checks added in Task 1 (prefix each of the four lines of the two `if` blocks, and their `}` lines, with `// `). Then:

```bash
git --no-pager diff --stat internal/jobspec/jobspec.go
git --no-pager diff -- internal/jobspec/jobspec.go
```

Expected: a non-empty stat line, and a diff showing the two `if` blocks commented out. **If the stat is empty the mutation did not apply and any result below is meaningless.**

- [ ] **Step 3: Run the test to verify it fails**

Run (needs Docker):

```bash
go test -tags integration -p 1 ./internal/api/ -run TestCreateJob_RetriesAndTimeoutBoundsAreEnforcedOnTheWire -v -timeout 300s
```

Expected: FAIL at the first assertion, with

```
Error:  Not equal:
        expected: 400
        actual  : 201
Messages: an out-of-range retries must be refused at submission, ...
```

- [ ] **Step 4: Restore, then run the test to verify it passes**

```bash
git checkout -- internal/jobspec/jobspec.go
git --no-pager diff --stat internal/jobspec/jobspec.go   # MUST be empty
go test -tags integration -p 1 ./internal/api/ -run TestCreateJob_RetriesAndTimeoutBoundsAreEnforcedOnTheWire -v -timeout 300s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/jobs_spec_bounds_integration_test.go
git commit -m "test(api): POST /v1/jobs refuses out-of-range retries and timeout_seconds"
```

---

## Task 3: Prove the refusal reaches a human (T-A4)

**Why this lane.** T-A3 proves the server refuses. It does not prove a person is told. `relay submit` decodes the server's error body into `relayclient.ResponseError` and returns it, and the shipped-at-`9f57720` lane (`make test-cli-integration`) runs the CLI entrypoint against a live `internal/api` server over real HTTP. This is a real-server assertion, so it belongs here and not behind an `httptest` fixture - the lane's standing rule.

**Files:**
- Create: `internal/cli/jobs_spec_bounds_integration_test.go`

- [ ] **Step 1: Write the test**

Create `internal/cli/jobs_spec_bounds_integration_test.go`:

```go
//go:build integration

package cli

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_SubmitSurfacesTheServersOutOfRangeRefusal is the only test in
// the tree that proves the half-A message reaches a user. It runs the real
// `relay submit` entrypoint against the real internal/api server over real HTTP,
// so nothing between jobspec.Validate and the terminal is faked.
func TestIntegration_SubmitSurfacesTheServersOutOfRangeRefusal(t *testing.T) {
	s := startRelayServer(t)

	// Offending task FIRST, healthy task second: the message must name which.
	const overBudget = `{
	  "name": "over-budget",
	  "tasks": [
	    {"name": "bad-task", "command": ["echo", "x"], "retries": 11},
	    {"name": "healthy-task", "command": ["echo", "y"]}
	  ]
	}`

	var out, errOut bytes.Buffer
	err := doSubmit(testCtx(t), s.adminCfg(),
		[]string{"--detach", writeSpecFile(t, overBudget)}, &out, &errOut)
	require.Error(t, err, "an out-of-range spec must not submit successfully")

	var re *relayclient.ResponseError
	require.ErrorAs(t, err, &re,
		"the refusal must arrive as a typed ResponseError, or ErrorIsTransient would classify it as "+
			"retryable and a polling caller would keep asking")
	assert.Equal(t, http.StatusBadRequest, re.StatusCode,
		"400, so ErrorIsTransient reports it as PERMANENT")
	assert.Equal(t, "task bad-task: retries must be between 0 and 10", err.Error(),
		"the SERVER's own message must reach the user verbatim - this is what a validation error is FOR, "+
			"and it is the only assertion in the tree covering that whole path")
	assert.Empty(t, strings.TrimSpace(out.String()),
		"a refused submit must print no job id: doSubmit prints job.ID only after Do returns nil, and a "+
			"printed empty id is worse than none")

	// POSITIVE CONTROL on the same command and the same live server, at BOTH
	// boundaries. Without it, a doSubmit that had started failing on every spec
	// would pass every assertion above.
	const atTheBoundary = `{
	  "name": "at-the-boundary",
	  "tasks": [{"name": "t1", "command": ["echo", "ok"], "retries": 10, "timeout_seconds": 604800}]
	}`
	var okOut, okErr bytes.Buffer
	require.NoError(t, doSubmit(testCtx(t), s.adminCfg(),
		[]string{"--detach", writeSpecFile(t, atTheBoundary)}, &okOut, &okErr),
		"a spec AT the boundary must still submit")
	require.NotEmpty(t, strings.TrimSpace(okOut.String()),
		"the accepted submit must print its job id")
}
```

- [ ] **Step 2: Apply MUT-A1 and verify it applied**

In `internal/jobspec/jobspec.go`, comment out both bound checks added in Task 1. Then:

```bash
git --no-pager diff --stat internal/jobspec/jobspec.go
git --no-pager diff -- internal/jobspec/jobspec.go
```

Expected: non-empty stat; the diff shows both `if` blocks commented out.

- [ ] **Step 3: Run the test to verify it fails**

Run:

```bash
go test -tags integration -count=1 ./internal/cli/ -run TestIntegration_SubmitSurfacesTheServersOutOfRangeRefusal -v -timeout 480s
```

Expected: FAIL with

```
Error:  An error is expected but got nil.
Messages: an out-of-range spec must not submit successfully
```

- [ ] **Step 4: Restore, then run the test to verify it passes**

```bash
git checkout -- internal/jobspec/jobspec.go
git --no-pager diff --stat internal/jobspec/jobspec.go   # MUST be empty
go test -tags integration -count=1 ./internal/cli/ -run TestIntegration_SubmitSurfacesTheServersOutOfRangeRefusal -v -timeout 480s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/jobs_spec_bounds_integration_test.go
git commit -m "test(cli): relay submit surfaces the server's out-of-range refusal"
```

---

## Task 4: Pin the retroactive-invalidation hazard (T-A6)

**Read this task's framing before writing a line of it.** The test below **asserts a defect**, deliberately, and that is exactly the shape this project has been burned by ("a test can be green because of the bug"). Three things keep it honest and all three are load-bearing:

1. The test name ends `_DocumentedHazard`.
2. Its header comment states in the first sentence that the behaviour it pins is wrong, names `bug-2026-08-23-unfireable-schedule-is-invisible` as the item that fixes it, and states **what each assertion becomes** once that item ships.
3. The last assertion is an active tripwire that goes RED the moment the sibling adds its column, so the sibling's implementer is forced to read the comment rather than merely hoping to find it.

**Files:**
- Create: `internal/schedrunner/stored_spec_bounds_test.go`

- [ ] **Step 1: Write the test**

Create `internal/schedrunner/stored_spec_bounds_test.go`:

```go
//go:build integration

package schedrunner_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeOverBudgetSpecJSON is a spec that was VALID when it was stored and is not
// any more. retries: 50 was accepted by every relay release before the
// retry-bounds change; jobspec.Validate now refuses it, and schedrunner
// re-validates the stored spec on EVERY fire because fireOne calls
// jobcreate.CreateJobFromSpec, which calls jobspec.Validate.
func makeOverBudgetSpecJSON(t *testing.T) []byte {
	t.Helper()
	spec, err := json.Marshal(map[string]any{
		"name":  "legacy",
		"tasks": []map[string]any{{"name": "t", "command": []string{"echo", "hi"}, "retries": 50}},
	})
	require.NoError(t, err)
	return spec
}

// TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard
// ASSERTS A DEFECT ON PURPOSE. Read this paragraph before reading the code.
//
// WHAT IT PINS IS WRONG, AND IT IS PINNED SO THAT IT IS IN THE SUITE RATHER THAN
// IN PROSE. Bounding `retries` in jobspec.Validate is retroactive on stored
// scheduled_jobs rows, because the spec is re-validated at fire time. A schedule
// stored with retries: 50 stops producing jobs the instant this deploys, and
// TickOnce logs one line and calls advanceNextRun - so next_run_at keeps
// marching and GET /v1/scheduled-jobs/{id}, `relay schedules` and the SPA all
// show a healthy schedule whose last_run_at has quietly stopped moving.
//
// THAT IS THE SUBJECT OF docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md,
// which ROADMAP.md pairs with the retry-bounds item. The human's gate decision
// for this slice was to ship the bounds alone, with this test plus an upgrade
// note in the PR body as the agreed mitigation.
//
// WHAT EACH ASSERTION BECOMES WHEN THE SIBLING SHIPS:
//   - "the poisoned schedule fires no job" STAYS. It is correct behaviour: a
//     spec that does not validate must not produce a job.
//   - "next_run_at still advances" STAYS. It is what stops a poisoned schedule
//     hot-looping every tick.
//   - "the row exposes no field that could record the failure" INVERTS. Require
//     the new field to EXIST, require it to carry the validation error this tick
//     produced, and drop the _DocumentedHazard suffix from this test's name.
//
// DO NOT satisfy the last assertion by adding the sibling's new field to a
// deny-list. It is written to go RED on ANY new failure-shaped field precisely
// so that the sibling's implementer has to come here.
func TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "legacy-spec@example.com")

	overdue := pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true}

	// The poisoned schedule sorts FIRST (older next_run_at), so it cannot pass by
	// being skipped after the healthy one already proved the tick works.
	poisoned, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "legacy-retries", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-10 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// CONTROL, in the same tick: a schedule whose spec still validates must still
	// fire. Without it, a TickOnce that had stopped firing anything at all would
	// pass every assertion below.
	healthy, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "healthy", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: overdue,
	})
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))

	healthyJobs, err := h.q.ListJobsByScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	require.Len(t, healthyJobs, 1, "control: a still-valid stored spec must still fire")

	poisonedJobs, err := h.q.ListJobsByScheduledJob(ctx, poisoned.ID)
	require.NoError(t, err)
	assert.Empty(t, poisonedJobs,
		"CORRECT AND PERMANENT: a stored spec that no longer validates must not produce a job")

	row, err := h.q.GetScheduledJob(ctx, poisoned.ID)
	require.NoError(t, err)
	assert.True(t, row.NextRunAt.Time.After(time.Now()),
		"CORRECT AND PERMANENT: next_run_at must advance so the poisoned schedule does not hot-loop")
	assert.False(t, row.LastRunAt.Valid,
		"no run happened, so last_run_at must stay unset")
	assert.False(t, row.LastJobID.Valid,
		"no job was created, so last_job_id must stay unset")
	assert.True(t, row.Enabled,
		"THE HAZARD, STATED POSITIVELY: nothing disables the schedule either, so it stays in every "+
			"'enabled' listing forever while producing nothing")

	// THE TRIPWIRE. Every user-visible read of a schedule is built from this row -
	// GET /v1/scheduled-jobs/{id}, `relay schedules`, the SPA - so asserting that
	// the row has no field capable of carrying the failure is what turns "nothing
	// user-visible records it" from prose into a checked claim.
	//
	// WHEN bug-2026-08-23-unfireable-schedule-is-invisible SHIPS ITS COLUMN AND
	// models.go IS REGENERATED, THIS GOES RED. That is the point. Invert it as
	// described in this test's header comment; do not exempt the new field.
	for _, f := range reflect.VisibleFields(reflect.TypeOf(store.ScheduledJob{})) {
		name := strings.ToLower(f.Name)
		assert.NotContains(t, name, "error",
			"store.ScheduledJob gained field %q. If that is the sibling item's failure surface, this "+
				"test's last block must INVERT - see its header comment - not grow an exemption.", f.Name)
		assert.NotContains(t, name, "fail",
			"store.ScheduledJob gained field %q. Same instruction as above.", f.Name)
	}
}
```

- [ ] **Step 2: Apply MUT-A1 and verify it applied**

In `internal/jobspec/jobspec.go`, comment out both bound checks added in Task 1. Then:

```bash
git --no-pager diff --stat internal/jobspec/jobspec.go
git --no-pager diff -- internal/jobspec/jobspec.go
```

Expected: non-empty stat; both `if` blocks commented out.

- [ ] **Step 3: Run the test to verify it fails**

Run:

```bash
go test -tags integration -p 1 ./internal/schedrunner/ -run TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard -v -timeout 300s
```

Expected: FAIL with

```
Error:  Should be empty, but was [{...}]
Messages: CORRECT AND PERMANENT: a stored spec that no longer validates must not produce a job
```

That is the whole point: at HEAD the over-budget spec validates, so the schedule fires and the assertion sees one job. **This test is genuinely RED against pre-change behaviour** - MUT-A1 here reproduces HEAD exactly.

- [ ] **Step 4: Restore, then run the test to verify it passes**

```bash
git checkout -- internal/jobspec/jobspec.go
git --no-pager diff --stat internal/jobspec/jobspec.go   # MUST be empty
go test -tags integration -p 1 ./internal/schedrunner/ -v -timeout 600s
```

Expected: PASS, and every pre-existing `internal/schedrunner` test still passes (`makeSpecJSON`'s spec carries no bounded field, so nothing else moves).

- [ ] **Step 5: Commit**

```bash
git add internal/schedrunner/stored_spec_bounds_test.go
git commit -m "test(schedrunner): pin the retroactive-invalidation hazard for a stored over-budget spec"
```

---

## Task 5: Guard the two task INSERTs against a second writer (T-A5)

**What this is for.** The spec declines a database `CHECK` on `tasks.retries` / `tasks.timeout_seconds` (Q6), and the price of declining it is that "some future writer bypasses `jobspec.Validate`" is no longer impossible. This guard is what stands in for the constraint: it fails at test time, in the default lane, with no migration and no startup risk. `CreateTask` and `CreateTaskWithSource` have exactly one non-test caller today, `internal/jobcreate/jobcreate.go`, and `jobcreate.CreateJobFromSpec` calls `jobspec.Validate` first.

It reuses `scanForIdentReferences` from `internal/store/incrementtaskretrycount_guard_test.go` (same package, `store_test`), which parses Go and never sees comments - so a mention in prose cannot trip it.

**Files:**
- Create: `internal/store/createtask_guard_test.go`

- [ ] **Step 1: Write the guard and its own discriminating test**

Create `internal/store/createtask_guard_test.go`:

```go
package store_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestCreateTaskHasNoCallerOutsideJobcreate is a STRUCTURAL guard, deliberately
// NOT integration-tagged so it runs in the plain `go test ./...` gate.
//
// IT IS WHAT STANDS IN FOR A DATABASE CHECK CONSTRAINT, and that is the whole
// reason it exists. tasks.retries and tasks.timeout_seconds carry no CHECK: the
// spec declined one because `ALTER TABLE tasks ADD CONSTRAINT ... CHECK (...)`
// validates every existing row at startup, and migrations run in-process, so a
// deployment that already holds an out-of-range row would refuse to start - on
// exactly the population that has the bug. NOT VALID avoids the scan and buys a
// worse failure: the constraint still fires on any UPDATE of a pre-existing bad
// row, which lands in handleTaskStatus's non-ErrNoRows arm as one budgeted log
// line and a task stuck until the 24h watchdog.
//
// What makes that trade acceptable is that the bound has exactly one enforcement
// point and these two statements have exactly one caller. This test is what
// keeps the second half of that sentence true. If it goes RED, the new caller
// must either route through jobcreate.CreateJobFromSpec (which calls
// jobspec.Validate first) or validate the spec itself and say so here - do not
// add an exemption without doing one of those two.
//
// Known weakness, accepted, and identical to the weakness on
// TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath: a rename defeats it,
// and an identifier reached reflectively through a string literal is invisible to
// an AST walk. The walk covers the whole module, not just internal/, because
// cmd/relay-server wires the store layer by hand and can reach *store.Queries as
// directly as anything under internal/.
func TestCreateTaskHasNoCallerOutsideJobcreate(t *testing.T) {
	root := repoRoot(t)

	// The single job-creation path. It calls jobspec.Validate before either
	// INSERT, which is the property the whole bound rests on.
	allowed := map[string]bool{
		filepath.Join(root, "internal", "jobcreate", "jobcreate.go"): true,
	}

	for _, ident := range []string{"CreateTask", "CreateTaskWithSource"} {
		offenders, unparseable, err := scanForIdentReferences(root, ident, allowed)
		if err != nil {
			t.Fatalf("walking %s for %s: %v", root, ident, err)
		}
		for _, f := range unparseable {
			t.Errorf("%s could not be parsed, so %s could be referenced there and this guard would not "+
				"see it. Every other file was still scanned.", f, ident)
		}
		if len(offenders) > 0 {
			t.Fatalf("%s inserts a tasks row with a caller-supplied retries and timeout_seconds, and it "+
				"must be called only from internal/jobcreate/jobcreate.go, but it is REFERENCED IN CODE "+
				"in: %v\n"+
				"There is NO CHECK constraint on either column (see this test's comment for why), so "+
				"jobspec.Validate at the single job-creation path is the entire enforcement. Route the "+
				"new caller through jobcreate.CreateJobFromSpec, or validate the spec at the new site "+
				"and record that decision here.", ident, offenders)
		}
	}
}

// TestScanForIdentReferences_FindsAPlantedCreateTaskCallAndHonoursTheAllowList is
// the PERMANENT discriminating input for the guard above.
//
// A guard proved only by planting a call site and reverting it locks nothing in:
// the reverted plant leaves no test behind. This keeps the plant, over a
// synthetic root, so the guard's two halves - "an unlisted reference is
// reported" and "the listed one is not" - are both asserted forever.
//
// THE OFFENDER SORTS FIRST. WalkDir visits in lexical order, and a poisoned
// input placed last cannot distinguish "the walk found it" from "the walk
// stopped before reaching it".
func TestScanForIdentReferences_FindsAPlantedCreateTaskCallAndHonoursTheAllowList(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("aaa_offender.go", "package p\n\nfunc f(q Q) { q.CreateTask() }\n")
	write("internal/jobcreate/jobcreate.go",
		"package jobcreate\n\nfunc g(q Q) { q.CreateTask(); q.CreateTaskWithSource() }\n")

	allowed := map[string]bool{
		filepath.Join(root, "internal", "jobcreate", "jobcreate.go"): true,
	}

	offenders, unparseable, err := scanForIdentReferences(root, "CreateTask", allowed)
	if err != nil {
		t.Fatalf("the walk must not abort: %v", err)
	}
	if len(unparseable) != 0 {
		t.Errorf("unparseable = %v, want none", unparseable)
	}
	if want := []string{"aaa_offender.go"}; !slices.Equal(offenders, want) {
		t.Errorf("offenders = %v, want %v. Either the guard cannot see a plain method call on a "+
			"non-allowed file, or it is reporting the allowed one.", offenders, want)
	}
}
```

- [ ] **Step 2: Run both tests to verify the guard is green and the scanner test discriminates**

Run:

```bash
go test ./internal/store/ -run 'TestCreateTaskHasNoCallerOutsideJobcreate|TestScanForIdentReferences_FindsAPlantedCreateTaskCall' -v
```

Expected: both PASS. `TestCreateTaskHasNoCallerOutsideJobcreate` is **green at HEAD** - it is a guard, and a guard that is red on arrival is a bug report, not a guard. Its evidence is Step 3, not a RED here.

- [ ] **Step 3: Apply MUT-A2 against the real repository and verify the guard reddens**

The permanent test in Step 1 proves the scanner works against a synthetic root. This step proves the **guard** is wired to the real tree with the right allow-list - the direction a synthetic-root test structurally cannot cover.

Add one line to the body of `dispatchOne` in `internal/scheduler/dispatch.go` (any non-test `.go` file outside `internal/jobcreate` will do; this one is chosen because it already imports `store`):

```go
	_ = d.q.CreateTask
```

Verify it applied, then run:

```bash
git --no-pager diff --stat internal/scheduler/dispatch.go   # MUST be non-empty
go test ./internal/store/ -run TestCreateTaskHasNoCallerOutsideJobcreate -v
```

Expected: FAIL, naming the planted path:

```
CreateTask inserts a tasks row with a caller-supplied retries and timeout_seconds, and it must be
called only from internal/jobcreate/jobcreate.go, but it is REFERENCED IN CODE in:
[internal/scheduler/dispatch.go]
```

**If it passes, the guard is not seeing the real tree** - check that `repoRoot` resolved to the worktree and not to `D:/dev/relay`, and that `.claude` pruning is not hiding the file.

- [ ] **Step 4: Restore and re-run**

```bash
git checkout -- internal/scheduler/dispatch.go
git --no-pager diff --stat internal/scheduler/dispatch.go   # MUST be empty
go test ./internal/store/ -run 'TestCreateTaskHasNoCallerOutsideJobcreate|TestScanForIdentReferences_FindsAPlantedCreateTaskCall' -v
```

Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/createtask_guard_test.go
git commit -m "test(store): guard the two task INSERTs against a caller that bypasses jobspec.Validate"
```

---

## Task 6: Give the RetryJobTasks fixture a retry budget

**This task lands BEFORE the predicate and it is not optional.** See finding F4 above. `retryFixture.pending` creates tasks with `Retries: 0`, and two tests in that file call `IncrementTaskRetryCount` on such a task:

- `TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne` calls it with `require.NoError`. Once `AND retry_count < retries` exists, `0 < 0` is false and that becomes `pgx.ErrNoRows`. **Hard RED.**
- `TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected` calls it expecting `ErrNoRows`, and its comment claims the epoch and worker predicates are what reject it. With `retries = 0` the budget predicate alone would reject it, so that assertion would survive both predicates being deleted. **Silent vacuity.**

Giving the fixture a real budget fixes both, and it changes nothing about `RetryJobTasks` itself: no assertion in that file reads `retries`, and `retry_count` is asserted independently.

**Files:**
- Modify: `internal/store/retry_job_tasks_integration_test.go` (the `pending` helper)

- [ ] **Step 1: Change the fixture**

In `internal/store/retry_job_tasks_integration_test.go`, replace the `pending` helper's doc comment and `Retries` value:

```go
// pending creates a task and leaves it at epoch 0 with worker_id NULL.
//
// RETRIES IS 1, NOT 0, AND THAT IS LOAD-BEARING FOR TWO TESTS IN THIS FILE.
// IncrementTaskRetryCount carries `AND retry_count < retries`, so a task created
// with retries = 0 is refused by the BUDGET predicate before any other predicate
// is consulted. That would break
// TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne outright (its
// agent-retry step asserts NoError) and would silently hollow out
// TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected, whose
// retry leg exists to isolate the epoch and worker predicates and would instead
// pass on the budget alone - green with both of the predicates it names removed.
// Nothing in this file asserts on `retries`, so a budget of 1 costs nothing.
func (f *retryFixture) pending(t *testing.T, name string) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 1,
	})
	require.NoError(t, err)
	return task
}
```

- [ ] **Step 2: Run the package to verify it is still green**

Run (needs Docker; this package spins one Postgres container per test):

```bash
go test -tags integration -p 1 ./internal/store/ -run TestRetryJobTasks -v -timeout 900s
```

Expected: PASS. This change is deliberately behaviour-neutral **today** - the predicate does not exist yet, so nothing about these tests moves. Its verification is Task 7 Step 6, where the same command must still be green after the predicate lands.

- [ ] **Step 3: Commit**

```bash
git add internal/store/retry_job_tasks_integration_test.go
git commit -m "test(store): give the RetryJobTasks fixture a retry budget ahead of the budget predicate"
```

---

## Task 7: Add `AND retry_count < retries` to `IncrementTaskRetryCount` (T-B1)

**Files:**
- Create: `internal/store/increment_task_retry_count_budget_integration_test.go`
- Modify: `internal/store/query/tasks.sql` (`IncrementTaskRetryCount`'s doc block and WHERE clause; one sentence in `UpdateTaskStatus`'s doc block)
- Regenerate: `internal/store/tasks.sql.go` (**via `make generate` only**)

- [ ] **Step 1: Write the failing test**

Create `internal/store/increment_task_retry_count_budget_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows is the
// backlog item's own repro for half B, at the layer where it is reachable.
//
// THE POSITIVE CONTROL IS THE FIRST LEG AND IT IS ALSO THE SETUP, which is why
// this is one test and not two: the only way to reach retry_count == retries
// through production statements is to spend the budget, so the successful retry
// has to happen first anyway. Doing it deliberately also satisfies this file's
// standing convention - a suite where the statement stopped working at all must
// not be able to look like a suite of successful rejections.
//
// EVERY OTHER PREDICATE PASSES ON THE SECOND CALL. The task is freshly
// re-claimed, so the epoch is current, the worker is the assignee and the status
// is 'dispatched'. Only the budget predicate can reject it, which is what makes
// this test discriminating rather than merely red.
func TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Bud", "budget@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "budget-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "bw1", Hostname: "budget-w1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t-budget", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: 1,
	})
	require.NoError(t, err)

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// LEG 1 - POSITIVE CONTROL. The task's one retry, spent legitimately by its
	// own assignee at the current epoch. Asserted in full: this is also the
	// backlog item's "a task with a normal budget still retries exactly as many
	// times as configured" at the statement layer.
	first, err := q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w.ID,
	})
	require.NoError(t, err, "the assignee's own retry at the current epoch, inside budget, must succeed")
	assert.Equal(t, int32(1), first.RetryCount, "the retry burns exactly one")
	assert.Equal(t, "pending", first.Status, "the retry returns the task to the queue")
	assert.False(t, first.WorkerID.Valid, "the retry releases the assignee")
	assert.Equal(t, int32(2), first.AssignmentEpoch, "the retry ends the generation")

	// The dispatcher hands it out again. The budget is now SPENT.
	reclaimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), reclaimed.AssignmentEpoch, "fixture: claim, retry, claim moves the epoch 1 -> 2 -> 3")
	require.Equal(t, "dispatched", reclaimed.Status, "fixture: the row must be non-terminal, or terminality would reject too")
	require.Equal(t, int32(1), reclaimed.RetryCount, "fixture: retry_count must equal retries, or nothing here is at budget")
	require.Equal(t, int32(1), reclaimed.Retries)

	// LEG 2 - THE SUBJECT. Correct id, correct epoch, correct assignee,
	// non-terminal status. At HEAD this SUCCEEDS and leaves retry_count = 2.
	_, err = q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{
		ID: reclaimed.ID, AssignmentEpoch: reclaimed.AssignmentEpoch, WorkerID: w.ID,
	})
	assert.ErrorIs(t, err, pgx.ErrNoRows,
		"a task at retry_count == retries has no retry left, and the STATEMENT must say so - the budget "+
			"was the one part of the caller's decision that lived only in Go, so a second caller could "+
			"take a task past its budget by omission")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	// Non-fatal asserts so a RED run reports every part of the exposure rather
	// than stopping at the first one.
	assert.Equal(t, int32(1), after.RetryCount, "a rejected retry must not burn a retry it does not have")
	assert.Equal(t, "dispatched", after.Status, "a rejected retry must not return the task to the queue")
	assert.Equal(t, int32(3), after.AssignmentEpoch, "a rejected retry must not end the assignment")
	require.True(t, after.WorkerID.Valid)
	assert.Equal(t, w.ID.Bytes, after.WorkerID.Bytes, "a rejected retry must not clear the assignee")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run (needs Docker):

```bash
go test -tags integration -p 1 ./internal/store/ -run TestIncrementTaskRetryCount_BudgetPredicate -v -timeout 300s
```

Expected: **PASS on leg 1, FAIL on leg 2**, with four failures reported together:

```
Error:  Target error should be in err chain:
        expected: "no rows in result set"
        in chain:
Messages: a task at retry_count == retries has no retry left, ...
Error:  Not equal: expected: 1  actual: 2
Messages: a rejected retry must not burn a retry it does not have
Error:  Not equal: expected: "dispatched"  actual: "pending"
Error:  Not equal: expected: 3  actual: 4
```

That is the backlog item's repro, verbatim.

- [ ] **Step 3: Edit the SQL and its doc block**

In `internal/store/query/tasks.sql`:

**(a)** In `IncrementTaskRetryCount`'s WHERE clause, add one line after the status predicate:

```sql
WHERE id = sqlc.arg(id)
  AND assignment_epoch = sqlc.arg(assignment_epoch)
  AND worker_id = sqlc.arg(worker_id)
  AND status IN ('pending', 'dispatched', 'running')
  AND retry_count < retries
RETURNING *;
```

**(b)** Change the doc block's opening sentence from `Three predicates` to `Four predicates`:

```sql
-- Burns one retry on a task whose CURRENT generation just failed, and returns it
-- to the queue. Four predicates, each answering a different question; none is
-- redundant with the others and none may be deleted:
```

**(c)** Add a fourth bullet after the existing `status - TERMINALITY` bullet, keeping the same indentation style as its three siblings:

```sql
--   retry_count    - BUDGET. The task must have a retry left. This completes the
--     statement's precondition: before it, the budget was the one part of the
--     caller's decision that stayed in Go
--     (`terminal && task.RetryCount < task.Retries`, internal/worker/handler.go),
--     so a SECOND caller could take a task past its budget by omission. That was
--     the 2026-08-12 audit's remaining gap at this end of the retry path.
--     Written `retry_count < retries` and NOT `retry_count <= retries - 1`:
--     jobspec.Validate now rejects a negative `retries` at ingest, and this
--     spelling does not depend on that having been true for every row ever
--     written. TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows
--     pins it, and is the only test in the tree that can - see the two paragraphs
--     below for why nothing at the handler layer can distinguish this predicate.
-- THIS PREDICATE CHANGES NO PRODUCTION ROWCOUNT TODAY, and that is checkable
-- rather than hopeful. For it to be the SOLE reason a row fails to match,
-- retry_count would have to advance (or retries shrink) between
-- handleTaskStatus's GetTask and this UPDATE without the epoch moving. Searched
-- by shape across query/: retry_count has exactly TWO writers, this statement
-- (retry_count + 1) and RetryJobTasks (retry_count = 0), and BOTH bump
-- assignment_epoch in the same statement; `retries` has no UPDATE writer at all,
-- only the two INSERTs (CreateTask, CreateTaskWithSource). So wherever this
-- predicate would reject, the currency predicate rejects too and the rowcount is
-- identical. It is a precondition completed for a FUTURE caller, not a behaviour
-- change - which is also why the mutation that removes it can only be caught at
-- the store layer.
-- THE GO GATE IN handleTaskStatus MUST STAY, AND THIS PREDICATE IS NOT A REASON
-- TO DEMOTE IT TO AN EARLY RETURN. If the retry branch were entered for a
-- budget-exhausted task, this statement's refusal would arrive as pgx.ErrNoRows
-- and the handler would RETURN BEFORE UpdateTaskStatus: no `failed`, no
-- finished_at, no FailDependentTasks cascade, no RecomputeJobStatus, no SSE
-- frame, until the coordinator watchdog stamps `timed_out` at
-- RELAY_TASK_MAX_ASSIGNMENT (24h default). Because `retries` defaults to 0 that
-- is EVERY ordinary failing task in the system. It would also mis-COUNT:
-- classifyStatusFenceRejection labels a row that was still writable at T0
-- `raced`, and a budget-exhausted task is `running`, so every routine budget
-- exhaustion would inflate task_status_fence.raced_total - a signal
-- internal/api/server_counters.go publishes as "a concurrent writer ended the
-- generation inside this handler's own read-to-write window" and whose whole
-- value is that it sits near zero.
```

**(d)** In `UpdateTaskStatus`'s doc block, the sentence that currently reads

```
-- That statement now carries the same three predicates, so the two together
-- cover every production writer.
```

becomes

```
-- That statement carries these same three predicates plus a FOURTH on the retry
-- budget (`retry_count < retries`), so the two together cover every production
-- writer.
```

- [ ] **Step 4: Regenerate, and survive the CRLF revert**

This repo is CRLF and sqlc emits LF, so `make generate` rewrites line endings across every generated file. The revert that fixes that has previously discarded a regenerated `.sql.go`, leaving a generated doc comment contradicting its own source. **Run these six commands in order, from Git Bash at the worktree root, and do not skip 5 or 6.**

1. Regenerate:

```bash
make generate
```

2. See everything the generators touched:

```bash
git --no-pager diff --stat
```

3. See what actually changed in CONTENT:

```bash
git --no-pager diff --ignore-all-space --stat
```

Expected: step 3 lists `internal/store/tasks.sql.go` and nothing else. Step 2 may additionally list `internal/store/models.go`, the other `*.sql.go` files, and the `internal/proto/relayv1` bindings.

4. Revert every file that appears in step 2's list but **not** in step 3's - those are LF-only churn:

```bash
git checkout -- <each file from step 2 that is absent from step 3>
```

5. **VERIFY THE REAL CHANGE SURVIVED.** Both of these must print a match:

```bash
grep -n "retry_count < retries" internal/store/tasks.sql.go
grep -n "Four predicates" internal/store/tasks.sql.go
```

The first is the SQL constant; the second is the regenerated doc comment. **If the second prints nothing, step 4 reverted `tasks.sql.go`.** Re-run `make generate` and revert more carefully - a generated doc comment that contradicts the `.sql` it was generated from is the exact defect this step exists to prevent.

6. Confirm no `.sql.go` was hand-edited:

```bash
git --no-pager diff --ignore-all-space -- internal/store/tasks.sql.go
```

Expected: the diff shows the added `AND retry_count < retries` line in the `incrementTaskRetryCount` constant and the rewritten doc comment above `func (q *Queries) IncrementTaskRetryCount`, and nothing else.

- [ ] **Step 5: Run the test to verify it passes**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestIncrementTaskRetryCount_BudgetPredicate -v -timeout 300s
```

Expected: PASS.

- [ ] **Step 6: Run the packages the predicate can reach**

```bash
go test -tags integration -p 1 ./internal/store/ -timeout 900s
go test -tags integration -p 1 ./internal/worker/ -timeout 900s
go test ./internal/worker/ -run TestTaskStatusWritableSetMatchesTheSQLAllowList -v
go test ./... -timeout 120s
```

Expected: all PASS.

- `internal/store` is where finding F4 lives. Task 6 already repaired it; if `TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne` is red here with `Received unexpected error: no rows in result set`, Task 6 was skipped or reverted.
- `TestTaskStatusWritableSetMatchesTheSQLAllowList` parses `tasks.sql` and requires **exactly one** `status IN (...)` clause per statement in the executable text. The new predicate adds none, and every word added to the doc block is stripped as a comment, so this must stay green. If it fails with "carries 2 `status IN (...)` clauses", something was added to the WHERE clause rather than the comment.

- [ ] **Step 7: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/store/increment_task_retry_count_budget_integration_test.go
git commit -m "fix(store): IncrementTaskRetryCount refuses a task whose retry budget is spent"
```

---

## Task 8: Pin the Go gate (T-B3)

**This is the single most important test in the slice.** The predicate from Task 7 and the Go gate look redundant and are not: with the predicate in place, deleting the gate does not let an exhausted task burn a retry - it makes every budget-exhausted terminal report **return before `UpdateTaskStatus`**, so the task never reaches `failed`, never gets `finished_at`, never cascades to its dependents and never recomputes its job. The assertion below is therefore that **the task ENDS TERMINAL**, not that `retry_count` stopped moving - the predicate alone would satisfy the latter.

**Files:**
- Create: `internal/worker/handler_retry_budget_integration_test.go`

- [ ] **Step 1: Write the test**

Create `internal/worker/handler_retry_budget_integration_test.go`:

```go
//go:build integration

package worker_test

import (
	"context"
	"testing"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claimRunAndFail drives one full generation of a task through the handler:
// claim it for w, report RUNNING, then report FAILED. Returns the task row as it
// stands afterwards.
//
// It goes through ClaimTaskForWorker and HandleTaskStatus rather than planting
// rows, so every epoch, every started_at and every retry_count in these tests was
// produced by the production path.
func claimRunAndFail(t *testing.T, ctx context.Context, q *store.Queries, h *worker.Handler,
	taskID, w pgtype.UUID) store.Task {
	t.Helper()
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w})
	require.NoError(t, err)
	idStr := h.UUIDStringForTest(taskID)

	h.HandleTaskStatus(ctx, w, &relayv1.TaskStatusUpdate{
		TaskId: idStr, Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch: int64(claimed.AssignmentEpoch),
	})
	running, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "running", running.Status, "fixture: the assignee's RUNNING must land")

	h.HandleTaskStatus(ctx, w, &relayv1.TaskStatusUpdate{
		TaskId: idStr, Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch: int64(claimed.AssignmentEpoch),
	})
	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	return after
}

// TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection
// is the permanent guard on `task.RetryCount < task.Retries` in
// internal/worker/handler.go.
//
// WHAT IT PROTECTS, AND WHY THE SQL PREDICATE DOES NOT MAKE THE GATE REDUNDANT.
// IncrementTaskRetryCount now carries `AND retry_count < retries`, so deleting
// the Go term does NOT let an exhausted task burn another retry. What it does is
// send every budget-exhausted terminal report INTO the retry branch, where the
// refusal arrives as pgx.ErrNoRows and the branch RETURNS - before
// UpdateTaskStatus. The task is then never marked failed, never stamped
// finished_at, never cascades through FailDependentTasks and never recomputes its
// job; it sits `running` until the coordinator watchdog stamps `timed_out` at
// RELAY_TASK_MAX_ASSIGNMENT, 24 hours later by default. Because `retries`
// defaults to 0, that is every ordinary failing task in the system.
//
// THE COUNTER ASSERTION IS THE DISCRIMINATING ONE. The three assertions above it
// are also killed by several existing tests in this package (any of them whose
// positive control drives a retries=0 task to `failed`), so on their own they
// would not tell you this test had done anything the tree did not already do.
// The fence counters are different: classifyStatusFenceRejection labels a row
// that was still writable at T0 `raced`, and a budget-exhausted task is
// `running` - so a gate-less handler puts a steady, deterministic,
// agent-driven, unbudgeted increment onto raced_total, whose published meaning
// (internal/api/server_counters.go) is "a concurrent writer ended the generation
// inside this handler's own read-to-write window" and whose whole operator value
// is that it sits near zero. Nothing else in the tree asserts that.
func TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	// retries = 1: one generation gets retried, the second is at budget.
	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "budget-gate", 1)

	// GENERATION 1 - the retry is legitimate and must be taken.
	retried := claimRunAndFail(t, ctx, q, h, taskID, w1)
	require.Equal(t, "pending", retried.Status, "fixture: an in-budget failure must requeue the task")
	require.Equal(t, int32(1), retried.RetryCount, "fixture: it must burn exactly one retry")
	require.Equal(t, int32(2), retried.AssignmentEpoch, "fixture: the retry ends the generation")

	// GENERATION 2 - the budget is spent. THE SUBJECT.
	final := claimRunAndFail(t, ctx, q, h, taskID, w1)

	// Non-fatal asserts so a RED run reports every part of the exposure.
	assert.Equal(t, "failed", final.Status,
		"THE HEADLINE: a task whose retry budget is spent must END TERMINAL. Not 'retry_count stopped "+
			"moving' - the SQL predicate alone gives you that while the row sits running for 24 hours.")
	assert.True(t, final.FinishedAt.Valid,
		"a terminal transition must stamp finished_at, which is what every duration and every 'when did "+
			"this end' read in the product depends on")
	assert.Equal(t, int32(1), final.RetryCount,
		"and it must not have burned a retry it does not have")
	assert.Equal(t, int32(3), final.AssignmentEpoch,
		"a terminal transition must NOT bump the epoch - that would close the trailing-log flush")

	assert.Equal(t, worker.TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"THE DISCRIMINATING ASSERTION. A budget exhaustion is the NORMAL end of a task's life: "+
			"deterministic, single-writer, and nothing to do with a concurrent writer. It must never "+
			"reach a fence counter. Without the Go gate it lands on raced_total, which "+
			"GET /v1/server/counters publishes as a FLOOR on concurrent-writer activity - a signal whose "+
			"entire value is that it sits near zero, driven steadily by every failing task in the fleet.")
}
```

- [ ] **Step 2: Run the test to verify it passes**

```bash
go test -tags integration -p 1 ./internal/worker/ -run TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection -v -timeout 300s
```

Expected: PASS. **This test is green on arrival**, because the gate it protects is already correct. Its evidence is Step 3.

- [ ] **Step 3: Apply MUT-B1, verify it applied, and observe the RED**

In `internal/worker/handler.go`, change

```go
	if terminal && task.RetryCount < task.Retries {
```

to

```go
	if terminal {
```

Verify and run:

```bash
git --no-pager diff --stat internal/worker/handler.go     # MUST be non-empty
git --no-pager diff -- internal/worker/handler.go         # MUST show exactly this line
go test -tags integration -p 1 ./internal/worker/ -run TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection -v -timeout 300s
```

Expected: FAIL, with all five assertions reported:

```
Error:  Not equal: expected: "failed"  actual: "running"
Messages: THE HEADLINE: a task whose retry budget is spent must END TERMINAL. ...
Error:  Should be true
Messages: a terminal transition must stamp finished_at, ...
Error:  Not equal:
        expected: worker.TaskStatusFenceCounts{Raced:0x0, Duplicate:0x0, Conflicting:0x0}
        actual  : worker.TaskStatusFenceCounts{Raced:0x1, Duplicate:0x0, Conflicting:0x0}
Messages: THE DISCRIMINATING ASSERTION. ...
```

**EXPECTED COLLATERAL, so a wide run does not read as a botched mutation.** MUT-B1 also reddens at least these, and that is correct - it is a genuinely destructive mutation:

```bash
go test -tags integration -p 1 ./internal/worker/ -timeout 900s
```

- `TestHandleTaskStatus_RejectsFailedFromANonAssigneeAndDoesNotCascade` (its positive control drives a `retries = 0` task to `failed`; with the gate gone the branch is entered, `0 < 0` fails in SQL, and the task never reaches `failed`)
- `TestHandleTaskStatus_ASecondTerminalFromTheAssigneeDoesNotOverwriteOrCascade` (same positive control shape)
- `TestHandleTaskStatus_TheRetryArmCountsItsOwnRejections` and `TestHandleTaskStatus_TheUpdateArmCountsEachRejectionReasonAndASuccessCountsNothing` in the default lane, which route by `terminal && budget`

**What matters is that the test above is among them.** If it is not, the mutation did not apply - re-check `git diff`.

- [ ] **Step 4: Restore and re-run**

```bash
git checkout -- internal/worker/handler.go
git --no-pager diff --stat internal/worker/handler.go     # MUST be empty
go test -tags integration -p 1 ./internal/worker/ -timeout 900s
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler_retry_budget_integration_test.go
git commit -m "test(worker): a budget-exhausted task still ends terminal and touches no fence counter"
```

---

## Task 9: Positive control on the whole retry loop (T-B4)

**Files:**
- Modify: `internal/worker/handler_retry_budget_integration_test.go` (append one test; `claimRunAndFail` from Task 8 is reused)

- [ ] **Step 1: Write the test**

Append to `internal/worker/handler_retry_budget_integration_test.go`:

```go
// TestHandleTaskStatus_ATaskRetriesExactlyItsBudgetThenGoesTerminalAndCascades is
// the backlog item's own positive control, in its own wording: "a task with a
// normal budget still retries exactly as many times as configured and then goes
// terminal."
//
// IT IS THE OFF-BY-ONE TEST. The `< / <=` boundary in the Go gate is the one
// place where a plausible edit changes how many times work is repeated in
// production, and this is what pins it: retries = 2 must produce exactly TWO
// retries and a third failure that ends the task, not three retries and a fourth.
//
// The dependent and the job status are asserted because the retry branch's
// `return` skips ALL of them: FailDependentTasks, RecomputeJobStatus,
// NotifyTaskCompleted and the SSE publish are downstream of UpdateTaskStatus, so
// "the row says failed" understates what a gate defect actually costs. A
// dependent left `pending` behind a `failed` dependency is unreachable forever -
// GetEligibleTasks will not dispatch a task whose dependency is not `done`.
func TestHandleTaskStatus_ATaskRetriesExactlyItsBudgetThenGoesTerminalAndCascades(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	jobID, taskAID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "budget-loop", 2)
	taskB, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "t-b", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: taskB.ID, DependsOnTaskID: taskAID,
	}))

	// RETRY 1 of 2.
	first := claimRunAndFail(t, ctx, q, h, taskAID, w1)
	assert.Equal(t, "pending", first.Status, "retry 1 must requeue the task")
	assert.Equal(t, int32(1), first.RetryCount)
	assert.Equal(t, int32(2), first.AssignmentEpoch)

	// RETRY 2 of 2.
	second := claimRunAndFail(t, ctx, q, h, taskAID, w1)
	assert.Equal(t, "pending", second.Status, "retry 2 must requeue the task")
	assert.Equal(t, int32(2), second.RetryCount)
	assert.Equal(t, int32(4), second.AssignmentEpoch)

	dependentBefore, err := q.GetTask(ctx, taskB.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", dependentBefore.Status,
		"fixture: the dependent must still be pending, or the cascade assertion below proves nothing")

	// THE THIRD FAILURE. The budget is spent; this one must END the task.
	final := claimRunAndFail(t, ctx, q, h, taskAID, w1)
	assert.Equal(t, "failed", final.Status,
		"exactly as many retries as configured, THEN terminal - not one more")
	assert.Equal(t, int32(2), final.RetryCount,
		"retry_count must stop at retries. A `<=` on either side of this budget check gives a third "+
			"retry, which is one extra full execution of the task's commands on real hardware.")
	assert.True(t, final.FinishedAt.Valid, "the terminal transition must stamp finished_at")
	assert.Equal(t, int32(5), final.AssignmentEpoch,
		"claim/retry twice then claim once more is 1->2->3->4->5, and the terminal transition adds none")

	dependent, err := q.GetTask(ctx, taskB.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", dependent.Status,
		"FailDependentTasks runs AFTER UpdateTaskStatus, so a retry branch that swallows the terminal "+
			"report leaves this pending behind a failed dependency - unreachable forever, because "+
			"GetEligibleTasks will not dispatch a task whose dependency is not done")

	job, err := q.GetJob(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, "failed", job.Status,
		"and the job must reach a terminal status: RecomputeJobStatus is downstream of the same write")

	assert.Equal(t, worker.TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"three generations, every one of them a legitimate report from the task's own assignee at the "+
			"current epoch: not one of them may be counted as a fence rejection")
}
```

- [ ] **Step 2: Run the test to verify it passes**

```bash
go test -tags integration -p 1 ./internal/worker/ -run TestHandleTaskStatus_ATaskRetriesExactlyItsBudgetThenGoesTerminalAndCascades -v -timeout 300s
```

Expected: PASS. Green on arrival, like Task 8. Its evidence is Step 3.

- [ ] **Step 3: Apply MUT-B2, verify it applied, and observe the RED**

In `internal/worker/handler.go`, change

```go
	if terminal && task.RetryCount < task.Retries {
```

to

```go
	if terminal && task.RetryCount <= task.Retries {
```

Verify and run:

```bash
git --no-pager diff --stat internal/worker/handler.go     # MUST be non-empty
git --no-pager diff -- internal/worker/handler.go         # MUST show `<` -> `<=` on exactly this line
go test -tags integration -p 1 ./internal/worker/ -run 'TestHandleTaskStatus_(ATaskRetriesExactlyItsBudgetThenGoesTerminalAndCascades|AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection)' -v -timeout 300s
```

Expected: **both** FAIL. The third failure enters the retry branch (`2 <= 2`), the SQL budget predicate refuses it, the branch returns, and the task stays `running`:

```
Error:  Not equal: expected: "failed"  actual: "running"
Messages: exactly as many retries as configured, THEN terminal - not one more
Error:  Not equal: expected: "failed"  actual: "pending"
Messages: FailDependentTasks runs AFTER UpdateTaskStatus, ...
Error:  Not equal:
        expected: worker.TaskStatusFenceCounts{Raced:0x0, ...}
        actual  : worker.TaskStatusFenceCounts{Raced:0x1, ...}
```

- [ ] **Step 4: Restore and re-run**

```bash
git checkout -- internal/worker/handler.go
git --no-pager diff --stat internal/worker/handler.go     # MUST be empty
go test -tags integration -p 1 ./internal/worker/ -timeout 900s
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler_retry_budget_integration_test.go
git commit -m "test(worker): a task retries exactly its budget, then goes terminal and cascades"
```

---

## Task 10: Correct every comment the change falsified

**This is not commentary.** Wrong prose about correct code is this project's most-recurring defect class, eight iterations running, and this change falsifies four sentences outside `tasks.sql` plus one whose truth condition has quietly widened. Each is listed with its exact current wording so it can be found by search rather than by line number.

**Files:**
- Modify: `internal/api/server_counters.go`
- Modify: `internal/worker/taskstatus_fence_counters.go`
- Modify: `internal/store/store_test.go` (two comments on `TestIncrementTaskRetryCount_StatusEpochAndAssigneeGuarded`)
- Modify: `internal/store/incrementtaskretrycount_guard_test.go`
- Modify: `internal/worker/handler.go` (comment addition at the retry gate; **no code change**)

- [ ] **Step 1: Fix the four falsified comments**

**(a) `internal/api/server_counters.go`**, in the `taskStatusFenceSection` doc block. Find:

```go
// WHY THE ARMS ARE NOT SPLIT BY STATEMENT, which is the split the filing item
// proposed: IncrementTaskRetryCount and UpdateTaskStatus carry the IDENTICAL
// three predicates, which of the two runs is decided by the reported status and
// the row's retry budget rather than by anything about the rejection, and both
// mean the same thing to an operator - the agent's report of this task's outcome
// was discarded. Splitting by reason answers the question the item actually
// asked (which of these is alarming); splitting by statement does not.
```

Replace with:

```go
// WHY THE ARMS ARE NOT SPLIT BY STATEMENT, which is the split the filing item
// proposed: IncrementTaskRetryCount and UpdateTaskStatus carry the IDENTICAL
// THREE FENCE predicates - epoch, worker id, terminality - which of the two runs
// is decided by the reported status and the row's retry budget rather than by
// anything about the rejection, and both mean the same thing to an operator - the
// agent's report of this task's outcome was discarded. Splitting by reason
// answers the question the item actually asked (which of these is alarming);
// splitting by statement does not.
//
// IncrementTaskRetryCount carries a FOURTH predicate, `retry_count < retries`,
// and it can never reach these counters. The Go gate in handleTaskStatus refuses
// to enter the retry branch on an exhausted budget, so the statement is not
// called at all. That is deliberate rather than incidental: a budget exhaustion
// is deterministic, single-writer and the normal end of a task's life, and
// classifyStatusFenceRejection would label it `raced` - putting a steady,
// agent-driven, unbudgeted increment on the one key here that is meant to sit
// near zero. See the gate's own comment in internal/worker/handler.go.
```

**(b) `internal/worker/taskstatus_fence_counters.go`**, in the `TaskStatusFenceCounts` doc block. Find:

```go
// A FINER SPLIT - WHICH OF THE THREE SQL PREDICATES ACTUALLY FIRED - IS
```

Replace with:

```go
// A FINER SPLIT - WHICH SQL PREDICATE ACTUALLY FIRED - IS
```

(The count is dropped rather than corrected: the two statements no longer carry the same number, and `internal/api/server_counters.go`'s parallel paragraph already spells it without one.)

**(c) `internal/store/store_test.go`**, the header of `TestIncrementTaskRetryCount_StatusEpochAndAssigneeGuarded`. Find:

```go
// IncrementTaskRetryCount burns one retry on a task whose CURRENT generation
// just failed, and returns it to the queue. Three predicates guard it - epoch
// (currency), worker_id (identity) and status (terminality) - and every case
// below names the one that rejects it, because a case that could be rejected by
// two predicates isolates neither.
```

Replace with:

```go
// IncrementTaskRetryCount burns one retry on a task whose CURRENT generation
// just failed, and returns it to the queue. FOUR predicates guard it - epoch
// (currency), worker_id (identity), status (terminality) and retry_count
// (budget) - and every case below names the one that rejects it, because a case
// that could be rejected by two predicates isolates neither.
//
// THE BUDGET PREDICATE IS NOT ISOLATED BY ANY CASE IN THIS TEST, deliberately.
// Every task here is created with Retries: 1 and every call is made at
// retry_count = 0, so the budget predicate PASSES throughout and each case still
// isolates the predicate it names. The budget predicate's own cases live in
// TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows
// (increment_task_retry_count_budget_integration_test.go). Keep the Retries: 1:
// dropping it to 0 would let the budget predicate reject every case here and
// silently make the whole test vacuous.
```

**(d) `internal/store/store_test.go`**, Case 5's comment in the same test. Find:

```go
	// Read this case honestly: it does NOT isolate one predicate. The first retry
	// both bumped the epoch and NULLed worker_id, so epoch and worker each reject
	// it independently, and it goes red only if BOTH are removed (matrix row M6).
	// That is the same defense-in-depth shape as the two .Valid checks in
	// handleTaskStatus's Go gate. Do not read it as an epoch test.
```

Replace with:

```go
	// Read this case honestly: it does NOT isolate one predicate, and it now
	// isolates even less than it used to. The first retry bumped the epoch, NULLed
	// worker_id AND spent the task's only retry, so epoch, worker and BUDGET each
	// reject it independently: it goes red only if all THREE are removed (matrix
	// row M6 covered the first two). That is the same defense-in-depth shape as
	// the two .Valid checks in handleTaskStatus's Go gate. Do not read it as an
	// epoch test.
```

**(e) `internal/store/incrementtaskretrycount_guard_test.go`**, in the guard's doc block. Find:

```go
// IncrementTaskRetryCount (query/tasks.sql) is the AGENT-DRIVEN retry. Its three
// predicates - assignment_epoch, worker_id, and
// status IN ('pending','dispatched','running') - are the exact inverse of an
// operator re-run's preconditions: POST /v1/jobs/{id}/retry reopens tasks that
// ARE terminal and has no worker identity to supply, so both the status and the
// worker predicate would reject every call it made.
```

Replace with:

```go
// IncrementTaskRetryCount (query/tasks.sql) is the AGENT-DRIVEN retry. Its four
// predicates - assignment_epoch, worker_id,
// status IN ('pending','dispatched','running') and retry_count < retries - are
// the exact inverse of an operator re-run's preconditions:
// POST /v1/jobs/{id}/retry reopens tasks that ARE terminal and has no worker
// identity to supply, so both the status and the worker predicate would reject
// every call it made, and the budget predicate would additionally refuse any task
// whose retries the agent path had already spent - which is precisely the task an
// operator presses Retry on.
```

- [ ] **Step 2: Add the gate comment in `internal/worker/handler.go`**

**No code changes in this step.** Immediately above `if terminal && task.RetryCount < task.Retries {`, after the existing "Retry if applicable" paragraph, insert:

```go
	// THE BUDGET TERM HAS A SECOND JOB SINCE THE SQL PREDICATE LANDED, and it is
	// not the one the statement duplicates. IncrementTaskRetryCount now carries
	// `AND retry_count < retries` too, so deleting `task.RetryCount <
	// task.Retries` does NOT let an exhausted task burn another retry - the
	// statement refuses it. What it does is send every budget-exhausted terminal
	// report INTO this branch, where the refusal arrives as pgx.ErrNoRows and the
	// branch RETURNS, before UpdateTaskStatus. The task is then never marked
	// failed, never stamped finished_at, never cascaded through
	// FailDependentTasks and never recomputed; it sits there until the
	// coordinator watchdog stamps `timed_out` at RELAY_TASK_MAX_ASSIGNMENT (24h
	// default). Because `retries` defaults to 0, that is EVERY ordinary failing
	// task in the system.
	//
	// And the COUNT would be wrong as well as the state.
	// classifyStatusFenceRejection returns fenceReasonRaced for any row that was
	// still writable at T0, and a budget-exhausted task is `running` - so each of
	// those refusals would land on raced_total, whose published meaning
	// (internal/api/server_counters.go) is "a concurrent writer ended the
	// generation inside this handler's own read-to-write window" and which is
	// documented as a FLOOR on concurrent-writer activity. A budget exhaustion is
	// none of those things. Keeping this term is what makes that unreachable,
	// because the statement is never called at all.
	//
	// Pinned by
	// TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection
	// (handler_retry_budget_integration_test.go), whose fence-counter assertion is
	// the only thing in the tree that discriminates this second job.
```

- [ ] **Step 3: Verify no falsified wording survives, and that nothing compiled differently**

Run from Git Bash at the worktree root:

```bash
rg -n -i "three predicates|THREE SQL PREDICATES|Three predicates guard|Its three predicates" \
   --glob '!docs/**' --glob '!ROADMAP.md' --glob '!CLAUDE.md' .
```

Expected: **zero hits.** (`docs/` holds historical specs and plans that are entitled to describe the tree as it was; `ROADMAP.md` and `CLAUDE.md` are the conductor's to update, and neither currently makes a false statement - `CLAUDE.md`'s epoch-fence bullet says the statement "fences on epoch, `worker_id` and terminality", which stays true, merely incomplete.)

Then:

```bash
go build ./...
go vet ./...
go vet -tags integration ./...
go test ./... -timeout 120s
```

Expected: all clean. `internal/worker/handler.go` gained comment lines only; if anything behaves differently, a code line was touched.

- [ ] **Step 4: Commit**

```bash
git add internal/api/server_counters.go internal/worker/taskstatus_fence_counters.go \
        internal/worker/handler.go internal/store/store_test.go \
        internal/store/incrementtaskretrycount_guard_test.go
git commit -m "docs: correct every comment the fourth retry predicate falsified"
```

---

## Task 11: Document the two bounds in README

**Files:**
- Modify: `README.md` (the job-spec field table, rows `tasks[].timeout_seconds` and `tasks[].retries`)

- [ ] **Step 1: Edit the two rows**

In `README.md`'s job-spec field table (the one whose header is `| Field | Required | Description |` and whose rows start `tasks[].name`), replace:

```
| `tasks[].timeout_seconds` | No | Kill task after this many seconds |
| `tasks[].retries` | No | Retry up to this many times on failure (default 0) |
```

with:

```
| `tasks[].timeout_seconds` | No | Kill task after this many seconds. Max `604800` (7 days); a larger or negative value is rejected at submission. **Omitted or `0` both mean "no deadline"** - the field is optional and `0` is its second spelling. Independent of `RELAY_TASK_MAX_ASSIGNMENT`, which bounds how long a task may stay ASSIGNED rather than how long it may RUN; a task whose own timeout exceeds that cap is simply swept by the other arm. |
| `tasks[].retries` | No | Retry up to this many times on failure (default `0`, max `10`). A larger or negative value is rejected at submission. There is no backoff between a failed task and its redispatch, so a deterministically-failing command burns the whole budget in seconds; for a contended resource use a reservation rather than a large retry count. |
```

Both bounds are enforced in `jobspec.Validate` and therefore apply identically to `POST /v1/jobs`, `POST /v1/scheduled-jobs`, `relay submit`, the MCP submit tool and the cron scheduler.

- [ ] **Step 2: Verify the rendered table and the contract it now states**

```bash
rg -n "tasks\[\]\.(retries|timeout_seconds)" README.md
```

Expected: exactly two hits, each a single table row with three `|`-delimited cells (a stray newline inside a cell breaks the table silently). Read both back against `internal/jobspec/jobspec.go`: the numbers, the "omitted or 0" claim, and the "rejected at submission" claim must each be true of the code as written. **A wrong contract in docs is a defect in this project's terms** - consumers implement against this prose and no test covers it.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: state the retries and timeout_seconds bounds in the job-spec table"
```

---

## Task 12: Mutation battery and gates

**Files:** none modified permanently. Every mutation is applied, measured, and reverted with `git checkout --`.

**The rules for every row.** Apply the mutation; run `git --no-pager diff --stat <file>` and confirm it is **non-empty**; run the named test; record the result; `git checkout -- <file>`; confirm the stat is now empty. A mutation that silently fails to apply reports as "survived", and CRLF has broken four in a row on this repo, which is why row 0 exists.

- [ ] **Step 1: Run the battery**

| # | Mutation | File | Must kill | Notes |
|---|---|---|---|---|
| 0 | **CONTROL, run first.** `maxRetries = 10` -> `maxRetries = 0` | `internal/jobspec/jobspec.go` | `TestValidate_BoundaryValuesAreAccepted/retries_exactly_at_the_cap` **and** `/retries_at_zero_the_default` stays green | If nothing dies, the harness is broken - stop and fix that before reading any row below |
| 1 | Delete the budget term: `if terminal && task.RetryCount < task.Retries {` -> `if terminal {` | `internal/worker/handler.go` | `TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection` | Wide collateral is EXPECTED - see Task 8 Step 3 for the named list. Confirm the test above is among the failures. |
| 2 | Delete `AND retry_count < retries` from the SQL, then `make generate` + the Task 7 Step 4 procedure | `internal/store/query/tasks.sql` | `TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows` | The revert here is `git checkout -- internal/store/query/tasks.sql internal/store/tasks.sql.go`, and both must come back |
| 3a | SQL `retry_count < retries` -> `retry_count <= retries`, then `make generate` | `internal/store/query/tasks.sql` | `TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows` | **NOT T-B4.** The spec names T-B4 here and that is wrong: the Go gate short-circuits, so the handler-layer outcome is byte-identical and this mutation looks like it did nothing. Only the store layer can see it. |
| 3b | Go `task.RetryCount < task.Retries` -> `<=` | `internal/worker/handler.go` | `TestHandleTaskStatus_ATaskRetriesExactlyItsBudgetThenGoesTerminalAndCascades` **and** `TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection` | Both, and both for the same reason: the branch is entered, SQL refuses, the handler returns before `UpdateTaskStatus` |
| 4a | `maxRetries = 10` -> `11` | `internal/jobspec/jobspec.go` | `TestValidate_RetriesAndTimeoutOutOfRangeAreRejected/retries_one_over_the_cap` | **The spec has 4a and 4b inverted.** Raising the cap accepts a value the rejection table refuses. |
| 4b | `maxRetries = 10` -> `9` | `internal/jobspec/jobspec.go` | `TestValidate_BoundaryValuesAreAccepted/retries_exactly_at_the_cap` | And `TestCreateJob_RetriesAndTimeoutBoundsAreEnforcedOnTheWire`'s 201 leg, and `TestIntegration_SubmitSurfacesTheServersOutOfRangeRefusal`'s positive control |
| 5a | `maxTimeoutSeconds = 604800` -> `604801` | `internal/jobspec/jobspec.go` | `TestValidate_RetriesAndTimeoutOutOfRangeAreRejected/timeout_one_over_the_cap` | |
| 5b | `maxTimeoutSeconds = 604800` -> `604799` | `internal/jobspec/jobspec.go` | `TestValidate_BoundaryValuesAreAccepted/timeout_seconds_exactly_at_the_cap` | |
| 6 | Reject nil: `if ts.TimeoutSeconds != nil && (...)` -> `if ts.TimeoutSeconds == nil \|\| (...)` | `internal/jobspec/jobspec.go` | `TestValidate_BoundaryValuesAreAccepted/timeout_seconds_omitted_entirely` | The `*int32` nil case the backlog item names by hand |
| 7 | Drop the offending task's name: `"task %s: retries must be between 0 and %d"` -> `"retries must be between 0 and %d"` (and fix the args) | `internal/jobspec/jobspec.go` | `TestValidate_RetriesAndTimeoutOutOfRangeAreRejected` (all three retries rows) | Pins the per-task naming the item asks for, which a `require.Error`-only test would miss |
| 8 | Plant `_ = d.q.CreateTask` in `dispatchOne` | `internal/scheduler/dispatch.go` | `TestCreateTaskHasNoCallerOutsideJobcreate` | Already run in Task 5 Step 3; re-run here so the battery is one record |

The commands, per row:

```bash
# jobspec rows (0, 4a, 4b, 5a, 5b, 6, 7)
go test ./internal/jobspec/ -v

# store rows (2, 3a)
go test -tags integration -p 1 ./internal/store/ -run TestIncrementTaskRetryCount_BudgetPredicate -v -timeout 300s

# worker rows (1, 3b)
go test -tags integration -p 1 ./internal/worker/ -run 'TestHandleTaskStatus_(AnExhaustedBudget|ATaskRetriesExactly)' -v -timeout 600s

# guard row (8)
go test ./internal/store/ -run TestCreateTaskHasNoCallerOutsideJobcreate -v
```

- [ ] **Step 2: Confirm the tree is clean**

```bash
git status --short
```

Expected: **empty.** If anything is listed, a mutation was not reverted. Do not proceed until it is.

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

Expected: all green. **If `-race` does not run, say so plainly in the PR body rather than substituting `-count=N`** - repetition raises confidence in flakiness, not in race-freedom, and this slice adds no concurrency anyway, so an honest "did not run" costs nothing.

- [ ] **Step 4: Record the battery in the PR body**

The PR body must carry, at minimum:

1. The table above with a measured result per row (killed / survived), including row 0's control.
2. **The upgrade note**, verbatim in substance: *operators with stored `scheduled_jobs` rows should check for out-of-range `retries` or `timeout_seconds` before upgrading.* A stored spec that exceeds the new bound stops firing silently the moment this deploys - `next_run_at` keeps advancing and every read surface shows a healthy schedule. `docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md` is the fix; this slice ships with `TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard` pinning the hazard instead, per the gate decision.
3. A query an operator can run before upgrading:

```sql
SELECT id, name
FROM scheduled_jobs, jsonb_array_elements(job_spec->'tasks') AS t
WHERE (t->>'retries')::int NOT BETWEEN 0 AND 10
   OR (t->>'timeout_seconds')::int NOT BETWEEN 0 AND 604800;
```

4. A statement of whether `-race` ran.

---

## Conductor steps after the last task

These are outside an engineer's scope and are recorded so they are not forgotten:

- **Close the backlog item** with `/backlog close bug-2026-08-12-retries`. The `git mv` into `docs/backlog/closed/` is required scope, not cleanup; never hand-edit `status:`.
- **`ROADMAP.md`'s Now section** currently leads with this item and says of its sibling "Ship the two together." The human's gate decision was to ship this alone. `/roadmap` regenerates that section from `docs/backlog/`, so run it after the close rather than hand-editing line 39.
- **Two backlog items the spec proposes but does not file** (spec section 10, items 3 and 4), awaiting the human's accept:
  - *No backoff between a failed task and its redispatch.* `IncrementTaskRetryCount` returns the task to `pending` and `handleTaskStatus` immediately calls `NotifyTaskSubmitted`, so ten retries of a millisecond-failing command are ten dispatches inside a second. Bounded now, but still a burst.
  - *A database `CHECK` on `tasks.retries` / `tasks.timeout_seconds`*, if the human wants the guarantee Q6 declines. It must decide clamp-versus-fail for pre-existing rows (migration `000019` set the precedent with a normalizing `UPDATE` before constraining) and must not share a commit with the validator, so a rollback of one is not a rollback of both.
- **Phase 6 proposals** worth considering after the retro: `retryFixture.pending`'s `Retries: 0` was invisible to every reading-based lens and to CI, because `.github/workflows/go-ci.yml` never calls `make test-integration`. That is already filed as `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` and this slice is a fresh, concrete instance of its cost - worth appending to that item rather than filing a new one.
