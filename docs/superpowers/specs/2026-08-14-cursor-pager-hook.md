# Extract the cursor-pager block into one hook - Design

Date: 2026-08-14
Status: Draft (autonomous gate mode; conductor review)

Backlog item: `docs/backlog/idea-2026-08-13-cursor-pager-hook.md`.
Frontend-only. No backend change, no endpoint change, no new dependency, no new
runtime behaviour of any kind.

Written in autonomous gate mode: every question the item left open was decided
here and carries a one-line rationale in Decisions rather than being asked. What
would have been escalated is listed in "Escalations" at the end.

---

## 0. The gate, stated before anything else

**The gate is a zero-line diff to every existing test file that mounts one of the
seven migrated surfaces.** Not "the seven named in the item" - the definitive set
is twelve files and is enumerated in section 6.

An assertion that needs adjusting during a refactor that is supposed to change
nothing **is the finding**, not an obstacle to the finding. Either the refactor
changed behaviour, or the test was green because of a defect the refactor
removed. Both outcomes stop the work and get investigated. There is no third
branch in which a selector or a text match gets "fixed up" and the migration
continues.

The temptation arrives around file four, when eleven of twelve are clean and one
`getByRole` needs one extra `await`. That is exactly the moment this section
exists for. `git diff --numstat -- <the twelve files>` must print `0<TAB>0` for
every one of them, and that command is the acceptance evidence, not a reviewer's
impression.

This gate is the substance of the work. The extraction itself is an afternoon.

---

## 1. Verified current state at HEAD (ee88de0)

