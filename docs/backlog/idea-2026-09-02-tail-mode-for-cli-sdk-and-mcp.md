---
title: The CLI, Python SDK and MCP server do not use the task-log tail mode, and MCP passes prev_seq through as always 0
type: idea
status: open
created: 2026-09-02
priority: low
source: 2026-09-01 tail-paging spec (lane D), D21, and the Phase 4 correctness lens
---

# Tail mode for the CLI, Python SDK and MCP server

## Summary
GET /v1/tasks/{id}/logs gained order=desc, before_seq and a prev_seq cursor. Only the SPA uses them.
relay logs, the Python SDK's log paging and the MCP task-logs tool still walk forward from since_seq,
so none can fetch the end of a long log cheaply. The MCP tool returns the envelope as map[string]any
verbatim, so it now hands an LLM a prev_seq that is always 0 (it never sends order=desc); an agent
reading that could infer no earlier history exists.

## Proposal
- A tail (order=desc) mode on relay logs and on the SDK's log paging.
- A tail mode on the MCP tool.

## Related
- [[idea-2026-08-09-task-log-tail-and-paging-improvements]] (closed)
- [[bug-2026-08-26-mcp-since-seq-is-advertised-inclusive-and-is-exclusive]]
- internal/cli/logs.go, python/src/relay/client.py, internal/mcp/task_logs.go
