# MCP Structured Errors Lost - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make MCP tool failures deliver the structured `ToolError` (code + message + hint) to clients as an `IsError` tool result, instead of flattening it to plain text through the go-sdk's `SetError`.

**Architecture:** Introduce one generic registration helper `addTool[A, R]` in `internal/mcp/server.go` that wraps `mcpsdk.AddTool`. On `*ToolError`, it returns `&mcpsdk.CallToolResult{Content: [...TextContent(json(terr))], IsError: true}, nil, nil` (never returns the error as the Go `error`). On success it marshals the result into a single `TextContent`, exactly matching today's behavior. Then migrate all 18 conforming registration closures across 12 files to call it.

**Tech Stack:** Go, `github.com/modelcontextprotocol/go-sdk` v1.6.0, testify. Backend Go only - no frontend, no `.sql`, no `.proto`, no `make generate`.

---

## Slice independence

Single backend slice. No frontend work, no API/schema changes. Not applicable to Phase 3 parallelism.

## Worktree-path constraint (read before any command)

This is a git worktree. The working tree lives at
`D:/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a` on branch
`claude/gifted-meninsky-5fc18a`. Run every command from that directory (the harness
resets cwd between bash calls, so use the absolute path or `cd` into it within the
same command). **NEVER `cd D:/dev/relay`** - that is a separate checkout on `main`;
committing there lands work on the wrong branch. All command blocks below assume cwd is
the worktree root.

## The bug (validated against the code)

- `internal/mcp/errors.go:16`: `func (e *ToolError) Error() string { return e.Code + ": " + e.Message }` - the `Hint` field and JSON structure are not in the string form.
- Every tool registration closure does `return nil, nil, terr` on failure (returning the `*ToolError` as the Go `error`).
- go-sdk v1.6.0 `internal/.../mcp/server.go:345-353`: a non-`*jsonrpc.Error` returned error is wrapped via `var errRes CallToolResult; errRes.SetError(err); return &errRes, nil`. `SetError` stores only `err.Error()` text (`protocol.go:124-136`). So clients receive `"<code>: <message>"` with **no hint and no JSON structure**.

## go-sdk v1.6.0 type/signature confirmation

Verified in `C:/Users/chadv/go/pkg/mod/github.com/modelcontextprotocol/go-sdk@v1.6.0/mcp`:

- `func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out])` (`server.go:503`). The handler signature in use is `func(context.Context, *mcpsdk.CallToolRequest, In) (*mcpsdk.CallToolResult, any, error)`.
- `type CallToolResult struct { ...; Content []Content; IsError bool ... }` (`protocol.go:71-108`). `IsError` is a settable exported `bool`.
- `type TextContent struct { Text string; Meta Meta; Annotations *Annotations }` (`content.go:28`); `&mcpsdk.TextContent{Text: ...}` satisfies `mcpsdk.Content`. These exact names are already used in `jobs.go`.
- For the delivery test: `mcpsdk.NewInMemoryTransports()` (`transport.go:147`), `(*mcpsdk.Server).Connect` (`server.go:1020`), `mcpsdk.NewClient` (`client.go:44`), `(*mcpsdk.Client).Connect` (`client.go:255`), `(*mcpsdk.ClientSession).CallTool(ctx, *mcpsdk.CallToolParams)` (`client.go:990`). `CallToolParams{Name string; Arguments any}` (`protocol.go:40`).

## 18-site survey (all CONFORMING)

Every registration closure has the identical shape:

```go
out, terr := s.callX(ctx, args)   // or s.callWhoami(ctx) for the no-arg case
if terr != nil { return nil, nil, terr }
b, _ := json.Marshal(out)
return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}}}, nil, nil
```

Success result shape is uniform: a single `TextContent` holding `json.Marshal(out)`, no extra `CallToolResult` fields, and the `*mcpsdk.CallToolRequest` is always ignored (`_`). All `call*` methods return `(map[string]any, *ToolError)`. The only variant is the args type (whoami uses `struct{}` and `callWhoami` takes no args struct). `task_logs.go` and `wait.go` were specifically checked: their registration closures are byte-for-byte the same shape (the text-formatting / timeout logic lives entirely inside `callGetTaskLogs` / `callWaitForJob`, which still just return `(map[string]any, *ToolError)`). **No bespoke sites. No site sets extra `CallToolResult` fields or returns pre-built content.**

