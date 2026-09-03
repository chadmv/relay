# Prepare-failure visibility - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a task's prepare phase fails, the cause is visible in three places it is invisible today - the task log through the API/CLI/SPA, the classified p4 message for a full workspace volume, and the agent's own host log.

**Architecture:** Three independent slices in two lanes. Slice 1 adds one new `AppendTaskLog` write inside `handleTaskStatus`, above the retry branch, fenced exactly as `handleTaskLog`'s is, publishing its SSE frame before the status frame. Slice 2 adds one `case` to `classifyP4Error`. Slice 3 adds five `log.Printf` lines to `Runner.Run` and three `progress` lines around `SyncStream` in the Perforce provider. No SQL, no migration, no proto, no frontend.

**Tech Stack:** Go 1.26, testify, testcontainers-go (the `internal/worker` integration lane), Postgres 16. **No `make generate` step anywhere in this plan** - no `.sql` and no `.proto` file is touched, so the sqlc/buf CRLF hazard does not arise. If you find yourself running `make generate`, you have gone outside the plan.

**Spec:** `docs/superpowers/specs/2026-09-03-prepare-failure-visibility.md` (autonomous gate; its section 8 decisions all stand, and its section 10 open question is decided below).

**Backlog items this closes (all three, via `/backlog close`, by the conductor):**

- `docs/backlog/bug-2026-09-03-prepare-failure-error-message-is-discarded.md` (slice 1)
- `docs/backlog/feature-2026-09-03-classify-out-of-disk-p4-errors.md` (slice 2)
- `docs/backlog/feature-2026-09-03-agent-task-lifecycle-logging.md` (slice 3)

**In-tree exemplars this plan copies:**

- Lane A test scaffolding: `internal/worker/handler_taskstatus_integration_test.go`'s `seedTaskAndTwoWorkers`, `internal/worker/handler_tasklog_integration_test.go`'s `seedClaimedTask` and `captureLog`, and `internal/worker/handler_test.go`'s `newTestStore`.
- Lane A fence-argument shape: `handleTaskLog`'s `store.AppendTaskLogParams` literal in `internal/worker/handler.go`.
- Lane B slice 3 provider test scaffolding: `internal/agent/source/perforce/perforce_test.go`'s `TestProvider_PrepareCreatesClientAndSyncs` fixture block.

---

## Slice independence declaration

**This is a two-lane, two-PR, single-session plan. It has NO stages and must NOT be handed to `/backlog phases`.** Every unit here lands in this session; there is no unit deferred to a later one.

**There is no frontend work in any slice.** Nothing under `web/` is read, edited, rebuilt or committed. `web/dist` must not be touched. **Do not dispatch `relay-frontend-engineer`.** Phase 3 is two backend lanes.

**Lane A and Lane B are fully independent and MUST run in parallel, in two separate git worktrees**, dispatched to two `relay-backend-engineer` agents in one message.

| Lane | Slices | Files it may create or modify | Gate |
|---|---|---|---|
| **A** | 1 | `internal/worker/handler.go`, `internal/worker/handler_taskstatus_errormessage_integration_test.go` (**new**), `README.md` | `go test ./internal/worker/...` plus the `internal/worker` integration lane |
| **B** | 2 then 3 | `internal/agent/source/perforce/diagnostics.go`, `internal/agent/source/perforce/diagnostics_test.go`, `internal/agent/source/perforce/perforce.go`, `internal/agent/source/perforce/fixtures_test.go`, `internal/agent/source/perforce/perforce_progress_test.go` (**new**), `internal/agent/runner.go`, `internal/agent/runner_lifecycle_log_test.go` (**new**) | `go test ./internal/agent/...` |

**No file appears in both lanes.** I checked this explicitly, including the two that would be the obvious collisions:

- `README.md` is **Lane A only**. Slice 3 requires no README change (spec 6.4) and slice 2 requires none (spec 5.3). If a Lane B engineer edits README, that is a merge conflict the plan did not sanction - do not.
- `internal/agent/source/perforce/` is **Lane B only**, and slices 2 and 3 share that Go *package* (spec section 7), which is exactly why they share one lane and one worktree and are sequenced 2 then 3.

Lane B's internal ordering is load-bearing for one reason and one reason only: **slice 2's new message is the text slice 3's failure path delivers.** Landing slice 2 first means the combined behaviour is exercised once. Do not interleave them.

**Two PRs, one per lane.** Lane A should land first at integration time - it is what makes every later Perforce prepare failure diagnosable - but neither lane blocks the other's implementation.

**Conductor decisions folded in (do not re-open):**

1. **Spec section 10 is decided YES.** The no-provider `PREPARE_FAILED` gets its own host-log line, as a **fifth** line in slice 3, placed immediately before that send. The operator who fixes it (unset `RELAY_WORKSPACE_ROOT`, or a failed p4 preflight) is standing at that host, and the line is bounded by dispatch rate exactly like the other four.
2. **Spec section 8's five decisions all stand.** `stderr` for the synthesized line; terminal reports only; no T0-writability pre-filter; no counter on the fence-rejection arm; the provider's sync-failure line carries no error text.

---

## What I re-verified in the spec, and the one thing I refuted

The spec's section 9 refutes nine points in the handed-down backlog items. I re-derived the load-bearing ones against the tree rather than taking them on trust. **Symbols, never line numbers** - line citations rot.

### Confirmed against the tree

| Spec claim | How I checked it | Verdict |
|---|---|---|
| `task_logs.stream` admits only `stdout` and `stderr`, so `prepare` is unwritable | `internal/store/migrations/000019_status_vocabulary_checks.up.sql` carries `ADD CONSTRAINT task_logs_stream_check CHECK (stream IN ('stdout','stderr'))`; `internal/store/status_vocabulary_constraints_test.go` asserts an insert of `'syslog'` errors and an insert of `'stderr'` succeeds | **Confirmed.** Both directions pinned. |
| `handleTaskLog` maps only `LOG_STREAM_STDERR` to `"stderr"`, everything else to `"stdout"` | `handleTaskLog` in `internal/worker/handler.go` | **Confirmed.** The item's literal instruction would have produced `stdout`, and its README step would have produced the word `prepare`. |
| `AppendTaskLog`'s fence is **strictly weaker** than `UpdateTaskStatus`'s and `IncrementTaskRetryCount`'s, so no new rejection goes uncounted | Read all three `WHERE` clauses in `internal/store/query/tasks.sql`. `AppendTaskLog`: `t.id`, `t.assignment_epoch`, `t.worker_id`, and `(t.status IN ('pending','dispatched','running') OR t.finished_at > min_finished_at)`. `UpdateTaskStatus`: `id`, `assignment_epoch`, `worker_id`, `status IN ('pending','dispatched','running')`. `IncrementTaskRetryCount`: those four plus `retry_count < retries` | **Confirmed.** The first three conjuncts are identical; the fourth is a strict relaxation (a disjunction whose first arm is the other statement's whole predicate). Append refused implies the following write refused. The converse does not hold - that is the duplicate case spec 4.7 documents. |
| `IncrementTaskRetryCount` bumps `assignment_epoch` | Its `SET` clause contains `assignment_epoch = assignment_epoch + 1`, and it returns the task to `'pending'` with `worker_id = NULL` | **Confirmed.** So an append placed *after* the retry branch cannot run on the retry path (the branch returns) and an append placed after the bump cannot pass the fence at all. The write site must be **above** the branch. |
| `handleTaskStatus` maps `PREPARE_FAILED` onto `"failed"` and never reads `ErrorMessage` | The mapping `switch` in `handleTaskStatus`; `ErrorMessage` appears nowhere under `internal/worker/` | **Confirmed.** Which is why **no existing test can go RED**; the RED must be new. |
| The row is non-terminal during prepare | `handleTaskStatus`'s mapping `switch` has no `case` for `TASK_STATUS_PREPARING` and falls to `default: return`, so the row stays `dispatched` | **Confirmed.** |
| A `Filter{JobID, TaskID}` subscriber receives both frame types on one channel in publish order | `Broker.Subscribe` inserts such a subscriber into **both** `b.subs` and `b.logSubs`; `Broker.Publish` fans a `TypeTaskLog` out of `logSubs` and everything else out of `subs`, both synchronously under one mutex; `TestBroker_JobAndTaskSubscriberReceivesBoth` | **Confirmed.** `HasLogSubscriber` also returns true for such a subscriber, so the publish fast path does not swallow the frame. |
| `taskLogFenceRejects` is published as documented "with no Go-side pre-filter" | `internal/api/server_counters.go`'s `task_log_fence` block says exactly that, twice, and the `task_status_fence` block repeats it as the reason the two are not comparable | **Confirmed.** Folding the new site's rejections in would make a published sentence false. |
| The `updatetaskstatusepoch` guard fails on the *identifier* in any non-test Go file under `internal/` | `internal/store/updatetaskstatusepoch_guard_test.go` walks `internal/`, skips `_test.go` and `internal/store/tasks.sql.go`, and `strings.Contains(string(b), "UpdateTaskStatusEpoch")` | **Confirmed, and the escape is confirmed too.** `internal/worker/taskstatus_fence_counters.go` already refers to that statement by naming the file in lowercase (`internal/store/updatetaskstatusepoch_guard_test.go`), which does not contain the mixed-case identifier. **Copy that.** |
| `RELAY_WORKSPACE_MIN_FREE_GB` exists and README documents it as the free-disk eviction threshold | README's `### Source workspaces` -> `**Eviction.**` bullets: "Oldest workspaces (LRU) when free disk drops below `RELAY_WORKSPACE_MIN_FREE_GB`" | **Confirmed**, and **raise** is indeed the direction that causes eviction. |
| `internal/agent` contains zero `t.Parallel` calls | Searched the package | **Confirmed.** A process-global `log.SetOutput` capture is safe there today. The new tests must not introduce the first `t.Parallel`. |
| The Perforce package has a `fakeRunner` that drives `Prepare` without p4d | `internal/agent/source/perforce/fixtures_test.go`'s `newFakeP4Fixture`, used by `TestProvider_PrepareCreatesClientAndSyncs` | **Confirmed**, with one gap - see R1 below. |

