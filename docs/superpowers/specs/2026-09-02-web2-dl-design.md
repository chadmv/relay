# Lane DL - the dialog and overlay layer: enforce it, document it, and settle the native-element question

Date: 2026-09-02
Branch: `claude/web2-dl-dialogs`
Worktree: `.claude/worktrees/web2-dl`
Gate mode: autonomous. Every question that would have been asked of a human is recorded below as a
Decision with its question, its options and the reason for the answer; the calls a human would most
plausibly make the other way are listed in Escalations.

Frontend only. Zero Go changes. No component's rendered output changes.

## 0. Scope

Four open backlog items on one layer, taken together because three of them are decided by the first:

1. `docs/backlog/idea-2026-08-09-native-dialog-element-reconsideration.md`
2. `docs/backlog/idea-2026-08-09-dialog-shell-sweep-test.md`
3. `docs/backlog/idea-2026-08-09-body-level-portal-inert-marking.md`
4. `docs/backlog/idea-2026-08-12-document-z-index-layering-scale.md`

The deliverable is one new guard test file, one shared tree-walking helper, one documentation block in
`web/src/theme/tokens.css`, four short comment edits, and one backlog item closed with a recorded
reason and a replacement trigger. Nothing under `web/src` changes what any user sees.

## 1. Decision 1 - native `<dialog>` plus `showModal()`

This is stated first because Decisions 3 and 4 are conditioned on it.

**Question.** The item's written trigger has fired. Migrate to the native element now, adopt it
partially, or close the item with a recorded reason and a replacement trigger?

**Options.**

- A. Full migration. `<dialog>` plus `showModal()`, the browser's top layer, `::backdrop` as the
  scrim, the `cancel` event for Escape.
- B. Partial adoption. Native element for the trap and the stacking order; `dialogStack` keeps the
  scroll lock and the Escape policy.
- C. Close with a recorded reason and a sharper replacement trigger.

**Decision: C.** Close the item. Do not migrate, and do not adopt partially.

### 1.1 The evidence, re-measured rather than carried forward

The item names two trigger conditions. Their status is not symmetric and neither is what they unlock.

**Trigger 1 - jsdom implements `HTMLDialogElement` - has NOT fired.** `web/package-lock.json` resolves
`jsdom` to 29.1.1. `web/node_modules` is not installed in this worktree, so the file was read from the
published 29.1.1 package rather than from disk. Its
`lib/jsdom/living/nodes/HTMLDialogElement-impl.js` is, in full, an empty subclass of the base HTML
element implementation - no `showModal`, no `close`, no `open` reflection. jsdom's own tracking issue
for implementing `HTMLDialogElement` is still open. The evidence recorded in `dialogStack.ts`'s header
and in `docs/superpowers/specs/2026-08-09-dialog-hardening.md` section 3 is therefore unchanged, not
merely unrefuted: a component calling `showModal()` still throws in every existing dialog test, and
the only workaround is a hand-rolled polyfill, which makes the tests exercise the polyfill instead of
the platform - so the focus trap, the entire point of the route, becomes the one thing never verified.

**Trigger 2 - a real-browser harness - HAS fired, and unlocks less than it reads.** `web/e2e/` runs
Chromium and WebKit against the production-embedded SPA on every pull request. Three limits, and the
third is not written down anywhere before this spec:

- The harness has **no visual assertions**. Screenshots are uploaded artifacts a human opens, not
  baselines a test compares. A `::backdrop` regression would not fail a build.
- Playwright's `webkit` is a bundled WebKit build and is **not Safari**; both `playwright.config.ts`
  and `web/e2e/README.md` say so explicitly.
- **The harness contains no dialog spec at all.** Its eight files are `auth.spec.ts`,
  `header-nav.spec.ts`, `keyboard.spec.ts`, `layout.spec.ts`, `global.setup.ts`, `fixtures.ts`,
  `nav.ts` and `surfaces.ts`. Searching them for `dialog`, `modal` or `Confirm` returns nothing. Not
  one of them opens a modal.

So trigger 2 delivered a *capability* to verify the platform route and **zero inherited coverage** to
build on. A migration would have to author the entire browser dialog suite from scratch before it
could claim parity, while deleting assertions that run in the unit lane on every commit.

### 1.2 The item's four questions, answered against the tree

**Q1: would the migration keep the two-element rendered depth, or does `::backdrop` change the
backdrop handle?** It changes it. `DialogShell` renders exactly two elements - a scrim div and the
panel - and the panel's header records that depth as non-negotiable because three test files take the
backdrop as `getByRole('dialog').parentElement`: `TokenRevealDialog.test.tsx` (a stray backdrop click
must not destroy the only copy of a credential), `DialogShell.test.tsx`, and
`WorkspacesPanel.test.tsx`. Under `<dialog>` the visible dark area is a `::backdrop` pseudo-element on
the dialog itself, so a press there targets the dialog element and `.parentElement` resolves to the
portal layer. Every one of those three tests would keep passing while testing something else - the
"a test is green because of the bug" shape. Keeping an outer wrapper div for centering does not rescue
this: `showModal()` promotes the dialog to the top layer regardless of its DOM parent, so the wrapper
would no longer be what the user presses. And `::backdrop` is unassertable in both lanes - jsdom has no
CSSOM for pseudo-elements, and the browser lane has no visual baselines.

