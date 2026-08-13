# Admin console: Invites tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the fifth and final admin-console tab to be BUILT (it sits second in the bar, between Users and Agent enrolls: tabs.ts:21-25) at `/admin/invites` - create an invite with an optional bound email and one of four TTL presets, reveal the raw token clear-text exactly once through the shared `TokenRevealDialog`, and list every invite with a client-derived four-state pill - without leaving the raw credential or the invitee's email anywhere in the DOM, the mutation cache, the query cache, storage, a URL or the console after dismissal.

**Architecture:** Seven new files under `web/src/admin/invites/` (api client, status derivation, list hook, actions hook, create form, table, tab) plus one additive tone on `web/src/components/holo/Chip.tsx`, one entry in `web/src/admin/tabs.ts`, and edits to three shipped test files whose assertions currently encode the absence of this tab. The tab is a structural clone of `web/src/admin/enrollments/` with the nouns changed; the two places it genuinely diverges get their own tasks and their own files: the four-state derivation with its load-bearing precedence order (Task 3) and the token-retention rule (Task 5, with a second, integration-level proof in Task 9).

**Tech Stack:** React 18, TypeScript 5.7, TanStack Query v5 (`@tanstack/react-query` 5.101), react-router-dom v7, Tailwind v4 (Holo tokens), Vitest 2.1 + Testing Library 16 + user-event 14 + MSW 2.7, jsdom 29.

**Spec:** `docs/superpowers/specs/2026-08-13-admin-invites-tab.md` (approved; do not reopen its decisions). Deviations from it that this plan makes are listed under "Deviations from the spec" below, each with a reason.

**Backlog item closed by this slice:** `docs/backlog/feature-2026-08-08-admin-invites-tab.md`. **The engineer does not close it.** Phase 6 closes it with `/backlog close feature-2026-08-08-admin-invites-tab`, which `git mv`s it into `docs/backlog/closed/`, and corrects its stale title in the same commit. Never hand-edit `status:`.

---

## Slice independence declaration

- **Backend slice: NONE.** This is 100% `web/`. Zero Go files change, zero `.sql` files change - therefore **no `make generate`, no `*.sql.go`, no `models.go`, no migration**. I re-verified every backend claim in the spec against the tree at HEAD `22b553a` (see "Verified backend surface" below) and all of them hold; not one required a Go change to make the frontend work.
- **Frontend slice: ONE ENGINEER (`relay-frontend-engineer`), SEQUENTIAL.** Do not split these tasks across two engineers and do not run any of them in parallel. The dependency chain is linear: Tasks 4-8 import Tasks 2 and 3; Task 6 imports Task 1 (the `err` Chip tone); Task 8 imports Tasks 5, 6 and 7; Task 9 exercises Task 8 end to end; Task 10's registry entry imports Task 8. Tasks 1 and 10 both write into shipped shared modules (`components/holo/Chip.tsx`, `admin/tabs.ts`, two shipped test files); concurrent writers there have burned this project before.
- **Parallelism available to the conductor for Phase 3: none within this plan.** Unrelated work elsewhere in the repo can run alongside it. There is no backend slice to run in parallel with, and nothing here is blocked on one.
- **Invariants:** none of the six CLAUDE.md backend Invariants is in play (no Go, no task row, no gRPC stream, no request body outside `apiFetch`). Two frontend analogues apply and are called out per task: every request goes through `apiFetch` (`web/src/lib/api.ts:29`), and "end the generation before releasing the resource" governs the credential's lifecycle - `create.reset()` ends the mutation's generation and is what `onDone` must call, so dismissal and destruction are one step (Tasks 5, 8, 9).

---

## Verified backend surface (re-verified against the tree; do not trust the spec alone)

Read: `internal/api/invites.go:16-250`; `internal/api/server.go:142-143`; `internal/store/query/invites.sql`; `internal/api/pagination.go:288-293`.

| Claim | Verdict | Evidence |
|---|---|---|
| `POST /v1/invites` and `GET /v1/invites` are both registered, both `auth(admin(...))` | Confirmed | `server.go:142-143` |
| No revoke, delete, patch or resend route exists for invites | Confirmed | those two registrations are the only `invites` hits in `server.go`; `invites.sql` has no delete query |
| `readJSON` runs **unconditionally** on the POST, so an absent body decodes as `io.EOF` and 400s | Confirmed | `invites.go:27` |
| `expires_in` is parsed by `time.ParseDuration`; default `72h`; must be `> 0`; max `720h` **inclusive** | Confirmed | `invites.go:31-47` (`if dur > maxInviteDuration` at `:44`, so `720h` exactly is accepted) |
| **Go duration strings have no day unit.** `"7d"` / `"30d"` 400 with `invalid expires_in: ...` | Confirmed by the conductor running `time.ParseDuration("7d")` -> `unknown unit "d"`; `invites.go:34-38` is the branch that returns the 400 | this is the single most likely thing to get wrong; Task 2 gives it a discriminating test |
| A non-empty `email` is validated by `mail.ParseAddress`; a bad one is a 400 `invalid email address` | Confirmed | `invites.go:65-69` |
| The 201 body is `{id, token, expires_at}` plus `email` only when bound | Confirmed | `invites.go:79-87` |
| `token` is the raw 64-char hex and is unrecoverable after that response - only `tokenhash.Hash(rawHex)` is stored | Confirmed | `invites.go:55-56`; the list projection omits `token_hash` |
| The list item carries `id`, `created_at`, `expires_at`, `created_by`, `created_by_email` **always**, and `email` / `used_at` **only when set** | Confirmed | single shared `inviteEntry`, `invites.go:125-146`, keys added conditionally at `:139-144` |
| There is **no** `status` field, by design | Confirmed | `invites.go:112-121` says so in the handler's own comment |
| Sort allowlist is `created_at` / `expires_at`, both directions, default `-created_at`, and all four dispatch arms exist | Confirmed | `InvitesSortSpec` `invites.go:97-103`; arms at `:160, :180, :200, :220` |
| Envelope is `{items, next_cursor, total}`; `total` is the unfiltered `CountInvites` | Confirmed | `invites.go:244-249`; `pagination.go:288-293` |
| There is no filter parameter, so **every state is returned** | Confirmed | no filter is read anywhere in `handleListInvites` |

**Client-side consequences that follow, which the code must match exactly:**

- Optional keys are **absent, not null**: TypeScript `email?: string` and `used_at?: string`. A check written as `used_at !== null` is a compile error, not a silently-always-true condition.
- `ApiError.message` is `"<status> <server sentence>"` (`lib/api.ts:53`), so a bad email renders as `400 invalid email address`.
- `apiFetch` prefixes `/v1` (`lib/api.ts:38`), so client paths are written **without** it: `/invites`, never `/v1/invites`.
- `onUnauthorized` fires on a literal **401 only** (`lib/api.ts:44-46`). A 403 from a non-admin does not sign anyone out.
- No path interpolation anywhere on this surface - `/invites` is a literal. Do not add `encodeURIComponent` to a literal.

---

## File-by-file correspondence with `web/src/admin/enrollments/`

"Mirror X at `file:line`" is a literal instruction: copy the shape, change the nouns. **Read each source file before writing the file that mirrors it.** Where a test can be adapted rather than written fresh, the "Adapt" column says so - adapting means copying the shipped test file, changing fixtures and nouns, and then adding the invite-specific cases; it does not mean copying assertions you have not re-read.

| New file | Mirrors | Diverges, and why |
|---|---|---|
| `invites/api.ts` | `enrollments/api.ts` | TTL presets are **duration strings**, not seconds, and the label diverges from the wire value for two of the four (`7d` -> `168h`, `30d` -> `720h`). The row type has two optional keys, not one. |
| `invites/api.test.ts` | `enrollments/api.test.ts` (**adapt**) | The preset-bounds test becomes a regex test for hour-denomination. |
| `invites/inviteStatus.ts` | `enrollments/enrollmentStatus.ts` | Four states, not three; the input is the row (needs `used_at`), not a bare `expires_at` string; a fourth tone. `formatExpiryLabel` and the 1h window are duplicated verbatim - see "Extraction debt". |
| `invites/inviteStatus.test.ts` | `enrollments/enrollmentStatus.test.ts` (**adapt**) | Adds the REDEEMED cases and the precedence case. |
| `invites/useInvites.ts` | `enrollments/useAgentEnrollments.ts` | Key prefix only. |
| `invites/useInvites.test.tsx` | `enrollments/useAgentEnrollments.test.tsx` (**adapt**) | Key prefix and fixture only. |
| `invites/useInviteActions.ts` | `enrollments/useAgentEnrollmentActions.ts` | Same `gcTime: 0` + bare-prefix shape. The mutation variables now carry a **second asset** (the invitee email), which changes what the tests must assert. |
| `invites/useInviteActions.test.tsx` | `enrollments/useAgentEnrollmentActions.test.tsx` (**adapt**) | Adds the cache-emptiness test (Task 5), which has no counterpart there. |
| `invites/CreateInviteForm.tsx` | `enrollments/CreateEnrollmentForm.tsx` | Email input instead of hostname hint; duration-string presets. Deliberately **not** shared - the decision is already recorded at `CreateEnrollmentForm.tsx:22-25`. |
| `invites/CreateInviteForm.test.tsx` | `enrollments/CreateEnrollmentForm.test.tsx` (**adapt**) | |
| `invites/InvitesTable.tsx` | `enrollments/EnrollmentsTable.tsx` | Six columns instead of five; `CREATED BY` is fillable here because the list query joins `users`; the `NOTE` cell is three-way instead of constant. |
| `invites/InvitesTable.test.tsx` | `enrollments/EnrollmentsTable.test.tsx` (**adapt**) | Adds the no-control assertion **with a positive control**, and the four-state pill test. |
| `invites/InvitesTab.tsx` | `enrollments/EnrollmentsTab.tsx` | No prev-escape-hatch on an empty non-first page (spec decision 17: this list is unfiltered and nothing reaps invites, so that state is unreachable and the hatch would be untestable dead code); footer says "all states", not "active only". |
| `invites/InvitesTab.test.tsx` | `enrollments/EnrollmentsTab.test.tsx` (**adapt**) | Drop the two enrollment-specific empty-page tests, keep the rest. |
| `invites/inviteTokenSecrecy.test.tsx` | `enrollments/enrollmentTokenSecrecy.test.tsx` (**adapt**) | **Drop the seven matcher self-tests** (they are shipped in the enrollments suite and duplicating them buys nothing). The cache assertion changes from "no entry's state stringifies to contain the secret" to "the cache is EMPTY". |

**Reused unchanged, no edits:** `Table` / `TableRow` / `TableCell` / `ariaSort` / `sortCaret` (`components/holo/Table.tsx`), `TokenRevealDialog` (`admin/TokenRevealDialog.tsx`, second consumer, no prop added), `DialogShell` via it, `GlassPanel`, `PillButton`, `Button`, `Field`, `Input`, `useNow`, `formatTimeUntil`, `computePageRange`, `apiFetch`, `ApiError`, `web/src/test/secretLeaks.ts`.

---

## Extraction debt this slice knowingly adds - recorded, not hidden

**This tab is the SEVENTH consumer of the cursor-pager block** - the `cursor` / `stack` / `startOffset` / `offsets` quartet with its `next` / `prev` / `resetPaging` functions and the `computePageRange` footer. Shipped copies live in `JobsPage.tsx`, `WorkersPage.tsx`, `SchedulesPage.tsx`, `admin/users/UsersTab.tsx`, `admin/enrollments/EnrollmentsTab.tsx` and `admin/reservations/ReservationsTab.tsx`. The `toggleSort` helper is duplicated in four of those. The house rule is **extract before the third**, so this is four consumers past the rule.

**Confirmed decision: do not extract it in this slice.** I re-verified the two facts the decision rests on:

1. There is no open backlog item for it. Glob of `docs/backlog/*cursor*` returns only `bug-2026-08-13-cursor-value-kind-not-validated.md`, which is an endpoint-validation item, not a pager item.
2. The extraction would have to migrate six shipped surfaces, and the honest gate for a behaviour-preserving refactor is a **zero-line diff to the existing test files** (`reference_refactor_gate_byte_identical_tests`). That is its own slice with its own risk profile, and folding it in would put a feature behind an unrelated refactor.

**Phase 6 proposes** `docs/backlog/idea-2026-08-13-cursor-pager-hook.md` (low/medium) for human accept. It must state up front: the zero-line-diff gate on the six existing test files, and that a partial migration producing a seventh *variant* is worse than seven copies. **The engineer does not file it.** This paragraph is the record that the seventh copy was shipped deliberately.

**Second-consumer note, no item due yet:** `formatExpiryLabel` and the 1h `EXPIRING_WINDOW_MS` constant are duplicated between `enrollmentStatus.ts` and `inviteStatus.ts`. Extract before the **third**. A comment in `inviteStatus.ts` names `web/src/lib/expiry.ts` as the destination so consumer three does not have to rediscover it.

---

## Test-environment constraints (pin these; they have bitten this repo before)

- **Runner:** vitest 2.1 + jsdom 29 + `@testing-library/react` 16 + `user-event` 14. **MSW 2.7 is fail-closed** (`onUnhandledRequest: 'error'`) with **zero default handlers** - every endpoint a test touches needs an explicit `server.use(...)`, including probe requests used as instrument controls.
- **No `inert`, no native `<dialog>`, no focus-trap library.** `DialogShell` implements the trap as a keydown intercept and documents why: `user-event@14` computes its Tab destination from a document-wide `querySelectorAll` and the string `inert` appears nowhere in the shipped package, so `userEvent.tab()` walks straight past an inert background (`DialogShell.tsx:48-57`). This slice **adds no modal machinery**; `TokenRevealDialog` is reused unmodified. No test in this slice may claim `inert` blocks anything - assert it as an attribute or not at all.
- **A TanStack `invalidateQueries` test needs an ACTIVE OBSERVER.** Mount the list with `renderHook` or inside the rendered tab; a `client.fetchQuery` / `setQueryData` seed leaves no observer, `invalidateQueries`' default `refetchType: 'active'` never fires, and the assertion passes vacuously. Cited in Task 5.
- **Mutation lifecycle, read out of the installed library (do not re-derive):**
  - `Mutation.execute` awaits the hook-level `options.onSuccess` at `@tanstack/query-core/build/modern/mutation.js:123` and only **then** dispatches `{type:'success'}` at `:144`. `MutationObserver.reset()` removes the observer (`mutationObserver.js:50-55`). **So a `reset()` inside the hook-level `onSuccess` detaches the observer before the success notification: `isSuccess` never becomes true, `data` never lands on the observer's result, and the reveal dialog never opens.** Task 5 forbids it and pins it with a test.
  - `Mutation.optionalRemove` removes from the cache **only when `#observers.length === 0`** (`mutation.js:47-55`), and `removeObserver` is what schedules the GC (`:38-46`). So a settled mutation with a live observer stays in the cache **even with `gcTime: 0`**, and the "the cache does hold mutations" positive control is valid at any point **before `reset()`** - while pending or while the reveal dialog is open. It is **invalid after `reset()`**, where it would measure an already-evicted entry. Tasks 5 and 9 take it before.
  - `Removable.updateGcTime` is `Math.max(this.gcTime || 0, newGcTime ?? 5 * 60 * 1000)` (`removable.js:18-23`) and `isValidTimeout(0)` is true, so `gcTime: 0` really means "next tick", not "clamped to the 5-minute default".
