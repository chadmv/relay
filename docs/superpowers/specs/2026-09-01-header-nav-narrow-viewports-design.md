---
date: 2026-09-01
topic: header-nav-narrow-viewports
closes: bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports
lane: web SPA batch, lane A
---

# Header nav at narrow viewports: collapse into a disclosure below `md`

## Summary

At 320px and 375px the app header renders the RELAY wordmark, the first one or two nav
labels, and then the user chip. The remaining destinations are inside the nav's
`overflow-x-auto` scroll container, reachable only by a horizontal drag that nothing on
screen advertises. Every number the app measures says the page is correct: the document
does not overflow, the header does not overflow, `<main>` does not overflow. The defect is
only visible in a picture.

This spec collapses the four top-level destinations into a **disclosure** below the `md`
breakpoint: a visible `Menu` toggle in the header, and a full-bleed panel that drops below
the header carrying the same four links, one copy of them, always present in the DOM and
switched between "inline row" and "dropdown panel" by CSS alone. Above `md` the header is
byte-identical to what ships today.

The acceptance criterion becomes both numeric and visual, and it is enforced from both
sides of the breakpoint: at 320 and 375 every primary destination is either visible or
behind a visible control, and at 1280 the toggle is not rendered to the user at all.

## Context: what exists at HEAD, by symbol

Cited by symbol, not by line, because line citations rot.

- **`HoloShell`** (`web/src/shell/HoloShell.tsx`) is the app shell. It holds the
  module-level `NAV` array - `{to,label}` pairs for Jobs, Workers, Schedules, and Admin
  with `adminOnly: true` - filters it against `user?.is_admin`, and renders a `<header>`
  containing a left group (the `Eyebrow` wordmark plus a `<nav>` of `NavLink`s) and
  `UserMenu` on the right. The `<header>` carries `relative z-10`; `<main>` carries
  `relative z-0`.
- The **`<nav>` is the scroll container**, carrying `min-w-0 overflow-x-auto`, and its
  parent group carries `min-w-0`. This is the 2026-08-13 narrow-viewport fix. The header
  itself must never carry an `overflow-` class: an overflow there establishes a scroll
  container that clips the `UserMenu` dropdown, which deliberately hangs out of the header
  over `<main>`.
- **`HoloShell.test.tsx`** holds four tests: the three nav-entry/admin-gating tests, the
  `relative z-10` / `relative z-0` stacking pin, and
  `the nav is the only shrinkable scroll container in the header`, whose last assertion -
  `expect(header.className).not.toMatch(/\boverflow-/)` - the 2026-08-13 retro labels the
  one **real guard** in that file rather than a class pin.
- **`UserMenu`** (`web/src/shell/UserMenu.tsx`) is the app's only disclosure. It owns the
  pattern this spec reuses: `aria-expanded` on the toggle in both states, `aria-controls`
  only while the panel is mounted, a document-level `keydown` Escape listener and a
  document-level `mousedown` outside listener both registered **only while open**,
  `closeAndRestoreFocus()` (containment-checked, so a close never steals focus that was
  never inside), a plain `close()` for the two paths where the browser is already moving
  focus, an `onBlur` containment rule that ignores a null `relatedTarget`, and an
  `onNavItemClick` guard that leaves the panel open for a modifier-click because React
  Router's `Link` calls the caller's `onClick` before it decides whether to navigate. It is
  an ARIA disclosure and deliberately not a menu; `2026-08-13-usermenu-menu-roles.md` is the
  spec that inverted the opposite proposal.
- **`DialogShell`** (`web/src/components/dialog/DialogShell.tsx`) is the modal shell: portal
  to a body-level layer, scrim, `role="dialog"`, `aria-modal`, Tab trap, `dialogStack`
  registration for `inert`/`aria-hidden`/scroll lock. Its Escape listener is document-level
  for the same reason `UserMenu`'s is, and it calls `stopImmediatePropagation`, which only
  suppresses a listener registered **after** it on the same dispatch. None of its modal
  machinery applies to a disclosure.
- **`web/e2e/layout.spec.ts`** loops `WIDTHS = [320, 375, 1280]` over `surfaces()`, measures
  `documentElement.scrollWidth/clientWidth` plus `<header>` and `<main>` `scrollWidth`,
  attaches the numbers and a full-page screenshot on every run, and asserts three
  `toBeLessThanOrEqual` comparisons. `surfaces()` carries 14 entries; `auth` is the control
  and renders no shell. Screenshots are artifacts, not assertions - no pixel baselines.
- **`web/e2e/keyboard.spec.ts`** is the precedent for real key events in this repo, tagged
  `@webkit` so it runs in both engines; `playwright.config.ts` gives the `webkit` project a
  `grep: /@webkit/`, so an untagged spec runs in chromium only.
