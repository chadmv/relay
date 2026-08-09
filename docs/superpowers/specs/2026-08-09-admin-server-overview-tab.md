# Admin Console - Server / Overview Tab - Design

Date: 2026-08-09
Status: Draft (autonomous cycle; conductor review)
Backlog item: `docs/backlog/feature-2026-08-08-admin-server-overview-tab.md`

## Overview

The fourth tab on the shipped admin console registry. A read-only operational
overview: fleet-wide job counts, fleet-wide worker counts, the self-registration
policy flag, and a reachability pill.

This is the last of the five admin tabs that is buildable with today's backend
(Invites stays blocked on a `GET /v1/invites` that does not exist). It is
**frontend-only**: four endpoints already exist, none change.

It is also the smallest of the four built tabs by surface: no mutations, no
pagination, no dialogs, no forms. Its whole design problem is *what not to
render*, and that is where this spec spends its budget.

## Scope decision: ship the four real sources, omit the rest

The hi-fi's `AdminServer` (`design_handoff_relay_holo/hifi3-holo-pages.jsx:2280`)
is, by line count, ~90% a two-column table of 13 environment variables with
values and prose descriptions, plus a `● HEALTHY` pill. `HoloAdmin`'s header
(`:1952`) adds a `VERSION` / `BUILD` / `DB` / `UPTIME` strip.

**None of that is backed by an endpoint.** Verified in this cycle:

- `internal/api/health.go:5-7` - `handleHealth` writes exactly
  `{"status":"ok"}`. It performs no database check.
- `internal/api/config.go:7-11` - `handleConfig` writes exactly
  `{"allow_self_register": bool}`.
- No route anywhere returns a version, a build SHA, a Go version, a Postgres
  version, a process start time, or any `RELAY_*` value. Grepped
  `internal/api/server.go`'s full route table.

The backlog item's Notes recommend shipping only the four existing sources and
filing the build-info endpoint separately. **Adopted**, and the security half of
that recommendation is the stronger argument, not the effort half:

- The env table would need a new endpoint that returns effective server
  configuration. Relay's env includes `RELAY_DATABASE_URL` (the hi-fi cheerfully
  renders it as `postgres://relay:****@db-host:5432/relay`) and both rate-limit
  settings. A generic "dump the env, redact what looks secret" endpoint is a
  deny-list, and deny-lists for secrets fail open on the next variable someone
  adds. If we want this at all it must be an **allowlist**: a hand-written set of
  non-secret keys, admin-gated, with the value formatting decided per key.
- Rate-limit values (`RELAY_LOGIN_RATE_LIMIT`, `RELAY_REGISTER_RATE_LIMIT`) and
  the grace window are operational detail an attacker benefits from. That does
  not make them unshippable to an admin, but it does make the decision one that
  deserves its own spec with its own threat model, not a footnote in a frontend
  slice.

So: this tab renders four groups of numbers from four live endpoints and states,
in its own footnote, what it deliberately does not show. Proposed as a separate
backend item, `GET /v1/server/info` (see "Proposed follow-up" below).

There is no counter-evidence to the split. The one thing worth recording that
the item did not: `handleHealth` does not touch the database, so a `DB` fact and
a health pill sourced from `/v1/health` would have been two different kinds of
lie - one fabricated, one over-claiming. That shapes the pill's copy below.

## Design source of truth

Hi-fi Holo (`hifi3-holo-pages.jsx`) is authoritative; `reference/` is
structure-only. What survives from `AdminServer` after the omissions:

- The panel-level header: a title, a mono sub-line reading "Read-only", and a
  right-aligned health pill in `ok` color at 10px mono with 0.14em tracking.
- The two-column section grid with mono `Eyebrow`-style section captions
  (`AUTH`, `FLEET`, ... at 10px / 0.18em / bottom-ruled).
- The mono-value-in-accent treatment for the values themselves.

The section grid survives; the *rows inside it* become KPI numbers instead of
env pairs. This is the first admin surface where `KpiStat` genuinely applies -
the users/enrollments/reservations tabs are all tables.

## Backend surface - verified, no changes