### R1 - REFUTED: the spec's prescribed negative-test assertion is vacuous

**Spec 5.3 says:** *"Each must pass through **unchanged**, asserted by `errors.Is(got, in)` on the same pointer, which is how the existing passthrough arm is written."*

**That is false, and it matters.** `TestClassifyP4Error`'s passthrough arm in `internal/agent/source/perforce/diagnostics_test.go` reads:

```go
if tc.wantSub == "" {
    // Passthrough: implementation must return err unchanged (same pointer).
    if !errors.Is(got, tc.in) {
        t.Errorf("expected passthrough (errors.Is failed); got=%v in=%v", got, tc.in)
    }
    return
}
```

The **comment** says "same pointer". The **code** calls `errors.Is`, which unwraps. `errors.Is(fmt.Errorf("out of disk space ...: %w", in), in)` is **true**. So a negative case added with `wantSub: ""` is green whether the input passes through untouched or is misclassified as a full disk and rewrapped. Under the fork's two-substring mutant (`Contains(msg,"insufficient") || Contains(msg,"space")`), `insufficient permissions on workspace` gets rewrapped - and this assertion does not notice.

This is the exact "a principle in a comment is not a check" shape: the code violates the rule its own comment states correctly.

**Consequence for the plan:** Lane B Task 1 **must first strengthen the passthrough arm to an identity check** (`got != tc.in` fails), and prove the strengthening is meaningful by running the mutation. Adding the negative cases without it would ship three tests that pin nothing. This is folded into Lane B Task 1 below and is the reason that task has an extra step.

### Spec criteria already green at HEAD - do not count these as evidence

- **A3's "all three `task_status_fence` counters unmoved" for a non-assignee.** Already true at HEAD and already pinned by `TestHandleTaskStatus_OnlyTheAssigneeMovesTheFenceCounters` (default lane, `taskstatus_fence_counters_test.go`). What is **new** in A3 is the *rows* clause: the identity gate must also cover the new write. Write A3 so the rows clause is what discriminates, and keep the counter clause as a regression backstop.
- **A4's "zero rows" for a stale epoch.** Zero rows is HEAD's behaviour for every input. A4 needs the **paired positive control in the same test** (the same report at the *current* epoch stores exactly one row) or it is satisfied by a handler that does nothing at all. Same for A8, A9 and A10 - each is an "absence" assertion and each needs its own positive control. `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished` is the in-tree exemplar of that discipline; copy its structure.
- **The existing `handleTaskLog` suite.** All of it is green at HEAD and stays green; it is the *gate* on Task 4's extraction, not new evidence. Its diff must be **zero lines**.
- **B1's "does not contain SECRET" clause.** Vacuously green at HEAD, because there is no step line at all. **Assert the presence of `tool` FIRST**, so the test is RED at HEAD for the right reason. Stated again inside the task.

---

## File structure

### Lane A

| File | Action | Responsibility after this lane |
|---|---|---|
| `internal/worker/handler.go` | Modify | Adds `MaxAgentErrorMessageBytes`, `errorMessageLogStream`, `sanitizeAgentErrorMessage`, `(*Handler).trailingLogCutoff`, `(*Handler).publishTaskLog`; `handleTaskLog` is re-wired onto the last two with no behaviour change; `handleTaskStatus` gains one guarded write above the retry branch |
| `internal/worker/handler_taskstatus_errormessage_integration_test.go` | **Create** | The ten acceptance tests A1-A10, `//go:build integration`, `package worker_test` |
| `README.md` | Modify | Two paragraphs: prepare-failure cause in `### Source workspaces`; a coordinator-synthesized-line note in the task-log paging block |

### Lane B

| File | Action | Responsibility after this lane |
|---|---|---|
| `internal/agent/source/perforce/diagnostics.go` | Modify | `classifyP4Error` gains one out-of-disk `case` |
| `internal/agent/source/perforce/diagnostics_test.go` | Modify | Passthrough arm strengthened to an identity check; four positive and three negative disk cases |
| `internal/agent/runner.go` | Modify | Five host-log lines in `Run` |
| `internal/agent/runner_lifecycle_log_test.go` | **Create** | `captureAgentLog` plus B1, B2, B3, B6 |
| `internal/agent/source/perforce/perforce.go` | Modify | Three `progress` bracket lines around the `SyncStream` call |
| `internal/agent/source/perforce/fixtures_test.go` | Modify | Adds `setStreamErr`, the missing setter for the already-declared `streamErr` map |
| `internal/agent/source/perforce/perforce_progress_test.go` | **Create** | B4 and B5 |

---

## Standing rules for both lanes

- **Never use an em dash or an en dash.** New operator-visible text uses a hyphen. The four existing `classifyP4Error` messages carry em dashes; **leave them alone and do not copy them.**
- **All new strings are pure ASCII.** After any programmatic edit to a tracked text file, check the diffstat against the size of the change you intended and run `git ls-files --eol` on the touched paths - every one must read `i/lf`.
- **Test bodies in this plan are SKETCHES.** They are written against the tree as I read it, but they are guesses about helper signatures and exact assertion wording. **Derive the real assertion from the tree; do not transcribe.** If a helper's signature differs from what is written here, the tree wins - and say so in your report rather than editing the plan.
- **Comment discipline (CLAUDE.md).** One comment per new hazard, stating the constraint the code cannot show, naming the test that pins it. No dates, no history, no counts of anything elsewhere, no uniqueness claims about other code, no census of other files.
- **Mutation batteries run in your own worktree only.** Save a copy of the file to the scratchpad before mutating and restore from that copy - **never `git checkout --`**, which would discard an uncommitted guard. After every restore, re-run the suite and confirm it is green again before the next mutation.

---

# LANE A - Slice 1: the coordinator stores the agent's prepare-failure message

## Lane A Task 1: the bound, the stream, and the sanitiser

**Files:**
- Modify: `internal/worker/handler.go` (add near `DefaultTrailingLogWindow`, and a new unexported function beside it)
- Test: `internal/worker/handler_taskstatus_errormessage_integration_test.go` (created here)

This task adds only pure helpers, so its test is a unit test over the sanitiser. The behaviour tests come in Task 3.

- [ ] **Step 1: Write the failing test**

Create `internal/worker/handler_taskstatus_errormessage_integration_test.go` with the build tag and package line, and this first test. **Sketch** - derive the real assertions from the tree.

```go
//go:build integration

package worker_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three properties spec 4.4 requires of the stored message, at the layer
// that produces it. They are asserted here as well as through the handler
// because a Postgres Bind failure is indistinguishable at the handler from any
// other dropped write, so a handler-level RED cannot say WHICH property broke.
func TestSanitizeAgentErrorMessage_BoundsAndValidity(t *testing.T) {
	t.Run("a short ascii message is unchanged", func(t *testing.T) {
		require.Equal(t, "boom", worker.SanitizeAgentErrorMessageForTest("boom"))
	})

	t.Run("a NUL byte is removed", func(t *testing.T) {
		got := worker.SanitizeAgentErrorMessageForTest("boom\x00boom")
		require.Equal(t, "boomboom", got)
		require.NotContains(t, got, "\x00")
	})

	t.Run("an oversized message is cut at the bound and stays valid UTF-8", func(t *testing.T) {
		// A three-byte rune, so the bound does NOT fall on a rune boundary: with
		// MaxAgentErrorMessageBytes not a multiple of 3, a naive msg[:N] cut
		// lands mid-rune and produces invalid UTF-8. That is the discriminating
		// property; a two-byte rune with an even bound would be green under the
		// naive cut and prove nothing.
		in := strings.Repeat("€", worker.MaxAgentErrorMessageBytes)
		got := worker.SanitizeAgentErrorMessageForTest(in)
		assert.True(t, utf8.ValidString(got), "the truncated message must be valid UTF-8")
		assert.LessOrEqual(t, len(got), worker.MaxAgentErrorMessageBytes)
		assert.Greater(t, len(got), worker.MaxAgentErrorMessageBytes-4,
			"the cut must be AT the bound, not far below it")
		assert.True(t, strings.HasPrefix(in, got), "truncation must keep a prefix of the input")
	})

	t.Run("invalid UTF-8 on the wire is made valid", func(t *testing.T) {
		got := worker.SanitizeAgentErrorMessageForTest("ok\xff\xfe tail")
		require.True(t, utf8.ValidString(got))
	})
}
```

`SanitizeAgentErrorMessageForTest` and `MaxAgentErrorMessageBytes` do not exist yet. `MaxAgentErrorMessageBytes` will be a real exported constant (the same shape as `DefaultTrailingLogWindow`); the sanitiser stays unexported and gets a one-line seam in `export_test.go`.

**Wait** - `internal/worker/export_test.go` is already `//go:build integration`, `package worker`. Adding the seam there is a third Lane A file. That is fine and stays inside Lane A. Add to the Lane A file list: `internal/worker/export_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestSanitizeAgentErrorMessage -v -timeout 300s`

Expected: **compile failure** - `undefined: worker.SanitizeAgentErrorMessageForTest` and `undefined: worker.MaxAgentErrorMessageBytes`. A compile failure is an acceptable RED for a first test that names a symbol absent at HEAD; record the exact message.

- [ ] **Step 3: Write minimal implementation**

In `internal/worker/handler.go`, immediately after the `DefaultTrailingLogWindow` const block, add:

```go
// MaxAgentErrorMessageBytes bounds how much of an agent-supplied
// TaskStatusUpdate.ErrorMessage the coordinator will store as a task-log line.
// The field is a proto3 string: agent-controlled, and bounded on the wire only
// by gRPC's 4 MiB receive limit.
const MaxAgentErrorMessageBytes = 4096

// errorMessageLogStream is the task_logs.stream value the synthesized
// prepare-failure line lands on. task_logs_stream_check (migration 000019)
// admits only 'stdout' and 'stderr', so 'prepare' is not merely unused, it is
// unwritable; changing this to anything outside that pair fails at the database.
const errorMessageLogStream = "stderr"
```

