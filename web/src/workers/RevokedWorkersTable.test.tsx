import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { RevokedWorkersTable } from './RevokedWorkersTable'
import type { Worker } from './api'

const revoked: Worker = {
  id: 'w1',
  name: 'gone-1',
  hostname: 'gone-1-host',
  cpu_cores: 4,
  ram_gb: 16,
  gpu_count: 0,
  gpu_model: '',
  os: 'linux',
  max_slots: 1,
  labels: null,
  status: 'revoked',
  revoked_at: '2026-01-02T03:04:05Z',
}

test('renders revoked workers with hostname and revoked time', () => {
  render(<RevokedWorkersTable workers={[revoked]} />)
  expect(screen.getByText('gone-1')).toBeInTheDocument()
  expect(screen.getByText('gone-1-host')).toBeInTheDocument()
})

test('renders an empty state when there are no revoked workers', () => {
  render(<RevokedWorkersTable workers={[]} />)
  expect(screen.getByText('No revoked workers.')).toBeInTheDocument()
})

test('the table is named, like every grid table in the app', () => {
  render(<RevokedWorkersTable workers={[revoked]} />)
  // The one native <table> in web/src. Named invisibly rather than with a caption:
  // the page already shows an h1 and a pressed section tab saying the same thing,
  // and a caption would make this the only table in the app announcing its name on
  // screen. It is also a zero-pixel change, which matters because no e2e surface
  // reaches this table.
  expect(screen.getByRole('table', { name: 'Revoked workers' })).toBeInTheDocument()
})
