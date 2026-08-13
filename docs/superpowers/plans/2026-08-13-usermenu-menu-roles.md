# UserMenu: fulfil the dropdown's advertised ARIA contract - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the header account dropdown an honest ARIA **disclosure** - delete `aria-haspopup="menu"`, add `aria-controls` pointing at a real panel `id`, and fix the three keyboard/focus defects that sit underneath it (the menu stays open after you select a link, Escape drops focus on the floor, and nothing closes when focus Tabs out of the container).

**Architecture:** One component file changes: `web/src/shell/UserMenu.tsx`. No new file, no new dependency, no portal, no `dialogStack` registration, no change to `HoloShell`, the router, or any other component. Two close helpers are introduced - `close()` (no focus movement) for the two paths where the browser is already moving focus itself, and `closeAndRestoreFocus()` (guarded on focus having been inside the container) for Escape and for all four items. The containment check is read **before** the state update that unmounts the panel, which is CLAUDE.md's "end the generation before releasing the resource" in its smallest form.

**Tech Stack:** React 18 (`useId`, `useRef`, `useState`, `useEffect`), TypeScript 5.7, react-router-dom v7 `Link`, Tailwind v4 (Holo tokens, unchanged), Vitest 2.1 + Testing Library 16 + user-event 14, jsdom 29. No MSW on this surface - the component issues no request.

**Spec:** `docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md` (approved; do not reopen its decisions)

**Backlog item closed by this slice:** `docs/backlog/feature-2026-06-05-usermenu-panel-menu-roles.md`. Close it with `/backlog close feature-2026-06-05-usermenu-panel-menu-roles`, which `git mv`s the file into `docs/backlog/closed/`; never hand-edit `status:`. **Its Resolution must record that the item's Proposal was deliberately INVERTED**, or a closed item titled "lacks menu/menuitem roles" becomes circumstantial evidence that those roles were added.

---

## THE SIZE OF THIS SLICE, STATED HONESTLY

**This is a five-task slice, not an eleven-task one, and the tests carry most of the weight.**

The component delta is roughly one deleted attribute, four added attributes/props, two four-line helpers, one five-line event handler and a header comment - on the order of 60 added lines, of which more than half are comments. The test delta is roughly 180 lines across twelve new tests plus one sanctioned two-line edit.

That ratio is correct and is not a sign the plan is thin. The shipped change **removes** an ARIA attribute rather than adding machinery, so almost every acceptance criterion is about something being **absent** (`aria-haspopup` gone, no `role`, no negative `tabindex`, arrow keys inert) or about **where focus lands** (the toggle, or deliberately nowhere). Both are trivially satisfiable by a component that does nothing, or by a test that looks at the wrong element. **Every absence assertion in this plan therefore carries either a positive control in the same test or a named mutation the engineer must run and record.** The per-task "How this test discriminates" blocks are the substance of the slice; do not skip them, and do not collapse them into "the test passes".

Do not pad this plan back out. If a task feels too small to be worth a commit, it is still worth its own RED.

---

## Slice independence declaration

- **Backend slice: NONE. This is 100% `web/`. Zero Go files change, zero `.sql` files, zero `.proto` files.** Therefore: no `make generate`, no `*.sql.go`, no `models.go`, no migration, no integration test. None of the six backend Invariants in CLAUDE.md is in play. The Go gate in Task 6 exists only to prove no regression, not because anything server-side is touched.
- **Frontend slice: ONE `relay-frontend-engineer`, SEQUENTIAL.** Every task edits the same 86-line file. Tasks 3, 4 and 5 all depend on helpers introduced in Task 2. Do not split this across two engineers and do not run any task in parallel with another.
- **Could it be one commit?** Technically yes - the whole change is one file and about 60 lines. **Do not do that.** Five commits are required here because three of the twelve tests must be proven RED against a component that does not yet have the behaviour, and a single commit destroys that evidence. Tasks 1, 2, 3 and 4 each have at least one genuine RED that only exists at that point in the sequence.
- **Parallelism available to the conductor for Phase 3: none within this plan.** Unrelated work elsewhere in the repo can run alongside it.

---

## Verified current state (re-verified against the tree at HEAD; do not trust the spec alone)

Read in full before starting: `web/src/shell/UserMenu.tsx` (86 lines), `web/src/shell/UserMenu.test.tsx` (58 lines).

| Claim | Verdict | Evidence |
|---|---|---|
| Toggle carries `aria-haspopup="menu"` and `aria-expanded={open}`, and **no** `aria-controls` | Confirmed | `UserMenu.tsx:34-35` |
| The panel is a conditionally rendered `GlassPanel` with `data-testid="user-menu-panel"`, **no `id`**, **no `role`** | Confirmed | `UserMenu.tsx:52-55` |
| `GlassPanel` spreads unknown props onto its tag, so `id` will land without touching the primitive | Confirmed | `web/src/components/holo/GlassPanel.tsx:19-25` (`{...rest}` after `className`) |
| Items are three `Link`s plus one `<button>`, with a non-interactive email header and a divider between | Confirmed | `UserMenu.tsx:57-59`, `:60-74`, `:75`, `:76-81` |
| **No item has an `onClick` that closes.** The three `Link`s have none at all; the button calls `onLogout` only | Confirmed - **defect 1** | `UserMenu.tsx:60,63,69`, `:77` |
| Close routes are a document `mousedown`-outside and a document Escape, registered only while open | Confirmed | `UserMenu.tsx:14-28` |
| Neither close route moves focus, and nothing moves focus on open | Confirmed - **defect 2** | `UserMenu.tsx:16-21`, `:33` |
| The container `<div ref>` is `relative` and the toggle and panel are siblings inside it | Confirmed | `UserMenu.tsx:31`, `:32`, `:52` |
| The menu does not import anything from `../components/dialog/*` and is not in `dialogStack` | Confirmed | no such import in the file |
| `aria-haspopup` appears tree-wide in exactly two places | Confirmed | `UserMenu.tsx:34` and `UserMenu.test.tsx:34` |
| `role="menu"`, `role="menuitem"` and roving tabindex appear **nowhere** in `web/src` | Confirmed | there is no in-repo precedent to follow, and none is being created |
| `UserMenu` is rendered once, in the header, before `<main>` in document order | Confirmed | `web/src/shell/HoloShell.tsx:70-72` |
| The repo has **no ESLint and no Prettier config**; `npm run build` is `tsc -b && vite build` | Confirmed | `web/package.json:6-12`, no `eslint*` file anywhere under `web/` |

**Consequence of that last row:** nothing will reformat your JSX for you and nothing will flag a missing hook dependency. Write the line breaks exactly as this plan shows them, and keep the `eslint-disable-next-line react-hooks/exhaustive-deps` comment shown in Task 3 - it is house style carried from `DialogShell.tsx:339` for editor tooling, not for a configured linter.

---

## Pinned jsdom evidence - do NOT re-derive this

The spec measured all of the following by reading `web/node_modules`. It is reproduced here so the engineer does not spend the slice re-deriving it. Each claim names the file it came from; verify one at random if you like, but do not re-derive the set.

