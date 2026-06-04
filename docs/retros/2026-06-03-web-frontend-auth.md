# Session Retro: 2026-06-03 — Web Front End Foundation + Auth Slice

## What Was Built

The first slice of a brand-new web front end for Relay: a React + Vite + TypeScript + Tailwind v4 SPA under `web/`, embedded into `relay-server` via `go:embed` and served same-origin with SPA fallback (Vite proxy in dev, one binary in prod). The slice delivers a working auth path end-to-end against the real REST API:

- Foundation: typed `fetch` client with an `ApiError` envelope parser and a global 401 interceptor; a `localStorage` bearer-token store; `AuthProvider` context (hydrate-from-token, login, register, logout); CSS-variable Holo theme tokens (compact density, baked cyan accent, vendored fonts); React Router with `ProtectedRoute`/`PublicOnlyRoute` guards and the `HoloShell` topbar + `UserMenu`.
- Auth screens: centered login (generic 401 / 429 hint) and a two-mode register (invite-required vs self-serve) driven by a new public `GET /v1/config` endpoint.
- Backend: the single `GET /v1/config` addition plus a `webui` embed package and a `Server.StaticHandler` mount point; Makefile `web-install`/`web-build`/`web-dev` targets with `build` depending on `web-build`.

Delivered through the full superpowers flow: brainstorming → spec → plan → subagent-driven development (12 tasks, fresh implementer + two-stage spec/quality review each), then a final whole-branch review. 24 front-end tests (Vitest + RTL + MSW) and Go handler/embed tests, all green.

## Key Decisions

- **React + Vite + TS + Tailwind v4 (CSS-first `@theme`)** over Next.js — closest to the design prototype and trivial to embed as static files; no Node runtime in production.
- **Embed via `go:embed`** (single binary, same-origin, no CORS in prod) over a separate static host.
- **`localStorage` + `Authorization` header** over httpOnly cookies — zero backend auth changes for the slice; cookie hardening deferred to its own backend spec.
- **`/users/me` as the single source of user identity** — after the contract mismatch (below), login/register persist only the token and hydrate the user via `/users/me`.
- **Scope discipline:** Foundation + Auth only; TanStack Query deferred to the Workers slice; Profile/Sessions/change-password out of scope; each later page group gets its own spec → plan → implement cycle.

## Problems Encountered

- **Auth response contract was invented, not verified.** The spec/plan assumed login/register return `{token, expires, user}` and `/users/me` returns `role`; the real backend returns `{token, expires_at}` (no user) and `is_admin`. Every per-task review passed because the MSW mocks replicated the *invented* shape. The final whole-branch review (opus) caught it; fixed by hydrating via `/users/me` and correcting the types.
- **No post-login redirect (user-reported "nothing happens").** Login succeeded — token stored, state authenticated — but the `/auth` route rendered unconditionally and the screen never navigated, stranding the user on the form. Isolated screen tests missed it; there was no app-level navigation test. Fixed with a `PublicOnlyRoute` guard plus an app-level login→jobs test that failed before the fix.
- **`go:embed` bootstrap friction.** `go:embed all:dist` won't compile against an empty dir, so a placeholder `dist/index.html` is committed — but a nested-directory `.gitignore` negation doesn't un-ignore it, requiring `git add -f`.
- **TS project references.** `tsc -b` with a referenced `tsconfig.node.json` requires `composite: true` + `emitDeclarationOnly: true` (not `noEmit`), which is incompatible with the reviewer's first instinct.
- **Register 409 test ambiguity.** The plan rendered two "Sign in" links while the test queried a single one; resolved by dropping the redundant inline link and keeping the footer link, preserving consistent wording.

## Known Limitations

- See [`bug-2026-06-03-usermenu-aria-attributes`](../backlog/bug-2026-06-03-usermenu-aria-attributes.md) — UserMenu toggle button lacks aria-expanded / aria-haspopup
- See [`bug-2026-06-03-gofmt-import-order`](../backlog/bug-2026-06-03-gofmt-import-order.md) — Pre-existing gofmt import-order issues in server.go and main.go
- httpOnly-cookie token hardening was deliberately deferred.
- Only the placeholder `web/dist/index.html` is committed; real builds are ephemeral, so a production binary must run `make web-build` first or it embeds the stub.

## Open Questions

- See [`idea-2026-06-03-login-return-user-object`](../backlog/idea-2026-06-03-login-return-user-object.md) — Return the user object from login/register to skip the /users/me round-trip
- See [`idea-2026-06-03-web-e2e-harness`](../backlog/idea-2026-06-03-web-e2e-harness.md) — Add an end-to-end (Playwright) test harness for the web UI

## Improvement Goals

- When a plan specifies an API contract, verify request/response shapes against the actual server response builders (`internal/api/*.go`) before writing client types or test mocks. Mocks that mirror an invented shape give false green.
- For SPA auth flows, always include at least one app-level (router-mounted) test asserting navigation after login/logout — not just isolated per-screen tests.

## Files Most Touched

- `web/src/auth/AuthProvider.tsx` (+87) — auth context; hydrate/login/register/logout, the `/users/me` hydration fix.
- `web/src/auth/RegisterScreen.tsx` (+125) — two-mode register driven by `/v1/config`.
- `web/src/auth/LoginScreen.tsx` (+81) — centered login with 401/429 handling.
- `web/src/lib/api.ts` (+57) — typed fetch client, `ApiError`, 401 interceptor.
- `web/src/shell/HoloShell.tsx` / `UserMenu.tsx` (+109) — app chrome and dropdown.
- `web/src/app/router.tsx` + `ProtectedRoute.tsx` + `PublicOnlyRoute.tsx` — routing and the two auth guards (PublicOnlyRoute is the redirect fix).
- `web/src/lib/types.ts` (+15) — auth contract types, corrected to `is_admin`/`expires_at`.
- `web/embed.go` (+45) — `webui` package: embeds `dist`, serves SPA with `/v1` 404 guard.
- `internal/api/config.go` + `config_test.go` — the public `GET /v1/config` endpoint.
- `internal/api/server.go` + `static_test.go` — `StaticHandler` field and mount.
- `web/vite.config.ts` / `tokens.css` / `package.json` — scaffold, theme tokens, dev proxy.
- `Makefile` — `web-install`/`web-build`/`web-dev`; `build` embeds the UI.

## Commit Range

62b152d853e0688b3e58a1ef1dd9771a04491308..aa470b97979bc8e6b61bd2edb82cdf148991530b
