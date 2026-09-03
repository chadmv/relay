import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Button } from '../components/Button'
import { useJobs } from './useJobs'
import { useJobStats } from './useJobStats'
import { JobsTable } from './JobsTable'
import { JobsLanes } from './JobsLanes'
import { useJobLanes } from './useJobLanes'
import { LANE_CHIP_KEY } from './lanes'
import { SortControl } from './SortControl'
import { computePageRange } from '../lib/pageRange'
import { useCursorPager } from '../lib/useCursorPager'
import { useDebouncedValue } from '../lib/useDebouncedValue'
import { usePersistedChoice } from '../lib/usePersistedChoice'
import type { JobSort, JobStatus } from './api'
import { Eyebrow, GlassPanel } from '../components/holo'

export const FILTERS: { key: string; label: string; status: string }[] = [
  { key: 'all', label: 'All', status: '' },
  { key: 'running', label: 'Running', status: 'running' },
  { key: 'queued', label: 'Queued', status: 'pending' },
  { key: 'done', label: 'Done', status: 'done' },
  { key: 'failed', label: 'Failed', status: 'failed' },
  { key: 'cancelled', label: 'Cancelled', status: 'cancelled' },
]

const DEFAULT_SORT: JobSort = '-created_at'

// The runtime list and the type derive from one tuple, so a view added to one
// cannot be missing from the other. usePersistedChoice validates a stored value
// against this list, so a value outside it reads as the fallback.
const VIEWS = ['table', 'lanes'] as const
type View = (typeof VIEWS)[number]

const VIEW_KEY = 'relay.jobs.view'

