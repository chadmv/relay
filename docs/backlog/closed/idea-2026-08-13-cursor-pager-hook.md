---
title: The cursor-pager block is duplicated verbatim in seven list surfaces
type: idea
status: closed
created: 2026-08-13
priority: medium
closed: 2026-08-14
resolution: fixed
source: recorded deviation from the extract-before-the-third-consumer rule in the 2026-08-13-admin-invites-tab slice
---

# The cursor-pager block is duplicated verbatim in seven list surfaces

## Summary

Every paginated list in the SPA carries its own copy of the same state quartet plus the same three
functions plus the same footer call:

```tsx
const [stack, setStack] = useState<string[]>([])
const [startOffset, setStartOffset] = useState(0)
const [offsets, setOffsets] = useState<number[]>([])
const cursor = stack[stack.length - 1] ?? ''
// ... next(): push data.next_cursor, push startOffset, advance by currentPageSize
// ... prev(): pop both
// ... resetPaging(): clear all three, because the server 400s a cursor issued under
//     another sort (internal/api/pagination.go:272-286)
const { x, y } = computePageRange(startOffset, rows.length)
```

**Seven live copies**, verified at HEAD:

| File | Pager | Local `toggleSort` |
|---|---|---|
| `web/src/jobs/JobsPage.tsx:29-34, 68-69, 108` | yes | no |
| `web/src/workers/WorkersPage.tsx` | yes | yes (`:22`) |
| `web/src/schedules/SchedulesPage.tsx:29-35, 64-65, 122` | yes, **renamed** | no |
| `web/src/admin/users/UsersTab.tsx:38-40, 95-96, 132` | yes | yes (`:17`) |
| `web/src/admin/enrollments/EnrollmentsTab.tsx:29-31, 60-61, 84` | yes | yes (`:16`) |
| `web/src/admin/reservations/ReservationsTab.tsx:54-56, 90-91, 118` | yes | yes (`:21`) |
| `web/src/admin/invites/InvitesTab.tsx:35-37, 66-67, 93` | yes | yes (`:22`) |

`toggleSort` - "clicking the active column flips its direction, clicking another selects it
ascending" - is duplicated in **five** of the seven. (The spec and plan for the invites slice both
say "four of those"; that was true when they were written and stopped being true when the slice they
described landed.)

`SchedulesPage` is already a **naming variant**: it calls the stack `cursorStack` and the functions
`goNext` / `goPrev` (`:30, :56-72`). The bodies are the same; only the identifiers differ. That
matters for the extraction, because a mechanical find-and-replace will not find it.

## Context

**The house rule is extract before the third consumer. This is the seventh.** Every one of the last
four was a deliberate, recorded deferral rather than an oversight - the invites plan flagged it in an
"Extraction debt this slice knowingly adds" section before the task that wrote the seventh copy, and
the same reasoning is recorded in the spec's Scoped-out table. The reason was consistent each time:
the extraction has to migrate every shipped surface at once, which is a behavior-preserving refactor
with its own risk profile, and folding it into a feature slice would put the feature behind an
unrelated refactor.

**There is no open backlog item for it.** This is the first. Glob of `docs/backlog/` for
pager/pagination/cursor returns only `bug-2026-08-13-cursor-value-kind-not-validated`, which is an
endpoint-validation item about the server's cursor decoding and is unrelated to this.

**Priority is medium rather than low**, unlike the sibling `idea-2026-08-12-detail-page-state-triad-primitive`
(three copies, low). Seven copies of stateful pagination logic is a different thing from three copies
of a render early-return: a pagination bug found in one copy has to be fixed in seven places, and the
project has already shipped one pagination footer bug
(`bug-2026-06-21-jobs-pagination-footer-absolute-range`, closed) whose fix would today need to be
applied seven times.

The honest counter-argument, recorded so it does not have to be re-argued: **at seven copies, the
duplication is arguably the codebase's convention rather than a deferral.** If an eighth list surface
ships before this lands, that argument wins and this item should be closed as rejected rather than
left open to accumulate a ninth.

