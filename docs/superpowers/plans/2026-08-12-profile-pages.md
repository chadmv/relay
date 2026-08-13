# Profile Pages (Identity / Password / Sessions) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `/profile/:tab` surface behind the three dead `UserMenu` links - an Identity tab that renames the user through a changed-fields-only `PATCH /v1/users/me`, a Password tab that changes the password through `PUT /v1/users/me/password` with three client-side guards, and a Sessions tab whose one confirmed action calls `DELETE /v1/auth/tokens` and then tears the SPA's own session down, because that endpoint destroys the caller's own token.

**Architecture:** Seven new files under `web/src/profile/` (page, tab bar, tab registry, three panels, one API client module) plus three surgical edits to shipped modules (`web/src/lib/types.ts`, `web/src/auth/AuthProvider.tsx`, `web/src/app/router.tsx`) and one deletion (`web/src/app/JobsPlaceholder.tsx`, whose only importer is the route this slice replaces). No `useQuery` anywhere on the surface: identity data is already resident in `AuthProvider`, and there is no list to keep fresh. The two places a plausible implementation is silently wrong get their own tasks and their own test files: the sign-out-everywhere teardown (Task 7) and the 403-not-401 wrong-password path (Task 5).

**Tech Stack:** React 18, TypeScript, TanStack Query v5 (mutations only), react-router-dom v7, Tailwind v4 (Holo tokens), Vitest 2.1 + Testing Library 16 + user-event 14 + MSW 2.7, jsdom 29.

**Spec:** `docs/superpowers/specs/2026-08-12-profile-pages.md` (approved; do not reopen its decisions)

**Backlog item closed by this slice:** `docs/backlog/feature-2026-06-26-profile-identity-password-sessions.md` (close it with `/backlog close feature-2026-06-26-profile-identity-password-sessions`, which `git mv`s it into `docs/backlog/closed/`; never hand-edit `status:`).

---

## Slice independence declaration

- **Backend slice: NONE. This is 100% `web/`. Zero Go files change, zero `.sql` files change, therefore no `make generate`, no `*.sql.go`, no `models.go`, no migration.** I re-verified every backend claim in the spec against the tree (see "Verified backend surface" below) and all of them hold; not one required a Go change to make the frontend work. None of the six Invariants in CLAUDE.md is in play server-side. Two frontend analogues **do** apply and are called out per task: every request goes through `apiFetch` (`web/src/lib/api.ts:29`), and "end the generation before releasing the resource" governs the Sessions teardown (Task 7).
- **Frontend slice: ONE ENGINEER, SEQUENTIAL.** Do not split these tasks across two engineers and do not run any of them in parallel. The dependency chain is linear: Tasks 3-7 import Tasks 1 and 2; Task 8's registry imports the three panels from Tasks 3-7; Task 9 imports Task 8; Task 10 imports Task 9. Tasks 1 and 10 both write into shipped shared modules (`web/src/auth/AuthProvider.tsx`, `web/src/lib/types.ts`, `web/src/app/router.tsx`); concurrent writers there have burned this project before.
- **Parallelism available to the conductor for Phase 3: none within this plan.** Unrelated work elsewhere in the repo can run alongside it.

---

## Verified backend surface (re-verified against the tree; do not trust the spec alone)

Read: `internal/api/server.go:96-100,153`; `internal/api/users.go:19-67,403-430`; `internal/api/auth.go:276-357`; `internal/store/query/tokens.sql:1-29`; `internal/store/migrations/000001_initial.up.sql:4-19`.

| Claim | Verdict | Evidence |
|---|---|---|
| All five self-service routes are `auth(...)`, **none** `AdminOnly` | Confirmed | `server.go:97-100` and `:153` |
| `PATCH /v1/users/me` is registered, and separately from the other four | Confirmed | `server.go:153` (the other four are `:97-100`) |
| `updateUserRequest` has exactly one field, `Name string`, not a pointer | Confirmed | `users.go:49-51` |
| The **trimmed** name is what gets stored, and an empty trim is a 400 `name is required` | Confirmed | `parseUpdateUserRequest`, `users.go:61-65`; `handleUpdateMe` passes `name` at `:421-424` |
| Email is immutable through the entire store layer | Confirmed | no `UPDATE users SET email` anywhere in `internal/store/query/users.sql` |
| `handleUpdateMe` returns **200** with the full `userResponse`, the same struct `handleGetMe` returns | Confirmed | `users.go:429` and `:410` both call `toUserResponse` |
| `userResponse.CreatedAt` has no `omitempty` and `users.created_at` is `NOT NULL DEFAULT NOW()` | Confirmed | `users.go:22-29`; `000001_initial.up.sql:9` |
| `PUT /v1/users/me/password` body is exactly `{current_password, new_password}` | Confirmed | `auth.go:277-280` |
| The **only** rule on `new_password` is `len(...) < 8` -> 400. No complexity policy exists | Confirmed | `auth.go:284-287`; nothing else validates it |
| A wrong current password is **403**, not 401 | Confirmed | `auth.go:298-301` `http.StatusForbidden` |
| A password over 72 bytes is an opaque **500** `failed to hash password` | Confirmed | `auth.go:303-307` |
| Success is **204** and it revokes **other** sessions only - the caller stays signed in | Confirmed | `auth.go:338`; `DeleteOtherTokensForUser` at `:325-328` -> `tokens.sql:28-29` `AND id <> $2` |
| `DELETE /v1/auth/tokens` deletes **every** token for the user, the caller's included | Confirmed, and this is the defect driver | `handleLogoutAll` `auth.go:350-357` -> `DeleteTokensForUser` -> `tokens.sql:25-26` `WHERE user_id = $1`, **no** `id <> $2` |
| `DELETE /v1/auth/token` (singular) revokes only the caller's current token | Confirmed | `handleLogoutCurrent`, `auth.go:341-348` -> `DeleteToken(authUser.TokenID)` |
| `GET /v1/auth/tokens` does not exist, and `api_tokens` has no `last_used_at` column | Confirmed | route table `server.go:96-100`; `tokens.sql` has no list query; table is `id, user_id, token_hash, created_at, expires_at` (`000001_initial.up.sql:13-19`) |
| Every one of these acts on `authUser.ID`/`authUser.TokenID` from the bearer token; no id in any path or body | Confirmed | `users.go:414`, `auth.go:289`, `auth.go:342`, `auth.go:351` |

**Client-side consequences that follow, which the code must match exactly:**

- `apiFetch` returns `undefined` for a 204 (`web/src/lib/api.ts:57`), so the two void calls need no special handling.
- `onUnauthorized` fires on a literal **401 only** (`web/src/lib/api.ts:44-46`). A 403 does not fire it; a 204 does not fire it.
- `ApiError.message` is `"<status> <server sentence>"` (`lib/api.ts:53`), so a wrong current password renders as `403 current password is incorrect`.
- Timestamps are Go `time.Time`, RFC3339 with nanoseconds. The house rendering for an absolute date is a **string slice**, `created_at.slice(0, 10)` (`web/src/admin/users/UsersTable.tsx:123`), not `new Date()`. Reuse it: it has no timezone behaviour to get wrong, and on a fixture missing the field it throws loudly (`Cannot read properties of undefined (reading 'slice')`) instead of silently rendering `Invalid Date`.
- **No path interpolation anywhere on this surface** - all three paths are literals. The defect filed as `bug-2026-08-12-unencoded-path-interpolation-api-clients` therefore gains no new instance here. Do not add `encodeURIComponent` to a literal.

---

## Existing precedent for every new artifact

"Mirror X at `file:line`" is a literal instruction: copy the shape, change the nouns. Read each file before writing the file that mirrors it.

| New artifact | Shipped file it mirrors |
|---|---|
| `web/src/profile/tabs.ts` | `web/src/admin/tabs.ts:21-32` - registry array, `DEFAULT_*`, `find*` returning `undefined` for anything unknown |
| `web/src/profile/ProfileTabs.tsx` | `web/src/admin/AdminTabs.tsx:9-29` - pill group, `NavLink` supplying `aria-current="page"`, no count badge |
| `web/src/profile/ProfilePage.tsx` | `web/src/admin/AdminPage.tsx:15-40` - `useParams().tab` -> `find` -> `<Navigate replace>` on unknown -> render `<Panel/>` |
| `web/src/profile/IdentityTab.tsx` changed-fields save | `web/src/workers/WorkerEditForm.tsx:17-46`, especially the patch construction at `:42-45` |
| `web/src/profile/PasswordTab.tsx` guards | `web/src/admin/users/ResetPasswordDialog.tsx:35-50` - the min-8 literal, the 72-byte `TextEncoder` guard, the mismatch message |
| Mutation error rendered as `error.message` | `web/src/admin/users/ResetPasswordDialog.tsx:84` |
| Destructive action behind a confirm | `web/src/components/ConfirmDialog.tsx:17-61` - **reuse as-is, do not modify it** |
| Omission footnote in the house style | `web/src/admin/enrollments/EnrollmentsTab.tsx:197-205` (the `▸` mono block) |
| Absence asserted in both directions | `web/src/admin/enrollments/EnrollmentsTable.test.tsx:74-78`, `web/src/admin/users/UsersTable.test.tsx:51-56` |
| Role chip | `web/src/admin/users/UsersTable.tsx:120` - `<Chip tone={is_admin ? 'accent' : 'muted'}>{is_admin ? 'ADMIN' : 'USER'}</Chip>` |
| Absolute date rendering | `web/src/admin/users/UsersTable.tsx:123` - `created_at.slice(0, 10)` |
| Routed tab test harness | `web/src/admin/AdminPage.test.tsx:18-92` (MemoryRouter + AuthProvider + Routes) and `web/src/admin/AdminTabs.test.tsx:7-13` |
| Form field label/hint/error | `web/src/components/Field.tsx:11-25`, `Input.tsx:3-14` |

Available Holo primitives, all sufficient - **no new primitive is needed and no table is needed**: `GlassPanel, Eyebrow, ProgressBar, Chip, PillButton, KpiStat, Panel, StatusDot, Table, TableRow, TableCell, ariaSort, sortCaret` (`web/src/components/holo/index.ts:3-12`). Note `PillButton` spreads `...rest` **after** its own `type="button"` (`PillButton.tsx:20`), so `type="submit"` on a form's primary action works.

### SECOND-CONSUMER FLAG (not a third; no extraction due)

The registry-plus-switch tab shell (`tabs.ts` + `Tabs.tsx` + `Page.tsx`) gets its **second** consumer here. `web/src/admin/` is the first. The house rule is *extract before the third*, so this plan deliberately writes a local copy and extracts nothing. Recorded here so the third tabbed surface triggers the extraction. The two are not parameterizable today without inventing options neither needs: admin sits behind `AdminRoute` and profile does not, and the hi-fi wants a count badge on Sessions (`hifi3-holo-pages.jsx:2819`) that `AdminTabs.tsx:6-8` deliberately does not render and that we could not supply anyway, since `GET /v1/auth/tokens` does not exist.

The min-8 password check reaches a **fourth** site here (`RegisterScreen.tsx:31-32`, `ResetPasswordDialog.tsx:36`, `CreateUserForm.tsx:40` are the first three). Spec decision 11 rules that it is copied, not extracted: two lines with no decision inside them become indirection when extracted, and a shared constant would still leave four call sites writing the same two lines. **Do not extract it.** If the message or the bound ever diverges between sites, that divergence is the trigger to revisit.

**No other new artifact in this slice is a third consumer of a non-primitive pattern.** In particular the detail-page state triad (`idea-2026-08-12-detail-page-state-triad-primitive.md`) gains **no** consumer here: this page fetches no resource by id, has no 404 state, renders no loading panel and no retryable error card. Its countdown is explicitly **not** advanced by this slice.

---

## Test-environment constraints (pin these; they have bitten this repo before)

- **Runner:** vitest 2.1 + jsdom 29 + `@testing-library/react` 16 + `user-event` 14. MSW 2.7 with `onUnhandledRequest: 'error'` (`web/src/test/setup.ts:5`) and **zero default handlers** (`web/src/test/msw.ts:4`) - every endpoint a test touches needs an explicit `server.use(...)` or the test errors.
- **`renderWithQuery` does NOT provide a router** (`web/src/test/renderWithQuery.tsx:7-12`) and does not provide `AuthProvider`. Every test file in this slice builds its own harness with `QueryClientProvider` + `MemoryRouter` + `AuthProvider`, mirroring `web/src/admin/AdminPage.test.tsx:78-91`. Without the router you get `useHref() may be used only in the context of a <Router>`.
- **Auth-state teardown tests must observe the real store, not a component's props.** `getToken()` reads `localStorage` (`web/src/lib/token.ts:3-5`). A teardown test asserts on `getToken()`, on a mounted `useAuth()` probe's `status`, and on `client.getQueryCache().getAll()` - three separate stores. Asserting only the navigation passes against an implementation that leaves a live token behind.
- **A TanStack `invalidateQueries` test needs an ACTIVE OBSERVER.** Mount the query with `renderHook` or inside the rendered tree; a `client.fetchQuery` / `setQueryData` seed leaves no observer, `invalidateQueries`' default `refetchType: 'active'` never fires, and the assertion passes vacuously. Cited in Task 7, where the assertion is the *negative* - that no refetch happens - so the observer must provably be live (the paired positive is its mount-time fetch).
- **Callback ordering inside a mutation, verified in the installed library:** a hook-level `onSuccess` (`useMutation({onSuccess})`) runs at `@tanstack/query-core/build/modern/mutation.js:123`, **before** the success dispatch that notifies observers and before any mutate-level `onSuccess`. So a teardown placed in the hook-level `onSuccess` runs while the page is still mounted with live observers. That is exactly why Task 7's ordering matters.
- **`queryClient.clear()` clears the MUTATION cache too** (`queryClient.js:296-298`), and `mutationCache.clear()` only notifies and empties a `Set` (`mutationCache.js:81-89`) - it does not throw when called from inside a running mutation's own `onSuccess`, and the mutation's `execute` continues normally. Verified by reading the installed source. Do not add defensive code for this.
- **`useMutation` retains `state.variables` on the settled mutation for the 5-minute default `gcTime`** (`mutation.js:94` dispatches `{type:'pending', variables}`; `mutationObserver.js:50-55` `reset()` only removes *this* observer and `mutation.js:38-46` reschedules the same GC). This is why Task 4's password mutation takes **no variables**. Cited in Task 5.
- **Dialogs:** `inert` and focus-trap libraries are unusable in this jsdom, and native `<dialog>` is not viable. This slice therefore **reuses `ConfirmDialog`/`DialogShell` unmodified** and adds no new modal machinery. Cited in Tasks 6 and 7.
- **An overlay owns its own error surface.** A mutation error rendered in a page-level box while a modal is open sits behind the modal's `fixed inset-0 z-50` scrim. Task 7 closes the confirm dialog *before* firing the mutation, so the error lands on a visible page; that ordering is deliberate and is asserted.
- **Plan-supplied test bodies are guesses until run RED.** Every step below states the expected failure text. **A green test before the implementation exists is vacuous - fix the test, do not proceed.** Where a task's RED cannot come from a missing implementation, the task says so and names the substitute evidence (a mutate-and-revert with both outputs recorded).

