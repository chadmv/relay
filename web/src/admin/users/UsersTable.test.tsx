import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { UsersTable } from './UsersTable'
import type { AdminUser, UserSort } from './api'

function user(over: Partial<AdminUser> = {}): AdminUser {
  return {
    id: 'u1',
    email: 'ada@studio.dev',
    name: 'Ada',
    is_admin: false,
    created_at: '2026-08-01T12:00:00Z',
    archived_at: null,
    ...over,
  }
}

function renderTable(over: Partial<Parameters<typeof UsersTable>[0]> = {}) {
  const props = {
    users: [user()],
    sort: '-created_at' as UserSort,
    onSort: vi.fn(),
    showArchived: false,
    currentUserId: 'me',
    busy: false,
    onRename: vi.fn(),
    onResetPassword: vi.fn(),
    onArchive: vi.fn(),
    onUnarchive: vi.fn(),
    ...over,
  }
  return { props, ...render(<UsersTable {...props} />) }
}

test('renders email, name, created date, and the avatar initial', () => {
  renderTable()
  expect(screen.getByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('Ada')).toBeInTheDocument()
  expect(screen.getByText('2026-08-01')).toBeInTheDocument()
  expect(screen.getByText('A')).toBeInTheDocument()
})

test('the role pill reads ADMIN or USER and nothing else', () => {
  renderTable({ users: [user({ is_admin: true }), user({ id: 'u2', email: 'b@c.dev', is_admin: false })] })
  expect(screen.getByText('ADMIN')).toBeInTheDocument()
  expect(screen.getByText('USER')).toBeInTheDocument()
  expect(screen.queryByText(/service/i)).not.toBeInTheDocument()
})

test('renders no SESSIONS or LAST LOGIN column (no backend for either)', () => {
  renderTable()
  expect(screen.queryByText('SESSIONS')).not.toBeInTheDocument()
  expect(screen.queryByText('LAST LOGIN')).not.toBeInTheDocument()
  expect(screen.queryByText(/active$/)).not.toBeInTheDocument()
})

test('active rows offer Reset pw, Rename, and Archive', () => {
  renderTable()
  expect(screen.getByRole('button', { name: 'Reset pw' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Rename' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Archive' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Unarchive' })).not.toBeInTheDocument()
})

test('the Archive button is absent on the acting admin own row', () => {
  renderTable({ currentUserId: 'u1' })
  expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Rename' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Reset pw' })).toBeInTheDocument()
})

test('with showArchived on, an archived row is dimmed and offers only Unarchive', () => {
  const { container } = renderTable({
    showArchived: true,
    users: [user({ archived_at: '2026-08-02T00:00:00Z' })],
  })
  expect(screen.getByRole('button', { name: 'Unarchive' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Rename' })).not.toBeInTheDocument()
  expect(container.querySelector('.opacity-\\[0\\.55\\]')).not.toBeNull()
})

test('with showArchived off, a stray archived_at does not archive the row', () => {
  // internal/api/users.go:111-132 sends a null archived_at in active-only mode, so
  // "archived" must be gated on the toggle, never inferred from the field alone.
  renderTable({ showArchived: false, users: [user({ archived_at: '2026-08-02T00:00:00Z' })] })
  expect(screen.getByRole('button', { name: 'Archive' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Unarchive' })).not.toBeInTheDocument()
})

test('clicking a sortable header calls onSort with that field', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: /^EMAIL/ }))
  expect(props.onSort).toHaveBeenCalledWith('email')
  await userEvent.click(screen.getByRole('button', { name: /^NAME/ }))
  expect(props.onSort).toHaveBeenCalledWith('name')
  await userEvent.click(screen.getByRole('button', { name: /^CREATED/ }))
  expect(props.onSort).toHaveBeenCalledWith('created_at')
})

test('exposes aria-sort on the active column and "none" on the others', () => {
  renderTable({ sort: 'email' })
  expect(screen.getByRole('columnheader', { name: /EMAIL/ })).toHaveAttribute('aria-sort', 'ascending')
  expect(screen.getByRole('columnheader', { name: /NAME/ })).toHaveAttribute('aria-sort', 'none')
})

test('descending sort shows a descending caret', () => {
  renderTable({ sort: '-name' })
  expect(screen.getByRole('button', { name: 'NAME ▼' })).toBeInTheDocument()
})

test('Rename turns the name cell into an input and submits the trimmed value', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
  const input = screen.getByLabelText('Name for ada@studio.dev')
  await userEvent.clear(input)
  await userEvent.type(input, '  Ada L  ')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(props.onRename).toHaveBeenCalledWith('u1', 'Ada L')
  expect(screen.queryByLabelText('Name for ada@studio.dev')).not.toBeInTheDocument()
})

test('Cancel leaves the rename without calling onRename', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onRename).not.toHaveBeenCalled()
  expect(screen.getByText('Ada')).toBeInTheDocument()
})

test('an empty rename is not submitted', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
  await userEvent.clear(screen.getByLabelText('Name for ada@studio.dev'))
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(props.onRename).not.toHaveBeenCalled()
})

test('row actions fire their callbacks with the row user', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: 'Reset pw' }))
  expect(props.onResetPassword).toHaveBeenCalledWith(props.users[0])
  await userEvent.click(screen.getByRole('button', { name: 'Archive' }))
  expect(props.onArchive).toHaveBeenCalledWith(props.users[0])
})

test('busy disables every row action', () => {
  renderTable({ busy: true })
  expect(screen.getByRole('button', { name: 'Reset pw' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Rename' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Archive' })).toBeDisabled()
})
