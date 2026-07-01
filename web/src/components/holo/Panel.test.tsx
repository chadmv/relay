import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { Panel } from './Panel'

test('renders the title, optional meta, and body', () => {
  render(
    <Panel title="Source workspaces" meta="2 OF 4 SLOTS">
      <div>body content</div>
    </Panel>,
  )
  expect(screen.getByText('Source workspaces')).toHaveClass('text-[13px]', 'text-fg')
  expect(screen.getByText('2 OF 4 SLOTS')).toHaveClass('font-mono', 'text-[10px]', 'text-fg-mute')
  expect(screen.getByText('body content')).toBeInTheDocument()
})

test('omits the footer when not provided', () => {
  render(
    <Panel title="Labels">
      <div>b</div>
    </Panel>,
  )
  expect(screen.queryByText('endnote')).toBeNull()
})

test('renders a footer endnote when provided', () => {
  render(
    <Panel title="Utilization" footer={<span>endnote</span>}>
      <div>b</div>
    </Panel>,
  )
  expect(screen.getByText('endnote')).toBeInTheDocument()
})

test('applies bodyClassName to the body wrapper', () => {
  render(
    <Panel title="t" bodyClassName="p-4">
      <div>body</div>
    </Panel>,
  )
  expect(screen.getByText('body').parentElement).toHaveClass('p-4')
})
