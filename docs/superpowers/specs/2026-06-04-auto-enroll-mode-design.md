# Token-less Auto-Enrollment Mode

Date: 2026-06-04
Status: Approved (design)

## Problem

Bringing a new worker agent online today requires an admin to mint a one-time
enrollment token (`POST /v1/agent-enrollments`) and deliver it to the agent via
`RELAY_AGENT_ENROLLMENT_TOKEN`. On a trusted private network - where any host
that can reach the gRPC server is already considered trusted - this per-agent
token dance is friction without a corresponding security benefit.

We want a mode where an agent on a trusted network can join with no token at
all, while preserving the existing token lifecycle for everything after the
initial join.

## Goals

- Allow an agent to enroll without presenting any credential, gated by a single
  server-wide flag.
- After joining, the agent receives and persists a normal long-lived agent
  token; all subsequent reconnects use the unchanged token path.
- Keep admin revocation meaningful: a revoked host must not be silently revived
  by auto-enroll.
- No proto changes.

## Non-Goals

- Mixed fleets (some agents token-based, some token-less against the same
  server). Not supported now. The server flag is global; the whole fleet is
  treated uniformly.
- Per-host allowlisting (CIDR ranges), admin approval queues, mTLS, or signed
  requests. The trust boundary is network reachability alone.
- Defending against a determined operator who renames a host to evade
  revocation. Out of scope (see Security model).

## Security Model

The trust boundary is **network reachability to the gRPC server**. Enabling the
flag is an explicit statement that any host able to reach gRPC may join. This is
appropriate only for trusted private networks (LAN/VPC the operator controls).

Revocation remains the one deliberate, manual control over the fleet. Because an
explicit admin action (revoke) should outrank a passive event (a host joining),
auto-enroll refuses to revive a `revoked` worker. This is a speed-bump, not a
wall: identity is keyed by hostname, so a renamed revoked host can rejoin as a
new identity. That is an accepted limitation - the guard protects revocation
against routine reconnects and accidental undo, not against conscious evasion.

## Configuration

- **Server:** new env var `RELAY_ALLOW_AUTO_ENROLL` (bool, default `false`).
  When false (the default), behavior is identical to today. When true, the
  handshake accepts agents that present no credential.
- **Agent:** no new configuration. Token-less enrollment is purely implicit.

## Design

### Credential signal: unset oneof

`RegisterRequest` carries `oneof credential { enrollment_token, agent_token }`.
An **unset** oneof is the natural "no credential" signal. No proto change is
required.

### Server: handler wiring (`internal/worker/handler.go`)

Add an exported field to `Handler`, mirroring the existing `Metrics` field that
is set by `cmd/relay-server` after construction (avoids changing both the
`NewHandler` and `NewHandlerWithGrace` signatures):

```go
// AllowAutoEnroll, when true, permits agents to register with no credential
// (token-less auto-enrollment). Set by cmd/relay-server after construction.
AllowAutoEnroll bool
```

`cmd/relay-server/main.go` reads `RELAY_ALLOW_AUTO_ENROLL` and sets the field.

### Server: handshake dispatch

`authenticateAndRegister` currently has `default -> Unauthenticated "authentication failed"`
for the unset-credential case. Change the `default` branch:

- If `h.AllowAutoEnroll` is **false** -> reject with a *specific* message so the
  agent can fail loud distinguishably, e.g.
  `status.Errorf(codes.Unauthenticated, "auto-enroll disabled")`.
- If **true** -> call new `autoEnrollAndRegister`.

The existing `enrollment_token` and `agent_token` cases are unchanged and
continue to take priority whenever a credential is present.

### Server: `autoEnrollAndRegister`

Mirrors `enrollAndRegister` but omits the enrollment-token lookup/consume. Steps:

1. Generate a fresh agent token via `agentTokenGenerator()` -> `(rawHex, hash)`.
2. In a single `pgx.BeginTxFunc` transaction:
   a. **Revoked guard (before upsert):** read the existing worker by hostname
      with a row lock. If it exists and `status = 'revoked'`, abort the
      transaction (via a sentinel error) and reject the connection with
      `status.Errorf(codes.PermissionDenied, "worker revoked")`. The guard must
      precede the upsert so the upsert cannot silently reset a revoked worker to
      online.
   b. `UpsertWorkerByHostname` - creates a new worker row or rebinds the
      existing one (admin-managed fields preserved on conflict, as today).
   c. `SetWorkerAgentToken` with the new hash.
3. On success, `finishRegister` sends `RegisterResponse{agent_token: rawHex}`.
   The agent persists it exactly as in the existing enrollment path.

After this point the worker owns a normal long-lived token. Reconnects,
revocation, grace-window requeue, and all other behavior are identical to a
token-enrolled worker.

### Store query (`internal/store/query/workers.sql`)

`GetWorkerByHostname` exists but performs a plain `SELECT` with no row lock. Add
a locking variant for use inside the enrollment transaction:

```sql
-- name: GetWorkerByHostnameForUpdate :one
SELECT * FROM workers WHERE hostname = $1 FOR UPDATE;
```

Run `make generate` after editing. (`pgx.ErrNoRows` from this query means "no
existing worker" -> proceed to upsert as a brand-new host.)

### Agent (`internal/agent/`)

- `buildRegisterRequest` currently returns an error when neither an agent token
  nor an enrollment token is present. Remove that error: when no credential is
  available, leave `Credential` unset and connect anyway. The agent-token and
  enrollment-token paths are unchanged and still take priority when present.
- On a rejected handshake, surface the server's message clearly so that an agent
  pointed at a non-auto-enroll server fails loud with something actionable
  (distinguishing "auto-enroll disabled" from a genuine bad-token rejection)
  rather than a generic auth error.

## Behavioral Edges (documented, not expanded scope)

- **Token rotation:** an active worker that lost its token file and auto-enrolls
  receives a *new* token; the old hash is invalidated. Expected.
- **Hostname collisions:** two hosts sharing a hostname collide on one worker
  row. A pre-existing property of hostname-keyed identity, not introduced here.
- **Revocation is a speed-bump, not a wall:** a renamed revoked host can rejoin.
  Accepted (see Security model).

## Observability

The server logs each auto-enroll event: worker id, hostname, and the connection
`RemoteAddr`. Cheap, and valuable because - unlike token enrollment - there is
no per-agent enrollment record for these joins.

## Testing (integration, `internal/worker/handler_auth_test.go`)

- Auto-enroll **disabled** + unset credential -> rejected with the specific
  "auto-enroll disabled" status.
- Enabled + new hostname -> worker created, token issued in the response.
- Enabled + existing `revoked` hostname -> rejected (not revived); the worker
  remains `revoked`.
- Enabled + existing active hostname -> token rotated, worker row rebound.
- End-to-end: an auto-enrolled agent persists its issued token, then reconnects
  successfully via the unchanged token path.
