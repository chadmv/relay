import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import type { Job } from './api'
import { JobsLanes } from './JobsLanes'
import type { LaneState } from './useJobLanes'

// LaneState is this component's OWN input contract, so building it here simulates
// nothing on the wire and is not the vacuous-fixture case. The wire fixtures live
// in useJobLanes.test.tsx and JobsPage.lanes.test.tsx, hand-written there.
function job(id: string, name: string): Job {
  return {
    id,
    name,
    priority: 'normal',
    status: 'running',
    labels: null,
    created_at: '2026-06-05T10:00:00Z',
    updated_at: '2026-06-05T10:00:00Z',
    total_tasks: 4,
    done_tasks: 3,
  }
}

function lane(over: Partial<LaneState> & { status: LaneState['status'] }): LaneState {
  return { items: [], total: null, isLoading: false, isFetching: false, error: null, refetch: () => {}, ...over }
}

// Five lanes whose requests SUCCEEDED and returned nothing. total 0 is a real
// answer and is stated here; the helper's own default is null, which is the
// different state of having no answer at all.
function fiveEmpty(): LaneState[] {
  return [
    lane({ status: 'pending', total: 0 }),
    lane({ status: 'running', total: 0 }),
    lane({ status: 'done', total: 0 }),
    lane({ status: 'failed', total: 0 }),
    lane({ status: 'cancelled', total: 0 }),
  ]
}

function renderLanes(
  lanes: LaneState[],
  onShowAll: (s: Job['status']) => void = () => {},
  opts: { filtering?: boolean } = {},
) {
  return render(
    <MemoryRouter>
      <JobsLanes lanes={lanes} onShowAll={onShowAll} filtering={opts.filtering} />
    </MemoryRouter>,
  )
}

// Case-insensitive throughout: the heading is uppercased by CSS, which jsdom does
// not apply and Chromium reflects in the accessible name.
function region(name: string) {
  return screen.getByRole('region', { name: new RegExp(`^${name}$`, 'i') })
}

test('each lane is a labelled region with a heading, and each card links to its job', () => {
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', items: [job('A1', 'ingest frames')], total: 1 })
  lanes[1] = lane({ status: 'running', items: [job('B2', 'shot-042 render')], total: 1 })
  renderLanes(lanes)

  for (const name of ['Queued', 'Running', 'Done', 'Failed', 'Cancelled']) {
    expect(within(region(name)).getByRole('heading', { level: 2 })).toBeInTheDocument()
  }
  const card = within(region('Queued')).getByRole('link', { name: /ingest frames/ })
  expect(card).toHaveAttribute('href', '/jobs/A1')
  // Progress is exposed as text, not carried by the bar's width alone.
  expect(card).toHaveTextContent('3/4 tasks, 75%')
  expect(within(region('Queued')).getByText('1 total')).toBeInTheDocument()
})

test('an empty lane keeps its header and shows no jobs, with no skeleton and no error', () => {
  renderLanes(fiveEmpty())
  const queued = region('Queued')
  expect(within(queued).getByText('0 total')).toBeInTheDocument()
  expect(within(queued).getByText('No jobs')).toBeInTheDocument()
  expect(within(queued).queryByRole('button', { name: /retry/i })).toBeNull()
  expect(within(queued).queryByRole('list')).toBeNull()
})

test('a failing lane renders its own error and Retry while the others keep their rows', async () => {
  const refetch = vi.fn()
  renderLanes([
    lane({ status: 'pending', items: [job('A1', 'ingest frames')], total: 1 }),
    lane({ status: 'running', items: [job('B1', 'shot render')], total: 1 }),
    lane({ status: 'done', items: [job('C1', 'nightly etl')], total: 1 }),
    lane({ status: 'failed', error: new Error('list jobs failed'), refetch }),
    lane({ status: 'cancelled', items: [job('D1', 'aborted bake')], total: 1 }),
  ])

  const failed = region('Failed')
  expect(within(failed).getByText('list jobs failed')).toBeInTheDocument()
  // No response means no count. Rendering '0 total' beside the heading would
  // state that this status is empty, which is a wrong answer, not a missing one.
  expect(within(failed).queryByText('0 total')).toBeNull()
  await userEvent.click(within(failed).getByRole('button', { name: /retry/i }))
  expect(refetch).toHaveBeenCalledTimes(1)

  for (const [name, jobName] of [
    ['Queued', 'ingest frames'],
    ['Running', 'shot render'],
    ['Done', 'nightly etl'],
    ['Cancelled', 'aborted bake'],
  ] as const) {
    const r = region(name)
    expect(within(r).getByRole('link', { name: new RegExp(jobName) })).toBeInTheDocument()
    expect(within(r).queryByRole('button', { name: /retry/i })).toBeNull()
  }
})

