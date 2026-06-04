# Web Front End: Foundation + Auth Slice

**Date:** 2026-06-03
**Status:** Approved (design)
**Scope:** Foundation for the Relay web UI plus the first vertical slice (Auth + shell).

## Summary

Build the foundation for a Relay web front end and prove it end-to-end with the
smallest vertical slice: sign-in, registration, and the app shell. Later page
groups (Workers, Jobs, Schedules, Admin, Profile) each get their own
spec → plan → implement cycle.

The UI recreates the "Holo" design direction from
`design_handoff_relay_holo/` (a glassy dark theme with a configurable accent),
using the target codebase's own environment rather than copying the prototype
HTML. The prototype is a reference; the implementation fetches from the real
Relay REST API.

## Decisions (locked during brainstorming)

| Decision | Choice |
| --- | --- |
| Session scope | Foundation + Auth slice only |
| Framework | React + Vite + TypeScript + Tailwind |
| Production serving | Embedded in `relay-server` via `go:embed` (single binary) |
| Token storage | `localStorage` + `Authorization: Bearer` header |
| Auth slice contents | Login + Register + shell (no Profile/Sessions) |
| Density | `compact` (single default; no runtime toggle) |
| Accent | Baked default cyan (`#3DD0F7`), overridable via CSS variable |
| Fonts | Vendored (self-hosted), not Google Fonts CDN |
| Data fetching | Thin typed `fetch` client; TanStack Query deferred to Workers slice |

## Architecture

### Directory layout

A new `web/` directory at the repo root holds a self-contained Vite app:

```
web/
  index.html
  package.json
  vite.config.ts          # dev proxy: /v1 -> http://localhost:8080
  tailwind.config.ts
  tsconfig.json
  src/
    main.tsx              # React root, router, providers
    app/                  # router + route definitions
    shell/               # HoloShell, UserMenu (topbar + nav)
    auth/                # login + register screens, AuthProvider
    lib/                 # api client, token store, error types, types
    theme/               # Holo tokens (CSS vars), glass panel utilities
    components/          # shared primitives (Button, Input, Field, StatusDot)
  dist/                  # vite build output (embedded by Go)
  embed.go              # package webui: //go:embed all:dist
```

### Serving

- **Production:** `web/embed.go` exposes built `dist/` as an `embed.FS`.
  `relay-server` mounts a static handler on `/` in `internal/api/server.go`.
  The existing `/v1/...` routes are more specific and win; everything else
  serves a static file, falling back to `index.html` for client-side routes so
  deep links (`/jobs`, `/workers/foo`) resolve. Same origin as the API: no CORS.
- **Development:** Vite dev server on `:5173` proxies `/v1` to
  `localhost:8080`. Same-origin in dev too, so CORS configuration is untouched.
  `relay-server` remains the only listener on `:8080`; Vite only makes outbound
  calls to it.

### Build integration

New Makefile targets:

- `make web-install` -> `npm ci` in `web/`
- `make web-build` -> `npm run build` (vite -> `web/dist`)
- `make web-dev` -> `npm run dev`
- `make build` gains a dependency on `web-build` so `dist/` exists before
  `go build` embeds it.

**`go:embed` bootstrap:** `go:embed` fails to compile against an empty `dist/`.
Commit a minimal placeholder `web/dist/index.html` (a "run `make web-build`"
stub) so `go build ./...` and `make test` work for backend-only contributors;
real builds overwrite it. Add `web/dist` (except the placeholder) and
`web/node_modules` to `.gitignore`.

## Theming

- **Tokens as CSS variables** in `theme/tokens.css` on `:root`, using the fixed
  Holo tokens from the handoff (`--bg #050410`, `--fg #EDE9FE`, `--fg-mute`,
  `--fg-dim`, `--border`, `--ok #34D399`, `--warn #FBBF24`, `--err #FB7185`,
  `--accent #3DD0F7`, `--accent-b`).
