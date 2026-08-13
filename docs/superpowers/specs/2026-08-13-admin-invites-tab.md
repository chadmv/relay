# Admin console: Invites tab - Design

Date: 2026-08-13
Status: Draft (autonomous cycle; conductor review)

## Overview

The fifth and last admin-console tab: create an invite (optional email binding, choice of
TTL), reveal the raw token clear-text exactly once, and list every invite with a
client-derived state pill.

Backlog item: `docs/backlog/feature-2026-08-08-admin-invites-tab.md`. **Its title is
stale** - it still reads "(create now, list blocked on GET /v1/invites)", while its body
(`:30-39`) records that the list endpoint shipped on 2026-08-13. The title must be
corrected when the item is closed in Phase 6; the item itself has no remaining backend
dependency.

Frontend only. No Go file is touched. Seven new files under `web/src/admin/invites/`, one
registry entry, one additive tone on a shared primitive, and additive edits to two shipped
test files.

Written in autonomous gate mode: every design question below is decided here with a
one-line rationale in the Decisions section rather than asked.

## Where the backlog item and the neighbouring docs are wrong, incomplete, or right

Every claim below was re-derived from the tree at HEAD (`b971a0b`), not taken from the
item, the roadmap, or the preceding spec. The standing rule is that authorship is not
evidence, and the preceding slice's spec was written by a predecessor of this one.

1. **Verified correct: the whole `GET /v1/invites` contract.** Route registered at
   `internal/api/server.go:143` as `auth(admin(http.HandlerFunc(s.handleListInvites)))`,
   directly beside the `POST` at `:142`. Handler `internal/api/invites.go:148-250`.
   `InvitesSortSpec` (`invites.go:97-103`) allowlists `created_at` and `expires_at` with
   default `-created_at`, and all **four** dispatch arms exist (`:160, :180, :200, :220`),
   so both directions of both keys are live. Items are built by the single shared
   `inviteEntry` (`invites.go:125-146`), which emits `id`, `created_at`, `expires_at`,
   `created_by`, `created_by_email` always, and `email` / `used_at` **only when set**
   (`:139-144`). Envelope is `page[map[string]any]` -> `{items, next_cursor, total}`
   (`:249`).
2. **Verified correct: there is no `status` field and there must not be one.** The
   handler's own comment says so at `internal/api/invites.go:112-121`, and the query
   projection (`internal/store/query/invites.sql:31-32`) selects seven columns, none of
   them a status. The four states are the client's arithmetic.
3. **Verified correct: no token, hash, or prefix can be returned.** `invites.sql:22-25`
   states the omission of `i.token_hash` from the projection is the endpoint's entire
   security control, and the four page queries (`:31, :46, :67, :79`) each enumerate
   columns with `token_hash` absent. Only `tokenhash.Hash(rawHex)` is ever persisted
   (`internal/api/invites.go:56`).
4. **Verified correct: there is no revoke and no delete endpoint for invites.** Grep of
   `internal/api/server.go` for `invites` returns exactly two registrations, `:142` and
   `:143`. No `DELETE /v1/invites/{id}`, no `PATCH`, no resend. The store has no delete
   query either: `invites.sql` holds `CreateInvite`, `GetInviteByTokenHash`,
   `MarkInviteUsed`, four page queries and `CountInvites`, and nothing else.
5. **Verified correct: the `POST` contract.** `internal/api/invites.go:16-88`. Body
   `{email?, expires_in?}`; `expires_in` parsed by `time.ParseDuration` (`:34`), default
   `72h` (`:31`), must be positive (`:39-42`), max `720h` with the message
   `expires_in exceeds maximum of 720h` (`:43-47`). A non-empty `email` is validated with
   `mail.ParseAddress` (`:66`). Response is **201** (`:87`) with
   `{id, token, expires_at}` plus `email` only when bound (`:79-86`).
6. **Newly established, and a live trap: `readJSON` is called unconditionally**
   (`invites.go:27`), so a `POST` with no body decodes as `io.EOF` and 400s. A body is
   always sent, exactly as `web/src/admin/enrollments/api.ts:94-101` already documents for
   the sibling endpoint.
7. **Newly established, and a live trap: Go duration strings have no day unit.**
   `time.ParseDuration` accepts `h`, `m`, `s` and smaller. The backlog item's proposed
   presets are labelled `24h / 72h / 7d / 30d` (`feature-2026-08-08-admin-invites-tab.md:44-45`).
   Sending the literal `"7d"` or `"30d"` 400s. The wire values must be `"168h"` and
   `"720h"`. This is not stated anywhere in the item, the README, or the preceding spec.
8. **`docs/superpowers/specs/2026-08-13-web-enabler-list-endpoints.md:229-241` is correct**
   about the derivation and its precedence, and `README.md:1282-1305` documents the same
   contract in the shipped API reference. The README is the citation of record for the
   1h window (`:1300-1303`), which this spec does not invent.
9. **A shipped comment is now wrong, and a wrong contract in a comment is a defect.**
   `web/src/admin/tabs.ts:16-18` says the Invites tab "stays blocked on a GET /v1/invites
   that does not exist". It exists. That comment is corrected in this slice, not left for
   the next reader.
10. **The roadmap's registry claim is correct.** `ROADMAP.md:162-163` says the tab is "one
    registry entry plus its panel; it does not touch routing or gating". Verified:
    `AdminTabs.tsx:12` renders the bar by mapping `ADMIN_TABS`, and `AdminPage.tsx:20-23`
    resolves the `:tab` param through `findAdminTab` and redirects to the default when it
    misses. Adding the entry is sufficient and nothing else gates the tab.
11. **`TokenRevealDialog` moved.** The brief cites `web/src/components/dialog/TokenRevealDialog.tsx`;
    the file is at **`web/src/admin/TokenRevealDialog.tsx`** and composes
    `web/src/components/dialog/DialogShell.tsx`. Its own header comment (`:30-32`) already
    names invites as its intended second consumer.

