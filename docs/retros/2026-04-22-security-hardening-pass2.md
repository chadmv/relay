# Session Retro: 2026-04-22 — Security Hardening Pass 2

## What Was Built

A complete authentication and security hardening pass for all three Relay binaries, implemented across 15 tasks via subagent-driven development:

- **CORS middleware** (`internal/api/cors.go`): opt-in allowlist middleware that fails closed — empty allowlist means no CORS headers ever. Wildcard `*` is rejected. Never emits `Access-Control-Allow-Credentials`. Wired via `RELAY_CORS_ORIGINS` env var.
- **Per-IP rate limiting** (`internal/api/ratelimit.go`): sliding-window limiter applied to `POST /v1/auth/login` and `POST /v1/auth/register`. Rates configurable via `RELAY_LOGIN_RATE_LIMIT` / `RELAY_REGISTER_RATE_LIMIT` (format `N:duration`). Returns `429` with `Retry-After` header.
- **Bootstrap password cleared from process env** (`cmd/relay-server/main.go`): `RELAY_BOOTSTRAP_ADMIN` and `RELAY_BOOTSTRAP_PASSWORD` are now `os.Unsetenv`'d immediately after first use so they don't linger in `/proc/<pid>/environ`.
- **Agent enrollment tokens** (`internal/store/migrations/000005_agent_auth.up.sql`): new `agent_enrollments` table for admin-issued, one-time-use, TTL-bounded enrollment tokens. SHA-256 hashed in DB. Consumed atomically via `ConsumeAgentEnrollment :execrows`.
- **Agent long-lived tokens**: `agent_token_hash` column on `workers` table. `SetWorkerAgentToken` / `ClearWorkerAgentToken` / `GetWorkerByAgentTokenHash` queries. Revocation via nulling the hash.
- **Proto additions** (`proto/relayv1/relay.proto`): `credential` oneof (`EnrollmentToken` | `AgentToken`) on `RegisterRequest`; `agent_token` string on `RegisterResponse`.
- **Admin HTTP endpoints** (`internal/api/agent_enrollments.go`): `POST /v1/agent-enrollments` (create), `GET /v1/agent-enrollments` (list), `DELETE /v1/workers/{id}/token` (revoke). All admin-only.
- **CLI commands** (`internal/cli/agent_enroll.go`, `internal/cli/workers.go`): `relay agent enroll --hostname X --ttl 1h` issues a token; `relay workers revoke <id-or-hostname>` revokes it. Token to stdout, metadata to stderr.
- **Agent credential persistence** (`internal/agent/credentials.go`): reads/writes `<state-dir>/token` at `0600` perms. First boot reads `RELAY_AGENT_ENROLLMENT_TOKEN` (cleared from env immediately after capture). Subsequent boots use the persisted long-lived token.
- **Agent sends credential on Connect** (`internal/agent/agent.go`): `buildRegisterRequest()` populates the `credential` oneof; on enrollment response the agent persists the returned `AgentToken`. `codes.Unauthenticated` from the server causes a clean shutdown (no retry loop).
- **gRPC Connect auth enforcement** (`internal/worker/handler.go`): `Connect` now calls `authenticateAndRegister`, which dispatches on credential type. Enrollment path: hash lookup → consumed/expiry checks → upsert worker → consume enrollment atomically → set agent token → send response. Reconnect path: hash lookup → `pgx.ErrNoRows` → `Unauthenticated`. All failures use the same opaque "authentication failed" message.
- **Enrollment janitor** (`cmd/relay-server/main.go`): hourly goroutine calls `DeleteExpiredAgentEnrollments` to prevent table bloat.

## Key Decisions

**Hash-before-consume ordering for concurrent enrollment race**: initially the agent token was written before consuming the enrollment, meaning two concurrent callers could both write different tokens, with the DB holding whichever wrote last regardless of which caller won the `ConsumeAgentEnrollment` race. Fixed by moving `ConsumeAgentEnrollment` to before `SetWorkerAgentToken`, so only the atomic winner sets the token.

**Enrollment consumed before response sent**: an earlier draft called `ConsumeAgentEnrollment` after `finishRegister` (which sends `RegisterResponse`). A failure in the consume would leave the client with a valid token while the server returned an error, causing the stream to close. Fixed: consume atomically before calling `finishRegister`.

**Token format consistency**: enrollment tokens follow the same pattern as HTTP API tokens (32 random bytes → hex → SHA-256(hex) → store hash), except enrollment tokens hash the raw string directly rather than its hex encoding. This was an internal consistency choice — the test helpers match — but deviates from the CLAUDE.md token-format doc. Not corrected in this session.

