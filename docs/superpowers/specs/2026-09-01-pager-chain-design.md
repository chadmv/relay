# The pager chain: four items, one branch, one PR - Design

Date: 2026-09-01
Status: Draft (autonomous gate mode; conductor review)

Lane B of the web SPA batch. Four backlog items that all touch the same seven
paginated surfaces and the same gate-frozen test files, so they ship as one
branch and one PR with one commit per item, in this fixed order:

1. `docs/backlog/idea-2026-08-14-cursor-pager-next-takes-the-page.md`
2. `docs/backlog/idea-2026-08-14-toggle-sort-generic.md`
3. `docs/backlog/bug-2026-08-14-schedules-footer-range-not-localized.md`
4. `docs/backlog/bug-2026-08-14-stale-citations-in-gate-frozen-test-files.md`

Frontend only. Zero Go, zero SQL, zero proto, zero migration, no endpoint
change, no new dependency.

Written in autonomous gate mode: every question the four items left open was
decided here and carries its options, its choice and its reason in that item's
Decisions section rather than being asked. Section 6 lists what would have been
escalated to a human.

---

## 0. The gate, and the exact moment the frozen set is unfrozen

Read this before anything else. The chain's whole structure exists to serve it.

Items 1 and 2 are behaviour-preserving refactors. Their licence is the project's
standing refactor gate (`reference_refactor_gate_byte_identical_tests`, worked in
`docs/superpowers/specs/2026-08-14-cursor-pager-hook.md` section 0 and its retro):
**a zero-line diff to every test file that existed at the merge base**. An
assertion needing adjustment during a change that is supposed to change nothing
IS the finding, not an obstacle to it. There is no branch in which a selector
gets fixed up and the migration continues.

Items 3 and 4 then deliberately edit three of those files. So the freeze is not a
property of the branch; it is a property of a commit range.

### 0.1 The gate is stated enumeration-free, over files that existed at the base

The 2026-08-14 retro's promoted lesson is that stating a refactor gate over an
enumerated file list fails twice: the enumeration was wrong (it missed
`web/src/admin/AdminTabs.test.tsx`, which reaches four migrated surfaces
transitively through `web/src/admin/tabs.ts`), and the enumeration-free
formulation that replaced it was itself stated over the diff's file list
("exactly one new test file") and went stale the moment the slice added five more.

State it over the base, not over the diff:

```
git diff --numstat --diff-filter=M <merge-base> <commit> -- web/src \
  | grep -E '\.test\.tsx?$'
```

`--diff-filter=M` means added test files never appear, so the command survives
the branch adding new test files. The expected output per commit:

| After commit | Licensed modified test files | Everything else |
|---|---|---|
| 1 (`next` takes the page) | `web/src/lib/useCursorPager.test.ts` only | must print nothing |
| 2 (`toggleSort`) | none | must print nothing |
| 3 (schedules footer) | `web/src/schedules/SchedulesPage.test.tsx` | must print nothing |
| 4 (citations) | `web/src/admin/reservations/ReservationsTab.test.tsx`, `web/src/admin/enrollments/EnrollmentsTab.test.tsx` | must print nothing |

**The evidence must be captured per commit, not once at the tip.** A tip-only run
cannot distinguish "item 3 edited `SchedulesPage.test.tsx`" from "item 1 did and
nobody noticed". Run the command four times, once per commit, and paste all four
outputs into the verification report.

`useCursorPager.test.ts` is the one exception in commit 1 and it is the whole
point of item 1: the hook's API is the thing being changed, so its own tests must
change. Item 1's backlog entry says so explicitly.

### 0.2 The twelve-file corroborating table

The enumeration is not the gate, but it is useful corroboration and the conductor
asked for it. These twelve must each print `0` insertions and `0` deletions from
`git diff --numstat <merge-base> <tip>` for the whole branch, with the three
licensed exceptions from the table above:

Tier 1, the seven surfaces' own suites:

1. `web/src/jobs/JobsPage.test.tsx`
2. `web/src/workers/WorkersPage.test.tsx`
3. `web/src/schedules/SchedulesPage.test.tsx` - **licensed to change at commit 3**
4. `web/src/admin/users/UsersTab.test.tsx`
5. `web/src/admin/enrollments/EnrollmentsTab.test.tsx` - **licensed at commit 4**
6. `web/src/admin/reservations/ReservationsTab.test.tsx` - **licensed at commit 4**
7. `web/src/admin/invites/InvitesTab.test.tsx`

Tier 2, the five `*.pager.test.tsx` siblings added by the 2026-08-14 Phase 4
remediation. Verified present at HEAD by glob:

8. `web/src/jobs/JobsPage.pager.test.tsx`
9. `web/src/schedules/SchedulesPage.pager.test.tsx`
10. `web/src/workers/WorkersPage.pager.test.tsx`
11. `web/src/admin/users/UsersTab.pager.test.tsx`
12. `web/src/admin/reservations/ReservationsTab.pager.test.tsx`

The enumeration-free command in 0.1 additionally covers the five tier-2 files
from the 2026-08-14 gate (`inviteTokenSecrecy.test.tsx`,
`enrollmentTokenSecrecy.test.tsx`, `AdminPage.test.tsx`, `AdminRoute.test.tsx`,
`App.test.tsx`) and `AdminTabs.test.tsx`, none of which is enumerated here. That
is the point of stating it enumeration-free.

### 0.3 The gate is a NEGATIVE control, and item 1 must not repeat the 2026-08-14 mistake

The single most valuable finding of the slice that created these four items was
that its gate held perfectly and licensed nothing on six wirings: two of the
seven `pageSize` arguments were provably unconstrained (a bogus `999` at
`WorkersPage` and at `ReservationsTab` left both suites fully green).

A zero-diff gate proves you did not weaken the tests. It says nothing about
whether the tests constrained the thing you changed. Item 1 therefore carries a
mutation obligation, in section 1.6. Item 2 carries a smaller one.

Mutation runs must happen in an isolated tree, never in the shared worktree
(`feedback_mutation_testing_needs_isolated_tree`), the mutation must be verified
to have actually applied by diffing against a saved copy rather than by assuming
a string replace succeeded (`reference_verify_the_mutation_applied`), and a
mutation is reverted from that saved copy, never with `git checkout --`, which
would discard the uncommitted guard under test
(`feedback_never_git_checkout_to_revert_a_mutation`).

### 0.4 Every acceptance criterion in all four items is RED at HEAD

Checked before writing, per `reference_a_replacement_criterion_must_not_be_already_green`:

- Item 1: `pager.next(` with a `.length` second argument occurs at seven call
  sites. Not green.
- Item 2: `function toggleSort` occurs five times in `web/src`, none in
  `web/src/lib/`. Not green.
- Item 3: `SchedulesPage.tsx`'s footer renders `{x}-{y} of {total}` raw and has
  no zero-rows branch. Not green.
