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

test('publishes a string title as data-panel-title and omits it for a node title', () => {
  // An inert hook. It exists so a page test can walk a table up to its own
  // panel and compare the RENDERED title with the RENDERED accessible name. A test
  // asserting "both sites use the same imported constant" cannot fail, because they
  // are the same symbol.
  const { container, rerender } = render(
    <Panel title="Source workspaces">
      <div>b</div>
    </Panel>,
  )
  expect(container.firstElementChild).toHaveAttribute('data-panel-title', 'Source workspaces')
  rerender(
    <Panel title={<span>Source workspaces</span>}>
      <div>b</div>
    </Panel>,
  )
  expect(container.firstElementChild).not.toHaveAttribute('data-panel-title')
})
