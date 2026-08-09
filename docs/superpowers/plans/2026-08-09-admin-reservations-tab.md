# Admin Console - Reservations Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a third admin-console tab, Reservations, that lists worker reservations, creates one from a worker picker, and deletes one behind a confirm dialog - with copy that tells the truth about what a reservation does (it removes workers from the dispatch pool for everyone; it grants nobody anything).

**Architecture:** A new feature module `web/src/admin/reservations/` that is a structural clone of the shipped `web/src/admin/enrollments/` tree (api clients -> list query hook -> actions hook -> presentational table -> inline create panel -> composing tab), plus a `WorkerPicker` because creating a reservation needs worker UUIDs. The tab reaches the router through a single new `ADMIN_TABS` entry. Status is derived client-side by transcribing the scheduler's own SQL predicate, which is honest here because the list endpoint is unfiltered.

**Tech Stack:** React 18, TypeScript, TanStack Query v5, react-router-dom v7, Tailwind v4 (Holo tokens), Vitest + Testing Library + MSW.

**Spec:** `docs/superpowers/specs/2026-08-09-admin-reservations-tab.md` (approved; do not reopen its decisions)

---

## Slice independence declaration

- **Backend slice: NONE. Zero Go files change. Zero `.sql` files change, therefore no `make generate`,** and no `*.sql.go` / `models.go` involvement. All three endpoints already exist and are already `auth(admin(...))`. None of the six Invariants is in play. The frontend analogue of the last one does apply: every request goes through `apiFetch` (`web/src/lib/api.ts:29`), never a bare `fetch`.
- **Frontend slice: ONE, and it is SEQUENTIAL.** Do not split these tasks across two engineers. Tasks 2 and 4-9 all write into the same new `web/src/admin/reservations/` directory; Task 3 edits shared `web/src/workers/api.ts`; Task 10 edits `web/src/admin/tabs.ts` plus two shipped test files. The dependency chain is near-linear: Task 9 imports the output of Tasks 1-8, Task 10 imports Task 9. The project has been burned by concurrent writers on shared frontend files.
- **Parallelism available to the conductor: none within this plan.** Unrelated work elsewhere in the repo can run alongside it.

---

## Verified backend surface (I read it; you should too before Task 2)

Read: `internal/api/server.go:134-136`, `internal/api/reservations.go` (whole file), `internal/store/query/reservations.sql`, `internal/api/pagination.go:205-249`, `internal/api/server.go:235-240` (`rawJSON`), `internal/scheduler/dispatch.go:178-223`.

**The spec's contract table is correct on every claim.** Nothing in it needed correcting. Six points it makes that the implementation hangs on, with the evidence:

| Claim | Verdict | Evidence |
|---|---|---|
| All three routes exist and are `auth(admin(...))` | Confirmed | `server.go:134-136` |
| List is 200 `{items, next_cursor, total}`; sort keys `created_at\|name\|starts_at\|ends_at` with optional `-`, default `-created_at`; all eight arms implemented | Confirmed | `ReservationsSortSpec` `reservations.go:55-63`; the eight-arm switch `:106-217` with a `panic` default at `:215-216`; envelope `:219` |
| `?limit=` 1..200, default 50; `?limit=0` is a **400, not a clamp** | Confirmed | `pagination.go:206-207`, `:242-248` |
| **List is unfiltered** - past, future and current rows all appear | Confirmed | `reservations.sql:16-17` (`COUNT(*)` with no `WHERE`); every `ListReservationsPage*` filters on the cursor only |
| Create validates only `name != ""` and UUID syntax; a **valid-but-nonexistent `user_id` yields 500**, not 400 | Confirmed | `reservations.go:243-246` (name), `:256-263` / `:266-274` (UUID syntax only), `:294-297` (any `CreateReservation` error, including the `users(id)` FK violation, becomes 500 `create reservation failed`) |
| Delete is 204 no-body; 404 `reservation not found`; 400 `invalid reservation id` | Confirmed | `reservations.go:302-323` |

**Nullability - get this exactly right; the same trap bit the enrollments tab.** `reservationResponse` (`reservations.go:13-23`):

- `selector` has **no** `omitempty` and is a `json.RawMessage` fed by `rawJSON` (not `rawObject`). `handleCreateReservation` does `json.Marshal(body.Selector)` on a nil `map[string]string`, which is the four bytes `null`; `rawJSON` only substitutes `{}` for a **zero-length** slice (`server.go:236-240`), so `null` passes straight through. **`"selector": null` is a real response value.** Rows whose column kept its default read `{}`. TS type: `Record<string, string> | null`, and the render path must tolerate both.
- `project` (`*string`), `starts_at` and `ends_at` (`*time.Time`) all carry `omitempty` on a **pointer**, so when NULL the **key is absent**, not null. TS type: `project?: string`, `starts_at?: string`, `ends_at?: string`. A `| null` on these three, or a bare optional on `selector`, would both be wrong.
- `worker_ids` is built with `make([]string, len(...))` (`:26`), so it is always an array - `[]` when empty, never `null`.
- `id`, `name`, `user_id`, `created_at` are always present. `user_id` is a bare user UUID with **no join to `users`**.
- Timestamps are Go `time.Time`, RFC3339 with nanosecond precision. Parse with `new Date()`; never string-compare.

**What a reservation actually does - the whole reason the copy in this plan is load-bearing.** `reservations` is read in exactly one place. `ListActiveReservations` (`reservations.sql:19-23`) returns rows where `(ends_at IS NULL OR ends_at > NOW()) AND (starts_at IS NULL OR starts_at <= NOW())`. `Dispatcher.selectWorker` unions their `worker_ids` into a `reservedIDs` set (`dispatch.go:185-191`) and then, for **every** task, `continue`s past any worker in that set (`dispatch.go:221-223`). Therefore:

- A reservation **removes workers from the general dispatch pool**. It does not route the reserving user's work to them. `user_id` grants nothing.
- `selector` and `project` are never read by the scheduler. Only explicit `worker_ids` are enforced.
- Creating or deleting one never preempts a task already running; it only affects future dispatch, from the next 30s tick (`dispatch.go:50`). No reservation write emits a NOTIFY or calls `Trigger()`.

---

## What is inherited, not re-specified

This is the third instance of a shipped pattern. Read these before writing anything. "Mirror X at `file:line`" is the literal instruction: copy the shape, change the nouns.

| Inherited thing | Source to mirror |
|---|---|
| Whole list-page shape: `sort`/`cursor`/`stack`/`offsets` state, `resetPaging`, `pickSort`, `next`, `prev`, `computePageRange`, mono `SHOWING` footer, `isPlaceholderData` pager gating, loading skeleton / error-with-Retry / empty triad, empty-page `prev` escape hatch | `web/src/admin/enrollments/EnrollmentsTab.tsx:16-222` (near-verbatim template for Task 9) |
| List query: `keepPreviousData`, **no** `refetchInterval` | `web/src/admin/enrollments/useAgentEnrollments.ts:14-20` |
| Mutation shape: bare-prefix invalidation, no optimistic update, `reset()` before reopening the form | `web/src/admin/enrollments/useAgentEnrollmentActions.ts:25-38` |
| Header-click sorting: `toggleSort`, `caret`, `ariaSort`, `aria-sort` on the `columnheader` | `web/src/admin/enrollments/EnrollmentsTab.tsx:16-21`, `EnrollmentsTable.tsx:18-29`, `:47-66` |
| Inline create panel (not a modal), form-level error line, warn box | `web/src/admin/enrollments/CreateEnrollmentForm.tsx:26-98` |
| Destructive row action -> `ConfirmDialog` driven by a `confirm` state object | `web/src/admin/users/UsersTab.tsx:24`, `:42`, `:115-120`, `:278-295` |
| Shared inline `actionError` box above the table | `web/src/admin/users/UsersTab.tsx:58`, `:263-267` |
| Row action buttons: `MINI_*` literal classes, `disabled={busy}`, **row identity in `aria-label`** | `web/src/admin/users/UsersTable.tsx:14-17`, `:169-199` |
| Client status derived by a pure module with `now` injected as a prop; `useNow(60_000)` local tick, zero requests | `web/src/admin/enrollments/enrollmentStatus.ts`, `web/src/lib/useNow.ts:8-15` |
| `Chip` tones `accent` / `muted` / `warn`; `opacity-[0.55]` row for a dead row | `web/src/components/holo/Chip.tsx:8-12`, `EnrollmentsTable.tsx:74-76` |
| Absent-key placeholder is a plain ASCII hyphen, never an em dash | `EnrollmentsTable.tsx:78-83` |
| Single fetch entry point, `ApiError`, 204 -> `undefined` | `web/src/lib/api.ts:4-13`, `:29-59` (note `:57`: 204 and 202 return no body) |
| Confirm dialog a11y baseline (`role="dialog"`, `aria-modal`, `aria-labelledby`, Cancel focused, Escape dismisses, no focus trap) | `web/src/components/ConfirmDialog.tsx:15-70` - **reuse it as-is, do not modify it** |
| Test harness | `web/src/test/setup.ts` (MSW `onUnhandledRequest: 'error'` - every touched endpoint needs a handler), `web/src/test/setup-helpers.ts` re-exports `server`; representative tab test `web/src/admin/enrollments/EnrollmentsTab.test.tsx:25-46`; the fake-timer tick idiom at `:226-256` |
| Holo primitives | `GlassPanel` (`as` prop for `form`), `PillButton` (variants `primary`/`ghost`/`muted`/`danger`), `Chip`, barrel `web/src/components/holo/index.ts`; `web/src/components/Field.tsx`, `Input.tsx`, `Button.tsx` |

---

## Scope guard - do NOT build

- **Nothing here is secret.** No `TokenRevealDialog`, no `gcTime: 0`, no `web/src/test/secretLeaks.ts` import, no clipboard affordance, no one-time-reveal copy, no console-secrecy suite. `reservationResponse` contains no credential and the 201 echoes the same row the list returns. The immediately preceding slice needed all of that machinery; **this one must not cargo-cult it.** The only mildly sensitive field is `user_id`, an opaque UUID, and it is never rendered.
- **No `selector` input on the create form.** The scheduler never reads it, and the handler decodes it into `map[string]string` so a nested object 400s as `invalid request body`. It stays visible in the table (rows created by API/CLI/MCP may carry one).
- **No `user_id` in the create body.** The handler defaults it to the authenticated admin (`reservations.go:255-263`); a wrong-but-valid UUID returns 500.
- **No owner column, no worker-name resolution.** `user_id` is a bare UUID with no join, and the picker's 200-row window cannot resolve arbitrary ids. Both are proposals folded into `feature-2026-06-26-web-enabler-backend-endpoints`, not work here.
- **No dedicated `SELECTOR` column** (replaced by a `sel` chip beside the name), **no `SortControl` dropdown** (clickable headers instead), **no tab count badge**, **no `VERSION`/`BUILD`/`DB`/`UPTIME` strip**, **no per-worker reservations panel**.
- **No backend change, no paginated/server-filtered picker, no unique-name validation, no blocking of a wholly-past window.** All four are recorded in the spec as out of scope or deliberate.
- **No edits to `ConfirmDialog`, `Chip`, or any other shared primitive.** A wrapping `<Link>` is how a chip becomes a link.
- **No edits to the shipped Users or Enrollments tabs** beyond the two test-file edits the registry change forces (Task 10).

---

## Conventions for every task

- All commands run from the `web/` directory.
- Single file: `npx vitest run src/<path>.test.tsx`. Full suite: `npm test`.
- TDD: write the failing test, run it and watch it fail with the stated message, implement, run it and watch it pass, commit.
- MSW is fail-closed. Any test that mounts `ReservationsTab` needs a `GET /v1/reservations` handler; any test that opens the create panel also needs `GET /v1/workers`. `ReservationsTab` does not use `useAuth`, so it needs **no** `AuthProvider` and no `/v1/users/me` handler.
- **`ReservationsTable` renders react-router `<Link>`s**, so its tests and the tab's tests need a `MemoryRouter` wrapper. This is a difference from the enrollments tests, which needed no router - forgetting it produces `useHref() may be used only in the context of a <Router>`.
- House rule: **never an em dash or en dash.** Placeholders are the plain ASCII hyphen `-`.
- Never reformat code you were not asked to change.

---

## File Structure

**New files** (all under `web/src/admin/reservations/` unless stated)

| File | Responsibility |
|---|---|
| `reservationStatus.ts` | Pure `deriveStatus(reservation, now)` + `statusTone`. Transcribes `ListActiveReservations`. |
| `reservationStatus.test.ts` | Boundary tests at a fixed `now` in the SQL's own direction. |
| `api.ts` | `Reservation`, `ReservationsPage`, `ReservationSort`, `ReservationSortField`, `CreateReservationBody`; `listReservations`, `createReservation`, `deleteReservation`. |
| `api.test.ts` | Field-for-field contract: null vs absent, `[]` worker_ids, exact create body, 204 with no body. |
| `useReservations.ts` | List query. `keepPreviousData`, no `refetchInterval`. |
| `useReservations.test.tsx` | Query key, params, non-vacuous no-poll. |
| `useReservationActions.ts` | `{ create, remove }`, both invalidating the bare `['reservations']` prefix. |
| `useReservationActions.test.tsx` | Exact body/path + active-observer invalidation + 404 still refetches. |
| `useWorkerOptions.ts` | Picker query at `limit=200`, `staleTime: 30_000`, no polling. |
| `useWorkerOptions.test.tsx` | Key, `limit=200`, no-poll with positive control. |
| `WorkerPicker.tsx` | Checkbox list + client-side filter + visible 200-row ceiling note. |
| `WorkerPicker.test.tsx` | Request shape, filter without a request, stable-order selection, ceiling note both directions. |
| `ReservationsTable.tsx` | Presentational table, derived status pill, sortable NAME/STARTS/ENDS/CREATED, linked worker chips. |
| `ReservationsTable.test.tsx` | Columns, null/absent handling, `[]` workers, sort wiring, per-row delete identity. |
| `CreateReservationForm.tsx` | Inline create panel: name, picker, project, `datetime-local` window, client validation. |
| `CreateReservationForm.test.tsx` | Three validation blocks with paired positives, RFC3339 dates, exact body. |
| `ReservationsTab.tsx` | Composition: caption, create panel, `actionError`, table, footer, honest footnote, confirm dialog. |
| `ReservationsTab.test.tsx` | States, sort, pager, create flow, delete-behind-confirm, 60s tick, **the honesty assertions**. |

**Modified files**

| File | Change |
|---|---|
| `web/src/lib/time.ts` | Append `formatDateTime`. Do not touch `formatRelativeTime` or `formatTimeUntil`. |
| `web/src/lib/time.test.ts` | Append a `formatDateTime` describe block. |
| `web/src/workers/api.ts:47-52` | Add an optional second parameter `limit = 50` to `listWorkers`. Nothing else. |
| `web/src/workers/api.test.ts` | Append one test for the explicit limit. The existing `limit=50` test at `:17-29` stays untouched and **is** the regression guard. |
| `web/src/admin/tabs.ts:2-3`, `:11-22` | Import `ReservationsTab`; one `ADMIN_TABS` entry after `enrollments`; drop reservations from the not-yet-built comment. |
| `web/src/admin/AdminTabs.test.tsx:16`, `:20-26`, `:28-36`, `:49-54` | Registry now holds three slugs / three links; `Reservations` is no longer absent; one new current-tab test. |
| `web/src/admin/AdminPage.test.tsx:20-53`, end of file | Add a `GET /v1/reservations` handler to `renderAt`; add two shell-integration tests. |

