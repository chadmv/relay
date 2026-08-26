# relay logs envelope drift - implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `relay logs <job-id>` actually print a job's task logs - decode the pagination envelope the server has written since `a90c727`, page it to the end, and fail loudly on stderr with a non-zero exit when it cannot.

**Architecture:** One package-private envelope type plus a bounded paging loop in `internal/cli/logs.go`, streaming each page to the output writer as it arrives. `printTaskLogs` stops swallowing errors and returns the last seq it printed together with the reason it stopped; `watchJobLogs` counts log failures and reports each on an error writer; `doLogs` and `doSubmit` turn a non-zero failure count into a printed, non-silent error so `Dispatch` exits 1 with a message. All four fake servers in `internal/cli/logs_test.go` stop hand-writing a JSON literal and route through one behavioural simulator of `handleGetTaskLogs`.

**Tech Stack:** Go 1.26, stdlib `net/http` + `net/http/httptest`, `github.com/stretchr/testify/require`, `relay/internal/relayclient`.

Spec: `docs/superpowers/specs/2026-08-26-relay-logs-envelope-drift.md`
Backlog item: `docs/backlog/bug-2026-08-25-relay-logs-prints-nothing-envelope-drift.md`

---

## Slice independence declaration

**This is a single backend/CLI slice. There is ZERO `web/` work in this plan.**

- Files touched are `internal/cli/logs.go`, `internal/cli/logs_test.go`, `internal/cli/jobs.go`, `internal/cli/jobs_test.go`, `README.md`, and one `git mv` under `docs/backlog/`.
- No frontend slice exists, so there is nothing for `relay-frontend-engineer` to run in parallel in Phase 3. One engineer (`relay-backend-engineer`) executes every task in this plan, in order.
- For the conductor's Phase 4 fan-out: this is a **Go diff with no `web/` component**. The integration lane brief's "skip on a zero-Go diff" carve-out does **not** apply - there is Go to test - but no `//go:build integration` test exists in `internal/cli`, so see "Verification gates" below for what the integration lane can and cannot do here.
- The tasks within the slice are **sequential, not independent**. Task N's implementation is the thing Task N+1's test is written against. Do not reorder.

---

## What this plan refutes or changes in the spec

Read this section before writing code. Six spec claims did not survive contact with the tree.

1. **`watchJobLogs` has a second PRODUCTION caller the spec never mentions: `doSubmit` in `internal/cli/jobs.go`.** The spec says "Its two callers are closures in the same function, so the ripple is local" - that sentence is true of `printTaskLogs` and false of `watchJobLogs`. `relay submit` (without `--detach`) calls `watchJobLogs` and would fail to compile the moment its signature changes. Scope therefore includes `doSubmit`, `SubmitCommand`, and the one `doSubmit` call in `internal/cli/jobs_test.go`. `relay submit` also gets the same loud-failure behaviour, because leaving it silent would re-create this exact bug in the sibling command.

2. **The spec's "six existing `doLogs`/`watchJobLogs` call sites in `logs_test.go`" is an undercount. There are eight**: `watchJobLogs` at six sites and `doLogs` at two, plus a ninth site in `jobs_test.go` (`doSubmit`). Counted by symbol, not by line.

3. **The spec's `writeTaskLogPage(w, r, rows []taskLogEntry)` signature builds the fixture out of the type under test, which is the same defect class this slice exists to fix.** If `taskLogEntry`'s json tag were misspelled, the helper would marshal the same misspelling and every test would stay green against a CLI that cannot talk to the real server. The helper takes a **test-local `logRow` type with its own hand-written tags mirroring `api.logEntry`**, and marshals the envelope through a test-local anonymous struct, never `taskLogPage`. This is the single most important correction in this plan.

4. **The handler does not "cap at 200" - it 400s.** `handleGetTaskLogs` rejects `limit` outside `1..200` with `"limit must be 1..200"`; it never clamps. The simulator must reject, not clamp, or it is lying in a new way.

5. **`printTaskLogs` returning only `error` cannot carry "the last seq successfully printed", which the spec's own diagnostic requires.** Decision: `printTaskLogs` returns `(lastSeq int64, err error)`. The caller owns one format string naming the task, the task id and `lastSeq`; the error's text is the tail reason. This gives all three stop reasons (fetch failure, non-advancing cursor, page cap) one sentence shape and one place to change it. No named error type - there is no consumer that needs to branch on the reason.

6. **The spec's example diagnostic tail `request failed (500)` is the wrong client message for a 500.** `relayclient.Client.Do` returns `request failed (%d)` only for 4xx; a 500 with no JSON body yields `server error (500) - try again`. Consequence: **never pin the wrapped client text in a test.** Pin the parts this slice owns - the task name, the task id, `stopped after seq N`, `truncated after N pages`, `did not advance`.

Also corrected, smaller:

- The spec's `error: logs incomplete for 1 of 3 tasks` needs a denominator `watchJobLogs` does not return. This plan uses `logs incomplete for %d of the job's tasks`, which reads correctly at 1 and needs only the failure count.
- The diagnostic prints the **full** task id, not the spec's 8-character prefix. A truncation rule with no consumer is a rule to maintain, and the full id is what an operator pastes into `curl`.
- **T5's `maxLogPages` is set to 50 in the test, not left at the 10000 default.** The spec wants T5 to prove the non-advancing guard fires before the cap; 50 proves that just as well as 10000, and it makes the mandatory mutation check (delete the guard, watch the test go red) cost 50 HTTP requests instead of 10000. The property under test is precedence, not the default's value.

Confirmed as the spec states, re-checked here: four fake servers hand-write the bare array; `since_seq` is exclusive (`WHERE id > $2`); `next_seq` is the last returned row's id, zeroed when the page is short; `Dispatch` in `internal/cli/command.go` prints `error: <err>` and returns 1 for any non-`silentError`; `readPasswordFn` in `internal/cli/login.go` is the package-var testability-override precedent, so a `var maxLogPages` is shrinkable from a same-package test; `doAgentEnroll(ctx, args, cfg, out, errOut io.Writer)` is the in-package `errOut` precedent.

---

## Critical files

| File | Role |
|---|---|
| `internal/cli/logs.go` | The defect and the whole fix. `printTaskLogs`, `watchJobLogs`, `doLogs`, `LogsCommand`. |
| `internal/cli/logs_test.go` | The stale fixture. Four fake servers, eight `doLogs`/`watchJobLogs` call sites, six new tests. |
| `internal/cli/jobs.go` | `doSubmit`/`SubmitCommand` - the second `watchJobLogs` caller. Signature ripple only, plus the same failure-count check. |
| `internal/cli/jobs_test.go` | One `doSubmit` call site to update. |
| `internal/api/tasks.go` | **Read-only reference.** `handleGetTaskLogs` and `logEntry` are the contract the fixture simulates. Do not edit. |
| `internal/worker/handler_tasklog_e2e_integration_test.go` | **Read-only reference.** Contains a working `since_seq` -> `next_seq == 0` paging loop against the real handler. Read it before writing the loop. |
| `internal/relayclient/client.go` | **Read-only reference.** `Do` error texts; `PageRequestLimit` lives in `page.go`. |
| `README.md` | `#### relay logs` and `#### relay submit` prose corrections. |

