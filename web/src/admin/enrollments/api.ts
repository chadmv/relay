import { apiFetch } from '../../lib/api'

// Mirrors enrollmentRowToMap (internal/api/agent_enrollments.go:83-94) and its
// three sort-variant twins (:100-149), which are field-for-field identical.
//
// hostname_hint is OPTIONAL, not nullable: the Go map omits the key entirely when
// the column is NULL (:90-92). Consumers must handle `undefined`, never `null`.
// created_by is a bare user UUID - there is no join to `users`, which is why no
// CREATED BY column is rendered.
// created_at / expires_at are Go time.Time values, i.e. RFC3339 with nanosecond
// precision. Parse with new Date(); never string-compare them.
export interface AgentEnrollment {
  id: string
  created_at: string
  expires_at: string
  created_by: string
  hostname_hint?: string
}

// internal/api/pagination.go:289-293.
export interface AgentEnrollmentsPage {
  items: AgentEnrollment[]
  next_cursor: string
  total: number
}

// AgentEnrollmentsSortSpec (internal/api/agent_enrollments.go:75-81): two keys,
// each with an optional '-' prefix, default '-created_at'. All four arms are
// implemented (:162-217).
export type EnrollmentSortField = 'created_at' | 'expires_at'
export type EnrollmentSort = 'created_at' | '-created_at' | 'expires_at' | '-expires_at'

// internal/api/agent_enrollments.go:16-20. NOTE: the 60s floor is enforced only
// for a NONZERO ttl_seconds (:31-38) - 0 or an absent key means the 24h default,
// and a negative value 400s. This UI always sends an explicit preset, so the
// floor and the ceiling are both unreachable from the UI; the constants exist so
// TTL_PRESETS can be checked against them (api.test.ts).
export const MIN_TTL_SECONDS = 60
export const MAX_TTL_SECONDS = 604800
export const DEFAULT_TTL_SECONDS = 86400

export interface TtlPreset {
  label: string
  seconds: number
}

// The hi-fi's four presets (hifi3-holo-pages.jsx:2375), 24h preselected to match
// both the server default and the hi-fi. Raw seconds are never shown to the
// admin: "604800" is hostile, "7d" is not.
export const TTL_PRESETS: TtlPreset[] = [
  { label: '1h', seconds: 3600 },
  { label: '24h', seconds: DEFAULT_TTL_SECONDS },
  { label: '3d', seconds: 259200 },
  { label: '7d', seconds: MAX_TTL_SECONDS },
]

export interface ListEnrollmentsParams {
  sort: EnrollmentSort
  cursor: string
}

export function listAgentEnrollments({
  sort,
  cursor,
}: ListEnrollmentsParams): Promise<AgentEnrollmentsPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (cursor) q.set('cursor', cursor)
  return apiFetch<AgentEnrollmentsPage>(`/agent-enrollments?${q}`)
}

// hostname_hint is omitted when blank rather than sent as "": the server treats
// the two identically (internal/api/agent_enrollments.go:58-60), and omitting
// keeps the request body honest about what the admin actually supplied.
export interface CreateEnrollmentBody {
  hostname_hint?: string
  ttl_seconds: number
}

// The 201 body, internal/api/agent_enrollments.go:68-72. There is no created_at
// and no hostname_hint echo, which is why no optimistic row is appended anywhere.
//
// SECURITY: `token` is the raw 64-char hex enrollment credential. Only
// tokenhash.Hash(rawHex) is persisted (:51) and the list endpoint returns no token
// field, so it is UNRECOVERABLE after this response. Never log it, never put it in
// a URL or a query key, and never copy it into component state - it is rendered
// straight from the mutation's data by web/src/admin/TokenRevealDialog.tsx so that
// create.reset() is the single point that destroys it.
export interface CreateEnrollmentResponse {
  id: string
  token: string
  expires_at: string
}

// A body is ALWAYS sent, even when the hint is blank: the handler calls readJSON
// unconditionally (internal/api/agent_enrollments.go:27 -> server.go:199-211), so
// a POST with no body decodes as io.EOF and 400s "invalid request body".
export function createAgentEnrollment(
  body: CreateEnrollmentBody,
): Promise<CreateEnrollmentResponse> {
  return apiFetch<CreateEnrollmentResponse>('/agent-enrollments', { method: 'POST', json: body })
}