Then, beside it:

```go
// sanitizeAgentErrorMessage makes an agent-supplied message storable in a
// Postgres TEXT column. All three transforms are load-bearing and none is
// theoretical: a NUL is legal in a proto3 string and illegal in TEXT (SQLSTATE
// 22021), invalid UTF-8 is rejected at Bind, and a cut at a byte offset can
// halve a multi-byte rune and produce the second failure from the first. Both
// are REAL errors rather than pgx.ErrNoRows, which is what lets the caller's
// error arm stay silent. Pinned by TestSanitizeAgentErrorMessage_BoundsAndValidity.
func sanitizeAgentErrorMessage(msg string) string {
	if strings.IndexByte(msg, 0) >= 0 {
		msg = strings.ReplaceAll(msg, "\x00", "")
	}
	if !utf8.ValidString(msg) {
		msg = strings.ToValidUTF8(msg, "")
	}
	if len(msg) <= MaxAgentErrorMessageBytes {
		return msg
	}
	cut := MaxAgentErrorMessageBytes
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut]
}
```

Add `"strings"` and `"unicode/utf8"` to the import block if they are not already there (check - `strings` may not be imported in `handler.go` today).

In `internal/worker/export_test.go`, add:

```go
// SanitizeAgentErrorMessageForTest exposes the unexported sanitiser so
// package worker_test can assert the three storability properties directly,
// rather than inferring them from a dropped write.
func SanitizeAgentErrorMessageForTest(msg string) string { return sanitizeAgentErrorMessage(msg) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestSanitizeAgentErrorMessage -v -timeout 300s`

Expected: PASS, four subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go internal/worker/export_test.go internal/worker/handler_taskstatus_errormessage_integration_test.go
git commit -m "worker: bound and sanitize an agent-supplied task error message"
```

---

## Lane A Task 2: extract the trailing-window cutoff and the task-log publish

**Files:**
- Modify: `internal/worker/handler.go` (`handleTaskLog`; two new unexported methods)
- Test: none new. **The gate is a ZERO-LINE test diff** across `internal/worker`.

Spec 4.6 requires exactly one definition of the cutoff resolution and one of the publish, shared by both callers. This task is behaviour-preserving and is gated as such.

- [ ] **Step 1: Record the pre-extraction green baseline**

Run, and save the output to the scratchpad:

```
go test ./internal/worker/... -count=1
go test -tags integration -p 1 ./internal/worker/... -timeout 1800s
```

Expected: both PASS. **Note the 1800s timeout deliberately** - CLAUDE.md records that this package's integration run is long and that a 600s timeout reports FAIL with no `--- FAIL` line beneath it.

- [ ] **Step 2: Extract, with no test edited**

In `internal/worker/handler.go`, add these two methods (place them immediately above `handleTaskLog`):

```go
// trailingLogCutoff resolves the effective trailing window and returns it as the
// absolute cutoff AppendTaskLog's recency arm compares against. Resolved PER
// CALL and never cached: TestHandleTaskLog_TheWindowIsReadFromTheHandlerFieldAtEveryCall
// moves the field between two calls on one handler and requires them to differ.
// Non-positive means the default, not a zero-length window -
// TestHandleTaskLog_AZeroWindowMeansTheDefaultNotAZeroLengthWindow is that leg.
func (h *Handler) trailingLogCutoff() pgtype.Timestamptz {
	window := h.TrailingLogWindow
	if window <= 0 {
		window = DefaultTrailingLogWindow
	}
	return pgtype.Timestamptz{Time: time.Now().Add(-window), Valid: true}
}

// publishTaskLog fans a STORED task-log row out to anyone tailing that task. It
// takes the row rather than a flag, so it cannot be reached without the insert
// having happened - which is the "never publish an unstored chunk" rule made
// structural rather than remembered at each call site.
func (h *Handler) publishTaskLog(taskIDStr, stream, content string, row store.AppendTaskLogRow) {
	if !h.broker.HasLogSubscriber(taskIDStr) {
		return // steady state: one map lookup, no marshal, no allocation
	}
	taskLogPublishes.Add(1)
	data, err := json.Marshal(taskLogEvent{
		TaskID:    taskIDStr,
		JobID:     uuidStr(row.JobID),
		Seq:       row.ID,
		Stream:    stream,
		Content:   content,
		CreatedAt: row.CreatedAt.Time,
	})
	if err != nil {
		log.Printf("worker: task log publish marshal %s: %v", taskIDStr, err)
		return
	}
	h.broker.Publish(events.Event{
		Type:   events.TypeTaskLog,
		JobID:  uuidStr(row.JobID),
		TaskID: taskIDStr,
		Data:   data,
	})
}
```

Then in `handleTaskLog`:

- replace the four-line `window := h.TrailingLogWindow; if window <= 0 { ... }` block and the `MinFinishedAt:` expression with `MinFinishedAt: h.trailingLogCutoff(),`, deleting the block and its comment (the comment moves onto `trailingLogCutoff`);
- replace everything from `taskIDStr := uuidStr(taskID)` to the end of the function with:

```go
	// Persistence is unconditional and strictly precedes any publish; the publish
	// is derived from the stored row, so no line is ever published unstored.
	h.publishTaskLog(uuidStr(taskID), stream, string(chunk.Content), row)
```

The marshal-error log line's wording changes from `handleTaskLog marshal` to `task log publish marshal`, because it now serves two callers. **No test in the tree asserts that wording** - I checked. That arm is unreachable in practice (a `json.Marshal` of a struct of strings and ints cannot fail) and is kept only so the arm is not silent.

- [ ] **Step 3: Prove the test diff is zero**

Run:

```
git diff --stat -- "internal/worker/*_test.go"
```

Expected: **empty output.** If any test file appears, the extraction is not behaviour-preserving and must be redone. `handler_taskstatus_errormessage_integration_test.go` from Task 1 is already committed, so it must not appear either.

- [ ] **Step 4: Re-run both lanes and compare against the baseline**

```
go test ./internal/worker/... -count=1
go test -tags integration -p 1 ./internal/worker/... -timeout 1800s
```

Expected: both PASS, with the same test set as Step 1's baseline. In particular `TestHandleTaskLog_TheWindowIsReadFromTheHandlerFieldAtEveryCall`, `TestHandleTaskLog_AZeroWindowMeansTheDefaultNotAZeroLengthWindow`, `TestHandleTaskLog_NoSubscriberSkipsMarshalButStillPersists` and `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` must all be green.

- [ ] **Step 5: Mutate each re-wired behaviour, then restore**

A zero-line test diff plus green is not sufficient on its own - it is consistent with the extraction having quietly dropped a behaviour nothing covers. Copy `handler.go` to the scratchpad, then run each mutation, record the result, and restore from the copy after each:

| # | Mutation | Must go RED |
|---|---|---|
| E1 | In `trailingLogCutoff`, replace the body with `return pgtype.Timestamptz{Time: time.Now().Add(-DefaultTrailingLogWindow), Valid: true}` (ignore the field) | `TestHandleTaskLog_TheWindowIsReadFromTheHandlerFieldAtEveryCall` |
| E2 | In `trailingLogCutoff`, change `if window <= 0` to `if window < 0` | `TestHandleTaskLog_AZeroWindowMeansTheDefaultNotAZeroLengthWindow` |
| E3 | In `publishTaskLog`, delete the `HasLogSubscriber` early return | `TestHandleTaskLog_NoSubscriberSkipsMarshalButStillPersists` (its `TaskLogPublishesForTest` delta) |
| E4 | In `handleTaskLog`, move the `h.publishTaskLog(...)` call above the `if err != nil` block | `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished` and `TestHandleTaskLog_RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow` |

**Survivor control:** rename the unexported method `publishTaskLog` to `fanOutTaskLog` at both its definition and its call site. Every test must stay **GREEN**. If that reddens something, the harness is reporting RED for everything and none of E1-E4's kills mean anything.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handler.go
git commit -m "worker: one definition of the trailing-log cutoff and the task-log publish"
```

---

## Lane A Task 3: the RED - A1, the whole feature

**Files:**
- Test: `internal/worker/handler_taskstatus_errormessage_integration_test.go`

- [ ] **Step 1: Write the failing test**

Append to the test file. **Sketch.** `seedTaskAndTwoWorkers`, `newTestStore` and `h.UUIDStringForTest` all exist in this package's integration lane; verify their signatures in `handler_taskstatus_integration_test.go` and `handler_test.go` before writing.

```go
// A1 - the whole feature. RED at HEAD: handleTaskStatus never reads
// ErrorMessage, so the task's log is empty and the operator's only record of
// why a P4 sync failed is the agent process's stdout, if anyone kept it.
func TestHandleTaskStatus_APrepareFailureMessageIsStoredAsAStderrLogLine(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg1", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)

	const cause = "p4 sync: out of disk space on this agent's workspace volume"
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId:       h.UUIDStringForTest(taskID),
		Status:       relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: cause,
		Epoch:        int64(claimed.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the prepare failure's cause must be stored as exactly one log line")
	assert.Equal(t, "stderr", rows[0].Stream,
		"the synthesized line lands on stderr so the SPA renders it in the error colour")
	assert.Equal(t, "[failed] "+cause+"\n", rows[0].Content)

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "failed", after.Status, "PREPARE_FAILED is still routed through the failed path")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestHandleTaskStatus_APrepareFailureMessageIsStoredAsAStderrLogLine -v -timeout 600s`

Expected: FAIL at `require.Len(t, rows, 1)` with `"Should have 1 item(s), but has 0"`. The task status assertion is already green at HEAD - it is a control, not the RED.

- [ ] **Step 3: Write minimal implementation**

In `internal/worker/handler.go`, inside `handleTaskStatus`, insert this block **immediately after** the `terminal := statusStr == "failed" || statusStr == "timed_out"` line and **immediately before** the retry branch's comment block:

```go
	// ABOVE THE RETRY BRANCH, BECAUSE THAT BRANCH RETURNS AND ENDS THE
	// GENERATION. IncrementTaskRetryCount bumps assignment_epoch, so an append
	// placed below it never runs on the retry path and could not pass the fence
	// after it. The publish goes out BEFORE the status event because the CLI's
	// log follower stops at the terminal frame and the SPA's tail stops on a
	// terminal status, so a line published after it is a line the live view never
	// shows. A2 and A5 in handler_taskstatus_errormessage_integration_test.go are
	// the two that redden if either half moves.
	//
	// A pgx.ErrNoRows here is the fence refusing: drop it, publish nothing, count
	// nothing, log nothing. AppendTaskLog's fence is strictly weaker than the
	// fence of the write one statement below - identical identity, currency and
	// id predicates, and a status allow-list relaxed by a recency disjunct - so a
	// refusal here is refused there too and lands in task_status_fence with a
	// reason. That argument needs no production caller of the epoch-only status
	// write; internal/store/updatetaskstatusepoch_guard_test.go is what keeps
	// that true, and is named by FILE because spelling its identifier turns that
	// guard RED. Any other error is dropped for the same reason:
	// sanitizeAgentErrorMessage removes the only caller-reachable cause, and a
	// genuine fault on this connection is logged one statement below under the
	// connection's existing budget key.
	if terminal && upd.ErrorMessage != "" {
		content := "[" + statusStr + "] " + sanitizeAgentErrorMessage(upd.ErrorMessage) + "\n"
		row, appendErr := h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
			TaskID:          taskID,
			Stream:          errorMessageLogStream,
			Content:         content,
			AssignmentEpoch: int32(upd.Epoch),
			WorkerID:        workerID,
			MinFinishedAt:   h.trailingLogCutoff(),
		})
		if appendErr == nil {
			h.publishTaskLog(uuidStr(taskID), errorMessageLogStream, content, row)
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestHandleTaskStatus_APrepareFailureMessageIsStoredAsAStderrLogLine -v -timeout 600s`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_taskstatus_errormessage_integration_test.go
git commit -m "worker: store the agent's prepare-failure message as a task log line"
```

---

## Lane A Task 4: A2 - the log frame is published before the status frame

**Files:**
- Test: `internal/worker/handler_taskstatus_errormessage_integration_test.go`

- [ ] **Step 1: Write the failing test**

**Sketch.**

```go
// A2 - the ordering in spec 4.2. A single subscriber filtered on BOTH the job
// and the task is in both of the broker's indexes, so it receives both frame
// types on one channel in publish order (TestBroker_JobAndTaskSubscriberReceivesBoth).
// Publish is synchronous under the broker's own mutex and HandleTaskStatus is
// synchronous, so a non-blocking drain after the call is exact - no wall clock.
func TestHandleTaskStatus_TheLogEventIsPublishedBeforeTheStatusEvent(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	jobID, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg2", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	taskIDStr := h.UUIDStringForTest(taskID)

	ch, cancel := broker.Subscribe(events.Filter{JobID: h.UUIDStringForTest(jobID), TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId:       taskIDStr,
		Status:       relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "sync failed",
		Epoch:        int64(claimed.AssignmentEpoch),
	})

	var types []string
	for done := false; !done; {
		select {
		case e := <-ch:
			types = append(types, e.Type)
		default:
			done = true
		}
	}

	logAt := indexOf(types, events.TypeTaskLog)
	statusAt := indexOf(types, "task")
	require.GreaterOrEqual(t, logAt, 0, "the log frame must be published at all, got %v", types)
	require.GreaterOrEqual(t, statusAt, 0, "the status frame must still be published, got %v", types)
	assert.Less(t, logAt, statusAt,
		"a line published after the terminal status frame is a line the live view never shows: %v", types)
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}
```

Note the job also goes terminal here, so a third `"job"` frame is expected; the assertion is on relative position, not on the exact frame list.

- [ ] **Step 2: Run test to verify it fails**

At HEAD-plus-Task-3 this test is already green. **That is expected and is why the RED is established by mutation instead.** Before running it, apply this mutation to `handleTaskStatus`: keep the `AppendTaskLog` call where it is, but stash the publish into a local closure and invoke it at the very end of the function, after the `Type: "task"` publish.

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestHandleTaskStatus_TheLogEventIsPublishedBeforeTheStatusEvent -v -timeout 600s`

Expected: FAIL at the `assert.Less` with `logAt` greater than `statusAt`.

Restore `handler.go` from your scratchpad copy and re-run: PASS. Record both results.

- [ ] **Step 3: No implementation needed**

Task 3's placement already satisfies this. State that in the commit message rather than inventing a change.

- [ ] **Step 4: Run the whole new file**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestHandleTaskStatus_ -v -timeout 900s`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler_taskstatus_errormessage_integration_test.go
git commit -m "worker: pin that the prepare-failure log frame precedes the status frame"
```

---

## Lane A Task 5: A3, A4, A5 - the gates and the retry path

**Files:**
- Test: `internal/worker/handler_taskstatus_errormessage_integration_test.go`

- [ ] **Step 1: Write the three tests**

**Sketches.** Each absence assertion carries its own positive control in the same test, on the same handler and the same task, for the reason `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished` states: without one, a handler that had stopped accepting anything at all passes.

```go
// A3 - the identity gate must cover the NEW write too. The rows clause is what
// discriminates: the counters clause is already green at HEAD and is kept as a
// regression backstop for the "no new counted arm" decision (spec 4.5).
func TestHandleTaskStatus_ANonAssigneeCannotWriteAnErrorMessageLine(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, w2 := seedTaskAndTwoWorkers(t, ctx, q, "errmsg3", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	taskIDStr := h.UUIDStringForTest(taskID)

	before := h.TaskStatusFenceRejections()
	h.HandleTaskStatus(ctx, w2, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "forged", Epoch: int64(claimed.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a non-assignee must not be able to write into a task's log")
	assert.Equal(t, before, h.TaskStatusFenceRejections(),
		"a forged report is dropped a round trip before any write, so no counter moves")
	if t.Failed() {
		t.FailNow()
	}

	// Positive control on the same code path.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "genuine", Epoch: int64(claimed.AssignmentEpoch),
	})
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "positive control: the assignee's own message must land")
	require.Contains(t, rows[0].Content, "genuine")
}

// A4 - the currency gate. Requeue-then-redispatch is the reachable way to make
// a NON-ZERO epoch stale; do not use CancelJobTasks, which leaves worker_id NULL
// and makes the positive control unreachable.
func TestHandleTaskStatus_AStaleEpochWritesNoErrorMessageLine(t *testing.T) {
	// ... seed, claim, RequeueTask at `epoch`, ClaimTaskForWorker again ...
	// require.Equal(t, epoch+2, fresh.AssignmentEpoch)
	// report at `epoch`   -> zero rows
	// report at `epoch+1` -> zero rows (kills an off-by-one fence)
	// report at fresh.AssignmentEpoch -> exactly one row  (positive control)
}

// A5 - the above-the-retry-branch position. A prepare failure that is going to
// be retried is exactly the case where the operator most needs the cause of
// attempt N recorded, and the retry branch RETURNS.
func TestHandleTaskStatus_TheErrorMessageLineSurvivesARequeueingRetry(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg5", 1) // ONE retry
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID), Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "attempt one died", Epoch: int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "pending", after.Status, "fixture: this report must take the RETRY branch")
	require.Equal(t, claimed.AssignmentEpoch+1, after.AssignmentEpoch,
		"fixture: the retry must have bumped the epoch, which is what makes the position load-bearing")

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the cause of the retried attempt must survive the requeue")
	require.Contains(t, rows[0].Content, "attempt one died")
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags integration -p 1 ./internal/worker/... -run "TestHandleTaskStatus_ANonAssignee|TestHandleTaskStatus_AStaleEpoch|TestHandleTaskStatus_TheErrorMessageLineSurvives" -v -timeout 900s`

Expected: PASS. A3 and A4 are absence-plus-control tests that Task 3's code already satisfies; A5's `require.Len(t, rows, 1)` would be RED under a write placed below the retry branch, which is the position mutation in Task 8.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/handler_taskstatus_errormessage_integration_test.go
git commit -m "worker: pin the identity gate, the currency gate and the retry path for the error-message line"
```

---

## Lane A Task 6: A6, A7, A8, A9 - the message and the condition

**Files:**
- Test: `internal/worker/handler_taskstatus_errormessage_integration_test.go`

- [ ] **Step 1: Write the four tests**

**Sketches.** A6 and A7 exercise the sanitiser *through the database*, which is what Task 1's unit test cannot do: without the sanitiser these are Bind failures, and a Bind failure is not `pgx.ErrNoRows`.

```go
// A6 - spec 4.4.1 and 4.4.2 together. The properties are asserted against the
// CONSTANT, not against a hard-coded byte count, so widening the bound is a
// survivor and a byte-offset cut is a kill.
func TestHandleTaskStatus_AnOversizedErrorMessageIsTruncatedAtARuneBoundary(t *testing.T) {
	// ... seed, claim ...
	msg := strings.Repeat("€", worker.MaxAgentErrorMessageBytes) // 3 bytes/rune: the bound is mid-rune
	// ... report PREPARE_FAILED with msg ...
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "an oversized message must still produce a stored line, truncated")
	stored := strings.TrimSuffix(strings.TrimPrefix(rows[0].Content, "[failed] "), "\n")
	assert.True(t, utf8.ValidString(rows[0].Content))
	assert.LessOrEqual(t, len(stored), worker.MaxAgentErrorMessageBytes)
	assert.Greater(t, len(stored), worker.MaxAgentErrorMessageBytes-4)
	assert.True(t, strings.HasPrefix(msg, stored))
}

// A7 - spec 4.4.3. A proto3 string may legally carry \x00; Postgres TEXT may not
// (SQLSTATE 22021). Without the strip this is a Bind failure and the row is
// never written at all.
func TestHandleTaskStatus_ANulByteInTheErrorMessageIsStripped(t *testing.T) {
	// ... report ErrorMessage: "boom\x00boom" ...
	require.Len(t, rows, 1)
	assert.Equal(t, "[failed] boomboom\n", rows[0].Content)
	assert.NotContains(t, rows[0].Content, "\x00")
}

