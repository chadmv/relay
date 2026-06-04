import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { Button } from '../components/Button'
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
      <form
        onSubmit={onSubmit}
        className="w-[320px] rounded-card border border-border bg-white/5 p-6 backdrop-blur"
      >
        <div className="mb-1 font-sans text-[32px] font-bold leading-none">
          relay<span className="text-accent">.</span>
        </div>
        <div className="mb-5 text-[12px] text-fg-mute">Sign in to the coordinator</div>

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

        {error && <div className="mb-3 text-[12px] text-err">{error}</div>}

        <Button type="submit" disabled={busy}>
          Sign in →
        </Button>

        <div className="mt-4 text-center text-[11px] text-fg-mute">
          New here?{' '}
          <Link to="/register" className="text-accent">
            Create an account
          </Link>
        </div>
        <div className="mt-2 text-center text-[10px] text-fg-dim">
          Tokens last 30 days.
        </div>
      </form>
    </div>
  )
}
