import { apiFetch } from '../../lib/api'

// Mirrors inviteEntry (internal/api/invites.go:125-146), the SINGLE builder all
// four sort arms share - so unlike the enrollments handler there is exactly one
// response shape to model here.
//
// email and used_at are OPTIONAL, not nullable: the Go map omits each key
// entirely when the value is unset (:139-144). Consumers must handle `undefined`,
// never `null`, and a check written as `used_at !== null` is a compile error
// rather than a silently-always-true condition.
//
// created_by_email comes from an inner JOIN on users
// (internal/store/query/invites.sql:32), which is why this table CAN render a
// CREATED BY column where the enrollments one could not.
//
// There is deliberately NO status field (the handler says so at :112-121) and no
// token, hash or prefix: omitting i.token_hash from the projection is the
// endpoint's entire security control (invites.sql:22-25).
//
// created_at / expires_at / used_at are Go time.Time values, i.e. RFC3339 with
// nanosecond precision. Parse with new Date(); never string-compare them.
export interface Invite {
  id: string
  created_at: string
  expires_at: string
  created_by: string
  created_by_email: string
  email?: string
  used_at?: string
}

// internal/api/pagination.go:288-293.
export interface InvitesPage {
  items: Invite[]
  next_cursor: string
  total: number
}

// InvitesSortSpec (internal/api/invites.go:97-103): two keys, each with an
// optional '-' prefix, default '-created_at'. All four dispatch arms exist
// (:160, :180, :200, :220), so both directions of both keys are live.
export type InviteSortField = 'created_at' | 'expires_at'
export type InviteSort = 'created_at' | '-created_at' | 'expires_at' | '-expires_at'

// The server default (internal/api/invites.go:31) and the hi-fi's preselection
// (design_handoff_relay_holo/hifi3-holo-pages.jsx:2087) agree on 72h.
export const DEFAULT_EXPIRES_IN = '72h'

// internal/api/invites.go:43-47. The check is `dur > maxInviteDuration`, so 720h
// EXACTLY is accepted. Exported so api.test.ts can check the presets against it.
export const MAX_EXPIRES_IN_HOURS = 720

export interface TtlPreset {
  label: string
  // What goes on the wire, and it is NOT always the label.
  value: string
}

// `expires_in` is parsed by Go's time.ParseDuration (internal/api/invites.go:34),
// which understands h, m, s and smaller and has NO DAY UNIT - ParseDuration("7d")
// fails with `unknown unit "d"`, so sending the literal "7d" or "30d" is a 400.
// The LABEL stays human ("30d" is readable, "720h" is hostile) and the VALUE is
// always hour-denominated. This divergence is deliberate; api.test.ts asserts it
// with a /^\d+h$/ regex, because a "7d" preset passes any naive four-presets test
// and only fails in production.
//
// Every value is inside the server's (0, 720h] window, so the invalid range is
// UNREACHABLE from the UI rather than merely rejected - there is no free-text
// duration input and therefore no client-side max check to write.
export const TTL_PRESETS: TtlPreset[] = [
  { label: '24h', value: '24h' },
  { label: '72h', value: DEFAULT_EXPIRES_IN },
  { label: '7d', value: '168h' },
  { label: '30d', value: '720h' },
]

export interface ListInvitesParams {
  sort: InviteSort
  cursor: string
}

export function listInvites({ sort, cursor }: ListInvitesParams): Promise<InvitesPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (cursor) q.set('cursor', cursor)
  return apiFetch<InvitesPage>(`/invites?${q}`)
}

// email is omitted when blank rather than sent as "": the server treats the two
// identically (internal/api/invites.go:65), and omitting keeps the request body
// honest about what the admin actually supplied. A non-empty value is validated
// server-side by mail.ParseAddress (:66) and a bad one is a 400 that the create
// form renders in its own error slot - the client does not reimplement that
// parser, because two parsers disagreeing is worse than one round trip.
export interface CreateInviteBody {
  email?: string
  expires_in: string
}

// The 201 body, internal/api/invites.go:79-87. `email` is echoed only when bound.
// There is no created_at.
//
// SECURITY: `token` is the raw 64-char hex invite credential, and it grants
// account creation on this server. Only tokenhash.Hash(rawHex) is persisted (:56)
// and the list endpoint returns no token field, so it is UNRECOVERABLE after this
// response. Never log it, never put it in a URL or a query key, and never copy it
// into component state - it is rendered straight from the mutation's data by
// web/src/admin/TokenRevealDialog.tsx so that create.reset() is the single point
// that destroys it.
export interface CreateInviteResponse {
  id: string
  token: string
  expires_at: string
  email?: string
}

// A body is ALWAYS sent, even when no email is supplied: readJSON runs
// unconditionally (internal/api/invites.go:27 -> server.go:199-211), so a POST
// with no body decodes as io.EOF and 400s "invalid request body". The minimum
// legal body is {expires_in: "72h"}.
export function createInvite(body: CreateInviteBody): Promise<CreateInviteResponse> {
  return apiFetch<CreateInviteResponse>('/invites', { method: 'POST', json: body })
}