// A8 - the != "" condition. An empty message must not become a blank line.
func TestHandleTaskStatus_AnEmptyErrorMessageWritesNoLine(t *testing.T) {
	// report PREPARE_FAILED with ErrorMessage: "" -> zero rows, task is failed
	// positive control: a SECOND task on the same handler, same shape, WITH a
	// message -> exactly one row. (A second task, because the first is now
	// terminal and its own re-report is the duplicate case A10 covers.)
}

// A9 - the `terminal` condition (spec 4.3). A RUNNING report leaves status,
// worker_id and assignment_epoch untouched and nothing rate-limits status
// messages, so admitting one would be an unbudgeted insert the assignee can
// repeat forever at one gRPC message per row.
func TestHandleTaskStatus_ARunningReportWithAMessageWritesNoLine(t *testing.T) {
	// report TASK_STATUS_RUNNING with ErrorMessage: "chatty" -> zero rows
	// require the task really did go `running` (the report was ACCEPTED, so the
	// absence is the condition and not a rejected message)
	// positive control on the SAME task: a terminal report with a message -> one row
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags integration -p 1 ./internal/worker/... -run "TestHandleTaskStatus_AnOversized|TestHandleTaskStatus_ANulByte|TestHandleTaskStatus_AnEmptyError|TestHandleTaskStatus_ARunningReport" -v -timeout 900s`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/handler_taskstatus_errormessage_integration_test.go
git commit -m "worker: pin the error-message bound, sanitisation and admission condition"
```

---

## Lane A Task 7: A10 - the fence-rejection arm is silent, uncounted and unpublished

**Files:**
- Test: `internal/worker/handler_taskstatus_errormessage_integration_test.go`

- [ ] **Step 1: Write the failing test**

**Sketch.** This is the one that pins all four clauses of spec 4.5 at once. Drive the rejection by making the row already terminal with a `finished_at` outside the trailing window, the way `TestHandleTaskLog_RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow` does - on **this process's clock**, never `NOW() - interval`.

```go
func TestHandleTaskStatus_AFenceRefusedErrorMessageIsSilentUncountedAndUnpublished(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	jobID, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg10", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	taskIDStr := h.UUIDStringForTest(taskID)

	// Terminal an hour ago: outside DefaultTrailingLogWindow, so AppendTaskLog's
	// recency arm rejects and its status arm rejects too. The epoch and the
	// assignee are untouched by a terminal transition, so BOTH Go gates still pass
	// and control genuinely reaches the write.
	_, err = q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "done", WorkerID: w1, AssignmentEpoch: claimed.AssignmentEpoch,
		FinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err, "fixture: the terminal transition must land")

	ch, cancel := broker.Subscribe(events.Filter{JobID: h.UUIDStringForTest(jobID), TaskID: taskIDStr})
	defer cancel()

	fenceBefore := h.TaskLogFenceRejections()
	statusBefore := h.TaskStatusFenceRejections()
	logged := captureLog(t)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		ErrorMessage: "too late", Epoch: int64(claimed.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a fence-refused append must store nothing")

	var published []byte
	select {
	case e := <-ch:
		published = e.Data
	default:
	}
	assert.Nil(t, published, "a fence-refused append must publish nothing")
	assert.Empty(t, logged(), "the fence-rejection arm must be entirely silent")
	assert.Equal(t, fenceBefore, h.TaskLogFenceRejections(),
		"the new site must not join task_log_fence, whose published meaning rests on having no Go-side pre-filter")

	// POSITIVE CONTROL, and it is what stops this test being vacuous: the message
	// really did reach the write path. The status write that follows is refused by
	// its terminality predicate and IS counted, which is the whole
	// strictly-weaker-fence argument for not adding a fourth counter here.
	after := h.TaskStatusFenceRejections()
	assert.Equal(t, statusBefore.Conflicting+1, after.Conflicting,
		"the following status write must have been reached and refused")
}
```

- [ ] **Step 2: Run it**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestHandleTaskStatus_AFenceRefused -v -timeout 600s`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/handler_taskstatus_errormessage_integration_test.go
git commit -m "worker: pin that a fence-refused error-message append is silent and uncounted"
```

---

## Lane A Task 8: the mutation battery

**Files:** none committed. This task produces evidence for the report.

- [ ] **Step 1: Establish a green baseline**

Run the whole lane unmutated:

```
go test ./internal/worker/... -count=1
go test -tags integration -p 1 ./internal/worker/... -timeout 1800s
```

Expected: PASS. **Uniform results in the battery below mean a broken harness, and a compile error is not a kill** - if a mutation does not compile, fix the mutation, do not record it as a kill.

- [ ] **Step 2: Copy `handler.go` to the scratchpad, then run each mutation**

Restore from the copy after each one and re-run to confirm green before the next.

| # | Mutation in `handleTaskStatus` / helpers | Must go RED | Must stay GREEN |
|---|---|---|---|
| M1 | Delete the identity gate (`if !workerID.Valid \|\| !task.WorkerID.Valid \|\| task.WorkerID.Bytes != workerID.Bytes { return }`) | **A3**, rows clause | A1 |
| M2 | Move the whole `if terminal && upd.ErrorMessage != ""` block to immediately below the retry branch's closing brace | **A5** | A1, A2 |
| M3 | Keep the append in place; stash the publish in a closure and invoke it at the end of the function | **A2** | A1, A5 |
| M4 | In `sanitizeAgentErrorMessage`, replace the rune-boundary loop with `return msg[:MaxAgentErrorMessageBytes]` | **A6** (and Task 1's unit subtest) | A1, A7 |
| M5 | In `sanitizeAgentErrorMessage`, delete the NUL strip | **A7** (and Task 1's unit subtest) | A1, A6 |
| M6 | Change the condition to `if upd.ErrorMessage != ""` (drop `terminal`) | **A9** | A1 |
| M7 | Change the condition to `if terminal` (drop `!= ""`) | **A8** | A1 |
| M8 | Delete the `if appendErr == nil` gate and publish unconditionally | **A10**, publish clause | A1 |
| M9 | Add `h.taskLogFenceRejects.Add(1)` on the `appendErr != nil` path | **A10**, counter clause | A1 |
| M10 | Change `errorMessageLogStream` to `"stdout"` | **A1**, stream clause | A2, A5 |

- [ ] **Step 3: Run the two survivor controls**

Both must leave **every** test GREEN. If either reddens something, the corresponding test hard-codes a value it should have read, and that test must be fixed before its kills above count.

| # | Mutation | Why it must survive |
|---|---|---|
| S1 | Change `MaxAgentErrorMessageBytes` from `4096` to `8192` | A6 asserts against the constant, not a byte count. Reversing the bound is a decision, not a defect. |
| S2 | Rename the unexported `sanitizeAgentErrorMessage` at both its definition and its two call sites | An identifier rename changes no behaviour. A RED here means the harness is failing everything. |

- [ ] **Step 4: Record every result in the report**

For each mutation, record: what you changed, which test names went RED, and the failing assertion's message. **A kill must name its guard** - if a mutation reddens a test for a reason other than the guard you expected, trace the failing branch and say so.

---

## Lane A Task 9: README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Edit `### Source workspaces`**

Insert a new paragraph immediately **before** the line beginning `**Workspace arbitration.**` (which follows the `source` field table), reading exactly:

```markdown
**Prepare failures.** When a task's prepare phase fails - a sync error, a bad stream, a missing ticket, or a worker with no workspace provider at all - the provider's error is stored as the last line of that task's log, on the **stderr** stream, prefixed `[failed] `. It is readable through `GET /v1/tasks/{id}/logs`, `relay logs` and the SPA's task log view, live and on refresh. Before this, a failed prepare left the task `failed` with an empty log and the cause only in the agent process's own stdout.
```

- [ ] **Step 2: Edit the task-log paging block**

Insert a new bullet immediately **after** the bullet beginning `` - `total` counts the task's log ENTRIES `` and before the paragraph beginning `Stop when the cursor for your direction is `0`.`, reading exactly:

```markdown
- **Not every line comes from the subprocess.** The coordinator synthesizes one:
  a task whose prepare failed carries the provider's error as a `stderr` entry
  prefixed `[failed] `. An agent that repeats its terminal status message can
  produce that line more than once - the duplicate status write is refused and
  recorded in `task_status_fence.counts.duplicate_total`, but the log line is
  written each time inside `RELAY_TASKLOG_TRAILING_WINDOW`.
```

- [ ] **Step 3: Verify no counter documentation moved**

Run: `git diff README.md`

Expected: exactly the two insertions above. **Nothing under the `GET /v1/server/counters` bullets may change.** If a diff touches them, spec 4.5's counter decision was not followed and the change must be reverted.

Then run: `git ls-files --eol README.md` - must read `i/lf`. And check the diffstat is proportionate to two inserted paragraphs; a diffstat in the hundreds means the file was reclassified as binary and the edit must be redone.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: where a prepare failure's cause appears, and that one log line is synthesized"
```

---

## Lane A Task 10: full-lane gate

- [ ] **Step 1: Run every lane the change can reach**

```
go test ./... -count=1
go test -tags integration -p 1 ./internal/worker/... -timeout 1800s
go test -tags integration -p 1 ./internal/api/... -timeout 1800s
```

The `internal/api` run is included because `internal/api` imports `internal/worker` for the counter types, and `TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers` drives the served counters end to end.

Expected: all PASS. If `internal/api` is red, measure it **both ways** - re-run at `origin/main` before concluding the change caused it.

- [ ] **Step 2: Report the tree state and hand back**

Print `git status --porcelain` and `git log --oneline origin/main..HEAD`. Do not open the PR yourself unless the conductor asked for it.

---

# LANE B - Slices 2 then 3

## Lane B Task 1: strengthen the passthrough arm (prerequisite for slice 2)

**Files:**
- Modify: `internal/agent/source/perforce/diagnostics_test.go`

**Read R1 above before starting.** The existing passthrough arm's `errors.Is(got, tc.in)` is satisfied by a *wrapped* error, so it cannot detect a misclassification. Slice 2's negative cases would pin nothing without this task.

- [ ] **Step 1: Prove the weakness**

Copy `diagnostics.go` to the scratchpad. Then temporarily add a case to `classifyP4Error` that matches the existing passthrough input:

```go
	case strings.Contains(msg, "not in client view"):
		return fmt.Errorf("bogus classification: %w", err)
```

Run: `go test ./internal/agent/source/perforce/... -run TestClassifyP4Error -v -count=1`

Expected: **PASS** - the `passthrough` subtest is green while the error is being rewrapped. Record this output; it is the evidence for R1.

Restore `diagnostics.go` from the copy and re-run: still PASS.

- [ ] **Step 2: Strengthen the arm**

In `diagnostics_test.go`, replace the passthrough branch body with an identity check:

```go
			if tc.wantSub == "" {
				// IDENTITY, NOT errors.Is. errors.Is unwraps, so it is TRUE for a
				// rewrapped error and cannot see a misclassification at all -
				// which is precisely what the negative cases below exist to
				// catch. classifyP4Error's default arm returns its argument, so
				// the interface value compares equal.
				if got != tc.in {
					t.Errorf("expected the error back unchanged; got=%v (%T) in=%v (%T)", got, got, tc.in, tc.in)
				}
				return
			}
```

- [ ] **Step 3: Run to verify the strengthened arm still passes at HEAD, and now kills the bogus case**

Run: `go test ./internal/agent/source/perforce/... -run TestClassifyP4Error -v -count=1`

Expected: PASS (the real `default` arm does return the same value).

Re-apply Step 1's bogus case and re-run.

Expected: **FAIL** on the `passthrough` subtest with `expected the error back unchanged`. Restore `diagnostics.go` and confirm green.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/source/perforce/diagnostics_test.go
git commit -m "perforce: the passthrough arm must assert identity, not errors.Is"
```

---

## Lane B Task 2: slice 2 - classify an out-of-disk p4 failure

**Files:**
- Modify: `internal/agent/source/perforce/diagnostics_test.go`
- Modify: `internal/agent/source/perforce/diagnostics.go`

- [ ] **Step 1: Write the failing cases**

Add these entries to `TestClassifyP4Error`'s `cases` slice. Four positives, one per phrasing, at least one in the capitalisation p4 actually emits (the function lower-cases before matching, so that is what exercises case-insensitivity). Three negatives.

```go
		{
			name:    "disk full linux enospc",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: write //s/x/big.bin: no space left on device)")),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		{
			name:    "disk full windows full sentence",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: There is not enough space on the disk.)")),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		{
			name:    "disk full p4d phrasing",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Disk full; cannot write to depot.)")),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		{
			name:    "disk full p4 client-side check",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Insufficient disk space to complete sync.)")),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		// THE THREE NEGATIVES ARE THE REASON THIS TEST EXISTS. The four positives
		// above all pass under a version that matches `insufficient` and `space`
		// as two independent substrings, and `workspace` contains `space` - so
		// that version reports a permissions problem as a full disk and sends an
		// operator to free space on a machine whose disk is fine.
		{
			name:    "workspace not found is not a disk problem",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Client 'relay_h_ab12' unknown - workspace not found.)")),
			wantSub: "",
		},
		{
			name:    "insufficient permissions on workspace is not a disk problem",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: insufficient permissions on workspace //s/x)")),
			wantSub: "",
		},
		{
			name:    "a space in an unrelated message is not a disk problem",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: no such file or directory)")),
			wantSub: "",
		},
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/agent/source/perforce/... -run TestClassifyP4Error -v -count=1`

Expected: the four positive subtests FAIL with `missing "out of disk space on this agent's workspace volume" in classified message`. The three negatives PASS at HEAD (nothing matches them yet) - **they are not the RED, they are the mutation guard**, and Step 5 is what proves they do anything.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/source/perforce/diagnostics.go`, add one `case` to the existing `switch`, after the `connect to server failed` case and before `default`:

```go
	// Match the PHRASE, never the words: `workspace` contains `space`, so an
	// independent `space` substring reports a permissions problem as a full disk.
	// TestClassifyP4Error's "insufficient permissions on workspace" case is what
	// goes RED on that mistake.
	case strings.Contains(msg, "no space left on device"),
		strings.Contains(msg, "not enough space"),
		strings.Contains(msg, "disk full"),
		strings.Contains(msg, "insufficient disk space"):
		return fmt.Errorf("out of disk space on this agent's workspace volume - free space, raise RELAY_WORKSPACE_MIN_FREE_GB so the sweeper evicts idle workspaces sooner, or reduce the sync paths: %w", err)
```

**Note three things about that string and do not change them:**
- It uses a **hyphen**, not an em dash. The four existing messages carry em dashes; do not copy them and do not fix them here.
- It says **raise**, because raising `RELAY_WORKSPACE_MIN_FREE_GB` is the direction that causes eviction, and an operator reading a remedy under time pressure should not have to derive that.
- It does **not** mention sync-path exclusions. That feature does not exist. "Reduce the sync paths" is achievable today by editing the spec.

`there is not enough space on the disk` is deliberately **not** a fifth substring: it contains `not enough space`, so matching both is dead weight. It is kept as a test case above so the coverage claim stays honest.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/agent/source/perforce/... -run TestClassifyP4Error -v -count=1`

Expected: PASS, all subtests.

- [ ] **Step 5: Mutation battery for slice 2**

Copy `diagnostics.go` to the scratchpad first.

| # | Mutation | Must go RED | Must stay GREEN |
|---|---|---|---|
| P1 | Replace the four-substring case with the fork's `strings.Contains(msg, "insufficient"), strings.Contains(msg, "space")` | `workspace not found` **and** `insufficient permissions on workspace` | all four positives, `passthrough`, `no such file or directory` |
| P2 | Drop `strings.Contains(msg, "no space left on device")` from the case | `disk full linux enospc` | the other three positives |
| P3 | Drop `strings.Contains(msg, "not enough space")` | `disk full windows full sentence` | the other three positives |
| P4 | Change `msg := strings.ToLower(err.Error())` to `msg := err.Error()` | the two positives whose fixtures are capitalised (`There is not enough space`, `Disk full`, `Insufficient disk space`) | the lower-case ENOSPC positive |

**Survivor control:** reorder the four substrings inside the case (put `disk full` first). **Every** subtest must stay GREEN. If any reddens, the test is keyed on evaluation order rather than on the match, and its kills above do not count.

Record P1's exact failure output - it is the evidence that the negative cases are load-bearing rather than decorative.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/diagnostics.go internal/agent/source/perforce/diagnostics_test.go
git commit -m "perforce: classify an out-of-disk sync failure with an actionable remedy"
```

**Slice 2 ends here. Slice 3 begins. Do not interleave them.**

---

## Lane B Task 3: slice 3 - the log-capture helper and B1, the `argv[0]` narrowing

**Files:**
- Create: `internal/agent/runner_lifecycle_log_test.go`
- Modify: `internal/agent/runner.go`

**Write B1 FIRST.** It is the test the backlog item exists to carry, and it is the only one whose failure mode is a leaked secret.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/runner_lifecycle_log_test.go`. **Sketch.**

```go
package agent

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureAgentLog redirects the standard logger for the test's duration and
// returns an accessor for what was written. The restore is UNCONDITIONAL, via
// t.Cleanup. This is a PROCESS-GLOBAL capture and is safe only because no test
// in this package calls t.Parallel; do not add the first one.
func captureAgentLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return buf.String
}

// B1 - the narrowing. NOTE THE ASSERTION ORDER: the presence check comes FIRST,
// so this test is RED at HEAD because there is no step line at all. The absence
// check alone is vacuously green on a runner that logs nothing.
//
// The narrowing bounds the HOST log only and closes nothing: sendStepMarker four
// lines away already writes strings.Join(argv, " ") into task_logs, so the same
// token is already stored and API-readable. Do not read this test as a secrecy
// guarantee.
func TestRunner_AStepLineNamesTheProgramAndNotItsArguments(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 64)

	r, runCtx := newRunner("secret-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "secret-task",
		Commands: []*relayv1.CommandLine{{Argv: tokenArgv()}},
	})

	out := logged()
	require.Contains(t, out, "exec step 1/1",
		"every step must announce itself on the host log before it runs")
	require.Contains(t, out, programNameForTokenArgv(),
		"the step line must name the program so an operator can see what is running")
	assert.NotContains(t, out, "SUPER-SECRET-TOKEN",
		"nothing beyond argv[0] may reach the host log; arguments are unsanitised")
}
```

You need a cross-platform argv carrying a token. Follow `echoArgv`'s pattern in `runner_multistep_test.go`:

```go
// tokenArgv is a command that succeeds on either platform while carrying a
// token-shaped argument. The token is the discriminator; the program name is
// what the line is allowed to carry.
func tokenArgv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", "--token", "SUPER-SECRET-TOKEN"}
	}
	return []string{"echo", "--token", "SUPER-SECRET-TOKEN"}
}

func programNameForTokenArgv() string { return tokenArgv()[0] }
```

Add `"runtime"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/... -run TestRunner_AStepLineNamesTheProgramAndNotItsArguments -v -count=1`

Expected: FAIL at the **first** `require.Contains` with `"exec step 1/1"` not found - because HEAD logs nothing between accepting a dispatch and finalizing. Record that the failure is the *presence* assertion, not the absence one.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/runner.go`, inside `Run`'s per-step loop, immediately after `r.sendStepMarker(step, stepTotal, argv)` and before `cmd := exec.CommandContext(...)`:

```go
		// argv[0] ONLY. Nothing in relay sanitises command arguments, so a token
		// passed as one would land here verbatim.
		// TestRunner_AStepLineNamesTheProgramAndNotItsArguments is the guard. It
		// bounds THIS surface and closes nothing: sendStepMarker above already
		// writes the whole vector into task_logs.
		log.Printf("runner: exec step %d/%d for %s: %s", step, stepTotal, r.taskID, argv[0])
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/... -run TestRunner_AStepLineNamesTheProgramAndNotItsArguments -v -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_lifecycle_log_test.go
git commit -m "agent: log each step's index and program name, never its arguments"
```

---

## Lane B Task 4: B2 and B6 - the two prepare-failure host lines

**Files:**
- Modify: `internal/agent/runner_lifecycle_log_test.go`
- Modify: `internal/agent/runner.go`

B6 is spec section 10's open question, decided **YES** by the conductor.

- [ ] **Step 1: Write the failing tests**

**Sketches.** `fakeProvider` with a `prepareErr` already exists in `runner_test.go`, same package.

```go
// B2 - the line whose whole purpose is that it survives when the send does not.
func TestRunner_APrepareFailureIsOnTheHostLogWithItsCause(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 16)

	const cause = "p4 sync failed: PREPARE-CAUSE-SENTINEL"
	r, runCtx := newRunner("prep-task", 0, sendCh, context.Background(), 0)
	r.SetProviderForTest(&fakeProvider{prepareErr: errors.New(cause)})
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "prep-task",
		Commands: singleCmd(echoTaskCmd()),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	})

	out := logged()
	assert.Contains(t, out, "preparing workspace", "the prepare phase must announce itself")
	assert.Contains(t, out, "PREPARE-CAUSE-SENTINEL",
		"the cause must reach the host log, which is the record that survives a lost connection")
	assert.Contains(t, out, "prep-task", "every lifecycle line carries the task id")
}