- **`navigator.clipboard` is feature-detected** in `TokenRevealDialog` (`:85-87`) because it is `undefined` outside a secure context. A test that exercises Copy must install a clipboard descriptor and restore it (`enrollmentTokenSecrecy.test.tsx:30-38`).
- **jsdom fires no event when a focused node is silently detached** (`DialogShell.tsx:299-305`), so no test may rely on a focusout to observe removal.
- **`getByText` matches an element's DIRECT text children only**, so the footer's composite `<span>` (literal text plus a nested `<span>` holding the range) is matched with a regex against the outer span's own text - see Task 8.
- **Plan-supplied test bodies are guesses until run RED.** Every step below states the expected failure. **A green test before the implementation exists is vacuous - fix the test, do not proceed.** Where a test genuinely passes on first write, the task **says so** and names the substitute evidence (a mutate-and-revert with both outputs recorded).

---

## Conventions for every task

- All `npm` / `npx` commands run from the `web/` directory of the worktree: `D:/dev/relay/.claude/worktrees/pr-merge-session-f5796e/web`. **This worktree, not `D:/dev/relay`.**
- Single file: `npx vitest run src/<path>.test.tsx`. Whole directory: `npx vitest run src/admin/invites/`. Full suite: `npm test`.
- TDD per step: write the failing test, run it and watch it fail with the stated message, implement, run it and watch it pass, commit.
- House rule: **never an em dash or en dash**, in code, comments, copy or this document. Placeholders are the plain ASCII hyphen `-`. The hi-fi uses em dashes at `:2111, :2120, :2130`; do not copy them.
- Never reformat code you were not asked to change. Never edit a shipped test's assertions to make new code pass - **except** for the four enumerated assertions in Task 10, which encode the absence of the very tab this slice adds; those are listed explicitly and nothing else in those files may change.
- `make` is **not on PATH in this shell**. Use `go build ./...` and `go test ./...` directly from the repo root.
- `web/dist` is tracked but stale. A production build dirties it: run `git checkout -- web/dist/` from the repo root before assembling any change set.

---

## Scope guard - do NOT build

- **No revoke, delete or resend control, anywhere.** No endpoint exists (`server.go:142-143` is the whole route surface; `invites.sql` has no delete query) and the hi-fi asks for none - its `ACTIONS` cell renders prose (`hifi3-holo-pages.jsx:2119-2121`) and its footnote says outright there is no revoke endpoint in v1 (`:2130`). A guaranteed-failing button is a dead control.
- **No `TOKEN PREFIX` column.** Only the SHA-256 is stored and the list query cannot select it. Persisting a prefix would weaken a secret for a cosmetic column.
- **No `used_by` / redeemed-by column.** The endpoint returns no such field.
- **No `?status=` filter and no search box.** No endpoint support.
- **No `refetchInterval`, no polling, no timer other than `useNow(60_000)`** (which issues no request).
- **No client-side reimplementation of `mail.ParseAddress`** and no client-side max-duration check. `type="email"` plus the server's 400 is the whole email story; presets make the invalid duration range unreachable rather than merely rejected.
- **No new shared primitive and no extraction**: not the cursor pager (seventh consumer, recorded above), not `formatExpiryLabel` (second consumer).
- **No edits to `TokenRevealDialog`, `DialogShell`, `Table`, `Field`, `Input`, `PillButton`, `GlassPanel`, `Button`** or any other shared component except the one additive `Chip` tone in Task 1.
- **No Go file, no `.sql` file, no `web/dist` commit.**
- **No backlog file created, moved or edited by the engineer.** Phase 6 does that.

---

## File Structure

**New files** (all under `web/src/admin/invites/`)

| File | Responsibility |
|---|---|
| `api.ts` | `Invite`, `InvitesPage`, `InviteSort` / `InviteSortField`, `TTL_PRESETS`, `DEFAULT_EXPIRES_IN`, `MAX_EXPIRES_IN_HOURS`, `CreateInviteBody`, `CreateInviteResponse`, `listInvites`, `createInvite`. Every backend hazard comment lives here. |
| `api.test.ts` | Query string construction, the always-sent body, the omitted-not-empty email, and the hour-denomination of every preset. |
| `inviteStatus.ts` | `InviteStatus`, `deriveStatus`, `statusTone`, `formatExpiryLabel`. |
| `inviteStatus.test.ts` | Four states, the precedence case, the two boundaries, the tone map, the sub-minute collapse. |
| `useInvites.ts` | `useQuery(['invites', sort, cursor])`, `keepPreviousData`, no `refetchInterval`. |
| `useInvites.test.tsx` | Key shape, params passed through, no polling with a positive control on the same counter. |
| `useInviteActions.ts` | One `create` mutation: `gcTime: 0`, bare-prefix invalidation, no `reset()` in `onSuccess`, no logging. |
| `useInviteActions.test.tsx` | Bare-prefix invalidation with an active observer, the cache-empties-after-reset test (**RED #1**), the settled-mutation-still-exposes-data guard. |
| `CreateInviteForm.tsx` | Optional email (trimmed, omitted when blank), four duration presets, the shown-once warning, its own error slot. |
| `CreateInviteForm.test.tsx` | Body shape both ways, the four presets and their exact wire values, no free-form duration field, pending, error, cancel. |
| `InvitesTable.tsx` | Six columns, the pill, the three-way `NOTE` cell, terminal-row dimming. |
| `InvitesTable.test.tsx` | Headers and `aria-sort`, the absent-email hyphen, `created_by_email` present and `created_by` absent, the `NOTE` cell three ways, dimming, no control (with a positive control), no 64-hex string. |
| `InvitesTab.tsx` | Composition: control row, inline create panel, body, footer, footnote, reveal dialog. |
| `InvitesTab.test.tsx` | Loading, error + Retry, empty, sort resets paging, cursor walk, footer copy, create -> reveal -> Done -> refetch, the 60s tick with zero requests. |
| `inviteTokenSecrecy.test.tsx` | The credential's whole lifecycle: positive controls while the dialog is open, then DOM / mutation cache / query cache / storage / URLs / console all clean. |

**Modified files**

| File | Change |
|---|---|
| `web/src/components/holo/Chip.tsx:8-12` | One additive `err` tone key. No existing consumer changes. |
| `web/src/components/holo/Chip.test.tsx` | One appended test. |
| `web/src/admin/tabs.ts:2-26` | One import, one `ADMIN_TABS` entry between Users and Agent enrolls, and the stale "blocked on a GET /v1/invites that does not exist" comment corrected. |
| `web/src/admin/AdminTabs.test.tsx` | Four enumerated assertion changes plus two additions - see Task 10. |
| `web/src/admin/AdminPage.test.tsx` | One handler added to `renderAt`, one test replaced, one test added - see Task 10. |

**No other file is modified. No file is deleted.**

---

## Task 0: Measure the baseline

The plan was written without shell access, so the "973 tests" figure in the brief is **unverified**. Measure it yourself before you change anything; every later "expected N tests" claim in this plan is relative to what you record here.

**Files:** none.

- [ ] **Step 1: Record the baseline**

From `web/`:

```bash
npm test 2>&1 | tail -20
```

Write down the exact `Test Files` and `Tests` totals and confirm the run is green. From the repo root:

```bash
go build ./... && go test ./... 2>&1 | tail -20
```

Expected: green. If either gate is already red, **stop and report** - do not start work on top of a red gate, and do not accept "that was already broken" without a number measured both ways.

- [ ] **Step 2: No commit**

Nothing changed.

---

## Task 1: `Chip` gains a fourth tone

Four derivable invite states need four tones; `Chip` ships three (`Chip.tsx:8-12`). One additive key, no consumer change.

**Files:**
- Modify: `web/src/components/holo/Chip.tsx:8-12`
- Modify (append only): `web/src/components/holo/Chip.test.tsx`

- [ ] **Step 1: Write the failing test**

Append to `web/src/components/holo/Chip.test.tsx`, after the `warn` test at `:17-20`:

```tsx
test('err tone uses the err palette', () => {
  render(<Chip tone="err">EXPIRED</Chip>)
  expect(screen.getByText('EXPIRED')).toHaveClass('border-err/40', 'bg-err/10', 'text-err')
})
```

Change nothing else in that file.

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/components/holo/Chip.test.tsx`

Expected: FAIL on the new test only - `Expected the element to have class: border-err/40 ... Received: rounded-full px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em] undefined`. The literal `undefined` in the received class list is `TONES['err']` not existing; vitest transpiles without type-checking, so the invalid `tone` prop arrives at runtime rather than as a TS error. (`npm run build` **would** fail on it - that is the second gate, and it is why the type must be widened by adding the key rather than by casting.)

- [ ] **Step 3: Implement**

Replace `web/src/components/holo/Chip.tsx:8-12` with:

```tsx
const TONES = {
  accent: 'border border-accent/40 bg-accent/10 text-accent',
  muted: 'border border-border bg-white/[0.04] text-fg-mute',
  warn: 'border border-warn/40 bg-warn/10 text-warn',
  // Fourth tone, added for the invites STATUS pill: four derivable states need
  // four tones (web/src/admin/invites/inviteStatus.ts). Collapsing EXPIRED and
  // REDEEMED into `muted` would discard information the hi-fi deliberately
  // encodes - it uses C.err for expired and C.fgMute for redeemed
  // (design_handoff_relay_holo/hifi3-holo-pages.jsx:2101). The class string is
  // the error idiom already used at seven call sites (LoginScreen.tsx:62,
  // JobActions.tsx:65, UsersTable.tsx:25, ...), and PillButton.tsx:11 carries the
  // sibling `danger` tone, so this introduces no new palette.
  err: 'border border-err/40 bg-err/10 text-err',
} as const
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/components/holo/`

Expected: PASS. Chip's own file goes from 6 tests to 7; nothing else in `components/holo/` changes. Confirm no shipped assertion moved:

```bash
git diff -U0 web/src/components/holo/Chip.test.tsx
```

Expected: an addition-only hunk.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/Chip.tsx web/src/components/holo/Chip.test.tsx
git commit -m "feat(web): add an err tone to Chip"
```

---

## Task 2: The invites API client, types and TTL presets

The whole "Go duration strings have no day unit" hazard lives here, and its test is the one that stops a `"7d"` preset from reaching production.

**Files:**
- Create: `web/src/admin/invites/api.ts`
- Test: `web/src/admin/invites/api.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/invites/api.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import {
  createInvite,
  listInvites,
  DEFAULT_EXPIRES_IN,
  MAX_EXPIRES_IN_HOURS,
  TTL_PRESETS,
} from './api'

const ROW = {
  id: 'i1',
  created_at: '2026-08-01T09:00:00Z',
  expires_at: '2026-08-04T09:00:00Z',
  created_by: '11111111-2222-3333-4444-555555555555',
  created_by_email: 'admin@studio.dev',
  email: 'invitee@studio.dev',
}

test('listInvites sends sort and limit=50, omits an empty cursor, and returns the envelope', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/invites', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: 'c2', total: 7 })
    }),
  )
  const page = await listInvites({ sort: '-created_at', cursor: '' })
  expect(params?.get('sort')).toBe('-created_at')
  // ?limit=0 is a 400 rather than a clamp (internal/api/pagination.go:244), so the
  // page size is always stated explicitly.
  expect(params?.get('limit')).toBe('50')
  expect(params?.has('cursor')).toBe(false)
  expect(page.items[0].created_by_email).toBe('admin@studio.dev')
  expect(page.next_cursor).toBe('c2')
  expect(page.total).toBe(7)
})

test('listInvites sends the cursor and each of the four sort values', async () => {
  const seen: string[] = []
  server.use(
    http.get('/v1/invites', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(`${p.get('sort')}|${p.get('cursor')}`)
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  for (const sort of ['created_at', '-created_at', 'expires_at', '-expires_at'] as const) {
    await listInvites({ sort, cursor: 'cur1' })
  }
  expect(seen).toEqual([
    'created_at|cur1',
    '-created_at|cur1',
    'expires_at|cur1',
    '-expires_at|cur1',
  ])
})

test('email and used_at stay ABSENT rather than null when the server omits them', async () => {
  server.use(
    http.get('/v1/invites', () =>
      HttpResponse.json({
        items: [
          {
            id: 'i2',
            created_at: ROW.created_at,
            expires_at: ROW.expires_at,
            created_by: ROW.created_by,
            created_by_email: ROW.created_by_email,
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
  )
  const page = await listInvites({ sort: '-created_at', cursor: '' })
  // inviteEntry adds these two keys conditionally (internal/api/invites.go:139-144),
  // so the types are `?: string`, never `string | null`, and every consumer must
  // test for undefined - a `!== null` check would be always-true.
  expect('email' in page.items[0]).toBe(false)
  expect('used_at' in page.items[0]).toBe(false)
  expect(page.items[0].email).toBeUndefined()
  expect(page.items[0].used_at).toBeUndefined()
})

test('createInvite ALWAYS sends a JSON body, even with no email', async () => {
  let body: unknown
  let contentType: string | null = null
  server.use(
    http.post('/v1/invites', async ({ request }) => {
      // This handler mirrors readJSON (internal/api/server.go:199-211): an absent or
      // unparseable body is a 400 "invalid request body". readJSON runs
      // UNCONDITIONALLY on this endpoint (internal/api/invites.go:27), so that is
      // what makes this test non-vacuous - a client that stops sending a body fails
      // here exactly as it would against the real server.
      const raw = await request.text()
      if (raw === '') return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      try {
        body = JSON.parse(raw)
      } catch {
        return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      }
      contentType = request.headers.get('content-type')
      return HttpResponse.json(
        { id: 'i9', token: 'f00dcafe'.repeat(8), expires_at: ROW.expires_at },
        { status: 201 },
      )
    }),
  )
  const created = await createInvite({ expires_in: DEFAULT_EXPIRES_IN })
  expect(body).toEqual({ expires_in: '72h' })
  expect(contentType).toContain('application/json')
  expect(created.token).toBe('f00dcafe'.repeat(8))
})

test('createInvite sends the email key only when one is supplied', async () => {
  let body: unknown
  server.use(
    http.post('/v1/invites', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(
        { id: 'i9', token: 'tok', expires_at: ROW.expires_at, email: 'invitee@studio.dev' },
        { status: 201 },
      )
    }),
  )
  const created = await createInvite({ email: 'invitee@studio.dev', expires_in: '24h' })
  expect(body).toEqual({ email: 'invitee@studio.dev', expires_in: '24h' })
  expect(created.email).toBe('invitee@studio.dev')
})

test('a 400 surfaces as an ApiError carrying the status and the server message', async () => {
  server.use(
    http.post('/v1/invites', () =>
      HttpResponse.json({ error: 'invalid email address' }, { status: 400 }),
    ),
  )
  await expect(createInvite({ email: 'nope', expires_in: '72h' })).rejects.toMatchObject({
    status: 400,
    code: 'invalid email address',
  })
})

// THE discriminating test of this file. `expires_in` is parsed by Go's
// time.ParseDuration (internal/api/invites.go:34), which accepts h/m/s and
// smaller and has NO DAY UNIT: ParseDuration("7d") returns `unknown unit "d"`,
// so a preset shipped as "7d" 400s in production while passing any naive
// "there are four presets" test. The labels stay human ("30d" is readable,
// "720h" is hostile); only the WIRE VALUES are constrained.
test('every TTL preset is hour-denominated and inside the server bounds', () => {
  expect(TTL_PRESETS.map((p) => p.label)).toEqual(['24h', '72h', '7d', '30d'])
  expect(TTL_PRESETS.map((p) => p.value)).toEqual(['24h', '72h', '168h', '720h'])
  for (const p of TTL_PRESETS) {
    expect(p.value).toMatch(/^\d+h$/)
    // Explicitly, not just as a consequence of the regex: a day suffix is the
    // exact failure this test exists to catch.
    expect(p.value).not.toMatch(/[dwy]$/)
    const hours = Number(p.value.slice(0, -1))
    expect(hours).toBeGreaterThan(0)
    // 720h EXACTLY is accepted: the server rejects `dur > maxInviteDuration`
    // (internal/api/invites.go:44), not `>=`.
    expect(hours).toBeLessThanOrEqual(MAX_EXPIRES_IN_HOURS)
  }
  expect(MAX_EXPIRES_IN_HOURS).toBe(720)
})

test('the default preset is 72h, matching the server default, and is one of the four', () => {
  expect(DEFAULT_EXPIRES_IN).toBe('72h')
  expect(TTL_PRESETS.some((p) => p.value === DEFAULT_EXPIRES_IN)).toBe(true)
})

test('two presets deliberately show a label that is NOT their wire value', () => {
  // Pins the divergence as intentional. If someone "simplifies" this by making
  // label === value, either the UI starts showing 720h or the wire starts
  // carrying 30d - and the second one 400s.
  expect(TTL_PRESETS.filter((p) => p.label !== p.value).map((p) => p.label)).toEqual(['7d', '30d'])
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/invites/api.test.ts`

