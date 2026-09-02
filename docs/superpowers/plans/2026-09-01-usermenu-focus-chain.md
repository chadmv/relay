# UserMenu Focus Chain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record the accepted dead-space focus gap in `UserMenu` and pin it with a test, then make both auth screens claim focus on arrival so a sign-out, a 401 teardown or a direct visit no longer lands a keyboard user on `<body>`.

**Architecture:** Two commits on one branch, in a fixed order. Commit 1 touches `web/src/shell/UserMenu.tsx` with comment lines only plus one new test in `UserMenu.test.tsx`. Commit 2 adds the `autoFocus` attribute to the first control of `LoginScreen` and `RegisterScreen`, adds one unit test per screen, adds a new route-level test file covering the three arrival paths, and adds one assertion to an existing Playwright test. No backend change, no new endpoint, no new dependency, no new runtime mechanism.

**Tech Stack:** React 18.3.1, react-router-dom 7, vitest 2.1.9 + jsdom 29.1.1 + @testing-library/react 16.3.2 + @testing-library/user-event 14.6.1 + msw 2, Playwright 1.62.1, TypeScript 5.7.

**Spec:** `docs/superpowers/specs/2026-09-01-usermenu-focus-chain-design.md` (approved, committed on this branch).

---

## Slice independence declaration

**This lane is frontend-only. There is no backend slice.** Nothing in either commit touches Go,
SQL, protobuf or any generated file. Do not run `make generate`, `make test` (Go), `make
test-integration` or `make test-race` for this work, and do not claim any Go lane.

Within the lane the two commits are **sequential, not parallel**: commit 2's correctness argument
restates commit 1's close-then-hand-off ordering as a regression guard, and the spec fixes the
order. One engineer, one session, one PR. There is no work here that a second agent could take in
parallel.

This is a single-session plan. It does **not** need `/backlog phases`; there are no `## Stage N`
units.

**The engineer must NOT run `/backlog close`.** Closing
`idea-2026-08-13-usermenu-outside-mousedown-drops-focus` and
`idea-2026-08-13-post-logout-focus-lands-on-body` is the conductor's step, after the PR. Name the
two items in the commit messages (the text is given below) and stop there.

---

## Spec contradiction check, and what it left for the implementer

The spec was read once for contradiction against the tree at HEAD in this worktree before this plan
was written. It does not contradict itself, and every path, line reference and symbol it names
exists as described. Two things needed correcting or discharging, and both are folded into the tasks
below.

**1. The spec's one open item is now closed.** Its "Could not verify" note says `web/node_modules`
was absent, so `@testing-library/user-event`'s `focusElement` and jsdom's focus handling were quoted
from existing test comments rather than read. `node_modules` **is** installed in this worktree. All
of it was read at source; the findings are in the next section. The engineer does not need to
re-derive any of it, and must not copy the spec's unverified phrasing into a comment.

