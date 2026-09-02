# Header nav narrow-viewport collapse - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collapse the four top-level header destinations into an ARIA disclosure below the `md`
breakpoint, so that at 320px and 375px every destination is either visible or behind a visible
control, while the header at 1280px stays byte-identical to what ships today.

**Architecture:** One copy of the links, always mounted inside the `<nav>` landmark, wrapped in a
panel `<div>` whose presentation is switched entirely by CSS (`max-md:` overrides on top of an
unchanged desktop base). A plain `<button>` toggle sits before the panel in DOM order. All open/close
behaviour is a deliberate transcription of `web/src/shell/UserMenu.tsx`'s handler set: Escape,
outside mousedown, focusout containment, modifier-click guard, containment-checked focus restore. No
`matchMedia`, no `ResizeObserver`, no width state, no breakpoint constant in TypeScript.

**Tech Stack:** React 18.3.1, react-router-dom 7, Tailwind CSS 4.3.0 (`@tailwindcss/vite`), vitest +
jsdom + @testing-library, Playwright 1.62.1 (chromium + a `@webkit`-tagged subset).

**Spec:** `docs/superpowers/specs/2026-09-01-header-nav-narrow-viewports-design.md`

---

## Slice independence declaration

**This slice is FRONTEND-ONLY. There is no backend slice and no backend dependency.**

Nothing here touches Go, SQL, protobuf or any handler. No `make generate` step exists in this plan.
The only Go interaction is indirect: `make test-e2e` rebuilds `relay-server` so the freshly built
`web/dist` is embedded, which is a build step, not a code change.

Consequently there is nothing to parallelise in Phase 3 and nothing to sequence against. The whole
plan runs as one lane, in the task order below, by one engineer.

## Scope check

One subsystem (the app shell header) plus its two test lanes. One PR. This is not a multi-stage plan
and it must **not** be handed to `/backlog phases`.

## Things the engineer must NOT do

- **Do NOT run `/backlog close`.** This slice closes
  `docs/backlog/bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports.md`, and the conductor runs
  the close command. Do not hand-edit the item's `status` either.
- **Do NOT stage `web/dist`.** Every commit in this plan names its paths explicitly. Never
  `git add -A`, never `git add .`, never `git add web/`. `web/dist/index.html` is a tracked
  placeholder that `npm run build` overwrites; restore it with
  `git checkout -- web/dist/index.html` after any build, and never let it into a commit.
- **Do NOT spell a `max-md:` or `md:` class literal anywhere under `web/` except inside a
  `className` attribute in `web/src/shell/HoloShell.tsx`.** Tailwind v4's scanner reads every file
  under the Vite root (`web/`) for class-shaped substrings, comments and test files included, and
  emits CSS for each one it finds. A literal in a test file or a comment keeps the rule alive
  independently of the component that is supposed to own it, which would make Task 3's post-build
  check vacuous. Task 2 gives the exact form the pin test must use instead. This plan document lives
  in `docs/`, outside the Vite root, so it spells them freely - that asymmetry is the point.
- **Do NOT reach for `matchMedia`, `ResizeObserver`, a width in state, or a breakpoint number in
  TypeScript.** The spec's "Out of scope" section says that if a task needs one, the design is being
  re-litigated and the plan should stop. Stop and report instead.
- **Do NOT restructure `UserMenu`, `DialogShell`, `Table`, the tab bars, or extract a shared
  disclosure primitive.** The one permitted `UserMenu` edit is a single comment line in Task 8.

## Contradictions found in the spec, and how this plan resolves them

Read the spec once for contradiction before planning. Four things were found. None invalidate the
design; three change what a task must do.

1. **"`HoloShell.test.tsx` holds four tests" is a miscount - it holds five.** The enumeration that
   follows it is correct (three nav-entry tests, one stacking pin, one scroll-container test), so
   the number is the only error. No effect on any task.
2. **"The repo uses `md:` (four sites)" is a miscount - there are six `md:`-prefixed class strings
   across five files** (`workers/WorkerDetailPage.tsx` x2, `schedules/ScheduleDetailPage.tsx`,
   `admin/server/StatSection.tsx` x2, `admin/server/ServerTab.tsx`). The load-bearing half of the
   claim - `md:` is the established convention and no `max-*` variant exists in `web/src` today - is
   correct and was verified (`max-(md|sm|lg|xl):` returns zero matches anywhere under `web/` outside
   `node_modules`).
3. **"header height unchanged from today" in the Behaviour-by-width table is probably false, and is
   pinned by nothing.** Below `md` the panel is out of flow (`max-md:absolute`), so the tallest
   in-flow item in the header becomes the `Menu` pill (`py-1`, `text-[10px]`) beside the user chip,
   rather than today's nav links (`py-[7px]`, `text-[13px]`). The header will most likely get
   **shorter** below `md`. Nothing in either lane asserts header height, and a shorter header is not
   a defect, so no task changes. It is recorded here so nobody cites the table as a measured claim.
4. **AC12's predicate, taken literally, throws a Playwright strict-mode violation on four
   surfaces.** `page.getByRole('link', { name: 'Jobs' })` matches by accessible name, and Playwright's
   default is a case-insensitive **substring** match (`web/e2e/auth.spec.ts:83-86` states this
   explicitly). `JobDetailPage`, `NewJobPage`, `WorkerDetailPage` and `ScheduleDetailPage` each render
   a breadcrumb link whose accessible name contains "Jobs" / "Workers" / "Schedules", so an unscoped
   locator resolves two elements on `job-detail`, `job-new`, `workers/:id` and `schedule-detail`.
   **Resolution:** the AC12 helper scopes every destination locator to `page.getByRole('banner')` and
   passes `exact: true`. The spec's own wording ("if the **header** link is visible") already implies
   the scope; this plan makes it explicit because the unscoped form fails.

Nothing else in the spec was refuted. Everything it asserts about the tree at HEAD was checked and
holds: the `<nav>` (not the `<header>`) is the scroll container; the `<header>` carries `relative z-10`
and `<main>` carries `relative z-0`; `expect(header.className).not.toMatch(/\boverflow-/)` is the last
assertion of `the nav is the only shrinkable scroll container in the header`; `UserMenu` owns exactly
the handler set described; `--color-popover` exists in `web/src/theme/tokens.css`; `tokens.css`
overrides no breakpoint, so `md` is Tailwind's default 48rem; `web/src` ships no icon component;
`/auth` is the only `anonymous` surface and renders no shell; `keyboard.spec.ts` is the `@webkit`
precedent and `playwright.config.ts` greps `/@webkit/` for that project.

**The `max-*` variant is confirmed registered in the installed Tailwind.**
`web/node_modules/tailwindcss/package.json` reads `"version": "4.3.0"`, and
`web/node_modules/tailwindcss/dist/lib.mjs` line 16 contains
`functional("max",(p,v)=>{...p.nodes=[W("@media",`(width < ${h})`,p.nodes)]},{compounds:1})`, so
`max-md:` compiles to `@media (width < 48rem)`. Task 3 confirms this against a real build rather
than leaving it as a source reading.

## Deviations from the spec, and why

1. **AC12's locators are header-scoped and exact.** Reason 4 above.
2. **`layout.spec.ts`'s test title is conditional on `s.anonymous`, and the auth surface keeps
   running.** The spec says the reachability check is "skipped when `s.anonymous`". Skipping the
   whole *test* for `auth` would delete the harness's control surface, which `surfaces.ts:122-126`
   says is "what makes a header/main finding an attribution rather than a correlation". So the
   reachability *assertion* is skipped and the three overflow assertions still run - and the title
   changes with it, because a title claiming reachability over a body that does not assert it is the
   test-honesty problem the spec's own D11 rejects.
3. **The AC11 and AC10 class pins spell their breakpoint prefix as a separate constant instead of
   one literal string, and AC17 gains a discriminating control.** This is the largest deviation and
   it is a correctness fix. As written, AC17 ("grep the built CSS for the emitted `max-md:` rules")
   cannot establish what it claims: `HoloShell.test.tsx` lives under the Vite root, so its own pin
   strings would emit those exact rules into the production bundle, and the grep would pass with the
   classes deleted from `HoloShell.tsx`. Task 2 therefore writes the pins as `NARROW + 'absolute'`
   (with `const NARROW = 'max-md:'`), so the only literal `max-md:absolute` under `web/` is in
   `HoloShell.tsx`; and Task 3 runs the A/B that proves it, by stripping the classes from
   `HoloShell.tsx` alone and requiring the rules to disappear. If they do not disappear, AC17 is
   reported as establishing only that the variant compiles, never as a producer check.
4. **AC11 also pins the panel's `hidden` / `flex` state class.** That pair is the entire collapse
   mechanism and jsdom can see it, so leaving it unpinned would let a silent deletion of the state
   switch through the unit lane. Two extra assertions in a test that already exists.
5. **Task 11 updates `web/e2e/README.md`.** Not in the spec's AC list. The README currently states
   that the `scrollWidth <= clientWidth` gap's live instance *is* this bug, and after this slice that
   sentence is wrong prose about correct code. The spec's own follow-up proposal 4 says the slice
   "removes the instance and leaves the gap", so the README must say the gap remains without citing a
   closed bug as its instance.

