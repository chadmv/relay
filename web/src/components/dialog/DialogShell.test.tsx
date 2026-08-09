import { useRef } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'
import { DialogShell } from './DialogShell'
import { __resetForTest } from './dialogStack'

afterEach(() => __resetForTest())

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
