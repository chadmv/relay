import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { SchedulesSummary } from './SchedulesSummary'

// Hand-written, no type annotation naming ScheduleStats. All five keys, always:
// the response carries no omitempty, so a body that drops one is a response the
// server cannot send.
const STATS = { enabled: 12, paused: 5, total: 17, failed_runs_24h: 3, failing: 2 }

// EACH LABEL NAMES ITS UNIT, and that is the deviation from the hi-fi that this
// test exists to hold. The strip carries two failure numbers counted in different
// units over different windows - failed_runs_24h is over jobs and windowed and
// includes run-now jobs, failing is over schedules and is not windowed - and two
// adjacent uppercase labels both beginning FAIL invite the reading that one is a
// subset of the other. It is not.
test('the strip names four numbers and their units', () => {
  render(<SchedulesSummary stats={STATS} statsFailed={false} filterActive={false} />)
  expect(screen.getByTestId('schedules-stat-enabled')).toHaveTextContent('12 ENABLED')
  expect(screen.getByTestId('schedules-stat-paused')).toHaveTextContent('5 PAUSED')
  expect(screen.getByTestId('schedules-stat-failed_runs_24h')).toHaveTextContent('3 FAILED RUNS 24H')
  expect(screen.getByTestId('schedules-stat-failing')).toHaveTextContent('2 FAILING SCHEDULES')
})

// A HYPHEN, NOT A ZERO. A tile falling back to 0 before the first response states
// a fact it does not have, and "0 FAILING SCHEDULES" is the reassuring one - a
// fabricated stat reads as broken only after someone trusts it. A tile that
// VANISHES instead is also wrong: it changes the strip's width mid-measure and
// reads as a missing feature.
test('an absent stats response renders placeholders, not zeros', () => {
  render(<SchedulesSummary stats={undefined} statsFailed={false} filterActive={false} />)
  for (const key of ['enabled', 'paused', 'failed_runs_24h', 'failing']) {
    const tile = screen.getByTestId(`schedules-stat-${key}`)
    expect(tile).toHaveTextContent('-')
    expect(tile).not.toHaveTextContent('0')
  }
  expect(screen.getByTestId('schedules-stat-total')).toHaveTextContent('- SCHEDULES TOTAL')
})

// THE POSITIVE CONTROL FOR THE TEST ABOVE, and the property the server contract
// turns on: a zero is a zero and never an absence. Without this, a `stats?.enabled
// || '-'` implementation passes the placeholder test and silently renders a real
// zero as a hyphen.
test('a real zero renders as a zero, not as a placeholder', () => {
  render(
    <SchedulesSummary
      stats={{ enabled: 0, paused: 0, total: 0, failed_runs_24h: 0, failing: 0 }}
      statsFailed={false}
      filterActive={false}
    />,
  )
  expect(screen.getByTestId('schedules-stat-enabled')).toHaveTextContent('0 ENABLED')
  expect(screen.getByTestId('schedules-stat-failing')).toHaveTextContent('0 FAILING SCHEDULES')
})

// TEXT, NOT A COLOUR, and no Retry: the query polls, so a retry button would be a
// second and weaker copy of the poll.
test('an errored stats query says so in words, alongside the placeholders', () => {
  render(<SchedulesSummary stats={undefined} statsFailed={true} filterActive={false} />)
  expect(screen.getByText('counts unavailable')).toBeInTheDocument()
  expect(screen.getByTestId('schedules-stat-enabled')).toHaveTextContent('- ENABLED')
})

// The caption is the strip's answer to a question the footer asks at the same
// time. The parenthetical appears at the exact moment the two totals can disagree.
test('the total caption names itself unfiltered exactly when a filter is active', () => {
  const { unmount } = render(
    <SchedulesSummary stats={STATS} statsFailed={false} filterActive={false} />,
  )
  expect(screen.getByTestId('schedules-stat-total')).toHaveTextContent('17 SCHEDULES TOTAL')
  expect(screen.getByTestId('schedules-stat-total')).not.toHaveTextContent('UNFILTERED')
  unmount()

  render(<SchedulesSummary stats={STATS} statsFailed={false} filterActive={true} />)
  expect(screen.getByTestId('schedules-stat-total')).toHaveTextContent(
    '17 SCHEDULES TOTAL (UNFILTERED)',
  )
})