// B6 - the no-provider refusal (spec section 10, decided YES). It returns before
// the prepare line's position, so without its own line it produces no host
// record at all - and the operator who fixes it (unset RELAY_WORKSPACE_ROOT, or
// a failed p4 preflight) is standing at this host.
func TestRunner_ASourceTaskWithNoProviderLogsWhyOnTheHost(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 16)

	// No SetProviderForTest call: r.provider is nil.
	r, runCtx := newRunner("noprov-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "noprov-task",
		Commands: singleCmd(echoTaskCmd()),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	})

	out := logged()
	assert.Contains(t, out, "no workspace provider")
	assert.Contains(t, out, "RELAY_WORKSPACE_ROOT", "the line must name what the operator has to fix")
	assert.Contains(t, out, "noprov-task")
	assert.NotContains(t, out, "exec step", "the command must not run when the provider is nil")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/agent/... -run "TestRunner_APrepareFailureIsOnTheHostLog|TestRunner_ASourceTaskWithNoProviderLogsWhy" -v -count=1`

Expected: both FAIL on their first `assert.Contains`.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/runner.go`'s `Run`:

Immediately **before** the `r.send(...)` in the `if task.Source != nil && r.provider == nil` block:

```go
		// The one prepare failure that is identical on every task this worker
		// accepts, and the one whose remedy is a change to THIS host - so it gets
		// a host line as well as the coordinator-side one the server stores from
		// ErrorMessage. TestRunner_ASourceTaskWithNoProviderLogsWhyOnTheHost.
		log.Printf("runner: no workspace provider for %s; refusing its source spec (check p4 preflight / RELAY_WORKSPACE_ROOT)", r.taskID)
```

