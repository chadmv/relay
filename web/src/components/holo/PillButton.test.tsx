import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { PillButton } from './PillButton'

test('defaults to the ghost variant and pill base classes', () => {
  render(<PillButton>Drain</PillButton>)
  const el = screen.getByRole('button', { name: 'Drain' })
  expect(el).toHaveClass('rounded-full', 'border', 'border-border', 'bg-white/5', 'text-fg')
})

test('primary variant uses the accent gradient', () => {
  render(<PillButton variant="primary">Save</PillButton>)
  expect(screen.getByRole('button', { name: 'Save' })).toHaveClass('from-accent', 'to-accent-b', 'text-bg')
})

test('danger variant uses the err palette', () => {
  render(<PillButton variant="danger">Revoke</PillButton>)
  expect(screen.getByRole('button', { name: 'Revoke' })).toHaveClass('border-err/50', 'bg-err/10', 'text-err')
})

test('forwards standard button attributes (onClick, disabled)', async () => {
  const onClick = vi.fn()
  render(
    <PillButton onClick={onClick} disabled>
      Edit
    </PillButton>,
  )
  const el = screen.getByRole('button', { name: 'Edit' })
  expect(el).toBeDisabled()
  await userEvent.click(el)
  expect(onClick).not.toHaveBeenCalled()
})

test('merges a caller className', () => {
  render(<PillButton className="ml-2">X</PillButton>)
  expect(screen.getByRole('button', { name: 'X' })).toHaveClass('ml-2')
})