- Item 4: all three citations name line numbers and all three were re-verified
  stale at HEAD (section 4.1). Not green.

---

## 1. Item 1 - `useCursorPager.next` takes the page

Backlog item: `idea-2026-08-14-cursor-pager-next-takes-the-page` (idea, medium).

### 1.1 The problem, restated from verified state

`useCursorPager` (`web/src/lib/useCursorPager.ts`) is deliberately asymmetric. It
returns `canPrev: boolean` and never the `stack` or `offsets` arrays, because a
consumer holding one could `pop()` it and desync the two behind the hook's back -
the frontend form of "no interior pointers across locks", and it is right.

And then `next` takes `(nextCursor: string | undefined, pageSize: number)`, where
`pageSize` is a bare number the hook cannot check against anything. `pageSize` is
the exact value both of this project's shipped pagination bugs were about
(`docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md`,
`docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md`),
and it is measurably unconstrained: the 2026-08-14 mutation sweep passed `999` at
two call sites and both suites stayed green.

**Verified at HEAD, by opening all seven call sites in full:** every one of them
reads the cursor and the row count off the *same* query result object in the same
render.

| Surface | Call | Rows variable |
|---|---|---|
| `JobsPage` | `pager.next(data?.next_cursor, jobs.length)` | `jobs = data?.items ?? []` |
| `WorkersPage` | `revokedPager.next(revoked.data?.next_cursor, revokedWorkers.length)` | `revokedWorkers = revoked.data?.items ?? []` |
| `SchedulesPage` | `pager.next(data?.next_cursor, schedules.length)` | `schedules = data?.items ?? []` |
| `UsersTab` | `pager.next(data?.next_cursor, users.length)` | `users = data?.items ?? []` |
| `EnrollmentsTab` | `pager.next(data?.next_cursor, enrollments.length)` | `enrollments = data?.items ?? []` |
| `ReservationsTab` | `pager.next(data?.next_cursor, reservations.length)` | `reservations = data?.items ?? []` |
| `InvitesTab` | `pager.next(data?.next_cursor, invites.length)` | `invites = data?.items ?? []` |

That table is the behaviour-preservation argument. `X.length` and
`page.items.length` are the same number whenever `page` is defined, and when
`page` is undefined both the old form (falsy `next_cursor`) and the new form
(undefined page) are a no-op. There is no case where the two forms disagree.

### 1.2 What changes, by symbol

`web/src/lib/useCursorPager.ts`:

- New exported structural type `CursorPage`, declared by the hook, imported from
  nothing.
- `CursorPager.next` changes from `(nextCursor: string | undefined, pageSize: number) => void`
  to `(page: CursorPage | undefined) => void`.
- `next`'s body derives `page?.next_cursor` for the guard and the advance, and
  `page.items.length` for the offset accumulation.
- `cursor`, `startOffset`, `canPrev`, `prev` and `resetPaging` are untouched in
  signature and in body. The four `useState` pieces and the plain-setter
  mechanics are untouched.

The seven call sites each lose one argument:

- `JobsPage.tsx` -> `pager.next(data)`
- `WorkersPage.tsx` -> `revokedPager.next(revoked.data)`
- `SchedulesPage.tsx` -> `pager.next(data)`
- `UsersTab.tsx` -> `pager.next(data)`
- `EnrollmentsTab.tsx` -> `pager.next(data)`
- `ReservationsTab.tsx` -> `pager.next(data)`
- `InvitesTab.tsx` -> `pager.next(data)`

The rows variable at each site stays exactly as it is; it still feeds
`computePageRange` and the table. Nothing else in any of the seven changes. In
particular the `disabled={!data?.next_cursor || isPlaceholderData}` expression on
each next button is untouched: whether a further page exists is a fact about the
query result, not about the pager, and moving it in was rejected in 2026-08-14
and stays rejected.

### 1.3 Decisions

**Decision 1.1 - the structural type is `CursorPage`, with `items: readonly unknown[]`.**

Options weighed: (a) `items: unknown[]`, as the item sketches; (b)
`items: readonly unknown[]`; (c) a generic over the element type.

Chosen: (b). The hook reads only `.length`, so the element type is never used and
a generic parameter would be inferred, unused, and pure signature noise - (c)
buys nothing. Between (a) and (b), `readonly` states in the type what the hook's
existing design already argues in prose: the pager does not mutate arrays it does
not own. `Worker[]`, `Job[]` and the rest are all assignable to
`readonly unknown[]`, so no call site pays for it.

**Decision 1.2 - `next_cursor` is REQUIRED on `CursorPage`, not optional. This
refutes the item's own sketch.**

The item proposes an optional `next_cursor`. All seven response page interfaces
declare it required and non-nullable, verified at HEAD:
`JobsPage` (`web/src/jobs/api.ts`), `WorkersPage` (`web/src/workers/api.ts`),
`SchedulesPage` (`web/src/schedules/api.ts`), `AdminUsersPage`
(`web/src/admin/users/api.ts`), `AgentEnrollmentsPage`
(`web/src/admin/enrollments/api.ts`), `InvitesPage`
(`web/src/admin/invites/api.ts`), `ReservationsPage`
(`web/src/admin/reservations/api.ts`) - every one is
`{ items: T[]; next_cursor: string; total: number }`.

If the hook's field is optional, a response type that ever loses or renames
`next_cursor` still satisfies the parameter, the property reads `undefined`,
`next` becomes a silent permanent no-op, and the next button is permanently
disabled with no compile error and nothing red. With the field required, that
same rename is a compile error at all seven call sites. Required fails closed;
optional fails open. This is the same shape as
`reference_equality_guard_is_blind_to_absent_optional_fields`.

So:

```ts
export interface CursorPage {
  next_cursor: string
  items: readonly unknown[]
}
next: (page: CursorPage | undefined) => void
```

The name is `CursorPage`, not `Page`, because `web/src/workers/api.ts` already
exports a type literally named `WorkersPage` and three others end in `Page`; a
bare `Page` invites an import collision at exactly the seven files that import
both.

**Decision 1.3 - the `| undefined` moves from the cursor to the page, and it is
still what makes tsc enforce the falsy guard.**

The current doc comment claims the `string | undefined` on `nextCursor` makes
deleting the guard a compile error rather than an untested regression. That
property survives the change and it is worth stating why, because it is the
reason the parameter is `CursorPage | undefined` rather than two overloads.
`setCursor` takes `string`; `page?.next_cursor` is `string | undefined`; assigning
it without the guard does not typecheck. The enforcement simply moves from one
optional operand to another.

**Decision 1.4 - `startOffset` keeps accumulating through the `offsets` stack.
Nothing about accumulation changes.**