| Claim | Source |
|---|---|
| `userEvent.keyboard('{ArrowDown}')` dispatches a real `keydown` with `key: 'ArrowDown'` to `document.activeElement` | `user-event/dist/cjs/keyboard/keyMap.js:126-150`, `system/keyboard.js:58,64-67` |
| jsdom's only built-in defaults for the arrow keys are radio-group walking and text-caret movement; `Home`/`End` act only on inputs, textareas and contenteditables. **On links and buttons these keys are inert** | `user-event/dist/cjs/event/behavior/keydown.js:24-54,69-91` |
| `<a href>`, `<button>` and anything with a numeric `tabindex` are focusable areas, so `document.activeElement` assertions are meaningful for every element here | `jsdom/lib/jsdom/living/helpers/focusing.js:29-53` |
| `userEvent.tab()` computes its destination from a document-wide query that excludes negative `tabindex`, so natural tab order through the panel is measurable | `user-event/dist/cjs/utils/focus/getTabDestination.js:8-11` |
| On a focus **move**, jsdom fires bubbling `focusout` on the old element with `relatedTarget` set to the new one | `jsdom/lib/jsdom/living/nodes/HTMLOrSVGElement-impl.js:57-58` |
| On a bare `.blur()`, jsdom fires bubbling `focusout` with **`relatedTarget === null`** | `jsdom/lib/jsdom/living/nodes/HTMLOrSVGElement-impl.js:82-83` |
| `.blur()` early-returns unless the element is currently the focused one, so a test must call it on `document.activeElement` | `jsdom/lib/jsdom/living/nodes/HTMLOrSVGElement-impl.js:77-79` |
| **user-event moves focus as the mousedown default action, AFTER the mousedown listeners run**, and when the click target has no focusable ancestor it instead **blurs** the active element | `user-event/dist/cjs/event/focus.js:14-25` |

That last row is load-bearing and is the single most important thing in this section. It means **the end state of `document.activeElement` after an outside click cannot distinguish a focus-stealing close from a correct one** - user-event overwrites the stolen focus a moment later either way. Task 3 therefore measures the `focus()` **call** with `vi.spyOn(toggle, 'focus')`, which is the same instrument `DialogShell.tsx:256-259` used when it hit the equivalent problem.

### The real-browser lane is NOT a fallback for any keyboard claim

The 2026-08-12 slice ran a real-browser lane in the Phase 4 integration slot and **it could not deliver synthetic key events** - it said so and refused to assert Enter-to-submit (`docs/retros/2026-08-12-profile-pages.md:323-327`). Assume the same this time. The browser lane can usefully confirm paint order, the dropdown's position, mouse open/close and close-on-select via clicks, and it should be asked for exactly those. **jsdom is the only place the keyboard and focus claims in this plan can be proven**, and it is genuinely capable of all of them. If the lane turns out to deliver key events, Escape-restores-focus and Tab-out-closes are the two worth measuring there; if it cannot, the verification report must **say so** rather than implying a double confirmation that did not happen.

### Named as untestable here, rather than glossed

- **That a screen reader announces "link" and not "menu item".** No test in this repo can measure an accessibility-tree announcement. The ARIA decision rests on the specification and on element semantics. Same honesty `docs/retros/2026-08-09-dialog-hardening.md:231-237` applied to `inert`.
- **Safari not focusing a `<button>` on click.** user-event always focuses the closest focusable on click, so jsdom cannot reach that state naturally. Task 3's test **simulates** it with an explicit `.blur()` and its comment must say so, or it reads as proof of something it did not reproduce.
- **Paint order and occlusion.** jsdom does no layout. Covered by the existing class assertions (`UserMenu.test.tsx:47-51`) plus the measurements in `HoloShell.tsx:29-48`. Unchanged here.

---

## Precedent for every artifact

"Mirror X at `file:line`" is a literal instruction: copy the shape and the reasoning, change the nouns. **No code is imported from `DialogShell`; it is reused as reasoning only.**

| Artifact in this slice | Precedent | What is reused |
|---|---|---|
| Escape stays a `document` listener | `web/src/components/dialog/DialogShell.tsx:59-75` | The whole argument: a React `onKeyDown` only fires when the target is a descendant of the panel, and focus leaves through more routes than the component can close. **Do not "tidy" the existing listener onto the panel.** |
| Escape coexisting with `DialogShell`'s own Escape | `DialogShell.tsx:355-370` | That comment names *this* listener by name and explains `stopImmediatePropagation`. This slice does not change this listener's registration site or its open-only lifetime, so that argument stays true. Do not weaken it. |
| `focusWasInside` before teardown | `DialogShell.tsx:234-239` and `:276-282` | Capture the containment check first, restore only if it passed. The guard is what separates a restore from focus theft. |
| End the generation before releasing the resource | `web/src/components/dialog/dialogStack.ts:22-38`, and CLAUDE.md's Invariants | Read `document.activeElement` **before** `setOpen(false)`. After the state update, the panel is unmounted and the check can no longer tell "focus was on an item" from "focus was never in here". |
| Measuring a focus **call** rather than the end state | `DialogShell.tsx:256-259` (instrumented `HTMLElement.prototype.focus`) | Used in Task 3, scoped down to `vi.spyOn(toggle, 'focus')`. |
| Everything staying the same | the shipped `UserMenu` itself | Class strings, `GlassPanel`, `data-testid`, the `bg-popover`/`z-50` comment, the email header, the divider, item order and copy. |

### Explicitly INAPPLICABLE - do not import the modal machinery

`DialogShell`'s Tab trap, its `inert`/`aria-hidden` background marking, the scrim, the scroll lock, `dialogStack` registration, `isTopmost`, the portal, and the landmark focus fallback are all **modality**, and a menu is not modal. In particular:

- **A disclosure must not trap Tab.** Tab out is a *dismiss* route here, not something to intercept.
- **Do not portal the panel.** The disclosure pattern needs it to follow the toggle in DOM order so Tab reaches it, and the dropdown's paint order is already solved by `relative z-10` on the header (`HoloShell.tsx:29-49`, measured over 275 hit-test points) - portalling would invalidate that measurement and reopen the stacking problem.
- **Do not copy the landmark fallback.** Its premise is a trigger that may have been removed from the DOM. This trigger is a sibling in the same component and is always connected while the panel exists, so the branch is unreachable.

---

## Scope guard - do NOT build

- **No `role="menu"`, no `role="menuitem"`, no roving tabindex, no `aria-activedescendant`.** This is the backlog item's Proposal and it is deliberately **inverted**. Task 5's sweep is the durable guard against someone re-adding it from the item without reading the spec.
- **No arrow keys, no Home/End, no PageUp/PageDown, no typeahead.** Arrow navigation is the observable signature of the menu contract; shipping it without `role="menu"` would be the same "advertise what you do not implement" defect this item exists to fix, run in reverse.
- **No focus movement on open.** The disclosure pattern does not move focus into disclosed content, and doing it on a mouse open is hostile.
- **No portal, no `dialogStack`, no scrim, no scroll lock, no `inert`, no `aria-hidden` on the background.**
- **No change to any class string**, and **no reflow or edit of the `bg-popover`/`z-50` comment block at `UserMenu.tsx:40-51`**. This slice is pixel-neutral and the review gate is that every rendered class attribute is byte-identical.
- **No change to the toggle's accessible name.** It is the email (`UserMenu.tsx:38`); changing it touches five shipped queries for no measured gain.
- **No visually-hidden "Account menu" label, no per-item icons or hints** from the hi-fi (`hifi3-holo-pages.jsx:217-219,278-280`). That is a visual scope this slice does not open.
- **No edits to `GlassPanel`, `DialogShell`, `dialogStack`, `HoloShell` or any other shared component.**
- **No change to the Escape listener's registration site or its open-only lifetime.**

---

## File Structure

**Modified files - exactly two, and nothing else in `web/src`**

| File | Change |
|---|---|
| `web/src/shell/UserMenu.tsx` | Delete `aria-haspopup` (`:34`); add `useId` panel id + `aria-controls`; add `toggleRef`; add `close()` and `closeAndRestoreFocus()`; wire Escape, the four items and a container `onBlur`; add a header comment. |
| `web/src/shell/UserMenu.test.tsx` | **One sanctioned edit** (the test name at `:31` and the assertion at `:34`), plus twelve appended tests and one appended render helper. |

**No new file. No deleted file. No dependency change.**

### The byte-identity gate on `UserMenu.test.tsx`

**Exactly two lines may be deleted from this file: `:31` (the test name) and `:34` (the `aria-haspopup` assertion).** That assertion is the defect this slice removes, so it is sanctioned in advance. Everything else is appended at the end of the file.

