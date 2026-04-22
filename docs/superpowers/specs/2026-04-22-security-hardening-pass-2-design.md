# Security Hardening Pass 2 — Design

**Date:** 2026-04-22
**Scope:** Four security gaps identified in prior design review: unauthenticated agent registration (mDNS + gRPC `Connect`), missing rate limits on auth endpoints, missing CORS policy, and `RELAY_BOOTSTRAP_PASSWORD` lingering in process env.
**Non-goals:** mTLS / TPM-bound client certs, short-lived agent tokens with refresh, multi-server rate-limit aggregation, CORS support behind reverse proxies with trusted `X-Forwarded-For`, password-file alternative to `RELAY_BOOTSTRAP_PASSWORD`.

## Threat model

**Semi-trusted LAN.** The server and agents run inside a studio network. Most hosts are friendly, but we want to defend against:

- A compromised workstation on the same network registering as a rogue worker and receiving dispatched tasks.
- A compromised workstation brute-forcing an admin account via the HTTP auth endpoints.
- A compromised workstation exhausting server CPU by repeatedly triggering bcrypt on the login path.
- Accidental cross-origin access from a future browser dashboard being exposed to any origin by default.
- Operator footguns that leave the bootstrap admin password readable in `/proc/<pid>/environ` for the life of the server process.

**Explicitly not in scope:**

- Fully hostile networks or internet-exposed deployments. Those need mTLS and are deferred.
- A distributed attacker (botnet or many compromised LAN hosts) coordinating across many source IPs. Per-IP rate limiting doesn't stop this; we accept the residual risk.
- Defense against an attacker who already has code execution on an already-enrolled agent host. That attacker can exfiltrate the agent's token and impersonate that worker until revoked. See "Residual risks" below.

## Motivation

After Critical (2026-04-19) and Major (2026-04-22) concurrency passes, the coordinator is correct under concurrent load. The remaining v1 gaps are authentication and auth-layer hardening:

- **Issue 1 — Unauthenticated agent registration.** [internal/worker/handler.go:38](internal/worker/handler.go:38) accepts any `RegisterRequest` with zero auth. mDNS advertises `_relay._tcp` unauthenticated. Any host that can reach gRPC port `:9090` registers as a worker and receives real task dispatches.
- **Issue 2 — No rate limiting.** `POST /v1/auth/login` and `POST /v1/auth/register` are public endpoints with no middleware. bcrypt cost 12 is deliberately expensive; without rate limiting, unauthenticated traffic can pin every server CPU at 100% and makes dictionary brute-force viable.
- **Issue 3 — No CORS policy.** [internal/api/server.go](internal/api/server.go) emits no CORS headers at all. Not exploitable today (no browser UI), but the moment one ships, the policy must be explicit — fails-closed by default avoids a much messier migration later.
- **Issue 4 — Bootstrap password lifetime.** [cmd/relay-server/main.go:70-78](cmd/relay-server/main.go:70) consumes `RELAY_BOOTSTRAP_PASSWORD` at startup but never unsets it. It sits in the process env for the life of `relay-server`, visible via `/proc/<pid>/environ` and readable by any child process.

Fixed together because they share an implementation scope (auth layer and startup wiring) and reinforce each other: agent auth without rate limits leaves the new `/v1/agent-enrollments` endpoint brute-forceable; bootstrap env hygiene is orthogonal but small enough that bundling it avoids a second hardening pass.

## Guiding principle

Fail closed at every unauthenticated surface. Make credentials revocable per identity, not globally. Document what this pass does and does not defend against so operators aren't mistaken about the guarantees.

## Approach summary

1. **Agent enrollment tokens** gate first-boot registration; **per-agent long-lived tokens** gate subsequent reconnects. Tokens are revocable per worker.
2. **In-memory per-IP rate limiting** on `/v1/auth/login` and `/v1/auth/register`, with configurable limits.
3. **Opt-in CORS allowlist** from env var, default empty (same-origin only).
4. **`os.Unsetenv`** on `RELAY_BOOTSTRAP_PASSWORD` and `RELAY_BOOTSTRAP_ADMIN` after consumption.

---

## 1. Agent enrollment & authentication