## File structure

**Modify:**

- `web/src/shell/HoloShell.tsx` (85 lines at HEAD) - the whole implementation. Adds the disclosure
  state and five handlers, restructures the `<nav>` subtree, moves two classes off the `<nav>`.
- `web/src/shell/HoloShell.test.tsx` (100 lines at HEAD) - edits the two class pins inside
  `the nav is the only shrinkable scroll container in the header` (the guard line stays
  byte-identical) and appends eleven new tests. The three nav-entry tests are **not** edited.
- `web/src/shell/UserMenu.tsx` - one comment line only (Task 8).
- `web/e2e/layout.spec.ts` (100 lines at HEAD) - conditional test title, reachability check,
  open-state overflow assertion and open-state screenshot.
- `web/e2e/README.md` - one paragraph (Task 11).

**Create:**

- `web/e2e/nav.ts` - `expectDestinationsReachable(page)` and `closeNavPanel(page)`, shared by
  `layout.spec.ts` and `header-nav.spec.ts`. A helper module, not a spec: Playwright only collects
  `*.spec.ts` from `testDir`, and `tsconfig.json` already includes `e2e`, so this file is
  type-checked by `npx tsc -b`.
- `web/e2e/header-nav.spec.ts` - the breakpoint-value tests (chromium) and the real-key-event tests
  (`@webkit`-tagged, so both engines).

**Create outside the repo (never committed, never under `web/`):**

- an emitted-CSS checker script, used by Task 3 only.

**Never touched:** `web/src/app/*`, `web/src/theme/tokens.css`, `web/src/components/**`,
`web/e2e/surfaces.ts`, `web/e2e/keyboard.spec.ts`, `web/playwright.config.ts`, `web/dist/**`,
anything outside `web/` other than this plan and the backlog item the conductor closes.

## Command reference

Run these from the worktree root `D:/dev/relay/.claude/worktrees/web-a-header-nav` unless a step says
otherwise.

| what | command | shell |
|---|---|---|
| one unit test file | `cd web && npx vitest run src/shell/HoloShell.test.tsx` | PowerShell or bash |
| one unit test by name | `cd web && npx vitest run src/shell/HoloShell.test.tsx -t "<test title>"` | PowerShell or bash |
| the whole unit lane | `cd web && npm test` | PowerShell or bash |
| type-check (src + e2e + config) | `cd web && npx tsc -b` | PowerShell or bash |
| production build | `cd web && npm run build` | PowerShell or bash |
| the browser lane | see Task 12 | **Git Bash only** |

`npm run build` is `tsc -b && vite build`, so it covers the type-check too. After any build,
`web/dist/index.html` is modified; `web/dist/assets/` is gitignored (`.gitignore:7-8`).

---

### Task 1: The panel and the toggle

**Files:**
- Modify: `web/src/shell/HoloShell.tsx`
- Modify: `web/src/shell/HoloShell.test.tsx`

This task introduces the structure and the open state. It has to move `min-w-0` and `overflow-x-auto`
off the `<nav>` in the same breath, because the element those pins describe has moved - which is
exactly what AC10 calls out. The guard line
`expect(header.className).not.toMatch(/\boverflow-/)` stays byte-identical.

- [ ] **Step 1: Write the failing test**

Append to `web/src/shell/HoloShell.test.tsx`:

```tsx
// AC1. The collapsed nav is an ARIA disclosure, and its panel is ALWAYS mounted -
// which is why aria-controls is present in both states here and only while open in
// UserMenu, whose panel is conditionally mounted. An IDREF to a node that does not
// exist is an authoring error; an IDREF to a node that is merely display:none is
// not. A reviewer "fixing" this into agreement with UserMenu would be wrong.
test('the nav toggle exposes disclosure semantics in both states', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  const panel = screen.getByTestId('header-nav-panel')

  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(toggle).toHaveAttribute('aria-controls', panel.id)
  expect(panel.id).toBeTruthy()

  await userEvent.click(toggle)

  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(toggle).toHaveAttribute('aria-controls', panel.id)
})
```

The file has no `userEvent` import at HEAD. Change the import block at the top of
`web/src/shell/HoloShell.test.tsx` from:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
```

to:

```tsx
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
```

(`within` is used by a later task; adding it now avoids a second edit to the same line.
`noUnusedLocals` applies to `web/src`, and an unused *import binding* from a module is reported by
`tsc`, so if you run `npx tsc -b` before the task that uses `within`, expect that one error and
ignore it until Task 7. If you would rather not, add `within` in Task 7 instead.)

- [ ] **Step 2: Run it and watch it fail for the right reason**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "the nav toggle exposes disclosure semantics in both states"
```

Expected: FAIL with `Unable to find an accessible element with the role "button" and name /menu/i`.
That is the right reason: there is no toggle at HEAD. If it instead fails on `getByTestId`, you have
a toggle and no panel, which is not the state HEAD is in - stop and re-read.

- [ ] **Step 3: Write the minimal implementation**

Replace the whole of `web/src/shell/HoloShell.tsx` with this. `NAV`, the admin filter, `onLogout`,
`<main>`, the `<header>`'s own classes and both existing comment blocks are unchanged; the diff is
the import block, the four new state/ref lines, the `navPanelClass` constant, and the `<nav>`
subtree. The `max-md:` classes are deliberately **not** here yet - they are Task 2.

```tsx
import { useId, useRef, useState, type ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { Eyebrow } from '../components/holo'
import { UserMenu } from './UserMenu'

const NAV = [
  { to: '/jobs', label: 'Jobs' },
  { to: '/workers', label: 'Workers' },
  { to: '/schedules', label: 'Schedules' },
  // Cosmetic gate only - AdminRoute redirects and the server's AdminOnly
  // middleware is the real boundary. Hiding it keeps non-admins out of a route
  // that would only 403 for them.
  { to: '/admin', label: 'Admin', adminOnly: true },
]

export function HoloShell({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth()
  const navigate = useNavigate()
  const nav = NAV.filter((n) => !n.adminOnly || user?.is_admin)

  const [navOpen, setNavOpen] = useState(false)
  const navRef = useRef<HTMLElement>(null)
  const navToggleRef = useRef<HTMLButtonElement>(null)
  const navPanelId = useId()

  async function onLogout() {
    await logout()
    navigate('/auth')
  }

  // ONE copy of the links, always mounted, switched between an inline row and a
  // dropdown by CSS alone. Two copies would put two links named "Jobs" in the
  // accessibility tree and would break every getByRole('link', { name }) query in
  // this file's tests, which throw on multiple matches. jsdom applies none of this
  // app's CSS, so no unit test can tell the collapsed state from a broken one -
  // the browser lane owns that claim.
  //
  // `hidden` with `md:flex` is the Tailwind idiom: the variant rule is emitted
  // after the base utility, so md:flex wins at and above the breakpoint whatever
  // the open state is.
  const navPanelClass = `min-w-0 gap-0.5 md:flex md:overflow-x-auto ${navOpen ? 'flex' : 'hidden'}`

  return (
    <div className="min-h-screen bg-bg text-fg">
      {/* `relative z-10` here is what keeps UserMenu's dropdown visible. The
          dropdown hangs out of this header and over <main>, which comes LATER in
          the document and whose panels create stacking contexts of their own
          (every GlassPanel carries backdrop-blur), so at z-auto the page content
          paints over the open menu. The z-index has to be declared out HERE, on
          the header, not on the dropdown: this header's own backdrop-blur makes
          it a stacking context, which confines any z-index inside it. Measured
          in Chrome over 275 hit-test points across the open dropdown - with the
          dropdown's own z-50 and nothing else, 220 of them still returned a page
          panel; with `z-10` here and no z-index on the dropdown at all, 0 did.

          `relative z-0` on <main> does NOT fix today's bug (z-10 alone measured
          0/275 with <main> untouched). It is a guard on the next one: it wraps
          every descendant z-index in one stacking context, so a page-level
          z-index can never climb over the header. Measured the same way with a
          `relative z-20` added to a page panel - 99/275 occluded without this
          class, 0 with it.

          Dialogs are unaffected either way: they portal to a layer appended to
          <body>, outside both siblings, and keep their z-50 above them. */}
      {/* Narrow-viewport rule for this header (measured 2026-08-13): the nav
          panel is the ONLY element allowed to become a scroll container. The
          dropdown that UserMenu hangs below this header would be clipped by an
          overflow declared here, which is the same stacking behaviour the comment
          above measures. See
          docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md. */}
      <header className="relative z-10 flex items-center justify-between gap-3 border-b border-border bg-white/[0.025] px-[22px] py-3 backdrop-blur-[10px]">
        <div className="flex min-w-0 items-center gap-6">
          <Eyebrow className="text-accent">RELAY</Eyebrow>
          {/* The landmark wraps the toggle as well as the links, so it exists at
              every width even while the panel is collapsed, and aria-label names
              it now that it contains a control. It must NOT become positioned:
              the panel anchors to the <header>, which is already `relative`. */}
          <nav ref={navRef} aria-label="Main" className="min-w-0">
            <button
              ref={navToggleRef}
              type="button"
              onClick={() => setNavOpen((v) => !v)}
              aria-expanded={navOpen}
              // Present in BOTH states, unlike UserMenu's, because this panel is
              // always mounted so the IDREF always resolves.
              aria-controls={navPanelId}
              className={`md:hidden rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${navOpen ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}`}
            >
              Menu
            </button>
            {/* min-w-0 lets this shrink below its content (a flex item's automatic
                minimum is its content width, which is what made the header a 523px
                floor); md:overflow-x-auto then makes every route reachable by
                scrolling at and above the breakpoint. Inert at any width where the
                links fit: a scroll container with no overflow renders no scrollbar
                and no visual difference. */}
            <div id={navPanelId} data-testid="header-nav-panel" className={navPanelClass}>
              {nav.map((n) => (
                <NavLink
                  key={n.to}
                  to={n.to}
                  className={({ isActive }) =>
                    `border-b-2 px-[14px] py-[7px] text-[13px] tracking-[0.02em] transition-colors ${
                      isActive
                        ? 'border-accent text-fg'
                        : 'border-transparent text-fg-mute hover:text-fg'
                    }`
                  }
                >
                  {n.label}
                </NavLink>
              ))}
            </div>
          </nav>
        </div>
        <UserMenu email={user?.email ?? ''} onLogout={onLogout} />
      </header>
      <main className="relative z-0 p-5">{children}</main>
    </div>
  )
}
```

