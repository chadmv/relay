# Admin Console - Agent Enrollments Tab - Design

Date: 2026-08-09
Status: Draft (autonomous cycle; conductor review)

## Overview

The second slice of the admin console. The shell (`/admin/:tab`, the registry-driven
pill tab bar, the `is_admin` route guard and nav filter) and the Users tab shipped on
2026-08-08 per `docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`.
This slice adds the **Agent enrollments** tab: list active enrollment tokens and
create new ones, with the raw token revealed clear-text exactly once.

Backlog item: `docs/backlog/feature-2026-08-08-admin-enrollments-tab.md`.
Frontend-only. No backend change, no new endpoint.

One thing is genuinely new here and drives most of the design: this is the first
surface in the SPA that **displays a credential it can never retrieve again**. The
rest is inherited.

## What is inherited, not re-derived

The following are already decided and shipped; this spec does not revisit them.

| Inherited | Source |
|---|---|
| `/admin/:tab` routing, `/admin` and unknown-tab redirects | `web/src/app/router.tsx`, `web/src/admin/AdminPage.tsx` |
| Admin gating (server `auth(admin(...))` is the boundary; `AdminRoute` + nav filter are UX) | `web/src/app/AdminRoute.tsx`, `web/src/shell/HoloShell.tsx` |
| Tab registration: one entry in `ADMIN_TABS` | `web/src/admin/tabs.ts` |
| Shell header (eyebrow + 32px title); no VERSION/BUILD/DB/UPTIME strip | `web/src/admin/AdminPage.tsx` |
| Tab bar with no count badges | `web/src/admin/AdminTabs.tsx` |
| List-page shape: cursor + `stack`/`offsets`, `computePageRange`, the mono `SHOWING x-y of total` footer, `isPlaceholderData` to disable the pager, loading/error/empty triad | `web/src/admin/users/UsersTab.tsx` |
| `keepPreviousData`, and **no `refetchInterval`** (not live data) | `web/src/admin/users/useAdminUsers.ts` |
| Mutation shape: invalidate the **bare** query-key prefix on success, no optimistic updates, `reset()` before reopening a form | `web/src/admin/users/useAdminUserActions.ts` |
| Header-click sorting via a `toggleSort` helper, `aria-sort` + caret | `web/src/admin/users/UsersTable.tsx` |
| Inline create panel (not a modal) for a multi-field create form | `web/src/admin/users/CreateUserForm.tsx` |
| Dialog a11y baseline: `role="dialog"`, `aria-modal`, `aria-labelledby` title, Escape dismisses, first field focused, **no focus trap** | `web/src/components/ConfirmDialog.tsx`, `web/src/admin/users/ResetPasswordDialog.tsx` |
| Single fetch entry point (`apiFetch`); nothing calls `fetch` directly | `web/src/lib/api.ts` |
| Omit unbacked hi-fi data rather than fake it | standing project rule |

Design source of truth is the hi-fi Holo (`design_handoff_relay_holo/hifi3-holo-pages.jsx`,
`AdminEnrollments` at line 2137, `AdminTokenModal` at line 2340). The
`reference/screens/admin.js` sketch is structure-only.

## Verified backend surface

Read from `internal/api/server.go:141-143`, `internal/api/agent_enrollments.go`, and
`internal/store/query/agent_enrollments.sql`. Both routes are
`auth(admin(http.HandlerFunc(...)))`, so a non-admin gets 403 regardless of the UI.

| Action | Method + path | Request | Success | Errors |
|---|---|---|---|---|
| Create enrollment | `POST /v1/agent-enrollments` | **JSON body required**: `{ hostname_hint?: string, ttl_seconds?: number }` | **201** `{ id, token, expires_at }` | 400 `invalid request body` (including an **empty** body); 400 `ttl_seconds must be at least 60`; 400 `ttl_seconds must not exceed 604800 (7 days)`; 500 `failed to create enrollment` |
| List enrollments | `GET /v1/agent-enrollments` | query only | 200 `page[...]` = `{ items, next_cursor, total }` | 400 from `parsePage` on a bad sort/cursor/limit; 500 `failed to list enrollments` / `failed to count enrollments` |

