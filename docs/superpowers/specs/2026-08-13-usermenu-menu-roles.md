# UserMenu: fulfil the dropdown's advertised ARIA contract - Design

Date: 2026-08-13
Status: Draft (autonomous cycle; conductor review)

## Overview

`web/src/shell/UserMenu.tsx` renders the header account dropdown. Its toggle carries
`aria-haspopup="menu"` (`UserMenu.tsx:34`) and the thing it points at is a plain `<div>`
of three `react-router` `Link`s and one `<button>` (`UserMenu.tsx:53-82`) with no
`role="menu"`, no `role="menuitem"`, no `aria-controls`, and no keyboard model beyond a
document-level Escape that closes without restoring focus (`UserMenu.tsx:19-21`).

Backlog item: `docs/backlog/feature-2026-06-05-usermenu-panel-menu-roles.md` (closed by
this slice). Frontend-only. No backend change, no new endpoint, no new dependency.

Written in autonomous gate mode: every question below was decided here and carries a
one-line rationale in the Decisions section rather than being asked.

**This spec inverts the backlog item's Proposal.** The item asks for `role="menu"` /
`role="menuitem"` plus roving-tabindex arrow navigation. This spec instead **removes
`aria-haspopup="menu"`** and ships the surface as an ARIA **disclosure** of ordinary
links. The reasoning is in "The ARIA contract decision" below; it is stated loudly here
because anyone reading only the item will expect the opposite change.

## Verified current state

Read in full: `web/src/shell/UserMenu.tsx` (86 lines) and `web/src/shell/UserMenu.test.tsx`
(58 lines).

| Fact | Evidence |
|---|---|
| Open state is one `useState` boolean; no context, no registry, no portal | `UserMenu.tsx:11` |
| Container is a `<div ref>` with `relative`; the toggle and the panel are siblings inside it | `UserMenu.tsx:31`, `:32`, `:52` |
| Toggle carries `aria-haspopup="menu"` and `aria-expanded={open}`. **No `aria-controls`.** | `UserMenu.tsx:34-35` |
| Toggle's accessible name is the raw email, nothing else | `UserMenu.tsx:38` |
| Panel is `GlassPanel` (renders a `<div>` by default, spreads unknown props onto it) | `UserMenu.tsx:53`, `web/src/components/holo/GlassPanel.tsx:19-25` |
| `data-testid="user-menu-panel"` **does** exist - the item's claim is confirmed | `UserMenu.tsx:54` |
| Panel is conditionally rendered (`{open && ...}`), so it is absent from the DOM when closed | `UserMenu.tsx:52` |
| Panel has **no `id`** and **no `role`** | `UserMenu.tsx:53-55` |
| Items are 3 `Link`s (`<a href>`) + 1 `<button>`, with a non-interactive email header div and a divider div between them | `UserMenu.tsx:57-59`, `:60-74`, `:75`, `:76-81` |
| **No item has an `onClick` that closes the menu.** The three `Link`s have none at all; the logout button calls `onLogout` only | `UserMenu.tsx:60,63,69`, `:77` |
| Close routes today: document `mousedown` outside the container, and document `keydown` Escape. Both listeners are registered only while `open` | `UserMenu.tsx:14-28` |
| Neither close route moves focus anywhere | `UserMenu.tsx:16-21` |
| No focus is moved on open either | `UserMenu.tsx:33` |
| The menu does **not** participate in `dialogStack` and holds no `inert`/scroll-lock/portal machinery | no import of `../components/dialog/*` anywhere in the file |
| Rendered once, in the app header, before `<main>` in document order | `web/src/shell/HoloShell.tsx:70-72` |
| `aria-haspopup` occurs in exactly two places in `web/src`: the source line and one test assertion | `UserMenu.tsx:34`, `UserMenu.test.tsx:34` |
| No `role="menu"`, `role="menuitem"` or roving tabindex exists anywhere in `web/src` - there is no in-repo precedent to follow | tree sweep for `role="menu`, `roving` |

### Two defects the item does not mention

1. **The menu stays open after you select a navigation item.** The three `Link`s have no
   `onClick` (`UserMenu.tsx:60,63,69`), `UserMenu` lives in the persistent shell and is
   not remounted by a route change (`HoloShell.tsx:70`), and the outside-mousedown
   handler does not fire because the press target is *inside* `ref.current`
   (`UserMenu.tsx:17`). So clicking `Profile` navigates and leaves the dropdown hanging
   open over the page it just navigated to. The prototype does close on select
   (`design_handoff_relay_holo/hifi3-holo-pages.jsx:221`, `goTo` calls `setOpen(false)`
   before navigating); the shipped component dropped that half. This was invisible until
   2026-08-12, when `/profile/*` stopped being a placeholder and the links started
   resolving to real pages (`docs/superpowers/specs/2026-08-12-profile-pages.md`,
   `web/src/profile/`).
