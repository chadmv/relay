# Handoff: Relay — Holo direction

A self-hosted distributed-job coordinator. This bundle covers the full web UI:
Sign-in, Jobs (list + detail + per-task log), Workers (list + detail), Schedules
(list + detail), Admin (Users / Invites / Agent enrollments / Reservations /
Server), and Profile (Identity / Password / Sessions).

The Holo direction is the **picked direction** — a glassy, dark UI with a
configurable accent color. Other visual variants in the project history are
superseded.

---

## About the design files

These files are **design references** created in HTML/React, **not production
code to copy directly**. They are a pixel-accurate prototype of the intended
look and behavior, rendered in-browser with Babel-transpiled JSX.

Your task is to **recreate these designs in the target codebase's existing
environment** — using its components, routing, state management, and API
client. If no codebase exists yet, choose the most appropriate framework
(React + Vite + Tailwind, Next.js, etc.) and implement there. The contained
HTML is a reference, not a starting point.

The prototype mocks data (sample workers, jobs, schedules, etc.) — your
implementation should fetch from the real Relay REST API (see "Backend
contract" below).

## Fidelity

**High-fidelity (hifi).** Final colors, typography, spacing, and interactions.
The accent color is parameterized — see "Design tokens". All other tokens
(neutrals, status colors, type scale, radii) are fixed.

---

## Files in this bundle

| File | Role |
| --- | --- |
| `Relay Holo.html` | Entry point — loads React, Babel, and the JSX modules. Defines `TWEAK_DEFAULTS`, the accent-color HSV controls, and renders `<HoloApp>` inside a `<DesignCanvas>` wrapper. **Drop the canvas/tweaks wrappers when porting** — `HoloApp` is the real root. |
| `hifi3-holo-pages.jsx` | **Main file.** All top-level pages and most components. ~2900 lines. |
| `hifi2-holo.jsx` | Shared Holo styling utilities (color tokens, palettes). Imported as `HoloPalettes`. |
| `hifi2-shared.jsx` | Cross-page primitives: `StatusDot`, `Spark`, `DAGSVG`, sample data (`JOB_DETAIL`, `TASKS`, `LOG`). |
| `hifi2-data.js` | Sample data for the Job Detail screen. |
| `design-canvas.jsx`, `tweaks-panel.jsx` | Prototype scaffolding — pan-zoom canvas and tweaks side-panel. **Not part of the product** — omit when porting. |
| `reference/Relay Wireframes.html`, `reference/screens/`, `reference/shell.js`, `reference/styles.css` | Original wireframe explorations + per-screen specs (`screens/auth.js`, `screens/admin.js`, etc.). These contain inline annotations describing the auth model, error codes, rate limits, and admin flow — useful as a second source of truth alongside the hi-fi mocks. |

To preview the design locally: open `Relay Holo.html` in a browser (no build
step). It loads React 18, ReactDOM, and Babel from unpkg.

---

## Component / page map

Top-level pages, all defined in `hifi3-holo-pages.jsx`:

| Function | Route | Purpose |
| --- | --- | --- |
| `HoloShell` | wrapper | Topbar (logo, nav, sync indicator, user pill + dropdown). Hidden on `auth`. |
| `UserMenu` | (in shell) | Profile/password/sessions/logout dropdown. Opaque background; closes on outside-click or Esc. |
| `HoloAuth` | `auth` | Email + password sign-in. Branching link copy by `allowSelfRegister` prop. |
| `HoloJobsList` | `jobs` | Three sub-views: `table` / `lanes` / `timeline` (sub-components `HoloLanes`, `HoloTimeline`). KPI counts, filter chips, "mine only" toggle. |
| `HoloJobDetail` | `job-detail` | Resizable left/right split: tasks + DAG on left, logs/spec/events pane on right. Click a task in left list to focus its log. |
| `HoloTaskLog` | `task-log` | Full-screen single-task log stream. |
| `HoloWorkers` | `workers` | Grid (cards) + Table view toggle. Disabled-worker treatment included. |
| `HoloWorkerDetail` | `worker-detail` | KPIs, current tasks, source workspaces, labels, reservations, agent token, utilization chart. **Enable/Disable toggle** in the header (added per `POST /v1/workers/:name/disable\|enable`). |
| `HoloSchedules` | `schedules` | Table with filter chips. **Edit** opens detail. |
| `HoloScheduleDetail` | `schedule-detail` | Editable trigger (cron + tz + overlap), read-only YAML job spec, next-fires preview, recent-runs table. |
| `HoloAdmin` | `admin` | Five tabs: Users, Invites, Agent enrolls, Reservations, Server. |
| `HoloProfile` | `profile` | Three tabs: Profile (identity), Password, Sessions. |

Routing is internal `useState` in `HoloApp` (see bottom of `hifi3-holo-pages.jsx`). In production this should map to your router (React Router, Next.js app router, TanStack Router, etc.) with these URL paths:

```
/auth                 → HoloAuth
/jobs                 → HoloJobsList
/jobs/:id             → HoloJobDetail
/jobs/:id/tasks/:n    → HoloTaskLog
/workers              → HoloWorkers
/workers/:name        → HoloWorkerDetail
/schedules            → HoloSchedules
/schedules/:name      → HoloScheduleDetail
/admin                → HoloAdmin (tab via ?tab= or /admin/:tab)
/profile              → HoloProfile (tab via /profile/:tab)
```

---

## Design tokens

### Colors

The accent is **HSV-parameterized** so studios can rebrand without touching code. Default values in `TWEAK_DEFAULTS` (in `Relay Holo.html`):

```
hue: 198°, sat: 80%, val: 97%  →  #3DD0F7 (cyan)
```

`makeTokens(palette, hueOffsets)` in `hifi2-holo.jsx` derives the full token set from the accent. Per-metric hue offsets (CPU/MEM/GPU-MEM) are also exposed so metric sparklines are visually distinguishable from the accent without inventing arbitrary colors.

Fixed colors (don't parameterize):

| Token | Hex | Use |
| --- | --- | --- |
| `bg` | `#050410` | Page background (with three layered radial gradients on top) |
| `fg` | `#EDE9FE` | Primary text |
| `fgMute` | ~`rgba(237,233,254,0.55)` | Secondary text |
| `fgDim` | ~`rgba(237,233,254,0.35)` | Tertiary text / footers |
| `border` | `rgba(255,255,255,0.08)` | Card/divider borders |
| `ok` | `#34D399` | Success / running-ok |
| `warn` | `#FBBF24` | Stale / paused / warning |
| `err` | `#FB7185` | Failed / destructive |
| `accent`, `accentB` | derived from HSV | Primary action / hover / link |

Status-color usage is consistent across pages: ok=green for success, accent for running/active, warn for stale or admin-paused, err for failed, fgMute for disabled (intentional off — distinct from offline-red).

### Typography

Loaded from Google Fonts in `Relay Holo.html`:
- **Space Grotesk** (300–700) — UI sans (headings, body)
- **JetBrains Mono** (300–700) — monospace (IDs, tokens, code, metric values, eyebrow labels)

Scale (from observed values):

| Use | Size | Weight | Letter-spacing |
| --- | --- | --- | --- |
| Page H1 | 32px | 400 | -0.02em |
| Section heading | 13–14px | 500–600 | 0.02em |
| Body | 12.5–13px | 400 | normal |
| Eyebrow labels | 10–11px mono | 400 | 0.16–0.22em |
| Table column headers | 10px mono | 400 | 0.16em |
| Numbers / IDs / tokens | 11–13px mono | 400 | 0.04em |

### Radii & shadows

- Cards / panels: **14px** radius
- Inputs / buttons (rectangular): **6–8px**
- Pills / chips: **999px**
- Card shadow: `inset 0 1px 0 rgba(255,255,255,0.08), 0 8px 32px rgba(0,0,0,0.4)`
- Dropdown shadow: `0 16px 48px rgba(0,0,0,0.5), inset 0 1px 0 rgba(255,255,255,0.08)` + opaque dark bg (`rgba(14,12,30,0.96)`)

### Spacing & density

Two density modes (`compact` and `comfortable`) are exposed as a tweak. Implementation uses a `D` props bag threaded into every page:

```js
comfortable: { pad: '22px 26px', gap: 16, rowPad: '10px 18px', rowFs: 11.5, nameFs: 13 }
compact:     { pad: '14px 20px', gap: 10, rowPad: '5px 18px',  rowFs: 11,   nameFs: 12 }
```

In production, you can either keep this as a user preference or just pick one default.

### Glass panel

The fundamental container is a "glass panel" — `glassPanel(C)` returns:

```js
{
  background: 'linear-gradient(180deg, rgba(255,255,255,0.06), rgba(255,255,255,0.02))',
  border: '1px solid rgba(255,255,255,0.08)',
  backdropFilter: 'blur(8px)',
  borderRadius: 14,
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,0.08), 0 8px 32px rgba(0,0,0,0.4)',
}
```

**Stacking gotcha** — dropdowns/popovers on this surface need an opaque background (`rgba(14,12,30,0.96)`) because the 4% white over the page bg is essentially see-through. See `UserMenu`.

---

## Backend contract

The wireframe annotations (`reference/screens/*.js`) document the API model from `chadmv/relay@master`. Key endpoints the UI talks to:

### Auth

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/v1/auth/login` | email + password → `{ token, expires, user }` |
| `POST` | `/v1/auth/register` | email + password + name + `invite_token` (optional iff `RELAY_ALLOW_SELF_REGISTER=true`) |
| `DELETE` | `/v1/auth/token` | logout this token |
| `DELETE` | `/v1/auth/tokens` | logout all of user's tokens |
| `GET` | `/v1/auth/tokens` | list active sessions (for Profile → Sessions) |
| `PUT` | `/v1/users/me/password` | current + new — revokes other sessions |
| `POST` | `/v1/users/password-reset` | admin issues a temp password |

- Tokens are 30-day bearer with a sliding window
- 401 is generic (`invalid_credentials`) — never distinguish unknown user vs wrong password
- Rate limits: login 10/min/IP (`RELAY_LOGIN_RATE_LIMIT`), register 5/min/IP
- The CLI writes credentials to `~/.relay/config.json`; the web UI uses httpOnly cookie / localStorage. **No "token" input on the login screen** — see `HoloAuth`.

### Jobs

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/v1/jobs` | cursor-paginated, query: `status`, `mine`, `since`, `cursor`, `page_size` |
| `POST` | `/v1/jobs` | submit (body = job spec) |
| `GET` | `/v1/jobs/:id` | full job + task summary |
| `DELETE` | `/v1/jobs/:id` | cancel (SIGTERM to in-flight tasks) |
| `POST` | `/v1/jobs/:id/retry` | `?task=failed\|all` |
| `GET` | `/v1/jobs/:id/tasks/:n/logs` | `?follow=1` for SSE stream — used by `HoloTaskLog` |

### Workers

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/v1/workers` | online + stale within 30s |
| `GET` | `/v1/workers/:name` | full detail + recent task history |
| `PATCH` | `/v1/workers/:name` | `{ labels: { +k:v, -k:null } }` |
| `POST` | `/v1/workers/:name/drain` | finish in-flight, then idle |
| `POST` | `/v1/workers/:name/cordon` | immediately stop accepting tasks |
| `POST` | `/v1/workers/:name/disable` | scheduler ignores (heartbeat continues). **New endpoint** — drives the Enable/Disable button on `HoloWorkerDetail`. |
| `POST` | `/v1/workers/:name/enable` | resume scheduling |
| `DELETE` | `/v1/workers/:name` | revokes worker token |

Worker status states surfaced in the UI: `online · busy`, `online · idle`, `online · stale`, `disabled`, `offline`. Use color: ok / fg / warn / fgMute / err respectively.

### Schedules

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/v1/schedules` | |
| `POST` | `/v1/schedules` | `{ name, cron, tz, overlap, spec, enabled }` |
| `PATCH` | `/v1/schedules/:name` | inline-edit cron / tz / overlap / spec |
| `DELETE` | `/v1/schedules/:name` | |
| `POST` | `/v1/schedules/:name/pause` | |
| `POST` | `/v1/schedules/:name/resume` | |
| `POST` | `/v1/schedules/:name/run` | one-shot, returns spawned job id |

Cron evaluated in coordinator's `TZ` env. Overlap modes: `skip` (default), `allow`, `queue`. The recent-runs table on `HoloScheduleDetail` queries `GET /v1/jobs?source=schedule&name={name}`.

### Admin

| Method | Path | Notes |
| --- | --- | --- |
| `GET` / `POST` | `/v1/users` | list + create. `?include_archived=true` |
| `PATCH` | `/v1/users/:email` | rename / role |
| `POST` | `/v1/users/:email/archive` / `/unarchive` | revokes all tokens on archive |
| `POST` | `/v1/invites` | one-time token, default 72h max 720h. **Returned in clear-text only at creation.** |
| `GET` | `/v1/invites` | states: active / expiring / expired / redeemed |
| `POST` | `/v1/agent-enrollments` | for worker bootstrapping, default 24h max 7d |
| `GET` | `/v1/agent-enrollments` | active only |
| `GET` / `POST` | `/v1/reservations` | admin-only |

---

## Interaction notes

- **Routing transitions** are instant (no slide/fade) — the prototype simply swaps the route state. Don't add page-level animation in the port.
- **Resizable splits** — `HoloJobDetail` has a draggable left/right divider with `useRef` + `useLayoutEffect` to initialize width. Persist the user's chosen width if you want (the prototype doesn't).
- **Live data** — Workers grid shows fake telemetry sparklines (`fakeTelemetry` in `hifi3-holo-pages.jsx`) seeded by worker name. In production, poll `GET /v1/workers` every ~3s, or subscribe to SSE/WebSocket if backend supports it.
- **Log tailing** — `HoloTaskLog`'s "Follow tail" button maps to `GET /v1/jobs/:id/tasks/:n/logs?follow=1` (SSE stream).
- **User-menu dropdown** — must close on outside-click and Escape (`UserMenu` shows the pattern with `useRef` + `mousedown` + `keydown` listeners).
- **Tweaks panel** — the floating tweaks UI is prototype scaffolding only. Don't port it.

---

## What to omit when porting

- `<DesignCanvas>` / `<DCSection>` / `<DCArtboard>` wrappers in `Relay Holo.html`
- `<TweaksPanel>` and all `Tweak*` controls
- HSV → hex conversion logic (`hsvToHex`, `hsvToRgba`) — just bake one accent color, or expose theme as a real settings page
- All `hifi-v1-*`, `hifi-v2-*`, `hifi-v3-*`, `hifi-v4-*` files (not in this bundle — they were earlier exploration variants)
- `fakeTelemetry` and other deterministic-mock helpers — wire real API data instead

---

## Suggested implementation order

1. **Auth + shell** — sign-in, topbar, user-menu, route scaffolding. Get a real bearer token flowing.
2. **Workers list + detail** — straightforward CRUD-ish list. Get telemetry polling working here; reuse for jobs/workers everywhere.
3. **Jobs list + detail + task log** — most complex page; SSE log streaming is the new primitive.
4. **Schedules list + detail** — cron editing UI.
5. **Admin** — five tabs. Invites/enrollments token-on-create modal is the tricky bit.
6. **Profile** — small surface; mostly forms.