---

## Task 1: The fixture tells the truth, and that is the RED

This task changes **test files only**. No production file is touched. Its purpose is to produce and record the measurement the backlog item demands: the new fixture plus the unchanged production decode is RED.

**Files:**
- Modify: `internal/cli/logs_test.go` (`fakeJobServer`, `fakeCompletedJobServer`, `fakeRaceJobServer`, `fakeOverlapJobServer`)

- [ ] **Step 1: Add the simulator and its row type to `internal/cli/logs_test.go`**

Add these imports to the existing import block: `"strconv"`. (`time` is already imported; `errors` is added in Task 3.)

Insert immediately after the import block, before `fakeJobServer`:

```go
// logRow is the wire shape handleGetTaskLogs writes for one row - the api
// package's unexported logEntry (internal/api/tasks.go), reproduced here with
// its own json tags.
//
// It is deliberately NOT the CLI's own taskLogEntry. A fixture built out of the
// type under test cannot detect drift in that type: if taskLogEntry's tags were
// wrong, the fixture would marshal the same wrong keys and the whole suite would
// stay green against a CLI that cannot talk to the real server. That is exactly
// the failure this file is being changed to fix, so do not "de-duplicate" these
// two structs.
type logRow struct {
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// writeTaskLogPage serves rows the way handleGetTaskLogs (internal/api/tasks.go)
// does. Four behaviours are load-bearing and each is asserted by a test below:
//
//   - ?since_seq is EXCLUSIVE: rows with Seq > since_seq, because the SQL is
//     `WHERE task_id = $1 AND id > $2`. Asserted by
//     TestWatchJobLogs_ExactPageMultiple_NoDropNoDuplicate.
//   - ?limit defaults to 50, and a value outside 1..200 is a 400 - the handler
//     rejects, it does not clamp.
//   - next_seq is the last returned row's seq, or 0 when the page is short.
//     Asserted by TestWatchJobLogs_PagesUntilDrained.
//   - total is the full row count, independent of the page.
//
// Every fake server in this file routes its logs case through here, so editing
// any of those four lines changes what every CLI log test means.
func writeTaskLogPage(w http.ResponseWriter, r *http.Request, rows []logRow) {
	writeErr := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeErr(http.StatusBadRequest, "limit must be 1..200")
			return
		}
		limit = n
	}

	var since int64
	if v := r.URL.Query().Get("since_seq"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(http.StatusBadRequest, "since_seq must be a non-negative integer")
			return
		}
		since = n
	}

	items := make([]logRow, 0, limit)
	for _, row := range rows {
		if row.Seq > since && len(items) < limit {
			items = append(items, row)
		}
	}
	var nextSeq int64
	if len(items) > 0 {
		nextSeq = items[len(items)-1].Seq
	}
	if len(items) < limit {
		nextSeq = 0 // drained
	}

	// Marshalled through a local anonymous struct rather than the CLI's
	// taskLogPage, for the same reason logRow is not taskLogEntry.
	_ = json.NewEncoder(w).Encode(struct {
		Items   []logRow `json:"items"`
		NextSeq int64    `json:"next_seq"`
		Total   int64    `json:"total"`
	}{Items: items, NextSeq: nextSeq, Total: int64(len(rows))})
}

// oneFrameRows is the single row the four fake servers below used to hand-write
// as a bare JSON array. The assertion "[frame-001 stdout] frame rendered" in the
// tests below is this row.
func oneFrameRows() []logRow {
	return []logRow{{
		Seq:       1,
		Stream:    "stdout",
		Content:   "frame rendered",
		CreatedAt: time.Unix(0, 0).UTC(),
	}}
}
```

- [ ] **Step 2: Route all four fake servers through the simulator**

In `fakeJobServer`, `fakeCompletedJobServer`, `fakeRaceJobServer` and `fakeOverlapJobServer`, replace each logs case body. Each currently reads:

```go
		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			json.NewEncoder(w).Encode([]struct {
				Stream  string `json:"stream"`
				Content string `json:"content"`
			}{
				{Stream: "stdout", Content: "frame rendered"},
			})
```

Replace with, in all four:

```go
		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
```

Also update the doc comment on `fakeJobServer`: change the line `//	GET /v1/tasks/<id>/logs     → log entries` to `//	GET /v1/tasks/<id>/logs     -> one page of the handler's log envelope`.

- [ ] **Step 3: Measure the RED - this is the backlog item's acceptance criterion 3**

Run:

```bash
go test ./internal/cli/ -run 'TestWatchJobLogs|TestRunLogs' -v -timeout 60s
```

Expected: **FAIL**, with exactly these four tests failing and the rest of the package passing:

- `TestWatchJobLogs_TerminalBeforeSubscribe_DoesNotHang` - `Error: "" does not contain "[frame-001 stdout] frame rendered"` (`out` is empty)
- `TestWatchJobLogs_TaskInSnapshotAndStream_PrintedOnce` - `Not equal: expected: 1, actual: 0`
- `TestWatchJobLogs_DoneExits0` - `Error: "" does not contain "[frame-001 stdout] frame rendered"`
- `TestWatchJobLogs_AlreadyDone_PrintsLogsAndExits` - `Error: "" does not contain "[frame-001 stdout] frame rendered"`

`TestWatchJobLogs_FailedReturnsFailed`, `TestWatchJobLogs_AlreadyCancelled_ReturnsCancelled`, `TestRunLogs_DoneExitsCleanly` and `TestRunLogs_FailedReturnsSilentError` still PASS - they assert status, not output.

**Save the exact output** to the scratchpad; it goes verbatim into Task 2's commit message. This one run is the proof that "the new fixture with the production decode unchanged turns the test RED". Do not commit a red tree - Task 1 and Task 2 land in one commit.

If any of the four still passes, stop: the fixture is not being reached, and everything downstream is vacuous.

---

## Task 2: Decode the envelope

**Files:**
- Modify: `internal/cli/logs.go` (`printTaskLogs`)

- [ ] **Step 1: Replace the decode target in `printTaskLogs`**

Add above `LogsCommand` in `internal/cli/logs.go`:

```go
// taskLogPage mirrors the envelope GET /v1/tasks/{id}/logs returns
// (handleGetTaskLogs, internal/api/tasks.go). The handler has written this
// object since 2026-05-08; the CLI decoded a bare array into a slice until
// 2026-08-26, which fails and printed nothing for three and a half months.
type taskLogPage struct {
	Items   []taskLogEntry `json:"items"`
	NextSeq int64          `json:"next_seq"`
	Total   int64          `json:"total"`
}

// taskLogEntry is one row. created_at is deliberately not decoded: the CLI does
// not print it, and an unused field is a maintenance claim this package cannot
// keep. Seq is decoded because the incomplete-log diagnostic names the last seq
// printed.
type taskLogEntry struct {
	Seq     int64  `json:"seq"`
	Stream  string `json:"stream"`
	Content string `json:"content"`
}
```

