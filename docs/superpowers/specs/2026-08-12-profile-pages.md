# Profile Pages (Identity / Password / Sessions) - Design

Date: 2026-08-12
Status: Draft (autonomous cycle; conductor review)

## Overview

`/profile/*` renders `JobsPlaceholder` (`web/src/app/router.tsx:41`) while `UserMenu`
already links to `/profile`, `/profile/password` and `/profile/sessions`
(`web/src/shell/UserMenu.tsx:60,64,70`) - three dead links in the shipped app. This
slice builds the profile surface behind them.

Backlog item: `docs/backlog/feature-2026-06-26-profile-identity-password-sessions.md`
(closed by this slice). Design source of truth: the hi-fi `HoloProfile`
(`design_handoff_relay_holo/hifi3-holo-pages.jsx:2812-3130`). The `reference/`
sketch (`reference/screens/auth.js`) is structure-only.

Frontend-only. No backend change, no new endpoint, no store query, no migration.

Written in autonomous gate mode: every design question below was decided here and
carries a one-line rationale in the Decisions section rather than being asked.

## Where the backlog item and the hi-fi were wrong or incomplete

The item's Proposal is three bullets. Verified against the code, **the single most
consequential claim in the whole item - "revokes other sessions" versus "revokes all
sessions" - is stated correctly for one endpoint and incorrectly by the hi-fi for the
other**, and the difference decides whether the user stays logged in.

1. **`DELETE /v1/auth/tokens` does NOT spare the caller's own token.** The item is
   right that it exists. The hi-fi is wrong about what it does: its header comment says
   "revoke all-but-current" (`hifi3-holo-pages.jsx:2796`) and its button reads **"Sign
   out everywhere else"** (`:3049`). The handler calls `DeleteTokensForUser(authUser.ID)`
   (`internal/api/auth.go:350-357`), which is `DELETE FROM api_tokens WHERE user_id = $1`
   (`internal/store/query/tokens.sql:25-26`) - **every** token, the caller's included.
   Shipping the hi-fi's label would be a lie about a security control. See decision 6.
2. **`PUT /v1/users/me/password` DOES spare the caller's own token, so the change does
   not log you out.** It calls `DeleteOtherTokensForUser` with `authUser.TokenID`
   (`auth.go:325-328`), which is `... WHERE user_id = $1 AND id <> $2`
   (`tokens.sql:28-29`). The hi-fi's warning copy here is correct
   (`hifi3-holo-pages.jsx:3010-3012`). The task's hypothesis that the password tab is
   the defect-prone spot **inverts**: password is the safe one, Sessions is the hazard.
3. **A wrong current password is a 403, not a 401** (`auth.go:298-301`), so it does not
   fire `onUnauthorized` (`web/src/lib/api.ts:44-46`) and does not tear down
   `AuthProvider` (`AuthProvider.tsx:39-49`). Verified: the only 401s from this
   endpoint come from `BearerAuth` itself (`internal/api/middleware.go:21,33`).
4. **`PATCH /v1/users/me` is name-only, and email is immutable through the entire API.**
   `updateUserRequest` has exactly one field, `Name` (`internal/api/users.go:49-51`);
   `handleUpdateMe` calls `UpdateUserName` (`users.go:421-424`). Confirmed at the store
   layer too: `internal/store/query/users.sql` contains **no** `UPDATE users SET email`
   anywhere (only `password_hash`, `is_admin`, `name`, `archived_at` -
   `users.sql:13,21,51,55,60`). The Users-tab finding that the *admin* PATCH is name-only
   holds for the self-serve one identically - they share `parseUpdateUserRequest`
   (`users.go:56-67`).
5. **`GET /v1/auth/tokens` does not exist** - confirmed by reading the whole route table
   (`internal/api/server.go:96-100`, which registers only `GET /v1/users/me`,
   `PUT /v1/users/me/password`, `DELETE /v1/auth/token`, `DELETE /v1/auth/tokens`). The
   item is correct. The enabler is tracked at
   `docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md:25-27`.
6. **The enabler item's own proposal is partly unbuildable, and this matters for the
   Sessions decision.** It asks for "created_at, last_used_at, current-session flag".
   `api_tokens` has exactly five columns - `id, user_id, token_hash, created_at,
   expires_at` (`internal/store/migrations/000001_initial.up.sql:13-19`). **There is no
   `last_used_at` column**, so that field is a migration, not a query. Every other
   column the hi-fi's session table draws - kind, agent string, IP, location, last
   active (`hifi3-holo-pages.jsx:3057-3058`) - has no column either.
7. **The hi-fi's "30-day TTL · sliding window on use" and "tokens roll over 30 days from
   last use" are fiction** (`hifi3-holo-pages.jsx:3044,3119`). `issueToken` sets
   `expires = time.Now().Add(30 * 24 * time.Hour)` once at creation
   (`auth.go:44-49`), and nothing anywhere updates `expires_at`; `BearerAuth` only reads
   it (`middleware.go:32-35`). The TTL is fixed from issuance, not sliding.
8. **The hi-fi's per-session `Revoke` needs `DELETE /v1/auth/token/{id}`
   (`hifi3-holo-pages.jsx:2795`), which does not exist.** The real
   `DELETE /v1/auth/token` takes no id and always revokes the caller's current token
   (`server.go:99`, `handleLogoutCurrent` at `auth.go:341-348`).
9. **The hi-fi's password strength meter is a fabricated signal.** It renders bars plus
   "strong · 12 chars · mixed case · 1 number" (`hifi3-holo-pages.jsx:2994-3004`). The
   server's only rule is `len(req.NewPassword) < 8` (`auth.go:284-287`). There is no
   complexity check anywhere. See decision 9.
10. **A password over 72 bytes is an opaque 500, and the shipped app already knows
    this.** `bcrypt.GenerateFromPassword` rejects inputs over 72 bytes and
    `handleChangePassword` maps that error to `500 failed to hash password`
    (`auth.go:303-307`) - the same trap `ResetPasswordDialog.tsx:41-45` already guards
    client-side with a comment naming password managers. The item does not mention it.
