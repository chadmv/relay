---
title: MCP token read once at startup — mid-session expiry requires client restart
type: bug
status: closed
created: 2026-05-09
closed: 2026-06-25
resolution: fixed
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

## Resolution
Fixed 2026-06-25 via a hybrid reload-on-401-retry-once. `relayclient.Client`'s token is now
`sync.RWMutex`-guarded with `Token()`/`SetToken()` accessors (both `Do` and `StreamEvents` read
through the lock; no lock held across the network call). MCP gained a single `Server.do` chokepoint
(`internal/mcp/do.go`) that all ~21 tool call sites route through: on a 401 it calls an injected
config-backed reloader and, only if the reloaded token is non-empty AND different from the token
*this* call used (captured before the request), swaps via `SetToken` and retries exactly once; a
second 401 surfaces `auth_expired`. No retry loop. The CLI wires a `LoadConfig`-backed reloader via
`SetTokenReloader` after `NewServer`, so the startup `callWhoami` probe runs with a nil reloader and
a startup 401 stays fatal (mid-session-only scope). Steady-state calls do zero extra I/O. The
`usedTok` comparison (vs the live token) was a correctness fix found under `-race`: comparing against
the live token spuriously short-circuited concurrent callers' retries. Verified with unit + on-disk
reload + negative-case tests, `-race` clean in Docker on both concurrency tests, integration green,
`go vet` clean; adversarial review found no findings.
