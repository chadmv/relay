import { useState } from 'react'
import { Input } from '../../components/Input'
import { useWorkerOptions, WORKER_PICKER_LIMIT } from './useWorkerOptions'

interface WorkerPickerProps {
  value: string[]
  onChange: (ids: string[]) => void
}

// Controlled multi-select over the loaded worker page. A free-text UUID field was the
// alternative and was rejected: worker UUIDs appear nowhere in the SPA's UI text, so
// an admin could not verify what they typed.
//
// The query lives here rather than in the form so it mounts only while the create
// panel is open - which is also why ReservationsTab tests need no /v1/workers handler
// until they open the panel.
export function WorkerPicker({ value, onChange }: WorkerPickerProps) {
  const [filter, setFilter] = useState('')
  const { data, error, isLoading } = useWorkerOptions()

  const workers = data?.items ?? []
  const total = data?.total ?? 0
  // Client-side filter over the ALREADY LOADED set only. It issues no request, and it
  // therefore cannot reach a worker outside the loaded page - which is exactly why the
  // ceiling below is stated rather than hidden.
  const q = filter.trim().toLowerCase()
  const shown = q
    ? workers.filter(
        (w) => w.name.toLowerCase().includes(q) || w.hostname?.toLowerCase().includes(q),
      )
    : workers

  const selected = new Set(value)

  function toggle(id: string) {
    const next = new Set(selected)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    // Emit in the LOADED (name-sorted) order, never click order, so the submitted
    // worker_ids array is a deterministic function of the selection. Selections can
    // only originate from `workers`, so nothing is dropped by this projection.
    onChange(workers.filter((w) => next.has(w.id)).map((w) => w.id))
  }

  return (
    <div className="mb-3">
      <span className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">
        Workers to reserve
      </span>

      {isLoading && <div className="text-[12px] text-fg-mute">Loading workers…</div>}
      {error && <div className="text-[12px] text-err">{(error as Error).message}</div>}

      {!isLoading && !error && workers.length === 0 && (
        <div className="text-[12px] text-fg-mute">No workers are registered.</div>
      )}

      {workers.length > 0 && (
        <>
          <Input
            aria-label="Filter workers"
            placeholder="filter by name or hostname"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="py-1 text-[12px]"
          />
          <div className="mt-1.5 max-h-48 overflow-y-auto rounded-[6px] border border-border bg-black/20 px-2.5 py-1.5">
            {shown.map((w) => (
              <label
                key={w.id}
                className="flex cursor-pointer items-center gap-2 py-0.5 text-[12px] text-fg-mute"
              >
                <input
                  type="checkbox"
                  className="accent-accent"
                  checked={selected.has(w.id)}
                  onChange={() => toggle(w.id)}
                />
                <span className="font-sans text-fg">{w.name}</span>
                <span className="font-mono text-[10.5px] text-fg-dim">{w.status}</span>
              </label>
            ))}
            {shown.length === 0 && (
              <div className="py-1 text-[11.5px] text-fg-dim">No workers match that filter.</div>
            )}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 font-mono text-[10.5px] text-fg-dim">
            <span>{value.length} selected</span>
            {/* The ceiling, STATED. One request at the server's maxLimit and no
                cursor, so a fleet larger than the loaded page is genuinely
                unreachable from this control - it must never look complete. */}
            {total > workers.length && (
              <span className="text-warn">
                showing first {WORKER_PICKER_LIMIT} of {total} workers by name - use the CLI for
                workers beyond this page
              </span>
            )}
          </div>
        </>
      )}
    </div>
  )
}
