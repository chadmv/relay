import { useState } from 'react'
import { Button } from '../components/Button'
import { useWorkers } from './useWorkers'
import { WorkersGrid } from './WorkersGrid'
import { WorkersTable, type SortField } from './WorkersTable'
import type { Worker, WorkerSort, WorkerStatus } from './api'

type View = 'grid' | 'table'

const VIEW_KEY = 'relay.workers.view'

function loadView(): View {
  return localStorage.getItem(VIEW_KEY) === 'table' ? 'table' : 'grid'
}

function toggleSort(field: SortField, current: WorkerSort): WorkerSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as WorkerSort
  }
  return field
}

function countByStatus(workers: Worker[]): Record<WorkerStatus, number> {
  const counts: Record<WorkerStatus, number> = { online: 0, stale: 0, offline: 0, disabled: 0 }
  for (const w of workers) counts[w.status]++
  return counts
}

export function WorkersPage() {
  const [sort, setSort] = useState<WorkerSort>('-created_at')
  const [view, setView] = useState<View>(loadView)
  const { data, error, isLoading, isFetching, refetch } = useWorkers(sort)

  function chooseView(v: View) {
    setView(v)
    localStorage.setItem(VIEW_KEY, v)
  }

  if (isLoading && !data) {
    return (
      <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="h-28 rounded-card border border-border bg-white/5" />
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

  const workers = data?.items ?? []
  if (workers.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No workers enrolled yet.
      </div>
    )
  }

  const counts = countByStatus(workers)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">FLEET</div>
          <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
        </div>
        <div
          className="flex gap-4 font-mono text-[11px] text-fg-mute"
          title="Counts for the loaded page"
        >
          <span><b className="text-ok">{counts.online}</b> ONLINE</span>
          <span><b className="text-warn">{counts.stale}</b> STALE</span>
          <span><b className="text-fg-mute">{counts.disabled}</b> DISABLED</span>
          <span><b className="text-err">{counts.offline}</b> OFFLINE</span>
          <span className="text-fg-dim">· <span>{`${data?.total ?? workers.length} workers`}</span></span>
        </div>
        <div className="ml-auto flex items-center gap-3">
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={isFetching ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
          <div className="flex rounded-full border border-border p-0.5">
            {(['grid', 'table'] as View[]).map((v) => (
              <button
                key={v}
                type="button"
                onClick={() => chooseView(v)}
                className={`rounded-full px-3 py-1 text-[12px] ${view === v ? 'bg-accent text-bg' : 'text-fg-mute'}`}
              >
                {v === 'grid' ? 'Grid' : 'Table'}
              </button>
            ))}
          </div>
        </div>
      </div>

      {view === 'grid' ? (
        <WorkersGrid workers={workers} />
      ) : (
        <WorkersTable workers={workers} sort={sort} onSort={(f) => setSort((cur) => toggleSort(f, cur))} />
      )}
    </div>
  )
}
