---
title: Return the user object from login/register to skip the /users/me round-trip
type: idea
status: open
created: 2026-06-03
priority: low
source: web front end auth slice
---

# Return the user object from login/register to skip the /users/me round-trip

## Summary
`POST /v1/auth/login` and `/v1/auth/register` currently return only `{token, expires_at}`, so the web client makes a second `GET /v1/users/me` call after authenticating to populate the current user. Including the user object in the auth response (a small backend change) would let the client set the user in one round-trip.

## Proposal
Add the user payload (the same shape as `GET /v1/users/me`: `id`, `email`, `name`, `is_admin`) to the login and register response bodies. The web `AuthProvider.applyAuth` could then set the user directly and drop the extra fetch. Weigh against keeping `/users/me` as the single source of identity (current design) and any non-web clients that don't need the object.

## Related
- `internal/api/auth.go` (login/register response builders)
- `web/src/auth/AuthProvider.tsx`
- Retro: `docs/retros/2026-06-03-web-frontend-auth.md`
