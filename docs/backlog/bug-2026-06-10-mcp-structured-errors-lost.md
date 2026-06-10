---
title: MCP structured errors (code/hint) never reach clients
type: bug
status: open
created: 2026-06-10
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