- **`web/e2e/README.md`** already names this defect: "a `scrollWidth <= clientWidth` gate
  cannot distinguish fits from clipped behind a scroller", citing this backlog item by name.
- Tailwind is **4.3.0** with default breakpoints (`md` = 48rem = 768px); `tokens.css`
  overrides no breakpoint. `--color-popover` exists precisely because `GlassPanel` sets no
  `background-color` and anything floating over live content needs its own fill.
- The repo uses `md:` (four sites) and `lg:` (`JobDetailPage`). **No `max-*` variant is used
  anywhere in `web/src` today.** The `max-*` variant is registered in the installed
  Tailwind 4.3.0 (`functional("max"` appears in `node_modules/tailwindcss/dist/lib.mjs`).
- `web/src` ships **no icon component**; there is no icon set to draw a hamburger from.
- **`AppRoutes`** (`web/src/app/router.tsx`) confirms the destination set: `/jobs`,
  `/workers`, `/schedules`, `/admin/:tab` are the only top-level sections. `/profile/:tab`
  is reached through `UserMenu`, whose chip is visible at every width, so the three profile
  destinations are already "behind a visible control" and this spec does not touch them.

### What was checked in the backlog item, and what it got wrong

Treated as a proposal, not a contract. Three corrections, one of which changes what the fix
may touch:

1. **"The header is a horizontal scroll container" is wrong, and the correction is
   load-bearing.** The `<nav>` is the scroll container; the `<header>` is explicitly
   guarded against ever becoming one, because an overflow there clips the `UserMenu`
   dropdown. Anyone reading the item literally would reach for the header, which is the
   documented wrong fix.
2. **"`web/src/app/` - the header/nav shell" is the wrong path.** `web/src/app/` holds
   `router.tsx`, `AdminRoute`, `ProtectedRoute` and `PublicOnlyRoute`. The shell is
   `web/src/shell/HoloShell.tsx`.
3. **"`design_handoff_relay_holo` is silent on narrow viewports" is true for the product
   and needs one qualifier.** There is no breakpoint, no wrap and no mobile-nav treatment
   anywhere in `hifi3-holo-pages.jsx` (the authoritative hi-fi) or in the reference screens.
   The bundle does contain exactly one `@media` rule, in `reference/styles.css`, and it
   governs the wireframe **gallery page's** `variations` grid - prototype scaffolding the
   README tells you to omit when porting. It is not a statement about the app.

Not refuted, and not independently verified either: the exact rendered strings quoted from
the screenshots (`RELAY  Jobs  Workers  S...` at 375, `Workers` cut mid-word at 320). No
screenshot artifact exists in the tree to check against. The claim is consistent with the
measured 494-523px content floor the header used to have, and nothing in this design depends
on the exact truncation point.

Two further claims were checked and hold: `/auth` renders no shell (so it is the correct
control and must be skipped by any header assertion), and the existing document-level
assertions really do pass today, which is why this is a design slice and not a regression.

## Decisions

Autonomous run: there is no human to answer these, so each was decided here. Question,
options weighed, choice, and why.

### D1. Which of the item's three options?

Options: (A) a scroll affordance - gradient mask or chevron at the clipped edge;
(B) a collapsed menu below a breakpoint; (C) prioritised truncation - the current page plus
an overflow control.

**Choice: B, a collapsed menu below `md`.**

Why, against the other two:

- **A is not a control, or it is the most expensive option, and it cannot be both.** A
  gradient mask fails the acceptance criterion as written: the destination is neither
  visible nor behind a *control*. A chevron button is a control, but an honest one has to
  know whether the nav actually overflows, and that is a runtime measurement - a
  `ResizeObserver` or a scroll listener comparing `scrollWidth` to `clientWidth`. That
  introduces the JS-driven responsiveness this app has none of, adds an async lifecycle
  subject to the "end the generation before releasing the resource" invariant, and is
  **unpinnable in jsdom**, where every width is 0. The cheapest-looking option needs the
  most runtime machinery and gets the least test coverage.
- **C pays B's cost and adds measurement on top.** A true priority-plus nav needs the same
  per-item width measurement as A. The static variant - render the active destination plus
  a "More" disclosure - still needs a disclosure (so it pays every ARIA and focus cost of
  B), and it produces a control whose contents change per route, so the user's mental model
  of "where can I go" changes as they navigate. For **four** destinations, splitting them
  into one-plus-three is complexity with no payoff.
- **B is deterministic.** The breakpoint is a CSS media query, not a measurement, so it
  behaves identically in every engine, needs no JS, and every one of its states can be
  driven in a test. It is what a user on a phone expects. Its cost is honestly stated in
  D5 and D8 below.