## The two constraints that make this safe, and neither is optional

### 1. The gate is a zero-line diff to all seven existing test files

This is the substance of the item, not the extraction. The extraction itself is an afternoon.

This repo has a hard-won rule about behavior-preserving refactors: require a **zero-line diff to the
existing test files** (`reference_refactor_gate_byte_identical_tests`). An assertion that needs
adjusting during a refactor that is supposed to change nothing **is the finding** - either the
refactor changed behaviour, or the test was green because of a defect the refactor removed. The
tempting move mid-migration is to "fix up" a selector or a text match and keep going. Any plan that
picks this up must state the gate before the first task, because by the time the temptation arrives
it is three files deep.

The seven test files are:

- `web/src/jobs/JobsPage.test.tsx`
- `web/src/workers/WorkersPage.test.tsx`
- `web/src/schedules/SchedulesPage.test.tsx`
- `web/src/admin/users/UsersTab.test.tsx`
- `web/src/admin/enrollments/EnrollmentsTab.test.tsx`
- `web/src/admin/reservations/ReservationsTab.test.tsx`
- `web/src/admin/invites/InvitesTab.test.tsx`

Confirm the exact file set at spec time; some surfaces carry more than one test file (for example a
separate transition or secrecy suite) and those count too if they exercise paging.

### 2. `statusTone` must stay per-module. Do not fold it in.

The seven surfaces look alike, and two of them differ in a way a naive merge would silently destroy:

- `web/src/admin/invites/inviteStatus.ts:59-63` maps **EXPIRED to `err`**.
- `web/src/admin/enrollments/enrollmentStatus.ts:29-33` maps **EXPIRED to `muted`**.

That is deliberate, not drift. The invites tab has four states and needs four tones, and the hi-fi
encodes expired and redeemed differently on purpose (`hifi3-holo-pages.jsx:2101` uses `C.err` for
expired and `C.fgMute` for redeemed). The `err` tone was added to `Chip` in the invites slice
specifically so that EXPIRED and REDEEMED would not collapse into one appearance. The enrollments tab
has three states and no redeemed state at all, so `muted` for expired is right there.

A refactor that "harmonizes the status pills while it is in there" would flatten a deliberate design
difference and no test would necessarily catch it, because each module's own tone test would simply
be rewritten to match the merged behaviour. **`statusTone` and the status modules are out of scope.
The pager is the pager.**

More generally: **do not fold in the surfaces' other shared shapes.** Sort-header wiring, the
`isPlaceholderData` disabling of the prev/next buttons, the footer's composite span, and the control
row are all near-identical too. Each is a separate question with its own set of variations, and
extracting them in the same change turns a mechanical refactor into a design exercise and defeats the
zero-diff gate.

## Proposal

A hook, working name `useCursorPager`, in `web/src/lib/` or `web/src/components/` - **not** in any
feature module.

It owns the three pieces of state and returns `{cursor, startOffset, next, prev, resetPaging}`, plus
whatever the footer needs to call `computePageRange`. Points to settle at spec time:

- **What `next` takes.** Today each copy reads `data.next_cursor` and `rows.length` off its own
  query. The hook can take them as arguments to `next(nextCursor, pageSize)` and stay ignorant of
  TanStack, or take the query result. The former is the lower-risk starting point.
- **Whether `resetPaging` is called by the consumer or triggered by a `sort` argument.** Today every
  copy calls it explicitly from its sort handler. Making the hook watch a `sort` key is tidier and
  changes the control flow in seven shipped files, which raises the risk against the zero-diff gate.
  Explicit is the lower-risk starting point.
- **Whether `toggleSort` comes along.** It is a pure function with no state and five copies, so it is
  the easy half - but its type is per-module (`UserSort`, `InviteSort`, ...), so a shared version
  needs a generic. Doing it in the same change is defensible; doing it in a **separate** change is
  safer, because the pager migration is where the risk lives.