Replace the body of `printTaskLogs` (keep the existing signature for now - it changes in Task 3):

```go
// printTaskLogs fetches and prints all log lines for a task.
// Errors are silently ignored — best-effort output.
func printTaskLogs(ctx context.Context, c *relayclient.Client, taskID, taskName string, w io.Writer) {
	var page taskLogPage
	if err := c.Do(ctx, "GET", "/v1/tasks/"+taskID+"/logs", nil, &page); err != nil {
		return
	}
	for _, l := range page.Items {
		fmt.Fprintf(w, "[%s %s] %s\n", taskName, l.Stream, l.Content)
	}
}
```

- [ ] **Step 2: Run the four tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestWatchJobLogs|TestRunLogs' -v -timeout 60s
```

Expected: PASS, all eight tests in that filter.

- [ ] **Step 3: Run the whole package**

```bash
go test ./internal/cli/ -count=1 -timeout 120s
```

Expected: `ok relay/internal/cli`.

- [ ] **Step 4: Commit, recording the RED measurement in the message**

```bash
git add internal/cli/logs.go internal/cli/logs_test.go
git commit -m "fix(cli): decode the task-log pagination envelope, and stop the fixture lying

printTaskLogs decoded []struct{Stream,Content} from an endpoint that has
written {\"items\",\"next_seq\",\"total\"} since a90c727 (2026-05-08). The
decode failed on every call and the error was swallowed, so relay logs
printed nothing for every task of every job.

All four fake servers in logs_test.go now route through writeTaskLogPage,
one behavioural simulator of handleGetTaskLogs, instead of hand-writing the
bare array. logRow is declared separately from the CLI's taskLogEntry on
purpose: a fixture built from the type under test cannot detect drift in it.

RED measured once, with the new fixture and the OLD decode in place:

  --- FAIL: TestWatchJobLogs_TerminalBeforeSubscribe_DoesNotHang
  --- FAIL: TestWatchJobLogs_TaskInSnapshotAndStream_PrintedOnce
  --- FAIL: TestWatchJobLogs_DoneExits0
  --- FAIL: TestWatchJobLogs_AlreadyDone_PrintsLogsAndExits

Refs docs/backlog/bug-2026-08-25-relay-logs-prints-nothing-envelope-drift.md"
```

Paste the real captured failure lines from Task 1 Step 3 in place of the four abbreviated ones.

---

## Task 3: Stop swallowing - the error path, the writers, and the exit code

This task changes signatures across two production files and nine call sites. Do it in one task so no test is written twice.

**Files:**
- Modify: `internal/cli/logs.go` (`LogsCommand`, `doLogs`, `watchJobLogs`, `printTaskLogs`)
- Modify: `internal/cli/jobs.go` (`SubmitCommand`, `doSubmit`)
- Modify: `internal/cli/logs_test.go` (eight call sites, plus the new test)
- Modify: `internal/cli/jobs_test.go` (one call site)

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/logs_test.go`. Add `"errors"` to the import block.

```go
// fakeLogsFailServer serves a terminal job whose logs route always 500s.
func fakeLogsFailServer(t *testing.T, jobID, taskID string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A failed log fetch must be distinguishable from an empty log. Before this
// slice the job was "done", so doLogs returned nil and the shell saw exit 0
// with nothing on either stream - the exact production symptom.
func TestWatchJobLogs_LogsFetchFails_ReportsOnStderr(t *testing.T) {
	jobID, taskID := "job-500", "task-500"
	srv := fakeLogsFailServer(t, jobID, taskID)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, logFailures, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err, "a log-fetch failure must not abort the watch")
	require.Equal(t, "done", status)
	require.Equal(t, 1, logFailures)
	require.Empty(t, out.String())
	require.Contains(t, errOut.String(), "frame-001", "the diagnostic names the task")
	require.Contains(t, errOut.String(), taskID, "the diagnostic names the task id")
	require.Contains(t, errOut.String(), "incomplete")

	// And doLogs turns that count into a printed, non-silent error, so the
	// shell sees exit 1 WITH a message rather than the bare exit 1 of
	// silentError{}.
	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out2, errOut2 strings.Builder
	err = doLogs(ctx, cfg, []string{jobID}, &out2, &errOut2)
	require.Error(t, err)
	var se silentError
	require.False(t, errors.As(err, &se),
		"a described log failure must not be silent - Dispatch has to print it")
	require.Contains(t, err.Error(), "logs incomplete")
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/cli/ -run TestWatchJobLogs_LogsFetchFails -v -timeout 60s
```

Expected: FAIL to **compile**, with `too many return values` / `not enough arguments in call to watchJobLogs` and `not enough arguments in call to doLogs`. A compile failure is the correct RED for a signature change; do not treat it as a pass, and do not proceed until the compiler names both functions.

- [ ] **Step 3: Change `printTaskLogs` to report instead of swallow**

In `internal/cli/logs.go`, replace `printTaskLogs` entirely:

```go
// printTaskLogs fetches a task's log and writes every line to out. It returns
// the seq of the last row written (0 when nothing was written) and the reason
// it stopped early, or a nil error when the server reported the log as drained.
//
// The last seq is returned rather than logged here because the caller owns the
// diagnostic's wording, and the seq is what makes that diagnostic actionable:
// it tells an operator where the output stops and what since_seq to resume from
// by hand.
func printTaskLogs(ctx context.Context, c *relayclient.Client, taskID, taskName string, out io.Writer) (int64, error) {
	var lastSeq int64
	var page taskLogPage
	path := fmt.Sprintf("/v1/tasks/%s/logs?since_seq=%d&limit=%d", taskID, 0, relayclient.PageRequestLimit)
	if err := c.Do(ctx, "GET", path, nil, &page); err != nil {
		return lastSeq, err
	}
	for _, l := range page.Items {
		fmt.Fprintf(out, "[%s %s] %s\n", taskName, l.Stream, l.Content)
		lastSeq = l.Seq
	}
	return lastSeq, nil
}
```

(The paging loop replaces this body in Task 4. It is written single-page here so that this task's diff is the error plumbing and nothing else.)

- [ ] **Step 4: Thread the error writer and the failure count through `watchJobLogs`**

Replace the signature line and the two `printTaskLogs` call sites in `internal/cli/logs.go`. The full new `watchJobLogs` header and the changed lines:

```go
// watchJobLogs subscribes to SSE events for jobID, then takes a snapshot so a job
// that went terminal before the subscribe is still caught (the broker has no replay).
// When a task reaches a terminal state its logs are fetched and printed once.
// Returns the final job status ("done", "failed", or "cancelled"), the number of
// tasks whose logs could not be printed in full, and any error.
//
// A log failure never aborts the watch: the remaining tasks still stream and
// print. It is reported on errOut immediately and counted, and doLogs turns a
// non-zero count into a non-silent error.
func watchJobLogs(ctx context.Context, c *relayclient.Client, jobID string, out, errOut io.Writer) (string, int, error) {
	taskNames := make(map[string]string)
	printed := make(map[string]bool)
	var finalStatus string
	logFailures := 0

	// emit prints one task's log and reports an incomplete one on errOut. One
	// diagnostic per failing task, naming the task, the task id and the last
	// seq written; the error's own text is the reason it stopped.
	emit := func(taskID, taskName string) {
		lastSeq, err := printTaskLogs(ctx, c, taskID, taskName, out)
		if err != nil {
			logFailures++
			fmt.Fprintf(errOut, "relay: logs for task %s (%s) are incomplete - stopped after seq %d: %v\n",
				taskName, taskID, lastSeq, err)
		}
	}
```

Inside `onSubscribed`, replace `printTaskLogs(ctx, c, t.ID, t.Name, w)` with `emit(t.ID, t.Name)`.
Inside `handler`, replace `printTaskLogs(ctx, c, data.ID, taskNames[data.ID], w)` with `emit(data.ID, taskNames[data.ID])`.

Replace the three return statements at the end of `watchJobLogs`:

```go
	if err := c.StreamEvents(ctx, "/v1/events?job_id="+jobID, onSubscribed, handler); err != nil {
		return "", logFailures, err
	}
	if finalStatus == "" {
		return "", logFailures, fmt.Errorf("connection lost — job %s may still be running", jobID)
	}
	return finalStatus, logFailures, nil
}
```

(The em dash in `connection lost — job` is pre-existing text; leave it byte-identical so no existing assertion moves. All NEW strings in this plan use hyphens.)

- [ ] **Step 5: Update `doLogs` and `LogsCommand`**

```go
// LogsCommand returns the relay logs Command.
func LogsCommand() Command {
	return Command{
		Name:  "logs",
		Usage: "logs <job-id>  - print each task's log as the task finishes, until the job is done",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doLogs(ctx, cfg, args, os.Stdout, os.Stderr)
		},
	}
}

func doLogs(ctx context.Context, cfg *Config, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay logs <job-id>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	status, logFailures, err := watchJobLogs(ctx, c, args[0], out, errOut)
	if err != nil {
		return err
	}
	// A described failure takes precedence over silentError{}: both exit 1, and
	// the described one is strictly more informative. Silence is the thing being
	// fixed, so where the two compete silence loses.
	if logFailures > 0 {
		return fmt.Errorf("logs incomplete for %d of the job's tasks", logFailures)
	}
	if status != "done" {
		return silentError{}
	}
	return nil
}
```

- [ ] **Step 6: Update `doSubmit` and `SubmitCommand` in `internal/cli/jobs.go`**

`doSubmit` is the second production caller of `watchJobLogs` and would otherwise keep the silence this slice removes.

```go
			return doSubmit(ctx, cfg, args, os.Stdout, os.Stderr)
```

```go
func doSubmit(ctx context.Context, cfg *Config, args []string, w, errOut io.Writer) error {
```

and, at the tail of `doSubmit`, replace the `watchJobLogs` block:

```go
	status, logFailures, err := watchJobLogs(ctx, c, job.ID, w, errOut)
	if err != nil {
		return err
	}
	if logFailures > 0 {
		return fmt.Errorf("logs incomplete for %d of the job's tasks", logFailures)
	}
	if status != "done" {
		return silentError{}
	}
	return nil
}
```

- [ ] **Step 7: Update the nine existing call sites (mechanical, no behaviour change)**

In `internal/cli/logs_test.go`, every `var out strings.Builder` in a test that calls `watchJobLogs` or `doLogs` becomes `var out, errOut strings.Builder`, and:

- `status, err := watchJobLogs(ctx, c, jobID, &out)` becomes `status, logFailures, err := watchJobLogs(ctx, c, jobID, &out, &errOut)`, with `require.Equal(t, 0, logFailures)` added after the existing `require.NoError(t, err)`. Six sites: `TestWatchJobLogs_TerminalBeforeSubscribe_DoesNotHang`, `TestWatchJobLogs_TaskInSnapshotAndStream_PrintedOnce`, `TestWatchJobLogs_DoneExits0`, `TestWatchJobLogs_FailedReturnsFailed`, `TestWatchJobLogs_AlreadyDone_PrintsLogsAndExits`, `TestWatchJobLogs_AlreadyCancelled_ReturnsCancelled`.
- `err := doLogs(context.Background(), cfg, []string{jobID}, &out)` becomes `err := doLogs(context.Background(), cfg, []string{jobID}, &out, &errOut)`. Two sites: `TestRunLogs_DoneExitsCleanly`, `TestRunLogs_FailedReturnsSilentError`.

In `internal/cli/jobs_test.go`, `TestSubmitJob_DetachPrintsID`: add `errOut` and pass it.

```go
	var out, errOut strings.Builder
	err = doSubmit(context.Background(), cfg, []string{"--detach", f.Name()}, &out, &errOut)
```

- [ ] **Step 8: Run the new test and the package**

```bash
go test ./internal/cli/ -run TestWatchJobLogs_LogsFetchFails -v -timeout 60s
go test ./internal/cli/ -count=1 -timeout 120s
```

Expected: PASS, and `ok relay/internal/cli`. `TestRunLogs_FailedReturnsSilentError` must still pass - its fake server serves logs successfully, so `logFailures` is 0 and the `silentError{}` branch is still reached for a failed job.

- [ ] **Step 9: Commit**

```bash
git add internal/cli/logs.go internal/cli/logs_test.go internal/cli/jobs.go internal/cli/jobs_test.go
git commit -m "fix(cli): relay logs reports a failed log fetch instead of swallowing it

printTaskLogs returned nothing on any error under a best-effort comment, so
a broken call was indistinguishable from a task with no output and the
command exited 0. It now returns the last seq it printed and the reason it
stopped; watchJobLogs prints one diagnostic per failing task on stderr and
counts the failures; doLogs and doSubmit turn a non-zero count into a
non-silent error, so Dispatch prints it and exits 1.

The described error takes precedence over silentError{} for a non-done job:
both exit 1 and the described one is strictly more informative.

doSubmit is the second production caller of watchJobLogs and gets the same
treatment - leaving it silent would re-create this bug in relay submit."
```

---

## Task 4: Page to the end

**Files:**
- Modify: `internal/cli/logs.go` (`printTaskLogs`)
- Modify: `internal/cli/logs_test.go` (new server + three tests)

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/logs_test.go`:

```go
// fakeLogPagingServer serves a job that is already done with one finished task,
// and answers the logs route through writeTaskLogPage - so the paging contract
// under test is the handler's, not a literal. It records one entry per logs
// request so a test can assert how the client paged.
type fakeLogPagingServer struct {
	*httptest.Server

	mu        sync.Mutex
	requests  int
	sinceSeqs []string
	limits    []string
	failFrom  int // when > 0, the Nth and later logs requests return 500
}

func newFakeLogPagingServer(t *testing.T, jobID, taskID string, rows []logRow, failFrom int) *fakeLogPagingServer {
	t.Helper()
	f := &fakeLogPagingServer{failFrom: failFrom}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			f.mu.Lock()
			f.requests++
			n := f.requests
			f.sinceSeqs = append(f.sinceSeqs, r.URL.Query().Get("since_seq"))
			f.limits = append(f.limits, r.URL.Query().Get("limit"))
			f.mu.Unlock()
			if f.failFrom > 0 && n >= f.failFrom {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeTaskLogPage(w, r, rows)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// There is no /v1/events case: an unmatched request returns 200 with an empty
// body, which StreamEvents treats as a live subscription that immediately ends.
// onSubscribed then sees the terminal job and prints. fakeCompletedJobServer
// relies on the same thing.

func (f *fakeLogPagingServer) stats() (int, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, append([]string(nil), f.sinceSeqs...), append([]string(nil), f.limits...)
}

// genRows returns n rows with CONTIGUOUS seq ids 1..n and content "line <seq>",
// so an assertion naming a line names a specific row.
//
// The contiguity is load-bearing. task_logs.id is a global BIGSERIAL, so
// per-task seqs are usually gapped - and a gapped fixture makes the classic
// off-by-one invisible, because `id > lastSeq+1` and `id > lastSeq` return the
// same rows when no id equals lastSeq+1. Contiguous ids are a legitimate
// production state (one task logging alone) and are the discriminating input.
// Do not "make this more realistic" by introducing gaps.
func genRows(n int) []logRow {
	rows := make([]logRow, n)
	for i := range rows {
		rows[i] = logRow{
			Seq:       int64(i + 1),
			Stream:    "stdout",
			Content:   fmt.Sprintf("line %d", i+1),
			CreatedAt: time.Unix(0, 0).UTC(),
		}
	}
	return rows
}

func outLines(t *testing.T, out string) []string {
	t.Helper()
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

func TestWatchJobLogs_PagesUntilDrained(t *testing.T) {
	jobID, taskID := "job-page", "task-page"
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(450), 0)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, logFailures, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 0, logFailures)
	require.Empty(t, errOut.String())

	require.Contains(t, out.String(), "[frame-001 stdout] line 450",
		"the last row of the last page must be printed - a single-page client stops at 200")
	lines := outLines(t, out.String())
	require.Len(t, lines, 450)
	require.Equal(t, "[frame-001 stdout] line 1", lines[0])
	require.Equal(t, "[frame-001 stdout] line 450", lines[449])

	requests, sinceSeqs, limits := srv.stats()
	require.Equal(t, 3, requests, "450 rows at limit=200 is two full pages plus one short page")
	require.Equal(t, []string{"0", "200", "400"}, sinceSeqs,
		"the cursor is the previous page's next_seq verbatim - since_seq is exclusive")
	require.Equal(t, []string{"200", "200", "200"}, limits)
}

func TestWatchJobLogs_ExactPageMultiple_NoDropNoDuplicate(t *testing.T) {
	jobID, taskID := "job-exact", "task-exact"
	// 400 CONTIGUOUS rows: page 1 and page 2 are both full, so both carry a
	// non-zero next_seq, and a third request is needed to learn the log is
	// drained. See genRows for why contiguity is the discriminating input.
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(400), 0)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, logFailures, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 0, logFailures)

	require.Contains(t, out.String(), "[frame-001 stdout] line 201\n",
		"since_seq is EXCLUSIVE: paging with lastSeq+1 skips this row entirely")
	require.Equal(t, 1, strings.Count(out.String(), "[frame-001 stdout] line 200\n"),
		"paging with lastSeq-1 would re-return this row")
	require.Len(t, outLines(t, out.String()), 400)

	requests, sinceSeqs, _ := srv.stats()
	require.Equal(t, 3, requests,
		"a full second page carries a non-zero next_seq, so a third (empty) request is required")
	require.Equal(t, []string{"0", "200", "400"}, sinceSeqs)
}

func TestWatchJobLogs_FailsOnSecondPage_PrintsFirstPage(t *testing.T) {
	jobID, taskID := "job-midfail", "task-midfail"
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(400), 2)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, logFailures, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, logFailures)

	// Printed as it went: page 1 survives the failure of page 2. An
	// implementation that accumulates pages and discards them on error fails
	// here and passes TestWatchJobLogs_LogsFetchFails.
	require.Contains(t, out.String(), "[frame-001 stdout] line 1\n")
	require.Contains(t, out.String(), "[frame-001 stdout] line 200\n")
	require.NotContains(t, out.String(), "line 201")
	require.Len(t, outLines(t, out.String()), 200)

	require.Contains(t, errOut.String(), "stopped after seq 200",
		"the diagnostic names where the output stops, so an operator can resume by hand")
	require.Contains(t, errOut.String(), "frame-001")
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go test ./internal/cli/ -run 'TestWatchJobLogs_PagesUntilDrained|TestWatchJobLogs_ExactPageMultiple|TestWatchJobLogs_FailsOnSecondPage' -v -timeout 120s
```

Expected: FAIL, three tests:

- `TestWatchJobLogs_PagesUntilDrained` - `Error: "...[frame-001 stdout] line 200\n" does not contain "[frame-001 stdout] line 450"`, then `Not equal: expected: 3, actual: 1` on the request count.
- `TestWatchJobLogs_ExactPageMultiple_NoDropNoDuplicate` - `Error: ... does not contain "[frame-001 stdout] line 201\n"`.
- `TestWatchJobLogs_FailsOnSecondPage_PrintsFirstPage` - `Not equal: expected: 1, actual: 0` on `logFailures` (only one request is made, and it succeeds).

- [ ] **Step 3: Implement the paging loop**

Replace `printTaskLogs` in `internal/cli/logs.go`:

```go
// maxLogPages bounds the paging loop against a server whose next_seq keeps
// advancing but which never reports the log as drained. 10000 pages at 200 rows
// is 2,000,000 rows: this is a hang bound, not a product limit - no real task
// log approaches it, and reaching it means the server is misbehaving.
//
// It is a var rather than a const so a test can shrink it, following this
// package's testability-override convention (readPasswordFn, saveConfigFn,
// configFilePathFn).
var maxLogPages = 10000