**Q2: does the top layer make `dialogStack`'s manual `inert`/`aria-hidden` marking and its stacking
redundant, or only partly?** Only partly, and the redundant part is exactly where jsdom is blind. In a
browser, a modal dialog in the top layer does make everything outside it inert, and multiple
`showModal()` calls stack in call order, which matches the module's LIFO. But none of that happens in
jsdom, so `dialogStack.test.ts` and `DialogShell.test.tsx`'s attribute assertions would have to be
**deleted rather than rewritten** - there would be nothing left to assert in the lane that gates every
commit. Meanwhile the parts that are app policy rather than platform behaviour survive untouched:
which dialog owns Escape, whether Escape is suppressed at all, and where focus parks when a dialog in
the middle of the stack closes.

**Q3: does the platform's `cancel` event support `dismissOnEscape={false}` cleanly?** In principle it
is cleaner than the current interception - `preventDefault()` on `cancel` is the specified mechanism.
Two problems. It is entirely unobservable in jsdom, so the one dialog whose suppression matters
(`TokenRevealDialog`, where Escape must not discard the only copy of a credential) would lose its
regression gate. And HTML's close-watcher machinery is deliberately designed so a page cannot block
the close request indefinitely, which means "Escape never closes this" is a UA-discretion question
rather than a guarantee. That is a claim this lane has **not** measured, and the honest reading is
that it would have to be measured in both engines before the migration could be scoped - which is
itself an argument against migrating on a lane that cannot assert it.

**Q4: is the scroll lock still ours to own?** Yes, entirely. `showModal()` does not lock document
scroll. `dialogStack`'s `previousBodyOverflow` save on the empty-to-non-empty transition, and its rule
that a per-dialog save/restore pair would permanently strand the page unscrollable, survive any
version of this migration untouched.

### 1.3 Why C rather than B

The item offers partial adoption as "a legitimate outcome". Q4's answer is stronger than that: partial
adoption is the **only** possible shape, because the scroll lock cannot move to the platform. But B
still pays Q1's and Q2's full cost - the backdrop handle still changes and the attribute assertions
still have to go - while buying only the trap and the stacking order, in a lane that cannot see either.
B is the worst of the three: all of A's test destruction, less than all of A's benefit.

### 1.4 What the closure records, and the replacement trigger

The old triggers are now useless: one has fired and the other has been re-measured as unchanged, so a
future reader would re-litigate rather than re-evaluate. The closed item records:

- The re-run of all four questions with the answers above.
- That the deciding fact is not jsdom alone but the **pair**: jsdom still cannot run the route, and the
  browser lane that could has no dialog spec to inherit.
- The replacement trigger, which is a conjunction, not a disjunction:
  1. jsdom's `HTMLDialogElementImpl` stops being an empty subclass, **and**
  2. `web/e2e/` carries a dialog spec that opens a modal and asserts the trap, focus restoration and
     Escape scoping in both engines.
  Either alone is insufficient, and the item says why for each.
- That any migration is partial by construction, because the scroll lock stays in `dialogStack`.

`dialogStack.ts`'s "WHY NOT native `<dialog>`" header note is currently stale in a way that matters:
it points at the e2e-harness backlog item as a future trigger, and that item has closed. The note is
rewritten to state the constraint (the impl is an empty subclass, so the trap would be verified only
against a polyfill) and to point at the closed reconsideration item for the full argument. No dates,
no history, per the repository's comment policy.

### 1.5 What this decision does to the rest of the lane

- The dialog scrim stays a painted div at stacking order 50, so it stays on the layering scale
  (Decision 4). The layering block must nonetheless say the mechanism is today's mechanism and name the
  alternative, so a future migration edits one entry rather than invalidating the document.
- `dialogStack` keeps owning the `inert` and `aria-hidden` marking of background content, so the
  body-level portal constraint (Decision 3) is live rather than about to be obsoleted.

## 2. Decision 2 - the sweep mechanism, settled once for this and for the field-error audit

**Question.** A Vitest file reading the source tree, or an ESLint rule?

**Options.**

- A. A Vitest guard test that walks `web/src` with `node:fs` and asserts over file contents.
- B. ESLint with a custom rule (or `no-restricted-syntax` selectors) plus a flat config and a CI step.
- C. Both - ESLint for the AST-shaped assertions, Vitest for the class-string one.

**Decision: A, for this sweep and for `idea-2026-08-13-field-error-wiring-audit`.**

