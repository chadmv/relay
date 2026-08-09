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
  expect(ADMIN_TABS.map((t) => t.slug)).toEqual([
    'users',
    'enrollments',
    'reservations',
    'server',
  ])
  expect(DEFAULT_ADMIN_TAB).toBe('users')
})

test('findAdminTab resolves a known slug and rejects everything else', () => {
  expect(findAdminTab('users')?.label).toBe('Users')
  expect(findAdminTab('enrollments')?.label).toBe('Agent enrolls')
  expect(findAdminTab('reservations')?.label).toBe('Reservations')
  expect(findAdminTab('server')?.label).toBe('Server')
  expect(findAdminTab('invites')).toBeUndefined()
  expect(findAdminTab('bogus')).toBeUndefined()
  expect(findAdminTab(undefined)).toBeUndefined()
})

test('renders one link per registry entry, pointing at /admin/<slug>', () => {
  renderTabs('/admin/users')
  expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('href', '/admin/users')
  expect(screen.getByRole('link', { name: 'Agent enrolls' })).toHaveAttribute(
    'href',
    '/admin/enrollments',
  )
  expect(screen.getByRole('link', { name: 'Reservations' })).toHaveAttribute(
    'href',
    '/admin/reservations',
  )
  expect(screen.getByRole('link', { name: 'Server' })).toHaveAttribute('href', '/admin/server')
  expect(screen.getAllByRole('link')).toHaveLength(4)
})

test('the current tab is marked as the current page', () => {
  renderTabs('/admin/users')
  expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('aria-current', 'page')
})

test('the enrollments tab is marked current on its own route', () => {
  renderTabs('/admin/enrollments')
  expect(screen.getByRole('link', { name: 'Agent enrolls' })).toHaveAttribute('aria-current', 'page')
  expect(screen.getByRole('link', { name: 'Users' })).not.toHaveAttribute('aria-current')
})

test('the reservations tab is marked current on its own route', () => {
  renderTabs('/admin/reservations')
  expect(screen.getByRole('link', { name: 'Reservations' })).toHaveAttribute(
    'aria-current',
    'page',
  )
  expect(screen.getByRole('link', { name: 'Users' })).not.toHaveAttribute('aria-current')
})

test('tabs that are not built yet are absent', () => {
  renderTabs('/admin/users')
  expect(screen.queryByText('Invites')).not.toBeInTheDocument()
})
