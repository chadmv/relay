# Admin Console - Agent Enrollments Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a second admin-console tab, Agent enrollments, that lists active enrollment tokens and creates new ones, revealing each raw token clear-text exactly once in a shared, security-audited dialog.

**Architecture:** A new feature module `web/src/admin/enrollments/` that is a structural clone of the shipped `web/src/admin/users/` tree (api clients -> list query hook -> actions hook -> presentational table -> inline create panel -> composing tab), plus one shared component at the admin module root, `web/src/admin/TokenRevealDialog.tsx`, which is the only place the raw credential is rendered. The tab reaches the router through a single new entry in `ADMIN_TABS`; `/admin/:tab` already exists (`web/src/app/router.tsx:34`), so no routing change is needed. Status is derived client-side from `expires_at` because the server returns active-only rows and no status field.

**Tech Stack:** React 18, TypeScript, TanStack Query v5, react-router-dom v7, Tailwind v4 (Holo tokens), Vitest + Testing Library + MSW.

**Spec:** `docs/superpowers/specs/2026-08-09-admin-enrollments-tab.md` (approved; do not reopen its decisions)

---

## Slice independence declaration

- **Backend slice: NONE. Zero Go files change.** Both endpoints already exist and are already admin-gated. No `.sql` edit, therefore **no `make generate`**, and no `*.sql.go` / `models.go` involvement. None of the six Invariants (epoch fence, single job-spec pipeline, one bounded sender per stream, identity-checked teardown, no interior pointers across locks, single JSON entry point) is in play. The frontend analogue of the last one does apply: every request goes through `apiFetch` (`web/src/lib/api.ts:29`), never a bare `fetch`.
- **Frontend slice: ONE, and it is SEQUENTIAL.** Do not split these tasks across two engineers.
  - Tasks 2-8 all write into the same new `web/src/admin/enrollments/` directory and Task 9 edits `web/src/admin/tabs.ts` plus two shipped test files. Any second writer collides.
  - The dependency chain is nearly linear: Task 8 imports the output of Tasks 1-7; Task 9 imports Task 8; Task 10 renders Task 8.
  - The project has been burned twice by concurrent writers on shared frontend files. One engineer, tasks in order.
- **Parallelism available to the conductor:** none *within* this plan. Unrelated work elsewhere in the repo can run alongside the plan as a whole.

---

## Verified backend surface (read for yourself; do not trust a table)

I read `internal/api/server.go:141-144`, `internal/api/agent_enrollments.go`, `internal/api/server.go:196-211` (`readJSON`), `internal/api/pagination.go:206-293`, and `internal/store/query/agent_enrollments.sql`. Result: **the spec's endpoint table is correct on every claim it makes.** Three clarifications it does not make explicit, which the implementation depends on:

| Spec claim | Verdict | Evidence |
|---|---|---|
| `POST /v1/agent-enrollments`, `GET /v1/agent-enrollments`, both `auth(admin(...))` | **Confirmed** | `internal/api/server.go:142-143` |
| A JSON body is mandatory on POST; no body is a 400 `invalid request body` | **Confirmed** | `agent_enrollments.go:27-29` calls `readJSON` unconditionally; `server.go:201-207` turns the resulting `io.EOF` into 400 `invalid request body` |
| 201 returns exactly `{ id, token, expires_at }` | **Confirmed** | `agent_enrollments.go:68-72` - no `created_at`, no `hostname_hint` echo |
| TTL default 24h, min 60s, max 604800s (7d) | **Confirmed** | `agent_enrollments.go:16-20`, `:31-42` |
| `?sort=` is `created_at` or `expires_at`, optional `-`, default `-created_at`; all four arms implemented | **Confirmed** | `AgentEnrollmentsSortSpec` `agent_enrollments.go:75-81`; dispatch `:162-217` |
| `?limit=` 1..200 default 50, `?cursor=` | **Confirmed** | `pagination.go:206-207`, `:242-249`, `:267-271` |
| Envelope `{ items, next_cursor, total }` | **Confirmed** | `pagination.go:289-293`; `agent_enrollments.go:224` |
| Row is `{ id, created_at, expires_at, created_by }` plus `hostname_hint` **only when set - key absent, not null** | **Confirmed** | `enrollmentRowToMap` `agent_enrollments.go:83-94`, specifically the `if row.HostnameHint != nil` guard at `:90-92`; identical in all three sort twins at `:100-149` |
| No DELETE for enrollments | **Confirmed** | `server.go:141-146` - the only DELETE nearby is `/v1/workers/{id}/token` at `:144` |
| List and count are active-only | **Confirmed** | `internal/store/query/agent_enrollments.sql:17`, `:26-27`, `:35`, `:40-41`, `:50-51`, `:60-61` |

**Clarification 1 (matters for the create body).** The 60s floor applies **only to a nonzero `ttl_seconds`**. `agent_enrollments.go:31-34` reads: `ttl := defaultEnrollmentTTL; if req.TTLSeconds != 0 { ttl = ... }`. So `{"ttl_seconds": 0}` and `{}` both mean 24h and are **not** 400s; a negative value falls through to the `< minEnrollmentTTL` branch and **is** a 400. This UI never sends `0` or omits the key, so the floor is stated in code but unreachable from the UI.

**Clarification 2 (matters for date handling).** `expires_at` and `created_at` are Go `time.Time` values marshalled by `encoding/json`, i.e. RFC3339 with nanosecond precision (`2026-08-10T12:00:00.123456789Z`). Always parse with `new Date(iso)`; never string-compare timestamps.

**Clarification 3 (matters for the list client).** `?limit=0` is a **400**, not a clamp (`pagination.go:244`). We always send `limit=50` explicitly, so this cannot bite, but do not "optimise" the param away and then reintroduce it computed.

Also worth stating because it shapes the UI: `POST /v1/agent-enrollments` is **not** rate-limited (`internal/api/ratelimit.go` covers login and register only), and `runEnrollmentJanitor` (`cmd/relay-server/main.go:245-258`) deletes unconsumed expired rows hourly.

---

## What is inherited, not re-specified

Read these files before writing anything. Where a task says "mirror X at `file:line`", that is the literal instruction - copy the shape, change the nouns.

| Inherited thing | Source to mirror |
|---|---|
| Tab registration (one entry + a `findAdminTab` lookup) | `web/src/admin/tabs.ts:18-24` |
| Admin shell header, tab bar, unknown-tab redirect | `web/src/admin/AdminPage.tsx:10-35`, `web/src/admin/AdminTabs.tsx:9-29` |
| List-page shape: cursor + `stack` + `offsets`, `computePageRange`, mono footer, `isPlaceholderData` pager gating, loading/error/empty triad | `web/src/admin/users/UsersTab.tsx:30-213` |
| `keepPreviousData`, no `refetchInterval` | `web/src/admin/users/useAdminUsers.ts:9-20` |
| Mutation shape: bare-prefix invalidation, no optimistic update, `reset()` before reopening a form | `web/src/admin/users/useAdminUserActions.ts:20-51` |
| Header-click sorting: `toggleSort`, `caret`, `ariaSort`, `aria-sort` on the `columnheader` | `web/src/admin/users/UsersTab.tsx:17-22`, `web/src/admin/users/UsersTable.tsx:19-27`, `:81-100` |
| Inline create panel (not a modal) with a form-level error line | `web/src/admin/users/CreateUserForm.tsx:18-98` |
| Dialog a11y baseline: `role="dialog"`, `aria-modal="true"`, `aria-labelledby` title, Escape dismisses, first field focused, **no focus trap** | `web/src/components/ConfirmDialog.tsx:26-46`, `web/src/admin/users/ResetPasswordDialog.tsx:30-67` |
| Single fetch entry point, `ApiError` | `web/src/lib/api.ts:4-13`, `:29-59` |
| Page range maths | `web/src/lib/pageRange.ts:6-12` |
| Tick-hook shape (sibling of the new `useNow`) | `web/src/lib/useDebouncedValue.ts:7-14` |
| Console-secrecy matcher that survives an `Error` | `web/src/jobs/logSecrecy.test.tsx:20-56` |
| Holo primitives | `web/src/components/holo/Chip.tsx` (tones `accent`/`muted`/`warn`), `PillButton.tsx` (variants `primary`/`ghost`/`muted`/`danger`), `GlassPanel`, `Eyebrow`, barrel `web/src/components/holo/index.ts`; `web/src/components/Field.tsx`, `web/src/components/Input.tsx`, `web/src/components/Button.tsx` |
| Test harness | `web/src/test/setup.ts:5` (MSW `onUnhandledRequest: 'error'` - every touched endpoint needs a handler), `web/src/test/setup-helpers.ts` re-exports `server`, `web/src/test/renderWithQuery.tsx:7-12`; representative component test `web/src/admin/users/UsersTab.test.tsx:28-52` |

---

## Conventions for every task

- All commands run from the `web/` directory.
- Single file: `npx vitest run src/<path>.test.tsx`. Full suite: `npm test`.
- TDD: write the failing test, run it and watch it fail with the stated message, implement, run it and watch it pass, commit.
- MSW is fail-closed on unhandled requests. Any test that mounts `EnrollmentsTab` must register a `GET /v1/agent-enrollments` handler. `EnrollmentsTab` does **not** use `useAuth`, so unlike `UsersTab` tests it needs **no** `AuthProvider` and no `/v1/users/me` handler.
- House rule: **never use an em dash or en dash.** The absent-hint placeholder is a plain ASCII hyphen `-`, not `—`. (The hi-fi uses `—` at `hifi3-holo-pages.jsx:2178`; do not copy that character.)
- Never reformat code you were not asked to change.

---

## Scope guard

**Do NOT build, in this slice:**
- **Any revoke control.** `DELETE /v1/agent-enrollments/{id}` does not exist (verified above). A revoke button would be a guaranteed-405 dead affordance. Tracked by `docs/backlog/feature-2026-06-26-agent-enrollment-revocation.md`. The row's last column is headed **NOTE**, not ACTIONS, and holds prose.
- **Any backend change.** Zero Go edits. If you find yourself wanting a `token_prefix` column or a `created_by_email` enricher, stop: those are recorded in the spec's "Omitted from the hi-fi" table as proposals for other items.
- **An Invites tab.** `TokenRevealDialog` is built here as a shared component because the reveal half is identical for invites, but it is wired to **enrollments only**. Do not add an `isInvite` flag, and do not add an `invites` registry entry.
- **A `TOKEN PREFIX` column or a `CREATED BY` column.** Nothing stores a prefix; `created_by` is a bare UUID with no join to `users`.
- **A `CONSUMED` status.** Structurally unobservable: every query filters `consumed_at IS NULL`.
- **A free-form TTL input, a `SortControl` dropdown, a success toast, a tab count badge, or a `size: 50` footer button.** All four are hi-fi elements the spec replaced or dropped.
- **Changes to the shipped Users tab** beyond the two test-file edits the registry change forces (Task 9).

---

## File Structure

**New files**

| File | Responsibility |
|---|---|
| `web/src/lib/useNow.ts` | 60s local clock tick. Issues no request. Sibling of `useDebouncedValue.ts`. |
| `web/src/lib/useNow.test.ts` | Fake-timer tick + interval-cleared-on-unmount. |
| `web/src/admin/enrollments/enrollmentStatus.ts` | Pure `deriveStatus(expiresAt, now)` + `statusTone`. |
| `web/src/admin/enrollments/enrollmentStatus.test.ts` | Boundary tests at a fixed `now`. |
| `web/src/admin/enrollments/api.ts` | Types, TTL bounds + presets, the two typed clients. |
| `web/src/admin/enrollments/api.test.ts` | Method/path/params/body contract, absent-key handling, preset-bounds guard. |
| `web/src/admin/enrollments/useAgentEnrollments.ts` | List query. `keepPreviousData`, no `refetchInterval`. |
| `web/src/admin/enrollments/useAgentEnrollments.test.tsx` | Query key, params, non-vacuous no-poll. |
| `web/src/admin/enrollments/useAgentEnrollmentActions.ts` | `{ create }`, invalidating bare `['agent-enrollments']`. |
| `web/src/admin/enrollments/useAgentEnrollmentActions.test.tsx` | Exact body + active-observer invalidation. |
| `web/src/admin/TokenRevealDialog.tsx` | **Shared.** The only component that renders a raw credential. |
| `web/src/admin/TokenRevealDialog.test.tsx` | a11y baseline, no-backdrop-close, Escape, Done, clipboard feature-detect. |
| `web/src/admin/enrollments/EnrollmentsTable.tsx` | Presentational table, derived status pill, sortable CREATED/EXPIRES. |
| `web/src/admin/enrollments/EnrollmentsTable.test.tsx` | Columns, absent hint, three states, sort wiring, no row controls. |
| `web/src/admin/enrollments/CreateEnrollmentForm.tsx` | Inline create panel: hint + four TTL presets. |
| `web/src/admin/enrollments/CreateEnrollmentForm.test.tsx` | Exact payloads, preset selection, no free-form TTL. |
| `web/src/admin/enrollments/EnrollmentsTab.tsx` | Composition: control row, panel, table, footer, footnote, reveal dialog. |
| `web/src/admin/enrollments/EnrollmentsTab.test.tsx` | States, sort, pager, create-to-reveal flow, 60s tick with no request. |
| `web/src/test/secretLeaks.ts` | Reusable leak matchers (console walker, DOM-incl-input-values, storage). |
| `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx` | Absence assertions, each with a paired positive control. |

