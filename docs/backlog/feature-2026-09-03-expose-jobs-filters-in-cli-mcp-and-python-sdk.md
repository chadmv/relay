---
title: Expose q, mine, since and until on the CLI, MCP and Python SDK job lists
type: feature
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# Expose q, mine, since and until on the CLI, MCP and Python SDK job lists

## Summary
GET /v1/jobs accepts q, mine, since and until since PR #178 and the web SPA uses all four; the relay CLI, the MCP server and the Python SDK still list jobs with status only. Each client should gain the same four flags or parameters with the server's validation errors passed through verbatim.

## Context
Filed as one item from the JB spec so the three clients move together and the README's per-client sections stay in step.

## Proposal
- CLI: --q, --mine, --since, --until on relay jobs list, integration-lane tests since the assertions depend on what the server puts on the wire.
- MCP: the same fields on the list tool's input schema.
- Python SDK: keyword arguments on list_jobs, with the envelope decoder unchanged.

## Related
- internal/cli, internal/mcp, sdk/python