| # | File | Tool name | `call*` method | Args type | Success shape |
|---|------|-----------|----------------|-----------|----------------|
| 1 | whoami.go | relay_whoami | callWhoami(ctx) | `struct{}` | json(out) -> TextContent |
| 2 | jobs.go | relay_list_jobs | callListJobs | listJobsArgs | json(out) -> TextContent |
| 3 | jobs.go | relay_get_job | callGetJob | getJobArgs | json(out) -> TextContent |
| 4 | tasks.go | relay_list_tasks | callListTasks | listTasksArgs | json(out) -> TextContent |
| 5 | tasks.go | relay_get_task | callGetTask | getTaskArgs | json(out) -> TextContent |
| 6 | task_logs.go | relay_get_task_logs | callGetTaskLogs | getTaskLogsArgs | json(out) -> TextContent |
| 7 | workers.go | relay_list_workers | callListWorkers | listWorkersArgs | json(out) -> TextContent |
| 8 | workers.go | relay_get_worker | callGetWorker | getWorkerArgs | json(out) -> TextContent |
| 9 | schedules_read.go | relay_list_schedules | callListSchedules | listSchedulesArgs | json(out) -> TextContent |
| 10 | schedules_read.go | relay_get_schedule | callGetSchedule | getScheduleArgs | json(out) -> TextContent |
| 11 | schedules_write.go | relay_create_schedule | callCreateSchedule | createScheduleArgs | json(out) -> TextContent |
| 12 | schedules_write.go | relay_update_schedule | callUpdateSchedule | updateScheduleArgs | json(out) -> TextContent |
| 13 | schedules_write.go | relay_delete_schedule | callDeleteSchedule | deleteScheduleArgs | json(out) -> TextContent |
| 14 | reservations.go | relay_list_reservations | callListReservations | listReservationsArgs | json(out) -> TextContent |
| 15 | submit.go | relay_submit_job | callSubmitJob | submitJobArgs | json(out) -> TextContent |
| 16 | cancel.go | relay_cancel_job | callCancelJob | cancelJobArgs | json(out) -> TextContent |
| 17 | wait.go | relay_wait_for_job | callWaitForJob | waitForJobArgs | json(out) -> TextContent |
| 18 | run_now.go | relay_run_schedule_now | callRunScheduleNow | runScheduleNowArgs | json(out) -> TextContent |

Because all 18 conform, the generic wrapper replaces every closure with no behavior change on the success path. The whoami no-arg case is handled by wrapping its body in a tiny adapter (`func(ctx, struct{}) { return s.callWhoami(ctx) }`) so it fits the `func(ctx, A) (R, *ToolError)` shape.

---

## Task 1: RED - delivery test proving the hint never reaches the client

**Files:**
- Test: `internal/mcp/delivery_test.go` (create)

