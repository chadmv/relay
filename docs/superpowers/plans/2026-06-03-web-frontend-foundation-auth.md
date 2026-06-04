# Web Front End: Foundation + Auth Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a React + Vite + TypeScript + Tailwind front end for Relay, embedded into `relay-server`, and prove it end-to-end with a working sign-in / register / shell slice against the real REST API.

**Architecture:** A self-contained `web/` Vite app builds to `web/dist`, which is embedded into `relay-server` via `go:embed` and served (with SPA fallback) on the same origin as the `/v1` API. In dev, the Vite server proxies `/v1` to `:8080`. Auth uses a `localStorage` bearer token attached by a thin typed `fetch` client; a React context exposes login/register/logout. The only backend change is a new public `GET /v1/config` endpoint that tells the register screen whether self-registration is enabled.

**Tech Stack:** React 18, Vite 5, TypeScript, Tailwind v4 (`@tailwindcss/vite`, CSS-first `@theme`), React Router v7, Vitest + React Testing Library + MSW, `@fontsource-variable` fonts. Backend: Go 1.26, `net/http` `ServeMux`, `embed`.

**Design reference:** `design_handoff_relay_holo/` (Holo direction). The prototype JSX is a visual reference, not code to copy. Consult `design_handoff_relay_holo/README.md` for exact tokens and `design_handoff_relay_holo/reference/screens/auth.js` for auth copy/error semantics.

**Spec:** `docs/superpowers/specs/2026-06-03-web-frontend-foundation-auth-design.md`

---

## File Structure

**Backend (Go):**
- `internal/api/config.go` (create) — `handleConfig` returning `{allow_self_register}`.
- `internal/api/config_test.go` (create) — handler test.
- `internal/api/server.go` (modify) — add `StaticHandler http.Handler` field; mount on `/`; register `GET /v1/config`.
- `internal/api/static_test.go` (create) — SPA-fallback wiring test.
- `web/embed.go` (create) — `package webui`; embeds `dist/`; exposes `Handler()`.
- `web/embed_test.go` (create) — SPA fallback + `/v1` guard test.
- `cmd/relay-server/main.go` (modify) — set `srv.StaticHandler = webui.Handler()`.

**Front end (`web/`):**
- Project config: `package.json`, `vite.config.ts`, `tsconfig.json`, `tsconfig.node.json`, `index.html`, `.gitignore`.
- `src/main.tsx` — root, providers, router mount.
- `src/test/setup.ts`, `src/test/msw.ts` — Vitest + MSW harness.
- `src/theme/tokens.css` — Tailwind import + `@theme` tokens + base styles + font imports.
- `src/theme/glass.ts` — glass-panel class strings.
- `src/lib/token.ts` — localStorage token store.
- `src/lib/types.ts` — auth contract types.
- `src/lib/api.ts` — typed fetch client + `ApiError`.
- `src/auth/AuthProvider.tsx` — auth context.
- `src/components/Field.tsx`, `Input.tsx`, `Button.tsx` — form primitives.
- `src/auth/LoginScreen.tsx`, `src/auth/RegisterScreen.tsx` — auth screens.
- `src/app/router.tsx`, `src/app/ProtectedRoute.tsx`, `src/app/JobsPlaceholder.tsx` — routing.
- `src/shell/HoloShell.tsx`, `src/shell/UserMenu.tsx` — app chrome.
- `web/dist/index.html` (committed placeholder) — keeps `go:embed` compiling.

**Build:**
- `Makefile` (modify) — `web-install` / `web-build` / `web-dev`; `build` depends on `web-build`.

---

## Task 1: Backend — `GET /v1/config` endpoint

**Files:**
- Create: `internal/api/config.go`
- Modify: `internal/api/server.go` (add route near `GET /v1/health`, line ~74)
- Test: `internal/api/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/config_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleConfig_ReportsSelfRegister(t *testing.T) {
	cases := []struct {
		name  string
		allow bool
	}{
		{"enabled", true},
		{"disabled", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{AllowSelfRegister: tc.allow}
			h := s.Handler()

			req := httptest.NewRequest("GET", "/v1/config", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body struct {
				AllowSelfRegister bool `json:"allow_self_register"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.AllowSelfRegister != tc.allow {
				t.Fatalf("allow_self_register = %v, want %v", body.AllowSelfRegister, tc.allow)
			}
		})
	}
}

