# CLI logs/submit terminal-race hang fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop `relay logs` and `relay submit` from hanging forever when a job goes terminal in the window between the initial job GET and the SSE subscribe, by restructuring `watchJobLogs` to subscribe first and then take a snapshot.

**Architecture:** Add an `onSubscribed` hook to `relayclient.StreamEvents` that fires after the server returns HTTP 200 (the server flushes immediately after `broker.Subscribe`, so the subscription is established by then). `watchJobLogs` uses that hook to GET the job snapshot after the stream is live: any task or job already terminal is handled from the snapshot; anything that goes terminal after subscribe arrives over the stream. A `printed` dedup set covers the overlap. Pure client-side fix in Go; no server change.

**Tech Stack:** Go, `net/http`, `bufio.Scanner` SSE parsing, `httptest` fakes, testify.

**Slice independence:** Backend/CLI Go only (`internal/cli`, `internal/relayclient`). There is NO frontend work in this plan. The two changes are sequential within one slice: Task 2 (the `StreamEvents` hook) must land before Task 3 (the `watchJobLogs` restructure) because the restructure calls the new signature.

**Out of scope (do not expand):** Server-side SSE keepalive comment frames (the backlog's alternative) are NOT part of this fix. The subscribe-first snapshot is the correctness fix for the terminal-before-subscribe race; keepalives only bound an unrelated network-stall hang. Do not touch `internal/api/events.go` or `internal/events/broker.go`.

---

## Validated codebase facts (confirmed against current code)

- **`StreamEvents` current signature** (`internal/relayclient/client.go:96`):
  `func (c *Client) StreamEvents(ctx context.Context, path string, handler func(SSEEvent) bool) error`.
  The post-200 hook point is between the `resp.StatusCode != http.StatusOK` check (lines 112-114) and the `bufio.NewScanner` loop (line 116+).
- **`StreamEvents` production callers:** exactly ONE - `internal/cli/logs.go:68`. Two unit tests also call it (`internal/relayclient/client_test.go:29` and `:54`); both pass a positional `handler` and must be updated to pass a `nil` hook.
- **`watchJobLogs` production callers:** exactly TWO - `internal/cli/logs.go:34` (`relay logs`) and `internal/cli/jobs.go:230` (`relay submit`). Its signature is unchanged by this plan, so both callers keep compiling untouched.
- **Server subscribe-then-flush ordering** confirmed at `internal/api/events.go:15` (`s.broker.Subscribe(jobID)`) then `:23` (`flusher.Flush()`). So when the client's `c.http.Do(req)` returns 200 in `StreamEvents`, the broker subscription is already live.
- **Broker has no replay** confirmed: `handleEvents` only forwards events received on `ch` after `Subscribe` (`internal/api/events.go:25-39`). This is the root cause; a terminal transition published before `Subscribe` is lost.
- **Existing test harness** in `internal/cli/logs_test.go`:
  - `fakeJobServer(t, jobID, taskID, finalJobStatus)` (lines 23-54): serves `GET /v1/jobs/<id>` (running, one pending task), `GET /v1/events` (writes a `task` done frame then a `job` `<finalJobStatus>` frame), `GET /v1/tasks/<id>/logs`. Routes on `r.URL.Path == "/v1/events"` (query string ignored).
  - `fakeCompletedJobServer(t, jobID, taskID, jobStatus)` (lines 57-77): job already terminal, no SSE.
  - Assertion style: `relayclient.NewClient(srv.URL, "tok")`, `var out strings.Builder`, `watchJobLogs(context.Background(), c, jobID, &out)`, then `require.Equal(t, "<status>", status)` and `require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")`.
- **Types available in package `cli`:** `jobResp` (`internal/cli/jobs.go:20`, has `Status`, `Tasks []taskResp`), `taskResp` (`:32`, has `ID`, `Name`, `Status`), `silentError` (`internal/cli/command.go:23`). `printTaskLogs` (`internal/cli/logs.go:106`) GETs ALL logs for a task, so it stays correct even if both snapshot and stream observe the same task.

---

## Task 1: RED test - reproduce the terminal-before-subscribe hang

This test models the exact race: the fake server returns the job as `running` until the SSE subscription is hit, and `done` afterward, while the events endpoint sends NO event and holds the connection open. Under old code (`watchJobLogs` GETs before subscribe), this hangs and only escapes via the context timeout, returning a ctx error (NOT `"done"`) - RED. Under the new code it returns `"done"` from the snapshot - GREEN.

**Files:**
- Test: `internal/cli/logs_test.go` (add a new fake server helper + one test)

- [ ] **Step 1: Write the failing test and its fake server**

Add to `internal/cli/logs_test.go`. Add `"sync"` and `"time"` to the import block (the existing imports are `context`, `encoding/json`, `fmt`, `net/http`, `net/http/httptest`, `strings`, `testing`, testify `require`, and `relay/internal/relayclient`).

```go
// fakeRaceJobServer models the terminal-before-subscribe race: the job reads
// "running" until the SSE subscription is established, then "done" afterward.
// The events endpoint sends NO event and holds the connection open, modeling
// the missed terminal event that the broker (no replay) never delivers.
func fakeRaceJobServer(t *testing.T, jobID, taskID string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	subscribed := false
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			done := subscribed
			mu.Unlock()
			status, taskStatus := "running", "pending"
			if done {
				status, taskStatus = "done", "done"
			}
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: status,
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: taskStatus}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			mu.Lock()
			subscribed = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Send no event; hold open until the request context is cancelled.
			<-r.Context().Done()

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			json.NewEncoder(w).Encode([]struct {
				Stream  string `json:"stream"`
				Content string `json:"content"`
			}{
				{Stream: "stdout", Content: "frame rendered"},
			})
		}
	}))
}

func TestWatchJobLogs_TerminalBeforeSubscribe_DoesNotHang(t *testing.T) {
	jobID, taskID := "job-race", "task-race"
	srv := fakeRaceJobServer(t, jobID, taskID)
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var out strings.Builder
	status, err := watchJobLogs(ctx, c, jobID, &out)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
}
```

- [ ] **Step 2: Run the test to verify it fails (proven RED via the timeout)**

Run: `cd D:/dev/relay && go test ./internal/cli/... -run TestWatchJobLogs_TerminalBeforeSubscribe_DoesNotHang -v -timeout 30s`
Expected: FAIL. With the current code, `watchJobLogs` GETs the job (running, because nothing has subscribed yet), enters the stream, blocks with no event, and only the 2s context timeout unblocks `scanner.Scan()`; `StreamEvents` returns a non-nil error so `watchJobLogs` returns `("", err)` - `require.NoError` fails (and `status != "done"`). This proves the bug: the result is a ctx-driven error, not `"done"`.

- [ ] **Step 3: Commit the RED test**

```bash
cd D:/dev/relay && git add internal/cli/logs_test.go && git commit -m "test(cli): reproduce logs terminal-before-subscribe hang"
```

---

## Task 2: Add the `onSubscribed` hook to `StreamEvents`

**Files:**
- Modify: `internal/relayclient/client.go:93-136` (doc comment, signature, and post-200 hook call)
- Modify: `internal/relayclient/client_test.go:29` and `:54` (update the two existing callers to pass `nil`)

- [ ] **Step 1: Update the two existing `StreamEvents` callers in `client_test.go` first**

In `internal/relayclient/client_test.go`, change the `TestStreamEvents_ParsesFrames` call (currently line 29):

```go
	err := c.StreamEvents(context.Background(), "/v1/events", nil, func(e SSEEvent) bool {
		got = append(got, e)
		return true // keep going until server closes
	})
```

And the `TestStreamEvents_HandlerReturnFalseStops` call (currently line 54):

```go
	_ = c.StreamEvents(context.Background(), "/v1/events", nil, func(e SSEEvent) bool {
		count++
		return false // stop after first event
	})
```

- [ ] **Step 2: Add the hook parameter and call site in `client.go`**

Replace the `StreamEvents` doc comment and the function header through the start of the scanner loop. Current code (`internal/relayclient/client.go:93-116`):

```go
// StreamEvents opens an SSE connection to path and calls handler for each complete event.
// handler returns false to stop streaming cleanly. Returns nil when the handler stops
// or the server closes the connection; returns an error on network/HTTP failure.
func (c *Client) StreamEvents(ctx context.Context, path string, handler func(SSEEvent) bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error (%d)", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
```

becomes:

```go
// StreamEvents opens an SSE connection to path and calls handler for each complete event.
// onSubscribed, if non-nil, is called once after the server returns HTTP 200 (the
// subscription is established server-side at that point) and before any event is read;
// if it returns false, StreamEvents returns nil immediately without reading the stream.
// handler returns false to stop streaming cleanly. Returns nil when the handler stops
// or the server closes the connection; returns an error on network/HTTP failure.
func (c *Client) StreamEvents(ctx context.Context, path string, onSubscribed func() bool, handler func(SSEEvent) bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error (%d)", resp.StatusCode)
	}

	if onSubscribed != nil && !onSubscribed() {
		return nil
	}

	scanner := bufio.NewScanner(resp.Body)
```

Leave the scanner loop (current lines 117-136) unchanged.

- [ ] **Step 3: Run the relayclient tests to verify they still pass**

Run: `cd D:/dev/relay && go test ./internal/relayclient/... -run TestStreamEvents -v -timeout 30s`
Expected: PASS for `TestStreamEvents_ParsesFrames` and `TestStreamEvents_HandlerReturnFalseStops` (both now pass `nil` for `onSubscribed`).

- [ ] **Step 4: Confirm `internal/cli` does not yet compile against the new signature**

Run: `cd D:/dev/relay && go build ./internal/cli/... 2>&1`
Expected: build FAILS - `logs.go:68` still calls the old 3-arg form. This is expected; Task 3 fixes it. (Skip the commit until Task 3 so the tree never has a broken `cli` package committed.)

---

## Task 3: Restructure `watchJobLogs` to subscribe-first, then snapshot

**Files:**
- Modify: `internal/cli/logs.go:44-102` (`watchJobLogs`)
- Test: re-run Task 1's RED test plus the existing logs tests

- [ ] **Step 1: Replace `watchJobLogs`**

Replace the whole function body (`internal/cli/logs.go:44-102`) with:

```go
// watchJobLogs subscribes to SSE events for jobID, then takes a snapshot so a job
// that went terminal before the subscribe is still caught (the broker has no replay).
// When a task reaches a terminal state its logs are fetched and printed once.
// Returns the final job status ("done", "failed", or "cancelled") and any error.
func watchJobLogs(ctx context.Context, c *relayclient.Client, jobID string, w io.Writer) (string, error) {
	taskNames := make(map[string]string)
	printed := make(map[string]bool)
	var finalStatus string

	// onSubscribed runs after the SSE subscription is live. Any task or job already
	// terminal at this point would never produce a future event, so we GET a snapshot
	// and handle it here. Returning false stops the stream when the job is done.
	onSubscribed := func() bool {
		var job jobResp
		if err := c.Do(ctx, "GET", "/v1/jobs/"+jobID, nil, &job); err != nil {
			// Fall through to the stream; a transient snapshot error should not abort.
			return true
		}
		for _, t := range job.Tasks {
			taskNames[t.ID] = t.Name
		}
		for _, t := range job.Tasks {
			if t.Status == "done" || t.Status == "failed" || t.Status == "timed_out" {
				if !printed[t.ID] {
					printed[t.ID] = true
					printTaskLogs(ctx, c, t.ID, t.Name, w)
				}
			}
		}
		if job.Status == "done" || job.Status == "failed" || job.Status == "cancelled" {
			finalStatus = job.Status
			return false
		}
		return true
	}

	handler := func(e relayclient.SSEEvent) bool {
		switch e.Type {
		case "task":
			var data struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(e.Data), &data) != nil {
				return true
			}
			if data.Status == "done" || data.Status == "failed" || data.Status == "timed_out" {
				if !printed[data.ID] {
					printed[data.ID] = true
					printTaskLogs(ctx, c, data.ID, taskNames[data.ID], w)
				}
			}
		case "job":
			var data struct {
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(e.Data), &data) != nil {
				return true
			}
			if data.Status == "done" || data.Status == "failed" || data.Status == "cancelled" {
				finalStatus = data.Status
				return false
			}
		}
		return true
	}

	if err := c.StreamEvents(ctx, "/v1/events?job_id="+jobID, onSubscribed, handler); err != nil {
		return "", err
	}
	if finalStatus == "" {
		return "", fmt.Errorf("connection lost — job %s may still be running", jobID)
	}
	return finalStatus, nil
}
```

Notes for the engineer:
- `taskNames` and `printed` are closed over by both `onSubscribed` and `handler`; both run on the same goroutine (`onSubscribed` runs before the scan loop, `handler` runs inside it), so no locking is needed.
- The leading GET-and-early-return is gone; the snapshot now happens inside `onSubscribed` after the subscription is established. This is the whole fix.
- `printTaskLogs` GETs all logs for a task, so the dedup `printed` set (not partial-log tracking) is the correct dedup granularity.

- [ ] **Step 2: Run the RED test from Task 1 - it must now be GREEN**

Run: `cd D:/dev/relay && go test ./internal/cli/... -run TestWatchJobLogs_TerminalBeforeSubscribe_DoesNotHang -v -timeout 30s`
Expected: PASS. Flow: `StreamEvents` returns 200, `onSubscribed` fires (the fake set `subscribed=true` when `/v1/events` was hit), the snapshot GET now reads `done` + task `done`, prints the task logs, sets `finalStatus="done"`, returns false; `watchJobLogs` returns `("done", nil)`, well under the 2s timeout.

- [ ] **Step 3: Run the full existing logs + relayclient suites for regressions**

Run: `cd D:/dev/relay && go test ./internal/cli/... ./internal/relayclient/... -v -timeout 60s`
Expected: PASS, including the pre-existing `TestWatchJobLogs_DoneExits0`, `TestWatchJobLogs_FailedReturnsFailed`, `TestWatchJobLogs_AlreadyDone_PrintsLogsAndExits`, `TestWatchJobLogs_AlreadyCancelled_ReturnsCancelled`, `TestRunLogs_DoneExitsCleanly`, `TestRunLogs_FailedReturnsSilentError`.

Why the existing tests still pass:
- `fakeCompletedJobServer` tests (already-terminal job, no SSE endpoint): `StreamEvents` GETs `/v1/events` which the fake does not handle, so the test server returns 200 with an empty body. `onSubscribed` GETs the job (terminal), prints logs, sets `finalStatus`, returns false. The empty stream is never read. Status returned correctly. (If instead the unhandled route returned a non-200, adjust by noting `fakeCompletedJobServer` returns the default 200 for unmatched routes, which it does via the zero-value `http.ResponseWriter`.)
- `fakeJobServer` tests (running job, SSE sends task-done then job-done): `onSubscribed` GETs the still-`running` job (task `pending`), prints nothing, returns true; the stream then delivers the `task` done frame (prints once, `printed` now true) and the `job` frame (sets `finalStatus`). Same output as before.

- [ ] **Step 4: Commit Tasks 2 and 3 together (first point the whole tree compiles)**

```bash
cd D:/dev/relay && git add internal/relayclient/client.go internal/relayclient/client_test.go internal/cli/logs.go && git commit -m "fix(cli): subscribe-first snapshot so logs/submit do not hang on terminal-before-subscribe race"
```

---

## Task 4: Dedup test - a task terminal in BOTH snapshot and stream prints once

This proves the `printed` set: if a task is already terminal in the post-subscribe snapshot AND the broker also delivers a buffered terminal `task` event for it, its logs are printed exactly once.

**Files:**
- Test: `internal/cli/logs_test.go` (one fake server + one test)

- [ ] **Step 1: Write the dedup test**

Add to `internal/cli/logs_test.go`:

```go
// fakeOverlapJobServer returns the job already terminal (task done) AND streams a
// duplicate terminal task event plus a job event - modeling the snapshot/stream overlap.
func fakeOverlapJobServer(t *testing.T, jobID, taskID string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "running", // not terminal, so the stream is still consumed
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Duplicate terminal task event for the same task already seen in the snapshot.
			fmt.Fprintf(w, "event: task\ndata: {\"id\":%q,\"status\":\"done\"}\n\n", taskID)
			fmt.Fprintf(w, "event: job\ndata: {\"status\":\"done\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			json.NewEncoder(w).Encode([]struct {
				Stream  string `json:"stream"`
				Content string `json:"content"`
			}{
				{Stream: "stdout", Content: "frame rendered"},
			})
		}
	}))
}

func TestWatchJobLogs_TaskInSnapshotAndStream_PrintedOnce(t *testing.T) {
	jobID, taskID := "job-dup", "task-dup"
	srv := fakeOverlapJobServer(t, jobID, taskID)
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var out strings.Builder
	status, err := watchJobLogs(ctx, c, jobID, &out)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, strings.Count(out.String(), "[frame-001 stdout] frame rendered"),
		"task terminal in both snapshot and stream must print exactly once")
}
```

- [ ] **Step 2: Run the dedup test**

Run: `cd D:/dev/relay && go test ./internal/cli/... -run TestWatchJobLogs_TaskInSnapshotAndStream_PrintedOnce -v -timeout 30s`
Expected: PASS. The snapshot prints the task once (`printed[taskID]=true`); the duplicate stream `task` event is skipped because `printed[taskID]` is already true; the `job` done event sets `finalStatus`.

- [ ] **Step 3: Commit the dedup test**

```bash
cd D:/dev/relay && git add internal/cli/logs_test.go && git commit -m "test(cli): assert snapshot/stream task overlap prints logs once"
```

---

## Task 5: Full package gate

- [ ] **Step 1: Run the whole CLI + relayclient packages once more**

Run: `cd D:/dev/relay && go test ./internal/cli/... ./internal/relayclient/... -timeout 120s`
Expected: PASS, all tests.

- [ ] **Step 2: Build all binaries to confirm nothing else broke against the new signature**

Run: `cd D:/dev/relay && go build ./...`
Expected: success, no output. (Only `internal/cli/logs.go:68` consumed the old signature; it was updated in Task 3.)

---

## Self-review

- **Spec coverage:** Subscribe-first hook on `StreamEvents` (Task 2); `watchJobLogs` snapshot-after-subscribe restructure with `printed` dedup (Task 3); deterministic RED race test proven via timeout (Task 1, GREEN in Task 3); dedup test (Task 4); happy-path and already-terminal paths preserved by reusing the existing `fakeJobServer`/`fakeCompletedJobServer` tests (Task 3 Step 3). Keepalives explicitly out of scope. All covered.
- **Placeholder scan:** Every code step shows complete code; every run step has an exact command and expected result. No TBD/TODO.
- **Type consistency:** `onSubscribed func() bool` and the 4-arg `StreamEvents(ctx, path, onSubscribed, handler)` are used identically in client.go, both test callers, and `watchJobLogs`. `printed map[string]bool`, `taskNames map[string]string`, `finalStatus string`, `jobResp`, `taskResp`, `printTaskLogs` all match the existing package definitions. Terminal sets used consistently: tasks `done|failed|timed_out`, job `done|failed|cancelled` (matching the original code).
- **Invariants:** No epoch, job-spec, gRPC-sender, teardown, or JSON-entry-point invariants are touched; this is read-only client streaming. No `.sql`/`.proto` edits, so no `make generate`. No frontend.