2. **Escape closes the menu and drops focus on the floor.** `setOpen(false)`
   (`UserMenu.tsx:20`) unmounts the panel; if focus was on an item inside it, jsdom and
   browsers both fall back to `<body>`, so a keyboard user's next Tab restarts from the
   top of the document. This is the exact gap `DialogShell` fills for modals
   (`DialogShell.tsx:276-282`).

There is also a **third** gap that only exists once items are reachable by Tab: today a
user can Tab from the last item straight into `<main>` and the dropdown stays open behind
them, floating over content they are now interacting with. Nothing closes on focus
leaving the container.

## The ARIA contract decision

**Decision: correct the advertised contract. Remove `aria-haspopup` and ship an ARIA
disclosure. Do NOT implement `role="menu"` / `role="menuitem"` / roving tabindex.**

The item's premise is right and its remedy is the wrong one of the two available. The
premise - "a screen-reader user is told a menu is there and then handed something that is
not one" (`feature-2026-06-05-usermenu-panel-menu-roles.md:43-45`) - is a true statement
about a contract mismatch. A contract mismatch has two fixes: implement the promise, or
stop making it. Which one is correct depends on whether the promise was the right promise,
and here it was not.

Five reasons, in order of weight:

1. **Three of the four items are site navigation, and `role="menu"` is specified for
   actions.** `/profile`, `/profile/password` and `/profile/sessions`
   (`UserMenu.tsx:60,64,70`) are links to pages that now exist (`web/src/profile/`). The
   ARIA authoring practices define `menu` as a widget offering a list of actions or
   functions, and state directly that it should not be used for site navigation. One
   item out of four (`Log out`, `UserMenu.tsx:76-81`) is an action. Building a menu
   around a 3:1 navigation majority is applying the pattern to the case its own
   specification excludes.
2. **`role="menuitem"` on an `<a href>` destroys the link role.** Role is not additive:
   an anchor announced as "menu item" is no longer announced as "link", drops out of a
   screen reader's links list, and loses the affordances a user has for links (the
   "open in new tab" mental model, browse-mode navigation). We would be trading a real,
   working semantic for a synthetic one to satisfy an attribute we chose ourselves.
3. **The menu contract removes the items from the tab order, and here that is a
   regression.** A conforming `role="menu"` uses roving tabindex: exactly one item is
   tabbable and the rest carry `tabindex="-1"`. A keyboard user who today Tabs into the
   header, opens the dropdown and keeps Tabbing would, after the change, find the
   navigation links unreachable by Tab and have to know to press ArrowDown. That is
   defensible for a set of actions inside an application widget and poor for three
   ordinary page links.
4. **The disclosure discharges the same duty with a quarter of the machinery.** No roving
   tabindex, no arrow keys, no Home/End, no typeahead question, no "Tab closes the menu"
   special case, no `aria-activedescendant`-versus-real-focus decision. Every mechanism
   not built is a mechanism that cannot be built wrong, and the two real defects found
   above (stays open on select, drops focus on Escape) are orthogonal to the roles and
   get fixed either way.
5. **Nothing in the hi-fi asks for the menu contract.** The prototype's items are
   `<div onClick>` with no `tabIndex`, no role and no key handling
   (`hifi3-holo-pages.jsx:270-296`) - i.e. not keyboard-operable at all. The hi-fi is
   authoritative for look, and it implies **no** keyboard affordance beyond what the
   shipped app already has. There is no design intent being contradicted here; the
   `aria-haspopup="menu"` attribute is the only thing that ever claimed "menu", and it
   arrived as a one-line fix on 2026-06-03 (`docs/backlog/closed/bug-2026-06-03-usermenu-aria-attributes.md`)
   without the panel it was describing.

**What we lose by deciding this way**, stated honestly: users of some screen readers
have learned that an avatar/account control in a web app header is usually a `menu`, and
GitHub's account dropdown really is one. Nothing here is a violation - the disclosure
pattern is a first-class APG pattern and this is what most design systems do for account
menus containing links. But if this menu ever becomes actions-only (no navigation links),
the calculus flips and `role="menu"` becomes correct. That trigger is recorded in
Follow-ups.

**What this does NOT license:** silently deleting `aria-haspopup` and stopping. The
attribute goes *and* `aria-controls`, the focus restore, the close-on-select and the
close-on-focus-leave all land in the same change. Removing the advertisement without
fixing the keyboard behaviour would close the item on a technicality.

## What is reusable from DialogShell, and what is not

The item instructs reuse of the DialogShell reasoning rather than rediscovery
(`feature-2026-06-05-usermenu-panel-menu-roles.md:48-53`). Read
`web/src/components/dialog/DialogShell.tsx`, `dialogStack.ts` and
`docs/retros/2026-08-09-dialog-hardening.md`. The honest split:

### Reusable (as reasoning; no code is shared)

