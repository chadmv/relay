# Admin Console - Route Shell + Users Tab - Design

Date: 2026-08-08
Status: Draft (autonomous cycle; conductor review)

## Overview

`/admin` is still a `JobsPlaceholder` stub (`web/src/app/router.tsx:28`) and is the
top of Now on the roadmap. The omnibus backlog item
`docs/backlog/feature-2026-06-26-admin-console-pages.md` specifies five tabs
(Users, Invites, Agent enrollments, Reservations, Server) and itself says "slice
per tab - each is independently shippable". One of those tabs (the Invites
*list*) is backend-blocked on a `GET /v1/invites` endpoint that does not exist.

This spec therefore covers **only the first slice**: the admin route shell (the
tabbed container plus admin gating) and the **Users tab**, fully wired. The other
four tabs are carved into their own backlog items so later cycles pick them up:

- `docs/backlog/feature-2026-08-08-admin-enrollments-tab.md`
- `docs/backlog/feature-2026-08-08-admin-reservations-tab.md`
- `docs/backlog/feature-2026-08-08-admin-server-overview-tab.md`
- `docs/backlog/feature-2026-08-08-admin-invites-tab.md`

This follows the precedent of `feature-2026-06-26-job-actions-submit-cancel-retry`,
which was closed as decomposed after its first slice shipped and its remainder
became `feature-2026-07-01-job-retry-action`.

This slice is **frontend-only**. Every endpoint the Users tab needs already
exists and is already `auth(admin(...))`-gated server-side.

## Design source of truth

The authoritative design layer is the **hi-fi Holo**:
`design_handoff_relay_holo/hifi3-holo-pages.jsx`, component `HoloAdmin`
(line 1930) with its `AdminUsers` child (line 2003). Cyan/dark palette, glass
panels, pill controls.

`design_handoff_relay_holo/reference/screens/admin.js` is the **structure-only**
sketch (orange/cursive). It is useful for confirming which surfaces exist and
which endpoints each column implies; it is **not** authoritative for visuals.
Where the two disagree on look, the hi-fi wins.

### What `HoloAdmin` specifies for the shell