Expected: FAIL at import time - `Failed to resolve import "./api" from "src/admin/invites/api.test.ts"`. The whole file fails to load, which is the correct RED for a missing module.

- [ ] **Step 3: Implement**

Create `web/src/admin/invites/api.ts`:

```ts
import { apiFetch } from '../../lib/api'

// Mirrors inviteEntry (internal/api/invites.go:125-146), the SINGLE builder all
// four sort arms share - so unlike the enrollments handler there is exactly one
// response shape to model here.
//
// email and used_at are OPTIONAL, not nullable: the Go map omits each key
// entirely when the value is unset (:139-144). Consumers must handle `undefined`,
// never `null`, and a check written as `used_at !== null` is a compile error
// rather than a silently-always-true condition.
//
// created_by_email comes from an inner JOIN on users
// (internal/store/query/invites.sql:32), which is why this table CAN render a
// CREATED BY column where the enrollments one could not.
//
// There is deliberately NO status field (the handler says so at :112-121) and no
// token, hash or prefix: omitting i.token_hash from the projection is the
// endpoint's entire security control (invites.sql:22-25).
//
// created_at / expires_at / used_at are Go time.Time values, i.e. RFC3339 with
// nanosecond precision. Parse with new Date(); never string-compare them.
export interface Invite {
  id: string
  created_at: string
  expires_at: string
  created_by: string
  created_by_email: string
  email?: string
  used_at?: string
}

// internal/api/pagination.go:288-293.
export interface InvitesPage {
  items: Invite[]
  next_cursor: string
  total: number
}

// InvitesSortSpec (internal/api/invites.go:97-103): two keys, each with an
// optional '-' prefix, default '-created_at'. All four dispatch arms exist
// (:160, :180, :200, :220), so both directions of both keys are live.
export type InviteSortField = 'created_at' | 'expires_at'
export type InviteSort = 'created_at' | '-created_at' | 'expires_at' | '-expires_at'

// The server default (internal/api/invites.go:31) and the hi-fi's preselection
// (design_handoff_relay_holo/hifi3-holo-pages.jsx:2087) agree on 72h.
export const DEFAULT_EXPIRES_IN = '72h'

// internal/api/invites.go:43-47. The check is `dur > maxInviteDuration`, so 720h
// EXACTLY is accepted. Exported so api.test.ts can check the presets against it.
export const MAX_EXPIRES_IN_HOURS = 720

export interface TtlPreset {
  label: string
  // What goes on the wire, and it is NOT always the label.
  value: string
}

// `expires_in` is parsed by Go's time.ParseDuration (internal/api/invites.go:34),
// which understands h, m, s and smaller and has NO DAY UNIT - ParseDuration("7d")
// fails with `unknown unit "d"`, so sending the literal "7d" or "30d" is a 400.
// The LABEL stays human ("30d" is readable, "720h" is hostile) and the VALUE is
// always hour-denominated. This divergence is deliberate; api.test.ts asserts it
// with a /^\d+h$/ regex, because a "7d" preset passes any naive four-presets test
// and only fails in production.
//
// Every value is inside the server's (0, 720h] window, so the invalid range is
// UNREACHABLE from the UI rather than merely rejected - there is no free-text
// duration input and therefore no client-side max check to write.
export const TTL_PRESETS: TtlPreset[] = [
  { label: '24h', value: '24h' },
  { label: '72h', value: DEFAULT_EXPIRES_IN },
  { label: '7d', value: '168h' },
  { label: '30d', value: '720h' },
]

export interface ListInvitesParams {
  sort: InviteSort
  cursor: string
}

export function listInvites({ sort, cursor }: ListInvitesParams): Promise<InvitesPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (cursor) q.set('cursor', cursor)
  return apiFetch<InvitesPage>(`/invites?${q}`)
}

// email is omitted when blank rather than sent as "": the server treats the two
// identically (internal/api/invites.go:65), and omitting keeps the request body
// honest about what the admin actually supplied. A non-empty value is validated
// server-side by mail.ParseAddress (:66) and a bad one is a 400 that the create
// form renders in its own error slot - the client does not reimplement that
// parser, because two parsers disagreeing is worse than one round trip.
export interface CreateInviteBody {
  email?: string
  expires_in: string
}

// The 201 body, internal/api/invites.go:79-87. `email` is echoed only when bound.
// There is no created_at.
//
// SECURITY: `token` is the raw 64-char hex invite credential, and it grants
// account creation on this server. Only tokenhash.Hash(rawHex) is persisted (:56)
// and the list endpoint returns no token field, so it is UNRECOVERABLE after this
// response. Never log it, never put it in a URL or a query key, and never copy it
// into component state - it is rendered straight from the mutation's data by
// web/src/admin/TokenRevealDialog.tsx so that create.reset() is the single point
// that destroys it.
export interface CreateInviteResponse {
  id: string
  token: string
  expires_at: string
  email?: string
}

// A body is ALWAYS sent, even when no email is supplied: readJSON runs
// unconditionally (internal/api/invites.go:27 -> server.go:199-211), so a POST
// with no body decodes as io.EOF and 400s "invalid request body". The minimum
// legal body is {expires_in: "72h"}.
export function createInvite(body: CreateInviteBody): Promise<CreateInviteResponse> {
  return apiFetch<CreateInviteResponse>('/invites', { method: 'POST', json: body })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/invites/api.test.ts`

Expected: PASS, 9 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/invites/api.ts web/src/admin/invites/api.test.ts
git commit -m "feat(web): invites API client with hour-denominated TTL presets"
```

---

## Task 3: The four-state derivation

Mirrors `enrollments/enrollmentStatus.ts` in shape and differs in state set. The precedence order is load-bearing and gets a dedicated discriminating test.

**Files:**
- Create: `web/src/admin/invites/inviteStatus.ts`
- Test: `web/src/admin/invites/inviteStatus.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/invites/inviteStatus.test.ts`:

```ts
import { expect, test } from 'vitest'
import { deriveStatus, formatExpiryLabel, statusTone } from './inviteStatus'

const NOW = new Date('2026-08-09T12:00:00Z')

test('an unredeemed invite with a day left is ACTIVE', () => {
  expect(deriveStatus({ expires_at: '2026-08-10T12:00:00Z' }, NOW)).toBe('ACTIVE')
})

test('an unredeemed invite with 30m left is EXPIRING', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T12:30:00Z' }, NOW)).toBe('EXPIRING')
})

test('an unredeemed invite past its expiry is EXPIRED', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T11:00:00Z' }, NOW)).toBe('EXPIRED')
})

test('an invite with used_at set is REDEEMED', () => {
  expect(
    deriveStatus({ expires_at: '2026-08-10T12:00:00Z', used_at: '2026-08-09T10:00:00Z' }, NOW),
  ).toBe('REDEEMED')
})

// THE discriminating test for the ordering. An implementation that checks expiry
// before redemption passes every other test in this file and fails only this one.
// Redemption is terminal and one-way - MarkInviteUsed is the only writer and
// carries `AND used_at IS NULL` (internal/store/query/invites.sql:9-12), called
// once from registration (internal/api/auth.go:147-158) - so expiry of an
// already-spent credential is a non-event. README.md:1300-1301 documents this
// precedence as the shipped contract.
test('a REDEEMED invite that is ALSO past its expiry reads REDEEMED, never EXPIRED', () => {
  expect(
    deriveStatus({ expires_at: '2026-08-09T11:00:00Z', used_at: '2026-08-09T10:00:00Z' }, NOW),
  ).toBe('REDEEMED')
})

test('exactly at expires_at is EXPIRED', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T12:00:00Z' }, NOW)).toBe('EXPIRED')
})

test('59m59s remaining is EXPIRING', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T12:59:59Z' }, NOW)).toBe('EXPIRING')
})

test('exactly 1h remaining is ACTIVE (the window is strictly under an hour)', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T13:00:00Z' }, NOW)).toBe('ACTIVE')
})

test('1h00m01s remaining is ACTIVE', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T13:00:01Z' }, NOW)).toBe('ACTIVE')
})

test('a nanosecond-precision RFC3339 timestamp parses (Go marshals time.Time this way)', () => {
  expect(deriveStatus({ expires_at: '2026-08-10T12:00:00.123456789Z' }, NOW)).toBe('ACTIVE')
})

test('tones map all four states, including the Chip tone added for this tab', () => {
  expect(statusTone('ACTIVE')).toBe('accent')
  expect(statusTone('EXPIRING')).toBe('warn')
  expect(statusTone('EXPIRED')).toBe('err')
  expect(statusTone('REDEEMED')).toBe('muted')
})

test('formatExpiryLabel collapses any sub-minute remainder to "in <1m"', () => {
  expect(formatExpiryLabel('2026-08-09T12:00:45Z', NOW)).toBe('in <1m')
  expect(formatExpiryLabel('2026-08-09T12:00:01Z', NOW)).toBe('in <1m')
})

test('formatExpiryLabel passes minutes and longer through unchanged', () => {
  expect(formatExpiryLabel('2026-08-09T12:01:00Z', NOW)).toBe('in 1m')
  expect(formatExpiryLabel('2026-08-10T09:00:00Z', NOW)).toBe('in 21h')
})

