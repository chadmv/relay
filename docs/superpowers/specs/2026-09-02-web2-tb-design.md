# Lane TB: the accessible-table follow-ups - design

Date: 2026-09-02
Lane: TB of the second web-frontend batch
Branch: `claude/web2-tb-tables` (worktree `.claude/worktrees/web2-tb`, at `origin/main` 5360a7e)
Author: relay-tpm (autonomous run; every question a human would have been asked is answered in
Decisions, and the calls most likely to go the other way are in Escalations)

Backlog items:

- `docs/backlog/idea-2026-08-09-sort-caret-in-accessible-name.md`
- `docs/backlog/idea-2026-08-09-tasks-table-grid-role-selection.md`
- `docs/backlog/idea-2026-08-09-table-accessible-name-consistency.md`
- `docs/backlog/idea-2026-08-09-table-visual-harmonization.md`

## Summary

Four follow-ups from the 2026-08-09 shared-accessible-table primitive, all on
`web/src/components/holo/Table.tsx` and its consumers. Frontend only, zero Go changes, no new
dependency, no API change.

1. **Caret.** Wrap the sort caret in an `aria-hidden` span so a sortable column header's accessible
   name is the column label alone, and move five test files off asserting the glyph as part of that
   name.
2. **TasksTable role.** Do **not** build a grid and do **not** switch to a listbox. Remove the
   inert `aria-selected` and the `as="button"` row override, put a real named button inside the
   name cell, and mark the selected one with `aria-current`. The selection then announces, the row
   announces as activatable, and the column semantics `role="table"` provides are kept.
3. **Names.** Give `RevokedWorkersTable` an accessible name, and stop hand-duplicating a panel
   title into a table label in the two places that do it.
4. **Visual.** The hi-fi's admin, workers, jobs and schedules list surfaces all spread the full
   `glassPanel(C)`, shadow included, so the shadowless admin surface is drift, not intent. Five
   hand-rolled frames become `GlassPanel`, and the eleven header rows collapse to two
   `headerClassName` values taken from the hi-fi's own two header treatments.

Each is a separate commit, ordered so both of the lane's competing zero-diff constraints are
checkable per commit.

## Scope and non-goals

In scope: `web/src/components/holo/Table.tsx`, `GlassPanel.tsx`, `Panel.tsx`, the eleven `Table`
consumers, `RevokedWorkersTable.tsx`, `WorkerDetailPage.tsx`, `JobDetailPage.tsx`, their test files,
and one new `web/e2e` describe block.

Explicit non-goals, each with its reason:

- **No `role="grid"` and no grid keyboard model.** See Decision 2.
- **No `GlassPanel` variant prop.** The hi-fi refutes the premise that a shadowless surface is a
  design intent; see Decision 4.
- **No change to row vertical padding, border colours, text sizes or the header font size.** The
  visual item scopes itself to frames and header spacing and says so. The horizontal component of
  row padding is the one exception, and only because it is mechanically coupled to the header
  change; see Decision 4.
- **No axe or automated a11y rule engine.** The repo has none (a search for `axe` under `web/`
  returns only the substring inside `leading-relaxed`). Adding one would have caught the
  `aria-selected`-under-`table` defect, and it is a lane of its own; see Backlog proposals.
- **No new `min-w-` values.** The horizontal padding change costs 4px of content width per affected
  table; the headroom check is in Decision 4.

## Context at HEAD, verified in this worktree

Line numbers are deliberately absent throughout; everything is cited by symbol or by file, per the
rule in `web/CLAUDE.md`.

**Prose is compiled input under `web/`, and only under `web/`.** `@tailwindcss/vite` builds its
scanner over the Vite root, which is `web/`. This document lives in `docs/`, a sibling, so the class
literals spelled below are not scanned. Any prose the implementer writes **inside `web/`** must not
spell a utility class; name the CSS property instead.

### The primitive

