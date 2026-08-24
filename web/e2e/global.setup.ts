import { expect, test as setup } from '@playwright/test'
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { seedAll } from './fixtures'

const runDir = join(dirname(fileURLToPath(import.meta.url)), '.run')
const runEnv = JSON.parse(readFileSync(join(runDir, 'env.json'), 'utf8')) as {
  adminEmail: string
  adminPassword: string
  runId: string
}

setup('log in and seed fixtures', async ({ request, baseURL }) => {
  const res = await request.post('/v1/auth/login', {
    data: { email: runEnv.adminEmail, password: runEnv.adminPassword },
  })
  // A 401 here almost always means the database was NOT freshly created:
  // bootstrapAdmin returns before it looks at the email when ANY admin already
  // exists (cmd/relay-server/bootstrap.go:20-23), so RELAY_BOOTSTRAP_PASSWORD is
  // never consumed and there is no matching credential. That is the single
  // failure this whole lane is most likely to hit, so it gets a named message.
  expect(res.status(), 'bootstrap admin login (401 => the database was not empty)').toBe(201)
  const { token } = (await res.json()) as { token: string }
  expect(token).toBeTruthy()

  const seed = await seedAll(request, token, runEnv.runId, runEnv.adminEmail)

  // storageState is written by hand rather than via a browser round trip: the
  // SPA's only credential is localStorage['relay.token'] (web/src/lib/token.ts:1)
  // and AuthProvider hydrates from /users/me on mount whenever that key is
  // present (web/src/auth/AuthProvider.tsx:124-139). No browser needed.
  writeFileSync(
    join(runDir, 'state.json'),
    JSON.stringify({
      cookies: [],
      origins: [
        {
          origin: new URL(baseURL!).origin,
          localStorage: [{ name: 'relay.token', value: token }],
        },
      ],
    }),
  )
  writeFileSync(join(runDir, 'seed.json'), JSON.stringify(seed, null, 2))
})