- **Escape must be a document-level listener, never a panel `onKeyDown`.**
  `DialogShell.tsx:59-75` records this as the iteration's one high-severity finding: a
  React `onKeyDown` only fires when the event target is a descendant of the panel, and
  focus leaves the panel through more routes than the component can close. This applies
  to the menu **more** strongly than to a dialog, because Safari does not focus a
  `<button>` on click, so after a mouse-open the menu can be open with `activeElement ===
  document.body` and a panel-scoped Escape would never fire at all. `UserMenu.tsx:19-21`
  already has the document listener; the rule here is *do not "tidy" it onto the panel*.
- **Capture `focusWasInside` before tearing down, and only restore focus if it was.**
  `DialogShell.tsx:234-239` and `:276-282`. Yanking focus to the trigger when the user's
  focus was never inside is focus theft. This directly decides the outside-click rule
  below.
- **End the generation before releasing the resource**, the project invariant in its
  frontend form (`dialogStack.ts:22-38`). Here the "generation" is the open state and the
  decision it drives is ordering inside `close()`: read `activeElement` and decide about
  focus *before* the state change that unmounts the panel.
- **`stopImmediatePropagation` interaction.** `DialogShell.tsx:355-370` explains that the
  shell's document Escape listener calls `stopImmediatePropagation` specifically so a
  sibling document listener on the same node - named in that comment as UserMenu's - is
  suppressed, and that the two are structurally prevented from overlapping anyway because
  the toggle goes `inert` while a dialog is open. This spec preserves that structure
  exactly: the menu keeps one document `keydown` listener that exists only while open.
  Nothing here re-orders or duplicates it.

### Inapplicable (a menu is not a modal)

- **The Tab trap** (`DialogShell.tsx:382-415`). A disclosure must not trap Tab; Tab out is
  a *dismiss* route here, not something to intercept.
- **The `inert` / `user-event` evidence** (`DialogShell.tsx:48-57`). That measurement
  exists because a modal must make the background unreachable. A menu must not: the page
  behind it stays fully interactive. The finding is real and simply does not bear on this
  surface.
- **The jsdom `HTMLDialogElement` evidence** (`dialogStack.ts:12-20`). `<dialog>` was
  never a candidate for a dropdown.
- **`dialogStack` registration, the scrim, the scroll lock, the `aria-hidden` background
  marking, `isTopmost`.** All are modality. The menu must not lock scroll and must not
  hide the page from assistive tech.
- **The portal** (`DialogShell.tsx:417-447`, `dialogStack.ts:57-63`). Two independent
  reasons not to: (a) under the disclosure pattern the panel **must** follow the toggle in
  DOM order so Tab reaches it, which a body-level portal breaks; (b) the dropdown's paint
  order is currently solved by `relative z-10` on the `<header>`
  (`HoloShell.tsx:29-49`, measured over 275 hit-test points), and moving the panel out of
  the header would invalidate that measurement and re-open the stacking problem. See
  cross-links.
- **The landmark focus fallback** (`DialogShell.tsx:149-162`). Its whole premise is a
  trigger that may have been removed from the DOM. The menu's trigger is a sibling in the
  same component and is always connected while the panel exists, so the fallback branch is
  unreachable and must not be copied.

## The chosen model

### ARIA

| Element | Attributes |
|---|---|
| Toggle `<button>` | `aria-expanded={open}`; `aria-controls={open ? panelId : undefined}`. **No `aria-haspopup`.** Accessible name unchanged (the email). |
| Panel `<div>` (`GlassPanel`) | `id={panelId}` from `useId()`. **No role.** `data-testid="user-menu-panel"` unchanged. |
| Items | Unchanged elements and unchanged roles: three `<a href>` links, one `<button>`. No `tabindex` on any of them. |

`aria-controls` is set only while the panel is rendered, because the panel is
conditionally mounted (`UserMenu.tsx:52`) and an IDREF pointing at a non-existent element
is an authoring error. `aria-expanded` remains present in both states.

### Keyboard model, every key

| Key | Behaviour | Mechanism |
|---|---|---|
| Enter / Space on the toggle | Toggles open. Focus stays on the toggle. | Native `<button>` + the existing `onClick` (`UserMenu.tsx:33`). No new code. |
| Tab (menu closed) | Toggle is one stop in the header's natural order. | Native. |
| Tab (menu open, focus on toggle) | Moves to the first item, because the panel follows the toggle in DOM order and every item is a natural tab stop. | Native. **This is the entire reason not to portal.** |
| Tab (menu open, within the panel) | Moves to the next item. | Native. |
| Tab from the last item (`Log out`) | Focus leaves the container; **the menu closes**. Focus lands wherever Tab was going. | New `onBlur` (focusout) rule below. |
| Shift+Tab from the first item | Moves to the toggle, which is *inside* the container, so the menu **stays open**. | Native + the containment check. |
| Shift+Tab from the toggle (menu open) | Focus leaves the container; **the menu closes**. | Same focusout rule. |
| Escape (anywhere, menu open) | Closes. Focus returns to the toggle **only if focus was inside the container**; otherwise focus is left alone. | Existing document listener (`UserMenu.tsx:19-21`), plus the restore. |
| Enter on a link | Native navigation, **and the menu closes** with focus returned to the toggle. | New `onClick` on each `Link`. |
| Enter / Space on `Log out` | Native activation, `onLogout()` fires, **and the menu closes**. | New `setOpen(false)` alongside the existing `onLogout` call (`UserMenu.tsx:77`). |
| ArrowUp / ArrowDown / Home / End / PageUp / PageDown | **Nothing.** No handler; the browser's default (scrolling) stands. | Deliberate - decision 3. |
| Any printable character | **Nothing.** No typeahead. | Deliberate - decision 3. |

