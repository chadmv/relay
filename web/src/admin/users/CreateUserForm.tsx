import { useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { GlassPanel, PillButton } from '../../components/holo'
import { Input } from '../../components/Input'
import { ApiError } from '../../lib/api'
import type { CreateUserBody } from './api'

interface CreateUserFormProps {
  pending: boolean
  error: Error | null
  onSubmit: (body: CreateUserBody) => void
  onCancel: () => void
}

// Inline create panel (mirrors WorkerEditForm's inline-toggle rather than a modal,
// since it is a multi-field form). This is the only surface that can set is_admin:
// no endpoint mutates it after creation.
export function CreateUserForm({ pending, error, onSubmit, onCancel }: CreateUserFormProps) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [isAdmin, setIsAdmin] = useState(false)
  const [emailError, setEmailError] = useState<string | undefined>()
  const [passwordError, setPasswordError] = useState<string | undefined>()

  // 409 is the duplicate-email case: show it on the email field and keep the form
  // state so the admin can edit and retry. Anything else is a form-level message.
  const duplicate = error instanceof ApiError && error.status === 409
  const formError = error && !duplicate ? error.message : undefined

  function submit(e: FormEvent) {
    e.preventDefault()
    const trimmedEmail = email.trim()
    if (!trimmedEmail) {
      setEmailError('Email is required.')
      return
    }
    setEmailError(undefined)
    if (password.length < 8) {
      setPasswordError('Password must be at least 8 characters.')
      return
    }
    setPasswordError(undefined)
    onSubmit({ email: trimmedEmail, name: name.trim(), password, is_admin: isAdmin })
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field
        label="Email"
        htmlFor="new-user-email"
        error={emailError ?? (duplicate ? 'That email is already registered.' : undefined)}
      >
        <Input id="new-user-email" value={email} onChange={(e) => setEmail(e.target.value)} />
      </Field>
      <Field label="Name" htmlFor="new-user-name" hint="Defaults to the email when blank.">
        <Input id="new-user-name" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Password" htmlFor="new-user-password" error={passwordError}>
        <Input
          id="new-user-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
      <label
        htmlFor="new-user-admin"
        className="mb-3 flex items-center gap-2 text-[12px] text-fg-mute"
      >
        <input
          id="new-user-admin"
          type="checkbox"
          checked={isAdmin}
          onChange={(e) => setIsAdmin(e.target.checked)}
          className="accent-accent"
        />
        Admin
        <span className="font-mono text-[11px] text-fg-dim">is_admin - set once, at creation</span>
      </label>
      {formError && <div className="mb-3 text-[11px] text-err">{formError}</div>}
      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Create
        </PillButton>
      </div>
    </GlassPanel>
  )
}
