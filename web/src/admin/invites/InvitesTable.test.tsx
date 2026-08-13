import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { InvitesTable } from './InvitesTable'
import type { Invite, InviteSort } from './api'

const NOW = new Date('2026-08-09T12:00:00Z')
const CREATOR_UUID = '11111111-2222-3333-4444-555555555555'

function row(over: Partial<Invite> = {}): Invite {
  return {
    id: 'i1',
    created_at: '2026-08-01T09:00:00Z',
    expires_at: '2026-08-10T09:00:00Z',
    created_by: CREATOR_UUID,
    created_by_email: 'admin@studio.dev',
    email: 'invitee@studio.dev',
    ...over,
  }
}

function renderTable(over: Partial<Parameters<typeof InvitesTable>[0]> = {}) {
  const props = {
    invites: [row()],
    sort: '-created_at' as InviteSort,
    onSort: vi.fn(),
    now: NOW,
    ...over,
  }
  return { props, ...render(<InvitesTable {...props} />) }
}

test('renders all six headers', () => {
  renderTable()
  // CREATED is checked separately and anchored: the default sort is
  // -created_at, so its rendered text is "CREATED ▼" (the caret), and an
  // unanchored /CREATED/ also matches the "CREATED BY" columnheader - the two
  // must be told apart, not merely found.
  for (const label of ['BINDS TO', 'EXPIRES', 'CREATED BY', 'STATUS', 'NOTE']) {
    expect(screen.getByRole('columnheader', { name: new RegExp(label) })).toBeInTheDocument()
  }
  expect(screen.getByRole('columnheader', { name: /^CREATED ▼$/ })).toBeInTheDocument()
})

test('only CREATED and EXPIRES are sortable; the other four carry no aria-sort', () => {
  renderTable()
  expect(screen.getByRole('columnheader', { name: /CREATED BY/ })).not.toHaveAttribute('aria-sort')
  expect(screen.getByRole('columnheader', { name: /BINDS TO/ })).not.toHaveAttribute('aria-sort')
  expect(screen.getByRole('columnheader', { name: /STATUS/ })).not.toHaveAttribute('aria-sort')
  expect(screen.getByRole('columnheader', { name: /NOTE/ })).not.toHaveAttribute('aria-sort')
  // Anchored and exact: "CREATED BY" is a separate, non-sortable columnheader
  // that also starts with "CREATED ", so an unanchored /^CREATED / would match
  // both and throw on multiple elements found.
  expect(screen.getByRole('columnheader', { name: /^CREATED ▼$/ })).toHaveAttribute(
    'aria-sort',
    'descending',
  )
  expect(screen.getByRole('columnheader', { name: /EXPIRES/ })).toHaveAttribute('aria-sort', 'none')
  expect(screen.getByRole('button', { name: 'CREATED ▼' })).toBeInTheDocument()
})

test('ascending sort shows an ascending caret', () => {
  renderTable({ sort: 'expires_at' })
  expect(screen.getByRole('columnheader', { name: /EXPIRES/ })).toHaveAttribute(
    'aria-sort',
    'ascending',
  )
  expect(screen.getByRole('button', { name: 'EXPIRES ▲' })).toBeInTheDocument()
})

test('clicking a sortable header calls onSort with that field', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: /^CREATED/ }))
  expect(props.onSort).toHaveBeenCalledWith('created_at')
  await userEvent.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  expect(props.onSort).toHaveBeenCalledWith('expires_at')
})

test('a bound invite shows the email; an unbound one shows a plain ASCII hyphen', () => {
  renderTable()
  expect(screen.getByText('invitee@studio.dev')).toBeInTheDocument()

  const { email: _drop, ...unbound } = row()
  render(<InvitesTable invites={[unbound]} sort="-created_at" onSort={vi.fn()} now={NOW} />)
  // The key is ABSENT, not null (internal/api/invites.go:139-141), so the cell
  // renders a placeholder that means "not bound to an address" - a real state, not
  // missing data. House rule: ASCII hyphen, never the hi-fi's em dash (:2111).
  expect(screen.getAllByText('-').length).toBeGreaterThan(0)
  expect(screen.queryByText(/undefined/)).not.toBeInTheDocument()
  expect(screen.queryByText(/null/)).not.toBeInTheDocument()
  expect(screen.queryByText('—')).not.toBeInTheDocument()
})

