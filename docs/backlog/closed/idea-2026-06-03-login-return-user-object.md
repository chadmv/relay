---
title: Return the user object from login/register to skip the /users/me round-trip
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
created: 2026-06-03
priority: low
source: web front end auth slice
---

# Return the user object from login/register to skip the /users/me round-trip

## Summary
`POST /v1/auth/login` and `/v1/auth/register` currently return only `{token, expires_at}`, so the web client makes a second `GET /v1/users/me` call after authenticating to populate the current user. Including the user object in the auth response (a small backend change) would let the client set the user in one round-trip.

## Proposal
Add the user payload (the same shape as `GET /v1/users/me`: `id`, `email`, `name`, `is_admin`) to the login and register response bodies. The web `AuthProvider.applyAuth` could then set the user directly and drop the extra fetch. Weigh against keeping `/users/me` as the single source of identity (current design) and any non-web clients that don't need the object.

**Note added 2026-08-13:** `User` now also carries `created_at` (`web/src/lib/types.ts`), which the
profile header renders, so the payload added here must include it or the header breaks on a
fresh login. `AuthProvider` also gained an `applyUser(u: User)` method for exactly this shape of
"here is the authoritative row, no refetch needed" - `applyAuth` can reuse it rather than calling
`setUser` a second way.

## Related
- `internal/api/auth.go` (login/register response builders)
- `web/src/auth/AuthProvider.tsx`
- Retro: `docs/retros/2026-06-03-web-frontend-auth.md`
- Touches the same `applyAuth` path, and must not be done blind to it:
  [[bug-2026-08-13-cross-generation-401-clears-a-new-session]] - a stale 401 landing right after
  this path stores a new token is what clears it

## Resolution
Shipped in lane MF of the 2026-09-02 web-frontend batch: applyAuth applies the login body's user behind a shape guard (non-empty id, created_at and email; boolean is_admin) and falls back to /v1/users/me when the body lacks it or it is malformed, so an older server still works; /users/me stays the identity source on reload. The discriminating test counts zero identity requests on a good body. The item's five-key user shape was refuted: the wire carries archived_at.