**Reason.** The item calls ESLint "more idiomatic but adds config surface". That framing assumes
ESLint exists. **It does not exist in this repository at all**: there is no config file under `web/`,
no `eslint` entry in `web/package.json`, and zero occurrences of the string `eslint` in
`web/package-lock.json`. The `eslint-disable-next-line react-hooks/exhaustive-deps` comments in
`DialogShell.tsx` and `UserMenu.tsx` are inert text that no tool reads - which is worth stating,
because they make the repository look linted and they are the most likely reason the item framed
option B as incremental. Option B is a from-zero adoption: `eslint`, `typescript-eslint`, a local
plugin package (stock rules cannot express "this attribute may appear in exactly one file", and cannot
express a class-string match at all), a flat config, an npm script, and a new job in
`.github/workflows/web-ci.yml`. That is a large surface for two rules.

Against that, option A has a working, reviewed, in-tree exemplar:
`web/src/components/holo/responsive.guard.test.ts`. It already carries every piece the sweep needs and
every piece it needs is there because something went wrong without it - a recursive directory walk
anchored with an explicit `node:url` URL (jsdom shadows the global `URL` and cannot resolve a Windows
drive letter against a `file://` base), a comment stripper that handles both `//` lines and block
comments including the JSX form (a prose comment near a matched tag broke an earlier version), a POSIX
path normalizer so a received-array diff reads the same on both platforms, and two positive controls -
one that the walker found a tree at all, one that the rule is satisfied by compliance rather than by
deletion. Reusing that is cheaper and better-tested than authoring an ESLint plugin.

Option C is rejected outright: two enforcement mechanisms for one family of rule is the accumulation
this lane exists to prevent, one level up.

**Consequence for the field-error audit item.** That item's part 3 asks the same question and defers to
this one. It is answered here: a Vitest guard test, reusing the shared walker this lane extracts. This
spec does **not** implement that sweep. It only settles the mechanism and leaves the walker in a shape
that sweep can import.

### 2.1 The shared walker

`web/src/test/sourceTree.ts`, a new module beside the existing helpers in that directory. It exports
the walk, the comment stripper and the path normalizer, all currently private to
`responsive.guard.test.ts`, which is re-pointed at it.

Two behaviour notes that must be measured rather than assumed during implementation:

- The existing walker collects `.tsx` files that are not `.test.tsx`, which today **includes**
  `web/src/test/renderWithQuery.tsx` - a test harness component, not shipped UI. The shared walker
  excludes every path under `web/src/test/` and states why. That shrinks the existing file set by one,
  so `responsive.guard.test.ts`'s "more than 50 files" control still holds and its two rule assertions
  are unaffected; both must be re-run and confirmed green.
- The shared walker takes the file extensions as a parameter, because the document-listener assertion
  below has to scan `.ts` as well as `.tsx`.

**The extraction is a behaviour-preserving refactor and is gated as one:** the assertion bodies inside
`responsive.guard.test.ts` do not change - only the import that supplies the walker - and both of its
tests stay green.

## 3. Decision 3 - the body-level portal constraint

**Question.** Route 1 (document the constraint in `dialogStack.ts`'s header) or route 2 (enforce it
with a `MutationObserver` on `document.body`)?

**Verification of the item's premise first.** The item claims no body-level portal producer exists
today. Searched by shape across `web/src` for `createPortal`, `document.body.appendChild`,
`document.body.append` and `document.body.insertBefore`: **six hits.** Two are in shipped source and
both are inside `web/src/components/dialog/` - `DialogShell.tsx`'s `createPortal` into the dialog
layer, and `dialogStack.ts`'s `apply()` appending that layer to the body. Three are in test files
(`dialogStack.test.ts`, `enrollmentTokenSecrecy.test.tsx`) and one is a comment in `UsersTab.test.tsx`.
**The claim is confirmed. Nothing refuted.**

**Decision: route 1, plus a sweep assertion that makes route 1 fire.**

**Reason.** Route 2 is an always-on background watcher for a producer that does not exist. That is the
same shape as two mechanisms already considered and rejected in writing on this exact component: the
`focusin` sentinel rejected in `DialogShell`'s header, and the `MutationObserver` named and ruled out
of scope in its focus-restore comment. Adding one now would contradict both, and it could only be
tested against a synthetic producer, which proves the mechanism runs rather than that it fixes
anything.

But route 1 as the item describes it - a header comment - is the weaker half of the very dynamic this
lane is about: the sweep item's own Context section observes that the app reached three hand-rolled
dialogs because each author had no reason to open the others' files. A constraint recorded only in
`dialogStack.ts`'s header is a constraint the toast author does not read.

So route 1 gets a second half at near-zero marginal cost, because the sweep infrastructure is being
built anyway: **assertion A5 below** fails when `createPortal` or a `document.body` append appears in
shipped source outside `web/src/components/dialog/`. The header note states the constraint and the
sweep is what delivers it to the person who needs it, on the commit that needs it.