This test stands up the real registered MCP server against an httptest backend that
returns 401, connects an in-memory client, calls `relay_whoami`, and asserts the
returned `CallToolResult` is `IsError` and its text unmarshals to a `ToolError` with
`code == "auth_expired"` and the `relay login` hint. Against the current
`return nil, nil, terr` code this is RED: the go-sdk flattens the error to the text
`"auth_expired: <msg>"`, which does not unmarshal to a `ToolError` carrying a `hint`.

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/delivery_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// connectClient stands up s's MCP server over an in-memory transport and returns
// a connected client session. The caller owns closing the session.
func connectClient(t *testing.T, s *Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	if _, err := s.mcp.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestDelivery_ToolErrorReachesClient drives a real registered tool through the MCP
// transport with a backend that returns 401 and asserts the structured ToolError
// (code + hint) is delivered as an IsError result, not flattened to plain text.
func TestDelivery_ToolErrorReachesClient(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
	defer backend.Close()

	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "relay_whoami",
		Arguments: map[string]any{},
	})
	require.NoError(t, err, "transport-level call must succeed; the failure is a tool result, not a protocol error")
	require.True(t, res.IsError, "tool result must have IsError=true")
	require.NotEmpty(t, res.Content, "tool result must carry content")

	text, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok, "content[0] must be TextContent")

	var got ToolError
	require.NoError(t, json.Unmarshal([]byte(text.Text), &got),
		"delivered text must be the JSON-encoded ToolError, got %q", text.Text)
	require.Equal(t, "auth_expired", got.Code)
	require.Equal(t, "run `relay login` to refresh credentials", got.Hint)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from the worktree root):

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && go test ./internal/mcp/... -run TestDelivery_ToolErrorReachesClient -v -timeout 60s
```

Expected: FAIL. The current closure returns the `*ToolError` as a Go error; the go-sdk
wraps it via `SetError`, so `text.Text` is `"auth_expired: token expired"`, which is not
valid JSON for a `ToolError` - the `json.Unmarshal` assertion fails (or, if it parses,
`got.Hint` is empty). Confirm the failure is on the unmarshal/hint assertion, not a
compile or connect error.

- [ ] **Step 3: Commit the RED test**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && git add internal/mcp/delivery_test.go && git commit -m "test(mcp): add RED delivery test for structured tool errors"
```

---

## Task 2: GREEN - add the generic `addTool` wrapper and migrate whoami

**Files:**
- Modify: `internal/mcp/server.go` (add `addTool` helper + `encoding/json` import)
- Modify: `internal/mcp/whoami.go:10-24` (use the wrapper)

- [ ] **Step 1: Add the `addTool` helper to `server.go`**

Add `"encoding/json"` to the import block in `internal/mcp/server.go` and append this
helper at the end of the file (after `nopWriteCloser`):

```go
// addTool registers a relay MCP tool whose call function returns either a result
// to JSON-encode or a structured *ToolError. On a *ToolError it returns an
// IsError CallToolResult carrying the marshaled ToolError (code/message/hint),
// instead of returning the error to the SDK (which would flatten it to plain text
// via CallToolResult.SetError and drop the hint and JSON structure).
func addTool[A, R any](s *Server, tool *mcpsdk.Tool, call func(context.Context, A) (R, *ToolError)) {
	mcpsdk.AddTool(s.mcp, tool, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args A) (*mcpsdk.CallToolResult, any, error) {
		out, terr := call(ctx, args)
		if terr != nil {
			b, _ := json.Marshal(terr)
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
				IsError: true,
			}, nil, nil
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}
```

- [ ] **Step 2: Migrate `whoami.go` to the wrapper**

Replace the body of `registerWhoami` in `internal/mcp/whoami.go` (lines 10-24) and drop
the now-unused `encoding/json` import. The whoami `call` takes no args, so adapt it to
the `func(ctx, A)` shape with a `struct{}` arg:

```go
package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) registerWhoami() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_whoami",
		Description: "Return the identity of the authenticated relay user (email, user ID, admin flag) and the server URL.",
	}, func(ctx context.Context, _ struct{}) (map[string]any, *ToolError) {
		return s.callWhoami(ctx)
	})
}
```

Leave `callWhoami` below unchanged.

- [ ] **Step 3: Run the delivery test to verify it passes**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && go test ./internal/mcp/... -run TestDelivery_ToolErrorReachesClient -v -timeout 60s
```

Expected: PASS. The whoami tool now returns an `IsError` result whose text is the JSON
`ToolError` with `code: "auth_expired"` and the `relay login` hint.

- [ ] **Step 4: Run the full mcp package unit tests**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && go test ./internal/mcp/... -timeout 120s
```

Expected: PASS (no integration build tag, so integration tests are skipped). Confirms
the whoami migration and helper compile and break nothing.