The 2026-08-13 slice chose scrolling over a disclosure and named its own reason for
revisiting: it was "the most conservative and most reversible option", "flagged for the
human to overrule", taken with no hi-fi reference and no picture of the result. The picture
now exists, and it is the overruling.

### D2. Which breakpoint, and written in which direction?

Options: `sm` (640px) so the inline nav survives on landscape phones; `md` (768px), the
one breakpoint convention this app already has; a custom value sized to the measured
content floor (about 523px).

**Choice: `md`, with the narrow-width rules written as `max-md:` overrides on top of an
unchanged desktop base.**

Why `md`: the 2026-08-13 slice made "use `md:`, copied from the one site already doing it
right, rather than inventing a second convention" an explicit decision, and four components
now follow it. A custom breakpoint sized to a measured floor makes the collapse depend on
the length of the signed-in user's email address, which is exactly the kind of hidden
coupling the header floor already taught this project about. Stated cost: between 640px and
767px the menu collapses where the inline nav would have fit. That band is a landscape phone
or a small tablet, where a collapsed menu is conventional, and it is unmeasured by the
harness today either way.

Why `max-md:` rather than mobile-first `md:` resets: with the dropdown chrome as the base
and `md:` resets on top, the desktop class strings all change, and "the desktop header is
unchanged" becomes an argument instead of a fact. With desktop as the base, the shipped
desktop classes are literally untouched and the narrow behaviour is additive - which keeps
most of the "revert is a deletion" property the previous slice chose scrolling for. The
cost is that this introduces the repo's first `max-*` variant; it is the same breakpoint
value, read in the other direction, and the variant is registered in the installed Tailwind.

### D3. One copy of the links in the DOM, or two?

Options: render an inline nav and a separate collapsed nav, hiding one with `hidden`/
`md:hidden` (the common pattern); render one copy and switch its presentation with CSS;
render one copy and choose the mode in JS with `window.matchMedia`.

**Choice: one copy, always mounted, presentation switched by CSS.**

Two copies is the wrong answer three times over: it puts two links with the accessible name
"Jobs" in the accessibility tree in whatever moment both are display-visible; it breaks
`HoloShell.test.tsx`'s existing `getByRole('link', { name })` queries, which throw on
multiple matches, so a fix would have to rewrite the tests that guard admin gating; and
jsdom applies none of this app's CSS (no stylesheet is imported in `src/test/setup.ts`), so
in every unit test **both** copies are present and "visible" and no jsdom assertion can tell
the intended state from the broken one.

`matchMedia` avoids duplication but adds a subscription with a teardown, a jsdom stub in
every shell test, and the first JS-driven responsiveness in the app.

One copy, always mounted, gives the property that matters: the existing three nav tests keep
passing **byte-identical**, because the links are still in the DOM exactly once. The honest
corollary is that those tests would also pass against a completely broken collapsed state -
jsdom cannot see `display: none`. That is why AC12-AC16 are Playwright, not vitest.

### D4. What is the panel's positioning anchor?

Options: anchor to the `<nav>` with a fixed width, as `UserMenu` does (`absolute right-0
w-56`); anchor to the `<header>` and span it edge to edge.

**Choice: anchor to the `<header>`, full bleed (`max-md:left-0 max-md:right-0
max-md:top-full`).**

A `w-56` (224px) panel anchored at the nav's left edge starts roughly 100px in from the
viewport edge at 320px (22px header padding, the wordmark, a 24px gap), so it would reach
about 325px and overflow the viewport - re-creating the exact document overflow the
2026-08-13 slice closed, in the fix for its sequel. `left-0 right-0` cannot overflow by
construction, and full-bleed under the header is the conventional treatment. The `<header>`
is already `relative`, so it is the nearest positioned ancestor and no new positioned
element is introduced; the `<nav>` must therefore **not** gain `relative`.

### D5. What does the toggle say?

Options: an icon-only hamburger with `aria-label`; the text `Menu`; the active section's
name (Jobs / Workers / ...).

**Choice: the text `Menu`, rendered uppercase by CSS.**

`web/src` ships no icon component, so a hamburger means inventing an SVG - a visual design
decision with no hi-fi reference, in a slice that already has one. Text has a visible label
identical to its accessible name and is locatable by role and name in both test lanes.

**The DOM text is `Menu` and the uppercase is `text-transform`**, following `Eyebrow`'s
stated convention that callers pass normal-case text. Engines differ on whether a
transformed string reaches the accessible name, so **every locator in both lanes must match
case-insensitively** - `getByRole('button', { name: /menu/i })` - exactly as `UserMenu`'s
own tests already do for the email chip. Getting this wrong costs an implementation round in
the lane that cannot be run locally.