- **Tailwind maps to the variables** (`tailwind.config.ts` extends `colors`,
  the type scale, radii `14`/`8`/`999`, and the two font families) so components
  use readable utilities (`text-fg-mute`, `border-border`, `rounded-card`).
- **Baked but variable-shaped:** the default cyan accent is baked, but because
  it is a CSS variable a future settings page can override `--accent` at runtime
  with no component changes. The HSV-picker math and tweaks panel are NOT
  ported (prototype scaffolding).
- **Glass panel:** the prototype's core container becomes one Tailwind component
  class (or small `<GlassPanel>` wrapper): linear-gradient background, 8px
  backdrop-blur, 14px radius, inset + drop shadow. The dropdown/popover variant
  uses the opaque `rgba(14,12,30,0.96)` background to avoid the see-through
  stacking gotcha.
- **Fonts:** vendored via `@fontsource/space-grotesk` and
  `@fontsource/jetbrains-mono` so they bundle into `dist/` and need no internet
  (air-gap friendly). This is a deliberate change from the prototype's CDN load.
- **Density:** `compact` as the single default (`pad: 14px 20px`, `gap: 10`,
  `rowPad: 5px 18px`, `rowFs: 11`, `nameFs: 12`). No runtime toggle.
- **Styling approach:** Tailwind utilities + CSS-variable tokens + a few
  component classes (chosen over porting inline-style objects verbatim or
  per-component CSS Modules).

## Data, auth state, and error handling

- **API client (`lib/api.ts`):** thin typed `fetch` wrapper. Prefixes `/v1`,
  sets JSON content type, attaches `Authorization: Bearer <token>` from the
  token store, parses the backend `{ "error": "..." }` envelope on non-2xx and
  throws a typed `ApiError { status, code, message }`. Surfaces `429`
  specially for the login retry hint. A global `401` interceptor clears the
  token and redirects to `/auth` (handles mid-session 30-day-token expiry).
- **Token store (`lib/token.ts`):** small module wrapping `localStorage` under a
  single key (`relay.token`). Single choke-point for a future httpOnly
  migration.
- **Auth state (`auth/AuthProvider.tsx`):** React context exposing
  `{ user, status, login(), register(), logout() }`. On mount, if a token
  exists, calls `GET /v1/users/me` to hydrate/validate; `login`/`register`
  persist the returned token and set the user; `logout` calls
  `DELETE /v1/auth/token`, clears the store, redirects to `/auth`.
- **Typed contracts (`lib/types.ts`):** hand-written TS types for the auth
  surface only: `LoginResponse { token, expires, user }`,
  `User { id, email, name, role }`, `ConfigResponse { allow_self_register }`.
  No codegen (no OpenAPI spec exists; four hand-written types are cheaper).
- **TanStack Query deferred:** introduced in the Workers slice where polling and
  caching earn it. The auth slice is one fetch plus two mutations; Query would
  be dead weight here.

### Error-handling map

| Status / code | UI treatment |
| --- | --- |
| `401` login | generic "invalid email or password" (never distinguish unknown user) |
| `409` register | "email already registered" + Sign in link |
| `400` `invite_*` | inline error under the invite-token field |
| `429` | "try again in Xs" hint near the submit button |

## Routing and shell

- **Router:** React Router (v7, declarative). Routes wired for this slice:

  ```
  /auth        -> HoloAuth (login)          public
  /register    -> register screen           public
  /jobs        -> placeholder "coming soon" protected   (default authed landing)
  /*           -> redirect to /jobs (authed) or /auth (anon)
  ```

  `/jobs` is a placeholder so login has a landing target and the shell has
  something to wrap. Real pages arrive in later slices.
- **`ProtectedRoute`:** while `AuthProvider` hydrates (`status === 'loading'`),
  render a minimal splash; if unauthenticated, `<Navigate to="/auth" replace>`;
  otherwise render shell + route.
