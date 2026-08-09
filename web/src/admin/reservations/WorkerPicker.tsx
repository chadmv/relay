import { useId, useState } from 'react'
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
  const groupLabelId = useId()

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
  const loadedIds = new Set(workers.map((w) => w.id))
  // A `value` id absent from the loaded page (revoked/renamed since selection, or a
  // refetch simply landed a different page while the panel sat open - the query's
  // staleTime only DELAYS a refetch, it does not disable refetchOnWindowFocus). The
  // note below is what lets the admin decide rather than the id being silently
  // dropped or silently kept with no visibility either way.
  const staleSelected = data ? value.filter((id) => !loadedIds.has(id)) : []

  function toggle(id: string) {
    const alreadySelected = selected.has(id)
    // Work from `value` itself, not from a projection through `workers` - the
    // previous version rebuilt the whole emitted array as
    // `workers.filter(w => next.has(w.id))`, which can only ever contain LOADED
    // ids, so any stale id in `value` vanished the instant any OTHER box was
    // toggled. Only ever add/remove the one id this call is actually about.
    const nextValues = alreadySelected ? value.filter((v) => v !== id) : [...value, id]
    // Re-sort the loaded subset into loaded (name-sorted) order so the submitted
    // worker_ids array is a deterministic function of the selection and not of
    // click order; ids with no loaded position (stale) are appended after,
    // preserving them rather than losing them.
    const known = workers.filter((w) => nextValues.includes(w.id)).map((w) => w.id)
    const stale = nextValues.filter((v) => !loadedIds.has(v))
    onChange([...known, ...stale])
  }

  function removeStale(id: string) {
    onChange(value.filter((v) => v !== id))
  }

  return (
    <div className="mb-3">
      <span
        id={groupLabelId}
        className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute"
      >
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
          <div
            role="group"
            aria-labelledby={groupLabelId}
            className="mt-1.5 max-h-48 overflow-y-auto rounded-[6px] border border-border bg-black/20 px-2.5 py-1.5"
          >
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

      {staleSelected.length > 0 && (
        <div className="mt-1.5 flex flex-col gap-1 rounded-[6px] border border-warn/40 bg-warn/10 px-2.5 py-1.5 font-mono text-[10.5px] text-warn">
          {staleSelected.map((id) => (
            <div key={id} className="flex items-center justify-between gap-2">
              <span>{id.slice(0, 8)} - no longer on this page (may have been revoked)</span>
              <button
                type="button"
                className="underline"
                aria-label={`remove ${id.slice(0, 8)} from the selection`}
                onClick={() => removeStale(id)}
              >
                remove
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
