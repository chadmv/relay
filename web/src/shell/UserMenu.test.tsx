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

test('exposes menu semantics and reflects open state via aria attributes', async () => {
  renderMenu()
  const toggle = screen.getByRole('button', { name: /ada@studio.dev/i })
  expect(toggle).toHaveAttribute('aria-haspopup', 'menu')
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