func TestHandleConfig_IsPublic(t *testing.T) {
	s := &Server{}
	h := s.Handler()
	req := httptest.NewRequest("GET", "/v1/config", nil) // no Authorization header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("config must be public, got 401")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestHandleConfig -v`
Expected: FAIL — route not registered (404, decode error / unexpected status).

- [ ] **Step 3: Create the handler**

Create `internal/api/config.go`:

```go
package api

import "net/http"

// handleConfig exposes server configuration the web UI needs before
// authentication. Public — must not require a bearer token.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"allow_self_register": s.AllowSelfRegister,
	})
}
```

- [ ] **Step 4: Register the route**

In `internal/api/server.go`, directly below the health route (`mux.HandleFunc("GET /v1/health", s.handleHealth)`), add:

```go
	mux.HandleFunc("GET /v1/config", s.handleConfig)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestHandleConfig -v`
Expected: PASS (both subtests + public test).

- [ ] **Step 6: Commit**

```bash
git add internal/api/config.go internal/api/config_test.go internal/api/server.go
git commit -m "feat(api): add public GET /v1/config for self-register discovery"
```

---

## Task 2: Front-end scaffold (Vite + React + TS + Tailwind + Vitest)

Creates a minimal but building app with the test harness wired. No TDD here — this is project setup; the verification is "it builds and a trivial test runs."

**Files:**
- Create: `web/package.json`, `web/index.html`, `web/vite.config.ts`, `web/tsconfig.json`, `web/tsconfig.node.json`, `web/.gitignore`
- Create: `web/src/main.tsx`, `web/src/App.tsx`, `web/src/App.test.tsx`
- Create: `web/src/test/setup.ts`, `web/src/test/msw.ts`
- Create: `web/src/theme/tokens.css`
- Create: `web/dist/index.html` (placeholder)
- Modify: root `.gitignore`

- [ ] **Step 1: Create `web/package.json`**

```json
{
  "name": "relay-web",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview",
    "test": "vitest run",
    "test:watch": "vitest"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "^7.1.1",
    "@fontsource-variable/space-grotesk": "^5.1.0",
    "@fontsource-variable/jetbrains-mono": "^5.1.0"
  },
  "devDependencies": {
    "@tailwindcss/vite": "^4.0.0",
    "tailwindcss": "^4.0.0",
    "@vitejs/plugin-react": "^4.3.4",
    "vite": "^5.4.11",
    "typescript": "^5.7.2",
    "vitest": "^2.1.8",
    "jsdom": "^25.0.1",
    "@testing-library/react": "^16.1.0",
    "@testing-library/jest-dom": "^6.6.3",
    "@testing-library/user-event": "^14.5.2",
    "msw": "^2.7.0",
    "@types/react": "^18.3.18",
    "@types/react-dom": "^18.3.5"
  }
}
```

- [ ] **Step 2: Create config files**

`web/vite.config.ts`:

```ts
/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/v1': 'http://localhost:8080',
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    css: true,
  },
})
```

`web/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "useDefineForClassFields": true,
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src"],
  "references": [{ "path": "./tsconfig.node.json" }]
}
```

`web/tsconfig.node.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2023"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "isolatedModules": true,
    "noEmit": true,
    "strict": true
  },
  "include": ["vite.config.ts"]
}
```

`web/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Relay</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`web/.gitignore`:

```
node_modules
dist
!dist/index.html
*.log
```

- [ ] **Step 3: Create theme tokens (Tailwind v4 `@theme`)**

`web/src/theme/tokens.css`:

```css
@import "tailwindcss";
@import "@fontsource-variable/space-grotesk";
@import "@fontsource-variable/jetbrains-mono";

@theme {
  --color-bg: #050410;
  --color-fg: #ede9fe;
  --color-fg-mute: rgba(237, 233, 254, 0.55);
  --color-fg-dim: rgba(237, 233, 254, 0.35);
  --color-border: rgba(255, 255, 255, 0.08);
  --color-ok: #34d399;
  --color-warn: #fbbf24;
  --color-err: #fb7185;
  --color-accent: #3dd0f7;
  --color-accent-b: #6fe0fa;

  --font-sans: "Space Grotesk Variable", system-ui, sans-serif;
  --font-mono: "JetBrains Mono Variable", ui-monospace, monospace;

  --radius-card: 14px;
  --radius-input: 8px;
}

body {
  margin: 0;
  background: var(--color-bg);
  color: var(--color-fg);
  font-family: var(--font-sans);
  -webkit-font-smoothing: antialiased;
}
```

- [ ] **Step 4: Create the trivial app + MSW harness**

`web/src/test/msw.ts`:

```ts
import { setupServer } from 'msw/node'

// Shared MSW server. Per-test handlers are added with server.use(...).
export const server = setupServer()
```

`web/src/test/setup.ts`:

```ts
import '@testing-library/jest-dom/vitest'
import { afterAll, afterEach, beforeAll } from 'vitest'
import { server } from './msw'

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())
```

`web/src/App.tsx`:

```tsx
export function App() {
  return <div>Relay</div>
}
```

`web/src/App.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { App } from './App'

test('renders the app name', () => {
  render(<App />)
  expect(screen.getByText('Relay')).toBeInTheDocument()
})
```

`web/src/main.tsx`:

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './theme/tokens.css'
import { App } from './App'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
```

- [ ] **Step 5: Create the committed `dist/` placeholder**

`web/dist/index.html`:

```html
<!doctype html>
<html lang="en">
  <head><meta charset="UTF-8" /><title>Relay</title></head>
  <body>
    <p>The Relay web UI has not been built. Run <code>make web-build</code>.</p>
  </body>
</html>
```

- [ ] **Step 6: Update the root `.gitignore`**

Append to the repository-root `.gitignore`:

```
web/node_modules/
web/dist/
!web/dist/index.html
```

- [ ] **Step 7: Install, build, and test**

Run (from `web/`):

```bash
cd web && npm install
npm run build
npm test
```

Expected: `npm run build` writes `web/dist/assets/*` and an `index.html`; `npm test` shows 1 passing test (`renders the app name`).

> Note: `npm run build` overwrites the placeholder `dist/index.html`. That is fine locally. Do **not** `git add web/dist/assets`; only the placeholder `index.html` is tracked (enforced by `.gitignore`). After building, restore the placeholder before committing: `git checkout web/dist/index.html` if it was staged, or simply never stage built assets.

- [ ] **Step 8: Commit (scaffold + placeholder only, no built assets)**

```bash
git add web/package.json web/package-lock.json web/index.html web/vite.config.ts \
  web/tsconfig.json web/tsconfig.node.json web/.gitignore \
  web/src/main.tsx web/src/App.tsx web/src/App.test.tsx \
  web/src/test/setup.ts web/src/test/msw.ts web/src/theme/tokens.css \
  web/dist/index.html .gitignore
git commit -m "feat(web): scaffold Vite + React + TS + Tailwind app with Vitest/MSW"
```

---

## Task 3: Token store (`lib/token.ts`)

**Files:**
- Create: `web/src/lib/token.ts`
- Test: `web/src/lib/token.test.ts`

- [ ] **Step 1: Write the failing test**

`web/src/lib/token.test.ts`:

```ts
import { afterEach, expect, test } from 'vitest'
import { clearToken, getToken, setToken } from './token'

afterEach(() => clearToken())

test('round-trips a token', () => {
  expect(getToken()).toBeNull()
  setToken('abc123')
  expect(getToken()).toBe('abc123')
})

test('clears a token', () => {
  setToken('abc123')
  clearToken()
  expect(getToken()).toBeNull()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/lib/token.test.ts`
Expected: FAIL — module `./token` not found.

- [ ] **Step 3: Implement**

`web/src/lib/token.ts`:

```ts
const KEY = 'relay.token'

export function getToken(): string | null {
  return localStorage.getItem(KEY)
}

export function setToken(token: string): void {
  localStorage.setItem(KEY, token)
}

export function clearToken(): void {
  localStorage.removeItem(KEY)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/lib/token.test.ts`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/token.ts web/src/lib/token.test.ts
git commit -m "feat(web): add localStorage token store"
```

---

## Task 4: Types + API client (`lib/types.ts`, `lib/api.ts`)

**Files:**
- Create: `web/src/lib/types.ts`
- Create: `web/src/lib/api.ts`
- Test: `web/src/lib/api.test.ts`

- [ ] **Step 1: Define the contract types**

`web/src/lib/types.ts`:

```ts
export interface User {
  id: string
  email: string
  name: string
  role: string
}

export interface LoginResponse {
  token: string
  expires: string
  user: User
}

export interface ConfigResponse {
  allow_self_register: boolean
}
```

- [ ] **Step 2: Write the failing test**

`web/src/lib/api.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError, apiFetch, onUnauthorized } from './api'
import { clearToken, setToken } from './token'

afterEach(() => clearToken())

test('attaches the bearer token when present', async () => {
  let seen: string | null = null
  server.use(
    http.get('/v1/users/me', ({ request }) => {
      seen = request.headers.get('authorization')
      return HttpResponse.json({ id: '1', email: 'a@b.co', name: 'A', role: 'user' })
    }),
  )
  setToken('tok_123')
  await apiFetch('/users/me')
  expect(seen).toBe('Bearer tok_123')
})

test('parses the error envelope into ApiError', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  const err = await apiFetch('/auth/login', { method: 'POST', json: {} }).catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect((err as ApiError).status).toBe(401)
  expect((err as ApiError).code).toBe('invalid_credentials')
})

test('invokes the unauthorized handler on 401', async () => {
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  const spy = vi.fn()
  const off = onUnauthorized(spy)
  await apiFetch('/users/me').catch(() => {})
  expect(spy).toHaveBeenCalledOnce()
  off()
})

test('surfaces 429 as ApiError with status 429', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'rate_limited' }, { status: 429 }),
    ),
  )
  const err = await apiFetch('/auth/login', { method: 'POST', json: {} }).catch((e) => e)
  expect((err as ApiError).status).toBe(429)
})
```

> The test imports `server` from `../test/setup-helpers`. Create a one-line re-export so tests have a stable import path independent of the MSW node entry.

`web/src/test/setup-helpers.ts`:

```ts
export { server } from './msw'
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd web && npx vitest run src/lib/api.test.ts`
Expected: FAIL — `./api` exports not found.

- [ ] **Step 4: Implement the client**

`web/src/lib/api.ts`:

```ts
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

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd web && npx vitest run src/lib/api.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/types.ts web/src/lib/api.ts web/src/lib/api.test.ts web/src/test/setup-helpers.ts
git commit -m "feat(web): add typed fetch client with ApiError and 401 interceptor"
```

---

## Task 5: Auth context (`auth/AuthProvider.tsx`)

**Files:**
- Create: `web/src/auth/AuthProvider.tsx`
- Test: `web/src/auth/AuthProvider.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/auth/AuthProvider.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken, getToken, setToken } from '../lib/token'
import { AuthProvider, useAuth } from './AuthProvider'