**Modified files**

| File | Change |
|---|---|
| `web/src/lib/time.ts` | Append `formatTimeUntil`. Do not touch `formatRelativeTime`. |
| `web/src/lib/time.test.ts` | Append a `formatTimeUntil` describe block. |
| `web/src/admin/tabs.ts:2`, `:10-18` | Import `EnrollmentsTab`; add one `ADMIN_TABS` entry after `users`; drop the enrollments line from the not-yet-built comment. |
| `web/src/admin/AdminTabs.test.tsx:16`, `:22`, `:30`, `:40` | Registry now holds two slugs / two links; `Agent enrolls` is no longer absent. |
| `web/src/admin/AdminPage.test.tsx:20-38` | Add a `GET /v1/agent-enrollments` handler to `renderAt`; add two shell-integration tests. |

**Reused, not rebuilt:** `GlassPanel`, `PillButton`, `Chip`, `Field`, `Input`, `Button`, `computePageRange`, `apiFetch`, `ApiError`.

---

## Task 1: Pure helpers - useNow, formatTimeUntil, deriveStatus

Three tiny pure units in one task and one commit. They are the only things in the slice with zero React-Query or DOM coupling.

**Files:**
- Create: `web/src/lib/useNow.ts`, `web/src/lib/useNow.test.ts`
- Create: `web/src/admin/enrollments/enrollmentStatus.ts`, `web/src/admin/enrollments/enrollmentStatus.test.ts`
- Modify: `web/src/lib/time.ts` (append), `web/src/lib/time.test.ts` (append)

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/useNow.test.ts`:

```ts
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { useNow } from './useNow'

// Vitest's fake timers fake Date as well as setInterval by default, so
// vi.setSystemTime + advanceTimersByTime moves what `new Date()` returns.
beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

test('returns the current time and advances on each tick', () => {
  vi.setSystemTime(new Date('2026-08-09T00:00:00Z'))
  const { result } = renderHook(() => useNow(60_000))
  expect(result.current.toISOString()).toBe('2026-08-09T00:00:00.000Z')

  act(() => {
    vi.advanceTimersByTime(60_000)
  })
  expect(result.current.toISOString()).toBe('2026-08-09T00:01:00.000Z')

  act(() => {
    vi.advanceTimersByTime(120_000)
  })
  expect(result.current.toISOString()).toBe('2026-08-09T00:03:00.000Z')
})

test('does not tick before the interval elapses', () => {
  vi.setSystemTime(new Date('2026-08-09T00:00:00Z'))
  const { result } = renderHook(() => useNow(60_000))
  act(() => {
    vi.advanceTimersByTime(59_999)
  })
  expect(result.current.toISOString()).toBe('2026-08-09T00:00:00.000Z')
})

test('clears its interval on unmount', () => {
  vi.setSystemTime(new Date('2026-08-09T00:00:00Z'))
  const { result, unmount } = renderHook(() => useNow(60_000))
  const before = result.current
  unmount()
  act(() => {
    vi.advanceTimersByTime(600_000)
  })
  // Two independent proofs: the value is frozen, and no timer survives. The
  // second is what actually distinguishes a cleared interval from a stale
  // render snapshot.
  expect(result.current).toBe(before)
  expect(vi.getTimerCount()).toBe(0)
})
```

Create `web/src/admin/enrollments/enrollmentStatus.test.ts`:

```ts
import { expect, test } from 'vitest'
import { deriveStatus, statusTone } from './enrollmentStatus'

const NOW = new Date('2026-08-09T12:00:00Z')

test('exactly at expires_at is EXPIRED', () => {
  expect(deriveStatus('2026-08-09T12:00:00Z', NOW)).toBe('EXPIRED')
})

test('already past expires_at is EXPIRED', () => {
  expect(deriveStatus('2026-08-09T11:59:59Z', NOW)).toBe('EXPIRED')
})

test('59m59s remaining is EXPIRING', () => {
  expect(deriveStatus('2026-08-09T12:59:59Z', NOW)).toBe('EXPIRING')
})

test('exactly 1h remaining is ACTIVE (the window is strictly under an hour)', () => {
  expect(deriveStatus('2026-08-09T13:00:00Z', NOW)).toBe('ACTIVE')
})

test('1h00m01s remaining is ACTIVE', () => {
  expect(deriveStatus('2026-08-09T13:00:01Z', NOW)).toBe('ACTIVE')
})

test('a nanosecond-precision RFC3339 timestamp parses (Go marshals time.Time this way)', () => {
  expect(deriveStatus('2026-08-10T12:00:00.123456789Z', NOW)).toBe('ACTIVE')
})

test('tones map to the three Chip tones that exist', () => {
  expect(statusTone('ACTIVE')).toBe('accent')
  expect(statusTone('EXPIRING')).toBe('warn')
  expect(statusTone('EXPIRED')).toBe('muted')
})
```

Append to `web/src/lib/time.test.ts`:

```ts
describe('formatTimeUntil', () => {
  const now = new Date('2026-08-09T12:00:00Z')

  test('seconds', () => {
    expect(formatTimeUntil('2026-08-09T12:00:45Z', now)).toBe('in 45s')
  })
  test('minutes', () => {
    expect(formatTimeUntil('2026-08-09T12:42:00Z', now)).toBe('in 42m')
  })
  test('hours', () => {
    expect(formatTimeUntil('2026-08-10T09:42:00Z', now)).toBe('in 21h')
  })
  test('days', () => {
    expect(formatTimeUntil('2026-08-16T12:00:00Z', now)).toBe('in 7d')
  })
  test('the instant of expiry, and any past instant, read as expired', () => {
    expect(formatTimeUntil('2026-08-09T12:00:00Z', now)).toBe('expired')
    expect(formatTimeUntil('2026-08-09T11:00:00Z', now)).toBe('expired')
  })

  // Positive control for WHY this function exists: formatRelativeTime clamps the
  // future to zero (time.ts:2) and appends "ago", so reusing it for an expiry
  // would render every future expiry as "0s ago". If this assertion ever fails,
  // formatRelativeTime changed and formatTimeUntil may be redundant.
  test('formatRelativeTime cannot do this - it renders the same future as 0s ago', () => {
    expect(formatRelativeTime('2026-08-10T09:42:00Z', now)).toBe('0s ago')
  })
})
```

Update the import line at `web/src/lib/time.test.ts:2` to `import { formatRelativeTime, formatTimeUntil } from './time'`.

- [ ] **Step 2: Run the tests to verify they fail**

```
npx vitest run src/lib/useNow.test.ts src/lib/time.test.ts src/admin/enrollments/enrollmentStatus.test.ts
```

Expected: FAIL - `Failed to resolve import "./useNow"`, `Failed to resolve import "./enrollmentStatus"`, and `formatTimeUntil is not a function` for `time.test.ts`.

- [ ] **Step 3: Implement the three helpers**

Create `web/src/lib/useNow.ts`:

```ts
import { useEffect, useState } from 'react'