The active section's name was rejected: it makes the control's accessible name change per
route (so every Playwright locator for it becomes route-dependent), and it reads as a
section switcher rather than as a nav. Its real motivation - that collapsing the nav removes
the at-a-glance "where am I" cue the active underline gave - is a genuine cost of D1, and it
is mitigated rather than ignored: `NavLink` sets `aria-current="page"` on the active
destination inside the panel, and the active item keeps its accent marker there (D9). A
follow-up item is proposed for the label question so a human can overrule it.

### D6. Is the toggle a `PillButton`?

**Choice: a plain `<button>` with a literal class string, modelled on `UserMenu`'s toggle.**

`PillButton` is a function component with no `forwardRef`, and the focus-restore contract
needs a ref on the toggle. Adding `forwardRef` to a primitive with many consumers, to serve
one new call site, is a change to shared code that this slice does not need; `UserMenu`
already reaches for a plain `<button>` for the same reason. Reusing `UserMenu`'s toggle
visual language (pill, mono, uppercase, accent-tinted while open) also makes the two header
disclosures read as siblings, and every arbitrary value in that string already exists in the
tree, so no new CSS rule is emitted for the styling.

### D7. Is `aria-controls` present in both states?

`UserMenu` sets it only while open, because its panel is conditionally mounted and an IDREF
to a node that does not exist is an authoring error. This panel is **always** mounted (D3),
so the IDREF always resolves.

**Choice: `aria-controls` is present in both states here.** This is a deliberate divergence
from the sibling component, driven by the different mounting model, and it must be written
at the site so a reviewer does not "fix" it into agreement. `aria-expanded` is present in
both states in both components.

### D8. Extract a shared `Disclosure` primitive?

The house rule in this repo is **extract before the third consumer**. A collapsed nav menu
is the **second** disclosure beside `UserMenu`.

**Choice: no extraction in this slice. File the item that names the trigger.**

Beyond the count, the two differ in enough places that extracting from N=2 would be guessing
at the interface: `UserMenu`'s panel is conditionally mounted and this one is not (D7); its
panel is right-aligned and fixed-width and this one is full-bleed and breakpoint-scoped; its
items are three profile links plus a logout button and these are four route links. What they
genuinely share is the **behaviour** - Escape, outside mousedown, focusout containment,
modifier-click guard, containment-checked focus restore - which is the part a third consumer
would make safe to generalise. Duplicating roughly 40 lines of handler logic once, with a
comment at each site naming the other, is the cheaper wrong answer than a premature
abstraction over two dissimilar shapes.

### D9. What happens to the active-item indicator inside the panel?

The `NavLink` class function renders `border-b-2` plus `border-accent` when active. In a
vertical panel a full-width bottom border reads as a row separator rather than as a
selection marker.

**Choice: add `max-md:border-b-0 max-md:border-l-2` to the `NavLink` class string**, so the
accent marker becomes a left bar below `md` and stays an underline above it. `border-accent`
already sets the colour on all four sides, so no colour class changes. This is a design call
with no hi-fi reference; it is two classes, and its revert is deleting them.

These two classes are deliberately **not** pinned by AC11. Deleting them changes how the
active row looks and breaks nothing, so a pin on them would be a brittle assertion about
taste rather than a guard on the fix.

### D10. Does the nav keep `overflow-x-auto`?

**Choice: yes, scoped as `md:overflow-x-auto` on the panel.** Below `md` the panel is the
dropdown and must not be a scroll container. Above `md` the 2026-08-13 fallback still has a
job (a long email in the user chip can still put shrink pressure on the left group), it is
inert whenever the content fits, and it is the class the existing guard names.

The real guard - `expect(header.className).not.toMatch(/\boverflow-/)` - stays
**byte-identical**. The two class pins around it move from the `<nav>` to the panel, because
the element they describe moved. That edit is called out explicitly in AC10 rather than made
quietly.

### D11. Where do the new browser assertions live, and is a fourth width added?

Options: extend the existing per-surface test in `layout.spec.ts`; add a second per-surface
test; put everything in a new spec; add a 768px width to `WIDTHS`.

**Choice: extend the existing per-surface test with a reachability check after the
measurement and screenshot, and add one new `header-nav.spec.ts` for the disclosure's own
behaviour. No fourth width in `WIDTHS`.**

The reachability check needs no extra page load, so putting it in the existing test is free;
a second per-surface test would add 42 page loads per project for the same information. The
test's title is updated to say what it now asserts - a title that says "does not overflow
horizontally" over a body that also asserts reachability is exactly the test-honesty problem
this project audits for.

A fourth width in `WIDTHS` costs 14 more surfaces times two engines for one property that a
single page needs. `header-nav.spec.ts` measures 767 and 768 on `/jobs` alone instead, which
pins the breakpoint **value** rather than merely pinning that some collapse happens
somewhere.