afterEach(() => clearToken())

const ME = { id: '1', email: 'ada@studio.dev', name: 'Ada', role: 'user' }

function Probe() {
  const { status, user, login, logout } = useAuth()
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="user">{user?.email ?? 'none'}</span>
      <button onClick={() => login('ada@studio.dev', 'pw')}>login</button>
      <button onClick={() => logout()}>logout</button>
    </div>
  )
}

function renderProbe() {
  return render(
    <AuthProvider>
      <Probe />
    </AuthProvider>,
  )
}

test('starts unauthenticated with no token', async () => {
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  expect(screen.getByTestId('user')).toHaveTextContent('none')
})

test('hydrates the user from an existing token', async () => {
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_existing')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('user')).toHaveTextContent('ada@studio.dev'))
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
})

test('login stores the token and sets the user', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ token: 'tok_new', expires: '', user: ME }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('user')).toHaveTextContent('ada@studio.dev'))
  expect(getToken()).toBe('tok_new')
})

test('logout clears token and user', async () => {
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.delete('/v1/auth/token', () => new HttpResponse(null, { status: 204 })),
  )
  setToken('tok_existing')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('user')).toHaveTextContent('ada@studio.dev'))
  await userEvent.click(screen.getByText('logout'))
  await waitFor(() => expect(getToken()).toBeNull())
  expect(screen.getByTestId('user')).toHaveTextContent('none')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/auth/AuthProvider.test.tsx`
Expected: FAIL — `./AuthProvider` not found.

- [ ] **Step 3: Implement**

`web/src/auth/AuthProvider.tsx`:

```tsx
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { apiFetch } from '../lib/api'
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
    setUser(res.user)
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/auth/AuthProvider.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/AuthProvider.tsx web/src/auth/AuthProvider.test.tsx
git commit -m "feat(web): add AuthProvider context (hydrate, login, register, logout)"
```

---

## Task 6: Form primitives (`components/`)

Small shared primitives used by both auth screens. Styling approximates the Holo design; refine against `design_handoff_relay_holo` during review.

**Files:**
- Create: `web/src/components/Button.tsx`, `web/src/components/Input.tsx`, `web/src/components/Field.tsx`
- Test: `web/src/components/Field.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/components/Field.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { Field } from './Field'
import { Input } from './Input'