**Reused, not rebuilt:** `GlassPanel`, `PillButton`, `Chip`, `Button`, `Field`, `Input`, `ConfirmDialog`, `computePageRange`, `useNow`, `apiFetch`, `ApiError`.

---

## Task 1: formatDateTime and reservation status derivation

Two pure units, one commit. Both are the ones the spec flags as most likely to be vacuously green, for different reasons: `formatDateTime` via CI timezone, `deriveStatus` via boundary direction.

**Files:**
- Modify: `web/src/lib/time.ts` (append), `web/src/lib/time.test.ts` (append)
- Create: `web/src/admin/reservations/reservationStatus.ts`, `web/src/admin/reservations/reservationStatus.test.ts`

- [ ] **Step 1: Write the failing tests**

Append to `web/src/lib/time.test.ts` (and extend the import at `:1-2` to `import { formatDateTime, formatRelativeTime, formatTimeUntil } from './time'`):

```ts
describe('formatDateTime', () => {
  // TZ-INDEPENDENT BY CONSTRUCTION. The input is built from LOCAL Date components
  // and the expected string spells out those same components, so this holds in
  // every runner timezone. A test that asserted a literal against a 'Z' input
  // would pass only in a UTC CI, which is worse than no test at all.
  test('renders an instant as local YYYY-MM-DD HH:MM', () => {
    const local = new Date(2026, 7, 9, 14, 5) // 2026-08-09 14:05 LOCAL
    expect(formatDateTime(local.toISOString())).toBe('2026-08-09 14:05')
  })

  test('zero-pads month, day, hour and minute', () => {
    const local = new Date(2026, 0, 3, 9, 7) // 2026-01-03 09:07 LOCAL
    expect(formatDateTime(local.toISOString())).toBe('2026-01-03 09:07')
  })

  test('midnight is 00:00, not 24:00 and not blank', () => {
    const local = new Date(2026, 11, 31, 0, 0)
    expect(formatDateTime(local.toISOString())).toBe('2026-12-31 00:00')
  })

  test('the shape is fixed, so it is not toLocaleString output', () => {
    // toLocaleString would emit '8/9/2026, 2:05:00 PM' under en-US and something
    // else entirely under another ICU locale. This asserts the invariant shape.
    const local = new Date(2026, 7, 9, 14, 5)
    expect(formatDateTime(local.toISOString())).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  })

  test('positive control: off UTC the local rendering really does differ from the ISO text', () => {
    // Proves the first test is exercising a conversion rather than an accidental
    // pass-through. The control is on the TEST DATA, not on the function: for this
    // same instant the raw ISO text only reads '2026-01-15 09:30' at UTC+0.
    const local = new Date(2026, 0, 15, 9, 30)
    expect(formatDateTime(local.toISOString())).toBe('2026-01-15 09:30')
    const isoText = local.toISOString().slice(0, 16).replace('T', ' ')
    if (local.getTimezoneOffset() !== 0) {
      expect(isoText).not.toBe('2026-01-15 09:30')
    }
  })

  test('a nanosecond-precision RFC3339 timestamp parses (Go marshals time.Time this way)', () => {
    const local = new Date(2026, 7, 9, 14, 5)
    const withNanos = local.toISOString().replace('.000Z', '.123456789Z')
    expect(formatDateTime(withNanos)).toBe('2026-08-09 14:05')
  })
})
```

Create `web/src/admin/reservations/reservationStatus.test.ts`:

```ts
import { expect, test } from 'vitest'
import { deriveStatus, statusTone } from './reservationStatus'

const NOW = new Date('2026-08-09T12:00:00Z')

test('both bounds absent is ACTIVE (an open-ended reservation)', () => {
  expect(deriveStatus({}, NOW)).toBe('ACTIVE')
})

test('open start with a future end is ACTIVE', () => {
  expect(deriveStatus({ ends_at: '2026-08-09T13:00:00Z' }, NOW)).toBe('ACTIVE')
})

test('open end with a past start is ACTIVE', () => {
  expect(deriveStatus({ starts_at: '2026-08-09T11:00:00Z' }, NOW)).toBe('ACTIVE')
})

// The two boundary tests that matter. The scheduler's predicate is
// `starts_at <= NOW()` AND `ends_at > NOW()` (internal/store/query/reservations.sql:21-22),
// so at exactly starts_at the row IS active and at exactly ends_at it is NOT.
// Flipping either comparison makes this client disagree with the dispatcher, which
// is the entire failure this module exists to prevent.
test('starts_at exactly now is ACTIVE (starts_at <= NOW)', () => {
  expect(deriveStatus({ starts_at: '2026-08-09T12:00:00Z' }, NOW)).toBe('ACTIVE')
})

test('ends_at exactly now is ENDED (ends_at > NOW is false)', () => {
  expect(deriveStatus({ ends_at: '2026-08-09T12:00:00Z' }, NOW)).toBe('ENDED')
})

test('a future start is SCHEDULED', () => {
  expect(deriveStatus({ starts_at: '2026-08-09T18:00:00Z' }, NOW)).toBe('SCHEDULED')
})

test('a wholly past window is ENDED', () => {
  expect(
    deriveStatus({ starts_at: '2026-08-01T00:00:00Z', ends_at: '2026-08-02T00:00:00Z' }, NOW),
  ).toBe('ENDED')
})

test('an inverted window is ENDED, not SCHEDULED', () => {
  // The server accepts ends_at < starts_at and such a row can NEVER satisfy
  // ListActiveReservations, so it must read as dead rather than pending. ENDED is
  // checked first precisely to make this true.
  expect(
    deriveStatus({ starts_at: '2026-08-20T00:00:00Z', ends_at: '2026-08-10T00:00:00Z' }, NOW),
  ).toBe('ENDED')
})

test('ACTIVE agrees with the scheduler predicate on a matrix of windows', () => {
  // sqlSaysActive is a transcription of reservations.sql:21-22, written out
  // independently of deriveStatus' precedence structure. It only guards
  // ACTIVE-vs-not-ACTIVE - which is exactly the property shared with the
  // dispatcher; the ENDED-vs-SCHEDULED split is guarded by the cases above.
  function sqlSaysActive(r: { starts_at?: string; ends_at?: string }): boolean {
    const endsOk = r.ends_at === undefined || new Date(r.ends_at).getTime() > NOW.getTime()
    const startsOk = r.starts_at === undefined || new Date(r.starts_at).getTime() <= NOW.getTime()
    return endsOk && startsOk
  }
  const past = '2026-08-01T00:00:00Z'
  const exact = '2026-08-09T12:00:00Z'
  const future = '2026-09-01T00:00:00Z'
  const cases: { starts_at?: string; ends_at?: string }[] = []
  for (const s of [undefined, past, exact, future]) {
    for (const e of [undefined, past, exact, future]) {
      cases.push({ ...(s ? { starts_at: s } : {}), ...(e ? { ends_at: e } : {}) })
    }
  }
  for (const c of cases) {
    expect(deriveStatus(c, NOW) === 'ACTIVE').toBe(sqlSaysActive(c))
  }
  // The matrix must contain both outcomes, or the loop above proves nothing.
  expect(cases.filter(sqlSaysActive).length).toBeGreaterThan(0)
  expect(cases.filter((c) => !sqlSaysActive(c)).length).toBeGreaterThan(0)
})

test('tones map to the three Chip tones that exist', () => {
  expect(statusTone('ACTIVE')).toBe('accent')
  expect(statusTone('SCHEDULED')).toBe('warn')
  expect(statusTone('ENDED')).toBe('muted')
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```
npx vitest run src/lib/time.test.ts src/admin/reservations/reservationStatus.test.ts
```

Expected: FAIL - `formatDateTime is not a function` in `time.test.ts`, and `Failed to resolve import "./reservationStatus"`.

- [ ] **Step 3: Implement both units**

Append to `web/src/lib/time.ts`:

```ts
// Absolute LOCAL wall-clock rendering, 'YYYY-MM-DD HH:MM'.
//
// Local, not UTC, because the only writers of these values are the admin's own
// `datetime-local` inputs (web/src/admin/reservations/CreateReservationForm.tsx):
// the cell must read back what they typed.
//
// Built from Date getters rather than toLocaleString/Intl on purpose. Intl output
// depends on the runner's ICU locale, which makes it both unassertable in tests
// and inconsistent across machines; getters give one fixed shape everywhere.
export function formatDateTime(iso: string): string {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
```

Create `web/src/admin/reservations/reservationStatus.ts`:

```ts
export type ReservationStatus = 'ACTIVE' | 'SCHEDULED' | 'ENDED'

// `reservations` has NO lifecycle column - no status, consumed, released or deleted
// field. The row is read in exactly one place, Dispatcher.selectWorker, via
// ListActiveReservations (internal/store/query/reservations.sql:19-23):
//
//   WHERE (ends_at IS NULL OR ends_at > NOW())
//     AND (starts_at IS NULL OR starts_at <= NOW())
//
// GET /v1/reservations is UNFILTERED (CountReservations is a bare COUNT(*), and the
// page queries filter on the cursor only), so unlike the enrollments list this
// client sees past, current and future rows and can reproduce that predicate
// exactly. ACTIVE here means "in the dispatcher's reservedIDs set right now".
//
// Comparison directions are load-bearing: `<=` on starts_at and a strict `>` on
// ends_at, matching the SQL. ENDED is tested FIRST so that an inverted window
// (ends_at < starts_at, which the server accepts and which can never be active)
// reads as dead rather than pending.
//
// Reads the local clock, so a badly skewed browser mislabels a row. Accepted for
// the same reason as enrollmentStatus.ts: the server exposes no status to prefer.
export function deriveStatus(
  r: { starts_at?: string; ends_at?: string },
  now: Date,
): ReservationStatus {
  const t = now.getTime()
  if (r.ends_at !== undefined && new Date(r.ends_at).getTime() <= t) return 'ENDED'
  if (r.starts_at !== undefined && new Date(r.starts_at).getTime() > t) return 'SCHEDULED'
  return 'ACTIVE'
}

// The three tones Chip already ships (web/src/components/holo/Chip.tsx:8-12).
export function statusTone(status: ReservationStatus): 'accent' | 'warn' | 'muted' {
  if (status === 'ENDED') return 'muted'
  if (status === 'SCHEDULED') return 'warn'
  return 'accent'
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
npx vitest run src/lib/time.test.ts src/admin/reservations/reservationStatus.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/time.ts web/src/lib/time.test.ts web/src/admin/reservations/reservationStatus.ts web/src/admin/reservations/reservationStatus.test.ts
git commit -m "feat(web): formatDateTime and reservation status derivation"
```

---

## Task 2: Reservation API clients and types

The nullability contract lives here. Mirror `web/src/admin/enrollments/api.ts` for shape.

**Files:**
- Create: `web/src/admin/reservations/api.ts`
- Test: `web/src/admin/reservations/api.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/reservations/api.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ApiError } from '../../lib/api'
import { createReservation, deleteReservation, listReservations } from './api'

const ROW = {
  id: 'r1',
  name: 'gpu-farm-hold',
  selector: { tier: 'gpu' },
  worker_ids: ['11111111-1111-1111-1111-111111111111'],
  user_id: '99999999-9999-9999-9999-999999999999',
  created_at: '2026-08-09T09:30:00Z',
  project: 'atlas',
  starts_at: '2026-08-09T10:00:00Z',
  ends_at: '2026-08-11T10:00:00Z',
}

test('listReservations sends sort and limit=50, omits an empty cursor, returns the envelope', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/reservations', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: 'c2', total: 7 })
    }),
  )
  const page = await listReservations({ sort: '-created_at', cursor: '' })
  expect(params?.get('sort')).toBe('-created_at')
  // ?limit=0 is a 400 rather than a clamp (internal/api/pagination.go:244), so the
  // page size is always stated explicitly.
  expect(params?.get('limit')).toBe('50')
  expect(params?.has('cursor')).toBe(false)
  expect(page.items[0].name).toBe('gpu-farm-hold')
  expect(page.next_cursor).toBe('c2')
  expect(page.total).toBe(7)
})