Immediately **before** `handle, err := r.provider.Prepare(ctx, r.taskID, task.Source, progress)`:

```go
		log.Printf("runner: preparing workspace for %s", r.taskID)
```

Immediately **before** the `r.send(...)` inside `if err != nil {` (the `PREPARE_FAILED` send):

```go
			// The record that survives when the send does not: this is the only
			// trace of the cause on the worker host if the connection is gone.
			// TestRunner_APrepareFailureIsOnTheHostLogWithItsCause.
			log.Printf("runner: prepare failed for %s: %v", r.taskID, err)
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/agent/... -run "TestRunner_APrepareFailureIsOnTheHostLog|TestRunner_ASourceTaskWithNoProviderLogsWhy" -v -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_lifecycle_log_test.go
git commit -m "agent: log the prepare phase and both of its failure paths on the host"
```

---

## Lane B Task 5: B3 - each step's exit

**Files:**
- Modify: `internal/agent/runner_lifecycle_log_test.go`
- Modify: `internal/agent/runner.go`

- [ ] **Step 1: Write the failing test**

**Sketch.** `echoArgv` and `failArgv` already exist in `runner_multistep_test.go`, same package.

```go
// B3 - lines 3 and 4 of the lifecycle, with the right indices, on both a
// successful step and a failing one. Without the exit line, an operator watching
// a wedged agent cannot tell "still running step 2" from "step 2 finished and
// nothing happened next".
func TestRunner_EveryStepLogsItsStartAndItsExit(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 64)

	r, runCtx := newRunner("steps-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "steps-task",
		Commands: []*relayv1.CommandLine{
			{Argv: echoArgv("alpha")},
			{Argv: failArgv()},
		},
	})

	out := logged()
	assert.Contains(t, out, "exec step 1/2")
	assert.Contains(t, out, "exec step 2/2")
	assert.Contains(t, out, "step 1/2 for steps-task exited (exit=0")
	assert.Contains(t, out, "step 2/2 for steps-task exited (exit=7")
	assert.Equal(t, 2, strings.Count(out, "exited ("),
		"one exit line per step that ran, no more: %s", out)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/agent/... -run TestRunner_EveryStepLogsItsStartAndItsExit -v -count=1`

Expected: FAIL on `step 1/2 for steps-task exited (exit=0`. The two `exec step` assertions are already green from Task 3 - they are controls here, not the RED.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/runner.go`'s per-step loop, insert immediately **after** the `lastExitCode` computation block (the `if cmd.ProcessState != nil { ... }` block) and **before** `if waitErr == nil { continue }`:

```go
		// After lastExitCode is computed and before either loop exit, so every
		// path out of a step logs exactly once. TestRunner_EveryStepLogsItsStartAndItsExit.
		exit := "unknown"
		if lastExitCode != nil {
			exit = strconv.Itoa(int(*lastExitCode))
		}
		log.Printf("runner: step %d/%d for %s exited (exit=%s, err=%v)", step, stepTotal, r.taskID, exit, waitErr)
```

`strconv` is already imported in this file. Do not add it twice.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/agent/... -run TestRunner_EveryStepLogsItsStartAndItsExit -v -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_lifecycle_log_test.go
git commit -m "agent: log each step's exit code and wait error"
```

---

## Lane B Task 6: the `setStreamErr` fixture setter

**Files:**
- Modify: `internal/agent/source/perforce/fixtures_test.go`

`fakeRunner` already declares and reads a `streamErr` map in `Stream`, but there is **no setter** for it - so no test can currently drive a `SyncStream` failure. B5 needs one.

- [ ] **Step 1: Add the setter**

Immediately after `setStream`:

```go
// setStreamErr makes Stream return err for the given args key without invoking
// onLine, which is how a test drives a p4 sync FAILURE. The streamErr map has
// existed since fakeRunner was written and has never had a setter, so nothing
// could reach Stream's error arm.
func (f *fakeRunner) setStreamErr(key string, err error) {
	f.streamErr[key] = err
}
```

- [ ] **Step 2: Verify the package still compiles and is green**

Run: `go test ./internal/agent/source/perforce/... -count=1`

Expected: PASS. An unused method on a test type is not a compile error in Go, so this step is a compile check only.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/source/perforce/fixtures_test.go
git commit -m "perforce: give the fake runner a Stream-error setter"
```

---

## Lane B Task 7: B4 and B5 - the provider's sync bracket lines

**Files:**
- Create: `internal/agent/source/perforce/perforce_progress_test.go`
- Modify: `internal/agent/source/perforce/perforce.go`

- [ ] **Step 1: Write the failing tests**

**Sketch.** Copy the fixture block from `TestProvider_PrepareCreatesClientAndSyncs` verbatim - it is the working recipe for driving `Prepare` through `fakeRunner` without p4d.

```go
package perforce

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

func countLinesContaining(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// B4 - the shape and the bound. ONE start and ONE complete, and no per-file line
// of the provider's own: per-file progress is a separate item. The p4 output
// lines the runner already forwards through `progress` must keep flowing - this
// test must not turn into "the sync emits nothing".
func TestProvider_PrepareBracketsTheSyncWithExactlyOneStartAndOneCompleteLine(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	expectedClient := expectedClientName("h", "//s/x")
	fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("client -o -S //s/x "+expectedClient, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+expectedClient+" changes -c "+expectedClient+" -s pending -l", "")
	fr.setStream("-c "+expectedClient+" sync -q --parallel=4 //s/x/...@12345", "1 of 3 files\n2 of 3 files\n3 of 3 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	var lines []string
	h, err := p.Prepare(context.Background(), "task-1", spec, func(s string) { lines = append(lines, s) })
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	assert.Equal(t, 1, countLinesContaining(lines, "[sync] starting"),
		"exactly one start bracket, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "[sync] complete"),
		"exactly one complete bracket, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "1 path"),
		"the start line reports how many paths are being synced, got: %v", lines)
	assert.Equal(t, 3, countLinesContaining(lines, "of 3 files"),
		"p4's own output must still reach progress unchanged, got: %v", lines)
}

// B5 - the cross-slice decision (spec 6.3). The cause has exactly ONE home: the
// coordinator stores it from ErrorMessage, carrying the CLASSIFIED text
// including the disk-full remedy. Repeating it here would put it in the log
// twice, in two spellings, with nothing saying which is authoritative. A
// reviewer looking at this diff alone will want to add the error back; this test
// is what stops them.
func TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	expectedClient := expectedClientName("h", "//s/x")
	fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("client -o -S //s/x "+expectedClient, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+expectedClient+" changes -c "+expectedClient+" -s pending -l", "")
	fr.setStreamErr("-c "+expectedClient+" sync -q --parallel=4 //s/x/...@12345",
		fmt.Errorf("exit status 1 (stderr: SYNC-CAUSE-SENTINEL no space left on device)"))

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	var lines []string
	_, err := p.Prepare(context.Background(), "task-1", spec, func(s string) { lines = append(lines, s) })

	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of disk space on this agent's workspace volume",
		"the RETURNED error is where the classified cause lives")
	assert.Contains(t, err.Error(), "SYNC-CAUSE-SENTINEL", "and it still wraps the original")

	assert.Equal(t, 1, countLinesContaining(lines, "[sync] failed"),
		"exactly one failure bracket, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "[sync] starting"),
		"the start bracket must still have gone out, got: %v", lines)
	for _, l := range lines {
		assert.NotContains(t, l, "SYNC-CAUSE-SENTINEL",
			"the cause has one home and it is not this line: %q", l)
	}
	_ = errors.Is // keep the import honest if unused after your edits
}
```

Delete the `errors` import and the trailing `_ = errors.Is` line if you do not end up needing them - that placeholder is an artefact of the sketch, not something to ship.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/agent/source/perforce/... -run "TestProvider_PrepareBracketsTheSync|TestProvider_ASyncFailureProgressLine" -v -count=1`