### Data model

**New table** `agent_enrollments`:

```sql
CREATE TABLE agent_enrollments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash      TEXT NOT NULL UNIQUE,     -- SHA256(hex) of raw token
    hostname_hint   TEXT,                      -- optional, log-only (not enforced)
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,      -- default created_at + 24h
    consumed_at     TIMESTAMPTZ,
    consumed_by     UUID REFERENCES workers(id)
);
CREATE INDEX ix_agent_enrollments_token_hash ON agent_enrollments(token_hash);
```

**Modified** `workers` table:

```sql
ALTER TABLE workers ADD COLUMN agent_token_hash TEXT UNIQUE;
CREATE INDEX ix_workers_agent_token_hash
  ON workers(agent_token_hash) WHERE agent_token_hash IS NOT NULL;
```

**New worker status**: `"revoked"` alongside `"online"`, `"offline"`, `"draining"`. Revoked workers are skipped by the scheduler dispatch loop. Status is a latched state; re-enrollment clears it automatically.

Token format matches existing user bearer tokens: 32 crypto/rand bytes → hex-encode → SHA-256(hex) → hex-encode stored. Raw token returned to client exactly once.

### Proto additions (`proto/relayv1/relay.proto`)

```proto
message RegisterRequest {
  // existing fields...
  oneof credential {
    string enrollment_token = N;   // first-boot only
    string agent_token      = N+1; // every subsequent reconnect
  }
}

message RegisterResponse {
  // existing fields...
  string agent_token = N;  // populated only on successful enrollment
}
```

Field numbers are TBD based on current proto slots.

### SQL / sqlc queries

**New** (in `internal/store/query/agent_enrollments.sql`):

- `CreateAgentEnrollment :one` — inserts with hash, hostname hint, creator, expiry. Returns id + expires_at.
- `GetAgentEnrollmentByTokenHash :one` — lookup by hash. Used during Connect enrollment path.
- `ConsumeAgentEnrollment :exec` — sets `consumed_at = now(), consumed_by = $worker_id` where id = $1 and consumed_at IS NULL. Zero rows affected → enrollment already consumed or deleted (handler returns `Unauthenticated`).
- `ListActiveAgentEnrollments :many` — returns rows where `consumed_at IS NULL AND expires_at > now()`. For admin visibility.
- `DeleteExpiredAgentEnrollments :exec` — janitorial; called on a 1h ticker in `relay-server` main.

**New / modified** (in `internal/store/query/workers.sql`):

- `SetWorkerAgentToken :exec` — sets `agent_token_hash = $2` where id = $1. Used during enrollment after token generation.
- `ClearWorkerAgentToken :exec` — sets `agent_token_hash = NULL, status = 'revoked'` where id = $1. Used on revocation.
- `GetWorkerByAgentTokenHash :one` — lookup by hash for subsequent reconnects. Excludes `status = 'revoked'` as defense-in-depth (revoked rows already have `agent_token_hash = NULL`, so the exclusion is belt-and-suspenders).

`UpsertWorkerByHostname` and `UpdateWorkerStatus` (existing) are **unchanged**. The `revoked → online` flip happens automatically: after enrollment auth succeeds, `registerWorker` proceeds through its existing upsert-then-status-update path, and `UpdateWorkerStatus(..., "online", ...)` flips the row out of `revoked` as a side effect.

Regenerate via `make generate` after editing SQL.

### Server-side Connect flow (`internal/worker/handler.go`)

Modify `Connect` first-message handling before `registerWorker`:

```go
first, err := stream.Recv()
// ... existing nil/type checks ...

reg := first.GetRegister()
workerID, sender, issuedToken, err := h.authenticateAndRegister(ctx, stream, reg)
if err != nil {
    return status.Errorf(codes.Unauthenticated, "authentication failed")
}
```

`authenticateAndRegister` branches on `reg.Credential`:

- `EnrollmentToken`: look up enrollment by hash. Reject if missing / expired / already consumed. On success:
  1. `UpsertWorkerByHostname` (existing logic, bumps status to `online`).
  2. Generate new agent token (32 rand bytes → hex). Compute hash.
  3. `SetWorkerAgentToken(worker.id, hash)`.
  4. `ConsumeAgentEnrollment(enrollment.id, worker.id)` — transactional with step 3.
  5. Include raw token in `RegisterResponse.agent_token`. Log info-level "worker enrolled".