test('associates the label with its control and shows error text', () => {
  render(
    <Field label="Email" htmlFor="email" error="required">
      <Input id="email" />
    </Field>,
  )
  expect(screen.getByLabelText('Email')).toBeInTheDocument()
  expect(screen.getByText('required')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/Field.test.tsx`
Expected: FAIL — components not found.

- [ ] **Step 3: Implement the primitives**

`web/src/components/Input.tsx`:

```tsx
import type { InputHTMLAttributes } from 'react'

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className={
        'w-full rounded-[8px] border border-border bg-white/5 px-3 py-2 text-[13px] ' +
        'text-fg placeholder:text-fg-dim outline-none focus:border-accent ' +
        (props.className ?? '')
      }
    />
  )
}
```

`web/src/components/Field.tsx`:

```tsx
import type { ReactNode } from 'react'

interface FieldProps {
  label: string
  htmlFor: string
  error?: string
  hint?: ReactNode
  children: ReactNode
}

export function Field({ label, htmlFor, error, hint, children }: FieldProps) {
  return (
    <div className="mb-3">
      <label
        htmlFor={htmlFor}
        className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute"
      >
        {label}
      </label>
      {children}
      {hint && <div className="mt-1 text-[11px] text-fg-dim">{hint}</div>}
      {error && <div className="mt-1 text-[11px] text-err">{error}</div>}
    </div>
  )
}
```

`web/src/components/Button.tsx`:

```tsx
import type { ButtonHTMLAttributes } from 'react'

export function Button(props: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...props}
      className={
        'w-full rounded-[8px] bg-accent px-3 py-2 text-[13px] font-medium text-bg ' +
        'transition hover:bg-accent-b disabled:opacity-50 ' +
        (props.className ?? '')
      }
    />
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/Field.test.tsx`
Expected: PASS (1 test).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/
git commit -m "feat(web): add Field/Input/Button form primitives"
```

---

## Task 7: Login screen (`auth/LoginScreen.tsx`)

**Files:**
- Create: `web/src/auth/LoginScreen.tsx`
- Test: `web/src/auth/LoginScreen.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/auth/LoginScreen.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken } from '../lib/token'
import { AuthProvider } from './AuthProvider'
import { LoginScreen } from './LoginScreen'

afterEach(() => clearToken())

function renderLogin() {
  return render(
    <MemoryRouter>
      <AuthProvider>
        <LoginScreen />
      </AuthProvider>
    </MemoryRouter>,
  )
}

test('shows a generic message on 401', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  renderLogin()
  await userEvent.type(screen.getByLabelText('Email'), 'ada@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'wrongpw')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByText(/invalid email or password/i)).toBeInTheDocument()
})

test('shows a rate-limit hint on 429', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'rate_limited' }, { status: 429 }),
    ),
  )
  renderLogin()
  await userEvent.type(screen.getByLabelText('Email'), 'ada@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'pw')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByText(/too many attempts/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/auth/LoginScreen.test.tsx`
Expected: FAIL — `./LoginScreen` not found.

- [ ] **Step 3: Implement**

`web/src/auth/LoginScreen.tsx`:

```tsx
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/auth/LoginScreen.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/LoginScreen.tsx web/src/auth/LoginScreen.test.tsx
git commit -m "feat(web): add login screen with 401/429 handling"
```

---

## Task 8: Register screen (`auth/RegisterScreen.tsx`)

Two modes driven by `GET /v1/config`. Invite field shows only when self-register is off.

**Files:**
- Create: `web/src/auth/RegisterScreen.tsx`
- Test: `web/src/auth/RegisterScreen.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/auth/RegisterScreen.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken } from '../lib/token'
import { AuthProvider } from './AuthProvider'
import { RegisterScreen } from './RegisterScreen'

afterEach(() => clearToken())

function renderRegister() {
  return render(
    <MemoryRouter>
      <AuthProvider>
        <RegisterScreen />
      </AuthProvider>
    </MemoryRouter>,
  )
}

test('hides the invite field when self-register is enabled', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: true })))
  renderRegister()
  await waitFor(() => expect(screen.getByLabelText('Email')).toBeInTheDocument())
  expect(screen.queryByLabelText(/invite token/i)).not.toBeInTheDocument()
})

test('shows the invite field when self-register is disabled', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })))
  renderRegister()
  expect(await screen.findByLabelText(/invite token/i)).toBeInTheDocument()
})

test('shows an inline invite error on 400', async () => {
  server.use(
    http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })),
    http.post('/v1/auth/register', () =>
      HttpResponse.json({ error: 'invite_expired' }, { status: 400 }),
    ),
  )
  renderRegister()
  await userEvent.type(await screen.findByLabelText('Display name'), 'Ada')
  await userEvent.type(screen.getByLabelText('Email'), 'ada@studio.dev')
  await userEvent.type(screen.getByLabelText(/invite token/i), 'rl_invt_x')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: /create account/i }))
  expect(await screen.findByText(/invite_expired/i)).toBeInTheDocument()
})

test('shows email-exists error with sign-in link on 409', async () => {
  server.use(
    http.get('/v1/config', () => HttpResponse.json({ allow_self_register: true })),
    http.post('/v1/auth/register', () =>
      HttpResponse.json({ error: 'email_taken' }, { status: 409 }),
    ),
  )
  renderRegister()
  await userEvent.type(await screen.findByLabelText('Display name'), 'Ada')
  await userEvent.type(screen.getByLabelText('Email'), 'ada@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: /create account/i }))
  expect(await screen.findByText(/already registered/i)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /sign in/i })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/auth/RegisterScreen.test.tsx`
Expected: FAIL — `./RegisterScreen` not found.

- [ ] **Step 3: Implement**

`web/src/auth/RegisterScreen.tsx`:

```tsx
import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { ApiError, apiFetch } from '../lib/api'
import { Button } from '../components/Button'
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
      <form
        onSubmit={onSubmit}
        className="w-[360px] rounded-card border border-border bg-white/5 p-6 backdrop-blur"
      >
        <div className="mb-1 text-[18px] font-medium">Create your relay account</div>
        <div className="mb-5 text-[12px] text-fg-mute">
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
          <div className="mb-3 text-[12px] text-err">
            That email is already registered.{' '}
            <Link to="/auth" className="text-accent">
              Sign in
            </Link>
          </div>
        )}

        <Button type="submit" disabled={busy}>
          Create account →
        </Button>

        <div className="mt-4 text-center text-[11px] text-fg-mute">
          Already have an account?{' '}
          <Link to="/auth" className="text-accent">
            Sign in
          </Link>
        </div>
      </form>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/auth/RegisterScreen.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/RegisterScreen.tsx web/src/auth/RegisterScreen.test.tsx
git commit -m "feat(web): add register screen with invite/self-serve modes"
```

---

## Task 9: Shell + user menu (`shell/`)

**Files:**
- Create: `web/src/shell/UserMenu.tsx`
- Create: `web/src/shell/HoloShell.tsx`
- Test: `web/src/shell/UserMenu.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/shell/UserMenu.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { UserMenu } from './UserMenu'

function renderMenu(onLogout = vi.fn()) {
  render(<UserMenu email="ada@studio.dev" onLogout={onLogout} />)
  return onLogout
}

test('opens and closes on outside click', async () => {
  renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  expect(screen.getByText('Log out')).toBeInTheDocument()
  await userEvent.click(document.body)
  expect(screen.queryByText('Log out')).not.toBeInTheDocument()
})

test('closes on Escape', async () => {
  renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByText('Log out')).not.toBeInTheDocument()
})

test('calls onLogout when Log out is clicked', async () => {
  const onLogout = renderMenu()
  await userEvent.click(screen.getByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.click(screen.getByText('Log out'))
  expect(onLogout).toHaveBeenCalledOnce()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/shell/UserMenu.test.tsx`
Expected: FAIL — `./UserMenu` not found.

- [ ] **Step 3: Implement `UserMenu`**

`web/src/shell/UserMenu.tsx`:

```tsx
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'

interface UserMenuProps {
  email: string
  onLogout: () => void
}

export function UserMenu({ email, onLogout }: UserMenuProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        className="rounded-full border border-border bg-white/5 px-3 py-1 text-[12px] text-fg"
      >
        {email}
      </button>
      {open && (
        <div
          className="absolute right-0 mt-2 w-44 rounded-card border border-border p-1 text-[12px] shadow-xl"
          style={{ background: 'rgba(14,12,30,0.96)' }}
        >
          <Link to="/profile" className="block rounded px-3 py-2 hover:bg-white/5">
            Profile
          </Link>
          <Link to="/profile/password" className="block rounded px-3 py-2 hover:bg-white/5">
            Password
          </Link>
          <Link to="/profile/sessions" className="block rounded px-3 py-2 hover:bg-white/5">
            Sessions
          </Link>
          <button
            onClick={onLogout}
            className="block w-full rounded px-3 py-2 text-left text-err hover:bg-white/5"
          >
            Log out
          </button>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Implement `HoloShell`**

`web/src/shell/HoloShell.tsx`:

```tsx
import type { ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { UserMenu } from './UserMenu'

const NAV = [
  { to: '/jobs', label: 'Jobs' },
  { to: '/workers', label: 'Workers' },
  { to: '/schedules', label: 'Schedules' },
  { to: '/admin', label: 'Admin' },
]

export function HoloShell({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth()
  const navigate = useNavigate()

  async function onLogout() {
    await logout()
    navigate('/auth')
  }

  return (
    <div className="min-h-screen bg-bg text-fg">
      <header className="flex items-center justify-between border-b border-border px-5 py-3">
        <div className="flex items-center gap-6">
          <span className="font-sans text-[18px] font-bold">
            relay<span className="text-accent">.</span>
          </span>
          <nav className="flex gap-4 text-[12px]">
            {NAV.map((n) => (
              <NavLink
                key={n.to}
                to={n.to}
                className={({ isActive }) =>
                  isActive ? 'text-accent' : 'text-fg-mute hover:text-fg'
                }
              >
                {n.label}
              </NavLink>
            ))}
          </nav>
        </div>
        <UserMenu email={user?.email ?? ''} onLogout={onLogout} />
      </header>
      <main className="p-5">{children}</main>
    </div>
  )
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd web && npx vitest run src/shell/UserMenu.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/shell/
git commit -m "feat(web): add HoloShell topbar and UserMenu dropdown"
```

---

## Task 10: Router + protected routes (`app/`)

**Files:**
- Create: `web/src/app/JobsPlaceholder.tsx`
- Create: `web/src/app/ProtectedRoute.tsx`
- Create: `web/src/app/router.tsx`
- Modify: `web/src/App.tsx` (replace trivial body with the router)
- Modify: `web/src/App.test.tsx` (update for the new root)
- Test: `web/src/app/ProtectedRoute.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/app/ProtectedRoute.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { ProtectedRoute } from './ProtectedRoute'

afterEach(() => clearToken())

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <Routes>
          <Route path="/auth" element={<div>login page</div>} />
          <Route element={<ProtectedRoute />}>
            <Route path="/jobs" element={<div>jobs page</div>} />
          </Route>
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
}

test('redirects anonymous users to /auth', async () => {
  renderAt('/jobs')
  await waitFor(() => expect(screen.getByText('login page')).toBeInTheDocument())
})

test('renders the protected route when authenticated', async () => {
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: '1', email: 'a@b.co', name: 'A', role: 'user' }),
    ),
  )
  setToken('tok')
  renderAt('/jobs')
  await waitFor(() => expect(screen.getByText('jobs page')).toBeInTheDocument())
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/app/ProtectedRoute.test.tsx`
Expected: FAIL — `./ProtectedRoute` not found.

- [ ] **Step 3: Implement `ProtectedRoute`**

`web/src/app/ProtectedRoute.tsx`:

```tsx
import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { HoloShell } from '../shell/HoloShell'

export function ProtectedRoute() {
  const { status } = useAuth()
  if (status === 'loading') return <div className="min-h-screen bg-bg" />
  if (status === 'anonymous') return <Navigate to="/auth" replace />
  return (
    <HoloShell>
      <Outlet />
    </HoloShell>
  )
}
```

- [ ] **Step 4: Implement the placeholder + router**

`web/src/app/JobsPlaceholder.tsx`:

```tsx
export function JobsPlaceholder() {
  return (
    <div className="text-[14px] text-fg-mute">
      Jobs — coming soon.
    </div>
  )
}
```

`web/src/app/router.tsx`:

```tsx
import { Navigate, Route, Routes } from 'react-router-dom'
import { LoginScreen } from '../auth/LoginScreen'
import { RegisterScreen } from '../auth/RegisterScreen'
import { JobsPlaceholder } from './JobsPlaceholder'
import { ProtectedRoute } from './ProtectedRoute'

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/auth" element={<LoginScreen />} />
      <Route path="/register" element={<RegisterScreen />} />
      <Route element={<ProtectedRoute />}>
        <Route path="/jobs" element={<JobsPlaceholder />} />
        <Route path="/workers" element={<JobsPlaceholder />} />
        <Route path="/schedules" element={<JobsPlaceholder />} />
        <Route path="/admin" element={<JobsPlaceholder />} />
        <Route path="/profile/*" element={<JobsPlaceholder />} />
      </Route>
      <Route path="*" element={<Navigate to="/jobs" replace />} />
    </Routes>
  )
}
```

- [ ] **Step 5: Wire the root + 401 redirect**

Replace `web/src/App.tsx` with:

```tsx
import { useEffect } from 'react'
import { BrowserRouter, useNavigate } from 'react-router-dom'
import { AuthProvider } from './auth/AuthProvider'
import { onUnauthorized } from './lib/api'
import { AppRoutes } from './app/router'