- [ ] **Step 5: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && git add internal/mcp/server.go internal/mcp/whoami.go && git commit -m "feat(mcp): deliver structured tool errors via generic addTool wrapper"
```

---

## Task 3: Migrate `jobs.go`, `tasks.go`, `workers.go` to the wrapper

**Files:**
- Modify: `internal/mcp/jobs.go:25-53`
- Modify: `internal/mcp/tasks.go:19-47`
- Modify: `internal/mcp/workers.go:24-52`

These three files each register two tools and import `encoding/json` only for the
closure. After migration the `encoding/json` import is unused in each and must be
removed; the `call*` methods below the register func are unchanged.

- [ ] **Step 1: Rewrite `registerJobs` in `jobs.go`**

Replace lines 25-53 with:

```go
func (s *Server) registerJobs() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_list_jobs",
		Description: "List relay jobs with optional status filter and pagination.",
	}, s.callListJobs)

	addTool(s, &mcpsdk.Tool{
		Name:        "relay_get_job",
		Description: "Get details of a single relay job by ID.",
	}, s.callGetJob)
}
```

Then remove `"encoding/json"` from the `jobs.go` import block (keep `context`, `fmt`,
`net/url`, `strconv`, the mcpsdk import, and `relay/internal/relayclient` - all still
used by `callListJobs`/`callGetJob`).

- [ ] **Step 2: Rewrite `registerTasks` in `tasks.go`**

Replace lines 19-47 with:

```go
func (s *Server) registerTasks() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_list_tasks",
		Description: "List all tasks belonging to a relay job.",
	}, s.callListTasks)

	addTool(s, &mcpsdk.Tool{
		Name:        "relay_get_task",
		Description: "Get details of a single relay task by ID.",
	}, s.callGetTask)
}
```

Then remove `"encoding/json"` from the `tasks.go` import block (keep `context`, `fmt`,
and the mcpsdk import).

- [ ] **Step 3: Rewrite `registerWorkers` in `workers.go`**

Replace lines 24-52 with:

```go
func (s *Server) registerWorkers() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_list_workers",
		Description: "List relay workers (agents) connected to the server.",
	}, s.callListWorkers)

	addTool(s, &mcpsdk.Tool{
		Name:        "relay_get_worker",
		Description: "Get details of a single relay worker by ID.",
	}, s.callGetWorker)
}
```

Then remove `"encoding/json"` from the `workers.go` import block (keep `context`, `fmt`,
`net/url`, `strconv`, the mcpsdk import, and `relay/internal/relayclient`).

- [ ] **Step 4: Run the mcp package unit tests**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && go test ./internal/mcp/... -timeout 120s
```

Expected: PASS. The existing `jobs_test.go`, `tasks_test.go`, `workers_test.go` call the
`call*` methods directly (unchanged), and the delivery test still passes.

- [ ] **Step 5: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && git add internal/mcp/jobs.go internal/mcp/tasks.go internal/mcp/workers.go && git commit -m "refactor(mcp): migrate jobs/tasks/workers tools to addTool wrapper"
```

---

## Task 4: Migrate the schedules files to the wrapper

**Files:**
- Modify: `internal/mcp/schedules_read.go:24-52`
- Modify: `internal/mcp/schedules_write.go:22-64`

- [ ] **Step 1: Rewrite `registerSchedules` in `schedules_read.go`**

Replace lines 24-52 with:

```go
func (s *Server) registerSchedules() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_list_schedules",
		Description: "List relay scheduled jobs (cron schedules).",
	}, s.callListSchedules)

	addTool(s, &mcpsdk.Tool{
		Name:        "relay_get_schedule",
		Description: "Get details of a single relay scheduled job by ID.",
	}, s.callGetSchedule)
}
```

Then remove `"encoding/json"` from the `schedules_read.go` import block (keep `context`,
`fmt`, `net/url`, `strconv`, the mcpsdk import, and `relay/internal/relayclient`).

- [ ] **Step 2: Rewrite `registerSchedulesWrite` in `schedules_write.go`**

Replace lines 22-64 with:

```go
func (s *Server) registerSchedulesWrite() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_create_schedule",
		Description: "Create a new relay scheduled job (cron schedule).",
	}, s.callCreateSchedule)

	addTool(s, &mcpsdk.Tool{
		Name:        "relay_update_schedule",
		Description: "Update an existing relay scheduled job. Only provided fields are changed.",
	}, s.callUpdateSchedule)

	addTool(s, &mcpsdk.Tool{
		Name:        "relay_delete_schedule",
		Description: "Delete a relay scheduled job by ID.",
	}, s.callDeleteSchedule)
}
```

Then remove `"encoding/json"` from the `schedules_write.go` import block (keep `context`,
`fmt`, the mcpsdk import, and `relay/internal/jobspec`).

- [ ] **Step 3: Run the mcp package unit tests**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && go test ./internal/mcp/... -timeout 120s
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && git add internal/mcp/schedules_read.go internal/mcp/schedules_write.go && git commit -m "refactor(mcp): migrate schedule tools to addTool wrapper"
```

