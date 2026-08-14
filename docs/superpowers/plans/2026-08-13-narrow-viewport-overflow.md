# Narrow-viewport horizontal overflow - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every SPA surface satisfy `document.documentElement.scrollWidth <= clientWidth` at 375px and 320px, by fixing the three independent causes a real-browser per-element measurement pass identified - the header nav floor, the unconditional multi-column detail bodies, and the fixed-px table templates with no scroll container - plus the residual non-wrapping page header rows that the header floor was masking.

**Architecture:** Frontend only. Three structural changes and one primitive change. (1) `HoloShell`'s `<nav>` becomes the single shrinkable, horizontally scrollable element in the header, and `UserMenu`'s toggle becomes shrinkable and truncating - the header itself gains no `overflow`, because an `overflow` on the header would clip the dropdown that hangs out of it. (2) The four unconditional numeric grids get the `md:` breakpoint that `ServerTab` already uses. (3) The shared `Table` primitive gains one optional `minWidth` prop that publishes a shared min-width onto the header row and every body row *and* wraps the `role="table"` subtree in an `overflow-x-auto` div, so a table scrolls inside its own frame while footers and error banners - which live outside `Table` - stay put. (4) Five non-wrapping breadcrumb/toolbar rows gain `flex-wrap`.

**Tech Stack:** React 18.3, TypeScript 5.7, Tailwind v4 (arbitrary values must appear as literal strings for the static scan), react-router-dom v7, Vitest 2.1 + Testing Library 16 + jsdom 29 (**which performs no layout**), plus a real browser for the acceptance measurement.

**Backlog item this closes:** `docs/backlog/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md`. Close it with `/backlog close bug-2026-08-12-web-narrow-viewport-horizontal-overflow` **only after Task 7's browser numbers are recorded** - the item's acceptance is a measurement, and a partial fix must not close it.

---

## Slice independence declaration

- **Backend slice: NONE.** Zero Go files, zero `.sql` files, zero `.proto` files, zero migrations. No `make generate`, no `*.sql.go`, no `models.go`, no integration test. None of the six backend Invariants in CLAUDE.md is in play in the database sense; the "end the generation before releasing the resource" family is not touched either (no async lifecycle changes anywhere in this slice).
- **Frontend slice: ONE `relay-frontend-engineer`, SEQUENTIAL.** Task 4 depends on the prop introduced in Task 3. Tasks 1, 2 and 5 are independent of each other but all feed Task 7's single measurement pass, and every task edits `web/src`, so **do not fan this out to two engineers**.
- **Parallelism available to the conductor for Phase 3: none within this plan.** Unrelated work elsewhere in the repo can run alongside it.
- **`web/dist` will NOT be dirtied.** No task runs `npm run build`. Task 7 runs `npm run dev`, which writes nothing into `web/dist`. If `git status` ever shows `web/dist` modified, run `git checkout -- web/dist/` before assembling the PR (`feedback_web_dist_not_maintained`).
- **Concurrency warning carried from the item:** `idea-2026-08-12-detail-page-state-triad-primitive` touches the same three detail pages. It must not run concurrently with this slice.

---

## What this plan CORRECTS about the backlog item - read this first

The item has now been wrong twice about where the overflow comes from, in opposite directions, and both errors have the same root: **a page total was measured, and the cause was inferred from the source rather than observed.**

1. **The original framing** (2026-08-12) said the fix belonged in the shared `Table` primitive. Evidence: page totals on schedule detail and the Invites tab. No per-element width was taken.
2. **The 2026-08-13 amendment** said the driver was the HEADER, not the tables, and told the next implementer to start there. Evidence: one per-element measurement on `/profile/sessions`. That surface **renders no table at all**.
3. **The 2026-08-13 measurement pass** (whose numbers are the baseline below) took per-element widths on eleven surfaces and found **both readings incomplete**. The header sets a 494-523px floor on every shell page, and on every page with table rows present the table exceeds that floor (763 / 728 / 607 / 593). The header-only reading came from surfaces whose tables were **empty or not rendered**: the Invites tab in its empty state, `/workers` in its default Grid view, `/profile/*` which has no table.

**The transferable lesson: measure the populated state, not the convenient one.** An empty-state page and a page with rows are different layouts, and the empty state is the one you get for free on a fresh database. Both wrong attributions in this item's history came from a surface that happened to be empty.

A third cause the item never names is visible in the baseline: **`MAIN` alone exceeds 375px on pages with no table and no two-column body** (`/jobs/:id` 458, `/workers` Grid 401, `/admin/invites` empty 422). Those are non-wrapping breadcrumb and toolbar rows, currently hidden under the 523px header floor. Fixing only causes 0, 1 and 2 would move the failure from the header to `MAIN` and the acceptance criterion would still fail. Task 5 exists for them.

---

## The baseline - preserve this table

Measured in a real browser (Postgres + `relay-server` + Vite dev server) at a **375px viewport, `clientWidth` 375**. `docSW` = `document.documentElement.scrollWidth`. **This is the before-baseline every number in Task 7 is compared against, and it cost a full stack to obtain. Do not delete it from this document.**

| surface | docSW | widest element | header sW | main sW |
|---|---|---|---|---|
| `/auth` | 375 (no overflow) | none | n/a (no shell) | n/a |
| `/jobs` (rows present) | 763 | MAIN > JobsTable grid (`grid-cols-[90px_1fr_120px_150px_120px_70px_150px]`, 716-742) | 523 | 763 |
| `/jobs/:id` | 523 | HEADER | 523 | 458 |
| `/workers` (Grid view, default) | 523 | HEADER (no table rendered) | 523 | 401 |
| `/workers` (Table view) | 593 | MAIN (WorkersTable grid) | 523 | 593 |
| `/schedules` | 728 | MAIN (SchedulesTable, 9 cols) | 523 | 728 |
| `/schedules/:id` | 651 | MAIN (`grid-cols-2` body) | 494 | 651 |
| `/admin/users` | 607 | MAIN (UsersTable) | 523 | 607 |
| `/admin/invites` (empty state) | 523 | HEADER | 523 | 422 |
| `/profile/identity` | 523 | HEADER | 523 | 375 |
| `/profile/sessions` | 494-523 | HEADER | 494-523 | 375 |

Two further facts from that pass:

- **At 320px, `docSW` is unchanged from 375px** on every surface. The overflow is a content floor, not a proportional squeeze.
- **At 768px the table pages stop overflowing** (the `fr` tracks absorb it), **but `/schedules/:id` still overflows to 840px**, driven by its `grid-cols-2` body. Cause 1 therefore persists past 768px independently of the header, which is why the `md:` breakpoint (Task 2) is not optional cosmetic tidying.

**Surfaces the baseline does NOT cover, which Task 7 must add:** `/workers/:id` (it carries `grid-cols-4` *and* `grid-cols-2` *and* `WorkspacesPanel`, so it is probably the worst page in the app and nobody has measured it), `/admin/enrollments`, `/admin/reservations` (the second-widest fixed-track sum after Schedules), `/jobs/new`, `/profile/password`, and `/admin/invites` **with rows present** rather than in its empty state.

---

## Design decisions

### Decision 1: the header nav scrolls. THIS HAS NO HI-FI REFERENCE.

**Flag this prominently in the PR body.** The Holo hi-fi (`design_handoff_relay_holo/hifi3-holo-pages.jsx`) is **silent** on narrow viewports - the measurement pass found no breakpoint, no wrap, and no mobile-nav treatment anywhere in it. There is nothing to follow, so this is a call made without a design reference and **the human reviewing the PR should treat it as theirs to overrule.**

The options were: let the nav wrap onto a second line; give the nav its own horizontal scroll; collapse it into a hamburger/disclosure below a breakpoint.

**Chosen: the nav shrinks and scrolls horizontally.** Rationale, in the order that decided it:

1. **It changes nothing at any width where the content already fits.** `overflow-x-auto` on an element whose content fits renders no scrollbar and no visual difference. There is no breakpoint constant, so there is no width at which the design "switches".
2. **The header keeps its height at every width.** Wrapping adds a second row below ~500px, which changes the header's height and therefore `main`'s offset. Scrolling does not.
3. **No new interaction, no new state, no new a11y surface.** A hamburger is a disclosure with open/closed state, focus management, an Escape route, and an `aria-expanded` contract - a second `UserMenu`, essentially - plus a visible design change on every page. That is a feature, not a bug fix, and it is exactly what the item says it is not proposing ("Deliberately not proposed: a responsive redesign, a mobile navigation shell").
4. **It is three class-string edits and is reverted by deleting them.**

