import { useState } from 'react'
import { Field } from '../components/Field'
import { GlassPanel, PillButton } from '../components/holo'
import { Input } from '../components/Input'
import type { Worker, WorkerPatch } from './api'

interface LabelRow {
  key: string
  value: string
}

function toRows(labels: Record<string, string> | null): LabelRow[] {
  if (!labels) return []
  return Object.entries(labels).map(([key, value]) => ({ key, value }))
}

function rowsToMap(rows: LabelRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const r of rows) {
    const key = r.key.trim()
    if (key) out[key] = r.value
  }
  return out
}

function sameMap(a: Record<string, string>, b: Record<string, string>): boolean {
  const ak = Object.keys(a)
  const bk = Object.keys(b)
  if (ak.length !== bk.length) return false
  return ak.every((k) => a[k] === b[k])
}

interface WorkerEditFormProps {
  worker: Worker
  pending: boolean
  onSubmit: (patch: WorkerPatch) => void
  onCancel: () => void
  // Distinguishes ids/htmlFor when two mounts of this form can be open at once
  // (the header Edit pill and the Labels "+ add label" chip each own their own
  // editing state). Defaults to "worker" to keep prior ids for back-compat.
  idPrefix?: string
}

// Inline edit form for name / labels / max_slots. Builds a WorkerPatch with only
// changed fields; labels, when changed, is submitted as the full rebuilt map
// (the server does a full replace of the label map, not a per-key merge).
export function WorkerEditForm({ worker, pending, onSubmit, onCancel, idPrefix = 'worker' }: WorkerEditFormProps) {
  const [name, setName] = useState(worker.name)
  const [maxSlots, setMaxSlots] = useState(String(worker.max_slots))
  const [rows, setRows] = useState<LabelRow[]>(toRows(worker.labels))
  const [nameError, setNameError] = useState<string | undefined>()
  const [maxSlotsError, setMaxSlotsError] = useState<string | undefined>()

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const trimmedName = name.trim()
    const nameChanged = name !== worker.name
    if (nameChanged && !trimmedName) {
      setNameError('Name is required.')
      return
    }
    setNameError(undefined)

    const nextSlots = Number(maxSlots)
    const slotsChanged = maxSlots.trim() !== String(worker.max_slots)
    const slotsValid = Number.isInteger(nextSlots) && nextSlots >= 1
    if (slotsChanged && !slotsValid) {
      setMaxSlotsError('Max slots must be a whole number of at least 1.')
      return
    }
    setMaxSlotsError(undefined)

    const patch: WorkerPatch = {}
    if (nameChanged) patch.name = trimmedName
    if (slotsChanged && slotsValid) patch.max_slots = nextSlots
    const nextLabels = rowsToMap(rows)
    if (!sameMap(nextLabels, worker.labels ?? {})) patch.labels = nextLabels
    onSubmit(patch)
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field label="Name" htmlFor={`${idPrefix}-name`} error={nameError}>
        <Input id={`${idPrefix}-name`} value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Max slots" htmlFor={`${idPrefix}-slots`} error={maxSlotsError}>
        <Input
          id={`${idPrefix}-slots`}
          type="number"
          value={maxSlots}
          onChange={(e) => setMaxSlots(e.target.value)}
        />
      </Field>
      <div className="mb-3">
        <div className="mb-1 font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">Labels</div>
        <div className="flex flex-col gap-1.5">
          {rows.map((row, i) => (
            <div key={i} className="flex items-center gap-1.5">
              <Input
                placeholder="key"
                value={row.key}
                onChange={(e) =>
                  setRows(rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)))
                }
              />
              <Input
                placeholder="value"
                value={row.value}
                onChange={(e) =>
                  setRows(rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))
                }
              />
              <button
                type="button"
                aria-label={`Remove ${row.key}`}
                onClick={() => setRows(rows.filter((_, j) => j !== i))}
                className="shrink-0 rounded-md border border-border px-2 py-1 text-[11px] text-fg-mute"
              >
                x
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          onClick={() => setRows([...rows, { key: '', value: '' }])}
          className="mt-1.5 rounded-md border border-border px-2 py-1 text-[11px] text-fg-mute"
        >
          Add label
        </button>
      </div>
      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Save
        </PillButton>
      </div>
    </GlassPanel>
  )
}