### Focus rules

- **On open: focus is not moved.** The disclosure pattern does not move focus into
  disclosed content, and moving focus on a *mouse* open is actively hostile. Tab reaches
  the content because it is next in DOM order.
- **On close, exactly one rule:** if `container.contains(document.activeElement)` at the
  moment the close is decided, focus the toggle; otherwise do nothing. Read the check
  **before** the state update that unmounts the panel.
- **Route-by-route consequences of that one rule:**
  - Escape with focus on an item -> toggle regains focus. (Fixes defect 2.)
  - Escape after a mouse-open where focus is on `<body>` -> focus is left on `<body>`. No
    theft.
  - Item click -> focus was on the item, so the toggle regains focus. For the three links
    this also gives the app its only focus management across a navigation; without it,
    focus falls to `<body>` on every menu-driven route change.
  - Focusout (Tab out) -> by construction focus is already *outside*, so the rule
    no-ops and the user's Tab destination keeps focus. Correct.
  - **Outside mousedown -> the rule must NOT run.** Native ordering is `mousedown` before
    the focus change, so at handler time `activeElement` is still inside the panel and a
    naive shared `close()` would yank focus to the toggle after the user clicked an input
    elsewhere. The outside-mousedown path closes **without** touching focus. This is the
    `!focusWasInside` reasoning from `DialogShell.tsx:239-275` arriving at a different
    answer because the event ordering is different, which is exactly why it had to be
    re-derived rather than copied.

### The focusout close rule, stated precisely

On the container's `onBlur` (React maps this to the native bubbling `focusout`):

> Close if and only if `event.relatedTarget` is a `Node` and
> `!container.contains(event.relatedTarget)`.

**`relatedTarget === null` must be treated as "do not close."** Verified in
`web/node_modules/jsdom/lib/jsdom/living/nodes/HTMLOrSVGElement-impl.js:57-58,82-83`:
jsdom fires `focusout` with `relatedTarget` set to the newly focused element on a focus
move, and with `relatedTarget === null` on a bare `blur()` or a blur to nothing. In a real
browser, pressing the mouse on the non-focusable email header inside the panel
(`UserMenu.tsx:57-59`) blurs to `<body>` with a null `relatedTarget`; closing on that
would make the panel vanish when a user clicks its own header text. The existing
outside-mousedown handler (`UserMenu.tsx:16-18`) already owns the "clicked somewhere else"
case, so the null branch has a correct owner and needs no second one.

## Architecture

One file changes: `web/src/shell/UserMenu.tsx`. No new file, no new dependency, no change
to `HoloShell`, the router, or any other component.

Shape of the change (described, not prescribed line by line):

- `useId()` for the panel id; a `toggleRef` for the focus restore. The existing container
  `ref` (`UserMenu.tsx:12`) is reused for both containment checks.
- One `closeAndRestoreFocus()` helper used by the Escape handler and by all four item
  handlers. It reads `ref.current?.contains(document.activeElement)` first, then
  `setOpen(false)`, then focuses the toggle if the check passed.
- One `close()` used by the outside-mousedown and focusout paths: `setOpen(false)` only.
- `aria-haspopup` deleted from `UserMenu.tsx:34`; `aria-controls` added.
- `onClick={closeAndRestoreFocus}` on each of the three `Link`s; the logout handler
  becomes close-then-`onLogout`.
- `onBlur` on the container div (`UserMenu.tsx:31`) implementing the focusout rule.
- A header comment recording (a) why this is a disclosure and not a menu, (b) why Escape
  stays on `document`, (c) why the panel is not portalled, with a pointer to
  `DialogShell.tsx` for the three-way split above. The file already carries a long
  load-bearing comment about `bg-popover` and `z-50` (`UserMenu.tsx:40-51`) - that comment
  is not to be reflowed or edited.

Everything else in the file - the class strings, `GlassPanel`, the `data-testid`, the
`bg-popover`/`z-50` comment, the email header, the divider, the item order and copy - is
untouched. This is deliberately a pixel-neutral change; the review gate is that the
rendered class attribute of every element is byte-identical.

## What is testable under jsdom, and what is not

Environment: vitest 2 + jsdom 29 + `@testing-library/user-event` 14
(`web/package.json:23-34`). Read rather than assumed:

**Confirmed capable:**