test('formatExpiryLabel still reads expired at and past the boundary', () => {
  expect(formatExpiryLabel('2026-08-09T12:00:00Z', NOW)).toBe('expired')
  expect(formatExpiryLabel('2026-08-09T11:00:00Z', NOW)).toBe('expired')
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/invites/inviteStatus.test.ts`

Expected: FAIL at import time - `Failed to resolve import "./inviteStatus" from "src/admin/invites/inviteStatus.test.ts"`.

- [ ] **Step 3: Implement**

Create `web/src/admin/invites/inviteStatus.ts`:

```ts
import { formatTimeUntil } from '../../lib/time'

export type InviteStatus = 'REDEEMED' | 'EXPIRED' | 'EXPIRING' | 'ACTIVE'

// Duplicated from enrollmentStatus.ts:5 rather than shared. SECOND consumer; the
// repo rule is extract before the THIRD, so a third status module must lift this
// constant AND formatExpiryLabel below into a shared web/src/lib/expiry.ts. Cited
// rather than invented: README.md:1300-1303 documents the 1h window as the
// shipped contract.
const EXPIRING_WINDOW_MS = 60 * 60 * 1000

// The list endpoint returns FACTS and no status field, by design - the handler
// says so at internal/api/invites.go:112-121 and the query projection selects
// seven columns, none of them a status (internal/store/query/invites.sql:31-32).
// A server-asserted "expired" is stale the moment the row is on screen, and
// "expiring" needs an invented threshold. So the pill is the client's arithmetic
// over expires_at and used_at.
//
// The order below is LOAD-BEARING and matches README.md:1300-1303:
//
//  1. REDEEMED first. Redemption is terminal and one-way: MarkInviteUsed is the
//     only writer and carries `AND used_at IS NULL` (invites.sql:9-12), called
//     once from registration (internal/api/auth.go:147-158). A redeemed invite
//     that later passes its expiry is STILL redeemed - both facts are on the row,
//     and redemption is the one that describes what happened to the credential.
//     It is also the only state that is immune to clock skew, because it derives
//     from a server-written timestamp's PRESENCE, not from a comparison.
//  2. EXPIRED at `remaining <= 0` on the raw millisecond delta, byte-identical to
//     enrollmentStatus.ts:23 and to formatTimeUntil's own boundary
//     (web/src/lib/time.ts:29), so the pill and the EXPIRES cell flip at the same
//     instant.
//  3. EXPIRING strictly under the window: 59m59s is EXPIRING, exactly 1h00m00s is
//     ACTIVE.
//  4. ACTIVE otherwise.
//
// This reads the local clock, so a badly skewed browser mislabels EXPIRING and
// EXPIRED. Accepted for the same reason enrollmentStatus.ts:19-20 accepts it: the
// server exposes no status to prefer instead. It does make the browser a THIRD
// clock after the app host and the database - see
// docs/backlog/bug-2026-08-13-token-expiry-two-clocks.md, which is not widened
// here.
//
// The parameter is the ROW SHAPE, not a bare string, because the derivation needs
// two fields. used_at is optional-not-nullable (invites.go:142-144), so the check
// is against undefined; `used_at !== null` would be always-true.
export interface InviteStatusInput {
  expires_at: string
  used_at?: string
}

export function deriveStatus(invite: InviteStatusInput, now: Date): InviteStatus {
  if (invite.used_at !== undefined) return 'REDEEMED'
  const remaining = new Date(invite.expires_at).getTime() - now.getTime()
  if (remaining <= 0) return 'EXPIRED'
  if (remaining < EXPIRING_WINDOW_MS) return 'EXPIRING'
  return 'ACTIVE'
}

// Four states, four Chip tones (web/src/components/holo/Chip.tsx:8-13, where
// `err` was added for this tab). Colour is never the only channel: the pill TEXT
// differs per state and both terminal states also dim their row.
export function statusTone(status: InviteStatus): 'accent' | 'warn' | 'muted' | 'err' {
  if (status === 'REDEEMED') return 'muted'
  if (status === 'EXPIRED') return 'err'
  if (status === 'EXPIRING') return 'warn'
  return 'accent'
}

// Duplicated verbatim from enrollmentStatus.ts:45-48, reasoning included, because
// the reasoning is what stops it being "simplified" back to formatTimeUntil:
// the row's `now` is useNow(60_000) - a local clock tick refreshed once a MINUTE -
// so a seconds-precision label such as "in 20s" is only accurate at the instant of
// the tick; for up to 59 more real seconds the row's actual remaining time keeps
// falling while the label stays frozen, so a row can read "in 20s" / EXPIRING for
// nearly a minute after it has genuinely expired. Collapsing anything under a
// minute to "in <1m" means the displayed precision never promises more freshness
// than the 60s refresh cadence actually delivers.
//
// SECOND consumer of this exact body. Extract before the third, into
// web/src/lib/expiry.ts together with EXPIRING_WINDOW_MS above.
export function formatExpiryLabel(expiresAt: string, now: Date): string {
  const label = formatTimeUntil(expiresAt, now)
  return /^in \d+s$/.test(label) ? 'in <1m' : label
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/invites/inviteStatus.test.ts`

Expected: PASS, 14 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/invites/inviteStatus.ts web/src/admin/invites/inviteStatus.test.ts
git commit -m "feat(web): derive the four invite states client-side"
```

---

## Task 4: The list query hook

**Files:**
- Create: `web/src/admin/invites/useInvites.ts`
- Test: `web/src/admin/invites/useInvites.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/invites/useInvites.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useInvites } from './useInvites'
import type { InvitesPage } from './api'

const ROW = {
  id: 'i1',
  created_at: '2026-08-01T09:00:00Z',
  expires_at: '2026-08-04T09:00:00Z',
  created_by: 'u1',
  created_by_email: 'admin@studio.dev',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["invites", sort, cursor] and passes both through', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/invites', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useInvites('expires_at', 'cur1'), {
    wrapper: makeWrapper(client),
  })

  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(params?.get('sort')).toBe('expires_at')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('limit')).toBe('50')

  const cached = client.getQueryData<InvitesPage>(['invites', 'expires_at', 'cur1'])
  expect(cached?.items[0].id).toBe('i1')
})

test('does not poll - invites are not live data', async () => {
  let calls = 0
  server.use(
    http.get('/v1/invites', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useInvites('-created_at', ''), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // Long enough that a copy-pasted refetchInterval (the live list hooks use
  // 3000ms, but 150ms catches any small value too) would have fired.
  await new Promise((r) => setTimeout(r, 150))
  expect(calls).toBe(1)

  // Positive control on the SAME counter: the instrument can move, so the
  // assertion above is about polling and not about a dead counter.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/invites/useInvites.test.tsx`

Expected: FAIL at import time - `Failed to resolve import "./useInvites"`.

- [ ] **Step 3: Implement**

Create `web/src/admin/invites/useInvites.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listInvites, type InvitesPage, type InviteSort } from './api'

// The list query for the admin Invites tab. Same shape as useAgentEnrollments
// (web/src/admin/enrollments/useAgentEnrollments.ts:14-20), including the
// deliberate absence of refetchInterval: this is not live data, so polling it is
// pointless load. Freshness of the EXPIRING/EXPIRED pill comes from useNow, a
// local 60s clock tick that issues no request; freshness of the ROW SET comes from
// useInviteActions invalidating the bare ['invites'] prefix.
//
// keepPreviousData keeps rows visible while a new sort/page loads, which is also
// what makes isPlaceholderData usable to disable the pager mid-fetch.
export function useInvites(sort: InviteSort, cursor: string) {
  return useQuery<InvitesPage>({
    queryKey: ['invites', sort, cursor],
    queryFn: () => listInvites({ sort, cursor }),
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/invites/useInvites.test.tsx`

Expected: PASS, 2 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/invites/useInvites.ts web/src/admin/invites/useInvites.test.tsx
git commit -m "feat(web): invites list query hook"
```

---

## Task 5: `useInviteActions` - the token-retention rule (RED #1)

**This is the highest-stakes task in the slice.** The mutation's result holds a credential that grants account creation, and its **variables** hold the invitee's email address - a second asset, and one that also lives in the `mutationFn` closure that `@tanstack/query-core`'s `mutationObserver` builds from `this.options` and does not replace on post-success re-renders. `JSON.stringify(m.state)` can never see a closure (`docs/retros/2026-08-12-profile-pages.md:171-184`). The only assertion that covers both is **the mutation cache is EMPTY**.

Three library facts, read out of `web/node_modules/@tanstack/query-core/build/modern/` and not to be re-derived:

- `Mutation.optionalRemove` removes the entry **only when `#observers.length === 0`** (`mutation.js:47-55`), and `removeObserver` is what schedules the GC (`:38-46`). So `gcTime: 0` alone evicts nothing while the hook is mounted, and `reset()` alone evicts nothing either - `MutationObserver.reset()` detaches the observer but the `Mutation` then sits in the cache for the default 5-minute `gcTime`. **Both are required.**
- `Removable.updateGcTime` is `Math.max(this.gcTime || 0, newGcTime ?? 5 * 60 * 1000)` (`removable.js:18-23`) and `isValidTimeout(0)` is true, so `gcTime: 0` genuinely means "next tick".
- `Mutation.execute` awaits the hook-level `options.onSuccess` at `mutation.js:123` and dispatches `{type:'success'}` only at `:144`. **A `reset()` placed inside `onSuccess` therefore detaches the observer BEFORE the success notification**: `isSuccess` never becomes true, `data` never lands on the observer's result, and the reveal dialog - which reads `create.data` - never opens. `reset()` lives at exactly three UI-driven sites (Task 8) and **never** in `onSuccess`. The third test below is the guard for that.

**Files:**
- Create: `web/src/admin/invites/useInviteActions.ts`
- Test: `web/src/admin/invites/useInviteActions.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/invites/useInviteActions.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useInviteActions } from './useInviteActions'
import { useInvites } from './useInvites'

const TOKEN = 'f00dcafe'.repeat(8)
// Distinct from every email in the list fixtures on purpose: it is the SECOND
// asset this mutation carries, and it must be traceable independently of the token.
const EMAIL = 'invitee-secret@studio.dev'

const ROW = {
  id: 'i1',
  created_at: '2026-08-01T09:00:00Z',
  expires_at: '2026-08-04T09:00:00Z',
  created_by: 'u1',
  created_by_email: 'admin@studio.dev',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function created() {
  return HttpResponse.json(
    { id: 'i9', token: TOKEN, expires_at: ROW.expires_at, email: EMAIL },
    { status: 201 },
  )
}

test('create POSTs the exact body and invalidates the BARE ["invites"] prefix', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/invites', async ({ request }) => {
      body = await request.json()
      return created()
    }),
  )
  const { result } = renderHook(() => useInviteActions(), { wrapper: makeWrapper(client) })
  const out = await result.current.create.mutateAsync({ email: EMAIL, expires_in: '72h' })

  expect(body).toEqual({ email: EMAIL, expires_in: '72h' })
  expect(out.token).toBe(TOKEN)
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['invites'] }))

  // The decoupling lesson from web/src/jobs/queryKeyDecoupling.test.tsx: a
  // fully-qualified key only refetches the sort/page combination that happens to
  // be mounted. EVERY call must use the bare prefix.
  for (const call of spy.mock.calls) {
    expect((call[0] as { queryKey: unknown[] }).queryKey).toEqual(['invites'])
  }
})

test('creating refetches a MOUNTED invites list (active observer, not a cache seed)', async () => {
  let listCalls = 0
  server.use(
    http.get('/v1/invites', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.post('/v1/invites', () => created()),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)

  // The list query MUST be mounted via renderHook so it has an ACTIVE OBSERVER.
  // A client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter what key the mutation invalidated.
  const { result: list } = renderHook(() => useInvites('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useInviteActions(), { wrapper })
  await actions.current.create.mutateAsync({ expires_in: '72h' })

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})

// RED #1 for the token-retention rule. Fails against a mutation without
// gcTime: 0, because reset() only DETACHES the observer - the underlying Mutation
// then sits in the cache for the default 5-minute gcTime with the token in
// state.data, the invitee email in state.variables, AND the same email captured in
// the mutationFn closure that no state stringify can reach.
test('after reset() the settled create mutation leaves the mutation cache entirely', async () => {
  const client = newClient()
  server.use(http.post('/v1/invites', () => created()))
  const { result } = renderHook(() => useInviteActions(), { wrapper: makeWrapper(client) })
  await result.current.create.mutateAsync({ email: EMAIL, expires_in: '72h' })

  // POSITIVE CONTROL, taken BEFORE reset() and therefore while the observer is
  // still attached. Mutation.optionalRemove only removes when observers.length
  // === 0 (query-core mutation.js:47-55), so a settled mutation with a live
  // observer stays put even with gcTime: 0 - which is exactly why the control is
  // valid here and would be vacuous after the reset below.
  const held = client.getMutationCache().getAll()
  expect(held).toHaveLength(1)
  expect(JSON.stringify(held[0].state)).toContain(TOKEN)
  expect(JSON.stringify(held[0].state)).toContain(EMAIL)

  act(() => {
    result.current.create.reset()
  })

  // EMPTY, not "no entry stringifies to contain the secret". The 2026-08-12
  // profile-pages slice found a plaintext secret surviving in the settled
  // mutation's mutationFn CLOSURE, which mutationObserver builds from
  // this.options and does not replace on post-success re-renders; a
  // JSON.stringify(m.state) assertion can never see a closure
  // (docs/retros/2026-08-12-profile-pages.md:171-184). This mutation passes
  // variables, so the closure really does hold the invitee email. Do NOT weaken
  // this back to a state check.
  await waitFor(() => expect(client.getMutationCache().getAll()).toHaveLength(0))
})

// Guard for the ordering trap, and it PASSES ON FIRST WRITE - its RED is produced
// by the mutate-and-revert in Step 4b, not by the missing implementation.
// Mutation.execute awaits the hook-level onSuccess at query-core mutation.js:123
// and only THEN dispatches {type:'success'} at :144, while
// MutationObserver.reset() detaches the observer (mutationObserver.js:50-55). So a
// reset() inside onSuccess silently kills the success state: isSuccess stays
// false, data stays undefined, and InvitesTab's reveal dialog - which renders iff
// create.data exists - never opens. reset() belongs at the three UI-driven sites
// in InvitesTab, never here.
test('a settled create still exposes data and isSuccess - reset() must NOT live in onSuccess', async () => {
  const client = newClient()
  server.use(http.post('/v1/invites', () => created()))
  const { result } = renderHook(() => useInviteActions(), { wrapper: makeWrapper(client) })
  await result.current.create.mutateAsync({ expires_in: '72h' })

  await waitFor(() => expect(result.current.create.isSuccess).toBe(true))
  expect(result.current.create.data?.token).toBe(TOKEN)
})

test('a create failure surfaces the ApiError and does not invalidate', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/invites', () =>
      HttpResponse.json({ error: 'invalid email address' }, { status: 400 }),
    ),
  )
  const { result } = renderHook(() => useInviteActions(), { wrapper: makeWrapper(client) })
  await expect(
    result.current.create.mutateAsync({ email: 'nope', expires_in: '72h' }),
  ).rejects.toMatchObject({ status: 400 })
  expect(spy).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/invites/useInviteActions.test.tsx`

Expected: FAIL at import time - `Failed to resolve import "./useInviteActions"`.

- [ ] **Step 3: Implement**

Create `web/src/admin/invites/useInviteActions.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createInvite, type CreateInviteBody } from './api'

// Mutations for the admin Invites tab. Plural name for a single mutation, matching
// useAgentEnrollmentActions and useAdminUserActions, so a future second action is
// an addition here rather than a rename of the module.
//
// SECURITY - read before editing:
//  - create.data holds the RAW invite token, which grants account creation on this
//    server, and create.variables holds the INVITEE EMAIL. TanStack retains a
//    mutation's data AND variables for the mutation's lifetime, and the same email
//    is additionally captured in the mutationFn CLOSURE that mutationObserver
//    builds from this.options - a place no state inspection can reach. So the test
//    that guards this asserts the mutation cache is EMPTY, not that one field of
//    one entry is clean (docs/retros/2026-08-12-profile-pages.md:171-184).
//  - gcTime: 0 and create.reset() are BOTH required and neither is sufficient.
//    reset() only detaches the observer (MutationObserver.reset() clears
//    currentMutation; it does not delete the underlying Mutation), and
//    Mutation.optionalRemove refuses to remove while any observer is attached
//    (query-core mutation.js:47-55). With the default 5-minute gcTime the token
//    and the email would stay readable in queryClient.getMutationCache() long
//    after the admin clicked Done. gcTime: 0 makes the now-observer-less mutation
//    eligible for removal on the very next tick once reset() detaches it.
//    DO NOT DELETE gcTime: 0 as redundant - useInviteActions.test.tsx goes RED.
//  - reset() is NEVER called here. Mutation.execute awaits this onSuccess at
//    query-core mutation.js:123 and dispatches the success action only at :144, so
//    a reset() inside onSuccess would detach the observer before the notification:
//    isSuccess would never become true, data would never arrive, and InvitesTab's
//    reveal dialog would never open - silently. reset() lives at the three
//    UI-driven sites in InvitesTab (dialog onDone, panel cancel, panel reopen).
//  - No onSuccess logging, ever. The success payload is a credential.
//  - No optimistic append: the 201 echoes no created_at and no created_by_email
//    (internal/api/invites.go:79-87), so a locally synthesised row would be partly
//    invented.
export function useInviteActions() {
  const qc = useQueryClient()

  const create = useMutation({
    mutationFn: (body: CreateInviteBody) => createInvite(body),
    gcTime: 0,
    // BARE prefix, never a fully-qualified key, so every mounted
    // ['invites', sort, cursor] combination refetches (see
    // web/src/jobs/queryKeyDecoupling.test.tsx).
    onSuccess: () => qc.invalidateQueries({ queryKey: ['invites'] }),
  })

  return { create }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/invites/useInviteActions.test.tsx`

Expected: PASS, 5 tests.

- [ ] **Step 4b: Prove the ordering guard is not vacuous (mutate, record, revert)**

The `reset() must NOT live in onSuccess` test passed on first write, so it needs a substitute RED. Temporarily change `useInviteActions.ts`:

```ts
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['invites'] })
      create.reset() // TEMPORARY - about to be reverted
    },
```

(To make that compile, hoist it as `const create: ReturnType<typeof useMutation> = ...` or simply inline `qc.getMutationCache().clear()` in its place - either produces the same detachment. The point is to remove the observer inside `onSuccess`.)

Run: `npx vitest run src/admin/invites/useInviteActions.test.tsx -t 'reset\(\) must NOT live in onSuccess'`

Expected: FAIL - `expected false to be true` on `isSuccess`, and `create.data` undefined. **Record the exact output in the task report**, then revert the file to the Step 3 version and re-run the whole file to confirm PASS, 5 tests. The permanent test stays; only the implementation mutation is reverted.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/invites/useInviteActions.ts web/src/admin/invites/useInviteActions.test.tsx
git commit -m "feat(web): invite create mutation with gcTime 0 and bare-prefix invalidation"
```

---

## Task 6: `InvitesTable`

Six columns. Mirror `EnrollmentsTable.tsx` exactly, including the `Table` composition, the `opacity-[0.55]` terminal dimming and the ASCII hyphen placeholder.

**Files:**
- Create: `web/src/admin/invites/InvitesTable.tsx`
- Test: `web/src/admin/invites/InvitesTable.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/invites/InvitesTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { InvitesTable } from './InvitesTable'
import type { Invite, InviteSort } from './api'

const NOW = new Date('2026-08-09T12:00:00Z')
const CREATOR_UUID = '11111111-2222-3333-4444-555555555555'

function row(over: Partial<Invite> = {}): Invite {
  return {
    id: 'i1',
    created_at: '2026-08-01T09:00:00Z',
    expires_at: '2026-08-10T09:00:00Z',
    created_by: CREATOR_UUID,
    created_by_email: 'admin@studio.dev',
    email: 'invitee@studio.dev',
    ...over,
  }
}

function renderTable(over: Partial<Parameters<typeof InvitesTable>[0]> = {}) {
  const props = {
    invites: [row()],
    sort: '-created_at' as InviteSort,
    onSort: vi.fn(),
    now: NOW,
    ...over,
  }
  return { props, ...render(<InvitesTable {...props} />) }
}

test('renders all six headers', () => {
  renderTable()
  for (const label of ['BINDS TO', 'CREATED', 'EXPIRES', 'CREATED BY', 'STATUS', 'NOTE']) {
    expect(screen.getByRole('columnheader', { name: new RegExp(label) })).toBeInTheDocument()
  }
})

test('only CREATED and EXPIRES are sortable; the other four carry no aria-sort', () => {
  renderTable()
  expect(screen.getByRole('columnheader', { name: /CREATED BY/ })).not.toHaveAttribute('aria-sort')
  expect(screen.getByRole('columnheader', { name: /BINDS TO/ })).not.toHaveAttribute('aria-sort')
  expect(screen.getByRole('columnheader', { name: /STATUS/ })).not.toHaveAttribute('aria-sort')
  expect(screen.getByRole('columnheader', { name: /NOTE/ })).not.toHaveAttribute('aria-sort')
  expect(screen.getByRole('columnheader', { name: /^CREATED / })).toHaveAttribute(
    'aria-sort',
    'descending',
  )
  expect(screen.getByRole('columnheader', { name: /EXPIRES/ })).toHaveAttribute('aria-sort', 'none')
  expect(screen.getByRole('button', { name: 'CREATED ▼' })).toBeInTheDocument()
})

test('ascending sort shows an ascending caret', () => {
  renderTable({ sort: 'expires_at' })
  expect(screen.getByRole('columnheader', { name: /EXPIRES/ })).toHaveAttribute(
    'aria-sort',
    'ascending',
  )
  expect(screen.getByRole('button', { name: 'EXPIRES ▲' })).toBeInTheDocument()
})

test('clicking a sortable header calls onSort with that field', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: /^CREATED/ }))
  expect(props.onSort).toHaveBeenCalledWith('created_at')
  await userEvent.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  expect(props.onSort).toHaveBeenCalledWith('expires_at')
})

