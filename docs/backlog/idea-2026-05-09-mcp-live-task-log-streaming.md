---
title: MCP live task-log streaming via resources or streaming tool calls
type: idea
status: open
created: 2026-05-09
source: MCP server session retro
---

# MCP live task-log streaming via resources or streaming tool calls

## Summary
Is there a clean way to surface live task-log streaming via MCP resources or a streaming tool call, or is polling always the right model for stdio transport? The current `relay_get_task_logs` tool returns a paginated snapshot; an LLM watching a running job must call it repeatedly to see new output.

## Notes
The MCP spec supports server-sent notifications and resource subscriptions, but the go-sdk's stdio transport support for these is worth verifying. Relay's existing SSE broker (`internal/events/`) already emits job events; bridging that to an MCP notification stream could enable a "subscribe to job logs" resource that pushes lines as they arrive. The complexity tradeoff (especially on stdio) may favour keeping polling as the v1 model and revisiting when the HTTP+SSE transport lands.

## Related
- `internal/mcp/task_logs.go` — current snapshot-based tool
- `internal/events/` — existing SSE broker
- `internal/mcp/wait.go` — polling approach used by relay_wait_for_job
