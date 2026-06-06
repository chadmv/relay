import { useState } from 'react'
import { Button } from '../components/Button'
import { useWorkers } from './useWorkers'
import { useWorkerStats } from './useWorkerStats'
import { useRevokedWorkers } from './useRevokedWorkers'
import { WorkersGrid } from './WorkersGrid'
import { WorkersTable, type SortField } from './WorkersTable'
import { RevokedWorkersTable } from './RevokedWorkersTable'
import type { Worker, WorkerSort, WorkerStats, WorkerStatus } from './api'

type View = 'grid' | 'table'
type Section = 'active' | 'decommissioned'

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
  const counts: Record<WorkerStatus, number> = { online: 0, stale: 0, offline: 0, disabled: 0, revoked: 0 }
  for (const w of workers) counts[w.status]++
  return counts
}

export function WorkersPage() {
  const [sort, setSort] = useState<WorkerSort>('-created_at')
  const [view, setView] = useState<View>(loadView)
  const [section, setSection] = useState<Section>('active')
  const { data, error, isLoading, isFetching, refetch } = useWorkers(sort)
  const { data: stats } = useWorkerStats()
  const revoked = useRevokedWorkers(section === 'decommissioned')

  function chooseView(v: View) {
    setView(v)
    localStorage.setItem(VIEW_KEY, v)
  }

  const sectionTabs = (
    <div className="flex rounded-full border border-border p-0.5">
      {(['active', 'decommissioned'] as Section[]).map((s) => (
        <button
          key={s}
          type="button"
          aria-pressed={section === s}
          onClick={() => setSection(s)}
          className={`rounded-full px-3 py-1 text-[12px] ${section === s ? 'bg-accent text-bg' : 'text-fg-mute'}`}
        >
          {s === 'active' ? 'Active' : 'Decommissioned'}
        </button>
      ))}
    </div>
  )

  const header = (
    <div className="flex flex-wrap items-end gap-6">
      <div>
        <div className="font-mono text-[11px] tracking-widest text-fg-mute">FLEET</div>
        <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
      </div>
      <div className="ml-auto">{sectionTabs}</div>
    </div>
  )

  if (section === 'decommissioned') {
    return (
      <div className="flex flex-col gap-4">
        {header}
        {revoked.isLoading && !revoked.data ? (
          <div className="text-[13px] text-fg-mute">Loading...</div>
        ) : revoked.error && !revoked.data ? (
          <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
            <div className="mb-3 text-[13px] text-err">{(revoked.error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => revoked.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <RevokedWorkersTable workers={revoked.data?.items ?? []} />
        )}
      </div>
    )
  }

  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-4">
        {header}
        <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-28 rounded-card border border-border bg-white/5" />
          ))}
        </div>
      </div>
    )
  }

  if (error && !data) {
    return (
      <div className="flex flex-col gap-4">
        {header}
        <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
          <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
          <Button className="w-auto px-4" onClick={() => refetch()}>
            Retry
          </Button>
        </div>
      </div>
    )
  }

  const workers = data?.items ?? []
  if (workers.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        {header}
        <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
          No workers enrolled yet.
        </div>
      </div>
    )
  }

  // Prefer fleet-wide counts from the stats endpoint. Until the first stats
  // response arrives, fall back to page-scoped counts so the strip is never empty.
  const fallback = countByStatus(workers)
  const counts: WorkerStats = stats ?? {
    online: fallback.online,
    stale: fallback.stale,
    offline: fallback.offline,
    disabled: fallback.disabled,
    total: data?.total ?? workers.length,
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">FLEET</div>
          <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
        </div>
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-ok">{counts.online}</b> ONLINE</span>
          <span><b className="text-warn">{counts.stale}</b> STALE</span>
          <span><b className="text-fg-mute">{counts.disabled}</b> DISABLED</span>
          <span><b className="text-err">{counts.offline}</b> OFFLINE</span>
          <span className="text-fg-dim">· <span>{`${counts.total} workers`}</span></span>
        </div>
        <div className="ml-auto flex items-center gap-3">
          {sectionTabs}
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={isFetching ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
          <div className="flex rounded-full border border-border p-0.5">
            {(['grid', 'table'] as View[]).map((v) => (
              <button
                key={v}
                type="button"
                aria-pressed={view === v}
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