// debounceMs is a prop only so tests can shrink it and stay on real timers;
// production always uses the 300ms default, matching UsersTab's convention
// rather than introducing a second constant for two boxes to drift apart on.
//
// THE DEBOUNCE IS NOT A BOUND. GET /v1/jobs carries no rate limit and ?q= is an
// unindexed scan by design, so a caller that is not a typing user is unaffected
// by a client-side timer. It reduces how many scans one person's typing costs
// and bounds nothing else.
export function JobsPage({ debounceMs = 300 }: { debounceMs?: number }) {
  const [sort, setSort] = useState<JobSort>(DEFAULT_SORT)
  const [filter, setFilter] = useState('all')
  const [view, chooseView] = usePersistedChoice<View>(VIEW_KEY, VIEWS, 'table')
  const [qInput, setQInput] = useState('')
  const q = useDebouncedValue(qInput, debounceMs).trim()
  const [mine, setMine] = useState(false)
  const pager = useCursorPager()

  const status = FILTERS.find((f) => f.key === filter)?.status ?? ''
  const statusFiltered = filter !== 'all'
  const { data, error, isLoading, isFetching, isPlaceholderData, refetch } = useJobs(
    sort,
    status,
    pager.cursor,
    undefined,
    view === 'table',
    q,
    mine,
  )
  const { data: stats } = useJobStats()
  // Called unconditionally and gated by `enabled`, so the lanes stop polling the
  // moment the page returns to the table rather than running behind it.
  const lanes = useJobLanes(view === 'lanes', undefined, undefined, q, mine)

  function pickFilter(key: string) {
    setFilter(key)
    pager.resetPaging()
    if (key !== 'all') setSort(DEFAULT_SORT) // server rejects sort + status
  }

  function pickSort(s: JobSort) {
    setSort(s)
    pager.resetPaging()
  }

  function pickQ(v: string) {
    setQInput(v)
    // Reset here rather than in an effect on the debounced value: an effect runs
    // after the render that already issued a query carrying the new q and the old
    // cursor, so exactly one request goes out under a cursor minted for different
    // filters. Matches UsersTab's pickEmail.
    pager.resetPaging()
  }

  function pickMine(v: boolean) {
    setMine(v)
    pager.resetPaging()
  }

  function showAll(s: JobStatus) {
    // pickFilter also resets the pager and snaps sort back to the default, which is
    // exactly what a freshly filtered table needs.
    pickFilter(LANE_CHIP_KEY[s])
    chooseView('table')
  }

  // The table query is disabled in lanes view, so its isFetching would leave the
  // dot permanently dark beside text claiming the page is auto-refreshing.
  const polling = view === 'lanes' ? lanes.some((l) => l.isFetching) : isFetching
  const filtering = q !== '' || mine

  const pageHeader = (
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <Eyebrow>OVERVIEW</Eyebrow>
          <h1 className="text-[32px] font-normal tracking-tight">Jobs</h1>
        </div>
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-[18px] text-accent">{stats?.running ?? 0}</b> RUNNING</span>
          <span><b className="text-[18px] text-warn">{stats?.queued ?? 0}</b> QUEUED</span>
          <span><b className="text-[18px] text-ok">{stats?.done_24h ?? 0}</b> DONE·24H</span>
          <span><b className="text-[18px] text-err">{stats?.failed_24h ?? 0}</b> FAILED·24H</span>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-3">
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={polling ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
          <div role="group" aria-label="Jobs view" className="flex rounded-full border border-border p-0.5">
            {(['table', 'lanes'] as View[]).map((v) => (
              <button
                key={v}
                type="button"
                aria-pressed={view === v}
                onClick={() => chooseView(v)}
                className={`rounded-full px-3 py-1 text-[12px] ${view === v ? 'bg-accent text-bg' : 'text-fg-mute'}`}
              >
                {v === 'table' ? 'Table' : 'Lanes'}
              </button>
            ))}
          </div>
          <Link
            to="/jobs/new"
            className="rounded-[8px] bg-accent px-3 py-2 text-[13px] font-medium text-bg transition hover:bg-accent-b"
          >
            + New job
          </Link>
        </div>
      </div>
  )

  const toolbar = (
    <div className="flex flex-wrap items-center gap-2">
      {view === 'table' &&
        FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            aria-pressed={filter === f.key}
            onClick={() => pickFilter(f.key)}
            className={`rounded-full border px-3.5 py-1.5 text-[12px] ${
              filter === f.key ? 'border-accent/60 bg-accent/15 text-fg' : 'border-border bg-white/5 text-fg-mute'
            }`}
          >
            {f.label}
          </button>
        ))}
      {/* No fixed minimum width, unlike UsersTab's copy of this control: this
          toolbar is more crowded, and a flex item with a zero minimum takes the
          space that is left and wraps to its own line when there is none, which
          is the simplest thing that cannot widen the document at 320. */}
      <input
        type="search"
        aria-label="Search jobs"
        placeholder="Filter by job name or owner email"
        maxLength={200}
        value={qInput}
        onChange={(e) => pickQ(e.target.value)}
        className="ml-auto min-w-0 flex-1 rounded-full border border-border bg-black/25 px-3.5 py-1.5 text-[12px] text-fg outline-none placeholder:text-fg-dim focus:border-accent"
      />
      <button
        type="button"
        aria-pressed={mine}
        onClick={() => pickMine(!mine)}
        className={`flex-none rounded-full border px-3.5 py-1.5 text-[12px] ${
          mine ? 'border-accent bg-accent/25 text-fg' : 'border-accent/40 bg-white/5 text-accent'
        }`}
      >
        My jobs
      </button>
      {view === 'table' && (
        <SortControl
          value={sort}
          onChange={pickSort}
          disabled={statusFiltered}
          disabledHint="Sorting is unavailable while a status filter is active - the server rejects sort + status together. Switch to All to sort."
        />
      )}
    </div>
  )

  // Before the table's loading and error early returns, which belong to the table
  // query: in lanes view that query is disabled, and a lane owns its own loading,
  // empty and error states so one lane's 500 cannot blank the page.
  if (view === 'lanes') {
    return (
      <div className="flex flex-col gap-4">
        {pageHeader}
        {toolbar}
        <JobsLanes lanes={lanes} onShowAll={showAll} filtering={filtering} />
      </div>
    )
  }

  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <GlassPanel key={i} className="h-9" />
        ))}
      </div>
    )
  }

  if (error && !data) {
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </GlassPanel>
    )
  }

  const jobs = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(pager.startOffset, jobs.length)
  const rangeText =
    jobs.length === 0
      ? `0 of ${total.toLocaleString()}`
      : `${x.toLocaleString()}-${y.toLocaleString()} of ${total.toLocaleString()}`

  return (
    <div className="flex flex-col gap-4">
      {pageHeader}
      {toolbar}

      <JobsTable
        jobs={jobs}
        emptyMessage={filtering ? 'No jobs match those filters.' : undefined}
        footer={
          <div className="flex items-center justify-between font-mono text-[10.5px] tracking-wider text-fg-mute">
            <span>
              SHOWING <span className="text-fg">{rangeText}</span>
              {' · '}SORT <span className="text-accent-b">{statusFiltered ? `status=${status}` : sort}</span> · CURSOR PAGINATED
            </span>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={pager.prev}
                disabled={!pager.canPrev || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                onClick={() => pager.next(data)}
                disabled={!data?.next_cursor || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                next 50 →
              </button>
            </div>
          </div>
        }
      />
    </div>
  )
}