| Source | Route | Auth | Response | Notes |
|---|---|---|---|---|
| Job counts | `GET /v1/jobs/stats` | `auth` (any authenticated user) | `{ running, queued, done_24h, failed_24h }` | `internal/api/jobs.go:148-160`, query `JobStatusCounts` (`internal/store/query/jobs.sql:282-292`) |
| Worker counts | `GET /v1/workers/stats` | `auth` | `{ online, stale, offline, disabled, total }` | `internal/api/workers.go:92-105`, query `WorkerStatusCounts` (`internal/store/query/workers.sql:204-215`) |
| Self-registration | `GET /v1/config` | **public** | `{ allow_self_register }` | `internal/api/config.go` |
| Reachability | `GET /v1/health` | **public** | `{ status: "ok" }` | `internal/api/health.go` |

Four facts about these payloads that the UI copy must respect, each read out of
the SQL rather than assumed:

1. **`failed_24h` counts cancelled jobs too.** The filter is
   `status IN ('failed','cancelled')`. Labelling that cell "Failed" alone is
   wrong; it reads as "FAILED OR CANCELLED · 24H".
2. **`done_24h` / `failed_24h` window on `jobs.updated_at`**, a finish-time
   proxy, not a real finish timestamp - `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`.
   It is accurate today (the only writer of `updated_at` is `UpdateJobStatus`,
   and no endpoint re-opens a terminal job - there is no `POST /v1/jobs/:id/retry`
   in the route table as of this cycle), and it silently degrades the day one
   lands. Fine to display; **the page must not be framed as an authoritative
   operational source of truth.** The footnote says so in one clause.
3. **`total` excludes revoked workers**, and so does every bucket. A worker that
   is both disabled and revoked counts in no bucket, exactly matching
   `GET /v1/workers`. The TOTAL cell carries this as its sub-line, so an admin
   never has to reconcile it against the decommissioned list by hand.
4. **`queued` means `jobs.status = 'pending'`**, a job-level count, not a task
   count. It is not "tasks waiting for a slot".

## Placement in the console

One entry appended to `ADMIN_TABS` in `web/src/admin/tabs.ts`:

```
{ slug: 'server', label: 'Server', Panel: ServerTab }
```

