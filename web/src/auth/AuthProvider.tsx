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
  // Replaces the in-memory user row with an authoritative server response.
  // PATCH /v1/users/me returns the same userResponse struct GET /v1/users/me
  // returns (internal/api/users.go:429 and :410 both call toUserResponse), so
  // there is nothing to confirm with a second round trip. This exists so the
  // profile page does NOT introduce a second ['me'] query: one owner of
  // identity, not two caches that can disagree.
  applyUser: (u: User) => void
  // Local-only session teardown: forget the token, forget the user, go anonymous,
  // drop the query cache. Issues NO request, on purpose.
  //
  // Its one caller is the Sessions tab, and by the time it runs the server has
  // already destroyed EVERY bearer token for this user - DELETE /v1/auth/tokens
  // is DeleteTokensForUser, `DELETE FROM api_tokens WHERE user_id = $1`
  // (internal/store/query/tokens.sql:25-26), with no `id <> $2`. Any request made
  // after that point is a guaranteed 401 racing this teardown, which is exactly
  // why logout() is NOT reused there: logout() would first fire
  // DELETE /v1/auth/token against a token that no longer exists.
  //
  // The ORDER inside it is CLAUDE.md's "end the generation before releasing the
  // resource": setStatus('anonymous') is what ends the generation, because
  // ProtectedRoute re-renders to <Navigate to="/auth" replace/> in the same
  // commit and unmounts every page and every active query observer, so nothing
  // is left holding a continuation that could fire against the dead credential.
  clearSession: () => void
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

  function clearSession() {
    clearToken()
    setUser(null)
    setStatus('anonymous')
    queryClient.clear()
  }

  function applyUser(u: User) {
    setUser(u)
  }

  async function logout() {
    await apiFetch('/auth/token', { method: 'DELETE' }).catch(() => {})
    clearSession()
  }

  return (
    <AuthContext.Provider
      value={{ status, user, login, register, logout, applyUser, clearSession }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