// printTaskLogs pages GET /v1/tasks/{id}/logs and writes every line to out as
// each page arrives. It returns the seq of the last row written (0 when nothing
// was written) and the reason it stopped early, or a nil error when the server
// reported the log as drained.
//
// Printing per page rather than accumulating is deliberate twice over: memory
// stays O(one page) on a multi-hundred-megabyte log, and a failure on page N
// still leaves pages 1..N-1 on the output.
//
// since_seq is EXCLUSIVE server-side - GetTaskLogsPage is
// `WHERE task_id = $1 AND id > $2` - so the cursor is the previous page's
// next_seq verbatim. Never lastSeq+1: task_logs.id is a global BIGSERIAL, so
// when one task is logging alone its ids are contiguous and +1 skips the very
// next row.
//
// The loop is bounded twice and both bounds are needed. The cursor is
// server-supplied and drives a client loop, and the provenance of a value says
// nothing about who controls its content or the timing of the writes behind it.
// next_seq <= since catches a non-advancing cursor on the second request;
// maxLogPages catches an ever-advancing cursor that never drains, which the
// first guard cannot see.
func printTaskLogs(ctx context.Context, c *relayclient.Client, taskID, taskName string, out io.Writer) (int64, error) {
	var lastSeq int64
	since := int64(0)
	for pages := 1; ; pages++ {
		path := fmt.Sprintf("/v1/tasks/%s/logs?since_seq=%d&limit=%d",
			taskID, since, relayclient.PageRequestLimit)
		var page taskLogPage
		if err := c.Do(ctx, "GET", path, nil, &page); err != nil {
			return lastSeq, fmt.Errorf("fetching page %d: %w", pages, err)
		}
		for _, l := range page.Items {
			fmt.Fprintf(out, "[%s %s] %s\n", taskName, l.Stream, l.Content)
			lastSeq = l.Seq
		}
		// Defensive, and ordered first because it is the one arm that is right
		// no matter what the cursor claims. A correct handler assigns next_seq
		// from a returned row, so an empty page always carries next_seq == 0
		// and this never fires against the real server.
		if len(page.Items) == 0 {
			return lastSeq, nil
		}
		if page.NextSeq == 0 {
			return lastSeq, nil // the server says drained
		}
		if page.NextSeq <= since {
			return lastSeq, fmt.Errorf(
				"server cursor did not advance (next_seq %d after since_seq %d)", page.NextSeq, since)
		}
		if pages >= maxLogPages {
			return lastSeq, fmt.Errorf(
				"truncated after %d pages - the server never reported the log as drained", maxLogPages)
		}
		since = page.NextSeq
	}
}
```

Break on `next_seq`, never on `len(items) < limit`: the two agree today, but the second re-derives a rule the server already applied and desynchronizes the moment the server's drain rule changes.

- [ ] **Step 4: Run the three tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestWatchJobLogs_PagesUntilDrained|TestWatchJobLogs_ExactPageMultiple|TestWatchJobLogs_FailsOnSecondPage' -v -timeout 120s
```