Ordering inside the extended test is fixed and load-bearing: measure, attach, screenshot the
**closed** state (which is the state the bug is about), assert the three existing overflow
expectations, and only then open the menu. The open state gets its own document-overflow
assertion, and its own screenshot on the `jobs` surface only - the header is the same
component on all 13 shell surfaces, and the artifact exists for a human to open, not as an
assertion, so 26 near-identical PNGs per engine would make the review worse rather than
better.

### D12. Which overlay is this, for the batch-wide overlay rule?

Checked against both items the rule names:

- `idea-2026-08-09-body-level-portal-inert-marking`: **does not apply.** This panel is
  **header-scoped**, rendered in place inside `<header>`; it is not portalled, does not
  touch `document.body`, and does not register with `dialogStack`. The item's concern is a
  node appended to `<body>` while a dialog is open. Nothing here creates one. It follows
  `UserMenu`'s own reasoning: a disclosure needs its panel to follow the toggle in DOM order
  so Tab reaches it natively, and moving it to `<body>` would invalidate the header's
  measured stacking fix.
- `idea-2026-08-12-document-z-index-layering-scale`: **applies, and adds no new value.** The
  panel carries `max-md:z-50`, which is local ordering **inside the header's stacking
  context** - identical in kind to `UserMenu`'s `z-50`, and for the same reason: the
  header's own `backdrop-blur` makes it a stacking context, so what actually puts this panel
  over `<main>` is the header's `relative z-10`, which already exists. The scale gains a
  fourth entry that duplicates an existing one; it does not gain a new number. If that item
  is ever done, this panel is a row in its table.

### D13. Does the collapsed menu take a modal posture?

**Choice: no.** No scrim, no scroll lock, no `inert`, no `aria-hidden` on the background, no
Tab trap. Tab out is a dismiss route for a disclosure. This mirrors `UserMenu` exactly, and
`DialogShell`'s machinery is not to be copied here.

### D14. Is any coordination needed between this disclosure and `UserMenu`?

**Choice: none. Do not add shared state.** Mutual exclusion falls out of the two rules both
components already implement. Pressing either toggle fires a document `mousedown` outside
the other's container, which closes it. Tabbing from one to the other moves focus outside
the first's container, and its `focusout` containment rule closes it. Both routes are
covered by tests. Escape while both are somehow open would close both, which the
`DialogShell` and `UserMenu` comments already record as acceptable and not guaranteed by
registration order.

### D15. Backend data

The batch rule is to omit data the backend cannot supply rather than fabricate it. This
slice renders **no data**: four static routes and one admin boolean the shell already has.
The one concrete instance of the rule is a temptation declined - the hi-fi's `HoloShell`
topbar includes a "sync indicator" that the shipped shell does not render and no endpoint
feeds. It stays unported.

## Design

### Component structure

One file changes: `web/src/shell/HoloShell.tsx`. `NAV`, the admin filter, `UserMenu`, the
header's own classes, `<main>`, and every other file are untouched.

```
<header className="relative z-10 ..."/>            unchanged, still `relative`, still no overflow
  <div className="flex min-w-0 items-center gap-6">  unchanged left group
    <Eyebrow>RELAY</Eyebrow>                         unchanged
    <nav className="min-w-0" aria-label="Main">      landmark, always present, NOT positioned
      <button ref={toggleRef} className="md:hidden ..."
              aria-expanded={open} aria-controls={panelId}>Menu</button>
      <div id={panelId} className={panelClass}>      one copy of the links
        {nav.map(NavLink)}                           unchanged NavLink bodies + D9's two classes
      </div>
    </nav>
  </div>
  <UserMenu ... />                                   unchanged
```

The `<nav>` landmark now wraps the toggle as well as the links, so the landmark exists at
every width even while the panel is collapsed. `aria-label="Main"` names it, which matters
once it contains a control.

State and handlers live in `HoloShell` and are a deliberate transcription of `UserMenu`'s,
not an invention: `open`, `containerRef` on the `<nav>`, `toggleRef`, `useId` for `panelId`,
`closeAndRestoreFocus()` (reads containment **before** `setOpen`), `close()`,
`onNavItemClick(e)` with the modifier/button predicate, an `open`-gated effect registering
`mousedown` and `keydown` on `document`, and `onBlur` on the `<nav>` ignoring a null
`relatedTarget`. Each carries a comment pointing at `UserMenu` as the sibling, and
`UserMenu` gains a reciprocal pointer, so the pair is discoverable from either end.

### Class strings

Written out so the implementer does not have to invent them, and so review has something
exact to check. All literals; nothing computed. Note the repo rule that Tailwind scans
source, including comments - these belong in `className` attributes, not in prose inside
`web/`. This spec lives outside the Vite root, so spelling them here is safe.

Toggle:

```
md:hidden rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase
tracking-[0.12em] transition-colors
+ open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'
```

Panel (`panelClass`), three parts concatenated:

