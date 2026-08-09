# Admin Console - Reservations Tab - Design

Date: 2026-08-09
Status: Draft (autonomous cycle; conductor review)

## Overview

The third slice of the admin console: list worker reservations, create one, delete
one. The shell plus Users tab shipped 2026-08-08
(`docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`); the Agent
enrollments tab shipped 2026-08-09
(`docs/superpowers/specs/2026-08-09-admin-enrollments-tab.md`). Frontend-only, no
backend change.

Backlog item: `docs/backlog/feature-2026-08-08-admin-reservations-tab.md`.

This is the third instance of an established pattern, so most of this spec is a
pointer. Two things are genuinely new and get the words:

1. **What a reservation does is not what the hi-fi implies.** A reservation is a
   pure *exclusion* of `worker_ids` from dispatch, for everyone. `user_id`,
   `project` and `selector` are inert.
2. **This is the first create form that needs a picker over another resource**
   (workers), and the first tab with a destructive row action against a real
   endpoint.

Nothing in this slice is secret. See decision 1.

## What is inherited, not re-derived

Everything in the enrollments spec's "What is inherited" table applies unchanged:
`/admin/:tab` routing and redirects, admin gating (server `auth(admin(...))` is the
boundary; `AdminRoute` and the nav filter are UX), one-entry tab registration in
`web/src/admin/tabs.ts`, the shell header, the count-badge-free tab bar, the
list-page shape (cursor + `stack`/`offsets`, `computePageRange`, the mono `SHOWING`
footer, `isPlaceholderData` to disable the pager, loading/error/empty triad), the
empty-page `prev` escape hatch, `keepPreviousData` with **no** `refetchInterval`,
mutations that invalidate the **bare** key prefix with no optimistic updates,
`reset()` before reopening a form, header-click sorting via `toggleSort` with
`aria-sort` and a caret, inline create panel rather than a modal, the dialog a11y
baseline (`role="dialog"`, `aria-modal`, `aria-labelledby`, first control focused,
no focus trap), `apiFetch` as the only fetch entry point, and omit-rather-than-fake.

Additionally inherited from the two shipped tabs:

| Inherited | Source |
|---|---|
| Destructive row action -> `ConfirmDialog` with `destructive`, driven by a `confirm` state object | `web/src/admin/users/UsersTab.tsx:116-118,278-291` |
| Shared inline `actionError` box above the table for mutation failures | `web/src/admin/users/UsersTab.tsx:53-60` |
| Client status derived by a pure module, `now` injected as a prop, `useNow(60_000)` local tick with zero requests | `web/src/admin/enrollments/enrollmentStatus.ts`, `web/src/lib/useNow.ts` |
| `Chip` tones `accent` / `muted` / `warn`, row `opacity-[0.55]` for a dead row | `web/src/components/holo/Chip.tsx`, `EnrollmentsTable.tsx:74-76` |
| Absent-key placeholder is a plain ASCII hyphen, never an em dash | `EnrollmentsTable.tsx:78-83` |

Design source of truth is the hi-fi Holo `AdminReservations`
(`design_handoff_relay_holo/hifi3-holo-pages.jsx:2205-2278`);
`reference/screens/admin.js` is structure-only. The hi-fi gives: an endpoint
caption, a `SortControl` with eight options, a `+ Reserve workers` button, the
columns `NAME | PROJECT | WORKER IDS | SELECTOR | STARTS | ENDS | ACT.` with a red
`Delete` in `ACT.`, a `PageFooter`, and the footnote that selectors are
informational. There is **no create modal designed for reservations** - the
`+ Reserve workers` button is inert in the hi-fi and `AdminTokenModal` is the
enrollment/invite form. The create form is therefore designed here.

## Verified backend surface

Read from `internal/api/server.go:133-136`, `internal/api/reservations.go`,
`internal/store/query/reservations.sql`, `internal/store/migrations/000001_initial.up.sql:84-95`,
and `internal/scheduler/dispatch.go:98-262`. All three routes are
`auth(admin(http.HandlerFunc(...)))`.