test('listReservations sends the cursor and all EIGHT server sort values', async () => {
  const seen: string[] = []
  server.use(
    http.get('/v1/reservations', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(`${p.get('sort')}|${p.get('cursor')}`)
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const sorts = [
    'created_at',
    '-created_at',
    'name',
    '-name',
    'starts_at',
    '-starts_at',
    'ends_at',
    '-ends_at',
  ] as const
  for (const sort of sorts) await listReservations({ sort, cursor: 'cur1' })
  expect(seen).toEqual(sorts.map((s) => `${s}|cur1`))
})

test('selector arrives as JSON null and as {} and as pairs - all three parse', async () => {
  server.use(
    http.get('/v1/reservations', () =>
      HttpResponse.json({
        items: [
          { ...ROW, id: 'r-null', selector: null },
          { ...ROW, id: 'r-empty', selector: {} },
          { ...ROW, id: 'r-pairs', selector: { tier: 'gpu', site: 'west' } },
        ],
        next_cursor: '',
        total: 3,
      }),
    ),
  )
  const page = await listReservations({ sort: '-created_at', cursor: '' })
  // `selector` has NO omitempty and goes through rawJSON, not rawObject
  // (internal/api/reservations.go:16, :45; internal/api/server.go:236-240), so a
  // create with no selector marshals a nil map to the literal `null` and the key is
  // PRESENT with value null. Column-default rows read {}. The type is
  // `Record<string, string> | null` and must tolerate both.
  expect(page.items[0].selector).toBeNull()
  expect(page.items[1].selector).toEqual({})
  expect(page.items[2].selector).toEqual({ tier: 'gpu', site: 'west' })
})

test('project / starts_at / ends_at keys are ABSENT (not null) when the column is NULL', async () => {
  server.use(
    http.get('/v1/reservations', () =>
      HttpResponse.json({
        items: [
          {
            id: 'r2',
            name: 'open-ended',
            selector: null,
            worker_ids: [],
            user_id: ROW.user_id,
            created_at: ROW.created_at,
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
  )
  const page = await listReservations({ sort: '-created_at', cursor: '' })
  const r = page.items[0]
  // omitempty on a POINTER field omits the key entirely
  // (internal/api/reservations.go:19-21), so these are `?: string`, never
  // `string | null`, and consumers must handle undefined - not null.
  for (const key of ['project', 'starts_at', 'ends_at']) {
    expect(key in r).toBe(false)
  }
  expect(r.project).toBeUndefined()
  // Positive control on the same instrument: a key that IS always present.
  expect('created_at' in r).toBe(true)
  // worker_ids is built with make() (:26), so it is [] and never null.
  expect(r.worker_ids).toEqual([])
})

test('createReservation sends the exact body, as JSON, with NO selector and NO user_id', async () => {
  let body: Record<string, unknown> | undefined
  let contentType: string | null = null
  server.use(
    http.post('/v1/reservations', async ({ request }) => {
      // Mirrors readJSON (internal/api/server.go:196-211): an absent or
      // unparseable body is a 400 'invalid request body'. That is what makes this
      // non-vacuous - a client that stopped sending a body would fail here exactly
      // as it does against the real server.
      const raw = await request.text()
      if (raw === '') return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      body = JSON.parse(raw)
      contentType = request.headers.get('content-type')
      return HttpResponse.json({ ...ROW, id: 'r9' }, { status: 201 })
    }),
  )
  const created = await createReservation({
    name: 'gpu-farm-hold',
    worker_ids: [ROW.worker_ids[0]],
    project: 'atlas',
    starts_at: '2026-08-09T10:00:00.000Z',
  })
  expect(body).toEqual({
    name: 'gpu-farm-hold',
    worker_ids: [ROW.worker_ids[0]],
    project: 'atlas',
    starts_at: '2026-08-09T10:00:00.000Z',
  })
  // Asserted on the PARSED body, not a substring: `selector` is never sent because
  // the scheduler never reads it, and `user_id` is never sent because the handler
  // defaults it to the authenticated admin and a valid-but-nonexistent UUID would
  // produce a 500 from the users FK (internal/api/reservations.go:255-263, :294-297).
  expect('selector' in body!).toBe(false)
  expect('user_id' in body!).toBe(false)
  expect(contentType).toContain('application/json')
  expect(created.id).toBe('r9')
})

test('createReservation omits blank optionals rather than sending empty strings', async () => {
  let body: Record<string, unknown> | undefined
  server.use(
    http.post('/v1/reservations', async ({ request }) => {
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ROW, id: 'r9' }, { status: 201 })
    }),
  )
  await createReservation({ name: 'minimal', worker_ids: [ROW.worker_ids[0]] })
  expect(body).toEqual({ name: 'minimal', worker_ids: [ROW.worker_ids[0]] })
  for (const key of ['project', 'starts_at', 'ends_at']) {
    expect(key in body!).toBe(false)
  }
})

test('a create 500 surfaces as an ApiError with the server message', async () => {
  // Reachable in production only via a valid-but-nonexistent user_id, which this UI
  // never sends. Kept because the error path must still render, not crash.
  server.use(
    http.post('/v1/reservations', () =>
      HttpResponse.json({ error: 'create reservation failed' }, { status: 500 }),
    ),
  )
  await expect(createReservation({ name: 'x', worker_ids: [] })).rejects.toMatchObject({
    status: 500,
    code: 'create reservation failed',
  })
})

test('deleteReservation issues DELETE on the id path and tolerates a 204 with NO body', async () => {
  let method: string | undefined
  let path: string | undefined
  server.use(
    http.delete('/v1/reservations/:id', ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      // A real 204 has no body at all. A client that unconditionally calls
      // res.json() throws 'Unexpected end of JSON input' here.
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(deleteReservation('r1')).resolves.toBeUndefined()
  expect(method).toBe('DELETE')
  expect(path).toBe('/v1/reservations/r1')
})

test('a delete 404 surfaces as ApiError(404, "reservation not found")', async () => {
  server.use(
    http.delete('/v1/reservations/:id', () =>
      HttpResponse.json({ error: 'reservation not found' }, { status: 404 }),
    ),
  )
  const err = await deleteReservation('gone').catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err).toMatchObject({ status: 404, code: 'reservation not found' })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/reservations/api.test.ts`
Expected: FAIL - `Failed to resolve import "./api"`.

- [ ] **Step 3: Implement the clients and types**

Create `web/src/admin/reservations/api.ts`:

```ts
import { apiFetch } from '../../lib/api'

// Mirrors reservationResponse / toReservationResponse
// (internal/api/reservations.go:13-53). The nullability split is the whole point of
// this comment:
//
//  - `selector` has NO omitempty and is a json.RawMessage produced by rawJSON, not
//    rawObject (:16, :45; server.go:236-240). A create with no selector marshals a
//    nil map to the literal `null`, which rawJSON passes through unchanged, so
//    `"selector": null` is a REAL response value. Rows that kept the column default
//    read `{}`. Both must be tolerated - hence `| null` and not `?`.
//  - `project` / `starts_at` / `ends_at` are POINTERS with omitempty (:19-21), so
//    the KEY IS ABSENT when NULL. Hence `?: string` and never `| null`.
//  - `worker_ids` is built with make() (:26): always an array, [] when empty, never
//    null.
//  - `user_id` is a bare user UUID with NO join to `users`, which is why no owner
//    column is rendered.
//  - Timestamps are Go time.Time, i.e. RFC3339 with nanosecond precision. Parse
//    with new Date(); never string-compare.
//
// The selector type is the shape this UI can produce and the shape the handler can
// accept (it decodes into map[string]string, :232). A row written directly through
// SQL could hold nested JSON; the table only ever renders `k=v` pairs of it, so an
// exotic value degrades to a stringified cell rather than crashing.
export interface Reservation {
  id: string
  name: string
  selector: Record<string, string> | null
  worker_ids: string[]
  user_id: string
  created_at: string
  project?: string
  starts_at?: string
  ends_at?: string
}

// internal/api/pagination.go:289-293.
export interface ReservationsPage {
  items: Reservation[]
  next_cursor: string
  total: number
}

// ReservationsSortSpec (internal/api/reservations.go:55-63): four keys, each with an
// optional '-' prefix, default '-created_at'. All EIGHT arms are implemented
// (:106-217) and all eight are indexed (migration 000013), so every one is a real
// server capability rather than a hopeful client string.
export type ReservationSortField = 'created_at' | 'name' | 'starts_at' | 'ends_at'
export type ReservationSort =
  | 'created_at'
  | '-created_at'
  | 'name'
  | '-name'
  | 'starts_at'
  | '-starts_at'
  | 'ends_at'
  | '-ends_at'

export interface ListReservationsParams {
  sort: ReservationSort
  cursor: string
}

export function listReservations({
  sort,
  cursor,
}: ListReservationsParams): Promise<ReservationsPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (cursor) q.set('cursor', cursor)
  return apiFetch<ReservationsPage>(`/reservations?${q}`)
}

// What this UI sends, and nothing else.
//  - `selector` is never sent: the scheduler never reads it, and the handler decodes
//    it into map[string]string so a nested object 400s as 'invalid request body'.
//  - `user_id` is never sent: the handler defaults it to the authenticated admin
//    (:255-263), it grants nothing, and a valid-but-nonexistent UUID returns 500
//    from the users FK (:294-297) rather than a 400.
//  - `project` / `starts_at` / `ends_at` are OMITTED when blank rather than sent as
//    "" or null, so the stored row says what the admin actually supplied.
//  - Dates are full RFC3339 with an offset. A datetime-local input yields a
//    zone-less string that Go's time.Time decoder rejects, so the form converts with
//    new Date(localValue).toISOString().
export interface CreateReservationBody {
  name: string
  worker_ids: string[]
  project?: string
  starts_at?: string
  ends_at?: string
}

// 201 echoes the full row - the same shape the list returns, and nothing secret.
export function createReservation(body: CreateReservationBody): Promise<Reservation> {
  return apiFetch<Reservation>('/reservations', { method: 'POST', json: body })
}

// 204 with no body (internal/api/reservations.go:322); apiFetch returns undefined for
// 204 (web/src/lib/api.ts:57). Hard delete - there is no soft-delete column.
export function deleteReservation(id: string): Promise<void> {
  return apiFetch<void>(`/reservations/${id}`, { method: 'DELETE' })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/reservations/api.test.ts`
Expected: PASS (9 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/reservations/api.ts web/src/admin/reservations/api.test.ts
git commit -m "feat(web): reservation API clients and types"
```

---

## Task 3: listWorkers gains an optional limit, and the picker query

This is the one task that touches a **shipped shared module**. The change is a single optional parameter with the existing default preserved, so no current call site and no asserted URL changes. `web/src/workers/useWorkers.ts:10` calls `listWorkers(sort)` and keeps `limit=50`; `web/src/workers/api.test.ts:17-29` asserts `limit=50` for `listWorkers('name')` and must **still pass untouched**. Do not "tidy" it.

**Files:**
- Modify: `web/src/workers/api.ts:47-52`
- Modify: `web/src/workers/api.test.ts` (append one test)
- Create: `web/src/admin/reservations/useWorkerOptions.ts`
- Test: `web/src/admin/reservations/useWorkerOptions.test.tsx`

- [ ] **Step 1: Write the failing tests**

Append to `web/src/workers/api.test.ts`:

```ts
test('an explicit limit overrides the default and changes nothing else', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/workers', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  // 200 is the server's maxLimit (internal/api/pagination.go:207). A value outside
  // [1, 200] is a 400 from parsePage (:244), not a clamp, so callers pass literals -
  // never a computed number.
  await listWorkers('name', 200)
  expect(captured?.get('limit')).toBe('200')
  expect(captured?.get('sort')).toBe('name')
  expect(captured?.get('cursor')).toBeNull()
})
```

Create `web/src/admin/reservations/useWorkerOptions.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useWorkerOptions, WORKER_PICKER_LIMIT } from './useWorkerOptions'
import type { WorkersPage } from '../../workers/api'

const W = { id: 'w1', name: 'render-01', status: 'online' }

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('requests the first 200 workers by name and caches under ["workers","reservation-picker"]', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/workers', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [W], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useWorkerOptions(), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))

  expect(WORKER_PICKER_LIMIT).toBe(200)
  expect(params?.get('limit')).toBe('200')
  expect(params?.get('sort')).toBe('name')
  // Under the BARE ['workers'] prefix, so the shipped worker mutations
  // (web/src/workers/useWorkerActions.ts:26, :50, :73, :82) invalidate it for free;
  // and 'reservation-picker' is not a WorkerSort, so it cannot collide with
  // useWorkers' ['workers', sort] key.
  const cached = client.getQueryData<WorkersPage>(['workers', 'reservation-picker'])
  expect(cached?.items[0].name).toBe('render-01')
})

test('does not poll - a form does not need the workers page 3s refresh', async () => {
  let calls = 0
  server.use(
    http.get('/v1/workers', () => {
      calls++
      return HttpResponse.json({ items: [W], next_cursor: '', total: 1 })
    }),
  )
  const { result } = renderHook(() => useWorkerOptions(), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // useWorkers polls at 3000ms; 150ms catches any small copy-pasted interval too.
  await new Promise((r) => setTimeout(r, 150))
  expect(calls).toBe(1)

  // Positive control on the SAME counter, so the equality above is about polling and
  // not about a dead instrument.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```
npx vitest run src/workers/api.test.ts src/admin/reservations/useWorkerOptions.test.tsx
```

Expected: FAIL - `Expected "200" but got "50"` on the workers test (the second argument is ignored today), and `Failed to resolve import "./useWorkerOptions"`.

- [ ] **Step 3: Implement**

Replace `web/src/workers/api.ts:47-52` with:

```ts
// First page only. `limit` defaults to 50 (the server default) so every existing
// caller keeps its exact URL - useWorkers (useWorkers.ts:10) and the workers page
// are unchanged by this parameter. The admin reservation worker picker passes 200,
// the server's maxLimit (internal/api/pagination.go:207); a value outside [1, 200]
// is a 400 from parsePage (:244), not a clamp, so pass literals, never a computed
// number. Cursor paging is deliberately still not exposed here - see
// web/src/admin/reservations/useWorkerOptions.ts for the ceiling this leaves.
export function listWorkers(sort: WorkerSort, limit = 50): Promise<WorkersPage> {
  const q = new URLSearchParams({ sort, limit: String(limit) })
  return apiFetch<WorkersPage>(`/workers?${q}`)
}
```

Create `web/src/admin/reservations/useWorkerOptions.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { listWorkers, type WorkersPage } from '../../workers/api'

// The server's maxLimit (internal/api/pagination.go:207). Asking for more is a 400,
// not a clamp, so this is the hard ceiling of a single-request picker.
export const WORKER_PICKER_LIMIT = 200

// Worker options for the reservation create form. Deliberately NOT useWorkers:
//  - useWorkers polls every 3s for the live workers page; a form does not need that,
//    and a list that reorders under the admin's cursor mid-selection is hostile.
//  - staleTime keeps the list stable while the panel is open and across reopens.
//
// The key sits under the BARE ['workers'] prefix, so the shipped worker mutations
// (useWorkerActions.ts:26, :50, :73, :82) invalidate it for free, and
// 'reservation-picker' is not a WorkerSort so it cannot collide with
// useWorkers' ['workers', sort].
//
// CEILING, stated rather than hidden: this is ONE page of at most 200 workers by
// name, with no cursor. WorkerPicker renders a visible note whenever
// total > items.length. A genuinely paginated or server-filtered picker is a bigger
// unit and belongs in its own backlog item if a fleet ever exceeds 200 workers - it
// must never silently offer a truncated list as if it were complete.
export function useWorkerOptions() {
  return useQuery<WorkersPage>({
    queryKey: ['workers', 'reservation-picker'],
    queryFn: () => listWorkers('name', WORKER_PICKER_LIMIT),
    staleTime: 30_000,
  })
}
```

- [ ] **Step 4: Run the tests to verify they pass, INCLUDING the untouched regression guard**

```
npx vitest run src/workers/ src/admin/reservations/useWorkerOptions.test.tsx
```

Expected: PASS for the whole `src/workers/` directory - the pre-existing `limit=50` test, `useWorkers`, `useWorkerActions`, `WorkersPage` and `WorkerDetailPage` tests all still green. If anything under `src/workers/` changed behaviour, the optional parameter was not additive; fix it before continuing.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/api.ts web/src/workers/api.test.ts web/src/admin/reservations/useWorkerOptions.ts web/src/admin/reservations/useWorkerOptions.test.tsx
git commit -m "feat(web): optional limit on listWorkers plus the reservation worker-options query"
```

---

## Task 4: useReservations list query

Mirror `web/src/admin/enrollments/useAgentEnrollments.ts:14-20` exactly.

**Files:**
- Create: `web/src/admin/reservations/useReservations.ts`
- Test: `web/src/admin/reservations/useReservations.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/reservations/useReservations.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useReservations } from './useReservations'
import type { ReservationsPage } from './api'

const ROW = {
  id: 'r1',
  name: 'gpu-farm-hold',
  selector: null,
  worker_ids: [],
  user_id: 'u1',
  created_at: '2026-08-09T09:30:00Z',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["reservations", sort, cursor] and passes both through', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/reservations', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useReservations('starts_at', 'cur1'), {
    wrapper: makeWrapper(client),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))

  expect(params?.get('sort')).toBe('starts_at')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('limit')).toBe('50')
  const cached = client.getQueryData<ReservationsPage>(['reservations', 'starts_at', 'cur1'])
  expect(cached?.items[0].id).toBe('r1')
})

test('does not poll - reservations are not live data', async () => {
  let calls = 0
  server.use(
    http.get('/v1/reservations', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useReservations('-created_at', ''), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  await new Promise((r) => setTimeout(r, 150))
  expect(calls).toBe(1)

  // Positive control on the SAME counter: the instrument can move, so the assertion
  // above is about polling and not about a dead counter.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/reservations/useReservations.test.tsx`
Expected: FAIL - `Failed to resolve import "./useReservations"`.

- [ ] **Step 3: Implement the hook**

Create `web/src/admin/reservations/useReservations.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listReservations, type ReservationsPage, type ReservationSort } from './api'

// Same shape as useAgentEnrollments (web/src/admin/enrollments/useAgentEnrollments.ts:14-20),
// including the deliberate absence of refetchInterval: reservations change only when
// an admin changes them, so polling them is pointless load. Freshness of the STATUS
// pill comes from useNow, a local 60s clock tick that issues no request; freshness of
// the ROW SET comes from useReservationActions invalidating the bare ['reservations']
// prefix.
//
// keepPreviousData keeps rows visible while a new sort/page loads, which is also what
// makes isPlaceholderData usable to disable the pager mid-fetch.
export function useReservations(sort: ReservationSort, cursor: string) {
  return useQuery<ReservationsPage>({
    queryKey: ['reservations', sort, cursor],
    queryFn: () => listReservations({ sort, cursor }),
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/reservations/useReservations.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/reservations/useReservations.ts web/src/admin/reservations/useReservations.test.tsx
git commit -m "feat(web): useReservations list query hook"
```

---

## Task 5: useReservationActions - create and remove

Mirror `web/src/admin/enrollments/useAgentEnrollmentActions.ts:25-38`, with two mutations and **no `gcTime` override** (nothing here is a credential). Step 5 is a **mandatory** non-vacuity check.

**Files:**
- Create: `web/src/admin/reservations/useReservationActions.ts`
- Test: `web/src/admin/reservations/useReservationActions.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/reservations/useReservationActions.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useReservationActions } from './useReservationActions'
import { useReservations } from './useReservations'

const ROW = {
  id: 'r1',
  name: 'gpu-farm-hold',
  selector: null,
  worker_ids: ['11111111-1111-1111-1111-111111111111'],
  user_id: 'u1',
  created_at: '2026-08-09T09:30:00Z',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function expectAllBarePrefix(spy: ReturnType<typeof vi.spyOn>) {
  // The decoupling lesson from web/src/jobs/queryKeyDecoupling.test.tsx: a
  // fully-qualified key only refetches the sort/page combination that happens to be
  // mounted. EVERY call must use the bare prefix.
  for (const call of spy.mock.calls) {
    expect((call[0] as { queryKey: unknown[] }).queryKey).toEqual(['reservations'])
  }
}

test('create POSTs the exact body and invalidates the BARE ["reservations"] prefix', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/reservations', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...ROW, id: 'r9' }, { status: 201 })
    }),
  )
  const { result } = renderHook(() => useReservationActions(), { wrapper: makeWrapper(client) })
  const created = await result.current.create.mutateAsync({
    name: 'gpu-farm-hold',
    worker_ids: ROW.worker_ids,
  })

  expect(body).toEqual({ name: 'gpu-farm-hold', worker_ids: ROW.worker_ids })
  expect(created.id).toBe('r9')
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['reservations'] }))
  expectAllBarePrefix(spy)
})

test('creating refetches a MOUNTED reservations list (active observer, not a cache seed)', async () => {
  let listCalls = 0
  server.use(
    http.get('/v1/reservations', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.post('/v1/reservations', () => HttpResponse.json({ ...ROW, id: 'r9' }, { status: 201 })),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)

  // The list query MUST be mounted via renderHook so it has an ACTIVE OBSERVER. A
  // client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter what key the mutation invalidated.
  const { result: list } = renderHook(() => useReservations('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useReservationActions(), { wrapper })
  await actions.current.create.mutateAsync({ name: 'x', worker_ids: ROW.worker_ids })

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})

test('remove DELETEs the id, resolves on the empty 204, and refetches the mounted list', async () => {
  let listCalls = 0
  let deletedPath: string | undefined
  server.use(
    http.get('/v1/reservations', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.delete('/v1/reservations/:id', ({ request }) => {
      deletedPath = new URL(request.url).pathname
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)
  const { result: list } = renderHook(() => useReservations('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useReservationActions(), { wrapper })
  await actions.current.remove.mutateAsync('r1')

  expect(deletedPath).toBe('/v1/reservations/r1')
  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})

test('a remove 404 rejects AND still refetches, so the stale row leaves the table', async () => {
  // The interesting failure: someone else deleted the row first. onSettled (not
  // onSuccess) is what makes the error informational rather than a dead end.
  let listCalls = 0
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.get('/v1/reservations', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.delete('/v1/reservations/:id', () =>
      HttpResponse.json({ error: 'reservation not found' }, { status: 404 }),
    ),
  )
  const wrapper = makeWrapper(client)
  const { result: list } = renderHook(() => useReservations('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useReservationActions(), { wrapper })
  await expect(actions.current.remove.mutateAsync('gone')).rejects.toMatchObject({ status: 404 })

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
  expectAllBarePrefix(spy)
})

test('a create failure surfaces the ApiError and does NOT invalidate (nothing was created)', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/reservations', () =>
      HttpResponse.json({ error: 'create reservation failed' }, { status: 500 }),
    ),
  )
  const { result } = renderHook(() => useReservationActions(), { wrapper: makeWrapper(client) })
  await expect(
    result.current.create.mutateAsync({ name: 'x', worker_ids: [] }),
  ).rejects.toMatchObject({ status: 500 })
  expect(spy).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/reservations/useReservationActions.test.tsx`
Expected: FAIL - `Failed to resolve import "./useReservationActions"`.

- [ ] **Step 3: Implement the hook**

Create `web/src/admin/reservations/useReservationActions.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createReservation, deleteReservation, type CreateReservationBody } from './api'

// Mutations for the admin Reservations tab.
//
// NOTHING HERE IS SECRET. reservationResponse carries no token, hash or credential
// and the 201 echoes the same row the list returns, so - unlike
// useAgentEnrollmentActions - there is deliberately NO gcTime: 0, no reveal dialog
// and no secrecy harness. Do not copy that machinery in.
//
// No optimistic insert or removal: the table's STATUS column is derived from the
// row's own timestamps, and a locally synthesised row is one more place for the
// client and the scheduler to disagree. A refetch is cheap and always right.
export function useReservationActions() {
  const qc = useQueryClient()
  // BARE prefix, never a fully-qualified key, so every mounted
  // ['reservations', sort, cursor] combination refetches (see
  // web/src/jobs/queryKeyDecoupling.test.tsx).
  const invalidate = () => qc.invalidateQueries({ queryKey: ['reservations'] })

  const create = useMutation({
    mutationFn: (body: CreateReservationBody) => createReservation(body),
    onSuccess: invalidate,
  })

  const remove = useMutation({
    mutationFn: (id: string) => deleteReservation(id),
    // onSettled, NOT onSuccess. The interesting failure is a 404 from a row someone
    // else already deleted; refetching on that path is what makes the error message
    // informational (the stale row disappears) instead of a dead end the admin can
    // only escape by reloading. Refetching after a 500 is harmless.
    onSettled: invalidate,
  })

  return { create, remove }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/reservations/useReservationActions.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 5: Prove the invalidation tests are not vacuous (mandatory)**

Temporarily break the key in `web/src/admin/reservations/useReservationActions.ts`:

```ts
  const invalidate = () => qc.invalidateQueries({ queryKey: ['reservations-broken'] })
```

Run: `npx vitest run src/admin/reservations/useReservationActions.test.tsx`
Expected: FAIL on **four** tests - the two bare-prefix assertions and, critically, both `listCalls` assertions (stuck at 1). **If either mounted-list test still passes, its observer is not active and the test is worthless - fix the test before continuing.**

Revert to `['reservations']` and re-run. Expected: PASS (5 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/admin/reservations/useReservationActions.ts web/src/admin/reservations/useReservationActions.test.tsx
git commit -m "feat(web): useReservationActions create and remove with bare-prefix invalidation"
```

---

## Task 6: WorkerPicker - the highest-risk new component

Everything else in this slice is a clone of a shipped file. This is not. Two failure modes to design out: **offering a truncated list as if it were complete**, and a submitted `worker_ids` array that depends on click order rather than selection.

**Files:**
- Create: `web/src/admin/reservations/WorkerPicker.tsx`
- Test: `web/src/admin/reservations/WorkerPicker.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/reservations/WorkerPicker.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { WorkerPicker } from './WorkerPicker'

const A = { id: 'aaaa1111-1111-1111-1111-111111111111', name: 'render-01', status: 'online' }
const B = { id: 'bbbb2222-2222-2222-2222-222222222222', name: 'render-02', status: 'offline' }
const C = { id: 'cccc3333-3333-3333-3333-333333333333', name: 'sim-01', status: 'online' }

function renderPicker(value: string[] = []) {
  const onChange = vi.fn()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={client}>
      <WorkerPicker value={value} onChange={onChange} />
    </QueryClientProvider>,
  )
  return { onChange, ...view }
}

test('requests limit=200 sorted by name and lists every loaded worker', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/workers', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })
    }),
  )
  renderPicker()
  expect(await screen.findByRole('checkbox', { name: /render-01/ })).toBeInTheDocument()
  expect(params?.get('limit')).toBe('200')
  expect(params?.get('sort')).toBe('name')
  expect(screen.getAllByRole('checkbox')).toHaveLength(3)
  // Offline workers are NOT hidden: a reservation is a pure exclusion from dispatch,
  // so reserving a currently-offline worker is legitimate. Revoked workers are
  // already excluded server-side.
  expect(screen.getByRole('checkbox', { name: /render-02/ })).toBeInTheDocument()
})

test('the filter narrows the rendered set WITHOUT issuing a request', async () => {
  let calls = 0
  server.use(
    http.get('/v1/workers', () => {
      calls++
      return HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })
    }),
  )
  renderPicker()
  await screen.findByRole('checkbox', { name: /render-01/ })
  // Positive control: the counter is live - it already moved from 0 to 1 on mount,
  // so the equality below is about the filter and not about a dead instrument.
  expect(calls).toBe(1)

  await userEvent.type(screen.getByLabelText('Filter workers'), 'sim')
  expect(await screen.findByRole('checkbox', { name: /sim-01/ })).toBeInTheDocument()
  expect(screen.getAllByRole('checkbox')).toHaveLength(1)
  expect(screen.queryByRole('checkbox', { name: /render-01/ })).not.toBeInTheDocument()
  expect(calls).toBe(1)
})

test('selecting and deselecting emits ids in loaded order, not click order', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  const { onChange } = renderPicker()
  await screen.findByRole('checkbox', { name: /render-01/ })

  // Click the THIRD worker first, then the first. The emitted array must still be in
  // the loaded (name-sorted) order, so the submitted worker_ids are a function of the
  // SELECTION and not of the click sequence.
  await userEvent.click(screen.getByRole('checkbox', { name: /sim-01/ }))
  expect(onChange).toHaveBeenLastCalledWith([C.id])
  onChange.mockClear()

  // The component is controlled, so drive the next state in through `value`.
  const { onChange: onChange2 } = renderPicker([C.id])
  await screen.findByRole('checkbox', { name: /render-01/ })
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  expect(onChange2).toHaveBeenLastCalledWith([A.id, C.id])

  const { onChange: onChange3 } = renderPicker([A.id, C.id])
  await screen.findByRole('checkbox', { name: /sim-01/ })
  await userEvent.click(screen.getByRole('checkbox', { name: /sim-01/ }))
  expect(onChange3).toHaveBeenLastCalledWith([A.id])
})

test('a selected worker stays checked and the count is shown', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  renderPicker([A.id, C.id])
  expect(await screen.findByRole('checkbox', { name: /render-01/ })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /render-02/ })).not.toBeChecked()
  expect(screen.getByText('2 selected')).toBeInTheDocument()
})

test('the 200-row ceiling is STATED when the fleet is larger', async () => {
  server.use(
    http.get('/v1/workers', () =>
      // 3 loaded, 512 exist: the picker must say so rather than pretend.
      HttpResponse.json({ items: [A, B, C], next_cursor: 'c2', total: 512 }),
    ),
  )
  renderPicker()
  await screen.findByRole('checkbox', { name: /render-01/ })
  expect(
    screen.getByText(
      'showing first 200 of 512 workers by name - use the CLI for workers beyond this page',
    ),
  ).toBeInTheDocument()
})

test('the ceiling note is ABSENT when the whole fleet is loaded (both directions asserted)', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  const { container } = renderPicker()
  await screen.findByRole('checkbox', { name: /render-01/ })
  // Matched on normalized container text, which is the representation the real
  // failure would take: an always-on note anywhere in the panel.
  expect(container.textContent?.replace(/\s+/g, ' ')).not.toMatch(/showing first 200/)
})

test('an empty fleet and a failed load both say so instead of rendering a silent empty list', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })))
  renderPicker()
  expect(await screen.findByText('No workers are registered.')).toBeInTheDocument()

  server.use(http.get('/v1/workers', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderPicker()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/reservations/WorkerPicker.test.tsx`
Expected: FAIL - `Failed to resolve import "./WorkerPicker"`.

- [ ] **Step 3: Implement the picker**

Create `web/src/admin/reservations/WorkerPicker.tsx`:

```tsx
import { useState } from 'react'
import { Input } from '../../components/Input'
import { useWorkerOptions, WORKER_PICKER_LIMIT } from './useWorkerOptions'

interface WorkerPickerProps {
  value: string[]
  onChange: (ids: string[]) => void
}

// Controlled multi-select over the loaded worker page. A free-text UUID field was the
// alternative and was rejected: worker UUIDs appear nowhere in the SPA's UI text, so
// an admin could not verify what they typed.
//
// The query lives here rather than in the form so it mounts only while the create
// panel is open - which is also why ReservationsTab tests need no /v1/workers handler
// until they open the panel.
export function WorkerPicker({ value, onChange }: WorkerPickerProps) {
  const [filter, setFilter] = useState('')
  const { data, error, isLoading } = useWorkerOptions()

  const workers = data?.items ?? []
  const total = data?.total ?? 0
  // Client-side filter over the ALREADY LOADED set only. It issues no request, and it
  // therefore cannot reach a worker outside the loaded page - which is exactly why the
  // ceiling below is stated rather than hidden.
  const q = filter.trim().toLowerCase()
  const shown = q
    ? workers.filter(
        (w) => w.name.toLowerCase().includes(q) || w.hostname?.toLowerCase().includes(q),
      )
    : workers

  const selected = new Set(value)

  function toggle(id: string) {
    const next = new Set(selected)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    // Emit in the LOADED (name-sorted) order, never click order, so the submitted
    // worker_ids array is a deterministic function of the selection. Selections can
    // only originate from `workers`, so nothing is dropped by this projection.
    onChange(workers.filter((w) => next.has(w.id)).map((w) => w.id))
  }

  return (
    <div className="mb-3">
      <span className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">
        Workers to reserve
      </span>

      {isLoading && <div className="text-[12px] text-fg-mute">Loading workers…</div>}
      {error && <div className="text-[12px] text-err">{(error as Error).message}</div>}

      {!isLoading && !error && workers.length === 0 && (
        <div className="text-[12px] text-fg-mute">No workers are registered.</div>
      )}

      {workers.length > 0 && (
        <>
          <Input
            aria-label="Filter workers"
            placeholder="filter by name or hostname"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="py-1 text-[12px]"
          />
          <div className="mt-1.5 max-h-48 overflow-y-auto rounded-[6px] border border-border bg-black/20 px-2.5 py-1.5">
            {shown.map((w) => (
              <label
                key={w.id}
                className="flex cursor-pointer items-center gap-2 py-0.5 text-[12px] text-fg-mute"
              >
                <input
                  type="checkbox"
                  className="accent-accent"
                  checked={selected.has(w.id)}
                  onChange={() => toggle(w.id)}
                />
                <span className="font-sans text-fg">{w.name}</span>
                <span className="font-mono text-[10.5px] text-fg-dim">{w.status}</span>
              </label>
            ))}
            {shown.length === 0 && (
              <div className="py-1 text-[11.5px] text-fg-dim">No workers match that filter.</div>
            )}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 font-mono text-[10.5px] text-fg-dim">
            <span>{value.length} selected</span>
            {/* The ceiling, STATED. One request at the server's maxLimit and no
                cursor, so a fleet larger than the loaded page is genuinely
                unreachable from this control - it must never look complete. */}
            {total > workers.length && (
              <span className="text-warn">
                showing first {WORKER_PICKER_LIMIT} of {total} workers by name - use the CLI for
                workers beyond this page
              </span>
            )}
          </div>
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/reservations/WorkerPicker.test.tsx`
Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/reservations/WorkerPicker.tsx web/src/admin/reservations/WorkerPicker.test.tsx
git commit -m "feat(web): WorkerPicker with a stated 200-row ceiling"
```

---

## Task 7: ReservationsTable

Mirror `web/src/admin/enrollments/EnrollmentsTable.tsx` structurally. Needs a `MemoryRouter` in tests because worker chips are `<Link>`s.

**Files:**
- Create: `web/src/admin/reservations/ReservationsTable.tsx`
- Test: `web/src/admin/reservations/ReservationsTable.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/reservations/ReservationsTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, test, vi } from 'vitest'
import { ReservationsTable } from './ReservationsTable'
import type { Reservation } from './api'

const NOW = new Date('2026-08-09T12:00:00Z')
const W1 = 'aaaa1111-1111-1111-1111-111111111111'
const W2 = 'bbbb2222-2222-2222-2222-222222222222'

function row(over: Partial<Reservation> = {}): Reservation {
  return {
    id: 'r1',
    name: 'gpu-farm-hold',
    selector: null,
    worker_ids: [W1],
    user_id: 'u1',
    created_at: '2026-08-09T09:30:00Z',
    ...over,
  }
}

function renderTable(reservations: Reservation[], over: Record<string, unknown> = {}) {
  const onSort = vi.fn()
  const onDelete = vi.fn()
  const view = render(
    <MemoryRouter>
      <ReservationsTable
        reservations={reservations}
        sort="-created_at"
        onSort={onSort}
        now={NOW}
        busy={false}
        onDelete={onDelete}
        {...over}
      />
    </MemoryRouter>,
  )
  return { onSort, onDelete, ...view }
}

test('renders the agreed columns and no owner column', () => {
  renderTable([row()])
  for (const h of ['NAME', 'PROJECT', 'WORKERS', 'STARTS', 'ENDS', 'STATUS', 'CREATED']) {
    expect(screen.getByText(new RegExp(`^${h}`))).toBeInTheDocument()
  }
  // user_id is a bare UUID with no join to `users`, so there is nothing honest to put
  // in an owner column (internal/api/reservations.go:18, :47).
  for (const h of ['OWNER', 'USER', 'RESERVED FOR', 'SELECTOR']) {
    expect(screen.queryByText(h)).not.toBeInTheDocument()
  }
})

test('absent project / starts_at / ends_at render a plain ASCII hyphen, never null or undefined', () => {
  const { container } = renderTable([row()])
  const text = container.textContent ?? ''
  expect(text).not.toMatch(/null/)
  expect(text).not.toMatch(/undefined/)
  expect(text).not.toMatch(/—|–/) // em dash and en dash are banned house-wide
  // Exactly three absent cells on this row.
  expect(screen.getAllByText('-')).toHaveLength(3)
})

test('positive control: present project and window render their values', () => {
  renderTable([
    row({ project: 'atlas', starts_at: '2026-08-09T10:00:00Z', ends_at: '2026-08-11T10:00:00Z' }),
  ])
  expect(screen.getByText('atlas')).toBeInTheDocument()
  // Local rendering, so build the expectation the same way formatDateTime does
  // rather than hardcoding a UTC string.
  const d = new Date('2026-08-09T10:00:00Z')
  const p = (n: number) => String(n).padStart(2, '0')
  const expected = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
  expect(screen.getByText(expected)).toBeInTheDocument()
  expect(screen.queryByText('-')).not.toBeInTheDocument()
})

test('the three derived statuses render with the right rows dimmed', () => {
  renderTable([
    row({ id: 'a', name: 'active-now' }),
    row({ id: 's', name: 'later', starts_at: '2026-08-20T00:00:00Z' }),
    row({ id: 'e', name: 'over', ends_at: '2026-08-01T00:00:00Z' }),
  ])
  expect(screen.getByText('ACTIVE')).toBeInTheDocument()
  expect(screen.getByText('SCHEDULED')).toBeInTheDocument()
  expect(screen.getByText('ENDED')).toBeInTheDocument()
  const rows = screen.getAllByRole('row')
  // rows[0] is the header. Only the ENDED row is dimmed.
  expect(rows[3].className).toContain('opacity-[0.55]')
  expect(rows[1].className).not.toContain('opacity-[0.55]')
})

test('worker ids render as truncated chips linking to the worker detail page', () => {
  renderTable([row({ worker_ids: [W1, W2] })])
  const links = screen.getAllByRole('link')
  expect(links).toHaveLength(2)
  expect(links[0]).toHaveAttribute('href', `/workers/${W1}`)
  expect(links[0]).toHaveTextContent(W1.slice(0, 8))
  // Full UUID recoverable without a backend enricher.
  expect(links[0]).toHaveAttribute('title', W1)
})

test('worker_ids: [] renders no chips, says none, and does not crash', () => {
  renderTable([row({ worker_ids: [] })])
  expect(screen.queryAllByRole('link')).toHaveLength(0)
  // 'none' rather than a hyphen: an empty reservation reserves nothing, and the word
  // also keeps the absent-key hyphen count unambiguous.
  expect(screen.getByText('none')).toBeInTheDocument()
})

test('a non-empty selector renders a sel chip; null and {} render none', () => {
  renderTable([row({ id: 'p', name: 'with-sel', selector: { tier: 'gpu', site: 'west' } })])
  const chip = screen.getByText('sel')
  expect(chip).toBeInTheDocument()
  expect(chip).toHaveAttribute('title', 'tier=gpu site=west')

  renderTable([row({ id: 'n', selector: null })])
  renderTable([row({ id: 'z', selector: {} })])
  // One chip total across the three renders: only the first row has a selector.
  expect(screen.getAllByText('sel')).toHaveLength(1)
})

test('the four sortable headers call onSort with the server field names', async () => {
  const { onSort } = renderTable([row()])
  for (const [label, field] of [
    ['NAME', 'name'],
    ['STARTS', 'starts_at'],
    ['ENDS', 'ends_at'],
    ['CREATED', 'created_at'],
  ] as const) {
    await userEvent.click(screen.getByRole('button', { name: new RegExp(`^${label}`) }))
    expect(onSort).toHaveBeenLastCalledWith(field)
  }
  // PROJECT, WORKERS and STATUS are NOT sortable: the server has no sort arm for
  // them, and a header that silently does nothing is a dead affordance.
  expect(screen.queryByRole('button', { name: /^PROJECT/ })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /^STATUS/ })).not.toBeInTheDocument()
})

test('aria-sort and the caret follow the active sort', () => {
  renderTable([row()], { sort: 'name' })
  const headers = screen.getAllByRole('columnheader')
  const nameHeader = headers.find((h) => h.textContent?.startsWith('NAME'))
  expect(nameHeader).toHaveAttribute('aria-sort', 'ascending')
  expect(nameHeader?.textContent).toContain('▲')
  const createdHeader = headers.find((h) => h.textContent?.startsWith('CREATED'))
  expect(createdHeader).toHaveAttribute('aria-sort', 'none')
})

test('every Delete button names its own row, and clicking one passes that row', async () => {
  // A 50-row page of buttons all named "Delete" was a real finding on the Users tab:
  // it makes both the a11y tree and the tests unable to tell rows apart.
  const { onDelete } = renderTable([
    row({ id: 'r1', name: 'gpu-farm-hold' }),
    row({ id: 'r2', name: 'sim-drain' }),
  ])
  const first = screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' })
  const second = screen.getByRole('button', { name: 'Delete reservation sim-drain' })
  expect(first).not.toBe(second)

  await userEvent.click(second)
  expect(onDelete).toHaveBeenCalledTimes(1)
  expect(onDelete.mock.calls[0][0]).toMatchObject({ id: 'r2', name: 'sim-drain' })
})

test('busy disables every Delete button', () => {
  renderTable([row()], { busy: true })
  expect(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' })).toBeDisabled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/reservations/ReservationsTable.test.tsx`
Expected: FAIL - `Failed to resolve import "./ReservationsTable"`.

- [ ] **Step 3: Implement the table**

Create `web/src/admin/reservations/ReservationsTable.tsx`:

```tsx
import { Link } from 'react-router-dom'
import { Chip } from '../../components/holo'
import { formatDateTime } from '../../lib/time'
import { deriveStatus, statusTone } from './reservationStatus'
import type { Reservation, ReservationSort, ReservationSortField } from './api'

// NAME | PROJECT | WORKERS | STARTS | ENDS | STATUS | CREATED | ACT.
//
// Against the hi-fi (hifi3-holo-pages.jsx:2205-2278):
//  - The dedicated SELECTOR column is dropped to pay for STATUS and CREATED. A
//    selector, when present, is a `sel` chip beside the name. Every row THIS UI can
//    create has no selector, so a column for it would be permanently empty.
//  - CREATED is added because it is the default sort key and needs a clickable header.
//  - No owner column: user_id is a bare UUID with no join to `users`
//    (internal/api/reservations.go:18, :47).
// The header is WORKERS, not "RESERVED FOR": the listed workers are EXCLUDED from
// dispatch for everyone, so any possessive header would be a claim the scheduler does
// not implement (internal/scheduler/dispatch.go:185-223).
const COLS = 'grid grid-cols-[1.3fr_110px_1.5fr_130px_130px_110px_110px_100px]'

const MINI = 'rounded-full border px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em] disabled:opacity-40'
const MINI_DANGER = `${MINI} border-err/40 bg-err/10 text-err`

function caret(field: ReservationSortField, sort: ReservationSort): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

function ariaSort(
  field: ReservationSortField,
  sort: ReservationSort,
): 'ascending' | 'descending' | 'none' {
  if (sort.replace('-', '') !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
}

// Absent KEY (not null) for project/starts_at/ends_at: plain ASCII hyphen, never an
// em dash.
const DASH = <span className="text-fg-dim">-</span>

interface ReservationsTableProps {
  reservations: Reservation[]
  sort: ReservationSort
  onSort: (field: ReservationSortField) => void
  // Injected so the status pill is a pure function of props. The tab supplies
  // useNow(60_000); tests supply a fixed Date.
  now: Date
  busy: boolean
  onDelete: (reservation: Reservation) => void
}

function SortHeader({
  label,
  field,
  sort,
  onSort,
}: {
  label: string
  field: ReservationSortField
  sort: ReservationSort
  onSort: (f: ReservationSortField) => void
}) {
  return (
    <div role="columnheader" aria-sort={ariaSort(field, sort)}>
      <button type="button" className="text-left" onClick={() => onSort(field)}>
        {label}
        {caret(field, sort)}
      </button>
    </div>
  )
}

export function ReservationsTable({
  reservations,
  sort,
  onSort,
  now,
  busy,
  onDelete,
}: ReservationsTableProps) {
  return (
    <div
      role="table"
      aria-label="Reservations"
      className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]"
    >
      <div
        role="row"
        className={`${COLS} border-b border-border px-[18px] py-3 font-mono text-[10px] tracking-[0.16em] text-fg-mute`}
      >
        <SortHeader label="NAME" field="name" sort={sort} onSort={onSort} />
        <span role="columnheader">PROJECT</span>
        <span role="columnheader">WORKERS</span>
        <SortHeader label="STARTS" field="starts_at" sort={sort} onSort={onSort} />
        <SortHeader label="ENDS" field="ends_at" sort={sort} onSort={onSort} />
        <span role="columnheader">STATUS</span>
        <SortHeader label="CREATED" field="created_at" sort={sort} onSort={onSort} />
        <span role="columnheader" className="text-right">
          ACT.
        </span>
      </div>

      {reservations.map((r) => {
        const status = deriveStatus(r, now)
        // `selector` can be null (a create with no selector marshals a nil map to the
        // literal `null`) or {} (column default) or pairs - all three must render
        // without null/undefined reaching the DOM.
        const pairs = r.selector ? Object.entries(r.selector) : []
        return (
          <div
            key={r.id}
            role="row"
            className={`${COLS} items-center border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
              status === 'ENDED' ? 'opacity-[0.55]' : ''
            }`}
          >
            <span role="cell" className="flex min-w-0 items-center gap-2">
              <span className="truncate font-sans text-[12.5px] text-fg">{r.name}</span>
              {pairs.length > 0 && (
                <span title={pairs.map(([k, v]) => `${k}=${v}`).join(' ')}>
                  <Chip tone="muted">sel</Chip>
                </span>
              )}
            </span>

            <span role="cell" className="truncate font-sans text-[12px] text-fg-mute">
              {r.project ?? DASH}
            </span>

            <span role="cell" className="flex flex-wrap gap-1">
              {r.worker_ids.length === 0 ? (
                <span className="text-[11px] text-fg-dim">none</span>
              ) : (
                // No FK on worker_ids, so a link can 404 on a deleted or revoked
                // worker. That is the existing detail page's error state, and an
                // unresolvable id is itself useful information. Wrapping in a Link
                // rather than giving Chip an href keeps the shared primitive untouched.
                r.worker_ids.map((id) => (
                  <Link key={id} to={`/workers/${id}`} title={id}>
                    <Chip tone="muted">{id.slice(0, 8)}</Chip>
                  </Link>
                ))
              )}
            </span>

            <span role="cell" className="text-[10.5px] text-fg-mute">
              {r.starts_at ? formatDateTime(r.starts_at) : DASH}
            </span>
            <span role="cell" className="text-[10.5px] text-fg-mute">
              {r.ends_at ? formatDateTime(r.ends_at) : DASH}
            </span>

            <span role="cell">
              <Chip tone={statusTone(status)}>{status}</Chip>
            </span>

            <span role="cell" className="text-[10.5px] text-fg-mute">
              {r.created_at.slice(0, 10)}
            </span>

            <span role="cell" className="flex justify-end">
              {/* Row identity in the accessible name: a page of 50 buttons all named
                  "Delete" is indistinguishable to a screen reader and to a test. */}
              <button
                type="button"
                className={MINI_DANGER}
                disabled={busy}
                aria-label={`Delete reservation ${r.name}`}
                onClick={() => onDelete(r)}
              >
                Delete
              </button>
            </span>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/reservations/ReservationsTable.test.tsx`
Expected: PASS (11 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/reservations/ReservationsTable.tsx web/src/admin/reservations/ReservationsTable.test.tsx
git commit -m "feat(web): ReservationsTable with derived status and linked worker chips"
```

---

## Task 8: CreateReservationForm

Mirror `web/src/admin/enrollments/CreateEnrollmentForm.tsx:26-98` for shape. New here: three client-side validations and the `datetime-local` -> RFC3339 conversion.

**Files:**
- Create: `web/src/admin/reservations/CreateReservationForm.tsx`
- Test: `web/src/admin/reservations/CreateReservationForm.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/reservations/CreateReservationForm.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { CreateReservationForm } from './CreateReservationForm'

const A = { id: 'aaaa1111-1111-1111-1111-111111111111', name: 'render-01', status: 'online' }
const B = { id: 'bbbb2222-2222-2222-2222-222222222222', name: 'render-02', status: 'online' }

function renderForm(over: Record<string, unknown> = {}) {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B], next_cursor: '', total: 2 })),
  )
  const onSubmit = vi.fn()
  const onCancel = vi.fn()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={client}>
      <CreateReservationForm
        pending={false}
        error={null}
        onSubmit={onSubmit}
        onCancel={onCancel}
        {...over}
      />
    </QueryClientProvider>,
  )
  return { onSubmit, onCancel, ...view }
}

const submitButton = () => screen.getByRole('button', { name: 'Reserve' })

test('an empty name blocks submit, and filling it unblocks (paired positive)', async () => {
  renderForm()
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  expect(screen.getByText('Name is required.')).toBeInTheDocument()
  expect(submitButton()).toBeDisabled()

  await userEvent.type(screen.getByLabelText('Name'), 'gpu-farm-hold')
  expect(screen.queryByText('Name is required.')).not.toBeInTheDocument()
  expect(submitButton()).toBeEnabled()
})

test('whitespace alone is not a name', async () => {
  renderForm()
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  await userEvent.type(screen.getByLabelText('Name'), '   ')
  expect(screen.getByText('Name is required.')).toBeInTheDocument()
  expect(submitButton()).toBeDisabled()
})

test('zero workers blocks submit, and selecting one unblocks (paired positive)', async () => {
  renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), 'gpu-farm-hold')
  // The server accepts worker_ids: [] and stores a reservation that reserves nothing,
  // because reservedIDs is built only from that array
  // (internal/scheduler/dispatch.go:186-191). Submitting one is always a mistake.
  expect(screen.getByText('Select at least one worker.')).toBeInTheDocument()
  expect(submitButton()).toBeDisabled()

  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  expect(screen.queryByText('Select at least one worker.')).not.toBeInTheDocument()
  expect(submitButton()).toBeEnabled()
})