- `userEvent.keyboard('{ArrowDown}')` etc. dispatch a real `keydown` with
  `key: 'ArrowDown'` to `document.activeElement`
  (`user-event/dist/cjs/keyboard/keyMap.js:126-150`,
  `system/keyboard.js:58,64-67`). The only built-in default behaviours for the arrow keys
  are radio-group walking and text-caret movement
  (`event/behavior/keydown.js:24-54`), and `Home`/`End` only act on inputs, textareas and
  contenteditables (`:69-91`). So on links and buttons these keys are inert, which means a
  test can prove **absence** of arrow handling as cleanly as it could have proven presence.
  Had we chosen the menu contract, arrow navigation would have been fully testable here;
  it is not being rejected for lack of a harness.
- `document.activeElement` assertions are meaningful for every element involved. jsdom
  treats `<a href>`, `<button>` and anything with a numeric `tabindex` as focusable areas
  (`jsdom/lib/jsdom/living/helpers/focusing.js:29-53`).
- `userEvent.tab()` computes its destination from a document-wide query that **excludes**
  negative `tabindex` (`user-event/dist/cjs/utils/focus/getTabDestination.js:8-11`), so
  natural tab order through the panel is measurable, and would also have measured a roving
  tabindex correctly.
- `focusout` bubbles and carries `relatedTarget`
  (`jsdom/.../HTMLOrSVGElement-impl.js:57-58,71-72`), so React's `onBlur` and the
  containment check are directly testable, including the null-`relatedTarget` branch via
  an explicit `.blur()` (`:82-83`).
- Attribute presence/absence, `aria-expanded` transitions, and `aria-controls` matching
  the panel's `id`.

**Not testable under jsdom, named rather than glossed:**

- **That a screen reader announces "link" rather than "menu item".** No test in this repo
  can measure an accessibility-tree announcement. The ARIA decision rests on the
  specification and on the element semantics, not on evidence a test can produce. This is
  the same honesty the dialog retro applied to `inert`
  (`docs/retros/2026-08-09-dialog-hardening.md:231-237`): attributes are asserted,
  assistive-technology behaviour is argued.
- **Safari's "a click does not focus a button" behaviour**, which is the strongest reason
  Escape stays on `document`. user-event always focuses the closest focusable on click
  (`user-event/dist/cjs/event/focus.js:14-25`), so jsdom cannot reproduce that state
  naturally. The test **simulates** it with an explicit `.blur()` and must say so in a
  comment, or it will read as proof of something it did not reproduce.
- **Paint order / occlusion.** jsdom does no layout or hit-testing. The dropdown's
  stacking is already covered by class assertions (`UserMenu.test.tsx:47-51`) plus the
  measurements recorded in `HoloShell.tsx:29-48`. Unchanged by this slice, and the
  pixel-neutrality claim is an argument, not a screenshot.
- **Real-browser Tab traversal**, including whether the open dropdown visually obscures
  the element Tab lands on after leaving the panel.

**Substitute evidence, stated accurately.** The 2026-08-12 slice ran a real-browser lane
in the Phase 4 integration slot and it is now the standing shape for a frontend-only slice
(`docs/retros/2026-08-12-profile-pages.md:226-228`). **But that lane could not deliver
synthetic key events** - it said so and refused to assert Enter-to-submit
(`docs/retros/2026-08-12-profile-pages.md:323-327`). So for this slice the browser lane is
**not** a substitute for the keyboard evidence: it can confirm paint order, the dropdown's
position, mouse open/close, and close-on-select via clicks, and it should be asked for
exactly those. If the lane can deliver key events this time, Escape-restores-focus and
Tab-out-closes are the two behaviours worth measuring there; if it cannot, it must say so
rather than infer. jsdom remains the primary evidence for every keyboard claim in this
spec, and jsdom is genuinely capable of all of them.

## Testing

`web/src/shell/UserMenu.test.tsx`, appended to. Existing harness (`renderMenu`,
`MemoryRouter`) is sufficient; two new tests need a sibling focusable rendered after the
component, which is a local render helper, not a change to `renderMenu`.

**The one sanctioned existing-test edit, named in advance:** `UserMenu.test.tsx:34`
asserts `aria-haspopup === 'menu'`. That assertion is the defect this slice removes, so
it becomes an assertion that the attribute is **absent** plus an assertion that
`aria-controls` matches the panel's `id`, and the test's name changes from "exposes menu
semantics" to disclosure wording. Nothing else in the file moves: lines 16-29, 40-51 and
53-58 must be byte-identical against the merge base, and `git diff --numstat` on the file
is the gate. If any other assertion needs adjusting, **that is the finding, not the fix.**

New tests, each with its required control:

1. **`aria-haspopup` is absent in both states**, and `aria-controls` is present only when
   open and equals the panel's `id`. Positive control: `aria-expanded` still flips
   `false` -> `true`, so the test cannot pass against a component that stopped rendering
   the toggle.
2. **Escape returns focus to the toggle** when focus is inside the panel. Setup must
   *actually* put focus inside (Tab to the first link and assert
   `document.activeElement`), or the test passes against a component that focuses the
   toggle unconditionally. Paired negative -> test 3.
