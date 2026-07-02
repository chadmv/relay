import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { ApiError, apiFetch } from '../lib/api'
import { GlassPanel, Eyebrow, PillButton } from '../components/holo'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import type { ConfigResponse } from '../lib/types'
import { useAuth } from './AuthProvider'

export function RegisterScreen() {
  const { register } = useAuth()
  const [selfRegister, setSelfRegister] = useState<boolean | null>(null)
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [invite, setInvite] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [emailExists, setEmailExists] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    apiFetch<ConfigResponse>('/config')
      .then((c) => setSelfRegister(c.allow_self_register))
      .catch(() => setSelfRegister(false))
  }, [])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setEmailExists(false)
    if (password.length < 8) {
      setError('Password must be at least 8 characters.')
      return
    }
    setBusy(true)
    try {
      await register({
        email,
        name,
        password,
        invite_token: selfRegister ? undefined : invite,
      })
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setEmailExists(true)
      } else if (err instanceof ApiError) {
        setError(err.code)
      } else {
        setError('Something went wrong.')
      }
    } finally {
      setBusy(false)
    }
  }

  if (selfRegister === null) {
    return <div className="flex min-h-screen items-center justify-center bg-bg" />
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg">
      <GlassPanel as="form" onSubmit={onSubmit} className="w-[360px] p-6">
        <Eyebrow>REGISTER</Eyebrow>
        <h1 className="text-[28px] font-normal tracking-tight">Create your relay account</h1>
        <div className="mb-5 text-[13px] text-fg-mute">
          {selfRegister ? 'Open registration is enabled.' : 'You need an invite to register.'}
        </div>

        <Field label="Display name" htmlFor="name">
          <Input id="name" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Email" htmlFor="email">
          <Input
            id="email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        {!selfRegister && (
          <Field label="Invite token" htmlFor="invite" error={error ?? undefined}>
            <Input
              id="invite"
              value={invite}
              onChange={(e) => setInvite(e.target.value)}
              className="font-mono text-[11px] text-accent"
            />
          </Field>
        )}
        <Field
          label="Password"
          htmlFor="password"
          hint="min 8 characters"
          error={selfRegister ? (error ?? undefined) : undefined}
        >
          <Input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>

        {emailExists && (
          <div
            role="alert"
            className="mb-3 rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err"
          >
            That email is already registered.
          </div>
        )}

        <PillButton
          variant="primary"
          type="submit"
          disabled={busy}
          className="w-full justify-center"
        >
          Create account →
        </PillButton>

        <div className="mt-4 text-center text-[11px] text-fg-mute">
          Already have an account?{' '}
          <Link to="/auth" className="text-accent">
            Sign in
          </Link>
        </div>
      </GlassPanel>
    </div>
  )
}