test('a loading lane shows skeletons, not an empty message', () => {
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', isLoading: true })
  renderLanes(lanes)
  expect(within(region('Queued')).queryByText('No jobs')).toBeNull()
  expect(within(region('Running')).getByText('No jobs')).toBeInTheDocument()
  // The control for the absence assertions elsewhere: the header is still there
  // in the no-count state, so a passing queryByText('0 total') cannot be a header
  // that rendered nothing at all.
  expect(within(region('Queued')).getByText('-')).toBeInTheDocument()
})

test('overflow shows total minus shown, and is absent when nothing is hidden', async () => {
  const onShowAll = vi.fn()
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', items: [job('A1', 'a'), job('A2', 'b')], total: 490 })
  lanes[1] = lane({ status: 'running', items: [job('B1', 'c')], total: 1 })
  renderLanes(lanes, onShowAll)

  const more = within(region('Queued')).getByRole('button', { name: '+ 488 more' })
  expect(within(region('Running')).queryByRole('button', { name: /more/i })).toBeNull()
  await userEvent.click(more)
  expect(onShowAll).toHaveBeenCalledWith('pending')
})

test('a lane with a count but no rows offers no overflow control', () => {
  const lanes = fiveEmpty()
  // A list-then-count skew: the count arrived, the rows did not. Rendering
  // 'No jobs' and '+ 3 more' together states two contradictory things at once.
  lanes[0] = lane({ status: 'pending', items: [], total: 3 })
  renderLanes(lanes)
  expect(within(region('Queued')).getByText('No jobs')).toBeInTheDocument()
  expect(within(region('Queued')).queryByRole('button', { name: /more/i })).toBeNull()
})

test('tab order is the scroll container, then each lane in document order', async () => {
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', items: [job('A1', 'alpha'), job('A2', 'beta')], total: 9 })
  lanes[1] = lane({ status: 'running', items: [job('B1', 'gamma')], total: 1 })
  renderLanes(lanes)

  const scroller = screen.getByRole('group', { name: /scrolls horizontally/i })
  await userEvent.tab()
  expect(document.activeElement).toBe(scroller)
  await userEvent.tab()
  expect(document.activeElement).toHaveAttribute('href', '/jobs/A1')
  await userEvent.tab()
  expect(document.activeElement).toHaveAttribute('href', '/jobs/A2')
  await userEvent.tab()
  expect(document.activeElement).toHaveTextContent('+ 7 more')
  await userEvent.tab()
  expect(document.activeElement).toHaveAttribute('href', '/jobs/B1')
})

test('the lane count says matching while a filter is active', () => {
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', items: [job('A1', 'ingest frames')], total: 1 })

  renderLanes(lanes, undefined, { filtering: true })
  // With a filter active the number is no longer that status's all-time count,
  // and a caption that still says "total" is a wrong answer rather than a
  // missing one.
  expect(within(region('Queued')).getByText('1 matching')).toBeInTheDocument()
  expect(within(region('Queued')).queryByText('1 total')).toBeNull()
})

test('the lanes sit in one horizontal scroll container with fixed-width lanes', () => {
  renderLanes(fiveEmpty())
  const scroller = screen.getByRole('group', { name: /scrolls horizontally/i })
  // jsdom does no layout, so this pins the classes, not a width. The real widths
  // are measured by web/e2e/layout.spec.ts's jobs-lanes surface.
  expect(scroller).toHaveClass('overflow-x-auto')
  expect(scroller).toHaveAttribute('tabindex', '0')
  expect(region('Queued')).toHaveClass('w-[280px]', 'shrink-0')
})