The item requires this and it is correct. `startOffset` grows by the actual page
size on each forward step and `prev` restores the popped real offset, which is
what makes a partial final page's absolute range honest. A stack-depth times
limit formula would reintroduce both closed bugs. The only thing item 1 changes
is where the increment's operand comes from: `page.items.length` instead of a
hand-passed argument. `useCursorPager.test.ts`'s three-page three-size walk
(sizes 13, 50, 7, with the coincidence analysis above it) is the test that pins
this, and it survives adapted.

**Decision 1.5 - the placeholder-data case changes nothing, for two independent
reasons.**

The item asks for confirmation that a click cannot arrive with stale `data` under
`keepPreviousData`, since that is exactly the case where a derived size would
differ from a hand-passed one. Two answers, and both hold:

- *Unreachable.* Every next button's `disabled` includes `isPlaceholderData`.
  TanStack sets that flag true for exactly the window in which `data` is the
  previous page's payload, so the button is disabled for the whole window. A
  disabled button dispatches no click, in a browser and in `user-event` alike.
- *Harmless even if reachable.* Old and new read the cursor and the count off the
  SAME object. If a click did land while `data` were stale, the old form would
  push a stale cursor with a stale count and the new form would push a stale
  cursor with a stale count. They cannot disagree, because there is only one
  object to be stale.

The second answer is the load-bearing one, because it does not depend on a
`disabled` prop anybody could delete. `isPlaceholderData` stays at the call
sites; it is a query fact and remains out of the hook.

**Decision 1.6 - the hook's file header and `resetPaging` doc lose their counts
and their history; the hazards stay.**

The hook shipped on 2026-08-14, before the comment policy landed in CLAUDE.md on
2026-08-30. Item 1 rewrites the `next` doc and the header paragraph that contains
the `pageSize` sentence, so it is editing these blocks regardless, and leaving
policy-violating content inside a paragraph you just rewrote is the same call the
2026-08-14 spec made in its Decision 7. Specifically:

- The header's census of what the seven surfaces used to carry ("five were
  byte-identical copies, WorkersPage had ... SchedulesPage ran a different
  algorithm") is history plus a census of other files. **Deleted, not rewritten.**
- `resetPaging`'s "the surfaces reset from 9 call sites across 6 surfaces, on four
  distinct trigger conditions" is a count of other files that nothing reddens when
  it moves. **The count is deleted;** the hazard it decorates - the server 400s a
  cursor issued under a different sort, so consumers MUST call this on any change
  to the sort or the filters, and the hook deliberately does not watch a sort
  argument - stays.
- The `canNext` note's "Every surface computes its next button's disabled as ..."
  is a claim about the complement. **Deleted.** The rationale that survives is
  this hook's own contract: there is no `canNext` because whether a further page
  exists is a fact about the query result, not about the pager.
- The StrictMode plain-setters warning is a genuine hazard the code cannot show
  and **stays**, minus its "this is the merged form of the warning that used to
  sit in JobsPage, SchedulesPage and UsersTab" provenance and its
  "seven shipped surfaces" count.
- `resetPaging`'s body comment keeps its mechanism (offsets is popped only while
  stack is non-empty and `next` pushes exactly one offset per stack entry, so a
  stale prefix is unreachable) and **loses** "no test covers it" and the
  byte-for-byte-with-the-five-originals provenance.

`web/src/lib/useCursorPager.ts` is the file being changed, not a gate-frozen
file, so none of this is at risk of costing the gate its evidence. Comment
tidying elsewhere in `web/src` is explicitly out of scope for this commit; the
surviving instances are proposed as a follow-up item in section 5.

The reasoning being deleted is not lost. It lives in
`docs/superpowers/specs/2026-08-14-cursor-pager-hook.md` and
`docs/retros/2026-08-14-cursor-pager-hook.md`, which is where records of a moment
belong.

### 1.4 Gate for item 1

Frozen set: every test file that existed at the merge base, except
`web/src/lib/useCursorPager.test.ts`.

Evidence: the section 0.1 command run at commit 1, printing exactly one line, for
`web/src/lib/useCursorPager.test.ts`.

### 1.5 The hook's own tests

`web/src/lib/useCursorPager.test.ts` is adapted, not rewritten. Every existing
test keeps its property and its name; only the call shape changes. A helper local
to the test file builds pages, so the arity change is expressed once:

```ts
function page(next_cursor: string, size: number) {
  return { next_cursor, items: Array.from({ length: size }, (_, i) => i) }
}
```

`items` must be a real array of the stated length, never an object with a
`length` property cast to an array: the hook reads `.length` and a fake would let
a mutation that reads some other property survive.

Mapping of the eight existing tests to the new signature:

| Existing test | New call shape |
|---|---|
| starts on the first page | unchanged, no `next` call |
| next advances the cursor and accumulates the real page size | `next(page('CUR1', 50))`, then `next(page('CUR2', 50))` |
| prev walks back to the cursor of the page we came from | two `next(page(...))`, two `prev()` |
| a page with no next_cursor is a no-op | `next(page('', 13))` |
| next(undefined, n) is a no-op | `next(undefined)`; test renamed to `next(undefined) is a no-op` |
| paging back off a partial last page restores the previous offset | sizes 50 then 13 |
| paging back through three partial pages restores each real offset | sizes 13, 50, 7, comment block preserved verbatim |
| resetPaging returns to the first page | two `next(page(...))`, then `resetPaging()`, then the render-count no-op check |

Plus one new test that only the new API can express, and that is the item's whole
reason for existing:

- **`the offset advances by the page's own row count, not by a caller-supplied number`**
  - `next(page('CUR1', 7))` and assert `startOffset === 7`. Under the old API a
    caller could pass `50` beside a seven-row page and nothing could tell. Under
    the new API the two cannot be given separately. The test's job is to pin that
    the hook reads `items.length` and not a constant: mutate the accumulation in
    the hook to a literal `50` and this test is the one that reddens.

Every adapted test must be demonstrated RED against a deliberately-wrong hook
before being accepted GREEN. Plan-supplied test bodies are guesses until run
(`reference_plan_supplied_tests_untrusted`).

### 1.6 The mutation obligation

The gate cannot see whether item 1's rewiring is constrained. Two mutations,
each run in an isolated tree, each verified applied by diff, each reverted from a
saved copy:

- **M1 - wrong object at `WorkersPage`.** Change `revokedPager.next(revoked.data)`
  to `revokedPager.next(data)`. This typechecks (`data` is
  `WorkersPage | undefined` from `useWorkers`) and is exactly the class of error
  the single-argument form makes possible for the first time: you can no longer
  pass a wrong SIZE, but you can pass a wrong PAGE. Predicted RED:
  `WorkersPage.test.tsx`'s decommissioned-section pager tests. If nothing
  reddens, that is a finding, and the remedy is a new assertion in
  `web/src/workers/WorkersPage.pager.test.tsx` - which is frozen at commit 1, so
  the remedy lands as a **new sibling file**, never as an edit to a frozen one.
  This is the same call the 2026-08-14 conductor made when an engineer asked for
  an "adding cases is obviously safe" exception: a mechanical gate stops being a
  gate the moment it admits a judgment call.
