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
