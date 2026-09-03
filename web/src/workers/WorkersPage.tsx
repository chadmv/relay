import { useState } from 'react'
import { Button } from '../components/Button'
import { Eyebrow, GlassPanel } from '../components/holo'
import { useWorkers } from './useWorkers'
import { useWorkerStats } from './useWorkerStats'
import { useRevokedWorkers } from './useRevokedWorkers'
import { WorkersGrid } from './WorkersGrid'
import { WorkersTable } from './WorkersTable'
import { RevokedWorkersTable } from './RevokedWorkersTable'
import { computePageRange } from '../lib/pageRange'
import { toggleSort } from '../lib/toggleSort'
import { useCursorPager } from '../lib/useCursorPager'
import { usePersistedChoice } from '../lib/usePersistedChoice'
import type { Worker, WorkerSort, WorkerStats, WorkerStatus } from './api'

const VIEWS = ['grid', 'table'] as const
type View = (typeof VIEWS)[number]
type Section = 'active' | 'decommissioned'

const VIEW_KEY = 'relay.workers.view'

function countByStatus(workers: Worker[]): Record<WorkerStatus, number> {
  const counts: Record<WorkerStatus, number> = { online: 0, stale: 0, offline: 0, disabled: 0, revoked: 0 }
  for (const w of workers) counts[w.status]++
  return counts
}

export function WorkersPage() {
  const [sort, setSort] = useState<WorkerSort>('-created_at')
  const [view, chooseView] = usePersistedChoice<View>(VIEW_KEY, VIEWS, 'grid')
  const [section, setSection] = useState<Section>('active')

  const revokedPager = useCursorPager()

  const { data, error, isLoading, isFetching, refetch } = useWorkers(sort)
  const { data: stats } = useWorkerStats()
  const revoked = useRevokedWorkers(section === 'decommissioned', revokedPager.cursor)

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
        <Eyebrow>FLEET</Eyebrow>
        <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
      </div>
      <div className="ml-auto">{sectionTabs}</div>
    </div>
  )

  if (section === 'decommissioned') {
    const revokedWorkers = revoked.data?.items ?? []
    const revokedTotal = revoked.data?.total ?? 0
    const { x, y } = computePageRange(revokedPager.startOffset, revokedWorkers.length)
    const rangeText =
      revokedWorkers.length === 0
        ? `0 of ${revokedTotal.toLocaleString()}`
        : `${x.toLocaleString()}-${y.toLocaleString()} of ${revokedTotal.toLocaleString()}`

    return (
      <div className="flex flex-col gap-4">
        {header}
        {revoked.isLoading && !revoked.data ? (
          <div className="text-[13px] text-fg-mute">Loading...</div>
        ) : revoked.error && !revoked.data ? (
          <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
            <div className="mb-3 text-[13px] text-err">{(revoked.error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => revoked.refetch()}>
              Retry
            </Button>
          </GlassPanel>
        ) : (
          <>
            <RevokedWorkersTable workers={revokedWorkers} />
            <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
              <span>
                SHOWING <span className="text-fg">{rangeText}</span>
                {' · '}CURSOR PAGINATED
              </span>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={revokedPager.prev}
                  disabled={!revokedPager.canPrev || revoked.isPlaceholderData}
                  className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
                >
                  &larr; prev
                </button>
                <button
                  type="button"
                  onClick={() => revokedPager.next(revoked.data)}
                  disabled={!revoked.data?.next_cursor || revoked.isPlaceholderData}
                  className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
                >
                  next 50 &rarr;
                </button>
              </div>
            </div>
          </>
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
            <GlassPanel key={i} className="h-28" />
          ))}
        </div>
      </div>
    )
  }

  if (error && !data) {
    return (
      <div className="flex flex-col gap-4">
        {header}
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
          <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
          <Button className="w-auto px-4" onClick={() => refetch()}>
            Retry
          </Button>
        </GlassPanel>
      </div>
    )
  }

  const workers = data?.items ?? []
  if (workers.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        {header}
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No workers enrolled yet.
        </GlassPanel>
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
          <Eyebrow>FLEET</Eyebrow>
          <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
        </div>
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-ok">{counts.online}</b> ONLINE</span>
          <span><b className="text-warn">{counts.stale}</b> STALE</span>
          <span><b className="text-fg-mute">{counts.disabled}</b> DISABLED</span>
          <span><b className="text-err">{counts.offline}</b> OFFLINE</span>
          <span className="text-fg-dim">· <span>{`${counts.total} workers`}</span></span>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-3">
          {sectionTabs}
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={isFetching ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
          <div role="group" aria-label="Workers view" className="flex rounded-full border border-border p-0.5">
            {VIEWS.map((v) => (
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
