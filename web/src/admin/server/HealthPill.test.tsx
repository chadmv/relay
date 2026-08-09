import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { deriveHealthPill, HealthPill } from './HealthPill'

test('loading with no data is CHECKING in the muted tone', () => {
  expect(deriveHealthPill(undefined, null)).toEqual({ text: 'CHECKING', tone: 'text-fg-mute' })
})

test('status ok is HEALTHY in the ok tone', () => {
  expect(deriveHealthPill({ status: 'ok' }, null)).toEqual({ text: 'HEALTHY', tone: 'text-ok' })
})

test('a non-ok status is reported verbatim in the warn tone, never coerced to HEALTHY', () => {
  // The pill must report what the server SAID, not assert health from a 200.
  expect(deriveHealthPill({ status: 'degraded' }, null)).toEqual({
    text: 'DEGRADED',
    tone: 'text-warn',
  })
})

test('an error is UNREACHABLE in the err tone even if stale data exists', () => {
  const err = new Error('500 boom')
  expect(deriveHealthPill(undefined, err)).toEqual({ text: 'UNREACHABLE', tone: 'text-err' })
  expect(deriveHealthPill({ status: 'ok' }, err)).toEqual({
    text: 'UNREACHABLE',
    tone: 'text-err',
  })
})

test('an empty string status renders UNKNOWN rather than an empty label', () => {
  expect(deriveHealthPill({ status: '' }, null)).toEqual({ text: 'UNKNOWN', tone: 'text-warn' })
})

test('non-printable characters are stripped from a non-ok status', () => {
  // Bidi control characters (e.g. RTL override) are stripped, not rendered verbatim.
  expect(deriveHealthPill({ status: 'degr‮aded' }, null)).toEqual({
    text: 'DEGRADED',
    tone: 'text-warn',
  })
})

test('the dot is a separate node so the label is assertable on its own', () => {
  render(<HealthPill data={{ status: 'ok' }} error={null} />)
  expect(screen.getByText('HEALTHY')).toBeInTheDocument()
})

test('renders UNREACHABLE from an error', () => {
  render(<HealthPill data={undefined} error={new Error('500 boom')} />)
  expect(screen.getByText('UNREACHABLE')).toBeInTheDocument()
  expect(screen.queryByText('HEALTHY')).not.toBeInTheDocument()
})

test('the wrapper announces state transitions to assistive tech', () => {
  render(<HealthPill data={{ status: 'ok' }} error={null} />)
  const status = screen.getByRole('status')
  expect(status).toHaveAttribute('aria-live', 'polite')
  expect(status).toHaveTextContent('HEALTHY')
})
