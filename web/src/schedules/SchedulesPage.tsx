import { useEffect, useState } from 'react'
import { Button } from '../components/Button'
import type { Schedule, ScheduleSort } from './api'
import { useSchedules } from './useSchedules'
import { useScheduleActions } from './useScheduleActions'
import { SchedulesTable } from './SchedulesTable'

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
  // Cursor stack: [] is the first page; each entry is the cursor for a deeper page.
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const cursor = cursorStack[cursorStack.length - 1]
  const [pendingId, setPendingId] = useState<string | null>(null)

  const { data, error, isLoading, refetch } = useSchedules(sort, cursor)
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
    setCursorStack([]) // restart paging when the sort changes
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
          <div key={i} className="h-10 rounded-card border border-border bg-white/5" />
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

  const schedules = data?.items ?? []
  if (schedules.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No schedules yet.
      </div>
    )
  }

  const counts = countEnabled(schedules)
  const total = data?.total ?? schedules.length
  const actionError = (runNow.error ?? setEnabled.error) as Error | null

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">RECURRING</div>
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
      />

      <div className="flex items-center justify-between font-mono text-[10.5px] tracking-wide text-fg-mute">
        <span>
          SHOWING <span className="text-fg">{schedules.length}</span> OF{' '}
          <span className="text-fg">{total}</span> · OWNED + ADMINISTRATIVE
        </span>
        <div className="flex gap-1.5">
          <button
            type="button"
            disabled={cursorStack.length === 0}
            onClick={() => setCursorStack((s) => s.slice(0, -1))}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            ← prev
          </button>
          <button
            type="button"
            disabled={!data?.next_cursor}
            onClick={() => data?.next_cursor && setCursorStack((s) => [...s, data.next_cursor])}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            next 50 →
          </button>
        </div>
      </div>
    </div>
  )
}
