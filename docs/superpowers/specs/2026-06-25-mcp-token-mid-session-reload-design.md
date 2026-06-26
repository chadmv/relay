# MCP token mid-session reload design

- Date: 2026-06-25
- Status: draft
- Owner: relay-tpm
- Backlog: `docs/backlog/bug-2026-05-09-mcp-token-mid-session-expiry.md`

## Problem

The MCP server (`relay mcp`) reads the auth token once, at process start. `internal/cli/mcp.go` passes `cfg.Token` into `mcp.NewServer`, which constructs a single `*relayclient.Client` whose `token` is a fixed string field set in `relayclient.NewClient`. Every tool call reuses that one client and that one captured token.

If the token expires mid-session, every subsequent tool call returns `auth_expired` (401 mapped in `mcp/errors.go`) and stays broken until the user kills and restarts the MCP client. Refreshing credentials out of band with `relay login` (which writes a new token to the config file via `SaveConfig`) has no effect on the running MCP process, because nothing re-reads the config. With 30-day tokens this is rare, but the only recovery is a disruptive restart.

Note on a recent interaction: the role-filtering change added a startup `callWhoami` in `NewServer`. That makes a *stale-at-startup* token a fatal construction error (the process exits cleanly). This spec is strictly about the *mid-session* case: the server started successfully with a valid token, and that token later expires while the process keeps running.

## Grounding (verified against code)

- `internal/relayclient/client.go`: `Client` has unexported `base`, `token`, `http` fields. `token` is set once in `NewClient` and read in `Do` (and `StreamEvents`) to set `Authorization: Bearer <token>`. No setter, no mutex. `ResponseError{StatusCode, Message}` is returned for non-2xx; 401 carries `StatusCode == 401`.
- `internal/mcp/server.go`: `Server` holds one `*relayclient.Client`, built in `NewServer(serverURL, token)`. All tools share it. `NewServer` does a startup `callWhoami` to resolve `isAdmin`.
- `internal/mcp/whoami.go`, `reservations.go`, etc.: every tool calls `s.client.Do(...)` then `MapError(err)` on failure. A 401 becomes `ToolError{Code: "auth_expired", ...}`.
- `internal/cli/config.go`: `LoadConfig` reads the config file (path from `configFilePathFn`) then applies `RELAY_URL` / `RELAY_TOKEN` env overrides. `relay login` persists a refreshed token via `SaveConfig` (`internal/cli/login.go` sets `cfg.Token = resp.Token`).

### Where the token lives today, and is the client shared?

The token lives as a fixed unexported `token` string on a single `*relayclient.Client` instance owned by the MCP `Server`. That client is shared by all tools, and MCP tool-call handlers can run concurrently, so any in-place token swap must be synchronized. There is no synchronization on `token` today.

## Chosen approach

**Hybrid: reload-from-config on `auth_expired`, retry the call once, then surface.** (Backlog option B, refined.)

Considered alternatives:

- **(A) Re-read config on every tool call.** Adds a file read + JSON parse + env-var resolution to every call, including the overwhelmingly common case where the token is still valid. Pure cost for the steady state to serve a rare expiry. Rejected on per-call I/O cost.
- **(B-naive) Reload on 401 and retry once.** Right trigger, but needs precise scoping so the retry is bounded and the swap is thread-safe.
- **(Hybrid, chosen)** Same trigger as B, with explicit bounds: reload happens only on a 401, the retry runs at most once, and if the reloaded token is identical to the in-use token the reload-retry is skipped entirely (no point retrying with the same credential). This keeps the steady-state path zero-cost and bounds worst-case work to one extra config read plus one extra HTTP request per genuinely-expired call.

### Data flow

1. A tool calls the client through a new thin wrapper (see "Where the swap lives") instead of `s.client.Do` directly. The wrapper performs the request with the current token.
2. If the request fails with a `*relayclient.ResponseError` whose `StatusCode == 401`:
   a. Reload the token from config (config file + env overrides, the same resolution `LoadConfig` uses).
   b. If reload fails, or yields an empty token, or yields a token byte-identical to the one just used, do **not** retry: return the original 401 error so `MapError` produces `auth_expired` as before.
   c. Otherwise, atomically swap the client's token to the reloaded value and re-issue the *same* request exactly once.
3. The result of the retry (success or error) is final. A second 401 is surfaced as `auth_expired`; no further reload or retry occurs. This guarantees at most one reload and one retry per originating tool call, so there is no retry loop.

Only 401 triggers reload. 403/404/409/429/5xx and network errors pass straight through to `MapError` unchanged.

### Where the swap lives, and thread-safety

The token must become mutable on the shared client, guarded against concurrent tool calls.