That also gives route 2 a real trigger for the first time: the sweep going red *is* the trigger, and
the author it stops is, as the item wanted, the one who knows whether their layer belongs above or
below a modal.

## 4. Decision 4 - where the layering scale is documented

**Verification of the item's table first.** It tabulates four values. **The tree now has five.** The
fifth is the collapsed navigation panel in `HoloShell.tsx`'s `navPanelClass`, which carries a
below-breakpoint stacking order of 50, added by the header-nav work after the item was filed. It is
not a defect - it sits inside the header's stacking context for the same reason the profile dropdown
does, and it picked the same value for the same reason. It is the item's own prediction coming true
while the item sat open: the next overlay chose a number by reading whichever neighbour it happened to
open first.

**Question.** A `layers.ts` constant set, a comment block in `web/src/theme/tokens.css`, or a section
in `web/CLAUDE.md`?

**Options and reasons.**

- **A. `web/src/layers.ts` exporting the values, consumed by components. Rejected, on a measured
  project-specific ground.** Tailwind v4 scans source files on disk for literal class-shaped
  substrings and never reads the emitted bundle. A component that builds its class from an imported
  constant emits no CSS - `web/CLAUDE.md` records this and records that esbuild folding the constant
  back to a literal does not rescue it. A consumed constants module would silently drop the stacking
  rules from the production stylesheet. This is the strongest single argument in this decision and the
  item could not have known it.
- **A'. `layers.ts` as documentation only, imported by nothing. Rejected, worse than A.** An unimported
  module is unmaintained by construction, and its string values would themselves be class-shaped
  literals in a scanned file, keeping rules alive independent of the component that is supposed to own
  them - exactly the hazard the last web batch measured when comments spelling utility names kept a
  producer check green with the classes deleted.
- **B. A comment block in `web/src/theme/tokens.css`, next to the popover surface token. Chosen.**
- **C. A section in `web/CLAUDE.md`. Rejected.** That file is agent-facing guidance. The item's
  acceptance criterion is that a human adding an overlay reads exactly one file, and a human who
  starts in `GlassPanel.tsx` or `DialogShell.tsx` will follow a code pointer, not a pointer into a
  contributor guide. `web/CLAUDE.md` is also under the Vite root and therefore scanned, so it carries
  A''s hazard too.

**Reason for B.** Three properties, and the third is decisive:

1. It is already the file the same 2026-08-12 fix touched for the same reason - the opaque popover
   surface token exists because a glass panel sets no background colour at all. The two halves of "how
   do I build an overlay in this app" then sit together.
2. CSS is not TypeScript. Nothing can import it, so it cannot rot into a constant that disagrees with
   the call sites, and it cannot become dead code.
3. **A CSS comment can state the scale in CSS property terms - the property name and the numeric
   value - which is not class-shaped and emits nothing.** Every other candidate location would have to
   either spell utility names, keeping those rules alive independent of their real owner, or describe
   them so indirectly as to be useless. Option B is the only one that can say the thing precisely and
   emit nothing. This constraint is binding on the implementation: the block names CSS properties and
   values, never utility class names.

## 5. Design - the guard test

One new file: `web/src/components/dialog/dialogShellIsSole.guard.test.ts`. The `.guard.test.ts` suffix
matches the existing sweep so the family is greppable. It imports the walker from
`web/src/test/sourceTree.ts`.

Every assertion strips comments before scanning and excludes test files, `web/src/test/`, and the
allowlisted owner. Both exclusions are load-bearing and for different reasons, and the item's stated
reasoning covers only one of the two:

- `role="dialog"` genuinely does not appear in test files, which use `getByRole('dialog')` - the item
  is right about that.
- `aria-modal` **is** spelled literally in four test files, which assert
  `toHaveAttribute('aria-modal', 'true')`. The test-file exclusion is what makes assertion A2 work at
  all, and the item's justification does not reach it.
- `role="dialog"` and `aria-modal` each also appear a **second** time inside `DialogShell.tsx` itself,
  in its own header comment. So even within the allowlisted file, comment stripping is required before
  any count-shaped formulation would be correct.

Every assertion carries a failure message naming the remedy, not just the violation.

### A1 - modal role

Shipped `.tsx` sources, comments stripped: a `role` attribute whose value is the dialog role appears in
`DialogShell.tsx` only. The probe accepts the string form and the braced form so `role={'dialog'}` is
caught. **Known gap, stated rather than papered over:** a role assembled from a variable is not caught.

Message: compose `DialogShell` rather than declaring modal semantics on a div; the shell owns the
portal, the trap, the scoped Escape and registration in the stack, and a hand-rolled modal gets none of
them.

Positive control C1: the probe matches `DialogShell.tsx`.

### A2 - `aria-modal`

Same set, same treatment, same message. Positive control C2: the probe matches `DialogShell.tsx`.

### A3 - the scrim