**2. Mutation proof 3 cannot be applied as the spec words it, and this plan deviates.** The spec
asks for `RegisterScreen`'s attribute to be replaced with "a `ref` plus `useEffect(() =>
ref.current?.focus(), [])`". `Input` (`web/src/components/Input.tsx:3-14`) is a plain function
component, not a `forwardRef` one:

```tsx
export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={...} />
}
```

A `ref` prop on it never reaches the `<input>` (React 18 warns "Function components cannot be given
refs"), and TypeScript rejects the prop outright. That mutant would stay RED for a reason that has
nothing to do with commit timing, which is exactly the confound this proof exists to exclude - a
mutation that is behaviourally inert for the wrong reason reports the answer you wanted.
**Deviation:** the mutant looks the node up by id inside the same `[]`-deps effect
(`document.getElementById('name')`), which attaches to a real DOM node whenever one exists and so
isolates the timing claim and nothing else. Exact code in Task 9.

Everything else in the spec's verification table was re-confirmed at HEAD: `onDown` and its
rationale block at `UserMenu.tsx:123-131` / `:124-129`; `close()` at `:103-105`;
`closeAndRestoreFocus()` at `:95-99`; the spy-based test at `UserMenu.test.tsx:223-252`; the logout
handler at `:229-241`; `HoloShell.onLogout` at `:22-25`; `LoginScreen.tsx:37` (the `h1`) and
`:41-47` (the email `Input`, no `autoFocus`); `RegisterScreen`'s empty-div early return at `:56-58`
and its `id="name"` first control at `:69-71`; `Input` spreading arbitrary props;
`onUnhandledRequest: 'error'` in `web/src/test/setup.ts:5`; `DialogShell.tsx:264-273` already
deferring a focus decision with `queueMicrotask`. The spec's claim that the only two `autoFocus`
uses in `web/src` are `WorkerLabels.tsx:94` and `ResetPasswordDialog.tsx:70`, and that every
`.focus()` belongs to `UserMenu`, `DialogShell` or `TokenRevealDialog`, was re-checked by searching
for the shape and both hold.

---

## Library behaviour, read at source so you do not have to

All line numbers are in the installed ESM builds under `web/node_modules/`. Read them if you doubt
any of it; do not re-derive it from memory.

**user-event 14.6.1, `system/pointer/mouse.js:74-77`** - `Mouse.down` dispatches `mousedown` first
and only then focuses:

```js
if (!isPrevented && (disabled || instance.dispatchUIEvent(target, 'mousedown', init))) {
    this.startSelecting(instance, init.detail);
    focusElement(target);
}
```

**user-event 14.6.1, `event/focus.js:12-23`** - `focusElement` walks up for the closest focusable
ancestor and, if there is none, **blurs** the current active element:

```js
function focusElement(element) {
    const target = findClosest(element, isFocusable);
    const activeElement = getActiveElement(element.ownerDocument);
    if ((target ?? element.ownerDocument.body) === activeElement) { return; }
    else if (target) { wrapEvent(() => target.focus()); }
    else { wrapEvent(() => activeElement?.blur()); }
    ...
}
```

`findClosest` (`utils/misc/findClosest.js:1-10`) stops when it reaches `document.body`, and
`FOCUSABLE_SELECTOR` (`utils/focus/selector.js`) is inputs, buttons, selects, textareas,
contenteditable, `a[href]` and `[tabindex]`. A bare `<div>` inside the RTL container therefore
genuinely has no focusable ancestor. **This is why the new test must press a `<div>` and not
`document.body`: the two inputs take different branches of that function.**

**@testing-library/react 16.3.2, `dist/pure.js:104-108`** - every dispatched event is wrapped in
`act`:

```js
eventWrapper: cb => { let result; act(() => { result = cb(); }); return result; },
```

So `close()`'s `setOpen(false)` is flushed **inside** the `mousedown` dispatch, and the panel is
already unmounted by the time `focusElement` runs on the next line of `Mouse.down`.

**jsdom 29.1.1, `living/nodes/Node-impl.js:285-297`** - `_detach` clears the document's
`_lastFocusedElement` and recurses into children, so removing the panel clears it for the focused
`Profile` link inside it. `living/nodes/DocumentOrShadowRoot-impl.js:8` then falls back:
`this._ownerDocument._lastFocusedElement || this._ownerDocument.body`. **No `focusout` event is
fired for this**, which is why `onContainerBlur` does not run on that path.

Consequence for the new dead-space test: `document.activeElement` is already `document.body` when
`focusElement` is reached, so the early return at `focus.js:15` fires and user-event's blur branch
is a no-op. Both routes end at `<body>`, so the assertion is robust either way.

**react-dom 18.3.1** - `autoFocus` is **never written to the DOM as an attribute** (see the note at
`cjs/react-dom.development.js:517-519`). `finalizeInitialChildren` returns `!!props.autoFocus` for
`input`/`select`/`textarea` (`:10954-10957`), scheduling an Update effect, and `commitMount` calls
`domElement.focus()` (`:11026-11031`). That runs in `commitLayoutEffects`, which the commit root
calls **after** `resetAfterCommit` runs `restoreSelection` (`:26849-26862`). Two consequences:

- The spec's claim that the attribute lands after `restoreSelection`, so the departing shell's
  pre-commit focus target cannot overwrite it, is confirmed.
- **Never assert `toHaveAttribute('autofocus')`.** It will fail. Assert `document.activeElement`.

---

## File structure

| File | Change | Commit |
| --- | --- | --- |
| `web/src/shell/UserMenu.tsx` | Modify: six comment lines appended inside `onDown`, between the existing rationale paragraph (`:124-129`) and the `if (ref.current ...)` line (`:130`). **No executable line changes.** | 1 |
| `web/src/shell/UserMenu.test.tsx` | Modify: one new render helper after `renderMenuWithSibling` (ends `:174`) and one new test after the existing outside-mousedown test (ends `:252`). Everything already in the file stays byte-identical. | 1 |
| `web/src/auth/LoginScreen.tsx` | Modify: `autoFocus` on the `id="email"` `Input` (`:41-47`), plus a short hazard comment. | 2 |
| `web/src/auth/RegisterScreen.tsx` | Modify: `autoFocus` on the `id="name"` `Input` (`:69-71`), plus a short hazard comment. | 2 |
| `web/src/auth/LoginScreen.test.tsx` | Modify: one new test appended. | 2 |
| `web/src/auth/RegisterScreen.test.tsx` | Modify: one new test appended. | 2 |
| `web/src/auth/authArrivalFocus.test.tsx` | **Create**: three route-level tests, render shape copied from `web/src/app/ProtectedRoute.test.tsx:13-28`. | 2 |
| `web/e2e/auth.spec.ts` | Modify: one added assertion at the end of `logout returns to /auth and clears relay.token` (`:79-96`). No new test. | 2 |

Nothing else is touched. `web/dist` is tracked and stale by convention and must never be staged.

### Command shell note

The canonical commands below are written as `cd web && npx vitest run <file>`. That works in Git
Bash and in PowerShell 7. In Windows PowerShell 5.1, `&&` is a syntax error - run the two parts
separately using the absolute worktree path:

```powershell
cd D:/dev/relay/.claude/worktrees/web-c-usermenu-focus/web
npx vitest run src/shell/UserMenu.test.tsx
```

Every path in this plan is relative to `D:/dev/relay/.claude/worktrees/web-c-usermenu-focus`. Do not
work in `D:/dev/relay` or any other worktree.

---

## Task 1: Pin the accepted dead-space behaviour

**Files:**
- Modify/Test: `web/src/shell/UserMenu.test.tsx` (helper after line 174, test after line 252)

This is a **pinning** test, not a red-green cycle: it asserts behaviour that already exists, which
is the whole point of the item (route 1 is "no behaviour change, but make the decision falsifiable").
It therefore passes on the first run. Task 2 is what earns it: the mutation proof is this test's RED.

- [ ] **Step 1: Add the dead-space render helper**

Insert immediately after `renderMenuWithSibling`'s closing brace (currently line 174), before the
`Escape returns focus to the toggle...` test:

```tsx
// A NON-FOCUSABLE sibling AFTER the component. Pressing document.body is not the
// same probe: user-event's focusElement blurs the active element when the click
// target has no focusable ancestor (event/focus.js:12-23), so the two inputs take
// different branches. renderMenu and renderMenuWithSibling above are shipped and
// stay byte-identical.
function renderMenuWithDeadSpace(onLogout = vi.fn()) {
  render(
    <MemoryRouter>
      <UserMenu email="ada@studio.dev" onLogout={onLogout} />
      <div data-testid="dead-space">dead space</div>
    </MemoryRouter>
  )
  return onLogout
}
```

- [ ] **Step 2: Add the pin test**

Insert immediately after the existing `an outside mousedown closes the menu and never touches the
toggle focus` test (currently ends line 252), before `Tab out of the last item...`:

```tsx
// The accepted gap, pinned so that reversing the decision is a red light rather
// than a silent flip: pressing non-focusable content closes the menu and lets
// focus fall to <body>, because close() does not touch focus at all. Its partner
// above pins the other half - a press on a FOCUSABLE control must also leave
// toggle.focus uncalled - and both use the same spy, so neither can pass by
// measuring something the other does not.
//
// The macrotask turn after the click is what makes this a pin: without it the
// test stays green against a restore deferred with setTimeout or queueMicrotask
// and its claim to pin the decision would be false. It does not flush a
// requestAnimationFrame-deferred restore, which jsdom schedules on a timer.
test('pressing non-focusable dead space closes the menu and leaves focus on <body>', async () => {
  renderMenuWithDeadSpace()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  await userEvent.click(toggle)
  await userEvent.tab()
  // Positive control: focus really is inside the panel, so this cannot pass
  // against a menu that never opened.
  expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Profile' }))
  const toggleFocus = vi.spyOn(toggle, 'focus')

  await userEvent.click(screen.getByTestId('dead-space'))
  await new Promise((r) => setTimeout(r, 0))

  expect(screen.queryByTestId('user-menu-panel')).not.toBeInTheDocument()
  expect(toggleFocus).not.toHaveBeenCalled()
  expect(document.activeElement).toBe(document.body)
  toggleFocus.mockRestore()
})
```

No `act` wrapper is needed around the macrotask turn: the close already flushed inside the
act-wrapped `mousedown` dispatch, so no React state update happens in that window.

- [ ] **Step 3: Run the file**

Run: `cd web && npx vitest run src/shell/UserMenu.test.tsx`

Expected: **PASS**, all tests in the file green including the new one. This is the green baseline
Task 2's mutation proof needs. If the new test is red here, stop and diagnose before mutating
anything: a mutation battery without a green baseline reports nothing.

- [ ] **Step 4: Do not commit yet**

Commit 1 also carries the comment (Task 3). Leave the working tree dirty.

---

## Task 2: Mutation proof for the pin test

**Files:**
- Temporarily mutate: `web/src/shell/UserMenu.tsx`

**Never use `git checkout --` to revert a mutation.** It would discard uncommitted work - and here
`UserMenu.test.tsx` is uncommitted and is the guard under proof. Copy first, restore from the copy.

- [ ] **Step 1: Save a copy of the file you are about to mutate**

```powershell
$W = "D:/dev/relay/.claude/worktrees/web-c-usermenu-focus"
$BAK = "$env:TEMP\relay-mutation"
New-Item -ItemType Directory -Force -Path $BAK | Out-Null
Copy-Item "$W/web/src/shell/UserMenu.tsx" "$BAK/UserMenu.tsx.orig" -Force
```

- [ ] **Step 2: Apply the mutation**

In `web/src/shell/UserMenu.tsx`, append one statement to the end of `onDown` (currently lines
123-131), so the handler reads:

```tsx
    function onDown(e: MouseEvent) {
      // close(), NOT closeAndRestoreFocus(): mousedown fires BEFORE the browser
      // moves focus to whatever was pressed, so at this instant activeElement is
      // still inside the panel and a restore would steal focus away from the
      // control the user just clicked. Identical rule to the Escape path below,
      // opposite answer, purely because the event ordering differs. This is why the
      // DialogShell reasoning had to be re-derived here rather than copied.
      if (ref.current && !ref.current.contains(e.target as Node)) close()
      toggleRef.current?.focus()
    }