Expected: both FAIL on their `[sync] starting` count assertion (`0` observed, `1` expected). B5's `out of disk space` assertion is already green from slice 2 - it is a control here confirming the two slices compose.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/source/perforce/perforce.go`, in the **second** `if needsSync {` block (the one containing the `SyncStream` call, not the `recoverOrphanedCLs` one), rewrite it as:

```go
	if needsSync {
		// BRACKETS AROUND THE p4 OUTPUT, AND THE FAILURE LINE CARRIES NO CAUSE.
		// The error returned below becomes ErrorMessage on the agent's
		// PREPARE_FAILED, and the coordinator stores THAT - classified and
		// wrapped - as the task's last log line. Repeating it here would put the
		// cause in the log twice in two spellings with nothing saying which is
		// authoritative. TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause
		// is the guard.
		progress(fmt.Sprintf("[sync] starting: %d path(s)", len(syncSpecs)))
		if err := p.cfg.Client.SyncStream(ctx, wsRoot, clientName, syncSpecs, progress); err != nil {
			progress("[sync] failed; the cause is reported on the task's final status")
			handle.Release()
			return nil, classifyP4Error(fmt.Errorf("p4 sync: %w", err))
		}
		progress("[sync] complete")
		if curOK {
			_ = reg.Mutate(shortID, func(e *WorkspaceEntry) {
				e.BaselineHash = baseline
				e.LastUsedAt = time.Now()
			})
		}
		_ = reg.Save()
	}
```

Note the ordering that makes this work with no new mechanism: `flushProgress()` runs after `Prepare` returns and before the `PREPARE_FAILED` send in `Runner.Run`, and `sendCh` is FIFO, so the coordinator stores the sync lines, then the `[failed]` line, then the terminal status.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/agent/source/perforce/... -run "TestProvider_PrepareBracketsTheSync|TestProvider_ASyncFailureProgressLine" -v -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_progress_test.go
git commit -m "perforce: bracket the sync with progress lines that carry no cause"
```

---

## Lane B Task 8: the mutation battery

**Files:** none committed. Evidence for the report.

- [ ] **Step 1: Green baseline**

Run: `go test ./internal/agent/... -count=1`

Expected: PASS. **A compile error is not a kill.**

- [ ] **Step 2: Copy `runner.go` and `perforce.go` to the scratchpad, then mutate**

Restore from the copy after each and re-run to confirm green before the next.

| # | Mutation | Must go RED | Must stay GREEN |
|---|---|---|---|
| Q1 | In `runner.go`'s step line, `argv[0]` -> `strings.Join(argv, " ")` | **B1** | B2, B3, B6 |
| Q2 | Delete the `prepare failed for %s: %v` line | **B2** | B1, B3, B6 |
| Q3 | Delete the `no workspace provider` line | **B6** | B1, B2, B3 |
| Q4 | Move the step-exit line above the `if waitErr == nil { continue }` -> place it after that check instead, so a successful step logs no exit | **B3** (`exec step 1/2 ... exited (exit=0` missing, and the count drops to 1) | B1, B2, B6 |
| Q5 | In `perforce.go`, delete `progress("[sync] complete")` | **B4** | B5 |
| Q6 | In `perforce.go`, change the failure line to `progress("[sync] failed: " + err.Error())` | **B5**, the sentinel loop | B4 |
| Q7 | In `perforce.go`, move `progress(...startting...)` inside the `if err != nil` branch | **B4** and **B5** (both assert the start bracket) | - |

- [ ] **Step 3: Survivor controls**

| # | Mutation | Why it must survive |
|---|---|---|
| T1 | Change the prepare line's wording from `preparing workspace for %s` to `preparing the workspace for %s` | Only B2 reads that line, and it reads `"preparing workspace"` - **so this one is a KILL for B2 and a survivor for B1, B3, B4, B5, B6.** Record it as a partial: it confirms the harness discriminates. |
| T2 | Reorder the two independent `fr.set("client -o ...")` and `fr.set("client -i", ...)` fixture lines in `perforce_progress_test.go` | Fixture ordering is irrelevant to `fakeRunner`, which is a map. **Every** test must stay GREEN. A RED here means the harness is failing everything. |

- [ ] **Step 4: Record every result in the report**

Name, for each kill, the guard the mutation actually broke - not just the test that went red.

---

## Lane B Task 9: full-lane gate

- [ ] **Step 1: Run the lanes the change can reach**

```
go test ./... -count=1
go test -tags integration -p 1 ./internal/agent/... -timeout 900s
```

The Perforce integration lane needs Docker and the `p4` CLI. **If it is genuinely unavailable, say so plainly in the report rather than substituting `-count=N` for it** - repetition raises confidence in flakiness, not in coverage.

- [ ] **Step 2: Report the tree state and hand back**

Print `git status --porcelain` and `git log --oneline origin/main..HEAD`.

---

## Self-review against the spec

**Spec coverage, section by section:**

| Spec section | Task |
|---|---|
| 4.1 stream is `stderr`, `prepare` is out of scope | Lane A Task 1 (`errorMessageLogStream`), A1's stream assertion, M10 |
| 4.2 write above the retry branch; publish before the status event | Lane A Task 3 (placement), Task 4 (A2), A5, M2, M3 |
| 4.3 `terminal && ErrorMessage != ""`, no T0-writability pre-filter | Lane A Task 6 (A8, A9), M6, M7 |
| 4.4 bound, UTF-8, NUL | Lane A Task 1 (sanitiser + unit test), Task 6 (A6, A7), M4, M5 |
| 4.5 drop, count nothing, log nothing | Lane A Task 7 (A10), M8, M9 |
| 4.6 one cutoff, one publish, zero-line test diff | Lane A Task 2, including E1-E4 and its survivor control |
| 4.7 attacker size documented, not engineered away | Lane A Task 9, README task-log bullet |
| 4.8 A1-A10 plus the unchanged `handleTaskLog` suite | Lane A Tasks 3-7, gated by Task 2 Step 4 |
| 4.9 comment discipline, name the FILE not the identifier | Lane A Task 3's comment block |
| 5.1 phrase not words, four substrings | Lane B Task 2, P1-P4 |
| 5.2 the message, hyphen, `raise`, no exclusions | Lane B Task 2 Step 3 |
| 5.3 positives, capitalisation, negatives | Lane B Task 2 Step 1, plus **Task 1** which is what makes the negatives real |
| 5.4 one comment, name the negative test | Lane B Task 2 Step 3 |
| 6.1 four host lines, plus the fifth (section 10 decided YES) | Lane B Tasks 3, 4, 5 |
| 6.2 `argv[0]` narrowing bounds the new surface only | Lane B Task 3, comment and B1's own doc comment |
| 6.3 three progress lines, failure line carries no cause | Lane B Task 7 |
| 6.4 B1-B5, no `t.Parallel`, unconditional restore | Lane B Task 3's `captureAgentLog`, Tasks 4, 5, 7 |
| 6.5 comment discipline | Lane B Tasks 3, 4, 7 |
| 7 lane split | Slice independence declaration |
| 8 five decisions | Held; each is named at the task that implements it |
| 10 open question | Decided YES; Lane B Task 4 (B6) |

**Spec items deliberately NOT implemented** (spec section 2, all seven): no `prepare` stream value, no new or renamed counter, no closing of the command-argument exposure (`sendStepMarker` still writes the whole argv into `task_logs` and that needs its own item), no per-task task-log volume cap, no persisted `TASK_STATUS_PREPARING`, no touching the four existing em-dashed `classifyP4Error` messages, no "sync skipped" line.

**Two follow-up items the conductor should file at Phase 6:**

1. **The residual command-argument exposure.** `Runner.sendStepMarker` writes `strings.Join(argv, " ")` into `task_logs`, so a secret passed as a command argument is already stored verbatim and readable through `GET /v1/tasks/{id}/logs`, `relay logs` and the SPA. Slice 3's `argv[0]` narrowing bounds only the new host-log surface. This needs its own decision - redact at the agent, refuse secrets in argv at ingest, or accept and document.
2. **`preparing` must be added to `AppendTaskLog`'s status allow-list at the same time it enters the vocabulary.** `TASK_STATUS_PREPARING` is already in the proto and the agent already streams `LOG_STREAM_PREPARE` chunks. When `feature-2026-09-03-preparing-task-status` lands, a prepare-failure line for a row sitting in `preparing` is dropped with no error and no log line. That spec should cite this one. Slice 1 does not create the requirement - the existing prepare-progress chunks have it already - but it doubles what is lost if it is missed.

**Type consistency check.** Symbols introduced here and used consistently throughout: `MaxAgentErrorMessageBytes` (exported const), `errorMessageLogStream` (unexported const), `sanitizeAgentErrorMessage` (unexported func), `SanitizeAgentErrorMessageForTest` (export_test seam), `(*Handler).trailingLogCutoff`, `(*Handler).publishTaskLog(taskIDStr, stream, content string, row store.AppendTaskLogRow)`, `captureAgentLog(t)`, `countLinesContaining(lines []string, sub string) int`, `(*fakeRunner).setStreamErr(key string, err error)`. No symbol is spelled two ways.