- [ ] **Step 4: Move the two class pins in the existing scroll-container test**

In `web/src/shell/HoloShell.test.tsx`, inside
`test('the nav is the only shrinkable scroll container in the header', ...)`, replace these three
lines:

```tsx
  const nav = screen.getByRole('navigation')
  expect(nav).toHaveClass('min-w-0', 'overflow-x-auto')
  // A flex item's automatic minimum size is its content, so the group holding the
  // wordmark and the nav cannot shrink at all without this.
  expect(nav.parentElement).toHaveClass('min-w-0')
```

with:

```tsx
  // The scroll container MOVED and the rule did not. The links now live in an
  // always-mounted panel inside the <nav>, so the two pins that described the
  // <nav> describe the panel, and the scroll is scoped to md and up because below
  // it the panel is the dropdown and must not scroll. The whole shrink chain is
  // asserted, not just the leaf: a flex item's automatic minimum size is its
  // content, so panel, <nav> and the group holding the wordmark all need min-w-0
  // or the header gets a content floor back.
  const panel = screen.getByTestId('header-nav-panel')
  expect(panel).toHaveClass('min-w-0', 'md:overflow-x-auto')
  const nav = panel.parentElement as HTMLElement
  expect(nav).toHaveAttribute('aria-label', 'Main')
  expect(nav).toHaveClass('min-w-0')
  expect(nav.parentElement).toHaveClass('min-w-0')
```

Leave the comment block above the test and the final two lines
(`const header = screen.getByRole('banner')` and the `not.toMatch(/\boverflow-/)` assertion) exactly
as they are.

Note this pin spells `md:overflow-x-auto` as a literal. Task 2 replaces it with the prefix-constant
form for the reason given in Deviation 3; it is written literally here so this step is one
mechanical substitution.

- [ ] **Step 5: Run the whole file and watch it pass**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx
```

Expected: PASS, 6 tests. The three nav-entry tests must be untouched and green (AC9) - if any of
them needed an edit, the DOM contract has drifted from the design and you must stop.

- [ ] **Step 6: Commit**

```
git add web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
git commit -m "feat(web): mount the header nav links in a disclosure panel" -- web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
```

---

### Task 2: The collapse classes

**Files:**
- Modify: `web/src/shell/HoloShell.tsx`
- Modify: `web/src/shell/HoloShell.test.tsx`

- [ ] **Step 1: Write the failing test**

Append to `web/src/shell/HoloShell.test.tsx`:

```tsx
// The breakpoint prefixes are spelled as a constant plus a bare suffix, never as
// one literal string, and that is load-bearing rather than stylistic. Tailwind v4
// scans every file under web/ for class-shaped substrings - test files included -
// and emits a rule for each one it finds. A literal here would put these rules in
// the production bundle on its own, so the post-build check that is supposed to
// attribute them to HoloShell.tsx would pass with the classes deleted from the
// component. The bare suffixes still emit their own unprefixed utilities, which is
// harmless dead CSS.
const NARROW = 'max-md:'
const WIDE = 'md:'

// REGRESSION PIN: the collapsed nav is a full-bleed opaque panel below md and
// inline above it.
//
// A PIN, NOT A GUARD. jsdom applies no CSS and does no layout, so nothing here
// evaluates a breakpoint, a position or a fill - every one of these classes was
// chosen for an effect only a browser can show. Its whole job is to make a silent
// deletion visible in this lane. The behaviour is header-nav.spec.ts's.
//
// Full bleed rather than a fixed-width panel anchored at the nav: a 224px panel
// starting past the wordmark at a 320px viewport reaches beyond the viewport edge
// and re-creates the document overflow the previous narrow-viewport slice closed.
// left-0 with right-0 cannot overflow by construction. The <header> is the
// positioned ancestor it anchors to.
//
// The bg fill is load-bearing for the same reason it is in UserMenu: GlassPanel
// and the header set no background-color at all, so a panel floating over live
// content without its own fill reads straight through.
test('REGRESSION PIN: the collapsed nav is a full-bleed opaque panel below md and inline above it', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  const panel = screen.getByTestId('header-nav-panel')

  expect(toggle).toHaveClass(WIDE + 'hidden')
  expect(panel).toHaveClass(WIDE + 'flex')
  expect(panel).toHaveClass(
    NARROW + 'absolute',
    NARROW + 'left-0',
    NARROW + 'right-0',
    NARROW + 'top-full',
    NARROW + 'z-50',
    NARROW + 'flex-col',
    NARROW + 'bg-popover',
  )

  // The state switch itself, which is the entire collapse mechanism and the one
  // part of it jsdom can see.
  expect(panel).toHaveClass('hidden')
  expect(panel).not.toHaveClass('flex')
  await userEvent.click(toggle)
  expect(panel).toHaveClass('flex')
  expect(panel).not.toHaveClass('hidden')
})
```

Then change the pin added in Task 1 Step 4 to use the same constant, so no
`md:overflow-x-auto` literal survives under `web/` outside `HoloShell.tsx`:

```tsx
  expect(panel).toHaveClass('min-w-0', WIDE + 'overflow-x-auto')
```

(`NARROW` and `WIDE` are module-scope consts, so declare them near the top of the file, above the
first test, not inside this one.)

- [ ] **Step 2: Run it and watch it fail for the right reason**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "REGRESSION PIN"
```

Expected: FAIL on the first `toHaveClass` for the `max-md:` group, reporting that the panel's class
list is `min-w-0 gap-0.5 md:flex md:overflow-x-auto hidden`. The `md:hidden`, `md:flex` and the
state-switch assertions already pass from Task 1; only the narrow group is missing.

- [ ] **Step 3: Write the minimal implementation**

In `web/src/shell/HoloShell.tsx`, replace the `navPanelClass` constant:

```tsx
  const navPanelClass = `min-w-0 gap-0.5 md:flex md:overflow-x-auto ${navOpen ? 'flex' : 'hidden'}`
```

with:

```tsx
  const navPanelClass = `min-w-0 gap-0.5 md:flex md:overflow-x-auto ${
    navOpen ? 'flex' : 'hidden'
  } max-md:absolute max-md:left-0 max-md:right-0 max-md:top-full max-md:z-50 max-md:flex-col max-md:border-b max-md:border-border max-md:bg-popover max-md:p-1.5 max-md:shadow-xl`
```

and, in the `NavLink` class function, append the two active-marker classes to the shared segment.
Replace:

```tsx
                    `border-b-2 px-[14px] py-[7px] text-[13px] tracking-[0.02em] transition-colors ${
```

with:

```tsx
                    `border-b-2 px-[14px] py-[7px] text-[13px] tracking-[0.02em] transition-colors max-md:border-b-0 max-md:border-l-2 ${
```

Add this comment immediately above the `<NavLink` opening tag:

```tsx
                {/* In a vertical panel a full-width bottom border reads as a row
                    separator rather than a selection marker, so the active
                    accent becomes a left bar below the breakpoint and stays an
                    underline above it. border-accent already sets the colour on
                    all four sides. Deliberately unpinned: deleting these two
                    changes how the active row looks and breaks nothing. */}
```

- [ ] **Step 4: Run the whole file and watch it pass**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx
```

Expected: PASS, 7 tests.

- [ ] **Step 5: Commit**

```
git add web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
git commit -m "feat(web): collapse the header nav into a full-bleed panel below md" -- web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
```

---

### Task 3: AC17 - prove the classes reach the production bundle, and prove the check can tell

**Files:**
- Create (outside the repo): an emitted-CSS checker
- Temporarily modify then restore: `web/src/shell/HoloShell.tsx`
- Commit: nothing. This task produces a measurement, not a diff.

This is the check the spec calls AC17. It exists because this whole slice is class strings plus
handler code, because `max-*` is the repo's first use of that variant, and because a class-string
fix that emits no CSS is indistinguishable from a working one to every assertion in the unit lane.
The A/B control in Steps 5-8 is what makes it a check on `HoloShell.tsx` rather than on the test
file that names the same strings.

- [ ] **Step 1: Write the checker OUTSIDE the Vite root**

Save this as `%TEMP%\relay-ac17-check.mjs` (PowerShell: `$env:TEMP\relay-ac17-check.mjs`; Git Bash:
`"$TEMP/relay-ac17-check.mjs"`). **It must not live anywhere under `web/`.** A checker that names
these classes inside the Vite root emits the very rules it is looking for and can only ever pass.

```js
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const dir = process.argv[2]
const css = readdirSync(dir)
  .filter((f) => f.endsWith('.css'))
  .map((f) => readFileSync(join(dir, f), 'utf8'))
  .join('\n')

const want = [
  'max-md:absolute',
  'max-md:left-0',
  'max-md:right-0',
  'max-md:top-full',
  'max-md:z-50',
  'max-md:flex-col',
  'max-md:border-b',
  'max-md:border-border',
  'max-md:bg-popover',
  'max-md:p-1.5',
  'max-md:shadow-xl',
  'max-md:border-b-0',
  'max-md:border-l-2',
  'md:hidden',
  'md:flex',
  'md:overflow-x-auto',
]

// Tailwind escapes ':' and '.' with a backslash inside a class selector.
const selector = (u) => '.' + u.replace(/[:.]/g, (c) => '\\' + c)
const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
// Anchored on the character that ENDS a selector, so max-md:border-b cannot be
// satisfied by max-md:border-b-0.
const present = (u) => new RegExp(escapeRe(selector(u)) + '\\s*[,{]').test(css)

const missing = want.filter((u) => !present(u))
console.log('css bytes:', css.length)
console.log('narrow media blocks:', (css.match(/@media \(width < 48rem\)/g) || []).length)
console.log('present:', want.length - missing.length, 'of', want.length)
console.log('missing:', missing.length ? missing.join(', ') : '(none)')
process.exit(missing.length ? 1 : 0)
```

- [ ] **Step 2: Build**

```
cd web && npm run build
```

Expected: `tsc -b` clean, then `vite build` writing `dist/index.html` and `dist/assets/*.css`.

- [ ] **Step 3: Run the checker against the real bundle**

PowerShell, from the worktree root:

```
node $env:TEMP\relay-ac17-check.mjs web\dist\assets
```

Git Bash:

```
node "$TEMP/relay-ac17-check.mjs" web/dist/assets
```

Expected: `present: 16 of 16`, `missing: (none)`, and `narrow media blocks:` at least 1. Exit code 0.

If `narrow media blocks: 0`, the `max-*` variant did not compile and every `max-md:` class in this
slice is dead - stop, report it, and do not proceed to the browser lane, because it would produce
dozens of unrelated-looking timeouts whose real cause is one missing media query.

- [ ] **Step 4: Back up the component before mutating it**

```
copy web\src\shell\HoloShell.tsx %TEMP%\HoloShell.tsx.bak
```

(PowerShell: `Copy-Item web\src\shell\HoloShell.tsx $env:TEMP\HoloShell.tsx.bak`.)

Restore in Step 8 is **from this copy**. Do not use `git checkout --` to undo a mutation: it would
also discard any uncommitted work in the same file.

- [ ] **Step 5: Strip the narrow classes from `HoloShell.tsx` only**

Edit `web/src/shell/HoloShell.tsx` so `navPanelClass` reads exactly:

```tsx
  const navPanelClass = `min-w-0 gap-0.5 md:flex md:overflow-x-auto ${navOpen ? 'flex' : 'hidden'}`
```

and the `NavLink` shared segment reads exactly:

```tsx
                    `border-b-2 px-[14px] py-[7px] text-[13px] tracking-[0.02em] transition-colors ${
```

Leave `HoloShell.test.tsx` alone. It still names every one of these classes, as `NARROW + '...'`.

- [ ] **Step 6: Confirm the mutation actually applied**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "REGRESSION PIN"
```

Expected: FAIL on the `max-md:` group. A green here means the edit did not land and the rest of this
control is meaningless.

- [ ] **Step 7: Rebuild and re-run the checker**

```
cd web && npm run build
```

then the same checker command as Step 3.

Expected: `missing:` lists all thirteen `max-md:` utilities, exit code 1.

- **If that is what you see**, AC17 is a genuine producer check: the only source of those rules is
  `HoloShell.tsx`. Record the two outputs.
- **If the rules are still present**, the prefix-constant form did not defeat the scanner and the
  test file is emitting them. That is not a failure of the slice, but it means AC17 establishes only
  that the `max-*` variant compiles, and **nothing** about `HoloShell.tsx` being the producer. Say
  exactly that in your report and cite AC15/AC16 as the only producer evidence. Do not describe
  AC17 as verifying the fix reaches the bundle.

- [ ] **Step 8: Restore, rebuild the tree state, and clean up**

```
copy %TEMP%\HoloShell.tsx.bak web\src\shell\HoloShell.tsx
cd web && npx vitest run src/shell/HoloShell.test.tsx
```

Expected: PASS, 7 tests - the same 7 as at the end of Task 2.

Then restore the tracked placeholder:

```
git checkout -- web/dist/index.html
git status --short
```

Expected: `git status --short` shows **nothing**. `web/dist/assets/` is gitignored. If it shows a
modified `web/src/shell/HoloShell.tsx`, the restore did not take - diff it against the backup before
going further.

- [ ] **Step 9: No commit**

This task changes no tracked file. Record in your report: the `present: N of 16` line and the
`narrow media blocks:` count from Step 3, and the `missing:` line from Step 7.

---

### Task 4: Escape and outside mousedown

**Files:**
- Modify: `web/src/shell/HoloShell.tsx`
- Modify: `web/src/shell/HoloShell.test.tsx`

- [ ] **Step 1: Write the three failing tests**

Append to `web/src/shell/HoloShell.test.tsx`:

```tsx
// AC2, half one. Escape is a DOCUMENT listener, not an onKeyDown on the panel:
// focus leaves the panel through more routes than a panel-scoped handler sees, and
// WebKit does not focus a <button> on click, so the panel can legitimately be open
// with activeElement === <body>, where a panel-scoped handler would never fire.
// Same reasoning as UserMenu's, which owns the sibling copy of this handler set.
test('Escape closes the nav panel and returns focus to the toggle when focus was inside', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  await userEvent.click(toggle)
  // Put focus GENUINELY inside the panel and assert it landed, before pressing
  // Escape. Without this the test also passes against a component that focuses the
  // toggle unconditionally - the implementation its partner below refutes.
  await userEvent.tab()
  expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Jobs' }))
  const toggleFocus = vi.spyOn(toggle, 'focus')

  await userEvent.keyboard('{Escape}')

  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(document.activeElement).toBe(toggle)
  // Paired with the not.toHaveBeenCalled() below: the two use the SAME instrument,
  // so one cannot pass by measuring something the other does not.
  expect(toggleFocus).toHaveBeenCalled()
  toggleFocus.mockRestore()
})

// AC2, half two. The containment check is what stops a close from STEALING focus a
// user never put inside the panel.
test('Escape does not steal focus when focus was outside the nav container', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  await userEvent.click(toggle)
  // STANDS IN for WebKit, which does not focus a <button> on click. user-event
  // always focuses the closest focusable on click, so jsdom cannot reach that state
  // naturally; this blur() is a stand-in, not a reproduction. It fires focusout with
  // a NULL relatedTarget, which the focusout rule added later deliberately ignores,
  // so the panel is still open when Escape arrives.
  ;(document.activeElement as HTMLElement).blur()
  expect(document.activeElement).toBe(document.body)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')

  await userEvent.keyboard('{Escape}')

  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(document.activeElement).toBe(document.body)
})

// AC3. mousedown fires BEFORE the browser moves focus, so at that instant focus is
// still inside the panel and a restore would steal it from the control being
// pressed. Opposite answer to the Escape path above, purely because the event
// ordering differs.
//
// Spying on the CALL rather than reading activeElement at the end, because the end
// state cannot tell the two implementations apart: user-event moves focus to the
// clicked control AFTER the mousedown listeners run, so a focus-stealing close is
// overwritten a moment later and both versions finish with activeElement on the
// chip. The steal is only observable as the call. The real-browser harm: press on
// non-focusable page content while the panel is open, nothing else takes focus, and
// the stolen focus is never overwritten.
test('an outside mousedown closes the nav panel and never touches the toggle focus', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  const chip = screen.getByRole('button', { name: /me@studio\.dev/i })
  await userEvent.click(toggle)
  await userEvent.tab()
  expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Jobs' }))
  const toggleFocus = vi.spyOn(toggle, 'focus')

  await userEvent.click(chip)

  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(toggleFocus).not.toHaveBeenCalled()
  expect(document.activeElement).toBe(chip)
  toggleFocus.mockRestore()
})
```

Add `vi` to the vitest import at the top of the file:

```tsx
import { afterEach, expect, test, vi } from 'vitest'
```

- [ ] **Step 2: Run them and watch them fail for the right reason**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "Escape"
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "an outside mousedown"
```

Expected: all three FAIL on `expect(toggle).toHaveAttribute('aria-expanded', 'false')`, receiving
`"true"` - nothing closes the panel yet. The first also fails its `document.activeElement` assertion.
If any of them fails earlier, on the `tab()` control or the `blur()` control, the DOM order is not
what Task 1 built - stop and check.

- [ ] **Step 3: Write the minimal implementation**

In `web/src/shell/HoloShell.tsx`, widen the react import:

```tsx
import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
```

and insert these three blocks between the `navPanelId` line and `async function onLogout()`:

```tsx
  // The open/close behaviour below is a transcription of shell/UserMenu.tsx's, not
  // an invention. The two disclosures differ in mount model, alignment and item
  // kinds, so nothing is shared yet; what they DO share is this handler set, and
  // each site points at the other so the pair is discoverable from either end.
  //
  // Close AND return focus to the toggle, but ONLY if focus was inside the
  // container: a mouse user in an engine that does not focus a <button> on click
  // can legitimately have the panel open with activeElement on <body>, and must not
  // have focus yanked onto a toggle it was never on. The containment check is read
  // BEFORE setOpen.
  function closeNavAndRestoreFocus() {
    const focusWasInside = !!navRef.current && navRef.current.contains(document.activeElement)
    setNavOpen(false)
    if (focusWasInside) navToggleRef.current?.focus()
  }

  // Close WITHOUT touching focus, for the paths where the browser is already moving
  // focus itself and a restore would fight the user.
  function closeNav() {
    setNavOpen(false)
  }

  useEffect(() => {
    if (!navOpen) return
    function onDown(e: MouseEvent) {
      // closeNav(), NOT closeNavAndRestoreFocus(): mousedown fires before the
      // browser moves focus to whatever was pressed, so a restore here would steal
      // focus away from the control the user just clicked.
      if (navRef.current && !navRef.current.contains(e.target as Node)) closeNav()
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') closeNavAndRestoreFocus()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
    // Both helpers are captured from the render that ran this effect and are
    // deliberately not dependencies: they touch only refs and setNavOpen, all
    // stable for the component's life, so a stale closure cannot observe stale
    // state. Listing them would re-subscribe both listeners on every render.
  }, [navOpen])
```

- [ ] **Step 4: Run the whole file and watch it pass**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx
```

Expected: PASS, 10 tests.

- [ ] **Step 5: Commit**

```
git add web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
git commit -m "feat(web): close the collapsed nav on Escape and outside mousedown" -- web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
```

---

### Task 5: Activating a destination

**Files:**
- Modify: `web/src/shell/HoloShell.tsx`
- Modify: `web/src/shell/HoloShell.test.tsx`

- [ ] **Step 1: Write the two failing tests**

Append to `web/src/shell/HoloShell.test.tsx`:

```tsx
// AC4, half one. The shell is persistent and is not remounted by a route change,
// and the outside-mousedown handler does not fire because the press target is
// INSIDE the container - so without an explicit close the panel hangs open over the
// page it just navigated to.
test('selecting a destination closes the nav panel and returns focus to the toggle', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  for (const name of ['Workers', 'Schedules', 'Admin']) {
    await userEvent.click(toggle)
    // Positive control inside the loop: prove the panel was OPEN before asserting
    // it closed, so a component that failed to open cannot pass this for the wrong
    // reason.
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await userEvent.click(screen.getByRole('link', { name }))
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(document.activeElement).toBe(toggle)
  }
})

// AC4, half two. React Router's Link calls the caller's onClick BEFORE it decides
// whether to navigate, so an unconditional close runs even for a
// Ctrl/Cmd/Shift/Alt or non-primary click: the destination opens in a new tab AND
// the panel collapses and yanks focus in the tab the user is still looking at. The
// predicate is the one react-router itself uses to decide whether it will handle
// the click.
test('a modifier-clicked destination leaves the nav panel open and does not touch focus', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  await userEvent.click(toggle)
  const link = screen.getByRole('link', { name: 'Workers' })
  const toggleFocus = vi.spyOn(toggle, 'focus')

  // Each bare userEvent.X() call spins up a fresh input-device System unless one is
  // threaded through explicitly, so a held modifier from one call is invisible to
  // the next by default. Passing the System back in as keyboardState is what makes
  // Control still be down for the click.
  const heldControl = await userEvent.keyboard('{Control>}')
  await userEvent.click(link, { keyboardState: heldControl })
  await userEvent.keyboard('{/Control}', { keyboardState: heldControl })

  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(toggleFocus).not.toHaveBeenCalled()
  toggleFocus.mockRestore()
  // Expected stderr noise: jsdom logs "Not implemented: navigation to another
  // Document" because react-router sees the modifier and skips preventDefault,
  // letting the anchor's native default action run - the same route a real browser
  // takes to open a new tab. That warning is proof the click was NOT intercepted.
})
```

- [ ] **Step 2: Run them and watch them fail for the right reason**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "destination"
```

Expected: the first FAILS at `expect(toggle).toHaveAttribute('aria-expanded', 'false')` after the
first link click, receiving `"true"`. The second currently **passes** - there is no onClick at all,
so nothing closes and nothing touches focus. That is the correct RED/GREEN split: the first test is
the RED, and the second is the guard that stops the fix for it from being an unconditional close.
Prove that in Step 5.

- [ ] **Step 3: Write the minimal implementation**

In `web/src/shell/HoloShell.tsx`, add after `closeNav()`:

```tsx
  // One guard for every destination's onClick rather than a copy per link. React
  // Router's Link calls this BEFORE it decides whether to navigate, so an
  // unconditional close would also run for a modified or non-primary click, which
  // opens a new tab while collapsing the panel and stealing focus in the tab the
  // user is still on. Same predicate react-router uses for the same question.
  function onNavItemClick(e: ReactMouseEvent<HTMLAnchorElement>) {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return
    closeNavAndRestoreFocus()
  }
```

Widen the react import to bring in the event type:

```tsx
import {
  useEffect,
  useId,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
} from 'react'
```

and add the handler to the `NavLink`, immediately after its `to` prop:

```tsx
                  onClick={onNavItemClick}
```

- [ ] **Step 4: Run the whole file and watch it pass**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx
```

Expected: PASS, 12 tests.

- [ ] **Step 5: Prove the modifier guard, then restore**

Temporarily replace the body of `onNavItemClick` with `closeNavAndRestoreFocus()` alone (delete the
`if` line), then:

```
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "a modifier-clicked destination"
```

Expected: FAIL on `aria-expanded` being `"false"`. Put the `if` line back and re-run the whole file
(PASS, 12). Record that this mutation was measured.

- [ ] **Step 6: Commit**

```
git add web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
git commit -m "feat(web): close the collapsed nav on a plain click, not a modified one" -- web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
```

---

### Task 6: Tab out and Shift+Tab

**Files:**
- Modify: `web/src/shell/HoloShell.tsx`
- Modify: `web/src/shell/HoloShell.test.tsx`

- [ ] **Step 1: Write the two failing tests**

Append to `web/src/shell/HoloShell.test.tsx`:

```tsx
// AC5, half one. Tab out is a DISMISS route for a disclosure, not something to
// intercept: no Tab trap, because nothing here is modal and the page behind stays
// interactive. The destination after the last link is the user chip, which is the
// next control in the header.
test('Tab out of the last destination closes the nav panel without stealing the destination', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  const chip = screen.getByRole('button', { name: /me@studio\.dev/i })
  await userEvent.click(toggle)
  await userEvent.tab() // Jobs
  await userEvent.tab() // Workers
  await userEvent.tab() // Schedules
  await userEvent.tab() // Admin
  // Positive control on the tab order itself: the panel follows the toggle in DOM
  // order and every destination is a natural tab stop, which is the whole reason
  // this surface is not portalled and carries no roving tabindex.
  expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Admin' }))

  await userEvent.tab()

  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  // The close must not also yank focus back: the user asked to go forward.
  expect(document.activeElement).toBe(chip)
})

// AC5, half two. The toggle is INSIDE the container, so the containment check is
// what keeps the panel open here. Without this control, a rule that closed on EVERY
// focusout would pass the Tab-out test above.
test('Shift+Tab from the first destination lands on the toggle and leaves the panel open', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  await userEvent.click(toggle)
  await userEvent.tab()
  expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Jobs' }))

  await userEvent.tab({ shift: true })

  expect(document.activeElement).toBe(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
})

// A blur with a NULL relatedTarget means "blurred to nothing" - jsdom fires exactly
// that for a bare blur(), and in a real browser it is what pressing the mouse on a
// panel's own non-focusable content produces. Closing on it would make the panel
// vanish under the user's cursor, and the document mousedown handler already owns
// the "pressed somewhere else" case. A naive onBlur={() => setNavOpen(false)} passes
// every other test in this file and fails exactly here.
test('a blur with a null relatedTarget does NOT close the nav panel', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  await userEvent.click(toggle)
  await userEvent.tab()
  const first = screen.getByRole('link', { name: 'Jobs' })
  expect(document.activeElement).toBe(first)

  first.blur()

  expect(toggle).toHaveAttribute('aria-expanded', 'true')
})
```

- [ ] **Step 2: Run them and watch them fail for the right reason**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "Tab"
cd web && npx vitest run src/shell/HoloShell.test.tsx -t "a blur with a null relatedTarget"
```

Expected: `Tab out of the last destination` FAILS on `aria-expanded` being `"true"`. The Shift+Tab
test and the null-relatedTarget test both PASS already, because nothing closes on focusout yet -
they are the guards that constrain the fix, and Step 5 proves each of them.

- [ ] **Step 3: Write the minimal implementation**

In `web/src/shell/HoloShell.tsx`, add after `onNavItemClick`:

```tsx
  // React maps onBlur to the native, BUBBLING focusout, so this fires for focus
  // leaving any descendant of the container.
  function onNavBlur(e: FocusEvent<HTMLElement>) {
    // "Blurred to nothing" has a correct owner already - the document mousedown
    // handler - and closing on it would make the panel vanish under the cursor.
    if (!e.relatedTarget) return
    // Shift+Tab from the first destination lands on the toggle, which is INSIDE
    // this container, so the containment check is what keeps the panel open there.
    // closeNav(), not closeNavAndRestoreFocus(): by construction focus is already
    // outside, so a restore would be a theft from where the user just Tabbed.
    if (navRef.current && !navRef.current.contains(e.relatedTarget)) closeNav()
  }
```

Widen the react import once more:

```tsx
import {
  useEffect,
  useId,
  useRef,
  useState,
  type FocusEvent,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
} from 'react'
```

and add the handler to the `<nav>`:

```tsx
          <nav ref={navRef} onBlur={onNavBlur} aria-label="Main" className="min-w-0">
```

- [ ] **Step 4: Run the whole file and watch it pass**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx
```

Expected: PASS, 15 tests.

- [ ] **Step 5: Prove both guards, then restore**

Two mutations, one at a time, restoring the original line between them:

1. Delete the `if (!e.relatedTarget) return` line. Run
   `cd web && npx vitest run src/shell/HoloShell.test.tsx -t "a blur with a null relatedTarget"`.
   Expected: FAIL. Restore.
2. Replace the containment line with a bare `closeNav()`. Run
   `cd web && npx vitest run src/shell/HoloShell.test.tsx -t "Shift+Tab"`. Expected: FAIL. Restore.

Re-run the whole file: PASS, 15.

- [ ] **Step 6: Commit**

```
git add web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
git commit -m "feat(web): dismiss the collapsed nav when focus leaves it" -- web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
```

---

### Task 7: The three structural guards

**Files:**
- Modify: `web/src/shell/HoloShell.test.tsx`

Three properties that the implementation already satisfies. None of them can be TDD'd from a red,
because the code that satisfies them is already written - so each one names the mutation that makes
it red, and Step 4 runs all three. A guard nobody has watched go red is a guard nobody has watched.

- [ ] **Step 1: Write the three tests**

Append to `web/src/shell/HoloShell.test.tsx`:

```tsx
// AC6. The document keydown listener has an OPEN-ONLY lifetime. Every other test in
// this file opens the panel before asserting anything, so this is the only one that
// looks at the closed state's listener lifetime at all. The filter on type is what
// makes it robust: React attaches its own document-level listeners for other event
// types on mount.
test('no document keydown listener is registered while the nav panel is closed', async () => {
  const addSpy = vi.spyOn(document, 'addEventListener')
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })

  const keydownRegistrations = addSpy.mock.calls.filter(([type]) => type === 'keydown')

  expect(keydownRegistrations).toHaveLength(0)
  addSpy.mockRestore()
})

// AC7. A disclosure containing navigation links, which is the case the menu role's
// own specification excludes: role="menuitem" on an <a href> REPLACES the link role,
// so the item stops being announced as a link and drops out of a screen reader's
// links list, and a conforming menu's roving tabindex would make the destinations
// unreachable by Tab.
test('the nav panel is a plain disclosure - no menu roles, no roving tabindex', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  await userEvent.click(screen.getByRole('button', { name: /menu/i }))
  const panel = screen.getByTestId('header-nav-panel')

  expect(panel).not.toHaveAttribute('role')
  expect(panel.querySelectorAll('[role="menu"]')).toHaveLength(0)
  expect(panel.querySelectorAll('[role="menuitem"]')).toHaveLength(0)
  // No tabindex AT ALL, not merely no negative one: a roving tabindex is exactly
  // tabindex="0" on one item and tabindex="-1" on the rest, so asserting the
  // attribute is absent catches a half-built one too.
  expect(panel.querySelectorAll('[tabindex]')).toHaveLength(0)

  // Positive control: the sweep is looking at a POPULATED panel, so it cannot pass
  // against an empty one. Four elements whose computed role is LINK, and the same
  // four as real anchors with an href - the semantic a menu contract destroys.
  expect(screen.getAllByRole('link')).toHaveLength(4)
  expect(panel.querySelectorAll('a[href]')).toHaveLength(4)
})

// AC8. The reason the collapse renders ONE copy of the links rather than an inline
// nav plus a hidden mobile nav. Two copies put two links named "Jobs" in the
// accessibility tree, and jsdom applies no CSS, so no assertion in this lane could
// tell the intended state from the broken one. This is the test that reddens if
// someone re-solves the problem with a duplicated nav.
test('each destination is rendered exactly once in the header', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const header = within(screen.getByRole('banner'))
  for (const label of ['Jobs', 'Workers', 'Schedules', 'Admin']) {
    expect(header.getAllByRole('link', { name: label })).toHaveLength(1)
  }
})
```

- [ ] **Step 2: Run them and watch them pass**

```
cd web && npx vitest run src/shell/HoloShell.test.tsx
```

Expected: PASS, 18 tests.

- [ ] **Step 3: Type-check**

```
cd web && npx tsc -b
```

Expected: no output, exit 0. This is where the `within` import added in Task 1 stops being unused.

- [ ] **Step 4: Prove each guard by mutation, restoring between each**

1. **AC6.** In `HoloShell.tsx`, delete `if (!navOpen) return` from the effect and change its
   dependency array to `[]`. Run
   `cd web && npx vitest run src/shell/HoloShell.test.tsx -t "no document keydown listener"`.
   Expected: FAIL, received length 1. Restore both lines.
2. **AC7.** Add `role="menu"` to the panel `<div>`. Run
   `cd web && npx vitest run src/shell/HoloShell.test.tsx -t "plain disclosure"`.
   Expected: FAIL on `not.toHaveAttribute('role')`. Restore.
3. **AC8.** Duplicate the whole `<div id={navPanelId} ...>` block, giving the copy no `id` and no
   `data-testid`. Run
   `cd web && npx vitest run src/shell/HoloShell.test.tsx -t "exactly once"`.
   Expected: FAIL, received length 2. Restore.

Re-run the whole file: PASS, 18. Re-run `npx tsc -b`: clean.

- [ ] **Step 5: Commit**

```
git add web/src/shell/HoloShell.test.tsx
git commit -m "test(web): guard the collapsed nav's listener lifetime, roles and arity" -- web/src/shell/HoloShell.test.tsx
```

---

### Task 8: The reciprocal pointer, and the whole unit lane

**Files:**
- Modify: `web/src/shell/UserMenu.tsx`

- [ ] **Step 1: Add the pointer**

This is the only permitted edit to `UserMenu.tsx`. In its leading comment block, immediately after
the line

```
// docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md.
```

insert:

```
//
// shell/HoloShell.tsx's collapsed nav is the sibling disclosure and carries a
// transcription of this file's handler set - Escape, outside mousedown, focusout
// containment, modifier-click guard, containment-checked focus restore. The two
// differ deliberately: its panel is always mounted, so its aria-controls is present
// in both states where this one's is set only while open. A change to the behaviour
// here almost certainly belongs there too.
```

- [ ] **Step 2: Run the whole unit lane and the type-check**

```
cd web && npm test
cd web && npx tsc -b
```

Expected: every suite green, no type errors. Record the total test count.

- [ ] **Step 3: Commit**

```
git add web/src/shell/UserMenu.tsx
git commit -m "docs(web): point UserMenu at its sibling disclosure" -- web/src/shell/UserMenu.tsx
```

---

### Task 9: Browser reachability (AC12, AC13, AC14)

**Files:**
- Create: `web/e2e/nav.ts`
- Modify: `web/e2e/layout.spec.ts`

- [ ] **Step 1: Write the helper**

Create `web/e2e/nav.ts`:

```ts
import { expect, type Page } from '@playwright/test'

// The four top-level destinations. /profile's three are reached through the user
// chip, which is visible at every width, so they are already behind a visible
// control and are not this file's business.
const DESTINATIONS = ['Jobs', 'Workers', 'Schedules', 'Admin'] as const

// SCOPED TO THE HEADER, and exact. Playwright matches an accessible name
// case-insensitively and by SUBSTRING by default, and four pages render a
// breadcrumb link whose name contains one of these words - so an unscoped locator
// resolves two elements and throws a strict-mode violation on job-detail, job-new,
// workers/:id and schedule-detail.
function header(page: Page) {
  return page.getByRole('banner')
}

// The assertion the backlog item asks for, stated so that it FAILS at HEAD: at
// every width, every destination is visible, or reachable through a control that is
// visible. A scrollWidth <= clientWidth gate cannot express this - content that
// overflows into its own scroll wrapper reads as zero document overflow and passes.
//
// Returns whether the panel was opened, and LEAVES IT OPEN when it was, so the
// caller can measure the open state before closing it with closeNavPanel.
export async function expectDestinationsReachable(page: Page): Promise<boolean> {
  const h = header(page)
  const toggle = h.getByRole('button', { name: /menu/i })
  let opened = false
  for (const name of DESTINATIONS) {
    const link = h.getByRole('link', { name, exact: true })
    if (await link.isVisible()) continue
    await expect(toggle, `${name} is not visible and no collapse control is either`).toBeVisible()
    if (!opened) {
      await toggle.click()
      opened = true
    }
    await expect(link, `${name} is not reachable through the collapse control`).toBeVisible()
  }
  return opened
}

// Escape rather than a second click on the toggle: it exercises the document
// keydown listener, which is the dismissal route a keyboard user has, and it leaves
// the page as it was found so the caller's next step is not measuring a state it
// created. The hidden assertion is a positive control that the close actually
// happened - without it a no-op here would silently leave the panel open.
export async function closeNavPanel(page: Page): Promise<void> {
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('header-nav-panel')).toBeHidden()
}
```

- [ ] **Step 2: Extend `layout.spec.ts`**

Add the import beneath the existing ones at the top of `web/e2e/layout.spec.ts`:

```ts
import { closeNavPanel, expectDestinationsReachable } from './nav'
```

Replace the `test(...)` opening line:

```ts
      test(`${s.name} does not overflow horizontally`, async ({ page }, testInfo) => {
```

with:

```ts
      // The title is conditional because the body is. /auth renders no shell, so
      // there is nothing to navigate and no header to find - and a title claiming
      // reachability over a body that does not assert it is the test-honesty
      // problem this harness exists to avoid. The surface is still measured: it is
      // the control that makes a header finding an attribution.
      const title = s.anonymous
        ? `${s.name} fits the viewport (no shell to navigate)`
        : `${s.name} fits the viewport and keeps every destination reachable`
      test(title, async ({ page }, testInfo) => {
```

Then append, after the last of the three existing `expect(...)` calls and before the closing `})` of
the test body:

```ts
        // ORDERING IS LOAD-BEARING. Everything above measures, attaches and asserts
        // the CLOSED state, which is the state the defect is about. Only now does
        // anything open the menu.
        if (s.anonymous) return

        const opened = await expectDestinationsReachable(page)
        if (!opened) return

        // One extra screenshot on ONE surface, not thirteen: the header is the same
        // component everywhere, and 26 near-identical PNGs per engine would make the
        // human pass worse rather than better. Artifact, not assertion.
        if (s.name === 'jobs') {
          const openShot = testInfo.outputPath(`${s.name}-${width}-nav-open.png`)
          await page.screenshot({ path: openShot, fullPage: true })
          await testInfo.attach(`screenshot-${s.name}-${width}-nav-open`, {
            path: openShot,
            contentType: 'image/png',
          })
        }

        // The panel is a full-bleed dropdown pinned to both viewport edges, so it
        // cannot overflow by construction - which is exactly the kind of claim that
        // needs measuring rather than asserting in prose.
        const open = await page.evaluate(() => ({
          docScroll: document.documentElement.scrollWidth,
          docClient: document.documentElement.clientWidth,
        }))
        await testInfo.attach(`widths-${s.name}-${width}-nav-open`, {
          body: JSON.stringify({ surface: s.name, path, width, ...open }, null, 2),
          contentType: 'application/json',
        })
        expect(
          open.docScroll,
          `${path}: document overflows at ${width}px with the nav panel open`,
        ).toBeLessThanOrEqual(open.docClient)

        await closeNavPanel(page)
```

- [ ] **Step 3: Type-check**

```
cd web && npx tsc -b
```

Expected: clean. `tsconfig.json` includes `e2e`, so both new files are checked under `strict`.

- [ ] **Step 4: Commit**

```
git add web/e2e/nav.ts web/e2e/layout.spec.ts
git commit -m "test(e2e): assert every header destination is reachable at every width" -- web/e2e/nav.ts web/e2e/layout.spec.ts
```

The browser lane is not run yet - Task 12 runs the whole suite once, so a red is diagnosed against
the finished code rather than against a half-written spec set.

---

### Task 10: The breakpoint value and real key events (AC15, AC16)

**Files:**
- Create: `web/e2e/header-nav.spec.ts`

- [ ] **Step 1: Write the spec**

Create `web/e2e/header-nav.spec.ts`:

```ts
import { expect, test, type Page } from '@playwright/test'

// ONE PAGE, not every surface. The header is the same component on all thirteen
// shell surfaces, and layout.spec.ts already runs the reachability predicate across
// all of them at three widths. What this file adds is what that loop cannot say: at
// which width, exactly, the collapse happens, and whether a real key press can
// drive it.
//
// No fourth entry was added to layout.spec.ts's WIDTHS for the same reason: a
// fourth width there costs fourteen surfaces times two engines for a property one
// page needs.

const DESTINATIONS = ['Jobs', 'Workers', 'Schedules', 'Admin'] as const

// Gated on the page's own <h1> rather than on the header: the header renders as
// soon as AuthProvider reports authenticated, so a header-only gate can resolve
// before the route has finished mounting.
async function gotoJobs(page: Page, width: number) {
  await page.setViewportSize({ width, height: 900 })
  await page.goto('/jobs')
  await expect(page.getByRole('heading', { name: 'Jobs', level: 1 })).toBeVisible()
}

function header(page: Page) {
  return page.getByRole('banner')
}

test.describe('header nav collapse', () => {
  test('at 1280 every destination is inline and the collapse toggle is not exposed', async ({
    page,
  }) => {
    await gotoJobs(page, 1280)
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeVisible()
    }
    // toBeHidden, not toHaveCount(0): the toggle is always in the DOM and is removed
    // from the user's reach by CSS alone. An absence assertion would pass against a
    // component that stopped rendering it at every width.
    await expect(header(page).getByRole('button', { name: /menu/i })).toBeHidden()
  })

  // TWO VIEWPORTS ONE PIXEL APART. This is what makes the pair a test of the md
  // breakpoint rather than of "something collapses somewhere": move the breakpoint
  // and exactly one of these two goes red.
  test('at 768 the nav is still inline', async ({ page }) => {
    await gotoJobs(page, 768)
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeVisible()
    }
    await expect(header(page).getByRole('button', { name: /menu/i })).toBeHidden()
    const m = await page.evaluate(() => ({
      s: document.documentElement.scrollWidth,
      c: document.documentElement.clientWidth,
    }))
    expect(m.s, 'document overflows at 768px').toBeLessThanOrEqual(m.c)
  })

  test('at 767 the nav is collapsed', async ({ page }) => {
    await gotoJobs(page, 767)
    await expect(header(page).getByRole('button', { name: /menu/i })).toBeVisible()
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeHidden()
    }
    const m = await page.evaluate(() => ({
      s: document.documentElement.scrollWidth,
      c: document.documentElement.clientWidth,
    }))
    expect(m.s, 'document overflows at 767px').toBeLessThanOrEqual(m.c)
  })

  test('at 375 the toggle is the only visible nav control', async ({ page }) => {
    await gotoJobs(page, 375)
    await expect(header(page).getByRole('button', { name: /menu/i })).toBeVisible()
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeHidden()
    }
  })
})

// TAGGED so playwright.config.ts's webkit project grep runs this describe in both
// engines. This is the only lane in the repo that can send a real key, and the
// engine divergence it is here for is the one UserMenu documents: WebKit does not
// focus a <button> on click, so a focus-restore contract proven only through a
// click in chromium says nothing about it. Opening via Tab plus Enter puts focus on
// the toggle in BOTH engines by construction.
test.describe('header nav collapse keyboard @webkit', () => {
  async function tabToToggle(page: Page) {
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    const toggle = header(page).getByRole('button', { name: /menu/i })
    let reached = false
    for (let i = 0; i < 20 && !reached; i++) {
      await page.keyboard.press('Tab')
      reached = await toggle.evaluate((el) => el === document.activeElement)
    }
    expect(reached, 'Tab never reached the collapse toggle within 20 presses').toBe(true)
    return toggle
  }

  test('a real Tab press reaches the collapse toggle and Enter opens the panel', async ({
    page,
  }) => {
    await gotoJobs(page, 375)
    const toggle = await tabToToggle(page)
    await expect(page.getByTestId('header-nav-panel')).toBeHidden()

    await page.keyboard.press('Enter')

    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    for (const name of DESTINATIONS) {
      await expect(header(page).getByRole('link', { name, exact: true })).toBeVisible()
    }
  })

  test('Escape closes the panel and returns focus to the toggle', async ({ page }) => {
    await gotoJobs(page, 375)
    const toggle = await tabToToggle(page)
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('header-nav-panel')).toBeVisible()

    await page.keyboard.press('Escape')

    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByTestId('header-nav-panel')).toBeHidden()
    expect(await toggle.evaluate((el) => el === document.activeElement)).toBe(true)
  })
})
```

- [ ] **Step 2: Type-check**

```
cd web && npx tsc -b
```

Expected: clean.

- [ ] **Step 3: Commit**

```
git add web/e2e/header-nav.spec.ts
git commit -m "test(e2e): pin the nav collapse breakpoint from both sides and by key" -- web/e2e/header-nav.spec.ts
```

---

### Task 11: Correct the README's account of the coverage gap

**Files:**
- Modify: `web/e2e/README.md`

The README currently cites this bug as the live instance of the "cannot distinguish fits from clipped
behind a scroller" gap. After this slice that instance is gone and the gap remains, so the paragraph
becomes wrong prose about correct code unless it is edited. Keep the edit free of new class-shaped
substrings: this file is inside the Vite root and Tailwind scans it.

- [ ] **Step 1: Replace the paragraph**

Replace this paragraph (the one beginning `**A scrollWidth <= clientWidth gate cannot distinguish`):

```
**A `scrollWidth <= clientWidth` gate cannot distinguish "fits" from "clipped
behind a scroller".** `layout.spec.ts` only fails when content overflows past
the document edge; an element that overflows into its OWN `overflow-x-auto`
wrapper instead (a horizontally-scrollable nav with no keyboard affordance, for
instance) reads as zero document overflow and passes. That gap is real, not
hypothetical: it is the shape of `bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports`
in `docs/backlog/` - the shipped header nav is already an `overflow-x-auto`
scroll container and never wraps, so no mutation was needed to demonstrate the
gap. It was found by a human reviewing the screenshots from the harness's
first CI run, with the full 51-test suite green throughout.
```

with:

```
**A `scrollWidth <= clientWidth` gate cannot distinguish "fits" from "clipped
behind a scroller".** `layout.spec.ts` only fails when content overflows past
the document edge; an element that overflows into its OWN horizontal scroll
wrapper instead reads as zero document overflow and passes. The gap was found by
a human reviewing the screenshots from the harness's first CI run, with the full
suite green throughout - the header nav was clipped behind its own scroller at
320 and 375, and no number in this harness could say so.

That instance is closed. `layout.spec.ts` now also asserts, per surface and per
width, that every top-level destination is visible or reachable through a visible
control (`nav.ts`), and `header-nav.spec.ts` pins the breakpoint by value. The
GENERAL gap remains: no assertion here covers any other scroller. The remaining
in-tree ones are `Table`'s wrappers, which have a keyboard affordance and a
`role="group"` name of their own, so what is missing is the general predicate, not
a second known defect.
```

- [ ] **Step 2: Verify the file is unchanged apart from that**

```
git diff --stat web/e2e/README.md
git ls-files --eol web/e2e/README.md
```

Expected: a diffstat proportionate to a one-paragraph replacement (roughly 10 deleted, 16 inserted),
and `i/lf` in the eol output. If the insertion count is in the hundreds, the edit reclassified line
endings - do not commit, revert and redo it with an exact-anchor replacement.

- [ ] **Step 3: Commit**

```
git add web/e2e/README.md
git commit -m "docs(e2e): the scroller gap's known instance is closed, the gap is not" -- web/e2e/README.md
```

---

### Task 12: Run the browser lane

**Files:** none. This task runs the suite and reports.

- [ ] **Step 1: Preconditions**

Docker is running on this machine and the `relay-postgres` container is up. Confirm:

```
docker start relay-postgres
```

Install the browsers once if they are not already present:

```
cd web && npx playwright install chromium webkit
```

- [ ] **Step 2: Run the suite**

**From Git Bash, from the worktree root** `D:/dev/relay/.claude/worktrees/web-a-header-nav`. `make`
is not on PATH on this host; use the MSYS2 copy, and forward the variables its recipe subshells do
not inherit (without them the `RELAY_SERVER_BIN` branch silently drops the `.exe` suffix and
`go build` fails with `module cache not found` or `Access is denied`):

```bash
/c/msys64/usr/bin/make.exe test-e2e \
  OS="$OS" TEMP="$TEMP" TMP="$TMP" \
  GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"
```

The target runs `web-build`, then rebuilds `relay-server` (the embed is a compile-time snapshot, so
that order is load-bearing), then the suite, then restores `web/dist/index.html` on exit, pass or
fail.

Expected new tests: 42 `layout.spec.ts` tests keep their existing assertions and 39 of them (13 shell
surfaces x 3 widths) gain the reachability check; 4 new chromium-only tests and 2 new tests per
engine from `header-nav.spec.ts`.

- [ ] **Step 3: Iterate on one spec if something is red**

```bash
/c/msys64/usr/bin/make.exe web-build OS="$OS" TEMP="$TEMP" TMP="$TMP" GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"
go build -o bin/relay-server.exe ./cmd/relay-server
cd web && npm run test:e2e -- header-nav.spec.ts --project=chromium
cd .. && git checkout -- web/dist/index.html
```

**Rebuilding `relay-server` without re-running `web-build` first silently embeds the restored
placeholder**, and the suite then fails with dozens of generic "element not found" timeouts whose
real cause is that the page has no `#root` at all. If a run comes back with a wall of
unrelated-looking timeouts, that is the first thing to check.

- [ ] **Step 4: Open the screenshots**

`layout.spec.ts` attaches a full-page PNG per surface per width, plus `jobs-320-nav-open.png` and
`jobs-375-nav-open.png`. They are artifacts, not assertions, and an artifact nobody opens is worth
nothing. Open at least: `jobs-320.png`, `jobs-375.png` (the collapsed header, which is what the bug
is about), `jobs-1280.png` (the header must be unchanged from today), and both `nav-open` images (the
panel must be full bleed, opaque, and below the header). Say in your report what you saw.

- [ ] **Step 5: Leave the tree clean**

```
git status --short
```

Expected: nothing. If `web/dist/index.html` is modified, run `git checkout -- web/dist/index.html`.

- [ ] **Step 6: If the lane cannot run, say so plainly**

**Do not substitute anything for it, and do not describe the slice as verified.** The spec's Lane
availability section is explicit: AC1 to AC11 are the whole of what vitest can establish, and they
establish that the handlers behave and that the class strings are present in the DOM - **not** that
anything collapses, that anything is visible, or that the CSS exists. jsdom applies none of this
app's CSS and does no layout, so every width in it is zero.

If `make test-e2e` cannot be run, the report must say, in these terms:

- which of AC12 to AC17 did not run, by name;
- that AC12 to AC16 are the only evidence for the defect this item is actually about, so the
  slice's central claim is unverified;
- whether Task 3's AC17 build check ran, and which of its two conclusions it supports (a producer
  check, or only that the `max-*` variant compiles);
- what specifically blocked it (Docker down, Postgres container absent, browsers not installed, make
  not found), so the conductor can decide whether to unblock it or to route the slice on.

A green unit lane is not a substitute and must not be reported as one.

---

## Self-review

**Spec coverage.** AC1 - Task 1. AC2 and AC3 - Task 4. AC4 - Task 5. AC5 - Task 6 (which also adds
the null-relatedTarget guard the spec's Design section names but does not give an AC number).
AC6, AC7, AC8 - Task 7. AC9 - Task 1 Step 5 (the three nav-entry tests are never edited; the only
edit to a pre-existing test is the two class pins inside the scroll-container test). AC10 - Task 1
Step 4, with the guard line byte-identical. AC11 - Task 2. AC17 - Task 3. AC12, AC13, AC14 - Task 9.
AC15, AC16 - Task 10. D9's NavLink classes - Task 2 Step 3. D14's no-shared-state - satisfied by
construction; no task adds any. D15 - no data is rendered; nothing to omit. The `UserMenu` reciprocal
pointer - Task 8. The `web/e2e/README.md` correction - Task 11, declared as a deviation.

**Placeholder scan.** Every code step carries the code. No "similar to Task N", no "add error
handling", no TBD. Every command is exact, with its expected output. The two mutation-proof tasks
name the exact edit and the exact expected failure.

**Type consistency.** `navOpen` / `setNavOpen`, `navRef`, `navToggleRef`, `navPanelId`,
`navPanelClass`, `closeNavAndRestoreFocus`, `closeNav`, `onNavItemClick`, `onNavBlur` are spelled
identically in Tasks 1, 2, 4, 5, 6 and 7. The panel's `data-testid` is `header-nav-panel` in
`HoloShell.tsx` (Task 1), in `HoloShell.test.tsx` (Tasks 1, 2, 7), in `web/e2e/nav.ts` (Task 9) and
in `web/e2e/header-nav.spec.ts` (Task 10). The e2e helper exports are
`expectDestinationsReachable(page): Promise<boolean>` and `closeNavPanel(page): Promise<void>`, used
with those exact signatures in Task 9's `layout.spec.ts` edit. The react import block is widened once
per task that needs a new symbol, and the final form (Task 6) contains `useEffect`, `useId`,
`useRef`, `useState`, `type FocusEvent`, `type MouseEvent as ReactMouseEvent`, `type ReactNode`.