**The hazard this decision must not create, and the test that pins it:** the scroll container is the `<nav>`, **never the `<header>`**. `overflow-x-auto` on the header would establish a scroll container that clips the `UserMenu` dropdown, which deliberately hangs *out* of the header over `<main>` - a behaviour established by a 275-point hit test recorded in `HoloShell.tsx`'s header comment and depended on by the non-portalled dropdown. Task 1 ships an assertion that the header carries no `overflow-*` class at all, and Task 7 re-checks the dropdown paints over `<main>` at 375px.

**Accepted cost:** on Windows and Linux, a classic scrollbar will appear under the nav at widths where it actually scrolls. Hiding it (`[scrollbar-width:none]` plus a `::-webkit-scrollbar` variant) is deliberately **not** done here: a visible scrollbar is the affordance that tells the user the nav scrolls, and hiding it is a taste call that belongs to whoever owns the design, not to a bug fix.

### Decision 2: the table scroll container goes in the primitive, opt-in via `minWidth`

Chosen: **B1, the primitive**, with an opt-in prop - not B2's nine-plus per-consumer edits.

The item frames this as "one edit vs nine". That is the weakest argument for it. The decisive one is **alignment**:

A grid row whose template is `grid-cols-[90px_1fr_120px_...]` in a 375px container has *negative* free space, so the `1fr` track cannot take a share of it and falls back to its content-based minimum. The header row and the body rows are **separate grid containers**, so their content minimums differ ("NAME" versus a truncating link, whose min-content is 0) - the columns visibly desynchronize. The fix is a shared `min-width` that keeps the container wide enough for free space to stay non-negative, at which point `fr` resolves identically in both.

