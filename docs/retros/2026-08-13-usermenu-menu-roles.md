---
date: 2026-08-13
topic: usermenu-menu-roles
branch: claude/pr-merge-session-f5796e
range: origin/main..HEAD (green, not yet merged)
---

# Session Retro: 2026-08-13 - UserMenu as a Disclosure (the item inverted)

**TL;DR:** Closed `feature-2026-06-05-usermenu-panel-menu-roles` by **inverting its Proposal**. The
item asked for `role="menu"` / `role="menuitem"` / roving-tabindex arrow keys; the slice **removed
`aria-haspopup="menu"`** instead, added `aria-controls`, kept native Tab through ordinary links, and
added no arrow keys. Three of the four entries in that dropdown are site-navigation links, which is
the case the `menu` role's own specification excludes. The inversion reverses a priority call the
repository owner made personally, so it was adjudicated before merge by a review lane explicitly told
not to rubber-stamp it, and it survived with citations. Along the way the spec found **two defects the
item never mentioned** (the dropdown stayed open over the page it had just navigated to; Escape
dropped focus to `<body>`), review found **one behavior regression this PR introduced** (the new
`onClick` fired on Ctrl/Cmd+click because react-router calls the user's handler before it decides not
to navigate), and probes found **three false comments in one file plus one documented invariant with
zero guard**. Two source files, 14 new tests, suite 959 -> 973.

## The inversion

This is the headline and it deserves to be read before anything else in this document.

**What the item asked for.** `feature-2026-06-05-usermenu-panel-menu-roles.md:19-29`: give the panel
`role="menu"`, each item `role="menuitem"`, wire `aria-controls`, add roving-tabindex arrow
navigation with Home/End, focus the first item on open. It was raised **low -> medium by the
repository owner personally** (`:41-47`) with a stated and correct rationale: the toggle has
advertised `aria-haspopup="menu"` to assistive technology since 2026-06-05 while the panel it points
at has been a plain `<div>` the whole time, so a screen-reader user is promised a menu and handed
something that is not one.

**What shipped.** The premise was accepted in full and the remedy was reversed. A contract mismatch
has two fixes - implement the promise, or stop making it - and which one is right depends on whether
the promise was the right promise. `aria-haspopup` is gone (`UserMenu.tsx:174-183` carries
`aria-expanded` and `aria-controls` and nothing else), the panel carries an `id` and **no role**
(`:199-203`), the three entries stay `<a href>` and the fourth stays `<button>`, Tab reaches them
natively because the panel follows the toggle in DOM order, and there are no arrow keys.

**Why that is the right direction, in the order the weight actually falls.**

1. **Three of the four entries are site navigation.** `/profile`, `/profile/password` and
   `/profile/sessions` are links to pages that started existing on 2026-08-12. WAI-ARIA 1.2 defines
   `menu` as "a list of choices... often a list of common actions or functions". A 3:1 navigation
   majority is the case the pattern's own definition excludes.
2. **APG has a pattern for exactly this surface, and it says not to use the menu roles.** The review
   lane's decisive citation was the **Disclosure Navigation Menu** pattern, which exists precisely
   for a disclosure containing navigation links and states that `menu` and `menuitem` are
   deliberately not used when the entries are links. This is not a judgment call between two
   defensible readings; it is a named pattern for the exact shape.
3. **`role="menuitem"` on an `<a href>` replaces the link role rather than adding to it.** The item
   is announced as a menu item, drops out of a screen reader's links list, and drops out of
   browse-mode "next link" navigation. Confirmed by the review lane as an accurate statement about
   the platform accessibility tree.
4. **A conforming roving tabindex makes those three links Tab-unreachable.** Exactly one item is
   tabbable and the rest carry `tabindex="-1"`, so a keyboard user who opens the dropdown and keeps
   Tabbing would have to know to press ArrowDown instead.
5. **`aria-haspopup="true"` was not an escape hatch.** ARIA 1.2 treats `true` as equivalent to
   `menu`, so weakening the attribute rather than deleting it would have made the same claim in
   quieter words.

**The adjudication.** Because this reverses an owner decision, it was reviewed as a decision and not
as code. The lane was told not to rubber-stamp it. Verdict: correct, with the citations above, plus
independent confirmation of all three supporting claims (points 3, 4 and 5). The lane also recorded
the honest cost: some screen-reader users have learned that an account control in an app header is
usually a `menu`, and GitHub's really is one. **Nothing shipped is worse than `main` for a
screen-reader or a keyboard user** on any route.

