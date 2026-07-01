import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { Eyebrow } from './Eyebrow'

test('renders children with the mono uppercase eyebrow classes', () => {
  render(<Eyebrow>fleet</Eyebrow>)
  const el = screen.getByText('fleet')
  expect(el).toHaveClass('font-mono', 'uppercase', 'text-fg-mute', 'tracking-[0.18em]')
})

test('merges a caller className for the section-label variant', () => {
  render(<Eyebrow className="text-[10px] tracking-[0.16em]">labels</Eyebrow>)
  expect(screen.getByText('labels')).toHaveClass('text-[10px]', 'tracking-[0.16em]')
})