Last in the array, matching the hi-fi's tab order (`users, invites, enrollments,
reservations, server`; Invites remains absent). No routing change, no shell
change, no count badge (the hi-fi gives `server` a `null` count). `/admin/server`
therefore works the moment the entry lands, and `AdminRoute`'s `is_admin` guard
already covers it.

**The `AdminPage` header comment must be updated.** `web/src/admin/AdminPage.tsx:6-9`
currently reads "It belongs to the future Server/overview tab" about the
VERSION/BUILD/DB/UPTIME strip. That is now decided, not deferred: the strip is
omitted everywhere pending an allowlist endpoint. Leaving the comment as-is
leaves a stale forward reference in the file a future tab author reads first.
This is a comment-only edit; no behaviour changes in `AdminPage.tsx`.

## Admin gating, and an honest note about it

The route is admin-gated (`AdminRoute` + the nav filter, shipped 2026-08-08).
But **none of this tab's data is privileged**: `/v1/jobs/stats` and
`/v1/workers/stats` are `auth(...)`-only, and `/v1/config` and `/v1/health` are
public. Every number here is already visible to any authenticated user, and two
of them to anyone who can reach the port.

The gate is placement, not protection. Recording this matters for two reasons:
it means this slice widens no auth surface at all, and it means the proposed
`GET /v1/server/info` is a genuinely *different* kind of endpoint - the first one
whose payload actually needs `admin(...)` - so it must not be modelled on these
four.

## Architecture

New module `web/src/admin/server/`, mirroring the sibling tabs' shape.

### New files

- `web/src/admin/server/api.ts` - `getHealth()` -> `HealthResponse`
  (`{ status: string }`) and `getServerConfig()` -> `ConfigResponse` (the type
  already exists in `web/src/lib/types.ts:13-15`; import it, do not redeclare).
- `web/src/admin/server/useServerHealth.ts` - `useQuery({ queryKey: ['server-health'], refetchInterval: 30_000 })`.
- `web/src/admin/server/useServerConfig.ts` - `useQuery({ queryKey: ['server-config'], staleTime: Infinity })`,
  no polling: `AllowSelfRegister` is read from process env at startup and cannot
  change without a restart, which also restarts the SPA's server.
- `web/src/admin/server/HealthPill.tsx` - the pill, driven by a three-state
  input (see below).
- `web/src/admin/server/StatSection.tsx` - a section caption plus a KPI grid,
  with the degraded and loading bodies. Two consumers (jobs, fleet), which is
  exactly why it is a component rather than duplicated JSX - the degraded
  behaviour is the part that must be identical in both.
- `web/src/admin/server/ServerTab.tsx` - composition: header row, the two stat
  sections, the access panel, the footnote.

Plus a colocated `*.test.tsx` per file, per the module convention.

### Modified files

- `web/src/admin/tabs.ts` - the one registry entry.
- `web/src/admin/AdminPage.tsx` - the stale comment noted above.

### Reused, not rebuilt

- `useJobStats` (`web/src/jobs/useJobStats.ts`) and `useWorkerStats`
  (`web/src/workers/useWorkerStats.ts`), imported across module boundaries. This
  is established: `components/holo/StatusDot` imports `workers/liveness`, and
  `admin/reservations/useWorkerOptions` imports the workers API. Do **not** clone
  a second job-stats client into `admin/server/` - two clients for one endpoint
  is exactly the drift the single-pipeline habit exists to prevent.
  Both hooks take an interval argument; this tab passes `10_000` (below).
- Holo primitives: `KpiStat` (the KPI cells - first real use outside
  `WorkerDetailPage`), `Eyebrow` (section captions), `Panel` (the access card,
  which is titled prose + a chip, exactly `Panel`'s shape), `Chip`
  (ENABLED / DISABLED), `GlassPanel` (the degraded strip), `Button` (Retry).
- `apiFetch` for both new clients. `web/src/auth/RegisterScreen.tsx:21-25` calls
  `apiFetch<ConfigResponse>('/config')` inline in a `useEffect`; it is **not**
  refactored to use the new hook. That is unrelated churn on the sign-up path,
  and the two call sites want different semantics (one-shot with a fail-closed
  `false` fallback vs. a cached query with a visible error state). The
  duplication is one line and is noted here so it is not later mistaken for an
  oversight.

### Deliberately not built

- No `refetch`-all button. Each degraded section carries its own Retry; a
  page-level refresh control would duplicate four of them.
- No sparkline or history. `KpiStat` accepts `progress`, and nothing here is a
  ratio out of a meaningful max (`online / total` is tempting and misleading -
  a fleet that is 100% disabled would render a full bar).
- No count badge on the tab.

## Layout

```
┌ Server overview ─────────────────────────── ● HEALTHY ┐   (header row)
│ Read-only · live aggregates                            │
├────────────────────────────┬───────────────────────────┤
│ JOBS · GET /v1/jobs/stats  │ FLEET · GET /v1/workers/…  │  (2-col grid)
│  [RUNNING] [QUEUED]        │  [ONLINE] [STALE]          │
│  [DONE·24H] [FAILED/CAN…]  │  [OFFLINE] [DISABLED]      │
│                            │  [TOTAL]                   │
├────────────────────────────┴───────────────────────────┤
│ Panel "Access"  meta: GET /v1/config                    │
│   Self-registration   [DISABLED]                        │
│   POST /v1/auth/register requires an invite_token.       │
└─────────────────────────────────────────────────────────┘
▸ footnote
```

Two columns on wide viewports, stacking to one below the existing content
breakpoint used by the sibling tabs. KPI cells are a 2-up grid inside each
section (fleet's fifth cell, TOTAL, spans both columns).

**`KpiStat` is not nested inside `Panel`.** Both wrap `GlassPanel`, and glass
inside glass reads as a rendering bug in this palette. The stat sections use a
bare `Eyebrow` caption above a grid of `KpiStat`s - which is precisely the
`WorkerDetailPage` treatment (`WorkerDetailPage.tsx:100-110`), so the console
inherits a look the app already ships. `Panel` is used once, for the access
card, where the content is prose and a chip rather than a number.

### Cell contents

| Cell | Label | Value | Sub |
|---|---|---|---|
| Jobs 1 | `RUNNING` | `stats.running` | - |
| Jobs 2 | `QUEUED` | `stats.queued` | `status = pending` |
| Jobs 3 | `DONE · 24H` | `stats.done_24h` | - |
| Jobs 4 | `FAILED OR CANCELLED · 24H` | `stats.failed_24h` | - |
| Fleet 1 | `ONLINE` | `stats.online` | - |
| Fleet 2 | `STALE` | `stats.stale` | - |
| Fleet 3 | `OFFLINE` | `stats.offline` | - |
| Fleet 4 | `DISABLED` | `stats.disabled` | - |
| Fleet 5 | `TOTAL` | `stats.total` | `revoked workers excluded` |

Numbers render through `toLocaleString()`, matching the pagination footers.

## The health pill

Three states, derived from the `['server-health']` query only:

| Condition | Pill | Color |
|---|---|---|
| loading, no prior data | `● CHECKING` | muted |
| success and `status === 'ok'` | `● HEALTHY` | ok |
| success and `status !== 'ok'` | `● {status.toUpperCase()}` | warn |
| error | `● UNREACHABLE` | err |

The third row exists so the pill reports what the server said rather than
asserting health from a 200. `handleHealth` only ever writes `"ok"` today, so
that branch is unreachable in production - it is one `String()` and one ternary,
and it means a future non-`ok` status shows up instead of being silently
rendered as HEALTHY.

**The pill's copy must not over-claim.** `handleHealth` does not check the
database, so `HEALTHY` means "the HTTP listener answered", nothing more. The
footnote says exactly that, and it is testable copy: a scenario where
`/v1/health` returns ok while `/v1/jobs/stats` 500s is a *real* and important
one (Postgres down, server up) and this page renders it honestly - green pill,
both stat sections degraded. That combination is a required test.

## Degraded rendering - the core requirement

Four independent queries. **No query's failure may unmount another's data or the
page.** Per-section handling:

- **A stat section whose query errored with no data**: the KPI grid is replaced,
  in place, by a `GlassPanel` strip carrying the error message and a Retry
  button wired to that query's `refetch`. The other section, the access panel,
  the pill and the footnote are untouched.
- **A stat section that errored but has stale data** (a poll failed after a
  successful load): keep showing the numbers, and add a small mono line
  `stale · last update failed` beneath the caption. Blanking good numbers on a
  transient poll failure is worse than showing them with a staleness marker, and
  with a 10s poll a single dropped request is the common case.
- **Access panel query errored**: the panel body becomes the same error strip;
  the chip is not rendered. Never render a default - a fabricated `DISABLED`
  here would misreport the registration policy, which is a security-relevant
  claim.
- **Health query errored**: pill goes `UNREACHABLE`. No other effect.
- **Loading, first paint**: KPI cells render with value `—` and the section
  captions present, so the grid does not reflow when data lands. This differs
  from the sibling tabs' skeleton-row treatment because a fixed 4-cell grid has
  a known final size and a skeleton buys nothing.

This is the shape the last batch's retro called out as untested-by-default:
"the request fails" is a behaviour nobody writes down. Here it is written down,
and it is an acceptance criterion.

## Polling and load

| Query | Interval | Why |
|---|---|---|
| `['job-stats']` | 10s | Live-ish, but this is not a dashboard being watched during a render |
| `['worker-stats']` | 10s | Same |
| `['server-health']` | 30s | A reachability probe; 30s is enough to notice |
| `['server-config']` | none, `staleTime: Infinity` | Startup-only value; changing it requires a server restart |

Notes that matter:

- **Both stat keys are shared with existing pages.** `useJobStats` caches under
  `['job-stats']` (used by `JobsPage` / `JobActions`) and `useWorkerStats` under
  `['worker-stats']` (used by `WorkersPage`), both at a 3s default. Mounting this
  tab creates an *observer* on those existing cache entries, not new entries.
  Only one of these routes is mounted at a time, so the intervals never compete;
  and if they ever did, TanStack schedules per-observer, so the effect would be
  the faster of the two, not a doubling. No invalidation is needed anywhere -
  there are no mutations on this tab.
- **The 10s choice is strictly less load than the existing 3s dashboards**, so
  this tab introduces no new worst case for the two stats endpoints.
- **Scalability caveat, recorded not fixed:** `JobStatusCounts` and
  `WorkerStatusCounts` are unfiltered `COUNT(*) FILTER (...)` aggregates over the
  whole table, i.e. a sequential scan per call. That is fine at current volumes
  and is a pre-existing property of the shipped 3s dashboard polling, not
  something this tab introduces. If `jobs` grows to millions the fix is a
  partial index or a materialized counter, and it should be driven by a measured
  plan, not by this slice. Proposed for the backlog, not filed.

## Security and invariants

- **No new endpoint, no widened auth surface.** Four existing routes, read-only,
  called with the existing `apiFetch` (single fetch entry point, bearer attached
  in one place).
- **No secret is displayed.** This tab renders nine integers, one boolean and one
  status string. It is the only admin tab with no credential anywhere near it -
  in contrast to Enrollments' `TokenRevealDialog`.
- **No fabricated security claim.** The self-registration chip is rendered only
  from a successful `/v1/config`; on error the panel degrades rather than
  defaulting. A page that reported "self-registration: DISABLED" when it did not
  know would be worse than a page that reported nothing.
- **Backend invariants untouched** (epoch fence, single job-spec pipeline,
  bounded sender, identity-checked teardown, interior pointers, single JSON
  entry point) - no backend change.
- **Frontend generation-ordering invariant: not applicable and deliberately kept
  that way.** There is no `AbortController`, no SSE stream, no manual async
  lifecycle here; every request is a TanStack query whose cancellation TanStack
  owns. That is a reason to resist any future "live" upgrade of this tab that
  hand-rolls a stream.

## Testing

Vitest + MSW, colocated, using `server.use()` from `web/src/test/setup-helpers`
and the `renderHook` / `QueryClientProvider` harness the sibling modules use.

**Clients (`api.test.ts`)**
- `getHealth()` requests `/v1/health` and returns `{ status }`; throws `ApiError`
  on the error envelope.
- `getServerConfig()` requests `/v1/config` and returns `{ allow_self_register }`.

**Hooks**
- `useServerHealth` caches under `['server-health']`.
- `useServerConfig` caches under `['server-config']` and does not refetch on a
  second mount within the stale window. If this test asserts an absence over
  time, it must wait past a duration that would actually catch a refetch, not a
  token 50ms - the vacuous-timing-assertion lesson from the last batch.

**Panel - happy path (`ServerTab.test.tsx`)**
- All four handlers succeed: every one of the nine numbers renders with its
  value; the `FAILED OR CANCELLED · 24H` label is present (not "Failed");
  TOTAL's `revoked workers excluded` sub-line is present; the chip reads
  `DISABLED` for `allow_self_register: false` and `ENABLED` for `true`; the pill
  reads `HEALTHY`.
- Every rendered number is asserted against the exact mocked field, with
  distinct values per field, so a swapped-field regression (e.g. `stale`
  rendered under OFFLINE) fails.

**Panel - degraded (the acceptance criterion)**
- `/v1/jobs/stats` 500s while the other three succeed: the jobs section shows
  the error strip with a Retry, **and the fleet numbers, the chip and the pill
  are all still on screen**. Clicking Retry issues exactly one more
  `/v1/jobs/stats` request and, on success, replaces the strip with the grid.
- The mirror case: `/v1/workers/stats` 500s while the others succeed.
- `/v1/config` 500s: the access panel degrades and **no chip is rendered at all**
  (assert the absence of both `ENABLED` and `DISABLED`, so a fabricated default
  cannot pass).
- `/v1/health` 500s while both stats succeed: pill reads `UNREACHABLE` and all
  nine numbers still render.
- **The inverse, which is the realistic outage:** `/v1/health` returns ok while
  both stats endpoints 500. The pill still reads `HEALTHY` and both sections are
  degraded. This asserts the design intent - the pill is a listener probe, not a
  database probe - and it fails loudly if anyone later derives the pill from the
  stat queries.
- All four fail: the page still renders its header, both captions, the panel and
  the footnote. No blank screen, no thrown error boundary.
- Stale-with-error: after a successful load, a subsequent poll failure keeps the
  numbers on screen and adds the staleness line.

**Routing**
- `/admin/server` renders the tab and marks the Server pill active
  (`AdminTabs` / `tabs.ts` registry test, mirroring `AdminPage.test.tsx`).
- A non-admin at `/admin/server` is redirected (already covered by
  `AdminRoute.test.tsx`; extend its parameterization rather than duplicating).

**Contract verification**
- The TS types are checked field-for-field against Go: `JobStats` vs
  `jobStatsResponse`, `WorkerStats` vs `workerStatsResponse`, `HealthResponse` vs
  `handleHealth`'s `map[string]string`, `ConfigResponse` vs `handleConfig`'s
  `map[string]bool`.

## Acceptance criteria

1. `/admin/server` renders under the existing admin gate, added as exactly one
   entry in `ADMIN_TABS`. No router change, no shell change.
2. The page renders four job counts from `GET /v1/jobs/stats`, five worker
   counts from `GET /v1/workers/stats`, the self-registration flag from
   `GET /v1/config`, and a health pill from `GET /v1/health`.
3. Every displayed value traces to a named response field. No version, build,
   uptime, database, or environment-variable content appears anywhere.
4. The `failed_24h` cell is labelled as failed **or cancelled**; the TOTAL cell
   states that revoked workers are excluded; the footnote states that the 24h
   buckets are windowed on a finish-time proxy and that the health pill reflects
   HTTP reachability only, not database health.
5. Any one query failing degrades only its own region: the other regions keep
   rendering their data, and each degraded region offers a working Retry.
6. A stat query that fails *after* a successful load keeps its numbers visible
   and marks them stale.
7. `/v1/config` failing renders no self-registration chip in either state.
8. `AdminPage.tsx`'s stale comment about the deferred facts strip is corrected.
9. `npm test` and the production build are green; no file outside
   `web/src/admin/` changes; no backend change; `web/dist` is reverted before
   the change set is assembled.

## Proposed follow-up (not part of this slice)

`GET /v1/server/info` - admin-only, **allowlist-only** build and configuration
facts, so the hi-fi's header strip and env table can be built honestly. Sketch,
so the future spec starts from a position rather than a blank page:

- Response: `{ version, commit, go_version, started_at, db_version, config: [{ key, value, description }] }`.
- `config` is a **hand-written allowlist** of non-secret keys - grace window,
  telemetry window / stale-after, pool size, CORS origins, self-register, bind
  addresses - each with the value the process actually resolved.
  `RELAY_DATABASE_URL` and both rate-limit settings are absent by construction,
  not redacted. Never iterate `os.Environ()`.
- Version and commit come from `-ldflags` build vars, which relay does not set
  today; that is part of the item's work, not an assumption.
- Route registration must be `auth(admin(...))`, unlike the four sources this
  tab uses. Test the 403 explicitly.

Filed as `docs/backlog/feature-2026-08-09-server-info-allowlist-endpoint.md` for
human accept or reject.

Also proposed, **not filed**: an index or counter strategy for `JobStatusCounts`
if the jobs table grows large enough for the full-table aggregate to matter. No
measurement exists yet, and filing an unmeasured performance item invites a
speculative fix.

## Risks

- **Scope creep back into the env table.** The hi-fi is visually dominated by it,
  and the tab will look sparse next to the mock. Sparse and true beats dense and
  invented; the footnote is what closes the gap for a reader comparing the two.
- **Cross-module hook reuse.** Importing `useJobStats` from `jobs/` into
  `admin/server/` is right, but it means a future change to that hook's default
  interval silently changes this tab. The interval is therefore passed
  explicitly here, not defaulted.
- **`web/dist` is tracked but stale** - a frontend build dirties it; revert
  before assembling the change set.
