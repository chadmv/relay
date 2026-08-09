import { useEffect, useId, useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { Input } from '../../components/Input'

interface ResetPasswordDialogProps {
  email: string
  pending: boolean
  onSubmit: (newPassword: string) => void
  onCancel: () => void
}

// A sibling of ConfirmDialog, not a variant of it: ConfirmDialog takes a text-only
// `body` and cannot host form fields. Matches ConfirmDialog's a11y baseline -
// role="dialog", aria-modal, labelled by its title, Escape dismisses, first field
// focused on open (via autoFocus, so the shared Input primitive does not need to
// forward a ref). No focus trap, same as ConfirmDialog; that debt is tracked by
// docs/backlog/idea-2026-07-01-confirmdialog-focus-trap-hardening.md.
export function ResetPasswordDialog({ email, pending, onSubmit, onCancel }: ResetPasswordDialogProps) {
  const titleId = useId()
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | undefined>()

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onCancel])

  function submit(e: FormEvent) {
    e.preventDefault()
    if (password.length < 8) {
      setError('Password must be at least 8 characters.')
      return
    }
    if (password !== confirm) {
      setError('The two passwords do not match.')
      return
    }
    setError(undefined)
    onSubmit(password)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <form
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onSubmit={submit}
        className="w-full max-w-sm rounded-card border border-border bg-bg p-5 shadow-xl"
      >
        <h2 id={titleId} className="text-[15px] font-medium text-fg">
          Reset password for {email}?
        </h2>
        <p className="mb-4 mt-2 text-[13px] text-fg-mute">
          This revokes every session belonging to that user, so they must sign in again. If that
          user is you, you will be signed out immediately.
        </p>
        <Field label="New password" htmlFor="reset-pw">
          <Input
            id="reset-pw"
            type="password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <Field label="Confirm password" htmlFor="reset-pw-confirm" error={error}>
          <Input
            id="reset-pw-confirm"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </Field>
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={pending}
            className="rounded-md border border-err/50 bg-err/20 px-3 py-1.5 text-[12px] font-medium text-err disabled:opacity-40"
          >
            Reset password
          </button>
        </div>
      </form>
    </div>
  )
}
