import { vi, type MockInstance } from 'vitest'

const CONSOLE_METHODS = ['log', 'info', 'warn', 'error', 'debug', 'trace'] as const

export function spyOnConsole(): MockInstance[] {
  return CONSOLE_METHODS.map((m) => vi.spyOn(console, m).mockImplementation(() => {}))
}

// JSON.stringify on an Error yields '{}' (JSON.stringify([new Error('s')]) is
// '[{}]'), so a JSON.stringify(args) matcher is BLIND to console.error(err) where
// err.message or err.stack carries the secret - exactly the property these checks
// exist to protect. Every argument is stringified through its own representation
// instead, including a nested `cause`.
//
// Same walker as the inline copy at web/src/jobs/logSecrecy.test.tsx:27-35, which
// is intentionally left untouched: rewiring a shipped secrecy test buys nothing.
export function stringifyArg(a: unknown): string {
  if (a instanceof Error) {
    return [
      a.name,
      a.message,
      a.stack ?? '',
      a.cause === undefined ? '' : stringifyArg(a.cause),
    ].join(' ')
  }
  if (typeof a === 'string') return a
  try {
    return JSON.stringify(a) ?? String(a)
  } catch {
    return String(a)
  }
}

export function findConsoleLeak(spies: MockInstance[], secret: string): string | null {
  for (const spy of spies) {
    for (const call of spy.mock.calls) {
      for (const arg of call) {
        const s = stringifyArg(arg)
        if (s.includes(secret)) return s
      }
    }
  }
  return null
}

export function assertNoConsoleLeak(spies: MockInstance[], secret: string): void {
  const leak = findConsoleLeak(spies, secret)
  if (leak !== null) throw new Error(`secret leaked to console: ${leak}`)
}

// A credential rendered into an <input> lives in the element's VALUE PROPERTY. It
// is not text, and it is not in innerHTML - so document.body.textContent and
// queryByText can never see it, and an absence assertion built on either passes
// vacuously. Check both representations.
export function domContainsSecret(secret: string): boolean {
  if (document.body.innerHTML.includes(secret)) return true
  for (const el of Array.from(document.querySelectorAll('input, textarea'))) {
    if ((el as HTMLInputElement | HTMLTextAreaElement).value.includes(secret)) return true
  }
  return false
}

export function storageContainsSecret(secret: string): boolean {
  for (const store of [localStorage, sessionStorage]) {
    for (let i = 0; i < store.length; i++) {
      const k = store.key(i)
      if (k === null) continue
      if (k.includes(secret) || (store.getItem(k) ?? '').includes(secret)) return true
    }
  }
  return false
}
