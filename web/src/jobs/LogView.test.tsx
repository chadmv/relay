import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { LogView } from './LogView'
import { DROP_MARKER_TEXT, MAX_LINES, type LogRow } from './logBuffer'
import type { TaskLogStreamResult } from './useTaskLogStream'

function row(key: number, text: string, over: Partial<LogRow> = {}): LogRow {
  return { key, kind: 'line', stream: 'stdout', text, time: '2026-08-09T14:36:25.000Z', ...over }
}

function streamOf(over: Partial<TaskLogStreamResult> = {}): TaskLogStreamResult {
  return {
    rows: [],
    status: 'live',
    attempt: 0,
    dropped: false,
    evicted: false,
    historyTruncated: false,
    total: 0,
    errorMessage: '',
    reconnect: () => {},
    canLoadEarlier: false,
    loadingEarlier: false,
    earlierComplete: false,
    loadEarlier: () => {},
    ...over,
  }
}

test('renders log lines with a stdout/stderr distinction and a UTC time column', () => {
  render(
    <LogView
      stream={streamOf({
        rows: [row(1, 'building'), row(2, 'warning: x', { stream: 'stderr' })],
      })}
    />,
  )
  expect(screen.getByText('building')).toBeInTheDocument()
  expect(screen.getByText('warning: x').className).toMatch(/text-err/)
  expect(screen.getAllByText('14:36:25').length).toBeGreaterThan(0)
})

test('shows LIVE with a green dot only while the stream is actually open', () => {
  const { rerender } = render(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'live' })} />)
  expect(screen.getByText('LIVE')).toBeInTheDocument()
  // The inverse of the old LogTab.test.tsx:29-37 case: a LIVE badge on a
  // non-streaming view would imply output we are not receiving.
  rerender(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'history' })} />)
  expect(screen.queryByText('LIVE')).toBeNull()
  expect(screen.getByText('HISTORY')).toBeInTheDocument()
  rerender(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'ended' })} />)
  expect(screen.queryByText('LIVE')).toBeNull()
  expect(screen.getByText('ENDED')).toBeInTheDocument()
})

test('shows the reconnect attempt count while reconnecting', () => {
  render(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'reconnecting', attempt: 3 })} />)
  expect(screen.getByText(/RECONNECTING \(3\/5\)/)).toBeInTheDocument()
})

test('offers a manual Reconnect control when disconnected', async () => {
  const reconnect = vi.fn()
  render(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'disconnected', reconnect })} />)
  await userEvent.click(screen.getByRole('button', { name: /reconnect/i }))
  expect(reconnect).toHaveBeenCalledTimes(1)
})

test('renders the drop marker as a distinct in-stream row', () => {
  render(
    <LogView
      stream={streamOf({
        rows: [row(1, 'before'), row(2, DROP_MARKER_TEXT, { kind: 'marker', time: '' }), row(3, 'after')],
        dropped: true,
      })}
    />,
  )
  expect(screen.getByText(new RegExp(DROP_MARKER_TEXT))).toBeInTheDocument()
})

// earlierComplete is true throughout: historyTruncated is now the THIRD branch
// of the notice, so without it the tail notice wins and this case never renders.
test('shows the truncation notice with real counts, then the eviction notice', () => {
  const { rerender } = render(
    <LogView
      stream={streamOf({ rows: [row(1, 'a')], historyTruncated: true, earlierComplete: true, total: 94312 })}
    />,
  )
  expect(
    screen.getByText(
      // MAX_LINES counts reassembled LINES; total counts server-side log
      // ENTRIES - two different units (code review, L5). Comparing "2,000 of
      // 94,312 lines" implies they are the same unit, which they are not.
      `Showing the first ${MAX_LINES.toLocaleString('en-US')} of ${(94312).toLocaleString('en-US')} log entries. Live output continues below.`,
    ),
  ).toBeInTheDocument()

  rerender(
    <LogView
      stream={streamOf({
        rows: [row(1, 'a')],
        historyTruncated: true,
        earlierComplete: true,
        evicted: true,
        total: 94312,
      })}
    />,
  )
  expect(screen.getByText('Earlier output not shown.')).toBeInTheDocument()
})

test('renders Load earlier only when the stream says a page is available', async () => {
  const loadEarlier = vi.fn()
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(1, 'a')], canLoadEarlier: true, loadEarlier })} />,
  )
  await userEvent.click(screen.getByRole('button', { name: /load earlier/i }))
  expect(loadEarlier).toHaveBeenCalledTimes(1)

  // A complete log must not grow a control that implies missing history.
  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a')], canLoadEarlier: false, earlierComplete: true })} />,
  )
  expect(screen.queryByRole('button', { name: /load earlier/i })).toBeNull()
  expect(screen.queryByText(/loading earlier/i)).toBeNull()
})

test('shows a loading state instead of the button while a page is in flight', () => {
  render(
    <LogView stream={streamOf({ rows: [row(1, 'a')], canLoadEarlier: true, loadingEarlier: true })} />,
  )
  expect(screen.getByText(/loading earlier/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /load earlier/i })).toBeNull()
})