The scrim class string must be found **without being spelled**, because a class-shaped literal in a
test file emits CSS under Tailwind v4's prose scan. The probe is therefore **derived from
`DialogShell.tsx`'s own source at test time**: match the `SCRIM` assignment - anchored on the
identifier, which is not class-shaped - and take its string value. Two sub-assertions:

- **Exact.** No other shipped source contains that literal.
- **Near miss.** No other shipped source has a line on which all three of the extracted string's
  distinctive tokens co-occur (the positioning token, the inset token and the stacking-order token,
  taken from the extracted value by splitting it). This catches a hand-rolled scrim that reorders or
  adds classes, which the exact probe would miss.

Nothing is spelled anywhere in the test file, and the probe follows a change to the scrim value
automatically.

Positive control C3: the extraction yields at least five tokens, and both sub-probes match
`DialogShell.tsx`.

**Two known non-violations to confirm during implementation rather than assume.** `SessionsTab.tsx` and
`ResetPasswordDialog.tsx` each describe the scrim in a prose comment using a two-token fragment.
Comment stripping removes both, and the near-miss probe needs three tokens on one line, so neither
should register. Verify both, and if either does register, fix the probe rather than the comment.

**A note that belongs in this spec and NOT in a code comment:** `DialogShell.test.tsx` spells the full
scrim string in an assertion. Test files are outside the walk, so it is not a false positive - but it
does mean those Tailwind rules would survive deletion of `SCRIM` from the component. That is a
pre-existing observation about the emitted-CSS control, not a change this lane makes, and the
repository's comment policy keeps censuses of other files out of comments.

### A4 - document-level keydown listeners

**This is where the item is stale, and it matters.** The item says to allowlist `DialogShell.tsx` and
`UserMenu.tsx` "so a *third* keydown listener trips the test". A third already exists and is
legitimate: `HoloShell.tsx`'s collapsed navigation panel adds a document keydown listener for Escape
while the panel is open, added by the 2026-09-02 header-nav work. Implemented as written, the test
would be red on arrival against correct code.

Reframed so it does not decay the same way again. The assertion is not "there are at most N listeners".
It is: **every document-level keydown listener in shipped source appears in an allowlist that records,
per entry, why that surface is not a modal.** Adding an entry requires writing the reason, which is
the actual control. Today's three entries and their reasons:

| file | reason it is not a modal |
|---|---|
| `components/dialog/DialogShell.tsx` | the modal shell itself; Escape must fire when focus has left the panel, which a panel-scoped handler cannot see |
| `shell/UserMenu.tsx` | a disclosure, not a modal - no scrim, no scroll lock, no inert background, no trap, and Tab out is a dismiss route |
| `shell/HoloShell.tsx` | the collapsed navigation panel, the sibling disclosure of the above, sharing its handler set |

The probe matches a document-level `addEventListener` for the keydown event across shipped `.tsx` and
`.ts` sources, with flexible whitespace and both quote styles. It is anchored on `document.` so an
`AbortSignal` listener is not a false positive - `lib/api.ts` and the SSE test helper each register an
abort listener today and neither must register.

**Widening to `.ts` is a fence, not a fix:** no shipped `.ts` file adds a document listener today. A
future hook that did would otherwise be invisible to a `.tsx`-only walk.

Positive control C4, and it is the strongest one in the file: **every allowlisted file is matched by
the probe.** If the regex drifts, an allowlist entry stops matching and the test goes red, which a
count-shaped control could never detect.

Message: for a modal, compose `DialogShell`, which already owns a scoped document Escape. For a
non-modal disclosure, follow the two existing ones and add an allowlist entry stating the reason.

**Considered and deliberately not asserted:** both disclosures also register a document `mousedown`
listener. A fourth of those is not the hazard this item is about, and asserting it would make the
allowlist grow for a reason unrelated to modal semantics. Recorded here so the next reader knows it was
weighed rather than missed.

### A5 - body-level portals (Decision 3's enforcement half)

Shipped sources, comments stripped: `createPortal` and the enumerated `document.body` insertion methods
appear only under `web/src/components/dialog/`. The enumerated shape is `createPortal`,
`document.body.appendChild`, `document.body.append`, `document.body.prepend` and
`document.body.insertBefore`. **Known gap:** a body reference obtained some other way is not caught.

Message: a node appended to the body after a dialog opens is never marked inert or aria-hidden, because
the stack's marking pass runs only on register and unregister - so it stays keyboard-reachable from
behind a modal and is announced as though the modal were not there, and it paints above the scrim as a
later sibling. Portal into a container the component owns, or add the entry and decide, at the same
time, whether the new layer sits above or below a modal.

Positive control C5: the probe matches both files inside the dialog directory.

### A6 - the layering document is complete

Shipped sources, comments stripped: every file containing a stacking-order utility is named in the
layering block in `web/src/theme/tokens.css`.

Both sides are derived, so nothing class-shaped is spelled in the test file: the tree side builds its
pattern from concatenated fragments, and the document side parses the file names out of the block. The
block's required format is specified in section 6.