- **M2 - drop the accumulation.** Replace the `page.items.length` operand in the
  offset increment with a literal `50`. Predicted RED: the three-page three-size
  walk in `useCursorPager.test.ts`, the new row-count test above, and the
  partial-last-page footer tests in `JobsPage.test.tsx` and
  `SchedulesPage.test.tsx`.

M2 is the control that proves the harness works: if M2 also survives, the
mutations did not apply (`reference_mutation_battery_needs_green_baseline`).

### 1.7 Acceptance criteria for item 1, each mapped to a named test

| # | Criterion | Pinned by |
|---|---|---|
| 1.A | `next` takes one argument and derives both the cursor and the row count from it | `useCursorPager.test.ts` -> `the offset advances by the page's own row count, not by a caller-supplied number` |
| 1.B | No call site passes a row count. A grep for `pager.next(` in `web/src` shows no second argument at any of the seven sites | review plus `tsc -b` (a second argument is now an arity error) |
| 1.C | A partial final page still produces a correct absolute range | `useCursorPager.test.ts` -> `paging back through three partial pages restores each real offset, not a fixed-page-size guess`; and `SchedulesPage.test.tsx` -> `pagination footer shows correct absolute range on partial last page after paging forward` |
| 1.D | `prev` restores the real previous offset | `useCursorPager.test.ts` -> `paging back off a partial last page restores the previous offset, not pageSize * depth` |
| 1.E | An undefined page is a no-op | `useCursorPager.test.ts` -> `next(undefined) is a no-op` |
| 1.F | A page with no further cursor is a no-op | `useCursorPager.test.ts` -> `a page with no next_cursor is a no-op` |
| 1.G | The hook still imports only `react` | review of the import line; `CursorPage` is declared in the file, imported from nothing |
| 1.H | Zero-line diff to every test file that existed at the base except `useCursorPager.test.ts` | section 0.1 command at commit 1 |
| 1.I | The rewiring is constrained, or the gap is reported | M1 and M2 results in the verification report |

### 1.8 Risks

- **The gate gets negotiated when one `*.pager.test.tsx` sibling wants one line.**
  The five siblings drive the UI and never mention `next`'s arity, so they should
  not need to change. If one does, that is the finding: it means the click path
  changed, which is precisely what item 1 claims it does not.
- **A reviewer accepts an optional `next_cursor` from the item's sketch.** The
  item's code block is the least verified thing in it, and this project has twice
  had an item's snippet be the thing the item was wrong about. Decision 1.2 is the
  correction and it is a fail-open versus fail-closed difference, not a style call.
- **M1 survives.** Likely, on the evidence: `WorkersPage`'s `pageSize` was one of
  the two the 2026-08-14 sweep proved unconstrained. Plan for it.

---

## 2. Item 2 - one generic `toggleSort`

Backlog item: `idea-2026-08-14-toggle-sort-generic` (idea, low).

### 2.1 Verified current state

The item asks to verify the count, noting it has moved once. **Five copies of
`function toggleSort` exist in `web/src` and there is no sixth**, confirmed by a
grep for `function toggleSort` over `web/src`. All five bodies are the same five
lines. Their line numbers have all moved except one, which is itself an argument
for citing by symbol:

| File | Item says | At HEAD | Signature |
|---|---|---|---|
| `web/src/workers/WorkersPage.tsx` | `:22` | `:23` | `(field: SortField, current: WorkerSort): WorkerSort` |
| `web/src/admin/users/UsersTab.tsx` | `:17` | `:18` | `(field: UserSortField, current: UserSort): UserSort` |
| `web/src/admin/enrollments/EnrollmentsTab.tsx` | `:16` | `:16` | `(field: EnrollmentSortField, current: EnrollmentSort): EnrollmentSort` |
| `web/src/admin/reservations/ReservationsTab.tsx` | `:21` | `:22` | `(field: ReservationSortField, current: ReservationSort): ReservationSort` |
| `web/src/admin/invites/InvitesTab.tsx` | `:26` | `:26` | `(field: InviteSortField, current: InviteSort): InviteSort` |

The item is right that `SchedulesPage` and `JobsPage` use a select element and
carry no copy; verified by reading both.

**Refutation of the item's framing of the field type.** The item says "each
module has a `*SortField` union that pairs with its `*Sort`". Four do.
`WorkersPage` does not: its field union is named `SortField`, it lives in
`web/src/workers/WorkersTable.tsx` (a component module, not `api.ts`), and it is
`'name' | 'status' | 'last_seen_at'`, while `WorkerSort` in
`web/src/workers/api.ts` additionally carries `'created_at'` and `'-created_at'` -
which is the page's own default sort and is not a sortable column header. So for
four of the five, the sort union is exactly the field union plus each of its
members prefixed with a minus sign; for `WorkersPage` it is strictly larger. That
single exception decides Decision 2.2.

### 2.2 What changes, by symbol

New: `web/src/lib/toggleSort.ts` exporting one function `toggleSort`, and
`web/src/lib/toggleSort.test.ts`. `web/src/lib/` is the established home for
behaviour-only helpers with `.test` siblings (`pageRange.ts`, `useNow.ts`,
`useDebouncedValue.ts`, `useCursorPager.ts`), and the file-named-after-the-export
convention comes from `pageRange.ts`.

Deleted: the five local `function toggleSort` declarations, and the comment block
immediately above each of them. Added: one `import { toggleSort } from '...'` per
file. The five `pickSort` bodies and `WorkersPage`'s inline
`onSort={(f) => setSort((cur) => toggleSort(f, cur))}` are otherwise untouched.

### 2.3 Decisions

**Decision 2.1 - the cast lives inside the helper, and there is exactly one of
them. This corrects the item's arithmetic.**

Options: (a) one cast in the helper; (b) a cast at each of the five call sites.

Chosen: (a), which the item recommends and which is right - (b) is no better than
today and spreads an unsound assertion across five files instead of concentrating
it where it can be documented and tested.

The item says "five casts become one". Written in the natural shape it becomes
**two**, because the generic makes `field` a plain `string`, and `string` is not
assignable to `S` either, so the untaken branch needs its own cast:

```ts
// two casts - do not ship this shape
if (current.replace('-', '') === field) {
  return (current.startsWith('-') ? field : `-${field}`) as S
}
return field as S
```

Ship the single-cast shape instead, which also gives the hazard comment one
place to sit:

```ts
export function toggleSort<S extends string>(field: string, current: S): S {
  const next =
    current.replace('-', '') === field
      ? current.startsWith('-')
        ? field
        : `-${field}`
      : field
  return next as S
}
```

**Decision 2.2 - the field parameter is `string`, NOT typed against the module's
`*SortField` union.**