---

## Conventions for every task

- All `npm`/`npx` commands run from the `web/` directory of the worktree: `D:/dev/relay/.claude/worktrees/pr-merge-session-f5796e/web`.
- Single file: `npx vitest run src/<path>.test.tsx`. Full suite: `npm test`.
- TDD per step: write the failing test, run it and watch it fail with the stated message, implement, run it and watch it pass, commit.
- House rule: **never an em dash or en dash**, in code, comments, copy or this document. Placeholders are the plain ASCII hyphen `-`.
- Never reformat code you were not asked to change. Never edit a shipped test's assertions to make new code pass - an assertion needing adjustment IS the finding.
- `make` is **not on PATH in this shell**. Use `go build ./...` and `go test ./...` directly from the repo root.

---

## Scope guard - do NOT build

- **No Sessions list, no table, no per-session revoke.** `GET /v1/auth/tokens` does not exist and `api_tokens` has no `last_used_at`, agent, IP or location column. A list is a migration plus an endpoint plus a route family, not "one small table".
- **No password strength meter.** The server has no complexity policy to reflect (`auth.go:284-287` is the whole rule). A meter would assert a policy that does not exist.
- **No email editing, no email input that is enabled, no "request email change" control.** Email is immutable through the entire store layer.
- **No `LAST LOGIN`, `LOGIN COUNT`, `ACTIVE SESSIONS`, and no Activity side card.** Three of the four have no backing column; `MEMBER SINCE` moves into the header strip.
- **No "Forgot your password?" side card.** It is accurate but aimed at a locked-out user who by definition cannot reach a page behind the login wall.
- **No `useQuery` anywhere in `web/src/profile/`.** No `refetchInterval`, no timer, no polling. Zero background requests.
- **No `invalidateQueries` anywhere in `web/src/profile/`.** See Task 7 - it is the specific hazard this slice exists to avoid.
- **No changes to `UserMenu.tsx`.** Its three links already resolve once the routes exist.
- **No edits to `ConfirmDialog`, `DialogShell`, `Field`, `Input`, `Button`, `Chip`, `PillButton`, `GlassPanel`, `Eyebrow` or any other shared primitive.**
- **No edits to any assertion in `web/src/auth/AuthProvider.test.tsx`.** It is the regression gate for the `clearSession` extraction. New auth tests go in a new file.
- **No shared tab-shell extraction** (second consumer) and **no min-8 guard extraction** (a two-line guard with no decision inside it).

---

## File Structure

**New files** (all under `web/src/profile/` unless stated)

| File | Responsibility |
|---|---|
| `api.ts` | `updateMe`, `changePassword`, `signOutEverywhere`. The contract and every hazard comment live here. |
| `api.test.ts` | Method, path, exact body key sets, 204 handling, error status mapping. |
| `IdentityTab.tsx` | Name draft (seeded lazily, never re-derived once dirty), no-op-when-unchanged save, `applyUser` on 200, disabled email, role note. |
| `IdentityTab.test.tsx` | The no-op both directions, the trimmed body, `applyUser` proven through a sibling probe, the 400, the disabled email. |
| `PasswordTab.tsx` | Three fields, three client guards, the other-sessions warning, a variable-free mutation. |
| `PasswordTab.test.tsx` | Guards block the request (each with a positive control), exact body key set, 204 clears the inputs. |
| `PasswordTab.auth.test.tsx` | The 403 path and the secret-retention assertion. Its own file so it is findable. |
| `SessionsTab.tsx` | The confirmed sign-out-everywhere action, the honest copy, the omission footnote. |
| `SessionsTab.test.tsx` | Absence of the list in both directions, the label, the confirm copy, Cancel issues nothing. |
| `SessionsTab.teardown.test.tsx` | The Invariant-1 teardown. Its own file so it is findable. |
| `tabs.ts` | `PROFILE_TABS`, `DEFAULT_PROFILE_TAB`, `findProfileTab`. |
| `ProfileTabs.tsx` | The pill-group `NavLink` bar. |
| `ProfileTabs.test.tsx` | Registry contents, `findProfileTab` in both directions, hrefs, `aria-current`. |
| `ProfilePage.tsx` | Header, initials, meta strip, tab bar, active panel, unknown-tab redirect. |
| `ProfilePage.test.tsx` | Header and meta strip in both directions, initials, the redirect, the end-to-end rename reaching the `h1`. |
| `ProfileRoutes.test.tsx` | All three `UserMenu` hrefs resolve through the real `AppRoutes`; `JobsPlaceholder` is unreachable. |

**Modified files**

| File | Change |
|---|---|
| `web/src/lib/types.ts:1-6` | `User` gains `created_at: string`. |
| `web/src/auth/AuthProvider.tsx:16-22,91-100` | Context gains `applyUser` and `clearSession`; `logout()` re-expressed through `clearSession` with a byte-identical exported signature. |
| `web/src/app/router.tsx:4,41` | Drop the `JobsPlaceholder` import and the `/profile/*` splat; add `/profile` -> `<Navigate>` and `/profile/:tab` -> `<ProfilePage/>`. |

**Deleted files**

| File | Why |
|---|---|
| `web/src/app/JobsPlaceholder.tsx` | Its only importer is `router.tsx:4,41`, the route this slice replaces. Verified by grep: three hits total, two of them that import and that route, one the definition. It has no test file. |

**New test file outside `web/src/profile/`**

| File | Responsibility |
|---|---|
| `web/src/auth/AuthProvider.session.test.tsx` | Direct tests for `clearSession` and `applyUser`, kept out of `AuthProvider.test.tsx` so that file stays byte-identical. |

**Reused unchanged:** `GlassPanel`, `Eyebrow`, `Chip`, `PillButton`, `ConfirmDialog`, `Field`, `Input`, `apiFetch`, `ApiError`, `useAuth`, `getToken`/`setToken`/`clearToken`.

---

## Task 1: `User.created_at`, and `AuthProvider` gains `applyUser` and `clearSession`

Everything else in the slice depends on these two context methods. The extraction is behaviour-preserving for `logout()` on purpose, and `web/src/auth/AuthProvider.test.tsx` is the gate: **not one line of it may change.**

**Files:**
- Modify: `web/src/lib/types.ts:1-6`
- Modify: `web/src/auth/AuthProvider.tsx:16-22`, `:91-100`
- Create: `web/src/auth/AuthProvider.session.test.tsx`
- Untouched gate: `web/src/auth/AuthProvider.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/auth/AuthProvider.session.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken, getToken, setToken } from '../lib/token'
import { AuthProvider, useAuth } from './AuthProvider'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
}

function Probe() {
  const { status, user, clearSession, applyUser } = useAuth()
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="name">{user?.name ?? 'none'}</span>
      <button onClick={() => clearSession()}>clear</button>
      <button onClick={() => applyUser({ ...ME, name: 'Mira Renamed' })}>apply</button>
    </div>
  )
}

function renderProbe(client = new QueryClient()) {
  return render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </QueryClientProvider>,
  )
}

test('clearSession clears token, user, status and the query cache', async () => {
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_existing')
  const qc = new QueryClient()
  qc.setQueryData(['workers'], [{ id: 'w1' }])
  renderProbe(qc)
  await waitFor(() => expect(screen.getByTestId('name')).toHaveTextContent('Mira Sato'))
  // Paired positive: the three stores are non-empty BEFORE the action, so each
  // assertion below is about the teardown and not about a store that was never
  // populated.
  expect(getToken()).toBe('tok_existing')
  expect(qc.getQueryCache().getAll().length).toBeGreaterThan(0)

  await userEvent.click(screen.getByText('clear'))

  await waitFor(() => expect(getToken()).toBeNull())
  expect(screen.getByTestId('name')).toHaveTextContent('none')
  expect(screen.getByTestId('status')).toHaveTextContent('anonymous')
  expect(qc.getQueryCache().getAll()).toHaveLength(0)
})

test('clearSession issues NO network request', async () => {
  let meCalls = 0
  let revokeCalls = 0
  server.use(
    http.get('/v1/users/me', () => {
      meCalls++
      return HttpResponse.json(ME)
    }),
    // Registered so a stray call is COUNTED rather than blowing up as an
    // unhandled request. onUnhandledRequest:'error' would also catch it, but a
    // count says which route was hit.
    http.delete('/v1/auth/token', () => {
      revokeCalls++
      return new HttpResponse(null, { status: 204 })
    }),
    http.delete('/v1/auth/tokens', () => {
      revokeCalls++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  setToken('tok_existing')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('name')).toHaveTextContent('Mira Sato'))
  expect(meCalls).toBe(1)

  await userEvent.click(screen.getByText('clear'))
  await waitFor(() => expect(getToken()).toBeNull())

  // The whole point of clearSession existing separately from logout(): its one
  // caller has already destroyed every token server-side, so any request made
  // after that point is a guaranteed 401 racing this teardown.
  expect(revokeCalls).toBe(0)
  expect(meCalls).toBe(1)
})

test('applyUser replaces the user row in place with no refetch', async () => {
  let meCalls = 0
  server.use(
    http.get('/v1/users/me', () => {
      meCalls++
      return HttpResponse.json(ME)
    }),
  )
  setToken('tok_existing')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('name')).toHaveTextContent('Mira Sato'))

  await userEvent.click(screen.getByText('apply'))

  await waitFor(() => expect(screen.getByTestId('name')).toHaveTextContent('Mira Renamed'))
  // The PATCH response IS the authoritative row (internal/api/users.go:429 and
  // :410 both call toUserResponse), so there is nothing to confirm with a second
  // round trip. A confirming refetch would be a second source of truth.
  expect(meCalls).toBe(1)
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
})

test('logout STILL issues DELETE /v1/auth/token exactly once after the extraction', async () => {
  // Positive control on the extraction: re-expressing logout() through
  // clearSession must not gut its network half. AuthProvider.test.tsx already
  // covers logout's local effects and must stay byte-identical; this asserts the
  // request itself, which that file does not count.
  let revokeCalls = 0
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.delete('/v1/auth/token', () => {
      revokeCalls++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  setToken('tok_existing')
  function LogoutProbe() {
    const { logout, user } = useAuth()
    return (
      <div>
        <span data-testid="lname">{user?.name ?? 'none'}</span>
        <button onClick={() => logout()}>logout</button>
      </div>
    )
  }
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthProvider>
        <LogoutProbe />
      </AuthProvider>
    </QueryClientProvider>,
  )
  await waitFor(() => expect(screen.getByTestId('lname')).toHaveTextContent('Mira Sato'))
  await userEvent.click(screen.getByText('logout'))
  await waitFor(() => expect(getToken()).toBeNull())
  expect(revokeCalls).toBe(1)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/auth/AuthProvider.session.test.tsx`

Expected: FAIL - `TypeError: clearSession is not a function` on the first three tests (vitest transpiles without type-checking, so the missing context members arrive as `undefined` at runtime rather than as a TS error). The fourth test PASSES already; that is correct, it is the untouched-behaviour control.

- [ ] **Step 3: Implement**

Replace `web/src/lib/types.ts:1-6` with:

```ts
export interface User {
  id: string
  email: string
  name: string
  is_admin: boolean
  // ALWAYS present. userResponse has no omitempty on CreatedAt
  // (internal/api/users.go:22-29) and users.created_at is NOT NULL DEFAULT NOW()
  // (internal/store/migrations/000001_initial.up.sql:9). RFC3339 with
  // nanoseconds. Required rather than optional-with-a-fallback: no file in
  // web/src constructs a User-annotated literal (the only typed uses are
  // apiFetch<User> and user: User | null, AuthProvider.tsx:18,56,70), so making
  // it required costs nothing and a fallback would only hide a broken fixture.
  //
  // archived_at is deliberately NOT modelled: an archived user's token cannot
  // authenticate at all - GetTokenWithUser joins AND u.archived_at IS NULL
  // (internal/store/query/tokens.sql:20) - so on the endpoints that produce this
  // type the field can only ever be null.
  created_at: string
}
```

In `web/src/auth/AuthProvider.tsx`, replace the interface at `:16-22`:

```tsx
interface AuthContextValue {
  status: Status
  user: User | null
  login: (email: string, password: string) => Promise<void>
  register: (input: RegisterInput) => Promise<void>
  logout: () => Promise<void>
  // Replaces the in-memory user row with an authoritative server response.
  // PATCH /v1/users/me returns the same userResponse struct GET /v1/users/me
  // returns (internal/api/users.go:429 and :410 both call toUserResponse), so
  // there is nothing to confirm with a second round trip. This exists so the
  // profile page does NOT introduce a second ['me'] query: one owner of
  // identity, not two caches that can disagree.
  applyUser: (u: User) => void
  // Local-only session teardown: forget the token, forget the user, go anonymous,
  // drop the query cache. Issues NO request, on purpose.
  //
  // Its one caller is the Sessions tab, and by the time it runs the server has
  // already destroyed EVERY bearer token for this user - DELETE /v1/auth/tokens
  // is DeleteTokensForUser, `DELETE FROM api_tokens WHERE user_id = $1`
  // (internal/store/query/tokens.sql:25-26), with no `id <> $2`. Any request made
  // after that point is a guaranteed 401 racing this teardown, which is exactly
  // why logout() is NOT reused there: logout() would first fire
  // DELETE /v1/auth/token against a token that no longer exists.
  //
  // The ORDER inside it is CLAUDE.md's "end the generation before releasing the
  // resource": setStatus('anonymous') is what ends the generation, because
  // ProtectedRoute re-renders to <Navigate to="/auth" replace/> in the same
  // commit and unmounts every page and every active query observer, so nothing
  // is left holding a continuation that could fire against the dead credential.
  clearSession: () => void
}
```