function UnauthorizedRedirect() {
  const navigate = useNavigate()
  useEffect(() => onUnauthorized(() => navigate('/auth')), [navigate])
  return null
}

export function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <UnauthorizedRedirect />
        <AppRoutes />
      </AuthProvider>
    </BrowserRouter>
  )
}
```

Replace `web/src/App.test.tsx` with:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'
import { clearToken } from './lib/token'
import { App } from './App'

afterEach(() => clearToken())

test('anonymous user landing on / is sent to the sign-in screen', async () => {
  render(<App />)
  await waitFor(() =>
    expect(screen.getByText(/sign in to the coordinator/i)).toBeInTheDocument(),
  )
})
```

> Note: `App` now owns `BrowserRouter`, so it must not be wrapped in another router in tests.

- [ ] **Step 6: Run the full front-end suite**

Run: `cd web && npm test`
Expected: PASS — all suites green (token, api, AuthProvider, Field, Login, Register, UserMenu, ProtectedRoute, App).

- [ ] **Step 7: Commit**

```bash
git add web/src/app/ web/src/App.tsx web/src/App.test.tsx
git commit -m "feat(web): wire router, protected routes, and 401 redirect"
```

---

## Task 11: Backend — embed `dist/` and serve the SPA

**Files:**
- Create: `web/embed.go` (Go package `webui`, lives alongside the app)
- Create: `web/embed_test.go`
- Modify: `internal/api/server.go` (add `StaticHandler` field + mount on `/`)
- Create: `internal/api/static_test.go`
- Modify: `cmd/relay-server/main.go` (set `srv.StaticHandler`)