3. **Escape does not steal focus** when focus is outside the container. Reach that state
   with an explicit `(document.activeElement as HTMLElement).blur()`, which fires
   `focusout` with a null `relatedTarget` and therefore does not close the menu (see
   test 6). Assert the menu closed and `document.activeElement` is **not** the toggle.
   This pair is the discriminating evidence for the `focusWasInside` rule; either test
   alone passes against a wrong implementation.
4. **Tab out of the last item closes the menu.** Render a sibling `<button>After</button>`
   after `UserMenu`, Tab to `Log out`, Tab once more, assert the panel is gone **and**
   `document.activeElement` is the sibling - i.e. the close did not also steal the
   destination. Paired positive: Shift+Tab from the first link lands on the toggle and the
   menu is **still open**.
5. **Selecting a navigation item closes the menu.** Click `Profile`; assert the panel is
   gone and the toggle has focus. **Prove RED against `main`**: this test fails on the
   current component, and that RED run is the record that defect 1 was real. One test per
   link is redundant; one link plus the assertion that all three carry the handler
   (`Password` as a second case) is enough.
6. **A blur with a null `relatedTarget` does not close the menu.** Call `.blur()` on the
   focused item and assert the panel is still present. This is the branch a naive
   `onBlur={() => setOpen(false)}` gets wrong, and without this test that naive version
   passes every other test in the file.
7. **`Log out` still calls `onLogout` exactly once and now also closes.** The existing
   test at `UserMenu.test.tsx:53-58` covers the call; the new one covers the close, so
   the existing test stays byte-identical.
8. **Absence sweep with a positive control:** the panel's subtree contains no
   `role="menu"`, no `role="menuitem"` and no element carrying `tabindex="-1"`. Positive
   control: the panel subtree **does** contain four interactive elements
   (`getAllByRole('link')` length 3 and the logout button), so the sweep cannot pass
   against an empty or unmounted panel. This is the durable guard against someone
   "restoring" the roles later from the backlog item's Proposal without reading this spec.
9. **Arrow keys do nothing.** With the menu open and focus on the first link,
   `{ArrowDown}` leaves `document.activeElement` unchanged and the menu open. Cheap, and
   it pins decision 3 so a future half-implemented roving tabindex fails loudly rather
   than silently diverging from the announced contract.

Plan-supplied test bodies are guesses until run RED; test 5 in particular must be
demonstrated RED against the unmodified component rather than asserted to be a regression
test.

## Acceptance criteria

1. The toggle carries **no** `aria-haspopup` in either state, and `web/src` contains no
   occurrence of the attribute outside a comment.
2. The toggle carries `aria-expanded` reflecting open state and `aria-controls` equal to
   the panel's `id` while the panel is rendered; the panel carries that `id`.
3. The panel carries **no** `role`, and no descendant carries `role="menu"`,
   `role="menuitem"` or a negative `tabindex`. The three items remain `<a href>` and the
   fourth remains `<button>`.
4. Escape closes the menu and returns focus to the toggle when focus was inside the
   container, and leaves focus untouched when it was not.
5. Selecting any of the four items closes the menu and returns focus to the toggle;
   `Log out` still invokes `onLogout` exactly once.
6. Moving focus out of the container (Tab from the last item, Shift+Tab from the toggle)
   closes the menu without disturbing the focus destination. A blur with a null
   `relatedTarget` does **not** close it.
7. An outside mousedown closes the menu and does **not** move focus.
8. No focus is moved when the menu opens.
9. Arrow keys, Home, End and printable characters have no effect on the menu.
10. Escape remains a `document`-level listener registered only while open; the panel is
    **not** portalled, does not register with `dialogStack`, does not lock body scroll and
    does not mark the background `inert` or `aria-hidden`.
11. Rendered class attributes are byte-identical to `main` for every element in the file;
    the `bg-popover`/`z-50` comment block (`UserMenu.tsx:40-51`) is unedited.
12. `UserMenu.test.tsx` shows **zero deletions** against the merge base except the single
    sanctioned edit at line 34 and its test name; `git diff --numstat` is the gate.
13. `npm test` and `tsc -b && vite build` are green; the change set is confined to
    `web/src/shell/UserMenu.tsx`, `web/src/shell/UserMenu.test.tsx` and `docs/`;
    `web/dist` is reverted before the change set is assembled.
14. `feature-2026-06-05-usermenu-panel-menu-roles.md` is closed via `/backlog close`, and
    its Resolution states plainly that the item's Proposal was **inverted** and why, so a
    future reader does not treat the closed item as evidence that `role="menu"` was
    implemented.

## Cross-links and adjacent items

- **`docs/backlog/idea-2026-08-12-document-z-index-layering-scale.md`** - **adjacent, not
  touched.** That item lists the dropdown's `z-50` (`:27`) as one of two `z-50`s that mean
  different things. This slice changes no z-index and does not portal, precisely so that
  item's measured picture stays true. Worth cross-referencing from that item's Related
  list when it is picked up, because "the dropdown is not portalled, deliberately" is now
  a decision with a reason rather than an accident.
