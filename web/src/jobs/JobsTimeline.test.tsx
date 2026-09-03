import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { formatDateTime } from '../lib/time'
import { JobsTimeline } from './JobsTimeline'
import type { Job } from './api'
import type { TimelineState } from './useJobTimeline'
import type { TimelineWindow } from './timelineWindow'

const SINCE = '2026-09-01T12:00:00.000Z'
const UNTIL = '2026-09-02T12:00:00.000Z'

function job(over: Partial<Job> = {}): Job {
  return {
    id: 'AAAAAA',
    name: 'shot-042 render',
    priority: 'normal',
    status: 'running',
    labels: null,
    created_at: '2026-09-02T00:00:00.000Z',
    started_at: '2026-09-02T00:00:00.000Z',
    updated_at: '2026-09-02T06:00:00.000Z',
    total_tasks: 4,
    done_tasks: 2,
    ...over,
  }
}

function state(over: Partial<TimelineState> = {}): TimelineState {
  return {
    jobs: [],
    total: 0,
    truncated: false,
    sinceIso: SINCE,
    untilIso: UNTIL,
    isLoading: false,
    isFetching: false,
    error: null,
    refetch: () => {},
    ...over,
  }
}

function renderTimeline(
  over: Partial<TimelineState> = {},
  props: {
    window?: TimelineWindow
    filtering?: boolean
    onChooseWindow?: (w: TimelineWindow) => void
    onOpenTable?: () => void
  } = {},
) {
  return render(
    <MemoryRouter>
      <JobsTimeline
        state={state(over)}
        window={props.window ?? '24h'}
        filtering={props.filtering ?? false}
        onChooseWindow={props.onChooseWindow ?? (() => {})}
        onOpenTable={props.onOpenTable ?? (() => {})}
      />
    </MemoryRouter>,
  )
}

test('each row links to its job and states its status in text', () => {
  const { container } = renderTimeline({ jobs: [job()], total: 1 })
  const items = screen.getAllByRole('listitem')
  expect(items).toHaveLength(1)

  const link = within(items[0]).getByRole('link', { name: 'shot-042 render' })
  expect(link).toHaveAttribute('href', '/jobs/AAAAAA')
  // The row's ONLY tab stop. A per-bar stop would double the tab count and
  // expose nothing the row text does not already carry.
  expect(within(items[0]).getAllByRole('link')).toHaveLength(1)
  expect(items[0].querySelectorAll('button, [tabindex]')).toHaveLength(0)

  // The geometry is a positional summary. Every exact number is in the text, so
  // a bar is never the only carrier of a fact.
  expect(items[0].textContent).toMatch(/running/i)
  expect(items[0].textContent).toMatch(/50%/)

  // Through a data attribute, never a class name: a class-shaped string in a test
  // file is compiled input for the stylesheet scanner.
  expect(container.querySelector('[data-live-dot]')).not.toBeNull()
})

test('a job that never started says so rather than drawing a short one', () => {
  const { container } = renderTimeline({
    jobs: [job({ status: 'pending', started_at: undefined, name: 'frames teaser' })],
    total: 1,
  })
  const item = screen.getAllByRole('listitem')[0]
  expect(item.textContent).toMatch(/not started/i)
  expect(container.querySelector('[data-instant="true"]')).not.toBeNull()
  expect(container.querySelector('[data-live-dot]')).toBeNull()
})

test('the timeline describes its axis in text', () => {
  renderTimeline({ jobs: [job()], total: 1 })
  const region = screen.getByRole('region', { name: 'Jobs timeline' })
  const text = region.textContent ?? ''
  // CREATED, not a bare "the last 24 hours". since and until bound created_at,
  // so a job submitted ten days ago and still running is not here - a genuine
  // limit of the only predicate the server offers, and saying "the last 24
  // hours" would be a wrong-prose defect on a correct implementation.
  expect(text).toMatch(/created/i)
  expect(text).toMatch(/24 hours/)
  expect(text).toMatch(/newest first/i)
  expect(text).toContain(formatDateTime(SINCE))
  expect(text).toContain(formatDateTime(UNTIL))
})

test('a truncated window offers the next shorter one', async () => {
  const chosen: TimelineWindow[] = []
  renderTimeline(
    { jobs: [job()], total: 4210, truncated: true },
    { window: '7d', onChooseWindow: (w) => chosen.push(w) },
  )
  expect(screen.getByText(/most recent of 4,210/)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Show last 24 hours' }))
  expect(chosen).toEqual(['24h'])
})

test('a truncated shortest window offers the table instead', async () => {
  let opened = 0
  renderTimeline(
    { jobs: [job()], total: 4210, truncated: true },
    {
      window: '6h',
      onOpenTable: () => {
        opened++
      },
    },
  )
  // Nothing narrower exists, so the paged table is the only surface that can
  // show all of them. This is the branch most likely to be forgotten and the
  // only one where the view cannot fix itself.
  expect(screen.queryByRole('button', { name: /show last/i })).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'Open the Table view' }))
  expect(opened).toBe(1)
})

test('an empty window says no jobs were created in it', () => {
  renderTimeline({ jobs: [], total: 0 })
  expect(screen.getByText('No jobs were created in the last 24 hours.')).toBeInTheDocument()
})

test('an empty filtered window says nothing matched instead', () => {
  renderTimeline({ jobs: [], total: 0 }, { filtering: true })
  expect(screen.getByText('No jobs match those filters in the last 24 hours.')).toBeInTheDocument()
})

test('a failed walk shows one error with retry', async () => {
  let retried = 0
  renderTimeline({
    jobs: [],
    error: new Error('list jobs failed'),
    refetch: () => {
      retried++
    },
  })
  expect(screen.getByText('list jobs failed')).toBeInTheDocument()
  // ONE error for the whole view, unlike a lane. A partial walk drawn under the
  // window's own label is a chart that lies.
  expect(screen.queryAllByRole('listitem')).toHaveLength(0)
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  expect(retried).toBe(1)
})