test('a bound invite shows the email; an unbound one shows a plain ASCII hyphen', () => {
  renderTable()
  expect(screen.getByText('invitee@studio.dev')).toBeInTheDocument()

  const { email: _drop, ...unbound } = row()
  render(<InvitesTable invites={[unbound]} sort="-created_at" onSort={vi.fn()} now={NOW} />)
  // The key is ABSENT, not null (internal/api/invites.go:139-141), so the cell
  // renders a placeholder that means "not bound to an address" - a real state, not
  // missing data. House rule: ASCII hyphen, never the hi-fi's em dash (:2111).
  expect(screen.getAllByText('-').length).toBeGreaterThan(0)
  expect(screen.queryByText(/undefined/)).not.toBeInTheDocument()
  expect(screen.queryByText(/null/)).not.toBeInTheDocument()
  expect(screen.queryByText('—')).not.toBeInTheDocument()
})

test('CREATED BY renders the joined email and never the raw creator UUID', () => {
  renderTable()
  expect(screen.getByText('admin@studio.dev')).toBeInTheDocument()
  // The list query joins users (internal/store/query/invites.sql:32), which is the
  // one hi-fi column the enrollments table could not fill. A 36-character UUID
  // would be unusable, so created_by is carried on the type but never rendered.
  expect(screen.queryByText(CREATOR_UUID)).not.toBeInTheDocument()
})

test('the created date renders as a plain YYYY-MM-DD slice', () => {
  renderTable()
  expect(screen.getByText('2026-08-01')).toBeInTheDocument()
})

test('the status pill renders all four derivable states', () => {
  renderTable({
    invites: [
      row({ id: 'a', expires_at: '2026-08-10T12:00:00Z' }), // 24h left
      row({ id: 'b', expires_at: '2026-08-09T12:30:00Z' }), // 30m left
      row({ id: 'c', expires_at: '2026-08-09T11:00:00Z' }), // past
      row({ id: 'd', used_at: '2026-08-02T10:00:00Z' }),
    ],
  })
  expect(screen.getByText('ACTIVE')).toBeInTheDocument()
  expect(screen.getByText('EXPIRING')).toBeInTheDocument()
  expect(screen.getByText('EXPIRED')).toBeInTheDocument()
  expect(screen.getByText('REDEEMED')).toBeInTheDocument()
})

test('the NOTE cell reads three different ways', () => {
  renderTable({
    invites: [
      row({ id: 'a', expires_at: '2026-08-10T12:00:00Z' }),
      row({ id: 'c', expires_at: '2026-08-09T11:00:00Z' }),
      row({ id: 'd', used_at: '2026-08-02T10:00:00Z' }),
    ],
  })
  expect(screen.getByText('copy token only on creation')).toBeInTheDocument()
  // The ONLY consumer of used_at's VALUE rather than its presence.
  expect(screen.getByText('redeemed 2026-08-02')).toBeInTheDocument()
  expect(screen.getAllByText('-').length).toBeGreaterThan(0)
})

test('terminal rows are dimmed and active rows are not', () => {
  const { container } = renderTable({
    invites: [
      row({ id: 'a', expires_at: '2026-08-10T12:00:00Z' }),
      row({ id: 'c', expires_at: '2026-08-09T11:00:00Z' }),
      row({ id: 'd', used_at: '2026-08-02T10:00:00Z' }),
    ],
  })
  const rows = Array.from(container.querySelectorAll('[role="row"]')).slice(1) // drop the header row
  expect(rows[0].className).not.toContain('opacity-[0.55]')
  expect(rows[1].className).toContain('opacity-[0.55]')
  expect(rows[2].className).toContain('opacity-[0.55]')
})

test('there is no revoke, delete or resend control anywhere in the table', () => {
  renderTable({ invites: [row(), row({ id: 'i2' })] })
  expect(screen.queryAllByRole('button', { name: /revoke|delete|resend/i })).toHaveLength(0)
  // The only buttons in the table are the two sortable headers - two rows of data
  // add none. There is no DELETE, PATCH or resend route for invites
  // (internal/api/server.go:142-143 is the whole surface), so a control would be a
  // guaranteed-failing dead affordance.
  expect(screen.getAllByRole('button')).toHaveLength(2)
  expect(screen.queryByText('ACTIONS')).not.toBeInTheDocument()
})

test('the absence query above is NOT vacuous: it finds such a button when one exists', () => {
  // Without this control the assertion above would also pass against a table that
  // renders no buttons at all for an unrelated reason, or against a query whose
  // regex never matches anything.
  render(
    <>
      <InvitesTable invites={[row()]} sort="-created_at" onSort={vi.fn()} now={NOW} />
      <button type="button">Revoke invite</button>
    </>,
  )
  expect(screen.queryAllByRole('button', { name: /revoke|delete|resend/i })).toHaveLength(1)
})

test('no token, hash or prefix is rendered - no cell holds a 64-hex string', () => {
  const { container } = renderTable()
  expect(screen.queryByText('TOKEN PREFIX')).not.toBeInTheDocument()
  expect(container.textContent).not.toMatch(/[0-9a-f]{64}/i)
})

