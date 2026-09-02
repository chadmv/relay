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

type View = 'table' | 'lanes'

const VIEW_KEY = 'relay.jobs.view'

// Anything but the literal 'lanes' means the table, so a missing key, a value
// written by a future version, and a storage read that throws all land on the
// shipped default rather than on a blank page.
function loadView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === 'lanes' ? 'lanes' : 'table'
  } catch {
    return 'table'
  }
}

export function JobsPage() {
  const [sort, setSort] = useState<JobSort>(DEFAULT_SORT)
  const [filter, setFilter] = useState('all')
  const [view, setView] = useState<View>(loadView)
  const pager = useCursorPager()

  const status = FILTERS.find((f) => f.key === filter)?.status ?? ''
  const statusFiltered = filter !== 'all'
  const { data, error, isLoading, isFetching, isPlaceholderData, refetch } = useJobs(
    sort,
    status,
    pager.cursor,
    undefined,
    view === 'table',
  )
  const { data: stats } = useJobStats()
  // Called unconditionally and gated by `enabled`, so the lanes stop polling the
  // moment the page returns to the table rather than running behind it.
  const lanes = useJobLanes(view === 'lanes')

  function pickFilter(key: string) {
    setFilter(key)
    pager.resetPaging()
    if (key !== 'all') setSort(DEFAULT_SORT) // server rejects sort + status
  }

  function pickSort(s: JobSort) {
    setSort(s)
    pager.resetPaging()
  }

  function chooseView(v: View) {
    setView(v)
    try {
      localStorage.setItem(VIEW_KEY, v)
    } catch {
      // A storage failure must not take the click with it: the view still changes
      // for this session, it just does not survive a reload.
    }
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

  // Before the table's loading and error early returns, which belong to the table
  // query: in lanes view that query is disabled, and a lane owns its own loading,
  // empty and error states so one lane's 500 cannot blank the page.
  if (view === 'lanes') {
    return (
      <div className="flex flex-col gap-4">
        {pageHeader}
        <JobsLanes lanes={lanes} onShowAll={showAll} />
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

      {/*
        The hi-fi HoloJobsList also shows a Timeline view, a "My jobs" pill, and a
        free-text search input. All three are backend-blocked and deliberately
        omitted here (a dead list control reads as broken):
          - Timeline view: docs/backlog/idea-2026-06-05-jobs-timeline-view.md
          - My jobs + search: docs/backlog/idea-2026-06-05-my-jobs-toggle-mine-filter.md
        When those land, the remaining filters re-appear with real backing.
      */}
      <div className="flex flex-wrap items-center gap-2">
        {FILTERS.map((f) => (
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
        <div className="ml-auto">
          <SortControl
            value={sort}
            onChange={pickSort}
            disabled={statusFiltered}
            disabledHint="Sorting is unavailable while a status filter is active - the server rejects sort + status together. Switch to All to sort."
          />
        </div>
      </div>

      <JobsTable
        jobs={jobs}
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