That shared min-width is **exactly the thing `Table` already exists to own**: its own header comment says the grid template "travels on a context so the header row and the body rows cannot be put out of agreement by hand." Option B2 requires each consumer to apply a min-width by hand in two places (the `headerClassName` and every `TableRow`'s className), which reintroduces the precise defect class the primitive was built to prevent. So `minWidth` travels on the same context string as `columns`.

**What the no-frame contract risk actually is.** The comment at the top of `Table.tsx` says the primitive "deliberately renders NO frame", so that consumers keep four visually different wrappers and so footers, error banners and dialogs stay inside the visual surface but outside the `role="table"` subtree. The added wrapper is a bare `<div className="overflow-x-auto">` - no border, no background, no padding, no radius, no shadow - so it is not a frame in the sense the comment means. The real risk is different and worth naming precisely: **a scroll container clips anything inside it that used to escape the table's box.** That risk was audited against all ten consumers, and it is empty today:

- Every footer, error banner and empty-state strip is a **sibling of `<Table>`, not a child** (`JobsTable`, `SchedulesTable`, `WorkspacesPanel`), so all of them stay outside the new wrapper and do not scroll.
- `WorkspacesPanel`'s `ConfirmDialog` is a sibling of `<Table>` **and** portals to a layer on `<body>`.
- No row cell renders an absolutely-positioned popover or dropdown. Row content is text, chips, links, buttons and one `Input` (the `UsersTable` rename field), all in normal flow.
- Focus rings are inset outlines; a focused control near the right edge scrolls into view rather than being clipped.

**That audit is the contract**, and Task 3 records it in the primitive's comment. If a future table puts a popover inside a row, the popover must portal - the same rule `DialogShell` already follows.

**Why opt-in rather than unconditional:** the min-width is a different number for each table (it is a function of that table's fixed tracks), so it cannot be a constant inside the primitive, and a scroll wrapper with no min-width would scroll a *misaligned* table. One prop carries both halves, and its absence keeps `Table.test.tsx`'s existing renders byte-identical.

**Keyboard access to the scroll region:** no `tabIndex={0}` is added to the wrapper. Chrome and Edge make scroll containers keyboard-focusable by default, most rows contain focusable controls that scroll into view when Tabbed to, and adding an unconditional tab stop plus a `region` landmark to ten tables is a larger accessibility change than the one being fixed. **This is a judgement call and it belongs in the retro's follow-up list**, not silently in the code.

### Decision 3: `md:` is the breakpoint, copied from the one site already doing it right

`web/src/admin/server/ServerTab.tsx` already renders `grid grid-cols-1 gap-4 md:grid-cols-2`. Every multi-column body in this slice matches that shape rather than inventing a second convention. `WorkerDetailPage`'s four-up KPI row becomes `grid-cols-2 md:grid-cols-4` (two-up, not one-up, at narrow: they are four short stat cards, and stacking them one per row pushes the page body a screen down for no gain).

### Decision 4: one slice, not a split

**Recommend shipping all three causes plus the residual rows in one slice.** The item's acceptance is a single measured predicate (`scrollWidth <= clientWidth` on every listed surface) and **no proper subset of these fixes satisfies it on even one page**: fix the tables only and the header floor still fails every page; fix the header only and `/jobs` still measures 763; fix both and `/jobs/:id` still measures 458 from its breadcrumb row. A split would ship two PRs that each leave the item open and the acceptance unmet, and the second PR would have to re-run the same expensive browser pass anyway. The slice is large in file count (17 files) but every edit is a class string or a one-line prop, and the risk is concentrated in exactly two places (the header's stacking behaviour, and the `Table` context change), both of which get their own guard test.

### Where the conventions are documented

The item requires "one documented convention for the two-column breakpoint, and one for the table scroll container". `web/` has no README, and this repo keeps conventions in code comments next to the thing they govern:

- **Table scroll container:** the header comment of `web/src/components/holo/Table.tsx` (Task 3), which is the file the next table author must read anyway, plus the enforcement test in Task 4.
- **Multi-column breakpoint:** a one-line comment at each of the four grid sites pointing at the rule, plus the enforcement test in Task 2, which fails on the *fifth* site the day someone adds it.

Both conventions are enforced by a test, which is stronger than either comment.

---

## File Structure

**Modified - `web/src` (17 files), no file created except two test files, no file deleted, no dependency change**

| File | Change | Task |
|---|---|---|
| `web/src/shell/HoloShell.tsx` | header gains `gap-3`; left group gains `min-w-0`; `<nav>` gains `min-w-0 overflow-x-auto`; comment | 1 |
| `web/src/shell/UserMenu.tsx` | container `div` and toggle gain `min-w-0`; email span gains `truncate` | 1 |
| `web/src/shell/HoloShell.test.tsx` | append 1 test | 1 |
| `web/src/shell/UserMenu.test.tsx` | append 1 test | 1 |
| `web/src/schedules/ScheduleDetailPage.tsx` | body grid gains `md:` | 2 |
| `web/src/workers/WorkerDetailPage.tsx` | KPI grid and body grid gain `md:` | 2 |
| `web/src/admin/server/StatSection.tsx` | cell grid gains `md:` | 2 |
| **Create** `web/src/components/holo/responsive.guard.test.ts` | two source-scanning guard tests | 2, 4 |
| `web/src/components/holo/Table.tsx` | new optional `minWidth` prop, shared grid string, scroll wrapper, comment | 3 |
| `web/src/components/holo/Table.test.tsx` | append 3 tests | 3 |
| `web/src/jobs/JobsTable.tsx`, `web/src/jobs/TasksTable.tsx`, `web/src/schedules/SchedulesTable.tsx`, `web/src/schedules/ScheduleRunsPanel.tsx`, `web/src/workers/WorkersTable.tsx`, `web/src/workers/WorkspacesPanel.tsx`, `web/src/admin/users/UsersTable.tsx`, `web/src/admin/invites/InvitesTable.tsx`, `web/src/admin/enrollments/EnrollmentsTable.tsx`, `web/src/admin/reservations/ReservationsTable.tsx` | each: a `MIN_W` constant beside `COLS`, and `minWidth={MIN_W}` on `<Table` | 4 |
| `web/src/jobs/JobDetailPage.tsx`, `web/src/workers/WorkerDetailPage.tsx`, `web/src/schedules/ScheduleDetailPage.tsx`, `web/src/jobs/JobsPage.tsx`, `web/src/workers/WorkersPage.tsx` | `flex-wrap` on a breadcrumb or toolbar row | 5 |

**Not touched, deliberately:** `web/src/jobs/TaskDag.tsx` (already `overflow-x-auto`, the precedent), `web/src/workers/RevokedWorkersTable.tsx` (a native `<table>`, not a `Table` consumer), `web/src/components/holo/GlassPanel.tsx`, `web/src/components/holo/Panel.tsx`, every `grid-cols-[repeat(auto-fill,minmax(...))]` grid (auto-fill already collapses to one column), `web/src/jobs/LogView.tsx`'s `grid-cols-[62px_1fr]` (62px + `1fr`, no fixed floor), and every `.sql`, `.go` or `.proto` file in the repo.

---

## Conventions for every task

- All `npm`/`npx` commands run from the `web/` directory of the worktree: `D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web`.
- Single file: `npx vitest run src/path/File.test.tsx`. Full suite: `npm test`.
- **House rule: never an em dash or en dash**, in code, comments, copy or this document. Plain ASCII hyphens only.
- The repo has **no ESLint and no Prettier**. Nothing reformats your JSX. Write the class strings exactly as shown, in the order shown; Tailwind class order is cosmetic but the review gate compares strings.
- **Tailwind v4 statically scans source for class literals.** Every `min-w-[NNNpx]` must appear as a literal string in the consumer file. That is why Task 4 puts each one in a `const MIN_W = 'min-w-[880px]'` beside `COLS`, and never builds it from a number.
- **Plan-supplied test bodies are guesses until run.** Where a task says "expected RED", a green run before the implementation means the test is wrong - fix the test, do not proceed. Where a task names a mutation, run it and **record both outputs in the task report**.
- Never edit a shipped assertion to make new code pass. If an existing test needs adjusting, that is a finding - stop and report it.

---

## Verification: what is real, what is a pin, and what only a browser can do

**Say this out loud in the PR body.** jsdom performs no layout: `offsetWidth`, `scrollWidth` and `getBoundingClientRect()` all return 0 there, and no Vitest assertion in this repo can observe an overflow. Therefore:

| Fix | jsdom test | What it actually proves | Real proof |
|---|---|---|---|
| Header nav scroll (Task 1) | class assertions on `<nav>` and the left group | **Regression pin only.** That the classes are present. | Task 7 |
| Header must NOT be the scroll container (Task 1) | `expect(header.className).not.toMatch(/overflow-/)` | **Real guard.** It is a structural rule with a known failure mode (clipping the dropdown), and it reddens for the tempting wrong implementation. | Task 7 re-checks paint order |
| `md:` on numeric grids (Task 2) | source-scan guard test | **Real guard.** It catches every site including ones not yet written, which no rendering test can do. | Task 7 |
| `Table` publishes one grid string to header and rows (Task 3) | structural test: header row and body row carry the **identical** class string | **Real.** Agreement between two elements is exactly what the test compares, and it is the property the layout depends on. | Task 7 |
| `Table` renders the scroll wrapper (Task 3) | structural test: the parent of `role="table"` carries `overflow-x-auto` | **Real** as structure; says nothing about whether it scrolls. | Task 7 |
| All ten consumers opt in (Task 4) | source-scan guard test | **Real guard**, and the only thing that will catch the eleventh table. | Task 7 |
| `flex-wrap` on 5 rows (Task 5) | **none** | - | Task 7 only |

**Task 5 deliberately ships no unit test.** A `flex-wrap` class assertion would require mounting a page with msw scaffolding to prove a string is present in a className, jsdom would never wrap anything, and it would add five brittle pins that go stale the moment the row is restyled. Its RED is the baseline number in the table above and its GREEN is the re-measurement in Task 7. That is honest TDD at the level the defect lives; a green class assertion here would be the vacuous kind.

---

## Task 1: The header floor - the nav shrinks and scrolls, the toggle truncates

Cause 0. This sets a 494-523px floor under **every** shell page, so no other fix can bring any page under 375px until it lands. `/auth`, which renders no shell, was the clean control that measured 375 with no overflow.

**Files:**
- Modify: `web/src/shell/HoloShell.tsx` (the `header`, its left group `div`, its `nav`)
- Modify: `web/src/shell/UserMenu.tsx` (the container `div`, the toggle `button`, the email `span`)
- Modify: `web/src/shell/HoloShell.test.tsx` (append 1 test)
- Modify: `web/src/shell/UserMenu.test.tsx` (append 1 test)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/shell/HoloShell.test.tsx`:

```tsx
// Cause 0 of docs/backlog/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md.
// A real browser measured HEADER at 523px against a 375px viewport on EVERY shell
// page, including two that render no table at all - the header, not the tables, is
// the floor every page inherits.
//
// jsdom does no layout, so the first three assertions are REGRESSION PINS, not
// proof: they pin the classes whose effect was measured in Chrome (Task 7 of
// docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md). Same honesty as
// the stacking test above, which can only pin z-10/z-0.
//
// The FOURTH assertion is not a pin, it is a real guard. The scroll container must
// be the <nav> and NEVER the <header>: an overflow on the header establishes a
// scroll container that CLIPS the UserMenu dropdown, which deliberately hangs out
// of the header over <main> - the behaviour established by the 275-point hit test
// recorded in HoloShell.tsx. "Just put overflow-x-auto on the header" is the
// tempting wrong fix, and this line is what reddens for it.
test('the nav is the only shrinkable scroll container in the header', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })

  const nav = screen.getByRole('navigation')
  expect(nav).toHaveClass('min-w-0', 'overflow-x-auto')
  // A flex item's automatic minimum size is its content, so the group holding the
  // wordmark and the nav cannot shrink at all without this.
  expect(nav.parentElement).toHaveClass('min-w-0')

  const header = screen.getByRole('banner')
  expect(header.className).not.toMatch(/\boverflow-/)
})
```

Append to `web/src/shell/UserMenu.test.tsx`:

```tsx
// The other half of the header floor: the toggle renders the full email, and as a
// flex item of the header its automatic minimum size is that text - so a long
// address sets a floor of its own no matter what the nav does. REGRESSION PIN
// (jsdom does no layout); the widths were measured in Chrome.
//
// truncate carries overflow:hidden, which is also what drops the SPAN's automatic
// minimum size to 0; the button needs min-w-0 explicitly because it has no
// overflow of its own.
test('the toggle can shrink and truncates a long email rather than setting a header floor', () => {
  render(
    <MemoryRouter>
      <UserMenu email="a-very-long-address-that-would-set-a-floor@studio.dev" onLogout={vi.fn()} />
    </MemoryRouter>,
  )
  const toggle = screen.getByRole('button', { name: /studio\.dev/i })
  expect(toggle).toHaveClass('min-w-0')
  expect(toggle.parentElement).toHaveClass('min-w-0')
  // Positive control: the email is still rendered in full as text, so this is a
  // truncation, not a substring the component silently dropped.
  const label = toggle.firstElementChild as HTMLElement
  expect(label).toHaveClass('truncate')
  expect(label).toHaveTextContent('a-very-long-address-that-would-set-a-floor@studio.dev')
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/shell/HoloShell.test.tsx src/shell/UserMenu.test.tsx`

Expected: **2 failing tests.**
- `the nav is the only shrinkable scroll container in the header` fails on the first assertion: the nav's className is `flex gap-0.5`, so `min-w-0` is absent. Note that its **fourth** assertion passes already - the header has no `overflow-` class today - which is correct: it is a guard against a regression this task could introduce, not a defect being fixed.
- `the toggle can shrink and truncates a long email...` fails on `expect(toggle).toHaveClass('min-w-0')`.

- [ ] **Step 3: Implement**

In `web/src/shell/HoloShell.tsx`, replace the `<header>` opening tag, the left group `<div>` and the `<nav>` opening tag. **Do not touch the comment block above the header** - it carries the 275-point hit-test measurements and is load-bearing.

```tsx
      {/* Narrow-viewport rule for this header (measured 2026-08-13): the <nav> is
          the ONLY element allowed to become a scroll container. The dropdown that
          UserMenu hangs below this header would be clipped by an overflow declared
          here, which is the same stacking behaviour the comment above measures. See
          docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md. */}
      <header className="relative z-10 flex items-center justify-between gap-3 border-b border-border bg-white/[0.025] px-[22px] py-3 backdrop-blur-[10px]">
        <div className="flex min-w-0 items-center gap-6">
          <Eyebrow className="text-accent">RELAY</Eyebrow>
          {/* min-w-0 lets this shrink below its content (a flex item's automatic
              minimum is its content width, which is what made the header a 523px
              floor); overflow-x-auto then makes every route reachable by scrolling.
              Inert at any width where the links fit: a scroll container with no
              overflow renders no scrollbar and no visual difference. */}
          <nav className="flex min-w-0 gap-0.5 overflow-x-auto">
```

`gap-3` on the header is inert at desktop - `justify-between` already leaves far more than 12px between the two groups - and only comes into play once both groups are shrinking.

In `web/src/shell/UserMenu.tsx`, change the container `div`, the toggle `button` and the email `span`. Everything else in the file, including every comment, is untouched.

```tsx
    <div ref={ref} className="relative min-w-0" onBlur={onContainerBlur}>
```

```tsx
        className={`flex min-w-0 items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}`}
      >
        <span className="truncate text-fg normal-case tracking-normal">{email}</span>
```

The dropdown panel is `absolute right-0 ... w-56`, so it is unaffected by the toggle shrinking, and neither the container nor the button gains an `overflow`, so the panel is not clipped.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/shell/HoloShell.test.tsx src/shell/UserMenu.test.tsx`

Expected: PASS. The four shipped `HoloShell` tests and all shipped `UserMenu` tests must pass **untouched**.

- [ ] **Step 5: Run the mutation and record the output**

**Mutation A - put the scroll container on the header instead of the nav.** Move `overflow-x-auto` from the `<nav>` className onto the `<header>` className.

Run: `npx vitest run src/shell/HoloShell.test.tsx`
Expected: FAIL in **`the nav is the only shrinkable scroll container in the header`**, at both `expect(nav).toHaveClass('min-w-0', 'overflow-x-auto')` and `expect(header.className).not.toMatch(/\boverflow-/)`. Record both lines. **Revert.**

Re-run and confirm green.

- [ ] **Step 6: Commit**

```bash
git add web/src/shell/HoloShell.tsx web/src/shell/UserMenu.tsx web/src/shell/HoloShell.test.tsx web/src/shell/UserMenu.test.tsx
git commit -m "fix(web): the header nav shrinks and scrolls instead of setting a 523px floor"
```

---

## Task 2: Multi-column bodies get the `md:` breakpoint, enforced by a source scan

Cause 1. Three `grid-cols-2` sites and one `grid-cols-4`, none with a breakpoint prefix. `/schedules/:id` still overflows to 840px at a **768px** viewport because of this, so it is not only a phone concern.

**Files:**
- Create: `web/src/components/holo/responsive.guard.test.ts`
- Modify: `web/src/schedules/ScheduleDetailPage.tsx` (the two-column body grid, immediately below the `actionError` block)
- Modify: `web/src/workers/WorkerDetailPage.tsx` (the KPI stat row grid, and the two-column body grid below it)
- Modify: `web/src/admin/server/StatSection.tsx` (the `KpiStat` cell grid inside `StatSection`)

- [ ] **Step 1: Write the failing guard test**

Create `web/src/components/holo/responsive.guard.test.ts`:

```ts
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from 'vitest'

// web/src/ - this file lives at web/src/components/holo/.
const SRC = fileURLToPath(new URL('../../', import.meta.url))

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      out.push(...sourceFiles(full))
      continue
    }
    // Shipped JSX only. Test files render deliberately unrealistic markup.
    if (!entry.endsWith('.tsx') || entry.endsWith('.test.tsx')) continue
    out.push(full)
  }
  return out
}

const FILES = sourceFiles(SRC)

// THE CONVENTION, enforced rather than merely written down: a numeric Tailwind
// column count must carry a breakpoint prefix, so the layout is single-column (or
// two-column) on a narrow viewport and multi-column from `md` up. The one site that
// already did this correctly is admin/server/ServerTab.tsx; three others did not,
// and /schedules/:id overflowed a 768px viewport to 840px because of it.
//
// The lookbehind is what distinguishes `grid-cols-2` from `md:grid-cols-2`.
// `grid-cols-[...]` (arbitrary track lists) is not matched at all: a bracket
// follows the hyphen, not a digit. Those are the table templates, and Task 4's
// guard below is the rule for them.
test('every numeric grid column count carries a breakpoint prefix', () => {
  // Control: the walker found the tree, not an empty directory. A silent zero here
  // would make the assertion below pass vacuously.
  expect(FILES.length).toBeGreaterThan(50)

  const offenders: string[] = []
  for (const file of FILES) {
    const src = readFileSync(file, 'utf8')
    for (const m of src.matchAll(/(?<!:)grid-cols-[2-9]\b/g)) {
      offenders.push(`${relative(SRC, file)}: ${m[0]}`)
    }
  }
  expect(offenders).toEqual([])

  // Second control: the rule is satisfied by USING the prefix, not by deleting the
  // grids. Every site that was fixed must still be a multi-column grid from md up.
  const prefixed = FILES.filter((f) => readFileSync(f, 'utf8').includes('md:grid-cols-'))
    .map((f) => relative(SRC, f).replace(/\\/g, '/'))
    .sort()
  expect(prefixed).toEqual([
    'admin/server/ServerTab.tsx',
    'admin/server/StatSection.tsx',
    'schedules/ScheduleDetailPage.tsx',
    'workers/WorkerDetailPage.tsx',
  ])
})
```

**How this test discriminates.** It is a **real guard**, not a pin: it fails for a site nobody has written yet, which no rendering test can do. Its two controls are what stop it passing vacuously - a broken path would make `FILES` empty and trip the first, and deleting the grids instead of fixing them would trip the second.

- [ ] **Step 2: Run it to verify it fails**

Run: `npx vitest run src/components/holo/responsive.guard.test.ts`

Expected: FAIL at `expect(offenders).toEqual([])` with exactly four entries:
`admin/server/StatSection.tsx: grid-cols-2`, `schedules/ScheduleDetailPage.tsx: grid-cols-2`, `workers/WorkerDetailPage.tsx: grid-cols-4`, `workers/WorkerDetailPage.tsx: grid-cols-2` (order follows the directory walk). If you get more than four, a site exists that this plan did not enumerate - **stop and report it** rather than fixing it silently.

- [ ] **Step 3: Implement**

In `web/src/schedules/ScheduleDetailPage.tsx`, the two-column body grid (the `<div>` opening the Trigger/Job spec + right-hand column layout, directly below the `actionError` block):

```tsx
      {/* Narrow-viewport convention: multi-column bodies stack below `md`, matching
          admin/server/ServerTab.tsx. See
          docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md. */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
```

In `web/src/workers/WorkerDetailPage.tsx`, the KPI stat row:

```tsx
      {/* KPI stat row. Two-up rather than one-up below `md`: four short stat cards
          stacked singly push the page body a screen down for no gain. */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
```

and the two-column body immediately below it:

```tsx
      {/* Two-column body. Stacks below `md`, matching ServerTab.tsx. */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
```

In `web/src/admin/server/StatSection.tsx`, the grid of `KpiStat` cells (inside the non-error branch, below the stale strip):

```tsx
          {/* Stacks below `md`, matching the ServerTab grid that lays these out. */}
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
```

Note `StatSection`'s `wide` cells use `col-span-2`; at one column that is a span of 2 in a 1-track grid, which CSS clamps to the single column. No change needed there.

- [ ] **Step 4: Run the guard and the full suite**

Run: `npx vitest run src/components/holo/responsive.guard.test.ts`
Expected: PASS.

Run: `npm test`
Expected: PASS, with the suite total up by one test from HEAD plus Task 1's two. **`StatSection` and both detail pages have shipped tests that must pass untouched** - if any of them asserts a class string on one of these grids, that is a finding: report it, do not edit the assertion.

- [ ] **Step 5: Run the mutation and record the output**

**Mutation B - revert one site.** Change `StatSection`'s grid back to `grid grid-cols-2 gap-3`.

Run: `npx vitest run src/components/holo/responsive.guard.test.ts`
Expected: FAIL in **`every numeric grid column count carries a breakpoint prefix`**, at `expect(offenders).toEqual([])`, received `['admin/server/StatSection.tsx: grid-cols-2']`, **and** at the second control, whose received array is missing `admin/server/StatSection.tsx`. Record both. **Revert.**

- [ ] **Step 6: Commit**

```bash
git add web/src/components/holo/responsive.guard.test.ts web/src/schedules/ScheduleDetailPage.tsx web/src/workers/WorkerDetailPage.tsx web/src/admin/server/StatSection.tsx
git commit -m "fix(web): multi-column detail bodies stack below md"
```

---

## Task 3: `Table` gains an opt-in `minWidth` that publishes one grid string and a scroll wrapper

Cause 2, half one. Nothing changes for any consumer yet - `minWidth` is optional and no caller passes it, so this task is pure primitive plus tests.

**Files:**
- Modify: `web/src/components/holo/Table.tsx` (`TableProps`, the `ColumnsContext` comment, the `Table` function body, the file header comment)
- Modify: `web/src/components/holo/Table.test.tsx` (append 3 tests)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/components/holo/Table.test.tsx`:

```tsx
// Cause 2 of docs/backlog/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md.
// Every consumer's template has fixed px tracks that sum past a narrow viewport
// (SchedulesTable's nine columns total 580px of fixed track before any `fr` gets a
// pixel), and nothing wrapped them in a scroll region.
//
// The min-width is NOT decoration and it is not a substitute for the wrapper. With
// negative free space an `fr` track falls back to its CONTENT minimum, and the
// header row and the body rows are SEPARATE grid containers whose content minimums
// differ ("NAME" versus a truncating link, whose min-content is 0) - so the columns
// visibly desynchronize. A shared min-width keeps free space non-negative, at which
// point `fr` resolves identically in both. That agreement is the property this
// primitive exists to own, which is why minWidth travels on the same context string
// as columns rather than being applied by hand in each consumer.
test('minWidth lands on the header row and on every body row as ONE identical class string', () => {
  render(
    <Table label="W" columns="grid-cols-[1fr_80px]" minWidth="min-w-[640px]" headers={[{ label: 'A' }, { label: 'B' }]}>
      <TableRow data-testid="r1">
        <TableCell>x</TableCell>
        <TableCell>y</TableCell>
      </TableRow>
    </Table>,
  )
  const header = screen.getAllByRole('row')[0]
  const row = screen.getByTestId('r1')
  expect(header).toHaveClass('grid', 'grid-cols-[1fr_80px]', 'min-w-[640px]')
  expect(row).toHaveClass('grid', 'grid-cols-[1fr_80px]', 'min-w-[640px]', 'items-center')
  // The load-bearing assertion: not "both have a min-width" but "both have the
  // SAME one". A per-element implementation can satisfy the two lines above and
  // still put the two grids out of agreement.
  const gridOf = (el: HTMLElement) =>
    el.className.split(/\s+/).filter((c) => c.startsWith('grid-cols-') || c.startsWith('min-w-')).sort().join(' ')
  expect(gridOf(row)).toBe(gridOf(header))
})

test('minWidth wraps the table subtree in a scroll container, and nothing else moves', () => {
  render(
    <div data-testid="frame">
      <Table label="W" columns="grid-cols-[1fr_80px]" minWidth="min-w-[640px]" headers={[{ label: 'A' }]} />
      <div data-testid="footer">page 1 of 3</div>
    </div>,
  )
  const table = screen.getByRole('table', { name: 'W' })
  expect(table.parentElement).toHaveClass('overflow-x-auto')
  // The scroll container wraps the role="table" subtree ONLY. Footers, error
  // banners and dialogs are siblings of <Table> in every consumer, and they must
  // stay outside the scroll region or a paginator would scroll away with the rows.
  expect(screen.getByTestId('footer').parentElement).toBe(screen.getByTestId('frame'))
  expect(screen.getByTestId('footer').closest('.overflow-x-auto')).toBeNull()
  // And the wrapper is not a frame: overflow only, no border/background/padding,
  // per the no-frame contract in Table.tsx's header comment.
  expect(table.parentElement?.className).toBe('overflow-x-auto')
})

test('without minWidth the DOM is exactly what it was before: no wrapper, no min-width', () => {
  render(
    <div data-testid="frame">
      <Table label="W" columns="grid-cols-[1fr_80px]" headers={[{ label: 'A' }]} />
    </div>,
  )
  const table = screen.getByRole('table', { name: 'W' })
  // Opt-in, not unconditional: the min-width is a function of each table's own
  // fixed tracks, so it cannot be a constant in here, and a scroll wrapper with no
  // min-width would scroll a MISALIGNED table. It also keeps every render in this
  // file's shipped tests structurally byte-identical.
  expect(table.parentElement).toBe(screen.getByTestId('frame'))
  expect(screen.getAllByRole('row')[0].className).not.toMatch(/\bmin-w-/)
})
```

**How these tests discriminate.** All three are structural, not class-presence-only: they assert *which element is the parent of which*, and that two elements carry the *same* string. The third is a genuine no-op guard - it fails if someone makes the wrapper unconditional, which would change the DOM under all eight existing consumers before Task 4 gives them a min-width.

- [ ] **Step 2: Run to verify they fail**

Run: `npx vitest run src/components/holo/Table.test.tsx`

Expected: **2 failing tests** and 1 passing.
- `minWidth lands on the header row and on every body row...` fails at the first `toHaveClass`: `min-w-[640px]` is absent (the prop does not exist, so TypeScript will also object - that is expected at this step; `vitest` does not typecheck, so the test runs and fails on the assertion).
- `minWidth wraps the table subtree in a scroll container...` fails at `expect(table.parentElement).toHaveClass('overflow-x-auto')`.
- `without minWidth the DOM is exactly what it was before...` **passes** already. That is correct: it is a guard on the implementation about to be written, and its evidence is Mutation D in Step 5.

- [ ] **Step 3: Implement**

In `web/src/components/holo/Table.tsx`:

Add to the file header comment, immediately after the existing "It deliberately renders NO frame" paragraph:

```tsx
// THE NARROW-VIEWPORT CONVENTION FOR TABLES (2026-08-13). A consumer whose template
// has fixed px tracks passes `minWidth` (a Tailwind min-w-[NNNpx] literal, sized at
// or above the sum of its own fixed tracks). That does two things, and both are
// required: it publishes ONE min-width onto the header row and every body row, and
// it wraps the role="table" subtree in an overflow-x-auto div so the table scrolls
// inside the caller's frame instead of widening the document.
//
// Why both. With negative free space an `fr` track falls back to its CONTENT
// minimum; the header row and the body rows are separate grid containers with
// different content, so they desynchronize. The min-width keeps free space
// non-negative so `fr` resolves identically in both - the same agreement guarantee
// this component already gives for `columns`.
//
// The wrapper is not the frame this primitive refuses to be: overflow only, no
// border, background, padding or radius, and it wraps ONLY the role="table"
// subtree, so the footers, error banners and dialogs that every consumer renders as
// SIBLINGS of <Table> stay outside the scroll region. The standing risk is the
// other direction: a scroll container clips anything inside it that used to escape
// the table box. Audited across all ten consumers as of 2026-08-13 - no row cell
// renders an absolutely-positioned popover; WorkspacesPanel's ConfirmDialog is a
// sibling AND portals. If a future row needs a popover, it must portal.
//
// Enforced by web/src/components/holo/responsive.guard.test.ts, which fails if any
// <Table> call site omits minWidth.
```

Add the prop to `TableProps`, immediately after `columns`:

```tsx
  // Optional Tailwind min-width utility (a literal, for Tailwind v4's static scan),
  // applied to the header row AND every TableRow, and the trigger for the
  // overflow-x-auto wrapper. Omit it only for a table with no fixed px tracks.
  minWidth?: string
```

Replace the `ColumnsContext` declaration comment and the `Table` body:

```tsx
// The value is the grid class string - never a fresh object literal, so it stays
// referentially stable across renders. When minWidth is set the value is a template
// string, which is still stable in the sense React cares about: context change
// detection is Object.is, and Object.is compares equal strings as equal.
const ColumnsContext = createContext<string | null>(null)

export function Table<F extends string = string>({
  label,
  columns,
  minWidth,
  headers,
  sort = '',
  onSort,
  headerClassName,
  children,
}: TableProps<F>) {
  // ONE string for the header row and the body rows. The whole point of the context
  // is that these two cannot be put out of agreement by hand, and a min-width that
  // applied to only one of them would desynchronize the columns at exactly the
  // widths this exists to fix.
  const grid = minWidth ? `${columns} ${minWidth}` : columns
  const table = (
    <ColumnsContext.Provider value={grid}>
      <div role="table" aria-label={label}>
        <div role="row" className={`grid ${grid} ${HEADER_BASE} ${headerClassName ?? ''}`}>
```

...the header `.map(...)` body is unchanged...

and the closing of the function becomes:

```tsx
        {/* The caller's rows. No role="rowgroup": ARIA permits row children directly
            under table, and a rowgroup would force this component to wrap rows in an
            element it owns. */}
        {children}
      </div>
    </ColumnsContext.Provider>
  )
  // Opt-in: with no minWidth the DOM is byte-identical to what eight consumers
  // shipped against, and a wrapper with no min-width would only scroll a misaligned
  // table anyway.
  if (!minWidth) return table
  return <div className="overflow-x-auto">{table}</div>
}
```

`TableRow` is unchanged: it already applies whatever string the context carries.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/components/holo/Table.test.tsx`
Expected: PASS. The shipped test `applies the grid template to the header row and to every TableRow` must pass **untouched** - `toHaveClass` is a subset check, and that test passes no `minWidth`.

Run: `npx tsc -b --noEmit` (or `npx tsc -b`) to confirm the new prop typechecks.

- [ ] **Step 5: Run both mutations and record the output**

**Mutation C - apply the min-width to the header row only.** Change the header row's className to `` `grid ${columns} ${minWidth ?? ''} ${HEADER_BASE} ...` `` and pass plain `columns` to the provider.

Run: `npx vitest run src/components/holo/Table.test.tsx`
Expected: FAIL in **`minWidth lands on the header row and on every body row as ONE identical class string`**, at the `expect(row).toHaveClass(...)` line and, if that is stepped past, at `expect(gridOf(row)).toBe(gridOf(header))`. Record it - this is the mutation the whole primitive-versus-per-consumer decision turns on. **Revert.**

**Mutation D - make the wrapper unconditional.** Change the last two lines to `return <div className="overflow-x-auto">{table}</div>` with no guard.

Run: `npx vitest run src/components/holo/Table.test.tsx`
Expected: FAIL in **`without minWidth the DOM is exactly what it was before: no wrapper, no min-width`**, at `expect(table.parentElement).toBe(screen.getByTestId('frame'))`. **Revert.**

Re-run and confirm green.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/holo/Table.tsx web/src/components/holo/Table.test.tsx
git commit -m "feat(web): Table gains an opt-in minWidth that adds a shared scroll container"
```

---

## Task 4: All ten `Table` consumers opt in, enforced by a source scan

Cause 2, half two. Each consumer declares its own min-width, because the right value is a function of its own fixed tracks.

**How each number was chosen, and the ceiling it must respect.** `min-w` must be **at least** the sum of the fixed px tracks (below that, `fr` still collapses to content and the columns desynchronize) and **below** the container's width at a 1280px viewport (at or above it, a scrollbar would appear where today there is none, which the item's "behaviour at 1280 and above is unchanged" criterion forbids). `<main>` has `p-5`, so a full-width table's container at 1280 is 1240px; the detail-page columns are roughly 614px each, and `JobDetailPage`'s left column is `lg:w-[55%]` of 1240, about 682px.

| file | fixed tracks | `fr` tracks | `MIN_W` | container at 1280 |
|---|---|---|---|---|
| `web/src/jobs/JobsTable.tsx` | 700 | 1 | `min-w-[880px]` | 1240 |
| `web/src/schedules/SchedulesTable.tsx` | 580 | 4.7 | `min-w-[1040px]` | 1240 |
| `web/src/admin/reservations/ReservationsTable.tsx` | 690 | 2.8 | `min-w-[980px]` | 1240 |
| `web/src/admin/users/UsersTable.tsx` | 500 | 2.6 | `min-w-[780px]` | 1240 |
| `web/src/admin/invites/InvitesTable.tsx` | 330 | 3.9 | `min-w-[740px]` | 1240 |
| `web/src/workers/WorkersTable.tsx` | 450 | 2.2 | `min-w-[680px]` | 1240 |
| `web/src/admin/enrollments/EnrollmentsTable.tsx` | 380 | 2.6 | `min-w-[660px]` | 1240 |
| `web/src/workers/WorkspacesPanel.tsx` | 510 | 1 | `min-w-[600px]` | ~614 (detail column) |
| `web/src/jobs/TasksTable.tsx` | 310 | 2 | `min-w-[560px]` | ~682 (`lg:w-[55%]`) |
| `web/src/schedules/ScheduleRunsPanel.tsx` | 410 | 1 | `min-w-[560px]` | ~614 (detail column) |

`WorkspacesPanel` is the tight one (600 against ~614) and Task 7 must check it specifically at 1280.

**Files:** the ten files above, plus `web/src/components/holo/responsive.guard.test.ts` (append one test).

- [ ] **Step 1: Write the failing guard test**

Append to `web/src/components/holo/responsive.guard.test.ts`:

```ts
// THE CONVENTION FOR TABLES, enforced. Every <Table> call site passes minWidth, so
// the eleventh table cannot silently reintroduce
// docs/backlog/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md. A comment
// in the primitive would not have caught the tenth.
test('every Table call site opts in to a scroll min-width', () => {
  const opens: string[] = []
  const missing: string[] = []
  let bareCount = 0
  for (const file of FILES) {
    const src = readFileSync(file, 'utf8')
    bareCount += [...src.matchAll(/<Table\b/g)].length
    for (const m of src.matchAll(/<Table\b[\s\S]*?>/g)) {
      const where = relative(SRC, file).replace(/\\/g, '/')
      opens.push(where)
      if (!m[0].includes('minWidth')) missing.push(where)
    }
  }
  // Control 1: the tag regex matched every occurrence. It stops at the first '>',
  // so an inline arrow function in the props (`onSort={(f) => ...}`) would truncate
  // a tag - no consumer does that today, and if one starts, the truncated tag will
  // almost certainly lack minWidth and fail loudly below rather than silently pass.
  expect(opens).toHaveLength(bareCount)
  // Control 2: the scan found the consumers rather than nothing at all.
  expect(opens.length).toBeGreaterThanOrEqual(10)
  expect(missing).toEqual([])
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `npx vitest run src/components/holo/responsive.guard.test.ts`

Expected: FAIL at `expect(missing).toEqual([])` listing **exactly ten** paths - `admin/enrollments/EnrollmentsTable.tsx`, `admin/invites/InvitesTable.tsx`, `admin/reservations/ReservationsTable.tsx`, `admin/users/UsersTable.tsx`, `jobs/JobsTable.tsx`, `jobs/TasksTable.tsx`, `schedules/ScheduleRunsPanel.tsx`, `schedules/SchedulesTable.tsx`, `workers/WorkersTable.tsx`, `workers/WorkspacesPanel.tsx` (walk order). Controls 1 and 2 must **pass** at this point; if control 1 fails now, the tag regex is truncating on something - stop and report.

- [ ] **Step 3: Implement - ten one-line-plus-one-constant edits**

In every file, declare the constant immediately below the existing `COLS` constant, keeping the literal in the file for Tailwind v4's static scan, then pass it.

`web/src/jobs/JobsTable.tsx`:

```tsx
const COLS = 'grid-cols-[90px_1fr_120px_150px_120px_70px_150px]'
// Fixed tracks total 700px; 880 leaves the 1fr NAME column 180px before the table
// scrolls inside its panel. See Table.tsx's narrow-viewport convention.
const MIN_W = 'min-w-[880px]'
```

```tsx
      <Table label="Jobs" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName="px-4 py-3 tracking-wider">
```

`web/src/schedules/SchedulesTable.tsx`:

```tsx
const COLS = 'grid-cols-[1.4fr_120px_110px_90px_1fr_1fr_110px_1.3fr_150px]'
// Nine columns, 580px of fixed track before any fr gets a pixel - the worst case in
// the app. 1040 gives the 4.7fr of flexible tracks about 100px each.
const MIN_W = 'min-w-[1040px]'
```

```tsx
      <Table label="Schedules" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName="px-4 py-3 tracking-wider">
```

`web/src/schedules/ScheduleRunsPanel.tsx`:

```tsx
const COLS = 'grid-cols-[130px_70px_110px_100px_1fr]'
// Fixed tracks total 410px. Sits in a detail-page column (~614px at 1280), so 560
// stays under the container and only scrolls once the column is narrower.
const MIN_W = 'min-w-[560px]'
```

```tsx
        <Table label="Recent runs" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName="px-4 py-2.5 tracking-wider">
```

`web/src/jobs/TasksTable.tsx`:

```tsx
const COLS = 'grid-cols-[1fr_110px_80px_120px_1fr]'
// Fixed tracks total 310px; lives in JobDetailPage's lg:w-[55%] column (~682px at
// 1280). Rows are as="button", so the whole scrolled width stays clickable.
const MIN_W = 'min-w-[560px]'
```

```tsx
      <Table label="Tasks" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName="px-4 py-2 tracking-wider">
```

`web/src/workers/WorkersTable.tsx`:

```tsx
const COLS = 'grid-cols-[1fr_120px_70px_140px_1.2fr_120px]'
// Fixed tracks total 450px; 680 gives NAME and LABELS about 100px each.
const MIN_W = 'min-w-[680px]'
```

```tsx
      <Table
        label="Workers"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
```

`web/src/workers/WorkspacesPanel.tsx`:

```tsx
const COLS = 'grid-cols-[120px_90px_1fr_120px_90px_90px]'
// Fixed tracks total 510px and this sits in a detail-page column of about 614px at
// 1280, so 600 is deliberately tight: it is the largest value that does not put a
// scrollbar on a maximized desktop window. Task 7 measures this one specifically.
const MIN_W = 'min-w-[600px]'
```

```tsx
      <Table label="Source workspaces" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName="px-4 py-2 tracking-wider">
```

`web/src/admin/users/UsersTable.tsx`:

```tsx
const COLS = 'grid-cols-[1.6fr_1fr_110px_120px_270px]'
// Fixed tracks total 500px (the 270px ACTIONS column holds three mini buttons).
const MIN_W = 'min-w-[780px]'
```

```tsx
      <Table
        label="Users"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
```

`web/src/admin/invites/InvitesTable.tsx`:

```tsx
const COLS = 'grid-cols-[1.5fr_110px_110px_1.4fr_110px_1fr]'
// Fixed tracks total 330px; 740 gives the 3.9fr of flexible tracks about 105px each.
const MIN_W = 'min-w-[740px]'
```

```tsx
      <Table
        label="Invites"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
```

`web/src/admin/enrollments/EnrollmentsTable.tsx`:

```tsx
const COLS = 'grid-cols-[1.6fr_130px_130px_120px_1fr]'
// Fixed tracks total 380px.
const MIN_W = 'min-w-[660px]'
```

```tsx
      <Table
        label="Agent enrollments"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
```

`web/src/admin/reservations/ReservationsTable.tsx`:

```tsx
const COLS = 'grid-cols-[1.3fr_110px_1.5fr_130px_130px_110px_110px_100px]'
// Eight columns, 690px of fixed track - second only to SchedulesTable.
const MIN_W = 'min-w-[980px]'
```

```tsx
      <Table
        label="Reservations"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
```

- [ ] **Step 4: Run the guard, then the full suite**

Run: `npx vitest run src/components/holo/responsive.guard.test.ts`
Expected: PASS.

Run: `npm test`
Expected: PASS. **Every shipped table test must pass untouched.** These tests query by role and by test id, and the primitive adds one `<div>` between the caller's frame and the `role="table"` element - if a shipped test asserts a parent-child relationship across that boundary and goes red, **that is a finding to report, not an assertion to edit** (it would mean a consumer's footer or banner is inside the table subtree, contrary to the audit in Task 3).

- [ ] **Step 5: Run the mutation and record the output**

**Mutation E - drop one consumer's opt-in.** Remove `minWidth={MIN_W}` from `web/src/jobs/JobsTable.tsx`.

Run: `npx vitest run src/components/holo/responsive.guard.test.ts`
Expected: FAIL in **`every Table call site opts in to a scroll min-width`**, at `expect(missing).toEqual([])`, received `['jobs/JobsTable.tsx']`. **Revert.**

- [ ] **Step 6: Commit**

```bash
git add web/src/jobs/JobsTable.tsx web/src/jobs/TasksTable.tsx web/src/schedules/SchedulesTable.tsx web/src/schedules/ScheduleRunsPanel.tsx web/src/workers/WorkersTable.tsx web/src/workers/WorkspacesPanel.tsx web/src/admin/users/UsersTable.tsx web/src/admin/invites/InvitesTable.tsx web/src/admin/enrollments/EnrollmentsTable.tsx web/src/admin/reservations/ReservationsTable.tsx web/src/components/holo/responsive.guard.test.ts
git commit -m "fix(web): every table scrolls inside its own frame at narrow widths"
```

---

## Task 5: The residual non-wrapping rows the header floor was hiding

The baseline shows `MAIN` alone above 375px on three surfaces that have no table and no numeric grid: `/jobs/:id` at 458, `/workers` Grid view at 401, `/admin/invites` empty at 422. Those are `flex` rows of breadcrumb links, a title, a status pill and an action bar, with no `flex-wrap`. Once Tasks 1-4 land they become the widest elements on their pages and the acceptance criterion still fails.

**There is an in-repo precedent: `web/src/jobs/TaskLogPage.tsx`'s breadcrumb row is already `flex flex-wrap items-center gap-2.5`.** The three detail-page breadcrumbs are that same row without the `flex-wrap`.

**This task ships no unit test.** See "Verification" above: a `flex-wrap` class assertion would need page-level msw scaffolding to prove a string is present, jsdom would never wrap anything, and five brittle pins would be the vacuous kind of green. Its RED is the baseline table; its GREEN is Task 7.

**Files:**
- Modify: `web/src/jobs/JobDetailPage.tsx` (breadcrumb + title + status + `job-actions` row)
- Modify: `web/src/schedules/ScheduleDetailPage.tsx` (breadcrumb + name + state pill + action bar row)
- Modify: `web/src/workers/WorkerDetailPage.tsx` (breadcrumb + name + status row)
- Modify: `web/src/jobs/JobsPage.tsx` (the `ml-auto` live-indicator + New job group)
- Modify: `web/src/workers/WorkersPage.tsx` (the `ml-auto` section-tabs + live-indicator + view-toggle group, in the populated-list header)

- [ ] **Step 1: Implement**

`web/src/jobs/JobDetailPage.tsx` - the row directly inside the breadcrumb block, currently `<div className="flex items-center gap-2.5">`:

```tsx
        {/* flex-wrap so the breadcrumb, the 28px title and the action bar stack
            instead of setting a floor under <main> - MAIN measured 458px at a 375px
            viewport without it. Matches TaskLogPage's breadcrumb row. */}
        <div className="flex flex-wrap items-center gap-2.5">
```

`web/src/schedules/ScheduleDetailPage.tsx` - the row under the `Breadcrumb + name + state pill` comment:

```tsx
      <div className="flex flex-wrap items-center gap-2.5">
```

`web/src/workers/WorkerDetailPage.tsx` - the breadcrumb/name/status row above the identity sub-line:

```tsx
      <div className="flex flex-wrap items-center gap-2.5">
```

`web/src/jobs/JobsPage.tsx` - the right-hand group inside the page header:

```tsx
        <div className="ml-auto flex flex-wrap items-center gap-3">
```

`web/src/workers/WorkersPage.tsx` - the right-hand group inside the populated-list header (the one holding `sectionTabs`, the live indicator and the Grid/Table toggle):

```tsx
        <div className="ml-auto flex flex-wrap items-center gap-3">
```

Do **not** add `flex-wrap` to the pager rows (`flex items-center justify-between`) or to the `Panel` title row: those hold short text plus a small button group, they are not in the measured residuals, and `justify-between` with wrapping produces a worse layout than the squeeze it replaces. If Task 7 measures one of them as an offender, add it then, with the number recorded.

- [ ] **Step 2: Run the full suite**

Run: `npm test`
Expected: PASS, unchanged count from Task 4. These five rows have shipped tests that query by role and text; a `flex-wrap` class changes neither. If one goes red, it is asserting a class string on a row this task edited - report it.

- [ ] **Step 3: Commit**

```bash
git add web/src/jobs/JobDetailPage.tsx web/src/schedules/ScheduleDetailPage.tsx web/src/workers/WorkerDetailPage.tsx web/src/jobs/JobsPage.tsx web/src/workers/WorkersPage.tsx
git commit -m "fix(web): page header and breadcrumb rows wrap instead of setting a floor"
```

---

## Task 6: The green gate before measuring

- [ ] **Step 1: Full web suite**

Run: `npm test` from `web/`.
Expected: PASS. Record the total. HEAD is 1059; this slice adds 2 (Task 1) + 1 (Task 2) + 3 (Task 3) + 1 (Task 4) = **7**, so expect **1066**. A different number means a test was dropped or duplicated - reconcile it before continuing.

- [ ] **Step 2: Typecheck**

Run: `npx tsc -b --noEmit`
Expected: no output. **Do not run `npm run build`** - it would write `web/dist`.

- [ ] **Step 3: Go gate (proves nothing was touched server-side)**

From the repo root: `go build ./... && go test ./...`
Expected: PASS. This slice changes zero Go files; the gate exists to prove that, not because anything server-side is in play.

- [ ] **Step 4: Confirm the working tree is exactly the 17 intended files plus 1 new test file**

```bash
git status --short
git diff --stat origin/main
```
Expected: no `web/dist` entry, no `*.sql.go`, no `models.go`, no `.go` file at all. If `web/dist` appears, `git checkout -- web/dist/`.

---

## Task 7: The real-browser measurement pass - this is the acceptance criterion

**Nothing before this point has proven the bug is fixed.** jsdom computes no layout, so every green test above is either a structural guard or a regression pin. This task is the evidence.

### Bring up the stack

From the repo root, in three shells (PowerShell syntax; the worktree is `D:/dev/relay/.claude/worktrees/happy-mendel-18687f`):

```powershell
docker run --rm -d --name relay-pg -e POSTGRES_PASSWORD=relay -e POSTGRES_USER=relay -e POSTGRES_DB=relay -p 5432:5432 postgres:16

$env:RELAY_DATABASE_URL = 'postgres://relay:relay@localhost:5432/relay?sslmode=disable'
$env:RELAY_BOOTSTRAP_ADMIN = 'admin@studio.dev'
$env:RELAY_BOOTSTRAP_PASSWORD = 'relay-dev-password'
$env:RELAY_ALLOW_AUTO_ENROLL = '1'
go run ./cmd/relay-server
```

```powershell
cd web
npm run dev    # http://localhost:5173, with /v1 proxied to :8080 (vite.config.ts)
```

### Seed the POPULATED state - this is the step both previous measurement attempts got wrong

**Every surface must be measured with rows present.** The empty state is a different layout, and measuring it is how this item spent two rounds pointing at the wrong cause.

1. Log in as the bootstrap admin.
2. Create at least three jobs via `/jobs/new` (any spec that validates), so `/jobs` renders rows and at least one job has several tasks for `/jobs/:id`.
3. Create at least two schedules, so `/schedules` and `/schedules/:id` render rows and a run history.
4. Register one agent so the fleet is non-empty (`/workers` Table view renders nothing at all with zero workers - `WorkersPage` returns its empty state first): create an enrollment in the admin console's Agent enrollments tab, then
   `$env:RELAY_AGENT_ENROLLMENT_TOKEN = '<raw token>'; go run ./cmd/relay-agent -coordinator localhost:9090`.
5. Create at least one invite and one reservation in the admin console, so `/admin/invites` and `/admin/reservations` render **rows**, not the 422px empty state the baseline captured.

### The measurement snippet

Paste this in the devtools console on each surface. **The offender predicate deliberately excludes descendants of scroll containers** - after this slice, table rows legitimately extend past the viewport *inside* an `overflow-x-auto` wrapper, and a naive "widest element" query would report every one of them as a failure.

```js
(() => {
  const d = document.documentElement
  const scrolls = (el) => {
    const o = getComputedStyle(el)
    return o.overflowX === 'auto' || o.overflowX === 'scroll' || o.overflowX === 'hidden'
  }
  const insideScroller = (el) => {
    for (let p = el.parentElement; p && p !== d; p = p.parentElement) if (scrolls(p)) return true
    return false
  }
  const offenders = [...d.querySelectorAll('*')]
    .filter((el) => el.getBoundingClientRect().right > d.clientWidth + 1 && !insideScroller(el))
    .map((el) => ({
      tag: el.tagName,
      cls: (el.className || '').toString().slice(0, 90),
      w: Math.round(el.getBoundingClientRect().width),
      right: Math.round(el.getBoundingClientRect().right),
    }))
    .sort((a, b) => b.right - a.right)
    .slice(0, 8)
  return JSON.stringify(
    {
      docSW: d.scrollWidth,
      clientW: d.clientWidth,
      header: Math.round(document.querySelector('header')?.getBoundingClientRect().width ?? 0),
      main: Math.round(document.querySelector('main')?.getBoundingClientRect().width ?? 0),
      offenders,
    },
    null,
    1,
  )
})()
```

### What to record, and the pass condition

- [ ] **Step 1: At 375px and at 320px**, on every surface below, record `docSW`, `clientW`, `header`, `main` and `offenders`.

**PASS requires `docSW <= clientW` AND `offenders` empty, on every row.** Compare each against the baseline table at the top of this document.

| surface | baseline docSW @375 | target |
|---|---|---|
| `/auth` | 375 | 375 (unchanged control - it has no shell, so it must not move) |
| `/jobs` (rows present) | 763 | <= 375 |
| `/jobs/:id` (multi-task job) | 523 (main 458) | <= 375 |
| `/workers` (Grid view) | 523 (main 401) | <= 375 |
| `/workers` (Table view) | 593 | <= 375 |
| `/workers/:id` (as admin) | **not measured** | <= 375 |
| `/schedules` | 728 | <= 375 |
| `/schedules/:id` | 651 | <= 375 |
| `/admin/users` | 607 | <= 375 |
| `/admin/invites` (**rows**, not empty) | 523 (empty state) | <= 375 |
| `/admin/enrollments` (rows) | not measured | <= 375 |
| `/admin/reservations` (rows) | not measured | <= 375 |
| `/admin/server` | not measured | <= 375 |
| `/jobs/new` | not measured | <= 375 |
| `/profile/identity` | 523 | <= 375 |
| `/profile/password` | not measured | <= 375 |
| `/profile/sessions` | 494-523 | <= 375 |

- [ ] **Step 2: At 768px**, re-measure `/schedules/:id` specifically. Baseline: **840px**, driven by the `grid-cols-2` body independently of the header. Target: `docSW <= 768`.

- [ ] **Step 3: At 1280px, prove nothing regressed.** For every surface with a table, run:

```js
[...document.querySelectorAll('.overflow-x-auto')].map((el) => ({
  cls: el.firstElementChild?.getAttribute('aria-label'),
  scrollW: el.scrollWidth,
  clientW: el.clientWidth,
}))
```

**PASS requires `scrollW <= clientW` for every wrapper** - i.e. no new scrollbar at a maximized desktop width. **Check `WorkspacesPanel` on `/workers/:id` explicitly**: its `min-w-[600px]` sits against a container of roughly 614px and is the one value in Task 4's table with less than 15px of headroom. If it fails, lower it to `min-w-[560px]` (still above its 510px fixed-track sum), re-run Task 4's guard and `npm test`, and record the change.

- [ ] **Step 4: Prove the header's stacking behaviour is unchanged.** At 375px, 768px and 1280px, open the account dropdown and confirm it paints **over** `<main>` and is not clipped:

```js
(() => {
  const p = document.querySelector('[data-testid="user-menu-panel"]')
  const r = p.getBoundingClientRect()
  const pts = []
  for (let x = r.left + 4; x < r.right - 4; x += 12)
    for (let y = r.top + 4; y < r.bottom - 4; y += 12)
      pts.push(document.elementFromPoint(x, y))
  return { total: pts.length, occluded: pts.filter((e) => !p.contains(e)).length }
})()
```

**PASS requires `occluded: 0`** at all three widths. This is the reduced form of the 275-point hit test recorded in `HoloShell.tsx`, and it is what proves Decision 1's hazard (an overflow on the header clipping the dropdown) did not materialize.

- [ ] **Step 5: Prove the tables are usable, not just contained.** On `/schedules` at 375px, scroll the table horizontally and confirm every one of the nine columns is reachable, the header row stays aligned with the body rows across the full scroll, and the paginator footer **does not** scroll with them (it is outside the wrapper).

- [ ] **Step 6: Write the results into the PR body** as a before/after table against the baseline. If any surface fails, **stop and report which element the `offenders` list names** - do not adjust the target.

- [ ] **Step 7: Tear down**

```powershell
docker rm -f relay-pg
```

---

## Self-review notes for the executing engineer

- **Nothing in this slice touches an async lifecycle**, so the "end the generation before releasing the resource" invariant has no instance here. If you find yourself adding a `useEffect`, a resize listener or a `matchMedia` subscription, you have left the plan - every fix here is a CSS class.
- **No JavaScript-driven responsiveness.** No `window.innerWidth` reads, no `useMediaQuery`, no breakpoint constants in TypeScript. Tailwind's `md:` prefix and `overflow-x-auto` are the entire mechanism, which is why the whole slice is revertible by deleting class strings.
- **If a shipped test needs editing, that is the finding.** Every test in `web/` should pass untouched through all five code tasks.
- **The `min-w` numbers are engineering estimates**, derived from each template's fixed-track sum. Task 7 Step 3 is what validates them, and adjusting one downward on that evidence is expected, not a plan failure.

## Closing the item

After Task 7's numbers are recorded and the PR is green, the **conductor** closes the item:

```
/backlog close bug-2026-08-12-web-narrow-viewport-horizontal-overflow
```

The Resolution note must record three things, or the item's history stays misleading:

1. **There were three independent causes, not one**, and the item asserted a single cause twice - first the `Table` primitive, then the header - both times from a page total plus a source inference. A fourth (non-wrapping page header rows) was found only by the per-element pass.
2. **The lesson: measure the populated state.** Both wrong attributions came from surfaces whose tables were empty or not rendered.
3. **The header nav treatment was chosen without a hi-fi reference** (the Holo hi-fi is silent on narrow viewports) and is the reviewer's to overrule.

## Follow-ups to file (conductor, at retro time)

- **Keyboard access to the table scroll regions.** No `tabIndex={0}` was added to the `overflow-x-auto` wrappers - see Decision 2 for the argument. Worth an idea item so the choice is on the record rather than implicit.
- **Whether the nav scrollbar should be visually suppressed** at narrow widths (`[scrollbar-width:none]` plus a `::-webkit-scrollbar` variant). Deliberately not done here; it is a design call.
- **Column dropping below a breakpoint** for the widest tables (`SchedulesTable`'s nine columns, `ReservationsTable`'s eight). Scrolling is the honest minimum this slice ships; deciding which columns matter at 375px is a per-table product decision.
- **`idea-2026-06-03-web-e2e-harness`.** This slice is the strongest argument yet for it: seven of its eight verification points were only obtainable by a human driving a real browser, and none of them is protected against regression by `npm test`.