- [ ] **Step 1: Write the failing test for the webui handler**

`web/embed_test.go`:

```go
package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_ServesIndexForUnknownRoute(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/jobs/deep/link", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
}

func TestHandler_DoesNotHijackAPIPaths(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/v1/unknown", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for /v1 path", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run TestHandler -v`
Expected: FAIL — package `webui` / `Handler` does not exist.

- [ ] **Step 3: Implement the embed package**

`web/embed.go`:

```go
// Package webui embeds the built Relay web front end (web/dist) and serves it
// with SPA fallback. The dist directory is produced by `make web-build`; a
// committed placeholder index.html keeps this package compiling without a build.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the embedded SPA. Unknown, non-API paths fall back to
// index.html so client-side routes deep-link correctly. Requests under /v1 are
// never served here (they belong to the API mux); this handler 404s them so a
// missing API route does not return index.html.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}
		// Serve the file if it exists; otherwise fall back to index.html.
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web/ -run TestHandler -v`
Expected: PASS (serves placeholder index.html for `/jobs/deep/link`; 404 for `/v1/unknown`).

- [ ] **Step 5: Write the failing wiring test in the API package**

`internal/api/static_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_StaticHandlerServesNonAPIPaths(t *testing.T) {
	static := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("INDEX"))
	})
	s := &Server{StaticHandler: static}
	h := s.Handler()

	req := httptest.NewRequest("GET", "/jobs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "INDEX" {
		t.Fatalf("static path: code=%d body=%q, want 200 INDEX", rec.Code, rec.Body.String())
	}
}

func TestServer_APIRoutesWinOverStatic(t *testing.T) {
	static := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("INDEX"))
	})
	s := &Server{StaticHandler: static}
	h := s.Handler()

	req := httptest.NewRequest("GET", "/v1/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Body.String() == "INDEX" {
		t.Fatalf("/v1/health should hit the API handler, not the static handler")
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/api/ -run 'TestServer_StaticHandler|TestServer_APIRoutesWin' -v`
Expected: FAIL — `Server` has no `StaticHandler` field.

