import { apiFetch } from '../lib/api'
import type { User } from '../lib/types'

// Self-service account endpoints. All three act on the identity in the bearer
// token, resolved server-side by BearerAuth (internal/api/middleware.go:36-43) -
// there is no id in any path or body, so there is nothing for a client to tamper
// with and no horizontal-privilege vector to reason about. All three are auth(...)
// and NONE is AdminOnly (internal/api/server.go:97-100, :153).
//
// Every path here is a LITERAL. There is no interpolation on this surface, so do
// not add encodeURIComponent to any of them.

// PATCH /v1/users/me -> 200 with the full userResponse.
//
// ONE field. updateUserRequest is exactly { Name string } (internal/api/users.go:49-51)
// and it is NOT a pointer, so there is no omitted-versus-empty distinction to make.
// Email is immutable through the ENTIRE store layer - internal/store/query/users.sql
// contains no `UPDATE users SET email` anywhere - so there is deliberately no email
// argument here to be tempted by.
//
// The server trims and stores the trimmed value (users.go:61, :422), and 400s
// `name is required` when the trim is empty (:63). The 200 body is the same
// userResponse struct GET /v1/users/me returns (:429 and :410 both call
// toUserResponse), so the caller hands it straight to AuthProvider.applyUser
// rather than refetching.
//
// No optimistic concurrency: UpdateUserName is a bare WHERE id = $1
// (internal/store/query/users.sql:50-52), so concurrent edits are last-writer-wins.
// Unlike the admin arm (users.go:449-452) there is no pgx.ErrNoRows branch - the
// row is the caller's own and cannot vanish while their token authenticates.
export function updateMe(name: string): Promise<User> {
  return apiFetch<User>('/users/me', { method: 'PATCH', json: { name } })
}

// PUT /v1/users/me/password -> 204, no body (internal/api/auth.go:338); apiFetch
// returns undefined for a 204 (lib/api.ts:57).
//
// EXACTLY two keys. The form that calls this has three fields; the API takes two.
//
// Failure modes, in the handler's own order (auth.go:281-338):
//   400 `password must be at least 8 characters` - the ONLY server-side rule on
//       the new password (:284-287). There is NO complexity policy anywhere.
//   403 `current password is incorrect` (:298-301) - a 403, NOT a 401, so it does
//       not fire the onUnauthorized notifier in lib/api.ts and must never sign
//       anyone out.
//   500 `failed to hash password` (:303-307) - where a password over 72 BYTES
//       lands, because bcrypt rejects it. Guarded client-side by the caller.
// There is no check that the new password differs from the old one, and no
// constraint at all on current_password beyond matching.
//
// SESSION EFFECT: on success the server revokes every OTHER token for the user
// and KEEPS this one (DeleteOtherTokensForUser, auth.go:325-328 ->
// internal/store/query/tokens.sql:28-29 `AND id <> $2`). The caller stays signed
// in. The whole thing is one transaction (auth.go:309-336), so there is no window
// where the password changed but stale sessions survived. Contrast
// signOutEverywhere below, which is the exact opposite.
export function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  return apiFetch<void>('/users/me/password', {
    method: 'PUT',
    json: { current_password: currentPassword, new_password: newPassword },
  })
}

// DELETE /v1/auth/tokens -> 204, no body, no parameters, idempotent
// (handleLogoutAll, internal/api/auth.go:350-357).
//
// IT DESTROYS THE CALLER'S OWN TOKEN. DeleteTokensForUser is
// `DELETE FROM api_tokens WHERE user_id = $1` (internal/store/query/tokens.sql:40-41)
// with NO `id <> $2`. The hi-fi calls this "Sign out everywhere ELSE"
// (design_handoff_relay_holo/hifi3-holo-pages.jsx:2796, :3049) and there is no such
// endpoint: the all-but-current query exists (tokens.sql:28-29) but only the
// password path routes to it. Labelling this control "else" would understate its
// blast radius, which is a defect and not a copy nit.
//
// A 204 fires NO listener - onUnauthorized is 401-only (the onUnauthorized
// notifier in lib/api.ts) - so after this resolves the SPA still holds a token
// in localStorage against a credential the server has already destroyed, and
// still believes it is authenticated. The caller MUST tear its own session
// down immediately; see SessionsTab and AuthProvider.clearSession.
//
// Note the PLURAL path. DELETE /v1/auth/token (singular) is a different endpoint
// that revokes only the caller's current token (auth.go:341-348).
export function signOutEverywhere(): Promise<void> {
  return apiFetch<void>('/auth/tokens', { method: 'DELETE' })
}