- **`SchedulesPage`'s renamed identifiers.** It must be migrated too, and it will not turn up in a
  grep for `stack`. A partial migration that leaves it behind produces an eighth **variant**, which is
  strictly worse than seven copies.

**Migrate all seven in the same change.** This is the single most important instruction in the item.

## The smaller, separable half: `formatExpiryLabel` and `EXPIRING_WINDOW_MS`

Independent of the pager, and worth doing first because it is a fraction of the size and has no
behavioural surface at all.

`formatExpiryLabel` (collapse any sub-minute label to `in <1m`, because the row's `now` ticks once a
minute and a seconds-precision label promises a freshness the refresh cadence does not deliver) and
the 1h `EXPIRING_WINDOW_MS` constant are duplicated **verbatim, reasoning comment included**, between:

- `web/src/admin/enrollments/enrollmentStatus.ts:5, 45-48`
- `web/src/admin/invites/inviteStatus.ts:10, 77-82`

Two consumers, so the extract-before-the-third rule has not fired yet. `inviteStatus.ts` already
names `web/src/lib/expiry.ts` as the destination in a comment, so consumer three does not have to
rediscover it. **A third status module is the trigger.**

Note the window is a documented contract, not an invented threshold: `README.md:1300-1303` documents
the 1h expiring window for both surfaces. Extracting it into one constant is therefore strictly
correct - the risk is only that a shared constant reads as tunable when it is actually pinned to
documented behaviour, so the comment must move with it.

**Do these as two changes, not one.** The expiry half touches two pure functions and their two unit
test files. The pager half touches seven stateful components. Bundling them means the zero-diff gate
has to hold across nine test files at once, and a failure in either half blocks both.

## Acceptance / Done When

- One hook owns the cursor stack, the offset stack and the page range; **all seven** surfaces use it
  and none contains a copy.
- **All seven existing test files have a zero-line diff.** If any assertion needs adjustment, stop and
  investigate rather than adjusting - that is the finding.
- The hook has direct tests: first page, forward walk, backward walk, a partial last page, and
  `resetPaging` clearing all three pieces of state.
- `statusTone` in `inviteStatus.ts` and `enrollmentStatus.ts` is **unchanged**, and EXPIRED still maps
  to `err` in invites and `muted` in enrollments. Assert this explicitly at review; it is the one
  difference a well-meaning harmonization would erase.
- `SchedulesPage` is migrated and no renamed variant survives anywhere.
- The extraction-debt comments naming this item are removed from the files that carry them.
- (Separable, optional in the same project) `formatExpiryLabel` and `EXPIRING_WINDOW_MS` live in
  `web/src/lib/expiry.ts` with their reasoning comments, and the comment in `inviteStatus.ts` naming
  that destination is removed.

## Related

- Source: the seven files in the table above
- Same shape, same gate, different primitive:
  [[idea-2026-08-12-detail-page-state-triad-primitive]] - **check before starting**; both are
  behavior-preserving refactors gated on a zero test diff, and running them concurrently would make
  either failure impossible to attribute