**`RELAY_AGENT_ENROLLMENT_TOKEN` cleared from env**: mirrors what the server already does for `RELAY_BOOTSTRAP_PASSWORD`. Prevents the enrollment token leaking to subprocess task runners (which are untrusted).

**`ClearWorkerAgentToken` changed from `:exec` to `:execrows`**: enables the revoke handler to return `404` for unknown worker IDs, matching the codebase's 404-on-unknown pattern rather than silently returning `204`.

## Problems Encountered

**Plan used incorrect CLI patterns**: the implementation plan referenced `newClient(cfg)`, `client.PostJSON`, stdout/stderr globals, and `Config{Server: ...}` — none of which exist. The actual patterns are `cfg.NewClient()`, `c.do(ctx, method, path, body, &resp)`, `io.Writer` params, and `Config{ServerURL: ...}`. Required rebriefing the CLI implementer subagent with corrected patterns.

**`UpsertWorkerByHostname` returns `UpsertWorkerByHostnameRow` not `store.Worker`**: the plan's handler implementation assumed a `store.Worker` return type. Since `UpsertWorkerByHostnameRow` only carries the `ID`, the `finishRegister` helper signature had to use `pgtype.UUID` directly rather than a full worker struct.

**Merge conflict on CORS files**: `master` had already received an earlier CORS commit from another worktree (`claude/pedantic-burnell-6dd300`). The conflict was minimal — the branch's version added an early-return optimization for empty allowlists and a preflight passthrough regression test, both of which were taken.

## Known Limitations

**Enrollment token hashing inconsistency**: the CLAUDE.md token-format doc specifies SHA-256 of the hex-encoded bytes; enrollment tokens hash the raw string instead. Both the server and tests are internally consistent, but the deviation from the documented pattern is a future maintenance hazard.

**No transaction wrapping enrollment + token set**: `UpsertWorkerByHostname`, `ConsumeAgentEnrollment`, and `SetWorkerAgentToken` are three separate DB calls. A crash between consume and set-token would leave the enrollment consumed but no token written. The agent would be stuck until an admin issues a new enrollment. A future improvement is to wrap these in a single transaction.

## What We Did Well

- **Consistent opaque error messages**: all auth failures return "authentication failed" with no enumeration — no difference between "token not found", "token consumed", and "token expired" is visible to the caller.
- **Two-stage review caught real bugs**: the spec compliance reviewer caught the consume-after-response ordering issue; the code quality reviewer independently caught the `workerUUID.Scan` silent error and the dead `!enroll.ExpiresAt.Valid` guard. Both were fixed before merge.
- **`crypto/rand` everywhere**: all token generation uses `cryptorand.Read`, never `math/rand`.
- **Test coverage on security-critical paths**: 6 dedicated auth integration tests covering valid enrollment, reconnect, revocation, single-shot, expiry, and nil credential.

## What We Did Not Do Well

- **Plan accuracy on CLI patterns**: the plan author didn't verify actual CLI helper signatures before writing the spec, requiring an implementation rebriefing. A quick `grep` of `cli/` before writing the plan would have prevented this.
- **Plan type accuracy on store return types**: same root cause — plan assumed `store.Worker` without checking the actual sqlc-generated return type for `UpsertWorkerByHostname`.

## Improvement Goals

- Verify all referenced function signatures and return types against actual generated code before finalizing an implementation plan.
- Consider wrapping multi-step enrollment (upsert + consume + set-token) in a DB transaction for crash safety.

## Files Most Touched

| File | Notes |
|------|-------|
| `internal/worker/handler.go` | Full Connect auth rewrite: `authenticateAndRegister`, `enrollAndRegister`, `reconnectAndRegister`, `finishRegister` |
| `internal/worker/handler_auth_test.go` | 386 lines of new integration tests for all auth paths |
| `internal/store/agent_enrollments.sql.go` | sqlc-generated CRUD for enrollment tokens |
| `internal/store/workers.sql.go` | sqlc-generated agent token set/clear/lookup queries |
| `internal/store/agent_enrollments_test.go` | Integration tests: create, consume, list-active, delete-expired |
| `internal/proto/relayv1/relay.pb.go` | Generated from proto: credential oneof + AgentToken response field |
| `internal/cli/workers.go` | Added `revoke` subcommand with UUID/hostname resolution |
| `internal/cli/agent_enroll.go` | New `relay agent enroll` command |
| `internal/agent/credentials.go` | Token file load/persist with 0600 perms |
| `internal/agent/agent.go` | Credential sending, AgentToken persistence, Unauthenticated shutdown |

## Commit Range

`8660339..65ccb05`