```
base       min-w-0 gap-0.5 md:flex md:overflow-x-auto
visibility open ? 'flex' : 'hidden'
narrow     max-md:absolute max-md:left-0 max-md:right-0 max-md:top-full max-md:z-50
           max-md:flex-col max-md:border-b max-md:border-border max-md:bg-popover
           max-md:p-1.5 max-md:shadow-xl
```

`hidden md:flex` is the documented Tailwind idiom: the variant rule is emitted after the
base utility, so `md:flex` wins at and above the breakpoint regardless of state. `bg-popover`
is load-bearing for the same reason it is in `UserMenu` - `GlassPanel` and the header set no
`background-color`, so a panel over live content without it reads straight through.

NavLink additions (D9): `max-md:border-b-0 max-md:border-l-2` appended to the existing class
function's shared segment.

### Behaviour by width

| width | toggle | panel | notes |
|---|---|---|---|
| below 768 | visible, `aria-expanded=false` | `display:none` | header height unchanged from today |
| below 768, open | visible, `aria-expanded=true` | full-bleed dropdown under the header | overlays `<main>`; header `z-10` is what orders it |
| 768 and up | `display:none`, unreachable | inline row, scrolls if pressed | identical to what ships today |

The only way to reach "open at 768 and up" is to resize a window with the menu open. The
`md:` rules win in that state, so the panel renders inline and correct; `aria-expanded`
reads `true` on a toggle that is `display:none` and therefore absent from the accessibility
tree. That is inert, and no resize listener is added to tidy it - adding one would introduce
the async lifecycle D1 rejected.

### Keyboard model

Identical to `UserMenu`'s, which is the point of copying it rather than designing one:

- **Enter/Space on the toggle** opens and closes. Focus stays on the toggle on open (no
  auto-focus into the panel); the panel follows the toggle in DOM order, so **Tab** reaches
  the first destination next.
- **Tab** through the destinations, then out of the last one: the panel closes and focus
  lands on the next control (the user chip). The close must not restore focus - the user
  asked to go forward.
- **Shift+Tab** from the first destination lands on the toggle, which is inside the
  container, so the panel stays open.
- **Escape** closes and returns focus to the toggle, **only if focus was inside the
  container**. If focus was outside (the WebKit case where a click does not focus a button),
  the close must not steal focus.
- **Outside mousedown** closes without touching focus, because mousedown fires before the
  browser moves focus and a restore would steal it from the control being pressed.
- **Activating a destination** closes and restores focus to the toggle, except for a
  modifier or non-primary click, which leaves the panel open and touches no focus.
- **No arrow keys, no roving tabindex, no `role="menu"`.** This is a disclosure containing
  navigation links, the case the menu role's own specification excludes, and `role="menuitem"`
  on an `<a href>` replaces the link role.

### ARIA

- `<nav aria-label="Main">` - named landmark, present at every width.
- Toggle: `<button type="button" aria-expanded={open} aria-controls={panelId}>Menu</button>`.
  Both attributes present in both states (D7).
- Panel: `id={panelId}`, `data-testid="header-nav-panel"`, no `role`, no `tabindex` on it or
  on any descendant.
- Destinations: unchanged `NavLink`s, so `aria-current="page"` continues to mark the active
  one inside the panel.

## Acceptance criteria

Each maps to one named test and its lane. "Pin" and "guard" are used in the 2026-08-13
sense: a pin asserts a class string whose effect was measured elsewhere; a guard asserts a
behaviour the test can actually observe.

**vitest / jsdom** (`web/src/shell/HoloShell.test.tsx`; jsdom applies no CSS, so nothing
here can see a breakpoint):

- **AC1** The toggle exposes disclosure semantics: `aria-expanded` is `false` closed and
  `true` open, and `aria-controls` resolves to the panel's `id` in **both** states.
  Test: `the nav toggle exposes disclosure semantics in both states`. Guard.
- **AC2** Escape closes and returns focus to the toggle when focus was inside; and does not
  touch focus when focus was outside. Tests:
  `Escape closes the nav panel and returns focus to the toggle when focus was inside` and
  `Escape does not steal focus when focus was outside the nav container`. Guards. The second
  uses the same `blur()` stand-in and the same `vi.spyOn(toggle, 'focus')` instrument as its
  `UserMenu` counterpart, so the pair cannot pass by measuring different things.
- **AC3** An outside mousedown closes the panel and never calls `toggle.focus()`.
  Test: `an outside mousedown closes the nav panel and never touches the toggle focus`.
  Guard.
- **AC4** Activating a destination closes the panel and restores focus; a modifier-click
  leaves it open and touches no focus. Tests:
  `selecting a destination closes the nav panel and returns focus to the toggle` and
  `a modifier-clicked destination leaves the nav panel open and does not touch focus`.
  Guards. The second is the react-router `Link`-calls-onClick-first hazard and is proven RED
  against an unconditional close.