// Returns a Date that re-renders the caller every intervalMs. Sibling of
// useDebouncedValue: this is a local CLOCK tick, not a data refresh - it issues
// no network request. The admin enrollments table uses it at 60s so a relative
// "in 21h" label and an EXPIRING/EXPIRED pill stay correct without polling
// GET /v1/agent-enrollments, which is not live data.
export function useNow(intervalMs: number): Date {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}
```

Append to `web/src/lib/time.ts`:

```ts
// Future-facing sibling of formatRelativeTime. It exists because
// formatRelativeTime clamps with Math.max(0, ...) and suffixes "ago", so it
// renders every future instant as "0s ago". Same injectable-`now` signature so
// callers can drive it from useNow (or a fixed date in tests).
//
// A non-future instant reads "expired" rather than a negative duration, which
// keeps the EXPIRES cell consistent with the EXPIRED status pill derived from the
// same arithmetic.
export function formatTimeUntil(iso: string, now: Date = new Date()): string {
  const secs = Math.round((new Date(iso).getTime() - now.getTime()) / 1000)
  if (secs <= 0) return 'expired'
  if (secs < 60) return `in ${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `in ${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `in ${hours}h`
  return `in ${Math.floor(hours / 24)}d`
}
```

Create `web/src/admin/enrollments/enrollmentStatus.ts`:

```ts
export type EnrollmentStatus = 'ACTIVE' | 'EXPIRING' | 'EXPIRED'

const EXPIRING_WINDOW_MS = 60 * 60 * 1000

// Every list and count query filters `consumed_at IS NULL AND expires_at > NOW()`
// (internal/store/query/agent_enrollments.sql:26-27, :35, :40-41, :50-51, :60-61)
// and no row carries a status field, so the ONLY server-asserted fact about a
// returned row is "unconsumed and unexpired as of query time". Status is therefore
// arithmetic on expires_at and the browser clock, and nothing else is honestly
// derivable:
//
//  - There is deliberately no CONSUMED state. Consumption sets consumed_at and the
//    row simply vanishes from the list; inventing the state would be faking data.
//  - EXPIRED is reachable only for a row already on screen when its expiry passes.
//    The query never returns one. Rendering it as ACTIVE would be a lie the client
//    can disprove with arithmetic it already has.
//  - This reads the local clock, so a badly skewed browser mislabels a row.
//    Accepted: the server exposes no status to prefer instead.
export function deriveStatus(expiresAt: string, now: Date): EnrollmentStatus {
  const remaining = new Date(expiresAt).getTime() - now.getTime()
  if (remaining <= 0) return 'EXPIRED'
  if (remaining < EXPIRING_WINDOW_MS) return 'EXPIRING'
  return 'ACTIVE'
}

// The three tones Chip already ships (web/src/components/holo/Chip.tsx:8-12).
export function statusTone(status: EnrollmentStatus): 'accent' | 'warn' | 'muted' {
  if (status === 'EXPIRED') return 'muted'
  if (status === 'EXPIRING') return 'warn'
  return 'accent'
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
npx vitest run src/lib/useNow.test.ts src/lib/time.test.ts src/admin/enrollments/enrollmentStatus.test.ts
```

Expected: PASS (3 + 11 + 7 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/useNow.ts web/src/lib/useNow.test.ts web/src/lib/time.ts web/src/lib/time.test.ts web/src/admin/enrollments/enrollmentStatus.ts web/src/admin/enrollments/enrollmentStatus.test.ts
git commit -m "feat(web): useNow tick, formatTimeUntil, and enrollment status derivation"
```

---

## Task 2: Enrollment API clients, types, and TTL bounds

**Files:**
- Create: `web/src/admin/enrollments/api.ts`
- Test: `web/src/admin/enrollments/api.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/enrollments/api.test.ts`:

```ts
import { expect, test } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/setup-helpers'
import {
  createAgentEnrollment,
  listAgentEnrollments,
  DEFAULT_TTL_SECONDS,
  MAX_TTL_SECONDS,
  MIN_TTL_SECONDS,
  TTL_PRESETS,
} from './api'

const ROW = {
  id: 'e1',
  created_at: '2026-08-09T00:00:00Z',
  expires_at: '2026-08-10T00:00:00Z',
  created_by: '11111111-2222-3333-4444-555555555555',
  hostname_hint: 'farm-west-13',
}

test('listAgentEnrollments sends sort and limit=50, omits an empty cursor, and returns the envelope', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/agent-enrollments', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: 'c2', total: 7 })
    }),
  )
  const page = await listAgentEnrollments({ sort: '-created_at', cursor: '' })
  expect(params?.get('sort')).toBe('-created_at')
  // ?limit=0 is a 400 rather than a clamp (internal/api/pagination.go:244), so the
  // page size is always stated explicitly.
  expect(params?.get('limit')).toBe('50')
  expect(params?.has('cursor')).toBe(false)
  expect(page.items[0].hostname_hint).toBe('farm-west-13')
  expect(page.next_cursor).toBe('c2')
  expect(page.total).toBe(7)
})

test('listAgentEnrollments sends the cursor and each of the four sort values', async () => {
  const seen: string[] = []
  server.use(
    http.get('/v1/agent-enrollments', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(`${p.get('sort')}|${p.get('cursor')}`)
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  for (const sort of ['created_at', '-created_at', 'expires_at', '-expires_at'] as const) {
    await listAgentEnrollments({ sort, cursor: 'cur1' })
  }
  expect(seen).toEqual([
    'created_at|cur1',
    '-created_at|cur1',
    'expires_at|cur1',
    '-expires_at|cur1',
  ])
})

test('a row with no hostname_hint keeps the key ABSENT, not null', async () => {
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({
        items: [{ id: 'e2', created_at: ROW.created_at, expires_at: ROW.expires_at, created_by: ROW.created_by }],
        next_cursor: '',
        total: 1,
      }),
    ),
  )
  const page = await listAgentEnrollments({ sort: '-created_at', cursor: '' })
  // enrollmentRowToMap omits the key entirely when the column is NULL
  // (internal/api/agent_enrollments.go:90-92), so the type is `?: string`, never
  // `string | null`, and consumers must handle undefined - not null.
  expect('hostname_hint' in page.items[0]).toBe(false)
  expect(page.items[0].hostname_hint).toBeUndefined()
})

test('createAgentEnrollment ALWAYS sends a JSON body, and the 201 parses', async () => {
  let body: unknown
  let contentType: string | null = null
  server.use(
    http.post('/v1/agent-enrollments', async ({ request }) => {
      // This handler mirrors readJSON (internal/api/server.go:199-211): an absent
      // or unparseable body is a 400 "invalid request body". That is what makes
      // this test non-vacuous - if the client ever stops sending a body, the
      // request fails here exactly as it would against the real server.
      const raw = await request.text()
      if (raw === '') return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      try {
        body = JSON.parse(raw)
      } catch {
        return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      }
      contentType = request.headers.get('content-type')
      return HttpResponse.json(
        { id: 'e9', token: 'f00dcafe'.repeat(8), expires_at: '2026-08-10T00:00:00Z' },
        { status: 201 },
      )
    }),
  )
  const created = await createAgentEnrollment({ ttl_seconds: DEFAULT_TTL_SECONDS })
  // The exact preset literal, never 0 and never absent: relying on the server's
  // zero-means-default branch (internal/api/agent_enrollments.go:32-34) would hide
  // the real TTL from the request log and from this assertion.
  expect(body).toEqual({ ttl_seconds: 86400 })
  expect(contentType).toContain('application/json')
  expect(created).toEqual({ id: 'e9', token: 'f00dcafe'.repeat(8), expires_at: '2026-08-10T00:00:00Z' })
})

test('createAgentEnrollment includes hostname_hint only when it is provided', async () => {
  let body: unknown
  server.use(
    http.post('/v1/agent-enrollments', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'e9', token: 'tok', expires_at: ROW.expires_at }, { status: 201 })
    }),
  )
  await createAgentEnrollment({ hostname_hint: 'farm-west-13', ttl_seconds: 3600 })
  expect(body).toEqual({ hostname_hint: 'farm-west-13', ttl_seconds: 3600 })
})

test('a 400 surfaces as an ApiError carrying the status and the server message', async () => {
  server.use(
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ error: 'ttl_seconds must be at least 60' }, { status: 400 }),
    ),
  )
  await expect(createAgentEnrollment({ ttl_seconds: 30 })).rejects.toMatchObject({
    status: 400,
    code: 'ttl_seconds must be at least 60',
  })
})

// This is the client-side validation of the server's TTL bounds. Because the UI
// offers presets only, there is no runtime input to validate - so the bounds are
// enforced structurally here instead: a future preset (or a free-form field that
// reuses these constants) cannot silently exceed what the server accepts.
test('every TTL preset is within the server bounds and the default is one of them', () => {
  expect(MIN_TTL_SECONDS).toBe(60)
  expect(MAX_TTL_SECONDS).toBe(604800)
  expect(DEFAULT_TTL_SECONDS).toBe(86400)
  expect(TTL_PRESETS.map((p) => p.label)).toEqual(['1h', '24h', '3d', '7d'])
  expect(TTL_PRESETS.map((p) => p.seconds)).toEqual([3600, 86400, 259200, 604800])
  for (const p of TTL_PRESETS) {
    expect(p.seconds).toBeGreaterThanOrEqual(MIN_TTL_SECONDS)
    expect(p.seconds).toBeLessThanOrEqual(MAX_TTL_SECONDS)
  }
  expect(TTL_PRESETS.some((p) => p.seconds === DEFAULT_TTL_SECONDS)).toBe(true)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/enrollments/api.test.ts`
Expected: FAIL - `Failed to resolve import "./api"`.

- [ ] **Step 3: Implement the clients and types**

Create `web/src/admin/enrollments/api.ts`:

```ts
import { apiFetch } from '../../lib/api'

// Mirrors enrollmentRowToMap (internal/api/agent_enrollments.go:83-94) and its
// three sort-variant twins (:100-149), which are field-for-field identical.
//
// hostname_hint is OPTIONAL, not nullable: the Go map omits the key entirely when
// the column is NULL (:90-92). Consumers must handle `undefined`, never `null`.
// created_by is a bare user UUID - there is no join to `users`, which is why no
// CREATED BY column is rendered.
// created_at / expires_at are Go time.Time values, i.e. RFC3339 with nanosecond
// precision. Parse with new Date(); never string-compare them.
export interface AgentEnrollment {
  id: string
  created_at: string
  expires_at: string
  created_by: string
  hostname_hint?: string
}

// internal/api/pagination.go:289-293.
export interface AgentEnrollmentsPage {
  items: AgentEnrollment[]
  next_cursor: string
  total: number
}

// AgentEnrollmentsSortSpec (internal/api/agent_enrollments.go:75-81): two keys,
// each with an optional '-' prefix, default '-created_at'. All four arms are
// implemented (:162-217).
export type EnrollmentSortField = 'created_at' | 'expires_at'
export type EnrollmentSort = 'created_at' | '-created_at' | 'expires_at' | '-expires_at'

// internal/api/agent_enrollments.go:16-20. NOTE: the 60s floor is enforced only
// for a NONZERO ttl_seconds (:31-38) - 0 or an absent key means the 24h default,
// and a negative value 400s. This UI always sends an explicit preset, so the
// floor and the ceiling are both unreachable from the UI; the constants exist so
// TTL_PRESETS can be checked against them (api.test.ts).
export const MIN_TTL_SECONDS = 60
export const MAX_TTL_SECONDS = 604800
export const DEFAULT_TTL_SECONDS = 86400

export interface TtlPreset {
  label: string
  seconds: number
}

// The hi-fi's four presets (hifi3-holo-pages.jsx:2375), 24h preselected to match
// both the server default and the hi-fi. Raw seconds are never shown to the
// admin: "604800" is hostile, "7d" is not.
export const TTL_PRESETS: TtlPreset[] = [
  { label: '1h', seconds: 3600 },
  { label: '24h', seconds: DEFAULT_TTL_SECONDS },
  { label: '3d', seconds: 259200 },
  { label: '7d', seconds: MAX_TTL_SECONDS },
]

export interface ListEnrollmentsParams {
  sort: EnrollmentSort
  cursor: string
}

export function listAgentEnrollments({
  sort,
  cursor,
}: ListEnrollmentsParams): Promise<AgentEnrollmentsPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (cursor) q.set('cursor', cursor)
  return apiFetch<AgentEnrollmentsPage>(`/agent-enrollments?${q}`)
}

// hostname_hint is omitted when blank rather than sent as "": the server treats
// the two identically (internal/api/agent_enrollments.go:58-60), and omitting
// keeps the request body honest about what the admin actually supplied.
export interface CreateEnrollmentBody {
  hostname_hint?: string
  ttl_seconds: number
}

// The 201 body, internal/api/agent_enrollments.go:68-72. There is no created_at
// and no hostname_hint echo, which is why no optimistic row is appended anywhere.
//
// SECURITY: `token` is the raw 64-char hex enrollment credential. Only
// tokenhash.Hash(rawHex) is persisted (:51) and the list endpoint returns no token
// field, so it is UNRECOVERABLE after this response. Never log it, never put it in
// a URL or a query key, and never copy it into component state - it is rendered
// straight from the mutation's data by web/src/admin/TokenRevealDialog.tsx so that
// create.reset() is the single point that destroys it.
export interface CreateEnrollmentResponse {
  id: string
  token: string
  expires_at: string
}

// A body is ALWAYS sent, even when the hint is blank: the handler calls readJSON
// unconditionally (internal/api/agent_enrollments.go:27 -> server.go:199-211), so
// a POST with no body decodes as io.EOF and 400s "invalid request body".
export function createAgentEnrollment(
  body: CreateEnrollmentBody,
): Promise<CreateEnrollmentResponse> {
  return apiFetch<CreateEnrollmentResponse>('/agent-enrollments', { method: 'POST', json: body })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/enrollments/api.test.ts`
Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/enrollments/api.ts web/src/admin/enrollments/api.test.ts
git commit -m "feat(web): agent enrollment API clients, types, and TTL presets"
```

---

## Task 3: useAgentEnrollments list query

Mirror `web/src/admin/users/useAdminUsers.ts:9-20` exactly, with a two-element key tail instead of four.

**Files:**
- Create: `web/src/admin/enrollments/useAgentEnrollments.ts`
- Test: `web/src/admin/enrollments/useAgentEnrollments.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/enrollments/useAgentEnrollments.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useAgentEnrollments } from './useAgentEnrollments'
import type { AgentEnrollmentsPage } from './api'

const ROW = {
  id: 'e1',
  created_at: '2026-08-09T00:00:00Z',
  expires_at: '2026-08-10T00:00:00Z',
  created_by: 'u1',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["agent-enrollments", sort, cursor] and passes both through', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/agent-enrollments', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useAgentEnrollments('expires_at', 'cur1'), {
    wrapper: makeWrapper(client),
  })

  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(params?.get('sort')).toBe('expires_at')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('limit')).toBe('50')

  const cached = client.getQueryData<AgentEnrollmentsPage>(['agent-enrollments', 'expires_at', 'cur1'])
  expect(cached?.items[0].id).toBe('e1')
})

