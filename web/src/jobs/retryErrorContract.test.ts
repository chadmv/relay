import { readFileSync } from 'node:fs'
// Explicit node:url URL, not the global: the test environment is jsdom, which
// shadows the global URL constructor with its own (whatwg-url) implementation.
// That implementation cannot resolve a relative path against a file:// base that
// carries a Windows drive letter - it silently falls back to jsdom's default
// document location instead of throwing, so the bug surfaces one line later as
// "The URL must be of scheme file" out of readFileSync. Node's own URL has no
// such bug (see responsive.guard.test.ts for the same fix).
import { fileURLToPath, URL as NodeURL } from 'node:url'
import { expect, test } from 'vitest'
import { RETRY_ERROR_PREFIXES } from './retryError'

// Reads the Go handler this module classifies. Vitest runs under Node, so
// node:fs is available even with the jsdom environment; import.meta.url resolves
// against this file, so the path holds no matter where vitest is invoked from.
const JOBS_GO = fileURLToPath(new NodeURL('../../../internal/api/jobs.go', import.meta.url))

// What this guard does NOT cover: it only walks the prefixes retryError.ts
// already knows about. A NEW 409 branch added to handleRetryJob with no
// corresponding entry in RETRY_ERROR_PREFIXES is invisible to this test - it adds
// nothing to iterate over, so nothing reddens. That gap is a structural limit of
// "read the source and check containment", not something this test can close;
// catching a wholly new branch needs a human (or a reviewer) to notice the
// classifier wasn't extended, not a test.
test('every prefix classifyRetryFailure matches still exists in handleRetryJob', () => {
  // readFileSync throws if the path is wrong - which is the intended failure. A
  // try/catch that skipped would turn this contract into decoration.
  const src = readFileSync(JOBS_GO, 'utf8')
  expect(src).toContain('func (s *Server) handleRetryJob(')
  for (const [name, prefix] of Object.entries(RETRY_ERROR_PREFIXES)) {
    // Each prefix must be a branch-unique slice, not just some substring of the
    // handler: `raced` is deliberately the longer "...while the retry was in
    // flight" rather than "the job changed", because the blocked sentence a few
    // lines away also contains "the job changed" (a different clause: "...while
    // the REQUEST was in flight"). A short, non-unique prefix would let this
    // assertion stay green off the WRONG sentence, so a reworded raced message
    // would silently stop being classified while this test kept passing.
    expect(src, `retryError.ts prefix "${name}" no longer appears in internal/api/jobs.go`).toContain(prefix)
  }
})
