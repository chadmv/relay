import { useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { GlassPanel, PillButton } from '../../components/holo'
import { Input } from '../../components/Input'
import { DEFAULT_EXPIRES_IN, TTL_PRESETS, type CreateInviteBody } from './api'

interface CreateInviteFormProps {
  pending: boolean
  error: Error | null
  onSubmit: (body: CreateInviteBody) => void
  onCancel: () => void
}

const PRESET = 'flex-1 rounded-[6px] border px-2.5 py-1.5 font-mono text-[11px] tracking-[0.06em]'
const PRESET_ON = `${PRESET} border-accent/60 bg-accent/20 text-fg`
const PRESET_OFF = `${PRESET} border-border bg-white/[0.04] text-fg-mute`

// Inline create panel, mirroring CreateEnrollmentForm rather than the hi-fi's
// modal: it keeps exactly one un-trapped dialog on screen at a time - the reveal -
// and adds no modal machinery for two fields.
//
// Deliberately tab-local and NOT shared with CreateEnrollmentForm, which already
// records the reason at CreateEnrollmentForm.tsx:22-25: invites take an email that
// BINDS the invite, different presets, and a different endpoint. The hi-fi models
// the divergence with an `isInvite` boolean, which is the flag-driven component
// that rots. Only the reveal half is shared.
export function CreateInviteForm({ pending, error, onSubmit, onCancel }: CreateInviteFormProps) {
  const [email, setEmail] = useState('')
  const [expiresIn, setExpiresIn] = useState(DEFAULT_EXPIRES_IN)

  function submit(e: FormEvent) {
    e.preventDefault()
    const trimmed = email.trim()
    // expiresIn is always one of TTL_PRESETS, every value of which is
    // hour-denominated and inside the server's (0, 720h] window (asserted in
    // api.test.ts), so there is no client-side duration validation to do - the
    // invalid range is UNREACHABLE rather than merely rejected. No client-side
    // email validation either: the server runs mail.ParseAddress
    // (internal/api/invites.go:66) and its 400 renders in this form's own error
    // slot. Two parsers disagreeing is worse than one round trip.
    //
    // A body is ALWAYS produced, minimally {expires_in}, because readJSON runs
    // unconditionally on this endpoint (invites.go:27) and an empty POST 400s.
    onSubmit(trimmed ? { email: trimmed, expires_in: expiresIn } : { expires_in: expiresIn })
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field
        label="Email"
        htmlFor="new-invite-email"
        hint="Optional. Binds the invite to one address. Omitted from the request when blank."
      >
        <Input
          id="new-invite-email"
          type="email"
          placeholder="someone@studio.dev"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
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
              aria-pressed={expiresIn === p.value}
              onClick={() => setExpiresIn(p.value)}
              className={expiresIn === p.value ? PRESET_ON : PRESET_OFF}
            >
              {p.label}
            </button>
          ))}
        </div>
        {/* The label says 30d and the wire says 720h; the hint states the server's
            own vocabulary so the two are never confused. */}
        <div className="mt-1 font-mono text-[10.5px] text-fg-dim">
          expires_in - server default 72h, max 720h
        </div>
      </div>

      <div className="mb-3 rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[10.5px] leading-relaxed tracking-[0.04em] text-warn">
        ⚠ The raw token is returned once, in the dialog that opens next. It cannot be retrieved
        again.
      </div>

      {error && <div className="mb-3 text-[11px] text-err">{error.message}</div>}

      <div className="flex justify-end gap-2">
        {/* Disabled while pending: Cancel calls onCancel -> create.reset(), and
            reset() only detaches the mutation observer (query-core
            mutationObserver.js:50-54) - it does not cancel the in-flight
            Mutation.execute. Letting Cancel fire mid-request would strand a
            successful create with zero observers watching it, and gcTime: 0
            would evict the only copy of the token before it is ever rendered. */}
        <PillButton onClick={onCancel} disabled={pending}>
          Cancel
        </PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Create invite
        </PillButton>
      </div>
    </GlassPanel>
  )
}
