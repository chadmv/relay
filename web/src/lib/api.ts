import { getToken } from './token'

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

type Listener = () => void
const unauthorizedListeners = new Set<Listener>()

/** Register a callback fired whenever a request returns 401. Returns an unsubscribe fn. */
export function onUnauthorized(fn: Listener): () => void {
  unauthorizedListeners.add(fn)
  return () => unauthorizedListeners.delete(fn)
}

interface ApiOptions extends Omit<RequestInit, 'body'> {
  json?: unknown
}

/** Fetch wrapper: prefixes /v1, attaches bearer token, parses the {error} envelope. */
export async function apiFetch<T = unknown>(path: string, opts: ApiOptions = {}): Promise<T> {
  const { json, headers, ...rest } = opts
  const finalHeaders = new Headers(headers)
  const token = getToken()
  if (token) finalHeaders.set('Authorization', `Bearer ${token}`)
  if (json !== undefined) {
    finalHeaders.set('Content-Type', 'application/json')
  }

  const res = await fetch(`/v1${path}`, {
    ...rest,
    headers: finalHeaders,
    body: json !== undefined ? JSON.stringify(json) : undefined,
  })

  if (res.status === 401) {
    unauthorizedListeners.forEach((fn) => fn())
  }

  if (!res.ok) {
    const code = await res
      .json()
      .then((b) => (b as { error?: string }).error ?? 'error')
      .catch(() => 'error')
    throw new ApiError(res.status, code, `${res.status} ${code}`)
  }

  // 204 (revoke) and 202 (evict, best-effort async) return no body.
  if (res.status === 204 || res.status === 202) return undefined as T
  return (await res.json()) as T
}