`Table` takes a required `label` (becomes the table's accessible name), a required `minWidth`, a
`columns` grid template carried on `ColumnsContext`, `headers`, `sort`, `onSort` and an optional
`headerClassName`. It renders `role="table"` inside an always-present `overflow-x-auto` wrapper that
is itself `tabIndex={0} role="group"` with an accessible name derived from `label`. `TableRow`
hardcodes `role="row"` and spreads caller props *before* `role` so a caller cannot override it;
`TableCell` does the same for `role="cell"`. `TableRow` accepts `as` and `type` props, added for
exactly one consumer.

`sortCaret(field, sort)` returns a leading space plus U+25BC (a solid down-pointing triangle) when
the active sort is descending on that field, a leading space plus U+25B2 when ascending, and the
empty string otherwise. `Table` renders it as a bare text node inside the header `<button>`, so the
button's accessible name, and the enclosing `columnheader`'s, is the label plus the glyph.

### The consumers: eleven, not eight

`Table` consumers at HEAD, by file: `JobsTable`, `SchedulesTable`, `TasksTable`, `WorkersTable`,
`UsersTable`, `EnrollmentsTable`, `ReservationsTable`, `InvitesTable`, `WorkspacesPanel`,
`ScheduleRunsPanel`, `WorkerTasksPanel`. That is **eleven**. The axis of this count is "files that
render the `Table` component"; it was taken by searching for the holo barrel import together with
`<Table`, and it does not count `RevokedWorkersTable`, which is a native `<table>` and is not a
`Table` consumer.

Frames, by consumer:

- `GlassPanel` already: `JobsTable`, `SchedulesTable`, `TasksTable`.
- Hand-rolled, byte-identical to `GlassPanel`'s base minus its shadow, in **four** files:
  `UsersTable`, `EnrollmentsTable`, `ReservationsTable`, `InvitesTable`. The literal is
  `rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02]
  backdrop-blur-[8px]`.
- Hand-rolled and pre-upgrade flat, one file: `WorkersTable`, with
  `rounded-card border border-border bg-white/5 backdrop-blur`, and a comment stating the frame is
  "deliberately left as-is" because adopting `GlassPanel` would be a visible change.
- No frame of their own (they render inside a `Panel`, which composes `GlassPanel`):
  `WorkspacesPanel`, `ScheduleRunsPanel`, `WorkerTasksPanel`.

`headerClassName`, by value, **four** distinct strings:

- `px-4 py-3 tracking-wider` - `JobsTable`, `SchedulesTable`, `WorkersTable`.
- `px-4 py-2 tracking-wider` - `TasksTable`, `WorkspacesPanel`, `WorkerTasksPanel`.
- `px-4 py-2.5 tracking-wider` - `ScheduleRunsPanel`.
- `px-[18px] py-3 tracking-[0.16em]` - `UsersTable`, `EnrollmentsTable`, `ReservationsTable`,
  `InvitesTable`.

Row horizontal padding tracks the header's in every case: `px-4` rows under `px-4` headers,
`px-[18px]` rows under `px-[18px]` headers. This coupling is load-bearing and is why Decision 4 is
shaped the way it is.

### The glyph in test files: six files, five that break

Searching for the two caret code points across `web/`. Below, `<down-caret>` and `<up-caret>` stand
for the literal U+25BC and U+25B2 characters as they appear in the source. This document keeps those
two bytes out of its own text deliberately: a raw non-ASCII literal in prose is unverifiable by eye
and survives every check this repo runs.

- `WorkersTable.test.tsx` - one `getByRole('button', { name: /name <down-caret>/i })`. **Breaks.**
- `UsersTable.test.tsx` - one exact `getByRole('button', { name: 'NAME <down-caret>' })`. **Breaks.**
- `EnrollmentsTable.test.tsx` - two exact button-name queries in two tests. **Breaks.**
- `InvitesTable.test.tsx` - four assertions across three tests: two anchored `columnheader` name
  regexes and two exact button names. **Breaks.** Not named by the item.
- `Table.test.tsx` - two exact button-name assertions in `the caret follows the active sort
  direction`, plus four `sortCaret` return-value assertions that are unaffected. **Breaks.** Not
  named by the item.
- `ReservationsTable.test.tsx` - one `nameHeader?.textContent` `toContain` assertion. This reads
  `textContent`, which an `aria-hidden` wrapper does not change. **Survives**, and that is useful:
  it is a free proof that the caret is still rendered.

Every other caret-adjacent query in these files uses a substring or prefix regex on the label
(`/EMAIL/`, `/^CREATED/`, `/last seen/i`) and survives unchanged.

### TasksTable and the panes

`TasksTable` renders each task row as `TableRow as="button" type="button" aria-selected={selected}`
with `onClick`, and the primitive turns that into `<button role="row">`. It is the **only** consumer
of `TableRow`'s `as` and `type` props; a search for `as=` on `TableRow` returns exactly one call
site. `Table.test.tsx`'s `TableRow renders as the element named by 'as' and forwards arbitrary
props` exercises `as="button" type="button" aria-selected` together, which is what blesses the
arrangement as API.

`JobDetailPage` owns the selection (`pickedTaskId` plus a `defaultTaskId` fallback), renders
`<TasksTable ... onSelect={setPickedTaskId} />` in the left column and a `role="tablist"` with
`aria-label="Task detail"` and two `role="tab"` buttons in the right column. The tabs have no
`role="tabpanel"` and no `aria-controls`; that is a separate defect and a backlog proposal, not this
lane's scope. `JobDetailPage.test.tsx` has one test, `selecting a task updates aria-selected and
drives the spec pane`, that reads `aria-selected` off rows.

Nothing in `web/src` removes the user-agent focus ring: the only `outline` occurrences are
`outline-none` on five specific inputs and one select. A plain `<button>` therefore gets the
browser's default focus indicator, exactly as today's row-as-button does. **Do not invent a focus
style for this lane.**

### The two naming gaps, which are now three sites

- `RevokedWorkersTable` is a native `<table>` with `<thead>`/`<tbody>`, no `<caption>` and no
  `aria-label`. It is rendered by `WorkersPage` only in the `decommissioned` section.
- `WorkspacesPanel` passes `label="Source workspaces"` while `WorkerDetailPage` renders
  `<Panel title="Source workspaces">` around it. Two literals, kept equal by hand.
- `WorkerTasksPanel` passes `label="Current tasks"` while `WorkerDetailPage` renders
  `<Panel title="Current tasks">` around it. **The same defect, a second time**, in a component
  that shipped on 2026-09-02, three weeks after the item was filed. The item does not mention it.

`WorkerDetailPage.test.tsx`'s `the reservations panel contains no fabricated reservation rows` reads
the **`aria-label` attribute** off every table on the page and compares the sorted list to
`['Current tasks', 'Source workspaces']`. Any move to `aria-labelledby` would null that attribute and
redden this test; a move that keeps a string `label` does not.

`Panel` composes `GlassPanel` and renders its `title` in a `<span>` inside a bordered header row. It
has no id, no context and no test hook.

**The uniqueness claim in the naming item holds.** Searching `web/src` for `<table` returns exactly
one production hit (`RevokedWorkersTable`) and for `role="table"` returns exactly one production hit
(inside `Table.tsx`); every other match is prose. Since `label` is a required prop on `Table`, all
eleven grid tables are named by construction, so `RevokedWorkersTable` really is the only unnamed
table. Axis: elements exposing the `table` role anywhere under `web/src`.

### The hi-fi, quoted rather than paraphrased

`design_handoff_relay_holo/hifi3-holo-pages.jsx` defines one surface helper:

```js
function glassPanel(C){
  return {
    background:`linear-gradient(180deg, rgba(255,255,255,0.06), rgba(255,255,255,0.02))`,
    border:`1px solid ${C.border}`,
    backdropFilter:'blur(8px)',
    borderRadius:14,
    boxShadow:`inset 0 1px 0 rgba(255,255,255,0.08), 0 8px 32px rgba(0,0,0,0.4)`,
    position:'relative',
  };
}
```

`web/src/components/holo/GlassPanel.tsx`'s `BASE` is a faithful Tailwind transcription of exactly
that, shadow included.

Every top-level list surface in the hi-fi spreads it **whole**. The jobs table:

```js
<div style={{...glassPanel(C), flex:1, minHeight:0, display:'flex',flexDirection:'column', overflow:'hidden'}}>
```

The workers table (`view === 'table'`), the schedules table, the users table, the enrollments table
and the reservations table each open with a `<div>` carrying that identical spread. There is no
shadow-suppressing override anywhere among them: no `boxShadow:'none'` appears on any of these six
surfaces (the two `boxShadow:'none'` occurrences in the file are on a view-switch button and a
timeline bar).

The hi-fi has exactly **two** table header treatments.

Top-level lists - jobs, workers, schedules, users, enrollments, reservations - all use the same one.
The users table's header:

```js
<div style={{display:'grid',gridTemplateColumns:'1.4fr 1fr 110px 100px 110px 100px 220px',
  fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
  padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
```

and the jobs, workers and schedules headers repeat
`fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute` with `padding:'12px 18px'`
verbatim, differing only in `gridTemplateColumns`.

Tables nested inside a panel use a denser one. The worker-detail "Source workspaces" header:

```js
<div style={{display:'grid',gridTemplateColumns:'80px 70px 1fr 90px 70px 60px',
  fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.14em',color:C.fgMute,
  padding:'10px 16px',borderBottom:`1px solid ${C.border}`}}>
```

Row padding in the hi-fi is one shared token, `rowPad:'10px 18px'`, used by every top-level list
row; the nested workspaces rows use `padding:'7px 16px'`.

Two consequences, both decisive for Decision 4:

- 12px/18px with 0.16em is precisely today's `px-[18px] py-3 tracking-[0.16em]`. The four admin
  tables are the hi-fi-faithful ones; `tracking-wider` (0.05em) is the divergence.
- 10px/16px with 0.14em is `px-4 py-2.5 tracking-[0.14em]`. `ScheduleRunsPanel` is one token away
  from it already.

### What the e2e harness can and cannot say

`web/e2e/surfaces.ts` carries fifteen surfaces. Relevant here:

- `job-detail` is **populated**: `seedAll` creates a job with three tasks named alpha, beta and
  gamma (beta and gamma depend on alpha), and the surface gates on the job name heading. This is a
  real, populated `TasksTable` in a real browser, in chromium and webkit. It is the strongest
  instrument available to this lane.
- `admin-users`, `admin-invites`, `admin-enrollments`, `admin-reservations`, `jobs`, `schedules`,
  `schedule-detail` are all populated and screenshotted per width.
- `workers` is **empty-state only** and gates on the copy "No workers enrolled yet.". No worker row
  can exist in slice 1, so `WorkersTable` is never rendered under this harness. **The single most
  visible change in this lane has no browser coverage of any kind.**
- `web/e2e/keyboard.spec.ts` covers `admin/enrollments` and `admin/invites` at a 480px viewport,
  asserting the labelled scroll group is Tab-reachable and arrow-scrollable, with an
  `assertScrollable` precondition requiring more than 100px of overflow (measured 222px and 302px
  with the min-width rule present, 51px and 32px without it).
- Screenshots are artifacts, not assertions. There are no pixel baselines and there is no visual
  regression harness. `layout.spec.ts` fails only on document-level overflow.

## What the four backlog items got wrong

Refuted, item by item. This is not a list of typos; two of these change the shape of the work.

**Caret item.** "Broke **three** of the five test files" understates it. Five files break, not
three: `InvitesTable.test.tsx` (four assertions in three tests) and `Table.test.tsx` (two assertions
in one test) are also affected and neither is named. A sixth, `ReservationsTable.test.tsx`, asserts
the glyph through `textContent` rather than the accessible name and correctly does not break. The
item's *protected-file* count of three is right; its total is wrong, and an implementer who patched
only the three named files would ship a red suite.

**TasksTable item.** Accurate as a diagnosis. Its Proposal - add a `role` prop switching to
`grid`/`gridcell` - is the remedy this spec declines, on the grounds the item's own Related section
supplies. One factual correction: it says "seven other tables depend on that component"; the number
is ten others, eleven in total.

**Naming item.** The "only unnamed table" claim verifies. Two additions: the hand-duplication defect
now exists in **two** panels, not one, because `WorkerTasksPanel` reproduced it after the item was
filed; and the item's suggested `aria-labelledby` route would redden
`WorkerDetailPage.test.tsx`'s table census, which reads the `aria-label` attribute rather than the
accessible name. Neither is a reason not to do the work; both change how.

**Visual item.** Three counts are stale and one premise is refuted:

- "eight tables" is eleven.
- "four of eight hand-roll the glass frame" is five of eleven: `InvitesTable` is a fourth copy of
  the shadowless literal.
- "three different spacing/tracking strings" is four: `ScheduleRunsPanel` uses a value the item does
  not list.
- The item frames the shadowless admin surface as a live design question ("intentional, therefore a
  `GlassPanel` variant prop, or accidental drift"). The hi-fi answers it: the users, enrollments and
  reservations surfaces each spread the full `glassPanel(C)` including its `boxShadow`. There is no
  variant to express. **The proposed variant-prop branch is dead.**

And one thing the item could not have known, which reshapes its third proposal: **header horizontal
padding is coupled to row horizontal padding.** Both are applied to sibling grid containers that
must align column-for-column, so "pick one header spacing pair and apply it" cannot be executed
while row padding is out of scope, unless the horizontal component moves in lockstep. Decision 4
handles this by moving only the horizontal component of row padding and leaving the vertical
component alone.

Nothing else in the four items was refuted.

## Decisions

### Decision 1: the caret

**Question.** How does the caret stop being part of the accessible name, and what do the tests
assert instead?

**Options.**

- (a) Wrap the caret in `<span aria-hidden="true">`, leave `aria-sort` as the sole machine-readable
  carrier of direction, and re-point the five affected test files at `aria-sort`.
- (b) Replace the glyph with a CSS-drawn indicator (a border triangle or a background image), so
  there is no text node to hide.
- (c) Keep the glyph in the name and delete `aria-sort` as the duplicate.

**Decision: (a).**

**Reason.** (c) inverts the correctness: `aria-sort` is the ARIA-defined carrier and a glyph
announced as "black down-pointing triangle" is not. (b) is a larger visual change than this item
buys, and it would have to reproduce the glyph's exact metrics to stay pixel-neutral; the item's
Done-When requires the caret remain visible **unchanged**. (a) is one JSX change plus the test work
the item correctly identifies as the substantive half.

`sortCaret`'s signature and return values do not change, so its four unit assertions in
`Table.test.tsx` stay byte-identical - which keeps the anchored-prefix property (a field name
containing its own hyphen) pinned by the same test that pins it today.

### Decision 2: the TasksTable role

**Question.** Is the tasks list a grid, a listbox, or should it stop implying selection semantics?

**Options.**

- (a) **Grid.** Add `role?: 'table' | 'grid'` to `Table`, switch cells to `gridcell`, keep
  `aria-selected`, and implement the keyboard model `grid` obliges: one tab stop into the widget,
  roving tabindex, arrow-key row and cell navigation, Home/End, and a defined focus-restoration
  rule.
- (b) **Listbox.** Container `role="listbox"`, rows `role="option"`, `aria-selected` becomes
  meaningful, and the composite keyboard model comes with it.
- (c) **Correct the advertisement.** Keep `role="table"`. Delete `aria-selected` and the
  `as="button"` row override. Put a real `<button>` inside the name cell, named by the task name,
  carrying `aria-current="true"` when it is the selected task. Keep the row's `onClick` so the whole
  row stays a mouse target.

**Decision: (c).**

**Reason.** The closed precedent `feature-2026-06-05-usermenu-panel-menu-roles` asked this exact
question and its Resolution is the argument here, transposed one noun at a time:

- There, `role="menuitem"` **replaces** the link role in the platform accessibility tree, so an
  entry leaves the links list and browse-mode "next link". Here, `role="grid"` **replaces**
  `role="table"`, and a conforming grid's roving tabindex **removes the per-row native tab stop that
  exists today**. Every task row is a real `<button>` right now and Tab reaches each one. Option (a)
  takes that away and hands back a keyboard model we would have to hand-write and maintain. That is
  the same trade the UserMenu work refused.
- There, the fix was to stop advertising a menu, not to build one. Here, `aria-selected` under
  `role="table"` is advertising a selection model the container does not have. The honest repair is
  the same shape.

Option (b) is worse than (a), not better. `option` takes its accessible name from its content, so
the five cells would flatten into one announced string and the `columnheader` association that
`role="table"` gives every cell would be lost - a five-column task row would read as an
unpunctuated run of values. It also costs the same composite keyboard model as (a). The item
suggests `listbox` "may be the honest match"; it is not, because these rows are read column-wise
even though they are selected row-wise.

Option (c) additionally makes `TasksTable` structurally identical to the other ten tables in the
app, every one of which puts its interactive control (a `Link`, usually) inside the name cell and
leaves the row a plain `role="row"`. The only difference is that this control selects rather than
navigates.

**What the selected row announces after the change, and how the user learns which task drives the
panes.** Three things, and the first two are the load-bearing ones:

1. The name cell's button is a real button with an accessible name equal to the task name, inside a
   `cell` whose `columnheader` is NAME. It announces as a button, so it advertises that it is
   activatable - the second half of the item's Done-When, which `aria-selected` never addressed.
2. The selected task's button carries `aria-current="true"`. `aria-current` is valid on any element,
   is not conditional on the container role the way `aria-selected` is, and is announced (typically
   as "current") by every major screen reader. This is what tells the user *which* task is selected.
3. `JobDetailPage`'s tablist accessible name changes from the fixed `Task detail` to a name that
   includes the selected task's name. That closes the "drives the panes" half explicitly: a user who
   moves to the Spec/Log tabs hears which task's spec and log they are about to read. This is a
   two-token change in `JobDetailPage.tsx`, and it is in scope precisely because the item's gap is
   about the *linkage* between the selection and the panes, not only about the row.

**One handler, not two.** The row keeps `onClick`; the in-cell button carries **no** handler of its
own. Activating the button - by mouse, by Enter, or by Space - dispatches a click that bubbles to the
row's handler. This keeps exactly one call to `onSelect` per activation and keeps the whole row a
mouse target. It is subtle enough to need one comment stating the hazard, and it is pinned by the
keyboard test named in Acceptance.

**`TableRow`'s `as` and `type` props are removed.** `TasksTable` is their only consumer, and it is
this decision that removes it. Leaving the props in place would leave loaded the exact trap this
item exists to close: the next author writes `as="button"` and silently re-creates a
`<button role="row">`. `tsc -b` is the only enforcement mechanism in this repo that reaches every
call site including aliased imports, which is the reason `minWidth` was made required rather than
guarded by a source-scanning test; the same reasoning applies to removing a prop that must not be
used.

### Decision 3: accessible names

**Question A.** How is `RevokedWorkersTable` named - a visible `<caption>` or an `aria-label`?

**Decision: `aria-label="Revoked workers"` on the `<table>`.**

**Reason.** Every one of the eleven grid tables is named invisibly, via `Table`'s required `label`.
A visible caption here would make the one native table the only table in the app that announces its
name on screen, and it would duplicate context the page already shows: an `<h1>` reading "Workers"
plus a pressed "Decommissioned" section tab directly above it. `aria-label` on a native `<table>` is
valid and well supported. It is also a zero-pixel change, which matters because this table is not
reachable by any e2e surface - nobody would see a regression in a screenshot.

**Question B.** How does the Source workspaces (and now Current tasks) table stop being able to
diverge from its Panel title - derive via `aria-labelledby`, or pin equal?

**Options.**

- (a) `aria-labelledby`: `Panel` generates an id via `useId`, publishes it on a context, `Table`
  gains a second naming mode, and the consumers point at the title element.
- (b) One literal: each panel component exports its title constant, `WorkerDetailPage` imports it
  for `<Panel title={...}>`, and a page-level test pins the rendered title equal to the rendered
  accessible name.

**Decision: (b).**

**Reason.** Three reasons, in order of weight.

1. **(a) buys the user nothing.** With `aria-label="Source workspaces"` the screen reader announces
   "Source workspaces table". With `aria-labelledby` pointing at the same visible string it
   announces the same words. The entire difference is maintenance, so the cheaper mechanism wins on
   its own.
2. **(a) costs a second naming mode on the primitive**, with an "exactly one of `label` /
   `labelledBy`" invariant to enforce, plus a decision about the scroll wrapper's own name, which is
   currently derived as the label plus a "scrolls horizontally" suffix and has a test asserting it
   is derived rather than duplicated. A second mode either duplicates that suffix logic or leaves
   the wrapper unnamed.
3. **(a) reddens `WorkerDetailPage.test.tsx`** for an uninteresting reason: its table census reads
   the `aria-label` **attribute**, which `aria-labelledby` would null.

**The guard, and why the obvious version of it would be vacuous.** A test asserting "both sites use
the same imported constant" cannot fail: they are the same symbol. The test must read the
**rendered** visible title and the **rendered** accessible name and compare those. So `Panel` gains
one inert attribute, `data-panel-title`, set to its `title` prop when that prop is a string and
omitted otherwise, and the page test walks every `role="table"` on the rendered `WorkerDetailPage`
up to `closest('[data-panel-title]')` and asserts the table's accessible name equals that attribute.
The test also asserts it examined exactly two tables, so it cannot go vacuous if a panel stops
rendering. The repo has precedent for inert DOM hooks of this kind (`data-testid="user-menu-panel"`
and `data-dialog-layer`).

This is not gold-plating: the defect reproduced itself once already, in `WorkerTasksPanel`, three
weeks after the item was filed. A guard that catches the third instance is the whole point.

### Decision 4: the visual call

**Question A.** Is the shadowless admin surface intentional (a `GlassPanel` variant) or drift?

**Decision: drift. The four hand-rolled gradient frames become `<GlassPanel>`. No variant prop is
added.**

**Reason.** The hi-fi's users, enrollments and reservations surfaces each spread
`{...glassPanel(C), ...}` whole, and `glassPanel(C)` carries
`boxShadow: 'inset 0 1px 0 rgba(255,255,255,0.08), 0 8px 32px rgba(0,0,0,0.4)'`. No admin surface
overrides it. There is no design intent to express as a variant; there is a transcription that
dropped a property, four times.

**Question B.** Does `WorkersTable` adopt the gradient surface?

**Decision: yes. `<GlassPanel>`, and the "deliberately left as-is" comment goes with it.**

**Reason.** The hi-fi's workers table opens with the same
`<div style={{...glassPanel(C), flex:1, minHeight:0, display:'flex',flexDirection:'column',
overflow:'hidden'}}>` as the jobs table. The flat `bg-white/5 backdrop-blur` frame is the
pre-upgrade treatment, and it is currently the only one of the six top-level lists that looks
flatter than its neighbours. This is the one genuinely visible change in the item, and the item
correctly says so.

**Question C.** How many `headerClassName` values, and which?

**Decision: two.**

- `px-[18px] py-3 tracking-[0.16em]` for the **seven top-level list tables**: `JobsTable`,
  `SchedulesTable`, `WorkersTable`, `UsersTable`, `EnrollmentsTable`, `ReservationsTable`,
  `InvitesTable`. Four of the seven already carry it.
- `px-4 py-2.5 tracking-[0.14em]` for the **four tables nested inside a `Panel` or a detail-page
  column**: `TasksTable`, `WorkspacesPanel`, `WorkerTasksPanel`, `ScheduleRunsPanel`.

**Reason.** These are the hi-fi's own two header treatments, transcribed: `padding:'12px 18px'` with
`letterSpacing:'0.16em'` for every top-level list, and `padding:'10px 16px'` with
`letterSpacing:'0.14em'` for the nested workspaces table. The second value exists because the hi-fi
has a second value, for the nested case, and that reason is written into the code as a comment on
whichever constant the implementer chooses to name.

**Question D.** The coupling. Moving three headers from `px-4` to `px-[18px]` misaligns them from
their own rows, which are `px-4`.

**Decision: move the horizontal component of row padding on those same three tables (`JobsTable`,
`SchedulesTable`, `WorkersTable`) from `px-4` to `px-[18px]`, and change nothing else about row
padding.**

**Reason.** The header row and the body rows are sibling grid containers with the same template;
their horizontal padding must agree or every column label sits 2px off its data. Leaving the
horizontal component alone instead would force a third `headerClassName` value and miss the item's
own "one or two" criterion. Moving vertical row padding as well would be the larger, genuinely
visible change the item excludes - the hi-fi's `rowPad:'10px 18px'` versus the app's 8px - and that
half stays out, filed as a backlog proposal.

**The headroom check, because horizontal padding eats content width under `box-sizing: border-box`,
and `minWidth` and the padding sit on the same element.** Content width becomes `MIN_W` minus 36px
instead of minus 32px:

- `JobsTable`: 880 - 36 = 844, against 700px of fixed track. Headroom 144px.
- `SchedulesTable`: 1040 - 36 = 1004, against 580px of fixed track. Headroom 424px.
- `WorkersTable`: 680 - 36 = 644, against 450px of fixed track. Headroom 194px.

All three keep free space non-negative, so `fr` still resolves identically in the header row and the
body rows, which is the agreement property `Table` exists to own. **No `MIN_W` constant changes.**

The four admin tables' `headerClassName` and row padding are already the target values and do not
change at all, which is why `keyboard.spec.ts`'s measured overflow thresholds for enrollments and
invites are untouched by this lane. Their frames gain a shadow, and the frame is outside the
`overflow-x-auto` wrapper, so the scroll geometry those tests measure is unchanged.

## The work, as four commits

The order matters: it is what makes the lane's two competing zero-diff constraints separately
checkable. The five migration-protected files are `WorkersTable.test.tsx`, `UsersTable.test.tsx`,
`EnrollmentsTable.test.tsx`, `ReservationsTable.test.tsx` and `TasksTable.test.tsx`.

### Commit 1 - caret out of the accessible name

`web/src/components/holo/Table.tsx`: in the sortable-header branch, render the caret inside a span
carrying `aria-hidden="true"` instead of as a bare text node. `sortCaret` is unchanged. The leading
space stays inside the span so the rendered text is byte-identical.

Test files re-pointed from the glyph to `aria-sort` on the `columnheader`:

- `WorkersTable.test.tsx` - `shows a descending caret on the active sort column`.
- `UsersTable.test.tsx` - `descending sort shows a descending caret`.
- `EnrollmentsTable.test.tsx` - `aria-sort marks the active column and caret direction follows the
  sort` and `ascending sort shows an ascending caret`.
- `InvitesTable.test.tsx` - `renders all six headers`, `only CREATED and EXPIRES are sortable...`
  and `ascending sort shows an ascending caret`. The two anchored `columnheader` regexes exist to
  tell CREATED from CREATED BY; they must stay anchored and exact after the glyph is dropped, or
  they resolve two elements.
- `Table.test.tsx` - `the caret follows the active sort direction`.

Renaming these tests is expected: several currently name the caret, and after this commit they are
about `aria-sort`. `ReservationsTable.test.tsx` must **not** change.

### Commit 2 - accessible names

- `RevokedWorkersTable.tsx`: `aria-label="Revoked workers"` on the `<table>`.
- `Panel.tsx`: pass `data-panel-title` through to the `GlassPanel` root when `title` is a string.
- `WorkspacesPanel.tsx` and `WorkerTasksPanel.tsx`: export a title constant each, pass it as
  `Table`'s `label`, and drop the "aria-label matches the visible title" comments, which become
  false once the string is shared rather than duplicated.
- `WorkerDetailPage.tsx`: import both constants and use them as the two `Panel` titles.
- Tests: a new name assertion in `RevokedWorkersTable.test.tsx`, and the structural panel-name test
  in `WorkerDetailPage.test.tsx`. `Panel.test.tsx` gains one assertion for the new attribute.

### Commit 3 - TasksTable stops implying selection semantics

- `Table.tsx`: `TableRow` drops `as` and `type` from `TableRowProps` and always renders a `div`. A
  short comment states the hazard (`aria-selected` is not surfaced under `role="table"`, and an
  interactive row element would replace the row role) and cites the guard test by name.
- `TasksTable.tsx`: the row becomes a plain `TableRow` with `onClick` and no `aria-selected`; the
  name cell holds `<button type="button">` with the task name as its content and
  `aria-current={selected ? 'true' : undefined}`; the file header comment is rewritten to describe
  the new arrangement including the single-handler-by-bubbling rule.
- `JobDetailPage.tsx`: the tablist's accessible name carries the selected task's name.
- `Table.test.tsx`: the `as`/`type`/`aria-selected` forwarding test is replaced by one asserting
  `TableRow` renders a `div`, still forwards `data-*` and `onClick`, and still cannot have its role
  overridden - plus a `@ts-expect-error` line pinning that `as` is no longer accepted, compiled by
  the `tsc -b` gate (precedent: the `SortFieldOf` pin in `web/src/lib/toggleSort`).
- `TasksTable.test.tsx` and `JobDetailPage.test.tsx`: re-pointed off `aria-selected`.
- `web/e2e/keyboard.spec.ts`: a new describe block for the job-detail task selection, at the default
  viewport.

### Commit 4 - visual harmonization

- `UsersTable.tsx`, `EnrollmentsTable.tsx`, `ReservationsTable.tsx`, `InvitesTable.tsx`,
  `WorkersTable.tsx`: the hand-rolled frame div becomes `<GlassPanel>`.
- Eleven `headerClassName` values become the two from Decision 4.
- `JobsTable.tsx`, `SchedulesTable.tsx`, `WorkersTable.tsx`: row `px-4` becomes `px-[18px]`.
- No test file changes at all. In particular **none of the five protected files**, which is this
  commit's own proof obligation.

## Systems lens

**Load and rendering cost.** No new network requests, no new query keys, no change to any poll
cadence. The only DOM growth is one `<button>` per task row in `TasksTable`. A job's tasks arrive
embedded in `GET /v1/jobs/{id}`, so the row count is the job's task count and is not paginated -
this lane does not change that, but it does add a constant factor to it. For a thousand-task job
that is a thousand extra button elements; the row divs, five cells and their text already dominate,
so the marginal cost is small and the pre-existing unbounded-row-count question is unchanged and out
of scope.

**Failure modes.** Every change is a static render change. The one behavioural change is the
single-handler-by-bubbling rule in `TasksTable`: if a future edit adds `stopPropagation` inside the
name cell, or moves the row handler onto the button, selection breaks silently for one input mode
and not the other. That is the failure this lane's keyboard e2e test exists to catch, and it is the
reason the rule gets a comment.

**Threat model.** No new authenticated surface, no new parameter reaching the server, no new stored
value. Two places render server-supplied strings into ARIA-adjacent positions: the task name as the
button's content (already rendered as text today) and the task name inside the tablist's
`aria-label`. Attribute values are not parsed as markup and React escapes them, so a hostile task
name is a nuisance in an announcement, not an injection. Worth stating rather than assuming, per the
per-branch-sanitiser lesson: nothing here renders input-derived **markup**, and no new branch
renders input-derived content that a closed set could not describe.

**Invariants.** The backend invariants (epoch fence, single job-spec pipeline, one bounded sender,
identity-checked teardown, no interior pointers across locks, single JSON entry point) are not
touched: this lane makes zero Go changes. The frontend-shaped invariant - end the generation before
releasing the resource - has no instance here, because nothing in this lane creates, aborts or
releases an async resource. `Table`'s own local invariants are preserved and one is strengthened:
the `rest`-spreads-before-`role` ordering in `TableRow` and `TableCell` stays exactly as it is, and
removing `as` removes the only route by which a caller could change the element type under that
role.

## Testing and acceptance criteria

### New and changed tests, with the mutation each kills

Unit (vitest, jsdom):

1. `Table.test.tsx`, **new**: `the sort caret is hidden from the header's accessible name`. Renders
   a sortable header with a descending sort and asserts `getByRole('button', { name: 'NAME' })`
   resolves and `getByRole('columnheader', { name: 'NAME' })` resolves.
   **Kills:** deleting `aria-hidden="true"` from the caret span - the name becomes the label plus the
   glyph and both exact queries fail.
2. `Table.test.tsx`, **new**: `the caret is still rendered for sighted users`. Asserts the
   `columnheader`'s `textContent` contains the direction glyph in both directions.
   **Kills:** deleting the caret span entirely, or making `sortCaret` return the empty string - the
   mutation that "fixes" the name by removing the affordance.
   Both 1 and 2 are required: neither alone distinguishes "hidden" from "gone".
3. `RevokedWorkersTable.test.tsx`, **new**: asserts `getByRole('table', { name: 'Revoked workers' })`.
   **Kills:** removing the `aria-label`.
4. `WorkerDetailPage.test.tsx`, **new**: `every table on the page is named by its own panel title`.
   Walks each `role="table"` to `closest('[data-panel-title]')`, asserts the accessible name equals
   the attribute, and asserts exactly two tables were examined.
   **Kills:** changing either panel's `Table` label without changing its `Panel` title (the exact
   drift the item describes), and - via the count assertion - a future panel that renders a table
   with no panel title, which would otherwise make the loop pass vacuously.
5. `Table.test.tsx`, **replaced**: `TableRow always renders a div and cannot have its role
   overridden`, plus a `@ts-expect-error` line for `as`.
   **Kills:** re-adding an element-type escape hatch to `TableRow`. The type-level half is checked
   by `tsc -b`, not by vitest.
6. `TasksTable.test.tsx`, **replaced**: `the selected task's control is marked aria-current and no
   row carries aria-selected`. Asserts exactly one element in the table has `aria-current="true"`,
   that its accessible name is the selected task's name, and that
   `container.querySelectorAll('[aria-selected]')` is empty.
   **Kills:** putting `aria-current` on every row, putting it on none, and re-introducing
   `aria-selected`.
7. `TasksTable.test.tsx`, **new**: `each task row exposes a button named for the task`. Asserts
   `getByRole('button', { name: 'denoise' })` and that clicking it calls `onSelect` exactly once with
   that id.
   **Kills:** the double-dispatch mutation (adding an `onClick` to the button while the row keeps
   its own) - the call count goes to two - and the mutation that drops the button and leaves a bare
   text cell.
8. `JobDetailPage.test.tsx`, **replaced**: `selecting a task drives the spec pane and names the
   tabs`. Clicks the row for `frame-001`, asserts the spec pane content changed and that the
   `tablist`'s accessible name contains `frame-001`.
   **Kills:** reverting the tablist label to a fixed string, and any change that lets the panes and
   the marked selection disagree.
9. `Panel.test.tsx`, **new assertion**: the panel root carries `data-panel-title` equal to a string
   title and omits it for a node title.
   **Kills:** dropping the attribute, which would silently make test 4 examine zero tables - caught
   there too, by the count assertion, which is the intended belt and braces.

Browser (Playwright, chromium and webkit), in `web/e2e/keyboard.spec.ts`:

10. **new describe**, `job-detail task selection`, at the default viewport, on the populated
    `job-detail` surface (tasks alpha, beta, gamma):
    - a real Tab press sequence reaches a control whose accessible name is `alpha`, within a bounded
      number of presses, following the existing file's pattern;
    - pressing Enter on it leaves exactly one element inside the tasks table with
      `aria-current="true"`, and it is that control;
    - no element inside the tasks table has an `aria-selected` attribute.
    **Kills:** every version of this decision that only changes attributes in jsdom while leaving the
    row unreachable or unactivatable by a real key press - which is precisely the gap the existing
    file's own header comment says jsdom attribute assertions cannot close.

### Zero-diff proofs

Measured per commit, each against that commit's own parent:

- **Commit 4 touches none of the five protected test files.** `git show --stat` on commit 4 must
  list no `.test.tsx` file at all. This is the visual item's acceptance criterion, stated as a
  mechanical check.
- **Commit 1 touches exactly three of the five** (`WorkersTable`, `UsersTable`, `EnrollmentsTable`),
  plus two unprotected files (`InvitesTable.test.tsx`, `Table.test.tsx`). This is the caret item's
  explicit allowance, and the two extra files are the refutation above.
- **Commit 3 touches exactly one of the five** (`TasksTable.test.tsx`). This is the role item's
  explicit allowance.
- **`ReservationsTable.test.tsx` is unchanged across all four commits.** `git log --oneline` scoped
  to that path over the lane's range must be empty. This is a free proof that the caret is still
  rendered and that no header class assertion was disturbed.

### Gates

`web/node_modules` is **not installed** in this worktree. The implementing engineer runs
`cd web && npm ci` first; the spec author did not, by instruction.

- `cd web && npm test` - the full vitest suite, green.
- `cd web && npx tsc -b --force` - clean, and this is the gate that checks the `@ts-expect-error`
  pin for the removed `as` prop.
- `cd web && npm run build` - the production bundle builds.
- `make test-e2e` from Git Bash, with Postgres running at `postgres://relay:relay@127.0.0.1:5432`
  (`docker start relay-postgres`, or `scripts/dev.ps1` once to create it) and the browsers installed
  once (`cd web && npx playwright install chromium webkit`). If `make` is not on PATH, use the MSYS2
  copy with the variable forwarding documented in `web/e2e/README.md`.
- `git checkout -- web/dist/` before assembling the PR. `web/dist` is tracked and stale, and
  `make test-e2e` writes into it.

### The screenshot pass, and what a human compares

There are no visual assertions anywhere in this repo, so commit 4's correctness is established by a
human looking at pixels. `make test-e2e` writes one full-page PNG per surface per width; four of the
five affected surfaces are covered:

- `admin-users`, `admin-invites`, `admin-enrollments`, `admin-reservations`: the panel must now
  carry the same drop shadow and inset highlight as the jobs panel. Compare against the `jobs`
  screenshot at the same width.
- `jobs`, `schedules`: header labels sit 2px further in and are more widely tracked; column labels
  must still sit directly above their own column data, which is the only thing that could actually
  break here.
- `job-detail`: the tasks table header is slightly taller and more widely tracked.

The fifth is not covered and cannot be: **`workers` is an empty-state surface**, so `WorkersTable`
is never rendered by the harness, and the one genuinely visible change in the item - the flat frame
becoming the gradient-plus-shadow surface - appears in no screenshot. A human must run the app
against a database with at least one worker row, open `/workers` in table view beside `/jobs` at
1280, and confirm the two panels now read as one surface: same gradient direction and stops, same
1px inset highlight along the top edge, same drop shadow. State in the PR body whether that was
done; if it was not, say so rather than implying the screenshots covered it.

## Escalations

Calls a human might reasonably make the other way. The first two are the most likely.

1. **Decision 2, the TasksTable role.** A human who wants relay's tables to be full ARIA grids
   eventually would take option (a) instead and accept the keyboard model as this lane's real work.
   The counter-argument is in the decision; the trigger to revisit, recorded so it is re-evaluated
   rather than re-argued: if the tasks table ever gains per-cell interaction (an inline editable
   cell, a per-cell action), the grid calculus flips and `role="grid"` becomes correct. Nothing short
   of that.
2. **Decision 4, question B, `WorkersTable` adopting the gradient.** This is the only change in the
   lane a user will notice, and it has no browser coverage. A human might want it split into its own
   PR, or held until the e2e harness can render a worker row.
3. **Decision 4, question D, moving row horizontal padding.** A human might prefer three
   `headerClassName` values and zero row changes, on the grounds that row padding is out of the
   item's stated scope. That trade is: strictly smaller diff, misses the item's own "one or two"
   criterion, and leaves the header treatment permanently split three ways.
4. **Decision 3, question A.** A human might want a visible `<caption>` on `RevokedWorkersTable`,
   which is the native choice the item names first.
5. **Decision 2's announcement mechanism.** A human might want a `polite` live region announcing
   the selected task in addition to `aria-current`, for maximal screen-reader coverage. It was left
   out because `aria-current` is the supported mechanism for exactly this meaning and a live region
   would announce on every poll-driven re-render unless carefully gated.
6. **The `TableRow` `as`/`type` removal.** A human might keep the props and add a runtime throw when
   `aria-selected` is passed under `role="table"`, rather than removing the element-type escape
   hatch. Removal was chosen because `tsc` reaches call sites a runtime guard reaches only when the
   code path executes.

## Backlog proposals

Noticed while verifying this lane, out of scope, **not filed** - proposed for the human to accept or
reject:

- **The Spec/Log tabs have no tabpanel.** `JobDetailPage` renders `role="tablist"` and two
  `role="tab"` buttons with `aria-selected`, but the panel below is a plain `GlassPanel` with no
  `role="tabpanel"`, no `id` and no `aria-controls`. This is the same
  advertised-but-unfulfilled-contract shape as the UserMenu precedent, on a different surface.
- **No automated a11y rule engine under `web/`.** `aria-selected` under `role="table"` is a rule
  axe-core ships; so is `scrollable-region-focusable`, which this codebase found by review rather
  than by tooling. An axe pass wired into the existing Playwright harness would have caught the
  TasksTable item at authoring time.
- **Row density still diverges from the hi-fi.** The hi-fi's top-level list rows are
  `padding:'10px 18px'`; after this lane the app's are 18px horizontal and 8px vertical. Closing the
  vertical half is a visible change that belongs with a wider density pass.
- **`WorkersTable` has no populated e2e coverage of any kind**, which is a special case of the
  known slice-1 limit and the reason this lane's most visible change ships unwatched by the harness.

## Open risks

- **The `aria-current` announcement is not measured in this lane.** The e2e test asserts the
  attribute is on the right element after a real key press; it cannot assert what a screen reader
  says. Playwright's webkit is not Safari and the harness has no AT. The claim "the selection
  announces" rests on `aria-current` being a supported ARIA state, not on an observation made here.
  Say that plainly in the PR body rather than claiming the announcement was verified.
- **Commit 4's zero-diff proof is a claim about a commit, not about the tree.** If the four commits
  are squashed at merge, the proof survives only in this spec and in the PR's commit list. Keep the
  four commits distinct on the branch.