| Action | Method + path | Request | Success | Errors |
|---|---|---|---|---|
| List | `GET /v1/reservations` | query only | **200** `{ items, next_cursor, total }` | 400 `invalid limit` / `invalid cursor` / invalid-sort message / cursor-sort-mismatch; 500 `count reservations failed` / `list reservations failed` |
| Create | `POST /v1/reservations` | JSON body **required** | **201** full reservation row | 400 `invalid request body` (including an empty body); 400 `name is required`; 400 `invalid user_id`; 400 `invalid worker_id: <value>`; 413 `request body too large`; **500** `create reservation failed` - which is what a *well-formed but nonexistent* `user_id` produces, via the `users(id)` FK |
| Delete | `DELETE /v1/reservations/{id}` | none | **204**, no body | 400 `invalid reservation id`; 404 `reservation not found`; 500 `delete reservation failed` |

**DELETE exists.** The item's claim that all three endpoints exist and are
admin-gated is correct, verified at `internal/api/server.go:136` ->
`handleDeleteReservation` (`internal/api/reservations.go:302-323`), which checks
existence via `GetReservation` and returns 204.

List params (shared `parsePage` + `ReservationsSortSpec`,
`internal/api/reservations.go:55-63`): `?sort=` one of `created_at`, `name`,
`starts_at`, `ends_at`, each with an optional `-` prefix, default `-created_at`;
`?limit=` 1..200 default 50; `?cursor=`. **All eight arms are implemented**
(`:106-217`, with a `panic` default arm) and all eight are indexed
(migration `000013`). `starts_at` / `ends_at` order NULLS LAST descending, NULLS
FIRST ascending. So the Holo's eight sort options are fully backed - the item was
right to demand this be checked, and the answer is yes.

Row shape, from `reservationResponse` / `toReservationResponse` (`:13-53`):

```
{ id, name, selector, worker_ids, user_id, created_at }   // always present
{ ..., project, starts_at, ends_at }                      // key ABSENT when NULL (omitempty on pointers)
```

- `worker_ids` is always an array (built with `make`), `[]` when empty, never `null`.
- `selector` has **no** `omitempty` and goes through `rawJSON`, not `rawObject`. A
  create with no selector marshals a nil map to the literal `null`, which `rawJSON`
  passes through unchanged, so `"selector": null` is a real response value.
  Column-default rows read `{}`. The client type is
  `Record<string, string> | null` and must tolerate both.
- `user_id` is a bare user UUID with no join to `users`.
- Timestamps are Go `time.Time`, RFC3339 with nanosecond precision. Parse with
  `new Date()`; never string-compare.

**What a reservation actually is.** `reservations` is a plain table with no
lifecycle column: no consumed, released, deleted or status field. It is read in
exactly one place, `Dispatcher.selectWorker`:
`ListActiveReservations` returns rows where
`(ends_at IS NULL OR ends_at > NOW()) AND (starts_at IS NULL OR starts_at <= NOW())`,
their `worker_ids` are unioned into a `reservedIDs` set, and every worker in that
set is `continue`d past for **every** task (`internal/scheduler/dispatch.go:185-223`).

Therefore, stated plainly because the UI copy depends on it:

- A reservation **removes workers from the general dispatch pool**. It does not
  route the reserving user's work to them. `user_id` grants nothing.