Positive control C6: the parsed set is non-empty, the scanned set is non-empty, and both contain
`DialogShell.tsx` and `HoloShell.tsx`.

This is the one assertion a human might reasonably cut; see Escalations.

### C0 - the shared control

Before any assertion: the walker returned more than 50 shipped source files, the returned list contains
`components/dialog/DialogShell.tsx` and `shell/HoloShell.tsx`, and it contains no path under
`web/src/test/` and no file ending in `.test.tsx`. A silent zero, or a walk that quietly started
including test files, would make every absence assertion in the file pass forever.

## 6. Design - the layering block in `web/src/theme/tokens.css`

A comment block adjacent to the popover surface token. It states, in CSS property terms only:

**The scale.** Five entries. Each names the surface, the stacking-order value, the owning file and the
symbol or element that carries it, and **which stacking context it lives in**:

| surface | value | owner | context |
|---|---|---|---|
| dialog scrim | 50 | `DialogShell.tsx`, `SCRIM` | root (the layer is appended to the body) |
| application header | 10 | `HoloShell.tsx`, the `header` element | root |
| page content | 0 | `HoloShell.tsx`, the `main` element | root |
| profile dropdown | 50 | `UserMenu.tsx`, the panel | inside the header |
| collapsed navigation panel | 50, below the breakpoint | `HoloShell.tsx`, `navPanelClass` | inside the header |

Files and symbols, never line numbers, per `web/CLAUDE.md`.

**The block's format is load-bearing, because assertion A6 parses it.** One scale entry per line, and
each entry names its owning file as a path relative to `web/src`. A6's parser takes the file names off
those lines and ignores the rest of the line, which is prose for the reader. If the block is ever
reformatted so the parser sees nothing, control C6 - which requires the parsed set to be non-empty and
to contain two named files - fails rather than letting A6 pass vacuously.

**The rules.** Four, of which the first two are the item's and the last two are additions this lane's
verification produced:

1. An ancestor with a backdrop filter establishes a stacking context, so a stacking order declared
   inside it cannot compete with anything outside it. Every glass surface carries one, and so does the
   header. Evidence pointer: the comment block above the header element in `HoloShell.tsx` carries the
   275-point hit-test measurement, cited by phrase.
2. The main element carries a relative position and a stacking order of 0 deliberately, so page-level
   stacking orders are contained and can never climb over the header. A new page-level overlay stays
   inside that context rather than raising itself past it. Same evidence pointer.
3. **A value is comparable only to another value in the same stacking context.** Three of the five
   entries read 50 and only one of them competes with the others - which is precisely what a reader
   cannot infer from the numbers, and is the strongest reason this block exists.
4. **Paint order is only half of it.** A new overlay must also decide whether it is reachable and
   announced while a modal is open. Pointer to `dialogStack.ts`'s header note. This is the cross-link
   between the paint-order and inert halves that the layering item asked for.

**The impermanence note.** The dialog scrim entry says it describes today's mechanism - a painted
element on this scale - and names the alternative (the browser's top layer, which would take dialogs
off the scale entirely), with a pointer to the closed reconsideration item and its replacement trigger.
Written so that adopting the native route later edits one entry rather than invalidating the document.

**Cross-links.** One short comment in `GlassPanel.tsx` (this surface establishes a stacking context;
the scale and its rules are in the layering block in `tokens.css`) and one in `DialogShell.tsx` beside
`SCRIM`. Both name the file and the block by phrase.

## 7. Design - the two header notes

**`dialogStack.ts`, the native-element note.** Rewritten to state the constraint and the trigger
without the stale pointer at a closed backlog item, and pointing at the closed reconsideration item for
the argument. No dates, no history.

**`dialogStack.ts`, the portal note.** New, beside the existing note about the background marking:
`apply()` runs only on register and unregister, so a node appended to the body while a dialog is open
is neither marked nor covered by the scrim; a body-level portal author must decide whether their layer
sits above or below a modal, and the guard test is what will stop them to ask. Cites the guard test by
name, which is the form the repository's comment policy allows.

## 8. What the tree refuted

Per the standing rule that a backlog proposal is not a contract, every bullet across the four items was
checked. Six refutations and two confirmations.

1. **The sweep item's keydown allowlist is stale and would ship red.** It allowlists two files "so a
   third keydown listener trips the test". A legitimate third exists in `HoloShell.tsx`. Section 5, A4.
2. **The layering item's table is one row short.** Four entries tabulated, five in the tree; the fifth
   is the collapsed navigation panel. Section 4.
3. **The native-dialog item's second trigger is weaker than it reads.** The browser lane landed and
   contains no dialog spec - none of its eight files opens a modal - so it supplies a capability and
   zero inherited coverage. Section 1.1.
4. **The native-dialog item understates the constraint on partial adoption.** Q4's answer makes partial
   adoption the only possible shape rather than one legitimate outcome among several, because
   `showModal()` does not lock document scroll. Section 1.2.