The gate is:

```bash
git diff --numstat -- web/src/shell/UserMenu.test.tsx
```

Expected: deletions **exactly 2**. **If any other assertion in that file needs adjusting, that is the finding, not the fix** - stop and report it rather than editing it.

These assertions must all survive untouched, and here is why each one still holds under the new behaviour. Check this table if one of them goes red; the entry tells you which new rule you got wrong.

| Lines | Assertion | Why it still passes |
|---|---|---|
| `:1-5` | Imports | Task 5's sweep is written with `querySelectorAll` and document-scoped `screen` queries **specifically so that `within` is not needed** and line 1 stays byte-identical. |
| `:7-14` | The `renderMenu` helper | Unchanged. The two tests that need a sibling focusable get a **new** local helper appended at the end of the file, not a change to this one. |
| `:16-22` | opens and closes on outside click | `userEvent.click(document.body)`: the document mousedown handler still closes it. The new `onBlur` then sees a **null** `relatedTarget` (body has no focusable ancestor, so user-event *blurs* the toggle - `event/focus.js:21-23`) and correctly does nothing. |
| `:24-29` | closes on Escape | Escape now routes through `closeAndRestoreFocus()`. Focus is on the toggle (the preceding click focused it), which is inside the container, so it closes and re-focuses the element that already had focus. The assertion is only about `Log out` being gone. |
| `:31` | test name | **SANCTIONED EDIT** - rename to disclosure wording. |
| `:34` | `aria-haspopup === 'menu'` | **SANCTIONED EDIT** - becomes an absence assertion. |
| `:35-37` | the `aria-expanded` false -> true pair | Untouched, and it becomes the positive control inside the renamed test: it proves the test is looking at a live toggle, so the absence assertion above it cannot pass against a component that stopped rendering the toggle at all. |
| `:40-51` | the `bg-popover`/`z-50` comment and class assertion | No class string changes anywhere in this slice. |
| `:53-58` | calls `onLogout` when Log out is clicked | The logout handler becomes close-then-`onLogout`; `onLogout` is still called exactly once. `toHaveBeenCalledOnce()` holds. |

---

## Conventions for every task

- All `npm`/`npx` commands run from the `web/` directory of the worktree: `D:/dev/relay/.claude/worktrees/pr-merge-session-f5796e/web`.
- Single file: `npx vitest run src/shell/UserMenu.test.tsx`. Full suite: `npm test`.
- TDD per step: write the failing test, run it and watch it fail with the stated message, implement, run it and watch it pass, commit.
- **House rule: never an em dash or en dash**, in code, comments, copy or this document. Plain ASCII hyphens only.
- Never reformat code you were not asked to change. Never edit a shipped assertion to make new code pass.
- `make` is **not on PATH in this shell**. Use `go build ./...` and `go test ./...` directly from the repo root.
- **Plan-supplied test bodies are guesses until run.** Where a task says "expected RED", a green run before the implementation exists means the test is wrong - fix the test, do not proceed. Where a task says "guard, proven by mutation", run the named mutation and **record both outputs in the task report**.

---

## Task 1: The ARIA contract - remove `aria-haspopup`, add `aria-controls`

The one-line deletion the whole backlog item turns on, plus the attribute that replaces it. Nothing about focus or closing changes in this task.

**Files:**
- Modify: `web/src/shell/UserMenu.tsx:1` (import), `:10-12` (hooks), `:32-36` (toggle), `:53-55` (panel)
- Modify: `web/src/shell/UserMenu.test.tsx:31`, `:34` (the sanctioned edit), then append one test

- [ ] **Step 1: Make the sanctioned edit and write the failing test**

In `web/src/shell/UserMenu.test.tsx`, change **only** line 31 and line 34. Line 31 becomes:

```tsx
test('exposes disclosure semantics and reflects open state via aria attributes', async () => {
```

Line 34 becomes:

```tsx
  expect(toggle).not.toHaveAttribute('aria-haspopup')
```

Leave `:32-33` and `:35-38` exactly as they are. Then **append** to the end of the file:

```tsx
// The disclosure half of the contract that replaced aria-haspopup="menu". See
// docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md for why this surface is
// a disclosure and not a menu.
//
// aria-controls is set ONLY while the panel is rendered, because the panel is
// conditionally mounted and an IDREF pointing at a node that does not exist is an
// authoring error. The aria-expanded assertions interleaved below are the positive
// control: an absence assertion alone would also pass against a component that
// stopped rendering the toggle, or against a query that found the wrong element.
test('aria-controls names the panel while open and is absent while closed', async () => {
  renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  expect(toggle).not.toHaveAttribute('aria-controls')
  expect(toggle).toHaveAttribute('aria-expanded', 'false')

  await userEvent.click(toggle)

  const panelId = toggle.getAttribute('aria-controls')
  expect(panelId).toBeTruthy()
  expect(screen.getByTestId('user-menu-panel')).toHaveAttribute('id', panelId as string)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')

  await userEvent.click(toggle)

  expect(toggle).not.toHaveAttribute('aria-controls')
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
})
```

**How these tests discriminate.** Both absence assertions are paired with a positive control on the *same element in the same test*: `aria-expanded` must still be present and must still flip. A component that rendered no toggle, or a query that matched the wrong node, fails the control. The `aria-controls` test additionally asserts the IDREF **resolves** (`toBeTruthy` plus the matching `id` on the panel), so an implementation that emits `aria-controls=""` or points at nothing fails.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: **2 failing tests.**
- `exposes disclosure semantics...` fails at the edited line: `expected element not to have attribute "aria-haspopup"`.
- `aria-controls names the panel...` fails at `expect(panelId).toBeTruthy()`: `expected null to be truthy`.

The other four tests must still pass. If any of them fails at this point, stop - the sanctioned edit was not confined to lines 31 and 34.

- [ ] **Step 3: Implement**

In `web/src/shell/UserMenu.tsx`, change line 1 from:

```tsx
import { useEffect, useRef, useState } from 'react'
```

to:

```tsx
import { useEffect, useId, useRef, useState } from 'react'
```

Add the panel id below the existing state and ref at `:11-12`, so the block reads:

```tsx
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const panelId = useId()
```

Replace the toggle's attribute block at `:32-36`. Delete the `aria-haspopup` line and add `aria-controls`:

```tsx
      <button
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        // Only while the panel is actually rendered: it is conditionally mounted
        // below, and an IDREF pointing at a node that does not exist is an
        // authoring error. aria-expanded, by contrast, is present in BOTH states.
        aria-controls={open ? panelId : undefined}
        className={`flex items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}`}
      >
```

The `className` template string is copied byte for byte from the shipped line. Do not retype it - copy it.

Add the `id` to the panel at `:53-55`:

```tsx
        <GlassPanel
          id={panelId}
          data-testid="user-menu-panel"
          className="absolute right-0 z-50 mt-2 w-56 bg-popover p-1.5 text-[12px]"
        >
```