11. **The hi-fi's Activity card is three-quarters unbuildable.** It wants Member since,
    Last login, Login count, Active sessions (`hifi3-holo-pages.jsx:2950-2954`). Only
    `created_at` exists (`users.go:27`). There is no last-login column, no login
    counter, and no session count. Same for the header strip's `LAST LOGIN`
    (`hifi3-holo-pages.jsx:2846`).

Verified correct in the item: the route shape, that `PATCH /v1/users/me` and
`PUT /v1/users/me/password` exist and are frontend-ready, that `DELETE /v1/auth/tokens`
exists, that `GET /v1/auth/tokens` does not, and that the work is frontend-only.

## Verified backend contract

Routes, from `internal/api/server.go:96-100,153`. All four are `auth(...)`; **none** is
`AdminOnly`:

```go
mux.Handle("GET /v1/users/me",            auth(http.HandlerFunc(s.handleGetMe)))
mux.Handle("PUT /v1/users/me/password",   auth(http.HandlerFunc(s.handleChangePassword)))
mux.Handle("DELETE /v1/auth/token",       auth(http.HandlerFunc(s.handleLogoutCurrent)))
mux.Handle("DELETE /v1/auth/tokens",      auth(http.HandlerFunc(s.handleLogoutAll)))
mux.Handle("PATCH /v1/users/me",          auth(http.HandlerFunc(s.handleUpdateMe)))
```

### Authorization

Every one of these acts on `authUser.ID` / `authUser.TokenID` taken from the context
injected by `BearerAuth` (`middleware.go:36-43`) - **never from the request body or
path**. There is no id parameter to tamper with and no cross-user reach: the identity is
the bearer token. `handleUpdateMe` (`users.go:413-414`), `handleChangePassword`
(`auth.go:289`), `handleLogoutAll` (`auth.go:351`) all read it the same way. This is the
entire authorization story for the page; the SPA adds no gate of its own and must not.

### GET /v1/users/me - response, verbatim

`userResponse`, `internal/api/users.go:22-29`, returned by `handleGetMe`
(`users.go:403-411`):

```go
type userResponse struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	IsAdmin    bool       `json:"is_admin"`
	CreatedAt  time.Time  `json:"created_at"`
	ArchivedAt *time.Time `json:"archived_at"`
}
```

- `created_at` is always present (`users.sql:52` returns it; the column is
  `NOT NULL DEFAULT NOW()`).
- `archived_at` has **no** `omitempty`, so the key is always present and is `null` for
  an active user (`toUserResponse`, `users.go:31-45`). An archived user cannot reach
  this endpoint at all: `GetTokenWithUser` joins `AND u.archived_at IS NULL`
  (`tokens.sql:20`), so their token 401s. The field is therefore always `null` here.
- Timestamps are Go `time.Time`, RFC3339 with nanoseconds. Parse with `new Date()`.

The shipped `User` interface (`web/src/lib/types.ts:1-6`) is **missing `created_at`**
and `archived_at`. See decision 4.

### PATCH /v1/users/me - what it actually accepts

`updateUserRequest`, `internal/api/users.go:49-51`. **One field.** Not a pointer, so
there is no omitted-versus-empty distinction to make:

```go
type updateUserRequest struct {
	Name string `json:"name"`
}
```

Validation, `parseUpdateUserRequest` (`users.go:56-67`), shared with the admin PATCH:

| Rule | Failure |
|---|---|
| body decoded via `readJSON` (the single JSON entry point) | 400 from `readJSON` |
| `strings.TrimSpace(req.Name)` must be non-empty | 400 `name is required` (`users.go:63`) |

The **trimmed** name is what gets stored (`users.go:61,422`), so `"  Mira  "` persists
as `"Mira"` and the 200 response reflects that. There is no length cap, no character
restriction, and no uniqueness constraint on `name`.

Success is **200** with the full updated `userResponse` (`users.go:429`). There is no
optimistic-concurrency token: `UpdateUserName` is a bare `WHERE id = $1`
(`users.sql:50-52`), so concurrent edits are last-writer-wins. Unlike the admin arm
(`users.go:449-452`), `handleUpdateMe` has no `pgx.ErrNoRows` branch - the row is the
caller's own and cannot vanish while their token authenticates.

### PUT /v1/users/me/password - request, validation, and session side effects

Request body, `handleChangePassword` (`internal/api/auth.go:277-280`):

```go
var req struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}
```

Handler order, exactly (`auth.go:281-338`):

| Step | Rule | Failure |
|---|---|---|
| 1 | `readJSON` | 400 |
| 2 | `len(req.NewPassword) < 8` | 400 `password must be at least 8 characters` (`:284-287`) |
| 3 | `GetUser(authUser.ID)` | 500 `failed to look up user` (`:292-296`) |
| 4 | `bcrypt.CompareHashAndPassword(user.PasswordHash, req.CurrentPassword)` | **403** `current password is incorrect` (`:298-301`) |
| 5 | `bcrypt.GenerateFromPassword(req.NewPassword, 12)` | **500** `failed to hash password` (`:303-307`) - this is where a >72-byte password lands |
| 6 | tx: `SetPasswordHash` then `DeleteOtherTokensForUser{UserID, ID: authUser.TokenID}` | 500 (`:317-331`) |
| 7 | commit | 500 `failed to commit password change` (`:333-336`) |

**There is no complexity rule.** Length >= 8 is the only constraint on `new_password`.
There is no constraint at all on `current_password` beyond it matching - an empty string
simply fails step 4 with a 403. There is no check that the new password differs from the
old one, so re-submitting the current password succeeds and still revokes other
sessions.

Success is **204 No Content** (`auth.go:338`), no body.

**Session side effects, precisely.** Step 6 deletes every token for the user *except*
the one on the request (`tokens.sql:28-29`). So:

- The caller's own bearer token **survives**. The SPA is not logged out. `apiFetch`
  returns `undefined` for a 204 (`web/src/lib/api.ts:57`) and no listener fires.
- Every other session of the same user - another browser, another device, and any
  `relay` CLI login (`~/.relay/config.json`) - gets a 401 on its next request.
- The whole thing is one transaction (`auth.go:309-336`), so a failure to revoke rolls
  back the password change too. There is no window where the password changed but old
  sessions survived.

### DELETE /v1/auth/tokens - the sign-out-everywhere action

`handleLogoutAll` (`internal/api/auth.go:350-357`), in full:

```go
func (s *Server) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	authUser, _ := UserFromCtx(r.Context())
	if err := s.q.DeleteTokensForUser(r.Context(), authUser.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke tokens")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

backed by `DELETE FROM api_tokens WHERE user_id = $1` (`tokens.sql:25-26`).

- **It deletes the caller's own token.** No `id <> $2`. Contrast `tokens.sql:28-29`.
- Takes no body and no parameters. Idempotent: a second call deletes zero rows and still
  returns 204.
- **204**, no body. `apiFetch` yields `undefined` (`lib/api.ts:57`).
- **Critically: a 204 does not fire `onUnauthorized`** - that only happens on a literal
  401 (`lib/api.ts:44-46`). So after this call the SPA still holds a token in
  localStorage and still believes it is authenticated, against a credential the server
  has destroyed. Nothing tells it otherwise until the *next* request 401s. This is the
  one place in this change where a plausible implementation is silently wrong; see
  decision 7.

### GET /v1/auth/tokens - confirmed absent

Not registered anywhere in `internal/api/server.go` (the auth block is `:96-100`), and
`internal/store/query/tokens.sql` has no list query - only `CreateToken`,
`GetTokenWithUser`, `DeleteToken`, `DeleteTokensForUser`, `DeleteOtherTokensForUser`
(`tokens.sql:1,6,22,25,28`). Tracked as an enabler at
`docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md:25-27`.

## Existing frontend this must match

| Thing | Precedent |
|---|---|
| Registry-driven `/x/:tab` shell: `useParams().tab` -> `find` -> render `Panel`, unknown tab `<Navigate replace>` to the default | `web/src/admin/AdminPage.tsx:15-40`, `web/src/admin/tabs.ts:21-32` |
| Pill-group tab bar with `NavLink` supplying `aria-current="page"` | `web/src/admin/AdminTabs.tsx:9-29` |
| `/x` (no tab) is a separate `<Route>` rendering `<Navigate to="/x/<default>" replace/>` | `web/src/app/router.tsx:38` |
| Inline edit form building a patch from **changed fields only**; no-op when clean | `web/src/workers/WorkerEditForm.tsx:17-46`, especially `:42-45` |
| Client-side min-8 password check with the exact string `Password must be at least 8 characters.` | `web/src/auth/RegisterScreen.tsx:31-32`, `web/src/admin/users/ResetPasswordDialog.tsx:36`, `web/src/admin/users/CreateUserForm.tsx:40` |
| Client-side 72-byte bcrypt guard, to avoid an opaque 500 | `web/src/admin/users/ResetPasswordDialog.tsx:41-45` |
| Mutation error rendered as `error.message` (i.e. `"<status> <server sentence>"`, per `ApiError` at `lib/api.ts:53`) | `web/src/admin/users/ResetPasswordDialog.tsx:84` |
| Destructive action behind `ConfirmDialog` (composes `DialogShell`/`dialogStack`) | `web/src/components/ConfirmDialog.tsx:17-61` |
| Copy that states a verified session consequence rather than hedging | `web/src/admin/users/ResetPasswordDialog.tsx:62-63`, `web/src/admin/users/UsersTab.tsx:272-275` |
| Omit-rather-than-fake, with the reason written at the site | `web/src/admin/enrollments/EnrollmentsTab.tsx:202-204` (no revoke endpoint), `web/src/admin/AdminPage.tsx:6-14` (no build/uptime endpoint) |
| Absence asserted in both directions in tests | `web/src/admin/enrollments/EnrollmentsTable.test.tsx:74-78`, `web/src/admin/users/UsersTable.test.tsx:51-56` |
| Auth state, 401 teardown, `queryClient.clear()` on logout | `web/src/auth/AuthProvider.tsx:39-49,91-97` |
| Single fetch entry point | `web/src/lib/api.ts:29-59` (`apiFetch`, `ApiError`) |
| Form field label/hint/error | `web/src/components/Field.tsx:11-25`, `Input.tsx:3-14`, `Button.tsx:3-14` |

Available Holo primitives: `GlassPanel, Eyebrow, ProgressBar, Chip, PillButton, KpiStat,
Panel, StatusDot, Table, TableRow, TableCell, ariaSort, sortCaret`
(`web/src/components/holo/index.ts`). **No new primitive is needed and no table is
needed** - this surface is two forms and one button.

### How the SPA holds auth state, and what that means here

`AuthProvider` (`web/src/auth/AuthProvider.tsx`) is the single owner of "who am I":

- `user` and `status` are `useState`, seeded once from `GET /users/me` on mount
  (`:51-66`). **Nothing ever refetches it.** There is no `['me']` query and no
  invalidation path - `apiFetch<User>('/users/me')` appears only at `:56` and `:70`.
- On any 401 anywhere (including a streaming one, `lib/api.ts:127-129`), the provider
  clears the token, nulls `user`, sets `anonymous` and calls `queryClient.clear()`
  (`:41-47`), guarded to no-op when already anonymous (`:42`).
- `logout()` (`:91-97`) is `DELETE /v1/auth/token` (best-effort, `.catch(() => {})`)
  followed by the same local teardown.
- The context exposes `{status, user, login, register, logout}` (`:16-22`) - **no setter
  and no refresh**. So a successful rename leaves `user.name` stale for the rest of the
  session.

Today only `user.is_admin` and `user.email` are consumed downstream
(`web/src/shell/HoloShell.tsx:20`, `UserMenu` via `email`), so the staleness is not
currently visible in the shell - but the Identity tab itself displays the name it just
changed, so it is visible there. Decisions 3 and 7 both follow from this section.

## Tabs, and what each is scoped to

Three tabs at `/profile/:tab`, slugs `identity | password | sessions`, default
`identity`.

### Header and meta strip (shared shell)

Eyebrow `YOUR ACCOUNT`, an avatar square with the user's initials, and the display name
as the `h1`. Right-aligned mono meta strip: `EMAIL`, `ROLE` (`ADMIN`/`USER` from
`is_admin`), `MEMBER SINCE` (`created_at`, absolute date). Maps to
`hifi3-holo-pages.jsx:2824-2854` minus `LAST LOGIN` (no column exists - decision 8).

No breadcrumb/back link: the hi-fi's `← BACK` (`:2829`) is a prototype router artifact,
and the app's real back affordance is the persistent shell nav.

### Identity tab (`/profile/identity`)

One `GlassPanel`, max-width, mirroring `hifi3-holo-pages.jsx:2909-2944`:

- **Display name** - editable `Input`, seeded once from `user.name`.
- **Email** - `disabled` `Input` with the hint `identity - contact your admin to change`.
  This is not a deferral: email is immutable through the entire API and store layer
  (finding 4). The hint states the real remedy.
- **Role note** - the `ADMIN`/`USER` chip plus "Role is server-side only - promote or
  demote from Admin -> Users" (`hifi3-holo-pages.jsx:2929-2938`). Truthful: promotion is
  `PromoteUserToAdmin` (`users.sql:20-21`), reachable only through admin paths.
- `Save changes` / `Cancel`. Save is a no-op issuing **zero requests** when the name is
  unchanged (decision 2).

The hi-fi's Activity side card (`:2946-2965`) is **not** built - three of its four rows
have no backing column (finding 11), and `MEMBER SINCE` moves to the header strip.

### Password tab (`/profile/password`)

One `GlassPanel`, mirroring `hifi3-holo-pages.jsx:2970-3019`:

- **Current password** / **New password** (hint `min 8 characters`) / **Confirm new
  password**, all `type="password"`.
- A warning box stating the verified consequence: *All of your other sessions will be
  signed out. This browser stays signed in.* Verified against
  `DeleteOtherTokensForUser` (`auth.go:325-328`, `tokens.sql:28-29`).
- `Update password` / `Cancel`. On 204: clear all three inputs and show a success line.

Client-side pre-submit checks, in this order, each blocking the request:
confirm-matches-new; `new.length >= 8` with the shipped literal; `TextEncoder` byte
length <= 72. Rationale in decisions 9 and 10.

The hi-fi's strength meter (`:2994-3004`) and its "Forgot your password?" side card
(`:3021-3034`) are both out - see Scoped out.

### Sessions tab (`/profile/sessions`)

**No list.** One destructive action plus an honest statement of what the backend can and
cannot tell you:

- A `Sign out everywhere` button (destructive styling), behind `ConfirmDialog`.
- Copy above it stating the verified semantics: this revokes **every** bearer token for
  your account **including this browser**, so you will be returned to sign-in, and any
  `relay` CLI login will need `relay login` again.
- A footnote in the `EnrollmentsTab.tsx:202-204` house style explaining that a per-session
  list is not available because the server exposes no endpoint to enumerate tokens, and
  naming the filed enabler.

Rationale in the next section.

## The Sessions decision

**Decision: ship the Sessions tab with the action only, no list (option b).**

The precedents are consistent and both point the same way *as stated*: the Agent
enrollments tab shipped with no revoke control because `DELETE /v1/agent-enrollments/{id}`
does not exist (`EnrollmentsTab.tsx:202-204`, asserted in both directions at
`EnrollmentsTable.test.tsx:74-78`), and the admin Server tab shipped without the hi-fi's
VERSION/BUILD/DB/UPTIME strip because no endpoint returns those facts
(`AdminPage.tsx:6-14`). The governing rule is: **omit what the backend cannot supply,
and file the enabler.**

The asymmetry the task names is real, and it is what makes this a decision rather than a
lookup: in both precedents the *control itself* would have failed. Here the control
works - `DELETE /v1/auth/tokens` is a live, auth-gated, idempotent 204
(`auth.go:350-357`) - and only the *list* is unsupported. Applied faithfully rather than
by analogy, the rule omits the list and keeps the action. Option (a) would over-apply it
and drop a working capability because an unrelated read endpoint is missing.

Four supporting reasons:

1. **The tab is not "almost nothing".** Sign-out-everywhere is the action a user reaches
   for after a suspected compromise - the one profile action with a time-critical use.
   Deferring it behind a list endpoint has an actual security cost, and the list is
   further away than the enabler item suggests: `api_tokens` has no `last_used_at`
   column and no agent/IP columns at all (finding 6), so a useful list is a migration
   plus an endpoint, not "one small endpoint".
2. **Option (a) fails the hard requirement in a subtler way.** With no `sessions` entry
   in the registry, `findProfileTab('sessions')` returns undefined and the shell
   redirects to `identity` (the `AdminPage.tsx:23` behaviour). The UserMenu's third link
   would then *resolve* but land somewhere it does not name - a link that lies rather
   than a link that is dead. That is worse than a small, honest tab.
3. **The no-list shape is what makes the teardown safe.** Because the tab holds no
   query, there is no active observer to refire against a token the server has just
   destroyed when we clear session state (decision 7). A Sessions *list* would have to
   solve that ordering problem; a Sessions *action* does not have it.
4. **The omission is self-documenting.** The tab is where a user goes to ask "what is
   logged in as me?", and the honest answer today is "the server does not track that -
   here is the blunt instrument". That footnote is the enabler's user-facing trace, the
   same job `EnrollmentsTab.tsx:202-204` does.

**What this decision does NOT license:** the hi-fi's button label. `Sign out everywhere
else` (`hifi3-holo-pages.jsx:3049`) describes an endpoint that does not exist. The
control ships as `Sign out everywhere` and its confirm copy says you will be signed out
of this browser too, because that is what `DeleteTokensForUser` does. A control that
understates its own blast radius is worse than a missing one.

## Decisions

**1. `/profile/:tab` reuses the admin console's registry-plus-switch shape locally; no
shared tab primitive is extracted.** `web/src/profile/tabs.ts` mirrors
`web/src/admin/tabs.ts:21-32` and `ProfileTabs.tsx` mirrors `AdminTabs.tsx:9-29`. This is
the **second** consumer, and the house rule is extract before the *third* - so extraction
is not yet due, and the two differ in ways that would have to be parameterized now for no
present benefit (admin sits behind `AdminRoute`, profile does not; the hi-fi wants a
count badge on Sessions that `AdminTabs.tsx:6-8` deliberately does not render and that we
could not supply anyway). Recorded so the third tabbed surface triggers the extraction.

**2. The Identity save is changed-fields-only, degenerating to "send nothing if
unchanged".** PATCH takes one field, so this is simply: if `name.trim() === user.name`,
Save issues zero requests. Same construction as `WorkerEditForm.tsx:42-45`. Not merely
tidy - a no-op PATCH is a write to the users table and a 200 that would reset the form's
success state for no reason.

**3. The PATCH 200 body is pushed into `AuthProvider`; no refetch, no second source of
truth.** `AuthContext` gains one method, `applyUser(u: User)`, which is `setUser`. The
response of `PATCH /v1/users/me` is the same `userResponse` struct `GET /v1/users/me`
returns (`users.go:429` and `:410` both call `toUserResponse`), so it is authoritative
and needs no confirming round trip. Rejected: a separate `useQuery(['me'])` in the
profile module - that would make two components disagree about who the user is, which is
the "single entry point" shape this repo favours, applied to identity.

**4. `User` gains `created_at: string` as a required field.** The server always sends it
(`users.go:27`, `users.sql:52`). Verified safe: no file in `web/src` constructs a
`User`-annotated literal - the only typed uses are `apiFetch<User>` and
`user: User | null` (`AuthProvider.tsx:18,56,70`), and every test fixture is an untyped
object inside `HttpResponse.json`, which is not checked against this interface. So the
field is required (not optional-with-a-fallback), and tests that render the header add
it. `archived_at` is **not** added: it is always `null` on this endpoint by construction
(an archived user's token cannot authenticate - `tokens.sql:20`), so a field that can
only ever be null is noise.

**5. Tab slugs are `identity | password | sessions`, default `identity`.** The hi-fi's
first slug is `profile` (`hifi3-holo-pages.jsx:2817`), which would make the URL
`/profile/profile`. `identity` matches the panel's own name (`ProfileIdentity`,
`:2909`) and the backlog item's title. `/profile` with no segment gets its own route
rendering `<Navigate to="/profile/identity" replace />`, exactly as `/admin` does
(`router.tsx:38`), so **`UserMenu` needs no change** and all three of its links resolve.
The `/profile/*` splat (`router.tsx:41`) is replaced, not kept alongside.

**6. The sign-out-everywhere control is labelled `Sign out everywhere`, not the hi-fi's
`Sign out everywhere else`, and its copy states that this browser is included.**
`DeleteTokensForUser` has no `id <> $2` (`tokens.sql:25-26`). The hi-fi's label and its
`:2796` comment are both wrong about the endpoint. Mislabelling a security control's
blast radius is a defect, not a copy nit.

**7. On a 204 from `DELETE /v1/auth/tokens` the SPA tears its own session down
immediately and navigates to `/auth`; it does not wait to be 401ed.** A 204 fires no
listener (`lib/api.ts:44-46` is 401-only), so without this the shell keeps rendering as
authenticated against a destroyed token until some later request happens to fail - a
window in which the UI asserts something false about the user's security state. `AuthProvider`
gains `clearSession()`, extracted from the local half of `logout()`
(`AuthProvider.tsx:93-96`: `clearToken`, `setUser(null)`, `setStatus('anonymous')`,
`queryClient.clear()`); `logout()` is re-expressed through it with its exported signature
unchanged. The Sessions tab calls `clearSession()` **then** `navigate('/auth')`.
Explicitly rejected: calling the existing `logout()`, which would first issue
`DELETE /v1/auth/token` against a token that no longer exists - a guaranteed 401 whose
`onUnauthorized` would race the teardown we are already doing. This is "end the
generation before releasing the resource" read forwards: the resource is already gone, so
end the generation that still believes in it before anything else can observe it.

**8. `LAST LOGIN`, `LOGIN COUNT` and `ACTIVE SESSIONS` are omitted from the meta strip
and the Activity card is not built at all.** No column, no endpoint, no proxy
(`migrations/000001_initial.up.sql` users table has no last-login field;
`api_tokens.created_at` is issuance, not login, and is unreadable anyway without
`GET /v1/auth/tokens`). Rendering `-` for three of four rows is the VERSION/BUILD strip
mistake (`AdminPage.tsx:6-14`). `MEMBER SINCE` - the one real value - moves into the
header strip where it stands on its own.

**9. No password strength meter. The `min 8 characters` hint and the shipped min-8 check
are the whole client-side story on strength.** The server enforces exactly
`len(new) >= 8` (`auth.go:284-287`) - no complexity rule exists to reflect. A meter
reading "mixed case · 1 number" (`hifi3-holo-pages.jsx:3003`) would assert a policy the
server does not have, which is fabrication in the same family as the build strip. The
min-8 check itself is copied from the three shipped sites rather than extracted (see
decision 11).

**10. A 72-byte client-side guard ships on the new password.** `bcrypt` rejects over 72
bytes and `handleChangePassword` turns that into a **500** (`auth.go:303-307`) - an
opaque server error for a legitimate password-manager password. `ResetPasswordDialog.tsx:41-45`
already guards exactly this with the message `Password must be 72 bytes or fewer.` and
`new TextEncoder().encode(...).length`. Byte length, not `.length`: a 40-character
passphrase with emoji or accents can exceed 72 bytes.

**11. The min-8 check is copied to a fourth site rather than extracted.** It is two lines
- a comparison against a literal and a message string - already at
`RegisterScreen.tsx:31-32`, `ResetPasswordDialog.tsx:36` and `CreateUserForm.tsx:40`.
This is deliberately **not** treated like the detail-page state triad
(`idea-2026-08-12-detail-page-state-triad-primitive.md`): that is a 25-line block with
branching and three variable strings, where a helper hides a real decision. A guard with
no decision inside it becomes indirection when extracted, and a shared constant would
still leave three call sites writing the same two lines. No enabler filed. If the message
or the bound ever diverges between sites, that divergence is the trigger to revisit.

**12. Errors render as `error.message`, matching `ResetPasswordDialog.tsx:84`.**
`ApiError.message` is `"<status> <server sentence>"` (`lib/api.ts:53`), so a wrong current
password reads `403 current password is incorrect`. Noted rather than improved: changing
the house error-rendering convention is not this slice's job, and it does surface the
server's own sentence, which is the property that matters.

**13. There is no polling anywhere on this surface.** Nothing here is live: the user row
changes only when this page changes it, and there is no list to keep fresh. The Identity
tab reads `AuthProvider.user`, which is already in memory. Zero background requests.

**14. No `useQuery` at all; the three mutations use `useMutation` without a shared
actions hook.** The tabs do not share state and each owns exactly one mutation, so a
`useProfileActions` mirroring `useScheduleActions` would be a wrapper around three
unrelated calls. Consequently the stale-error-mask bug from the schedule slice
(`??` short-circuiting across sibling mutations) has no analogue here.

**15. The detail-page state triad is NOT a consumer of this slice, and the countdown is
untouched.** `idea-2026-08-12-detail-page-state-triad-primitive.md` warns that a fourth
detail page turns the deviation into policy. The profile page is not one: it fetches no
resource by id, has no 404 state, and its data is already resident in `AuthProvider`. It
renders no loading panel and no retryable error card. Checked explicitly so the item's
countdown is not silently advanced.

## Architecture

New files, all under `web/src/profile/`:

- `ProfilePage.tsx` - route component: header, meta strip, `ProfileTabs`, active panel.
  Mirrors `AdminPage.tsx:15-40`.
- `ProfileTabs.tsx` - the pill-group `NavLink` bar. Mirrors `AdminTabs.tsx:9-29`.
- `tabs.ts` - `PROFILE_TABS`, `DEFAULT_PROFILE_TAB`, `findProfileTab`. Mirrors
  `admin/tabs.ts:21-32`.
- `IdentityTab.tsx` - name/email form, changed-fields save (decision 2), `applyUser` on
  success (decision 3).
- `PasswordTab.tsx` - three-field form, the three client-side guards, the
  other-sessions warning.
- `SessionsTab.tsx` - the confirmed sign-out-everywhere action and the omission
  footnote.
- `api.ts` - `updateMe`, `changePassword`, `signOutEverywhere`.

Modified:

- `web/src/app/router.tsx` - replace `<Route path="/profile/*" element={<JobsPlaceholder />} />`
  (`:41`) with `<Route path="/profile" element={<Navigate to="/profile/identity" replace />} />`
  and `<Route path="/profile/:tab" element={<ProfilePage />} />`, both inside
  `ProtectedRoute` and **not** `AdminRoute` (every endpoint is `auth(...)`, not
  `AdminOnly` - `server.go:96-100,153`).
- `web/src/lib/types.ts` - `User` gains `created_at: string` (decision 4).
- `web/src/auth/AuthProvider.tsx` - context gains `applyUser` (decision 3) and
  `clearSession` (decision 7); `logout()` is re-expressed through `clearSession` with an
  unchanged exported signature, so no existing call site or test moves.

Reused unchanged: `GlassPanel`, `Panel`, `Chip`, `PillButton`, `Eyebrow`,
`ConfirmDialog`, `Field`, `Input`, `Button`, `apiFetch`, `ApiError`, `useAuth`.

`JobsPlaceholder` (`web/src/app/JobsPlaceholder.tsx`) becomes unreferenced once `/profile/*`
goes; whether to delete it is a plan-time call, not a design question. Verify it has no
other importer before removing.

### Exact calls

```
updateMe({ name })                    -> PATCH  /v1/users/me           json -> User (200)
changePassword(current, next)         -> PUT    /v1/users/me/password  json -> void (204)
signOutEverywhere()                   -> DELETE /v1/auth/tokens             -> void (204)
```

`apiFetch` already returns `undefined` for 204 (`lib/api.ts:57`), so the two void calls
need no special handling. No path interpolation anywhere on this surface, so the
unencoded-interpolation defect filed as
`bug-2026-08-12-unencoded-path-interpolation-api-clients` has no new instance here.

### Interaction detail

| Control | Behaviour |
|---|---|
| UserMenu `Profile` | `/profile` -> `Navigate` -> `/profile/identity`. |
| UserMenu `Password` / `Sessions` | Direct to the tab. |
| Unknown `/profile/<x>` | `<Navigate to="/profile/identity" replace />`, per `AdminPage.tsx:23`. |
| Identity `Save changes` | No-op with zero requests when the trimmed name equals `user.name`; otherwise one PATCH, then `applyUser(response)`. |
| Identity `Cancel` | Resets the draft to `user.name`, clears the error. |
| Password `Update password` | Three client guards, then one PUT. On 204: clear all three inputs, show success. |
| Password `Cancel` | Clears all three inputs and any error. |
| Sessions `Sign out everywhere` | `ConfirmDialog` (destructive). Confirm -> one DELETE. On 204: `clearSession()` then `navigate('/auth')`. Cancel issues no request. |
| Errors | Rendered inline beside the control that caused them, never in a page-level box behind a scrim. |

Two lifecycle rules, both instances of house invariants:

- **A form seeds its draft once and is never re-derived on re-render.** Nothing polls
  here, but `applyUser` changes `user` and would otherwise reset a field mid-edit.
- **A settled mutation never writes back into draft state.** Success clears or marks
  clean; it does not push the response into the inputs. The one thing a success *does*
  push is the authoritative user row into `AuthProvider` (decision 3), which is state
  the form does not read after seeding.

## Security and system design

- **Threat model.** No new endpoint, no widened surface, no new parameter. Every call
  acts on the identity in the bearer token, resolved server-side by `BearerAuth`
  (`middleware.go:36-43`); there is no id in any path or body for a client to tamper
  with, so there is no horizontal-privilege vector to reason about. The SPA's route is
  behind `ProtectedRoute` only, which is correct - the endpoints are not admin-gated.
- **Secret handling.** Three password fields exist in component state for the life of the
  form. They are `type="password"`, are cleared on success and on Cancel, are never put
  in a query string, a `title`, a log line, or a react-query cache key, and no mutation
  result retains them. Note the standing lesson here: **calling a clear function is not
  evidence** - the test must assert absence from the store the library actually keeps,
  and `useMutation` retains `variables` on the settled mutation, so a test asserting the
  password is not recoverable must read the mutation object, not just the inputs.
- **The 500 is a disclosure-free failure, and the guard is a UX fix not a security fix.**
  The >72-byte path returns `failed to hash password` with no detail (`auth.go:303-307`).
  Guarding client-side avoids an opaque error; it does not gate anything, and the server
  remains the enforcer.
- **Email enumeration is not in play.** Unlike login, no endpoint on this surface takes
  an email. The 403-versus-404 distinction on a wrong current password reveals nothing
  the caller does not already know about their own account.
- **Blast radius.** The irreversible action here is sign-out-everywhere, and it is
  irreversible only in the sense that every device must sign in again - no data is
  destroyed. It is confirmed, and it is honestly labelled (decision 6). The password
  change is transactional (`auth.go:309-336`), so there is no state where the password
  changed but stale sessions survived.
- **Availability / load.** Zero polled queries, zero timers, zero background requests
  (decision 13). Every request is user-initiated and at most one per click.
- **Invariants.** No backend change: epoch fence, single job-spec pipeline, one bounded
  sender per stream, identity-checked teardown, no interior pointers across locks, and
  single JSON entry point are all untouched. `readJSON` remains the only body reader on
  the server side (`users.go:58`, `auth.go:281`). Frontend analogues that do apply: every
  request goes through `apiFetch`; the teardown ordering in decision 7 is "end the
  generation before releasing the resource"; and `applyUser` keeps one owner of identity
  rather than a second cache.

## Testing

Existing Vitest + MSW + `renderWithQuery`/`AuthProvider` harness; mirror the file layout
of `web/src/admin/`. Assertions whose vacuity is the specific risk here:

**Sign-out-everywhere teardown (the highest-value test in this slice)**
- Confirm the dialog, let the 204 resolve, and assert **all** of: `getToken()` returns
  null, the route is `/auth`, and the query cache is empty. Paired positive: before
  confirming, the token is present and the route is `/profile/sessions`. A test asserting
  only the navigation passes against an implementation that leaves a live token behind -
  which is the exact defect decision 7 exists to prevent.
- Assert the DELETE fires **exactly once** and that **no** `DELETE /v1/auth/token`
  (singular) is issued - the tell that `logout()` was reused instead of `clearSession()`.
- Cancel issues **zero** requests; paired positive that Confirm issues exactly one.

**Sessions tab omission, both directions**
- No table, no `role="table"`, no per-session Revoke control, and no request to
  `/v1/auth/tokens` on mount (mirroring `EnrollmentsTable.test.tsx:74-78`). Paired
  positive: the sign-out-everywhere button **is** present, so the test cannot pass
  against an empty component.
- The button's accessible name is `Sign out everywhere` and the string
  `everywhere else` is **absent** - the hi-fi label is a specific, likely mistake
  (finding 1).
- The confirm copy states that this browser is included. A regex for
  /this browser|signed out here|including this/ over the dialog body.

**Password: what the request contains and what it does not**
- The PUT body has exactly `current_password` and `new_password` - assert the parsed
  body's key set, so a stray `confirm_password` (a natural mistake, since the form has
  three fields and the API takes two) fails.
- A 204 leaves the user **signed in**: `getToken()` still returns the token and the route
  is unchanged. Paired positive against the sessions test above, which asserts the
  opposite for the other endpoint. These two tests are each other's control; write them
  together.
- A **403** response renders the error and leaves the user signed in - it must not trip
  the 401 teardown. Discriminating input: assert status 403 specifically, since a test
  written against a generic "error" mock would pass against a component that logs out on
  any failure.
- Each client guard blocks the request: mismatched confirm, a 7-character new password,
  and a 73-byte new password each issue **zero** requests. Paired positives: an
  8-character password and a 72-byte password each issue exactly one. The byte test needs
  a multi-byte string, not 73 ASCII characters, or it does not distinguish
  `TextEncoder().encode(x).length` from `x.length`.

**Identity: the no-op save**
- Save with an untouched name issues **zero** requests; paired positive that a changed
  name issues exactly one PATCH whose body is `{name: <trimmed>}`.
- Whitespace-only edit (`"Mira"` -> `"Mira  "`) is treated as unchanged and issues zero
  requests - the server would trim it to the same value anyway (`users.go:61`).
- On 200, the header `h1` shows the new name, proving `applyUser` ran. Paired negative:
  a component that only sets local form state passes a test that reads the input, so the
  assertion must read the **header**, which is fed from `AuthProvider`.
- A 400 `name is required` renders inline and leaves the header name unchanged.

**Routing**
- All three UserMenu hrefs resolve to a rendered tab and **none** renders
  `JobsPlaceholder`. Assert the placeholder's text is absent app-wide for these routes.
- `/profile` redirects to `/profile/identity`; an unknown `/profile/nope` redirects to
  the same place. Both `replace`, so history does not accumulate.

**AuthProvider regression gate**
- `web/src/auth/AuthProvider.test.tsx` must stay green with **zero edits**. `logout()`'s
  behaviour and signature are unchanged by the `clearSession` extraction; an assertion
  needing adjustment is itself the finding.
- Direct tests for `clearSession`: it clears token, user, status and cache, and issues
  **no** network request.

Plan-supplied test bodies are guesses until run RED. Every absence assertion above
carries a required positive control in the representation the real failure would take.

## Acceptance criteria

1. `/profile`, `/profile/password` and `/profile/sessions` - the three `UserMenu` links
   (`UserMenu.tsx:60,64,70`) - all resolve to real rendered tabs. `JobsPlaceholder` is no
   longer reachable from any `/profile` route. `UserMenu` itself is unmodified.
2. `/profile` and any unknown `/profile/:tab` redirect (`replace`) to
   `/profile/identity`; the tab bar uses `NavLink` so the active tab carries
   `aria-current="page"`.
3. The header shows initials, display name, and a meta strip of `EMAIL`, `ROLE` and
   `MEMBER SINCE` only - **no** `LAST LOGIN`, no login count, no session count, and no
   Activity card.
4. Identity edits the display name via `PATCH /v1/users/me`, sends `{name}` and nothing
   else, issues **zero** requests when unchanged (including whitespace-only edits), and
   on 200 updates `AuthProvider` so the header reflects the new name immediately.
5. The email input is present, `disabled`, and hinted that only an admin can change it.
   No control anywhere on the page attempts to mutate email.
6. Password submits `{current_password, new_password}` to `PUT /v1/users/me/password`.
   Client guards block the request for a confirm mismatch, a new password under 8
   characters, and a new password over 72 **bytes**, each with its own message; the
   shipped literal `Password must be at least 8 characters.` is reused verbatim.
7. On a password 204 the user **remains signed in**, the inputs clear, and a success
   message appears. A 403 renders `403 current password is incorrect` inline and does
   **not** sign the user out.
8. The password tab states that all **other** sessions are signed out and that this
   browser stays signed in. There is no strength meter.
9. Sessions renders **no** list, no table and no per-session revoke, issues no request on
   mount, and carries a footnote explaining that the server exposes no endpoint to
   enumerate sessions, naming the filed enabler.
10. Sessions' one action is labelled `Sign out everywhere` (never "else"), is confirmed
    through `ConfirmDialog` (destructive) whose copy states that this browser is included
    and that the `relay` CLI will need `relay login` again, and on 204 clears the token,
    nulls the user, clears the query cache and navigates to `/auth` - **without** issuing
    `DELETE /v1/auth/token`.
11. `AuthProvider` exposes `applyUser` and `clearSession`; `logout()`'s exported
    signature and behaviour are unchanged and `AuthProvider.test.tsx` needs zero edits.
12. `User` includes `created_at: string`.
13. Nothing on the surface polls; no `refetchInterval` and no timer is introduced.
14. `npm test` and the production build are green; changes are confined to
    `web/src/profile/`, `web/src/app/router.tsx`, `web/src/lib/types.ts` and
    `web/src/auth/AuthProvider.tsx`; no backend change; `web/dist` is reverted before the
    change set is assembled.
15. Backlog items are **proposed** (not auto-filed) in Phase 6 per the Scoped out table,
    and `feature-2026-06-26-profile-identity-password-sessions.md` is closed via
    `/backlog close`.

## Scoped out, with the enabler to file

| Hi-fi / item element | Why it is out | Enabler |
|---|---|---|
| Session list: kind, agent, IP, location, created, last active, expires, current flag (`hifi3:3054-3113`) | No `GET /v1/auth/tokens`, and no columns for agent, IP, location or last-used (`migrations/000001_initial.up.sql:13-19`). | **Already filed**, but the existing item is under-specified: `feature-2026-06-26-web-enabler-backend-endpoints.md:25-27` asks for `last_used_at` as if it were queryable. **Propose amending that item** to state that `last_used_at`, and any agent/IP attribution, require a migration first, and that the minimum useful list is `id`, `created_at`, `expires_at` and a current-session flag. |
| Per-session `Revoke` (`hifi3:3105-3108`) | `DELETE /v1/auth/token/{id}` does not exist; the real route takes no id (`server.go:99`). Same treatment the enrollments revoke got. | **Propose:** fold into the amended enabler above - a list without per-row revoke is half a feature, and both need the same new route family. |
| `Sign out everywhere **else**` semantics (`hifi3:2796,3049`) | The endpoint has no all-but-current variant (`tokens.sql:25-26`). | **Propose:** `idea-2026-08-12-sign-out-others-endpoint.md` (low) - a `DELETE /v1/auth/tokens?keep_current=true` arm, or a distinct route, reusing the `DeleteOtherTokensForUser` query the password path already calls. The query exists; only a route does not. |
| `LAST LOGIN`, `LOGIN COUNT` (`hifi3:2846,2952-2953`) | No column, no endpoint, no proxy. | **Propose:** `idea-2026-08-12-user-last-login-tracking.md` (low) - a `users.last_login_at` touched by `handleLogin`. Note it overlaps the already-filed audit-log discussion from the Users-tab spec; whoever picks it up should check for duplication first. |
| Password strength meter (`hifi3:2994-3004`) | The server has no complexity policy to reflect (decision 9). | **None.** A complexity policy is a product/security decision for the backend, not a UI gap - the same treatment `queue` overlap got in the schedule spec. If a policy is ever added, the meter follows it. |
| "Forgot your password?" side card (`hifi3:3021-3034`) | It is accurate (`POST /v1/users/password-reset` is admin-only, `server.go:152`) but it is documentation aimed at a locked-out user, who by definition cannot reach this page. | **None.** Belongs in README/onboarding, not behind a login wall. |
| Avatar upload / any image | Not in the hi-fi and no column exists. | **None.** |
| Extracting a shared tab-shell primitive from `admin/` and `profile/` | Second consumer; the rule is extract before the third (decision 1). | **None** - recorded in this spec so the third tabbed surface triggers it. |
| Extracting the min-8 password guard | Two lines with no decision inside; a helper would be indirection (decision 11). | **None.** |

Per the standing rule, these are **proposals**. Phase 6 files them for human accept;
nothing is auto-filed.

## Risks

- **The sign-out-everywhere teardown is the one place a plausible implementation is
  silently wrong.** Firing the DELETE and navigating - without `clearSession()` - looks
  correct, demos correctly, and leaves a live-looking session holding a dead token. It
  will only fail later, at a random request, as a confusing bounce to sign-in. The paired
  token-and-route assertion in Testing is a requirement, not a nicety.
- **Reusing `logout()` for that teardown is the tempting shortcut** and issues a
  guaranteed-401 `DELETE /v1/auth/token` against a destroyed token, racing
  `onUnauthorized` against the teardown already in flight. The "no singular DELETE" test
  is what catches it.
- **The hi-fi's "else" label will be copied.** It appears twice in the design
  (`hifi3:2796,3049`) and reads as authoritative. Anyone implementing from the mockup
  rather than this spec will ship a control that understates its blast radius.
- **The 403-not-401 distinction is easy to get backwards.** A component that treats every
  password error as an auth failure would log the user out on a typo. The test must
  assert 403 specifically.
- **`created_at` on `User` is a real runtime dependency on test fixtures.** Type-checking
  will not catch a fixture missing it (every fixture is an untyped `HttpResponse.json`
  literal), so the header will render `Invalid Date` in any test that forgets it. Guard
  with an explicit assertion on the rendered `MEMBER SINCE` value, not just its label.
- **`web/dist` is tracked but stale**; a frontend build dirties it and it must be
  reverted before the change set is assembled.
- **Scope creep toward the Sessions list.** Once the tab exists, adding "just a small
  table" will look cheap. It is a migration plus an endpoint plus a route family; keep
  the tab action-only until the amended enabler lands.