- [ ] **Step 7: Add the field and mount it**

In `internal/api/server.go`, add to the `Server` struct (after `Metrics`):

```go
	// StaticHandler, when non-nil, serves the embedded web UI for any path not
	// matched by a /v1 API route. Set by cmd/relay-server from package webui.
	StaticHandler http.Handler
```

In `Handler()`, immediately before the final `return CORS(...)` line, add:

```go
	if s.StaticHandler != nil {
		mux.Handle("/", s.StaticHandler)
	}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./internal/api/ -run 'TestServer_StaticHandler|TestServer_APIRoutesWin' -v`
Expected: PASS.

- [ ] **Step 9: Wire it in `main.go`**

In `cmd/relay-server/main.go`, add the import (the package directory is `relay/web` but its package name is `webui`, so alias it):

```go
	webui "relay/web"
```

The API server is constructed as `httpServer := api.New(...)` (around line 154) with `httpServer.Metrics = metricsStore` on the next line, and its handler is built later via `httpServer.Handler()` (around line 195). Set the static handler on `httpServer` directly after the `Metrics` assignment (it must run before `httpServer.Handler()` is called):

```go
	httpServer.StaticHandler = webui.Handler()
```

- [ ] **Step 10: Verify the whole backend builds and tests pass**

Run:

```bash
go build ./...
go test ./internal/api/ ./web/ -v
```

Expected: build succeeds; all API + webui tests pass.

- [ ] **Step 11: Commit**

```bash
git add web/embed.go web/embed_test.go internal/api/server.go internal/api/static_test.go cmd/relay-server/main.go
git commit -m "feat(server): embed and serve the web UI with SPA fallback"
```

---

## Task 12: Makefile integration

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add web targets and a build dependency**

In `Makefile`, update the `.PHONY` line to include the new targets and change `build` to depend on `web-build`. Replace the top of the file (lines 1-7) with:

```make
.PHONY: build test test-integration generate clean python-test python-test-integration python-lint web-install web-build web-dev

# Install web dependencies
web-install:
	cd web && npm ci

# Build the web UI into web/dist (embedded by relay-server)
web-build:
	cd web && npm run build

# Run the Vite dev server (proxies /v1 to :8080)
web-dev:
	cd web && npm run dev

# Build all binaries into bin/ (web UI is embedded into relay-server)
build: web-build
	go build -o bin/relay-server ./cmd/relay-server
	go build -o bin/relay-agent  ./cmd/relay-agent
	go build -o bin/relay        ./cmd/relay
```

> Leave the rest of the Makefile unchanged. `make test` stays Go-only and works against the committed placeholder `dist/` (no Node required for backend contributors).

- [ ] **Step 2: Verify the full build**

Run:

```bash
make web-install
make build
```

Expected: `web/dist` is rebuilt, then all three Go binaries compile with the real UI embedded. Run `./bin/relay-server` (with a database configured) and confirm `GET /` returns the built `index.html` and `GET /v1/health` returns `{"status":"ok"}`.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "build: add web-install/web-build/web-dev targets; build embeds the UI"
```

---

## Final verification

- [ ] **Run the complete test suites**

```bash
cd web && npm test          # all front-end suites green
cd .. && make test          # all Go tests green (uses placeholder dist)
```

- [ ] **Manual smoke (optional, needs a DB + the bootstrap admin)**

1. `make build && ./bin/relay-server` (with Postgres + `RELAY_BOOTSTRAP_ADMIN` configured).
2. Visit `http://localhost:8080/` — the login screen loads.
3. Sign in with the bootstrap admin — lands on `/jobs` ("coming soon") inside the shell.
4. Open the user menu → Log out — returns to `/auth`.
5. For dev iteration instead: `make web-dev` and visit `http://localhost:5173`.

---

## Self-Review Notes

- **Spec coverage:** foundation/layout (Tasks 2, 11, 12), serving + SPA fallback (Task 11), dev proxy (Task 2 `vite.config.ts`), theme tokens + compact density + vendored fonts (Task 2 `tokens.css`), token store (Task 3), api client + ApiError + 401 interceptor + 429 (Task 4), hand-written types (Task 4), AuthProvider (Task 5), error-handling map (Tasks 7-8), routing + ProtectedRoute + placeholders + redirect (Task 10), shell + UserMenu logout + outside-click/Esc (Task 9), login + register two-mode (Tasks 7-8), `GET /v1/config` (Task 1), tests both sides (throughout), Makefile (Task 12). TanStack Query deferral and Profile/Sessions out-of-scope are respected (no tasks).
- **Tailwind v4 note:** the spec described a `tailwind.config.ts`; this plan uses Tailwind v4's CSS-first `@theme` (same token mapping, current standard). Functionally equivalent to the spec's intent.
- **Type consistency:** `apiFetch`, `ApiError`, `onUnauthorized`, `getToken/setToken/clearToken`, `useAuth().{status,user,login,register,logout}`, `Server.StaticHandler`, and `webui.Handler()` are used consistently across tasks.
