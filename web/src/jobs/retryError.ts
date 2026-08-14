import { ApiError } from '../lib/api'
import type { RetryMode } from './api'

// The three conflicts POST /v1/jobs/{id}/retry can report are NOT interchangeable:
// nothing-matched is a dead end, blocked-by-dependents is permanent for this job
// in this mode, and raced means "do it again". Rendering all three as one generic
// failure would hand the operator no next step, so this module turns the server's
// sentence into a kind plus a frontend-owned hint.
//
// Classification keys on ApiError.code, which apiFetch fills with the server's
// {"error": ...} string VERBATIM (ApiError.message is that string with the numeric
// status prefixed). The prefixes below are copied from handleRetryJob in
// internal/api/jobs.go and are pinned by retryErrorContract.test.ts, which reads
// that file and reddens if a prefix stops existing.
//
// Unrecognized input NEVER collapses to a generic string: it falls through to the
// server's own text, so the reasons stay distinguishable even if the prose drifts
// ahead of this file.
// `raced` is deliberately the longer phrase "...while the retry was in flight",
// not the shorter "the job changed": the blocked sentence below ALSO contains
// "the job changed" ("...or the job changed while the REQUEST was in flight..."),
// so the short form is not branch-unique and retryErrorContract.test.ts could not
// tell a reworded raced sentence from an unrelated hit on the blocked sentence.
// "...while the retry was in flight" appears only in the raced sentence.
export const RETRY_ERROR_PREFIXES = {
  noneMatched: 'no tasks matched',
  blocked: 'no tasks were reopened',
  raced: 'the job changed while the retry was in flight',
  cancelled: 'job was cancelled',
  notFinished: 'job is not finished',
} as const

export type RetryFailureKind =
  | 'none-matched'
  | 'blocked'
  | 'raced'
  | 'cancelled'
  | 'not-finished'
  | 'denied'
  | 'unknown'

export interface RetryFailure {
  kind: RetryFailureKind
  /** The sentence to show. The server's own wording whenever there is one. */
  message: string
  /** Frontend-owned next step. Empty when there is nothing useful to add. */
  hint: string
}

export function classifyRetryFailure(err: unknown, mode?: RetryMode): RetryFailure {
  if (!(err instanceof ApiError)) {
    const message = err instanceof Error ? err.message : 'Retry failed.'
    return { kind: 'unknown', message, hint: '' }
  }

  if (err.status === 404) {
    return {
      kind: 'denied',
      message: 'This job is not available to retry.',
      hint: 'It may have been removed, or you may not be its owner.',
    }
  }

  if (err.status === 409) {
    const p = RETRY_ERROR_PREFIXES
    if (err.code.startsWith(p.noneMatched)) {
      return { kind: 'none-matched', message: err.code, hint: 'Nothing was changed.' }
    }
    if (err.code.startsWith(p.blocked)) {
      // The server sentence hedges ("or the job changed"), so the hint must not
      // assert dependents as a certainty. The Retry all suggestion is verified
      // against RetryJobTasks: its guard ignores a descendant that is itself in
      // `selected`, and task=all puts every finished descendant in `selected`.
      return {
        kind: 'blocked',
        message: err.code,
        hint:
          mode === 'failed'
            ? 'Nothing was applied. Retry all also reopens the tasks that depend on these, which is usually what unblocks it.'
            : 'Nothing was applied.',
      }
    }
    if (err.code.startsWith(p.raced)) {
      return { kind: 'raced', message: err.code, hint: 'Nothing was applied. Retry the action.' }
    }
    if (err.code.startsWith(p.cancelled)) {
      return { kind: 'cancelled', message: err.code, hint: 'A cancelled job cannot be retried.' }
    }
    if (err.code.startsWith(p.notFinished)) {
      return { kind: 'not-finished', message: err.code, hint: 'Wait for the job to finish, then retry it.' }
    }
  }

  // err.code, not err.message: every classified branch above renders the
  // server's clean sentence (ApiError.code), never the status-prefixed
  // "<status> <code>" (ApiError.message). This fallback must match its siblings
  // or an unclassified 4xx/5xx would show a raw HTTP status in a banner whose
  // neighbors never show one.
  return { kind: 'unknown', message: err.code, hint: '' }
}
