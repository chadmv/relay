---
title: MCP token read once at startup — mid-session expiry requires client restart
type: bug
status: open
created: 2026-05-09
source: MCP server session retro
---

# MCP token read once at startup — mid-session expiry requires client restart

## Summary
The MCP server reads the auth token once at startup. If the token expires mid-session, every tool call returns `auth_expired` and the user must restart the MCP client to reconnect. With 30-day tokens this is rare in practice, but the recovery path is disruptive.

## Proposal
Re-read the token from config on each tool call (or on each `auth_expired` response) so a freshly-issued token picked up by `relay login` takes effect without a client restart. Alternatively, proactively reload on `auth_expired` and retry the call once before surfacing the error.

## Acceptance / Done When
- Running `relay login` to refresh a token while an MCP session is active takes effect on the next tool call without restarting the client.
- `auth_expired` is still returned if the fresh token is also expired or missing.

## Related
- `internal/mcp/server.go` — token loaded in `NewServer` / `Run`
- `internal/cli/config.go` — `LoadConfig` / config file path resolution