5. **The sweep item's Vitest-versus-ESLint framing assumes ESLint exists.** It does not: no config
   under `web/`, no dependency, zero lockfile occurrences. The disable comments in two source files are
   inert text. Section 2.
6. **The sweep item's test-file reasoning covers one of its two attribute assertions.** `aria-modal` is
   spelled literally in four test files; the exclusion, not the item's `getByRole` argument, is what
   makes A2 work. And both attributes appear a second time inside `DialogShell.tsx`'s own header
   comment. Section 5.
7. **Confirmed, nothing refuted:** the portal item's claim that no body-level portal producer exists.
   Six hits by shape, two in shipped source, both inside the dialog module. Section 3.
8. **Confirmed:** the layering item's claim that the two identical values mean different things, and
   its note that the profile dropdown is not a defect. Both hold, and the fifth entry makes the point
   three times over rather than twice.

## 9. Sequencing

1. **Decision 1 recorded.** Close `idea-2026-08-09-native-dialog-element-reconsideration` through
   `/backlog close`, with the four answers and the replacement trigger. Rewrite `dialogStack.ts`'s
   native-element note. First, because it decides whether the scrim is on the scale.
2. **The shared walker.** Extract `web/src/test/sourceTree.ts` and re-point
   `responsive.guard.test.ts`, with its assertion bodies unchanged and both its tests green.
3. **The guard test.** A1 through A5 plus C0 through C5, each proven RED.
4. **The layering block, the cross-links, and the portal note.** Then A6 and C6, proven RED.
5. Close the remaining three backlog items with `/backlog close`.

## 10. Acceptance criteria

**Tests, by name.**

- `web/src/components/dialog/dialogShellIsSole.guard.test.ts` - six tests (A1 modal role, A2
  `aria-modal`, A3 scrim, A4 document keydown allowlist, A5 body-level portals, A6 layering document
  completeness) plus the shared control C0.
- `web/src/components/holo/responsive.guard.test.ts` - both existing tests green, **assertion bodies
  byte-identical**; only the import supplying the walker changes. The diff on this file is inspected
  and must show no change inside any `test(...)` body.

**Positive controls, each of which must be observed failing when deliberately broken.** C0 (walker
reached a tree of the expected size and shape), C1 and C2 (each attribute probe matches the shell), C3
(the scrim extraction yields at least five tokens and both sub-probes match the shell), C4 (every
allowlisted file is matched by the keydown probe), C5 (the portal probe matches both dialog-module
files), C6 (both parsed sets non-empty and both contain the two expected files). Break each by pointing
the walker at a directory that does not exist, or by corrupting the probe, and record the failure.

**Mutations, each added one at a time to a real file, red observed, then reverted.**

| mutation | kills |
|---|---|
| add the modal role attribute to a component outside the dialog module | A1 |
| add `aria-modal` to that component | A2 |
| copy the scrim class string onto a component outside the dialog module | A3 exact |
| copy it with the tokens reordered and one added | A3 near-miss |
| add a document keydown listener to a fourth file | A4 |
| remove one file from the A4 allowlist | C4 |
| add a `createPortal` call outside the dialog module | A5 |
| add a stacking-order utility to a component not named in the layering block | A6 |

Each mutation's failure message is recorded in the commit message, and each is checked to be the
message for the assertion it was meant to kill - a mutation that reddens a different assertion has not
pinned the guard it was named for.

**Two procedural requirements on the RED proof, both from prior sessions' failures.**

- **The guard file is committed before any mutation is applied.** Reverting a mutation with
  `git checkout -- <file>` while the guard is uncommitted discards the guard under test. With the
  guard committed, restoring the mutated consumer that way is safe.
- **After each revert, re-run the suite and confirm green** before applying the next mutation, so a
  silently-unapplied mutation cannot report as "survived".

**Emitted-CSS control on the `tokens.css` edit.** Build before and after the layering block is added
and confirm the emitted stylesheet gains no rules. This is the concrete check that the block names CSS
properties rather than utility classes; if any rule appears, the block spells a class and must be
reworded. The A6 mutation adds a stacking-order value that the app already emits, so it leaves the
built stylesheet unchanged and its revert leaves no residue.

**Gates.**

- `cd web && npm test` green, with a net increase in test count.
- `cd web && npx tsc -b --force` clean.
- `cd web && npm run build` clean.
- `git checkout -- web/dist/` before the change set is assembled.
- **`make test-e2e` is NOT required, and here is the reason rather than an omission.** No component's
  rendered output changes: the source edits are one CSS comment block, four code comments, one new test
  file and one new test-helper module. Nothing that renders moves. **The condition that flips this:** if
  any RED proof leaves a consumer edit behind, or if any of the four decisions is taken the other way
  and a dialog behaviour moves, the e2e lane runs.

