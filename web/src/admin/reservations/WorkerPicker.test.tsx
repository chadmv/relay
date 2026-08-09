import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { WorkerPicker } from './WorkerPicker'

const A = { id: 'aaaa1111-1111-1111-1111-111111111111', name: 'render-01', status: 'online' }
const B = { id: 'bbbb2222-2222-2222-2222-222222222222', name: 'render-02', status: 'offline' }
const C = { id: 'cccc3333-3333-3333-3333-333333333333', name: 'sim-01', status: 'online' }

function renderPicker(value: string[] = []) {
  const onChange = vi.fn()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={client}>
      <WorkerPicker value={value} onChange={onChange} />
    </QueryClientProvider>,
  )
  return { onChange, ...view }
}

test('requests limit=200 sorted by name and lists every loaded worker', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/workers', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })
    }),
  )
  renderPicker()
  expect(await screen.findByRole('checkbox', { name: /render-01/ })).toBeInTheDocument()
  expect(params?.get('limit')).toBe('200')
  expect(params?.get('sort')).toBe('name')
  expect(screen.getAllByRole('checkbox')).toHaveLength(3)
  // Offline workers are NOT hidden: a reservation is a pure exclusion from dispatch,
  // so reserving a currently-offline worker is legitimate. Revoked workers are
  // already excluded server-side.
  expect(screen.getByRole('checkbox', { name: /render-02/ })).toBeInTheDocument()
})

test('the filter narrows the rendered set WITHOUT issuing a request', async () => {
  let calls = 0
  server.use(
    http.get('/v1/workers', () => {
      calls++
      return HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })
    }),
  )
  renderPicker()
  await screen.findByRole('checkbox', { name: /render-01/ })
  // Positive control: the counter is live - it already moved from 0 to 1 on mount,
  // so the equality below is about the filter and not about a dead instrument.
  expect(calls).toBe(1)

  await userEvent.type(screen.getByLabelText('Filter workers'), 'sim')
  expect(await screen.findByRole('checkbox', { name: /sim-01/ })).toBeInTheDocument()
  expect(screen.getAllByRole('checkbox')).toHaveLength(1)
  expect(screen.queryByRole('checkbox', { name: /render-01/ })).not.toBeInTheDocument()
  expect(calls).toBe(1)
})

test('selecting and deselecting emits ids in loaded order, not click order', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  // FIX (vacuity found in the plan's test body): the component is controlled via
  // `value`, so simulating a parent's next render legitimately means a fresh
  // renderPicker() call - but three renderPicker() calls in one test each mount a
  // NEW root without unmounting the last, leaving TWO "render-01" checkboxes in the
  // document simultaneously. screen.getByRole/click then silently resolves against
  // whichever instance happens to match first, so onChange2/onChange3 are asserted
  // against a click that never reached them - this was failing RED with "expected
  // onChange2 to be called, received undefined" until each prior view is unmounted
  // before the next one mounts.
  const first = renderPicker()
  await screen.findByRole('checkbox', { name: /render-01/ })

  // Click the THIRD worker first, then the first. The emitted array must still be in
  // the loaded (name-sorted) order, so the submitted worker_ids are a function of the
  // SELECTION and not of the click sequence.
  await userEvent.click(screen.getByRole('checkbox', { name: /sim-01/ }))
  expect(first.onChange).toHaveBeenLastCalledWith([C.id])
  first.unmount()

  // The component is controlled, so drive the next state in through `value`.
  const second = renderPicker([C.id])
  await screen.findByRole('checkbox', { name: /render-01/ })
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  expect(second.onChange).toHaveBeenLastCalledWith([A.id, C.id])
  second.unmount()

  const third = renderPicker([A.id, C.id])
  await screen.findByRole('checkbox', { name: /sim-01/ })
  await userEvent.click(screen.getByRole('checkbox', { name: /sim-01/ }))
  expect(third.onChange).toHaveBeenLastCalledWith([A.id])
})

// L9 (review 2026-08-09): individual checkboxes are correctly named, but the GROUP
// itself had no accessible name - "Workers to reserve" was a plain <span>, invisible
// to assistive tech navigating by landmark/group.
test('the checkbox group has an accessible name from the visible label', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  renderPicker()
  const group = await screen.findByRole('group', { name: /workers to reserve/i })
  expect(group).toBeInTheDocument()
  expect(screen.getAllByRole('checkbox').every((c) => group.contains(c))).toBe(true)
})