test('a different now re-derives the pill and the label from the same row', () => {
  renderTable({ now: new Date('2026-08-10T08:59:30Z') }) // 30s before expiry
  expect(screen.getByText('EXPIRING')).toBeInTheDocument()
  // Not "in 30s": the table's `now` comes from a 60s clock tick (useNow), so a
  // seconds-precision label would go stale for up to 59 more real seconds after
  // the render that produced it - see inviteStatus.formatExpiryLabel.
  expect(screen.getByText('in <1m')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/invites/InvitesTable.test.tsx`

Expected: FAIL at import time - `Failed to resolve import "./InvitesTable"`.

- [ ] **Step 3: Implement**

Create `web/src/admin/invites/InvitesTable.tsx`:

```tsx
import { Chip, Table, TableCell, TableRow, type TableColumn } from '../../components/holo'
import { deriveStatus, formatExpiryLabel, statusTone } from './inviteStatus'
import type { Invite, InviteSort, InviteSortField } from './api'

// BINDS TO | CREATED | EXPIRES | CREATED BY | STATUS | NOTE.
//
// Against the hi-fi's header row (hifi3-holo-pages.jsx:2096):
//  - TOKEN PREFIX is DROPPED. Only tokenhash.Hash(rawHex) is stored
//    (internal/api/invites.go:56) and the list query cannot select it - omitting
//    i.token_hash from the projection IS the endpoint's security control
//    (internal/store/query/invites.sql:22-25). A prefix column would mean
//    persisting a fragment of a secret for cosmetics.
//  - CREATED BY is KEPT and filled with an EMAIL, because this list query joins
//    users (invites.sql:32). This is the one hi-fi column the enrollments table
//    could not fill; the bare created_by UUID is never rendered.
//  - CREATED is ADDED because it is the default sort key and needs a clickable
//    header (same reason as EnrollmentsTable.tsx:15).
//  - ACTIONS is renamed NOTE. The cell holds prose in the hi-fi too
//    (:2119-2121), and a header promising actions while delivering a sentence is
//    itself a dead affordance. There is no revoke, delete or resend route.
//
// Sortable headers ship even though the hi-fi has no sort control on this page:
// the endpoint supports both keys in both directions, Table makes the headers
// free, and the sketch's omission is a fidelity gap rather than a constraint.
const COLS = 'grid-cols-[1.5fr_110px_110px_1.4fr_110px_1fr]'

const HEADERS: TableColumn<InviteSortField>[] = [
  { label: 'BINDS TO' },
  { label: 'CREATED', field: 'created_at' },
  { label: 'EXPIRES', field: 'expires_at' },
  { label: 'CREATED BY' },
  { label: 'STATUS' },
  { label: 'NOTE', align: 'right' },
]

interface InvitesTableProps {
  invites: Invite[]
  sort: InviteSort
  onSort: (field: InviteSortField) => void
  // Injected so the pill and the relative label are pure functions of props. The
  // tab supplies useNow(60_000); tests supply a fixed Date.
  now: Date
}

export function InvitesTable({ invites, sort, onSort, now }: InvitesTableProps) {
  return (
    <div className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]">
      <Table
        label="Invites"
        columns={COLS}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-[18px] py-3 tracking-[0.16em]"
      >
        {invites.map((inv) => {
          const status = deriveStatus(inv, now)
          const terminal = status === 'REDEEMED' || status === 'EXPIRED'
          return (
            <TableRow
              key={inv.id}
              className={`border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
                terminal ? 'opacity-[0.55]' : ''
              }`}
            >
              {/* The key is ABSENT (not null) when the invite is not email-bound
                  (internal/api/invites.go:139-141), so this is a plain ASCII
                  hyphen placeholder - never an em dash - and it means "not bound
                  to an address", a real state rather than missing data. */}
              <TableCell className="truncate font-sans text-[12.5px] text-fg">
                {inv.email ?? <span className="text-fg-dim">-</span>}
              </TableCell>
              <TableCell className="text-[10.5px] text-fg-mute">
                {inv.created_at.slice(0, 10)}
              </TableCell>
              <TableCell
                className={`text-[11px] ${status === 'ACTIVE' ? 'text-fg' : 'text-fg-mute'}`}
              >
                {formatExpiryLabel(inv.expires_at, now)}
              </TableCell>
              <TableCell className="truncate text-[11px] text-fg-mute">
                {inv.created_by_email}
              </TableCell>
              <TableCell>
                <Chip tone={statusTone(status)}>{status}</Chip>
              </TableCell>
              {/* Prose, not controls. The only consumer of used_at's VALUE rather
                  than its presence. */}
              <TableCell className="text-right text-[10.5px] tracking-[0.04em] text-fg-dim">
                {status === 'REDEEMED'
                  ? `redeemed ${(inv.used_at ?? '').slice(0, 10)}`
                  : status === 'EXPIRED'
                    ? '-'
                    : 'copy token only on creation'}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/invites/InvitesTable.test.tsx`

Expected: PASS, 14 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/invites/InvitesTable.tsx web/src/admin/invites/InvitesTable.test.tsx
git commit -m "feat(web): invites table with the four-state pill and no dead controls"
```

---

## Task 7: `CreateInviteForm`

Tab-local, not shared with `CreateEnrollmentForm` - the decision is already recorded at `CreateEnrollmentForm.tsx:22-25`, and the hi-fi's `isInvite` boolean is the flag-driven component that rots. An inline `GlassPanel` form, not a modal, so exactly one dialog is on screen at a time.

**Files:**
- Create: `web/src/admin/invites/CreateInviteForm.tsx`
- Test: `web/src/admin/invites/CreateInviteForm.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/invites/CreateInviteForm.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../lib/api'
import { CreateInviteForm } from './CreateInviteForm'

function renderForm(over: Partial<Parameters<typeof CreateInviteForm>[0]> = {}) {
  const props = {
    pending: false,
    error: null as Error | null,
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  }
  return { props, ...render(<CreateInviteForm {...props} />) }
}

test('submitting with a blank email sends ONLY an explicit expires_in', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))
  // No email key at all (not ''), and the 72h default as a literal. A body is
  // ALWAYS sent because readJSON runs unconditionally (internal/api/invites.go:27).
  expect(props.onSubmit).toHaveBeenCalledWith({ expires_in: '72h' })
})

test('an email is trimmed and the chosen preset is sent as its WIRE value', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), '  invitee@studio.dev  ')
  await userEvent.click(screen.getByRole('button', { name: '7d' }))
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))
  // "7d" is the LABEL. Go's time.ParseDuration has no day unit, so the wire value
  // is 168h - sending "7d" would be a 400.
  expect(props.onSubmit).toHaveBeenCalledWith({
    email: 'invitee@studio.dev',
    expires_in: '168h',
  })
})

test('exactly four presets, with 72h preselected', async () => {
  renderForm()
  for (const label of ['24h', '72h', '7d', '30d']) {
    expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
  }
  expect(screen.getByRole('button', { name: '72h' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '30d' })).toHaveAttribute('aria-pressed', 'false')

  await userEvent.click(screen.getByRole('button', { name: '30d' }))
  expect(screen.getByRole('button', { name: '30d' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '72h' })).toHaveAttribute('aria-pressed', 'false')
})

test('every preset submits an hour-denominated literal the server can parse', async () => {
  const cases: [string, string][] = [
    ['24h', '24h'],
    ['72h', '72h'],
    ['7d', '168h'],
    ['30d', '720h'],
  ]
  for (const [label, wire] of cases) {
    const { props, unmount } = renderForm()
    await userEvent.click(screen.getByRole('button', { name: label }))
    await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))
    expect(props.onSubmit).toHaveBeenCalledWith({ expires_in: wire })
    expect(wire).toMatch(/^\d+h$/)
    unmount()
  }
})

test('there is no free-form duration field, so the 0 < d <= 720h bound is unreachable', () => {
  renderForm()
  expect(screen.queryByLabelText(/expires_in/i)).not.toBeInTheDocument()
  expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
  // Exactly one text-entry control: the email. Nothing else can carry a duration.
  expect(screen.getAllByRole('textbox')).toHaveLength(1)
})

test('the email input is type=email so the browser gives a first pass', () => {
  renderForm()
  // The client does NOT reimplement mail.ParseAddress (internal/api/invites.go:66).
  // Two parsers disagreeing is worse than one round trip; the server's 400 renders
  // in this form's own error slot.
  expect(screen.getByLabelText('Email')).toHaveAttribute('type', 'email')
})

test('states up front that the raw token is returned once', () => {
  renderForm()
  expect(screen.getByText(/returned once/i)).toBeInTheDocument()
  expect(screen.getByText(/cannot be retrieved again/i)).toBeInTheDocument()
})

test('pending disables submit', () => {
  renderForm({ pending: true })
  expect(screen.getByRole('button', { name: 'Create invite' })).toBeDisabled()
})

test('a server error renders inside the panel and the form keeps its state', async () => {
  const { rerender } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), 'invitee@studio.dev')
  rerender(
    <CreateInviteForm
      pending={false}
      error={new ApiError(400, 'invalid email address', '400 invalid email address')}
      onSubmit={vi.fn()}
      onCancel={vi.fn()}
    />,
  )
  // The form owns its own error surface; nothing routes to a page-level box, which
  // would render behind an overlay if one were open.
  expect(screen.getByText('400 invalid email address')).toBeInTheDocument()
  expect(screen.getByLabelText('Email')).toHaveValue('invitee@studio.dev')
})

test('Cancel calls onCancel and does not submit', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onCancel).toHaveBeenCalledTimes(1)
  expect(props.onSubmit).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/invites/CreateInviteForm.test.tsx`

Expected: FAIL at import time - `Failed to resolve import "./CreateInviteForm"`.

- [ ] **Step 3: Implement**

Create `web/src/admin/invites/CreateInviteForm.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { GlassPanel, PillButton } from '../../components/holo'
import { Input } from '../../components/Input'
import { DEFAULT_EXPIRES_IN, TTL_PRESETS, type CreateInviteBody } from './api'

interface CreateInviteFormProps {
  pending: boolean
  error: Error | null
  onSubmit: (body: CreateInviteBody) => void
  onCancel: () => void
}

const PRESET = 'flex-1 rounded-[6px] border px-2.5 py-1.5 font-mono text-[11px] tracking-[0.06em]'
const PRESET_ON = `${PRESET} border-accent/60 bg-accent/20 text-fg`
const PRESET_OFF = `${PRESET} border-border bg-white/[0.04] text-fg-mute`

// Inline create panel, mirroring CreateEnrollmentForm rather than the hi-fi's
// modal: it keeps exactly one un-trapped dialog on screen at a time - the reveal -
// and adds no modal machinery for two fields.
//
// Deliberately tab-local and NOT shared with CreateEnrollmentForm, which already
// records the reason at CreateEnrollmentForm.tsx:22-25: invites take an email that
// BINDS the invite, different presets, and a different endpoint. The hi-fi models
// the divergence with an `isInvite` boolean, which is the flag-driven component
// that rots. Only the reveal half is shared.
export function CreateInviteForm({ pending, error, onSubmit, onCancel }: CreateInviteFormProps) {
  const [email, setEmail] = useState('')
  const [expiresIn, setExpiresIn] = useState(DEFAULT_EXPIRES_IN)

  function submit(e: FormEvent) {
    e.preventDefault()
    const trimmed = email.trim()
    // expiresIn is always one of TTL_PRESETS, every value of which is
    // hour-denominated and inside the server's (0, 720h] window (asserted in
    // api.test.ts), so there is no client-side duration validation to do - the
    // invalid range is UNREACHABLE rather than merely rejected. No client-side
    // email validation either: the server runs mail.ParseAddress
    // (internal/api/invites.go:66) and its 400 renders in this form's own error
    // slot. Two parsers disagreeing is worse than one round trip.
    //
    // A body is ALWAYS produced, minimally {expires_in}, because readJSON runs
    // unconditionally on this endpoint (invites.go:27) and an empty POST 400s.
    onSubmit(trimmed ? { email: trimmed, expires_in: expiresIn } : { expires_in: expiresIn })
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field
        label="Email"
        htmlFor="new-invite-email"
        hint="Optional. Binds the invite to one address. Omitted from the request when blank."
      >
        <Input
          id="new-invite-email"
          type="email"
          placeholder="someone@studio.dev"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
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
              aria-pressed={expiresIn === p.value}
              onClick={() => setExpiresIn(p.value)}
              className={expiresIn === p.value ? PRESET_ON : PRESET_OFF}
            >
              {p.label}
            </button>
          ))}
        </div>
        {/* The label says 30d and the wire says 720h; the hint states the server's
            own vocabulary so the two are never confused. */}
        <div className="mt-1 font-mono text-[10.5px] text-fg-dim">
          expires_in - server default 72h, max 720h
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
          Create invite
        </PillButton>
      </div>
    </GlassPanel>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/invites/CreateInviteForm.test.tsx`

Expected: PASS, 10 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/invites/CreateInviteForm.tsx web/src/admin/invites/CreateInviteForm.test.tsx
git commit -m "feat(web): invite create form with hour-denominated TTL presets"
```

---

## Task 8: `InvitesTab` - the composition point

Mirror `EnrollmentsTab.tsx:1-222` structurally. Two deliberate divergences, both listed in the code comments: the footer says **all states** (this endpoint applies no filter), and the empty state carries **no prev escape hatch** (spec decision 17).

**Files:**
- Create: `web/src/admin/invites/InvitesTab.tsx`
- Test: `web/src/admin/invites/InvitesTab.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/invites/InvitesTab.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { InvitesTab } from './InvitesTab'
import type { Invite } from './api'

const TOKEN = 'f00dcafe'.repeat(8)

function row(over: Partial<Invite> = {}): Invite {
  return {
    id: 'i1',
    created_at: '2026-08-01T09:00:00Z',
    expires_at: '2026-08-10T09:00:00Z',
    created_by: 'u1',
    created_by_email: 'admin@studio.dev',
    email: 'invitee@studio.dev',
    ...over,
  }
}

// InvitesTab does not use useAuth, so no AuthProvider and no /v1/users/me handler
// are needed - unlike the UsersTab tests.
function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <InvitesTab />
      </QueryClientProvider>,
    ),
  }
}

function listHandler(
  seen: URLSearchParams[],
  envelope: (p: URLSearchParams) => { items: Invite[]; next_cursor: string; total: number },
) {
  return http.get('/v1/invites', ({ request }) => {
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
  expect(await screen.findByText('invitee@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('GET /v1/invites')).toBeInTheDocument()
  expect(seen[0].get('sort')).toBe('-created_at')
  expect(seen[0].get('limit')).toBe('50')
})

test('shows the loading skeleton, then rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  expect(container.querySelectorAll('.h-9').length).toBeGreaterThan(0)
  expect(await screen.findByText('invitee@studio.dev')).toBeInTheDocument()
})

test('shows an error card whose Retry refetches', async () => {
  let calls = 0
  server.use(
    http.get('/v1/invites', () => {
      calls++
      if (calls === 1) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [row()], next_cursor: '', total: 1 })
    }),
  )
  renderTab()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('invitee@studio.dev')).toBeInTheDocument()
})

test('shows the empty card when there are no invites, with no prev hatch', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [], next_cursor: '', total: 0 })))
  renderTab()
  expect(await screen.findByText('No invites yet.')).toBeInTheDocument()
  // Unlike EnrollmentsTab, this list is UNFILTERED and nothing deletes or reaps an
  // invite, so a non-first page landing on zero rows is unreachable. Shipping an
  // untestable escape hatch would be dead code (spec decision 17).
  expect(screen.queryByRole('button', { name: /prev/ })).not.toBeInTheDocument()
})

test('sort header clicks issue the four exact server sort values and reset the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: 'c2', total: 9 })))
  renderTab()
  await screen.findByText('invitee@studio.dev')

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
      items: [
        row({
          id: p.get('cursor') ? 'i2' : 'i1',
          email: p.get('cursor') ? 'page-two@studio.dev' : 'page-one@studio.dev',
        }),
      ],
      next_cursor: p.get('cursor') ? '' : 'c2',
      total: 2,
    })),
  )
  renderTab()
  await screen.findByText('page-one@studio.dev')
  expect(screen.getByText('1-1 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('page-two@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('2-2 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /next 50/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /prev/ }))
  expect(await screen.findByText('page-one@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('1-1 of 2')).toBeInTheDocument()
})

test('the footer names the endpoint and says ALL states are shown', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('invitee@studio.dev')
  // Unlike /v1/agent-enrollments, this endpoint applies NO filter
  // (internal/api/invites.go:148-250 reads no filter param), so every state is on
  // the page and `total` is the unfiltered COUNT(*) (invites.sql:55-61) - the
  // footer range can never state a number the admin cannot page to.
  expect(screen.getByText(/\/v1\/invites \(all states\)/)).toBeInTheDocument()
  expect(screen.queryByText(/active only/)).not.toBeInTheDocument()
})

test('the footnote states that expiry and redemption are the only terminal states', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('invitee@studio.dev')
  expect(screen.getByText(/one-time/i)).toBeInTheDocument()
  expect(screen.getByText(/no revoke endpoint in v1/i)).toBeInTheDocument()
  expect(screen.getByText(/only terminal states/i)).toBeInTheDocument()
})

test('the tab renders no revoke, delete or resend control at all', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('invitee@studio.dev')
  expect(screen.queryAllByRole('button', { name: /revoke|delete|resend/i })).toHaveLength(0)
})

test('creating posts the exact body, opens the reveal dialog, and refreshes the list', async () => {
  const seen: URLSearchParams[] = []
  let body: unknown
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/invites', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(
        { id: 'i9', token: TOKEN, expires_at: row().expires_at, email: 'new@studio.dev' },
        { status: 201 },
      )
    }),
  )
  renderTab()
  await screen.findByText('invitee@studio.dev')
  const listCallsBefore = seen.length

  await userEvent.click(screen.getByRole('button', { name: '+ Create invite' }))
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))

  const dialog = await screen.findByRole('dialog')
  expect(body).toEqual({ email: 'new@studio.dev', expires_in: '72h' })
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)
  expect(dialog).toHaveTextContent(/cannot be retrieved again/i)
  expect(dialog).toHaveTextContent(/expires/i)
  // The inline panel closes behind the dialog.
  expect(screen.queryByLabelText('Email')).not.toBeInTheDocument()
  // The bare-prefix invalidation refetched the mounted list.
  await waitFor(() => expect(seen.length).toBeGreaterThan(listCallsBefore))

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('a create error renders inside the panel and leaves the table mounted', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/invites', () =>
      HttpResponse.json({ error: 'invalid email address' }, { status: 400 }),
    ),
  )
  renderTab()
  await screen.findByText('invitee@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: '+ Create invite' }))
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))

  expect(await screen.findByText('400 invalid email address')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByText('invitee@studio.dev')).toBeInTheDocument()

  // Reopening the panel clears the stale error - and, critically, a stale
  // create.data that would otherwise re-open the reveal dialog.
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: '+ Create invite' }))
  expect(screen.queryByText('400 invalid email address')).not.toBeInTheDocument()
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

Run: `npx vitest run src/admin/invites/InvitesTab.test.tsx`

Expected: FAIL at import time - `Failed to resolve import "./InvitesTab"`.

- [ ] **Step 3: Implement**

Create `web/src/admin/invites/InvitesTab.tsx`:

```tsx
import { useState } from 'react'
import { Button } from '../../components/Button'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { useNow } from '../../lib/useNow'
import { TokenRevealDialog } from '../TokenRevealDialog'
import { CreateInviteForm } from './CreateInviteForm'
import { InvitesTable } from './InvitesTable'
import { useInviteActions } from './useInviteActions'
import { useInvites } from './useInvites'
import type { CreateInviteBody, InviteSort, InviteSortField } from './api'

// Same shape as EnrollmentsTab's toggleSort (EnrollmentsTab.tsx:16-21): clicking
// the active column flips its direction, clicking the other selects it ascending.
//
// SEVENTH consumer of the cursor-pager block below (JobsPage, WorkersPage,
// SchedulesPage, UsersTab, EnrollmentsTab, ReservationsTab are the first six), and
// FOURTH of this helper. Not extracted here on purpose: the extraction has to
// migrate six shipped surfaces under a zero-line-diff gate on their existing test
// files, which is its own slice with a different risk profile. See
// docs/superpowers/plans/2026-08-13-admin-invites-tab.md, "Extraction debt".
function toggleSort(field: InviteSortField, current: InviteSort): InviteSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as InviteSort
  }
  return field
}

export function InvitesTab() {
  const [sort, setSort] = useState<InviteSort>('-created_at')
  // Cursor of the current page (''=first); stack holds the cursors we paged
  // forward from; offsets tracks the real row offset so partial pages stay
  // correct. Same pattern as UsersTab / EnrollmentsTab.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
  const [creating, setCreating] = useState(false)

  // A local 60s clock tick, NOT a poll: it re-renders so relative labels and
  // status pills stay correct and issues no request.
  const now = useNow(60_000)

  const { data, error, isLoading, isPlaceholderData, refetch } = useInvites(sort, cursor)
  const { create } = useInviteActions()

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    setOffsets([])
  }

  function pickSort(field: InviteSortField) {
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

  function onCreate(body: CreateInviteBody) {
    // The reveal dialog is driven by create.data, so closing the panel here is all
    // that is needed on success; the hook's onSuccess does the invalidation. This
    // is a MUTATE-level callback (fired by MutationObserver after the success
    // dispatch, query-core mutationObserver.js:85-95), not the hook-level one, so
    // it cannot interfere with the success notification.
    create.mutate(body, { onSuccess: () => setCreating(false) })
  }

  const invites = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(startOffset, invites.length)
  const rangeText =
    invites.length === 0
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
  } else if (invites.length === 0) {
    // No prev escape hatch here, matching UsersTab rather than EnrollmentsTab.
    // That hatch exists there because the enrollments list is FILTERED
    // (consumed_at IS NULL AND expires_at > NOW()), so a row can vanish between
    // paging forward and the next fetch. This list applies no filter and nothing
    // deletes or reaps an invite, so a non-first page landing on zero rows is
    // unreachable - the hatch would be untestable dead code.
    body = (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
        No invites yet.
      </GlassPanel>
    )
  } else {
    body = (
      <>
        <InvitesTable invites={invites} sort={sort} onSort={pickSort} now={now} />
        <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
          <span>
            SHOWING <span className="text-fg">{rangeText}</span>
            {' · '}/v1/invites (all states) · CURSOR PAGINATED
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
          GET /v1/invites
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
          + Create invite
        </PillButton>
      </div>

      {creating && (
        <CreateInviteForm
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
        ▸ Invites are <span className="text-fg-mute">one-time</span>. The server returns the raw
        token only at creation, and there is no revoke endpoint in v1, so expiry or redemption are
        the only terminal states - prefer a short TTL. Email binding pins the invite to one address;
        an unbound invite can be redeemed by whoever holds the token. This list shows{' '}
        <span className="text-fg-mute">all states</span>, and the STATUS pill is derived in the
        browser from expires_at and used_at.
      </div>

      {/* Opens iff the mutation holds a result. The token is read straight from
          create.data and is never copied into state, so this is its only render
          site, and Done -> create.reset() both clears it and unmounts the dialog
          in one step: ending the generation IS releasing the resource, not a
          separate step that could be skipped. reset() must never move into the
          hook's onSuccess - query-core dispatches success only AFTER awaiting it
          (mutation.js:123 vs :144), so the detached observer would never see the
          success and this dialog would silently stop opening. */}
      {create.data && (
        <TokenRevealDialog
          token={create.data.token}
          title="Invite created"
          endpoint="POST /v1/invites"
          expiresAt={create.data.expires_at}
          onDone={() => create.reset()}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/invites/`

Expected: PASS across all seven invites test files written so far; `InvitesTab.test.tsx` contributes 12 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/invites/InvitesTab.tsx web/src/admin/invites/InvitesTab.test.tsx
git commit -m "feat(web): invites tab composition with cursor paging and reveal dialog"
```

---

## Task 9: The token-secrecy suite (RED #2, integration level)

Task 5 proved the retention rule at the hook boundary with a genuine first-write RED. **This task was believed to prove it again through the real components via a second, independent cause** - removing `create.reset()` from the dialog's `onDone` - **but that claim did not hold up under verification.** With `onDone={() => {}}`, this suite's run fails first at the dialog-dismissal assertion (`screen.queryByRole('dialog')).not.toBeInTheDocument()`), because the dialog is driven by `create.data` and nothing ever clears it; the cache-empty assertion below it is never reached. The cache-empty assertion's real independent counterpart was added later, at the hook boundary in `useInviteActions.test.tsx`: a case that omits `reset()` entirely and asserts the settled mutation stays in the cache. See the 2026-08-13 Phase 4 code review for the finding and the fix.

Adapted from `enrollments/enrollmentTokenSecrecy.test.tsx`. **Drop the seven matcher self-tests** at `:84-155` - they are shipped in the enrollments suite and duplicating them buys nothing. Keep the real-flow tests, the URL instrument control and the detached-layer test.

**Files:**
- Create: `web/src/admin/invites/inviteTokenSecrecy.test.tsx`
- Mutate-and-revert only (no permanent change): `web/src/admin/invites/useInviteActions.ts`, `web/src/admin/invites/InvitesTab.tsx`

- [ ] **Step 1: Write the test**

Create `web/src/admin/invites/inviteTokenSecrecy.test.tsx`:

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
  spyOnConsole,
  storageContainsSecret,
} from '../../test/secretLeaks'
import { InvitesTab } from './InvitesTab'

// The matcher self-tests (a bare string, an Error, an Error cause, an Error nested
// in an object and in an array, a storage write, an input value property) are
// SHIPPED in web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx:84-155
// against the same web/src/test/secretLeaks.ts module. They are not duplicated
// here; the per-instrument positive controls below are taken inline instead.

// A distinctive 64-hex-char stand-in for the real credential.
const TOKEN = 'f00dcafe'.repeat(8)
// The SECOND asset: the invitee address, passed as the mutation VARIABLE and
// therefore also captured in the mutationFn closure. Deliberately absent from
// every list fixture below so it can be traced independently of the token.
const INVITEE = 'invitee-secret@studio.dev'

const ROW = {
  id: 'i1',
  created_at: '2026-08-01T09:00:00Z',
  expires_at: '2026-08-10T09:00:00Z',
  created_by: 'u1',
  created_by_email: 'admin@studio.dev',
  email: 'listed@studio.dev',
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
      <InvitesTab />
    </QueryClientProvider>,
  )
}

function queryCacheContains(client: QueryClient, secret: string): boolean {
  return client
    .getQueryCache()
    .getAll()
    .some((q) => JSON.stringify({ key: q.queryKey, data: q.state.data }).includes(secret))
}

async function createOne(client: QueryClient) {
  server.use(
    http.get('/v1/invites', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
    http.post('/v1/invites', () =>
      HttpResponse.json(
        { id: 'i9', token: TOKEN, expires_at: ROW.expires_at, email: INVITEE },
        { status: 201 },
      ),
    ),
  )
  renderTab(client)
  await screen.findByText('listed@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: '+ Create invite' }))
  await userEvent.type(screen.getByLabelText('Email'), INVITEE)
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))
  await screen.findByRole('dialog')
}

test('the token is revealed once, then leaves the DOM, the caches, storage, URLs and the console', async () => {
  const spies = spyOnConsole()
  const writeText = vi.fn().mockResolvedValue(undefined)
  installClipboard(writeText)
  const client = newClient()
  await createOne(client)

  // POSITIVE CONTROLS, taken WHILE THE DIALOG IS OPEN - i.e. while the mutation
  // observer is still attached. Mutation.optionalRemove only removes an entry when
  // observers.length === 0 (query-core mutation.js:47-55), so with gcTime: 0 the
  // entry survives exactly until create.reset() detaches the observer. Taken after
  // Done instead, this control would measure an already-evicted cache and report
  // clean forever.
  expect(domContainsSecret(TOKEN)).toBe(true)
  const held = client.getMutationCache().getAll()
  expect(held).toHaveLength(1)
  expect(JSON.stringify(held[0].state)).toContain(TOKEN)
  expect(JSON.stringify(held[0].state)).toContain(INVITEE)

  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(writeText).toHaveBeenCalledWith(TOKEN)

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  // 1. Gone from the DOM, including every input value.
  expect(domContainsSecret(TOKEN)).toBe(false)
  // 2. THE MUTATION CACHE IS EMPTY. Not "no entry's state stringifies to contain
  //    the token" - that weaker form is what the 2026-08-12 profile slice shipped,
  //    and it was blind to the mutationFn CLOSURE, which mutationObserver builds
  //    from this.options and does not replace on post-success re-renders
  //    (docs/retros/2026-08-12-profile-pages.md:171-184). This mutation passes
  //    variables, so that closure really does hold INVITEE. Emptiness is the only
  //    assertion that covers both assets. DO NOT weaken it.
  //    It requires BOTH create.reset() on the dialog's onDone (which detaches the
  //    observer) and gcTime: 0 on the mutation (which lets the now-observer-less
  //    Mutation fall out on the next tick). Removing either turns this RED.
  await waitFor(() => expect(client.getMutationCache().getAll()).toHaveLength(0))
  // 3. Neither asset ever entered the query cache - they are mutation results, and
  //    no query fetches them.
  expect(queryCacheContains(client, TOKEN)).toBe(false)
  expect(queryCacheContains(client, INVITEE)).toBe(false)
  // 4. Neither entered web storage. No query persister is configured
  //    (web/src/lib/queryClient.ts), so nothing reaches IndexedDB either.
  expect(storageContainsSecret(TOKEN)).toBe(false)
  expect(storageContainsSecret(INVITEE)).toBe(false)
  // 5. Neither entered a request URL - no path segment and no query param, so
  //    neither can leak into history, a Referer header, or a proxy log. The
  //    invitee address travels in the POST BODY only.
  expect(requestUrls.length).toBeGreaterThan(0) // the instrument recorded something
  for (const url of requestUrls) {
    expect(url).not.toContain(TOKEN)
    expect(url).not.toContain(encodeURIComponent(INVITEE))
    expect(url).not.toContain(INVITEE)
  }
  // 6. No console method ever received either, in any representation.
  assertNoConsoleLeak(spies, TOKEN)
  assertNoConsoleLeak(spies, INVITEE)

  spies.forEach((s) => s.mockRestore())
})

test('the URL instrument would catch a secret in a query param (positive control)', async () => {
  server.use(
    http.get('/v1/invites', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )
  // The same handler answers the probe, so MSW's fail-closed policy is satisfied.
  await fetch(`/v1/invites?probe=${TOKEN}`)
  expect(requestUrls.some((u) => u.includes(TOKEN))).toBe(true)
})

test('the reveal is reachable only through the mutation - no route or link carries the token', async () => {
  server.use(
    http.get('/v1/invites', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
  )
  renderTab(newClient())
  await screen.findByText('listed@studio.dev')
  // The list response carries no token field at all (internal/store/query/invites.sql:22-25
  // omits token_hash from every projection), so nothing on this page can link to,
  // bookmark, or re-display one.
  expect(domContainsSecret(TOKEN)).toBe(false)
  for (const a of Array.from(document.querySelectorAll('a'))) {
    expect(a.getAttribute('href') ?? '').not.toContain(TOKEN)
  }
})

test('the dialog layer leaves the DOM with the credential, retaining no detached subtree', async () => {
  const spies = spyOnConsole()
  const client = newClient()
  await createOne(client)

  // The dialog is portaled into a single shared layer under <body>
  // (web/src/components/dialog/dialogStack.ts). Hold a reference to it so the
  // DETACHED node can be inspected after teardown - a container that is removed
  // from the document but still holds the credential in a subtree is exactly the
  // leak a portal could introduce and document.body-scoped sweeps could not see.
  const layer = document.querySelector('[data-dialog-layer]') as HTMLElement
  expect(layer).not.toBeNull()
  // Positive control on THIS instrument: it can see the token when it is present.
  expect(layer.innerHTML).toContain(TOKEN)

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  expect(document.querySelector('[data-dialog-layer]')).toBeNull()
  expect(layer.innerHTML).not.toContain(TOKEN)
  expect(layer.parentNode).toBeNull()
  expect(domContainsSecret(TOKEN)).toBe(false)
  assertNoConsoleLeak(spies, TOKEN)

  spies.forEach((s) => s.mockRestore())
})
```

- [ ] **Step 2: Run the test - it passes on first write, and that is expected**

Run: `npx vitest run src/admin/invites/inviteTokenSecrecy.test.tsx`

Expected: PASS, 4 tests. **Say so honestly in the task report**: Tasks 5, 7 and 8 already implemented everything this suite asserts, so there is no missing-implementation RED to harvest here. The substitute evidence is the two mutations below, and both must be run and both outputs recorded.

- [ ] **Step 3: RED #2 - remove `create.reset()` from the dialog's `onDone`**

In `web/src/admin/invites/InvitesTab.tsx`, temporarily change the reveal dialog's handler:

```tsx
          onDone={() => {}}
```

Run: `npx vitest run src/admin/invites/inviteTokenSecrecy.test.tsx`

Expected: FAIL. The dialog never unmounts (`create.data` still holds the result), so `waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())` times out with `Unable to find an element by role "dialog"` inverted - i.e. the element is still present - and if you get past that, the mutation cache still has length 1. **Record the exact output**, then revert this line to `onDone={() => create.reset()}` and re-run to confirm PASS.

- [ ] **Step 4: RED #2b - delete `gcTime: 0` (a second, independent cause)**

In `web/src/admin/invites/useInviteActions.ts`, temporarily delete the `gcTime: 0,` line.

Run: `npx vitest run src/admin/invites/inviteTokenSecrecy.test.tsx`

Expected: FAIL on the first test only - `expected [ Mutation ] to have a length of 0 but got 1` after the `waitFor` timeout. The dialog closes (reset ran) but the detached `Mutation` sits in the cache for the default 5-minute `gcTime` with the token in `state.data`, the invitee email in `state.variables`, and the same email in the `mutationFn` closure. **Record the exact output**, restore the line, and re-run to confirm PASS.