test('does not poll - enrollments are not live data', async () => {
  let calls = 0
  server.use(
    http.get('/v1/agent-enrollments', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useAgentEnrollments('-created_at', ''), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // Long enough that a copy-pasted refetchInterval (the live list hooks use
  // 3000ms, but 150ms catches any small value too) would have fired.
  await new Promise((r) => setTimeout(r, 150))
  expect(calls).toBe(1)

  // Positive control on the SAME counter: the instrument can move, so the
  // assertion above is about polling and not about a dead counter. The 2026-08-08
  // review caught a vacuous version of exactly this test on the Users tab.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/enrollments/useAgentEnrollments.test.tsx`
Expected: FAIL - `Failed to resolve import "./useAgentEnrollments"`.

- [ ] **Step 3: Implement the hook**

Create `web/src/admin/enrollments/useAgentEnrollments.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listAgentEnrollments, type AgentEnrollmentsPage, type EnrollmentSort } from './api'

// The list query for the admin Agent-enrollments tab. Same shape as
// useAdminUsers (web/src/admin/users/useAdminUsers.ts:9-20), including the
// deliberate absence of refetchInterval: this is not live data, so polling it is
// pointless load. Freshness of the EXPIRING/EXPIRED pill comes from useNow, a
// local 60s clock tick that issues no request; freshness of the ROW SET comes
// from useAgentEnrollmentActions invalidating the bare ['agent-enrollments']
// prefix.
//
// keepPreviousData keeps rows visible while a new sort/page loads, which is also
// what makes isPlaceholderData usable to disable the pager mid-fetch.
export function useAgentEnrollments(sort: EnrollmentSort, cursor: string) {
  return useQuery<AgentEnrollmentsPage>({
    queryKey: ['agent-enrollments', sort, cursor],
    queryFn: () => listAgentEnrollments({ sort, cursor }),
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/enrollments/useAgentEnrollments.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/enrollments/useAgentEnrollments.ts web/src/admin/enrollments/useAgentEnrollments.test.tsx
git commit -m "feat(web): useAgentEnrollments list query hook"
```

---

## Task 4: useAgentEnrollmentActions create mutation

Mirror `web/src/admin/users/useAdminUserActions.ts:20-51`, reduced to one mutation. Step 5 is a **mandatory** non-vacuity check - do not skip it.

**Files:**
- Create: `web/src/admin/enrollments/useAgentEnrollmentActions.ts`
- Test: `web/src/admin/enrollments/useAgentEnrollmentActions.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/enrollments/useAgentEnrollmentActions.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useAgentEnrollmentActions } from './useAgentEnrollmentActions'
import { useAgentEnrollments } from './useAgentEnrollments'

const TOKEN = 'f00dcafe'.repeat(8)

const ROW = {
  id: 'e1',
  created_at: '2026-08-09T00:00:00Z',
  expires_at: '2026-08-10T00:00:00Z',
  created_by: 'u1',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('create POSTs the exact body and invalidates the BARE ["agent-enrollments"] prefix', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/agent-enrollments', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: ROW.expires_at }, { status: 201 })
    }),
  )
  const { result } = renderHook(() => useAgentEnrollmentActions(), { wrapper: makeWrapper(client) })
  const created = await result.current.create.mutateAsync({
    hostname_hint: 'farm-west-13',
    ttl_seconds: 86400,
  })

  expect(body).toEqual({ hostname_hint: 'farm-west-13', ttl_seconds: 86400 })
  expect(created.token).toBe(TOKEN)
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['agent-enrollments'] }))

  // The decoupling lesson from web/src/jobs/queryKeyDecoupling.test.tsx: a
  // fully-qualified key only refetches the sort/page combination that happens to
  // be mounted. EVERY call must use the bare prefix.
  for (const call of spy.mock.calls) {
    expect((call[0] as { queryKey: unknown[] }).queryKey).toEqual(['agent-enrollments'])
  }
})

test('creating refetches a MOUNTED enrollments list (active observer, not a cache seed)', async () => {
  let listCalls = 0
  server.use(
    http.get('/v1/agent-enrollments', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: ROW.expires_at }, { status: 201 }),
    ),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)

  // The list query MUST be mounted via renderHook so it has an ACTIVE OBSERVER.
  // A client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter what key the mutation invalidated.
  const { result: list } = renderHook(() => useAgentEnrollments('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useAgentEnrollmentActions(), { wrapper })
  await actions.current.create.mutateAsync({ ttl_seconds: 86400 })

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})

test('a create failure surfaces the ApiError and does not invalidate', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ error: 'failed to create enrollment' }, { status: 500 }),
    ),
  )
  const { result } = renderHook(() => useAgentEnrollmentActions(), { wrapper: makeWrapper(client) })
  await expect(result.current.create.mutateAsync({ ttl_seconds: 86400 })).rejects.toMatchObject({
    status: 500,
  })
  expect(spy).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/enrollments/useAgentEnrollmentActions.test.tsx`
Expected: FAIL - `Failed to resolve import "./useAgentEnrollmentActions"`.

- [ ] **Step 3: Implement the hook**

Create `web/src/admin/enrollments/useAgentEnrollmentActions.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createAgentEnrollment, type CreateEnrollmentBody } from './api'

// Mutations for the admin Agent-enrollments tab. Plural name for a single
// mutation on purpose, matching useAdminUserActions' convention, so the
// revocation follow-up
// (docs/backlog/feature-2026-06-26-agent-enrollment-revocation.md) is an addition
// here rather than a rename of the module.
//
// SECURITY - read before editing:
//  - create.data holds the RAW enrollment token, and TanStack retains a
//    mutation's data AND variables for the mutation's lifetime. So create.reset()
//    is load-bearing, not tidiness: EnrollmentsTab calls it when the reveal dialog
//    closes and before the create panel is reopened.
//  - No onSuccess logging, ever. The success payload is a credential.
//  - No optimistic append: the 201 echoes neither created_at nor hostname_hint
//    (internal/api/agent_enrollments.go:68-72), so a locally synthesised row would
//    be partly invented.
export function useAgentEnrollmentActions() {
  const qc = useQueryClient()

  const create = useMutation({
    mutationFn: (body: CreateEnrollmentBody) => createAgentEnrollment(body),
    // BARE prefix, never a fully-qualified key, so every mounted
    // ['agent-enrollments', sort, cursor] combination refetches (see
    // web/src/jobs/queryKeyDecoupling.test.tsx).
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agent-enrollments'] }),
  })

  return { create }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/enrollments/useAgentEnrollmentActions.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Prove the invalidation test is not vacuous (mandatory)**

Temporarily break the key in `web/src/admin/enrollments/useAgentEnrollmentActions.ts`:

```ts
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agent-enrollments-broken'] }),
```

Run: `npx vitest run src/admin/enrollments/useAgentEnrollmentActions.test.tsx`
Expected: FAIL on **both** the bare-prefix assertion and the "refetches a MOUNTED enrollments list" test (`listCalls` stuck at 1). If the mounted-list test still passes, the observer is not active and the test is worthless - fix the test before continuing.

Revert the line to `['agent-enrollments']` and re-run.
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/admin/enrollments/useAgentEnrollmentActions.ts web/src/admin/enrollments/useAgentEnrollmentActions.test.tsx
git commit -m "feat(web): useAgentEnrollmentActions create with bare-prefix invalidation"
```

---

## Task 5: TokenRevealDialog (shared, highest-consequence component)

This is the new risk in the slice. Everything else is a clone of a shipped file; this is not. A bug that drops the token or dismisses early is **unrecoverable for the admin** - there is no second chance to read it.

**Files:**
- Create: `web/src/admin/TokenRevealDialog.tsx`
- Test: `web/src/admin/TokenRevealDialog.test.tsx`

Design constraints, all settled by the spec:
- Rendered from the `token` **prop** only, sourced from `create.data.token`. The component keeps no state containing it (`copied` is a boolean).
- Modal, acknowledge-to-dismiss. **Backdrop click does NOT close** - implemented by simply not attaching an `onClick` to the overlay, so there is no handler to accidentally reintroduce. Escape **does** close, preserving the inherited baseline; the tradeoff (an admin can lose the token with a deliberate keypress) is accepted rather than breaking the baseline for every dialog in the app.
- a11y baseline copied from `ConfirmDialog.tsx:36-46`. **No focus trap**, same as `ConfirmDialog` and `ResetPasswordDialog`. This is the **third** un-trapped consumer and the worst one, because the credential itself can be tabbed past - schedule `docs/backlog/idea-2026-07-01-confirmdialog-focus-trap-hardening.md` before the Invites tab makes it four. Do not fix it here; a focus trap is a shared-primitive change affecting three call sites and belongs in its own reviewable slice.
- Clipboard: feature-detected, guarded, no `document.execCommand` fallback, no clear-on-close.

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/TokenRevealDialog.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'
import { TokenRevealDialog } from './TokenRevealDialog'

const TOKEN = 'f00dcafe'.repeat(8) // 64 hex chars, like the real token

// jsdom implements no Clipboard API, so navigator.clipboard is undefined by
// default - which is also the real shape on http://host:8080, where the API is
// withheld outside a secure context. Tests that need the API install it.
let restoreClipboard: (() => void) | null = null

function installClipboard(writeText: (t: string) => Promise<void>) {
  const original = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  restoreClipboard = () => {
    if (original) Object.defineProperty(navigator, 'clipboard', original)
    else delete (navigator as { clipboard?: unknown }).clipboard
    restoreClipboard = null
  }
}

afterEach(() => restoreClipboard?.())

function renderDialog(over: Partial<Parameters<typeof TokenRevealDialog>[0]> = {}) {
  const props = {
    token: TOKEN,
    title: 'Agent enrollment created',
    endpoint: 'POST /v1/agent-enrollments',
    onDone: vi.fn(),
    ...over,
  }
  return { props, ...render(<TokenRevealDialog {...props} />) }
}

test('shows the endpoint, title, the one-time warning, and the token', () => {
  renderDialog()
  expect(screen.getByText('POST /v1/agent-enrollments')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 2, name: 'Agent enrollment created' })).toBeInTheDocument()
  expect(screen.getByText(/shown once/i)).toBeInTheDocument()
  expect(screen.getByText(/cannot be retrieved again/i)).toBeInTheDocument()
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)
})

test('matches the inherited dialog a11y baseline', () => {
  renderDialog()
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toHaveAccessibleName('Agent enrollment created')
})

test('the token field is readonly, focused, and pre-selected', () => {
  renderDialog()
  const input = screen.getByLabelText('Token') as HTMLInputElement
  expect(input).toHaveAttribute('readonly')
  // NOT type="password": the entire purpose of this dialog is to display it.
  expect(input.type).toBe('text')
  expect(input).toHaveFocus()
  expect(input.selectionStart).toBe(0)
  expect(input.selectionEnd).toBe(TOKEN.length)
})

test('a backdrop click does NOT dismiss it, but Escape does (paired positive control)', async () => {
  const { props } = renderDialog()
  const backdrop = screen.getByRole('dialog').parentElement as HTMLElement

  await userEvent.click(backdrop)
  // A stray click must never destroy the only copy of the credential.
  expect(props.onDone).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)

  // Positive control: something CAN close it, so the assertion above is about the
  // backdrop and not about a dialog that is impossible to dismiss.
  await userEvent.keyboard('{Escape}')
  expect(props.onDone).toHaveBeenCalledTimes(1)
})

test('Done calls onDone exactly once', async () => {
  const { props } = renderDialog()
  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  expect(props.onDone).toHaveBeenCalledTimes(1)
})

test('Copy writes exactly the token and flips the label', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  installClipboard(writeText)
  renderDialog()

  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(writeText).toHaveBeenCalledTimes(1)
  expect(writeText).toHaveBeenCalledWith(TOKEN)
  expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument()
})

test('with no clipboard API the Copy button is ABSENT and a manual hint replaces it', () => {
  // Default jsdom state: no navigator.clipboard, exactly like plain-HTTP relay.
  renderDialog()
  expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument()
  expect(screen.getByText(/needs HTTPS/i)).toBeInTheDocument()
  // The token is still selected, so the insecure path still works.
  expect(screen.getByLabelText('Token')).toHaveFocus()
})

test('positive control: the Copy button IS present when the API exists', () => {
  installClipboard(vi.fn().mockResolvedValue(undefined))
  renderDialog()
  // Without this, the absence assertion above could pass on a typo'd query.
  expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument()
  expect(screen.queryByText(/needs HTTPS/i)).not.toBeInTheDocument()
})

