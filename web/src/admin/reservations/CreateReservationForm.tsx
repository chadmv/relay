import { useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { GlassPanel, PillButton } from '../../components/holo'
import { Input } from '../../components/Input'
import { WorkerPicker } from './WorkerPicker'
import type { CreateReservationBody } from './api'

interface CreateReservationFormProps {
  pending: boolean
  error: Error | null
  onSubmit: (body: CreateReservationBody) => void
  onCancel: () => void
}

// Inline create panel, mirroring CreateEnrollmentForm rather than a modal. The hi-fi
// has no create form for reservations at all (its `+ Reserve workers` button is
// inert), so this surface is designed in the spec, not copied.
//
// Client validation is STRICTER than the server, deliberately. The handler validates
// only name != "" and UUID syntax (internal/api/reservations.go:243-274), so it will
// happily persist rows that can never do anything:
//  - worker_ids: [] reserves nothing, because reservedIDs is built only from that
//    array (internal/scheduler/dispatch.go:186-191).
//  - ends_at <= starts_at can never satisfy ListActiveReservations
//    (internal/store/query/reservations.sql:21-22).
// NOT blocked, on purpose: a window entirely in the past (a legitimate historical
// record) and duplicate names (the table has no unique constraint, and inventing one
// client-side would be a lie about the data model).
//
// selector and user_id are absent by design - see api.ts.
export function CreateReservationForm({
  pending,
  error,
  onSubmit,
  onCancel,
}: CreateReservationFormProps) {
  const [name, setName] = useState('')
  const [project, setProject] = useState('')
  const [workerIds, setWorkerIds] = useState<string[]>([])
  const [startsAt, setStartsAt] = useState('')
  const [endsAt, setEndsAt] = useState('')
  // Also-worth-fixing (review 2026-08-09): gates the validation list, matching
  // CreateUserForm's pattern of only surfacing a field error once the admin has
  // done something - opening a blank panel must not greet them with two error
  // messages before they have typed anything. One shared flag (rather than
  // per-field touched state) is enough: every test that expects a message visible
  // interacts with SOME field first, which is also the realistic case - an admin
  // who has touched nothing has nothing to correct yet.
  const [touched, setTouched] = useState(false)

  const trimmedName = name.trim()
  const nameMissing = trimmedName === ''
  const noWorkers = workerIds.length === 0
  // Zero-length counts as inverted: starts == ends is an empty window.
  const windowInverted =
    startsAt !== '' &&
    endsAt !== '' &&
    new Date(endsAt).getTime() <= new Date(startsAt).getTime()
  const valid = !nameMissing && !noWorkers && !windowInverted

  function submit(e: FormEvent) {
    e.preventDefault()
    setTouched(true)
    if (!valid) return
    const body: CreateReservationBody = { name: trimmedName, worker_ids: workerIds }
    const p = project.trim()
    if (p) body.project = p
    // datetime-local yields a zone-less local string ('2026-08-10T09:00'), which Go's
    // time.Time decoder rejects. new Date(...) on a date-TIME form is interpreted as
    // LOCAL per the ES spec, so toISOString() produces the correct instant for what
    // the admin typed.
    if (startsAt) body.starts_at = new Date(startsAt).toISOString()
    if (endsAt) body.ends_at = new Date(endsAt).toISOString()
    onSubmit(body)
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field label="Name" htmlFor="new-reservation-name" hint="Required. Not unique server-side.">
        <Input
          id="new-reservation-name"
          placeholder="gpu-farm-hold"
          value={name}
          onChange={(e) => {
            setTouched(true)
            setName(e.target.value)
          }}
        />
      </Field>

      <WorkerPicker
        value={workerIds}
        onChange={(ids) => {
          setTouched(true)
          setWorkerIds(ids)
        }}
      />

      <Field
        label="Project"
        htmlFor="new-reservation-project"
        hint="Optional label. Recorded, but never read by the scheduler. Omitted when blank."
      >
        <Input
          id="new-reservation-project"
          placeholder="atlas"
          value={project}
          onChange={(e) => {
            setTouched(true)
            setProject(e.target.value)
          }}
        />
      </Field>

      <div className="mb-3 flex gap-3">
        <div className="flex-1">
          <Field
            label="Starts"
            htmlFor="new-reservation-starts"
            hint="Optional. Open start = in force from creation (effective on the next dispatch cycle)."
          >
            <Input
              id="new-reservation-starts"
              type="datetime-local"
              value={startsAt}
              onChange={(e) => {
                setTouched(true)
                setStartsAt(e.target.value)
              }}
            />
          </Field>
        </div>
        <div className="flex-1">
          <Field
            label="Ends"
            htmlFor="new-reservation-ends"
            hint="Optional. Open end = in force until deleted."
          >
            <Input
              id="new-reservation-ends"
              type="datetime-local"
              value={endsAt}
              onChange={(e) => {
                setTouched(true)
                setEndsAt(e.target.value)
              }}
            />
          </Field>
        </div>
      </div>

      {/* The honest statement of effect, on the surface where the mistake is made.
          The hi-fi's framing implies reserved workers run the owner's work; they are
          excluded from dispatch entirely (internal/scheduler/dispatch.go:221-223). */}
      <div className="mb-3 rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[10.5px] leading-relaxed tracking-[0.04em] text-warn">
        ⚠ Reserving removes these workers from the dispatch pool for every job, including your
        own. They stop receiving new tasks while the reservation is in force, and relay does not
        send your work to them instead.
      </div>

      {touched && !valid && (
        <ul className="mb-3 list-inside list-disc text-[11px] text-err">
          {nameMissing && <li>Name is required.</li>}
          {noWorkers && <li>Select at least one worker.</li>}
          {windowInverted && <li>Ends must be after starts.</li>}
        </ul>
      )}

      {error && <div className="mb-3 text-[11px] text-err">{error.message}</div>}

      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending || !valid}>
          Reserve
        </PillButton>
      </div>
    </GlassPanel>
  )
}