- **AC5** Tab out of the last destination closes the panel without stealing the
  destination; Shift+Tab from the first lands on the toggle and leaves it open. Tests:
  `Tab out of the last destination closes the nav panel without stealing the destination`
  and `Shift+Tab from the first destination lands on the toggle and leaves the panel open`.
  Guards.
- **AC6** No document `keydown` listener is registered while the panel is closed.
  Test: `no document keydown listener is registered while the nav panel is closed`. Guard.
  Every other test in the file opens the panel first, so this is the only one that looks at
  the closed state's listener lifetime.
- **AC7** The panel is a plain disclosure: no `role` on the panel, no `role="menu"` or
  `role="menuitem"` inside it, no `tabindex` attribute anywhere in it, and - as the positive
  control that the sweep saw a populated panel - the expected number of real `a[href]`
  destinations. Test: `the nav panel is a plain disclosure - no menu roles, no roving tabindex`.
  Guard.
- **AC8** Each destination is rendered **exactly once** in the header. Test:
  `each destination is rendered exactly once in the header`. Guard, and the specific one
  that reddens if someone re-solves this with a duplicated mobile nav.
- **AC9** The three existing nav tests (`always shows the non-admin nav entries`,
  `hides the Admin nav entry from non-admins`, `shows the Admin nav entry to admins`) pass
  **byte-identical**, with no edit to the test file for those three. This is an acceptance
  criterion on the diff, not a new test: a change that forces them to be rewritten has
  changed the DOM contract in a way D3 rejected.
- **AC10** The scroll-container guard survives the restructure:
  `expect(header.className).not.toMatch(/\boverflow-/)` stays byte-identical inside
  `the nav is the only shrinkable scroll container in the header`; its two class pins move
  from the `<nav>` to the panel (`min-w-0`, `md:overflow-x-auto`), and a comment at the site
  records that the element moved and the rule did not.
- **AC11** Class pins for the collapse, labelled as pins: the toggle carries `md:hidden`;
  the panel carries `md:flex` and `max-md:absolute`, `max-md:left-0`, `max-md:right-0`,
  `max-md:top-full`, `max-md:z-50`, `max-md:flex-col` and `max-md:bg-popover`. Test:
  `REGRESSION PIN: the collapsed nav is a full-bleed opaque panel below md and inline above it`.
  Pin, not guard - jsdom cannot evaluate any of it. Its whole job is to make a silent
  deletion of one of these classes visible in the unit lane; the behaviour is AC12-AC16.

**Playwright** (`web/e2e/`):

- **AC12** On every authenticated surface, at 320, 375 and 1280, every primary destination
  is **visible, or reachable through a control that is visible**. In
  `layout.spec.ts`, inside the existing per-surface test, renamed to
  `${s.name} fits the viewport and keeps every destination reachable`, skipped when
  `s.anonymous` (the `/auth` control renders no shell). The predicate, in a new
  `web/e2e/nav.ts` helper `expectDestinationsReachable(page)`: for each of Jobs, Workers,
  Schedules, Admin - if the header link is visible, pass; otherwise require the `Menu`
  toggle to be visible, click it, and require the link to be visible then. Escape afterwards
  so the page is left as it was found. This is the assertion the item asks for, stated so
  that it fails at HEAD.
- **AC13** Opening the collapsed menu does not overflow the document. Same test, after the
  open: `documentElement.scrollWidth <= clientWidth` re-measured with the panel open. The
  three existing closed-state assertions are unchanged and still run first.
- **AC14** A human can see the result: the closed-state full-page screenshot per surface per
  width is unchanged, and the `jobs` surface additionally attaches
  `jobs-<width>-nav-open.png` at 320 and 375. Artifacts, not assertions, and the merge of
  this slice includes one human pass over them - the same process commitment
  `layout.spec.ts` already carries.
- **AC15** The breakpoint is pinned from both sides, by value. New spec
  `web/e2e/header-nav.spec.ts` on `/jobs`:
  - `at 1280 every destination is inline and the collapse toggle is not exposed` - four
    links visible, toggle `toBeHidden()`.
  - `at 768 the nav is still inline` and `at 767 the nav is collapsed` - two viewports one
    pixel apart, which is what makes this a test of `md` rather than of "something collapses
    somewhere". Both also assert no document overflow.
  - `at 375 the toggle is the only visible nav control` - toggle visible, all four links
    hidden.
- **AC16** Real key events drive the disclosure in both engines. Same new spec, in a
  describe titled with the `@webkit` tag so `playwright.config.ts`'s `grep` runs it in
  chromium and WebKit:
  `a real Tab press reaches the collapse toggle and Enter opens the panel`, and
  `Escape closes the panel and returns focus to the toggle`. This is the lane that matters
  for the engine divergence `UserMenu` documents - WebKit not focusing a button on click -
  and it is the only lane in the repo that can send a real key. jsdom's AC2 pins the
  handler; this pins that a user can get there.