**Item closure.** All four backlog items closed with `/backlog close <fragment>`, which performs the
`git mv` into `docs/backlog/closed/` and stamps the frontmatter. A status flip in place is not a close.

**Scope.** Nothing outside `web/src/`, `web/src/theme/`, `docs/backlog/` and this spec changes. Zero Go
changes.

## 11. Escalations - the calls a human might make the other way

1. **Decision 1, and this is the one that matters.** A human could reasonably fund the migration
   anyway, on the design ground that the platform supplies four mechanisms this app hand-rolls, and
   accept trading unit-lane assertions for browser-lane ones. The counter-argument this spec makes is
   evidential rather than aesthetic - jsdom still cannot run it, and the browser lane has no dialog
   spec to inherit - so a human who is willing to fund a browser dialog suite first is not disagreeing
   with the evidence, only with the order. **If that call is made, the right shape is: file the e2e
   dialog spec as its own item, land it, and only then re-open the reconsideration.** That is precisely
   the replacement trigger this spec writes, so the disagreement reduces to whether to fund the trigger
   now instead of waiting for it.
2. **Whether to file the e2e dialog spec as a backlog item now.** This lane proposes it and does not
   file it. Leaving it only as a trigger condition makes it unfalsifiable until someone happens to want
   it - which is the failure mode of conditioning a decision on work that is not a findable item. A
   human may want it filed today. Proposed, not filed; needs acceptance.
3. **Assertion A6.** The layering item is explicit that it is documentation-shaped and not a refactor,
   and a test pinning a comment block's completeness is arguably beyond that. The case for keeping it is
   that a document with nothing keeping it honest is the same defect as an invariant with nothing
   enforcing it, one level up - and that is the defect this whole lane exists to fix. The case for
   cutting it is that it makes a comment block load-bearing on a test, which raises the cost of every
   future overlay slightly. Cheap to cut; cut it wholesale rather than weakening it.
4. **Extracting the shared walker.** A human might prefer the guard test to carry its own copy and
   leave `responsive.guard.test.ts` untouched, keeping this lane's diff strictly additive. The cost is a
   second copy of a walker whose every feature exists because something broke without it, and a third
   copy arriving with the field-error sweep. The benefit is a smaller blast radius.
5. **Decision 3.** A human who knows a toast or notification layer is coming might want the
   `MutationObserver` built now. This spec's position is that route 2 belongs to the author of that
   layer, and that if the layer is planned it should be a filed item so the condition is findable.
6. **Decision 4's location.** A human might prefer the scale to live in `web/CLAUDE.md` on the grounds
   that agents are the more frequent readers. The counter is the item's own acceptance criterion about
   one file for a human overlay author, plus the emitted-CSS hazard, which applies there too.

## 12. Risks and known gaps

- **Every assertion in section 5 is a text scan and is only as good as its pattern.** Three gaps are
  stated at their assertions rather than left implied: a role assembled from a variable (A1), a body
  reference obtained by other means (A5), and a stacking order set through an inline style rather than
  a utility (A6). A scan cannot close these; the alternative that could is an AST rule, which
  Decision 2 rejects on cost. The honest framing is that this raises the cost of a silent violation, not
  that it makes one impossible.
- **The A4 allowlist will keep growing** as the app adds disclosures, and a long allowlist is a weak
  signal. The reason column is the mitigation: an entry cannot be added without stating why the surface
  is not a modal. If the list reaches a length where the reasons stop being read, the rule should be
  revisited rather than extended.
- **The close-watcher behaviour named in Q3 is unmeasured.** It is recorded as a risk on the migration
  route, not as evidence for the decision - the decision rests on jsdom and on the empty browser lane,
  both of which were measured. Anyone re-opening the item must measure it in both engines.
- **The comment stripper is shared machinery now**, so a bug in it fails two guard files at once. That
  is the point - one stripper with a test-earned history beats three - but it means a change to it is a
  change to both guards and both must be re-run.
- **`web/node_modules` is not installed in this worktree**, so the jsdom evidence was read from the
  published package at the version the lockfile resolves rather than from disk. The two can only differ
  if the installed tree has been tampered with. If a reviewer wants the on-disk read, it is one command
  in a worktree that has the install.

## 13. Follow-ups proposed, not filed

Proposals only. The human gives final acceptance on each.

- **An e2e dialog spec**: open a modal in both engines and assert the trap, focus restoration and
  Escape scoping. It is the second half of Decision 1's replacement trigger, and filing it makes that
  trigger findable instead of latent. Highest value of the three.
- **The field-error wiring audit's mechanism is now settled** by Decision 2 and its item should record
  that, so whoever picks it up does not re-open the question. A note on the existing item, not a new
  item.
- **The `MutationObserver` enforcement (route 2)** now has a trigger - assertion A5 going red - and the
  existing portal item can record that trigger rather than being closed with the question still open.
  Decide at closure time whether A5 makes the item closable or whether it should stay open carrying
  route 2.