Replace `:91-97` (`logout`) with:

```tsx
  function clearSession() {
    clearToken()
    setUser(null)
    setStatus('anonymous')
    queryClient.clear()
  }

  function applyUser(u: User) {
    setUser(u)
  }

  async function logout() {
    await apiFetch('/auth/token', { method: 'DELETE' }).catch(() => {})
    clearSession()
  }
```

and the provider value at `:100`:

```tsx
    <AuthContext.Provider
      value={{ status, user, login, register, logout, applyUser, clearSession }}
    >
```

**Do NOT re-express the 401 listener at `:39-49` through `clearSession`.** It looks like the same four lines, and it is - but it carries an `if (statusRef.current === 'anonymous') return` guard that `clearSession` must not have, and its effect deps are `[queryClient]` so it subscribes exactly once for the provider's life. Routing it through a function identity that changes every render would either re-subscribe on every render or need a ref dance, for zero behaviour gain. Leave it alone.

- [ ] **Step 4: Run the tests to verify they pass, including the untouched gate**

```
npx vitest run src/auth/
```

Expected: PASS, 9 tests (5 shipped in `AuthProvider.test.tsx` + 4 new). **`AuthProvider.test.tsx` must be byte-identical.** If any assertion in it needed an edit, the extraction was not behaviour-preserving - fix the implementation, not the test. Confirm with:

```bash
git diff --stat web/src/auth/AuthProvider.test.tsx
```

Expected: no output at all.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/types.ts web/src/auth/AuthProvider.tsx web/src/auth/AuthProvider.session.test.tsx
git commit -m "feat(web): AuthProvider applyUser and clearSession, User.created_at"
```

---

## Task 2: The profile API clients

Three functions, three literal paths, no interpolation. Every backend hazard in this slice is written down here so a later caller cannot reintroduce it without reading why not.

**Files:**
- Create: `web/src/profile/api.ts`
- Test: `web/src/profile/api.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `web/src/profile/api.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { changePassword, signOutEverywhere, updateMe } from './api'

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

test('updateMe PATCHes /v1/users/me with exactly { name } and parses the row', async () => {
  let method: string | undefined
  let path: string | undefined
  let body: Record<string, unknown> | undefined
  server.use(
    http.patch('/v1/users/me', async ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ME, name: 'Mira Renamed' })
    }),
  )
  const u = await updateMe('Mira Renamed')
  expect(method).toBe('PATCH')
  expect(path).toBe('/v1/users/me')
  // toEqual on the WHOLE body, not a property check: the failure mode is an EXTRA
  // key. updateUserRequest has exactly one field (internal/api/users.go:49-51)
  // and email is immutable store-wide, so an `email` key here would be a control
  // that silently does nothing.
  expect(body).toEqual({ name: 'Mira Renamed' })
  expect(u.name).toBe('Mira Renamed')
  // created_at is on the type and on the wire; the header renders it.
  expect(u.created_at).toBe('2025-04-02T09:15:00Z')
})

test('updateMe surfaces the empty-name 400 as ApiError with the server sentence', async () => {
  server.use(
    http.patch('/v1/users/me', () =>
      HttpResponse.json({ error: 'name is required' }, { status: 400 }),
    ),
  )
  const err = await updateMe('   ').catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err.status).toBe(400)
  // ApiError.message is "<status> <server sentence>" (lib/api.ts:53), which is
  // what the form renders verbatim (ResetPasswordDialog.tsx:84).
  expect(err.message).toBe('400 name is required')
})

test('changePassword PUTs exactly current_password and new_password, and NOTHING else', async () => {
  let method: string | undefined
  let path: string | undefined
  let body: Record<string, unknown> | undefined
  server.use(
    http.put('/v1/users/me/password', async ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      body = (await request.json()) as Record<string, unknown>
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(changePassword('old-secret', 'new-secret-123')).resolves.toBeUndefined()
  expect(method).toBe('PUT')
  expect(path).toBe('/v1/users/me/password')
  expect(body).toEqual({ current_password: 'old-secret', new_password: 'new-secret-123' })
  // The form has THREE fields and the API takes TWO. A confirm_password key is
  // the natural mistake; assert the key set so it cannot pass.
  expect(Object.keys(body!).sort()).toEqual(['current_password', 'new_password'])
})

test('changePassword surfaces a wrong current password as ApiError(403), not 401', async () => {
  // 403 is deliberate on the server (internal/api/auth.go:298-301). A 401 would
  // fire onUnauthorized (lib/api.ts:44-46) and sign the user out on a typo.
  server.use(
    http.put('/v1/users/me/password', () =>
      HttpResponse.json({ error: 'current password is incorrect' }, { status: 403 }),
    ),
  )
  const err = await changePassword('wrong', 'new-secret-123').catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err.status).toBe(403)
  expect(err.message).toBe('403 current password is incorrect')
})

test('signOutEverywhere DELETEs the PLURAL path and tolerates the empty 204', async () => {
  let method: string | undefined
  let path: string | undefined
  let singularCalls = 0
  server.use(
    http.delete('/v1/auth/tokens', ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      // A real 204 has NO body (internal/api/auth.go:356). A client that
      // unconditionally calls res.json() throws 'Unexpected end of JSON input'.
      return new HttpResponse(null, { status: 204 })
    }),
    http.delete('/v1/auth/token', () => {
      singularCalls++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(signOutEverywhere()).resolves.toBeUndefined()
  expect(method).toBe('DELETE')
  // Singular vs plural is a one-character difference between "revoke my current
  // token" (auth.go:341-348) and "revoke every token this user has"
  // (auth.go:350-357). Assert the exact path AND that the sibling never fired.
  expect(path).toBe('/v1/auth/tokens')
  expect(singularCalls).toBe(0)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/profile/api.test.ts`

Expected: FAIL at import time - `Failed to resolve import "./api" from "src/profile/api.test.ts"`. The whole file fails to load, which is the correct RED for a missing module.

- [ ] **Step 3: Implement**

Create `web/src/profile/api.ts`:

```ts
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
//       not fire onUnauthorized (lib/api.ts:44-46) and must never sign anyone out.
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
// `DELETE FROM api_tokens WHERE user_id = $1` (internal/store/query/tokens.sql:25-26)
// with NO `id <> $2`. The hi-fi calls this "Sign out everywhere ELSE"
// (design_handoff_relay_holo/hifi3-holo-pages.jsx:2796, :3049) and there is no such
// endpoint: the all-but-current query exists (tokens.sql:28-29) but only the
// password path routes to it. Labelling this control "else" would understate its
// blast radius, which is a defect and not a copy nit.
//
// A 204 fires NO listener - onUnauthorized is 401-only (lib/api.ts:44-46) - so
// after this resolves the SPA still holds a token in localStorage against a
// credential the server has already destroyed, and still believes it is
// authenticated. The caller MUST tear its own session down immediately; see
// SessionsTab and AuthProvider.clearSession.
//
// Note the PLURAL path. DELETE /v1/auth/token (singular) is a different endpoint
// that revokes only the caller's current token (auth.go:341-348).
export function signOutEverywhere(): Promise<void> {
  return apiFetch<void>('/auth/tokens', { method: 'DELETE' })
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/profile/api.test.ts`

Expected: PASS, 5 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/profile/api.ts web/src/profile/api.test.ts
git commit -m "feat(web): profile API clients for rename, password and sign-out-everywhere"
```

---

## Task 3: IdentityTab - the no-op save and the single owner of identity

Mirror `web/src/workers/WorkerEditForm.tsx:17-46`, especially the changed-fields construction at `:42-45`. Here the patch has one field, so "changed-fields-only" degenerates to "issue zero requests when unchanged" - which is not merely tidy: a no-op PATCH is a write to the users table and a 200 that would reset the form's success state for no reason.

**Files:**
- Create: `web/src/profile/IdentityTab.tsx`
- Test: `web/src/profile/IdentityTab.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/profile/IdentityTab.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider, useAuth } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { IdentityTab } from './IdentityTab'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

// Reads the user straight off the context, so it proves applyUser ran rather
// than proving the form set its own local state. This is the same instrument
// ProfilePage's <h1> uses; ProfilePage.test.tsx repeats the assertion against the
// real header once that file exists.
function UserProbe() {
  const { user } = useAuth()
  return <span data-testid="probe-name">{user?.name ?? 'none'}</span>
}

async function renderTab(me: Record<string, unknown> = ME) {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(me)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/identity']}>
        <AuthProvider>
          <UserProbe />
          <IdentityTab />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  // Wait for hydration before touching the form: the draft is derived lazily from
  // the live user row until the first keystroke, so a test that types before
  // hydration would be editing a field seeded from null.
  await waitFor(() => expect(screen.getByTestId('probe-name')).toHaveTextContent('Mira Sato'))
  return { ...utils, client }
}

test('Save with an untouched name issues ZERO requests', async () => {
  let patches = 0
  server.use(
    http.patch('/v1/users/me', () => {
      patches++
      return HttpResponse.json(ME)
    }),
  )
  await renderTab()
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(patches).toBe(0)
})

test('a changed name issues exactly one PATCH whose body is the TRIMMED name (positive control)', async () => {
  // Without this, the test above passes against a form whose Save does nothing.
  let patches = 0
  let body: Record<string, unknown> | undefined
  server.use(
    http.patch('/v1/users/me', async ({ request }) => {
      patches++
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ME, name: 'Mira Renamed' })
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, '  Mira Renamed  ')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(patches).toBe(1))
  // toEqual on the whole object: an extra key is the failure mode a property
  // check cannot see, and `email` is the specific extra key somebody will add.
  expect(body).toEqual({ name: 'Mira Renamed' })
})

