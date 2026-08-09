---
title: "Admin console: Server / overview tab"
type: feature
status: open
created: 2026-08-08
priority: medium
source: carved from feature-2026-06-26-admin-console-pages during the 2026-08-08 admin-console-shell-users-tab slice
---

# Admin console: Server / overview tab

## Summary
The Server tab of the admin console: a read-only operational overview aggregating the existing
`GET /v1/jobs/stats` and `GET /v1/workers/stats` endpoints. Frontend-only as scoped; the Holo's
env-var table is largely unbacked and is the open design question.

## Context
Carved out of the admin-console omnibus item when the 2026-08-08 slice shipped the `/admin/:tab` shell
plus the Users tab. Design source of truth is `AdminServer` in
`design_handoff_relay_holo/hifi3-holo-pages.jsx` (line 2280) plus the server-facts strip in
`HoloAdmin`'s header (line 1952); `reference/screens/admin.js` is structure-only.

## Proposal
- Register a `server` tab in the admin shell registry.
- Aggregate what exists today:
  - `GET /v1/jobs/stats` -> `{ running, queued, done_24h, failed_24h }` (`internal/api/jobs.go:148`)
  - `GET /v1/workers/stats` -> `{ online, stale, offline, disabled, total }`
  - `GET /v1/config` -> `{ allow_self_register }`
  - `GET /v1/health` -> `{ status: "ok" }` for the healthy/unhealthy pill
- Render these with the shipped `KpiStat` and `Panel` Holo primitives (this is the first admin surface
  where `KpiStat` actually applies).
- Decide, as part of the design, whether to add a backend build-info endpoint or drop the header
  facts strip entirely (see Notes). Do not fabricate values.

## Acceptance / Done When
- `/admin/server` renders job and worker aggregate counts plus the self-register flag and a health
  pill, admin-gated.
- Every number on the page traces to a real endpoint field; nothing is mocked or hard-coded.
- Vitest + MSW coverage for each query and for the degraded case where one stats call fails while the
  others succeed (the panel degrades, the page does not).

## Related
- Carved from [[feature-2026-06-26-admin-console-pages]]
- Shell + patterns: `docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`
- `done_24h`/`failed_24h` are known-approximate: [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]]
- Sibling tabs: [[feature-2026-08-08-admin-enrollments-tab]],
  [[feature-2026-08-08-admin-reservations-tab]], [[feature-2026-08-08-admin-invites-tab]]
- Source: `internal/api/jobs.go:148`, `internal/api/workers.go` (stats handler),
  `internal/api/config.go`, `internal/api/health.go`,
  `design_handoff_relay_holo/hifi3-holo-pages.jsx:2280`

## Notes
Most of what `AdminServer` draws is **not backed by any endpoint**, verified:
- The header `VERSION` / `BUILD` / `DB` / `UPTIME` facts strip has no source. `GET /v1/health` returns
  only `{"status":"ok"}` and `GET /v1/config` returns only `{allow_self_register}`.
- The four-group env-var table (`RELAY_WORKER_GRACE_WINDOW`, `RELAY_TELEMETRY_*`, `RELAY_DB_MAX_CONNS`,
  `RELAY_CORS_ORIGINS`, ...) has no endpoint. Exposing effective server configuration to admins is a
  deliberate decision with a security dimension: it leaks operational detail, and `RELAY_DATABASE_URL`
  must never be returned even redacted-looking. If we want this, it should be an explicit
  allowlist-only endpoint (`GET /v1/server/info`, admin-only, hand-picked non-secret keys plus version
  and build), specified separately - not a generic env dump.

Recommended split: ship this tab with only the four already-existing sources, and file the
`GET /v1/server/info` allowlist endpoint as its own backend item if the facts strip and env table are
still wanted. Also worth noting `bug-2026-06-05-jobs-stats-24h-updated-at-proxy` before putting
`done_24h`/`failed_24h` on a page labelled as an operational source of truth.