- **`docs/backlog/idea-2026-08-13-field-error-wiring-audit.md`** - **no overlap.**
  Verified by reading the item and the component: that audit is about error surfaces
  rendering into bare `text-err` divs, and its enumerated 11 sites
  (`idea-2026-08-13-field-error-wiring-audit.md:41-53`) do not include `UserMenu`.
  `UserMenu` renders no error, no `Field`, and issues no mutation. The only shared trait
  is that both are accessibility gaps found by reading rather than by a failing test.
- **`docs/backlog/idea-2026-08-09-native-dialog-element-reconsideration.md`** - **not
  affected.** The menu is not a dialog and would not adopt `<dialog>` even if jsdom
  implemented it. Checked because the backlog item told us to
  (`feature-2026-06-05-usermenu-panel-menu-roles.md:48-49`); the answer is that the
  overlap is zero.
- **`docs/backlog/idea-2026-08-09-dialog-shell-sweep-test.md`** - **same enforcement
  shape.** See Follow-ups.
- **`docs/retros/2026-08-09-dialog-hardening.md:268-269,286-290`** - records that
  `UserMenu` keeps its document-level Escape and mousedown listeners as an explicit
  non-goal, and that giving `UserMenu` the DialogShell treatment was proposed and
  deliberately **not** filed because it would split this item's work across two. This
  spec is the promised fold-in, and it declines the "same treatment" framing on the merits
  above.

## Security and system design

- **Threat model: unchanged and empty.** No request, no token, no user input, no
  interpolation, no new state. The only data on the surface is the user's own email, which
  is already rendered twice (`UserMenu.tsx:38,58`).
- **Load and availability: unchanged.** No query, no timer, no polling, no listener that
  outlives the open state. Both document listeners are registered on open and removed on
  close (`UserMenu.tsx:22-27`), and the new focusout handler is a React prop with no
  manual lifecycle at all.
- **Failure modes.** The one behaviour that could regress is Escape reaching the menu
  while a dialog is open. Structurally impossible per `DialogShell.tsx:361-370`, and
  unchanged by this slice because the listener's registration site and lifetime are
  untouched. The second is focus theft, which the `focusWasInside` split and the
  outside-mousedown carve-out exist to prevent, and which tests 3 and 7 pin.
- **Backend invariants.** No Go, no SQL, no proto, no migration. The epoch fence, the
  single job-spec pipeline, one bounded sender per stream, identity-checked teardown, no
  interior pointers across locks and the single JSON entry point are all untouched. The
  frontend analogue that does apply is **end the generation before releasing the
  resource**: the close path reads `activeElement` and decides about focus before the
  state update that unmounts the panel, so the dying panel's own teardown cannot describe
  a world in which it is still open.

## Follow-ups to propose (not to file automatically)

Per the standing rule these are proposals; Phase 6 offers them for human accept.

| Proposal | Why |
|---|---|
| `idea-2026-08-13-aria-haspopup-sweep-test` (low) - a sweep asserting that every `aria-haspopup` in `web/src` names a popup role that is actually implemented on the controlled element, and symmetrically that `role="menu"` never appears without a roving tabindex. | This item existed for over two months because an attribute claimed a contract no code implemented and nothing could notice. Same enforcement shape and the same Vitest-reads-the-tree-versus-ESLint question as the already-filed [[idea-2026-08-09-dialog-shell-sweep-test]] and [[idea-2026-08-13-field-error-wiring-audit]]; the three should be decided together and probably built once. |
| `idea-2026-08-13-usermenu-becomes-a-true-menu-if-it-goes-actions-only` (low) - carry the **trigger condition** rather than the conclusion: if the account dropdown ever stops containing navigation links and becomes a list of actions, `role="menu"` plus roving tabindex becomes the correct pattern and this decision should be revisited. | Same treatment [[idea-2026-08-09-native-dialog-element-reconsideration]] got: record the condition under which today's evidence expires, so the next author re-evaluates instead of re-litigating or blindly inheriting. |
| Note on `idea-2026-08-12-document-z-index-layering-scale` - add "the dropdown is deliberately not portalled, because the disclosure pattern needs it in DOM order after its toggle" to that item's context. | Otherwise the next person to read that item sees two confusing `z-50`s and may conclude portalling the dropdown is the tidy fix. It is not: it would break Tab order. |

Deliberately **not** proposed: adding a visually-hidden "Account menu" to the toggle's
accessible name (it is the email today, which is a good name, and changing it touches five
shipped queries for no measured gain); adding the hi-fi's per-item icons and hints
(`hifi3-holo-pages.jsx:217-219,278-280`), which is a visual scope this slice deliberately
does not open.

## Decisions

1. **Ship a disclosure, not a menu; remove `aria-haspopup` rather than implement
   `role="menu"`.** Three of four items are site navigation links, which is the case the
   `menu` role's own specification excludes, and `role="menuitem"` would destroy the link
   role on all three. This inverts the backlog item.
