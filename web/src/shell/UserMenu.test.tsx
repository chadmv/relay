import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { UserMenu } from './UserMenu'

function renderMenu(onLogout = vi.fn()) {
  render(
    <MemoryRouter>
      <UserMenu email="ada@studio.dev" onLogout={onLogout} />
    </MemoryRouter>
  )
  return onLogout
}

test('opens and closes on outside click', async () => {
  renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  expect(screen.getByText('Log out')).toBeInTheDocument()
  await userEvent.click(document.body)
  expect(screen.queryByText('Log out')).not.toBeInTheDocument()
})

test('closes on Escape', async () => {
  renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByText('Log out')).not.toBeInTheDocument()
})

test('exposes disclosure semantics and reflects open state via aria attributes', async () => {
  renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  expect(toggle).not.toHaveAttribute('aria-haspopup')
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await userEvent.click(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
})

// The dropdown floats over live page content, so it needs a surface of its own.
// GlassPanel sets no background-color at all - only a 6%->2% white gradient -
// which is right on the page floor and see-through over content. The prototype's
// own menu overrides the glass background for exactly this reason
// (hifi3-holo-pages.jsx:245-249). z-50 is asserted alongside it as the local
// ordering inside the header; the fix for the reported bleed-through lives in
// HoloShell, and HoloShell.test.tsx pins it.
test('the dropdown is an opaque overlay that paints above page content', async () => {
  renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  expect(screen.getByTestId('user-menu-panel')).toHaveClass('z-50', 'bg-popover')
})

test('calls onLogout when Log out is clicked', async () => {
  const onLogout = renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.click(screen.getByText('Log out'))
  expect(onLogout).toHaveBeenCalledOnce()
})

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
