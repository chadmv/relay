import { useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { GlassPanel, PillButton } from '../components/holo'
import { changePassword } from './api'

// The Password tab. Three fields, three client-side guards, one PUT.
//
// The hi-fi's strength meter (hifi3-holo-pages.jsx:2994-3004) is NOT built: the
// server's only rule on the new password is len(...) >= 8
// (internal/api/auth.go:284-287), so a meter would assert a complexity policy
// that does not exist. Its "Forgot your password?" side card (:3021-3034) is also
// out: it is accurate, but it is documentation aimed at a locked-out user, who by
// definition cannot reach a page behind the login wall.
export function PasswordTab() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [guardError, setGuardError] = useState<string | undefined>()

  const change = useMutation({
    // NO mutation VARIABLES, on purpose. mutate() is called with no argument and
    // this closure reads the fields instead.
    //
    // TanStack stores whatever you pass to mutate() on the mutation's
    // state.variables (@tanstack/query-core mutation.js:94) and keeps the SETTLED
    // mutation in the MutationCache for the 5-minute default gcTime, so passing
    // the plaintext password as a variable would leave it readable from
    // queryClient.getMutationCache().getAll() long after this form cleared its
    // inputs. reset() does not help - it removes only this observer and
    // reschedules the same GC (mutationObserver.js:50-55, mutation.js:38-46).
    // Clearing the inputs is not evidence; PasswordTab.auth.test.tsx asserts
    // absence from the store the library actually keeps.
    mutationFn: () => changePassword(current, next),
    onSuccess: () => {
      setCurrent('')
      setNext('')
      setConfirm('')
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    // Three guards, in this order, each blocking the request.
    if (next !== confirm) {
      setGuardError('The two passwords do not match.')
      return
    }
    // The shipped literal, copied verbatim from RegisterScreen.tsx:31-32,
    // ResetPasswordDialog.tsx:36 and CreateUserForm.tsx:40. Deliberately copied
    // rather than extracted: two lines with no decision inside them become
    // indirection when hidden behind a helper.
    if (next.length < 8) {
      setGuardError('Password must be at least 8 characters.')
      return
    }
    // BYTE length, not .length. bcrypt.GenerateFromPassword rejects over 72 bytes
    // and handleChangePassword maps that to an opaque 500
    // (internal/api/auth.go:303-307), so a routine password-manager password
    // would produce a server error with no explanation. A 40-character passphrase
    // with accents or emoji can exceed 72 bytes while passing a .length check.
    // Same guard as ResetPasswordDialog.tsx:43-46.
    if (new TextEncoder().encode(next).length > 72) {
      setGuardError('Password must be 72 bytes or fewer.')
      return
    }
    setGuardError(undefined)
    change.reset()
    change.mutate()
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="max-w-[560px] p-6">
      <div className="mb-4 flex items-baseline justify-between">
        <span className="text-[13px] text-fg">Change password</span>
        <span className="font-mono text-[10px] tracking-[0.06em] text-fg-dim">
          PUT /v1/users/me/password
        </span>
      </div>

      <Field label="Current password" htmlFor="pw-current">
        <Input
          id="pw-current"
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
      </Field>
      <Field label="New password" htmlFor="pw-new" hint="min 8 characters">
        <Input
          id="pw-new"
          type="password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
      </Field>
      <Field label="Confirm new password" htmlFor="pw-confirm" error={guardError}>
        <Input
          id="pw-confirm"
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
      </Field>

      {/* A verified consequence, not a hedge. The handler revokes every OTHER
          token in the same transaction as the password write and keeps the
          caller's own (internal/api/auth.go:325-328 ->
          internal/store/query/tokens.sql:28-29 `AND id <> $2`), so this browser
          survives and every other browser, device and `relay` CLI login gets a
          401 on its next request. */}
      <div
        data-testid="password-session-warning"
        className="mb-3 rounded-md border border-warn/35 bg-warn/[0.08] px-3 py-2.5 text-[12px] leading-relaxed text-fg"
      >
        All of your <b>other</b> sessions will be signed out, including any{' '}
        <span className="font-mono">relay</span> CLI login. This browser stays signed in.
      </div>

      {change.error && (
        <div role="alert" className="mb-3 text-[11px] text-err">
          {change.error.message}
        </div>
      )}
      {change.isSuccess && (
        <div role="status" className="mb-3 text-[11px] text-ok">
          Password updated. Your other sessions have been signed out.
        </div>
      )}

      <div className="flex gap-2">
        <PillButton type="submit" variant="primary" disabled={change.isPending}>
          Update password
        </PillButton>
        <PillButton
          onClick={() => {
            setCurrent('')
            setNext('')
            setConfirm('')
            setGuardError(undefined)
            change.reset()
          }}
        >
          Cancel
        </PillButton>
      </div>
    </GlassPanel>
  )
}
