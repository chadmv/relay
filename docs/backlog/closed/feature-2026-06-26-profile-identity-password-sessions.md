---
title: Profile pages (Identity / Password / Sessions tabs)
type: feature
status: closed
created: 2026-06-26
priority: medium
source: ROADMAP web-frontend deep review against design_handoff_relay_holo (2026-06-26)
closed: 2026-08-12
resolution: fixed
---

# Profile pages (Identity / Password / Sessions tabs)

## Summary
The `/profile/*` route is a `JobsPlaceholder` stub even though the UserMenu already links to
`/profile`, `/profile/password`, and `/profile/sessions` (all currently dead). The Holo design
(`HoloProfile`) specifies three tabs - Identity, Password, and Sessions - a small, mostly
form-based surface.

## Context
Surfaced by the 2026-06-26 `/roadmap web-frontend deep` review against `design_handoff_relay_holo/`.
Router stub at `web/src/app/router.tsx:25`; dead UserMenu links at `web/src/shell/UserMenu.tsx`.
Per-screen spec: `design_handoff_relay_holo/reference/screens/auth.js`.

## Proposal
- **Identity** - display name / email; rename via `PATCH /v1/users/me`. Backend exists.
- **Password** - current + new; `PUT /v1/users/me/password` (revokes other sessions). Backend exists.
- **Sessions** - list active bearer tokens with "sign out everywhere". The sign-out-everywhere
  action (`DELETE /v1/auth/tokens`) exists, but **listing sessions needs a new
  `GET /v1/auth/tokens` endpoint** that does not exist yet.

## Acceptance / Done When
- `/profile/:tab` renders Identity / Password / Sessions.
- Identity rename and password change are wired to the existing endpoints with validation/error states.
- Sessions tab lists active sessions (once `GET /v1/auth/tokens` lands) and supports sign-out-everywhere.
- The UserMenu links resolve to real tabs instead of the placeholder.

## Related
- Design: `design_handoff_relay_holo/reference/screens/auth.js`, `hifi3-holo-pages.jsx` (`HoloProfile`)
- Pairs with [[feature-2026-06-05-usermenu-panel-menu-roles]] (the menu that links here)
- Source: `web/src/app/router.tsx:25`, `web/src/shell/UserMenu.tsx`, `internal/api/{users,auth}.go`
- Filed out of the same work: [[bug-2026-08-13-cross-generation-401-clears-a-new-session]],
  [[idea-2026-08-13-field-error-wiring-audit]]

## Notes
Backend gap: `GET /v1/auth/tokens` (list active sessions) is missing; the other two tabs are
frontend-only against existing endpoints.

## Resolution
Implemented per `docs/superpowers/specs/2026-08-12-profile-pages.md` and
`docs/superpowers/plans/2026-08-12-profile-pages.md`, as `2026-08-12-profile-pages`.
`/profile` redirects to `/profile/identity` and `/profile/:tab` renders Identity, Password
and Sessions behind the three previously-dead `UserMenu` links, replacing the
`JobsPlaceholder` stub, which is deleted. `UserMenu` itself was not modified. 100%
frontend: zero Go files, zero SQL files, no migration. Tests 890 -> 959.

**Identity** renames through `PATCH /v1/users/me` with a changed-fields-only save that
issues zero requests when the trimmed name is unchanged, and pushes the authoritative 200
body into `AuthProvider` through a new `applyUser` rather than adding a second `['me']`
query. Email ships disabled with a hint naming the real remedy: it is immutable through
the entire store layer, not merely unimplemented.

**Password** changes through `PUT /v1/users/me/password` with three client-side guards
(confirm match, min-8, and a 72-byte `TextEncoder` cap that keeps a password-manager
password from landing as an opaque bcrypt 500). It correctly stays signed in on both a 204
and a 403 wrong-password response - a 403 is not a 401 and must not trip the session
teardown, which has its own test with a 401 control alongside it.

**Sessions** ships the `Sign out everywhere` action with no list. Two corrections to this
item and to the hi-fi drove that surface:

1. **The hi-fi's "Sign out everywhere *else*" label is wrong.** `DELETE /v1/auth/tokens` is
   `DeleteTokensForUser`, a bare `DELETE FROM api_tokens WHERE user_id = $1`
   (`internal/store/query/tokens.sql:25-26`) with no `id <> $2`, so it destroys the
   caller's own token too. The shipped label and confirm copy state that blast radius, and
   the security lane verified the copy against the SQL. Contrast the password path, which
   calls `DeleteOtherTokensForUser` and genuinely spares the caller.
2. **This item's Acceptance ("lists active sessions") and its Notes ("one small endpoint")
   were both overturned.** `GET /v1/auth/tokens` still does not exist, and `api_tokens` has
   exactly five columns - `id, user_id, token_hash, created_at, expires_at` - so
   `last_used_at` and any agent/IP/location attribution are a **migration** plus an
   endpoint. The tab ships action-only with an on-page footnote naming the gap, following
   the omit-what-the-backend-cannot-supply precedent from the Agent Enrollments tab. The
   new part of the reasoning, worth carrying: that rule applies at the granularity of the
   **control**, not the tab - here the action works and only the read is missing, so
   dropping the whole tab would have deleted a working security control.

On a 204 the SPA tears its own session down through a new `AuthProvider.clearSession`
rather than waiting to be 401ed, since a 204 fires no listener. `logout()` is deliberately
not reused there (it would fire `DELETE /v1/auth/token` against a token that no longer
exists) and there is no `invalidateQueries` anywhere on the surface (it would refetch every
mounted query against a destroyed credential before anything unmounts). What actually makes
the teardown safe was measured in Phase 4 rather than assumed: `clearToken()` runs first so
any escaped request carries no Authorization header, and `setStatus('anonymous')` flips
`ProtectedRoute` to a redirect that unmounts every active observer.
`queryClient.clear()` does **not** stop scheduled refetches, contrary to what an earlier
version of that comment claimed.

Phase 4 confirmed and fixed six findings, none high: a settled save clobbering a newer
mid-flight edit, a stale banner surviving a no-op save, client guard failures rendering on
the wrong control and never being announced, a false comment about the teardown mechanism,
the plaintext password surviving in the settled mutation's closure (fixed with `gcTime: 0`
plus a synchronous `reset()`, whose own first version silently broke the success banner),
and four small prose/reachability items. The accessibility fix reached a shared primitive:
`web/src/components/Field.tsx` now wires `role="alert"` and `aria-describedby`, which it
had never done across ten consumers.

The underlying enabler (`feature-2026-06-26-web-enabler-backend-endpoints`) remains open and
is the right place to pick the session list back up; the spec proposes amending its
`GET /v1/auth/tokens` bullet, which currently lists `last_used_at` as if it were queryable.
Full write-up: `docs/retros/2026-08-12-profile-pages.md`.
