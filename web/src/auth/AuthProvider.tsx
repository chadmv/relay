import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { apiFetch, onUnauthorized } from '../lib/api'
import { clearToken, getToken, setToken } from '../lib/token'
import type { LoginResponse, User } from '../lib/types'

type Status = 'loading' | 'authenticated' | 'anonymous'

interface RegisterInput {
  email: string
  name: string
  password: string
  invite_token?: string
}

interface AuthContextValue {
  status: Status
  user: User | null
  login: (email: string, password: string) => Promise<void>
  register: (input: RegisterInput) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('loading')
  const [user, setUser] = useState<User | null>(null)
  const queryClient = useQueryClient()

  // Mirror status in a ref so the 401 subscription can read the latest value
  // without re-subscribing. The effect below mounts once for the provider's life.
  const statusRef = useRef(status)
  statusRef.current = status

  // Reset auth state on any 401 so the route guards send the user to sign-in.
  // Guarded to no-op when already anonymous: a failed login still on the sign-in
  // screen must not churn state or clear an empty cache on every request.
  useEffect(
    () =>
      onUnauthorized(() => {
        if (statusRef.current === 'anonymous') return
        clearToken()
        setUser(null)
        setStatus('anonymous')
        queryClient.clear()
      }),
    [queryClient],
  )

  useEffect(() => {
    if (!getToken()) {
      setStatus('anonymous')
      return
    }
    apiFetch<User>('/users/me')
      .then((u) => {
        setUser(u)
        setStatus('authenticated')
      })
      .catch(() => {
        clearToken()
        setUser(null)
        setStatus('anonymous')
      })
  }, [])

  async function applyAuth(res: LoginResponse) {
    setToken(res.token)
    const me = await apiFetch<User>('/users/me')
    setUser(me)
    setStatus('authenticated')
  }

  async function login(email: string, password: string) {
    const res = await apiFetch<LoginResponse>('/auth/login', {
      method: 'POST',
      json: { email, password },
    })
    await applyAuth(res)
  }

  async function register(input: RegisterInput) {
    const res = await apiFetch<LoginResponse>('/auth/register', {
      method: 'POST',
      json: input,
    })
    await applyAuth(res)
  }

  async function logout() {
    await apiFetch('/auth/token', { method: 'DELETE' }).catch(() => {})
    clearToken()
    setUser(null)
    setStatus('anonymous')
    queryClient.clear()
  }

  return (
    <AuthContext.Provider value={{ status, user, login, register, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
