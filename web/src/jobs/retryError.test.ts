import { expect, test } from 'vitest'
import { ApiError } from '../lib/api'
import { classifyRetryFailure } from './retryError'

function conflict(sentence: string) {
  return new ApiError(409, sentence, `409 ${sentence}`)
}

test('a nothing-matched 409 is a dead end, not a retryable failure', () => {
  const f = classifyRetryFailure(
    conflict('no tasks matched task=failed; this job has no failed or timed_out tasks'),
    'failed',
  )
  expect(f.kind).toBe('none-matched')
  expect(f.message).toContain('no failed or timed_out tasks')
  expect(f.hint).toBe('Nothing was changed.')
})

test('a blocked 409 in failed mode points at Retry all, which is what unblocks it', () => {
  const f = classifyRetryFailure(
    conflict(
      'no tasks were reopened: a selected task has dependents that have already run, ' +
        'or the job changed while the request was in flight; nothing was applied',
    ),
    'failed',
  )
  expect(f.kind).toBe('blocked')
  expect(f.hint).toContain('Retry all')
})

test('a blocked 409 in all mode does NOT suggest Retry all', () => {
  const f = classifyRetryFailure(
    conflict(
      'no tasks were reopened: a selected task has dependents that have already run, ' +
        'or the job changed while the request was in flight; nothing was applied',
    ),
    'all',
  )
  expect(f.kind).toBe('blocked')
  expect(f.hint).not.toContain('Retry all')
})

test('a raced 409 says the action can be repeated', () => {
  const f = classifyRetryFailure(
    conflict('the job changed while the retry was in flight; nothing was applied - try again'),
    'failed',
  )
  expect(f.kind).toBe('raced')
  expect(f.hint).toContain('Retry the action.')
})

test('the three 409 kinds never share a rendered string', () => {
  const kinds = [
    classifyRetryFailure(conflict('no tasks matched task=all; this job has no finished tasks'), 'all'),
    classifyRetryFailure(conflict('no tasks were reopened: a selected task has dependents that have already run, or the job changed while the request was in flight; nothing was applied'), 'all'),
    classifyRetryFailure(conflict('the job changed while the retry was in flight; nothing was applied - try again'), 'all'),
  ]
  const rendered = kinds.map((k) => `${k.message} ${k.hint}`)
  expect(new Set(rendered).size).toBe(3)
})

test('a cancelled job is a permanent refusal', () => {
  const f = classifyRetryFailure(conflict('job was cancelled; retry is not available for a cancelled job'), 'failed')
  expect(f.kind).toBe('cancelled')
})

test('an unfinished job is a wait-and-try-later refusal', () => {
  const f = classifyRetryFailure(conflict('job is not finished; retry is available for a done or failed job'), 'failed')
  expect(f.kind).toBe('not-finished')
})

test('a 404 reads as absent-or-not-yours, never as a server fault', () => {
  const f = classifyRetryFailure(new ApiError(404, 'job not found', '404 job not found'), 'failed')
  expect(f.kind).toBe('denied')
  expect(f.hint).toContain('owner')
})

test('an unrecognized error falls back to the server text, never to a generic string', () => {
  const f = classifyRetryFailure(new ApiError(500, 'db error', '500 db error'), 'failed')
  expect(f.kind).toBe('unknown')
  // The server's own text is `code` ("db error"), not `message` ("500 db error"):
  // apiFetch builds message as `${status} ${code}`, and every OTHER classified
  // branch below renders the clean code (err.code), not the status-prefixed
  // message. This fallback must match its siblings or a raw HTTP status leaks
  // into a banner whose neighbors never show one.
  expect(f.message).toBe('db error')
})

test('a non-ApiError still renders its own message', () => {
  const f = classifyRetryFailure(new Error('network down'), 'failed')
  expect(f.kind).toBe('unknown')
  expect(f.message).toBe('network down')
})
