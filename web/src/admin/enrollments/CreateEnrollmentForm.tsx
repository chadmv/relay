import { useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { GlassPanel, PillButton } from '../../components/holo'
import { Input } from '../../components/Input'
import { DEFAULT_TTL_SECONDS, TTL_PRESETS, type CreateEnrollmentBody } from './api'

interface CreateEnrollmentFormProps {
  pending: boolean
  error: Error | null
  onSubmit: (body: CreateEnrollmentBody) => void
  onCancel: () => void
}

const PRESET = 'flex-1 rounded-[6px] border px-2.5 py-1.5 font-mono text-[11px] tracking-[0.06em]'
const PRESET_ON = `${PRESET} border-accent/60 bg-accent/20 text-fg`
const PRESET_OFF = `${PRESET} border-border bg-white/[0.04] text-fg-mute`

// Inline create panel, mirroring CreateUserForm rather than the hi-fi's modal
// (hifi3-holo-pages.jsx:2340): it keeps exactly one un-trapped dialog on screen
// at a time - the reveal - and adds no modal machinery for two fields.
//
// Deliberately tab-local, not shared with the future Invites tab: invites take an
// email that BINDS the invite, different presets, and a different endpoint. The
// hi-fi models that with an `isInvite` boolean; keeping the halves separate is why
// only the reveal is shared.
export function CreateEnrollmentForm({
  pending,
  error,
  onSubmit,
  onCancel,
}: CreateEnrollmentFormProps) {
  const [hint, setHint] = useState('')
  const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)

  function submit(e: FormEvent) {
    e.preventDefault()
    const trimmed = hint.trim()
    // ttl is always one of TTL_PRESETS, every value of which is inside the
    // server's [60, 604800] window (asserted in api.test.ts), so there is no
    // client-side TTL validation to do - the invalid range is unreachable rather
    // than merely rejected. No hostname_hint validation either: the server accepts
    // any string and stores it as an advisory label.
    onSubmit(trimmed ? { hostname_hint: trimmed, ttl_seconds: ttl } : { ttl_seconds: ttl })
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field
        label="Hostname hint"
        htmlFor="new-enrollment-hint"
        hint="Optional advisory label. Omitted from the request when blank."
      >
        <Input
          id="new-enrollment-hint"
          placeholder="farm-west-13"
          value={hint}
          onChange={(e) => setHint(e.target.value)}
        />
      </Field>

      <div className="mb-3">
        <span className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">
          Expires in
        </span>
        <div className="flex gap-1.5">
          {TTL_PRESETS.map((p) => (
            <button
              key={p.label}
              type="button"
              aria-pressed={ttl === p.seconds}
              onClick={() => setTtl(p.seconds)}
              className={ttl === p.seconds ? PRESET_ON : PRESET_OFF}
            >
              {p.label}
            </button>
          ))}
        </div>
        <div className="mt-1 font-mono text-[10.5px] text-fg-dim">
          ttl_seconds - server default 24h, min 60s, max 7d
        </div>
      </div>

      <div className="mb-3 rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[10.5px] leading-relaxed tracking-[0.04em] text-warn">
        ⚠ The raw token is returned once, in the dialog that opens next. It cannot be retrieved
        again.
      </div>

      {error && <div className="mb-3 text-[11px] text-err">{error.message}</div>}

      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Enroll
        </PillButton>
      </div>
    </GlassPanel>
  )
}