```

- [ ] **Step 3: Verify the mutation actually applied**

Run: `git diff --no-index "$BAK/UserMenu.tsx.orig" "$W/web/src/shell/UserMenu.tsx"`

Expected: exactly one added line, `      toggleRef.current?.focus()`. (`--no-index` exits 1 when the
files differ; that is the success case here.) A mutation that silently failed to apply reports
"survived" and proves nothing.

- [ ] **Step 4: Run the file and confirm TWO reds**

Run: `cd web && npx vitest run src/shell/UserMenu.test.tsx`

Expected: **FAIL**, and specifically these two must both be red:
- `pressing non-focusable dead space closes the menu and leaves focus on <body>` - fails on
  `expect(toggleFocus).not.toHaveBeenCalled()` (and on the `<body>` assertion, since vitest's
  `spyOn` calls through to the real `focus`).
- `an outside mousedown closes the menu and never touches the toggle focus` - fails on its own
  `expect(toggleFocus).not.toHaveBeenCalled()`.

If only one is red, the new test is not measuring what the existing one measures and the pair is
not doing its job. Stop and fix the test before continuing.

- [ ] **Step 5: Restore from the copy and verify the restore is byte-exact**

```powershell
Copy-Item "$BAK/UserMenu.tsx.orig" "$W/web/src/shell/UserMenu.tsx" -Force
git diff --no-index "$BAK/UserMenu.tsx.orig" "$W/web/src/shell/UserMenu.tsx"
```

Expected: the diff prints **nothing** (exit 0). Then confirm the tracked file is clean:

Run: `git status --short web/src/shell/UserMenu.tsx`

Expected: **no output** (the comment from Task 3 has not been written yet).

- [ ] **Step 6: Re-run green**

Run: `cd web && npx vitest run src/shell/UserMenu.test.tsx`

Expected: **PASS**, the same green baseline as Task 1 Step 3.

---

## Task 3: Record the accepted gap in the handler, and make commit 1

**Files:**
- Modify: `web/src/shell/UserMenu.tsx:124-130` (comment lines only)

- [ ] **Step 1: Append the hazard comment inside `onDown`**

Insert these six lines immediately after the existing `close(), NOT closeAndRestoreFocus()`
paragraph and immediately before the `if (ref.current ...)` line. The result:

```tsx
    function onDown(e: MouseEvent) {
      // close(), NOT closeAndRestoreFocus(): mousedown fires BEFORE the browser
      // moves focus to whatever was pressed, so at this instant activeElement is
      // still inside the panel and a restore would steal focus away from the
      // control the user just clicked. Identical rule to the Escape path below,
      // opposite answer, purely because the event ordering differs. This is why the
      // DialogShell reasoning had to be re-derived here rather than copied.
      // A press on NON-FOCUSABLE content is the uncovered case, accepted rather
      // than overlooked: nothing takes focus, so it falls to <body> when the panel
      // unmounts ('pressing non-focusable dead space...' in UserMenu.test.tsx pins
      // that). A fix must fire after the browser has moved focus for this press
      // and must still require focus to have been inside the panel, or it steals
      // focus in the mouse-open case closeAndRestoreFocus guards against.
      if (ref.current && !ref.current.contains(e.target as Node)) close()
    }