List query params (shared `parsePage` + `AgentEnrollmentsSortSpec`): `?sort=` one of
`created_at` / `expires_at` with an optional `-` prefix, default `-created_at`;
`?limit=` 1..200 default 50; `?cursor=`. All four sort arms are implemented
(`internal/api/agent_enrollments.go:162-217`).

Row shape, from `enrollmentRowToMap` (`internal/api/agent_enrollments.go:83-94`) and
its three sort-variant twins:

```
{ id, created_at, expires_at, created_by }        // created_by is a bare user UUID
{ ..., hostname_hint }                            // key ABSENT when unset, not null
```

Create response (`internal/api/agent_enrollments.go:68-72`):

```
{ id, token, expires_at }        // no created_at, no hostname_hint echoed back
```

`token` is the raw 64-char hex string. Only `tokenhash.Hash(rawHex)` is persisted
(`agent_enrollments.token_hash`); **there is no way to read the token back, ever**.

What "active" means, and what states exist. Every list and count query filters
`WHERE consumed_at IS NULL AND expires_at > NOW()`
(`internal/store/query/agent_enrollments.sql:14-65`). Therefore:

- **consumed** is not observable through this endpoint. `ConsumeAgentEnrollment` sets
  `consumed_at`/`consumed_by` (single-use: `WHERE consumed_at IS NULL`), and the row
  then simply disappears from the list.
- **expired** is not observable either: expired rows are filtered out, and
  `runEnrollmentJanitor` (`cmd/relay-server/main.go:245-258`, hourly ticker) deletes
  unconsumed expired rows outright.
- So the only server-asserted state for every returned row is "unconsumed and
  unexpired as of query time". `total` is the count of all active enrollments,
  unaffected by paging.

There is **no** `DELETE /v1/agent-enrollments/{id}`. The only removal paths are
consumption and the TTL sweep.

### Where the backlog item disagreed with the code

I wrote that item; three of its claims need correcting, and the code wins.

1. **Wrong.** The item names "the shared `AdminTokenModal` (line 2340)" as the
   token-reveal surface. `AdminTokenModal` in the hi-fi is the **create form** - a
   `hostname_hint` input, four `expires_in` preset buttons, a warning, Cancel /
   Enroll. It never renders a token. Its own warning copy says "copy it from the
   success toast", and no such toast exists in the hi-fi or anywhere in `web/src`
   (grep: zero `toast` matches). **The reveal surface is undesigned**, so this spec
   designs it (see "Token reveal" below).
2. **Incomplete.** The item gives "default 24h, max 7d" but omits the **minimum TTL
   of 60 seconds** and, more importantly, omits that `readJSON` is called
   unconditionally, so `POST` with **no body at all 400s**. A client must send at
   least `{}`.
3. **Incomplete.** The item's status-pill note says only `active` / `expiring` are
   derivable. Correct as far as it goes, but it misses that a row can be **observably
   expired client-side** once it has been sitting in a stale page past its
   `expires_at`. Three derived states, not two.

Correct in the item, verified: `POST`/`GET` paths and admin gating; the 201 body
`{ id, token, expires_at }`; the four sort keys; the row fields; that TOKEN PREFIX
and CREATED-BY-as-email are unbacked; that no revoke endpoint exists.

## Decisions

### 1. Revoke ships as nothing. No control, no endpoint.

Recommended and chosen: **omit**. Rationale:

- `DELETE /v1/agent-enrollments/{id}` does not exist. The project's standing rule is
  omit rather than fake, and the Users tab set the precedent one slice ago by
  shipping **no role-change control** because no endpoint could serve it. A button
  that is guaranteed to 405 is a dead control.
- Building the endpoint here would turn a frontend-only slice into a backend slice
  with its own migration-free store query, unit tests, and the integration assertion
  its item actually requires ("revoking does not disturb an already-enrolled worker").
  That is a separate reviewable unit and it already has an item:
  `docs/backlog/feature-2026-06-26-agent-enrollment-revocation.md`.
- Risk accepted, stated plainly: a leaked unconsumed token cannot be killed from the
  UI. Blast radius is bounded by three existing properties - single-use consumption,
  the TTL (min 60s, default 24h, max 7d), and `DELETE /v1/workers/{id}/token` for a
  worker that did enroll. The footnote copy tells the admin this, so the absence is
  explained rather than merely missing.