test('an inverted or zero-length window blocks submit, and fixing it unblocks', async () => {
  renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), 'gpu-farm-hold')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))

  // datetime-local: assign through fireEvent.change. jsdom implements no segmented
  // editing UI, so userEvent.type on this input type is unreliable.
  const starts = screen.getByLabelText('Starts') as HTMLInputElement
  const ends = screen.getByLabelText('Ends') as HTMLInputElement
  fireEvent.change(starts, { target: { value: '2026-08-20T09:00' } })
  fireEvent.change(ends, { target: { value: '2026-08-10T09:00' } })
  // Such a row can never satisfy ListActiveReservations, and the server persists it
  // happily (internal/store/query/reservations.sql:21-22).
  expect(screen.getByText('Ends must be after starts.')).toBeInTheDocument()
  expect(submitButton()).toBeDisabled()

  // Equal bounds are also empty, not merely inverted.
  fireEvent.change(ends, { target: { value: '2026-08-20T09:00' } })
  expect(screen.getByText('Ends must be after starts.')).toBeInTheDocument()

  fireEvent.change(ends, { target: { value: '2026-08-21T09:00' } })
  expect(screen.queryByText('Ends must be after starts.')).not.toBeInTheDocument()
  expect(submitButton()).toBeEnabled()
})