```

This is the spec's exact contract text. It carries no date, no history, no measurement provenance,
no count and no narrative, and it cites the test that pins its claim. **The mechanism argument -
why a microtask is the wrong primitive - goes in the commit message below, not here.** Do not add
it to the comment.

- [ ] **Step 2: Verify the diff is comment-only**

Run: `git diff -U0 -- web/src/shell/UserMenu.tsx`

Expected: six added lines, every one of them beginning with whitespace then `//`. Zero removed
lines, zero changed executable lines.

- [ ] **Step 3: Run the file green**

Run: `cd web && npx vitest run src/shell/UserMenu.test.tsx`

Expected: **PASS**, unchanged from Task 2 Step 6.

- [ ] **Step 4: Check line endings and diff size before committing**

Run:

```powershell
git ls-files --eol web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx
git diff --stat -- web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx
```

Expected: both files read `i/lf`, and the diffstat is roughly 6 added lines in `UserMenu.tsx` and
about 35 added lines in `UserMenu.test.tsx`, with no deletions. This repo is CRLF and a programmatic
edit can silently reclassify a text file as binary; a diffstat far larger than the change you made
is the tell. Both additions are plain ASCII, so if the diff shows any non-ASCII byte, something
rewrote more than you asked for.

- [ ] **Step 5: Commit 1, with an explicit pathspec**

Never `git add -A`. `web/dist` must not appear in this commit.

```bash
git add web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx
git status --short
git commit -F - <<'EOF'
fix(web): record UserMenu's accepted dead-space focus gap and pin it

Closing the account dropdown by pressing NON-FOCUSABLE content leaves focus on
<body>. The handler's existing rationale argues only the focusable-target case,
so to the next author the dead-space case reads as an oversight rather than as a
decision. This takes route 1 of the backlog item: record it where the decision is
made, and add a test so the decision is falsifiable rather than prose. No
executable line of UserMenu.tsx changes.

Why not the deferred-restore route. Its gate - restore only if activeElement is
document.body - is evaluated at a moment whose position relative to the browser's
own mousedown focusing step is the entire question. A microtask queued inside a
mousedown listener runs at the microtask checkpoint after that listener returns,
which is BEFORE the browser performs mousedown's focusing default action; a frame
or a task runs after it. On the microtask variant the gate therefore reads body in
both branches in a real browser, and the focusable case is saved only by the
browser overwriting the steal a moment later: a transient focus, a spurious
focus/blur pair on the toggle, and a spurious announcement for a screen reader.
That is a mechanism argument from the HTML event model, not a measurement, and the
default lane cannot observe the ordering at all - jsdom's ordering comes from
user-event's emulation (system/pointer/mouse.js dispatches mousedown, then calls
focusElement), not from the browser's event loop. The route as proposed also omits
a synchronous containment capture, so it would steal focus in the mouse-open case
closeAndRestoreFocus already guards against. Taking it needs a post-default-action
primitive, that capture, and a real-browser proof: a larger slice, proposed as a
follow-up rather than smuggled in here.

The new test presses a bare div, not document.body. user-event's focusElement
blurs the active element when the click target has no focusable ancestor
(event/focus.js:12-23), so the two are not the same probe. It awaits a macrotask
turn before asserting, so a task-deferred restore cannot hide behind it, and it
spies on toggle.focus with the same instrument the neighbouring focusable-target
test uses.

Mutation proof: appending toggleRef.current?.focus() to onDown turns BOTH the new
test and 'an outside mousedown closes the menu and never touches the toggle focus'
RED. Verified against a saved copy and restored from that copy.

Item: docs/backlog/idea-2026-08-13-usermenu-outside-mousedown-drops-focus.md
EOF
```

Use a bash heredoc through the Bash tool. If you are committing from PowerShell, write the message
to a scratchpad file and use `git commit -F <file>` instead.

---

## Task 4: Failing test - the sign-in screen claims focus

**Files:**
- Test: `web/src/auth/LoginScreen.test.tsx` (append after line 50)

- [ ] **Step 1: Write the failing test**

Append to `web/src/auth/LoginScreen.test.tsx`:

```tsx
// The sign-in page is the arrival point for a sign-out, a 401 teardown and a
// direct unauthenticated visit; if it claims no focus, the user's next Tab starts
// from the top of the document. The heading assertion is the positive control - an
// activeElement assertion against an unmounted tree is trivially satisfiable.
test('the email field takes focus when the sign-in screen mounts', () => {
  renderLogin()
  expect(screen.getByRole('heading', { name: 'Sign in', level: 1 })).toBeInTheDocument()
  expect(document.activeElement).toBe(screen.getByLabelText('Email'))
})
```

- [ ] **Step 2: Run it and watch it fail for the right reason**

Run: `cd web && npx vitest run src/auth/LoginScreen.test.tsx`

Expected: **FAIL**, one test. The heading assertion passes and the failure is on
`expect(document.activeElement).toBe(...)`, reporting the `<body>` element where the
`<input id="email">` was expected. If it fails on the heading instead, the render helper broke and
the focus assertion is not being exercised at all.

- [ ] **Step 3: Do not commit**

Commit 2 is assembled at the end of Task 11.

---

## Task 5: Failing tests - the three arrival paths

**Files:**
- Create: `web/src/auth/authArrivalFocus.test.tsx`

The render shape follows `web/src/app/ProtectedRoute.test.tsx:13-28`. The protected route's element
is a bare `<div>` on purpose: `ProtectedRoute` renders `HoloShell` (and therefore the real
`UserMenu`) around it, which is the departure point these tests need, while the page itself issues
no requests. That matters because `web/src/test/setup.ts` runs MSW with
`onUnhandledRequest: 'error'` - every request a test provokes must have a handler.

- [ ] **Step 1: Write the new test file**

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { apiFetch } from '../lib/api'
import { clearToken, setToken } from '../lib/token'
import { ProtectedRoute } from '../app/ProtectedRoute'
import { AuthProvider } from './AuthProvider'
import { LoginScreen } from './LoginScreen'

afterEach(() => clearToken())

const ME = { id: '1', email: 'ada@studio.dev', name: 'Ada', is_admin: false }

// Render shape from app/ProtectedRoute.test.tsx. The protected element is a bare
// div: ProtectedRoute still renders HoloShell and the real UserMenu around it,
// which is the departure point these tests need, while the page itself issues no
// request the lane would have to stub.
function renderAt(path: string) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/auth" element={<LoginScreen />} />
            <Route element={<ProtectedRoute />}>
              <Route path="/jobs" element={<div>jobs page</div>} />
            </Route>
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// findByRole is both the positive control that the sign-in page really rendered
// and the await that keeps the arriving commit inside act.
async function expectFocusOnEmail() {
  await screen.findByRole('heading', { name: 'Sign in', level: 1 })
  expect(document.activeElement).toBe(screen.getByLabelText('Email'))
}

test('arriving at /auth unauthenticated puts focus on the email field', async () => {
  renderAt('/auth')
  await expectFocusOnEmail()
})

test('signing out from the account menu leaves focus on the email field, not on <body>', async () => {
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.delete('/v1/auth/token', () => new HttpResponse(null, { status: 204 })),
  )
  setToken('tok')
  renderAt('/jobs')
  // Drive the real component: HoloShell renders UserMenu, whose toggle's
  // accessible name is the signed-in email. A synthetic logout button would not
  // exercise the path this test is about.
  await userEvent.click(await screen.findByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.click(screen.getByText('Log out'))

  await expectFocusOnEmail()
})

test('a 401 teardown lands on the sign-in page with focus on the email field', async () => {
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ error: 'unauthorized' }, { status: 401 }),
    ),
  )
  setToken('tok')
  renderAt('/jobs')
  await screen.findByText('jobs page')

  // The real teardown, not a hand-rolled navigation: apiFetch stamps the 401 with
  // the token the request carried, AuthProvider's subscription clears the session,
  // and ProtectedRoute redirects. Precedent: authTokenSecrecy.test.tsx.
  await apiFetch('/jobs/stats').catch(() => {})

  await expectFocusOnEmail()
})
```

- [ ] **Step 2: Run it and watch all three fail for the right reason**

Run: `cd web && npx vitest run src/auth/authArrivalFocus.test.tsx`

Expected: **FAIL, 3 of 3**. Each one gets past `findByRole('heading', ...)` - so the sign-in page
really rendered on all three arrival paths - and fails on `expect(document.activeElement).toBe(...)`
with the `<body>` element. If any test fails at the heading instead, the arrival path itself is not
reaching `/auth` and the focus claim is untested; fix that before continuing.

- [ ] **Step 3: Do not commit**

---

## Task 6: Make the sign-in screen claim focus

**Files:**
- Modify: `web/src/auth/LoginScreen.tsx:40-48`

- [ ] **Step 1: Add the attribute and its comment**

Replace the email `Field` block with:

```tsx
        {/* The sign-in page claims arrival focus here; authArrivalFocus.test.tsx
            pins the sign-out, 401-teardown and direct-visit paths. Keep the
            attribute rather than a mount effect - RegisterScreen.tsx states why the
            two are not interchangeable. */}
        <Field label="Email" htmlFor="email">
          <Input
            id="email"
            type="email"
            autoComplete="username"
            autoFocus
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
```

- [ ] **Step 2: Run the two sign-in files green**

Run: `cd web && npx vitest run src/auth/LoginScreen.test.tsx src/auth/authArrivalFocus.test.tsx`

Expected: **PASS**, all four previously-red tests now green, and the two pre-existing `LoginScreen`
tests (`shows a generic message on 401`, `shows a rate-limit hint on 429`) still green.

- [ ] **Step 3: Do not commit**

---

## Task 7: Failing test - the register screen claims focus

**Files:**
- Test: `web/src/auth/RegisterScreen.test.tsx` (append after line 69)

- [ ] **Step 1: Write the failing test**

Append to `web/src/auth/RegisterScreen.test.tsx`:

```tsx
// The register form renders on a LATER commit than the component - the /config
// early return holds until that request resolves - so this test is what
// discriminates the autoFocus attribute from a []-deps mount effect, which would
// run when there is no node to focus and never run again.
test('the display name field takes focus once the register form renders', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: true })))
  renderRegister()
  const name = await screen.findByLabelText('Display name')
  // Positive control: the form rendered, not just the empty placeholder.
  expect(
    screen.getByRole('heading', { name: 'Create your relay account', level: 1 }),
  ).toBeInTheDocument()
  expect(document.activeElement).toBe(name)
})
```

- [ ] **Step 2: Run it and watch it fail for the right reason**

Run: `cd web && npx vitest run src/auth/RegisterScreen.test.tsx`

Expected: **FAIL**, one test, on `expect(document.activeElement).toBe(name)` with the `<body>`
element. The `findByLabelText` and heading assertions before it pass, so the form did render.

- [ ] **Step 3: Do not commit**

---

## Task 8: Make the register screen claim focus

**Files:**
- Modify: `web/src/auth/RegisterScreen.tsx:69-71`

- [ ] **Step 1: Add the attribute and its comment**

Replace the display-name `Field` block with:

```tsx
        {/* autoFocus, NOT a useEffect mount focus: the form renders on a later
            commit than the component (the early return above holds until /config
            resolves), so a []-deps effect would run with no node to focus and never
            run again. React applies the attribute when this element mounts.
            RegisterScreen.test.tsx's focus test is the pin. */}
        <Field label="Display name" htmlFor="name">
          <Input id="name" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