The tab's ACTIONS column therefore carries no controls. Instead of a column headed
`ACTIONS` filled with prose (the hi-fi's "consumed on first agent connect"), the
header is renamed **NOTE**: a header promising actions and delivering a sentence is
itself a dead affordance.

### 2. Reveal is shared; the create form is tab-local.

Split the hi-fi's single `AdminTokenModal` in two, along the line where the two
future consumers actually diverge.

- **`web/src/admin/TokenRevealDialog.tsx` - shared, built now.** The reveal half is
  byte-identical in purpose for enrollments and invites: one opaque string, a
  one-time warning, a copy affordance, an acknowledge button. It is also the part
  that carries the security invariants below, and there should be exactly one audited
  copy of them. It takes `{ token, title, endpoint, warning?, onDone }` and knows
  nothing about enrollments.
- **`web/src/admin/enrollments/CreateEnrollmentForm.tsx` - tab-local.** The create
  half diverges: enrollments take `hostname_hint` with 1h/24h/3d/7d presets, invites
  take an email that *binds* the invite with 24h/72h/7d/30d presets and a different
  endpoint. The hi-fi models this with an `isInvite` boolean, which is exactly the
  flag-driven component that rots. Keep it local, as `CreateUserForm` is local while
  `ConfirmDialog` is shared.

Location: the shared dialog lives at the `web/src/admin/` module root, alongside
`AdminPage.tsx` / `AdminTabs.tsx` / `tabs.ts`, which is already the admin-wide level.
No new directory for one file, and it is not general enough for `web/src/components/`.

Create form is an **inline panel**, not a modal, following `CreateUserForm`: it keeps
exactly one un-trapped dialog on screen at a time and adds no modal machinery for two
fields.

### 3. Token reveal: modal, no auto-dismiss, no backdrop-close.

The reveal replaces the hi-fi's nonexistent success toast. A toast is the wrong
primitive for an unrecoverable secret: auto-dismissal turns a glance away from the
screen into permanent data loss. A modal that must be acknowledged is correct.

- Opens iff `create.data` is truthy. The token is rendered from `create.data.token`
  and nowhere else (no copy into `useState`, which would be a second retention site).
- The warning is a `warn`-toned block above the token, in the hi-fi's words: the raw
  token is shown **once** and cannot be retrieved again.
- **Backdrop click does not close.** The hi-fi's `AdminTokenModal` closes on backdrop
  click, which is fine for a form and catastrophic for a secret. A stray click must
  not destroy the only copy.
- **Escape does close**, preserving the a11y baseline of the two shipped dialogs.
  Escape is a deliberate keypress, not a stray one, and keyboard users need an exit.
  Tradeoff acknowledged: an admin can lose the token by pressing Escape. Accepted -
  the alternative (trapping Escape) breaks the inherited baseline for every dialog in
  the app.
- Single primary button, `Done`, labelled to require acknowledgement ("Done - I have
  copied it").
- Focus on open: the token is a **readonly `<input>`** (not `type="password"` - the
  whole point is to display it) with `autoFocus`, `spellCheck={false}`,
  `autoComplete="off"`, and `onFocus` selecting its contents. This satisfies
  "focus the first field" and gives keyboard users select-all for free.
- Matches the inherited baseline: `role="dialog"`, `aria-modal="true"`,
  `aria-labelledby` the title. **No focus trap**, same as `ConfirmDialog` and
  `ResetPasswordDialog`. This makes three consumers of an un-trapped primitive;
  `docs/backlog/idea-2026-07-01-confirmdialog-focus-trap-hardening.md` should be
  scheduled before a fourth. It is materially worse here than for the other two: a
  focus escape from a dialog whose sole content is a credential means the credential
  can be tabbed past and the dialog lost.

### 4. Copy to clipboard: offered, feature-detected.

Offer it - the alternative is an admin hand-transcribing 64 hex characters, and a
mistyped token is indistinguishable from a leaked one at the failure site.

`navigator.clipboard.writeText` requires a **secure context**. `relay-server` serves
plain HTTP on `:8080` by default, so on a LAN-hosted `http://host:8080` the whole
`navigator.clipboard` object is `undefined`. Therefore:

- Feature-detect `navigator.clipboard?.writeText`. When present, render `Copy`; on
  success flip its label to `Copied` for 2s.
- When absent, **do not render** a Copy button (it could only fail); render a hint
  that the token must be selected and copied manually because the clipboard API needs
  HTTPS. The token input is selectable and pre-selected either way, so the insecure
  path still works.
- `document.execCommand('copy')` is not used as a fallback: deprecated, and it buys
  nothing over the already-selected input.
- Security note, accepted not mitigated: `writeText` puts the credential on the OS
  clipboard, which is readable by other local applications and may sync across
  devices. This is inherent to any copy affordance, the admin is about to paste it
  into `RELAY_AGENT_ENROLLMENT_TOKEN` regardless, and it is not warned about in the
  UI (over-warning trains people to dismiss warnings). It is recorded here.
- No clipboard clear-on-close: `writeText` cannot be reliably undone and clearing the
  clipboard out from under a user who is mid-paste is worse than the exposure.

This is the SPA's **first** clipboard consumer (grep: zero prior `navigator.clipboard`
uses), so the feature-detect belongs inside `TokenRevealDialog`, not spread into the tab.

### 5. Status is derived from `expires_at`, in three honest states.

Since the server returns active-only rows and no status field, the pill is computed
client-side by a pure function in `web/src/admin/enrollments/enrollmentStatus.ts`:

| State | Condition | Tone |
|---|---|---|
| `EXPIRED` | `now >= expires_at` | muted |
| `EXPIRING` | less than 1h remaining | `warn` |
| `ACTIVE` | otherwise | accent |

`EXPIRED` is reachable only for a row already on screen when its expiry passes: the
query never returns one. Rendering it as `ACTIVE` would be a lie the client can
disprove with arithmetic it already has.

No `CONSUMED` state. It is unobservable, and inventing it would be faking data.

Freshness without polling: the tab reads `useNow(60_000)` (new
`web/src/lib/useNow.ts`, sibling of `useDebouncedValue.ts`) - a local 60s tick that
re-renders to recompute labels and pills and issues **no network request**. The
inherited no-poll decision stands; a stale `EXPIRING` pill is a UI bug, a 3s poll is
pointless load.

The `EXPIRES` column shows relative time ("in 21h"), which needs a new
`formatTimeUntil(iso, now)` in `web/src/lib/time.ts`. The existing
`formatRelativeTime` is past-only (`Math.max(0, now - iso)` and an "ago" suffix) and
would render every future expiry as `0s ago`. Same injectable-`now` signature.

### 6. Sorting uses clickable headers, not the hi-fi's dropdown.

The hi-fi uses a `SortControl` dropdown with four options. Use the shipped
header-click pattern instead: identical sort keys, `toggleSort` and the caret /
`aria-sort` treatment already exist in `UsersTable.tsx`, and introducing a
`SortControl` primitive that nothing else in the app uses is a second sorting idiom
for one screen. Consequence: a `CREATED` column is added (it is the default sort key
and needs a clickable header), which also fills the visual space freed by dropping
two unbacked columns.

## Architecture

New feature module `web/src/admin/enrollments/`, mirroring `web/src/admin/users/`.

New files:

- `web/src/admin/enrollments/api.ts` - `AgentEnrollment`, `AgentEnrollmentsPage`,
  `EnrollmentSort`, `CreateEnrollmentBody`, `CreateEnrollmentResponse` types plus the
  two clients.
- `web/src/admin/enrollments/useAgentEnrollments.ts` - list query,
  `placeholderData: keepPreviousData`, no `refetchInterval`.
- `web/src/admin/enrollments/useAgentEnrollmentActions.ts` - returns `{ create }`.
  Named for the sibling's convention even with one mutation, so the revoke follow-up
  is an addition rather than a rename.
- `web/src/admin/enrollments/EnrollmentsTab.tsx` - control row, create panel, table,
  footer, footnote, reveal dialog.
- `web/src/admin/enrollments/EnrollmentsTable.tsx` - the table.
- `web/src/admin/enrollments/CreateEnrollmentForm.tsx` - inline create panel.
- `web/src/admin/enrollments/enrollmentStatus.ts` - pure `deriveStatus(expiresAt, now)`.
- `web/src/admin/TokenRevealDialog.tsx` - shared reveal dialog.
- `web/src/lib/useNow.ts` - 60s tick hook.

Modified files:

- `web/src/admin/tabs.ts` - one `ADMIN_TABS` entry
  `{ slug: 'enrollments', label: 'Agent enrolls', Panel: EnrollmentsTab }`, placed
  after `users` per the hi-fi's tab order (Invites is still absent), and its comment
  block updated to drop this item from the not-yet-built list.
- `web/src/lib/time.ts` - add `formatTimeUntil`.

Reused, not rebuilt: `GlassPanel`, `PillButton`, `Chip` (which already has the
`accent` / `muted` / `warn` tones the three states need), `Field`, `Input`,
`computePageRange`, `apiFetch`, `ApiError`.

### API clients (exact calls)

```
listAgentEnrollments({ sort, cursor })
    -> GET  /v1/agent-enrollments?sort=&limit=50[&cursor=]   -> AgentEnrollmentsPage

createAgentEnrollment({ hostname_hint, ttl_seconds })
    -> POST /v1/agent-enrollments   json: { hostname_hint, ttl_seconds }
                                                            -> CreateEnrollmentResponse (201)
```

`json` is **always** passed, even when `hostname_hint` is blank, because the handler
calls `readJSON` unconditionally and an absent body 400s. `ttl_seconds` is always the
selected preset's literal value (3600 / 86400 / 259200 / 604800), never omitted and
never `0`: relying on the server's zero-means-default branch hides the actual TTL from
the request log and from the test assertions. Presets only, no free-form TTL field, so
the 60s / 7d bounds are unreachable from this UI and need no client validation.

`hostname_hint` is typed **optional** (`hostname_hint?: string`), not
`string | null`: `enrollmentRowToMap` omits the key entirely when the column is NULL.

### Query keys and invalidation

- List key: `['agent-enrollments', sort, cursor]`.
- `create` invalidates the **bare `['agent-enrollments']` prefix** on success, so
  every mounted sort/page combination refetches. Never a fully-qualified key
  (`web/src/jobs/queryKeyDecoupling.test.tsx`).
- No optimistic append: the 201 does not echo `created_at` or `hostname_hint`, so a
  locally synthesized row would be partly invented.
- `['agent-enrollments']` collides with nothing today.

### Interaction detail

| Control | Behaviour |
|---|---|
| `+ Enroll agent` | Toggles the inline `CreateEnrollmentForm`. Calls `create.reset()` first, so a stale error (or a stale token) never reappears. |
| Create form | `hostname_hint` (optional, free text, placeholder `farm-west-13`) and a four-button TTL preset group, 24h preselected to match both the server default and the hi-fi. Submit is disabled while pending. |
| On 201 | The inline panel closes and `TokenRevealDialog` opens. The list is invalidated in the same `onSuccess`. |
| Reveal `Done` | Calls `create.reset()`, which clears `create.data` and unmounts the dialog in one step. |
| Sort | Header click on `CREATED` / `EXPIRES` only (the two keys the server accepts), resetting the cursor. |
| Pager | prev / next over the cursor stack, disabled while `isPlaceholderData`. |
| Errors | 400 from a bad TTL cannot occur from this UI; any create error renders inside the create panel (as `CreateUserForm` does). List errors use the inherited error card with Retry. |

Footnote copy, extending the hi-fi's: enrollments bootstrap a `relay-agent`
(`RELAY_AGENT_ENROLLMENT_TOKEN` on first boot, exchanged for a long-lived agent
token, single use); this list shows **active only**, so a consumed or expired
enrollment disappears rather than changing state; and there is no revoke endpoint in
v1, so expiry or consumption are the only terminal states.

## Omitted from the hi-fi

| Hi-fi element | Why omitted | Backend enabler? |
|---|---|---|
| `TOKEN PREFIX` column | Only `tokenhash.Hash(rawHex)` is stored; no prefix column exists and nothing returns one. | **Propose, do not file:** fold a `token_prefix` (first 8 hex chars) column into the revocation item, so a revoke UI can identify a row by something other than an opaque UUID. Revealing 8 of 64 hex chars leaves 56 unknown, so the hash is not meaningfully weakened. |
| `CREATED BY` as an email | `created_by` is a bare user UUID with no join to `users`. A 36-char opaque UUID column is real data but useless and wide, so the column is dropped entirely rather than shown raw. | **Propose, do not file:** a `created_by_email` enricher, folded into `feature-2026-06-26-web-enabler-backend-endpoints`. |
| A `consumed` status | Structurally unobservable: every query filters `consumed_at IS NULL`. | No. Observing consumption means a history/audit view, which is `feature-2026-06-26-audit-log-admin-console-actions`. |
| Revoke row action | `DELETE /v1/agent-enrollments/{id}` does not exist. See decision 1. | Already filed: `feature-2026-06-26-agent-enrollment-revocation`. |
| `ACTIONS` header | Renamed `NOTE`; the cell holds prose, not controls. | n/a |
| Success toast | No toast primitive exists in the SPA, and auto-dismiss is wrong for an unrecoverable secret. Replaced by `TokenRevealDialog`. | No. |
| `SortControl` dropdown | Replaced by the shipped clickable-header idiom. See decision 6. | No. |
| Free-form TTL input | Presets only, as the hi-fi has. The CLI and API remain for unusual TTLs. | No. |
| Tab count badge | `AdminTabs` renders no counts by inherited decision; the footer already shows the total. | No. |
| `size: 50` footer button | Page size is fixed at 50 on every shipped list page. | No. |
| Relative `EXPIRES` staleness | Kept, via the 60s local tick. | n/a |

## Security and system design

- **Threat model.** Both routes are `auth(admin(...))`. The UI gate is cosmetic; a
  forged client state yields 403s. No new endpoint, no widened surface.
- **The token is a bearer credential** that mints a long-lived agent token, so leakage
  means a rogue worker joining the fleet. It is bounded by single-use consumption
  (`WHERE consumed_at IS NULL`, so a race resolves to exactly one consumer) and by the
  TTL. The footnote states that a short TTL is the safer choice; the default stays 24h
  to match the server rather than inventing UI-only policy.
- **Secret lifetime, precisely.** The token exists in exactly two places: the 201
  response body, and `create.data` in the mutation's state. TanStack retains a
  mutation's `data` and `variables` for the mutation's lifetime, so `create.reset()`
  on dialog dismissal is **load-bearing**, not tidiness. It is called on `Done`, on
  Escape, and before the create panel is reopened. The token is never copied into
  component state, never written to a query key or query cache (it is a mutation
  result, and no query fetches it), and no query persister is configured
  (`web/src/lib/queryClient.ts`), so nothing reaches `localStorage` or IndexedDB.
  `localStorage` in this app holds only the session token and a view preference; the
  enrollment token never goes near it.
- **Never in a URL.** No route, no query param, no path segment carries the token. The
  reveal is not linkable or bookmarkable by construction, so it cannot leak into
  browser history, a `Referer` header, or a proxy log. The list endpoint never returns
  it, so it is unrecoverable by design and not merely by convention.
- **Never logged.** No `console.*` call touches it. Note the `apiStream` precedent
  comment ("never log a frame") - same discipline, enforced by test.
- **Load.** One list query, no polling, `limit=50`, cursor-paginated, `total` from an
  indexed count. One 60s local timer, cleared on unmount, that issues no request.
  `POST` is not rate-limited (only login and register are, per
  `internal/api/ratelimit.go`), so an admin can mint unbounded active enrollments;
  acceptable, since an admin already holds strictly greater powers, and the hourly
  janitor sweeps expired rows.
- **Invariants.** No backend change, so the epoch fence, single job-spec pipeline,
  bounded-sender, identity-checked-teardown, interior-pointer, and single-JSON-entry
  invariants are untouched. The frontend analogue holds: every request goes through
  `apiFetch`.

## Testing

Existing Vitest + MSW + `renderWithQuery` / `AuthProvider` harness. Mirror the eight
`web/src/admin/**/*.test.tsx` files.

**Pure units** (`enrollmentStatus.test.ts`, `time.test.ts`, `useNow.test.ts`)
- `deriveStatus` boundaries with a fixed `now`: exactly at `expires_at` is `EXPIRED`;
  59m59s left is `EXPIRING`; 1h00m01s is `ACTIVE`.
- `formatTimeUntil` renders a future instant as "in 21h" and does **not** fall through
  to `formatRelativeTime`'s past-only clamp. Positive control: the same input through
  `formatRelativeTime` yields `0s ago`, proving the new function is what fixed it.
- `useNow` advances on a fake 60s tick and clears its interval on unmount.

**Contract, field-for-field against Go**
- `AgentEnrollment` vs `enrollmentRowToMap`: `hostname_hint` is `?: string` and a row
  lacking the key parses and renders the em-less placeholder without `undefined`
  leaking into the DOM.
- `CreateEnrollmentResponse` vs the 201 map: exactly `id`, `token`, `expires_at`.
- `createAgentEnrollment` always sends a body with `content-type: application/json`.
  **Non-vacuous because** the MSW handler for the positive control rejects an absent
  or unparseable body with 400 `invalid request body`, mirroring `readJSON`; the test
  fails if the client stops sending one.
- `ttl_seconds` is the exact preset literal (assert `86400` for the default), never
  `0` and never absent.

**List**
- Rows render from a mocked envelope; sort header clicks issue the exact `?sort=`
  values `created_at` / `-created_at` / `expires_at` / `-expires_at` and reset the
  cursor.
- Pagination walks the cursor stack, the footer range comes from `computePageRange`,
  the pager is disabled while `isPlaceholderData`.
- Loading skeleton, error card with a working Retry, and the empty card ("No active
  enrollments").
- **No-poll, non-vacuous:** with the query mounted, advance fake timers several
  minutes and assert the MSW handler hit count stays 1, having first proved the
  counter can move (an explicit `refetch()` takes it to 2). The 2026-08-08 review
  caught a vacuous version of exactly this test on the Users tab.
- The 60s tick flips a pill from `EXPIRING` to `EXPIRED` with **zero** additional
  requests (same hit-count instrument).

**Create + invalidation**
- `create` hits `POST /v1/agent-enrollments` once with the exact body.
- It invalidates the **bare** `['agent-enrollments']` prefix. The test mounts the list
  query via `renderHook` so an **active observer** exists; a `fetchQuery` seed leaves
  none, `refetchType: 'active'` never fires, and the assertion passes vacuously.
- A create error renders inside the create panel and leaves the table mounted.

**Reveal dialog**
- Opens on 201 and shows the token; the one-time warning text is present;
  `role="dialog"`, `aria-modal="true"`, `aria-labelledby` resolves to the title;
  focus lands on the token input with its contents selected.
- Backdrop click does **not** close it and the token is still displayed. Paired
  positive control: Escape **does** close it, proving the harness can close it at all
  and that the backdrop assertion is not passing because nothing can close it.
- `Done` closes it and the token string is absent from the DOM afterwards. Paired
  positive control: the same query finds the token **while** the dialog is open, so
  the absence assertion is not passing against a broken selector.
- After dismissal, no entry in `queryClient.getQueryCache().getAll()` has data
  containing the token, and the mutation's `data` is `undefined` (i.e. `reset()` ran).

**Token never leaks - and the traps in asserting it**
- **Console:** spy on `log` / `warn` / `error` / `info` / `debug` and assert no call
  carries the token. The matcher must **not** use `JSON.stringify(args)`: an `Error`
  carrying the token in its `message` stringifies to `{}`, so such a test is blind to
  the most likely real failure. Serialize each argument with a walker that reads
  `name`, `message`, and `cause` off `Error` instances in addition to strings and
  plain objects. **Prove RED with the representation the real failure takes:** a
  positive control that calls `console.error(new Error(token))` and asserts the
  matcher reports it dirty. A control using a bare string only proves the string path.
- **URL:** assert every request MSW intercepted has a full URL (pathname + search)
  free of the token, and that no navigation occurred carrying it. Positive control: a
  deliberate `fetch('/v1/x?token=' + token)` is reported dirty by the same matcher.
- **Storage:** assert `localStorage` and `sessionStorage` contain no value containing
  the token after the flow. Positive control: writing it manually is detected.
- **Clipboard:** with `navigator.clipboard.writeText` stubbed, `Copy` calls it with
  exactly the token and the label flips to `Copied`. With `navigator.clipboard`
  deleted, the `Copy` button is **absent** and the manual-copy hint is present; paired
  positive control asserts the button **is** present when the API exists, so the
  absence assertion cannot pass on a typo'd query.

**Shell integration**
- `/admin/enrollments` renders the tab and the tab bar marks it active
  (`aria-current="page"`); `/admin/users` still renders the Users tab; the registry
  gained exactly one entry.

## Acceptance criteria

1. `/admin/enrollments` renders inside the existing admin shell for an admin, is
   marked active in the tab bar, and is reached by a single new `ADMIN_TABS` entry;
   non-admins are still redirected to `/jobs` and see no Admin nav entry.
2. The tab lists active enrollments from `GET /v1/agent-enrollments?sort=&limit=50`
   with header-click sorting on `CREATED` and `EXPIRES` only, cursor prev/next, and
   the `SHOWING x-y of total · /v1/agent-enrollments (active only) · CURSOR PAGINATED`
   footer. It issues no periodic refetch.
3. Columns are `HOSTNAME HINT | CREATED | EXPIRES | STATUS | NOTE`. No `TOKEN PREFIX`,
   no `CREATED BY`, and no revoke or other row control anywhere in the tab.
4. The `STATUS` pill renders only `ACTIVE`, `EXPIRING` (under 1h), or `EXPIRED`
   (`now >= expires_at`), derived from `expires_at`. No `CONSUMED` state exists. Pills
   and relative labels stay correct across a 60s local tick with no network request.
5. Creating an enrollment posts a JSON body containing `hostname_hint` and an explicit
   `ttl_seconds` preset (3600 / 86400 / 259200 / 604800, default 86400), and
   invalidates the bare `['agent-enrollments']` prefix so the list refreshes with no
   manual reload.
6. On 201, a shared `TokenRevealDialog` shows the raw token clear-text with a warning
   that it is shown once and cannot be retrieved again. Backdrop click does not
   dismiss it; Escape and `Done` do; `Done` calls `create.reset()`.
7. The dialog offers `Copy` only when `navigator.clipboard.writeText` exists, and
   otherwise shows a manual-copy hint. The token is always rendered in a readonly,
   focused, pre-selected input.
8. After dismissal the token appears in no DOM node, no query-cache entry, no
   `localStorage` / `sessionStorage` value, no request URL, and no `console.*` call -
   each proven by a test with a paired positive control, and the console test's matcher
   detects a token carried inside an `Error`.
9. The reveal dialog matches the inherited dialog a11y baseline (`role="dialog"`,
   `aria-modal`, labelled by its title, first field focused).
10. `npm test` and the production build are green; changes are confined to
    `web/src/admin/`, `web/src/lib/useNow.ts`, and `web/src/lib/time.ts`; no backend
    change; `web/dist` is reverted before the change set is assembled.

## Risks

- **The reveal dialog is the highest-consequence component in the slice.** A bug that
  drops the token or dismisses the dialog early is unrecoverable for the admin. The
  no-backdrop-close rule and the `create.data`-as-single-source rule are the two
  things a reviewer should check first.
- **Focus-trap debt is now materially worse.** Three consumers of an un-trapped
  dialog, and one of them holds a credential that can be tabbed away from. Schedule
  `idea-2026-07-01-confirmdialog-focus-trap-hardening` before the Invites tab makes it
  four.
- **Secret-absence tests are the easiest thing here to write vacuously.** Every
  absence assertion in this spec is specified with a required positive control, in the
  representation the real failure would take. A plan that drops those controls ships a
  green suite that proves nothing.
- **`web/dist` is tracked but stale**; a frontend build dirties it.
- **Scope creep into revocation.** The endpoint is one small handler away and will be
  tempting. It is a separate item with an integration-test requirement this slice
  cannot satisfy.