`GlassPanel` spreads unknown props onto its tag after `className` (`GlassPanel.tsx:19-25`), so `id` lands on the rendered `<div>` with no change to the primitive.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx
git commit -m "fix(web): UserMenu is a disclosure, not a menu - drop aria-haspopup, add aria-controls"
```

---

## Task 2: Close on select, for all four items

**This fixes a live defect** (spec defect 1): the three `Link`s have no `onClick`, `UserMenu` lives in the persistent shell and is not remounted by a route change, and the outside-mousedown handler does not fire because the press target is *inside* the container. So clicking `Profile` navigates and leaves the dropdown hanging open over the page it just navigated to. This became visible on 2026-08-12 when `/profile/*` stopped being a placeholder.

**Files:**
- Modify: `web/src/shell/UserMenu.tsx` (add `toggleRef` and `closeAndRestoreFocus`, wire four items)
- Modify: `web/src/shell/UserMenu.test.tsx` (append three tests)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/shell/UserMenu.test.tsx`:

```tsx
// Defect fixed here: on main the three Links had no onClick at all, UserMenu lives
// in the persistent shell and is not remounted by a route change, and the
// outside-mousedown handler does not fire because the press target is INSIDE the
// container - so the dropdown hung open over the page it had just navigated to.
// These three tests were proven RED against that component.
test('selecting a navigation item closes the menu and returns focus to the toggle', async () => {
  renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  await userEvent.click(toggle)
  await userEvent.click(screen.getByRole('link', { name: 'Profile' }))
  expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()
  // Focus restore is the only focus management this app has across a
  // menu-driven navigation. Without it, focus falls to <body> on every route
  // change made from this dropdown.
  expect(document.activeElement).toBe(toggle)
})

test('the other navigation items close the menu too, not just the first', async () => {
  renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  for (const name of ['Password', 'Sessions']) {
    await userEvent.click(toggle)
    // Positive control inside the loop: prove the menu was actually OPEN before
    // asserting it closed, so a component that failed to open would fail here
    // rather than passing the absence assertion below for the wrong reason.
    expect(screen.getByTestId('user-menu-panel')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('link', { name }))
    expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()
  }
})

test('Log out closes the menu, returns focus to the toggle, and still calls onLogout once', async () => {
  const onLogout = renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  await userEvent.click(toggle)
  await userEvent.click(screen.getByText('Log out'))
  expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()
  expect(document.activeElement).toBe(toggle)
  expect(onLogout).toHaveBeenCalledOnce()
})
```

**How these tests discriminate.** All three are genuinely RED against the component as it stands after Task 1 - the panel simply stays mounted. The `activeElement` assertions are not absence assertions at all; they name the exact element focus must land on, and the shipped component leaves it on the clicked link.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: **3 failing tests**, each at its first panel assertion: `expected element not to be in the document` (the panel is still there). Record this output in the task report - it is the record that defect 1 was real.

- [ ] **Step 3: Implement**

Add a ref for the toggle alongside the existing refs:

```tsx
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const toggleRef = useRef<HTMLButtonElement>(null)
  const panelId = useId()
```

Add the helper immediately after those declarations, above the `useEffect`:

```tsx
  // Close AND return focus to the toggle, but ONLY if focus was inside the
  // container. Used by Escape and by all four items.
  //
  // The containment check is read BEFORE setOpen, which is CLAUDE.md's "end the
  // generation before releasing the resource" in its smallest form: setOpen(false)
  // unmounts the panel and detaches whatever was focused, after which
  // document.activeElement is <body> and the check can no longer tell "focus was
  // on an item" from "focus was never in here at all".
  //
  // The guard is what separates a restore from focus theft: a mouse user in Safari
  // (which does not focus a <button> on click, so the menu can legitimately be open
  // with activeElement === <body>) must not have focus yanked onto a toggle it was
  // never on. Same reasoning as DialogShell.tsx:234-239,276-282, reused as REASONING
  // ONLY - none of its modal machinery applies to a dropdown.
  function closeAndRestoreFocus() {
    const focusWasInside = !!ref.current && ref.current.contains(document.activeElement)
    setOpen(false)
    if (focusWasInside) toggleRef.current?.focus()
  }
```

Attach the ref to the toggle - `ref` goes first, before `onClick`:

```tsx
      <button
        ref={toggleRef}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
```

Wire the three `Link`s. The class strings are unchanged; only the `onClick` prop is added, and the first `Link` moves to the multi-line form because it no longer fits on one line:

```tsx
          <Link
            to="/profile"
            onClick={closeAndRestoreFocus}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Profile
          </Link>
          <Link
            to="/profile/password"
            onClick={closeAndRestoreFocus}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Password
          </Link>
          <Link
            to="/profile/sessions"
            onClick={closeAndRestoreFocus}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Sessions
          </Link>
```

react-router's `Link` calls the supplied `onClick` first and only navigates if the event was not default-prevented, so the close runs before the navigation and the navigation still happens.

Wire the logout button - close first, then hand off:

```tsx
          <button
            onClick={() => {
              // Close first, then hand off. onLogout tears the session down and
              // unmounts this whole shell; doing it in this order means the
              // containment check above runs while the panel is unambiguously
              // still mounted.
              closeAndRestoreFocus()
              onLogout()
            }}
            className="block w-full rounded-md px-2.5 py-2 text-left text-err hover:bg-white/5"
          >
            Log out
          </button>
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: PASS, 9 tests. The shipped `calls onLogout when Log out is clicked` test at `:53-58` must still pass **untouched**; if it does not, the logout handler ordering is wrong.

- [ ] **Step 5: Commit**

```bash
git add web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx
git commit -m "fix(web): UserMenu closes on select and returns focus to the toggle"
```

---

## Task 3: Escape restores focus - conditionally - and the outside-mousedown carve-out

**This task is the crux of the slice and the reason it is not one commit.** Two close routes now exist, they must behave *differently*, and the difference is entirely down to event ordering. Read this before writing anything:

- **Escape** fires with focus wherever the user left it. If that is inside the container, focus must come back to the toggle (spec defect 2: today it drops to `<body>`, so a keyboard user's next Tab restarts from the top of the document). If it is outside, focus must be left alone.
- **Outside mousedown** fires **before** the browser moves focus to whatever was pressed. So at handler time `document.activeElement` is *still inside the panel*, and a shared `closeAndRestoreFocus()` would see `focusWasInside === true` and yank focus onto the toggle, away from the control the user just clicked. **The mousedown path must close without touching focus.**

Same rule, opposite answer, purely because the event ordering differs. That is why it had to be re-derived rather than copied from `DialogShell`, and it is the same generation-before-release family the last two PRs both hit.

**Files:**
- Modify: `web/src/shell/UserMenu.tsx` (add `close()`, route `onDown` through it, route Escape through `closeAndRestoreFocus`)
- Modify: `web/src/shell/UserMenu.test.tsx` (append three tests and one render helper)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/shell/UserMenu.test.tsx`:

```tsx
// A focusable sibling AFTER the component, for the tests that need somewhere for
// focus to go. Deliberately a separate helper: renderMenu above is shipped and
// stays byte-identical.
function renderMenuWithSibling(onLogout = vi.fn()) {
  render(
    <MemoryRouter>
      <UserMenu email="ada@studio.dev" onLogout={onLogout} />
      <button>After</button>
    </MemoryRouter>
  )
  return onLogout
}

test('Escape returns focus to the toggle when focus was inside the panel', async () => {
  renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  await userEvent.click(toggle)
  // Genuinely put focus INSIDE the panel, and assert that it landed, before
  // pressing Escape. Without this the test passes against a component that
  // focuses the toggle unconditionally - which is exactly the implementation its
  // partner below exists to refute.
  await userEvent.tab()
  expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Profile' }))
  const toggleFocus = vi.spyOn(toggle, 'focus')

  await userEvent.keyboard('{Escape}')

  expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()
  expect(document.activeElement).toBe(toggle)
  // Paired with the not.toHaveBeenCalled() in the mousedown test below: the two
  // use the SAME instrument, so one cannot pass by measuring something the other
  // does not.
  expect(toggleFocus).toHaveBeenCalled()
  toggleFocus.mockRestore()
})

test('Escape does not steal focus when focus was outside the container', async () => {
  renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  await userEvent.click(toggle)
  // SIMULATING Safari, which does not focus a <button> on click, so the menu can
  // legitimately be open with document.activeElement === <body>. user-event always
  // focuses the closest focusable on click (event/focus.js:14-25), so jsdom cannot
  // reach that state naturally: this blur() STANDS IN for it and is not a
  // reproduction of Safari's behaviour.
  //
  // blur() fires focusout with a NULL relatedTarget
  // (jsdom/living/nodes/HTMLOrSVGElement-impl.js:82-83), which the focusout rule
  // added in the next task deliberately ignores - so the menu is still open when
  // Escape arrives, both now and after that task lands.
  ;(document.activeElement as HTMLElement).blur()
  expect(document.activeElement).toBe(document.body)
  expect(screen.getByTestId('user-menu-panel')).toBeInTheDocument()

  await userEvent.keyboard('{Escape}')

  expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()
  expect(document.activeElement).toBe(document.body)
})

test('an outside mousedown closes the menu and never touches the toggle focus', async () => {
  renderMenuWithSibling()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  const after = screen.getByRole('button', { name: 'After' })
  await userEvent.click(toggle)
  await userEvent.tab()
  expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Profile' }))

  // Spying on the CALL rather than reading activeElement at the end, because the
  // end state cannot tell the two implementations apart: user-event moves focus to
  // the clicked control AFTER the mousedown listeners run (event/focus.js:14-25),
  // so a focus-stealing close is overwritten a moment later and both versions
  // finish with activeElement === after. The steal is only observable as the call.
  // Same instrument DialogShell used for the equivalent problem
  // (DialogShell.tsx:256-259), scoped to one element.
  //
  // The real-browser harm this pins: press on non-focusable page content while the
  // menu is open. Nothing else takes focus, so the stolen focus is not overwritten
  // and the toggle keeps it.
  const toggleFocus = vi.spyOn(toggle, 'focus')

  await userEvent.click(after)

  expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()
  expect(toggleFocus).not.toHaveBeenCalled()
  expect(document.activeElement).toBe(after)
  toggleFocus.mockRestore()
})
```

**How these tests discriminate.**
- `Escape returns focus to the toggle` is a **genuine RED**: Escape currently calls `setOpen(false)` directly and focus falls to `<body>`.
- `Escape does not steal focus` and `an outside mousedown ... never touches the toggle focus` both **pass against the current code** - they are guards on the implementation about to be written, not regression tests. Their discriminating evidence is a **mutation, run and recorded in Step 4**. Do not skip it: without the mutation run, neither test has been shown to measure anything.

- [ ] **Step 2: Run the tests to verify the first one fails**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: **1 failing test.** `Escape returns focus to the toggle when focus was inside the panel` fails at `expect(document.activeElement).toBe(toggle)` - the received element is `<body />`. The other two new tests **pass**; that is expected and is why Step 4 exists.

- [ ] **Step 3: Implement**

Add the second helper immediately above `closeAndRestoreFocus`:

```tsx
  // Close WITHOUT touching focus. Used by the two paths where the browser is
  // already moving focus itself, so a restore here would fight the user.
  function close() {
    setOpen(false)
  }
```

Then rewrite the effect's two handlers. The listener registration site, the `[open]` dependency and the open-only lifetime are all unchanged on purpose - `DialogShell.tsx:355-370` argues that its own Escape listener and this one cannot overlap, and that argument depends on this listener's lifetime:

```tsx
  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      // close(), NOT closeAndRestoreFocus(): mousedown fires BEFORE the browser
      // moves focus to whatever was pressed, so at this instant activeElement is
      // still inside the panel and a restore would steal focus away from the
      // control the user just clicked. Identical rule to the Escape path below,
      // opposite answer, purely because the event ordering differs. This is why the
      // DialogShell reasoning had to be re-derived here rather than copied.
      if (ref.current && !ref.current.contains(e.target as Node)) close()
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') closeAndRestoreFocus()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
    // Both helpers are captured from the render that ran this effect and are
    // deliberately not dependencies: they touch only refs and setOpen, all of which
    // are stable for the component's life, so a stale closure cannot observe stale
    // state. Listing them would re-subscribe both document listeners on every
    // render for no behaviour gain.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
```

- [ ] **Step 4: Run the tests, then run BOTH mutations and record the output**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: PASS, 12 tests.

Now prove the two guard tests discriminate. **Run each mutation, record the exact failure output in the task report, then revert it.**

**Mutation A - drop the focus-theft guard.** In `closeAndRestoreFocus`, replace the last line with an unconditional restore:

```tsx
    toggleRef.current?.focus()
```

Run: `npx vitest run src/shell/UserMenu.test.tsx`
Expected: FAIL in `Escape does not steal focus when focus was outside the container`, at `expect(document.activeElement).toBe(document.body)`, received the toggle button. **Revert.**

**Mutation B - share the close path.** In `onDown`, replace `close()` with `closeAndRestoreFocus()`.

Run: `npx vitest run src/shell/UserMenu.test.tsx`
Expected: FAIL in `an outside mousedown closes the menu and never touches the toggle focus`, at `expect(toggleFocus).not.toHaveBeenCalled()`, reported as the spy having been called once. Note that `expect(document.activeElement).toBe(after)` in the same test still **passes** under this mutation - that is the whole reason the spy is there, and it is worth saying so in the report. **Revert.**

Re-run after reverting and confirm 12 passing.

- [ ] **Step 5: Commit**

```bash
git add web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx
git commit -m "fix(web): UserMenu Escape restores focus to the toggle, mousedown does not"
```

---

## Task 4: Close when focus leaves the container, keyed on a non-null `relatedTarget`

The third gap, which only exists once the items are reachable by Tab: today a user can Tab from the last item straight into `<main>` and the dropdown stays open behind them, floating over content they are now interacting with.

The rule, stated precisely:

> Close if and only if `event.relatedTarget` is a node **and** the container does not contain it.

**`relatedTarget === null` must be treated as "do not close."** jsdom fires `focusout` with a null `relatedTarget` on a bare `blur()`; in a real browser that is what pressing the mouse on the panel's own non-focusable email header (`UserMenu.tsx:57-59`) produces, and closing on it would make the panel vanish under the user's cursor. The document mousedown handler already owns the "pressed somewhere else" case, so the null branch has a correct owner and does not need a second one.

**Files:**
- Modify: `web/src/shell/UserMenu.tsx` (add `onContainerBlur`, wire `onBlur` on the container)
- Modify: `web/src/shell/UserMenu.test.tsx` (append three tests)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/shell/UserMenu.test.tsx`:

```tsx
test('Tab out of the last item closes the menu without stealing the destination', async () => {
  renderMenuWithSibling()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  const after = screen.getByRole('button', { name: 'After' })
  await userEvent.click(toggle)
  await userEvent.tab() // Profile
  await userEvent.tab() // Password
  await userEvent.tab() // Sessions
  await userEvent.tab() // Log out
  // Positive control on the tab order itself: the panel follows the toggle in DOM
  // order and every item is a natural tab stop, which is the entire reason this
  // surface is not portalled and carries no roving tabindex.
  expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Log out' }))

  await userEvent.tab()

  expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()
  // The close must not also yank focus back: the user asked to go forward.
  expect(document.activeElement).toBe(after)
})

test('Shift+Tab from the first item lands on the toggle and leaves the menu OPEN', async () => {
  renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  await userEvent.click(toggle)
  await userEvent.tab()
  expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Profile' }))

  await userEvent.tab({ shift: true })

  // The toggle is INSIDE the container, so the containment check is what keeps the
  // menu open here. Without this control, a rule that closed on EVERY focusout
  // would pass the Tab-out test above.
  expect(document.activeElement).toBe(toggle)
  expect(screen.getByTestId('user-menu-panel')).toBeInTheDocument()
})

test('a blur with a null relatedTarget does NOT close the menu', async () => {
  renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.tab()
  const first = screen.getByRole('link', { name: 'Profile' })
  expect(document.activeElement).toBe(first)

  first.blur()

  // jsdom fires focusout with relatedTarget === null here
  // (jsdom/living/nodes/HTMLOrSVGElement-impl.js:82-83). In a real browser that is
  // what pressing the mouse on this panel's own non-focusable email header
  // produces, and closing on it would make the dropdown vanish under the cursor.
  // A naive onBlur={() => setOpen(false)} passes every other test in this file and
  // fails exactly here.
  expect(screen.getByTestId('user-menu-panel')).toBeInTheDocument()
})
```

**How these tests discriminate.**
- `Tab out of the last item` is a **genuine RED**: nothing closes on focus leaving today.
- `Shift+Tab ... leaves the menu OPEN` and `a blur with a null relatedTarget` both **pass against the current code** - they are the two guards that pin the shape of the rule rather than its existence. Their evidence is the mutation in Step 4.

- [ ] **Step 2: Run the tests to verify the first one fails**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: **1 failing test.** `Tab out of the last item closes the menu without stealing the destination` fails at `expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()`. The other two new tests pass.

- [ ] **Step 3: Implement**

Change the import on line 1 to bring in the React `FocusEvent` type:

```tsx
import { useEffect, useId, useRef, useState, type FocusEvent } from 'react'
```

Add the handler immediately after `closeAndRestoreFocus` and before the `return`:

```tsx
  // React maps onBlur to the native, BUBBLING focusout, so this fires for focus
  // leaving any descendant of the container.
  //
  // Tab out is a DISMISS route for a disclosure, not something to intercept. Do not
  // copy DialogShell's Tab trap here: a menu is not modal, and the page behind it
  // stays fully interactive.
  function onContainerBlur(e: FocusEvent<HTMLDivElement>) {
    // A NULL relatedTarget means "blurred to nothing" - jsdom fires exactly that for
    // a bare blur() (jsdom/living/nodes/HTMLOrSVGElement-impl.js:82-83), and in a
    // real browser it is what pressing the mouse on this panel's own non-focusable
    // email header produces. Closing on it would make the dropdown vanish under the
    // user's cursor. The document mousedown handler already owns the "pressed
    // somewhere else" case, so this branch has a correct owner and does not need a
    // second one.
    if (!e.relatedTarget) return
    // Shift+Tab from the first item lands on the toggle, which is INSIDE this
    // container, so the containment check is what keeps the menu open there.
    //
    // close(), not closeAndRestoreFocus(): by construction focus is already outside,
    // so the restore would be a theft from the destination the user just Tabbed to.
    if (ref.current && !ref.current.contains(e.relatedTarget)) close()
  }
```

Wire it on the container at `:31`:

```tsx
    <div ref={ref} className="relative" onBlur={onContainerBlur}>
```

The handler is attached unconditionally, including while the menu is closed; `close()` is then a `setOpen(false)` on a state that is already `false`, which React bails out of. That is deliberate - a conditional prop would be a second lifetime to keep in sync with the effect's.

- [ ] **Step 4: Run the tests, then run the mutation and record the output**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: PASS, 15 tests. Note in particular that `Escape does not steal focus when focus was outside the container` (Task 3) still passes - it reaches its outside-focus state via `blur()`, and the null-`relatedTarget` carve-out is what keeps the menu open long enough for Escape to be the thing that closes it. That test is the canary if the carve-out is wrong.

**Mutation C - the naive handler.** Replace the whole body of `onContainerBlur` with `close()`:

```tsx
  function onContainerBlur() {
    close()
  }
```

Run: `npx vitest run src/shell/UserMenu.test.tsx`
Expected: **2 failing tests** - `a blur with a null relatedTarget does NOT close the menu` (`expected element not to be null` / the panel is gone) and `Shift+Tab from the first item lands on the toggle and leaves the menu OPEN` (same assertion). Record both. **Revert.**

Re-run after reverting and confirm 15 passing.

- [ ] **Step 5: Commit**

```bash
git add web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx
git commit -m "fix(web): UserMenu closes when focus leaves the container"
```

---

## Task 5: The durable guards - no menu roles, no arrow keys - and the header comment

Nothing about behaviour changes in this task. It exists because the largest risk in this slice is that **a reviewer or a future session re-adds `role="menu"` from the backlog item's Proposal**, which is more prescriptive than the spec, easier to find, and reads as authoritative. Two sweep tests and a header comment are the mitigation.

**Files:**
- Modify: `web/src/shell/UserMenu.tsx` (header comment only)
- Modify: `web/src/shell/UserMenu.test.tsx` (append two tests)

- [ ] **Step 1: Write the guard tests**

Append to `web/src/shell/UserMenu.test.tsx`:

```tsx
// The durable guard against someone "restoring" role="menu" / role="menuitem" from
// the backlog item's Proposal without reading
// docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md, which deliberately
// INVERTED it. Three of the four entries are site navigation links - the case the
// menu role's own specification excludes - and role="menuitem" on an <a href>
// replaces the link role rather than adding to it.
test('the panel is a plain disclosure - no menu roles, no negative tabindex', async () => {
  renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  const panel = screen.getByTestId('user-menu-panel')

  expect(panel).not.toHaveAttribute('role')
  expect(panel.querySelectorAll('[role="menu"]')).toHaveLength(0)
  expect(panel.querySelectorAll('[role="menuitem"]')).toHaveLength(0)
  // No tabindex AT ALL, not merely no negative one: a roving tabindex is exactly
  // tabindex="0" on one item and tabindex="-1" on the rest, so asserting the
  // attribute is absent catches a half-built one too.
  expect(panel.querySelectorAll('[tabindex]')).toHaveLength(0)

  // Positive control: the sweep is looking at a POPULATED panel, so it cannot pass
  // against an empty or unmounted one. Three elements whose computed role is LINK,
  // and the same three as real anchors with an href - which is the semantic the
  // menu contract would have destroyed.
  expect(screen.getAllByRole('link')).toHaveLength(3)
  expect(panel.querySelectorAll('a[href]')).toHaveLength(3)
  expect(panel.querySelectorAll('button')).toHaveLength(1)
})

test('arrow keys do nothing - this is a disclosure, not a menu', async () => {
  renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.tab()
  const first = screen.getByRole('link', { name: 'Profile' })
  expect(document.activeElement).toBe(first)

  await userEvent.keyboard('{ArrowDown}{ArrowUp}{Home}{End}')

  // user-event DOES dispatch these as real keydowns (keyboard/keyMap.js:126-150,
  // system/keyboard.js:58,64-67) and jsdom's only built-in defaults for them are
  // radio-group walking and text-caret movement (event/behavior/keydown.js:24-54,
  // 69-91), neither of which applies to an <a>. So an unchanged activeElement here
  // is evidence that NO roving-tabindex handler exists - not evidence that the
  // harness cannot deliver the key. Arrow navigation would have been fully testable
  // here; it is rejected on the merits, not for lack of a harness.
  expect(document.activeElement).toBe(first)
  expect(screen.getByTestId('user-menu-panel')).toBeInTheDocument()
})
```

**How these tests discriminate.** Both are **guards, not regression tests** - they pass against the component as it stands and against `main`. Neither has been shown to measure anything until Step 3 runs its mutation. The positive controls (three elements with role `link`, three real `a[href]`, one `button`, and the `{ArrowDown}` dispatch landing on a genuinely focused element) are what stop them passing against an unmounted panel or an inert harness, but the mutation is what proves the assertion itself bites.

- [ ] **Step 2: Run the tests**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: PASS, 17 tests. **A green run here is expected and is not evidence of anything** - proceed to the mutation.

- [ ] **Step 3: Run both mutations and record the output**

**Mutation D - restore the menu roles.** On the `GlassPanel`, add `role="menu"`, and on the first `Link` add `role="menuitem"` and `tabIndex={-1}`.

Run: `npx vitest run src/shell/UserMenu.test.tsx`
Expected: FAIL in `the panel is a plain disclosure` at `expect(panel).not.toHaveAttribute('role')`, and, once that line is stepped past, at the `[role="menuitem"]` and `[tabindex]` counts. Also expect `expect(screen.getAllByRole('link')).toHaveLength(3)` to fail with a received length of 2 - which is the mechanical demonstration of the whole ARIA decision: `role="menuitem"` **removed** an anchor from the links list. Record that specifically. **Revert.**

**Mutation E - half a roving tabindex.** Add to the first `Link`:

```tsx
onKeyDown={(e) => {
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    ;(e.currentTarget.nextElementSibling as HTMLElement | null)?.focus()
  }
}}
```

Run: `npx vitest run src/shell/UserMenu.test.tsx`
Expected: FAIL in `arrow keys do nothing` at the second `expect(document.activeElement).toBe(first)`, received the Password link. **Revert.**

Re-run after reverting and confirm 17 passing.

- [ ] **Step 4: Add the header comment**

Insert this between the `UserMenuProps` interface (`:5-8`) and the `export function UserMenu` line. **Do not touch the `bg-popover`/`z-50` comment block further down the file** - it is load-bearing, it is unrelated, and reflowing it would break the pixel-neutrality review.

```tsx
// The header account dropdown. It is an ARIA DISCLOSURE, not a menu, and that is a
// decision rather than an omission - see
// docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md.
//
// WHY NO role="menu" / role="menuitem" / ARROW KEYS. Three of the four entries are
// site navigation links, which is the case the menu role's own specification
// excludes, and role="menuitem" on an <a href> REPLACES the link role: the item
// stops being announced as a link and drops out of a screen reader's links list. A
// conforming menu also uses a roving tabindex, which would make those three links
// unreachable by Tab. So the toggle carries aria-expanded plus aria-controls and
// nothing else; the items stay three ordinary links and one ordinary button, and Tab
// reaches them natively because the panel follows the toggle in DOM order. This file
// previously advertised aria-haspopup="menu" against a plain <div> of links
// (backlog: feature-2026-06-05-usermenu-panel-menu-roles, closed by INVERTING its
// Proposal); that attribute was the only thing that ever claimed "menu". If this
// dropdown ever stops containing navigation links and becomes actions only, the
// calculus flips and role="menu" becomes correct - that is the trigger to revisit,
// and nothing short of it is.
//
// WHY ESCAPE IS A DOCUMENT LISTENER AND NOT AN onKeyDown ON THE PANEL. A React
// onKeyDown only fires when the event target is a descendant of the panel, and focus
// leaves the panel through more routes than this component can close - notably
// Safari, which does not focus a <button> on click, so the menu can be open with
// activeElement === <body> and a panel-scoped handler would never fire at all. This
// is DialogShell's one high-severity review finding
// (components/dialog/DialogShell.tsx:59-75) and it binds harder here. Do not "tidy"
// it onto the panel. DialogShell's own document Escape listener calls
// stopImmediatePropagation specifically to suppress this one (DialogShell.tsx:355-370),
// and the two are structurally prevented from overlapping anyway, so do not change
// this listener's registration site or its open-only lifetime without re-deriving
// that argument.
//
// WHY THE PANEL IS NOT PORTALLED and does not register with dialogStack. Two
// reasons: the disclosure pattern needs the panel to FOLLOW the toggle in DOM order
// so Tab reaches it, and the dropdown's paint order is already solved by
// `relative z-10` on the header (HoloShell.tsx:29-49, measured over 275 hit-test
// points), which moving the panel to <body> would invalidate. Nothing here is modal:
// no scrim, no scroll lock, no inert, no aria-hidden on the background, and no Tab
// trap - for a disclosure, Tab out is a dismiss route.
```

- [ ] **Step 5: Run the suite and commit**

Run: `npx vitest run src/shell/UserMenu.test.tsx`

Expected: PASS, 17 tests (a comment changes nothing).

```bash
git add web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx
git commit -m "test(web): pin UserMenu as a disclosure - no menu roles, no arrow keys"
```

---

## Task 6: Verification gate

- [ ] **Step 1: Full web suite**

From `web/`:

```
npm test
```

Expected: PASS, zero failures. The baseline on this branch is **959** tests, measured on HEAD `16fc6ca` (the 811 figure predates the schedule-detail and profile slices, which took it 813 -> 890 -> 959); this slice adds **12**, so expect about 971. **Measure the baseline yourself before starting rather than trusting that number.** Any pre-existing failure must be measured **both with and without** this change before it is called pre-existing - never merge past a red gate on the strength of an assumption.

- [ ] **Step 2: The byte-identity gate on the shipped test file**

```bash
git diff --numstat origin/main...HEAD -- web/src/shell/UserMenu.test.tsx
```

Expected: **deletions exactly 2** (the test name at `:31` and the `aria-haspopup` assertion at `:34`). Any other deletion means a shipped assertion was edited to make new code pass, which **is the finding, not the fix** - report it rather than keeping it.

Then confirm the attribute is gone tree-wide (acceptance criterion 1):

```bash
git grep -n "aria-haspopup" -- web/src
```

Expected: matches only inside comments, if any at all. Any match in JSX is a failure.

- [ ] **Step 3: Type-check and production build**

From `web/`:

```
npm run build
```

Expected: `tsc -b` clean, then a successful `vite build`. This is the only step that type-checks - vitest transpiles without type-checking, so the `FocusEvent<HTMLDivElement>` annotation and the `toggleRef` type are first checked here.

- [ ] **Step 4: Revert the build output**

`web/dist` is **tracked but stale** from the original scaffold and is not maintained per-PR. `npm run build` rewrites it and dirties the working tree. From the repo root:

```bash
git checkout -- web/dist/
git status --short
```

Expected: `web/dist/` shows no modifications.

- [ ] **Step 5: Go gate (proving no backend regression)**

`make` is **not on PATH in this shell**. From the repo root:

```
go build ./...
go test ./...
```

Expected: PASS. This slice changes zero Go files, so a failure here is unrelated - but run it, and if it is red, get a number with and without the change rather than assuming.

Integration tests are **not** required: no Go file, no `.sql` file and no migration changed, so there is no `make generate` step and no database surface is touched.

- [ ] **Step 6: Confirm the change set**

```bash
git status --short
git diff --stat origin/main...HEAD
```

Expected file set, and nothing else:

```
web/src/shell/UserMenu.tsx
web/src/shell/UserMenu.test.tsx
```

plus `docs/` (the spec, this plan, and the backlog close). **`web/dist`, `web/src/components/`, `web/src/shell/HoloShell.tsx` and every Go file must be absent from that list.**

Pixel-neutrality check (acceptance criterion 11) - review the diff by eye and confirm that **every `className` string is byte-identical** and the comment block at `UserMenu.tsx:40-51` is unedited:

```bash
git diff origin/main...HEAD -- web/src/shell/UserMenu.tsx
```

- [ ] **Step 7: Close the backlog item**

```
/backlog close feature-2026-06-05-usermenu-panel-menu-roles
```

That command `git mv`s the file into `docs/backlog/closed/`, stamps the frontmatter and appends a Resolution. **The Resolution must state plainly that the item's Proposal was INVERTED and why** - a closed item titled "lacks menu/menuitem roles" is otherwise circumstantial evidence that those roles were added. Then check for inbound references and repair any in the same commit:

```bash
git grep -n "feature-2026-06-05-usermenu-panel-menu-roles" -- docs
```

---

## Appendix: final state of `UserMenu.tsx`

Diff your result against this. If it differs anywhere other than whitespace, one of the tasks was applied wrongly. Comments are elided with `...` where a task above gives them in full; **write the full comments, not the elisions.**

```tsx
import { useEffect, useId, useRef, useState, type FocusEvent } from 'react'
import { Link } from 'react-router-dom'
import { GlassPanel } from '../components/holo'

interface UserMenuProps {
  email: string
  onLogout: () => void
}

// ... Task 5's header comment: disclosure not menu, Escape on document, no portal ...
export function UserMenu({ email, onLogout }: UserMenuProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const toggleRef = useRef<HTMLButtonElement>(null)
  const panelId = useId()

  // ... Task 3's comment: close WITHOUT touching focus ...
  function close() {
    setOpen(false)
  }

  // ... Task 2's comment: read the containment check BEFORE setOpen; the guard is
  // what separates a restore from focus theft ...
  function closeAndRestoreFocus() {
    const focusWasInside = !!ref.current && ref.current.contains(document.activeElement)
    setOpen(false)
    if (focusWasInside) toggleRef.current?.focus()
  }

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      // ... Task 3's comment: mousedown precedes the focus change ...
      if (ref.current && !ref.current.contains(e.target as Node)) close()
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') closeAndRestoreFocus()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
    // ... Task 3's comment: the helpers touch only stable refs and setOpen ...
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // ... Task 4's comment: onBlur is the bubbling focusout; a null relatedTarget
  // means "blurred to nothing" and must not close ...
  function onContainerBlur(e: FocusEvent<HTMLDivElement>) {
    if (!e.relatedTarget) return
    if (ref.current && !ref.current.contains(e.relatedTarget)) close()
  }

  return (
    <div ref={ref} className="relative" onBlur={onContainerBlur}>
      <button
        ref={toggleRef}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        // ... Task 1's comment: only while the panel is mounted ...
        aria-controls={open ? panelId : undefined}
        className={`flex items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}`}
      >
        <span className="text-fg normal-case tracking-normal">{email}</span>
      </button>
      {/* the shipped bg-popover / z-50 comment block, UNEDITED */}
      {open && (
        <GlassPanel
          id={panelId}
          data-testid="user-menu-panel"
          className="absolute right-0 z-50 mt-2 w-56 bg-popover p-1.5 text-[12px]"
        >
          <div className="mb-1.5 flex items-center gap-2.5 border-b border-border px-2.5 pb-2.5 pt-2">
            <span className="truncate text-[12.5px] text-fg">{email}</span>
          </div>
          <Link
            to="/profile"
            onClick={closeAndRestoreFocus}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Profile
          </Link>
          <Link
            to="/profile/password"
            onClick={closeAndRestoreFocus}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Password
          </Link>
          <Link
            to="/profile/sessions"
            onClick={closeAndRestoreFocus}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Sessions
          </Link>
          <div className="my-1.5 h-px bg-border" />
          <button
            onClick={() => {
              closeAndRestoreFocus()
              onLogout()
            }}
            className="block w-full rounded-md px-2.5 py-2 text-left text-err hover:bg-white/5"
          >
            Log out
          </button>
        </GlassPanel>
      )}
    </div>
  )
}
```

---

## Phase 6 proposals (propose, do NOT auto-file)

Three, matching the spec's Follow-ups table. Each is a proposal for human accept; nothing is auto-filed.

1. **`idea-2026-08-13-aria-haspopup-sweep-test` (low)** - a sweep asserting that every `aria-haspopup` in `web/src` names a popup role actually implemented on the controlled element, and symmetrically that `role="menu"` never appears without a roving tabindex. This item existed for over two months because an attribute claimed a contract no code implemented and nothing could notice. Same enforcement shape and the same Vitest-reads-the-tree-versus-ESLint question as the already-filed `idea-2026-08-09-dialog-shell-sweep-test` and `idea-2026-08-13-field-error-wiring-audit`; the three should be decided together and probably built once.
2. **`idea-2026-08-13-usermenu-becomes-a-true-menu-if-it-goes-actions-only` (low)** - carry the **trigger condition** rather than the conclusion: if this dropdown ever stops containing navigation links and becomes a list of actions, `role="menu"` plus roving tabindex becomes correct and this decision should be revisited. Same treatment `idea-2026-08-09-native-dialog-element-reconsideration` got.
3. **A note on `idea-2026-08-12-document-z-index-layering-scale`** - add "the dropdown is deliberately not portalled, because the disclosure pattern needs it in DOM order after its toggle" to that item's context, so the next reader does not conclude that portalling it is the tidy fix for the two confusing `z-50`s. It is not: it would break Tab order.

Deliberately **not** proposed: a visually-hidden "Account menu" addition to the toggle's accessible name (it is the email today, which is a good name, and changing it touches five shipped queries for no measured gain); the hi-fi's per-item icons and hints, which is a visual scope this slice does not open.

---

## Self-review

**Spec coverage.** All fourteen acceptance criteria map to a task: 1 -> Task 1 plus the `git grep` in Task 6 Step 2; 2 -> Task 1; 3 -> Task 5; 4 -> Task 3 (both directions); 5 -> Task 2; 6 -> Task 4 (all three directions, including the null `relatedTarget`); 7 -> Task 3's mousedown test; 8 -> no code moves focus on open, and Task 3's `Escape does not steal focus` test would fail if anything did (it asserts `activeElement` is `<body>` after a mouse open plus a blur); 9 -> Task 5's arrow test; 10 -> the unchanged effect in Task 3 plus the Scope guard plus Task 6's diff review; 11 -> Task 6 Step 6; 12 -> Task 6 Step 2; 13 -> Task 6 Steps 1, 3, 4 and 6; 14 -> Task 6 Step 7.

The spec's nine numbered tests map to twelve here: its test 1 splits into the sanctioned edit plus the `aria-controls` test (Task 1); test 5 splits into three (Task 2) so that `Log out` is covered separately from the links and the shipped `onLogout` test stays byte-identical; tests 2, 3 and 7's mousedown consequence become Task 3's three; test 4 splits into the Tab-out and Shift+Tab pair and test 6 joins them (Task 4); tests 8 and 9 are Task 5's two.

**Deviations from the spec, each with a reason.**

1. **The outside-mousedown rule is tested with `vi.spyOn(toggle, 'focus')` rather than with an `activeElement` assertion.** The spec's acceptance criterion 7 says the path "does not move focus", and I verified that an end-state assertion **cannot** measure that in this harness: user-event moves focus to the clicked control after the mousedown listeners run (`user-event/dist/cjs/event/focus.js:14-25`), so a focus-stealing close is overwritten and both implementations finish with the same `activeElement`. The spy is the only instrument that discriminates, and it is the same one `DialogShell.tsx:256-259` reached for. Recorded here because a reviewer comparing this test to the spec's prose will notice it asserts a call rather than a state.
2. **The absence sweep uses `querySelectorAll` and document-scoped `screen` queries instead of `within(panel)`.** `within` is not currently imported by `UserMenu.test.tsx:1`, and importing it would modify a line the byte-identity gate protects. The `querySelectorAll` form is also slightly stronger: `a[href]` asserts they are real anchors, not merely elements with the link role.
3. **Three tests are declared guards rather than regression tests, with a named mutation instead of a RED run** (Task 3's two, Task 4's two, Task 5's two). The spec asks for each new test's control; where the control cannot be a missing implementation, this plan substitutes a mutation that must be run and whose output must be recorded. Stated explicitly so a task report claiming "all green" without the mutation outputs is visibly incomplete.
4. **`aria-controls` is asserted absent when closed, not merely "set only while the panel is rendered".** Same rule, stated as something a test can see.
5. **The logout handler is `closeAndRestoreFocus()` then `onLogout()`**, matching the spec's Architecture note ("close-then-`onLogout`"). Worth noting that in the real app `onLogout` unmounts this whole shell, so the focus restore is a no-op there; it matters only in tests and in any future caller that does not navigate. Keeping the ordering uniform across all four items is worth more than special-casing one.

**Placeholder scan.** No TBD, no "add appropriate error handling", no "similar to Task N". Every code step carries literal code; every test step carries the literal test; every mutation names its exact edit and its expected failure.

**Type consistency.** `close`, `closeAndRestoreFocus`, `onContainerBlur`, `toggleRef`, `panelId` and `ref` are spelled identically in Tasks 1 through 5 and in the appendix. `renderMenu` (shipped) and `renderMenuWithSibling` (added in Task 3, reused in Task 4) are the only two render helpers; no test uses a helper before the task that defines it. `data-testid="user-menu-panel"` is the single panel handle throughout and is never renamed.