```

- [ ] **Step 2: Run the file green**

Run: `cd web && npx vitest run src/auth/RegisterScreen.test.tsx`

Expected: **PASS**, five tests, including the four pre-existing ones.

- [ ] **Step 3: Do not commit**

---

## Task 9: The three mutation proofs for commit 2

**Files:**
- Temporarily mutate: `web/src/auth/LoginScreen.tsx`, then `web/src/auth/RegisterScreen.tsx`

**Both files carry uncommitted work at this point.** `git checkout --` on either would destroy the
fix you just wrote and the proof would be meaningless. Copy first, restore from the copy, verify the
restore with a diff.

- [ ] **Step 1: Save copies**

```powershell
$W = "D:/dev/relay/.claude/worktrees/web-c-usermenu-focus"
$BAK = "$env:TEMP\relay-mutation"
New-Item -ItemType Directory -Force -Path $BAK | Out-Null
Copy-Item "$W/web/src/auth/LoginScreen.tsx" "$BAK/LoginScreen.tsx.orig" -Force
Copy-Item "$W/web/src/auth/RegisterScreen.tsx" "$BAK/RegisterScreen.tsx.orig" -Force
```

- [ ] **Step 2: Confirm the green baseline**

Run: `cd web && npx vitest run src/auth/LoginScreen.test.tsx src/auth/authArrivalFocus.test.tsx src/auth/RegisterScreen.test.tsx`

Expected: **PASS**, everything green. A battery run from a red baseline reports nothing.

- [ ] **Step 3: Mutation 1 - remove `autoFocus` from `LoginScreen`**

Delete the single `autoFocus` line from the email `Input` in `web/src/auth/LoginScreen.tsx` (leave
the comment). Verify it applied:

Run: `git diff --no-index "$BAK/LoginScreen.tsx.orig" "$W/web/src/auth/LoginScreen.tsx"`

Expected: exactly one removed line, `            autoFocus`.

Run: `cd web && npx vitest run src/auth/LoginScreen.test.tsx src/auth/authArrivalFocus.test.tsx`

Expected: **FAIL, 4 tests** - `the email field takes focus when the sign-in screen mounts`,
`arriving at /auth unauthenticated...`, `signing out from the account menu...`, `a 401 teardown...`.
This is the headline discriminator; if fewer than four go red, one of the arrival tests is not
actually asserting the property.

- [ ] **Step 4: Restore `LoginScreen` and re-run green**

```powershell
Copy-Item "$BAK/LoginScreen.tsx.orig" "$W/web/src/auth/LoginScreen.tsx" -Force
git diff --no-index "$BAK/LoginScreen.tsx.orig" "$W/web/src/auth/LoginScreen.tsx"
```

Expected: the diff prints nothing. Then run
`cd web && npx vitest run src/auth/LoginScreen.test.tsx src/auth/authArrivalFocus.test.tsx` and
expect **PASS**.

- [ ] **Step 5: Mutation 2 - remove `autoFocus` from `RegisterScreen`**

Delete `autoFocus` from the display-name `Input`. Verify with
`git diff --no-index "$BAK/RegisterScreen.tsx.orig" "$W/web/src/auth/RegisterScreen.tsx"` (expected:
one changed line, the attribute gone).

Run: `cd web && npx vitest run src/auth/RegisterScreen.test.tsx`

Expected: **FAIL, 1 test** - `the display name field takes focus once the register form renders`.

- [ ] **Step 6: Mutation 3 - attribute replaced by a mount effect, which must STAY RED**

Leave mutation 2 in place (the attribute is gone) and add a `[]`-deps mount effect to
`RegisterScreen`, immediately after the existing `/config` effect at lines 21-25:

```tsx
  useEffect(() => {
    ;(document.getElementById('name') as HTMLInputElement | null)?.focus()
  }, [])