- Page header: eyebrow `SETTINGS · ADMIN ONLY`, `h1` "Admin" at 32px/400 weight
  (identical treatment to `WorkersPage`'s `Eyebrow` + `h1`), plus a right-aligned
  server-facts strip (`VERSION` / `BUILD` / `DB` / `UPTIME`).
- A pill-group tab bar, `align-self: flex-start`, rounded-full, dark translucent
  background, one button per tab, active tab filled with the accent gradient,
  each tab optionally carrying a monospace count badge. Tab order:
  Users, Invites, Agent enrolls, Reservations, Server.
- A tab body region that fills the remaining height, one panel per tab.
- A shared token-on-create modal (`AdminTokenModal`) used by Invites and
  Enrollments (not this slice).

### What `AdminUsers` specifies

- A control row: an endpoint hint (`GET /v1/users`), an `include archived`
  checkbox annotated `?include_archived=true`, a right-aligned exact-match email
  input (`?email=… exact match`), and a primary `+ Create user` button.
- A glass-panel table, header row in mono 10px/0.16em tracking, columns:
  `EMAIL | NAME | ROLE | CREATED | SESSIONS | LAST LOGIN | ACTIONS`.
- Row treatment: a 24px gradient avatar square with the email's initial, email in
  sans, name muted, role as a colored pill (accent for `admin`), archived rows at
  `opacity: 0.55`.
- Per-row actions: archived rows get `Unarchive`; active rows get
  `Reset pw`, `Rename`, and a red-tinted `Archive`.
- A `PageFooter` ("SHOWING 1-n OF total · endpoint · CURSOR PAGINATED", prev /
  page-size buttons).
- A footnote explaining that archive revokes the target's API tokens, that server
  guards prevent archiving yourself or the last admin, and that password reset
  revokes the target's sessions too.

## Backend surface - verified against code, no changes

Read from `internal/api/server.go:150-156`, `internal/api/users.go`, and
`internal/api/auth.go:359-420`. All six routes are registered
`auth(admin(...))`, so a non-admin token gets 403 regardless of the UI.

| Action | Method + path | Request | Success | Notes |
|--------|---------------|---------|---------|-------|
| List users | `GET /v1/users` | query only | 200 `page[userResponse]` = `{ items, next_cursor, total }` | `?include_archived=true` includes archived rows; `?email=<exact>` returns the same envelope with 0 or 1 items; `?sort=` one of `created_at`/`name`/`email` with optional `-` prefix, default `-created_at`; `?limit=` 1..200, default 50; `?cursor=`. |
| Create user | `POST /v1/users` | `{ email, name?, password, is_admin }` | **201** `userResponse` | 400 on missing/invalid email or password < 8 chars; **409 `email already registered`** on duplicate. `name` defaults to the email when blank. This is the **only** way to set `is_admin`. |
| Rename user | `PATCH /v1/users/{id}` | `{ name }` | 200 `userResponse` | `{id}` is the **user UUID**, not the email. Body accepts **only `name`** - 400 `name is required` when blank. 404 on unknown id. |
| Archive user | `POST /v1/users/{id}/archive` | none | 200 `userResponse` | Transactional: archives, deletes the target's API tokens, disables their scheduled jobs. Guards: 400 `cannot archive yourself`; 400 `cannot archive the last active admin`; 409 `user is already archived`; 404 unknown id. |
| Unarchive user | `POST /v1/users/{id}/unarchive` | none | 200 `userResponse` | 400 `cannot unarchive yourself`; 409 `user is not archived`; 404 unknown id. |
| Admin password reset | `POST /v1/users/password-reset` | `{ email, new_password }` | **204 No Content** | Keyed by **email in the body**, not by a path id. 400 if `email` empty or `new_password` < 8 chars; 404 `user not found`. Transactional: sets the hash **and deletes all of the target's tokens**. |

`userResponse` (`internal/api/users.go:22-29`):

```
{ id, email, name, is_admin, created_at, archived_at }   // archived_at is nullable
```

### Where the backlog item's description disagrees with the code

The code wins on all three points; the spec below is written to the code.

1. The item says rename/role is `PATCH /v1/users/:email`. It is
   **`PATCH /v1/users/{id}`** keyed by UUID.
2. The item implies role can be changed. It cannot: `updateUserRequest` has a
   single `Name` field, and no endpoint mutates `is_admin` after creation. Role
   is set once, at `POST /v1/users`.
3. Admin password reset is not a per-user path. It is
   **`POST /v1/users/password-reset`** with `{ email, new_password }`.

### Two verified behaviours the UI must respect

- **`archived_at` is always `null` in active-only mode.** `usersListRowToResponse`
  passes a zero `pgtype.Timestamptz` for the non-archived query family
  (`internal/api/users.go:111-132`), so `archived_at` is only meaningful when
  `include_archived=true`. The UI must not infer "archived" from `archived_at`
  unless the toggle is on.
- **Password reset revokes *all* of the target's sessions, including your own if
  you target yourself.** `handleAdminPasswordReset` calls
  `DeleteTokensForUser(target.ID)`. An admin who resets their own password from
  this tab is immediately logged out (the next request 401s and the SPA's
  `onUnauthorized` listener redirects to `/auth`). The confirm copy must say so.

## Tab routing decision: `/admin/:tab`

Chosen over `?tab=`.

Rationale, against the actual router (`web/src/app/router.tsx`):

- The router already uses path params everywhere (`/jobs/:id`, `/workers/:id`)
  and already reserves a splat for the sibling console: `/profile/*`. There is
  **no `useSearchParams` usage anywhere in `web/src`** - adopting a query-string
  convention here would introduce a second, competing state-in-URL idiom for one
  screen.
- Path segments give each future tab a linkable, bookmarkable URL that reads the
  same way the reference sketch's own captions do
  (`relay.studio.dev/admin/invites`, `/admin/enrollments`).
- Adding the four future tabs is then a one-line change each in a tab registry;
  no route churn.

Concrete routes:

```
/admin              -> <Navigate to="/admin/users" replace />
/admin/:tab         -> <AdminPage />     // unknown tab -> redirect to /admin/users
```

`AdminPage` reads `useParams().tab`, looks it up in a local `TABS` registry, and
renders the matching panel. Tabs not yet built are simply absent from the
registry, so an unknown or not-yet-built segment redirects to `users` rather than
rendering an empty shell. That keeps this slice from shipping four dead tabs.

## Admin gating

Server-side `AdminOnly` is the security boundary. Everything below is UX.

- Add an `AdminRoute` guard beside `ProtectedRoute`
  (`web/src/app/ProtectedRoute.tsx` is the pattern: read `useAuth()`, render an
  `<Outlet />` or a `<Navigate>`). Non-admins hitting `/admin/*` are redirected
  to `/jobs`. It renders inside `ProtectedRoute`, so it can assume an
  authenticated user and only checks `user?.is_admin`.
- Gate the nav entry: `web/src/shell/HoloShell.tsx:7-12` currently lists
  `{ to: '/admin', label: 'Admin' }` unconditionally. Filter the `NAV` array on
  `user?.is_admin` so non-admins see no Admin tab. This satisfies the omnibus
  item's "Non-admins do not see the admin route or nav entry".
- Within the Users tab no further per-control `is_admin` checks are needed: the
  whole route is admin-only, mirroring how `WorkspacesPanel` skips an inner check
  because `WorkerDetailPage` only mounts it for admins.

## Architecture

New feature module `web/src/admin/`, mirroring `web/src/workers/`.

### New files

- `web/src/admin/AdminPage.tsx` - the shell: header, `AdminTabs`, tab-body
  switch driven by `useParams().tab`.
- `web/src/admin/AdminTabs.tsx` - the pill-group tab bar. Renders `NavLink`s from
  a `TABS` registry `[{ slug, label, count? }]`; active styling via `isActive`,
  matching the `HoloShell` nav pattern and the `aria-pressed` segmented-control
  pattern in `WorkersPage.tsx:76-90`. Only the Users tab exists in this slice.
- `web/src/admin/users/api.ts` - typed clients (below) plus `AdminUser`,
  `AdminUsersPage`, `UserSort` types.
- `web/src/admin/users/useAdminUsers.ts` - the list query
  (`placeholderData: keepPreviousData`, as `useWorkers.ts` does). **No
  `refetchInterval`** - the user table is not live data; polling it every 3s is
  pointless load. Refresh comes from mutation invalidation.
- `web/src/admin/users/useAdminUserActions.ts` - `useMutation` per action
  (create, rename, archive, unarchive, resetPassword), each invalidating the
  `['users']` prefix on success. Direct port of `useWorkerActions.ts`.
- `web/src/admin/users/UsersTab.tsx` - control row (include-archived toggle,
  email filter input, Create user button), table, pagination footer.
- `web/src/admin/users/UsersTable.tsx` - the table + per-row action buttons.
- `web/src/admin/users/CreateUserForm.tsx` - the create panel (email, name,
  password, is_admin checkbox).
- `web/src/admin/users/ResetPasswordDialog.tsx` - a small form dialog (new
  password + confirm) with the "this revokes the target's sessions" warning.

### Modified files

- `web/src/app/router.tsx` - replace the `/admin` stub with the two routes above,
  nested under a new `AdminRoute`.
- `web/src/app/AdminRoute.tsx` (new, but listed here as router plumbing) - the
  `is_admin` guard.
- `web/src/shell/HoloShell.tsx` - filter the Admin nav entry on `is_admin`.

### Reused, not rebuilt

Verified present and applicable:

- `web/src/components/holo/` primitives: **`GlassPanel`** (table container, error
  and empty cards), **`Eyebrow`** (`SETTINGS · ADMIN ONLY`), **`PillButton`**
  (`+ Create user`, row actions - it already has `primary` / `muted` / `danger`
  variants, which map exactly onto Create / Rename / Archive), **`Chip`** (the
  role pill). Not applicable in this slice: `ProgressBar`, `KpiStat`,
  `StatusDot`, `Panel` (no per-panel titled sections, no progress or status
  semantics in the users table).
- `web/src/components/ConfirmDialog.tsx` for Archive and Unarchive (its own
  comment says "Reused by Admin/Profile later"). It takes text-only `body`, so
  the password-reset form is a sibling dialog component, not a `ConfirmDialog`
  variant.
- The list-page shape from `WorkersPage.tsx` / `JobsPage.tsx`: cursor +
  `stack`/`offsets` state for prev/next, `computePageRange` from
  `web/src/lib/pageRange.ts`, the mono `SHOWING x-y of total · CURSOR PAGINATED`
  footer, `isPlaceholderData` to disable the pager mid-fetch, and the
  loading/error/empty triad (skeleton panels, error card with Retry, empty card).
- The mutation pattern from `useWorkerActions.ts` + `WorkerActions.tsx`:
  invalidate-on-success, `busy` derived from `isPending` to disable triggers, a
  single inline error box rendered from the first non-null mutation error, and
  `ConfirmDialog` for destructive actions.
- `apiFetch` from `web/src/lib/api.ts` - already returns `undefined` for 204,
  which is what password-reset needs.

### API clients (exact calls)

```
listUsers({ sort, includeArchived, cursor, email })
                          -> GET   /v1/users?sort=&limit=50[&include_archived=true][&cursor=][&email=]
                                                                -> AdminUsersPage
createUser(body)          -> POST  /v1/users            json: { email, name, password, is_admin }
                                                                -> AdminUser (201)
renameUser(id, name)      -> PATCH /v1/users/{id}        json: { name }   -> AdminUser
archiveUser(id)           -> POST  /v1/users/{id}/archive         -> AdminUser
unarchiveUser(id)         -> POST  /v1/users/{id}/unarchive       -> AdminUser
resetUserPassword(email, newPassword)
                          -> POST  /v1/users/password-reset json: { email, new_password } -> void (204)
```

`limit=50` is passed explicitly, matching `listWorkers`, so the client's page
size is self-documenting.

## Query keys and invalidation

- List query key: `['users', sort, includeArchived, cursor, email]`.
- Every mutation invalidates the **bare `['users']` prefix**, so any page /
  sort / filter combination refetches. This is the decoupling lesson from
  `web/src/jobs/queryKeyDecoupling.test.tsx`: never invalidate a fully-qualified
  key that a sibling view does not share.
- `['users']` does not collide with anything: `AuthProvider` keeps the current
  user in React state, not in a query.
- No optimistic updates in this slice. Every mutation here returns the updated
  row (or 204) and the table is not polling, so invalidate-on-success is both
  simplest and correct. `useWorkerActions`'s optimistic disable/enable exists
  because a 3s poll made the pill lag; that pressure does not exist here.

## Interaction detail

| Control | Behaviour |
|---------|-----------|
| `include archived` toggle | Sets `include_archived=true` **and resets cursor/stack/offsets** (different row set and total). Archived rows render at reduced opacity with only an Unarchive action. |
| Email exact-match input | Debounced (300ms) `?email=` query. The server short-circuits before pagination, so while a filter is active the pager is hidden and the footer reads `1 of 1` or `0 of 0`. |
| Sort | Header-click toggle on `created_at` / `name` / `email` via the `toggleSort` helper shape in `WorkersPage.tsx:22-27`; changing sort resets the cursor (the server rejects a cursor whose sort key does not match: `internal/api/pagination.go`). |
| `+ Create user` | Toggles an inline `CreateUserForm` panel above the table (mirrors `WorkerEditForm`'s inline-toggle rather than a modal, since it is a multi-field form). Client-side: email required, password >= 8 chars, `is_admin` checkbox. On 409, show `That email is already registered.` inline. |
| Rename | Inline row edit: the row's name cell becomes an input with Save/Cancel; submits `PATCH { name }`. No confirm. |
| Reset pw | Opens `ResetPasswordDialog` (new password + confirm, >= 8 chars). Copy states: this revokes every session belonging to that user, and **if that user is you, you will be signed out immediately**. |
| Archive | `ConfirmDialog`, destructive variant: "Archive {email}? This revokes all of their API tokens, forces re-login, and disables their scheduled jobs." |
| Unarchive | `ConfirmDialog`, non-destructive: restores access; tokens are not restored, so they must sign in again. |
| Your own row | The Archive button is not rendered on the current user's row (the server 400s `cannot archive yourself`; rendering a guaranteed-failing button is a dead control). Rename and Reset pw stay available. |

Server-guard rejections that the UI cannot pre-empt (last-active-admin, already
archived, unknown id) surface in the shared inline error box using the API's
`error` string, exactly as `WorkerActions` does.

## Omitted from the Holo (unbacked data - do not fake)

Per the standing "omit unbacked data rather than fake it" rule:

| Holo element | Why omitted | Worth a backend enabler? |
|---|---|---|
| `SESSIONS` column ("n active") | No endpoint exposes a per-user token count. `GET /v1/auth/tokens` does not exist and, as scoped in `feature-2026-06-26-web-enabler-backend-endpoints`, would be **self-only**, not per-user. | Not now. A per-user session count is a new admin endpoint plus a `CountTokensForUser` query; low value versus the audit-log item already filed. |
| `LAST LOGIN` column | `users` has no `last_login_at` column and `userResponse` has no such field. | Maybe later - it is a schema change (column + write on login). Not filed; it overlaps `feature-2026-06-26-audit-log-admin-console-actions`. |
| `service` role in the role pill | Mock fiction. Relay's model is a single `is_admin` boolean; there is no service-account concept. Render exactly two values: `ADMIN` and `USER`. | No. |
| Role **change** action | No endpoint mutates `is_admin` after creation (verified: `updateUserRequest` is name-only). The create form exposes `is_admin`; existing users cannot be promoted or demoted from the UI. | **Yes - proposed, not filed.** A `PATCH /v1/users/{id}` extension accepting `is_admin` (with a last-active-admin guard mirroring archive) is a genuine gap. Flagged to the conductor for accept. |
| Header `VERSION` / `BUILD` / `DB` / `UPTIME` strip | No endpoint returns build or uptime facts. `GET /v1/health` returns `{"status":"ok"}` and `GET /v1/config` returns only `{allow_self_register}`. | Deferred to the Server/overview tab item, which is where a build-info endpoint belongs if we want one. |
| Count badges on the Invites / Enrollments / Reservations tabs | Those tabs are not in this slice. | n/a |
| `size: 50` page-size button in `PageFooter` | Page size is fixed at 50 here, as on every shipped list page. | No. |
| The gradient avatar square | **Kept** - purely derived from the email's first character, no data needed. |

## System-design, load, and security considerations

- **Load.** One list query, no polling, `limit=50`, cursor-paginated - the same
  shape as the shipped list pages. Bounded by design: the server caps `limit` at
  200 (`internal/api/pagination.go:206-207`). The email filter is debounced so
  typing does not fan out one request per keystroke.
- **Threat model.** The UI gate is cosmetic. Every route is `auth(admin(...))`;
  a non-admin who forges the client state gets 403s. No new endpoint, no new
  auth surface, no widening of an existing one.
- **Secret handling.** The only secret this tab handles is a new password typed
  by the admin. It is sent over the existing `apiFetch` (same-origin,
  `Authorization: Bearer`), never logged, never stored in query cache (mutation
  variables are not cached by TanStack in a way we read back), and the input is
  `type="password"`. No token is ever displayed by this slice - the
  token-on-create modal belongs to the Invites and Enrollments tabs.
- **Self-lockout footguns.** Two exist and both are handled: archiving yourself
  and archiving the last active admin are server-guarded (the UI hides the first,
  surfaces the second as an inline error); resetting your own password logs you
  out, which the dialog copy warns about.
- **Invariants.** No backend change, so the epoch fence, single job-spec
  pipeline, bounded-sender, identity-checked-teardown, interior-pointer, and
  single-JSON-entry-point invariants are untouched. The frontend analogue we do
  respect: all requests go through `apiFetch` (single fetch entry point); no
  component calls `fetch` directly.

## States and edge cases

- **Empty user list** - impossible in practice (you are authenticated as a user),
  but the empty card is still rendered for the filtered case ("No users match
  that email.").
- **Filter + sort together** - the server ignores sort and cursor on the
  `?email=` branch (it returns before `parsePage`). The UI hides the pager while
  a filter is active so the footer never claims a page it does not have.
- **Archived row with `include_archived` off** - cannot appear; see the
  `archived_at` note above.
- **Admin renames themselves** - the header `UserMenu` shows the email, not the
  name, so nothing visibly staleness-breaks. `AuthProvider`'s cached `user` is
  not refreshed by this mutation; acceptable, and called out here so it is not
  rediscovered as a bug.
- **409 on create** - inline field error, form state preserved.
- **404 on rename/archive of a row deleted concurrently** - inline error; the
  invalidation that follows any subsequent action reconciles the table.
- **New modals inherit no focus trap** - `ConfirmDialog` deliberately has none
  (`idea-2026-07-01-confirmdialog-focus-trap-hardening` tracks that). The new
  `ResetPasswordDialog` must at least match `ConfirmDialog`'s baseline:
  `role="dialog"`, `aria-modal`, labelled by its title, Escape to dismiss, focus
  the first field on open.

## Testing

Existing Vitest + MSW + `renderWithQuery` / `AuthProvider` harness.

**Routing and gating**
- `/admin` redirects to `/admin/users`.
- `/admin/bogus` redirects to `/admin/users`.
- A non-admin at `/admin/users` is redirected to `/jobs` and the shell renders no
  Admin nav link; an admin sees the link and the page.

**Shell**
- The tab bar renders the Users tab as active with the correct
  `aria-current`/active styling; unbuilt tabs are absent.

**List**
- Renders rows from a mocked envelope; role pill reads `ADMIN` / `USER` only.
- Toggling `include archived` sets `include_archived=true` on the request and
  resets the cursor.
- The email input debounces into one `?email=` request and hides the pager.
- Header click issues the expected `?sort=` value and resets the cursor.
- Pagination: next/prev walk the cursor stack and the footer range is computed by
  `computePageRange`; the pager is disabled while `isPlaceholderData`.
- Loading skeleton, error card with a working Retry, and filtered-empty card.

**Mutations** (`useAdminUserActions.test.tsx`, modeled on
`useWorkerActions.test.tsx`)
- Each of create / rename / archive / unarchive / resetPassword hits the exact
  method and path listed above, with the exact body.
- Each invalidates `['users']` (assert the bare prefix, per the
  query-key-decoupling lesson) and not a fully-qualified key.
- `resetUserPassword` handles a 204 with no body without throwing.
- The invalidation test mounts the list query via `renderHook` so an active
  observer exists (otherwise `refetchType: 'active'` never fires and the
  assertion passes vacuously).

**Confirm / form UX**
- Archive and Unarchive open `ConfirmDialog`; Cancel fires no request; Confirm
  fires exactly one.
- The Archive button is absent on the current user's own row.
- `ResetPasswordDialog` rejects a password under 8 characters client-side and a
  mismatched confirmation without issuing a request; the session-revocation
  warning text is present.
- A mutation error (e.g. 400 `cannot archive the last active admin`) renders
  inline and leaves the table mounted.
- Trigger buttons are disabled while a mutation is pending.

**Contract verification**
- TS types checked field-for-field against Go: `AdminUser` vs `userResponse`
  (`archived_at` nullable), the create body vs `createUserRequest`, the rename
  body vs `updateUserRequest` (name only), and the reset body vs the anonymous
  struct in `handleAdminPasswordReset` (`email`, `new_password`).

## Acceptance criteria

1. `/admin` redirects to `/admin/users`; `/admin/:tab` renders the shell; an
   unknown tab segment redirects to `/admin/users`. No `?tab=` query param is
   introduced.
2. Non-admins see no Admin nav entry and are redirected to `/jobs` if they
   navigate to `/admin/*` directly.
3. The shell renders the hi-fi Holo header (eyebrow + 32px title) and a
   pill-group tab bar built from a registry that a future tab joins with one
   entry.
4. The Users tab lists users from `GET /v1/users` with sort, cursor pagination
   (prev/next + `SHOWING x-y of total` footer), an `include_archived=true`
   toggle, and an exact-match `?email=` filter.
5. An admin can create a user (including setting `is_admin` at creation), rename
   a user, archive and unarchive a user, and reset a user's password; each action
   updates the table with no manual refresh.
6. Archive and Unarchive are behind `ConfirmDialog`; password reset is behind a
   dialog whose copy states that all of the target's sessions are revoked and
   that resetting your own password signs you out.
7. The Archive control is not rendered on the acting admin's own row; the
   last-active-admin rejection surfaces as an inline error rather than a crash.
8. The table renders no `SESSIONS`, `LAST LOGIN`, or `service`-role content, and
   exposes no role-change control (no backend support for any of them).
9. `npm test` and the production build are green; no source file outside
   `web/src/admin/`, `web/src/app/`, and `web/src/shell/HoloShell.tsx` changes;
   no backend change.

## Risks

- **Scope creep into the other four tabs.** The shell must stay a registry plus a
  switch. Building placeholder panels for the unbuilt tabs would ship four dead
  tabs; the registry-only approach is the guard.
- **`web/dist` is tracked but stale.** A frontend build dirties it; revert it
  before assembling the change set.
- **Modal accessibility debt compounds.** This slice adds a second dialog on top
  of an un-trapped primitive. It matches the existing baseline and does not make
  it worse, but the focus-trap idea should be scheduled before Profile adds a
  third.
