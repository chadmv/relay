import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { GlassPanel, Eyebrow, PillButton } from '../components/holo'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { useAuth } from './AuthProvider'

export function LoginScreen() {
  const { login } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      await login(email, password)
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setError('Too many attempts. Try again in a minute.')
      } else {
        setError('Invalid email or password.')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg">
      <GlassPanel as="form" onSubmit={onSubmit} className="w-[360px] p-6">
        <Eyebrow>COORDINATOR</Eyebrow>
        <h1 className="text-[28px] font-normal tracking-tight">Sign in</h1>
        <div className="mb-5 text-[13px] text-fg-mute">Sign in to the coordinator</div>

        <Field label="Email" htmlFor="email">
          <Input
            id="email"
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <Field label="Password" htmlFor="password">
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>

        {error && (
          <div
            role="alert"
            className="mb-3 rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err"
          >
            {error}
          </div>
        )}

        <PillButton
          variant="primary"
          type="submit"
          disabled={busy}
          className="w-full justify-center"
        >
          Sign in →
        </PillButton>

        <div className="mt-4 text-center text-[11px] text-fg-mute">
          New here?{' '}
          <Link to="/register" className="text-accent">
            Create an account
          </Link>
        </div>
        <div className="mt-2 text-center text-[10px] text-fg-dim">
          Tokens last 30 days.
        </div>
      </GlassPanel>
    </div>
  )
}