Options, in words rather than in a table cell, because two of them need a
template-literal type:

- (a) one generic on the current sort only: `toggleSort<S extends string>(field: string, current: S): S`.
- (b) two generics, one per parameter, unrelated to each other.
- (c) one generic on the FIELD, with the current sort constrained to that field
  or that field prefixed with a minus sign, using a template-literal type.

Chosen: (a).

(c) is the only one that would buy real safety - it would relate the field to the
sort union and make a typo a compile error - and it is refuted by `WorkersPage`.
With the field generic bound to `SortField`, the derived sort union excludes
`'-created_at'`, so `toggleSort(f, cur)` with `cur: WorkerSort` does not
typecheck. Making it compile would mean either widening `SortField` to include a
column that has no header, or narrowing `WorkerSort`, and both are behaviour
changes to a surface whose test file is frozen at this commit. Refuted by exactly
one of the five, which is why the item's instruction to verify the count mattered
for more than the count.

(b) is (a) with an inferred parameter that relates to nothing. It costs a type
parameter in the signature and buys no constraint. Rejected as signature noise.

(a) it is. The residual weakness is stated plainly: a `string` field accepts any
string, so a typo produces an `S`-typed value that is not a member of `S`. That
weakness exists today in all five copies, unchanged - each local copy's field
parameter is narrow only because the caller passes a narrow value, and the cast
in each body is already unsound in exactly the same way. Item 2 does not make it
worse and does not claim to fix it. A follow-up that closes it properly is
proposed in section 5.

**Decision 2.3 - the five comment blocks are DELETED, not rewritten.**

