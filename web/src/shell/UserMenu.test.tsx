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

test('calls onLogout when Log out is clicked', async () => {
  const onLogout = renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.click(screen.getByText('Log out'))
  expect(onLogout).toHaveBeenCalledOnce()
})
