---
title: "GET /v1/server/info - admin-only allowlist build and config facts"
type: feature
status: open
created: 2026-08-09
priority: low
source: carved from feature-2026-08-08-admin-server-overview-tab during its design (2026-08-09)
---

# GET /v1/server/info - admin-only allowlist build and config facts

## Summary
A new admin-only endpoint returning hand-picked, non-secret server facts: version, commit, Go
version, process start time, database server version, and an **allowlisted** set of effective
configuration values. This is the backend enabler for the parts of the admin console's Server tab
that were deliberately omitted because nothing backs them.

## Context
Carved out during the design of the Server / overview tab
(`docs/superpowers/specs/2026-08-09-admin-server-overview-tab.md`). That slice shipped only the four
facts real endpoints already supply and omitted, rather than fabricated, the hi-fi's
`VERSION` / `BUILD` / `DB` / `UPTIME` header strip (`hifi3-holo-pages.jsx:1952`) and its 13-row
environment-variable table (`AdminServer`, `:2280`).

Verified during that design: `handleHealth` returns exactly `{"status":"ok"}` and performs no
database check (`internal/api/health.go:5-7`); `handleConfig` returns exactly
`{"allow_self_register": bool}` (`internal/api/config.go:7-11`); no route anywhere exposes a
version, commit, Go version, uptime, database version, or any `RELAY_*` value.

## Proposal
- `GET /v1/server/info`, registered `auth(admin(...))`. This is the first server-facts route whose
  payload actually needs admin - `/v1/jobs/stats` and `/v1/workers/stats` are `auth`-only and
  `/v1/config` and `/v1/health` are public, so it must not be modelled on them.
- Response shape:
  `{ version, commit, go_version, started_at, db_version, config: [{ key, value, description }] }`.
- `config` is a **hand-written allowlist**, never a filtered `os.Environ()` dump. A deny-list fails
  open on the next variable someone adds. Candidate keys: `RELAY_WORKER_GRACE_WINDOW`,
  `RELAY_TELEMETRY_WINDOW`, `RELAY_TELEMETRY_STALE_AFTER`, `RELAY_DB_MAX_CONNS`,
  `RELAY_CORS_ORIGINS`, `RELAY_ALLOW_SELF_REGISTER`, `RELAY_HTTP_ADDR`, `RELAY_GRPC_ADDR`. Values are
  what the process actually resolved, not the raw strings.
- **Excluded by construction, not redacted:** `RELAY_DATABASE_URL`, `RELAY_BOOTSTRAP_ADMIN`,
  `RELAY_LOGIN_RATE_LIMIT`, `RELAY_REGISTER_RATE_LIMIT`. The last two are operational detail an
  attacker benefits from; if they are wanted later that is a separate decision with its own argument.
- `version` / `commit` require `-ldflags` build vars, which the Makefile does not set today. Adding
  them is part of this item, not an assumption.
- `db_version` is one `SELECT version()` (or `SHOW server_version`) - decide whether it is read once
  at startup or per request; per request makes this endpoint fail when Postgres is down, which may
  be the useful behaviour.
- Frontend follow-on (small): the header facts strip in `AdminPage`, and a config section on the
  Server tab.

## Acceptance / Done When
- `GET /v1/server/info` returns the shape above; a non-admin token gets 403 and this is covered by a
  test.
- The config list is produced from an explicit allowlist in code; a test asserts that no excluded key
  and no substring of the database URL appears in the response, so adding a new secret env var
  cannot leak it.
- `make build` stamps version and commit; the endpoint reports them.

## Related
- Consumer/design: `docs/superpowers/specs/2026-08-09-admin-server-overview-tab.md`
- Parent: [[feature-2026-08-08-admin-server-overview-tab]]
- Source: `internal/api/health.go`, `internal/api/config.go`, `internal/api/server.go` (route table),
  `design_handoff_relay_holo/hifi3-holo-pages.jsx:1952,2280`