Four of them ("Same shape as WorkersPage's toggleSort", "Same shape as UsersTab's
toggleSort (web/src/admin/users/UsersTab.tsx)", "Same shape as EnrollmentsTab's
toggleSort (EnrollmentsTab.tsx)", and the invites one) are cross-file references
whose subject stops existing. `InvitesTab.tsx`'s block additionally carries the
FIFTH accounting, a parenthetical explaining that it used to read FOURTH, and a
pointer at the pager half of the debt. Under the comment policy that block is
three separate violations at once: a count of other files, change history, and
review narrative. The item's acceptance criterion says it loses the accounting
comment entirely, and "entirely" is right - there is nothing in any of the five
worth rewriting, because the one thing they all said ("this is a copy of a thing
elsewhere") stops being true.

The shared helper gets a short comment stating the one hazard the code cannot
show: the cast asserts on behalf of the caller, so it is sound only while the
field argument is a member of the union the caller's sort type is drawn from, and
it cites `toggleSort.test.ts` as the pin. No counts, no history, no census.

### 2.4 Gate for item 2

Frozen set: every test file that existed at the merge base, with no exception at
all - `useCursorPager.test.ts` was licensed only at commit 1.

Evidence: the section 0.1 command run at commit 2, printing nothing.

The five surfaces' own suites drive sorting through the rendered table headers,
so if the shared helper is not behaviour-identical they redden. That is the
positive control this item gets for free and item 1 does not.

### 2.5 Tests

`web/src/lib/toggleSort.test.ts`, direct, no React:

1. **`clicking the active ascending column flips it to descending`** -
   `toggleSort('name', 'name')` is `'-name'`.
2. **`clicking the active descending column flips it back to ascending`** -
   `toggleSort('name', '-name')` is `'name'`.
3. **`clicking a different column selects it ascending from an ascending current`** -
   `toggleSort('email', 'name')` is `'email'`.
4. **`clicking a different column selects it ascending from a descending current`** -
   `toggleSort('email', '-name')` is `'email'`. This is the case the item names
   and it is the one that discriminates a naive implementation that preserves the
   leading minus sign when switching columns.
5. **`a field whose name is a prefix of the current field is not treated as active`** -
   `toggleSort('created', 'created_at')` is `'created'`, not `'-created'`. Pins
   that the comparison is equality on the stripped string and not a
   `startsWith`, which is the mutation a reader is most likely to introduce while
   "simplifying" the `replace` call.

Each proven RED first against a deliberately-wrong helper.

### 2.6 Acceptance criteria for item 2, each mapped to a named test

| # | Criterion | Pinned by |
|---|---|---|
| 2.A | Exactly one `function toggleSort` in `web/src`, in `web/src/lib/` | grep in the verification report; `tsc -b` (a surviving local copy would shadow the import and be flagged, or collide) |
| 2.B | The helper has direct tests for flip-up, flip-down and select-other from either direction | `toggleSort.test.ts` tests 1 through 4 |
| 2.C | Behaviour at the five surfaces is unchanged | `WorkersPage.test.tsx`, `UsersTab.test.tsx`, `EnrollmentsTab.test.tsx`, `ReservationsTab.test.tsx`, `InvitesTab.test.tsx` sort tests, all green with a zero-line diff |
| 2.D | `InvitesTab.tsx` loses its accounting comment | review; a grep for `FIFTH` in `web/src` returns nothing |
| 2.E | Zero-line diff to every test file that existed at the base | section 0.1 command at commit 2, printing nothing |
| 2.F | `tsc -b` clean, no `any`, no `@ts-expect-error` at any call site | `npm run build` and `tsc -b` output; a grep for `ts-expect-error` in the five files returns nothing |

### 2.7 Risks

- **Someone tries to make the field parameter safe and quietly widens `SortField`.**
  Decision 2.2 records why that is off the table at this commit. `WorkersPage`'s
  test file is frozen here, and widening its column union is a behaviour change.
- **The single-cast shape gets "simplified" back to the two-cast if/return form
  during review.** Cosmetic either way, but the one-cast form is what the hazard
  comment is written against.

---

## 3. Item 3 - the schedules footer's row range

Backlog item: `bug-2026-08-14-schedules-footer-range-not-localized` (bug, low).

### 3.1 Verified current state, and two corrections to the item

The item's table is accurate on the facts that matter. At HEAD,
`web/src/schedules/SchedulesPage.tsx`'s footer renders
`SHOWING <span>{x}-{y} of {total}</span>` with no formatting and no zero-rows
branch, while the other six build a `rangeText` of exactly this shape:

```
rows.length === 0
  ? `0 of ${total.toLocaleString()}`
  : `${x.toLocaleString()}-${y.toLocaleString()} of ${total.toLocaleString()}`
```

Two corrections:

- **`StatSection` does not set an explicit locale.** The item offers, as its open
  question, moving the six to an explicit en-US locale "like `LogView.tsx:117`
  and `StatSection` do". `LogView.tsx:117` does. `StatSection.tsx` does not - its
  value cell calls `toLocaleString()` with no argument. Only its *test name* says
  en-US, and its assertion is a literal `'1,234'`. So the precedent for an
  explicit locale in this codebase is one call site, not two, and `StatSection`
  is in fact a second instance of the same latent fragility rather than the
  remedy for it.
- **`web/src/lib/time.ts`'s caveat is about a datetime SHAPE, not about a
  thousands separator.** The comment on `formatDateTime` rejects `Intl` because
  its output shape varies by locale, which makes the whole rendered string
  unassertable. Digit grouping is a far narrower surface: the shape is fixed and
  only the separator moves. The caveat is real and it is weaker evidence for the
  explicit-locale option than the item implies.

And one fact the item does not have, which is the strongest single input to the
decision: **the suite already depends on the runner's locale.**
`web/src/jobs/JobsPage.test.tsx` asserts against `/1-50 of 2,341/i` and
`/51-100 of 2,341/i` at six places, against a bare `toLocaleString()`, and
`StatSection.test.tsx` asserts `'1,234'` twice. Those eight assertions go red
today on a runner whose ICU locale groups differently. That is a pre-existing
condition of the suite, not something item 3 creates or removes.

### 3.2 Decision - the one-file change. `SchedulesPage` joins the six as they are.

Options: (a) `SchedulesPage` adopts the six's exact shape, bare
`toLocaleString()` plus the zero-rows branch - one source file changes;
(b) all seven move to an explicit en-US locale - seven source files change, plus
an assertion in up to six test files, five of which are in the frozen twelve.

Chosen: (a).

The reasons, in order of weight:

1. **The item's own instruction settles it.** "If the fix is applied, it should
   match what the six siblings already do rather than invent a third convention."
   Option (b) invents the third convention, in the same breath as fixing a bug
   whose entire content is that one surface deviates from six.
2. **Option (b) is a product regression, and the item never weighs it.** The item
   frames the explicit locale purely as CI determinism. But a bare
   `toLocaleString()` on a user-facing row count is the *correct* product
   behaviour: a reader in a locale that groups with a space or a period currently
   sees their own grouping, and option (b) would show them a US comma. Trading a
   real user-visible regression for test determinism is a decision that deserves
   its own item, not a parenthesis inside a one-line consistency fix.
3. **Option (b)'s premise is partly false.** See 3.1: `StatSection` is not the
   precedent the item thinks it is.
4. **Option (b) unfreezes five more files.** It would need a new assertion in
   `WorkersPage.test.tsx`, `UsersTab.test.tsx`, `EnrollmentsTab.test.tsx`,
   `InvitesTab.test.tsx` and `ReservationsTab.test.tsx` to satisfy its own "each
   has an assertion" criterion, since none of them currently asserts a
   four-digit total. That turns a chain whose unfreeze story is "one file at
   commit 3, two at commit 4" into one where six of twelve files are open, which
   costs the whole chain its clean evidence.

The determinism question is real and is **filed rather than dropped**, as a
follow-up in section 5, scoped to all eight existing locale-dependent assertion
sites at once so it is decided in one place.

### 3.3 What changes, by symbol

`web/src/schedules/SchedulesPage.tsx`:

- Add a `rangeText` const beside the existing
  `const { x, y } = computePageRange(pager.startOffset, schedules.length)`,
  identical in shape to the six siblings' (a `schedules.length === 0` ternary,
  `0 of ${total.toLocaleString()}` and
  `${x.toLocaleString()}-${y.toLocaleString()} of ${total.toLocaleString()}`).
- The footer's `<span className="text-fg">{x}-{y} of {total}</span>` becomes
  `<span className="text-fg">{rangeText}</span>`.

Nothing else. `total` at this surface is `data?.total ?? schedules.length`, which
differs from the six (they use `?? 0`); that difference is deliberate elsewhere in
the file (the ENABLED/PAUSED strip reads the same `total`) and is **not** in
scope. Do not harmonize it.

Note for the implementer: the footer IS rendered in the empty case.
`SchedulesTable` returns the "No schedules yet." panel followed by the footer
node when `schedules.length === 0`, so today a schedules page with zero rows
renders `0-0 of N` (`computePageRange` returns zeros for an empty page). After the
change it renders `0 of N`. That is a real rendered-text change and it is the
reason the zero-rows branch is in scope rather than a nicety.

### 3.4 Gate for item 3

The frozen set is unfrozen for exactly one file,
`web/src/schedules/SchedulesPage.test.tsx`, and only at this commit. Every other
test file that existed at the merge base stays frozen for the rest of the branch.

Evidence: the section 0.1 command run at commit 3, printing exactly one line, for
`web/src/schedules/SchedulesPage.test.tsx`.

The three existing footer tests in that file - `pagination footer shows 1-N of
total on the first full page`, `pagination footer shows correct absolute range on
partial last page after paging forward`, and `pagination footer restores prior
range when paging back` - assert against totals of 120 and 63. Both are under
four digits, so they render identically before and after and **must not be
edited**. If any of them needs an edit, that is the finding: it means the change
is not the formatting change it claims to be. The numstat line for that file must
therefore show insertions only.

### 3.5 Tests

Two new tests appended to `web/src/schedules/SchedulesPage.test.tsx`, beside the
three existing footer tests. They go there rather than in a new file because that
is where the footer's coverage already lives, and because item 3's licence to
edit that one file is the whole reason it is sequenced third.

1. **`the footer thousands-separates a four-digit total`** - one page of 50 rows
   with a total of 2341, asserting the literal string `1-50 of 2,341`. Never
   `(2341).toLocaleString()`, which on a runner with no group separator would
   compare the component's output against the same call and pass vacuously - the
   exact failure mode `StatSection.test.tsx` documents above its own literal.
2. **`the footer renders a bare count and no range when the page has no rows`** -
   an empty page with a total of 1234, asserting the literal string `0 of 1,234`.

Test 2 discriminates on both axes at once, which is why the total is 1234 and not
0: `0-0 of 1234` (today), `0-0 of 1,234` (localized but no zero-rows branch) and
`0 of 1234` (zero-rows branch but not localized) are all distinct from
`0 of 1,234`, so a half-applied fix cannot pass. A total of 0 would leave the
localization half unpinned. State that reasoning above the test, since 1234 looks
arbitrary.

Both proven RED against HEAD before the source change.

### 3.6 Acceptance criteria for item 3, each mapped to a named test

| # | Criterion | Pinned by |
|---|---|---|
| 3.A | The schedules footer formats its range like the six siblings | `SchedulesPage.test.tsx` -> `the footer thousands-separates a four-digit total` |
| 3.B | The zero-rows branch renders a bare count, like the six | `SchedulesPage.test.tsx` -> `the footer renders a bare count and no range when the page has no rows` |
| 3.C | The assertion is a literal, not a `toLocaleString()` round trip | review of the two new tests; `toLocaleString` appears nowhere in `SchedulesPage.test.tsx` |
| 3.D | No other surface's rendered text changes | the change set is one source file; section 0.1 command at commit 3 shows one modified test file |
| 3.E | The three existing footer tests are untouched | the numstat line for `SchedulesPage.test.tsx` shows insertions and zero deletions |

### 3.7 Risks

- **The two new tests are locale-dependent by construction.** They are, and so are
  the eight assertions that already ship. Accepted deliberately, with the
  determinism question filed as its own item rather than answered here. Say this
  out loud in the commit message so nobody later reads the new tests as an
  endorsement.
- **A reviewer asks for the explicit-locale option.** Decision 3.2 records the
  four reasons, including the one the item does not contain. Point at it rather
  than re-arguing.

---

## 4. Item 4 - three stale citations become symbol or phrase references

Backlog item: `bug-2026-08-14-stale-citations-in-gate-frozen-test-files` (bug, low).

### 4.1 All three re-verified stale at HEAD, and the item's proposed symbol is wrong

Re-verified by opening both source files and both test files:

| Citation | Says | At HEAD |
|---|---|---|
| `ReservationsTab.test.tsx:137` | the phrase "tasks already running on them are unaffected" is at `ReservationsTab.tsx:45` | the phrase is at `:46`; `:45` is a bare closing brace. **Still stale.** |
| `ReservationsTab.test.tsx:139` | the tab's own footnote is at `ReservationsTab.tsx:253` | the file is 243 lines, so the citation runs past EOF; the footnote's text begins at `:223`. **Still stale.** |
| `EnrollmentsTab.test.tsx:263` | the reset-before-reopen convention is at `UsersTab.tsx:238-245` | that convention is `create.reset()` at `UsersTab.tsx:207` (the `+ Create user` toggle) and `:221` (the form's Cancel). **Still stale.** |

**Refutation: the item prescribes citing `deleteWarning()`, and no such symbol
exists.** The function that builds the dialog body in `ReservationsTab.tsx` is
named `confirmDeleteBody`. Writing `deleteWarning()` into the comment would
replace a stale line number with a symbol name that resolves to nothing, which is
strictly worse - a line number at least used to be true. This is
`reference_accurate_item_wrong_remedy` in its plainest form: the diagnosis is
correct three times over and the prescribed fix names a symbol that is not there.

**Second refutation, smaller:** the item says the enrollments citation's target is
"the reset-before-reopen convention in `UsersTab`", but `UsersTab` has *two*
distinct reset conventions and they are cited separately elsewhere in the tree.
`ReservationsTab.tsx` already cites the other one ("the reset()-before-open
convention from UsersTab.tsx (resetPassword.reset() before setResetting)"). The
enrollments test is about reopening the create PANEL, so its target is
`create.reset()`, and the replacement must name `create.reset()` explicitly or it
points at the wrong one of two.

### 4.2 What changes, by symbol

Comment-only diffs to two test files. No assertion, no fixture, no import moves.

- `web/src/admin/reservations/ReservationsTab.test.tsx`, the positive-control
  comment: replace "a phrase that exists ONLY in the dialog body
  (`ReservationsTab.tsx:45`)" with a reference to `confirmDeleteBody`'s ACTIVE
  branch in `ReservationsTab.tsx`. Keep the reasoning intact - it is load-bearing
  and it is why the control is this phrase: the previous control
  (`/general dispatch pool/i`) also matched the tab's own footnote and therefore
  stayed green under exactly the scope error it existed to catch.
- Same file, next line: replace "the tab's own footnote at
  `ReservationsTab.tsx:253`" with "the tab's own explanatory footnote in
  `ReservationsTab.tsx`". There is exactly one such footnote in that file and the
  phrase identifies it; no line number, no count.
- `web/src/admin/enrollments/EnrollmentsTab.test.tsx`, the reopen comment:
  replace "the reset()-before-reopen convention from `UsersTab.tsx:238-245`" with
  "the `create.reset()`-before-reopen convention in `UsersTab`".

Each replacement must still let a reader check the reasoning above it without
opening a search engine. That is the item's second acceptance criterion and it is
the reason these are phrase references rather than bare deletions.

**Not in scope, deliberately.** `EnrollmentsTab.test.tsx` carries a fourth
line-number citation, `internal/api/pagination.go:272-286`. It was checked and
**is accurate at HEAD** (the sort-mismatch 400 is at `pagination.go:272-286`), so
it is not an instance of this bug. It is an instance of a broader shape - a
cross-language line citation, which the comment policy names separately - and it
is one of many. Folding it in would widen a three-citation fix into a sweep with
no boundary. Proposed as its own item in section 5.

### 4.3 Decision - include the conventions-doc note, in `web/CLAUDE.md`

The item calls this optional. Options: (a) no note; (b) a paragraph in
`web/CLAUDE.md`; (c) a bullet in the root `CLAUDE.md` "Comments" list.

Chosen: (b), plus a follow-up proposal for (c).

`web/CLAUDE.md` exists and is the frontend conventions doc; it currently carries
only the Tailwind-scanner rule. A one-paragraph note there is cheap, in-lane, and
lands where anyone working in `web/` will read it.

(c) is where the rule ultimately belongs, because it is not frontend-specific -
the root `CLAUDE.md` "Comments" section already forbids dates, counts, uniqueness
claims and censuses, and a cross-file line citation is the same family of claim:
one that goes stale on somebody else's diff and reddens nothing when it does. But
promoting it there is a change to the project's instruction file with repo-wide
consequences, and it pairs naturally with the sweep of surviving citations. Both
go in one follow-up item rather than riding along in a comment-only commit whose
acceptance criterion is that it changes nothing else.

The paragraph states the rule and its reason, with no counts of instances and no
session narrative: prefer a symbol or a phrase to a file-and-line citation,
because a line number is invalidated by an unrelated diff, no test covers it and
no compiler checks it, while a symbol name cannot drift.

### 4.4 Gate for item 4

The frozen set is unfrozen for exactly two files,
`web/src/admin/reservations/ReservationsTab.test.tsx` and
`web/src/admin/enrollments/EnrollmentsTab.test.tsx`, and only at this commit, and
only for comment lines.

Evidence, two parts:

- the section 0.1 command at commit 4, printing exactly two lines;
- `git diff <commit 3> <commit 4> -- web/src` reviewed line by line, showing only
  comment lines changed. Every changed line begins with `//` or sits inside a
  comment block. The verification report states this explicitly, because "the
  suite is green" cannot distinguish a comment edit from an assertion edit.

### 4.5 Acceptance criteria for item 4, each mapped to evidence

| # | Criterion | Pinned by |
|---|---|---|
| 4.A | None of the three citations names a line number | a grep of the two files for the three old citations returns nothing |
| 4.B | Each replacement resolves. `confirmDeleteBody` exists in `ReservationsTab.tsx`; `create.reset()` exists in `UsersTab.tsx` | a grep for both symbols; this is the criterion the item's own `deleteWarning()` would have failed |
| 4.C | The two test files are otherwise unchanged | the comment-only diff review in 4.4 |
| 4.D | No other test file that existed at the base is modified at this commit | section 0.1 command at commit 4 |
| 4.E | Full web suite green, unchanged test count | `npm test` before and after commit 4 report the same totals |

### 4.6 Risks

- **A replacement phrase is itself a uniqueness claim.** "the tab's own
  explanatory footnote" asserts there is one. There is, at HEAD, and it is in the
  same file the reader already has open, which is the difference between a
  checkable phrase and a claim about the complement. Keep the phrase inside the
  file it points at; do not write "the only footnote in `web/src`".
- **Rewriting prose regenerates claims** (`reference_correcting_a_uniqueness_claim`).
  Each of the three new comments is a fresh assertion and must be verified as if
  new, not merely checked as a faithful edit of the old one.

---

## 5. Backlog items this closes, and follow-ups to propose

### 5.1 Backlog items this closes

All four, via `/backlog close <fragment>` at the end of the branch, never by
hand-editing `status:`. The `git mv` into `docs/backlog/closed/` is required
scope, and the close commit must name **both** paths of the rename, or the
deletion stays staged and the item ends up in both directories.

- `idea-2026-08-14-cursor-pager-next-takes-the-page`
- `idea-2026-08-14-toggle-sort-generic`
- `bug-2026-08-14-schedules-footer-range-not-localized`
- `bug-2026-08-14-stale-citations-in-gate-frozen-test-files`

Resolutions that must record something beyond "done":

- Item 1: that `next_cursor` was made **required**, refuting the item's own
  sketch, and why (a renamed field is a compile error rather than a silently dead
  next button); and the M1 and M2 mutation results, including a survival.
- Item 2: that `WorkersPage`'s field union is `SortField` in
  `WorkersTable.tsx`, not a `WorkerSortField` in `api.ts`, and that this single
  exception is what ruled out the template-literal generic; and that the item's
  "five casts become one" is one only in the single-return shape.
- Item 3: that the one-file option was taken over the seven-file explicit-locale
  one, with the product-regression argument the item does not contain, and that
  `StatSection` was found not to set an explicit locale.
- Item 4: that the item's prescribed symbol `deleteWarning()` does not exist and
  the real one is `confirmDeleteBody`; and that the note went into
  `web/CLAUDE.md`.

### 5.2 Proposed follow-up backlog items

Proposals only. The human gives final accept; nothing here is filed by this spec.

| Proposal | Type / priority | Why |
|---|---|---|
| `bug-2026-09-01-row-count-formatting-is-runner-locale-dependent` | bug / low | Eight shipped assertions compare a literal grouped string against a bare `toLocaleString()`: `JobsPage.test.tsx` six times against `/1-50 of 2,341/i` and `/51-100 of 2,341/i`, and `StatSection.test.tsx` twice against `'1,234'`. `StatSection.tsx` passes no locale while its own test's NAME claims en-US, and `LogView.tsx:117` is the codebase's only explicit en-US call. Decide once, for all of them, whether user-facing counts follow the reader's locale (and the tests stop asserting separators) or the app's (and every site passes an explicit locale). Item 3 deliberately deferred this and added two more locale-dependent assertions; the item should say so. |
| `bug-2026-09-01-line-number-citations-across-web-src-and-into-other-languages` | bug / low | The class item 4 fixes for three citations survives widely. A grep of `web/src` for `//` comments containing a file-and-line reference filled a 60-result limit, so the population is at least 60 comment LINES matching that one regex - an axis that does not see block comments or citations without a file extension, and does not distinguish a line from a distinct citation. It includes cross-language citations the comment policy names separately (`internal/api/pagination.go:272-286` in three tab files, `internal/scheduler/dispatch.go:185-191, :221-223` in `ReservationsTab.tsx`, `internal/api/users.go:69-76` in `admin/users/api.ts`) and citations into `node_modules` (`query-core mutationObserver.js:85-95` and `mutation.js:123 vs :144` in `InvitesTab.tsx`, several jsdom and user-event internals in `UserMenu.test.tsx`). Spot-checked: `pagination.go:272-286` is still ACCURATE, so this is a durability item, not a correctness one. Should also carry the promotion of the rule into the root `CLAUDE.md` "Comments" list, per Decision 4.3. |
| `idea-2026-09-01-mutation-sweep-the-pager-wirings-nobody-has-asked-about` | idea / low | The 2026-08-14 retro's standing Known Limitation: the gate-versus-load-bearing question has been asked of exactly the wirings that slice touched. The sort-header plumbing, the `isPlaceholderData` disabling of prev and next, and the `EnrollmentsTab` and `ReservationsTab` empty-state prev hatches have never been mutated. Item 2 gives the sort-header plumbing its first shared implementation, which makes it the natural next subject. |
| `idea-2026-09-01-relate-the-sort-field-to-the-sort-union-in-toggleSort` | idea / low | Decision 2.2 leaves the field parameter as a plain `string`, so a typo produces a value outside the union. The clean fix is a template-literal generic relating the field to the sort union, blocked today because `WorkerSort` carries `created_at` while `SortField` (the column header union) does not. Closing it means deciding whether `WorkersPage`'s default sort belongs in the header union, which is a behaviour question about a surface whose test file was frozen for this chain. |

---

## 6. Escalations - what would have gone to a human

Recorded per the autonomous-mode convention. Each was decided; each is a place a
human might reasonably call it the other way.

1. **Decision 3.2, the one-file versus seven-file footer fix.** The item itself
   says the seven-file option is "arguably the better fix; decide before
   writing". **Called:** one file, on four grounds, of which the product
   regression for non-US readers is the one the item does not contain. A human
   who weights CI determinism above per-reader formatting would call it the other
   way, and the follow-up item exists so that call remains available.
2. **Decision 1.6, deleting the hook's header census inside a behaviour-preserving
   refactor.** **Called:** delete, because item 1 rewrites the paragraph anyway
   and the comment policy names both shapes explicitly. A stricter reading of
   "refactor, nothing else" would push it to the follow-up sweep. The cost of
   being wrong is about ten lines of comment diff in a file with no gate on it.
3. **Decision 4.3, `web/CLAUDE.md` versus the root `CLAUDE.md`.** **Called:**
   `web/CLAUDE.md` now, root later with the sweep. A human might prefer the root
   bullet immediately on the grounds that the rule is language-independent and
   the frontend doc is the wrong home for it.
4. **Whether items 1 and 2 should be one commit.** **Called:** two, per the lane
   brief, and independently right: each has its own gate evidence and its own
   backlog item, and a combined commit makes the section 0.1 per-commit table
   unreadable.
5. **Whether the chain's membership or order should change.** **Called:** no.
   Item 3 could technically land first, but that would move the frozen set's
   baseline for items 1 and 2 off the merge base, which is exactly the "gate
   result with a footnote" the 2026-08-14 slice was built to prevent.