## Verified backend contract

### GET /v1/invites (`internal/api/server.go:143`)

Admin-gated. 401 unauthenticated, 403 non-admin, 200 admin.

| Key | Type | Presence | Source |
|---|---|---|---|
| `id` | string (UUID) | always | `invites.id` |
| `created_at` | RFC3339 | always | `invites.created_at` |
| `expires_at` | RFC3339 | always (`NOT NULL`) | `invites.expires_at` |
| `created_by` | string (UUID) | always | `invites.created_by` |
| `created_by_email` | string | always | inner `JOIN users` (`invites.sql:32`) |
| `email` | string | **omitted** when not email-bound | `invites.email` |
| `used_at` | RFC3339 | **omitted** when unredeemed | `invites.used_at` |

Envelope `{items, next_cursor, total}` (`internal/api/pagination.go:288-293`). Query
parameters: `limit` (default 50, max 200), `cursor`, `sort`. `sort` accepts `created_at`,
`-created_at`, `expires_at`, `-expires_at`; anything else is a 400 naming the supported
keys and the path. **No filter parameter exists**, so every state is returned and `total`
is the unfiltered `COUNT(*)` (`invites.sql:55-61`).

Optional keys are **absent, not null**. In TypeScript that means `email?: string` and
`used_at?: string`, and a check written as `used_at !== null` is a compile error rather
than a silently-always-true condition.

### POST /v1/invites (`internal/api/server.go:142`)

Body `{email?: string, expires_in?: string}`. A body is mandatory. `expires_in` is a Go
duration; default `72h`; must be positive; max `720h` inclusive (`> maxInviteDuration` at
`invites.go:44`, so `720h` exactly is accepted).

201 response `{id, token, expires_at}` plus `email` when bound. **`token` is the raw 64-char
hex credential and is unrecoverable after this response.**

## What is inherited from the Agent enrollments tab, not re-derived

The Enrollments tab (`web/src/admin/enrollments/`, shipped 2026-08-09) is the same shape:
admin-gated, token-issuing, expiry-bearing, create-plus-list. Everything in this table is
copied structurally with the nouns changed, and is not re-argued here.

| Element | Inherited from | Note |
|---|---|---|
| Tab composition (control row, inline create panel, body, footer, footnote, reveal dialog) | `EnrollmentsTab.tsx:163-221` | |
| `toggleSort` semantics | `EnrollmentsTab.tsx:16-21` | Clicking the active column flips it; clicking another selects it ascending. |
| Cursor stack + `offsets` + `computePageRange` pager | `EnrollmentsTab.tsx:28-74, 84-88` | Sort change resets paging, because the server rejects a cursor issued under another sort (`pagination.go:272-286`). |
| `keepPreviousData`, **no `refetchInterval`** | `useAgentEnrollments.ts:14-20` | This is not live data; polling it is pointless load. |
| Bare-prefix `invalidateQueries` | `useAgentEnrollmentActions.ts:34` | Never a fully-qualified key. |
| `useNow(60_000)` local tick | `EnrollmentsTab.tsx:36` | Re-renders for pill freshness; issues no request. |
| `Table` / `TableRow` / `TableCell` | `components/holo/Table.tsx:75-155` | Used as-is. Owns roles, `aria-sort`, the sort button and the grid template. |
| `TokenRevealDialog` | `admin/TokenRevealDialog.tsx:68` | Used as-is, second consumer, no prop added. |
| Secret-leak matchers | `web/src/test/secretLeaks.ts` | Reused; not re-implemented. |
| `-` placeholder for an absent optional string | `EnrollmentsTable.tsx:58-60` | Plain ASCII hyphen. |
| `slice(0, 10)` date cells | `EnrollmentsTable.tsx:61` | |
| Terminal rows dimmed `opacity-[0.55]` | `EnrollmentsTable.tsx:52` | |

Three things the retro settled that are carried over as requirements rather than
rediscovered: `gcTime: 0` on the create mutation, no revoke control when no endpoint
exists, and client-side state derivation. Each gets its own section below.

## The state derivation

New module `web/src/admin/invites/inviteStatus.ts`, mirroring
`web/src/admin/enrollments/enrollmentStatus.ts` in shape and differing in state set.

```
export type InviteStatus = 'REDEEMED' | 'EXPIRED' | 'EXPIRING' | 'ACTIVE'
```

`deriveStatus(invite: {expires_at: string; used_at?: string}, now: Date)` resolves in this
order, and the order is load-bearing:

| Order | Pill | Predicate | Why it sits here |
|---|---|---|---|
| 1 | `REDEEMED` | `used_at !== undefined` | Redemption is terminal and one-way: `MarkInviteUsed` is the only writer and carries `AND used_at IS NULL` (`invites.sql:9-12`), called once from registration (`internal/api/auth.go:147-158`). A redeemed invite that later passes its expiry is still redeemed. Checked first, per `README.md:1300-1301`. |
| 2 | `EXPIRED` | `expires_at - now <= 0` | Boundary is `<= 0` on the raw millisecond delta, byte-identical to `enrollmentStatus.ts:23`, so the pill and the `formatTimeUntil` cell flip at the same instant (`web/src/lib/time.ts:16-26`). |
| 3 | `EXPIRING` | `expires_at - now < 1h`, strictly | The window is one hour: `README.md:1300-1303` documents it as the shipped contract, and `enrollmentStatus.ts:5` is the shipped constant. **Not invented here.** 59m59s is `EXPIRING`; exactly 1h00m00s is `ACTIVE`. |
| 4 | `ACTIVE` | otherwise | |