Expected: PASS, all three.

- [ ] **Step 5: Prove the incremental-printing test is load-bearing (mutation check)**

`TestWatchJobLogs_FailsOnSecondPage_PrintsFirstPage` exists to pin print-as-you-go. Prove it kills the buffering implementation. Temporarily edit `printTaskLogs` to accumulate:

```go
		// MUTATION - revert after measuring
		buffered = append(buffered, page.Items...)   // instead of the Fprintf loop
```

i.e. collect every page's items into a local slice and only `Fprintf` them after the loop returns nil (so nothing is written on the error return).

Run:

```bash
go test ./internal/cli/ -run 'TestWatchJobLogs_FailsOnSecondPage|TestWatchJobLogs_LogsFetchFails' -v -timeout 120s
```

Expected: `TestWatchJobLogs_FailsOnSecondPage_PrintsFirstPage` FAILS (`does not contain "[frame-001 stdout] line 1\n"`) and `TestWatchJobLogs_LogsFetchFails_ReportsOnStderr` still PASSES - which is the point: the mid-failure test is the only thing that sees the difference.

**Then revert the mutation and keep the test.** If the mutation reports "survived", the mutation did not apply - re-check the edit before concluding anything. Record the kill in the commit message.

- [ ] **Step 6: Run the whole package**

