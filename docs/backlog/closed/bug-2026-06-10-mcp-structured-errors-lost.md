---
title: MCP structured errors (code/hint) never reach clients
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-21
resolution: fixed
priority: medium
source: full-codebase review (2026-06-10)
---

# MCP structured errors (code/hint) never reach clients

## Summary
Every MCP tool handler returns `*ToolError` as the Go error. In go-sdk v1.6.0 the server wraps a returned error via `CallToolResult.SetError(err)`, which stores only `err.Error()` as text content - and `ToolError.Error()` is `Code + ": " + Message`. The JSON tags on `ToolError` and all the carefully written `Hint` fields ("run 'relay login' to refresh credentials", etc.) are dead weight; no MCP client ever sees them. Tests assert hints are set but nothing asserts they are delivered.

## Proposal
Return a result with `IsError` and the marshaled payload instead of an error:

```go
if terr != nil {
    b, _ := json.Marshal(terr)
    return &mcpsdk.CallToolResult{
        Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
        IsError: true,
    }, nil, nil
}
```

Best done once in a shared generic wrapper, which also removes the ~12 copy-pasted tool registration closures across `jobs.go`, `tasks.go`, `workers.go`, `schedules_*.go`, `reservations.go`, `cancel.go`, `run_now.go`, `submit.go`, `task_logs.go`, `wait.go`, `whoami.go`. Add a delivery test.

## Related
- `internal/mcp/errors.go:10-20` (`ToolError`, `MapError`)
- `internal/mcp/jobs.go:29-52` (representative registration)
- `internal/mcp/errors_test.go:31`

## Resolution
Fixed 2026-06-21 (mcp-structured-errors-lost). A generic `addTool[A, R]` registration wrapper was
added to `internal/mcp/server.go`: on a `*ToolError` it returns a `CallToolResult{IsError: true}`
carrying `json.Marshal(terr)` (preserving code/message/hint), instead of returning the error to the
go-sdk (which flattened it via `CallToolResult.SetError` to `Code + ": " + Message` text, dropping
the hint and JSON). All 18 tool-registration closures across 12 files (jobs, tasks, task_logs,
workers, schedules read/write, reservations, submit, cancel, wait, run_now, whoami) were migrated to
call the wrapper, removing the ~18 copy-pasted closures and each file's now-unused `encoding/json`
import; whoami's no-args call is adapted with a one-line `struct{}` shim. No `call*` method,
`ToolError`, or `MapError` was changed, and every success path is byte-identical (single
`TextContent` of `json.Marshal(out)`). Two delivery tests were added that drive a real registered
tool over the in-memory MCP transport: a 401 case (asserts `IsError` + the delivered text unmarshals
to `code: auth_expired` with the `relay login` hint) proven RED before the fix, and a validation case
(empty `job_id`, backend never hit) - the regression coverage the item asked for. Code review
returned no findings.