```

**Deviation from the spec, and why.** The spec words this mutant as a `ref` plus
`useEffect(() => ref.current?.focus(), [])`. `Input` is a plain function component, not a
`forwardRef` one, so a `ref` prop never reaches the `<input>` and TypeScript rejects it - the mutant
would stay red for a reason unrelated to commit timing, which is the confound this proof exists to
exclude. Looking the node up by id attaches to a real DOM node whenever one exists, so the only
thing left varying is *when* the effect runs.

Verify it applied with `git diff --no-index "$BAK/RegisterScreen.tsx.orig" "$W/web/src/auth/RegisterScreen.tsx"`
(expected: the `autoFocus` line gone, four lines added).

Run: `cd web && npx vitest run src/auth/RegisterScreen.test.tsx`

Expected: **STILL FAIL, the same 1 test.** That is the evidence for the attribute-over-effect
decision: the effect runs on the commit where the early return has rendered the empty div and there
is no `#name` to focus, and it never runs again.

**If it comes back GREEN, stop.** The spec's stated reason for choosing the attribute is then wrong,
and that must be reported to the conductor and corrected in the retro, not quietly kept. Do not
change the implementation on the strength of a green here without saying so.

- [ ] **Step 7: Restore `RegisterScreen` and re-run green**

```powershell
Copy-Item "$BAK/RegisterScreen.tsx.orig" "$W/web/src/auth/RegisterScreen.tsx" -Force
git diff --no-index "$BAK/RegisterScreen.tsx.orig" "$W/web/src/auth/RegisterScreen.tsx"
```

Expected: the diff prints nothing. Then run `cd web && npx vitest run src/auth/RegisterScreen.test.tsx`
and expect **PASS**, five tests.

---

## Task 10: The real-browser assertion

**Files:**
- Modify: `web/e2e/auth.spec.ts:79-96`

- [ ] **Step 1: Add one assertion to the existing logout test**

Append inside `logout returns to /auth and clears relay.token`, after the existing token poll, so
the test ends:

```ts
  await expect(page).toHaveURL(/\/auth$/)
  // Assert ABSENCE from the actual store, not that a clear function was called.
  // The key is web/src/lib/token.ts:1.
  await expect
    .poll(() => page.evaluate(() => window.localStorage.getItem('relay.token')))
    .toBeNull()
  // The destination claims focus, so a keyboard user who signs out does not land
  // on <body> with their next Tab starting from the top of the document. Polled
  // rather than read once: focus lands on the commit that mounts the form, which
  // is after the URL settles. jsdom cannot answer this - it has no browser event
  // loop and no real navigation.
  await expect
    .poll(() => page.evaluate(() => document.activeElement?.id ?? null))
    .toBe('email')
```

No new test, and nothing else in the file changes.

- [ ] **Step 2: Run the lane if you can, and say plainly if you cannot**

Run: `make test-e2e` (from Git Bash; see `web/e2e/README.md` for the MSYS2 make invocation and the
`OS`/`GOPATH` forwarding this host needs).

It needs Docker Desktop, a Postgres on `postgres://relay:relay@127.0.0.1:5432`, and the chromium and
webkit browsers installed once (`cd web && npx playwright install chromium webkit`).

Expected: the whole suite green, including `logout returns to /auth and clears relay.token` on both
chromium and webkit.

**If the lane cannot be run here, report that in one plain sentence and let CI run it
(`.github/workflows/web-ci.yml`). Do not substitute a jsdom assertion and describe it as browser
coverage, and do not describe the e2e result as verified if it did not run.**

- [ ] **Step 3: Restore `web/dist` if the run touched it**

`make test-e2e` restores the tracked `web/dist/index.html` placeholder on exit, pass or fail, but
check anyway:

Run: `git status --short web/dist`

Expected: **no output**. If there is any, run `git checkout -- web/dist/` before assembling the
commit. `web/dist` is tracked, stale by convention, and must never be staged by this PR.

---

## Task 11: Whole-suite gate and commit 2

**Files:** none changed in this task.

- [ ] **Step 1: The whole unit suite, not just the touched files**

Run: `cd web && npm test`

Expected: **PASS**, every file. `autoFocus` on the sign-in screen changes what has focus in every
test that renders it (`App.test.tsx` among them); those tests type into fields by label and are
unaffected, but the whole suite is the instrument that proves it rather than the argument.

- [ ] **Step 2: TypeScript**

Run: `cd web && npx tsc -b`

Expected: **no output**. `tsconfig.json` sets `noUnusedLocals` and `noUnusedParameters` and its
`include` covers `src` and `e2e`, so an unused import in the new test file is a hard failure here
even though vitest never typechecks.

- [ ] **Step 3: Production build**

Run: `cd web && npm run build`

Expected: success. Note that this writes `web/dist`.

- [ ] **Step 4: Restore `web/dist`**

Run: `git checkout -- web/dist/` then `git status --short`

Expected: `web/dist` clean, and the only modified paths are the five source files of commit 2 plus
the untracked `web/src/auth/authArrivalFocus.test.tsx`.

- [ ] **Step 5: Line endings and diff size**

Run:

```powershell
git ls-files --eol web/src/auth/LoginScreen.tsx web/src/auth/RegisterScreen.tsx web/src/auth/LoginScreen.test.tsx web/src/auth/RegisterScreen.test.tsx web/e2e/auth.spec.ts
git diff --stat -- web/src/auth web/e2e/auth.spec.ts
```

Expected: every tracked path reads `i/lf`, and the diffstat is small and proportionate (roughly: 5
added lines in `LoginScreen.tsx`, 6 in `RegisterScreen.tsx`, 9 in `LoginScreen.test.tsx`, 12 in
`RegisterScreen.test.tsx`, 9 in `auth.spec.ts`, with no deletions beyond the two `Field` blocks
being rewritten in place). A diffstat far larger than the change you made means a rewrite
reclassified a file; investigate before committing.

- [ ] **Step 6: Commit 2, with an explicit pathspec**