**Report both REDs together**: they exercise the two halves of the rule independently, which is the point - `gcTime: 0` cannot evict while an observer is attached, and `reset()` alone cannot evict at all.

- [ ] **Step 5: Commit**

Confirm the working tree contains only the new test file (both mutations reverted):

```bash
git status --short
git diff -- web/src/admin/invites/useInviteActions.ts web/src/admin/invites/InvitesTab.tsx
```

Expected: the diff is empty and the only untracked file is the new test.

```bash
git add web/src/admin/invites/inviteTokenSecrecy.test.tsx
git commit -m "test(web): invite token secrecy - the mutation cache must be empty after dismissal"
```

---

## Task 10: Register the tab, and correct the two shipped test files that assert its absence

One `ADMIN_TABS` entry is all that gates the tab: `AdminTabs.tsx:12` maps the array to build the bar, and `AdminPage.tsx:20-23` resolves the `:tab` param through `findAdminTab` and redirects unknown slugs to `/admin/users`. Nothing in routing or gating changes.

**This task edits shipped test assertions, which is normally forbidden.** The exception is narrow and enumerated: those assertions encode *the absence of the very tab this slice adds*. Every one of them is listed below with its replacement. **Nothing else in either file may change** - verify with `git diff` in Step 4.

**Files:**
- Modify: `web/src/admin/tabs.ts:2-26`
- Modify: `web/src/admin/AdminTabs.test.tsx` (four assertion changes, one test deleted, two tests added)
- Modify: `web/src/admin/AdminPage.test.tsx` (one handler added, one test replaced, one test added)

- [ ] **Step 1: Write the failing tests**

**In `web/src/admin/AdminTabs.test.tsx`:**

(a) Replace the registry array at `:16-21`:

```tsx
  expect(ADMIN_TABS.map((t) => t.slug)).toEqual([
    'users',
    'invites',
    'enrollments',
    'reservations',
    'server',
  ])
```

(b) In `findAdminTab resolves a known slug and rejects everything else`, **delete** the line `expect(findAdminTab('invites')).toBeUndefined()` (`:30`) and add, after the `users` line:

```tsx
  expect(findAdminTab('invites')?.label).toBe('Invites')
```

(c) In `renders one link per registry entry`, add before the length assertion:

```tsx
  expect(screen.getByRole('link', { name: 'Invites' })).toHaveAttribute('href', '/admin/invites')
```

and change `expect(screen.getAllByRole('link')).toHaveLength(4)` to `toHaveLength(5)`.

(d) **Delete** the test `tabs that are not built yet are absent` (`:70-73`). Its premise is dead: all five hi-fi tabs now exist, and the registry-equality test in (a) already pins the exact set, so the deletion loses no coverage. Add in its place:

```tsx
test('the invites tab sits between Users and Agent enrolls, matching the hi-fi order', () => {
  renderTabs('/admin/users')
  expect(screen.getAllByRole('link').map((a) => a.textContent)).toEqual([
    'Users',
    'Invites',
    'Agent enrolls',
    'Reservations',
    'Server',
  ])
})

test('the invites tab is marked current on its own route', () => {
  renderTabs('/admin/invites')
  expect(screen.getByRole('link', { name: 'Invites' })).toHaveAttribute('aria-current', 'page')
  expect(screen.getByRole('link', { name: 'Users' })).not.toHaveAttribute('aria-current')
})
```

**In `web/src/admin/AdminPage.test.tsx`:**

(e) Add to `renderAt`'s `server.use(...)` list, after the `/v1/agent-enrollments` handler:

```tsx
    http.get('/v1/invites', () =>
      HttpResponse.json({
        items: [
          {
            id: 'i1',
            created_at: '2026-08-01T09:00:00Z',
            expires_at: '2026-08-10T09:00:00Z',
            created_by: 'u1',
            created_by_email: 'admin@studio.dev',
            email: 'ada-invite@studio.dev',
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
```

(f) **Replace** the test `a not-yet-built tab segment redirects rather than rendering an empty shell` (`:118-121`) - it renders `/admin/invites`, which now resolves. The redirect path stays covered by `an unknown tab segment redirects to the users tab` (`:111-116`, which uses `/admin/bogus`). New tests:

```tsx
test('/admin/invites renders the invites panel inside the same shell', async () => {
  renderAt('/admin/invites')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('ada-invite@studio.dev')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Invites' })).toHaveAttribute('aria-current', 'page')
})

test('/admin/users still renders its own panel, not invites', async () => {
  renderAt('/admin/users')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.queryByText('ada-invite@studio.dev')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/admin/AdminTabs.test.tsx src/admin/AdminPage.test.tsx`

Expected: FAIL, with these exact causes:
- registry equality: `expected [ 'users', 'enrollments', 'reservations', 'server' ] to deeply equal [ 'users', 'invites', ... ]`
- `findAdminTab('invites')?.label` -> `expected undefined to be 'Invites'`
- `getByRole('link', { name: 'Invites' })` -> `Unable to find an accessible element with the role "link" and name "Invites"`
- `/admin/invites` -> `Unable to find an element with the text: ada-invite@studio.dev` (the route still redirects to users)

- [ ] **Step 3: Implement**

Edit `web/src/admin/tabs.ts`. Add the import after `import { EnrollmentsTab } ...`:

```ts
import { InvitesTab } from './invites/InvitesTab'
```

Replace the comment block and array at `:13-26` with:

```ts
// The admin console is a registry plus a switch. Tabs that are not built yet are
// ABSENT on purpose: an unknown /admin/:tab segment redirects to /admin/users
// instead of rendering an empty panel, so this file cannot ship dead tabs. Adding
// a tab is one entry here - nothing in routing or gating changes.
// Order matches the hi-fi's tab order: Invites sits between Users and Agent
// enrolls (design_handoff_relay_holo/hifi3-holo-pages.jsx:2083).
export const ADMIN_TABS: AdminTab[] = [
  { slug: 'users', label: 'Users', Panel: UsersTab },
  { slug: 'invites', label: 'Invites', Panel: InvitesTab },
  { slug: 'enrollments', label: 'Agent enrolls', Panel: EnrollmentsTab },
  { slug: 'reservations', label: 'Reservations', Panel: ReservationsTab },
  { slug: 'server', label: 'Server', Panel: ServerTab },
]
```

Note what changed in the comment: the old text claimed the Invites tab "stays blocked on a GET /v1/invites that does not exist". **It exists** (`internal/api/server.go:143`, handler at `internal/api/invites.go:148`). A wrong contract in a comment is a defect, not a stale note - it would tell the next reader the endpoint is missing.

- [ ] **Step 4: Run the tests to verify they pass, and verify nothing else moved**

Run: `npx vitest run src/admin/`

Expected: PASS across the whole admin directory.

```bash
git diff -- web/src/admin/AdminTabs.test.tsx web/src/admin/AdminPage.test.tsx
```

Read the diff line by line. It must contain **only** the changes enumerated in Step 1 - items (a) through (f) and nothing else. If any other assertion in either file needed adjusting, that adjustment IS a finding: stop and report it rather than absorbing it.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/tabs.ts web/src/admin/AdminTabs.test.tsx web/src/admin/AdminPage.test.tsx
git commit -m "feat(web): register the admin Invites tab"
```

---

## Task 11: The full verification gate

**Files:** none changed. If a step here needs a code change, that change is a new finding - report it, fix it in the owning file, and re-run from the top of this task.

- [ ] **Step 1: The whole web suite**

From `web/`:

```bash
npm test 2>&1 | tail -20
```

Expected: green, with the total up from your Task 0 baseline by the number of tests this slice added (7 files under `admin/invites/` plus 1 in `Chip.test.tsx` plus the net change in `AdminTabs.test.tsx` and `AdminPage.test.tsx`). Compare against the number you recorded in Task 0 - **not against any number quoted in this document or the brief**, both of which are unverified.

- [ ] **Step 2: The type and build gate**

```bash
npm run build
```

This is `tsc -b && vite build`. Expected: success. It is the only gate that type-checks - vitest transpiles without checking - so this is where a wrong `Chip` tone key, a `used_at !== null` comparison, or a mismatched prop type surfaces.

- [ ] **Step 3: Revert the build artifacts**

From the repo root:

```bash
git checkout -- web/dist/
git status --short
```

`web/dist` is tracked but stale from the scaffold, so a production build dirties it. Expected after the checkout: no `web/dist` entries in `git status`.

- [ ] **Step 4: The Go gate**

From the repo root (`make` is not on PATH):

```bash
go build ./... && go test ./... 2>&1 | tail -20
```

Expected: green, and **identical to your Task 0 measurement** - this slice touches no Go file. If it moved, something outside the plan changed; find out what before proceeding.

- [ ] **Step 5: Confirm the exact file set**

```bash
git status --short
git diff --stat b971a0b..HEAD
```

Expected file set for the whole slice, and nothing else:

```
web/src/admin/invites/api.ts
web/src/admin/invites/api.test.ts
web/src/admin/invites/inviteStatus.ts
web/src/admin/invites/inviteStatus.test.ts
web/src/admin/invites/useInvites.ts
web/src/admin/invites/useInvites.test.tsx
web/src/admin/invites/useInviteActions.ts
web/src/admin/invites/useInviteActions.test.tsx
web/src/admin/invites/CreateInviteForm.tsx
web/src/admin/invites/CreateInviteForm.test.tsx
web/src/admin/invites/InvitesTable.tsx
web/src/admin/invites/InvitesTable.test.tsx
web/src/admin/invites/InvitesTab.tsx
web/src/admin/invites/InvitesTab.test.tsx
web/src/admin/invites/inviteTokenSecrecy.test.tsx
web/src/components/holo/Chip.tsx
web/src/components/holo/Chip.test.tsx
web/src/admin/tabs.ts
web/src/admin/AdminTabs.test.tsx
web/src/admin/AdminPage.test.tsx
```

**No Go file. No `.sql` file. No `web/dist`. No file under `docs/`** - the backlog close and the proposed `idea-2026-08-13-cursor-pager-hook.md` are Phase 6's work, not the engineer's.

- [ ] **Step 6: Report**

Report to the conductor: the Task 0 baseline and the final suite totals; the two recorded REDs from Task 9 (with their exact failure text) plus the RED #1 output from Task 5 Step 2 and the ordering-guard RED from Step 4b; and confirmation that the `git diff` on the two shipped admin test files contains only the enumerated changes.

---

## Acceptance criteria mapped to tasks

| Spec criterion | Task |
|---|---|
| 1. `/admin/invites` reachable, in the bar between Users and Agent enrolls, one registry entry, stale comment corrected | 10 |
| 2. Create with optional email and four presets; body always sent; `expires_in` always `/^\d+h$/` and `<= 720h`; blank email omitted, not `""` | 2, 7 |
| 3. Raw token displayed exactly once in `TokenRevealDialog` with copy plus clipboard fallback, shared dialog unmodified | 8, 9 |
| 4. After dismissal: DOM, **mutation cache empty**, query cache, both storages, every request URL and every console method clean; cache-emptiness proven RED at the hook boundary (Task 5) and, independently, by a later `useInviteActions.test.tsx` case that omits `reset()` - Task 9's own "remove `create.reset()`" RED was found (2026-08-13 Phase 4 review) to land on the dialog-dismissal assertion instead, never reaching the cache-empty line | 5 (RED #1), later hook-boundary addition (RED #2) |
| 5. Every state listed with cursor pagination, both sortable headers in both directions, footer range and total from the endpoint's own `total` | 6, 8 |
| 6. Pill derives REDEEMED -> EXPIRED -> EXPIRING -> ACTIVE, 1h window, redeemed-and-expired reads REDEEMED | 3 |
| 7. `CREATED BY` renders `created_by_email`; the raw UUID never rendered; no token-prefix column | 6 |
| 8. No revoke, delete or resend control, asserted negatively **with a positive control**; footnote states the terminal states | 6, 8 |
| 9. No `refetchInterval` anywhere; pill freshness from `useNow`, row-set freshness from bare-prefix invalidation | 4, 5, 8 |
| 10. `Chip` gains exactly one tone key, no consumer changes | 1 |
| 11. Web suite green, `tsc -b && vite build` succeeds, `git checkout -- web/dist/` | 11 |
| 12. No file outside the Architecture table modified; no Go file touched | 11 |
| 13. Backlog items proposed, not auto-filed; the invites item closed via `/backlog close` with its stale title corrected | Phase 6 (**not the engineer**) |

---

## Deviations from the spec, and why

1. **The spec says the edits to `AdminTabs.test.tsx` and `AdminPage.test.tsx` are "additive edits only" (Architecture table). That is false, and I verified it.** `AdminTabs.test.tsx:16-21` asserts the registry is exactly four slugs, `:30` asserts `findAdminTab('invites')` is `undefined`, `:47` asserts four links, `:70-73` asserts the Invites label is absent, and `AdminPage.test.tsx:118-121` asserts `/admin/invites` **redirects**. All five assert the absence of the tab this slice adds and must change. Task 10 enumerates each one with its replacement and requires a line-by-line `git diff` review, so the exception stays narrow rather than becoming a licence to rewrite the files.
2. **`deriveStatus` takes the row shape, not a bare `expires_at` string,** unlike `enrollmentStatus.deriveStatus(expiresAt, now)`. It needs two fields. The spec writes the signature as `deriveStatus(invite: {expires_at: string; used_at?: string}, now: Date)`, which this plan names as an exported `InviteStatusInput` so the table and the tests share one type.
3. **The reveal dialog's open/closed state is sourced from `create.data`, i.e. from the mutation's own state machine,** rather than from a separate flag as the brief suggested. A separate boolean would not protect anything: the token itself must be read from `create.data` (one retention site, one destruction point - spec requirement 2), so a `reset()` in `onSuccess` would blank the token regardless of what gated the render. The real protection is structural - `reset()` is forbidden in `onSuccess` and lives only at the three UI-driven sites - plus the guard test in Task 5 that goes RED (proven by mutate-and-revert in Step 4b) if anyone moves it.
4. **The `INVITEE` email is carried through the secrecy suite as a named second asset** rather than being left implicit. The spec identifies it as the reason the assertion must be cache-emptiness; making it a distinct, fixture-absent constant lets the suite assert it independently of the token, so a regression that leaks only the email still fails.
5. **`docs/backlog/idea-2026-08-13-cursor-pager-hook.md` is not created by the engineer.** The spec says items are proposed rather than auto-filed; this plan removes the ambiguity by putting it in Phase 6 and by recording the seventh-consumer debt in this document instead, so the decision survives even if the item is never accepted.