- **`HoloShell`:** topbar with logo, nav links (Jobs/Workers/Schedules/Admin,
  rendered now pointing at placeholder routes), sync indicator, and user pill.
  Hidden on `/auth` and `/register`. `UserMenu` dropdown lists
  Profile/Password/Sessions/Logout; only **Logout** is wired in this slice, the
  rest route to placeholders. Dropdown uses the opaque background and closes on
  outside-click + Esc (`useRef` + `mousedown` + `keydown`).
- **Transitions:** instant, no page animation (per handoff).

## Auth screens and backend change

- **Backend: `GET /v1/config` (public, new).** Handler returns
  `{ "allow_self_register": <bool> }` from `s.AllowSelfRegister`. Wired in
  `internal/api/server.go` alongside `/v1/health` (no auth), with a handler
  test. This is the only backend change in the slice.
- **Login (`HoloAuth`)** - the centered picked variant: email + password, no
  token field, 30-day-token reassurance text, and a `/register` link whose copy
  branches on `/v1/config.allow_self_register`. Submits via
  `AuthProvider.login`; renders the 401/429 treatments.
- **Register** - one screen, two modes driven by `/v1/config`:
  - *invite-required* (default): display name, email, invite-token field
    (monospace, paste-friendly), password (>= 8). Handles `400 invite_*` inline
    and `409` with a Sign-in link.
  - *self-serve* (`allow_self_register=true`): same minus the invite field, with
    the "you'll be a non-admin user" helper text.
  - Client-side validation mirrors the server (email format, password >= 8);
    server remains source of truth.

### Out of scope (deferred to later specs)

Change-password, Profile/Sessions (needs a missing `GET /v1/auth/tokens`
endpoint), admin password reset, and all non-auth pages.

## Testing

- **Front end - Vitest + React Testing Library + MSW:**
  - MSW intercepts `fetch` at the network layer for realistic mocked `/v1`
    responses (success + each error envelope).
  - `lib/api` + `lib/token`: header attachment, error-envelope parsing,
    401 interceptor clears token, 429 surfacing.
  - `AuthProvider`: hydrate-from-token, login/register success + failure,
    logout clears state.
  - Login + Register screens: submit and assert each error-map treatment;
    register mode switches on mocked `/v1/config`.
  - `ProtectedRoute` / `UserMenu`: redirect when unauthenticated; dropdown
    closes on outside-click and Esc.
  - Run as `npm test` (`vitest run`).
- **Backend - Go:**
  - `handleConfig` handler test: returns `{allow_self_register}` reflecting the
    server flag; public (no auth).
  - Static-handler test: SPA fallback serves `index.html` for an unknown
    non-`/v1` path, and `/v1` routes still win (uses the committed placeholder
    `dist/`).
- **Not in scope:** end-to-end browser tests (Playwright/Cypress); a future
  decision once data pages exist.

## API contract gaps (for later slices)

The handoff's assumed API does not fully match the backend. These do not affect
the Auth slice but are recorded for the slices that need them:

| Design assumes | Backend reality | Affected slice |
| --- | --- | --- |
| `GET /v1/auth/tokens` (sessions list) | not present | Profile |
| worker `drain` / `cordon` | only `disable` / `enable` | Workers |
| `GET /v1/invites` (list) | only `POST /v1/invites` | Admin |
| schedule `pause`/`resume`/`run`, `/v1/schedules` | `/v1/scheduled-jobs`, `run-now`, PATCH enable/disable | Schedules |
| `GET /v1/jobs/:id/tasks/:n/logs?follow=1` | `GET /v1/tasks/{id}/logs` + separate `GET /v1/events` | Jobs |

Each will be specced as a paired backend addition where required.

## Later slices

Each is its own spec -> plan -> implement cycle, in suggested order:

1. Workers (introduces TanStack Query + ~3s polling)
2. Jobs + task-log (SSE log streaming)
3. Schedules (cron editing)
4. Admin (five tabs; token-on-create modals)
5. Profile (forms; needs the sessions-list backend addition)