test('a window entirely in the past is allowed (a legitimate historical record)', async () => {
  const { onSubmit } = renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), 'old-hold')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  fireEvent.change(screen.getByLabelText('Starts'), { target: { value: '2020-01-01T09:00' } })
  fireEvent.change(screen.getByLabelText('Ends'), { target: { value: '2020-01-02T09:00' } })
  expect(submitButton()).toBeEnabled()
  await userEvent.click(submitButton())
  expect(onSubmit).toHaveBeenCalledTimes(1)
})

test('submits the minimal body and omits every blank optional', async () => {
  const { onSubmit } = renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), '  gpu-farm-hold  ')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-02/ }))
  await userEvent.click(submitButton())

  expect(onSubmit).toHaveBeenCalledTimes(1)
  const body = onSubmit.mock.calls[0][0]
  expect(body).toEqual({ name: 'gpu-farm-hold', worker_ids: [B.id] })
  for (const key of ['project', 'starts_at', 'ends_at', 'selector', 'user_id']) {
    expect(key in body).toBe(false)
  }
})

test('dates are sent as RFC3339 with an offset, not the raw datetime-local value', async () => {
  const { onSubmit } = renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), 'gpu-farm-hold')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  fireEvent.change(screen.getByLabelText('Starts'), { target: { value: '2026-08-10T09:00' } })
  await userEvent.type(screen.getByLabelText('Project'), 'atlas')
  await userEvent.click(submitButton())

  const body = onSubmit.mock.calls[0][0]
  // A datetime-local value is zone-less and Go's time.Time decoder rejects it. The
  // expectation is computed the same way the component computes it, from the same
  // local string, so this is TZ-independent - a hardcoded 'Z' literal would only pass
  // in a UTC runner.
  expect(body.starts_at).toBe(new Date('2026-08-10T09:00').toISOString())
  expect(body.starts_at).toMatch(/Z$/)
  expect(body.starts_at).not.toBe('2026-08-10T09:00')
  expect(body.project).toBe('atlas')
  expect('ends_at' in body).toBe(false)
})