- **AC17** The class strings reach the production bundle. Not a test: a one-off build to a
  scratch `outDir` and a grep for the emitted `max-md:` rules, per the 2026-08-13 finding
  that a no-op class-string fix and a working one are indistinguishable to every unit test
  in this repo. Required because this slice is **entirely** class strings plus handler code,
  and because `max-*` is the repo's first use of that variant. `make test-e2e` runs against
  the production-embedded bundle, so AC12-AC16 also fail if the CSS is missing - AC17 is the
  check that says *why* rather than leaving 40 timeouts to be diagnosed.

### Lane availability

`make test-e2e` needs Docker, Postgres, a fresh `web/dist` and a rebuilt `relay-server`, and
the implementing engineer may not be able to run it. If it cannot be run, say so plainly
rather than substituting: AC1-AC11 are the whole of what vitest can establish, and they
establish that the handlers behave and the class strings are present - **not** that anything
collapses, that anything is visible, or that the CSS exists. AC12-AC17 are the only evidence
for the defect this item is actually about, and a slice that ships without them ships with
its central claim unverified.

## Out of scope

- **Extracting a shared disclosure primitive.** D8. Second consumer; the house rule is
  extract before the third. Proposed as a follow-up with its trigger named.
- **The nav scrollbar's appearance at `md` and up.** Still visible, still deliberate, still
  a taste call that belongs to whoever owns the design.
- **Any change to `UserMenu`** beyond a one-line comment pointing at its new sibling.
- **Any change to `Table`, the tab bars, or the narrow-viewport treatment of tables.** The
  closed 2026-08-12 item already ruled that horizontal scrolling is the honest minimum for
  wide tables.
- **A sync indicator, or anything else in the hi-fi topbar that no endpoint feeds.** D15.
- **Documenting the z-index scale** (`idea-2026-08-12-document-z-index-layering-scale`) and
  **body-level portal inert marking** (`idea-2026-08-09-body-level-portal-inert-marking`).
  Both checked in D12; this panel adds no new z-index value and creates no body-level node.
- **A non-admin browser session.** The harness seeds one admin. Admin gating stays a jsdom
  assertion; a second seeded user is its own slice.
- **A fourth width in `WIDTHS`.** D11.
- **Any JS-driven responsiveness**: no `matchMedia`, no `ResizeObserver`, no width state, no
  breakpoint constant in TypeScript. If a task in the plan reaches for one, the design is
  being re-litigated and the plan should stop.

## Backlog items this closes

- `docs/backlog/bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports.md`

Closed via `/backlog close`, which does the `git mv` into `docs/backlog/closed/` and stamps
the frontmatter. The Resolution note should record the three corrections to the item in the
Context section above, not only the fix, and should say that Decision 1 of the 2026-08-13
slice (scroll rather than collapse) is now overruled by the picture that slice could not
take.

## Proposed follow-up backlog items

Proposals only. The conductor files; the human accepts.

1. **`idea` - extract a shared header disclosure primitive when a third consumer appears.**
   `UserMenu` and the collapsed nav now duplicate roughly 40 lines of Escape / outside
   mousedown / focusout containment / modifier-click / containment-checked focus restore.
   The house rule is extract before the third consumer, and the trigger to state explicitly
   is: **any third header or in-page disclosure**. Records what the two already disagree on
   (mount model, alignment, item kinds) so the extraction starts from three data points
   rather than two.
2. **`idea` - the collapsed nav toggle does not say where you are.** Collapsing removes the
   at-a-glance active-section cue. `Menu` was chosen over a route-dependent label (D5) for
   locator stability and because the control is a nav, not a switcher. If a human prefers
   the section name, that is a small change with a real cost to every Playwright locator for
   the toggle, and it should be decided rather than drifted into.
3. **`idea` - the e2e harness has no non-admin session, so admin nav gating is jsdom-only.**
   `HoloShell.test.tsx` proves the Admin entry is absent for a non-admin; nothing in a real
   browser does, and the collapsed panel is a second place that filter must hold. Needs a
   second seeded user and a second `storageState`, which is a harness slice rather than a
   nav one.
4. **`idea` - `layout.spec.ts` still cannot tell "fits" from "clipped behind a scroller"**
   for anything other than the header nav. `web/e2e/README.md` states the gap and cites this
   bug as its instance; this slice removes the instance and leaves the gap. The remaining
   in-tree scrollers are `Table`'s wrappers, which have a keyboard affordance and a
   `role="group"` name, so the item is about the missing **general** assertion, not about a
   known second defect.
