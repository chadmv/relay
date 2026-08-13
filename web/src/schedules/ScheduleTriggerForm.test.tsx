import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ScheduleTriggerForm } from './ScheduleTriggerForm'
import type { Schedule } from './api'

function sched(over: Partial<Schedule> = {}): Schedule {
  return {
    id: 's1',
    name: 'nightly-build',
    owner_id: 'o1',
    owner_email: '',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: {},
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
    ...over,
  }
}

function renderForm(over: Partial<Schedule> = {}) {
  const onSubmit = vi.fn()
  render(<ScheduleTriggerForm schedule={sched(over)} pending={false} onSubmit={onSubmit} />)
  return { onSubmit }
}

test('changing ONLY the timezone emits a patch with NO cron_expr key', async () => {
  const { onSubmit } = renderForm()
  const tz = screen.getByLabelText('Timezone')
  await userEvent.clear(tz)
  await userEvent.type(tz, 'Europe/Berlin')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(onSubmit).toHaveBeenCalledTimes(1)
  const patch = onSubmit.mock.calls[0][0]
  // toEqual on the WHOLE object, not a property check: the failure mode is an extra
  // key, and a property check cannot see one. Sending an unchanged cron_expr
  // recomputes next_run_at from time.Now() server-side
  // (internal/api/scheduled_jobs.go:585, :595), silently delaying the next fire by
  // up to a full interval.
  expect(patch).toEqual({ timezone: 'Europe/Berlin' })
  expect('cron_expr' in patch).toBe(false)
  expect('overlap_policy' in patch).toBe(false)
})

test('changing ONLY the cron emits a patch WITH cron_expr and no timezone (positive control)', async () => {
  // Without this the test above passes against a form that emits {} for everything.
  const { onSubmit } = renderForm()
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 30m')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(onSubmit.mock.calls[0][0]).toEqual({ cron_expr: '@every 30m' })
})

test('changing all three emits all three', async () => {
  const { onSubmit } = renderForm()
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@hourly')
  const tz = screen.getByLabelText('Timezone')
  await userEvent.clear(tz)
  await userEvent.type(tz, 'America/New_York')
  await userEvent.click(screen.getByRole('button', { name: 'allow' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(onSubmit.mock.calls[0][0]).toEqual({
    cron_expr: '@hourly',
    timezone: 'America/New_York',
    overlap_policy: 'allow',
  })
})

test('Save with nothing changed emits NOTHING', async () => {
  const { onSubmit } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(onSubmit).not.toHaveBeenCalled()
})

test('typing a value and typing it back emits nothing (the diff is against the row, not against dirtiness)', async () => {
  const { onSubmit } = renderForm()
  const tz = screen.getByLabelText('Timezone')
  await userEvent.clear(tz)
  await userEvent.type(tz, 'Europe/Berlin')
  await userEvent.clear(tz)
  await userEvent.type(tz, 'UTC')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  // A "hasBeenEdited" flag instead of a value comparison would fail here, and that
  // implementation would drift next_run_at on every visit to the field.
  expect(onSubmit).not.toHaveBeenCalled()
})

test('Cancel restores the loaded values so a following Save emits nothing', async () => {
  const { onSubmit } = renderForm()
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 5m')
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(cron).toHaveValue('0 2 * * *')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(onSubmit).not.toHaveBeenCalled()
})

test('the overlap control offers EXACTLY skip and allow', async () => {
  renderForm()
  expect(screen.getByRole('button', { name: 'skip' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'allow' })).toBeInTheDocument()
  // The hi-fi offers a third, `queue` (hifi3-holo-pages.jsx:1773). The server
  // rejects it with 400 'overlap_policy must be skip or allow'
  // (internal/api/scheduled_jobs.go:561-564), so it would be a control that always
  // fails. Both directions asserted: presence alone would pass against a form that
  // renders every string it can think of.
  expect(screen.queryByRole('button', { name: 'queue' })).toBeNull()
  expect(screen.getAllByRole('button', { name: /^(skip|allow|queue)$/ })).toHaveLength(2)
})

test('the current overlap value is the pressed option', () => {
  renderForm({ overlap_policy: 'allow' })
  expect(screen.getByRole('button', { name: 'allow' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'skip' })).toHaveAttribute('aria-pressed', 'false')
})

test('an obviously invalid cron is still submitted - there is NO client-side validation', async () => {
  const { onSubmit } = renderForm()
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, 'not a cron at all')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  // Deliberate: a client-side check would be a second implementation of
  // robfig/cron/v3's grammar (internal/schedrunner/cron.go:14-16) that can disagree
  // with the server. The server is the validator of record.
  expect(onSubmit).toHaveBeenCalledWith({ cron_expr: 'not a cron at all' })
})

test('a server error is rendered verbatim in an alert beside the controls', () => {
  const msg = 'schedule fires faster than minimum interval 30s (observed 1s)'
  render(
    <ScheduleTriggerForm schedule={sched()} pending={false} error={msg} onSubmit={vi.fn()} />,
  )
  // role="alert" and inside the form, not in a page-level banner: an error routed
  // away from the control that caused it can end up rendered behind other content.
  const alert = screen.getByRole('alert')
  expect(alert).toHaveTextContent(msg)
})

test('Save is disabled while a save is pending', () => {
  render(<ScheduleTriggerForm schedule={sched()} pending onSubmit={vi.fn()} />)
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
})