The precedence question the item does not answer, answered: **a redeemed invite that is
also past its expiry reads `REDEEMED`, never `EXPIRED`.** Both facts are on the row and
both are true; redemption is the one that describes what happened to the credential, and
expiry of an already-spent invite is a non-event. This is pinned by a dedicated test, and
the backend spec's integration suite already pins the data-level half of it
(`2026-08-13-web-enabler-list-endpoints.md:565-566`).

`EXPIRING` is the only state that is not a server fact. It reads the browser clock, so a
badly skewed client mislabels a row - accepted for the same reason
`enrollmentStatus.ts:19-20` accepts it, because the server exposes no status to prefer
instead. Note this makes a **third** clock in the system, after the app host and the
database clocks that `docs/backlog/bug-2026-08-13-token-expiry-two-clocks.md` covers; that
item is not widened here, but the skew is real and is stated in Risks.

The `EXPIRES` cell uses a local `formatExpiryLabel` that collapses any sub-minute label to
`in <1m`, duplicating `enrollmentStatus.ts:45-48` verbatim including its reasoning comment:
the row's `now` ticks once a minute, so a seconds-precision label promises a freshness the
refresh cadence does not deliver.

### Tones

`Chip` ships three tones (`components/holo/Chip.tsx:8-12`): `accent`, `muted`, `warn`.
Four states need four. **One additive key** is added:

```
err: 'border border-err/40 bg-err/10 text-err'
```

That class string is the idiom already used at seven call sites (`LoginScreen.tsx:62`,
`JobActions.tsx:65`, `UsersTable.tsx:25`, and others), and `PillButton.tsx:11` already
carries the sibling `danger` tone. No existing `Chip` consumer changes; the addition is one
key plus one assertion appended to `Chip.test.tsx`.

Mapping: `ACTIVE` -> `accent`, `EXPIRING` -> `warn`, `EXPIRED` -> `err`, `REDEEMED` ->
`muted`. This matches the hi-fi's own colour assignment
(`design_handoff_relay_holo/hifi3-holo-pages.jsx:2101`, which uses `C.err` for expired and
`C.fgMute` for redeemed). Colour is never the only channel: the pill text differs and the
row dimming applies to both terminal states.

## The token-retention rule, and how it is tested

The raw invite token is a credential that grants account creation. It exists in plaintext
exactly once, in the 201 body (`invites.go:80-82`).

**Requirements, all structural:**

1. `useInviteActions.ts` sets **`gcTime: 0`** on the create mutation, with the reasoning in
   a comment at the site so it is not deleted as redundant (`useAgentEnrollmentActions.ts:10-24`
   is the model). `reset()` alone only detaches the observer; the underlying `Mutation`
   stays in `queryClient.getMutationCache()` for the default 5-minute `gcTime`. `gcTime: 0`
   makes the now-observer-less mutation eligible for removal on the next tick. Both are
   needed: `gcTime: 0` cannot evict while an observer is attached, and `reset()` alone
   cannot evict at all.
2. The token is rendered **only** from `create.data.token`, passed straight into
   `TokenRevealDialog`, never copied into component state. One retention site, one
   destruction point.