test('the tail notice keeps lines and entries as separate units, and eviction wins', () => {
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(1, 'a'), row(2, 'b')], earlierComplete: false, total: 94312 })} />,
  )
  expect(
    screen.getByText(`Showing the most recent 2 lines of ${(94312).toLocaleString('en-US')} log entries.`),
  ).toBeInTheDocument()

  // Eviction resolves first: it is the stronger statement, and both can be true.
  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a')], earlierComplete: false, evicted: true, total: 94312 })} />,
  )
  expect(screen.getByText('Earlier output not shown.')).toBeInTheDocument()

  // A complete log shows no notice and no control at all.
  rerender(<LogView stream={streamOf({ rows: [row(1, 'a')], earlierComplete: true, total: 1 })} />)
  expect(screen.queryByText(/showing the most recent/i)).toBeNull()
  expect(screen.queryByText(/earlier output not shown/i)).toBeNull()
})

test('anchors the viewport when rows are added above it', () => {
  const onPrependAdjust = vi.fn()
  // jsdom reports every geometry as 0, so the pixel is untestable here and only
  // the DECISION is asserted; preservedScrollTop owns the arithmetic.
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(10, 'tail')] })} onPrependAdjust={onPrependAdjust} />,
  )
  // Follow is on by default, so an append must not adjust anything.
  rerender(
    <LogView stream={streamOf({ rows: [row(10, 'tail'), row(11, 'more')] })} onPrependAdjust={onPrependAdjust} />,
  )
  expect(onPrependAdjust).not.toHaveBeenCalled()

  const box = screen.getByTestId('log-body')
  Object.defineProperty(box, 'scrollHeight', { value: 2000, configurable: true })
  Object.defineProperty(box, 'clientHeight', { value: 1000, configurable: true })
  box.scrollTop = 0
  act(() => {
    box.dispatchEvent(new Event('scroll', { bubbles: true }))
  })
  // Prepended rows carry FRESH keys, so the first row's key changes; an append
  // leaves it alone. That is the signal a prepend happened.
  rerender(
    <LogView
      stream={streamOf({ rows: [row(12, 'earlier'), row(10, 'tail'), row(11, 'more')] })}
      onPrependAdjust={onPrependAdjust}
    />,
  )
  expect(onPrependAdjust).toHaveBeenCalledTimes(1)
})

test('shows loading, empty and error states', async () => {
  const reconnect = vi.fn()
  const { rerender } = render(<LogView stream={streamOf({ status: 'loading' })} />)
  expect(screen.getByText(/loading logs/i)).toBeInTheDocument()

  rerender(<LogView stream={streamOf({ status: 'history' })} />)
  expect(screen.getByText(/no log output/i)).toBeInTheDocument()

  rerender(<LogView stream={streamOf({ status: 'error', errorMessage: '404 task not found', reconnect })} />)
  expect(screen.getByText(/failed to load logs/i)).toBeInTheDocument()
  expect(screen.getByText(/404 task not found/)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  expect(reconnect).toHaveBeenCalledTimes(1)
})

test('renders untrusted content as text, never as HTML', () => {
  render(<LogView stream={streamOf({ rows: [row(1, '<img src=x onerror=alert(1)>')] })} />)
  // A job that prints markup must render as characters. This is the XSS boundary.
  expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
  expect(document.querySelector('img')).toBeNull()
})

test('scrolls to the bottom on new rows while following, and stops when follow is off', async () => {
  const onScrolledToBottom = vi.fn()
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(1, 'a')] })} onScrolledToBottom={onScrolledToBottom} />,
  )
  const before = onScrolledToBottom.mock.calls.length
  expect(before).toBeGreaterThan(0)

  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a'), row(2, 'b')] })} onScrolledToBottom={onScrolledToBottom} />,
  )
  expect(onScrolledToBottom.mock.calls.length).toBeGreaterThan(before)

  // Asserting a pixel value in jsdom would be vacuously green (scrollTop and
  // scrollHeight are 0 there), so the geometry is set explicitly and only the
  // DECISION is asserted, via shouldFollow.
  await userEvent.click(screen.getByRole('button', { name: /follow tail/i }))
  const after = onScrolledToBottom.mock.calls.length
  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a'), row(2, 'b'), row(3, 'c')] })} onScrolledToBottom={onScrolledToBottom} />,
  )
  expect(onScrolledToBottom.mock.calls.length).toBe(after)
  expect(screen.getByRole('button', { name: /jump to latest/i })).toBeInTheDocument()
})

test('a scroll away from the bottom turns follow off and reveals Jump to latest', () => {
  const { container } = render(<LogView stream={streamOf({ rows: [row(1, 'a')] })} />)
  expect(screen.queryByRole('button', { name: /jump to latest/i })).toBeNull()

  const box = container.querySelector('[data-testid="log-body"]') as HTMLElement
  Object.defineProperty(box, 'scrollHeight', { value: 2000, configurable: true })
  Object.defineProperty(box, 'clientHeight', { value: 1000, configurable: true })
  box.scrollTop = 0
  act(() => {
    box.dispatchEvent(new Event('scroll', { bubbles: true }))
  })

  expect(screen.getByRole('button', { name: /jump to latest/i })).toBeInTheDocument()
})

test('renders the endpoint caption and extra header content when given', () => {
  render(
    <LogView
      stream={streamOf({ rows: [row(1, 'a')] })}
      endpointCaption="/v1/events?task_id=t1 · single-task stream"
      headerExtra={<span>EXTRA</span>}
    />,
  )
  expect(screen.getByText('/v1/events?task_id=t1 · single-task stream')).toBeInTheDocument()
  expect(screen.getByText('EXTRA')).toBeInTheDocument()
})