```bash
go test ./internal/cli/ -count=1 -timeout 180s
```

Expected: `ok relay/internal/cli`.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/logs.go internal/cli/logs_test.go
git commit -m "fix(cli): relay logs pages the task log to the end

The CLI ignored next_seq entirely, so even a corrected decode printed at
most the first page and silently truncated a long log - the same class of
silent incompleteness as the original bug.

printTaskLogs now walks ?since_seq= until the server reports the log as
drained, writing each page to the output as it arrives so memory stays
O(one page) and a failure on page N leaves pages 1..N-1 printed. since_seq
is exclusive, so the cursor is the previous page's next_seq verbatim.

Mutation checked: buffering the pages and flushing at the end kills
TestWatchJobLogs_FailsOnSecondPage_PrintsFirstPage and leaves
TestWatchJobLogs_LogsFetchFails_ReportsOnStderr green - the mid-failure
test is what makes print-as-you-go load-bearing. Mutation reverted."
```

---

## Task 5: Both bounds, and proof that each one is load-bearing

**Files:**
- Modify: `internal/cli/logs_test.go` (misbehaving server + two tests)
- `internal/cli/logs.go` is **not** modified in this task - `maxLogPages` and both guards shipped in Task 4. This task's job is to prove they work and that neither can be deleted silently.

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/logs_test.go`:

```go
// fakeNeverDrainingServer is a deliberately MISBEHAVING server, so it does not
// route through writeTaskLogPage (which is a correct simulator). Every logs
// request returns a full page of 200 rows. When advance is true the cursor
// strictly increases and the log never drains; when false the cursor is
// constant, which is the shape that loops forever at zero cost to the server.
func fakeNeverDrainingServer(t *testing.T, jobID, taskID string, advance bool) *fakeLogPagingServer {
	t.Helper()
	f := &fakeLogPagingServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			f.mu.Lock()
			f.requests++
			f.sinceSeqs = append(f.sinceSeqs, r.URL.Query().Get("since_seq"))
			f.mu.Unlock()

			since, _ := strconv.ParseInt(r.URL.Query().Get("since_seq"), 10, 64)
			base := since
			if !advance {
				base = 0 // always the same 200 rows, always the same next_seq
			}
			items := make([]logRow, 200)
			for i := range items {
				seq := base + int64(i) + 1
				items[i] = logRow{Seq: seq, Stream: "stdout", Content: fmt.Sprintf("line %d", seq)}
			}
			_ = json.NewEncoder(w).Encode(struct {
				Items   []logRow `json:"items"`
				NextSeq int64    `json:"next_seq"`
				Total   int64    `json:"total"`
			}{Items: items, NextSeq: base + 200, Total: 1 << 30})
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// These two tests mutate the maxLogPages package var, so they must never be
// t.Parallel() (nothing in this package is).
func TestWatchJobLogs_ServerNeverDrains_StopsAtCap(t *testing.T) {
	old := maxLogPages
	maxLogPages = 3
	defer func() { maxLogPages = old }()

	jobID, taskID := "job-nodrain", "task-nodrain"
	srv := fakeNeverDrainingServer(t, jobID, taskID, true)

	c := relayclient.NewClient(srv.URL, "tok")
	// Bounded so that a missing cap is a failed assertion rather than a hung
	// suite. If this test ever fails with "context deadline exceeded" in errOut,
	// report that plainly: the cap did not fire.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, logFailures, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, logFailures)

	requests, _, _ := srv.stats()
	require.Equal(t, 3, requests, "the loop must stop at maxLogPages requests")
	require.Len(t, outLines(t, out.String()), 600, "everything fetched before the cap is still printed")
	require.Contains(t, errOut.String(), "frame-001")
	require.Contains(t, errOut.String(), "truncated after 3 pages")
	require.NotContains(t, errOut.String(), "context deadline exceeded")
}

func TestWatchJobLogs_CursorDoesNotAdvance_StopsImmediately(t *testing.T) {
	old := maxLogPages
	// Well above 2. The point of this test is that the non-advancing guard
	// fires long BEFORE the page cap; 50 proves that as well as 10000 does and
	// keeps the mutation check below to 50 requests instead of 10000.
	maxLogPages = 50
	defer func() { maxLogPages = old }()

	jobID, taskID := "job-stuck", "task-stuck"
	srv := fakeNeverDrainingServer(t, jobID, taskID, false)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, logFailures, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, logFailures)

	requests, sinceSeqs, _ := srv.stats()
	require.Equal(t, 2, requests,
		"a cursor that does not advance is caught on the second request, far below maxLogPages")
	require.Equal(t, []string{"0", "200"}, sinceSeqs)
	require.Contains(t, errOut.String(), "did not advance")
	require.Contains(t, errOut.String(), "frame-001")
}
```

