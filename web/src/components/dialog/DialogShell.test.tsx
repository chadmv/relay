import { useRef, useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { DialogShell } from './DialogShell'
import { __resetForTest } from './dialogStack'

// beforeEach, NOT afterEach (code review, 2026-08-09): vitest's default
// sequence.hooks is 'stack' (LIFO), and @testing-library/react registers its
// own cleanup-unmounting afterEach at IMPORT time, before this file's body
// runs - so an afterEach registered here would run BEFORE RTL's, forcibly
// emptying the stack while a shell from the just-finished test is STILL
// mounted. That shell's own cleanup then runs against an already-empty stack,
// hits the tolerate-absent-id branch, and returns without ever calling
// apply() - so this file never exercised the real teardown path, and a
// regression leaving the page permanently scroll-locked would stay green
// here. beforeEach resets before each test starts (a belt-and-braces guard
// against a previous test's crash leaving stale state) without racing RTL's
// own afterEach, which still runs the real per-test teardown every time.
beforeEach(() => __resetForTest())

function Harness({
  dismissOnEscape,
  onDismiss,
}: {
  dismissOnEscape?: boolean
  onDismiss: () => void
}) {
  return (
    <DialogShell
      titleId="harness-title"
      onDismiss={onDismiss}
      dismissOnEscape={dismissOnEscape}
      panelClassName="max-w-sm"
    >
      <h2 id="harness-title">Shell harness</h2>
      <button type="button">first</button>
      <button type="button">middle</button>
      <button type="button">last</button>
    </DialogShell>
  )
}

test('renders exactly two elements deep - the panel inside the scrim, nothing between', () => {
  render(<Harness onDismiss={vi.fn()} />)
  const dialog = screen.getByRole('dialog')
  const scrim = dialog.parentElement as HTMLElement

  // The depth is a hard constraint, not an implementation detail:
  // TokenRevealDialog.test.tsx:82 obtains the backdrop as
  // getByRole('dialog').parentElement and clicks it to prove a stray click cannot
  // destroy a credential. An extra wrapper would silently retarget that click and
  // the test would keep passing while proving nothing - a self-vacuuming security
  // assertion.
  expect(scrim.className).toBe(
    'fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4',
  )
  expect(dialog.className).toBe(
    'w-full rounded-card border border-border bg-bg p-5 shadow-xl max-w-sm',
  )
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toHaveAttribute('aria-labelledby', 'harness-title')
  expect(dialog).toHaveAccessibleName('Shell harness')
  // tabIndex -1 gives the shell a focus target if a caller ever has no focusable
  // content; it does not enter the tab ring (user-event filters tabindex < 0 at
  // getTabDestination.js:11, and so do browsers).
  expect(dialog.getAttribute('tabindex')).toBe('-1')

  // Portaled to the single shared layer under <body>, not into the RTL container.
  expect(scrim.parentElement).toBe(document.querySelector('[data-dialog-layer]'))
  expect(document.querySelector('[data-dialog-layer]')?.parentElement).toBe(document.body)
})

test('the scrim has no click handler - a backdrop click never dismisses', async () => {
  const onDismiss = vi.fn()
  render(<Harness onDismiss={onDismiss} />)
  const scrim = screen.getByRole('dialog').parentElement as HTMLElement

  await userEvent.click(scrim)

  // None of the three dialogs has an overlay onClick today, and
  // TokenRevealDialog's invariant 4 records that as deliberate. The shell must
  // not add one.
  expect(onDismiss).not.toHaveBeenCalled()
})

test('Escape dismisses by default (the live-instrument control for the flag below)', async () => {
  const onDismiss = vi.fn()
  render(<Harness onDismiss={onDismiss} />)
  expect(screen.getByRole('button', { name: 'first' })).toHaveFocus()

  await userEvent.keyboard('{Escape}')

  expect(onDismiss).toHaveBeenCalledTimes(1)
})

test('dismissOnEscape={false} suppresses Escape while the keydown still reaches the panel', async () => {
  const onDismiss = vi.fn()
  render(<Harness dismissOnEscape={false} onDismiss={onDismiss} />)
  // Focus is inside the panel, so the keydown genuinely reaches the panel's
  // onKeyDown. Without this assertion the suppression could be an artefact of a
  // keystroke that landed on <body> - the test above is the paired positive
  // control on the same instrument.
  expect(screen.getByRole('button', { name: 'first' })).toHaveFocus()

  await userEvent.keyboard('{Escape}')

  expect(onDismiss).not.toHaveBeenCalled()
})

test('Tab from the last focusable wraps to the first, Shift+Tab from the first wraps to the last', async () => {
  render(<Harness onDismiss={vi.fn()} />)
  const first = screen.getByRole('button', { name: 'first' })
  const middle = screen.getByRole('button', { name: 'middle' })
  const last = screen.getByRole('button', { name: 'last' })
  expect(first).toHaveFocus()

  await userEvent.tab({ shift: true })
  expect(last).toHaveFocus()

  await userEvent.tab()
  expect(first).toHaveFocus()

  // The interior steps are NOT intercepted - the default tab behavior is left
  // alone so ordinary navigation inside the panel is the platform's, not ours.
  await userEvent.tab()
  expect(middle).toHaveFocus()
})

test('initialFocusRef wins over the first focusable', () => {
  function WithRef() {
    const ref = useRef<HTMLButtonElement>(null)
    return (
      <DialogShell titleId="ref-title" onDismiss={vi.fn()} initialFocusRef={ref}>
        <h2 id="ref-title">Ref harness</h2>
        <button type="button">first</button>
        <button type="button" ref={ref}>
          chosen
        </button>
      </DialogShell>
    )
  }
  render(<WithRef />)
  expect(screen.getByRole('button', { name: 'chosen' })).toHaveFocus()
})

test('with no panelClassName the panel carries the base string alone', () => {
  function Bare() {
    return (
      <DialogShell titleId="bare-title" onDismiss={vi.fn()}>
        <h2 id="bare-title">Bare</h2>
        <button type="button">only</button>
      </DialogShell>
    )
  }
  render(<Bare />)
  // The width utility must not sit in the base with a caller override: two
  // competing Tailwind utilities on one element resolve by stylesheet order, not
  // class-attribute order, so an override is not reliable.
  expect(screen.getByRole('dialog').className).toBe(
    'w-full rounded-card border border-border bg-bg p-5 shadow-xl',
  )
})

// Regression (code review, 2026-08-09): onKeyDown on the panel only fires when
// document.activeElement is inside the panel. A click on the scrim - a
// non-focusable element - blurs whatever was focused, via the browser's own
// default mousedown behavior, and document.activeElement falls back to <body>.
// A keydown targeted at <body> never reaches a React handler on the panel,
// since synthetic events only deliver to ancestors of the actual target. Every
// dialog on main used a document-level listener, so this worked before the
// shell existed; the panel-only handler was a regression.
test('Escape still dismisses after focus has moved outside the panel', async () => {
  const onDismiss = vi.fn()
  render(<Harness onDismiss={onDismiss} />)

  // Move focus outside the panel WITHOUT going through a route the shell might
  // separately guard (a scrim mousedown, see the next test). This models any
  // focus-outside route in general - a removed trigger, a blurred disabled
  // button, an extension moving focus - not just the backdrop.
  ;(document.activeElement as HTMLElement).blur()
  // Instrument control: without this, the assertion below could pass for the
  // wrong reason (e.g. focus never left in the first place).
  expect(document.activeElement).toBe(document.body)

  await userEvent.keyboard('{Escape}')

  expect(onDismiss).toHaveBeenCalledTimes(1)
})

// A scrim mousedown's preventDefault (below) closes this specific route rather
// than merely surviving it: the previous version of this test clicked the
// scrim, asserted document.activeElement === document.body as the RED-proving
// instrument control, and then asserted Escape recovers from there. That
// control now fails ON PURPOSE - proof the fix is stronger than "recovers from
// the blur", it prevents the blur outright - so the assertion is inverted here
// to match. The general "focus can be anywhere" regression test above
// (unconditional .blur()) is what proves the document-level Escape listener
// itself, independent of this specific route.
test('a scrim mousedown does not blur the panel at all - focus and the trap both survive', async () => {
  const onDismiss = vi.fn()
  render(<Harness onDismiss={onDismiss} />)
  const scrim = screen.getByRole('dialog').parentElement as HTMLElement
  const first = screen.getByRole('button', { name: 'first' })
  expect(first).toHaveFocus()

  await userEvent.click(scrim)

  expect(first).toHaveFocus()
  expect(onDismiss).not.toHaveBeenCalled()

  // And with focus never having left, the panel-scoped Tab trap is intact too -
  // not just Escape.
  await userEvent.tab({ shift: true })
  expect(screen.getByRole('button', { name: 'last' })).toHaveFocus()
})

// M4 (code review, 2026-08-09). This models a caller whose confirm handler
// removes its own trigger SYNCHRONOUSLY - in the same commit as the dialog
// closing, so trigger.isConnected is already false by the time DialogShell's
// cleanup reads it. This is the reachable subset of "the trigger is gone":
// investigated against the real app's five ConfirmDialog call sites, all of
// them close BEFORE their mutation resolves and remove the row only later, via
// an unrelated refetch-driven commit DialogShell has already returned from by
// then - a race this effect genuinely cannot observe (jsdom fires no event on
// a silent focus detach, and the mechanisms that would see it in a real
// browser are the same always-on-watcher shape this file's own header already
// rejects for the trap). See the KNOWN GAP comment beside the landmark
// fallback in DialogShell.tsx for the full account; this test pins what the
// fallback DOES cover.
test('when the trigger is already gone at close time, focus lands on the main landmark, not <body>', async () => {
  function Page() {
    const [open, setOpen] = useState(false)
    const [triggerGone, setTriggerGone] = useState(false)

    function closeAndRemoveTrigger() {
      // Both state changes land in the SAME commit (one event handler, no
      // await between them), so the trigger is already disconnected by the
      // time DialogShell's cleanup effect runs.
      setOpen(false)
      setTriggerGone(true)
    }

    return (
      <main>
        {!triggerGone && (
          <button type="button" onClick={() => setOpen(true)}>
            trigger
          </button>
        )}
        {open && (
          <DialogShell titleId="confirm-title" onDismiss={closeAndRemoveTrigger}>
            <h2 id="confirm-title">Confirm</h2>
            <button type="button" onClick={closeAndRemoveTrigger}>
              Confirm
            </button>
          </DialogShell>
        )}
      </main>
    )
  }

  render(<Page />)
  await userEvent.click(screen.getByRole('button', { name: 'trigger' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  // The trigger really is gone, so this is not a vacuous pass on the trigger
  // still being restorable.
  expect(screen.queryByRole('button', { name: 'trigger' })).not.toBeInTheDocument()
  expect(document.activeElement).not.toBe(document.body)
  expect(document.activeElement).toBe(screen.getByRole('main'))
})

// Finding 2 (code review, 2026-08-09). The landmark fallback writes
// tabIndex = -1 onto HoloShell's <main> - a node this shell does not own -
// and, before this test, never removed it: the tabindex attribute stuck
// around forever after the FIRST time it was needed, unlike every other
// mutation this module makes to nodes it does not own (dialogStack.ts's
// data-dialog-inert MARK is the same ownership pattern, applied identically).
test('the tabindex the landmark fallback adds is removed once focus leaves the landmark', async () => {
  function Page() {
    const [open, setOpen] = useState(false)
    const [triggerGone, setTriggerGone] = useState(false)

    function closeAndRemoveTrigger() {
      setOpen(false)
      setTriggerGone(true)
    }

    return (
      <div>
        <button type="button">elsewhere</button>
        <main>
          {!triggerGone && (
            <button type="button" onClick={() => setOpen(true)}>
              trigger
            </button>
          )}
          {open && (
            <DialogShell titleId="confirm-title" onDismiss={closeAndRemoveTrigger}>
              <h2 id="confirm-title">Confirm</h2>
              <button type="button" onClick={closeAndRemoveTrigger}>
                Confirm
              </button>
            </DialogShell>
          )}
        </main>
      </div>
    )
  }

  render(<Page />)
  await userEvent.click(screen.getByRole('button', { name: 'trigger' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  const main = screen.getByRole('main')
  expect(main).toHaveFocus()
  expect(main).toHaveAttribute('tabindex', '-1')

  await userEvent.click(screen.getByRole('button', { name: 'elsewhere' }))

  expect(main).not.toHaveAttribute('tabindex')
})

// M5 (code review, 2026-08-09). A browser puts only ONE radio per `name` group
// in the tab order - the checked one, or the first if none is checked - but the
// old FOCUSABLE selector matched every enabled radio unconditionally. With two
// UNCHECKED radios after the panel's only button, `last` resolved to the
// second radio - a node no browser ever lets Tab land on directly - so the
// wrap-around never fired once the loop reached it and focus walked off the
// end of the panel entirely.
test('an unchecked radio group contributes only its first radio to the tab order', async () => {
  function RadioHarness() {
    return (
      <DialogShell titleId="radio-title" onDismiss={vi.fn()} panelClassName="max-w-sm">
        <h2 id="radio-title">Radios</h2>
        <button type="button">only button</button>
        <input type="radio" name="choice" aria-label="first choice" />
        <input type="radio" name="choice" aria-label="second choice" />
      </DialogShell>
    )
  }
  render(<RadioHarness />)
  const onlyButton = screen.getByRole('button', { name: 'only button' })
  const first = screen.getByRole('radio', { name: 'first choice' })
  expect(onlyButton).toHaveFocus()

  await userEvent.tab()
  expect(first).toHaveFocus()
  // The wrap: if `second choice` had been left in the computed focusable list,
  // this next Tab would land there instead of cycling back.
  await userEvent.tab()
  expect(onlyButton).toHaveFocus()
})

// The checked-but-not-DOM-last radio is what isolates the group-filtering fix
// from user-event's OWN native radio-group tab semantics, which already
// correctly skip an unchecked sibling on an ORDINARY (non-boundary) Tab step -
// an earlier version of this test tabbed there from the button and passed
// identically whether or not the fix below existed, because that step never
// reaches this file's onKeyDown at all (only Tab FROM the panel's last
// focusable, or Shift+Tab FROM its first, is intercepted; everything between
// falls through to the platform's own tab order). Focusing the checked radio
// directly, then tabbing forward, exercises exactly the wrap computation this
// fix changes: `last` must resolve to the checked radio, not to whichever
// radio happens to sit last in the DOM.
test('a checked radio, not the group\'s DOM-last radio, is what the wrap-around lands on', async () => {
  function RadioHarness() {
    return (
      <DialogShell titleId="radio-title" onDismiss={vi.fn()} panelClassName="max-w-sm">
        <h2 id="radio-title">Radios</h2>
        <button type="button">only button</button>
        <input type="radio" name="choice" aria-label="checked choice" defaultChecked />
        <input type="radio" name="choice" aria-label="unchecked, DOM-last choice" />
      </DialogShell>
    )
  }
  render(<RadioHarness />)
  const onlyButton = screen.getByRole('button', { name: 'only button' })
  const checked = screen.getByRole('radio', { name: 'checked choice' })
  checked.focus()
  expect(checked).toHaveFocus()

  await userEvent.tab()
  expect(onlyButton).toHaveFocus()
})

// M5, the mirror-image leak: a contenteditable element is tabbable in browsers
// but was absent from FOCUSABLE, so once the intercept fired it became
// permanently keyboard-unreachable - measured with one after the panel's only
// button.
test('a contenteditable element is reachable in the tab order', async () => {
  function EditableHarness() {
    return (
      <DialogShell titleId="editable-title" onDismiss={vi.fn()} panelClassName="max-w-sm">
        <h2 id="editable-title">Editable</h2>
        <button type="button">only button</button>
        <div contentEditable="true" aria-label="notes" role="textbox" />
      </DialogShell>
    )
  }
  render(<EditableHarness />)
  const onlyButton = screen.getByRole('button', { name: 'only button' })
  const editable = screen.getByRole('textbox', { name: 'notes' })
  expect(onlyButton).toHaveFocus()

  await userEvent.tab()
  expect(editable).toHaveFocus()
  await userEvent.tab()
  expect(onlyButton).toHaveFocus()
})

// L8 (code review, 2026-08-09). Plain stopPropagation() does not stop a sibling
// listener on the SAME node (document) from also running - only
// stopImmediatePropagation does. Registered AFTER the shell's own listener, to
// model any other document-level keydown consumer that might exist (UserMenu's
// Escape handler in this app, though it is also structurally prevented from
// overlapping - see the comment in DialogShell.tsx).
test('a handled Escape stops a sibling document keydown listener from also seeing it', async () => {
  const onDismiss = vi.fn()
  const sibling = vi.fn()
  render(<Harness onDismiss={onDismiss} />)
  // Registered AFTER the shell mounts (and so after its own document listener
  // is attached): listeners on the same node fire in REGISTRATION order, so
  // this is the only order in which stopImmediatePropagation can do anything -
  // it cannot un-ring a bell a listener registered EARLIER already rang, which
  // is exactly why UserMenu's own Escape handler being registered after a
  // dialog opens (only when its dropdown is toggled open) is what this
  // mechanism actually protects, not a listener that predates the dialog.
  document.addEventListener('keydown', sibling)
  try {
    await userEvent.keyboard('{Escape}')

    expect(onDismiss).toHaveBeenCalledTimes(1)
    expect(sibling).not.toHaveBeenCalled()
  } finally {
    document.removeEventListener('keydown', sibling)
  }
})

// Finding 1 (code review, 2026-08-09), option (b). The "another dialog is
// still open, park focus on the survivor" branch never actually parked
// anything when focus was OUTSIDE the closing dialog to begin with: React DOM
// captures document.activeElement before the commit and, in
// resetAfterCommit -> restoreSelection (synchronous, in the SAME commitRootImpl
// call as this cleanup, which runs during commitMutationEffects), re-focuses
// that pre-commit element if it is still connected - which it always is here,
// since the whole premise of this branch is that focus was never inside the
// dialog being removed, so its pre-commit target survives untouched. A plain
// synchronous focus() call in the cleanup gets silently overwritten a moment
// later. Proven directly with an instrumented HTMLElement.prototype.focus
// before the fix: the call log was [background-control, survivor-panel,
// background-control AGAIN], with the background control winning as the
// final activeElement. queueMicrotask runs strictly after commitRootImpl (a
// synchronous function) returns, landing after restoreSelection has already
// had its say.
test('closing a dialog whose OWN focus was never inside it still parks focus on the surviving dialog, not a background control', async () => {
  function Page() {
    const [showA, setShowA] = useState(true)
    return (
      <div>
        {/* Closes A without ever focusing anything inside A's panel - models
            focus sitting on a background control (e.g. from an earlier route)
            when a still-open lower dialog closes. */}
        <button type="button" onClick={() => setShowA(false)}>
          background button
        </button>
        {showA && (
          <DialogShell titleId="a-title" onDismiss={() => setShowA(false)}>
            <h2 id="a-title">Dialog A</h2>
            <button type="button">a-btn</button>
          </DialogShell>
        )}
        <DialogShell titleId="b-title" onDismiss={vi.fn()}>
          <h2 id="b-title">Dialog B</h2>
          <button type="button">b-btn</button>
        </DialogShell>
      </div>
    )
  }

  render(<Page />)
  const bBtn = screen.getByRole('button', { name: 'b-btn' })
  // Instrument control: B is the survivor and starts out already focused (its
  // own initial-focus effect put focus there on mount) - proves the FINAL
  // assertion is about focus surviving A's close, not about focus never
  // having moved.
  expect(bBtn).toHaveFocus()

  const bg = screen.getByRole('button', { name: 'background button', hidden: true })
  await userEvent.click(bg)

  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Dialog A' })).not.toBeInTheDocument())
  await waitFor(() =>
    expect(screen.getByRole('dialog', { name: 'Dialog B' })).toContainElement(
      document.activeElement as HTMLElement,
    ),
  )
})