test('the panel states the exclusion effect and claims no affinity', () => {
  const { container } = renderForm()
  const text = (container.textContent ?? '').replace(/\s+/g, ' ')
  expect(text).toMatch(/removes these workers from the dispatch pool for every job/i)
  // A reservation does not route the owner's work anywhere
  // (internal/scheduler/dispatch.go:221-223).
  for (const claim of [/reserved for/i, /dedicated/i, /priority/i, /exclusive/i, /assigned to/i]) {
    expect(text).not.toMatch(claim)
  }
})

test('a create error renders inline and Cancel is wired', async () => {
  const { onCancel } = renderForm({ error: new Error('500 create reservation failed') })
  expect(screen.getByText('500 create reservation failed')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(onCancel).toHaveBeenCalledTimes(1)
})

test('pending disables submit even when the form is valid', async () => {
  renderForm({ pending: true })
  await userEvent.type(await screen.findByLabelText('Name'), 'gpu-farm-hold')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  expect(submitButton()).toBeDisabled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/reservations/CreateReservationForm.test.tsx`
Expected: FAIL - `Failed to resolve import "./CreateReservationForm"`.

- [ ] **Step 3: Implement the form**

Create `web/src/admin/reservations/CreateReservationForm.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { GlassPanel, PillButton } from '../../components/holo'
import { Input } from '../../components/Input'
import { WorkerPicker } from './WorkerPicker'
import type { CreateReservationBody } from './api'

interface CreateReservationFormProps {
  pending: boolean
  error: Error | null
  onSubmit: (body: CreateReservationBody) => void
  onCancel: () => void
}

// Inline create panel, mirroring CreateEnrollmentForm rather than a modal. The hi-fi
// has no create form for reservations at all (its `+ Reserve workers` button is
// inert), so this surface is designed in the spec, not copied.
//
// Client validation is STRICTER than the server, deliberately. The handler validates
// only name != "" and UUID syntax (internal/api/reservations.go:243-274), so it will
// happily persist rows that can never do anything:
//  - worker_ids: [] reserves nothing, because reservedIDs is built only from that
//    array (internal/scheduler/dispatch.go:186-191).
//  - ends_at <= starts_at can never satisfy ListActiveReservations
//    (internal/store/query/reservations.sql:21-22).
// NOT blocked, on purpose: a window entirely in the past (a legitimate historical
// record) and duplicate names (the table has no unique constraint, and inventing one
// client-side would be a lie about the data model).
//
// selector and user_id are absent by design - see api.ts.
export function CreateReservationForm({
  pending,
  error,
  onSubmit,
  onCancel,
}: CreateReservationFormProps) {
  const [name, setName] = useState('')
  const [project, setProject] = useState('')
  const [workerIds, setWorkerIds] = useState<string[]>([])
  const [startsAt, setStartsAt] = useState('')
  const [endsAt, setEndsAt] = useState('')

  const trimmedName = name.trim()
  const nameMissing = trimmedName === ''
  const noWorkers = workerIds.length === 0
  // Zero-length counts as inverted: starts == ends is an empty window.
  const windowInverted =
    startsAt !== '' &&
    endsAt !== '' &&
    new Date(endsAt).getTime() <= new Date(startsAt).getTime()
  const valid = !nameMissing && !noWorkers && !windowInverted

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!valid) return
    const body: CreateReservationBody = { name: trimmedName, worker_ids: workerIds }
    const p = project.trim()
    if (p) body.project = p
    // datetime-local yields a zone-less local string ('2026-08-10T09:00'), which Go's
    // time.Time decoder rejects. new Date(...) on a date-TIME form is interpreted as
    // LOCAL per the ES spec, so toISOString() produces the correct instant for what
    // the admin typed.
    if (startsAt) body.starts_at = new Date(startsAt).toISOString()
    if (endsAt) body.ends_at = new Date(endsAt).toISOString()
    onSubmit(body)
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field label="Name" htmlFor="new-reservation-name" hint="Required. Not unique server-side.">
        <Input
          id="new-reservation-name"
          placeholder="gpu-farm-hold"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>

      <WorkerPicker value={workerIds} onChange={setWorkerIds} />

      <Field
        label="Project"
        htmlFor="new-reservation-project"
        hint="Optional label. Recorded, but never read by the scheduler. Omitted when blank."
      >
        <Input
          id="new-reservation-project"
          placeholder="atlas"
          value={project}
          onChange={(e) => setProject(e.target.value)}
        />
      </Field>

      <div className="mb-3 flex gap-3">
        <div className="flex-1">
          <Field
            label="Starts"
            htmlFor="new-reservation-starts"
            hint="Optional. Open start = in force immediately."
          >
            <Input
              id="new-reservation-starts"
              type="datetime-local"
              value={startsAt}
              onChange={(e) => setStartsAt(e.target.value)}
            />
          </Field>
        </div>
        <div className="flex-1">
          <Field
            label="Ends"
            htmlFor="new-reservation-ends"
            hint="Optional. Open end = in force until deleted."
          >
            <Input
              id="new-reservation-ends"
              type="datetime-local"
              value={endsAt}
              onChange={(e) => setEndsAt(e.target.value)}
            />
          </Field>
        </div>
      </div>

      {/* The honest statement of effect, on the surface where the mistake is made.
          The hi-fi's framing implies reserved workers run the owner's work; they are
          excluded from dispatch entirely (internal/scheduler/dispatch.go:221-223). */}
      <div className="mb-3 rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[10.5px] leading-relaxed tracking-[0.04em] text-warn">
        ⚠ Reserving removes these workers from the dispatch pool for every job, including your
        own. They stop receiving new tasks while the reservation is in force, and relay does not
        send your work to them instead.
      </div>

      {!valid && (
        <ul className="mb-3 list-inside list-disc text-[11px] text-err">
          {nameMissing && <li>Name is required.</li>}
          {noWorkers && <li>Select at least one worker.</li>}
          {windowInverted && <li>Ends must be after starts.</li>}
        </ul>
      )}

      {error && <div className="mb-3 text-[11px] text-err">{error.message}</div>}

      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending || !valid}>
          Reserve
        </PillButton>
      </div>
    </GlassPanel>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/reservations/CreateReservationForm.test.tsx`
Expected: PASS (10 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/reservations/CreateReservationForm.tsx web/src/admin/reservations/CreateReservationForm.test.tsx
git commit -m "feat(web): CreateReservationForm with client validation stricter than the server"
```

---

## Task 9: ReservationsTab composition, honest copy, and delete behind ConfirmDialog

Near-verbatim from `web/src/admin/enrollments/EnrollmentsTab.tsx:23-222` with the nouns changed, plus the `confirm` / `actionError` pattern from `web/src/admin/users/UsersTab.tsx`. The two genuinely new things - **the honesty assertions** and **the destructive-action gating** - are tests here, not notes.

**Files:**
- Create: `web/src/admin/reservations/ReservationsTab.tsx`
- Test: `web/src/admin/reservations/ReservationsTab.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/reservations/ReservationsTab.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ReservationsTab } from './ReservationsTab'
import type { Reservation } from './api'

const W1 = 'aaaa1111-1111-1111-1111-111111111111'
const WORKER = { id: W1, name: 'render-01', status: 'online' }

function row(over: Partial<Reservation> = {}): Reservation {
  return {
    id: 'r1',
    name: 'gpu-farm-hold',
    selector: null,
    worker_ids: [W1],
    user_id: 'u1',
    created_at: '2026-08-09T09:30:00Z',
    ...over,
  }
}

// ReservationsTab does not use useAuth, so no AuthProvider and no /v1/users/me
// handler. It DOES render react-router Links, so MemoryRouter is required.
function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <ReservationsTab />
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  }
}

function listHandler(
  seen: URLSearchParams[],
  envelope: (p: URLSearchParams) => { items: Reservation[]; next_cursor: string; total: number },
) {
  return http.get('/v1/reservations', ({ request }) => {
    const params = new URL(request.url).searchParams
    seen.push(params)
    return HttpResponse.json(envelope(params))
  })
}

const workersHandler = http.get('/v1/workers', () =>
  HttpResponse.json({ items: [WORKER], next_cursor: '', total: 1 }),
)

afterEach(() => vi.useRealTimers())

test('renders rows, the endpoint caption, the default sort, and the footer', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  expect(await screen.findByText('gpu-farm-hold')).toBeInTheDocument()
  expect(screen.getByText('GET /v1/reservations')).toBeInTheDocument()
  expect(seen[0].get('sort')).toBe('-created_at')
  expect(seen[0].get('limit')).toBe('50')
  expect(screen.getByText('1-1 of 1')).toBeInTheDocument()
})

test('the tab never claims worker affinity the scheduler does not implement', async () => {
  // THE central honesty requirement. A reservation unions worker_ids into reservedIDs
  // and the dispatcher SKIPS those workers for every task
  // (internal/scheduler/dispatch.go:185-191, :221-223). Nothing routes the owner's
  // work to them, so none of the hi-fi's "reserve for X" framing may survive.
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  await screen.findByText('gpu-farm-hold')
  // Normalized container text, not getByText: the copy spans element boundaries and
  // whitespace, and "anywhere in the tab" is the representation a real regression
  // would take.
  const text = (container.textContent ?? '').replace(/\s+/g, ' ')

  for (const claim of [
    /reserved for/i,
    /dedicated/i,
    /priority/i,
    /exclusive/i,
    /assigned to/i,
    /only .{0,24} can use/i,
  ]) {
    expect(text).not.toMatch(claim)
  }

  // Paired positive controls on the SAME instrument, so the absences above are about
  // the copy and not about a matcher that can never match.
  expect(text).toMatch(/removes its worker_ids from the general dispatch pool/i)
  expect(text).toMatch(/including the owner's own jobs/i)
  expect(text).toMatch(/never read by the scheduler/i)
  expect(text).toMatch(/next dispatch cycle/i)
  expect(text).toMatch(/never preempt/i)
  // And the column header is the neutral one.
  expect(screen.getByText('WORKERS')).toBeInTheDocument()
})

test('shows the loading skeleton, then rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  expect(container.querySelectorAll('.h-9').length).toBeGreaterThan(0)
  expect(await screen.findByText('gpu-farm-hold')).toBeInTheDocument()
})

test('shows an error card whose Retry refetches, and an empty card', async () => {
  let calls = 0
  server.use(
    http.get('/v1/reservations', () => {
      calls++
      if (calls === 1) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderTab()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No reservations.')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /prev/ })).not.toBeInTheDocument()
})

test('header clicks issue all EIGHT exact sort values and reset the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: 'c2', total: 9 })))
  renderTab()
  await screen.findByText('gpu-farm-hold')

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('c2'))

  for (const [label, first, second] of [
    ['NAME', 'name', '-name'],
    ['STARTS', 'starts_at', '-starts_at'],
    ['ENDS', 'ends_at', '-ends_at'],
    ['CREATED', 'created_at', '-created_at'],
  ] as const) {
    await userEvent.click(screen.getByRole('button', { name: new RegExp(`^${label}`) }))
    await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe(first))
    // A cursor issued under one sort is rejected by the server
    // (internal/api/pagination.go:272-286), so paging must reset.
    expect(seen.at(-1)?.has('cursor')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: new RegExp(`^${label}`) }))
    await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe(second))
  }
})

test('the pager walks the cursor stack and an empty page 2 still offers a way back', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) =>
      p.get('cursor')
        ? { items: [], next_cursor: '', total: 1 }
        : { items: [row()], next_cursor: 'c2', total: 1 },
    ),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  expect(screen.getByRole('button', { name: /prev/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('No reservations.')).toBeInTheDocument()
  const prevButton = screen.getByRole('button', { name: /prev/ })
  expect(prevButton).toBeEnabled()
  await userEvent.click(prevButton)
  expect(await screen.findByText('gpu-farm-hold')).toBeInTheDocument()
})

test('creating posts the exact body, closes the panel, and refreshes the list', async () => {
  const seen: URLSearchParams[] = []
  let body: unknown
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    workersHandler,
    http.post('/v1/reservations', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...row(), id: 'r9' }, { status: 201 })
    }),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  const before = seen.length

  await userEvent.click(screen.getByRole('button', { name: '+ Reserve workers' }))
  await userEvent.type(await screen.findByLabelText('Name'), 'sim-drain')
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  await userEvent.click(screen.getByRole('button', { name: 'Reserve' }))

  await waitFor(() => expect(body).toEqual({ name: 'sim-drain', worker_ids: [W1] }))
  // The panel closes and the bare-prefix invalidation refetched the mounted list.
  await waitFor(() => expect(screen.queryByLabelText('Name')).not.toBeInTheDocument())
  await waitFor(() => expect(seen.length).toBeGreaterThan(before))
  // Nothing is revealed: this is not a credential-bearing create.
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('a create error renders in the panel, and reopening clears it', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    workersHandler,
    http.post('/v1/reservations', () =>
      HttpResponse.json({ error: 'create reservation failed' }, { status: 500 }),
    ),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  await userEvent.click(screen.getByRole('button', { name: '+ Reserve workers' }))
  await userEvent.type(await screen.findByLabelText('Name'), 'sim-drain')
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  await userEvent.click(screen.getByRole('button', { name: 'Reserve' }))

  expect(await screen.findByText('500 create reservation failed')).toBeInTheDocument()
  expect(screen.getByText('gpu-farm-hold')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: '+ Reserve workers' }))
  expect(screen.queryByText('500 create reservation failed')).not.toBeInTheDocument()
})

test('Delete is gated: Cancel sends NO request, Confirm sends exactly one', async () => {
  const seen: URLSearchParams[] = []
  let deletes = 0
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.delete('/v1/reservations/:id', () => {
      deletes++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')

  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  const dialog = await screen.findByRole('dialog')
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toHaveAccessibleName('Delete reservation "gpu-farm-hold"?')
  // The dialog body states the real effect and its latency.
  const dialogText = (dialog.textContent ?? '').replace(/\s+/g, ' ')
  expect(dialogText).toMatch(/returns its 1 worker/i)
  expect(dialogText).toMatch(/general dispatch pool/i)
  expect(dialogText).toMatch(/next dispatch cycle/i)
  expect(dialogText).toMatch(/already running .* are unaffected/i)

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(deletes).toBe(0)

  // Paired positive control: the same flow CAN issue a request, so the zero above is
  // about the gate and not about an inert button.
  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  await waitFor(() => expect(deletes).toBe(1))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('deleting the SECOND row deletes that row, not the first', async () => {
  const seen: URLSearchParams[] = []
  const deleted: string[] = []
  server.use(
    listHandler(seen, () => ({
      items: [row({ id: 'r1', name: 'gpu-farm-hold' }), row({ id: 'r2', name: 'sim-drain' })],
      next_cursor: '',
      total: 2,
    })),
    http.delete('/v1/reservations/:id', ({ params }) => {
      deleted.push(String(params.id))
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderTab()
  await screen.findByText('sim-drain')

  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation sim-drain' }))
  expect(await screen.findByRole('dialog')).toHaveAccessibleName('Delete reservation "sim-drain"?')
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  await waitFor(() => expect(deleted).toEqual(['r2']))
})

test('a delete 404 renders in the action-error box and the list still refetches', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.delete('/v1/reservations/:id', () =>
      HttpResponse.json({ error: 'reservation not found' }, { status: 404 }),
    ),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  const before = seen.length

  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))

  expect(await screen.findByText('404 reservation not found')).toBeInTheDocument()
  // onSettled invalidation: the stale row leaves on the refetch, so the message is
  // informational rather than a dead end.
  await waitFor(() => expect(seen.length).toBeGreaterThan(before))
})

test('the 60s tick flips SCHEDULED to ACTIVE with ZERO extra requests', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date('2026-08-09T12:00:00Z'))
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      // Starts 5 fake minutes from now.
      items: [row({ starts_at: '2026-08-09T12:05:00Z' })],
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  expect(await screen.findByText('SCHEDULED')).toBeInTheDocument()
  const callsAfterLoad = seen.length

  act(() => {
    vi.advanceTimersByTime(6 * 60_000)
  })
  expect(await screen.findByText('ACTIVE')).toBeInTheDocument()
  // The tick is a local clock, not a refetch.
  expect(seen.length).toBe(callsAfterLoad)

  // Positive control on the SAME counter, in this same test.
  await user.click(screen.getByRole('button', { name: /^NAME/ }))
  await waitFor(() => expect(seen.length).toBeGreaterThan(callsAfterLoad))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/reservations/ReservationsTab.test.tsx`
Expected: FAIL - `Failed to resolve import "./ReservationsTab"`.

- [ ] **Step 3: Implement the tab**

Create `web/src/admin/reservations/ReservationsTab.tsx`:

```tsx
import { useState } from 'react'
import { Button } from '../../components/Button'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { useNow } from '../../lib/useNow'
import { CreateReservationForm } from './CreateReservationForm'
import { ReservationsTable } from './ReservationsTable'
import { useReservationActions } from './useReservationActions'
import { useReservations } from './useReservations'
import type {
  CreateReservationBody,
  Reservation,
  ReservationSort,
  ReservationSortField,
} from './api'

// Same shape as EnrollmentsTab's toggleSort (EnrollmentsTab.tsx:16-21): clicking the
// active column flips its direction, clicking another selects it ascending.
function toggleSort(field: ReservationSortField, current: ReservationSort): ReservationSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as ReservationSort
  }
  return field
}

export function ReservationsTab() {
  const [sort, setSort] = useState<ReservationSort>('-created_at')
  // Cursor of the current page (''=first); stack holds the cursors we paged forward
  // from; offsets tracks the real row offset so partial pages stay correct. Same
  // pattern as EnrollmentsTab / UsersTab / JobsPage.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
  const [creating, setCreating] = useState(false)
  const [confirm, setConfirm] = useState<Reservation | null>(null)

  // A local 60s clock tick, NOT a poll: it re-renders so the derived STATUS pill
  // stays correct as a window opens or closes, and issues no request.
  const now = useNow(60_000)

  const { data, error, isLoading, isPlaceholderData, refetch } = useReservations(sort, cursor)
  const { create, remove } = useReservationActions()

  // create.error is routed into the panel (it owns that copy); delete errors land in
  // the shared box, matching UsersTab.tsx:53-60.
  const actionError = remove.error as Error | null

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    setOffsets([])
  }

  function pickSort(field: ReservationSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match
    // (internal/api/pagination.go:272-286).
    resetPaging()
  }

  function next() {
    if (!data?.next_cursor) return
    const currentPageSize = data.items.length
    setStack([...stack, cursor])
    setCursor(data.next_cursor)
    setOffsets([...offsets, startOffset])
    setStartOffset(startOffset + currentPageSize)
  }

  function prev() {
    if (stack.length === 0) return
    const copy = [...stack]
    const back = copy.pop() ?? ''
    setStack(copy)
    setCursor(back)
    const offsetsCopy = [...offsets]
    const prevOffset = offsetsCopy.pop() ?? 0
    setOffsets(offsetsCopy)
    setStartOffset(prevOffset)
  }

  function onCreate(body: CreateReservationBody) {
    create.mutate(body, { onSuccess: () => setCreating(false) })
  }

  function runConfirmed() {
    if (!confirm) return
    remove.mutate(confirm.id)
    setConfirm(null)
  }

  const reservations = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(startOffset, reservations.length)
  const rangeText =
    reservations.length === 0
      ? `0 of ${total.toLocaleString()}`
      : `${x.toLocaleString()}-${y.toLocaleString()} of ${total.toLocaleString()}`

  let body
  if (isLoading && !data) {
    body = (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <GlassPanel key={i} className="h-9" />
        ))}
      </div>
    )
  } else if (error && !data) {
    body = (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </GlassPanel>
    )
  } else if (reservations.length === 0) {
    // A concurrent delete can empty a non-first page; without a way back, a reload is
    // the admin's only exit (same escape hatch as EnrollmentsTab.tsx:113-130).
    body = (
      <>
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No reservations.
        </GlassPanel>
        {stack.length > 0 && (
          <div className="flex justify-center">
            <button
              type="button"
              onClick={prev}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute"
            >
              ← prev
            </button>
          </div>
        )}
      </>
    )
  } else {
    body = (
      <>
        <ReservationsTable
          reservations={reservations}
          sort={sort}
          onSort={pickSort}
          now={now}
          busy={remove.isPending}
          onDelete={(r) => {
            // Clear a stale error before opening for a (possibly different) row -
            // the reset()-before-open convention from UsersTab.tsx:173-179.
            remove.reset()
            setConfirm(r)
          }}
        />
        <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
          <span>
            SHOWING <span className="text-fg">{rangeText}</span>
            {' · '}/v1/reservations · CURSOR PAGINATED
          </span>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={prev}
              disabled={stack.length === 0 || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              ← prev
            </button>
            <button
              type="button"
              onClick={next}
              disabled={!data?.next_cursor || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              next 50 →
            </button>
          </div>
        </div>
      </>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-mono text-[11px] tracking-[0.06em] text-fg-mute">
          GET /v1/reservations
        </span>
        <PillButton
          variant="primary"
          className="ml-auto"
          onClick={() => {
            // reset() clears a stale error so a freshly reopened empty form never
            // shows a leftover message (UsersTab.tsx:238-245).
            create.reset()
            setCreating((v) => !v)
          }}
        >
          + Reserve workers
        </PillButton>
      </div>

      {creating && (
        <CreateReservationForm
          pending={create.isPending}
          error={create.error as Error | null}
          onSubmit={onCreate}
          onCancel={() => {
            create.reset()
            setCreating(false)
          }}
        />
      )}

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {body}

      {/* This footnote CORRECTS the hi-fi rather than repeating it. The hi-fi's
          framing implies reserved workers run their owner's work; the scheduler
          unions worker_ids into reservedIDs and skips those workers for EVERY task
          (internal/scheduler/dispatch.go:185-191, :221-223). Kept as plain text with
          no nested spans so the wording is assertable as one normalized string. */}
      <div className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
        ▸ A reservation removes its worker_ids from the general dispatch pool: those workers stop
        receiving new tasks from every job, including the owner's own jobs. relay does not route
        the owner's work to them. selector, project and the owner are recorded but never read by
        the scheduler; only explicit worker_ids are enforced. A reservation is in force only while
        starts_at &lt;= now &lt; ends_at, and either bound may be open. Changes take effect on the
        next dispatch cycle (about 30s) and never preempt a task that is already running.
      </div>

      {confirm && (
        <ConfirmDialog
          title={`Delete reservation "${confirm.name}"?`}
          body={`Deleting returns its ${confirm.worker_ids.length} worker(s) to the general dispatch pool on the next dispatch cycle (about 30s). Tasks already running on them are unaffected. This cannot be undone.`}
          confirmLabel="Delete"
          destructive
          onConfirm={runConfirmed}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/reservations/ReservationsTab.test.tsx`
Expected: PASS (12 tests).

- [ ] **Step 5: Prove the delete gate is not vacuous (mandatory)**

Temporarily wire the row button straight to the mutation, bypassing the dialog - replace the table's `onDelete` prop body with `remove.mutate(r.id)`.

Run: `npx vitest run src/admin/reservations/ReservationsTab.test.tsx`
Expected: FAIL on `Delete is gated` (`expected 0 to be 1` on `deletes` after Cancel, or a missing dialog). If it still passes, the test is asserting nothing about the gate - fix the test first.

Revert, and re-run. Expected: PASS (12 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/admin/reservations/ReservationsTab.tsx web/src/admin/reservations/ReservationsTab.test.tsx
git commit -m "feat(web): ReservationsTab with honest dispatch-pool copy and confirmed delete"
```

---

## Task 10: Register the tab

One registry entry. Two shipped test files state the old truth and must be updated first - that is the RED step, and it is honest TDD rather than a chore (same as the enrollments slice).

**Files:**
- Modify: `web/src/admin/tabs.ts:2-3`, `:11-22`
- Modify: `web/src/admin/AdminTabs.test.tsx:16`, `:20-26`, `:28-36`, `:49-54`
- Modify: `web/src/admin/AdminPage.test.tsx:20-53` (add a handler), end of file (two tests)

- [ ] **Step 1: Update the two shipped test files to the new truth (RED)**

In `web/src/admin/AdminTabs.test.tsx`, replace the bodies of the four affected tests:

```tsx
test('the registry holds exactly the built tabs', () => {
  expect(ADMIN_TABS.map((t) => t.slug)).toEqual(['users', 'enrollments', 'reservations'])
  expect(DEFAULT_ADMIN_TAB).toBe('users')
})

test('findAdminTab resolves a known slug and rejects everything else', () => {
  expect(findAdminTab('users')?.label).toBe('Users')
  expect(findAdminTab('enrollments')?.label).toBe('Agent enrolls')
  expect(findAdminTab('reservations')?.label).toBe('Reservations')
  expect(findAdminTab('invites')).toBeUndefined()
  expect(findAdminTab('bogus')).toBeUndefined()
  expect(findAdminTab(undefined)).toBeUndefined()
})

test('renders one link per registry entry, pointing at /admin/<slug>', () => {
  renderTabs('/admin/users')
  expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('href', '/admin/users')
  expect(screen.getByRole('link', { name: 'Agent enrolls' })).toHaveAttribute(
    'href',
    '/admin/enrollments',
  )
  expect(screen.getByRole('link', { name: 'Reservations' })).toHaveAttribute(
    'href',
    '/admin/reservations',
  )
  expect(screen.getAllByRole('link')).toHaveLength(3)
})

test('tabs that are not built yet are absent', () => {
  renderTabs('/admin/users')
  for (const label of ['Invites', 'Server']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
})
```

Append one test to the same file:

```tsx
test('the reservations tab is marked current on its own route', () => {
  renderTabs('/admin/reservations')
  expect(screen.getByRole('link', { name: 'Reservations' })).toHaveAttribute(
    'aria-current',
    'page',
  )
  expect(screen.getByRole('link', { name: 'Users' })).not.toHaveAttribute('aria-current')
})
```

In `web/src/admin/AdminPage.test.tsx`, add a handler to `renderAt`'s `server.use(...)` (after the `/v1/agent-enrollments` handler at `:38-52`) so the panel can mount under MSW's fail-closed policy. No `/v1/workers` handler is needed: the picker query lives inside `WorkerPicker`, which mounts only when the create panel is open.

```tsx
    http.get('/v1/reservations', () =>
      HttpResponse.json({
        items: [
          {
            id: 'r1',
            name: 'gpu-farm-hold',
            selector: null,
            worker_ids: ['aaaa1111-1111-1111-1111-111111111111'],
            user_id: 'u1',
            created_at: '2026-08-09T09:30:00Z',
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
```

and append two shell-integration tests:

```tsx
test('/admin/reservations renders the reservations panel inside the same shell', async () => {
  renderAt('/admin/reservations')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('gpu-farm-hold')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Reservations' })).toHaveAttribute(
    'aria-current',
    'page',
  )
})

test('/admin/users and /admin/enrollments still render their own panels', async () => {
  renderAt('/admin/users')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.queryByText('gpu-farm-hold')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```
npx vitest run src/admin/AdminTabs.test.tsx src/admin/AdminPage.test.tsx
```

Expected: FAIL - `expected [ 'users', 'enrollments' ] to deeply equal [ 'users', 'enrollments', 'reservations' ]`, `Unable to find role="link" with name "Reservations"`, and `/admin/reservations` redirecting to the Users panel so `gpu-farm-hold` is never found.

- [ ] **Step 3: Add the registry entry**

Edit `web/src/admin/tabs.ts` - add the import and the entry, and drop reservations from the not-yet-built comment:

```ts
import type { ComponentType } from 'react'
import { EnrollmentsTab } from './enrollments/EnrollmentsTab'
import { ReservationsTab } from './reservations/ReservationsTab'
import { UsersTab } from './users/UsersTab'

export interface AdminTab {
  slug: string
  label: string
  Panel: ComponentType
}

// The admin console is a registry plus a switch. Tabs that are not built yet are
// ABSENT on purpose: an unknown /admin/:tab segment redirects to /admin/users
// instead of rendering an empty panel, so this slice cannot ship dead tabs.
// Adding a tab later is one entry here - see
// docs/backlog/feature-2026-08-08-admin-invites-tab.md,
// docs/backlog/feature-2026-08-08-admin-server-overview-tab.md.
// Order matches the hi-fi's tab order (Invites, still absent, sits between Users and
// Agent enrolls).
export const ADMIN_TABS: AdminTab[] = [
  { slug: 'users', label: 'Users', Panel: UsersTab },
  { slug: 'enrollments', label: 'Agent enrolls', Panel: EnrollmentsTab },
  { slug: 'reservations', label: 'Reservations', Panel: ReservationsTab },
]

export const DEFAULT_ADMIN_TAB = 'users'

export function findAdminTab(slug: string | undefined): AdminTab | undefined {
  return ADMIN_TABS.find((t) => t.slug === slug)
}
```

No router change: `/admin/:tab` already exists at `web/src/app/router.tsx:34` and `AdminPage` already dispatches through `findAdminTab`.

- [ ] **Step 4: Run the tests to verify they pass**

```
npx vitest run src/admin/
```

Expected: PASS for the whole `src/admin/` directory - the Users and Enrollments suites are unchanged and must stay green.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/tabs.ts web/src/admin/AdminTabs.test.tsx web/src/admin/AdminPage.test.tsx
git commit -m "feat(web): register the Reservations admin tab"
```

---

## Task 11: Whole-plan gate

- [ ] **Step 1: Full suite green**

Run (from `web/`): `npm test`
Expected: PASS. The suite stood at **617** tests before this plan and should now be roughly 617 + 64 (16 pure, 9 api, 3 workers/picker-query, 2 list hook, 5 actions, 7 picker, 11 table, 10 form, 12 tab, 1 new AdminTabs test, 2 new AdminPage tests; none removed). The exact number does not matter; **zero failures and zero skips** does.

- [ ] **Step 2: Production build clean**

Run: `npm run build`
Expected: exit 0, no TypeScript errors. TS is the only checker for the `selector: Record<string, string> | null` vs `project?: string` split and for the `Chip` tone union, so a green build is part of the contract.

- [ ] **Step 3: Revert the tracked-but-stale build output**

`web/dist` is tracked in git but stale from the original scaffold, so Step 2 dirties it.

```bash
git checkout -- web/dist/
git status --short
```

Expected: `web/dist` clean; the only remaining changes are the source and test files this plan lists.

- [ ] **Step 4: Confirm the scope guard held**

```bash
git diff --stat origin/main...HEAD
```

Expected: **no Go files, no `.sql` files, no `web/dist` files.** Changed paths confined to `web/src/admin/`, `web/src/workers/api.ts`, `web/src/workers/api.test.ts`, `web/src/lib/time.ts` and `web/src/lib/time.test.ts`. Any Go file means a backend enricher crept in - revert it. Also grep the diff for `TokenRevealDialog`, `gcTime`, and `secretLeaks`: **all three must be absent.**

- [ ] **Step 5: Browser check against a real backend**

`make build`, run `relay-server` with a real Postgres, sign in as an admin at `http://localhost:8080`, then:

1. Navigate to `/admin` - it redirects to `/admin/users`; the tab bar shows **Users**, **Agent enrolls**, **Reservations**.
2. Click **Reservations** - the URL becomes `/admin/reservations`, the tab is highlighted, the list loads (empty card on a fresh DB), and the footnote reads as the exclusion statement, not an affinity claim.
3. Click **+ Reserve workers**. Confirm the picker lists real workers by name, that the filter narrows them with no network request in DevTools, and that with a fleet under 200 there is **no** ceiling note.
4. With an empty name, or zero workers selected, or `Ends` before `Starts`, confirm **Reserve** is disabled and the matching inline message is shown. Fix each and confirm it enables.
5. Create a reservation with one worker and a `Starts` a few minutes in the future. Confirm the POST body in DevTools contains **only** `name`, `worker_ids` (and `project`/`starts_at`/`ends_at` if set), the dates carry a `Z`/offset, the panel closes, and the row appears with a **SCHEDULED** pill and no manual reload.
6. Confirm the `STARTS` cell matches the local time you typed. Wait for the start to pass and confirm the pill flips to **ACTIVE** with **no** new request in the Network tab.
7. With the reservation active, queue a job requiring that worker's labels and confirm the task stays `pending` (the worker is excluded), and that nothing was routed to it "for" you. Delete the reservation and confirm the task dispatches within roughly 30s.
8. Click **Delete**, read the dialog body, press **Cancel** - no request is sent. Reopen and press **Delete** - a single `DELETE /v1/reservations/{id}` returns 204 and the row disappears.
9. Click a worker chip and confirm it navigates to that worker's detail page.

---

## Tests most at risk of being vacuous

A plan's test bodies are guesses, not verified guards. These are the ones to distrust; the proof for each is a numbered step above.

| Test | Why it can pass for the wrong reason | Proof of RED |
|---|---|---|
| `formatDateTime` (Task 1) | A hardcoded literal against a `Z` input passes only in a UTC CI. | Every input is built from **local** `Date` components and the expectation spells out those same components; plus the "off UTC the ISO text really differs" control on the test data. |
| `deriveStatus` boundaries (Task 1) | A `<` / `<=` flip on either bound still passes a test that only checks obviously-past and obviously-future rows. | The two exactly-at-`now` cases assert in the SQL's own direction, and the matrix test compares ACTIVE against an independent transcription of `reservations.sql:21-22` while asserting the matrix contains both outcomes. |
| "create/remove invalidates the list" (Task 5) | Without an **active observer** (`renderHook`-mounted query), `refetchType: 'active'` never fires and the assertion holds no matter what key is invalidated. A `fetchQuery`/`setQueryData` seed is the trap. | Task 5 Step 5: breaking the key to `['reservations-broken']` must fail **four** tests including both `listCalls` assertions. |
| "Cancel sends no DELETE" (Task 9) | Passes trivially if the Delete button is inert or the mutation is broken. | The paired positive control in the same test (Confirm sends exactly one), plus Task 9 Step 5: bypassing the dialog must turn it RED. |
| "no affinity claims" (Task 9, Task 8) | `queryByText` cannot see copy split across nested spans, and a typo'd pattern never matches anything. | Asserted on **normalized `container.textContent`**, with five positive controls on the same instrument (the honest phrases must be found). |
| "the ceiling note is absent" (Task 6) | An absence assertion passes on a typo'd query. | The paired over-ceiling test asserts the exact note text with `total > items.length`. |
| "the filter issues no request" / "does not poll" / "the tick issues no request" (Tasks 3, 4, 6, 9) | A counter that can never move makes any "stayed at N" assertion free. | Each carries an in-test control moving the same counter: `refetch()` in Tasks 3 and 4, mount-incremented `calls` in Task 6, a sort-header click in Task 9. |
| "project / starts_at / ends_at render a hyphen" (Task 7) | Would also pass if the cell rendered nothing at all. | The same test asserts `null`, `undefined` and dash characters are absent **and** counts exactly three hyphens, with a paired positive-value test. |
| "createReservation always sends a body" (Task 2) | An MSW handler that accepts anything proves nothing about the mandatory body. | The handler mirrors `readJSON` and 400s an empty body, so dropping the body fails the test. |
| "deleteReservation tolerates 204" (Task 2) | An MSW handler returning `HttpResponse.json({})` with status 204 would hide the bug. | The handler returns `new HttpResponse(null, { status: 204 })` - genuinely no body. |
| "an explicit limit does not disturb existing callers" (Task 3) | Only checkable by running the shipped suite, not the new test. | Task 3 Step 4 runs the whole `src/workers/` directory, including the untouched `limit=50` assertion. |

---

## Self-review against the spec

- **Decision 1** (nothing secret; no `TokenRevealDialog`, no `gcTime: 0`, no `secretLeaks.ts`): scope guard, the comment block in Task 5's hook, Task 9's "nothing is revealed" assertion, and Task 11 Step 4's grep.
- **Decision 2** (delete behind `ConfirmDialog`, destructive, 404 informational): Tasks 5 (`onSettled`) and 9 (gating + 404 tests).
- **Decision 3** (three client validations, two deliberate non-validations): Task 8.
- **Decision 4** (picker over the first 200 by name, `listWorkers` optional limit, `['workers','reservation-picker']`, no `refetchInterval`, `staleTime: 30_000`, stated ceiling): Tasks 3 and 6.
- **Decision 5** (`selector` and `user_id` omitted from the form and the body): Tasks 2 and 8.
- **Decision 6** (columns, four clickable sort headers, `sel` chip, linked truncated worker chips, `formatDateTime`, honest footnote): Tasks 1, 7, 9.
- **Verified backend surface / nullability**: Task 2's contract tests, field for field.
- **Testing section**: every listed assertion has a home - pure units Task 1, contract Task 2, list Tasks 4 and 9, create Tasks 5, 8, 9, delete Tasks 5 and 9, picker Tasks 3 and 6, shell integration Task 10.
- **Acceptance criteria 1-10**: 1 -> Task 10; 2 -> Tasks 4, 9; 3 -> Tasks 2, 7; 4 -> Tasks 1, 7, 9; 5 -> Tasks 2, 5, 8, 9; 6 -> Tasks 3, 6, 8; 7 -> Tasks 2, 5, 9; 8 -> Tasks 8, 9; 9 -> scope guard + Task 11 Step 4; 10 -> Task 11.
- **Type consistency**: `Reservation` / `ReservationsPage` / `ReservationSort` / `ReservationSortField` / `CreateReservationBody` are defined once in Task 2 and used under those exact names in Tasks 4, 5, 7, 8, 9. `deriveStatus` / `statusTone` / `ReservationStatus` (Task 1) are used under those names in Task 7. `formatDateTime` (Task 1) is used in Task 7. `useWorkerOptions` / `WORKER_PICKER_LIMIT` (Task 3) are used in Task 6. `WorkerPicker`'s props (`value`, `onChange`) match its single call site in Task 8. `ReservationsTable`'s props (`reservations`, `sort`, `onSort`, `now`, `busy`, `onDelete`) match its single call site in Task 9. `CreateReservationForm`'s props (`pending`, `error`, `onSubmit`, `onCancel`) match Task 9.
- **Gaps I am accepting, stated rather than hidden**: the picker cannot reach a worker beyond the first 200 (noted in the UI and in the spec's risks), and no test asserts `staleTime: 30_000` behaviourally, because any such test would only be re-asserting TanStack's own caching.