test('a selected worker stays checked and the count is shown', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  renderPicker([A.id, C.id])
  expect(await screen.findByRole('checkbox', { name: /render-01/ })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /render-02/ })).not.toBeChecked()
  expect(screen.getByText('2 selected')).toBeInTheDocument()
})

test('the 200-row ceiling is STATED when the fleet is larger', async () => {
  server.use(
    http.get('/v1/workers', () =>
      // 3 loaded, 512 exist: the picker must say so rather than pretend.
      HttpResponse.json({ items: [A, B, C], next_cursor: 'c2', total: 512 }),
    ),
  )
  renderPicker()
  await screen.findByRole('checkbox', { name: /render-01/ })
  expect(
    screen.getByText(
      'showing first 200 of 512 workers by name - use the CLI for workers beyond this page',
    ),
  ).toBeInTheDocument()
})

test('the ceiling note is ABSENT when the whole fleet is loaded (both directions asserted)', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  const { container } = renderPicker()
  await screen.findByRole('checkbox', { name: /render-01/ })
  // Matched on normalized container text, which is the representation the real
  // failure would take: an always-on note anywhere in the panel.
  expect(container.textContent?.replace(/\s+/g, ' ')).not.toMatch(/showing first 200/)
})

test('an empty fleet and a failed load both say so instead of rendering a silent empty list', async () => {
  // L7 (review 2026-08-09): unmount between renders in the SAME test. Two live
  // roots in one test previously passed only because the two asserted strings
  // happen to never collide - not because the test proved isolation - the exact
  // hazard already fixed once in the "loaded order" test above.
  server.use(http.get('/v1/workers', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })))
  const empty = renderPicker()
  expect(await screen.findByText('No workers are registered.')).toBeInTheDocument()
  empty.unmount()

  server.use(http.get('/v1/workers', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderPicker()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
})

// M1 (review 2026-08-09): the query default does not override refetchOnWindowFocus
// (true), and staleTime only DELAYS a refetch, so an admin who alt-tabs for >30s and
// returns can have `value` diverge from the freshly-loaded `data.items` (e.g. a
// worker they already checked was revoked meanwhile). `toggle` used to project the
// emitted array through the loaded `workers` array only, which silently dropped any
// selected id no longer present the instant the admin toggled ANY other box - while
// simply reopening the panel (no toggle) kept it, submitting a vanished id the
// server would happily persist (no FK on worker_ids). Two opposite behaviors from
// the same state was the bug.
const STALE_ID = 'ffffffff-ffff-ffff-ffff-ffffffffffff'

test('a selected id no longer on the loaded page is shown as a stale selection, not hidden', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  renderPicker([STALE_ID])
  await screen.findByRole('checkbox', { name: /render-01/ })
  // Visible, not silent: the admin can see the id no longer resolves to a loaded
  // worker rather than the count just quietly including a name nobody can see.
  expect(screen.getByText(new RegExp(STALE_ID.slice(0, 8)))).toBeInTheDocument()
  expect(screen.getByText(/no longer on this page/i)).toBeInTheDocument()
  expect(screen.getByText('1 selected')).toBeInTheDocument()
})

test('toggling a different worker does not silently drop a stale selected id', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  const { onChange } = renderPicker([STALE_ID])
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  // A.id is loaded (goes first, in loaded order); STALE_ID has no loaded position
  // and is appended after, preserving it rather than losing it.
  expect(onChange).toHaveBeenLastCalledWith([A.id, STALE_ID])
})

test('the stale selection can be explicitly removed - the admin decides, nothing removes it for them', async () => {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B, C], next_cursor: '', total: 3 })),
  )
  const { onChange } = renderPicker([STALE_ID, A.id])
  await screen.findByRole('checkbox', { name: /render-01/ })
  await userEvent.click(screen.getByRole('button', { name: new RegExp(`remove.*${STALE_ID.slice(0, 8)}`, 'i') }))
  expect(onChange).toHaveBeenLastCalledWith([A.id])
})