Every file below was opened in full at HEAD in the worktree, not inferred from
the item. The item's line numbers date from 2026-08-13 and several of these files
were touched afterwards (`JobsPage` by the retry slice #131, the tables by the
minWidth slice #129).

### 1.1 The seven copies exist. The count is right.

Seven live pager blocks, no eighth, no ninth. Verified by four independent greps
over `web/src` (`next_cursor`, `computePageRange`, `startOffset`,
`cursorStack|setStack|setOffsets`) plus a sweep of every `use*.ts` query hook for
a `cursor` parameter. Every list-endpoint query hook in the tree
(`useJobs`, `useRevokedWorkers`, `useSchedules`, `useAdminUsers`,
`useAgentEnrollments`, `useReservations`, `useInvites`) has exactly one consumer,
and that consumer is one of the seven.

`useScheduleRuns.ts:4` is the near-miss and it is explicitly **not** a copy: its
own comment says "A fixed latest-N window on a detail page, NOT a list page: no
cursor stack". Confirmed - `ScheduleRunsPanel.tsx` contains no occurrence of
`cursor`.

| # | File | Identifiers | State pieces | Named `resetPaging`? | Local `toggleSort` |
|---|---|---|---|---|---|
| 1 | `web/src/jobs/JobsPage.tsx:28-34, 62-83, 108` | `cursor`/`stack`/`startOffset`/`offsets`, `next`/`prev` | **4** | **no** - inlined twice | no |
| 2 | `web/src/workers/WorkersPage.tsx:41-44, 55-74, 105` | `revoked`-prefixed, `revokedNext`/`revokedPrev` | **4** | **no** - none exists | yes (`:22`) |
| 3 | `web/src/schedules/SchedulesPage.tsx:30-35, 60-77, 122` | `cursorStack`/`startOffset`/`offsets`, `goNext`/`goPrev` | **3** | **no** - inlined once | no |
| 4 | `web/src/admin/users/UsersTab.tsx:37-40, 63-68, 90-109, 132` | canonical | 4 | yes (`:63`) | yes (`:17`) |
| 5 | `web/src/admin/enrollments/EnrollmentsTab.tsx:28-31, 41-46, 55-74, 84` | canonical | 4 | yes (`:41`) | yes (`:16`) |
| 6 | `web/src/admin/reservations/ReservationsTab.tsx:53-56, 71-76, 85-104, 118` | canonical | 4 | yes (`:71`) | yes (`:21`) |
| 7 | `web/src/admin/invites/InvitesTab.tsx:34-37, 47-52, 61-80, 93` | canonical | 4 | yes (`:47`) | yes (`:22`) |

`toggleSort` is in **five** of seven, as the item says. Verified individually.

### 1.2 The single most important finding: only four of seven are verbatim

**The item's central premise is that all seven are character-for-character
faithful. At HEAD that is true of four of them, not seven.** Copies 4-7 (the
admin tabs) are genuinely byte-identical modulo the sort type name. Copies 1-3
each differ, and copy 3 differs *structurally*.

This does not sink the extraction - all three drifts are provably
behaviour-equivalent, and section 4 works each one through. But it does mean the
plan cannot treat this as find-and-replace across seven files. It is a
find-and-replace across four files and three individually-reasoned migrations.

**Drift A - `JobsPage` and `WorkersPage` have no `resetPaging`.** The item's
Summary sketch presents `resetPaging()` as part of the shared block. `JobsPage`
inlines the same four setters twice (`pickFilter:43-46`, `pickSort:52-55`) and
never names them; `WorkersPage` has no reset at all, because the revoked-workers
list carries no sort control and nothing invalidates its cursor. So the hook's
`resetPaging` has **six** call sites across five surfaces, not seven across seven,
and `WorkersPage` will call it zero times. A plan that expects to delete a
`resetPaging` function from all seven files will find it in four.

**Drift B - `SchedulesPage` is a different algorithm, not a renamed one.** This
is the finding the item most needs corrected. It says: "It calls the stack
`cursorStack` and the functions `goNext`/`goPrev`. **The bodies are the same;
only the identifiers differ.**" The bodies are **not** the same.

| | canonical (6 copies) | `SchedulesPage` |
|---|---|---|
| cursor | its own `useState('')` | **derived**: `cursorStack[cursorStack.length - 1]` (`:31`) |
| what `next` pushes | the **current** cursor (`:65` in JobsPage) | the **next** cursor (`:63`) |
| cursor at first page | `''` (empty string) | `undefined` |
| what `prev` pops | pops and **assigns** the popped value to `cursor` | pops and **discards** it |
| state pieces | 4 | 3 |

Both maintain `stack.length === pageIndex` and `offsets.length === pageIndex`, so
externally they agree - but a mechanical rename would have produced wrong code,
and a reviewer told "only identifiers differ" would have waved it through.

The one observable difference is the first-page cursor. `SchedulesPage` passes
`undefined`, the canonical shape passes `''`. **That difference is invisible to
the wire and to TanStack, for two specific reasons that must be preserved:**

- `web/src/schedules/api.ts:41` guards with `if (cursor) q.set('cursor', cursor)`,
  and both `''` and `undefined` are falsy, so the `cursor` query param is absent
  either way.
- `web/src/schedules/useSchedules.ts:10` normalizes with
  `queryKey: ['schedules', sort, cursor ?? '']`, so the cache key is `''` either
  way.

This is load-bearing for the gate. `SchedulesPage.test.tsx:113` asserts
`cursors.filter((c) => c === null).length >= 2` - it reads the raw query param and
requires it to be **absent** on the first page and after `prev`. That test stays
green after migration *only because of those two normalizations*. If either is
ever tightened (`cursor !== undefined`, or a key without `?? ''`), the migration
becomes a behaviour change. The plan must verify both lines are still as
described before it starts, and must not touch either.

**Drift C - two surfaces render `prev` twice, one hides it entirely.**
`EnrollmentsTab.tsx:118-128` and `ReservationsTab.tsx:150-160` render a **second,
undisabled** `prev` button inside their empty-state branch (the documented escape
hatch for a non-first page emptied by a concurrent delete or an expiry).
`UsersTab.tsx:191` wraps the whole footer control pair in `{!filtering && (...)}`.
`InvitesTab.tsx:118-123` carries a comment explaining why it deliberately has
*no* hatch. So `stack.length` is read at nine render sites, not seven, and the
hook must expose that predicate rather than assuming one footer per surface.

### 1.3 One wrong-prose defect found at HEAD, in the comment this slice must edit

`web/src/admin/invites/InvitesTab.tsx:16-21` is the only extraction-debt comment
in `web/src` that names this work. It says:

> SEVENTH consumer of the cursor-pager block below (...), and **FOURTH of this
> helper.**

`toggleSort` copies preceding `InvitesTab` are `WorkersPage.tsx:22`,
`UsersTab.tsx:17`, `EnrollmentsTab.tsx:16`, `ReservationsTab.tsx:21` - four of
them, making `InvitesTab` the **fifth**, not the fourth. The item caught this
off-by-one in the invites *spec and plan* ("that was true when they were written
and stopped being true when the slice they described landed") but did not notice
the shipped source comment carries the same error. Ninth consecutive iteration in
which wrong prose about correct code is the defect.

Correcting `FOURTH` to `FIFTH` is in scope for this slice and is **not** scope
creep, because the comment is being edited anyway (its pager half must go, its
`toggleSort` half must stay - see Decision 3). Fixing half of a sentence and
leaving a known-false number in the other half would be worse than not touching
it.

### 1.4 Other extraction-debt comments in `web/src` - what to touch, what not to

The item's acceptance criterion says "the extraction-debt comments naming this
item are removed from the files that carry them", plural. There is exactly **one**
that names this item's subject, and it must be **edited, not deleted**:

| Location | Names | Action |
|---|---|---|
| `InvitesTab.tsx:16-21` | pager (7th) + `toggleSort` (4th, wrong) | **Edit.** Drop the pager sentences; keep and correct the `toggleSort` accounting. |
| `inviteStatus.ts:5-9`, `:76-77` | `EXPIRING_WINDOW_MS` / `formatExpiryLabel` -> `web/src/lib/expiry.ts` | **Do not touch.** Out of scope, see Decision 5. |
| `ScheduleDetailPage.tsx:30-34` | `idea-2026-08-12-detail-page-state-triad-primitive` | **Do not touch.** Different item, different refactor. |
| `profile/tabs.ts:13-15` | a tab primitive | **Do not touch.** Unrelated. |

Also unmentioned by the item and worth knowing: `web/src/jobs/pageRange.ts` is a
two-line re-export shim over `web/src/lib/pageRange.ts`, with its own test file
`web/src/jobs/pageRange.test.ts`. `computePageRange` itself is not changing, the
shim is not being removed, and neither file is in the change set. Recorded so
nobody "tidies" it mid-migration.

### 1.5 The `statusTone` claims hold exactly as written

Re-verified at HEAD, both line ranges accurate:

- `web/src/admin/invites/inviteStatus.ts:59-64` - `statusTone` returns
  `'accent' | 'warn' | 'muted' | 'err'`, and `:61` maps **`EXPIRED` -> `err`**.
- `web/src/admin/enrollments/enrollmentStatus.ts:29-33` - `statusTone` returns
  `'accent' | 'warn' | 'muted'` (no `err` in the union at all), and `:30` maps
  **`EXPIRED` -> `muted`**.

The difference is deliberate and documented on both sides: invites has four
states and `Chip`'s `err` tone was added for it (`inviteStatus.ts:56-58`);
enrollments has three states and no `REDEEMED`, so `muted` is free for `EXPIRED`.
`web/src/admin/reservations/reservationStatus.ts:56` is a third, differently-shaped
`statusTone` with yet another state vocabulary.

`formatExpiryLabel` is byte-identical between `enrollmentStatus.ts:45-48` and
`inviteStatus.ts:78-81`, with its reasoning comment duplicated in full. The item
is accurate about this too.

### 1.6 `idea-2026-08-12-detail-page-state-triad-primitive` is not in flight

Read in full. `status: open`, `priority: low`, created 2026-08-12, unmodified.
Its three source files (`WorkerDetailPage.tsx`, `JobDetailPage.tsx`,
`ScheduleDetailPage.tsx`) still carry the triad and `ScheduleDetailPage.tsx:30-34`
still carries the deviation comment naming the item, which is precisely the
comment that item's own acceptance criteria require to be removed. No spec, no
plan, no branch, no ROADMAP entry (`ROADMAP.md` is at the repo root, not
`docs/`, and does not list it as in-progress). **Confirmed untouched and not
running.** These two must not run concurrently: they are both behaviour-preserving
refactors gated on a zero test diff, and `ScheduleDetailPage.test.tsx` /
`SchedulesPage.test.tsx` sit close enough that a red run would be hard to
attribute.

---

## 2. The hook

`web/src/lib/useCursorPager.ts`. Direct tests in `web/src/lib/useCursorPager.test.ts`.

`web/src/lib/` already houses `useNow.ts` and `useDebouncedValue.ts` - two
behaviour-only, render-free hooks with `.test.ts` siblings - so this is the
established home for exactly this kind of thing. `web/src/components/` is for
things that render, and the pager renders nothing. Not a feature module, per the
item.

### API

```ts
export interface CursorPager {
  cursor: string          // '' on the first page
  startOffset: number     // rows accumulated before this page
  canPrev: boolean        // stack.length > 0
  next: (nextCursor: string | undefined, pageSize: number) => void
  prev: () => void
  resetPaging: () => void
}

export function useCursorPager(): CursorPager
```

- **`next(nextCursor, pageSize)` takes explicit arguments and knows nothing about
  TanStack.** It returns early when `nextCursor` is falsy, which is where the
  current `if (!data?.next_cursor) return` guard moves to. Accepting
  `string | undefined` rather than `string` is deliberate: every call site passes
  `data?.next_cursor` off a possibly-undefined query result, and forcing a `?? ''`
  at seven call sites is noise that also invites someone to write `!` instead.
- **`prev()` takes nothing** and returns early when the stack is empty, exactly as
  the seven copies do.
- **`resetPaging()`** clears all of `cursor`, `stack`, `startOffset`, `offsets`.
- **`canPrev`** exists so no consumer reads a stack the hook owns. It replaces
  nine occurrences of `stack.length === 0` / `stack.length > 0` (drift C). The
  hook does **not** return the stack itself - see Invariants below.
- **There is no `canNext`.** Every copy computes it as `!data?.next_cursor`,
  which is a fact about the query, not about the pager, and moving it in would
  require the hook to know about the query result. Left alone.

### Internal shape: identical to today, deliberately

Four `useState` calls (`cursor`, `stack`, `startOffset`, `offsets`) with plain
setters that read current-render values - **not** functional updaters, and **not**
one merged state object.

This is not an aesthetic choice. Three of the seven copies carry a comment
explaining it (`JobsPage.tsx:58-61`, `SchedulesPage.tsx:56-59`,
`UsersTab.tsx:87-89`), and `SchedulesPage`'s is the sharpest: "Mixing a
functional `setCursorStack` updater with plain offset setters would desync the
stacks under StrictMode." A single merged `useState({cursor, stack, startOffset,
offsets})` with one functional updater is the tidier design and is **rejected
here**, because it changes the update mechanics of seven shipped surfaces in the
one change whose entire premise is that nothing changes. That reasoning is
recorded so it does not have to be re-derived; it is not an invitation to do it
in a follow-up either, since the current shape has three comments defending it
and no known defect.

The three warning comments do not vanish - the reasoning moves into the hook,
where it now defends one implementation instead of three copies.

### Invariants this respects

- **No interior pointers across locks**, in its frontend form: the hook returns
  `canPrev`, a boolean, not `stack`. A consumer that received the array could
  mutate it (`stack.pop()` on a React state array is a live footgun) and desync
  `offsets` from `cursor` behind the hook's back. Value out, mutation only through
  the hook's own methods.
- **End the generation before releasing the resource** does not apply: there is no
  async continuation, no listener, no abort, no timer. The hook holds state and
  three pure transitions.
- No backend invariant is in contact with this change. No Go, no SQL, no proto,
  no migration.

---

## 3. Decisions

The item named four points to settle plus two scope questions. All six are
decided here.

**Decision 1 - `next` takes explicit `(nextCursor, pageSize)` arguments and stays
TanStack-ignorant.** (Item's recommendation; taken.) The alternative - passing the
query result - would make the hook's type depend on seven different response
shapes or on a structural type it does not own, and would drag `isPlaceholderData`
and `next_cursor` semantics into a primitive whose job is arithmetic on two
stacks. **Traded away:** each call site keeps a two-argument call
(`pager.next(data?.next_cursor, data.items.length)`) instead of `pager.next(data)`,
so the "read the page size off the current page" step stays visible at seven sites
rather than being centralized. That is the same visibility the current code has,
which is what the gate wants.

**Decision 2 - `resetPaging` is called explicitly by the consumer; the hook does
not watch a `sort` key.** (Item's recommendation; taken.) A `sort`-watching hook
would need an effect or a render-phase comparison, and would fire on **six**
different trigger conditions across the surfaces - `JobsPage` resets on *filter*
as well as sort (`pickFilter:43-46`), `UsersTab` resets on sort, on
`include_archived` and on the debounced email (`:70-85`). A single `sort`
dependency does not model that, and modelling it properly means passing a
composite key, which is a design exercise. **Traded away:** the reason the reset
exists (the server 400s a cursor issued under another sort,
`internal/api/pagination.go:272-286`) stays as a comment at each call site rather
than being structurally enforced, so a future surface can still forget to call it.
That risk exists today and is not made worse.

**Decision 3 - `toggleSort` does NOT come along. Separate change, and not one
this slice files.** (Item's recommendation; taken.) It is a pure function, which
makes it the easy half, but its five copies are typed over five per-module unions
(`WorkerSort`, `UserSort`, `EnrollmentSort`, `ReservationSort`, `InviteSort`) and
a shared version needs a generic plus a cast at every call site. Doing it here
puts a type-level design question inside the change whose gate is that nothing
changes. **Traded away:** five copies of a five-line pure function survive this
slice, and the `InvitesTab` comment keeps accounting for them (corrected to
FIFTH). A follow-up proposal is listed in section 11; it is *proposed*, not filed.

**Decision 4 - `SchedulesPage` migrates in this change, onto the canonical shape,
and its variant does not survive anywhere.** (Item is unambiguous; taken - but for
a stronger reason than the item gives.) The item treats this as a naming problem.
It is an algorithm difference (drift B). A partial migration would leave behind
not an eighth *copy* but an eighth *variant* implemented differently from the
hook, which is strictly worse than seven copies because the next reader would
have to diff two algorithms to learn there is only one behaviour. **Traded away:**
`SchedulesPage`'s first-page cursor changes from `undefined` to `''` at the call
into `useSchedules`. Section 1.2 establishes that this is invisible at the wire
and at the query key, and names the two lines that make it so. This is the single
highest-risk edit in the slice and it gets its own task with its own RED/GREEN
evidence.

**Decision 5 - the `formatExpiryLabel` / `EXPIRING_WINDOW_MS` half is NOT in this
change, and does not need its own item.** (Item recommends two changes; taken, and
extended.) The item is right that bundling would stretch the zero-diff gate across
nine test files and let a failure in either half block both. But the stronger
reason is that the pair has **two** consumers, and this repo's rule is extract
before the **third** - so the extract-before-the-third trigger has not fired, and
doing it now would be extracting on aesthetics while the seven-consumer case sits
undone. It stays exactly as it is, in this item, under the heading it already has.
It does **not** get split into a separate backlog item: `inviteStatus.ts:5-9` and
`:76-77` already name the destination (`web/src/lib/expiry.ts`) and the trigger
(a third status module) in the source itself, which is a better carrier than a
second file. **Traded away:** if this item is closed on the pager alone, the
expiry half loses its backlog home. Mitigated by acceptance criterion 12, which
requires the item's Resolution to state that the expiry half was deliberately not
done and that its trigger lives in `inviteStatus.ts`.

**Decision 6 - the hook lives at `web/src/lib/useCursorPager.ts` and is named
`useCursorPager`.** `lib/` over `components/` because it renders nothing and
because `useNow` and `useDebouncedValue` set that precedent; the item's working
name is kept because it is accurate and already cited in the source comment being
edited. Test file `web/src/lib/useCursorPager.test.ts` (`.ts`, matching
`useNow.test.ts` and `useDebouncedValue.test.ts` - `renderHook` needs no JSX for a
hook with no provider).

**Decision 7 (not in the item) - the `InvitesTab` extraction-debt comment is
edited, not deleted, and its `FOURTH` is corrected to `FIFTH`.** See 1.3. The
comment's pager half becomes false the moment the hook lands; its `toggleSort`
half stays true and stays useful, because Decision 3 leaves that debt open.

---

## 4. Per-surface migration notes

Four of the seven are mechanical. Three are not. Each of the three gets its own
task and its own before/after reasoning; none of them is a rename.

**Group A - mechanical (4 files):** `UsersTab`, `EnrollmentsTab`,
`ReservationsTab`, `InvitesTab`. Delete the four `useState` lines, the
`resetPaging` body, and the `next`/`prev` bodies; add
`const pager = useCursorPager()`; rewrite the call sites. `pickSort` keeps its
own body and calls `pager.resetPaging()`. Footer reads become `pager.startOffset`,
`!pager.canPrev || isPlaceholderData`, `pager.prev`,
`() => pager.next(data?.next_cursor, data.items.length)`. The empty-state hatches
in `EnrollmentsTab` and `ReservationsTab` become `{pager.canPrev && ...}`. The
`UsersTab` `{!filtering && ...}` wrapper is untouched.

**Group B - `JobsPage`.** No named `resetPaging` exists; `pickFilter:43-46` and
`pickSort:52-55` each inline the four setters. Both become
`pager.resetPaging()`. `pickFilter`'s trailing
`if (key !== 'all') setSort(DEFAULT_SORT)` and `pickSort`'s `setSort(s)` stay
where they are and keep their order relative to the reset. The three-line comment
at `:25-27` and the four-line comment at `:58-61` move into the hook rather than
being deleted; the two-line comment at `:30-32` describing `startOffset` moves
with them.

**Group C - `WorkersPage`.** Prefixed identifiers and a pager that is *conditionally
rendered* (only the `section === 'decommissioned'` branch, `:102-154`) but
*unconditionally hooked* (state at `:41-44`, before any early return). The hook
call must stay above every early return - it already would, since `useWorkers`
and `useWorkerStats` are called at `:46-47`. `const revokedPager =
useCursorPager()` and `revoked.data` supplies the arguments. `resetPaging` is
never called here and that is correct: the revoked list has no sort control and no
filter. The `// Revoked-workers pagination state (mirrors JobsPage cursor-stack
pattern)` comment at `:40` becomes false and is replaced by nothing - the hook
name says it.

**Group D - `SchedulesPage`.** The real one. `cursorStack` (3 states, derived
cursor, pushes the *next* cursor) becomes the canonical 4-state shape inside the
hook. `cursor` at `:31` is deleted; `useSchedules(sort, pager.cursor)` at `:38`
now receives `''` rather than `undefined` on the first page. `chooseSort:49-54`
loses its three inline setters and its `// restart paging when the sort changes`
comment moves to `pager.resetPaging()`. `goNext`/`goPrev` are deleted and the two
footer buttons at `:185` and `:193` call `pager.prev` / `() =>
pager.next(data?.next_cursor, data.items.length)`. The `// Cursor stack: [] is the
first page` comment at `:29` and the `:56-59` StrictMode comment both go, the
latter having moved into the hook.