- Design record: `docs/superpowers/specs/2026-08-13-admin-invites-tab.md` ("Reuse, and the extraction
  debt this slice adds"), `docs/superpowers/plans/2026-08-13-admin-invites-tab.md` ("Extraction debt
  this slice knowingly adds"), `docs/retros/2026-08-13-admin-invites-tab.md`
- Precedent for how far to take an extraction and where to stop: the shared accessible-table
  primitive that landed earlier in this workstream
- Adjacent frontend consistency items: [[idea-2026-08-09-table-visual-harmonization]],
  [[idea-2026-08-09-table-accessible-name-consistency]]
- Unrelated despite the name: [[bug-2026-08-13-cursor-value-kind-not-validated]] is about the
  server's cursor decoding, not this block

## Notes

The value of this item **drops to zero the moment somebody writes the eighth copy**, because at that
point the duplication is the codebase's convention rather than a deferral. Whoever adds an eighth
paginated list should read this item first and either extract or close it as rejected.

A lens confirmed during the invites slice that the seventh copy is character-for-character faithful to
its siblings. That is the only thing that makes shipping it defensible and it is also what makes this
extraction mechanical rather than a redesign. If a future copy drifts, the extraction gets
meaningfully harder, so the window for a cheap version of this work is now rather than later.

## Resolution

Shipped 2026-08-14 as `web/src/lib/useCursorPager.ts`. All seven surfaces migrated in one
change; no copy or renamed variant survives (`setStack`/`setOffsets`/`setStartOffset`/
`cursorStack` appear nowhere in `web/src` outside the hook).

**This item's central premise was false, and that is the most useful thing it leaves behind.**
It claimed all seven copies were verbatim and that `SchedulesPage` "differs only in
identifiers; the bodies are the same". Verification at HEAD found only **four** byte-identical
(the admin tabs). `SchedulesPage` was a genuinely different algorithm: three state pieces
rather than four, `cursor` derived from the stack top rather than stored, `goNext` pushing the
*destination* cursor where the canonical `next` pushes the *source*, and `goPrev` popping and
discarding. A mechanical rename would have produced wrong code, and a reviewer told "only
identifiers differ" would have passed it. `JobsPage` and `WorkersPage` had no `resetPaging` at
all. Equivalence was ultimately established by hand-walk and by a 3000-trial x 40-step
randomized differential simulation of both state machines, zero divergence. The item's own
summary code sketch was the `SchedulesPage` shape presented as the shared block.

**The gate held, and mutation proved it was not enough.** No pre-existing test file was
modified (`git diff --diff-filter=M` over `web/src` returns no test file). But six wirings were
unconstrained by any existing test: the `resetPaging` calls in `SchedulesPage.chooseSort`,
`JobsPage.pickSort`, `JobsPage.pickFilter` and `UsersTab.pickEmail`, plus the `pageSize`
argument at `WorkersPage` and `ReservationsTab` - each could be broken with the full suite
green. Closed by five new sibling `*.pager.test.tsx` files, so the existing seven kept a literal
zero-line diff. **A zero-diff gate proves you did not weaken the tests; it says nothing about
whether the tests constrained the thing you changed.**

Deliberate exclusions, both from acceptance criterion 12:

- **`toggleSort` did not come along.** Five copies over five per-module sort unions; a shared
  version needs a generic plus a cast decision at every call site, which is a type-level design
  question and not something to settle inside a refactor whose premise is that nothing changes.
  Filed as [[idea-2026-08-14-toggle-sort-generic]]. The `InvitesTab` comment carrying the debt
  was corrected in passing (it said FOURTH copy; it is the FIFTH).
- **The `formatExpiryLabel` / `EXPIRING_WINDOW_MS` half was excluded and gets no item.** Two
  consumers, so the extract-before-the-third rule has not fired, and `inviteStatus.ts:5-9`
  already names both the destination (`web/src/lib/expiry.ts`) and the trigger (a third status
  module) in source. A source comment at the point of use is a better carrier than a backlog
  entry nobody is reading when they add consumer three.

Suite 1102 -> 1116. Also filed: [[idea-2026-08-14-cursor-pager-next-takes-the-page]] (the hook
hides `stack`/`offsets` but takes `pageSize` as an unvalidatable `number` - the value both
closed footer-range bugs were about), [[bug-2026-08-14-stale-citations-in-gate-frozen-test-files]]
and [[bug-2026-08-14-schedules-footer-range-not-localized]].

Note for anyone following this item's own references: the footer-range bug cited at line 68 as
`bug-2026-06-21-jobs-pagination-footer-absolute-range` does not exist. The two real ones are
`bug-2026-06-05-jobs-pagination-footer-absolute-range` and
`bug-2026-06-21-schedules-pagination-footer-absolute-range`; the phantom citation had also
propagated into three shipped source files and no longer survives anywhere in `web/src`.
