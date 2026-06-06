import { useState } from 'react'
import { Button } from '../components/Button'
import { useJobs } from './useJobs'
import { useJobStats } from './useJobStats'
import { JobsTable } from './JobsTable'
import { SortControl } from './SortControl'
import type { JobSort } from './api'

const FILTERS: { key: string; label: string; status: string }[] = [
  { key: 'all', label: 'All', status: '' },
  { key: 'running', label: 'Running', status: 'running' },
  { key: 'queued', label: 'Queued', status: 'queued' },
  { key: 'done', label: 'Done', status: 'done' },
  { key: 'failed', label: 'Failed', status: 'failed' },
]

const DEFAULT_SORT: JobSort = '-created_at'

export function JobsPage() {
  const [sort, setSort] = useState<JobSort>(DEFAULT_SORT)
  const [filter, setFilter] = useState('all')
  // Cursor of the current page (''=first). The stack holds the cursors of the
  // pages we paged forward from, so prev can pop back (server returns only
  // next_cursor).
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])

  const status = FILTERS.find((f) => f.key === filter)?.status ?? ''
  const statusFiltered = filter !== 'all'
  const { data, error, isLoading, isFetching, refetch } = useJobs(sort, status, cursor)
  const { data: stats } = useJobStats()

  function pickFilter(key: string) {
    setFilter(key)
    setCursor('')
    setStack([])
    if (key !== 'all') setSort(DEFAULT_SORT) // server rejects sort + status
  }

  function pickSort(s: JobSort) {
    setSort(s)
    setCursor('')
    setStack([])
  }

  // next/prev use plain setters (not functional updaters): cursor and stack are
  // both read from the current render and React batches the two updates in one
  // event. Side effects inside a functional updater would double-fire under
  // StrictMode.
  function next() {
    if (!data?.next_cursor) return
    setStack([...stack, cursor])
    setCursor(data.next_cursor)
  }

  function prev() {
    if (stack.length === 0) return
    const copy = [...stack]
    const back = copy.pop() ?? ''
    setStack(copy)
    setCursor(back)
  }

  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <div key={i} className="h-9 rounded border border-border bg-white/5" />
        ))}
      </div>
    )
  }

  if (error && !data) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </div>
    )
  }

  const jobs = data?.items ?? []

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">OVERVIEW</div>
          <h1 className="text-[32px] font-normal tracking-tight">Jobs</h1>
        </div>
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-[18px] text-accent">{stats?.running ?? 0}</b> RUNNING</span>
          <span><b className="text-[18px] text-warn">{stats?.queued ?? 0}</b> QUEUED</span>
          <span><b className="text-[18px] text-ok">{stats?.done_24h ?? 0}</b> DONE·24H</span>
          <span><b className="text-[18px] text-err">{stats?.failed_24h ?? 0}</b> FAILED·24H</span>
        </div>
        <div className="ml-auto flex items-center gap-3">
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={isFetching ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
        </div>
      </div>

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

      <JobsTable jobs={jobs} />

      <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
        <span>
          SHOWING <span className="text-fg">{jobs.length}</span> OF <span className="text-fg">{data?.total ?? 0}</span>
          {' · '}SORT <span className="text-accent-b">{statusFiltered ? `status=${status}` : sort}</span> · CURSOR PAGINATED
        </span>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={prev}
            disabled={stack.length === 0}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            ← prev
          </button>
          <button
            type="button"
            onClick={next}
            disabled={!data?.next_cursor}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            next 50 →
          </button>
        </div>
      </div>
    </div>
  )
}