- [ ] **Step 2: Run them**

```bash
go test ./internal/cli/ -run 'TestWatchJobLogs_ServerNeverDrains|TestWatchJobLogs_CursorDoesNotAdvance' -v -timeout 120s
```

Expected: **PASS**, both, because Task 4 shipped both guards. That is not a TDD violation, it is the honest report: these two tests are written as guards for behaviour that already exists, and their value is established by Step 3, not by a RED. Say so plainly in the commit message rather than manufacturing a RED by deleting code you just wrote and calling it a failing test.

- [ ] **Step 3: Mutation-kill each guard separately - this is what makes the tests load-bearing**

**Mutation A - delete the page cap.** In `printTaskLogs`, remove:

```go
		if pages >= maxLogPages {
			return lastSeq, fmt.Errorf(
				"truncated after %d pages - the server never reported the log as drained", maxLogPages)
		}
```

Run:

```bash
go test ./internal/cli/ -run 'TestWatchJobLogs_ServerNeverDrains|TestWatchJobLogs_CursorDoesNotAdvance' -v -timeout 120s
```

Expected: `TestWatchJobLogs_ServerNeverDrains_StopsAtCap` FAILS with `Not equal: expected: 3, actual: <a large number>` (the loop runs until the 20 s context deadline; `errOut` then contains `context deadline exceeded`). `TestWatchJobLogs_CursorDoesNotAdvance_StopsImmediately` still PASSES. Restore the code.

**Mutation B - delete the non-advancing guard.** Remove:

```go
		if page.NextSeq <= since {
			return lastSeq, fmt.Errorf(
				"server cursor did not advance (next_seq %d after since_seq %d)", page.NextSeq, since)
		}
```

Run the same command. Expected: `TestWatchJobLogs_CursorDoesNotAdvance_StopsImmediately` FAILS with `Not equal: expected: 2, actual: 50` (it now runs to the shrunken cap). `TestWatchJobLogs_ServerNeverDrains_StopsAtCap` still PASSES. Restore the code.

Both mutations must produce a kill. If either "survives", the mutation did not apply - verify the edit landed before drawing any conclusion. Record both kills in the commit message; this pair is the evidence that neither bound can be deleted silently.

- [ ] **Step 4: Run the whole package**

```bash
go test ./internal/cli/ -count=1 -timeout 180s
```

Expected: `ok relay/internal/cli`. Confirm `git diff internal/cli/logs.go` shows both guards restored before committing.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/logs_test.go
git commit -m "test(cli): pin both bounds on the task-log paging loop

Two guards, two tests, one mutation kill each. Deleting the page cap makes
TestWatchJobLogs_ServerNeverDrains_StopsAtCap run to the context deadline
(expected 3 requests, got thousands); deleting the non-advancing-cursor
guard makes TestWatchJobLogs_CursorDoesNotAdvance_StopsImmediately run to
the cap (expected 2 requests, got 50). Neither test sees the other's
mutation, which is why both are needed.

Both tests were green when written - the guards shipped with the paging
loop. Their value is the mutation kills above, not a RED."
```

---

## Task 6: The prose

Wrong prose is this project's dominant defect class, and both of these strings describe the command being changed.

**Files:**
- Modify: `README.md` (`#### relay logs`, `#### relay submit`)
- Modify: `internal/cli/logs.go` (`LogsCommand` usage - already corrected in Task 3 Step 5; verify only)

- [ ] **Step 1: Correct the README `relay logs` section**

Replace:

```markdown
#### `relay logs`

Stream task logs for a running or completed job via Server-Sent Events.

```sh
relay logs <job-id>
```
```

with:

```markdown
#### `relay logs`

Watch a job until it finishes, printing each task's log once that task reaches a
terminal state.

The command subscribes to `/v1/events?job_id=<id>`, which carries job and task
**status** frames only - `task_log` frames require a subscription that names an
explicit `task_id` (see the event table under "Events"). Log content is fetched
over REST from `GET /v1/tasks/{id}/logs` once a task goes terminal, so output
arrives in a burst per finished task rather than live, line by line.

```sh
relay logs <job-id>
```

A task's log is paged to the end, so a log longer than one page is printed in
full. If a page cannot be fetched, `relay logs` prints a diagnostic on **stderr**
naming the task and the last log sequence number it printed, keeps watching the
job's other tasks, and exits 1:

```
relay: logs for task frame-001 (7e660488-...) are incomplete - stopped after seq 4200: fetching page 22: server error (500) - try again
error: logs incomplete for 1 of the job's tasks
```

Exit codes: `0` when the job finishes `done` and every task's log printed in
full; `1` otherwise (a failed or cancelled job exits 1 silently, since the job
status is already on stdout).
```

- [ ] **Step 2: Correct the README `relay submit` comment**

`relay submit` (without `--detach`) shares `watchJobLogs`, so its "tail logs" claim is the same wrong claim. Replace:

```sh
relay submit job.json          # submit and tail logs until done
relay submit --detach job.json # submit and print job ID, then exit
```

with:

```sh
relay submit job.json          # submit, then print each task's log as it finishes
relay submit --detach job.json # submit and print job ID, then exit
```

- [ ] **Step 3: Verify the usage string**

```bash
grep -n "logs <job-id>" internal/cli/logs.go
```

Expected: `Usage: "logs <job-id>  - print each task's log as the task finishes, until the job is done",` (set in Task 3 Step 5). It must not contain the words "tail" or an em dash.

- [ ] **Step 4: Verify no dashes were introduced**

```bash
grep -nP '[\x{2013}\x{2014}]' internal/cli/logs.go internal/cli/jobs.go README.md | grep -iE 'logs|submit'
```

Expected: only the two **pre-existing** lines `no token configured — run 'relay login' first` and `connection lost — job %s may still be running`. Those are untouched by this slice on purpose - changing them would move an existing assertion's target for no reason. Nothing added by this plan may appear.

- [ ] **Step 5: Commit**