**The one thing that must be verified before this task starts, not after:** that
`api.ts:41` still reads `if (cursor) q.set('cursor', cursor)` and
`useSchedules.ts:10` still reads `['schedules', sort, cursor ?? '']`. If either
has changed, `SchedulesPage.test.tsx:113` will go red and the correct response is
to stop, because the migration would then be a behaviour change rather than a
refactor.

---

## 5. What is explicitly out of scope

Non-negotiable. Each of these is a thing a well-meaning reviewer or engineer will
suggest folding in "while we're in there", and each would defeat the gate.

1. **`statusTone` and the three status modules.** `inviteStatus.ts`,
   `enrollmentStatus.ts`, `reservationStatus.ts` are not opened. The invites
   `EXPIRED -> err` versus enrollments `EXPIRED -> muted` difference is a design
   decision documented on both sides (1.5); a harmonizing merge would flatten it
   and each module's own tone test would simply be rewritten to match the merged
   behaviour, so no test would catch it. **The pager is the pager.**
2. **`toggleSort`.** Decision 3.
3. **`formatExpiryLabel` / `EXPIRING_WINDOW_MS`.** Decision 5.
4. **Sort-header wiring.** Five surfaces pass `sort` and `onSort` into their table
   in near-identical shapes with different field unions. Separate question.