- `selector` is never read by the scheduler. Only explicit `worker_ids` are
  enforced. (The hi-fi's footnote and the existing worker-detail note are correct.)
- `project` is never read by the scheduler either.
- Creating a reservation does **not** preempt tasks already running on those
  workers; it only affects future dispatch, from the next dispatch cycle.
- The dispatcher's ticker is 30s (`internal/scheduler/dispatch.go:50`) and no
  reservation write emits a NOTIFY or calls `Trigger()`, so create and delete take
  effect within roughly one poll interval, not instantly.

**Derivable states.** The list is *unfiltered* - `ListReservationsPage` and
`CountReservations` have no `WHERE` beyond the cursor, so past, future and current
rows all appear (unlike enrollments). The client can therefore reproduce the
scheduler's own predicate exactly:

| State | Condition | Tone |
|---|---|---|
| `ENDED` | `ends_at` present and `<= now` | `muted`, row at `opacity-[0.55]` |
| `SCHEDULED` | otherwise, `starts_at` present and `> now` | `warn` |
| `ACTIVE` | otherwise (matches `ListActiveReservations` exactly) | `accent` |

`ENDED` takes precedence so that an inverted window (`ends_at < starts_at`, which
the server accepts and which can never be active) reads as dead rather than
pending. The create form prevents making one. There is no fourth state to invent:
nothing else is recorded. Same local-clock caveat as `enrollmentStatus.ts`, for the
same reason - the server exposes no status to prefer.

### Where the backlog item was wrong

I wrote that item. Verified correct: all three endpoints and their paths, admin
gating, cursor pagination, the eight sort keys, the response field list, that
`user_id` has no email join, that nullable dates must render as an explicit dash.
Wrong or materially incomplete:

1. **Incomplete, and it changes the copy.** The item asks only whether the
   *selector* footnote is true. The bigger truth is that `user_id` and `project`
   are equally inert and that a reservation excludes the workers from *all*
   dispatch including the named user's own tasks. "Reserve workers for Ada" is
   what the screen implies and is not what the system does. Corrected in the
   footnote copy (decision 6).
2. **Wrong about nullability.** The item writes the row as
   `{ ... project?, starts_at?, ends_at? }` but says nothing about `selector`,
   which is the one field that arrives as JSON `null` (no `omitempty`, `rawJSON`
   not `rawObject`). Conversely `project` / `starts_at` / `ends_at` keys are
   **absent**, not null. A `| null` type on the first three and a bare optional on
   `selector` would both be wrong.
3. **Silent on the only hard design problem.** Creating a reservation requires
   worker **UUIDs**, and the item's proposal never mentions how an admin obtains
   them. That is the substantive new work in this slice (decision 4).
4. **Silent on server-side validation being nearly absent.** The handler validates
   only `name != ""` and UUID syntax. Empty `worker_ids`, an inverted window, a
   past window and duplicate names are all accepted, and a nonexistent `user_id`
   returns **500**, not 400. Client validation is therefore load-bearing, not
   decoration (decision 3).

## Decisions

### 1. Nothing here is secret. Do not import the enrollments secrecy machinery.

`reservationResponse` contains no credential, no token, no hash, and the 201 echoes
the same row the list returns. So: **no** `TokenRevealDialog`, **no** `gcTime: 0`,
**no** `web/src/test/secretLeaks.ts`, no clipboard affordance, no one-time-reveal
copy. Stated explicitly because the immediately preceding slice needed all of it
and the pattern is easy to cargo-cult. The only mildly sensitive field is
`user_id`, an opaque internal UUID, and it is not rendered (decision 5).

### 2. Delete ships, behind `ConfirmDialog`.

Unlike the Users tab's role change and the enrollments tab's revoke, the endpoint
is real, so the control is real. `ConfirmDialog` with `destructive`, body naming
the reservation and stating the effect: the listed workers return to the general
dispatch pool on the next dispatch cycle (~30s), and tasks already running on them
are unaffected. 204 has no body, so the handler returns `void` and the mutation
invalidates the bare `['reservations']` prefix. A 404 (someone else deleted it
first) surfaces in the inherited `actionError` box; the invalidation makes the row
disappear on the refetch, so the error is informational, not a dead end.

### 3. Client validation is stricter than the server, deliberately.

The form blocks three states the server would happily persist as dead rows:

- **`name` required** (trimmed non-empty). Server agrees; validating client-side
  avoids a pointless 400.
- **At least one worker required.** The server accepts `worker_ids: []`, which
  creates a reservation that reserves nothing, because `reservedIDs` is built only
  from that array. Submitting one is always a mistake.
- **`ends_at` must be after `starts_at`** when both are set. An inverted window can
  never satisfy `ListActiveReservations`.

Not blocked, deliberately: a window entirely in the past (a legitimate historical
record) and duplicate names (the server has no unique constraint; inventing one
client-side would be a lie about the data model).

### 4. Worker picker over the first 200 workers, by name.

The create form's worker field is a checkbox list of workers with a client-side
type-to-filter over the loaded set, submitting the selected `id`s. Rationale: a
free-text UUID field is hostile and unverifiable, and worker UUIDs appear nowhere
in the SPA's UI text.

Source: the existing `listWorkers` client (`web/src/workers/api.ts:49-52`), which
today hardcodes `limit=50` and no cursor. Change: give it an optional
`limit = 50` second parameter and have the picker pass **200**, the server's
`maxLimit` (`internal/api/pagination.go:207`). This is one optional parameter with
the existing default preserved, so every current call site and its asserted
`limit=50` URL are unchanged.

New hook `web/src/admin/reservations/useWorkerOptions.ts`:
`queryKey: ['workers', 'reservation-picker']`, `queryFn: () => listWorkers('name', 200)`,
**no `refetchInterval`** (the shared `useWorkers` polls every 3s for the live
workers page; a form does not need that), `staleTime: 30_000`. The key sits under
the bare `['workers']` prefix, so existing worker mutations invalidate it for free,
and it cannot collide with `['workers', sort]`.

Ceiling stated in the UI, not hidden: when `total > items.length` the picker shows
`showing first 200 of N workers by name - use the CLI for workers beyond this page`.
Flagged as a limitation rather than solved: a genuinely paginated or server-filtered
picker is a bigger unit and belongs in its own item if a fleet ever exceeds 200.

### 5. `selector` and `user_id` are omitted from the create form.

- **`selector`: omitted.** The scheduler never reads it. An input whose value has
  no effect is worse than a missing input, because an admin could believe they had
  reserved by label. It stays visible in the table (rows created via API, CLI or
  MCP may carry one) and the footnote explains it is informational.
  Note for the planner: the handler decodes into `map[string]string`, so a nested
  selector object 400s as `invalid request body` - another reason not to offer the
  field.
- **`user_id`: omitted from the body.** The handler defaults it to the
  authenticated admin (`internal/api/reservations.go:255-263`). It grants nothing,
  a picker would need a second admin-only query, and a wrong-but-valid UUID returns
  500. Consequence: no owner column, matching the enrollments tab's dropped
  `CREATED BY`, with an enricher proposed in the omissions table.

### 6. Columns, sorting, and honest footnote copy.

Columns: `NAME (sort) | PROJECT | WORKERS | STARTS (sort) | ENDS (sort) | STATUS | CREATED (sort) | ACT.`

- `CREATED` is added because it is the default sort key and needs a clickable
  header, exactly as on the enrollments tab.
- Sorting uses the shipped clickable-header idiom, not the hi-fi's `SortControl`
  dropdown, for the same reason as last slice: identical keys, no new primitive.
  All four fields are sortable in both directions.
- The hi-fi's dedicated `SELECTOR` column is dropped to pay for `STATUS` and
  `CREATED`. A selector, when present, renders as a small `muted` `sel` chip beside
  the name with the pairs in its `title`. Tradeoff: a set selector is less
  prominent than in the hi-fi; the alternative was a column that is empty for
  every row this UI can create.
- `WORKERS` renders one truncated chip per id (first 8 chars, full UUID in `title`),
  each wrapped in a react-router `<Link>` to `/workers/{id}`. `Chip` needs no
  change (it takes `onClick`, and a wrapping `Link` avoids touching a shared
  primitive). There is no FK on `worker_ids`, so a link can 404 on a deleted or
  revoked worker; that is the existing detail page's error state, not new
  machinery, and an unresolvable id is itself useful information.
- `STARTS` / `ENDS` show an absolute local `YYYY-MM-DD HH:MM` via a new
  `formatDateTime(iso)` in `web/src/lib/time.ts`, computed from `Date` getters (not
  `toLocaleString`, whose output is locale-dependent). Local, so the value matches
  what the admin typed into the `datetime-local` input. Absent keys render `-`.

Footnote copy, correcting the hi-fi rather than repeating it: a reservation
**removes** its `worker_ids` from the general dispatch pool - relay does not route
the owner's jobs to them, and `selector`, `project` and the owner are recorded but
never read by the scheduler; only explicit `worker_ids` are enforced. A reservation
is in force only while `starts_at <= now < ends_at` (either bound may be open).
Changes take effect on the next dispatch cycle (~30s) and never preempt a task
already running.

## Architecture

New module `web/src/admin/reservations/`, mirroring `web/src/admin/enrollments/`:

- `api.ts` - `Reservation`, `ReservationsPage`, `ReservationSort`,
  `ReservationSortField`, `CreateReservationBody`, plus
  `listReservations`, `createReservation`, `deleteReservation`.
- `useReservations.ts` - list query, `placeholderData: keepPreviousData`, no
  `refetchInterval`.
- `useReservationActions.ts` - `{ create, remove }`, both invalidating the bare
  `['reservations']` prefix on success. No `gcTime` override.
- `useWorkerOptions.ts` - picker query (decision 4).
- `ReservationsTab.tsx` - caption, create panel, `actionError` box, table, footer,
  footnote, confirm dialog.
- `ReservationsTable.tsx` - the table.
- `CreateReservationForm.tsx` - inline create panel.
- `WorkerPicker.tsx` - checkbox list with filter, `value: string[]` /
  `onChange`. Separate file because it is the one non-trivial input here and the
  form is already the widest surface in the slice.
- `reservationStatus.ts` - pure `deriveStatus(reservation, now)` and
  `statusTone(status)`.

Modified: `web/src/admin/tabs.ts` (one `ADMIN_TABS` entry
`{ slug: 'reservations', label: 'Reservations', Panel: ReservationsTab }`, after
`enrollments` per the hi-fi tab order, and drop this item from the comment's
not-yet-built list); `web/src/workers/api.ts` (optional `limit` on `listWorkers`);
`web/src/lib/time.ts` (add `formatDateTime`).

Reused, not rebuilt: `GlassPanel`, `PillButton`, `Chip`, `Button`, `Field`,
`Input`, `ConfirmDialog`, `computePageRange`, `useNow`, `apiFetch`, `ApiError`.

### API clients (exact calls)

```
listReservations({ sort, cursor })
    -> GET    /v1/reservations?sort=&limit=50[&cursor=]   -> ReservationsPage (200)

createReservation(body)
    -> POST   /v1/reservations   json: { name, worker_ids, project?, starts_at?, ends_at? }
                                                          -> Reservation (201)

deleteReservation(id)
    -> DELETE /v1/reservations/{id}                       -> void (204)
```

A body is always sent on create (`readJSON` is unconditional). `project`,
`starts_at`, `ends_at` are omitted when blank rather than sent as `""` / `null`;
`selector` and `user_id` are never sent. Dates are sent as full RFC3339 via
`new Date(localValue).toISOString()`, since `datetime-local` yields a
zone-less string the Go decoder rejects.

Query keys: `['reservations', sort, cursor]` for the list;
`['workers', 'reservation-picker']` for the picker. Both mutations invalidate the
**bare** `['reservations']` prefix, never a fully-qualified key
(`web/src/jobs/queryKeyDecoupling.test.tsx`). No optimistic insert or removal.

### Interaction detail

| Control | Behaviour |
|---|---|
| `+ Reserve workers` | Toggles the inline create panel; calls `create.reset()` first so a stale error cannot reappear. |
| Create form | `name` (required), `WorkerPicker` (at least one), `project` (optional), `starts_at` / `ends_at` (optional `datetime-local`). Submit disabled while pending or while validation fails; validation messages are inline, not alerts. |
| On 201 | Panel closes, list invalidated in the hook's `onSuccess`. No dialog, nothing to reveal. |
| `Delete` | Opens `ConfirmDialog` (destructive) naming the reservation and its worker count; confirming calls `remove.mutate(id)`. |
| Sort | Header click on `NAME` / `STARTS` / `ENDS` / `CREATED`, resetting the cursor (the server rejects a cursor whose sort key changed). |
| Pager | prev / next over the cursor stack, disabled while `isPlaceholderData`; `prev` remains reachable from an empty non-first page. |
| Errors | Create errors render in the create panel; delete errors in the shared `actionError` box; list errors in the inherited error card with Retry. |

## Omitted from the hi-fi

| Hi-fi element | Why omitted | Backend enabler? |
|---|---|---|
| Owner / "created by" column | `user_id` is a bare UUID with no join to `users`; 36 opaque characters are real but useless, exactly as with the enrollments tab's `CREATED BY`. | **Propose, do not file:** a `user_email` enricher on reservation rows, folded into `feature-2026-06-26-web-enabler-backend-endpoints`. |
| Worker **names** on chips | Rows carry `worker_ids` only, and the picker's 200-row window cannot be relied on to resolve arbitrary ids. Truncated UUID + link instead. | Same enricher item (a `worker_names` or embedded `workers` field). |
| Dedicated `SELECTOR` column | Replaced by a `sel` chip beside the name (decision 6). | No. |
| `SortControl` dropdown | Replaced by the shipped clickable-header idiom. | No. |
| Selector input on create | The scheduler never reads it; a control with no effect is a dead control that misleads. | No - a selector-aware scheduler is a product decision, not a UI gap. |
| Tab count badge | `AdminTabs` renders no counts by inherited decision; the footer shows the total. | No. |
| `VERSION`/`BUILD`/`DB`/`UPTIME` strip | Not part of this tab; belongs to `feature-2026-08-08-admin-server-overview-tab`. | Already filed. |
| Per-worker reservation lookup | Out of scope; the worker detail page's placeholder panel stays as-is. | Already filed: `feature-2026-06-05-worker-detail-reservations-panel`. |

## Security and system design

- **Threat model.** All three routes are `auth(admin(...))`; the UI gate is
  cosmetic and a forged client state yields 403s. No new endpoint, no widened
  surface. Nothing returned is a credential (decision 1).
- **Delete is the only destructive action in the admin console so far.** It is
  irreversible (a hard `DELETE`, no soft-delete column) and unaudited. Mitigation
  is the confirm dialog naming the reservation; the audit gap is
  `feature-2026-06-26-audit-log-admin-console-actions`. Blast radius is bounded:
  deleting a reservation only *returns* workers to the pool, so the failure mode is
  over-scheduling, never data loss or task interruption.
- **Availability is the real hazard of create, not confidentiality.** A
  reservation naming every worker makes the fleet undispatchable, and tasks sit
  `pending` with no user-visible explanation. Bounded by admin-only access, and by
  the picker showing which workers are being taken out. `POST` is not rate limited
  (only login and register are), which is acceptable for an admin-only route.
- **Load.** One list query, no polling, `limit=50`, cursor-paginated over indexed
  keys for all eight sort arms (migration `000013`); `total` from an indexed count.
  One picker query at `limit=200` while the form is open, `staleTime: 30_000`. One
  60s local timer, cleared on unmount, issuing no request. The scheduler reads
  reservations once per 30s dispatch cycle regardless of this UI.
- **Invariants.** No backend change, so the epoch fence, single job-spec pipeline,
  bounded-sender, identity-checked-teardown, interior-pointer and
  single-JSON-entry-point invariants are untouched. The frontend analogue holds:
  every request goes through `apiFetch`.

## Testing

Existing Vitest + MSW + `renderWithQuery` / `AuthProvider` harness; mirror the
enrollments module's test files. Assertions whose vacuity is the specific risk here:

**Pure units** (`reservationStatus.test.ts`, `time.test.ts`)
- `deriveStatus` with a fixed `now`, covering: both bounds absent -> `ACTIVE`; open
  start with future end -> `ACTIVE`; `starts_at` exactly `now` -> `ACTIVE`;
  `ends_at` exactly `now` -> `ENDED`; future start -> `SCHEDULED`; inverted window
  -> `ENDED`.
- **The predicate must match the SQL.** Assert the boundary cases in the same
  direction as `ListActiveReservations` (`starts_at <= NOW()`, `ends_at > NOW()`) -
  an off-by-one on either bound is a client that disagrees with the scheduler, which
  is the whole failure this module exists to prevent.
- `formatDateTime` is **TZ-independent by construction**: build the input from
  `new Date(y, m, d, h, min)` local components and assert the formatted string
  matches those components. A test asserting a literal against a `Z` input passes
  only in a UTC CI and is exactly the kind of green-that-proves-nothing this
  project has been bitten by.

**Contract, field-for-field against Go**
- `selector` parses as both `null` and `{}` and an object of string pairs without
  `undefined` or `null` leaking into the DOM. Positive control: the `sel` chip is
  present for a non-empty selector and absent for both `null` and `{}`.
- `project` / `starts_at` / `ends_at` **absent** (not null) render a plain hyphen.
- `worker_ids: []` renders no chips and does not crash.
- `createReservation` sends a body with `content-type: application/json` and
  **never** a `selector` or `user_id` key; assert on the parsed body, not a
  substring.
- `deleteReservation` tolerates a 204 with no body (a client that unconditionally
  parses JSON throws here).

**List**
- Rows render from a mocked envelope; header clicks issue exactly
  `name` / `-name` / `starts_at` / `-starts_at` / `ends_at` / `-ends_at` /
  `created_at` / `-created_at` and reset the cursor.
- Pagination walks the cursor stack; footer range from `computePageRange`; pager
  disabled while `isPlaceholderData`; `prev` reachable from an empty page 2.
- Loading, error-with-working-Retry, empty card.
- **No-poll, non-vacuous:** with the query mounted, advance fake timers several
  minutes and assert the MSW hit count stays 1, having first proved the counter can
  move (an explicit `refetch()` takes it to 2).
- A 60s tick flips a `SCHEDULED` pill to `ACTIVE` with **zero** extra requests
  (same instrument).

**Create**
- Validation: submit disabled with an empty name, with zero workers selected, and
  with `ends_at <= starts_at`; enabled once each is fixed. Each negative needs the
  paired positive so it cannot pass on a permanently-disabled button.
- Dates are sent as RFC3339 with an offset, not the raw `datetime-local` value.
- `create` invalidates the **bare** `['reservations']` prefix, with the list query
  mounted via `renderHook` so an **active observer** exists - a `fetchQuery` seed
  leaves none, `refetchType: 'active'` never fires, and the assertion passes
  vacuously.
- A create error renders in the panel and leaves the table mounted.

**Delete**
- `Delete` opens `ConfirmDialog`; Cancel issues **no** request (paired positive:
  Confirm issues exactly one `DELETE /v1/reservations/{id}`), so the gating
  assertion cannot pass because the button is inert.
- Success invalidates the bare prefix (same `renderHook` requirement).
- A 404 renders in the `actionError` box and still refetches the list.

**Picker**
- Requests `limit=200`; filter narrows the rendered set without a request; selecting
  and deselecting drives the submitted `worker_ids` in stable order.
- The over-ceiling note appears when `total > items.length` and is **absent** when
  it is not (both directions asserted).
- Does not poll (hit-count instrument, with the positive control).

**Shell integration**
- `/admin/reservations` renders the tab, marked `aria-current="page"`;
  `/admin/users` and `/admin/enrollments` still render theirs; the registry gained
  exactly one entry.

The plan's test bodies are guesses until they have been run RED; every absence
assertion above carries a required positive control in the representation the real
failure would take.

## Acceptance criteria

1. `/admin/reservations` renders in the existing admin shell for an admin, is
   marked active in the tab bar, and is reached by a single new `ADMIN_TABS` entry;
   non-admins are still redirected to `/jobs` with no Admin nav entry.
2. The tab lists reservations from `GET /v1/reservations?sort=&limit=50` with
   header-click sorting on `NAME`, `STARTS`, `ENDS`, `CREATED` in both directions,
   cursor prev/next, and the `SHOWING x-y of total · /v1/reservations · CURSOR
   PAGINATED` footer. No periodic refetch.
3. Columns are `NAME | PROJECT | WORKERS | STARTS | ENDS | STATUS | CREATED | ACT.`.
   No owner column. `worker_ids` render as truncated linked chips with the full
   UUID in `title`; a non-empty `selector` renders as a `sel` chip beside the name;
   `selector: null`, `{}`, and absent `project` / `starts_at` / `ends_at` all render
   without `null` or `undefined` reaching the DOM.
4. `STATUS` is `ACTIVE` / `SCHEDULED` / `ENDED`, derived client-side by the same
   predicate as `ListActiveReservations`, refreshed by a 60s local tick that issues
   no request.
5. Creating a reservation posts `{ name, worker_ids, project?, starts_at?, ends_at? }`
   and nothing else - no `selector`, no `user_id` - with dates as RFC3339, and
   invalidates the bare `['reservations']` prefix so the list refreshes with no
   manual reload.
6. The form blocks an empty name, an empty worker selection, and `ends_at <= starts_at`;
   workers are chosen from a filterable picker over the first 200 by name, which
   states the ceiling when more exist.
7. Deleting a reservation requires confirmation through `ConfirmDialog`
   (destructive), sends `DELETE /v1/reservations/{id}`, tolerates the empty 204
   body, and refreshes the list; Cancel sends nothing.
8. The footnote states that a reservation removes workers from the general dispatch
   pool for everyone, that `selector` / `project` / owner are not enforced, and that
   changes take effect on the next dispatch cycle without preempting running tasks.
9. No token-reveal dialog, no `gcTime: 0`, and no secret-leak harness anywhere in
   the module.
10. `npm test` and the production build are green; changes are confined to
    `web/src/admin/`, `web/src/workers/api.ts`, and `web/src/lib/time.ts`; no
    backend change; `web/dist` is reverted before the change set is assembled.

## Risks

- **Copy is the highest-value thing to get right.** If the tab implies that a
  reservation gives its owner priority access, admins will make reservations that do
  the opposite of what they intend. The footnote and confirm-dialog wording are
  requirements, not polish.
- **The picker's 200-row ceiling** is the one place this slice can be outgrown by a
  real fleet. It fails visibly (a stated note), not silently, but a >200-worker
  deployment needs a follow-up item.
- **`formatDateTime` and `deriveStatus` are the two units where a green test is
  most likely to be vacuous** - the former via CI timezone, the latter via boundary
  direction. Both have prescribed constructions above.
- **Touching `web/src/workers/api.ts`** puts this slice in a shared module. The
  change is one optional parameter with the default preserved; a reviewer should
  confirm no existing call site or asserted URL changed.
- **`web/dist` is tracked but stale**; a frontend build dirties it.
- **Scope creep into the enricher.** Worker names and owner emails on reservation
  rows would improve every cell in this table and belong to the existing web-enabler
  item, not here.
