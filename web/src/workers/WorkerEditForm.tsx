import { useState } from 'react'
import { Field } from '../components/Field'
import { GlassPanel, PillButton } from '../components/holo'
import { Input } from '../components/Input'
import type { Worker, WorkerPatch } from './api'

interface WorkerEditFormProps {
  worker: Worker
  pending: boolean
  onSubmit: (patch: WorkerPatch) => void
  onCancel: () => void
}

// Inline edit form for name / max_slots. Builds a WorkerPatch with only changed
// fields. Label editing lives in WorkerLabels (inline, in the Labels panel), not
// here.
export function WorkerEditForm({ worker, pending, onSubmit, onCancel }: WorkerEditFormProps) {
  const [name, setName] = useState(worker.name)
  const [maxSlots, setMaxSlots] = useState(String(worker.max_slots))
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
    onSubmit(patch)
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field label="Name" htmlFor="worker-name" error={nameError}>
        <Input id="worker-name" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Max slots" htmlFor="worker-slots" error={maxSlotsError}>
        <Input
          id="worker-slots"
          type="number"
          value={maxSlots}
          onChange={(e) => setMaxSlots(e.target.value)}
        />
      </Field>
      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Save
        </PillButton>
      </div>
    </GlassPanel>
  )
}