3. `create.reset()` is called at exactly three sites, mirroring `EnrollmentsTab.tsx:174, 189, 217`:
   on the reveal dialog's `onDone`, on create-panel cancel, and before reopening the create
   panel (otherwise a previous create's `data` re-opens the dialog).
4. No `console.*` on the create path, and no token in a URL, route, query param, or query
   key.

**The test rule, and it is not the obvious one.** The secrecy suite must assert the
**mutation cache is empty** after dismissal:

```
await waitFor(() => expect(client.getMutationCache().getAll()).toHaveLength(0))
```

not that one field of one entry is clean. The 2026-08-12 profile-pages slice found a
plaintext secret surviving in the **settled mutation's `mutationFn` closure**, which
`@tanstack/query-core`'s `mutationObserver` builds from `this.options` and does not replace
on post-success re-renders; `JSON.stringify(m.state)` can never see a closure
(`docs/retros/2026-08-12-profile-pages.md:171-184`, fix pinned at
`PasswordTab.auth.test.tsx:160-166`).

Applied here that is not a theoretical concern: this mutation **does** pass variables. The
`mutate(body)` argument carries the invitee's email address, a secondary asset, and the
closure holds it. A `state`-only assertion would pass on the token (which does live in
`state.data`) while saying nothing about the closure. Assert the cache is empty and both
are covered.

**The positive control must be taken while an observer is still attached**, i.e. while the
reveal dialog is open and before `Done`, otherwise it measures an already-evicted entry and
proves nothing (`enrollmentTokenSecrecy.test.tsx:179-183` is the shipped shape; the profile
retro records the ordering mistake). Non-vacuity gate: deleting `gcTime: 0` must turn this
test RED and is performed and reported.

## No revoke control, and what that means for invites

The Enrollments tab ships no revoke button because `DELETE /v1/agent-enrollments/{id}` does
not exist and a guaranteed-405 button is a dead control. The same rule applies, and here it
costs even less to obey:

- There is no revoke or delete endpoint for invites (finding 4 above).
- **The hi-fi does not ask for one.** Its `ACTIONS` column renders prose, not a control:
  `copy token only on creation` for active rows and an em dash otherwise
  (`hifi3-holo-pages.jsx:2119-2121`). Its footnote (`:2130`) states outright that invites
  are one-time, there is no revoke endpoint in v1, and expiry or redemption are the only
  terminal states. **No revoke and no resend is implied by the design.**
- Following `EnrollmentsTable.tsx:11-14`, the `ACTIONS` header is renamed **`NOTE`**: a
  header promising actions while delivering a sentence is itself a dead affordance.

Consequence for the acceptance criteria: the table must contain **no** button matching
revoke, delete, or resend, asserted negatively with a positive control that the query would
find such a button if one existed.

The footnote ships close to the hi-fi's wording, because it is accurate: invites are
one-time, the raw token is returned only at creation, expiry or redemption are the only
terminal states, and email binding pins the invite to one address.

## Columns

Hi-fi header row (`hifi3-holo-pages.jsx:2096`):
`TOKEN PREFIX | BINDS TO | EXPIRES | CREATED BY | STATUS | ACTIONS`.

Shipped header row:
`BINDS TO | CREATED | EXPIRES | CREATED BY | STATUS | NOTE`.

| Column | Cell | Decision |
|---|---|---|
| ~~`TOKEN PREFIX`~~ | - | **Dropped.** Only the SHA-256 is stored (`invites.go:56`) and the list query cannot select it (`invites.sql:22-25`). Same omission as `EnrollmentsTable.tsx:7-9`. |
| `BINDS TO` | `email ?? '-'` | The dash is a plain ASCII hyphen and means "not bound to an address", which is a real state, not missing data. |
| `CREATED` | `created_at.slice(0, 10)`, sortable | Added because `created_at` is the default sort key and needs a clickable header (`EnrollmentsTable.tsx:15`). |
| `EXPIRES` | `formatExpiryLabel(expires_at, now)`, sortable | |
| `CREATED BY` | `created_by_email`, truncated | **This is the one hi-fi column enrollments could not fill and invites can**, because the list query joins `users` (`invites.sql:32`). The bare `created_by` UUID is never rendered. |
| `STATUS` | `<Chip tone={statusTone(status)}>{status}</Chip>` | |
| `NOTE` (was `ACTIONS`) | `REDEEMED`: `redeemed <used_at.slice(0,10)>`; `ACTIVE`/`EXPIRING`: `copy token only on creation`; `EXPIRED`: `-` | Renamed, and this is the only consumer of `used_at`'s value rather than its presence. |

Rows in a terminal state (`REDEEMED` or `EXPIRED`) render at `opacity-[0.55]`, matching
both `hifi3-holo-pages.jsx:2108` and `EnrollmentsTable.tsx:52`.

**Sortable headers ship even though the hi-fi has no sort control on this page.** The
endpoint supports both keys in both directions, `Table` makes the headers free, and the
sibling tab shipped them. The hi-fi's omission is a fidelity gap in the sketch, not a
constraint.

## Create flow

`CreateInviteForm.tsx`, **tab-local, not shared** with `CreateEnrollmentForm`. That is a
decision already recorded at `CreateEnrollmentForm.tsx:22-25`: invites take an email that
binds the invite, different presets and a different endpoint, and the hi-fi models the
divergence with an `isInvite` boolean, which is the flag-driven component that rots. Only
the reveal half is shared.

An inline `GlassPanel` form, not a modal, so exactly one dialog is on screen at a time.

- **Email**: optional `type="email"` input, trimmed, **omitted from the body when blank**.
  The client does not reimplement `mail.ParseAddress`; the browser's `type="email"` gives a
  cheap first pass and the server's 400 is surfaced in the form's own error slot
  (`CreateEnrollmentForm.tsx:88`). The form owns its error surface; nothing routes to a
  page-level box.
- **Expires in**: four preset pills, `aria-pressed`, default `72h` matching both the server
  default (`invites.go:31`) and the hi-fi (`:2087`).

| Label shown | Wire value |
|---|---|
| `24h` | `"24h"` |
| `72h` (default) | `"72h"` |
| `7d` | `"168h"` |
| `30d` | `"720h"` |

  Raw hours are never shown to the admin ("720h" is hostile, "30d" is not), and the day
  labels are never sent (Go duration strings have no day unit - finding 7). Because every
  preset is inside `(0, 720h]`, the invalid range is **unreachable rather than merely
  rejected**, the same argument as `CreateEnrollmentForm.tsx:38-42`. There is no free-text
  duration input, so no client-side max check is needed and none is written; the max is
  asserted against the preset table in `api.test.ts` instead.
- A body is **always** sent, minimally `{expires_in: "72h"}`, because `readJSON` runs
  unconditionally (`invites.go:27`).
- The warning that the raw token is returned once and cannot be retrieved again renders in
  the form, before submission, exactly as `CreateEnrollmentForm.tsx:83-86` does.

On success the create panel closes and `create.data` opens `TokenRevealDialog` with
`title="Invite created"`, `endpoint="POST /v1/invites"`, `expiresAt={create.data.expires_at}`
and `onDone={() => create.reset()}`. No prop is added to the shared dialog. The default
warning copy already reads correctly for an invite.

## Architecture

| File | Change |
|---|---|
| `web/src/admin/invites/api.ts` | **New.** `Invite`, `InvitesPage`, `InviteSort`/`InviteSortField`, `TTL_PRESETS`, `CreateInviteBody`, `CreateInviteResponse`, `listInvites`, `createInvite`. All requests go through `apiFetch`. |
| `web/src/admin/invites/inviteStatus.ts` | **New.** `deriveStatus`, `statusTone`, `formatExpiryLabel`. |
| `web/src/admin/invites/useInvites.ts` | **New.** `useQuery(['invites', sort, cursor])`, `keepPreviousData`, no `refetchInterval`. |
| `web/src/admin/invites/useInviteActions.ts` | **New.** One `create` mutation, `gcTime: 0`, bare-prefix invalidation. |
| `web/src/admin/invites/CreateInviteForm.tsx` | **New.** |
| `web/src/admin/invites/InvitesTable.tsx` | **New.** Composes `Table`. |
| `web/src/admin/invites/InvitesTab.tsx` | **New.** Composition point. |
| `web/src/components/holo/Chip.tsx` | Add the `err` tone. One key. No consumer changes. |
| `web/src/admin/tabs.ts` | One `ADMIN_TABS` entry, `{slug: 'invites', label: 'Invites', Panel: InvitesTab}`, placed **between Users and Agent enrolls** per the hi-fi order the file's own comment records (`:19-20`). Correct the stale blocked-on-endpoint comment (`:16-18`) in the same edit. |
| `web/src/admin/AdminTabs.test.tsx`, `web/src/admin/AdminPage.test.tsx`, `web/src/components/holo/Chip.test.tsx` | **Additive edits only.** Rewriting a shipped test file is coverage-losing. |

Plus one test file per new module. No other file is modified; `web/dist` is reverted with
`git checkout -- web/dist/` before the change set is assembled.

## Reuse, and the extraction debt this slice adds

Reused as-is, no change: `Table`/`TableRow`/`TableCell` including `ariaSort`/`sortCaret`
(`Table.tsx:36-46`), `TokenRevealDialog`, `DialogShell` via it, `GlassPanel`, `PillButton`,
`Field`, `Input`, `useNow`, `formatTimeUntil`, `computePageRange`, `secretLeaks.ts`.

**This tab is the seventh consumer of the cursor-pager block** - the
`cursor`/`stack`/`startOffset`/`offsets` quartet with its `next`/`prev`/`resetPaging`
functions and `computePageRange` footer. Shipped copies live in `JobsPage.tsx:29`,
`WorkersPage.tsx`, `SchedulesPage.tsx:122`, `UsersTab.tsx:38`, `EnrollmentsTab.tsx:29` and
`ReservationsTab.tsx:54`. The `toggleSort` helper is duplicated in four of those. There is
no open backlog item for either; grep of `docs/backlog/` for pager/pagination returns
endpoint items only.

**It is not extracted here**, for the reason the detail-page triad item already argues
(`docs/backlog/idea-2026-08-12-detail-page-state-triad-primitive.md:45-62`): the extraction
has to migrate six shipped surfaces, it is a behavior-preserving refactor whose real gate is
a **zero-line diff to the existing test files**, and folding it in would put a feature
behind an unrelated refactor with a different risk profile. A backlog item is **proposed**
in Phase 6 rather than auto-filed.

Second-consumer notes, recorded so the third does not rediscover them and does not need a
filed item yet: `formatExpiryLabel` and the 1h `EXPIRING_WINDOW_MS` constant are duplicated
between `enrollmentStatus.ts` and `inviteStatus.ts`. The repo rule is extract before the
**third** consumer, so a third status module must lift both into a shared
`web/src/lib/expiry.ts`. A comment in `inviteStatus.ts` says so.

## Registry and gating

One entry in `ADMIN_TABS` is sufficient and is all that gates the tab appearing, verified:
`AdminTabs.tsx:12` maps the array to build the bar, and `AdminPage.tsx:20-23` resolves the
`:tab` route param via `findAdminTab` and redirects unknown or unbuilt slugs to
`/admin/users`. Nothing else in routing or gating changes. Admin-only access is enforced by
`AdminRoute` on the client for UX and by `auth(admin(...))` on the server as the actual
control (`server.go:142-143`).

## jsdom and user-event constraints that apply

- **No native `<dialog>` and no focus-trap library.** `DialogShell` implements the trap as a
  keydown intercept and documents why: `@testing-library/user-event@14` computes its Tab
  destination from a document-wide `querySelectorAll` and the string `inert` appears nowhere
  in the shipped package, so `userEvent.tab()` walks straight past an inert background
  (`DialogShell.tsx:48-57`). `inert` and `aria-hidden` still ship as the browser and AT
  mechanism, but tests may assert them **as attributes only**; nothing in this suite may
  claim `inert` blocks anything.
- **`invalidateQueries` tests need an active observer.** Seed the list by mounting it
  (`renderHook` or rendering the tab), never with `fetchQuery`: `refetchType: 'active'` will
  not fire without an observer and the test passes vacuously.
- **`navigator.clipboard` is feature-detected** in `TokenRevealDialog` because it is
  `undefined` outside a secure context, which is the default for `relay-server` on plain
  HTTP `:8080`. Tests that exercise Copy must install a clipboard descriptor and restore it
  (`enrollmentTokenSecrecy.test.tsx:30-38`).
- **jsdom fires no event when a focused node is silently detached**
  (`DialogShell.tsx:299-305`), so no test may rely on a focusout to observe row removal.
- MSW is fail-closed: every request a test triggers needs a handler, including probe
  requests used as instrument controls.

## Invariants

Read against the CLAUDE.md Invariants block, including the instruction to read them for
shape rather than nouns when working in `web/`.

**Do not apply, stated rather than left silent:** epoch fence, single job-spec pipeline,
one bounded sender per gRPC stream, identity-checked teardown, no interior pointers across
locks, single JSON entry point. This slice writes no Go, touches no task row, opens no
stream, and sends no request body outside `apiFetch`.

**Applies, in its frontend form: end the generation before releasing the resource.** There
is no new async lifecycle here - no SSE, no `AbortController`, no subscription. The one
live analogue is the credential's lifecycle: `create.reset()` is the act that ends the
mutation's generation, and it is what `onDone` must call, so dismissal and destruction are
a single step and the dialog cannot be unmounted while the token remains readable in the
cache. The dialog's own teardown ordering (leave `dialogStack` before restoring focus) is
`DialogShell`'s, already audited, and is not modified.

**Applies, house form: every request goes through `apiFetch` and no component calls `fetch`
directly.**

## Security and system design

- **Threat model.** Primary asset: the raw invite token, which grants account creation on
  this server. It has exactly one render site and one destruction point, never enters a
  URL, query key, route, storage, or the console, and is unrecoverable after the 201.
  Secondary asset: invitee email addresses, which is why the endpoint is admin-gated rather
  than merely authenticated; the tab renders them but adds no new exposure surface, and no
  email is ever placed in a query key or URL.
- **Failure modes.** A 403 from a non-admin who reaches the route renders through the tab's
  error card, not a blank panel. A 500 on create renders in the create form's own error
  slot; a 500 on list renders the retryable error card. A 400 from a malformed email is the
  server's message shown verbatim in the form.
- **Load.** One request per page view or page turn, `limit=50`, plus the endpoint's own
  `COUNT(*)`. **No polling**; the 60s `useNow` tick issues no request. Sort or page change
  resets the cursor stack, which is one request, not a refetch storm. At farm scale invites
  are hand-created by admins, so this list is small-number territory.
- **The count that cannot lie.** `total` is computed with the list's own (empty) predicate
  (`invites.sql:55-61`), so the footer range cannot state a number the admin cannot page to
  - the defect class `bug-2026-06-21-jobs-pagination-footer-absolute-range` covered.
- **Clock skew is the one honest weakness.** `EXPIRING` and `EXPIRED` derive from the
  browser clock. A skewed client mislabels a row, and no server field exists to prefer.
  `REDEEMED` is immune, because it derives from a server-written timestamp's presence.

## Testing

Vitest plus MSW. The whole slice is frontend, so the web suite is the gate; no Go test runs
and `make test-integration` is not required.

### `inviteStatus.test.ts`

- Each of the four states from its own fixture.
- **Precedence: a row with `used_at` set and `expires_at` in the past is `REDEEMED`.** This
  is the discriminating test for the ordering; an implementation that checks expiry first
  passes every other test in this file and fails only this one.
- Boundaries, byte-identical in intent to `plans/2026-08-09-admin-enrollments-tab.md:221-222`:
  remaining exactly 0 is `EXPIRED`; 59m59s is `EXPIRING`; 1h00m01s is `ACTIVE`.
- A `used_at` that is present is honoured even when `expires_at` is far in the future.
- `statusTone` maps all four states, including the new `err`.
- `formatExpiryLabel` collapses `in 20s` to `in <1m` and leaves `in 3h` alone.

### `api.test.ts`

- `listInvites` builds `/invites?sort=<s>&limit=50` and appends `cursor` only when set.
- `createInvite` posts a body even when the email is blank, and **omits** the `email` key
  rather than sending `""`.
- **Every `TTL_PRESETS` wire value is hour-denominated and parses as a Go duration**, i.e.
  matches `/^\d+h$/`, and every value is `> 0` and `<= 720h`. This is the discriminating
  test for finding 7: a preset shipped as `"7d"` passes a naive "four presets exist" test
  and 400s in production.
- The default preset is `72h`, matching `invites.go:31`.

### `useInviteActions.test.tsx`

- A successful create invalidates the bare `['invites']` prefix and the mounted list
  refetches, with the list mounted through a **real active observer** and a comment saying
  why a `fetchQuery` seed would pass vacuously.
- The mutation is configured with `gcTime: 0` and this is asserted behaviourally (the cache
  empties), not by reading the option back.

### `inviteTokenSecrecy.test.tsx`

Reuses `web/src/test/secretLeaks.ts`; the matcher self-tests already live in the
enrollments suite and are not duplicated.

- Positive controls **while the dialog is open**: `domContainsSecret(TOKEN)` is `true` and
  `client.getMutationCache().getAll()` has length 1 and contains the token.
- After `Done`: the DOM (including every input value) is clean, the **mutation cache is
  empty**, the query cache never held it, storage never held it, no request URL contained
  it, and no console method received it in any representation.
- The detached dialog layer holds no credential after teardown
  (`enrollmentTokenSecrecy.test.tsx:243-279` is the shape).
- **Non-vacuity, performed and reported:** deleting `gcTime: 0` turns the cache-empty
  assertion RED directly. Removing `create.reset()` from `onDone` also turns this test file
  RED, but NOT at the cache-empty line: with `onDone={() => {}}` the run fails earlier, at
  `expect(screen.queryByRole('dialog')).not.toBeInTheDocument()`, because the dialog is
  driven by `create.data` and nothing ever clears it - the cache-empty assertion is never
  reached. The cache-empty assertion's own independent counterpart lives in
  `useInviteActions.test.tsx`: a case that skips `reset()` entirely and asserts the settled
  mutation stays in the cache, proving the emptiness claim genuinely depends on `reset()`
  running rather than on the dialog dismissing.

### `InvitesTable.test.tsx`

- All six headers render; `CREATED` and `EXPIRES` are sort buttons with `aria-sort`, the
  other four are static `columnheader`s with no `aria-sort`.
- An invite with no `email` renders the ASCII `-`; one with an email renders it.
- `created_by_email` renders; the bare `created_by` UUID appears nowhere in the table.
- The `NOTE` cell reads `redeemed 2026-08-02` for a redeemed row, the creation sentence for
  an active row, and `-` for an expired one.
- Terminal rows carry `opacity-[0.55]`; active rows do not.
- **No revoke, delete or resend control exists**, asserted as
  `queryAllByRole('button', {name: /revoke|delete|resend/i})` being empty, **with a positive
  control** proving the query finds such a button when one is rendered into an equivalent
  tree. An unanchored absence assertion here would pass against a table that renders no
  buttons at all for an unrelated reason.
- No token, hash, or prefix column exists; no cell contains a 64-hex string.

### `InvitesTab.test.tsx`

- Loading skeleton, error card with a working Retry, and the empty state.
- Clicking `CREATED` toggles the sort and **resets the cursor stack**, so the next request
  carries no `cursor` (the server 400s a cursor issued under a different sort,
  `pagination.go:272-286`).
- A `limit=1` style cursor walk forward and back keeps the footer range correct across a
  partial last page.
- The footer names `GET /v1/invites` and states that **all states** are shown, not "active
  only" - this endpoint applies no filter, unlike the enrollments one.
- The create panel opens, submits, the reveal dialog appears, `Done` closes it, and the list
  refetches.
- The 60s tick flips a pill from `EXPIRING` to `EXPIRED` with **zero** additional requests.

### Additive edits

- `AdminTabs.test.tsx`: the Invites tab renders in the bar, in position two.
- `AdminPage.test.tsx`: `/admin/invites` resolves to the panel rather than redirecting.
- `Chip.test.tsx`: the `err` tone renders its classes.

Plan-supplied test bodies are guesses until run RED. Every absence assertion above carries a
positive control in the representation the real failure would take.

## Acceptance criteria

1. `/admin/invites` is reachable, appears in the admin tab bar between Users and Agent
   enrolls, and is added by a single `ADMIN_TABS` entry with no routing or gating change.
   The stale comment at `tabs.ts:16-18` is corrected.
2. An admin can create an invite with an optional email and one of four TTL presets; the
   request body always exists and `expires_in` is always hour-denominated
   (`/^\d+h$/`, `<= 720h`); a blank email is omitted from the body, not sent as `""`.
3. The raw token is displayed exactly once in `TokenRevealDialog` with a copy affordance and
   a clipboard fallback, and the shared dialog is used unmodified.
4. After dismissal the token is absent from the DOM, **the mutation cache is empty**, the
   query cache, both web storages, every request URL, and every console method. The
   cache-empty assertion is proven RED by deleting `gcTime: 0`. Removing `create.reset()`
   from `onDone` also turns the suite RED, but at the dialog-dismissal assertion, not the
   cache-empty one - the latter is never reached on that path. Its independent counterpart is
   a `useInviteActions.test.tsx` case that omits `reset()` and asserts the mutation survives.
5. The list renders every state with cursor pagination, sortable `CREATED` and `EXPIRES`
   headers in both directions, and a footer whose range and total come from the endpoint's
   own `total`.
6. The status pill derives client-side in the order REDEEMED, EXPIRED, EXPIRING, ACTIVE,
   with a 1h expiring window, and a redeemed-and-expired invite reads `REDEEMED`.
7. `CREATED BY` renders `created_by_email`; the raw `created_by` UUID is never rendered; no
   token prefix column exists.
8. The table contains no revoke, delete, or resend control, asserted negatively with a
   positive control, and the footnote states that expiry and redemption are the only
   terminal states.
9. No `refetchInterval` anywhere in the tab; freshness of the pill comes from `useNow` and
   freshness of the row set from bare-prefix invalidation.
10. `Chip` gains exactly one tone key and no existing consumer changes.
11. The full web suite is green and `tsc -b && vite build` succeeds, both re-run by the
    conductor on the settled tree; `git checkout -- web/dist/` before the change set is
    assembled.
12. No file outside the Architecture table is modified; no Go file is touched.
13. Backlog items are **proposed**, not auto-filed. The invites item is closed via
    `/backlog close`, which `git mv`s it into `docs/backlog/closed/`, and **its stale title
    is corrected in the same commit**.

## Scoped out, with the enabler

| Element | Why it is out | Enabler |
|---|---|---|
| Revoke / delete an invite | No endpoint exists (`server.go:142-143` registers only POST and GET; `invites.sql` has no delete query), and the hi-fi explicitly does not ask for one (`:2130`). A guaranteed-failing button is a dead control. | **None.** Same ruling as the preceding backend spec. If one is ever wanted it is a product decision first, not a UI gap. |
| Resend an invite | Nothing in the hi-fi implies it; the `ACTIONS` cell is prose (`:2119-2121`). Resending would mean re-revealing a token that only ever existed once. | **None.** |
| `TOKEN PREFIX` column | Only the SHA-256 is stored; a prefix column would persist a fragment of a secret for a cosmetic column. | **None**, deliberately. |
| Redeemed-by (`used_by`) column | The endpoint returns no `used_by`; no hi-fi column and no consumer. One `LEFT JOIN` plus two keys when a consumer appears (`used_by` is `ON DELETE SET NULL`). | **None** - recorded so it is a lookup, not a rediscovery. |
| `?status=` filter or a search box | No endpoint support; adding it makes the sort+filter 400 rule (`internal/api/jobs.go:417-422`) live for this endpoint, and the client already derives the pill over a short list. | **None** - recorded so whoever adds it knows the rule attaches. |
| Extracting the cursor-pager block and `toggleSort` | This tab is the seventh consumer, but the extraction migrates six shipped surfaces under a zero-test-diff gate and is a refactor with its own risk profile. | **Propose:** `idea-2026-08-13-cursor-pager-hook.md` (low/medium). Must state the zero-line-diff gate on the six existing test files up front, and that a partial migration producing a seventh variant is worse than seven copies. |
| Extracting `formatExpiryLabel` + the 1h window | Second consumer; the rule is extract before the third. | **None yet.** A comment in `inviteStatus.ts` names `web/src/lib/expiry.ts` as the destination for consumer three. |
| Reaping expired invites | Nothing reaps the table, so the list grows monotonically. | **Already filed:** `docs/backlog/idea-2026-08-13-reap-expired-invites-and-tokens.md` (`ROADMAP.md:131`). Not refiled. |
| Audit record of invite creation | A privileged action with no trail. | **Already filed:** `docs/backlog/feature-2026-06-26-audit-log-admin-console-actions.md` (`ROADMAP.md:129`), which names invite creation explicitly. Not refiled. |
| Widening the clock-skew question | The browser is a third clock after the app host and the database. | **Already filed, adjacent:** `docs/backlog/bug-2026-08-13-token-expiry-two-clocks.md`. Not widened here; the skew is stated in Risks. |
| Count badges on the tab bar | Would have to lift a count out of each panel's own query (`AdminTabs.tsx:6-8`). | **None.** |

Per the standing rule these are proposals. Phase 6 files them for human accept; nothing is
auto-filed.

## Decisions

1. **Build the whole tab in one slice, not the item's two steps.** The item's step-1 /
   step-2 split existed only because the list endpoint was missing; it shipped
   (`server.go:143`), so the honest-empty-state half of the proposal is dead scope.
2. **Clone the Enrollments tab structurally and re-argue nothing that is already shipped.**
   It is the same shape and the second-instance effect is the whole reason it was built
   first; review attention should follow the novelty, not the diff.
3. **Four states, derived client-side, in the order REDEEMED, EXPIRED, EXPIRING, ACTIVE.**
   The server ships facts by design (`invites.go:112-121`), and `README.md:1300-1303`
   already documents this exact order as the shipped contract.
4. **A redeemed invite that is also expired reads REDEEMED.** Redemption is one-way and
   terminal (`invites.sql:9-12`), and expiry of an already-spent credential is a non-event.
5. **The expiring window is 1h, cited not invented.** `README.md:1300-1303` and
   `enrollmentStatus.ts:5`; boundaries `<= 0` for expired and strict `<` for expiring, so
   the pill and the `formatTimeUntil` cell flip at the same instant.
6. **Add one `err` tone to `Chip`.** Four states need four tones; the class string is the
   repo's existing error idiom; the addition is additive and matches the hi-fi's own colour
   assignment (`:2101`). Collapsing EXPIRED and REDEEMED into `muted` would discard
   information the design deliberately encodes.
7. **`gcTime: 0` plus `reset()` on the create mutation, and the test asserts the mutation
   cache is EMPTY.** `reset()` only detaches the observer. A `state`-only assertion cannot
   see the `mutationFn` closure, which here holds the invitee email passed as the mutation
   variable (`docs/retros/2026-08-12-profile-pages.md:171-184`).
8. **The positive control is taken while the dialog is open.** After dismissal the entry is
   already evicted, so a control taken there measures nothing.
9. **No revoke, delete, or resend control.** No endpoint exists and the hi-fi asks for
   none; `ACTIONS` is renamed `NOTE` because the cell holds prose, following
   `EnrollmentsTable.tsx:11-14`.
10. **`TOKEN PREFIX` is dropped with no enabler.** Persisting a prefix would weaken the
    secret for a cosmetic column.
11. **`CREATED BY` ships as an email.** The list query joins `users` (`invites.sql:32`), so
    this is the one hi-fi column enrollments could not fill; a 36-character UUID would be
    unusable.
12. **TTL presets are 24h / 72h / 7d / 30d by label and `24h` / `72h` / `168h` / `720h` on
    the wire.** Go duration strings have no day unit, so a literal `"7d"` 400s. Presets make
    the invalid range unreachable rather than merely rejected, so no client-side max check
    is written.
13. **A request body is always sent.** `readJSON` runs unconditionally (`invites.go:27`), so
    an empty POST 400s.
14. **Email is validated by `type="email"` plus the server's 400, not by a client-side
    reimplementation of `mail.ParseAddress`.** Two parsers disagreeing is worse than one
    round trip, and the error renders inside the form that owns it.
15. **The create form stays tab-local; only the reveal dialog is shared.** Already decided
    at `CreateEnrollmentForm.tsx:22-25`; the hi-fi's `isInvite` boolean is the flag-driven
    component that rots.
16. **Sortable headers ship even though the hi-fi has no sort control here.** The endpoint
    supports both keys in both directions and `Table` makes the headers free; the sketch's
    omission is a fidelity gap, not a constraint.
17. **The empty state carries no "prev" escape hatch, matching `UsersTab` rather than
    `EnrollmentsTab`.** That hatch exists because the enrollments list is filtered and a row
    can vanish mid-page; this list is unfiltered and nothing deletes or reaps invites, so a
    non-first page landing on zero rows is unreachable. Shipping an untestable escape hatch
    would be dead code.
18. **No polling.** Pill freshness comes from `useNow(60_000)`, row-set freshness from
    bare-prefix invalidation.
19. **The cursor-pager block is not extracted in this slice**, even at the seventh consumer,
    because the migration of six shipped surfaces under a zero-test-diff gate is its own
    project. An item is proposed instead.
20. **The stale `tabs.ts` comment is corrected here.** A wrong contract in a comment is a
    defect; leaving it would tell the next reader the endpoint does not exist.
21. **`inert` is asserted as an attribute only.** `user-event@14` walks past it
    (`DialogShell.tsx:48-57`), so no test in this slice may claim it blocks anything.
22. **The backlog item's stale title is corrected at close time**, not silently left in the
    closed directory to mislead a future search.

## Risks

- **A preset shipped as `"7d"` 400s in production and passes a naive test.** The regex
  assertion on `TTL_PRESETS` is the guard, and it is a requirement, not a nicety.
- **The mutation-cache assertion is easy to weaken back to `JSON.stringify(m.state)`.** That
  form is what the profile slice shipped and it was blind to the closure. The test carries
  the reason inline so it is not "simplified" later.
- **The positive control can drift after the dismissal.** An eviction-then-control ordering
  measures an already-empty cache and reports clean forever.
- **The reveal dialog holds the only copy of an unrecoverable secret**, and its dismissal is
  the destructive act. `DialogShell` is passed `dismissOnEscape={false}` by
  `TokenRevealDialog` already; nothing in this slice may reintroduce a backdrop or Escape
  dismissal, and no `onDone` may be passed that does not call `create.reset()`.
- **Clock skew mislabels EXPIRING and EXPIRED.** No server field exists to prefer; the
  adjacent two-clocks bug item is not widened here.
- **The `-` placeholder must stay a plain ASCII hyphen.** The hi-fi uses an em dash
  (`:2111, :2120`) and the repo forbids them.
- **`web/dist` is tracked but stale**, so a production build dirties it; revert it before
  assembling the change set.
- **A seventh copy of the pager makes the duplication the codebase's convention.** The
  proposed item is a countdown, and this slice is the event that starts it running.
