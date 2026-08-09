import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { ADMIN_TABS, DEFAULT_ADMIN_TAB, findAdminTab } from './tabs'
import { AdminTabs } from './AdminTabs'

function renderTabs(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AdminTabs />
    </MemoryRouter>,
  )
}

test('the registry holds exactly the built tabs', () => {
  expect(ADMIN_TABS.map((t) => t.slug)).toEqual(['users'])
  expect(DEFAULT_ADMIN_TAB).toBe('users')
})

test('findAdminTab resolves a known slug and rejects everything else', () => {
  expect(findAdminTab('users')?.label).toBe('Users')
  expect(findAdminTab('invites')).toBeUndefined()
  expect(findAdminTab('bogus')).toBeUndefined()
  expect(findAdminTab(undefined)).toBeUndefined()
})

test('renders one link per registry entry, pointing at /admin/<slug>', () => {
  renderTabs('/admin/users')
  expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('href', '/admin/users')
  expect(screen.getAllByRole('link')).toHaveLength(1)
})

test('the current tab is marked as the current page', () => {
  renderTabs('/admin/users')
  expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('aria-current', 'page')
})

test('tabs that are not built yet are absent', () => {
  renderTabs('/admin/users')
  for (const label of ['Invites', 'Agent enrolls', 'Reservations', 'Server']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
})