**Mutation D is the mechanical proof, and it is better evidence than any of the prose.** Adding
`role="menuitem"` to the first `Link` broke **seven other tests** that had nothing to do with roles,
because `getByRole('link', { name: 'Profile' })` stopped resolving, plus the sweep's own
`getAllByRole('link')` count fell from 3 to 2. The item's proposed fix would have destroyed the
semantics it was trying to add, and the test suite says so in eight places without being asked. That
is the argument that would have been unavailable in June, when the panel had four tests and none of
them queried by role.

**The trigger to revisit, recorded rather than the conclusion.** If this dropdown ever stops
containing navigation links and becomes actions only, the calculus flips and `role="menu"` plus a
roving tabindex becomes correct. That sentence is in the component header (`UserMenu.tsx:35-38`) so
the next author re-evaluates instead of either re-litigating or blindly inheriting. Same treatment
`idea-2026-08-09-native-dialog-element-reconsideration` got.

**What would have made the item right in the first place.** Its ancestor,
`docs/backlog/closed/bug-2026-06-03-usermenu-aria-attributes.md:13`, named a **fix** rather than a
**problem**: "Add `aria-haspopup="menu"` and bind `aria-expanded` to the open state." Nobody ever
asked what the panel was, because the item did not pose that question. The follow-up item then
inherited the fix as a premise and spent two months asking how to make the panel match an attribute
that should never have been written. An item phrased as "the toggle announces a popup type that the
panel does not implement; decide which of the two is wrong" would have been answerable correctly on
day one, in one line of code, by whoever picked it up. **The failure was in the item's grammar, not
in anybody's ARIA knowledge.**