---

## Task 5: Migrate the remaining single-tool files to the wrapper

**Files:**
- Modify: `internal/mcp/reservations.go:19-33`
- Modify: `internal/mcp/submit.go:15-29`
- Modify: `internal/mcp/cancel.go:15-29`
- Modify: `internal/mcp/wait.go:29-43`
- Modify: `internal/mcp/run_now.go:15-29`

- [ ] **Step 1: Rewrite `registerReservations` in `reservations.go`**

Replace lines 19-33 with:

```go
func (s *Server) registerReservations() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_list_reservations",
		Description: "List relay reservations (admin-only). Returns a paginated list of worker reservations.",
	}, s.callListReservations)
}
```

Then remove `"encoding/json"` from the `reservations.go` import block (keep `context`,
`net/url`, `strconv`, the mcpsdk import, and `relay/internal/relayclient`).

- [ ] **Step 2: Rewrite `registerSubmit` in `submit.go`**

Replace lines 15-29 with:

```go
func (s *Server) registerSubmit() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_submit_job",
		Description: "Submit a new relay job from a job spec. Validates the spec client-side before sending.",
	}, s.callSubmitJob)
}
```

Then remove `"encoding/json"` from the `submit.go` import block (keep `context`, the
mcpsdk import, and `relay/internal/jobspec`).

- [ ] **Step 3: Rewrite `registerCancel` in `cancel.go`**

Replace lines 15-29 with:

```go
func (s *Server) registerCancel() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_cancel_job",
		Description: "Cancel a running or pending relay job by ID.",
	}, s.callCancelJob)
}
```

Then remove `"encoding/json"` from the `cancel.go` import block (keep `context`, `fmt`,
and the mcpsdk import).

- [ ] **Step 4: Rewrite `registerWait` in `wait.go`**

Replace lines 29-43 with:

```go
func (s *Server) registerWait() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_wait_for_job",
		Description: "Poll a relay job until it reaches a terminal state (done, failed, cancelled) or the timeout elapses.",
	}, s.callWaitForJob)
}
```

Then remove `"encoding/json"` from the `wait.go` import block (keep `context`, `fmt`,
`time`, and the mcpsdk import).

- [ ] **Step 5: Rewrite `registerRunNow` in `run_now.go`**

Replace lines 15-29 with:

```go
func (s *Server) registerRunNow() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_run_schedule_now",
		Description: "Trigger a relay scheduled job to run immediately, outside its normal cron schedule.",
	}, s.callRunScheduleNow)
}
```

Then remove `"encoding/json"` from the `run_now.go` import block (keep `context`, `fmt`,
and the mcpsdk import).