- `AgentToken`: `GetWorkerByAgentTokenHash(hash)`. If missing → `codes.Unauthenticated`. If found and `status = 'revoked'` → same. Otherwise proceed with existing reconcile flow.
- Missing or empty oneof → `codes.Unauthenticated`.

All auth failure messages are generic ("authentication failed") — no enumeration of whether the token was unknown, expired, revoked, or consumed.

### Admin HTTP endpoints

**New** in `internal/api/agent_enrollments.go`:

- `POST /v1/agent-enrollments` (admin-only):
  - Body: `{"hostname_hint": "render-node-07", "ttl_seconds": 86400}` — both optional. Default TTL 24h. Minimum 60s, maximum 7d.
  - Response: `{"token": "7a8f...", "expires_at": "..."}`. Token returned once.
- `GET /v1/agent-enrollments` (admin-only) — lists non-consumed, non-expired enrollments (never token or hash, just metadata). Useful for cleanup visibility.
- `DELETE /v1/workers/{id}/token` (admin-only) — calls `ClearWorkerAgentToken`. Idempotent. Returns 204 regardless of whether token was previously set.

Wire in [internal/api/server.go](internal/api/server.go) via the existing `auth(admin(...))` chain.

### Agent-side credential handling

**New file** `internal/agent/credentials.go`:

```go
type Credentials struct {
    EnrollmentToken string  // from env, used once
    AgentToken      string  // from state-dir token file; persisted after enrollment
    tokenFilePath   string
}

func LoadCredentials(stateDir string) (*Credentials, error)
func (c *Credentials) Persist(token string) error  // writes 0600 file
func (c *Credentials) HasAgentToken() bool
```

**Modified** `internal/agent/agent.go`:

- On startup (in `Run()`): `LoadCredentials(stateDir)`. If `HasAgentToken()` → send `agent_token` field. Else if `EnrollmentToken != ""` → send `enrollment_token`. Else → `return fmt.Errorf("no credentials: set RELAY_AGENT_ENROLLMENT_TOKEN or provide %s", tokenFilePath)`.
- On successful first `RegisterResponse` with non-empty `agent_token`: call `c.Persist(resp.AgentToken)`, log info "agent token persisted to $path".
- On gRPC `codes.Unauthenticated` response when already holding a stored agent token: log error, exit non-zero. Do **not** fall back to `RELAY_AGENT_ENROLLMENT_TOKEN` if present — prevents silent re-enrollment after revocation.
- On `codes.Unauthenticated` during enrollment (no stored token yet): log error, exit non-zero. Operator must mint a fresh enrollment token.

**Modified** `cmd/relay-agent/main.go`: reads `RELAY_AGENT_ENROLLMENT_TOKEN` env var into `Credentials.EnrollmentToken` only if the token file doesn't already exist.

### Admin CLI

**New** `internal/cli/agent_enroll.go`:

- `relay agent enroll [--hostname HINT] [--ttl 24h]` — POSTs to `/v1/agent-enrollments`. Prints raw token to stdout on its own line plus `expires_at` to stderr. Single invocation; no retries.

**New** `internal/cli/workers_revoke.go`:

- `relay workers revoke <id-or-hostname>` — resolves hostname→id if needed via existing `relay workers list` logic, then `DELETE /v1/workers/{id}/token`. Prints `revoked.` on success.

### Rollout break

Step 4c below is a **breaking change**: all pre-existing worker rows have `agent_token_hash = NULL` after migration, and `Connect` now rejects requests without a valid credential. Every existing agent must be re-enrolled. Release notes must walk operators through:

1. Mint enrollment tokens for every existing host via `relay agent enroll`.
2. Distribute; set `RELAY_AGENT_ENROLLMENT_TOKEN` on each agent host.
3. Delete any stale `<state-dir>/token` (won't exist on pre-upgrade hosts).
4. Restart agents.

There is no in-place "bless existing workers" path — the whole point is that pre-upgrade workers have no authenticated identity.

---

## 2. Rate limiting on auth endpoints

**New file** `internal/api/ratelimit.go`:

```go
type rateLimiter struct {
    mu      sync.Mutex
    windows map[string][]time.Time
    limit   int
    window  time.Duration
}

func RateLimit(limit int, window time.Duration) func(http.Handler) http.Handler
```

On each request: extract IP via `net.SplitHostPort(r.RemoteAddr)`. Prune timestamps older than `window` for that IP. If remaining count ≥ limit → HTTP 429, body `{"error": "rate limit exceeded"}`, header `Retry-After: N` (seconds until oldest timestamp falls out of window). Otherwise append `time.Now()` and pass through.

**X-Forwarded-For is not trusted.** The server isn't designed to sit behind a reverse proxy. Trusting the header would let an attacker forge keys and bypass the limit trivially. Documented as a non-goal; revisit if reverse-proxy deployment becomes a supported configuration.

**Applied only to**:
- `POST /v1/auth/login`
- `POST /v1/auth/register`

Not applied to authenticated routes (bearer token itself is a stronger identity than IP).

**Configuration** (parsed at startup):

| Env var | Default | Format |
|---|---|---|
| `RELAY_LOGIN_RATE_LIMIT` | `10:1m` | `N:duration`, e.g. `20:30s` |
| `RELAY_REGISTER_RATE_LIMIT` | `5:1m` | same |

Invalid format → startup fails with clear error.

**GC**: background goroutine prunes empty map entries every 5 minutes to bound memory. Keyed by IP; each entry stores at most `limit` timestamps, so worst-case is O(attacking IPs × limit × 16 bytes). A 10k-IP attack holds ~1.6MB; acceptable for defense and self-limiting via the rate cap.

**Per-IP only, not per-email**: per-email creates a DoS vector (attacker floods failed logins against a known admin email, legitimate user gets locked out). Per-IP avoids this while still meaningfully raising the cost of horizontal credential guessing. The existing dummy-hash enumeration defense ([internal/api/auth.go](internal/api/auth.go) `getDummyHash`) handles the per-account timing side channel.

**Memory-only, not DB-backed**: multi-server is explicitly deferred. Single process handles all auth traffic today. DB-backed would cost a row insert per login attempt and serialize under lock contention during the exact attack conditions we're defending against. Called out as a deferred concern.

---

## 3. CORS policy

**New file** `internal/api/cors.go`:

```go
func CORS(allowedOrigins []string) func(http.Handler) http.Handler
```

Wraps the `Handler()` mux in [internal/api/server.go](internal/api/server.go).

**Behavior**:

- **Preflight** (`OPTIONS`): if `Origin` is in the allowlist, respond 204 with:
  - `Access-Control-Allow-Origin: <echoed origin>`
  - `Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS`
  - `Access-Control-Allow-Headers: Authorization, Content-Type`
  - `Access-Control-Max-Age: 600`
  If `Origin` is not allowlisted, respond 204 with no CORS headers (browser blocks).
- **Non-preflight**: emit `Access-Control-Allow-Origin: <echoed origin>` only when origin is in the allowlist; otherwise emit nothing and let the handler respond normally.
- **Never emit `Access-Control-Allow-Credentials`**. Bearer tokens ride in `Authorization` headers, not cookies. Credentials mode is both unnecessary and a footgun when combined with permissive origins.

**Configuration**:

| Env var | Default | Format |
|---|---|---|
| `RELAY_CORS_ORIGINS` | empty (same-origin only) | Comma-separated, e.g. `https://relay.studio.local,https://dashboard.studio.local` |

**Validation at startup**:
- Wildcard (`*`) is rejected with a startup error. Wildcard plus `Authorization` headers is always wrong for an API like this.
- Empty entries are ignored. Non-HTTP(S) schemes are rejected.
- Duplicate origins are deduplicated silently.

**Why opt-in with empty default**: there is no browser UI today, so empty-default means zero behavior change for existing deployments. When a dashboard ships, deployers explicitly opt in per origin — they can't accidentally expose the API to any origin.

---

## 4. Bootstrap password lifetime

Three-line change in [cmd/relay-server/main.go:70-78](cmd/relay-server/main.go:70):

```go
if bootstrapEmail := os.Getenv("RELAY_BOOTSTRAP_ADMIN"); bootstrapEmail != "" {
    bootstrapPassword := os.Getenv("RELAY_BOOTSTRAP_PASSWORD")
    if bootstrapPassword == "" {
        log.Fatalf("RELAY_BOOTSTRAP_PASSWORD must be set when RELAY_BOOTSTRAP_ADMIN is set")
    }
    if err := bootstrapAdmin(ctx, q, bootstrapEmail, bootstrapPassword); err != nil {
        log.Fatalf("bootstrap admin: %v", err)
    }
    os.Unsetenv("RELAY_BOOTSTRAP_PASSWORD")
    os.Unsetenv("RELAY_BOOTSTRAP_ADMIN")
    bootstrapPassword = ""
}
```

**Docs**: CLAUDE.md and README update — document that operators should unset the shell env after first boot, and that `relay-server` clears them from its own process env regardless. Child processes `relay-server` forks (none today; defensive) won't inherit either.

**Not doing**: `RELAY_BOOTSTRAP_PASSWORD_FILE` read-then-delete alternative. Adds a code path for a feature used once per deployment. YAGNI. Called out as a considered alternative.

**Best-effort caveat**: Go strings are immutable and GC-managed. `bootstrapPassword = ""` drops the local reference but cannot scrub the underlying bytes — they persist in memory until GC collects the heap region and potentially beyond if swapped. Memory scrubbing in Go requires `unsafe` tricks or a byte slice; not worth the complexity at this threat level. Documented.

---

## Rollout

Each step leaves `main` in a shipping state.

1. **CORS middleware.** Additive, empty default → zero behavior change. Safest first.
2. **Rate limiting middleware.** Wraps two existing routes. Happy path unaffected; integration tests cover 429 boundary.
3. **Bootstrap password `Unsetenv`.** Two-line code change + docs. Independent of everything else.
4. **Agent enrollment / auth.** Broken into sub-steps:
   - **4a. Migration + sqlc queries.** Additive schema, no behavior change. Table + column added, new queries generated. `status = 'revoked'` is a valid value but nothing sets it yet.
   - **4b. Admin HTTP endpoints + CLI.** `POST /v1/agent-enrollments`, `GET /v1/agent-enrollments`, `DELETE /v1/workers/{id}/token`, plus `relay agent enroll` and `relay workers revoke`. End-to-end testable via HTTP before gRPC enforcement lands.
   - **4c. gRPC `Connect` auth enforcement + agent-side credentials.** Breaking change — all existing workers must re-enroll. Release notes required. Janitorial `DeleteExpiredAgentEnrollments` ticker wired into `relay-server` main alongside existing dispatchers.

Steps 1–3 can land in any order. Step 4 depends on none of them but is gated by operator coordination for 4c.

---

## Testing strategy

### Unit tests (no DB)

- `internal/api/ratelimit_test.go` — under-limit pass, at-limit 429 with Retry-After, window boundary (oldest hit slides out), per-IP isolation, pruning goroutine doesn't drop keys with recent hits.
- `internal/api/cors_test.go` — empty allowlist emits no headers; allowlisted origin receives expected preflight headers; non-allowlisted origin receives bare 204; wildcard in config fails startup; `Access-Control-Allow-Credentials` never emitted.
- `internal/api/agent_enrollments_test.go` — admin-only enforcement; TTL bounds enforced (min 60s, max 7d); `GET` lists non-consumed non-expired only.
- `internal/agent/credentials_test.go` — token file read/write round-trip with 0600 perms; missing file returns empty credentials without error; corrupt file returns error; persisted token overwrites prior value.
- `internal/cli/agent_enroll_test.go` — argument parsing; happy-path request/response; error propagation.

### Integration tests (testcontainers postgres, `//go:build integration`)

- `internal/store/agent_enrollments_test.go` — create + consume + list + expire; `ConsumeAgentEnrollment` is single-shot (second call affects 0 rows); `SetWorkerAgentToken` + `GetWorkerByAgentTokenHash` round-trip; `ClearWorkerAgentToken` sets status to `revoked`.
- `internal/worker/handler_auth_test.go` — Connect with valid enrollment token issues agent token; Connect with the issued agent token succeeds; Connect with revoked agent token returns `Unauthenticated`; Connect with already-consumed enrollment returns `Unauthenticated`; Connect with expired enrollment returns `Unauthenticated`; Connect with no credential returns `Unauthenticated`.
- `internal/worker/handler_reenroll_test.go` — worker revoked → re-enrolled with fresh token → `UpsertWorkerByHostname` reuses same `workers.id`, status flips `revoked → online`, task history preserved.
- `cmd/relay-server/bootstrap_env_test.go` — after server `bootstrapAdmin` path runs, `os.Getenv("RELAY_BOOTSTRAP_PASSWORD")` returns empty within the same process.

### Tests that need updating

- Any existing `handler_test.go` test constructing `RegisterRequest` must set the `credential` oneof. Helper: add `testEnrolledRegisterRequest(...)` in `handler_test.go` to keep call sites terse.
- Integration tests that spin up `relay-server` end-to-end must seed an enrollment token and pass it to the test agent.

### Not tested

- CORS browser behavior end-to-end (can't fake a browser in Go tests; middleware unit test is sufficient).
- Memory scrubbing of bootstrap password (infeasible in Go without `unsafe`; documented best-effort).
- Multi-IP rate-limit bypass (out of scope — documented residual risk, not a defended path).

---

## Configuration reference

New env vars, all documented in CLAUDE.md and README:

| Var | Default | Purpose |
|---|---|---|
| `RELAY_CORS_ORIGINS` | empty | Comma-separated CORS allowlist. Empty = same-origin only. Wildcard rejected. |
| `RELAY_LOGIN_RATE_LIMIT` | `10:1m` | Per-IP rate limit on `POST /v1/auth/login`. Format `N:duration`. |
| `RELAY_REGISTER_RATE_LIMIT` | `5:1m` | Per-IP rate limit on `POST /v1/auth/register`. |
| `RELAY_AGENT_ENROLLMENT_TOKEN` | — | Agent-side one-time bootstrap credential. Read only when `<state-dir>/token` does not exist. |

New agent state-dir artifact:

| Path | Perms | Contents |
|---|---|---|
| `<state-dir>/token` | 0600 | Long-lived agent bearer token, persisted after first successful enrollment. |

---

## Residual risks (explicitly accepted)

| Risk | Why we accept it |
|---|---|
| **Token exfiltration from a compromised agent host.** An attacker with shell access on an enrolled agent host can read `<state-dir>/token` and impersonate that worker from elsewhere until revoked. | Defeating this requires TPM-bound client certs or hardware-attested credentials. Out of scope for threat model 2. Isolation (revocation kicks one host without affecting others) and audit (each worker has a unique token) are proportionate. |
| **Distributed rate-limit bypass.** Per-IP limits do nothing against an attacker distributing traffic across many source IPs (botnet, many compromised hosts). | Threat model 2 is "one compromised workstation." Distributed attacks live in threat model 3. Limits still raise cost materially for the one-host case. |
| **Rate limiter state is process-local.** If `relay-server` ever runs multi-instance, each instance has its own window and effective limits multiply by replica count. | Multi-server is a deferred design. This pass is correct for single-instance. Spec calls out the needed rework if multi-server lands. |
| **`X-Forwarded-For` untrusted.** Rate limiter uses `RemoteAddr` directly; deploying behind a reverse proxy rate-limits the proxy, not the client. | Supported deployment today is direct. Revisit when reverse proxy is a first-class deployment. |
| **Bootstrap password remains in heap until GC.** `os.Unsetenv` removes the env entry, but the Go string holding the value may linger in memory until garbage-collected. | Memory scrubbing in Go requires `unsafe` and is beyond this pass's ambition. Documented. |
| **Existing agents must re-enroll on upgrade.** No in-place migration preserves pre-auth worker identities. | This is the whole point — pre-upgrade workers have no authenticated identity. Documented in release notes. |

---

## Open questions

None at spec-writing time. Proto field numbers and migration number are determined at implementation time based on the current state of those files.