**The mitigations against the item being resurrected**, since a closed item titled "lacks
menu/menuitem roles" is otherwise circumstantial evidence that those roles were added: the sweep test
at `UserMenu.test.tsx:333-353` (no `role`, no `role="menu"`, no `role="menuitem"`, no `tabindex` at
all, with a populated-panel positive control), the arrow-key test at `:355-373`, the 22-line header
comment at `UserMenu.tsx:17-38`, and the requirement that the `/backlog close` Resolution state the
inversion in words. Four independent places, because the item is more prescriptive than the spec,
easier to find, and reads as authoritative.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md`, **plan**
  `docs/superpowers/plans/2026-08-13-usermenu-menu-roles.md` (five implementation tasks plus a
  verification gate, one frontend engineer, strictly sequential - every task edits the same file and
  Tasks 3 to 5 depend on helpers introduced in Task 2).
- **Exactly two source files changed**, both in `web/src/shell/`: `UserMenu.tsx` (86 lines -> 246,
  of which more than half the delta is comment) and `UserMenu.test.tsx` (58 -> 374).
- `aria-haspopup="menu"` deleted. `useId()` panel id plus `aria-controls={open ? panelId : undefined}`
  (`UserMenu.tsx:181`), set only while the panel is mounted because it is conditionally rendered and
  a permanent IDREF to a non-existent node is an authoring error. The `id` lands on `GlassPanel`
  (`:200`) through its existing prop spread, with no change to the primitive.
- `closeAndRestoreFocus()` (`:95-99`) - reads `ref.current.contains(document.activeElement)` first,
  then `setOpen(false)`, then focuses the toggle only if the check passed. Used by Escape and by all
  four items.
- `close()` (`:103-105`) - closes without touching focus. Used by the outside-mousedown path and by
  the focusout path, the two places where the browser is already moving focus itself.
- `onNavItemClick()` (`:116-119`) - the review fix. Bails on `metaKey || ctrlKey || shiftKey ||
  altKey || button !== 0`, the same predicate react-router's own `shouldProcessLinkClick` uses.
- `onContainerBlur()` (`:155-170`) - closes when focus leaves the container, keyed on a **non-null**
  `relatedTarget` outside it. A null `relatedTarget` means "blurred to nothing" and must not close,
  or pressing the panel's own non-focusable email header would make it vanish under the cursor.
- The document `mousedown` and `keydown` listeners keep their registration site and their open-only
  lifetime unchanged (`:121-147`), deliberately, because `DialogShell` reasons about this listener.
- 14 new tests (5 shipped tests were already there, 19 now in the file). Suite **959 -> 973**.
- **Zero Go, zero SQL, zero proto, zero migration.** Third consecutive frontend-only iteration.

## Key Decisions

- **The disclosure discharges the same duty with a quarter of the machinery.** No roving tabindex, no
  arrow keys, no Home/End, no typeahead question, no "Tab closes the menu" special case, no
  `aria-activedescendant`-versus-real-focus decision. Every mechanism not built is a mechanism that
  cannot be built wrong, and the two real defects (stays open on select, drops focus on Escape) were
  orthogonal to the roles and had to be fixed either way. The spec states loudly that this is **not**
  a licence to delete one attribute and stop: removing the advertisement without fixing the keyboard
  behaviour would have closed the item on a technicality.

- **Escape and outside-mousedown close differently, and the difference is entirely event ordering.**
  Escape fires with focus wherever the user left it, so if that was inside the container the toggle
  must get it back. `mousedown` fires **before** the browser moves focus to whatever was pressed, so
  at handler time `activeElement` is still inside the panel and a shared close would yank focus onto
  the toggle and away from the control the user just clicked. Same rule, opposite answer. This is why
  `DialogShell`'s `focusWasInside` reasoning had to be re-derived rather than copied, and the
  re-derivation is written at `UserMenu.tsx:124-129`.

- **Nothing from `DialogShell` is imported; it is reused as reasoning only, and the split was made
  explicit in advance.** Reusable: Escape stays a document listener, capture `focusWasInside` before
  teardown, end the generation before releasing the resource. Inapplicable, because a menu is not
  modal: the Tab trap (Tab out is a dismiss route here), the `inert`/`aria-hidden` background
  marking, the scrim, the scroll lock, `dialogStack` registration, `isTopmost`, the portal, and the
  landmark focus fallback (its premise is a trigger that may have been removed from the DOM; this
  trigger is a sibling that always outlives the panel). Writing the inapplicable half down was worth
  as much as the applicable half.

- **The panel is deliberately not portalled**, for two independent reasons: the disclosure pattern
  needs it to follow the toggle in DOM order so Tab reaches it, and the dropdown's paint order is
  already solved by `relative z-10` on the header (`HoloShell.tsx:29-49`, measured over 275 hit-test
  points), which moving the panel to `<body>` would invalidate. Recorded at `UserMenu.tsx:62-68` so
  the next reader of `idea-2026-08-12-document-z-index-layering-scale` does not conclude that
  portalling the dropdown is the tidy fix for the two confusing `z-50`s. It is not; it would break
  Tab order.

- **The absence sweep asserts no `tabindex` at all, not merely no negative one.** A roving tabindex
  is exactly `tabindex="0"` on one item and `tabindex="-1"` on the rest, so asserting the attribute
  is absent catches a half-built one too. That is the difference between a guard and a speed bump.

- **The toggle's accessible name was not touched.** It is the raw email, which is a good name, and
  changing it would move five shipped queries for no measured gain. Recorded as a deliberate
  non-change rather than an oversight.

- **Pixel-neutral by construction.** No class string changed, and the load-bearing
  `bg-popover`/`z-50` comment block was not reflowed. The repo has no visual regression harness, so
  neutrality is argued by byte-identical class attributes and a reviewer diffing them, and the plan
  named that as the review gate before implementation started.

## Problems Encountered

1. **The fix for defect 1 introduced a real behavior regression, and it came from the library's call
   order.** The three `Link`s got `onClick={closeAndRestoreFocus}`. React Router's `Link` calls the
   caller's `onClick` **before** `shouldProcessLinkClick` bails on `isModifiedEvent`, so a
   Ctrl/Cmd/Shift/Alt+click opened the destination in a background tab **and** collapsed the dropdown
   and yanked focus to the toggle in the tab the user is still looking at. Middle-click did not,
   because that arrives as `auxclick`, so two of the three new-tab gestures disagreed with each
   other. On `main` the menu stayed open for a modified click, so **this PR introduced it**. Fixed
   with `onNavItemClick` (`UserMenu.tsx:116-119`) using react-router's own predicate, pinned by
   `UserMenu.test.tsx:127-151`.

   Two things make this worth its own entry. First, the previous retro's lesson was "a fix that
   reaches into a library's state machine takes that machine's outputs with it"; this is the same
   family from the other side, where the library reaches into **you** at a moment you did not choose.
   Second, the fix deliberately copies react-router's predicate rather than re-deriving an equivalent
   one, so the two cannot drift.

2. **Three comments in one file said things that are false, and every one was caught by a probe
   rather than by reading.**
   - **"Structurally prevented from overlapping" - both halves false.** The comment claimed
     `UserMenu`'s document Escape listener and `DialogShell`'s cannot overlap, and that
     `stopImmediatePropagation` covers the residue. They **do** overlap: mouse-open the dropdown so
     focus never lands inside it, then keyboard-focus a page control and open a dialog. And
     `UserMenu`'s listener registers **first** in that ordering, so `stopImmediatePropagation` cannot
     suppress it - it cannot un-ring a bell an earlier listener already rang. One Escape dismissed
     both, measured directly. The comment additionally instructed future maintainers not to change
     the listener without "re-deriving that argument", against an argument that was already refuted.
     Corrected at `UserMenu.tsx:47-60`, which now states the overlap, states that dismissing both is
     acceptable, and states that **registration order** decides which listener wins.
   - **The Invariant-1 rationale named a mechanism that does not exist.** It claimed `setOpen(false)`
     synchronously unmounts the panel and detaches the focused node, which is why the containment
     check is read first. React 18.3.1 batches the update: `panel.isConnected === true` at the moment
     `focus()` runs, and reading the check after `setOpen` would observe the identical value. The
     ordering is harmless and was kept; the stated mechanism was false and is now corrected at
     `:78-94`, which points at `DialogShell.tsx:227-240` as the **real** instance of that shape (its
     read runs in an effect cleanup, which React defers past the point the node is gone).
   - **A cost claim that was too broad.** The comment said `role="menuitem"` costs "open in new tab".
     It does not: the anchor is still a real `<a href>`, so the browser's context menu and
     Ctrl/Cmd+click behave identically. Only the AT-exposed semantics change. Corrected at `:25-28`.
     This one matters more than it looks, because it appeared inside the argument for the inversion -
     an over-claim in favor of the conclusion you already reached.

   Plus a wrong source citation in a test comment, corrected to
   `system/pointer/mouse.js:74-79` (`UserMenu.test.tsx:234`).

3. **One documented invariant had zero guard, and the whole suite was blind to it.** The comment
   warned against changing the Escape listener's open-only lifetime, and `DialogShell` reasons about
   it. Mutating `if (!open) return` away and `[open]` to `[]` **passed the entire suite** - every
   other test in the file opens the menu before asserting anything, so none of them can see the
   closed state. A test was added (`UserMenu.test.tsx:317-325`) that spies on
   `document.addEventListener` and asserts zero `keydown` registrations on a freshly rendered, closed
   menu. Its mutation proof shows every other test staying green while only the new guard reddens,
   which is the cleanest discrimination evidence in the slice.

   This is the previous iteration's "a shared primitive can be missing a behaviour none of its
   consumers' tests can see", generalized: **a comment that forbids changing something is a
   specification with no test behind it until somebody writes one.**

4. **An implementer claim about evidence strength was refuted, and the refutation is the useful
   part.** The engineer reported mutations 3 and 4 as producing "stronger than predicted" RED - 8 to
   9 failing tests where the plan predicted 2. The correctness lane checked and found that for
   mutation 4 this is **coupling, not discrimination**: exactly one of those tests is a real
   discriminator and the other eight die in shared `getByRole`/`tab()` setup, so they would redden
   for many unrelated mutations too. Mutation 3 came out better - its extra failures include a
   genuine defect (an unconditional close on focusout unmounts the panel before `click` dispatches,
   breaking mouse activation of every item) - but roughly half are still the same prologue.

   Recorded as a standing lesson: **a wide RED is not automatically a strong RED.** The number of
   failing tests measures how much shared setup a mutation stands upstream of. What measures
   discrimination is whether a test fails *at its own assertion*, for the reason the mutation
   describes.

5. **The plan's own appendix is now stale, in the one place review changed the design.** Its
   "final state of `UserMenu.tsx`" block still shows `onClick={closeAndRestoreFocus}` on the three
   `Link`s and instructs the engineer to diff their result against it. Applied literally today, that
   instruction would flag the **correct** code as wrong and reintroduce the modifier-click
   regression. Nothing broke because the review fix landed after the plan was consumed, but a plan
   that ships a golden copy of the target file acquires a second thing to keep true.

## Findings Triage

- **1 behavior regression, 3 false comments, 1 wrong citation, 1 unguarded invariant, 1 refuted
  evidence claim. 0 HIGH, 0 security.** The regression (Problem 1) is the only finding that would
  have reached a user; it was introduced by this PR and caught before merge.
- **The regression came out of the fix, not out of the original code.** Both of the slice's
  user-visible behavior findings this iteration - the modifier-click close and the focusout ordering
  found under mutation 3 - are consequences of new handlers, not of anything the item complained
  about. That is now a pattern worth naming: on a slice whose stated job is to **remove** an
  attribute, the risk concentrates in the small amount of behavior added alongside it.
- **The inversion was adjudicated, not assumed.** A review lane was explicitly briefed not to
  rubber-stamp the spec's reversal of an owner decision, and it confirmed the direction with
  independent citations plus verification of the three supporting claims. This is the right shape for
  any spec that reverses a human call, and it should be the standing rule rather than a one-off.
- **Every absence assertion in the slice carries either a positive control or a named mutation.** The
  plan said so in advance and named the reason: this change is almost entirely about things being
  absent (`aria-haspopup` gone, no `role`, no `tabindex`, arrow keys inert), and an absence assertion
  is trivially satisfiable by a component that does nothing or by a test looking at the wrong
  element. Five mutations were run and recorded.
- **Two deletions in `UserMenu.test.tsx`, both sanctioned in the spec before implementation started**
  (the test name and the `aria-haspopup` assertion). The byte-identity gate held: no shipped
  assertion was adjusted to make new code pass, which the last three iterations have all used as the
  signal that a "refactor" is actually a behavior change.
- **The conductor's `/code-review` output was supplied to the lanes as prior findings**, per the
  standing shape. Each lane confirmed or refuted independently before adding its own passes.
- **I could not verify whether a real-browser lane ran this iteration.** The 2026-08-12 retro
  promoted "when the Go diff is empty, spend the integration lane on a real browser" from a good
  trade to the default, and this slice has an empty Go diff. Nothing in the material handed to Phase
  6 mentions a browser lane, and this pass has no shell, so I record it as **unknown, not as done**.
  If it did not run, that is a lapse against a goal promoted one iteration ago; if it did, the
  verification report should have said what it could and could not deliver.

## Deferred Findings

Filed this pass (three items, each proposed for human review rather than treated as accepted):

1. `idea-2026-08-13-post-logout-focus-lands-on-body.md` (**idea/low**) - `UserMenu.tsx:229-241` calls
   `closeAndRestoreFocus()` and then `onLogout()`, and `HoloShell.tsx:22-25` awaits `logout()` and
   navigates to `/auth`, which unmounts the toggle that just received focus. Focus falls to `<body>`
   on the sign-in page. **Identical on `main`, so not a regression**, and the close-then-hand-off
   ordering is correct for the containment check. But a keyboard user loses their place at exactly
   the moment they need it, and the fix belongs at the destination (focus the sign-in form's first
   control or its heading), not in the menu.
2. `idea-2026-08-13-usermenu-outside-mousedown-drops-focus.md` (**idea/low**) - the outside-mousedown
   path closes without touching focus (`UserMenu.tsx:130`), which is **correct** whenever the press
   target is focusable, because `mousedown` precedes the browser's focus move and a restore would
   steal focus from the control the user just clicked. When the press lands on dead space, nothing
   takes focus and it falls to `<body>`. A microtask-scheduled restore that re-checks
   `document.activeElement` was proposed. Filed rather than fixed because the carve-out's rationale
   is written at the call site and a future author needs the counter-case written down somewhere it
   can be found. Same family as item 1 and cross-linked to it.
3. ~~A `DialogShell` comment carrying the refuted overlap guarantee~~ - **proposed as an item and
   then fixed in-branch instead, which is the more interesting outcome.** The TPM correctly spotted
   that correcting the claim in `UserMenu.tsx` had left **its source** intact:
   `DialogShell.tsx:361-370` still asserted that the toggle "goes inert while any dialog is open, so
   the dropdown cannot be opened in the first place while this fires", so the two shipped comments
   contradicted each other and the false one lived in the shared primitive the next dialog author
   reads first. The conductor judged that filing a bug item describing a known-false claim in a
   shipped file is strictly worse than deleting the claim, and corrected it in this PR.

   **A tree-wide grep for the claim's own wording then found a fourth site** that nobody had named:
   `DialogShell.test.tsx:419` repeated it in a test comment. Both are corrected here. This is the
   fifth consecutive iteration in which a refutation propagated further than the person chasing it
   believed, and the third in which the extra site was found by grepping the claim's literal text
   rather than by reasoning about where it might be. The rule earns its place in Improvement Goals:
   **when a claim is refuted, grep the tree for its wording before deciding where it lives.**

Considered and **not** filed, with reasons:

- **An `aria-haspopup` sweep test** (proposed by the spec's Follow-ups table). **Rejected as a
  separate item, and recommended as an amendment instead.** There are already two open items
  proposing a source-tree sweep of the identical shape - `idea-2026-08-09-dialog-shell-sweep-test`,
  which already allowlists `web/src/shell/UserMenu.tsx` by name for its `document` keydown listener,
  and `idea-2026-08-13-field-error-wiring-audit`, which already says the Vitest-reads-the-tree versus
  ESLint question "should be made once for both". A third file would be the third statement of one
  open question and would fragment a decision that wants to be made once. **Recommendation for human
  accept:** add a fourth assertion to `idea-2026-08-09-dialog-shell-sweep-test`'s Proposal - every
  `aria-haspopup` in `web/src` must name a popup role actually implemented on the element it
  controls, and `role="menu"` must never appear without a roving tabindex - with a note that this
  slice is the worked example of what the sweep would have caught two months earlier. Not made
  unilaterally, because amending an existing item's Proposal is a change to somebody else's stated
  scope. Note that the component-level half is **already covered** by `UserMenu.test.tsx:333-353`;
  the sweep is only about the *next* component.
- **The email being announced twice** (the toggle's accessible name is the raw email at
  `UserMenu.tsx:184`, and the panel's first row repeats it at `:204-206`). **Rejected.**
  Pre-existing, deliberately out of scope per spec decision 12, and arguably not a defect at all: the
  toggle name is what a screen reader announces on focus and the panel row is visible context for a
  sighted user. Changing the toggle's accessible name moves five shipped queries for no measured
  gain, and "the same string appears twice" is not evidence of harm. Filing it would spend a
  reviewer's attention on the one candidate in the pile that has no argued cost.
- **The stale line citations in `UserMenu.test.tsx:309`**, which point at `UserMenu.tsx:82,107` for
  `if (!open) return` and `}, [open])`; the real lines are `:122` and `:147`, the drift almost
  certainly caused by the header comment growing during the review fixes. Recorded here rather than
  filed: it is a one-line repair for whoever next touches the file, and an item would cost more to
  triage than to fix.
- **`docs/backlog/closed/bug-2026-06-03-usermenu-aria-attributes.md` carries `status: open` in its
  frontmatter** despite living in `closed/`, and has no Resolution section. Pre-existing housekeeping
  from before the `/backlog close` command existed. Noted for whoever runs the next backlog sweep.

## Known Limitations

- **No screen reader was involved at any point.** The entire ARIA argument rests on the
  specification, on APG's named pattern, and on element semantics. No test in this repo can measure
  an accessibility-tree announcement, and the slice says so rather than implying otherwise. The
  claim "a screen-reader user now hears 'link' instead of 'menu item'" is **argued, not measured**.
- **Safari's "a click does not focus a `<button>`" behaviour is simulated, not reproduced.**
  `UserMenu.test.tsx:203-213` reaches the open-with-focus-on-`<body>` state by calling `.blur()`, and
  its comment says explicitly that this stands in for Safari rather than reproducing it. That state
  is the strongest reason Escape stays on `document`, and it has never been exercised in a real
  WebKit.
- **The Escape overlap with `DialogShell` is now known to be real and is left as is.** One Escape
  dismisses both the dropdown and the dialog. That was judged acceptable rather than fixed, and the
  reasoning is at `UserMenu.tsx:51-60`. Anyone who later portals the menu, changes its listener
  lifetime, or adds a third document keydown listener has to re-derive it - and the guard test at
  `:317-325` now fails loudly if the lifetime moves.
- **Focus lands on `<body>` on two close paths** (post-logout navigation, outside-mousedown onto dead
  space), both filed, both identical on `main`.
- **Paint order and occlusion are untested here.** jsdom does no layout. The dropdown's stacking
  rests on class assertions plus the 275-point hit test recorded in `HoloShell.tsx:29-48`, measured
  in a different iteration. This slice changed no class and no z-index specifically so that
  measurement stays true.
- **Real-browser Tab traversal was not confirmed** - in particular, whether the open dropdown
  visually obscures the element Tab lands on after leaving the panel.
- **No end-to-end coverage in CI.** `idea-2026-06-03-web-e2e-harness` remains open, and this is a
  third consecutive frontend slice whose keyboard behaviour is proven only in jsdom.
- **The dropdown was never exercised against a real `relay-server`.** Irrelevant to this surface -
  it issues no request - but true.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, sixth
  iteration running, and this time it produced not a correction but a **reversal**. Everything the
  item asserted about the current state was true; everything it prescribed was wrong.
- **A backlog proposal is not a contract** - six for six, and this is the strongest instance in the
  arc. The previous five were wrong in detail; this one was wrong in direction, and had it been
  implemented as written it would have made the surface worse while closing the item.
- **Stage the work so RED is behavioral** - partially honored. Three tests were genuinely RED against
  the component that preceded them (close-on-select, Escape restores focus, Tab-out closes). Six
  were guards that pass against `main` and are backed by named mutations instead, which the plan
  declared in advance so a report claiming "all green" without mutation output would be visibly
  incomplete. That declaration did its job: see the next goal.
- **Re-running the implementer's own proofs is cheap and should stay standard** - honored, and this
  time it **refuted** a claim (Problem 4) after the previous iteration's clean sweep. One iteration of
  "all five reproduced" and one of "the strength claim was wrong" is exactly why the step has to run
  when nothing is expected to be wrong.
- **A finding's stated scope is a starting point, not a census** - applied to the mutation-strength
  claim, and applied while filing: each new item's `file:line` references were re-read against the
  tree rather than copied from the spec, and two of them had moved.
- **When the Go diff is empty, spend the integration lane on a real browser** - **unverifiable this
  pass**, see Findings Triage. Promoted to the default one iteration ago and I cannot confirm it ran.
- **A zero-finding broad review does not close the question** - not tested this slice; review found
  plenty.
- **An invalidation is a continuation** - not applicable, deliberately: this component issues no
  request, mounts no query and holds no cache. Recorded so its absence is not read as a lapse.
- **A cadence test must assert the wiring** - not applicable; there is no cadence on this surface.
- **A new test file is a load change** - not applicable; 14 tests were appended to an existing file
  and no new test file landed.
- **Backlog housekeeping is required scope** - the `/backlog close` is the conductor's step and had
  not run at the time this document was written; the item is still in `docs/backlog/`. Its
  Resolution must state the inversion, which is acceptance criterion 14 of the spec rather than a
  nicety.

New from this iteration:

- **A wide RED is not a strong RED.** Eight failing tests measure how much shared setup a mutation
  stands upstream of, not how well the suite discriminates. Before reporting a mutation as stronger
  than predicted, check where each test failed: at its own assertion, or in a `getByRole` prologue it
  shares with eight others. **Candidate for durable memory**, and a direct sharpening of the existing
  "a mutation proof must leave a test behind" note.
- **A comment that forbids a change is a specification with no test behind it.** The Escape
  listener's open-only lifetime was documented, depended on by another component, and mutable with
  the entire suite green. When you write "do not change X", write the test that fails when X changes,
  in the same commit. This is the "shared primitive missing a behaviour no consumer can see" lesson
  moved from behaviour to prose.
- **Correcting a false claim where you found it is not the same as correcting it at its source.**
  `UserMenu.tsx` now says the Escape listeners overlap; `DialogShell.tsx:361-370` still says they
  cannot. When a probe refutes a claim, grep for every copy of it before closing the finding, and if
  a copy is out of scope, file it rather than leaving two shipped comments in contradiction.
- **When you adopt a library's decision, adopt the library's predicate.** The modifier-click fix uses
  the same test react-router itself uses to decide whether it will handle a click, so the two cannot
  drift. A hand-rolled `if (e.ctrlKey || e.metaKey)` would have been right today and wrong the first
  time the library's definition of a modified event changed.
- **On a slice whose job is to remove something, the risk lives in the small amount you add.** Both
  behaviour findings this iteration came from the new handlers, not from anything the item
  complained about. "This is mostly a deletion" is a reason to review the additions harder, not a
  reason to review less.
- **An item that names a fix instead of a problem propagates the fix's error.** The 2026-06-03 item
  said "add `aria-haspopup="menu"`"; the 2026-06-05 follow-up inherited the attribute as a premise;
  two months later the correct action was to delete it. When triaging or writing an item, state the
  observable problem and let the spec choose the remedy. **Candidate for durable memory.**
- **A spec that reverses a human decision should be adjudicated by a lane briefed not to
  rubber-stamp it.** It cost one lane a few minutes, it returned citations stronger than the spec's
  own, and it is the only reason this document can claim the inversion was checked rather than
  merely argued. Make it the standing rule for any spec that inverts an owner call, not a one-off.
- **A plan that ships a golden copy of the target file acquires a second thing to keep true.** The
  appendix at `plans/2026-08-13-usermenu-menu-roles.md:1083-1197` now contradicts the shipped
  component in the one place review changed the design. Either drop the appendix from future plans or
  mark it "as-designed, superseded by review".

## Files Most Touched

- `web/src/shell/UserMenu.tsx` - the whole slice. 86 lines to 246, and the majority of the delta is
  the comment block at `:17-68`, which carries the inversion argument, the revisit trigger, the
  corrected Escape-overlap analysis, and the not-portalled reasoning. Three of this file's comments
  were false when first written and are now corrected at `:25-28`, `:47-60` and `:78-94`.
- `web/src/shell/UserMenu.test.tsx` - 58 lines to 374, 19 tests. Two sanctioned deletions and nothing
  else removed. The load-bearing additions are the modifier-click regression test (`:127-151`), the
  listener-lifetime guard (`:317-325`), the disclosure sweep with its populated-panel positive
  control (`:333-353`), and the paired Escape tests (`:176-221`), neither of which is meaningful
  without the other.
- `web/src/components/dialog/DialogShell.tsx` - **not touched, and that is now a filed finding.** Its
  `:361-370` comment still asserts the structural guarantee this slice measured to be false.
- `web/src/shell/HoloShell.tsx` - not touched. Its `onLogout` at `:22-25` is why the post-logout
  focus item exists, and its `relative z-10` header comment at `:29-48` is why the panel is not
  portalled.
- `docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md` - the inversion argument, the
  reusable-versus-inapplicable split against `DialogShell`, and the key-by-key keyboard table.
- `docs/superpowers/plans/2026-08-13-usermenu-menu-roles.md` - notable for its "THE SIZE OF THIS
  SLICE, STATED HONESTLY" section, which pre-empted the obvious objection that a five-task plan for
  a 60-line change is padding, and for pinning the jsdom evidence so the engineer did not spend the
  slice re-deriving it.

## Verification

- **Web suite reported green at 973 tests** (959 before), consistent with the tree: `UserMenu.test.tsx`
  contains 19 tests of which 5 predate this slice. **This pass had no shell**, so the count and the
  green state are reported here, not re-run. The exact-file-set check, the `git diff --numstat`
  byte-identity gate on the test file, the `web/dist` revert and the final gate run are the
  conductor's, per the standing rule that subagent claims are verified against the tree rather than
  trusted.
- Every factual claim in this document that could be checked by reading was checked against the
  worktree: the deleted `aria-haspopup` and the conditional `aria-controls` (`UserMenu.tsx:177-181`);
  the panel `id` on `GlassPanel` (`:200`); `closeAndRestoreFocus`, `close`, `onNavItemClick` and
  `onContainerBlur` (`:95-99`, `:103-105`, `:116-119`, `:155-170`); the unchanged listener
  registration site and `[open]` dependency (`:121-147`); the three corrected comments; the 19 tests
  and the two sanctioned deletions; the corrected `system/pointer/mouse.js` citation
  (`UserMenu.test.tsx:234`); the listener-lifetime guard (`:317-325`); the sweep and its positive
  control (`:333-353`); `HoloShell.tsx:22-25` and `:29-49`; the item's own text including the
  owner's priority note (`feature-2026-06-05-usermenu-panel-menu-roles.md:41-47`); its ancestor's
  fix-shaped Summary (`closed/bug-2026-06-03-usermenu-aria-attributes.md:13`); and
  **`DialogShell.tsx:361-370`, which still carries the refuted claim**.
- **Not verified:** the test count and green state, the production build, the mutation outputs, the
  React 18.3.1 batching measurement and the Escape-overlap measurement (both reported by lanes with
  probes I cannot re-run without a shell), whether a real-browser lane ran, and anything requiring
  execution.
- The backlog item was still in `docs/backlog/` when this was written. The `/backlog close` and the
  `git mv` to `docs/backlog/closed/` are the conductor's required scope, and the Resolution must
  record the inversion.