- [ ] **Step 6: Run the mcp package unit tests**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && go test ./internal/mcp/... -timeout 120s
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && git add internal/mcp/reservations.go internal/mcp/submit.go internal/mcp/cancel.go internal/mcp/wait.go internal/mcp/run_now.go && git commit -m "refactor(mcp): migrate remaining tools to addTool wrapper"
```

---

## Task 6: Strengthen the delivery test to cover a validation error path and final verification

**Files:**
- Modify: `internal/mcp/delivery_test.go` (add a second case)

The 401 case proves a `MapError`-sourced error is delivered. Add a case proving a
locally-constructed `*ToolError` (the `validation` path that never touches the backend)
is also delivered with `IsError`, since those errors flow through the same wrapper.

- [ ] **Step 1: Add a validation delivery case**

Append to `internal/mcp/delivery_test.go`:

```go
// TestDelivery_ValidationErrorReachesClient drives relay_get_job with an empty job_id
// (a client-side validation *ToolError that never reaches the backend) and asserts the
// structured error is delivered as an IsError result.
func TestDelivery_ValidationErrorReachesClient(t *testing.T) {
	// Backend should never be hit; fail loudly if it is.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("backend should not be called for a validation error; got %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "relay_get_job",
		Arguments: map[string]any{"job_id": ""},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.NotEmpty(t, res.Content)

	text, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)

	var got ToolError
	require.NoError(t, json.Unmarshal([]byte(text.Text), &got),
		"delivered text must be the JSON-encoded ToolError, got %q", text.Text)
	require.Equal(t, "validation", got.Code)
	require.Contains(t, got.Message, "job_id is required")
}
```

- [ ] **Step 2: Run the delivery tests**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && go test ./internal/mcp/... -run TestDelivery -v -timeout 60s
```

Expected: PASS for both `TestDelivery_ToolErrorReachesClient` and
`TestDelivery_ValidationErrorReachesClient`.

- [ ] **Step 3: Run the full mcp package test suite (unit) and the whole repo unit suite**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && go test ./internal/mcp/... -timeout 120s && go build ./...
```

Expected: PASS and a clean build. (`go build ./...` catches any stray unused import left
behind in the migrated files.)

- [ ] **Step 4: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a && git add internal/mcp/delivery_test.go && git commit -m "test(mcp): cover validation-error delivery through addTool wrapper"
```

---

## Notes for the implementer

- `s.mcp` is an unexported field of `Server`; the delivery test lives in package `mcp`
  (not `mcp_test`), like the other unit tests in this package, so it can reach `s.mcp`
  directly. Keep the new test file in `package mcp`.
- Do not change any `call*` method or `ToolError` / `MapError`. The fix is purely in the
  registration layer plus the new wrapper. Touch only the register funcs and their now-
  unused `encoding/json` imports.
- The `IsError` JSON wire field is `isError,omitempty`; the in-memory transport round-trips
  it, so `res.IsError` on the client is reliable.
- The existing `errors_test.go` (asserts hints are SET) stays as-is - it is complementary
  to the new delivery test (asserts hints are DELIVERED).
- No `.sql` / `.proto` / `make generate` involved. Backend Go only.
- Worktree-path reminder: run everything from
  `D:/dev/relay/.claude/worktrees/gifted-meninsky-5fc18a`; never `cd D:/dev/relay`.

## Closing the backlog item

This plan resolves `docs/backlog/bug-2026-06-10-mcp-structured-errors-lost.md`. After the
work merges, close it with `/backlog close mcp-structured-errors-lost` (which `git mv`s the
file into `docs/backlog/closed/` and stamps the resolution) - do not hand-edit the status.

## Self-review

- Spec coverage: the wrapper (Task 2) fixes the central bug; Tasks 2-5 migrate all 18
  conforming sites; Task 1 + Task 6 add the delivery tests the backlog item asked for
  ("Add a delivery test"). No bespoke sites exist, so no bespoke-fix task is needed.
- Placeholder scan: every code step shows complete code; commands have expected output.
- Type consistency: `addTool[A, R](s *Server, tool *mcpsdk.Tool, call func(context.Context, A) (R, *ToolError))`
  is defined once in Task 2 and called identically (via method-value `s.callX` or the
  whoami adapter) in Tasks 2-5. `ToolError`, `CallToolResult`, `TextContent`, `IsError`,
  `CallToolParams`, `NewInMemoryTransports`, `ClientSession.CallTool` all match go-sdk
  v1.6.0 as verified above.
