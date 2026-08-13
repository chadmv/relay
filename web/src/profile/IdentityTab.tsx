import { useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { useAuth } from '../auth/AuthProvider'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { Chip, GlassPanel, PillButton } from '../components/holo'
import { updateMe } from './api'

// The Identity tab. Renames the signed-in user through PATCH /v1/users/me and
// pushes the authoritative 200 body back into AuthProvider.
//
// The hi-fi's Activity side card (hifi3-holo-pages.jsx:2946-2965) is NOT built:
// three of its four rows - Last login, Login count, Active sessions - have no
// backing column or endpoint anywhere, and MEMBER SINCE, the one real value, lives
// in the page header. Rendering "-" for three of four rows is the same mistake the
// admin VERSION/BUILD/DB/UPTIME strip avoided (AdminPage.tsx:6-14).
export function IdentityTab() {
  const { user, applyUser } = useAuth()

  // The draft is null until the user types. While it is null the field simply
  // shows the live user row, which is correct because nothing polls here and the
  // only writer of that row is this form. Once it is a string it is NEVER
  // re-derived from `user`, so a save landing (which changes `user`) cannot reset
  // a field mid-edit UNLESS the field still holds exactly what that save sent -
  // see the onSuccess guard below. Note the null-versus-empty distinction:
  // clearing the input gives '', which is a real edit, not a fall-through to
  // user.name.
  const [draft, setDraft] = useState<string | null>(null)

  const save = useMutation({
    mutationFn: (nextName: string) => updateMe(nextName),
    onSuccess: (updated, submittedName) => {
      // ONE owner of identity. The PATCH response is the same userResponse struct
      // GET /v1/users/me returns (internal/api/users.go:429, :410), so it is
      // authoritative and needs no confirming round trip - and pushing it here
      // avoids a second ['me'] query that could disagree with the provider.
      applyUser(updated)
      // Releasing the draft ONLY if it still equals what THIS save submitted.
      // The mutation's onSuccess is a promise continuation, not part of the
      // submit's own commit, so a newer keystroke can land in `draft` before a
      // slow PATCH settles. Clearing unconditionally here is CLAUDE.md's
      // "dying generation clobbers state a later action already set" shape:
      // the settled save must never overwrite an edit typed after it was sent.
      setDraft((current) => (current === submittedName ? null : current))
    },
  })

  if (!user) return null
  const name = draft ?? user.name

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!user) return
    const trimmed = name.trim()
    // Reset BEFORE the no-op early return, not just before mutate(). A stale
    // alert or success banner from a previous submit must not survive a Save
    // that the form now considers a no-op: retyping the original name after a
    // 400, or clicking Save again after a success, both clear the banner even
    // though neither sends a request.
    save.reset()
    // Changed-fields-only, degenerating to "send nothing when unchanged" because
    // PATCH takes exactly one field. Same construction as WorkerEditForm.tsx:42-45.
    // Compared against the TRIMMED draft because the server trims before storing
    // (internal/api/users.go:61), so "Mira  " and "Mira" are the same row and a
    // whitespace-only edit must not issue a write.
    if (trimmed === user.name) return
    // No client-side empty-name guard, deliberately: the server's own
    // `name is required` 400 (users.go:63) is the message we would otherwise
    // duplicate, and there is no second field here to protect from a wasted round
    // trip. One error-rendering path, not two.
    save.mutate(trimmed)
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="max-w-[560px] p-6">
      <div className="mb-4 flex items-baseline justify-between">
        <span className="text-[13px] text-fg">Identity</span>
        <span className="font-mono text-[10px] tracking-[0.06em] text-fg-dim">
          PATCH /v1/users/me
        </span>
      </div>

      <Field label="Display name" htmlFor="profile-name">
        <Input
          id="profile-name"
          value={name}
          autoComplete="name"
          onChange={(e) => setDraft(e.target.value)}
        />
      </Field>

      {/* Not a deferral and not "coming soon": email is immutable through the
          entire API and store layer - internal/store/query/users.sql has no
          `UPDATE users SET email` anywhere. The hint states the real remedy. */}
      <Field
        label="Email"
        htmlFor="profile-email"
        hint="identity - contact your admin to change"
      >
        <Input id="profile-email" value={user.email} disabled readOnly />
      </Field>

      <div className="mb-3 flex items-center gap-2.5 rounded-md border border-border bg-white/[0.02] px-3 py-2.5">
        <Chip tone={user.is_admin ? 'accent' : 'muted'}>{user.is_admin ? 'ADMIN' : 'USER'}</Chip>
        <span className="text-[12px] leading-relaxed text-fg-mute">
          Role is server-side only - promote or demote from{' '}
          <span className="text-fg">Admin -&gt; Users</span>.
        </span>
      </div>

      {save.error && (
        <div role="alert" className="mb-3 text-[11px] text-err">
          {save.error.message}
        </div>
      )}
      {save.isSuccess && (
        <div role="status" className="mb-3 text-[11px] text-ok">
          Display name updated.
        </div>
      )}

      <div className="flex gap-2">
        <PillButton type="submit" variant="primary" disabled={save.isPending}>
          Save changes
        </PillButton>
        <PillButton
          onClick={() => {
            setDraft(null)
            save.reset()
          }}
        >
          Cancel
        </PillButton>
      </div>
    </GlassPanel>
  )
}
