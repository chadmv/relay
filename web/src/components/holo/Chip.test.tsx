import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { Chip } from './Chip'

test('defaults to the accent tone', () => {
  render(<Chip>pool=render</Chip>)
  const el = screen.getByText('pool=render')
  expect(el).toHaveClass('rounded-full', 'border-accent/40', 'bg-accent/10', 'text-accent')
})

test('muted tone uses the border/muted palette', () => {
  render(<Chip tone="muted">HELD</Chip>)
  expect(screen.getByText('HELD')).toHaveClass('border-border', 'text-fg-mute')
})

test('warn tone uses the warn palette', () => {
  render(<Chip tone="warn">draining</Chip>)
  expect(screen.getByText('draining')).toHaveClass('border-warn/40', 'bg-warn/10', 'text-warn')
})

test('dashed renders a dashed transparent affordance', () => {
  render(<Chip dashed>+ add label</Chip>)
  expect(screen.getByText('+ add label')).toHaveClass('border-dashed', 'bg-transparent', 'cursor-pointer')
})

test('is a button when onClick is provided and fires it', async () => {
  const onClick = vi.fn()
  render(<Chip onClick={onClick}>EVICT</Chip>)
  const el = screen.getByRole('button', { name: 'EVICT' })
  await userEvent.click(el)
  expect(onClick).toHaveBeenCalledOnce()
})

test('is a span when no onClick is provided', () => {
  render(<Chip>label</Chip>)
  expect(screen.getByText('label').tagName).toBe('SPAN')
})