test('CREATED BY renders the joined email and never the raw creator UUID', () => {
  renderTable()
  expect(screen.getByText('admin@studio.dev')).toBeInTheDocument()
  // The list query joins users (internal/store/query/invites.sql:32), which is the
  // one hi-fi column the enrollments table could not fill. A 36-character UUID
  // would be unusable, so created_by is carried on the type but never rendered.
  expect(screen.queryByText(CREATOR_UUID)).not.toBeInTheDocument()
})

test('the created date renders as a plain YYYY-MM-DD slice', () => {
  renderTable()
  expect(screen.getByText('2026-08-01')).toBeInTheDocument()
})

test('the status pill renders all four derivable states', () => {
  renderTable({
    invites: [
      row({ id: 'a', expires_at: '2026-08-10T12:00:00Z' }), // 24h left
      row({ id: 'b', expires_at: '2026-08-09T12:30:00Z' }), // 30m left
      row({ id: 'c', expires_at: '2026-08-09T11:00:00Z' }), // past
      row({ id: 'd', used_at: '2026-08-02T10:00:00Z' }),
    ],
  })
  expect(screen.getByText('ACTIVE')).toBeInTheDocument()
  expect(screen.getByText('EXPIRING')).toBeInTheDocument()
  expect(screen.getByText('EXPIRED')).toBeInTheDocument()
  expect(screen.getByText('REDEEMED')).toBeInTheDocument()
})

test('the NOTE cell reads three different ways', () => {
  renderTable({
    invites: [
      row({ id: 'a', expires_at: '2026-08-10T12:00:00Z' }),
      row({ id: 'c', expires_at: '2026-08-09T11:00:00Z' }),
      row({ id: 'd', used_at: '2026-08-02T10:00:00Z' }),
    ],
  })
  expect(screen.getByText('copy token only on creation')).toBeInTheDocument()
  // The ONLY consumer of used_at's VALUE rather than its presence.
  expect(screen.getByText('redeemed 2026-08-02')).toBeInTheDocument()
  expect(screen.getAllByText('-').length).toBeGreaterThan(0)
})

test('terminal rows are dimmed and active rows are not', () => {
  const { container } = renderTable({
    invites: [
      row({ id: 'a', expires_at: '2026-08-10T12:00:00Z' }),
      row({ id: 'c', expires_at: '2026-08-09T11:00:00Z' }),
      row({ id: 'd', used_at: '2026-08-02T10:00:00Z' }),
    ],
  })
  const rows = Array.from(container.querySelectorAll('[role="row"]')).slice(1) // drop the header row
  expect(rows[0].className).not.toContain('opacity-[0.55]')
  expect(rows[1].className).toContain('opacity-[0.55]')
  expect(rows[2].className).toContain('opacity-[0.55]')
})

test('there is no revoke, delete or resend control anywhere in the table', () => {
  renderTable({ invites: [row(), row({ id: 'i2' })] })
  expect(screen.queryAllByRole('button', { name: /revoke|delete|resend/i })).toHaveLength(0)
  // The only buttons in the table are the two sortable headers - two rows of data
  // add none. There is no DELETE, PATCH or resend route for invites
  // (internal/api/server.go:142-143 is the whole surface), so a control would be a
  // guaranteed-failing dead affordance.
  expect(screen.getAllByRole('button')).toHaveLength(2)
  expect(screen.queryByText('ACTIONS')).not.toBeInTheDocument()
})

test('the absence query above is NOT vacuous: it finds such a button when one exists', () => {
  // Without this control the assertion above would also pass against a table that
  // renders no buttons at all for an unrelated reason, or against a query whose
  // regex never matches anything.
  render(
    <>
      <InvitesTable invites={[row()]} sort="-created_at" onSort={vi.fn()} now={NOW} />
      <button type="button">Revoke invite</button>
    </>,
  )
  expect(screen.queryAllByRole('button', { name: /revoke|delete|resend/i })).toHaveLength(1)
})

test('no token, hash or prefix is rendered - no cell holds a 64-hex string', () => {
  const { container } = renderTable()
  expect(screen.queryByText('TOKEN PREFIX')).not.toBeInTheDocument()
  expect(container.textContent).not.toMatch(/[0-9a-f]{64}/i)
})

test('a different now re-derives the pill and the label from the same row', () => {
  renderTable({ now: new Date('2026-08-10T08:59:30Z') }) // 30s before expiry
  expect(screen.getByText('EXPIRING')).toBeInTheDocument()
  // Not "in 30s": the table's `now` comes from a 60s clock tick (useNow), so a
  // seconds-precision label would go stale for up to 59 more real seconds after
  // the render that produced it - see inviteStatus.formatExpiryLabel.
  expect(screen.getByText('in <1m')).toBeInTheDocument()
})