test('a rejected clipboard write falls back to the manual hint and logs nothing', async () => {
  const spies = (['log', 'info', 'warn', 'error', 'debug', 'trace'] as const).map((m) =>
    vi.spyOn(console, m).mockImplementation(() => {}),
  )
  installClipboard(vi.fn().mockRejectedValue(new Error(`clipboard denied for ${TOKEN}`)))
  renderDialog()

  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(await screen.findByText(/needs HTTPS/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument()
  // The rejection is swallowed, not logged: a caught error can carry the argument
  // that caused it, and console output is captured by extensions and screen-shared.
  for (const s of spies) expect(s).not.toHaveBeenCalled()
  spies.forEach((s) => s.mockRestore())
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/TokenRevealDialog.test.tsx`
Expected: FAIL - `Failed to resolve import "./TokenRevealDialog"`.

- [ ] **Step 3: Implement the dialog**

Create `web/src/admin/TokenRevealDialog.tsx`:

```tsx
import { useEffect, useId, useRef, useState } from 'react'
import { PillButton } from '../components/holo'

interface TokenRevealDialogProps {
  // The raw credential. MUST be passed straight from the mutation's data
  // (create.data.token) and never copied into caller state, so there is exactly
  // one retention site and the caller's reset() destroys it.
  token: string
  title: string
  // The endpoint that minted it, e.g. "POST /v1/agent-enrollments". Display only.
  endpoint: string
  warning?: string
  // Called on Done AND on Escape. The caller MUST reset() the mutation here: that
  // is what actually drops the token. Unmounting this component alone does not,
  // because TanStack retains a mutation's data and variables until reset.
  onDone: () => void
}

const DEFAULT_WARNING =
  'This token is shown once. It cannot be retrieved again - copy it now, or create a replacement.'

// Shared reveal surface for a one-time credential: agent enrollments today,
// invites later. It replaces the hi-fi's success toast, which does not exist and
// would be the wrong primitive anyway - auto-dismissal turns a glance away from
// the screen into permanent data loss.
//
// SECURITY INVARIANTS, structural rather than incidental:
//  1. The token is rendered from the `token` prop and nowhere else. This component
//     holds NO state containing it (`copied` and `canCopy` are booleans).
//  2. Nothing here calls console.*. The clipboard catch swallows its rejection
//     rather than logging an error that could carry the argument.
//  3. The token never enters a URL, a route, a query param, or a query key. This
//     dialog is not linkable or bookmarkable by construction, so the credential
//     cannot leak into history, a Referer header, or a proxy log.
//  4. Backdrop click does NOT dismiss - there is deliberately no onClick on the
//     overlay (the hi-fi's AdminTokenModal has one at
//     design_handoff_relay_holo/hifi3-holo-pages.jsx:2345, which is fine for a
//     form and catastrophic for a secret). Escape DOES dismiss, preserving the
//     baseline of the two shipped dialogs.
//  5. a11y baseline copied from web/src/components/ConfirmDialog.tsx:36-46:
//     role="dialog", aria-modal, aria-labelledby the title, first field focused.
//     NO focus trap, same as ConfirmDialog and ResetPasswordDialog. This is the
//     THIRD un-trapped consumer and the worst one - the credential can be tabbed
//     past - so docs/backlog/idea-2026-07-01-confirmdialog-focus-trap-hardening.md
//     should land before a fourth.
export function TokenRevealDialog({
  token,
  title,
  endpoint,
  warning,
  onDone,
}: TokenRevealDialogProps) {
  const titleId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const [copied, setCopied] = useState(false)
  // navigator.clipboard is undefined outside a secure context, and relay-server
  // serves plain HTTP on :8080 by default, so on a LAN-hosted http://host:8080
  // there is no clipboard API at all. Feature-detected rather than assumed:
  // rendering a Copy button that can only fail is worse than not offering one.
  // document.execCommand('copy') is not used as a fallback - deprecated, and it
  // buys nothing over the already-selected input.
  const [canCopy, setCanCopy] = useState(
    () => typeof navigator.clipboard?.writeText === 'function',
  )

  useEffect(() => {
    // Focus + select the token: satisfies "first field focused" and gives keyboard
    // users select-all for free, which is the manual copy path when the clipboard
    // API is unavailable.
    inputRef.current?.focus()
    inputRef.current?.select()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onDone()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onDone])

  // The 2s "Copied" timer must be cleared on unmount: Done unmounts this dialog,
  // and a pending setState on an unmounted component warns through console.error -
  // which the secrecy suite spies on.
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(t)
  }, [copied])

  async function copy() {
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
    } catch {
      // A denied permission is not worth logging, and logging the rejection risks
      // logging the argument that caused it. Fall back to the manual hint so the
      // admin is never left with a silently dead button.
      setCanCopy(false)
    }
  }

  return (
    // No onClick here. See invariant 4.
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="w-full max-w-lg rounded-card border border-border bg-bg p-5 shadow-xl"
      >
        <div className="font-mono text-[10px] tracking-[0.18em] text-fg-mute">{endpoint}</div>
        <h2 id={titleId} className="mt-1 text-[17px] font-medium text-fg">
          {title}
        </h2>

        <div className="mt-4 rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[10.5px] leading-relaxed tracking-[0.04em] text-warn">
          ⚠ {warning ?? DEFAULT_WARNING}
        </div>

        <label
          htmlFor="reveal-token"
          className="mb-1 mt-4 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute"
        >
          Token
        </label>
        <input
          id="reveal-token"
          ref={inputRef}
          type="text"
          readOnly
          value={token}
          spellCheck={false}
          autoComplete="off"
          onFocus={(e) => e.currentTarget.select()}
          className="w-full rounded-[8px] border border-border bg-black/40 px-3 py-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
        />

        {canCopy ? (
          <div className="mt-2">
            <PillButton onClick={copy}>{copied ? 'Copied' : 'Copy'}</PillButton>
          </div>
        ) : (
          <div className="mt-2 text-[11px] text-fg-dim">
            Clipboard access needs HTTPS, so select the field above and copy it manually. The text
            is already selected.
          </div>
        )}

        <div className="mt-5 flex justify-end">
          <PillButton variant="primary" onClick={onDone}>
            Done - I have copied it
          </PillButton>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/TokenRevealDialog.test.tsx`
Expected: PASS (9 tests).

If `toHaveAccessibleName` fails, the `aria-labelledby` is not resolving to the `<h2 id>` - fix the wiring, do not weaken the assertion to `getByText`.

- [ ] **Step 5: Prove the no-backdrop-close test is not vacuous**

Temporarily add `onClick={onDone}` to the overlay `<div className="fixed inset-0 ...">`.

Run: `npx vitest run src/admin/TokenRevealDialog.test.tsx`
Expected: FAIL on "a backdrop click does NOT dismiss it" (`onDone` called once, token gone). If it still passes, the test is clicking the wrong element - fix the test's `backdrop` lookup before continuing.

Remove the `onClick` again and re-run: PASS (9 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/admin/TokenRevealDialog.tsx web/src/admin/TokenRevealDialog.test.tsx
git commit -m "feat(web): shared TokenRevealDialog for one-time credentials"
```

---

## Task 6: EnrollmentsTable

Presentational only - no queries, no mutations, `now` arrives as a prop so tests are deterministic. Mirror `web/src/admin/users/UsersTable.tsx:19-27` for the caret / `aria-sort` helpers and `:71-100` for the role-based table markup.

**Files:**
- Create: `web/src/admin/enrollments/EnrollmentsTable.tsx`
- Test: `web/src/admin/enrollments/EnrollmentsTable.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/enrollments/EnrollmentsTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { EnrollmentsTable } from './EnrollmentsTable'
import type { AgentEnrollment, EnrollmentSort } from './api'

const NOW = new Date('2026-08-09T12:00:00Z')

function row(over: Partial<AgentEnrollment> = {}): AgentEnrollment {
  return {
    id: 'e1',
    created_at: '2026-08-09T09:30:00Z',
    expires_at: '2026-08-10T09:42:00Z',
    created_by: '11111111-2222-3333-4444-555555555555',
    hostname_hint: 'farm-west-13',
    ...over,
  }
}

function renderTable(over: Partial<Parameters<typeof EnrollmentsTable>[0]> = {}) {
  const props = {
    enrollments: [row()],
    sort: '-created_at' as EnrollmentSort,
    onSort: vi.fn(),
    now: NOW,
    ...over,
  }
  return { props, ...render(<EnrollmentsTable {...props} />) }
}

test('renders hostname hint, created date, relative expiry, and the note', () => {
  renderTable()
  expect(screen.getByText('farm-west-13')).toBeInTheDocument()
  expect(screen.getByText('2026-08-09')).toBeInTheDocument()
  expect(screen.getByText('in 21h')).toBeInTheDocument()
  expect(screen.getByText(/consumed on first agent connect/i)).toBeInTheDocument()
})

test('a row whose hostname_hint key is ABSENT renders a plain hyphen, not undefined', () => {
  const { created_at, expires_at, created_by, id } = row()
  renderTable({ enrollments: [{ id, created_at, expires_at, created_by }] })
  expect(screen.getByText('-')).toBeInTheDocument()
  expect(screen.queryByText(/undefined/)).not.toBeInTheDocument()
  expect(screen.queryByText(/null/)).not.toBeInTheDocument()
  // House rule: an ASCII hyphen, never the em dash the hi-fi uses.
  expect(screen.queryByText('—')).not.toBeInTheDocument()
})

test('the status pill renders exactly the three derivable states', () => {
  renderTable({
    enrollments: [
      row({ id: 'a', expires_at: '2026-08-10T12:00:00Z' }), // 24h left
      row({ id: 'b', expires_at: '2026-08-09T12:30:00Z' }), // 30m left
      row({ id: 'c', expires_at: '2026-08-09T11:00:00Z' }), // past
    ],
  })
  expect(screen.getByText('ACTIVE')).toBeInTheDocument()
  expect(screen.getByText('EXPIRING')).toBeInTheDocument()
  expect(screen.getByText('EXPIRED')).toBeInTheDocument()
  // Consumption is unobservable through this endpoint, so there is no such state.
  expect(screen.queryByText('CONSUMED')).not.toBeInTheDocument()
})

test('renders no TOKEN PREFIX and no CREATED BY column, and never the raw creator UUID', () => {
  renderTable()
  expect(screen.queryByText('TOKEN PREFIX')).not.toBeInTheDocument()
  expect(screen.queryByText('CREATED BY')).not.toBeInTheDocument()
  expect(screen.queryByText('11111111-2222-3333-4444-555555555555')).not.toBeInTheDocument()
})

test('the last column is headed NOTE, and no row carries any control', () => {
  renderTable({ enrollments: [row(), row({ id: 'e2' })] })
  expect(screen.getByText('NOTE')).toBeInTheDocument()
  expect(screen.queryByText('ACTIONS')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /revoke|delete|remove/i })).not.toBeInTheDocument()
  // The only buttons in the table are the two sortable headers - two rows of data
  // add none. There is no DELETE /v1/agent-enrollments/{id} to serve one.
  expect(screen.getAllByRole('button')).toHaveLength(2)
})

test('clicking a sortable header calls onSort with that field', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: /^CREATED/ }))
  expect(props.onSort).toHaveBeenCalledWith('created_at')
  await userEvent.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  expect(props.onSort).toHaveBeenCalledWith('expires_at')
})

test('aria-sort marks the active column and caret direction follows the sort', () => {
  renderTable({ sort: '-created_at' })
  expect(screen.getByRole('columnheader', { name: /CREATED/ })).toHaveAttribute('aria-sort', 'descending')
  expect(screen.getByRole('columnheader', { name: /EXPIRES/ })).toHaveAttribute('aria-sort', 'none')
  expect(screen.getByRole('button', { name: 'CREATED ▼' })).toBeInTheDocument()
})

test('ascending sort shows an ascending caret', () => {
  renderTable({ sort: 'expires_at' })
  expect(screen.getByRole('columnheader', { name: /EXPIRES/ })).toHaveAttribute('aria-sort', 'ascending')
  expect(screen.getByRole('button', { name: 'EXPIRES ▲' })).toBeInTheDocument()
})

test('a different now re-derives the pill and the label from the same row', () => {
  const later = new Date('2026-08-10T09:41:30Z') // 30s before expiry
  renderTable({ now: later })
  expect(screen.getByText('EXPIRING')).toBeInTheDocument()
  expect(screen.getByText('in 30s')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/enrollments/EnrollmentsTable.test.tsx`
Expected: FAIL - `Failed to resolve import "./EnrollmentsTable"`.

- [ ] **Step 3: Implement the table**

Create `web/src/admin/enrollments/EnrollmentsTable.tsx`:

```tsx
import { Chip } from '../../components/holo'
import { formatTimeUntil } from '../../lib/time'
import { deriveStatus, statusTone } from './enrollmentStatus'
import type { AgentEnrollment, EnrollmentSort, EnrollmentSortField } from './api'

// HOSTNAME HINT | CREATED | EXPIRES | STATUS | NOTE.
//
// Two hi-fi columns are omitted (hifi3-holo-pages.jsx:2164):
//  - TOKEN PREFIX: only tokenhash.Hash(rawHex) is stored, no prefix column exists
//    and nothing returns one.
//  - CREATED BY: created_by is a bare user UUID with no join to `users`, so the
//    cell could only show 36 opaque characters.
// The hi-fi's ACTIONS header is renamed NOTE: the cell holds prose, and a header
// promising actions while delivering a sentence is itself a dead affordance. There
// is no DELETE /v1/agent-enrollments/{id} in v1.
// CREATED is added because it is the default sort key and needs a clickable header.
const COLS = 'grid grid-cols-[1.6fr_130px_130px_120px_1fr]'

function caret(field: EnrollmentSortField, sort: EnrollmentSort): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

function ariaSort(
  field: EnrollmentSortField,
  sort: EnrollmentSort,
): 'ascending' | 'descending' | 'none' {
  if (sort.replace('-', '') !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
}

interface EnrollmentsTableProps {
  enrollments: AgentEnrollment[]
  sort: EnrollmentSort
  onSort: (field: EnrollmentSortField) => void
  // Injected so the pill and the relative label are pure functions of props. The
  // tab supplies useNow(60_000); tests supply a fixed Date.
  now: Date
}

export function EnrollmentsTable({ enrollments, sort, onSort, now }: EnrollmentsTableProps) {
  return (
    <div
      role="table"
      aria-label="Agent enrollments"
      className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]"
    >
      <div
        role="row"
        className={`${COLS} border-b border-border px-[18px] py-3 font-mono text-[10px] tracking-[0.16em] text-fg-mute`}
      >
        <span role="columnheader">HOSTNAME HINT</span>
        <div role="columnheader" aria-sort={ariaSort('created_at', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('created_at')}>
            CREATED{caret('created_at', sort)}
          </button>
        </div>
        <div role="columnheader" aria-sort={ariaSort('expires_at', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('expires_at')}>
            EXPIRES{caret('expires_at', sort)}
          </button>
        </div>
        <span role="columnheader">STATUS</span>
        <span role="columnheader" className="text-right">
          NOTE
        </span>
      </div>

      {enrollments.map((e) => {
        const status = deriveStatus(e.expires_at, now)
        return (
          <div
            key={e.id}
            role="row"
            className={`${COLS} items-center border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
              status === 'EXPIRED' ? 'opacity-[0.55]' : ''
            }`}
          >
            {/* The key is ABSENT (not null) when unset
                (internal/api/agent_enrollments.go:90-92), so this is a plain
                ASCII hyphen placeholder - never an em dash. */}
            <span role="cell" className="truncate font-sans text-[12.5px] text-fg">
              {e.hostname_hint ?? <span className="text-fg-dim">-</span>}
            </span>
            <span role="cell" className="text-[10.5px] text-fg-mute">
              {e.created_at.slice(0, 10)}
            </span>
            <span
              role="cell"
              className={`text-[11px] ${status === 'ACTIVE' ? 'text-fg' : 'text-fg-mute'}`}
            >
              {formatTimeUntil(e.expires_at, now)}
            </span>
            <span role="cell">
              <Chip tone={statusTone(status)}>{status}</Chip>
            </span>
            <span role="cell" className="text-right text-[10.5px] tracking-[0.04em] text-fg-dim">
              consumed on first agent connect
            </span>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/enrollments/EnrollmentsTable.test.tsx`
Expected: PASS (9 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/enrollments/EnrollmentsTable.tsx web/src/admin/enrollments/EnrollmentsTable.test.tsx
git commit -m "feat(web): EnrollmentsTable with derived status and sortable headers"
```

---

## Task 7: CreateEnrollmentForm

Inline panel, not a modal - mirror `web/src/admin/users/CreateUserForm.tsx:18-98`. Keeping it tab-local (rather than folding it into the shared dialog) is deliberate: invites will diverge on the field, the presets, and the endpoint, and the hi-fi's `isInvite` boolean (`hifi3-holo-pages.jsx:2341`) is exactly the flag-driven component that rots.

**Files:**
- Create: `web/src/admin/enrollments/CreateEnrollmentForm.tsx`
- Test: `web/src/admin/enrollments/CreateEnrollmentForm.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/enrollments/CreateEnrollmentForm.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../lib/api'
import { CreateEnrollmentForm } from './CreateEnrollmentForm'

function renderForm(over: Partial<Parameters<typeof CreateEnrollmentForm>[0]> = {}) {
  const props = {
    pending: false,
    error: null as Error | null,
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  }
  return { props, ...render(<CreateEnrollmentForm {...props} />) }
}

test('submitting with a blank hint sends ONLY an explicit ttl_seconds', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
  // No hostname_hint key at all (not ''), and the 24h default as a literal - never
  // 0 and never omitted, so the request states its own TTL.
  expect(props.onSubmit).toHaveBeenCalledWith({ ttl_seconds: 86400 })
})

test('a hint is trimmed and a chosen preset is sent verbatim', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Hostname hint'), '  farm-west-13  ')
  await userEvent.click(screen.getByRole('button', { name: '1h' }))
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
  expect(props.onSubmit).toHaveBeenCalledWith({ hostname_hint: 'farm-west-13', ttl_seconds: 3600 })
})

test('exactly four TTL presets, with 24h preselected', async () => {
  renderForm()
  for (const label of ['1h', '24h', '3d', '7d']) {
    expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
  }
  expect(screen.getByRole('button', { name: '24h' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '7d' })).toHaveAttribute('aria-pressed', 'false')

  await userEvent.click(screen.getByRole('button', { name: '7d' }))
  expect(screen.getByRole('button', { name: '7d' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '24h' })).toHaveAttribute('aria-pressed', 'false')
})

test('every preset submits its exact server-legal literal', async () => {
  const cases: [string, number][] = [
    ['1h', 3600],
    ['24h', 86400],
    ['3d', 259200],
    ['7d', 604800],
  ]
  for (const [label, seconds] of cases) {
    const { props, unmount } = renderForm()
    await userEvent.click(screen.getByRole('button', { name: label }))
    await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
    expect(props.onSubmit).toHaveBeenCalledWith({ ttl_seconds: seconds })
    unmount()
  }
})

test('there is no free-form TTL field, so the 60s/7d bounds are unreachable from the UI', () => {
  renderForm()
  expect(screen.queryByLabelText(/ttl/i)).not.toBeInTheDocument()
  expect(screen.queryByLabelText(/seconds/i)).not.toBeInTheDocument()
  expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
})

test('states up front that the token is shown once', () => {
  renderForm()
  expect(screen.getByText(/returned once/i)).toBeInTheDocument()
  // The hi-fi's copy points at a success toast that does not exist
  // (hifi3-holo-pages.jsx:2390); this form points at the reveal dialog instead.
  expect(screen.queryByText(/toast/i)).not.toBeInTheDocument()
})

test('pending disables submit', () => {
  renderForm({ pending: true })
  expect(screen.getByRole('button', { name: 'Enroll' })).toBeDisabled()
})

test('a server error renders inside the panel and the form keeps its state', async () => {
  const { rerender } = renderForm()
  await userEvent.type(screen.getByLabelText('Hostname hint'), 'farm-west-13')
  rerender(
    <CreateEnrollmentForm
      pending={false}
      error={new ApiError(500, 'failed to create enrollment', '500 failed to create enrollment')}
      onSubmit={vi.fn()}
      onCancel={vi.fn()}
    />,
  )
  expect(screen.getByText('500 failed to create enrollment')).toBeInTheDocument()
  expect(screen.getByLabelText('Hostname hint')).toHaveValue('farm-west-13')
})

test('Cancel calls onCancel and does not submit', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onCancel).toHaveBeenCalledTimes(1)
  expect(props.onSubmit).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/enrollments/CreateEnrollmentForm.test.tsx`
Expected: FAIL - `Failed to resolve import "./CreateEnrollmentForm"`.

- [ ] **Step 3: Implement the form**

Create `web/src/admin/enrollments/CreateEnrollmentForm.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { GlassPanel, PillButton } from '../../components/holo'
import { Input } from '../../components/Input'
import { DEFAULT_TTL_SECONDS, TTL_PRESETS, type CreateEnrollmentBody } from './api'

interface CreateEnrollmentFormProps {
  pending: boolean
  error: Error | null
  onSubmit: (body: CreateEnrollmentBody) => void
  onCancel: () => void
}

const PRESET = 'flex-1 rounded-[6px] border px-2.5 py-1.5 font-mono text-[11px] tracking-[0.06em]'
const PRESET_ON = `${PRESET} border-accent/60 bg-accent/20 text-fg`
const PRESET_OFF = `${PRESET} border-border bg-white/[0.04] text-fg-mute`

// Inline create panel, mirroring CreateUserForm rather than the hi-fi's modal
// (hifi3-holo-pages.jsx:2340): it keeps exactly one un-trapped dialog on screen
// at a time - the reveal - and adds no modal machinery for two fields.
//
// Deliberately tab-local, not shared with the future Invites tab: invites take an
// email that BINDS the invite, different presets, and a different endpoint. The
// hi-fi models that with an `isInvite` boolean; keeping the halves separate is why
// only the reveal is shared.
export function CreateEnrollmentForm({
  pending,
  error,
  onSubmit,
  onCancel,
}: CreateEnrollmentFormProps) {
  const [hint, setHint] = useState('')
  const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)

  function submit(e: FormEvent) {
    e.preventDefault()
    const trimmed = hint.trim()
    // ttl is always one of TTL_PRESETS, every value of which is inside the
    // server's [60, 604800] window (asserted in api.test.ts), so there is no
    // client-side TTL validation to do - the invalid range is unreachable rather
    // than merely rejected. No hostname_hint validation either: the server accepts
    // any string and stores it as an advisory label.
    onSubmit(trimmed ? { hostname_hint: trimmed, ttl_seconds: ttl } : { ttl_seconds: ttl })
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field
        label="Hostname hint"
        htmlFor="new-enrollment-hint"
        hint="Optional advisory label. Omitted from the request when blank."
      >
        <Input
          id="new-enrollment-hint"
          placeholder="farm-west-13"
          value={hint}
          onChange={(e) => setHint(e.target.value)}
        />
      </Field>

      <div className="mb-3">
        <span className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">
          Expires in
        </span>
        <div className="flex gap-1.5">
          {TTL_PRESETS.map((p) => (
            <button
              key={p.label}
              type="button"
              aria-pressed={ttl === p.seconds}
              onClick={() => setTtl(p.seconds)}
              className={ttl === p.seconds ? PRESET_ON : PRESET_OFF}
            >
              {p.label}
            </button>
          ))}
        </div>
        <div className="mt-1 font-mono text-[10.5px] text-fg-dim">
          ttl_seconds - server default 24h, min 60s, max 7d
        </div>
      </div>

      <div className="mb-3 rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[10.5px] leading-relaxed tracking-[0.04em] text-warn">
        ⚠ The raw token is returned once, in the dialog that opens next. It cannot be retrieved
        again.
      </div>

      {error && <div className="mb-3 text-[11px] text-err">{error.message}</div>}

      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Enroll
        </PillButton>
      </div>
    </GlassPanel>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/enrollments/CreateEnrollmentForm.test.tsx`
Expected: PASS (9 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/enrollments/CreateEnrollmentForm.tsx web/src/admin/enrollments/CreateEnrollmentForm.test.tsx
git commit -m "feat(web): inline CreateEnrollmentForm with TTL presets"
```

---

## Task 8: EnrollmentsTab composition

Mirror `web/src/admin/users/UsersTab.tsx:30-213` for the paging state machine, the loading/error/empty triad, the footer, and the `reset()`-before-reopen convention. Differences: no auth lookup, no filter, no debounce, no confirm dialog, and the reveal dialog is driven by `create.data` rather than by local state.

**Files:**
- Create: `web/src/admin/enrollments/EnrollmentsTab.tsx`
- Test: `web/src/admin/enrollments/EnrollmentsTab.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/enrollments/EnrollmentsTab.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { EnrollmentsTab } from './EnrollmentsTab'
import type { AgentEnrollment } from './api'

const TOKEN = 'f00dcafe'.repeat(8)

function row(over: Partial<AgentEnrollment> = {}): AgentEnrollment {
  return {
    id: 'e1',
    created_at: '2026-08-09T09:30:00Z',
    expires_at: '2026-08-10T09:42:00Z',
    created_by: 'u1',
    hostname_hint: 'farm-west-13',
    ...over,
  }
}

// EnrollmentsTab does not use useAuth, so no AuthProvider and no /v1/users/me
// handler are needed - unlike the UsersTab tests.
function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <EnrollmentsTab />
      </QueryClientProvider>,
    ),
  }
}

function listHandler(
  seen: URLSearchParams[],
  envelope: (p: URLSearchParams) => { items: AgentEnrollment[]; next_cursor: string; total: number },
) {
  return http.get('/v1/agent-enrollments', ({ request }) => {
    const params = new URL(request.url).searchParams
    seen.push(params)
    return HttpResponse.json(envelope(params))
  })
}

afterEach(() => vi.useRealTimers())

test('renders rows, the endpoint hint, and the default sort', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
  expect(screen.getByText('GET /v1/agent-enrollments')).toBeInTheDocument()
  expect(seen[0].get('sort')).toBe('-created_at')
  expect(seen[0].get('limit')).toBe('50')
})

test('shows the loading skeleton, then rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  expect(container.querySelectorAll('.h-9').length).toBeGreaterThan(0)
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
})

test('shows an error card whose Retry refetches', async () => {
  let calls = 0
  server.use(
    http.get('/v1/agent-enrollments', () => {
      calls++
      if (calls === 1) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [row()], next_cursor: '', total: 1 })
    }),
  )
  renderTab()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
})

test('shows the empty card when there are no active enrollments', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [], next_cursor: '', total: 0 })))
  renderTab()
  expect(await screen.findByText('No active enrollments.')).toBeInTheDocument()
})

test('sort header clicks issue the four exact server sort values and reset the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: 'c2', total: 9 })))
  renderTab()
  await screen.findByText('farm-west-13')

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('c2'))

  await userEvent.click(screen.getByRole('button', { name: /^CREATED/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('created_at'))
  // A cursor issued under one sort is rejected by the server
  // (internal/api/pagination.go:272-286), so paging must reset.
  expect(seen.at(-1)?.has('cursor')).toBe(false)

  await userEvent.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('expires_at'))
  await userEvent.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('-expires_at'))
})

test('the pager walks the cursor stack and the footer range tracks the offset', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) => ({
      items: [row({ id: p.get('cursor') ? 'e2' : 'e1', hostname_hint: p.get('cursor') ? 'page-two' : 'page-one' })],
      next_cursor: p.get('cursor') ? '' : 'c2',
      total: 2,
    })),
  )
  renderTab()
  await screen.findByText('page-one')
  expect(screen.getByText('1-1 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('page-two')).toBeInTheDocument()
  expect(screen.getByText('2-2 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /next 50/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /prev/ }))
  expect(await screen.findByText('page-one')).toBeInTheDocument()
  expect(screen.getByText('1-1 of 2')).toBeInTheDocument()
})

test('creating posts the exact body, opens the reveal dialog, and refreshes the list', async () => {
  const seen: URLSearchParams[] = []
  let body: unknown
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/agent-enrollments', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: row().expires_at }, { status: 201 })
    }),
  )
  renderTab()
  await screen.findByText('farm-west-13')
  const listCallsBefore = seen.length

  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  await userEvent.type(screen.getByLabelText('Hostname hint'), 'farm-east-01')
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))

  const dialog = await screen.findByRole('dialog')
  expect(body).toEqual({ hostname_hint: 'farm-east-01', ttl_seconds: 86400 })
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)
  expect(dialog).toHaveTextContent(/cannot be retrieved again/i)
  // The inline panel closes behind the dialog.
  expect(screen.queryByLabelText('Hostname hint')).not.toBeInTheDocument()
  // The bare-prefix invalidation refetched the mounted list.
  await waitFor(() => expect(seen.length).toBeGreaterThan(listCallsBefore))

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('a create error renders inside the panel and leaves the table mounted', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ error: 'failed to create enrollment' }, { status: 500 }),
    ),
  )
  renderTab()
  await screen.findByText('farm-west-13')
  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))

  expect(await screen.findByText('500 failed to create enrollment')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByText('farm-west-13')).toBeInTheDocument()

  // Reopening the panel clears the stale error - the reset()-before-reopen
  // convention from UsersTab.tsx:238-245.
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  expect(screen.queryByText('500 failed to create enrollment')).not.toBeInTheDocument()
})

test('the 60s tick flips EXPIRING to EXPIRED with ZERO extra requests', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date('2026-08-09T12:00:00Z'))
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      // 30 minutes of life left at the fake now.
      items: [row({ expires_at: '2026-08-09T12:30:00Z' })],
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  expect(await screen.findByText('EXPIRING')).toBeInTheDocument()
  const callsAfterLoad = seen.length

  // 31 fake minutes: five useNow ticks past the expiry.
  act(() => {
    vi.advanceTimersByTime(31 * 60_000)
  })
  expect(await screen.findByText('EXPIRED')).toBeInTheDocument()
  // The tick is a local clock, not a refetch.
  expect(seen.length).toBe(callsAfterLoad)

  // Positive control on the SAME counter, inside this same test: it can move, so
  // the equality above is about the tick and not about a dead instrument.
  await user.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  await waitFor(() => expect(seen.length).toBeGreaterThan(callsAfterLoad))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/enrollments/EnrollmentsTab.test.tsx`
Expected: FAIL - `Failed to resolve import "./EnrollmentsTab"`.

- [ ] **Step 3: Implement the tab**

Create `web/src/admin/enrollments/EnrollmentsTab.tsx`:

```tsx
import { useState } from 'react'
import { Button } from '../../components/Button'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { useNow } from '../../lib/useNow'
import { TokenRevealDialog } from '../TokenRevealDialog'
import { CreateEnrollmentForm } from './CreateEnrollmentForm'
import { EnrollmentsTable } from './EnrollmentsTable'
import { useAgentEnrollmentActions } from './useAgentEnrollmentActions'
import { useAgentEnrollments } from './useAgentEnrollments'
import type { CreateEnrollmentBody, EnrollmentSort, EnrollmentSortField } from './api'

// Same shape as UsersTab's toggleSort (web/src/admin/users/UsersTab.tsx:17-22):
// clicking the active column flips its direction, clicking the other selects it
// ascending.
function toggleSort(field: EnrollmentSortField, current: EnrollmentSort): EnrollmentSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as EnrollmentSort
  }
  return field
}

export function EnrollmentsTab() {
  const [sort, setSort] = useState<EnrollmentSort>('-created_at')
  // Cursor of the current page (''=first); stack holds the cursors we paged
  // forward from; offsets tracks the real row offset so partial pages stay
  // correct. Same pattern as UsersTab / JobsPage.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
  const [creating, setCreating] = useState(false)

  // A local 60s clock tick, NOT a poll: it re-renders so relative labels and
  // status pills stay correct and issues no request.
  const now = useNow(60_000)

  const { data, error, isLoading, isPlaceholderData, refetch } = useAgentEnrollments(sort, cursor)
  const { create } = useAgentEnrollmentActions()

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    setOffsets([])
  }

  function pickSort(field: EnrollmentSortField) {
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

  function onCreate(body: CreateEnrollmentBody) {
    // The reveal dialog is driven by create.data, so closing the panel here is all
    // that is needed on success; the hook's onSuccess does the invalidation.
    create.mutate(body, { onSuccess: () => setCreating(false) })
  }

  const enrollments = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(startOffset, enrollments.length)
  const rangeText =
    enrollments.length === 0
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
  } else if (enrollments.length === 0) {
    body = (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
        No active enrollments.
      </GlassPanel>
    )
  } else {
    body = (
      <>
        <EnrollmentsTable enrollments={enrollments} sort={sort} onSort={pickSort} now={now} />
        <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
          <span>
            SHOWING <span className="text-fg">{rangeText}</span>
            {' · '}/v1/agent-enrollments (active only) · CURSOR PAGINATED
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
          GET /v1/agent-enrollments
        </span>
        <PillButton
          variant="primary"
          className="ml-auto"
          onClick={() => {
            // reset() clears a stale error AND, critically, a stale token: a
            // previous create's data would otherwise re-open the reveal dialog.
            create.reset()
            setCreating((v) => !v)
          }}
        >
          + Enroll agent
        </PillButton>
      </div>

      {creating && (
        <CreateEnrollmentForm
          pending={create.isPending}
          error={create.error as Error | null}
          onSubmit={onCreate}
          onCancel={() => {
            create.reset()
            setCreating(false)
          }}
        />
      )}

      {body}

      <div className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
        ▸ Enrollments bootstrap a <span className="text-fg-mute">relay-agent</span>: set the token
        as <span className="text-fg-mute">RELAY_AGENT_ENROLLMENT_TOKEN</span> on first boot, and the
        agent exchanges it for a long-lived agent token. Single use. This list shows{' '}
        <span className="text-fg-mute">active only</span>, so a consumed or expired enrollment
        disappears rather than changing state. There is no revoke endpoint in v1, so expiry or
        consumption are the only terminal states - prefer a short TTL. A worker that already
        enrolled can be cut off with DELETE /v1/workers/{'{id}'}/token.
      </div>

      {/* Opens iff the mutation holds a result. The token is read straight from
          create.data and is never copied into state, so this is its only render
          site, and Done -> create.reset() both clears it and unmounts the dialog
          in one step. */}
      {create.data && (
        <TokenRevealDialog
          token={create.data.token}
          title="Agent enrollment created"
          endpoint="POST /v1/agent-enrollments"
          onDone={() => create.reset()}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/enrollments/EnrollmentsTab.test.tsx`
Expected: PASS (9 tests).

If the fake-timer test hangs, confirm `userEvent.setup({ advanceTimers: vi.advanceTimersByTime })` is used for the clicks in that test and that `vi.useFakeTimers({ shouldAdvanceTime: true })` is set before `renderTab()` - MSW resolution needs timers to progress.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/enrollments/EnrollmentsTab.tsx web/src/admin/enrollments/EnrollmentsTab.test.tsx
git commit -m "feat(web): EnrollmentsTab composition with create-and-reveal flow"
```

---

## Task 9: Register the tab

One registry entry. Two shipped test files state the old truth and must be updated first - that is the RED step, and it is honest TDD rather than a chore.

**Files:**
- Modify: `web/src/admin/tabs.ts:2`, `:10-18`
- Modify: `web/src/admin/AdminTabs.test.tsx:16`, `:22`, `:30`, `:40`
- Modify: `web/src/admin/AdminPage.test.tsx:20-38` (add a handler + two tests)

- [ ] **Step 1: Update the two test files to the new truth (RED)**

In `web/src/admin/AdminTabs.test.tsx`, replace the bodies of the four affected tests:

```tsx
test('the registry holds exactly the built tabs', () => {
  expect(ADMIN_TABS.map((t) => t.slug)).toEqual(['users', 'enrollments'])
  expect(DEFAULT_ADMIN_TAB).toBe('users')
})

test('findAdminTab resolves a known slug and rejects everything else', () => {
  expect(findAdminTab('users')?.label).toBe('Users')
  expect(findAdminTab('enrollments')?.label).toBe('Agent enrolls')
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
  expect(screen.getAllByRole('link')).toHaveLength(2)
})

test('tabs that are not built yet are absent', () => {
  renderTabs('/admin/users')
  for (const label of ['Invites', 'Reservations', 'Server']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
})
```

Add one more test to the same file:

```tsx
test('the enrollments tab is marked current on its own route', () => {
  renderTabs('/admin/enrollments')
  expect(screen.getByRole('link', { name: 'Agent enrolls' })).toHaveAttribute('aria-current', 'page')
  expect(screen.getByRole('link', { name: 'Users' })).not.toHaveAttribute('aria-current')
})
```

In `web/src/admin/AdminPage.test.tsx`, add a handler to `renderAt`'s `server.use(...)` call (after the `/v1/users` handler at `:22-37`) so the enrollments panel can mount under MSW's fail-closed policy:

```tsx
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({
        items: [
          {
            id: 'e1',
            created_at: '2026-08-09T09:30:00Z',
            expires_at: '2026-08-10T09:42:00Z',
            created_by: 'u1',
            hostname_hint: 'farm-west-13',
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
```

and append two shell-integration tests:

```tsx
test('/admin/enrollments renders the enrollments panel inside the same shell', async () => {
  renderAt('/admin/enrollments')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Agent enrolls' })).toHaveAttribute('aria-current', 'page')
})

test('/admin/users still renders the Users panel', async () => {
  renderAt('/admin/users')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.queryByText('farm-west-13')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```
npx vitest run src/admin/AdminTabs.test.tsx src/admin/AdminPage.test.tsx
```

Expected: FAIL - `expected [ 'users' ] to deeply equal [ 'users', 'enrollments' ]`, `Unable to find role="link" with name "Agent enrolls"`, and `/admin/enrollments` redirecting to the Users panel so `farm-west-13` is never found.

- [ ] **Step 3: Add the registry entry**

Edit `web/src/admin/tabs.ts` - add the import and the entry, and drop the enrollments line from the not-yet-built comment:

```ts
import type { ComponentType } from 'react'
import { EnrollmentsTab } from './enrollments/EnrollmentsTab'
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
// docs/backlog/feature-2026-08-08-admin-reservations-tab.md,
// docs/backlog/feature-2026-08-08-admin-server-overview-tab.md.
// Order matches the hi-fi's tab order (Invites, still absent, sits between them).
export const ADMIN_TABS: AdminTab[] = [
  { slug: 'users', label: 'Users', Panel: UsersTab },
  { slug: 'enrollments', label: 'Agent enrolls', Panel: EnrollmentsTab },
]

export const DEFAULT_ADMIN_TAB = 'users'

export function findAdminTab(slug: string | undefined): AdminTab | undefined {
  return ADMIN_TABS.find((t) => t.slug === slug)
}
```

No router change: `/admin/:tab` already exists at `web/src/app/router.tsx:34` and `AdminPage` already dispatches through `findAdminTab` (`AdminPage.tsx:15-19`).

- [ ] **Step 4: Run the tests to verify they pass**

```
npx vitest run src/admin/AdminTabs.test.tsx src/admin/AdminPage.test.tsx
```

Expected: PASS (6 + 6 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/tabs.ts web/src/admin/AdminTabs.test.tsx web/src/admin/AdminPage.test.tsx
git commit -m "feat(web): register the Agent enrolls admin tab"
```

---

## Task 10: Token-secrecy suite

The absence assertions that make the reveal safe. Every one is paired with a positive control **in the representation the real failure would take**. Two traps this project has already fallen into are encoded directly:

1. `JSON.stringify([new Error(secret)])` is `'[{}]'`, so a console matcher built on `JSON.stringify(args)` is blind to `console.error(err)` - the single most likely real failure. Walk `Error.name`/`.message`/`.stack`/`.cause`.
2. The token lives in an `<input>`'s **value property**, which is not text and not in `innerHTML`. A "token is gone from the DOM" assertion built on `queryByText` or `document.body.textContent` passes **vacuously** even while the token is on screen.

**Files:**
- Create: `web/src/test/secretLeaks.ts`
- Test: `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx`

`web/src/jobs/logSecrecy.test.tsx` keeps its own inline copy of the console walker (`:20-56`). Rewiring that shipped, reviewed secrecy test to import the new helper is deliberately **out of scope** - it adds risk to an unrelated file for no behaviour change. The new module's comment records the relationship.

- [ ] **Step 1: Write the failing test**

Create `web/src/test/secretLeaks.ts`:

```ts
import { vi, type MockInstance } from 'vitest'

const CONSOLE_METHODS = ['log', 'info', 'warn', 'error', 'debug', 'trace'] as const

export function spyOnConsole(): MockInstance[] {
  return CONSOLE_METHODS.map((m) => vi.spyOn(console, m).mockImplementation(() => {}))
}

// JSON.stringify on an Error yields '{}' (JSON.stringify([new Error('s')]) is
// '[{}]'), so a JSON.stringify(args) matcher is BLIND to console.error(err) where
// err.message or err.stack carries the secret - exactly the property these checks
// exist to protect. Every argument is stringified through its own representation
// instead, including a nested `cause`.
//
// Same walker as the inline copy at web/src/jobs/logSecrecy.test.tsx:27-35, which
// is intentionally left untouched: rewiring a shipped secrecy test buys nothing.
export function stringifyArg(a: unknown): string {
  if (a instanceof Error) {
    return [
      a.name,
      a.message,
      a.stack ?? '',
      a.cause === undefined ? '' : stringifyArg(a.cause),
    ].join(' ')
  }
  if (typeof a === 'string') return a
  try {
    return JSON.stringify(a) ?? String(a)
  } catch {
    return String(a)
  }
}

export function findConsoleLeak(spies: MockInstance[], secret: string): string | null {
  for (const spy of spies) {
    for (const call of spy.mock.calls) {
      for (const arg of call) {
        const s = stringifyArg(arg)
        if (s.includes(secret)) return s
      }
    }
  }
  return null
}

export function assertNoConsoleLeak(spies: MockInstance[], secret: string): void {
  const leak = findConsoleLeak(spies, secret)
  if (leak !== null) throw new Error(`secret leaked to console: ${leak}`)
}

// A credential rendered into an <input> lives in the element's VALUE PROPERTY. It
// is not text, and it is not in innerHTML - so document.body.textContent and
// queryByText can never see it, and an absence assertion built on either passes
// vacuously. Check both representations.
export function domContainsSecret(secret: string): boolean {
  if (document.body.innerHTML.includes(secret)) return true
  for (const el of Array.from(document.querySelectorAll('input, textarea'))) {
    if ((el as HTMLInputElement | HTMLTextAreaElement).value.includes(secret)) return true
  }
  return false
}

export function storageContainsSecret(secret: string): boolean {
  for (const store of [localStorage, sessionStorage]) {
    for (let i = 0; i < store.length; i++) {
      const k = store.key(i)
      if (k === null) continue
      if (k.includes(secret) || (store.getItem(k) ?? '').includes(secret)) return true
    }
  }
  return false
}
```

Create `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import {
  assertNoConsoleLeak,
  domContainsSecret,
  findConsoleLeak,
  spyOnConsole,
  storageContainsSecret,
} from '../../test/secretLeaks'
import { EnrollmentsTab } from './EnrollmentsTab'

// A distinctive 64-hex-char stand-in for the real credential.
const TOKEN = 'f00dcafe'.repeat(8)

const ROW = {
  id: 'e1',
  created_at: '2026-08-09T09:30:00Z',
  expires_at: '2026-08-10T09:42:00Z',
  created_by: 'u1',
  hostname_hint: 'farm-west-13',
}

let requestUrls: string[] = []
let restoreClipboard: (() => void) | null = null

function installClipboard(writeText: (t: string) => Promise<void>) {
  const original = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  restoreClipboard = () => {
    if (original) Object.defineProperty(navigator, 'clipboard', original)
    else delete (navigator as { clipboard?: unknown }).clipboard
    restoreClipboard = null
  }
}

function onRequestStart({ request }: { request: Request }) {
  requestUrls.push(request.url)
}

beforeEach(() => {
  requestUrls = []
  server.events.on('request:start', onRequestStart)
})

afterEach(() => {
  server.events.removeListener('request:start', onRequestStart)
  restoreClipboard?.()
  localStorage.clear()
  sessionStorage.clear()
})

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderTab(client: QueryClient) {
  return render(
    <QueryClientProvider client={client}>
      <EnrollmentsTab />
    </QueryClientProvider>,
  )
}

function mutationStateContains(client: QueryClient, secret: string): boolean {
  return client
    .getMutationCache()
    .getAll()
    .some((m) => JSON.stringify(m.state).includes(secret))
}

function queryCacheContains(client: QueryClient, secret: string): boolean {
  return client
    .getQueryCache()
    .getAll()
    .some((q) => JSON.stringify({ key: q.queryKey, data: q.state.data }).includes(secret))
}

// ---- positive controls for the matchers themselves -------------------------

test('the console matcher catches a bare-string leak', () => {
  const spies = spyOnConsole()
  console.log(`prefix ${TOKEN} suffix`)
  expect(() => assertNoConsoleLeak(spies, TOKEN)).toThrow(/secret leaked/)
  spies.forEach((s) => s.mockRestore())
})

test('the console matcher catches a token carried INSIDE an Error, not just a string', () => {
  // The exact gap that bit this project: JSON.stringify([new Error(TOKEN)]) is
  // '[{}]', so a stringify-based matcher reports clean while the secret sits in
  // the recorded call.
  const spies = spyOnConsole()
  console.error(new Error(`enroll failed: ${TOKEN}`))
  expect(findConsoleLeak(spies, TOKEN)).not.toBeNull()
  expect(JSON.stringify([new Error(TOKEN)])).toBe('[{}]')
  spies.forEach((s) => s.mockRestore())
})

test('the console matcher catches a token on an Error cause', () => {
  const spies = spyOnConsole()
  console.warn(new Error('outer', { cause: new Error(TOKEN) }))
  expect(findConsoleLeak(spies, TOKEN)).not.toBeNull()
  spies.forEach((s) => s.mockRestore())
})

test('the storage matcher catches a manual write', () => {
  localStorage.setItem('probe', TOKEN)
  expect(storageContainsSecret(TOKEN)).toBe(true)
  localStorage.removeItem('probe')
  expect(storageContainsSecret(TOKEN)).toBe(false)
})

test('the DOM matcher sees a value that lives only in an input property', () => {
  // queryByText / textContent would both miss this. If this control ever fails,
  // every "token is gone" assertion below is vacuous.
  const { container } = render(<input readOnly value={TOKEN} aria-label="probe" />)
  expect(container.textContent).not.toContain(TOKEN)
  expect(domContainsSecret(TOKEN)).toBe(true)
})

// ---- the real flow ---------------------------------------------------------

test('the token is revealed once, then leaves the DOM, the caches, storage, URLs, and the console', async () => {
  const spies = spyOnConsole()
  const writeText = vi.fn().mockResolvedValue(undefined)
  installClipboard(writeText)
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: ROW.expires_at }, { status: 201 }),
    ),
  )
  const client = newClient()
  renderTab(client)
  await screen.findByText('farm-west-13')

  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
  await screen.findByRole('dialog')

  // POSITIVE CONTROLS, while the dialog is open: the token really did flow
  // through the real components, and the two "it is gone" instruments below can
  // both see it when it is present.
  expect(domContainsSecret(TOKEN)).toBe(true)
  expect(mutationStateContains(client, TOKEN)).toBe(true)

  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(writeText).toHaveBeenCalledWith(TOKEN)

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  // 1. Gone from the DOM, including every input value.
  expect(domContainsSecret(TOKEN)).toBe(false)
  // 2. create.reset() ran: the mutation retains neither data nor variables.
  expect(mutationStateContains(client, TOKEN)).toBe(false)
  // 3. It never entered the query cache - it is a mutation result, and no query
  //    fetches it.
  expect(queryCacheContains(client, TOKEN)).toBe(false)
  // 4. It never entered web storage. No query persister is configured
  //    (web/src/lib/queryClient.ts), so nothing reaches IndexedDB either.
  expect(storageContainsSecret(TOKEN)).toBe(false)
  // 5. It never entered a request URL - no path segment and no query param, so it
  //    cannot leak into history, a Referer header, or a proxy log.
  expect(requestUrls.length).toBeGreaterThan(0) // the instrument recorded something
  for (const url of requestUrls) expect(url).not.toContain(TOKEN)
  // 6. No console method ever received it, in any representation.
  assertNoConsoleLeak(spies, TOKEN)

  spies.forEach((s) => s.mockRestore())
})

test('the URL instrument would catch a token in a query param (positive control)', async () => {
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({ items: [], next_cursor: '', total: 0 }),
    ),
  )
  // The same handler answers the probe, so MSW's fail-closed policy is satisfied.
  await fetch(`/v1/agent-enrollments?probe=${TOKEN}`)
  expect(requestUrls.some((u) => u.includes(TOKEN))).toBe(true)
})

test('the reveal is reachable only through the mutation - no route or link carries the token', async () => {
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
  )
  renderTab(newClient())
  await screen.findByText('farm-west-13')
  // The list response carries no token field at all, so nothing on this page can
  // link to, bookmark, or re-display one.
  expect(domContainsSecret(TOKEN)).toBe(false)
  for (const a of Array.from(document.querySelectorAll('a'))) {
    expect(a.getAttribute('href') ?? '').not.toContain(TOKEN)
  }
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/enrollments/enrollmentTokenSecrecy.test.tsx`
Expected: FAIL - `Failed to resolve import "../../test/secretLeaks"`.

- [ ] **Step 3: Implement**

The helper module in Step 1 **is** the implementation - no production code changes in this task. Create `web/src/test/secretLeaks.ts` exactly as written above if it was not created yet.

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/enrollments/enrollmentTokenSecrecy.test.tsx`
Expected: PASS (8 tests).

- [ ] **Step 5: Prove the two absence assertions that matter are not vacuous**

**(a) The reset() proof.** Temporarily change `onDone={() => create.reset()}` in `web/src/admin/enrollments/EnrollmentsTab.tsx` to keep the token alive:

```tsx
        <TokenRevealDialog
          token={create.data.token}
          title="Agent enrollment created"
          endpoint="POST /v1/agent-enrollments"
          onDone={() => {}}
        />
```

Run: `npx vitest run src/admin/enrollments/enrollmentTokenSecrecy.test.tsx`
Expected: FAIL - the dialog never unmounts, so `waitFor(queryByRole('dialog') absent)` times out and `mutationStateContains` stays true. Restore `create.reset()` and re-run: PASS.

**(b) The console-matcher proof.** Temporarily add `console.error(new Error('copy failed: ' + token))` inside `TokenRevealDialog`'s `copy()` catch, and make the installed clipboard stub reject (change `mockResolvedValue(undefined)` to `mockRejectedValue(new Error('denied'))` in the flow test).

Run: `npx vitest run src/admin/enrollments/enrollmentTokenSecrecy.test.tsx`
Expected: FAIL with `secret leaked to console: Error copy failed: f00dcafe...`. This is the proof the matcher walks `Error.message`; a `JSON.stringify(args)` matcher would report clean here. Revert both edits and re-run: PASS (8 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/test/secretLeaks.ts web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx
git commit -m "test(web): enrollment token secrecy suite with paired positive controls"
```

---

## Task 11: Whole-plan gate

- [ ] **Step 1: Full suite green**

Run (from `web/`): `npm test`
Expected: PASS. The suite stood at **530** tests before this plan; it should now be roughly 530 + 78 (3 + 8 + 7 new pure/status tests, 7 api, 2 list-hook, 3 actions, 9 dialog, 9 table, 9 form, 9 tab, 8 secrecy, 1 new AdminTabs test, 2 new AdminPage tests, minus none removed). The exact number does not matter; **zero failures and zero skips** does.

- [ ] **Step 2: Production build clean**

Run: `npm run build`
Expected: exit 0, no TypeScript errors. TS is the only checker for the `hostname_hint?: string` optionality and for the `Chip` tone union, so a green build is part of the contract, not a formality.

- [ ] **Step 3: Revert the tracked-but-stale build output**

`web/dist` is tracked in git but stale from the original scaffold, so Step 2 dirties it.

```bash
git checkout -- web/dist/
git status --short
```

Expected: `web/dist` clean; the only remaining changes are the source and test files this plan lists.

- [ ] **Step 4: Browser check against a real backend**

`make build`, run `relay-server` with a real Postgres, sign in as an admin at `http://localhost:8080`, then:

1. Navigate to `/admin` - it redirects to `/admin/users`; the tab bar shows **Users** and **Agent enrolls**.
2. Click **Agent enrolls** - the URL becomes `/admin/enrollments`, the tab is highlighted, and the list loads (empty card on a fresh DB).
3. Click **+ Enroll agent**, type a hostname hint, pick **1h**, click **Enroll**.
4. The reveal dialog appears with a 64-hex token, the one-time warning, and (on `http://localhost`, which **is** a secure context) a **Copy** button. Click it - the label flips to **Copied**; paste elsewhere and confirm the token matches.
5. Click somewhere on the backdrop - **the dialog must not close.** Then press **Done** - it closes, and the list now shows the row with an **EXPIRING** pill (1h TTL is inside the window) and `in 59m`.
6. Reload the page - the token is nowhere on screen and nowhere in the list. Open DevTools: no `console` output from the flow, no `localStorage` entry containing it, and the Network tab shows the token only in the POST **response body**, never in a URL.
7. Optional insecure-context check: browse to the same server by LAN IP over plain HTTP (`http://<ip>:8080`), create an enrollment, and confirm the **Copy** button is absent and the manual-copy hint appears with the token pre-selected.

- [ ] **Step 5: Confirm the scope guard held**

```bash
git diff --stat origin/main...HEAD
```

Expected: **no Go files, no `.sql` files, no `web/dist` files.** Changed paths must be confined to `web/src/admin/`, `web/src/lib/time.ts`, `web/src/lib/useNow.ts` (+ tests), and `web/src/test/secretLeaks.ts`. Any Go file in that list means revocation or an enricher crept in - revert it.

---

## Tests most at risk of being vacuous

A plan's test bodies are guesses, not verified guards. These are the ones to distrust, and the proof for each is already a numbered step above.

| Test | Why it can pass for the wrong reason | Proof of RED |
|---|---|---|
| "the token leaves the DOM after Done" (Task 10) | The token lives in an `<input>` **value property**; `queryByText` / `textContent` cannot see it, so an absence assertion built on either is always true. | Task 10 Step 1's DOM-matcher control (`container.textContent` does **not** contain it, `domContainsSecret` does), plus Task 10 Step 5(a): with `onDone={() => {}}` the test must fail. |
| "no console call carries the token" (Task 10) | `JSON.stringify([new Error(secret)])` is `'[{}]'`, so a stringify matcher is blind to the likeliest real failure. | Task 10's `Error` and `cause` controls, and Step 5(b): a deliberate `console.error(new Error(token))` inside the component must turn it RED. |
| "create invalidates the list" (Task 4) | Without an **active observer** (`renderHook`-mounted query), `refetchType: 'active'` never fires and the assertion holds no matter what key is invalidated. A `fetchQuery`/`setQueryData` seed is the trap. | Task 4 Step 5: breaking the key to `['agent-enrollments-broken']` must fail the mounted-list test. |
| "does not poll" (Task 3) and "the 60s tick issues no request" (Task 8) | A counter that can never move makes any "stayed at N" assertion free. | Both tests carry an in-test positive control that moves the same counter (`refetch()` in Task 3, a sort-header click in Task 8). |
| "backdrop click does not close" (Task 5) | Passes trivially if nothing can close the dialog, or if the test clicks the wrong node. | Paired Escape control in the same test, plus Task 5 Step 5: adding `onClick={onDone}` to the overlay must fail it. |
| "no Copy button without the clipboard API" (Task 5) | A typo'd query returns null and the absence assertion passes. | Paired "the Copy button IS present when the API exists" test. |
| "hostname_hint renders a hyphen" (Task 6) | Would also pass if the cell rendered nothing at all. | Same test asserts `undefined`, `null`, and `—` are all absent **and** that the hyphen is present. |
| "createAgentEnrollment always sends a body" (Task 2) | An MSW handler that accepts anything proves nothing about the mandatory body. | The handler mirrors `readJSON` and 400s an empty body, so dropping the body fails the test. |

---

## Self-review against the spec

- Decision 1 (revoke ships as nothing, ACTIONS renamed NOTE): scope guard + Task 6 tests.
- Decision 2 (reveal shared at `web/src/admin/`, create form tab-local): Tasks 5 and 7.
- Decision 3 (modal, no auto-dismiss, no backdrop-close, Escape closes, readonly focused pre-selected input, single Done button): Task 5.
- Decision 4 (clipboard offered, feature-detected, no `execCommand`, no clear-on-close): Task 5, plus the guarded-rejection test.
- Decision 5 (three derived states, `useNow(60_000)`, `formatTimeUntil`): Tasks 1, 6, 8.
- Decision 6 (clickable headers, CREATED column added, no `SortControl`): Tasks 6 and 8.
- Architecture file list: every file in the spec's list appears in the File Structure table. Two additions the spec did not name: `web/src/test/secretLeaks.ts` (test helper for the secrecy suite) and the two shipped test files the registry change forces.
- Acceptance criteria 1-10: 1 -> Task 9; 2 -> Tasks 3, 8; 3 -> Task 6; 4 -> Tasks 1, 6, 8; 5 -> Tasks 2, 4, 7, 8; 6 -> Tasks 5, 8; 7 -> Task 5; 8 -> Task 10; 9 -> Task 5; 10 -> Task 11.
- Type consistency check: `EnrollmentSort` / `EnrollmentSortField` / `AgentEnrollment` / `AgentEnrollmentsPage` / `CreateEnrollmentBody` / `CreateEnrollmentResponse` / `TtlPreset` are defined once in Task 2 and used under those exact names in Tasks 3, 4, 6, 7, 8. `deriveStatus` / `statusTone` / `EnrollmentStatus` (Task 1) are used under those names in Task 6. `useNow` (Task 1) is used in Task 8. `formatTimeUntil` (Task 1) is used in Task 6. `TokenRevealDialog`'s prop names (`token`, `title`, `endpoint`, `warning`, `onDone`) match its single call site in Task 8.
