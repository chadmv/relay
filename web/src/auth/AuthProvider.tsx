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
  // (internal/store/query/tokens.sql:40-41), with no `id <> $2`. Any request made
  // after that point is a guaranteed 401 racing this teardown, which is exactly
  // why logout() is NOT reused there: logout() would first fire
  // DELETE /v1/auth/token against a token that no longer exists.
  //
  // What actually guards this teardown, verified by probe rather than assumed:
  // setStatus('anonymous') does NOT take effect in the same commit as the
  // calls before it just because they are written in sequence here - this
  // function is typically invoked from a mutation's onSuccess, itself a
  // promise continuation, so the eventual React commit is scheduled, not
  // synchronous with this call. And queryClient.clear() does not stop
  // anything already scheduled to refetch: it only evicts cached data, so a
  // still-mounted observer with a refetch interval keeps issuing new requests
  // against the now-empty cache until it is unmounted.
  //
  // What actually prevents an escaped request from doing anything is two
  // things together: clearToken() runs FIRST, so any request that does fire
  // after this point - whether it beats the render or not - carries no
  // Authorization header and cannot act as this user; and setStatus('anonymous')
  // eventually flips ProtectedRoute to <Navigate to="/auth" replace/>, which
  // unmounts every page and every active query observer beneath it, which is
  // what actually stops further requests from being scheduled at all.
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
  //
  // TWO fences, answering two different questions. This is CLAUDE.md's rule in its
  // frontend form: a status check establishes CURRENCY, never IDENTITY. The backend
  // learned it on tasks.status writes, where a matching assignment_epoch proves the
  // caller's generation is current and proves nothing about who the caller is, so
  // every such write also fences on worker_id. Until 2026-08-13 this listener had
  // the currency half and not the identity half.
  //
  // IDENTITY - requestToken !== getToken(). apiFetch and apiStream stamp each 401
  // with the token that request actually attached (lib/api.ts). Without this fence
  // a 401 produced by an ALREADY DEAD credential clears whatever token happens to
  // be in localStorage when it lands, including one issued seconds earlier by a
  // fresh login: sign out everywhere, sign back in, and a straggler 401 from a
  // still-in-flight poll silently undoes the new session with no error message.
  // Nothing cancels in-flight requests at teardown - apiFetch passes no
  // AbortSignal, and queryClient.clear() evicts cached data without aborting a
  // request already on the wire - so that straggler is guaranteed, not theoretical.
  //
  // The comparison reads getToken() FRESH rather than a ref, deliberately.
  // localStorage is the credential's single source of truth and setToken/clearToken
  // write it synchronously, whereas any React-committed mirror lags: applyAuth
  // stores the new token and only THEN awaits /users/me, so a mirror would still
  // say "old" through that whole window and would reject a 401 belonging to the
  // brand-new session - reintroducing this same bug through its own fix.
  //
  // Comparing by VALUE covers replacement as well as removal, so no session
  // generation counter is needed: a token is 32 random bytes (CLAUDE.md, "Token
  // format"), so a later session never reuses an earlier one's string.
  //
  // A 401 arriving DURING clearSession() fails this fence - the token is already
  // gone - and correctly does nothing: clearSession() already did all four of
  // its own statements, synchronously, with clearToken() first.
  //
  // CURRENCY - statusRef.current === 'anonymous'. Still load-bearing, and it is
  // NOT made redundant by the fence above: a failed login on the sign-in screen
  // sends a request with no token while getToken() is also null, so it passes the
  // identity fence BY EQUALITY. This guard is the only thing that stops it churning
  // state and clearing an empty cache on every attempt.
  useEffect(
    () =>
      onUnauthorized((requestToken) => {
        if (requestToken !== getToken()) return
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