```bash
git add README.md internal/cli/logs.go
git commit -m "docs: relay logs does not stream logs over SSE, and says so now

README claimed 'Stream task logs for a running or completed job via
Server-Sent Events' and the usage string claimed 'tail logs until job
completes'. Neither is what a user feels: the CLI subscribes with job_id
only, which carries status frames, and fetches each task's log over REST
once that task goes terminal. relay submit's README comment made the same
claim. Documents the new stderr diagnostic and the exit codes."
```

---

## Task 7: Full gates

**Files:** none.

- [ ] **Step 1: Format and vet**

```bash
gofmt -l internal/cli
go vet ./internal/cli/...
```

Expected: no output from either.

- [ ] **Step 2: Full unit suite**

```bash
make test
```

Expected: every package `ok`. Pay attention to `relay/internal/cli` and `relay/cmd/relay`.

- [ ] **Step 3: Race detector**

Per CLAUDE.md, the native Windows lane is unreliable; the Linux container is the route that works here.

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./internal/cli/... -count=1 -timeout 600s
```

Expected: `ok relay/internal/cli`, no data races. `fakeLogPagingServer` is written to concurrently by httptest handler goroutines and read by the test goroutine, which is exactly what this lane exists to check - the mutex on `requests`/`sinceSeqs`/`limits` is load-bearing.

If the lane is unavailable (the `ThreadSanitizer failed to allocate` symptom, or no Docker), **say so plainly in the handoff**. Do not substitute `-count=N`; it is not equivalent and CI is the gate.

- [ ] **Step 4: Confirm the working tree is exactly the intended file set**

```bash
git status --porcelain
git diff --stat origin/main...HEAD
```

Expected: changes confined to `internal/cli/logs.go`, `internal/cli/logs_test.go`, `internal/cli/jobs.go`, `internal/cli/jobs_test.go`, `README.md`, and the plan/spec docs the conductor committed. Nothing under `web/`, nothing under `internal/store/` (no `.sql` was touched, so `make generate` is **not** in scope), no stray scratch files.

---

## Verification gates

- **`make test` is the gate.** It covers every test in this plan; `internal/cli` has no integration-tagged tests.
- **`make test-race` (via the Linux container) is a CI merge gate** and Task 7 Step 3 runs it locally.
- **`make test-integration` is NOT relevant to this diff and should not be promised.** No `//go:build integration` file is touched or added, and no behaviour is being **removed**, so the removed-scaffolding hazard that normally forces the integration lane does not apply. Separately, the lane has a known Docker teardown timeout filed as `bug-2026-08-26-integration-lane-times-out-on-docker-teardown`; if the conductor runs it anyway for the Phase 4 fan-out, treat a teardown timeout as that known bug and not as a regression from this slice.
- **`make test-e2e` is not relevant.** Zero `web/` files change.

**A note for the Phase 4 integration lane.** The honest statement about this slice is that every new test still talks to a hand-written simulator of `handleGetTaskLogs`, not to the handler. `writeTaskLogPage` is a strictly better single point of failure than four JSON literals, and it is still a simulator. The general fix is `idea-2026-08-23-cli-tests-never-hit-real-server` (already `medium`, already citing this bug); **do not build that harness in this slice.** The highest-value thing an integration lane can do here is read `internal/api/tasks.go`'s `handleGetTaskLogs` against `writeTaskLogPage`'s doc comment and confirm the four simulated behaviours still match.

---

## Phase 5 closing scope

Not TDD tasks; the conductor's integrate phase.

- [ ] Close the backlog item with the command, never by hand-editing `status`:

```bash
/backlog close relay-logs-prints-nothing-envelope-drift
```

This `git mv`s `docs/backlog/bug-2026-08-25-relay-logs-prints-nothing-envelope-drift.md` into `docs/backlog/closed/`, stamps `status: closed` plus `closed:`/`resolution:` frontmatter, appends a `## Resolution` note, and commits. The `git mv` is required scope, not cleanup.

- [ ] The `## Resolution` note should record, because these are the item's own acceptance criteria: the RED measured once in Task 1 Step 3 (new fixture + old decode = four reddened assertions); that all four fake servers were converted, not three; and that `relay submit` was in scope because it is the second `watchJobLogs` caller.

---

## Phase 6 backlog proposals - do NOT implement in this slice

The spec drafted three follow-up items. They are **out of scope for every task above** and belong in `docs/backlog/` via `/backlog`, filed by the conductor after a human accept/reject:

1. `bug` - `relay logs` treats a `task_logs` row (a chunk) as a line, so the `[<task> <stream>]` prefix lands only on a chunk's first line and a chunk ending mid-line gets a spurious newline. Subsumes the interior-CR question. **This slice changes no stdout formatting**, which is what keeps its diff reviewable as a bug fix.
2. `bug` - the MCP `relay_get_task_logs` tool documents `since_seq` as inclusive (`seq >= this value`) while the SQL is `id > $2`.
3. `idea` - `relay logs` ignores the `dropped` SSE frame, so a slow-consumer eviction surfaces as the generic `connection lost` message.

If this plan's stages ever need scheduling across sessions they would be `## Stage N` sections handed to `/backlog phases`. **They do not** - this is one slice, one PR, one session.

---

## Self-review

**Spec coverage.** Decode + named type (Task 2); paging loop with both bounds and `maxLogPages` as a var (Task 4); loud errors, `errOut` threading, exit-code precedence (Task 3); the fixture converted in all four servers via one behavioural helper (Task 1); spec tests T1 (Task 1), T2/T3 (Task 4), T4/T5 (Task 5), T6 (Task 3), T7 (Task 4); the RED measurement recorded once (Task 1 Step 3, into Task 2's commit message); prose corrections (Task 6); `/backlog close` (Phase 5 section); follow-ups explicitly not implemented (Phase 6 section). Gaps found and filled beyond the spec: `doSubmit`/`SubmitCommand`/`jobs_test.go` ripple, the `logRow`-vs-`taskLogEntry` fixture independence, the handler's 400-not-clamp limit rule, the `(lastSeq, error)` return shape, and the README `relay submit` line.

**Placeholder scan.** No TBDs. Every code step carries the full text to write; the four fake-server edits are spelled out with the exact before and after rather than "similar to the others".

**Type consistency.** `taskLogPage`/`taskLogEntry` (production, `logs.go`) and `logRow` (test, `logs_test.go`) are distinct on purpose and never mixed: `writeTaskLogPage`, `oneFrameRows`, `genRows` and `fakeNeverDrainingServer` all use `logRow`; `printTaskLogs` decodes into `taskLogPage`. `printTaskLogs(ctx, c, taskID, taskName, out) (int64, error)`, `watchJobLogs(ctx, c, jobID, out, errOut) (string, int, error)`, `doLogs(ctx, cfg, args, out, errOut) error` and `doSubmit(ctx, cfg, args, w, errOut) error` are used identically in every task and every call site listed. `fakeLogPagingServer` is declared once (Task 4) and reused by `fakeNeverDrainingServer` (Task 5); `stats()` returns `(int, []string, []string)` at both call sites; `outLines` is declared once and used in Tasks 4 and 5.