test('a whitespace-only edit is NOT a change and issues zero requests', async () => {
  // The server trims before storing (internal/api/users.go:61), so "Mira Sato  "
  // and "Mira Sato" are the same row. A dirtiness flag instead of a value
  // comparison would fail here and would write on every visit to the field.
  let patches = 0
  server.use(
    http.patch('/v1/users/me', () => {
      patches++
      return HttpResponse.json(ME)
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.type(input, '   ')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(patches).toBe(0)
})

test('typing a new name and typing it back issues zero requests', async () => {
  let patches = 0
  server.use(
    http.patch('/v1/users/me', () => {
      patches++
      return HttpResponse.json(ME)
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'Someone Else')
  await userEvent.clear(input)
  await userEvent.type(input, 'Mira Sato')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(patches).toBe(0)
})

test('on 200 the AuthProvider user is replaced, not just the local input', async () => {
  server.use(
    http.patch('/v1/users/me', () => HttpResponse.json({ ...ME, name: 'Mira Renamed' })),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'Mira Renamed')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  // Reading the PROBE, not the input. A component that only sets its own state
  // passes an input-reading test while leaving every other consumer of `user`
  // stale for the rest of the session.
  await waitFor(() => expect(screen.getByTestId('probe-name')).toHaveTextContent('Mira Renamed'))
  expect(await screen.findByRole('status')).toHaveTextContent('Display name updated.')
})

test('a 400 renders the server sentence inline and leaves the user row unchanged', async () => {
  server.use(
    http.patch('/v1/users/me', () =>
      HttpResponse.json({ error: 'name is required' }, { status: 400 }),
    ),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'x')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('400 name is required')
  expect(screen.getByTestId('probe-name')).toHaveTextContent('Mira Sato')
})

test('Cancel restores the loaded name and clears the error, so a following Save issues nothing', async () => {
  let patches = 0
  server.use(
    http.patch('/v1/users/me', () => {
      patches++
      return HttpResponse.json({ error: 'name is required' }, { status: 400 })
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'x')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(await screen.findByRole('alert')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(input).toHaveValue('Mira Sato')
  expect(screen.queryByRole('alert')).toBeNull()

  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(patches).toBe(1)
})

test('the email field is present, disabled, and hinted - and no control can mutate it', async () => {
  await renderTab()
  const email = screen.getByLabelText('Email')
  expect(email).toHaveValue('mira@studio.dev')
  expect(email).toBeDisabled()
  expect(screen.getByText('identity - contact your admin to change')).toBeInTheDocument()
  // Both directions: the form has exactly ONE editable text control, so there is
  // no second input somebody could wire to an email PATCH the API does not have.
  const editable = screen
    .getAllByRole('textbox')
    .filter((el) => !(el as HTMLInputElement).disabled)
  expect(editable).toHaveLength(1)
  expect(editable[0]).toBe(screen.getByLabelText('Display name'))
})

test('the role note shows ADMIN for an admin and USER for a non-admin', async () => {
  await renderTab()
  expect(screen.getByText('ADMIN')).toBeInTheDocument()
  expect(screen.queryByText('USER')).toBeNull()
  expect(
    screen.getByText(/Role is server-side only/),
  ).toBeInTheDocument()
})

test('the role note shows USER for a non-admin (paired control)', async () => {
  await renderTab({ ...ME, is_admin: false })
  expect(screen.getByText('USER')).toBeInTheDocument()
  expect(screen.queryByText('ADMIN')).toBeNull()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/profile/IdentityTab.test.tsx`

Expected: FAIL - `Failed to resolve import "./IdentityTab" from "src/profile/IdentityTab.test.tsx"`.

- [ ] **Step 3: Implement**

Create `web/src/profile/IdentityTab.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { useAuth } from '../auth/AuthProvider'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { Chip, GlassPanel, PillButton } from '../components/holo'
import { updateMe } from './api'

// The Identity tab. Renames the signed-in user through PATCH /v1/users/me and
// pushes the authoritative 200 body back into AuthProvider.
//
// The hi-fi's Activity side card (hifi3-holo-pages.jsx:2946-2965) is NOT built:
// three of its four rows - Last login, Login count, Active sessions - have no
// backing column or endpoint anywhere, and MEMBER SINCE, the one real value, lives
// in the page header. Rendering "-" for three of four rows is the same mistake the
// admin VERSION/BUILD/DB/UPTIME strip avoided (AdminPage.tsx:6-14).
export function IdentityTab() {
  const { user, applyUser } = useAuth()

  // The draft is null until the user types. While it is null the field simply
  // shows the live user row, which is correct because nothing polls here and the
  // only writer of that row is this form. Once it is a string it is NEVER
  // re-derived from `user`, so a save landing (which changes `user`) cannot reset
  // a field mid-edit. Note the null-versus-empty distinction: clearing the input
  // gives '', which is a real edit, not a fall-through to user.name.
  const [draft, setDraft] = useState<string | null>(null)

  const save = useMutation({
    mutationFn: (nextName: string) => updateMe(nextName),
    onSuccess: (updated) => {
      // ONE owner of identity. The PATCH response is the same userResponse struct
      // GET /v1/users/me returns (internal/api/users.go:429, :410), so it is
      // authoritative and needs no confirming round trip - and pushing it here
      // avoids a second ['me'] query that could disagree with the provider.
      applyUser(updated)
      // Releasing the draft, not writing a value into it: the form is clean again
      // and follows the new authoritative row. A settled mutation must never push
      // a value back into draft state.
      setDraft(null)
    },
  })

  if (!user) return null
  const name = draft ?? user.name

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!user) return
    const trimmed = name.trim()
    // Changed-fields-only, degenerating to "send nothing when unchanged" because
    // PATCH takes exactly one field. Same construction as WorkerEditForm.tsx:42-45.
    // Compared against the TRIMMED draft because the server trims before storing
    // (internal/api/users.go:61), so "Mira  " and "Mira" are the same row and a
    // whitespace-only edit must not issue a write.
    if (trimmed === user.name) return
    // No client-side empty-name guard, deliberately: the server's own
    // `name is required` 400 (users.go:63) is the message we would otherwise
    // duplicate, and there is no second field here to protect from a wasted round
    // trip. One error-rendering path, not two.
    save.reset()
    save.mutate(trimmed)
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="max-w-[560px] p-6">
      <div className="mb-4 flex items-baseline justify-between">
        <span className="text-[13px] text-fg">Identity</span>
        <span className="font-mono text-[10px] tracking-[0.06em] text-fg-dim">
          PATCH /v1/users/me
        </span>
      </div>

      <Field label="Display name" htmlFor="profile-name">
        <Input
          id="profile-name"
          value={name}
          autoComplete="name"
          onChange={(e) => setDraft(e.target.value)}
        />
      </Field>

      {/* Not a deferral and not "coming soon": email is immutable through the
          entire API and store layer - internal/store/query/users.sql has no
          `UPDATE users SET email` anywhere. The hint states the real remedy. */}
      <Field
        label="Email"
        htmlFor="profile-email"
        hint="identity - contact your admin to change"
      >
        <Input id="profile-email" value={user.email} disabled readOnly />
      </Field>

      <div className="mb-3 flex items-center gap-2.5 rounded-md border border-border bg-white/[0.02] px-3 py-2.5">
        <Chip tone={user.is_admin ? 'accent' : 'muted'}>{user.is_admin ? 'ADMIN' : 'USER'}</Chip>
        <span className="text-[12px] leading-relaxed text-fg-mute">
          Role is server-side only - promote or demote from{' '}
          <span className="text-fg">Admin -&gt; Users</span>.
        </span>
      </div>

      {save.error && (
        <div role="alert" className="mb-3 text-[11px] text-err">
          {save.error.message}
        </div>
      )}
      {save.isSuccess && (
        <div role="status" className="mb-3 text-[11px] text-ok">
          Display name updated.
        </div>
      )}

      <div className="flex gap-2">
        <PillButton type="submit" variant="primary" disabled={save.isPending}>
          Save changes
        </PillButton>
        <PillButton
          onClick={() => {
            setDraft(null)
            save.reset()
          }}
        >
          Cancel
        </PillButton>
      </div>
    </GlassPanel>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/profile/IdentityTab.test.tsx`

Expected: PASS, 10 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/profile/IdentityTab.tsx web/src/profile/IdentityTab.test.tsx
git commit -m "feat(web): profile Identity tab with a no-op-when-unchanged rename"
```

---

## Task 4: PasswordTab - the three guards and the variable-free mutation

The three client-side guards run in the spec's stated order and each blocks the request. The mutation deliberately takes **no variables**; the reason is written at the call site and is tested in Task 5.

**Files:**
- Create: `web/src/profile/PasswordTab.tsx`
- Test: `web/src/profile/PasswordTab.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/profile/PasswordTab.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { PasswordTab } from './PasswordTab'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function renderTab() {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/password']}>
        <AuthProvider>
          <PasswordTab />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

async function fill(current: string, next: string, confirm: string) {
  await userEvent.type(screen.getByLabelText('Current password'), current)
  await userEvent.type(screen.getByLabelText('New password'), next)
  await userEvent.type(screen.getByLabelText('Confirm new password'), confirm)
}

function countingHandler(counter: { n: number; body?: Record<string, unknown> }) {
  return http.put('/v1/users/me/password', async ({ request }) => {
    counter.n++
    counter.body = (await request.json()) as Record<string, unknown>
    return new HttpResponse(null, { status: 204 })
  })
}

test('a valid submit sends EXACTLY current_password and new_password', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'new-secret-123', 'new-secret-123')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  await waitFor(() => expect(c.n).toBe(1))
  expect(c.body).toEqual({ current_password: 'old-secret', new_password: 'new-secret-123' })
  // The form has three fields and the API takes two. Assert the key set so a
  // stray confirm_password cannot pass.
  expect(Object.keys(c.body!).sort()).toEqual(['current_password', 'new_password'])
})

test('a 204 clears all three inputs and shows a success line', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'new-secret-123', 'new-secret-123')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  await waitFor(() => expect(screen.getByLabelText('Current password')).toHaveValue(''))
  expect(screen.getByLabelText('New password')).toHaveValue('')
  expect(screen.getByLabelText('Confirm new password')).toHaveValue('')
  expect(screen.getByRole('status')).toHaveTextContent(
    'Password updated. Your other sessions have been signed out.',
  )
})

test('a confirm mismatch blocks the request', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'new-secret-123', 'new-secret-124')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  expect(screen.getByText('The two passwords do not match.')).toBeInTheDocument()
  expect(c.n).toBe(0)
})

test('a 7-character new password blocks the request with the shipped literal', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'short12', 'short12')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  // The exact string already at RegisterScreen.tsx:31-32, ResetPasswordDialog.tsx:36
  // and CreateUserForm.tsx:40. Copied to a fourth site by design (spec decision 11).
  expect(screen.getByText('Password must be at least 8 characters.')).toBeInTheDocument()
  expect(c.n).toBe(0)
})

test('an 8-character new password IS sent (positive control on the min-8 guard)', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'eightchr', 'eightchr')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))
  await waitFor(() => expect(c.n).toBe(1))
})

test('a 73-byte new password blocks the request - BYTE length, not character length', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  // 37 characters, 74 BYTES in UTF-8. Deliberately not 73 ASCII characters: with
  // ASCII the test cannot distinguish TextEncoder().encode(x).length from
  // x.length, and a .length guard would pass it. bcrypt rejects over 72 bytes and
  // handleChangePassword turns that into an opaque 500 (internal/api/auth.go:303-307).
  const pw = 'e\u0301'.length ? '\u00e9'.repeat(37) : ''
  expect(pw).toHaveLength(37)
  expect(new TextEncoder().encode(pw).length).toBe(74)
  await fill('old-secret', pw, pw)
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  expect(screen.getByText('Password must be 72 bytes or fewer.')).toBeInTheDocument()
  expect(c.n).toBe(0)
})

test('a 72-byte new password IS sent (positive control on the byte guard)', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  const pw = '\u00e9'.repeat(36)
  expect(new TextEncoder().encode(pw).length).toBe(72)
  await fill('old-secret', pw, pw)
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))
  await waitFor(() => expect(c.n).toBe(1))
})

test('Cancel clears all three inputs and issues nothing', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'new-secret-123', 'new-secret-123')
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

  expect(screen.getByLabelText('Current password')).toHaveValue('')
  expect(screen.getByLabelText('New password')).toHaveValue('')
  expect(screen.getByLabelText('Confirm new password')).toHaveValue('')
  expect(c.n).toBe(0)
})

test('the warning states OTHER sessions are signed out and this browser stays signed in', async () => {
  renderTab()
  const warning = screen.getByTestId('password-session-warning')
  // Verified against DeleteOtherTokensForUser (internal/api/auth.go:325-328 ->
  // internal/store/query/tokens.sql:28-29 `AND id <> $2`). This is the ONE place
  // the hi-fi's session copy is correct (hifi3-holo-pages.jsx:3010-3012).
  expect(warning).toHaveTextContent(/other/i)
  expect(warning).toHaveTextContent(/this browser stays signed in/i)
})

test('there is NO strength meter', async () => {
  renderTab()
  // The server's only rule is len(new) >= 8 (internal/api/auth.go:284-287). A
  // meter reading "mixed case - 1 number" (hifi3-holo-pages.jsx:3003) would
  // assert a policy that does not exist anywhere in the codebase.
  expect(screen.queryByText(/strong|weak|mixed case/i)).toBeNull()
  // Paired positive on the same instrument: the honest hint IS rendered, so the
  // absence assertion is not about an empty component.
  expect(screen.getByText('min 8 characters')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/profile/PasswordTab.test.tsx`

Expected: FAIL - `Failed to resolve import "./PasswordTab" from "src/profile/PasswordTab.test.tsx"`.

- [ ] **Step 3: Implement**

Create `web/src/profile/PasswordTab.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { GlassPanel, PillButton } from '../components/holo'
import { changePassword } from './api'

// The Password tab. Three fields, three client-side guards, one PUT.
//
// The hi-fi's strength meter (hifi3-holo-pages.jsx:2994-3004) is NOT built: the
// server's only rule on the new password is len(...) >= 8
// (internal/api/auth.go:284-287), so a meter would assert a complexity policy
// that does not exist. Its "Forgot your password?" side card (:3021-3034) is also
// out: it is accurate, but it is documentation aimed at a locked-out user, who by
// definition cannot reach a page behind the login wall.
export function PasswordTab() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [guardError, setGuardError] = useState<string | undefined>()

  const change = useMutation({
    // NO mutation VARIABLES, on purpose. mutate() is called with no argument and
    // this closure reads the fields instead.
    //
    // TanStack stores whatever you pass to mutate() on the mutation's
    // state.variables (@tanstack/query-core mutation.js:94) and keeps the SETTLED
    // mutation in the MutationCache for the 5-minute default gcTime, so passing
    // the plaintext password as a variable would leave it readable from
    // queryClient.getMutationCache().getAll() long after this form cleared its
    // inputs. reset() does not help - it removes only this observer and
    // reschedules the same GC (mutationObserver.js:50-55, mutation.js:38-46).
    // Clearing the inputs is not evidence; PasswordTab.auth.test.tsx asserts
    // absence from the store the library actually keeps.
    mutationFn: () => changePassword(current, next),
    onSuccess: () => {
      setCurrent('')
      setNext('')
      setConfirm('')
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    // Three guards, in this order, each blocking the request.
    if (next !== confirm) {
      setGuardError('The two passwords do not match.')
      return
    }
    // The shipped literal, copied verbatim from RegisterScreen.tsx:31-32,
    // ResetPasswordDialog.tsx:36 and CreateUserForm.tsx:40. Deliberately copied
    // rather than extracted: two lines with no decision inside them become
    // indirection when hidden behind a helper.
    if (next.length < 8) {
      setGuardError('Password must be at least 8 characters.')
      return
    }
    // BYTE length, not .length. bcrypt.GenerateFromPassword rejects over 72 bytes
    // and handleChangePassword maps that to an opaque 500
    // (internal/api/auth.go:303-307), so a routine password-manager password
    // would produce a server error with no explanation. A 40-character passphrase
    // with accents or emoji can exceed 72 bytes while passing a .length check.
    // Same guard as ResetPasswordDialog.tsx:43-46.
    if (new TextEncoder().encode(next).length > 72) {
      setGuardError('Password must be 72 bytes or fewer.')
      return
    }
    setGuardError(undefined)
    change.reset()
    change.mutate()
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="max-w-[560px] p-6">
      <div className="mb-4 flex items-baseline justify-between">
        <span className="text-[13px] text-fg">Change password</span>
        <span className="font-mono text-[10px] tracking-[0.06em] text-fg-dim">
          PUT /v1/users/me/password
        </span>
      </div>

      <Field label="Current password" htmlFor="pw-current">
        <Input
          id="pw-current"
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
      </Field>
      <Field label="New password" htmlFor="pw-new" hint="min 8 characters">
        <Input
          id="pw-new"
          type="password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
      </Field>
      <Field label="Confirm new password" htmlFor="pw-confirm" error={guardError}>
        <Input
          id="pw-confirm"
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
      </Field>

      {/* A verified consequence, not a hedge. The handler revokes every OTHER
          token in the same transaction as the password write and keeps the
          caller's own (internal/api/auth.go:325-328 ->
          internal/store/query/tokens.sql:28-29 `AND id <> $2`), so this browser
          survives and every other browser, device and `relay` CLI login gets a
          401 on its next request. */}
      <div
        data-testid="password-session-warning"
        className="mb-3 rounded-md border border-warn/35 bg-warn/[0.08] px-3 py-2.5 text-[12px] leading-relaxed text-fg"
      >
        All of your <b>other</b> sessions will be signed out, including any{' '}
        <span className="font-mono">relay</span> CLI login. This browser stays signed in.
      </div>

      {change.error && (
        <div role="alert" className="mb-3 text-[11px] text-err">
          {change.error.message}
        </div>
      )}
      {change.isSuccess && (
        <div role="status" className="mb-3 text-[11px] text-ok">
          Password updated. Your other sessions have been signed out.
        </div>
      )}

      <div className="flex gap-2">
        <PillButton type="submit" variant="primary" disabled={change.isPending}>
          Update password
        </PillButton>
        <PillButton
          onClick={() => {
            setCurrent('')
            setNext('')
            setConfirm('')
            setGuardError(undefined)
            change.reset()
          }}
        >
          Cancel
        </PillButton>
      </div>
    </GlassPanel>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/profile/PasswordTab.test.tsx`

Expected: PASS, 10 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/profile/PasswordTab.tsx web/src/profile/PasswordTab.test.tsx
git commit -m "feat(web): profile Password tab with mismatch, min-8 and 72-byte guards"
```

---

## Task 5: The 403-not-401 path, and what the settled mutation retains

**HIGH-RISK TASK 1 of 2.** A component that treats every password error as an auth failure signs the user out on a typo. The distinction is easy to get backwards and impossible to see in a test written against a generic "error" mock, so this task asserts the status **403 specifically** and pairs it with a live control that proves the instrument can observe a teardown at all.

Its own file so it is findable, and so a future reviewer looking for "does a wrong password log me out" finds one place.

**Files:**
- Create: `web/src/profile/PasswordTab.auth.test.tsx`
- No implementation file. The behaviour was implemented in Tasks 2 and 4; see Step 2.

- [ ] **Step 1: Write the test**

Create `web/src/profile/PasswordTab.auth.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider, useAuth } from '../auth/AuthProvider'
import { clearToken, getToken, setToken } from '../lib/token'
import { PasswordTab } from './PasswordTab'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function SessionProbe() {
  const { status, user } = useAuth()
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="who">{user?.email ?? 'none'}</span>
    </div>
  )
}

function renderTab() {
  setToken('tok_live')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/password']}>
        <AuthProvider>
          <SessionProbe />
          <PasswordTab />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

async function submit(current: string, next: string) {
  await userEvent.type(screen.getByLabelText('Current password'), current)
  await userEvent.type(screen.getByLabelText('New password'), next)
  await userEvent.type(screen.getByLabelText('Confirm new password'), next)
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))
}

test('a 403 renders inline and leaves the user SIGNED IN', async () => {
  // 403, not 401, is the server's own choice (internal/api/auth.go:298-301), and
  // onUnauthorized is 401-only (web/src/lib/api.ts:44-46). Asserting a specific
  // 403 is the discriminating input: a test using a generic error mock would pass
  // against a component that signs the user out on ANY failure.
  server.use(
    http.put('/v1/users/me/password', () =>
      HttpResponse.json({ error: 'current password is incorrect' }, { status: 403 }),
    ),
  )
  renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  await submit('wrong-password', 'new-secret-123')

  expect(await screen.findByRole('alert')).toHaveTextContent('403 current password is incorrect')
  // All three stores, not just one: an implementation that navigated away without
  // clearing the token, or cleared the token without navigating, would pass a
  // single-store assertion.
  expect(getToken()).toBe('tok_live')
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
  expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev')
})

test('a 401 from the same endpoint DOES tear the session down (control: the probe is live)', async () => {
  // Without this, "still signed in after a 403" passes against a harness in which
  // nothing could ever sign anybody out. This proves the instrument works, so the
  // assertion above is about the 403 and not about a dead probe.
  server.use(
    http.put('/v1/users/me/password', () =>
      HttpResponse.json({ error: 'invalid token' }, { status: 401 }),
    ),
  )
  renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  await submit('old-secret', 'new-secret-123')

  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  expect(getToken()).toBeNull()
})

test('a 204 leaves the user SIGNED IN - this endpoint spares the caller token', async () => {
  // The direct counterpart of SessionsTab.teardown.test.tsx, which asserts the
  // opposite for DELETE /v1/auth/tokens. These two tests are each other's control
  // and the difference between them is the whole session story of this slice:
  // DeleteOtherTokensForUser has `AND id <> $2`
  // (internal/store/query/tokens.sql:28-29); DeleteTokensForUser does not (:25-26).
  server.use(http.put('/v1/users/me/password', () => new HttpResponse(null, { status: 204 })))
  renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  await submit('old-secret', 'new-secret-123')

  await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument())
  expect(getToken()).toBe('tok_live')
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
})

test('the settled mutation does not retain the plaintext password anywhere', async () => {
  server.use(http.put('/v1/users/me/password', () => new HttpResponse(null, { status: 204 })))
  const { client } = renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const secret = 'correct-horse-battery-staple'
  await submit('old-secret', secret)
  await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument())

  const mutations = client.getMutationCache().getAll()
  // POSITIVE CONTROL FIRST. TanStack keeps a settled mutation in the cache for
  // the 5-minute default gcTime, so this list must be non-empty - otherwise the
  // absence assertion below is about an empty array and proves nothing.
  expect(mutations.length).toBeGreaterThan(0)
  for (const m of mutations) {
    // state.variables is where mutate(x) puts x (query-core mutation.js:94).
    // Passing the password as a variable is the plausible implementation and is
    // exactly what this forbids. Stringify the whole state so `data`, `context`
    // and any nested carrier are covered too.
    expect(JSON.stringify(m.state)).not.toContain(secret)
    expect(m.state.variables).toBeUndefined()
  }
  // Second store: the cleared inputs. Necessary but NOT sufficient - calling a
  // clear function is not evidence, which is why the cache assertion comes first.
  expect(screen.getByLabelText('New password')).toHaveValue('')
})
```

- [ ] **Step 2: Run the tests, and prove the last one discriminates**

Run: `npx vitest run src/profile/PasswordTab.auth.test.tsx`

Expected: all four **PASS** as written, because Tasks 2 and 4 already implemented the behaviour. **This task's RED cannot come from a missing implementation, so it names its substitute evidence: a mutate-and-revert, with both outputs recorded in the task report.**

Mutation A - the 403 path. Temporarily change `web/src/lib/api.ts:44` from `if (res.status === 401)` to `if (res.status === 401 || res.status === 403)`. Re-run. Expected: FAIL on `a 403 renders inline and leaves the user SIGNED IN` with `expected null to be 'tok_live'`. **Revert** and confirm green.

Mutation B - the secret retention. Temporarily change `PasswordTab.tsx`'s mutation to the plausible variable-carrying form:

```ts
    mutationFn: (v: { current: string; next: string }) => changePassword(v.current, v.next),
```

with the call site becoming `change.mutate({ current, next })`. Re-run. Expected: FAIL on `the settled mutation does not retain the plaintext password anywhere` with `expected '{"variables":{"current":"old-secret","next":"correct-horse-battery-staple"}...' not to contain 'correct-horse-battery-staple'`. **Revert** and confirm green.

If either mutation does not turn its test red, the test is vacuous - fix the test, do not proceed. Record both failure outputs verbatim in the task report; the permanent tests are what survive, not the mutations.

- [ ] **Step 3: No implementation step**

The behaviour is implemented in Tasks 2 and 4. This task's deliverable is the permanent test file plus the recorded mutation evidence.

- [ ] **Step 4: Run the whole profile directory**

Run: `npx vitest run src/profile/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/profile/PasswordTab.auth.test.tsx
git commit -m "test(web): a wrong current password is a 403 and must not sign the user out"
```

---

## Task 6: SessionsTab - the honest omission and the honest label

The tab holds **no query and no list**. `GET /v1/auth/tokens` does not exist, and `api_tokens` has only `id, user_id, token_hash, created_at, expires_at` - no `last_used_at`, no agent, no IP, no location. Omit what the backend cannot supply and say so, in the `EnrollmentsTab.tsx:197-205` house style.

**Files:**
- Create: `web/src/profile/SessionsTab.tsx`
- Test: `web/src/profile/SessionsTab.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/profile/SessionsTab.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { SessionsTab } from './SessionsTab'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function renderTab() {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/sessions']}>
        <AuthProvider>
          <SessionsTab />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

test('renders NO session list and issues no request for one', async () => {
  let listCalls = 0
  server.use(
    // Registered so a call is COUNTED. Without a handler, MSW's
    // onUnhandledRequest:'error' (web/src/test/setup.ts:5) would also fail the
    // test, but a counter names the route.
    http.get('/v1/auth/tokens', () => {
      listCalls++
      return HttpResponse.json({ items: [] })
    }),
  )
  renderTab()
  // GET /v1/auth/tokens is not registered anywhere in internal/api/server.go, and
  // api_tokens has no last_used_at, agent, IP or location column
  // (internal/store/migrations/000001_initial.up.sql:13-19). Mirrors
  // EnrollmentsTable.test.tsx:74-78.
  expect(listCalls).toBe(0)
  expect(screen.queryByRole('table')).toBeNull()
  expect(screen.queryByRole('columnheader')).toBeNull()
  expect(screen.queryByRole('button', { name: /revoke/i })).toBeNull()
  // Paired positive: the action IS present, so none of the four assertions above
  // can be passing against an empty component.
  expect(screen.getByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
})

test('the button is labelled "Sign out everywhere" and never says "else"', async () => {
  const { container } = renderTab()
  expect(screen.getByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
  // The hi-fi's label is "Sign out everywhere else" (hifi3-holo-pages.jsx:3049)
  // and describes an endpoint that does not exist: DeleteTokensForUser has no
  // `id <> $2` (internal/store/query/tokens.sql:25-26). Anyone implementing from
  // the mockup rather than the spec ships it. Assert over the whole subtree, not
  // just the accessible name, so it cannot hide in the copy either.
  expect(container.textContent).not.toMatch(/everywhere else/i)
  expect(screen.queryByRole('button', { name: /everywhere else/i })).toBeNull()
})

test('the page copy states that this browser is included', async () => {
  renderTab()
  expect(screen.getByTestId('sessions-blast-radius')).toHaveTextContent(
    /this browser|signed out here|including this/i,
  )
})

test('the omission footnote explains the missing endpoint and names the enabler', async () => {
  renderTab()
  const note = screen.getByTestId('sessions-omission-note')
  expect(note).toHaveTextContent(/no endpoint/i)
  expect(note).toHaveTextContent('GET /v1/auth/tokens')
})

test('the confirm dialog states the blast radius and the CLI consequence', async () => {
  renderTab()
  await userEvent.click(screen.getByRole('button', { name: 'Sign out everywhere' }))
  const dialog = await screen.findByRole('dialog')
  expect(dialog).toHaveTextContent(/this browser/i)
  expect(dialog).toHaveTextContent(/relay login/i)
})

test('Cancel in the dialog issues ZERO requests', async () => {
  let deletes = 0
  server.use(
    http.delete('/v1/auth/tokens', () => {
      deletes++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderTab()
  await userEvent.click(screen.getByRole('button', { name: 'Sign out everywhere' }))
  await screen.findByRole('dialog')
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(deletes).toBe(0)
  expect(screen.queryByRole('dialog')).toBeNull()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/profile/SessionsTab.test.tsx`

Expected: FAIL - `Failed to resolve import "./SessionsTab" from "src/profile/SessionsTab.test.tsx"`.

- [ ] **Step 3: Implement**

Create `web/src/profile/SessionsTab.tsx`:

```tsx
import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { GlassPanel, PillButton } from '../components/holo'
import { signOutEverywhere } from './api'

// The Sessions tab: ONE action, NO list.
//
// There is no GET /v1/auth/tokens anywhere in internal/api/server.go, and
// api_tokens has exactly five columns - id, user_id, token_hash, created_at,
// expires_at (internal/store/migrations/000001_initial.up.sql:13-19) - so the
// hi-fi's kind / agent / IP / location / last-active table (:3054-3113) is a
// migration plus an endpoint, not a query. The house rule is: omit what the
// backend cannot supply, and file the enabler (EnrollmentsTab.tsx:197-205,
// AdminPage.tsx:6-14).
//
// The ACTION, though, works: DELETE /v1/auth/tokens is a live, auth-gated,
// idempotent 204 (internal/api/auth.go:350-357). Applied faithfully the rule
// drops the list and keeps the control - dropping a working capability because an
// unrelated READ endpoint is missing would be over-applying it. And because this
// tab holds no query, there is no active observer to fire against a destroyed
// token when the session is torn down below; a Sessions LIST would have had to
// solve that ordering problem too.
export function SessionsTab() {
  const { clearSession } = useAuth()
  const navigate = useNavigate()
  const [confirming, setConfirming] = useState(false)

  const signOut = useMutation({
    mutationFn: () => signOutEverywhere(),
    // CLAUDE.md Invariant 1 - "end the generation before releasing the resource" -
    // read forwards. The resource is ALREADY gone by the time this runs: the
    // server has deleted every bearer token for this user (DeleteTokensForUser,
    // internal/store/query/tokens.sql:25-26). A 204 fires NO listener -
    // onUnauthorized is 401-only (lib/api.ts:44-46) - so until we act, the SPA
    // still holds a token in localStorage and still renders as authenticated
    // against a credential that no longer exists. So end the generation that
    // still believes in it before anything can observe it.
    //
    // THERE IS DELIBERATELY NO invalidateQueries HERE, and the omission is the
    // point. The house pattern for a mutation is
    // `onSuccess: () => qc.invalidateQueries({ queryKey: [...] })`
    // (useScheduleActions.ts:11). Here that would refetch every mounted query
    // against the destroyed credential - a burst of guaranteed 401s whose
    // onUnauthorized would race the teardown already in flight - and it would run
    // BEFORE anything unmounts, because a hook-level onSuccess resolves at
    // query-core mutation.js:123, ahead of the success dispatch and of any
    // mutate-level callback. clearSession() is the whole cleanup: it drops the
    // cache outright and flips status to 'anonymous', which makes ProtectedRoute
    // render <Navigate to="/auth" replace/> in the same commit and unmount every
    // active observer.
    //
    // logout() is deliberately NOT reused: it would first issue
    // DELETE /v1/auth/token (singular) against a token that no longer exists - a
    // guaranteed 401 whose onUnauthorized would race this same teardown.
    // SessionsTab.teardown.test.tsx asserts that request is never made.
    //
    // The explicit navigate is belt and braces: ProtectedRoute already redirects
    // on 'anonymous', but stating the destination keeps the intent readable and
    // keeps this component correct on its own.
    onSuccess: () => {
      clearSession()
      navigate('/auth')
    },
  })

  return (
    <div className="flex max-w-[720px] flex-col gap-3">
      <GlassPanel className="p-6">
        <div className="mb-4 flex items-baseline justify-between">
          <span className="text-[13px] text-fg">Active sessions</span>
          <span className="font-mono text-[10px] tracking-[0.06em] text-fg-dim">
            DELETE /v1/auth/tokens
          </span>
        </div>

        {/* The verified blast radius. DeleteTokensForUser is
            `DELETE FROM api_tokens WHERE user_id = $1` with no `id <> $2`
            (internal/store/query/tokens.sql:25-26), so this browser goes too. A
            control that understates its own blast radius is worse than a missing
            one. */}
        <p
          data-testid="sessions-blast-radius"
          className="mb-4 text-[12.5px] leading-relaxed text-fg-mute"
        >
          Signing out everywhere revokes <b>every</b> bearer token on your account,{' '}
          <b>including this browser</b>. You will be returned to sign-in here, and any{' '}
          <span className="font-mono">relay</span> CLI login will need{' '}
          <span className="font-mono">relay login</span> again.
        </p>

        {signOut.error && (
          <div role="alert" className="mb-3 text-[11px] text-err">
            {signOut.error.message}
          </div>
        )}

        <PillButton
          variant="danger"
          disabled={signOut.isPending}
          onClick={() => setConfirming(true)}
        >
          Sign out everywhere
        </PillButton>
      </GlassPanel>

      <div className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
        ▸ There is no per-session list here because the server exposes no endpoint to enumerate
        tokens: <span className="text-fg-mute">GET /v1/auth/tokens</span> is not registered, and
        the <span className="text-fg-mute">api_tokens</span> table has no last-used, agent or IP
        column to populate one. Tracked at{' '}
        <span className="text-fg-mute">
          docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md
        </span>
        . Until it lands, signing out everywhere is the only session control, and it is
        all-or-nothing.
      </div>

      {confirming && (
        <ConfirmDialog
          title="Sign out everywhere?"
          body={
            'This revokes every bearer token on your account, including this browser - you will be returned to sign-in immediately. Any relay CLI login will need relay login again. Nothing else is deleted.'
          }
          confirmLabel="Sign out everywhere"
          destructive
          onConfirm={() => {
            // Close the dialog BEFORE firing. A mutation error rendered on the
            // page while a modal is open sits behind that modal's fixed inset-0
            // z-50 scrim, so the button would appear to do nothing. On success
            // this component is unmounted by the redirect anyway.
            setConfirming(false)
            signOut.mutate()
          }}
          onCancel={() => setConfirming(false)}
        />
      )}
    </div>
  )
}
```

Note for the implementer: the footnote's `data-testid` is on the mono `div`. Add `data-testid="sessions-omission-note"` to it - the block above omits it only to keep the JSX readable, so put it on the `<div className="font-mono text-[10px] ...">` element.

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/profile/SessionsTab.test.tsx`

Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/profile/SessionsTab.tsx web/src/profile/SessionsTab.test.tsx
git commit -m "feat(web): profile Sessions tab, action only with an honest omission note"
```

---

## Task 7: The sign-out-everywhere teardown

**HIGH-RISK TASK 2 of 2, and the highest-value test in the slice.** Firing the DELETE and navigating - without `clearSession()` - looks correct, demos correctly, and leaves a live-looking session holding a dead token that only fails later, at a random request, as a confusing bounce to sign-in.

This is CLAUDE.md **Invariant 1** in its frontend form. Read the invariant and `docs/retros/2026-08-12-schedule-detail-page.md` Problem 1 before writing this task: the immediately preceding PR shipped a HIGH of exactly this shape, where an `invalidateQueries` refetched a just-deleted row before `navigate` released the page. The generalization from that retro applies verbatim here: **an invalidation is a continuation**, and the tell is not an `abort()`/`close()`/`cancel()` call to grep for - it is a broad key prefix that still matches something that has ceased to exist. Here the thing that ceased to exist is not a row, it is the credential every query would use.

**Files:**
- Create: `web/src/profile/SessionsTab.teardown.test.tsx`
- No implementation file. The behaviour was implemented in Task 6; see Step 2.

- [ ] **Step 1: Write the test**

Create `web/src/profile/SessionsTab.teardown.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider, useAuth } from '../auth/AuthProvider'
import { apiFetch } from '../lib/api'
import { clearToken, getToken, setToken } from '../lib/token'
import { SessionsTab } from './SessionsTab'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function SessionProbe() {
  const { status } = useAuth()
  return <span data-testid="status">{status}</span>
}

// A query with an ACTIVE OBSERVER, mounted alongside the tab. Its mount-time
// fetch is the positive control that proves the observer is live; without it the
// "no further calls" assertion in the third test would be vacuous, since a query
// that never had an observer cannot be refetched by invalidateQueries' default
// refetchType:'active' either way.
function StatsProbe() {
  const q = useQuery({
    queryKey: ['jobs', 'stats'],
    queryFn: () => apiFetch<{ running: number }>('/jobs/stats'),
  })
  return <span data-testid="stats">{q.data ? 'loaded' : 'pending'}</span>
}

function renderTab() {
  setToken('tok_live')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/sessions']}>
        <AuthProvider>
          <SessionProbe />
          <Routes>
            <Route
              path="/profile/sessions"
              element={
                <>
                  <StatsProbe />
                  <SessionsTab />
                </>
              }
            />
            <Route path="/auth" element={<div>SIGN IN SCREEN</div>} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

async function confirmSignOut() {
  await userEvent.click(screen.getByRole('button', { name: 'Sign out everywhere' }))
  await screen.findByRole('dialog')
  // The dialog's own confirm button, not the page's trigger. ConfirmDialog gives
  // the confirm button the label passed as confirmLabel, so both carry the same
  // accessible name while the dialog is open - take the one inside the dialog.
  const dialog = screen.getByRole('dialog')
  const confirm = within(dialog).getByRole('button', { name: 'Sign out everywhere' })
  await userEvent.click(confirm)
}

test('on 204 the token, the user, the cache AND the route are all torn down', async () => {
  server.use(
    http.delete('/v1/auth/tokens', () => new HttpResponse(null, { status: 204 })),
    http.get('/v1/jobs/stats', () => HttpResponse.json({ running: 1 })),
  )
  const { client } = renderTab()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('authenticated'))
  await waitFor(() => expect(screen.getByTestId('stats')).toHaveTextContent('loaded'))

  // Paired positive BEFORE the action: all four stores are in the pre-state, so
  // every assertion after the action is about the teardown.
  expect(getToken()).toBe('tok_live')
  expect(client.getQueryCache().getAll().length).toBeGreaterThan(0)
  expect(screen.queryByText('SIGN IN SCREEN')).toBeNull()

  await confirmSignOut()

  // FOUR assertions, not one. A test asserting only the navigation passes against
  // an implementation that leaves a live token in localStorage - which is the
  // exact defect this task exists to prevent, and it only surfaces later, at a
  // random request, as a confusing bounce to sign-in.
  await waitFor(() => expect(getToken()).toBeNull())
  expect(await screen.findByText('SIGN IN SCREEN')).toBeInTheDocument()
  expect(screen.getByTestId('status')).toHaveTextContent('anonymous')
  expect(client.getQueryCache().getAll()).toHaveLength(0)
})

test('exactly one DELETE to the PLURAL path, and NEVER the singular one', async () => {
  let plural = 0
  let singular = 0
  server.use(
    http.delete('/v1/auth/tokens', () => {
      plural++
      return new HttpResponse(null, { status: 204 })
    }),
    // Returns 204, not 401, so a stray call is COUNTED rather than cascading into
    // an onUnauthorized teardown that would mask the defect by producing the
    // right-looking end state for the wrong reason.
    http.delete('/v1/auth/token', () => {
      singular++
      return new HttpResponse(null, { status: 204 })
    }),
    http.get('/v1/jobs/stats', () => HttpResponse.json({ running: 1 })),
  )
  renderTab()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('authenticated'))

  await confirmSignOut()
  await waitFor(() => expect(getToken()).toBeNull())

  expect(plural).toBe(1)
  // The tell that logout() was reused instead of clearSession(). logout() fires
  // DELETE /v1/auth/token against a token the server destroyed a moment ago - a
  // guaranteed 401 in production, whose onUnauthorized would race the teardown
  // already in flight.
  expect(singular).toBe(0)
})

test('no query refetches against the destroyed credential (Invariant 1)', async () => {
  let statsCalls = 0
  server.use(
    http.delete('/v1/auth/tokens', () => new HttpResponse(null, { status: 204 })),
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 1 })
    }),
  )
  renderTab()
  await waitFor(() => expect(screen.getByTestId('stats')).toHaveTextContent('loaded'))
  // Positive control: the observer is live and DID fetch once. Without this the
  // equality below could be about a query that was never mounted.
  expect(statsCalls).toBe(1)

  await confirmSignOut()
  await waitFor(() => expect(getToken()).toBeNull())
  await screen.findByText('SIGN IN SCREEN')

  // The whole invariant in one number. A hook-level onSuccess that invalidated a
  // broad key would refetch this query BEFORE the navigation - the callback runs
  // at query-core mutation.js:123, ahead of the success dispatch and of any
  // unmount - and every one of those requests would carry a credential the server
  // has already destroyed.
  expect(statsCalls).toBe(1)
})
```

Add `within` to the Testing Library import: `import { render, screen, waitFor, within } from '@testing-library/react'`.

- [ ] **Step 2: Run the tests, and prove the third one discriminates**

Run: `npx vitest run src/profile/SessionsTab.teardown.test.tsx`

Expected: the first two **PASS** as written, because Task 6 already implemented the behaviour. The third also passes. **This task's RED cannot come from a missing implementation, so it names its substitute evidence: a mutate-and-revert per test, with all outputs recorded.**

Mutation A - drop the teardown. In `SessionsTab.tsx`, temporarily replace the mutation's `onSuccess` body with `navigate('/auth')` alone (delete the `clearSession()` line). Re-run. Expected: FAIL on the first test with `expected 'tok_live' to be null`, and the cache and status assertions failing too. **Revert** and confirm green.

Mutation B - reuse `logout()`. Temporarily change the `onSuccess` body to:

```ts
      const { logout } = useAuth()   // hoist this to the component body
      void logout().then(() => navigate('/auth'))
```

Re-run. Expected: FAIL on the second test with `expected 0 to be 1` inverted - specifically `expect(singular).toBe(0)` failing with `expected 1 to be 0`. **Revert** and confirm green.

Mutation C - the invariant. Temporarily add a `useQueryClient()` and change the `onSuccess` body to the plausible house-pattern form:

```ts
      qc.invalidateQueries({ queryKey: ['jobs'] })
      clearSession()
      navigate('/auth')
```

Re-run. Expected: FAIL on the third test with `expected 2 to be 1` (or higher). **Revert** and confirm green.

If any mutation does not turn its test red, that test is vacuous - fix it, do not proceed. For Mutation C specifically, if `statsCalls` does not increase, check the positive control first: the observer must have fetched once at mount, or the query was never active and the test proves nothing. Record all three failure outputs verbatim in the task report.

- [ ] **Step 3: No implementation step**

The behaviour is implemented in Task 6. This task's deliverable is the permanent test file plus the recorded mutation evidence.

- [ ] **Step 4: Run the whole profile directory**

Run: `npx vitest run src/profile/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/profile/SessionsTab.teardown.test.tsx
git commit -m "test(web): sign-out-everywhere ends the session generation before releasing it"
```

---

## Task 8: The tab registry and the tab bar

Mirror `web/src/admin/tabs.ts:21-32` and `web/src/admin/AdminTabs.tsx:9-29`. Second consumer; nothing is extracted (see the SECOND-CONSUMER FLAG above).

**Files:**
- Create: `web/src/profile/tabs.ts`
- Create: `web/src/profile/ProfileTabs.tsx`
- Test: `web/src/profile/ProfileTabs.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/profile/ProfileTabs.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { DEFAULT_PROFILE_TAB, PROFILE_TABS, findProfileTab } from './tabs'
import { ProfileTabs } from './ProfileTabs'

function renderTabs(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ProfileTabs />
    </MemoryRouter>,
  )
}

test('the registry holds exactly the three built tabs, defaulting to identity', () => {
  expect(PROFILE_TABS.map((t) => t.slug)).toEqual(['identity', 'password', 'sessions'])
  expect(DEFAULT_PROFILE_TAB).toBe('identity')
})

test('findProfileTab resolves a known slug and rejects everything else', () => {
  expect(findProfileTab('identity')?.label).toBe('Identity')
  expect(findProfileTab('password')?.label).toBe('Password')
  expect(findProfileTab('sessions')?.label).toBe('Sessions')
  // The hi-fi's first slug is 'profile' (hifi3-holo-pages.jsx:2817), which would
  // make the URL /profile/profile. It must NOT resolve; the shell redirects it to
  // /profile/identity like any other unknown segment.
  expect(findProfileTab('profile')).toBeUndefined()
  expect(findProfileTab('bogus')).toBeUndefined()
  expect(findProfileTab(undefined)).toBeUndefined()
})

test('renders one link per registry entry, pointing at /profile/<slug>', () => {
  renderTabs('/profile/identity')
  expect(screen.getByRole('link', { name: 'Identity' })).toHaveAttribute(
    'href',
    '/profile/identity',
  )
  expect(screen.getByRole('link', { name: 'Password' })).toHaveAttribute(
    'href',
    '/profile/password',
  )
  expect(screen.getByRole('link', { name: 'Sessions' })).toHaveAttribute(
    'href',
    '/profile/sessions',
  )
  expect(screen.getAllByRole('link')).toHaveLength(3)
})

test('the current tab carries aria-current="page" and the others do not', () => {
  renderTabs('/profile/password')
  expect(screen.getByRole('link', { name: 'Password' })).toHaveAttribute('aria-current', 'page')
  expect(screen.getByRole('link', { name: 'Identity' })).not.toHaveAttribute('aria-current')
  expect(screen.getByRole('link', { name: 'Sessions' })).not.toHaveAttribute('aria-current')
})

test('the sessions tab is marked current on its own route', () => {
  renderTabs('/profile/sessions')
  expect(screen.getByRole('link', { name: 'Sessions' })).toHaveAttribute('aria-current', 'page')
})

test('no count badge is rendered on Sessions', () => {
  renderTabs('/profile/sessions')
  // The hi-fi puts a session count on this pill (hifi3-holo-pages.jsx:2819). We
  // could not supply one: GET /v1/auth/tokens does not exist. AdminTabs omits
  // badges for a related reason (AdminTabs.tsx:6-8). Both directions: the label
  // is exactly 'Sessions', with no digits anywhere in the bar.
  expect(screen.getByRole('link', { name: 'Sessions' })).toHaveTextContent(/^Sessions$/)
  expect(screen.getByRole('link', { name: 'Sessions' }).textContent).not.toMatch(/\d/)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/profile/ProfileTabs.test.tsx`

Expected: FAIL - `Failed to resolve import "./tabs" from "src/profile/ProfileTabs.test.tsx"`.

- [ ] **Step 3: Implement**

Create `web/src/profile/tabs.ts`:

```ts
import type { ComponentType } from 'react'
import { IdentityTab } from './IdentityTab'
import { PasswordTab } from './PasswordTab'
import { SessionsTab } from './SessionsTab'

export interface ProfileTab {
  slug: string
  label: string
  Panel: ComponentType
}

// A registry plus a switch, mirroring web/src/admin/tabs.ts:21-32. This is the
// SECOND consumer of that shape and the house rule is extract before the THIRD,
// so no shared tab primitive is extracted here - recorded so the next tabbed
// surface triggers the extraction. The two are not parameterizable today without
// inventing options neither needs: admin sits behind AdminRoute and profile does
// not (every endpoint here is auth(...), never AdminOnly -
// internal/api/server.go:97-100, :153), and the hi-fi wants a count badge on
// Sessions that AdminTabs deliberately does not render and that we could not
// supply anyway, since GET /v1/auth/tokens does not exist.
//
// Slug note: the hi-fi's first slug is 'profile' (hifi3-holo-pages.jsx:2817),
// which would make the URL /profile/profile. 'identity' matches the panel's own
// name in the design (ProfileIdentity, :2909) and the backlog item's title.
export const PROFILE_TABS: ProfileTab[] = [
  { slug: 'identity', label: 'Identity', Panel: IdentityTab },
  { slug: 'password', label: 'Password', Panel: PasswordTab },
  { slug: 'sessions', label: 'Sessions', Panel: SessionsTab },
]

export const DEFAULT_PROFILE_TAB = 'identity'

export function findProfileTab(slug: string | undefined): ProfileTab | undefined {
  return PROFILE_TABS.find((t) => t.slug === slug)
}
```

Create `web/src/profile/ProfileTabs.tsx`:

```tsx
import { NavLink } from 'react-router-dom'
import { PROFILE_TABS } from './tabs'

// The hi-fi's pill-group tab bar, identical in construction to AdminTabs.tsx:9-29:
// rounded-full, dark translucent, active tab filled with the accent gradient.
// NavLink supplies the active state and aria-current="page".
//
// The hi-fi's count badge on Sessions (hifi3-holo-pages.jsx:2819) is NOT
// rendered: the number would be a count of active bearer tokens, and no endpoint
// returns it.
export function ProfileTabs() {
  return (
    <div className="flex gap-1.5 self-start rounded-full border border-border bg-black/30 p-[3px] backdrop-blur-[8px]">
      {PROFILE_TABS.map((t) => (
        <NavLink
          key={t.slug}
          to={`/profile/${t.slug}`}
          className={({ isActive }) =>
            `rounded-full px-3.5 py-1.5 text-[12px] tracking-[0.02em] transition-colors ${
              isActive
                ? 'bg-gradient-to-r from-accent to-accent-b font-semibold text-bg'
                : 'text-fg-mute hover:text-fg'
            }`
          }
        >
          {t.label}
        </NavLink>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/profile/ProfileTabs.test.tsx`

Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/profile/tabs.ts web/src/profile/ProfileTabs.tsx web/src/profile/ProfileTabs.test.tsx
git commit -m "feat(web): profile tab registry and pill tab bar"
```

---

## Task 9: ProfilePage - header, meta strip and the tab switch

Mirror `web/src/admin/AdminPage.tsx:15-40`. The meta strip is `EMAIL`, `ROLE`, `MEMBER SINCE` and **nothing else**.

**Files:**
- Create: `web/src/profile/ProfilePage.tsx`
- Test: `web/src/profile/ProfilePage.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/profile/ProfilePage.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { ProfilePage } from './ProfilePage'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function renderAt(path: string, me: Record<string, unknown> = ME) {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(me)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/profile/:tab" element={<ProfilePage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('renders the eyebrow, the initials avatar, the name heading and the tab bar', async () => {
  renderAt('/profile/identity')
  expect(await screen.findByRole('heading', { level: 1, name: /Mira Sato/ })).toBeInTheDocument()
  expect(screen.getByText('YOUR ACCOUNT')).toBeInTheDocument()
  expect(screen.getByTestId('profile-initials')).toHaveTextContent('MI')
  expect(screen.getByRole('link', { name: 'Identity' })).toHaveAttribute(
    'aria-current',
    'page',
  )
})

test('the meta strip shows EMAIL, ROLE and MEMBER SINCE with real values', async () => {
  renderAt('/profile/identity')
  await screen.findByRole('heading', { level: 1, name: /Mira Sato/ })
  expect(screen.getByText('EMAIL')).toBeInTheDocument()
  expect(screen.getByTestId('meta-email')).toHaveTextContent('mira@studio.dev')
  expect(screen.getByTestId('meta-role')).toHaveTextContent('ADMIN')
  // Assert the VALUE, not just the label. created_at is a real runtime dependency
  // on every fixture: type-checking cannot catch a fixture missing it (every
  // fixture is an untyped HttpResponse.json literal), and the house rendering is
  // a string slice (UsersTable.tsx:123), so a missing field throws rather than
  // silently rendering Invalid Date. Either way, only a value assertion catches it.
  expect(screen.getByTestId('meta-member-since')).toHaveTextContent('2025-04-02')
})

test('the meta strip shows USER for a non-admin (paired control on ROLE)', async () => {
  renderAt('/profile/identity', { ...ME, is_admin: false })
  await screen.findByRole('heading', { level: 1, name: /Mira Sato/ })
  expect(screen.getByTestId('meta-role')).toHaveTextContent('USER')
})

test('renders NO unbacked activity facts', async () => {
  renderAt('/profile/identity')
  await screen.findByRole('heading', { level: 1, name: /Mira Sato/ })
  // No column, no endpoint, no proxy: the users table has no last-login field and
  // api_tokens.created_at is issuance, not login, and is unreadable anyway
  // without GET /v1/auth/tokens. Rendering "-" for three of four rows is the
  // VERSION/BUILD strip mistake (AdminPage.tsx:6-14).
  for (const label of ['LAST LOGIN', 'LOGIN COUNT', 'ACTIVE SESSIONS', 'Activity']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
})

test('initials handle a one-word name and collapse extra whitespace', async () => {
  renderAt('/profile/identity', { ...ME, name: 'Ada' })
  await screen.findByRole('heading', { level: 1, name: /Ada/ })
  expect(screen.getByTestId('profile-initials')).toHaveTextContent('A')
})

test('an unknown tab segment redirects to identity', async () => {
  render(
    (() => {
      setToken('tok')
      server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
      const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
      return (
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/profile/nope']}>
            <AuthProvider>
              <Routes>
                <Route path="/profile/:tab" element={<ProfilePage />} />
              </Routes>
            </AuthProvider>
          </MemoryRouter>
        </QueryClientProvider>
      )
    })(),
  )
  // The redirect lands on /profile/identity, which the same route renders.
  expect(await screen.findByLabelText('Display name')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Identity' })).toHaveAttribute('aria-current', 'page')
})

test('each tab route renders its own panel and not the others', async () => {
  renderAt('/profile/password')
  expect(await screen.findByLabelText('Current password')).toBeInTheDocument()
  expect(screen.queryByLabelText('Display name')).toBeNull()
  expect(screen.queryByRole('button', { name: 'Sign out everywhere' })).toBeNull()
})

test('the sessions route renders the sessions panel', async () => {
  renderAt('/profile/sessions')
  expect(await screen.findByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
  expect(screen.queryByLabelText('Display name')).toBeNull()
})

test('a successful rename updates the HEADING, not just the input', async () => {
  server.use(http.patch('/v1/users/me', () => HttpResponse.json({ ...ME, name: 'Mira Renamed' })))
  renderAt('/profile/identity')
  const input = await screen.findByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'Mira Renamed')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  // The header is fed from AuthProvider, so this is the end-to-end proof that
  // applyUser ran. A component that only set local form state passes an
  // input-reading test and leaves the header stale for the rest of the session.
  await waitFor(() =>
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Mira Renamed'),
  )
  // The initials follow the same source.
  expect(screen.getByTestId('profile-initials')).toHaveTextContent('MR')
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/profile/ProfilePage.test.tsx`

Expected: FAIL - `Failed to resolve import "./ProfilePage" from "src/profile/ProfilePage.test.tsx"`.

- [ ] **Step 3: Implement**

Create `web/src/profile/ProfilePage.tsx`:

```tsx
import { Navigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { Eyebrow } from '../components/holo'
import { ProfileTabs } from './ProfileTabs'
import { DEFAULT_PROFILE_TAB, findProfileTab } from './tabs'

// Up to two initials from the display name. Splits on runs of whitespace and
// drops empties, so an interior double space (which the server preserves - it
// only trims the ends, internal/api/users.go:61) cannot produce `undefined`.
function initialsOf(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .map((part) => part[0])
    .join('')
    .slice(0, 2)
    .toUpperCase()
}

// The profile shell. Mirrors AdminPage.tsx:15-40: registry, switch, redirect on
// anything unknown.
//
// The hi-fi's meta strip has a fourth entry, LAST LOGIN (hifi3-holo-pages.jsx:2846),
// and a whole Activity side card (:2946-2965). Both are omitted, and that is a
// decision rather than a deferral: the users table has no last-login column, there
// is no login counter, and a session count would need GET /v1/auth/tokens, which
// does not exist. MEMBER SINCE - the one real value - moves into this strip, where
// it stands on its own. Rendering "-" for facts the backend cannot supply is the
// VERSION/BUILD/DB/UPTIME mistake the admin console already avoided (AdminPage.tsx:6-14).
//
// The hi-fi's "<- BACK" link (:2829) is also omitted: it is a prototype router
// artifact, and the app's real back affordance is the persistent shell nav.
export function ProfilePage() {
  const { tab } = useParams()
  const { user } = useAuth()

  // No :tab segment means "use the default" - render it directly rather than
  // redirecting to the same path, which would be a Navigate that renders nothing
  // forever. Anything unknown redirects, so the surface can never show a dead tab.
  const active = tab === undefined ? findProfileTab(DEFAULT_PROFILE_TAB) : findProfileTab(tab)
  if (!active) return <Navigate to={`/profile/${DEFAULT_PROFILE_TAB}`} replace />
  // ProtectedRoute blanks the page while status is 'loading', so in the app this
  // is never null by the time the route renders. The guard keeps the component
  // correct on its own.
  if (!user) return null

  const Panel = active.Panel

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <Eyebrow>YOUR ACCOUNT</Eyebrow>
          <h1 className="flex items-center gap-3.5 text-[32px] font-normal tracking-tight">
            <span
              data-testid="profile-initials"
              aria-hidden="true"
              className="grid h-10 w-10 place-items-center rounded-[10px] bg-gradient-to-br from-accent to-accent-b text-[15px] font-bold tracking-[0.04em] text-bg"
            >
              {initialsOf(user.name)}
            </span>
            <span>{user.name}</span>
          </h1>
        </div>
        <div className="ml-auto flex gap-3.5 font-mono text-[11px] tracking-[0.06em] text-fg-mute">
          <div className="flex flex-col items-end gap-px">
            <span className="text-[9px] tracking-[0.16em]">EMAIL</span>
            <span data-testid="meta-email" className="text-[12px] text-fg">
              {user.email}
            </span>
          </div>
          <div className="flex flex-col items-end gap-px">
            <span className="text-[9px] tracking-[0.16em]">ROLE</span>
            <span data-testid="meta-role" className="text-[12px] text-fg">
              {user.is_admin ? 'ADMIN' : 'USER'}
            </span>
          </div>
          <div className="flex flex-col items-end gap-px">
            <span className="text-[9px] tracking-[0.16em]">MEMBER SINCE</span>
            {/* The house rendering for an absolute date: a string slice, matching
                UsersTable.tsx:123. No Date parsing, so there is no timezone
                behaviour to get wrong, and a fixture missing the field throws
                loudly instead of rendering "Invalid Date". */}
            <span data-testid="meta-member-since" className="text-[12px] text-fg">
              {user.created_at.slice(0, 10)}
            </span>
          </div>
        </div>
      </div>
      <ProfileTabs />
      <div className="flex min-h-0 flex-1 flex-col gap-3">
        <Panel />
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/profile/`

Expected: PASS for the whole directory, 9 new tests in `ProfilePage.test.tsx` plus everything from Tasks 2-8.

- [ ] **Step 5: Commit**

```bash
git add web/src/profile/ProfilePage.tsx web/src/profile/ProfilePage.test.tsx
git commit -m "feat(web): profile page shell with the honest meta strip"
```

---

## Task 10: Wire the routes and retire the placeholder

Three dead `UserMenu` links become live. `UserMenu.tsx` itself is **not modified** - `/profile` gets its own route that redirects, exactly as `/admin` does (`router.tsx:38`).

**Files:**
- Modify: `web/src/app/router.tsx:4`, `:41`
- Delete: `web/src/app/JobsPlaceholder.tsx`
- Test: `web/src/profile/ProfileRoutes.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/profile/ProfileRoutes.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { AppRoutes } from '../app/router'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: false,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function renderApp(path: string) {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('/profile - the UserMenu Profile link - lands on the Identity tab', async () => {
  // UserMenu.tsx:60 links here and is NOT modified by this slice.
  renderApp('/profile')
  expect(await screen.findByLabelText('Display name')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Identity' })).toHaveAttribute('aria-current', 'page')
})

test('/profile/password - the UserMenu Password link - lands on the Password tab', async () => {
  // UserMenu.tsx:64.
  renderApp('/profile/password')
  expect(await screen.findByLabelText('Current password')).toBeInTheDocument()
})

test('/profile/sessions - the UserMenu Sessions link - lands on the Sessions tab', async () => {
  // UserMenu.tsx:70.
  renderApp('/profile/sessions')
  expect(await screen.findByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
})

test('an unknown /profile/:tab redirects to identity', async () => {
  renderApp('/profile/nope')
  expect(await screen.findByLabelText('Display name')).toBeInTheDocument()
})

test('the placeholder is unreachable from every /profile route', async () => {
  // The exact text JobsPlaceholder rendered. This is the acceptance criterion
  // that the three dead links are actually dead no longer; asserted on all four
  // routes so a partial wiring cannot pass.
  for (const path of ['/profile', '/profile/identity', '/profile/password', '/profile/sessions']) {
    const { unmount } = renderApp(path)
    expect(await screen.findByText('YOUR ACCOUNT')).toBeInTheDocument()
    expect(screen.queryByText(/coming soon/i)).toBeNull()
    unmount()
  }
})

test('the profile routes are NOT admin-gated - a non-admin reaches all three', async () => {
  // Every endpoint behind this page is auth(...), never AdminOnly
  // (internal/api/server.go:97-100, :153). Putting these routes behind AdminRoute
  // would lock out exactly the users who need them, and the ME fixture above is
  // deliberately is_admin: false so every test in this file is that assertion.
  renderApp('/profile/sessions')
  expect(await screen.findByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
  expect(screen.getByTestId('meta-role')).toHaveTextContent('USER')
})
```

Before writing the last-but-one test, open `web/src/app/JobsPlaceholder.tsx` and use **its actual rendered text** in the `queryByText` assertion rather than the `/coming soon/i` shown here if it differs. The point of the assertion is that the placeholder's own copy is absent; a regex that matches nothing in either the old or the new component would be vacuous. Record the string you used.

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/profile/ProfileRoutes.test.tsx`

Expected: FAIL on all six tests. The first four fail with `Unable to find a label with the text of: Display name` (the `/profile/*` splat still renders `JobsPlaceholder`); the fifth fails with `Unable to find an element with the text: YOUR ACCOUNT`.

- [ ] **Step 3: Implement**

In `web/src/app/router.tsx`, delete the import at `:4`:

```tsx
import { JobsPlaceholder } from './JobsPlaceholder'
```

add next to the other page imports:

```tsx
import { ProfilePage } from '../profile/ProfilePage'
```

and replace the route at `:41`:

```tsx
        <Route path="/profile/*" element={<JobsPlaceholder />} />
```

with:

```tsx
        {/* No AdminRoute: every endpoint behind this page is auth(...) and acts
            on the identity in the bearer token, never on an id from a path or a
            body (internal/api/server.go:97-100, :153). Gating it on admin would
            lock out exactly the users who need it. Same two-route shape as
            /admin above, so UserMenu's /profile link resolves without change. */}
        <Route path="/profile" element={<Navigate to="/profile/identity" replace />} />
        <Route path="/profile/:tab" element={<ProfilePage />} />
```

`Navigate` is already imported at `:1`. Then delete the now-unreferenced placeholder:

```bash
git rm web/src/app/JobsPlaceholder.tsx
```

Before deleting, confirm it has no other importer:

```bash
git grep -n "JobsPlaceholder" -- web/src
```

Expected after the router edit: **no output**. If anything else references it, keep the file and say so in the task report.

- [ ] **Step 4: Run the tests to verify they pass**

```
npx vitest run src/profile/ src/app/
```

Expected: PASS for both directories. `src/app/` holds `AdminRoute.test.tsx` and `ProtectedRoute.test.tsx`, which must be unaffected - they render the guards directly, not `AppRoutes`.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/router.tsx web/src/profile/ProfileRoutes.test.tsx
git rm web/src/app/JobsPlaceholder.tsx
git commit -m "feat(web): route /profile/:tab to the profile page and retire the placeholder"
```

---

## Task 11: Verification gate

- [ ] **Step 1: Full web suite**

From `web/`:

```
npm test
```

Expected: PASS, zero failures. The last reported baseline was 890 tests after the schedule-detail slice; this slice adds roughly 60, so expect about 950. **Measure the baseline yourself before starting rather than trusting either number.** Any pre-existing failure must be measured **both with and without** this change before it is called pre-existing - never merge past a red gate on the strength of an assumption. Note the standing lesson from the previous slice: a set of new test files is itself a load change, and "new tests destabilized an old test" is a first-class outcome to measure, not noise to wait out.

- [ ] **Step 2: Type-check and production build**

From `web/`:

```
npm run build
```

Expected: `tsc -b` clean, then a successful `vite build`. A TypeScript error here that the test run did not catch is real: vitest transpiles without type-checking, so the `User.created_at` addition and the two new `AuthContextValue` members are only checked at this step.

- [ ] **Step 3: Revert the build output**

`web/dist` is **tracked but stale** from the original scaffold and is not maintained per-PR. `npm run build` rewrites it and dirties the working tree. From the repo root:

```bash
git checkout -- web/dist/
git status --short
```

Expected: `web/dist/` shows no modifications.

- [ ] **Step 4: Go gate (proving no backend regression)**

`make` is **not on PATH in this shell**. From the repo root, run the tools directly:

```
go build ./...
go test ./...
```

Expected: PASS. This slice changes zero Go files, so a failure here is unrelated to it - but run it, and if it is red, get a number with and without the change rather than assuming.

Integration tests are **not** required: no Go file, no `.sql` file and no migration changed, so there is no `make generate` step and no database surface is touched.

- [ ] **Step 5: Confirm the change set**

```bash
git status --short
git diff --stat origin/main...HEAD
```

Expected file set, and nothing else:

```
web/src/lib/types.ts
web/src/auth/AuthProvider.tsx
web/src/auth/AuthProvider.session.test.tsx
web/src/app/router.tsx
web/src/app/JobsPlaceholder.tsx            (deleted)
web/src/profile/api.ts
web/src/profile/api.test.ts
web/src/profile/IdentityTab.tsx
web/src/profile/IdentityTab.test.tsx
web/src/profile/PasswordTab.tsx
web/src/profile/PasswordTab.test.tsx
web/src/profile/PasswordTab.auth.test.tsx
web/src/profile/SessionsTab.tsx
web/src/profile/SessionsTab.test.tsx
web/src/profile/SessionsTab.teardown.test.tsx
web/src/profile/tabs.ts
web/src/profile/ProfileTabs.tsx
web/src/profile/ProfileTabs.test.tsx
web/src/profile/ProfilePage.tsx
web/src/profile/ProfilePage.test.tsx
web/src/profile/ProfileRoutes.test.tsx
```

**`web/src/auth/AuthProvider.test.tsx` and `web/src/shell/UserMenu.tsx` must not appear in that list.** Confirm explicitly:

```bash
git diff --stat origin/main...HEAD -- web/src/auth/AuthProvider.test.tsx web/src/shell/UserMenu.tsx
```

Expected: no output.

Plus, at the phase boundary, the `git mv` of `docs/backlog/feature-2026-06-26-profile-identity-password-sessions.md` into `docs/backlog/closed/` performed by `/backlog close feature-2026-06-26-profile-identity-password-sessions`. That `git mv` is required scope, not optional cleanup. Grep the tree for the slug in the same commit and repair any inbound reference:

```bash
git grep -n "feature-2026-06-26-profile-identity-password-sessions" -- docs
```

---

## Phase 6 proposals (propose, do NOT auto-file)

Three items, matching the spec's Scoped out table. Each is a **proposal** for human accept; nothing is auto-filed.

1. **Amend the existing enabler** `docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md:25-27`. It asks for `GET /v1/auth/tokens` returning "created_at, last_used_at, current-session flag" as though `last_used_at` were queryable. It is not: `api_tokens` has exactly five columns - `id, user_id, token_hash, created_at, expires_at` (`internal/store/migrations/000001_initial.up.sql:13-19`) - so `last_used_at`, and any agent/IP/location attribution, require a **migration first**. The amendment should state that, state that the minimum useful list is `id`, `created_at`, `expires_at` and a current-session flag, and fold in the per-session revoke: `DELETE /v1/auth/token/{id}` does not exist (the real route takes no id, `server.go:99`), and a list without per-row revoke is half a feature that needs the same new route family.
2. `idea-2026-08-12-sign-out-others-endpoint.md` (**idea/low**) - a `DELETE /v1/auth/tokens?keep_current=true` arm, or a distinct route, giving the "sign out everywhere **else**" semantics the hi-fi assumed (`hifi3-holo-pages.jsx:2796,3049`). The query already exists and is already called: `DeleteOtherTokensForUser` (`internal/store/query/tokens.sql:28-29`) is what the password path uses (`internal/api/auth.go:325-328`). Only a route does not exist.
3. `idea-2026-08-12-user-last-login-tracking.md` (**idea/low**) - a `users.last_login_at` touched by `handleLogin`, which would revive the meta strip's `LAST LOGIN` and part of the hi-fi's Activity card. Note it overlaps the already-filed audit-log discussion from the Users-tab spec; whoever picks it up should check for duplication first.

**Considered and deliberately NOT filed**, so the next person does not re-derive it: a password strength meter (a complexity policy is a product/security decision for the backend, not a UI gap - if a policy is ever added, the meter follows it); the "Forgot your password?" side card (accurate but aimed at a locked-out user who cannot reach a page behind the login wall); avatar upload (no column, not in the hi-fi); extracting a shared tab-shell primitive (second consumer, recorded in `tabs.ts`); and extracting the min-8 guard (two lines with no decision inside them).

---

## Self-review

**Spec coverage.** All fifteen acceptance criteria map to a task: 1 -> Task 10; 2 -> Tasks 8, 9 and 10; 3 -> Task 9; 4 -> Tasks 2, 3 and 9; 5 -> Task 3; 6 -> Tasks 2 and 4; 7 -> Tasks 4 and 5; 8 -> Task 4; 9 -> Task 6; 10 -> Tasks 6 and 7; 11 -> Task 1; 12 -> Task 1; 13 -> the Scope guard, enforced by the absence of any `useQuery` in `web/src/profile/` (grep it at Task 11); 14 -> Task 11; 15 -> the Phase 6 proposals section plus Task 11's `/backlog close` step.

**Deviations from the spec, each with a reason.**

1. **The new `AuthProvider` tests live in a new file, `AuthProvider.session.test.tsx`, not appended to `AuthProvider.test.tsx`.** The spec's criterion 11 requires `AuthProvider.test.tsx` to need zero edits, and appending a test would at minimum require editing its shared `Probe` component to expose the new methods. A separate file honours the criterion literally and keeps the shipped file as a clean byte-identical regression gate.
2. **The password mutation carries NO variables; `mutate()` is called with no argument and the `mutationFn` closes over the fields.** The spec names the hazard - `useMutation` retains `variables` on the settled mutation, so "the test must read the mutation object, not just the inputs" - but does not name the remedy. This is the remedy: with no variables there is nothing in the mutation cache to read. Task 5 asserts it, with the settled mutation's presence in the cache as the positive control so the absence assertion is not about an empty list.
3. **`IdentityTab` has no client-side empty-name guard**, unlike its named precedent `WorkerEditForm.tsx:27-30`. The spec's interaction table says an Identity save is "no-op with zero requests when the trimmed name equals `user.name`; **otherwise one PATCH**", which makes an empty name a request that gets the server's own `400 name is required`. Following that gives one error-rendering path instead of two, and there is no second field here to protect from a wasted round trip. Recorded so a reviewer comparing the two forms does not read it as an omission.
4. **The Identity draft is `useState<string | null>(null)` with a fall-through to `user.name`, rather than `useState(user.name)`.** The spec's lifecycle rule is "a form seeds its draft once and is never re-derived on re-render", and this satisfies it strictly - once the draft is a string it is never re-derived - while also being correct when the component mounts before `AuthProvider` has hydrated, which `useState(user?.name ?? '')` is not. `null` and `''` are distinct: clearing the input is a real edit, not a fall-through.
5. **`SessionsTab` closes the confirm dialog before firing the mutation.** The spec says errors render "inline beside the control that caused them, never in a page-level box behind a scrim". Closing first is how that is achieved with the unmodified `ConfirmDialog`, which takes no `error` prop. Stated so the ordering is not read as incidental.
6. **`web/src/app/JobsPlaceholder.tsx` is deleted.** The spec calls this "a plan-time call, not a design question" and asks for the no-other-importer check. The check is in Task 10 Step 3 with an explicit escape hatch if the grep returns anything.
7. **Tasks 5 and 7 have no implementation step.** Their behaviour is implemented in Tasks 2/4 and 6 respectively, so the plan replaces "run it and watch it fail" with a mandatory mutate-and-revert that proves each test discriminates, with every failure output recorded. Stated explicitly because a task whose RED cannot be a missing implementation must name its substitute evidence.

**Placeholder scan.** No TBD, no "add appropriate error handling", no "similar to Task N". Every code step carries literal code; every test step carries the literal test.

**Type consistency.** `User.created_at` is defined once in Task 1 and read in Task 9 (`user.created_at.slice(0, 10)`) and asserted in every fixture. `applyUser` and `clearSession` are spelled identically in Tasks 1, 3, 6 and 7. `updateMe` / `changePassword` / `signOutEverywhere` are defined in Task 2 and imported under those exact names in Tasks 3, 4 and 6. `PROFILE_TABS` / `DEFAULT_PROFILE_TAB` / `findProfileTab` / `ProfileTab` are defined in Task 8 and consumed in Tasks 8 and 9. `ProfileTabs` and `ProfilePage` are imported under those names in Tasks 9 and 10. The `data-testid`s `profile-initials`, `meta-email`, `meta-role`, `meta-member-since` are introduced in Task 9 and used only there; `password-session-warning` in Task 4; `sessions-blast-radius` and `sessions-omission-note` in Task 6. All three panels are zero-prop `ComponentType`s, matching the `ProfileTab.Panel` type.