2. **Do not move focus on open.** The disclosure pattern does not, the panel follows the
   toggle in DOM order so Tab reaches it anyway, and moving focus on a mouse open is
   hostile.
3. **No arrow keys, no Home/End, no typeahead.** Arrow navigation is the observable
   signature of the menu contract; shipping it without `role="menu"` would give a
   screen-reader user behaviour nothing announced - the same "advertise what you do not
   implement" defect this item exists to fix, run in reverse. Pinned by test 9.
4. **Close on select, for all four items.** The shipped component has no `onClick` on the
   three `Link`s (`UserMenu.tsx:60,63,69`) so the dropdown hangs open over the page it
   just navigated to; the prototype closes (`hifi3-holo-pages.jsx:221`). Defect found
   during verification, not in the item.
5. **Escape restores focus to the toggle, but only if focus was inside the container.**
   `DialogShell.tsx:234-239,276-282` reasoning, reused directly; the guard is what
   separates a restore from focus theft.
6. **The outside-mousedown path closes without touching focus.** `mousedown` precedes the
   focus change, so at handler time `activeElement` is still inside the panel and a shared
   close would yank focus away from whatever the user just clicked. Same rule as decision
   5, opposite answer, because the event ordering differs.
7. **Close on focus leaving the container, keyed on a non-null `relatedTarget` outside
   it.** Verified against `jsdom/.../HTMLOrSVGElement-impl.js:57-58,82-83`: a null
   `relatedTarget` means a blur to nothing, which in a browser is what pressing on the
   panel's own non-focusable header does, and closing on it would make the panel vanish
   under the user's cursor.
8. **Escape stays a `document` listener; it is not moved onto the panel.** The dialog
   iteration's one high-severity finding (`DialogShell.tsx:59-75`), and it binds harder
   here because Safari does not focus a button on click, so the menu can legitimately be
   open with focus on `<body>`.
9. **Do not portal the panel and do not register with `dialogStack`.** Tab order under the
   disclosure pattern requires DOM adjacency, and the dropdown's paint order is solved by
   `relative z-10` on the header measured over 275 hit-test points
   (`HoloShell.tsx:29-48`), which portalling would invalidate.
10. **`aria-controls` is set only while the panel is mounted.** The panel is conditionally
    rendered (`UserMenu.tsx:52`); a permanent IDREF to a non-existent node is an authoring
    error, and always-rendering the panel would break the two shipped tests that assert
    `Log out` is absent when closed (`UserMenu.test.tsx:21,28`).
11. **Exactly one sanctioned test edit, named before implementation:**
    `UserMenu.test.tsx:34`. Every other line in that file must be byte-identical, gated by
    `git diff --numstat`. Same rule the Table and dialog iterations shipped under.
12. **The toggle's accessible name does not change.** It is the email
    (`UserMenu.tsx:38`), which is a good name; changing it touches five shipped queries
    for no measured benefit.
13. **Pixel-neutral: no class string changes, and the `bg-popover`/`z-50` comment is not
    reflowed.** The repo has no visual regression harness, so neutrality is argued by
    byte-identical class attributes and a reviewer diffing them.
14. **The item is closed with a Resolution that records the inversion.** A closed item
    titled "lacks menu/menuitem roles" is otherwise circumstantial evidence that those
    roles were added; they were deliberately not, and the reason must survive with the
    file.

## Risks

- **The single largest risk is that this spec is read as permission to skip the work.**
  It is not. Removing one attribute is a quarter of the change; the focus restore, the
  close-on-select, the close-on-focus-leave and the null-`relatedTarget` carve-out are the
  rest, and three of those four fix live defects.
- **A reviewer or a future session re-adds `role="menu"` from the backlog item's
  Proposal.** The item is more prescriptive than this spec, is easier to find, and reads
  as authoritative. Mitigations: acceptance criterion 3, test 8, the file header comment,
  and criterion 14's Resolution wording.
- **A naive `onBlur={() => setOpen(false)}`** passes every test in this plan except test 6
  and breaks the panel in a real browser the first time a user clicks its own email
  header. Test 6 is a requirement, not a nicety.
- **Test 3 is easy to write vacuously.** If the setup does not genuinely leave focus
  outside the container, it passes against an implementation that always focuses the
  toggle, which is the exact thing it exists to refute.
- **Escape's document listener and `DialogShell`'s coexist by structure, not by
  contract.** `DialogShell.tsx:361-370` argues they cannot overlap because the toggle goes
  `inert` while a dialog is open, and `inert` is proven as an attribute only
  (`docs/retros/2026-08-09-dialog-hardening.md:231-237`). This slice does not weaken that
  argument, but it does not strengthen it either, and anyone who later portals the menu or
  changes its listener lifetime must re-derive it.
- **The browser lane may again be unable to deliver key events**
  (`docs/retros/2026-08-12-profile-pages.md:323-327`), in which case the keyboard model
  has jsdom evidence only. That is adequate - jsdom genuinely measures every keyboard
  claim here - but the verification report must say so rather than implying a
  double-confirmation that did not happen.