5. **The `isPlaceholderData` disabling of prev/next.** It reads off the query, not
   the pager, and `WorkersPage` reads it off a *nested* query object
   (`revoked.isPlaceholderData`). Left at the call sites.
6. **The footer's composite span.** `SHOWING {range} · SORT {sort} · CURSOR
   PAGINATED` differs in every one of the seven - different middle segments,
   different separators, `SchedulesPage` does not even use `toLocaleString`
   (`:179` renders `{x}-{y} of {total}` raw where the other six render
   `x.toLocaleString()`). That is six variations and one latent inconsistency;
   extracting it is a design exercise, and the inconsistency is a separate
   proposal (section 11), not a fix to smuggle in here.
7. **The control row** above each table.
8. **`computePageRange` and the `web/src/jobs/pageRange.ts` re-export shim.**
   Unchanged, not moved, not deleted.
9. **Any `.tsx` file not listed in section 7.** In particular no table component,
   no `Table` primitive, no `Chip`.

---

## 6. The zero-diff gate: the definitive file set

The item lists seven and warns the set may be larger. It is: **twelve files**, in
two tiers. Every one of them must show `0 0` from `git diff --numstat` against the
merge base.

**Tier 1 - directly mount a migrated surface and assert pager behaviour (7):**

1. `web/src/jobs/JobsPage.test.tsx` - pager assertions at `:98-155`, `:176-249`,
   `:267-311` (in-flight disabling, absolute range across a partial last page,
   forward/back walk).
2. `web/src/workers/WorkersPage.test.tsx` - `:159-205`, `:207-230`
   (decommissioned-section pager; the only surface whose pager is behind a tab).
3. `web/src/schedules/SchedulesPage.test.tsx` - `:94-113`, `:116-172`,
   `:198-257`. **`:113` is the assertion most at risk** (see 1.2 and section 4,
   Group D).
4. `web/src/admin/users/UsersTab.test.tsx`
5. `web/src/admin/enrollments/EnrollmentsTab.test.tsx`
6. `web/src/admin/reservations/ReservationsTab.test.tsx`
7. `web/src/admin/invites/InvitesTab.test.tsx`

**Tier 2 - mount a migrated surface without driving paging (5).** The item
predicted these ("a separate transition or secrecy suite") and it was right about
two of them; the other three are transitive through `admin/tabs.ts` and
`app/router.tsx`. They cannot catch a paging regression, but they *can* catch a
crash, a changed render count, or a broken hook-order rule, and their diff is
gated identically:

8. `web/src/admin/invites/inviteTokenSecrecy.test.tsx` (`:13` imports
   `InvitesTab`, `:73` renders it)
9. `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx` (`:14`, `:63`)
10. `web/src/admin/AdminPage.test.tsx` (renders all four admin tabs by route)
11. `web/src/app/AdminRoute.test.tsx` (renders `AppRoutes`)
12. `web/src/App.test.tsx` (renders `<App />`, which mounts `JobsPage`)

**Explicitly NOT in the gate**, checked and excluded so nobody re-adds them:
`web/src/jobs/queryKeyDecoupling.test.tsx` (`renderHook` only, no component -
verified by reading its imports), every `use*.test.tsx` query-hook suite, every
`api.test.ts`, and `web/src/jobs/pageRange.test.ts`.

The one new test file (`web/src/lib/useCursorPager.test.ts`) is of course not in
the gate - it did not exist at the merge base.

---

## 7. Change set

Exactly these nine source paths, plus `docs/`, and nothing else:

```
web/src/lib/useCursorPager.ts          (new)
web/src/lib/useCursorPager.test.ts     (new)
web/src/jobs/JobsPage.tsx
web/src/workers/WorkersPage.tsx
web/src/schedules/SchedulesPage.tsx
web/src/admin/users/UsersTab.tsx
web/src/admin/enrollments/EnrollmentsTab.tsx
web/src/admin/reservations/ReservationsTab.tsx
web/src/admin/invites/InvitesTab.tsx
docs/                                  (spec, plan, retro, backlog close)
```

`web/dist` is tracked but stale from the scaffold; a frontend build dirties it, so
`git checkout -- web/dist/` before the change set is assembled.

---

## 8. Testing

### 8.1 The hook's own tests (the item names five; all five are required)

`web/src/lib/useCursorPager.test.ts`, via `renderHook` + `act`. No provider, no
MSW, no component.

1. **First page.** Initial state is `cursor === ''`, `startOffset === 0`,
   `canPrev === false`. Control: assert all three, not just one - a hook that
   returns a frozen object passes any single-field assertion.
2. **Forward walk.** `next('CUR1', 50)` -> `cursor === 'CUR1'`,
   `startOffset === 50`, `canPrev === true`. Then `next('CUR2', 50)` ->
   `cursor === 'CUR2'`, `startOffset === 100`.
3. **Backward walk.** From depth 2, `prev()` -> `cursor === 'CUR1'`,
   `startOffset === 50`, `canPrev === true`; `prev()` again -> `cursor === ''`,
   `startOffset === 0`, `canPrev === false`. **This is the test that catches the
   drift-B class of bug**: a hook that pushed the *next* cursor instead of the
   *current* one passes the forward walk and fails here, at the second `prev`,
   with `cursor === 'CUR1'` instead of `''`.
4. **Partial last page.** `next('CUR1', 50)` then `next('', 13)` - i.e. a final
   page of 13 rows with no further cursor - must be a **no-op** (falsy
   `nextCursor` guard). Then, separately, walk `next('CUR1', 50)` ->
   `next('CUR2', 13)` -> `prev()` and assert `startOffset` returns to `50`, not to
   `63`. The offsets stack, not `pageSize * depth`, is what makes a partial page
   correct, and this is the bug
   `bug-2026-06-21-jobs-pagination-footer-absolute-range` (closed) already
   shipped once.
   > **Correction (Phase 4 review, 2026-08-14):** that filename does not exist.
   > The two real closed items are
   > `docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md`
   > and `docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md`
   > - this line conflated the date of one with the surface of the other. Also,
   > mutation-proven: the two-step `next(50)/next(13)/prev()` walk above does NOT
   > discriminate the bug it is named after - `prev`'s restore mutated to
   > `copy.length * 50` and to `startOffset - 50` both leave it green, because with
   > only one prior page the naive and correct answers coincide. The shipped test
   > (`web/src/lib/useCursorPager.test.ts`) instead walks three pages with three
   > distinct sizes (13, 50, 7) so neither wrong formula can coincide by accident.
5. **`resetPaging` clears all three pieces.** From depth 2, `resetPaging()` ->
   `cursor === ''` **and** `startOffset === 0` **and** `canPrev === false`.
   Asserting only `cursor` passes against a reset that forgets `offsets`, which is
   precisely the failure mode that produces a wrong footer range with a correct
   page of rows.
   > **Correction (Phase 4 review, 2026-08-14):** disproved by mutation. Deleting
   > `setOffsets([])` from `resetPaging` leaves the entire suite green: `offsets` is
   > popped only while `stack` is non-empty, and `next` pushes exactly one offset
   > per stack entry, so a stale prefix left behind by a forgotten clear is dead
   > weight the pops never reach - it cannot produce a wrong footer range. The
   > shipped hook keeps the `setOffsets([])` call anyway (byte-for-byte with the
   > five originals that had a four-setter reset body), but the claim that a test
   > exists to catch its removal is wrong; none does, and the code comment at
   > `web/src/lib/useCursorPager.ts`'s `resetPaging` says so directly.

Plus two guards the item does not name but that pin decisions:

6. **`prev()` on the first page is a no-op** (not a throw, not a negative offset).
7. **`next(undefined, 50)` is a no-op.** This is the guard that moved out of the
   seven call sites; without a test, a future edit that drops it turns a
   `next_cursor`-less final page into a request for `cursor=undefined`.

Every one of these must be demonstrated RED against a deliberately-wrong hook
before being accepted GREEN. Plan-supplied test bodies are guesses until run.

### 8.2 No new tests on the seven surfaces

None. Any new assertion on a migrated surface is either (a) covering behaviour
that already had coverage, in which case it is noise, or (b) covering behaviour
that did **not** have coverage, in which case the correct response is to notice
that the gate was weaker than believed and say so in the verification report -
not to quietly strengthen it inside a refactor. If the migration reveals an
uncovered path, that is a finding and a follow-up proposal, not a test written in
this PR.

### 8.3 What jsdom cannot tell us here

Nothing that matters. This change has no layout, no paint, no focus, no key
events, no network shape change. The 2026-08-13/14 lesson about reassigning the
integration slot to a real browser on a zero-Go diff **does not apply**: there is
no rendering change to look at, and a browser lane would confirm only that the
pages still load. The Phase 4 integration slot is better spent on a fourth review
lens (see 8.4) than on a screenshot of an unchanged page.

### 8.4 Verification shape

`/code-review` plus review lenses, with one lens brief written specifically for
this slice: **"confirm the twelve-file zero-diff, then confirm `statusTone` in
`inviteStatus.ts` and `enrollmentStatus.ts` is untouched and still maps EXPIRED to
`err` and `muted` respectively."** That second half is stated as a review
instruction because it is the one difference a harmonizing edit would erase
without any test going red.

---

## 9. Acceptance criteria

1. `web/src/lib/useCursorPager.ts` exists, exports `useCursorPager` returning
   `{cursor, startOffset, canPrev, next, prev, resetPaging}`, and does not import
   from `@tanstack/react-query` or from any feature module.
2. The hook does not return the cursor stack or the offsets array.
3. **All seven** surfaces use it. A grep over the seven files for
   `useState<string[]>` returns zero hits, and `setStartOffset` / `setOffsets` /
   `setCursorStack` appear nowhere in `web/src` outside `useCursorPager.ts`.
4. `SchedulesPage` is migrated onto the canonical shape. `cursorStack`, `goNext`
   and `goPrev` do not survive anywhere in `web/src`. No eighth variant exists.
5. **All twelve files in section 6 show `0 0` from `git diff --numstat` against
   the merge base.** This is the primary gate; the command output is the evidence.
6. `useCursorPager.test.ts` covers first page, forward walk, backward walk,
   partial last page, `resetPaging` clearing all three, `prev` on the first page,
   and `next` with no cursor - seven tests, each proven RED first.
7. `statusTone` in `inviteStatus.ts` and `enrollmentStatus.ts` is **byte-identical
   to the merge base**, EXPIRED still maps to `err` in invites and `muted` in
   enrollments, and neither file appears in the change set at all. Asserted
   explicitly at review.
8. `web/src/admin/invites/InvitesTab.tsx:16-21` no longer claims a seventh
   un-extracted pager copy; its `toggleSort` accounting survives and reads
   **FIFTH**, not FOURTH.
9. `inviteStatus.ts:5-9` and `:76-77` (the expiry destination comment) and
   `ScheduleDetailPage.tsx:30-34` (the triad item's comment) are unchanged.
10. The change set is exactly the nine source paths in section 7 plus `docs/`. No
    status module, no table component, no `Chip`, no `pageRange.ts`, no
    `web/dist`.
11. `npm test` green (suite count moves up by the seven new hook tests and by
    nothing else) and `tsc -b && vite build` green.
12. `idea-2026-08-13-cursor-pager-hook.md` is closed via `/backlog close`, and its
    Resolution records four things: that `SchedulesPage` was a different algorithm
    rather than a renamed one; that `toggleSort` was deliberately left at five
    copies; that the `formatExpiryLabel` / `EXPIRING_WINDOW_MS` half was
    deliberately **not** done and its trigger (a third status module) lives in
    `inviteStatus.ts:5-9`; and the twelve-file gate result.

---

## 10. Risks

- **The gate gets negotiated at file eleven of twelve.** The single largest risk,
  and the reason section 0 comes first. Mitigation is procedural, not technical:
  the plan states the gate before task one, and the verification report prints
  `git diff --numstat` rather than asserting cleanliness.
- **`SchedulesPage.test.tsx:113` goes red.** It reads the raw `cursor` query
  param and requires `null` on the first page. It survives the `undefined -> ''`
  change only because of `api.ts:41`'s truthiness guard and `useSchedules.ts:10`'s
  `?? ''`. Both are verified in this spec at HEAD and must be re-verified before
  the Group D task. If it goes red anyway, that is the finding: stop.
- **A reviewer reads the backlog item instead of this spec and concludes the
  seven copies are verbatim.** They are not (1.2). The mechanical framing is
  correct for four of seven and wrong for three, and a reviewer operating on the
  item's framing would rubber-stamp `SchedulesPage`. Mitigations: 1.2 is the
  loudest section in the doc, Group D is its own task, and hook test 3 fails
  specifically on the wrong-push bug.
- **`WorkersPage`'s pager is behind a tab.** Its state is created
  unconditionally at `:41-44` but its UI only exists in the `decommissioned`
  branch. If the hook call is moved below the `if (section === 'decommissioned')`
  early return, hook order breaks on tab switch. React will scream, so this fails
  loudly rather than silently - but it is the one place where "move the state into
  a hook" has a placement constraint.
- **Someone harmonizes `statusTone` while in the admin directory.** No test would
  catch it, because each module's tone test would be rewritten to match.
  Mitigations: criterion 7, the change set in section 7, and the dedicated review
  lens brief in 8.4.
- **`EnrollmentsTab` / `ReservationsTab` empty-state hatches get dropped.** They
  are the only `prev` render sites outside a footer, they are easy to miss when
  scanning for the footer pattern, and `EnrollmentsTab.test.tsx` /
  `ReservationsTab.test.tsx` do cover them - so this fails loudly. Recorded so the
  engineer looks for nine render sites, not seven.
- **The suite count moves by more than seven.** If it does, something rendered
  differently. Worth checking as a cheap independent signal alongside the numstat.

---

## 11. Follow-ups to propose (NOT to file automatically)

Per the standing rule these are proposals for human accept at Phase 6.

| Proposal | Why |
|---|---|
| `idea-2026-08-14-toggle-sort-generic` (low) - lift the five copies of `toggleSort` into one generic helper. | Decision 3 leaves it at five copies. The `InvitesTab` comment keeps carrying the debt, but a comment is not a queue. Blocked on nothing once the pager lands; the only real question is the generic's shape. |
| `bug-2026-08-14-schedules-footer-range-not-localized` (low) - `SchedulesPage.tsx:179` renders `{x}-{y} of {total}` where the other six surfaces render `x.toLocaleString()`. | A real inconsistency found while verifying the footer for scope exclusion 6. It is a one-line fix that this slice must **not** make (it would change rendered text and break the gate), and it will be invisible again the moment nobody is staring at all seven footers side by side. |
| Note on `idea-2026-08-12-detail-page-state-triad-primitive` - add "do not run concurrently with the cursor-pager extraction; both are zero-diff-gated refactors over overlapping test directories." | That item's Related section already cross-links this one but frames it as "worth reading together". The concurrency hazard is sharper than that and belongs in the item, not only in this spec. |

Deliberately **not** proposed: a `canNext` on the hook (it is a fact about the
query, not the pager); a merged single-`useState` internal shape (three shipped
comments defend the current one and there is no defect); extracting the footer
span (six genuine variations); anything touching `statusTone`.

---

## 12. Escalations - what would have gone to a human

Recorded per the autonomous-mode convention. Each was decided rather than asked;
each is a place where a human might reasonably call it the other way.

1. **Whether drift B changes the item's cost estimate enough to re-prioritize.**
   The item says "the extraction is an afternoon" on the premise that all seven
   are verbatim. Three are not, and `SchedulesPage` needs real reasoning rather
   than a rename. **Called:** proceed - the drift makes it a longer afternoon, not
   a different project, and every drift is provably behaviour-equivalent. But if
   a human wanted to re-check the priority against the roadmap, this is the fact
   they would want.
2. **Whether the expiry half should get its own backlog item on exclusion.**
   **Called:** no - the source comments in `inviteStatus.ts` already carry both
   the destination and the trigger, which is a more durable carrier than a file
   in `docs/backlog/`, and filing an item for a two-consumer extraction would
   contradict the extract-before-the-third rule this whole item is founded on.
   A human might reasonably prefer the item, on the grounds that a closed parent
   item makes the comment an orphan.
3. **Whether Tier 2 of the gate (five files) should be a hard `0 0` or merely
   "must stay green".** **Called:** hard `0 0`. They mount migrated components, so
   a diff in them is as much a signal as a diff in Tier 1, and the cost of the
   stricter rule is zero if the refactor is genuinely behaviour-preserving. A
   human might relax it to "green" on the grounds that these files change for
   unrelated reasons more often.
4. **Correcting `FOURTH` to `FIFTH` inside a refactor slice.** **Called:** in
   scope, because the comment is being edited regardless and leaving a known-false
   number in a sentence you just rewrote is worse than not touching it. A stricter
   reading of "behaviour-preserving refactor, nothing else" would push it out.
