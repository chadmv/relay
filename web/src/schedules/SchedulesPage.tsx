import { useEffect, useState } from 'react'
import { Button } from '../components/Button'
import type { Schedule, ScheduleSort } from './api'
import { useSchedules } from './useSchedules'
import { useScheduleActions } from './useScheduleActions'
import { SchedulesTable } from './SchedulesTable'
import { computePageRange } from '../lib/pageRange'
import { useCursorPager } from '../lib/useCursorPager'
import { Eyebrow, GlassPanel } from '../components/holo'

const SORT_OPTIONS: { value: ScheduleSort; label: string }[] = [
  { value: '-created_at', label: 'Newest' },
  { value: 'created_at', label: 'Oldest' },
  { value: 'name', label: 'Name A->Z' },
  { value: '-name', label: 'Name Z->A' },
  { value: 'next_run_at', label: 'Next run soonest' },
  { value: '-next_run_at', label: 'Next run latest' },
  { value: '-updated_at', label: 'Recently run' },
  { value: 'updated_at', label: 'Least recently run' },
]

function countEnabled(schedules: Schedule[]): { enabled: number; paused: number } {
  let enabled = 0
  for (const s of schedules) if (s.enabled) enabled++
  return { enabled, paused: schedules.length - enabled }
}

export function SchedulesPage() {
  const [sort, setSort] = useState<ScheduleSort>('-created_at')
  const pager = useCursorPager()
  const [pendingId, setPendingId] = useState<string | null>(null)

  const { data, error, isLoading, isPlaceholderData, refetch } = useSchedules(sort, pager.cursor)
  const { runNow, setEnabled } = useScheduleActions()

  // Tick once a second so relative "next run"/"last run" strings stay fresh
  // between 10s polls.
  const [, setTick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [])

  function chooseSort(next: ScheduleSort) {
    setSort(next)
    pager.resetPaging() // restart paging when the sort changes
  }

  async function onRunNow(id: string) {
    setPendingId(id)
    try {
      await runNow.mutateAsync(id)
    } finally {
      setPendingId(null)
    }
  }

  async function onToggleEnabled(id: string, nextEnabled: boolean) {
    setPendingId(id)
    try {
      await setEnabled.mutateAsync({ id, enabled: nextEnabled })
    } finally {
      setPendingId(null)
    }
  }

  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 6 }).map((_, i) => (
          <GlassPanel key={i} className="h-10" />
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

  const schedules = data?.items ?? []

  const counts = countEnabled(schedules)
  const total = data?.total ?? schedules.length
  const { x, y } = computePageRange(pager.startOffset, schedules.length)
  const actionError = (runNow.error ?? setEnabled.error) as Error | null

  return (
    <div className="flex flex-col gap-4">
      {/*
        The hi-fi HoloSchedules also shows filter chips (All/Enabled/Disabled), a
        free-text search input, and a FAILED-24H summary stat. All three are
        backend-blocked and deliberately omitted here (a dead list control or a
        fabricated stat reads as broken):
          - filter chips + search: docs/backlog/idea-2026-06-05-schedules-filter-search.md
          - FAILED-24H stat:       docs/backlog/idea-2026-06-05-failed-24h-stat.md
        The ENABLED/PAUSED summary strip below is page-scoped (counts only the
        loaded page) until the stats endpoint lands:
          - fleet-wide counts:     docs/backlog/idea-2026-06-05-schedules-stats-endpoint.md
      */}
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <Eyebrow>RECURRING</Eyebrow>
          <h1 className="text-[32px] font-normal tracking-tight">Schedules</h1>
        </div>
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-ok">{counts.enabled}</b> ENABLED</span>
          <span><b className="text-fg">{counts.paused}</b> PAUSED</span>
          <span className="text-fg-dim">· <span>{`${total} schedules`}</span></span>
        </div>
        <label className="ml-auto flex items-center gap-2 font-mono text-[10px] text-fg-mute">
          <span>Sort</span>
          <select
            aria-label="Sort"
            value={sort}
            onChange={(e) => chooseSort(e.target.value as ScheduleSort)}
            className="rounded-md border border-border bg-black/25 px-2 py-1 text-[11px] text-fg"
          >
            {SORT_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
      </div>

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      <SchedulesTable
        schedules={schedules}
        pendingId={pendingId}
        onRunNow={onRunNow}
        onToggleEnabled={onToggleEnabled}
        footer={
          <div className="flex items-center justify-between font-mono text-[10.5px] tracking-wide text-fg-mute">
            <span>
              SHOWING <span className="text-fg">{x}-{y} of {total}</span>
              {' · '}SORT <span className="text-accent-b">{sort}</span> · OWNED + ADMINISTRATIVE
            </span>
            <div className="flex gap-1.5">
              <button
                type="button"
                disabled={!pager.canPrev || isPlaceholderData}
                onClick={pager.prev}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                disabled={!data?.next_cursor || isPlaceholderData}
                onClick={() => pager.next(data)}
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