- Add synchronization to `relayclient.Client` so the token can be swapped while other goroutines read it. Concretely: guard the `token` field with a `sync.RWMutex` (or store it in an `atomic.Pointer[string]` / `atomic.Value`); `Do` and `StreamEvents` read it under the lock / via the atomic load when building the `Authorization` header, and a new exported `SetToken(string)` swaps it under the write lock / atomic store. This keeps all token access on the type that owns it and satisfies the "no interior pointers across locks" spirit (the token is a value-typed string, copied out under the lock, never a pointer that escapes).
- The reload-and-retry orchestration belongs in `internal/mcp`, not in `relayclient` (the client must not know about config files). Introduce a single MCP-side helper that wraps `s.client.Do`: it runs the call, and on 401 does reload -> compare -> `s.client.SetToken(new)` -> retry-once. A config-reader function is injected into the `Server` (a `func() (string, error)` field, defaulting to the real config+env resolver) so tests can supply a refreshed token deterministically without touching the filesystem.
- Concurrency note for the common multi-call case: two tool calls hitting 401 near-simultaneously may each reload and each call `SetToken`. Because both read the same refreshed config, the swaps are idempotent (same value), and `SetToken` under the lock is safe. The identical-token short-circuit means the second swap is a no-op write of the same string. No double-retry of a single originating call occurs because the retry counter is local to each call.
- The startup `isAdmin` resolution stays as-is: it runs once in `NewServer` before any tool dispatch and is not affected. Reload does not re-resolve `isAdmin` (a privilege change still requires a restart; that is out of scope and not a regression).

### Routing the wrapper

Tools currently call `s.client.Do(ctx, method, path, body, &out)` directly. The cleanest change is to route every tool's API call through the new `Server` helper (e.g. `s.do(ctx, method, path, body, &out)`) so the reload-retry applies uniformly, including `callWhoami` used by the `relay_whoami` tool. The startup probe in `NewServer` may keep calling `callWhoami`; since that path now routes through `s.do`, a startup 401 would attempt one reload-retry before failing fatally, which is a harmless (and arguably nice) improvement, but is not the target of this work.

## Acceptance / Done when

- Running `relay login` to refresh the token while an MCP session is active takes effect on the next tool call, with no client/process restart: a call that would have 401'd reloads the new token from config, retries once, and succeeds.
- `auth_expired` is still returned when the freshly-reloaded token is also expired or missing: the retry runs, gets a second 401, and the tool surfaces `ToolError{Code: "auth_expired"}`.
- If the reloaded token is identical to the in-use token (no refresh happened), no retry is attempted and the original `auth_expired` is surfaced immediately.
- No infinite retry loop: at most one reload and one retry per originating tool call. A 401 on the retry is final.
- Non-401 errors (403/404/409/429/5xx/network) are unchanged in behavior and mapping.
- Token swap is concurrency-safe: concurrent tool calls during a refresh do not race on the token field (verified with `-race`).

## Test strategy

All client-side; reuse the just-shipped whoami backend helpers (`whoamiHandler`, `newWhoamiBackend` in `whoami_test_helper_test.go`) so `NewServer`'s startup probe succeeds.

1. **401-then-200 after refresh.** Stand up an `httptest.Server` whose tool endpoint returns 401 while the request carries the old `Bearer` token and 200 once it sees the new token. Inject the `Server`'s config-reader to return the refreshed token (no real filesystem). Drive a tool call; assert it succeeds and the backend observed the new token on the retry. Assert exactly two requests to the tool endpoint (original + one retry).
2. **401-then-401 (fresh token also bad).** Backend always returns 401. Config-reader returns a different-but-also-invalid token. Assert the tool returns `auth_expired` and that exactly one retry occurred (two tool-endpoint requests).
3. **Identical token short-circuit.** Config-reader returns the same token already in use. Backend returns 401. Assert `auth_expired` and exactly one request (no retry).
4. **Non-401 passthrough.** Backend returns 403/404/etc.; assert the existing mapping and that no reload/retry occurred (config-reader not invoked, single request).
5. **Concurrency / race.** Fire N concurrent tool calls against a backend that 401s the old token and 200s the new one, with the config-reader returning the refreshed token. Run under `-race`; assert no data race and all calls eventually succeed. (Per memory `reference_race_detector_toolchain`: `-race` needs MSYS2 mingw64 gcc, `CC=/c/msys64/mingw64/bin/gcc.exe`.)
6. **`relayclient` unit test for `SetToken`.** Assert `Do` uses the latest token after `SetToken`, and that concurrent `SetToken`/`Do` is race-clean.

Per memory `regression_test_must_distinguish_fix`: test 1 must assert a property only the fix produces - the backend must verify the *new* token byte value appeared on the retry request, not merely that a second request happened.

## Invariants and constraints

- **Token hashing (server-side)** is not involved; this is purely client-side bearer-token attachment. `tokenhash.Hash` is untouched.
- **Single JSON entry point** (`readJSON` in `internal/api`) is server-side request decoding; not applicable to the MCP client.
- **No interior pointers across locks**: the token is a value-typed string read out under the client's lock (or via an atomic load); no pointer to mutable client state escapes. `SetToken` mutates only under the lock.
- **One bounded sender per stream / epoch fence / identity-checked teardown**: not applicable (no gRPC streams or task-status writes here).
- Scope guard: this does not add token *auto-refresh* (the MCP server never calls `relay login` itself); it only picks up a token the user refreshed out of band. It does not re-resolve `isAdmin` mid-session.

## Out of scope

- Proactive/background token refresh or expiry prediction.
- Re-resolving admin privilege mid-session.
- Any change to how `relay login` obtains or persists tokens.