```bash
git add web/src/auth/LoginScreen.tsx web/src/auth/RegisterScreen.tsx \
        web/src/auth/LoginScreen.test.tsx web/src/auth/RegisterScreen.test.tsx \
        web/src/auth/authArrivalFocus.test.tsx web/e2e/auth.spec.ts
git status --short
git commit -F - <<'EOF'
feat(web): the auth screens claim focus on arrival

Signing out landed the user on the sign-in page with focus on <body>: UserMenu
restores focus to its toggle and hands off, HoloShell.onLogout navigates, the
shell unmounts with the toggle still holding focus, and focus falls to the
document. The next Tab starts from the top of the page, at the moment the
application changed out from under the user.

The fix lives at the DESTINATION, not at any departure point. All three arrival
paths - the logout navigation, the 401 teardown, and a direct unauthenticated
visit - mount the same component, so one attribute covers all three by
construction. Three route-level tests plus one component test pin them anyway,
because covered-by-construction is an argument and the item asked for evidence.
UserMenu is untouched: by the time the shell unmounts it no longer exists to move
focus anywhere, and its close-then-hand-off ordering is a regression guard for
this commit, not a detail.

Why the first form control and not a focused h1. The app has no route-change
announcement policy - every .focus() in web/src belongs to a dialog or to the
account menu - so the heading treatment on the auth screens alone would announce a
transition there and stay silent on every other route change, which reads as an
inconsistency rather than as a policy. The honest cost: a screen-reader user
arriving after a 401 teardown, the one transition they did not ask for, is dropped
into a labelled text field rather than told where they are. Better than <body>,
worse than an announcement, and the trigger to revisit is a general route-change
focus policy for the SPA.

Why the attribute and not a mount effect. RegisterScreen returns an empty div
until /config resolves, so its form mounts on a LATER commit than the component; a
[]-deps effect fires once, when there is no node to focus, and never runs again.
React also applies autoFocus in the layout phase (commitLayoutEffects ->
commitMount -> focus()), which the commit root runs AFTER resetAfterCommit has run
restoreSelection - so on the logout path, where React's pre-commit focus target is
the toggle being removed in that same commit, nothing can overwrite it.

The register screen gets the same treatment in the same commit: identical gap,
identical cause, one attribute and one test, and omitting it would read to the next
author as a decision.

Mutation proofs. Removing autoFocus from LoginScreen turns all four sign-in tests
RED. Removing it from RegisterScreen turns the register test RED. Replacing the
attribute with a []-deps mount effect that looks the node up by id leaves the
register test RED, which is the evidence for the attribute-over-effect choice. (An
effect over a ref cannot be used as the mutant: Input is a plain function
component, so a ref prop never reaches the input and the mutant would be red for
the wrong reason.) Each mutation was verified applied by diffing against a saved
copy and restored from that copy.

Item: docs/backlog/idea-2026-08-13-post-logout-focus-lands-on-body.md
EOF
```

- [ ] **Step 7: Final tree check**

Run: `git status --short`

Expected: **clean**. Two commits ahead of the branch point, `web/dist` untouched.

---

## What this plan does NOT do

- **No `/backlog close`.** The two items stay open on this branch. The conductor runs
  `/backlog close` after the PR, which does the `git mv` into `docs/backlog/closed/`.
- **No follow-up items are filed.** The spec proposes five; filing them is the conductor's step.
  Name them in the report if you want them remembered: the deferred outside-mousedown restore proven
  in a real browser; a route-change focus and announcement policy for the SPA; `PublicOnlyRoute`
  rendering the auth screens while auth status is still `loading`; focus falling to `<body>` on
  arrival at `/jobs` after a successful sign-in; `/register` being absent from the e2e surface list.
- **No Go lane.** Nothing here touches Go, and running one would be noise.
- **No refactoring.** `renderMenu` and `renderMenuWithSibling` stay byte-identical, the existing
  `UserMenu` tests stay byte-identical, and `UserMenu.tsx` gains comment lines only.

---

## Self-review against the spec

| Spec requirement | Task |
| --- | --- |
| Item 1: the exact six-line comment inside `onDown`, no executable change | Task 3 Steps 1-2 |
| Item 1: new test `pressing non-focusable dead space...`, bare `<div>` target, macrotask flush, positive control, same spy instrument | Task 1 Steps 1-2 |
| Item 1: existing outside-mousedown test stays byte-identical and green | Task 1 Step 3, Task 3 Step 3 |
| Item 1: mutation proof, both tests RED, verified applied and restored from a copy | Task 2 |
| Item 2: `autoFocus` on `LoginScreen`'s email `Input` | Task 6 |
| Item 2: `autoFocus` on `RegisterScreen`'s display-name `Input` | Task 8 |
| Item 2: `LoginScreen.test.tsx` test with the heading positive control | Task 4 |
| Item 2: `RegisterScreen.test.tsx` test that awaits the form | Task 7 |
| Item 2: `authArrivalFocus.test.tsx`, three route-level tests, `ProtectedRoute.test.tsx` render shape, every handler registered, real component driven for logout, `authTokenSecrecy` precedent for the 401 | Task 5 |
| Item 2: `UserMenu` unchanged, its logout test still green | Task 11 Step 1 |
| Item 2: one added assertion in `web/e2e/auth.spec.ts`, no new e2e test | Task 10 |
| Item 2: three mutation proofs, the third must stay RED | Task 9 |
| Gates: whole unit suite, `tsc -b`, `npm run build`, e2e or an honest statement | Task 11 Steps 1-3, Task 10 Step 2 |
| Two commits in the given order, each carrying its argument | Task 3 Step 5, Task 11 Step 6 |
| `web/dist` never staged | Task 10 Step 3, Task 11 Steps 4 and 6 |
