import { apiFetch } from '../../lib/api'

// Mirrors reservationResponse / toReservationResponse
// (internal/api/reservations.go:13-53). The nullability split is the whole point of
// this comment:
//
//  - `selector` has NO omitempty and is a json.RawMessage produced by rawJSON, not
//    rawObject (:16, :45; server.go:236-240). A create with no selector marshals a
//    nil map to the literal `null`, which rawJSON passes through unchanged, so
//    `"selector": null` is a REAL response value. Rows that kept the column default
//    read `{}`. Both must be tolerated - hence `| null` and not `?`.
//  - `project` / `starts_at` / `ends_at` are POINTERS with omitempty (:19-21), so
//    the KEY IS ABSENT when NULL. Hence `?: string` and never `| null`.
//  - `worker_ids` is built with make() (:26): always an array, [] when empty, never
//    null.
//  - `user_id` is a bare user UUID with NO join to `users`, which is why no owner
//    column is rendered.
//  - Timestamps are Go time.Time, i.e. RFC3339 with nanosecond precision. Parse
//    with new Date(); never string-compare.
//
// The selector type is the shape this UI can produce and the shape the handler can
// accept (it decodes into map[string]string, :232). A row written directly through
// SQL could hold nested JSON; the table only ever renders `k=v` pairs of it, so an
// exotic value degrades to a stringified cell rather than crashing.
export interface Reservation {
  id: string
  name: string
  selector: Record<string, string> | null
  worker_ids: string[]
  user_id: string
  created_at: string
  project?: string
  starts_at?: string
  ends_at?: string
}

// internal/api/pagination.go:289-293.
export interface ReservationsPage {
  items: Reservation[]
  next_cursor: string
  total: number
}

// ReservationsSortSpec (internal/api/reservations.go:55-63): four keys, each with an
// optional '-' prefix, default '-created_at'. All EIGHT arms are implemented
// (:106-217) and all eight are indexed (migration 000013), so every one is a real
// server capability rather than a hopeful client string.
export type ReservationSortField = 'created_at' | 'name' | 'starts_at' | 'ends_at'
export type ReservationSort =
  | 'created_at'
  | '-created_at'
  | 'name'
  | '-name'
  | 'starts_at'
  | '-starts_at'
  | 'ends_at'
  | '-ends_at'

export interface ListReservationsParams {
  sort: ReservationSort
  cursor: string
  // Set only when non-empty, and appended LAST, so the admin caller's URL is
  // unchanged. The server treats an empty value as absent, so sending one would
  // be a silent URL change rather than a wire error - which is exactly the kind
  // that no server-side failure would ever surface.
  workerId?: string
}

export function listReservations({
  sort,
  cursor,
  workerId,
}: ListReservationsParams): Promise<ReservationsPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (cursor) q.set('cursor', cursor)
  if (workerId) q.set('worker_id', workerId)
  return apiFetch<ReservationsPage>(`/reservations?${q}`)
}

// What this UI sends, and nothing else.
//  - `selector` is never sent: the scheduler never reads it, and the handler decodes
//    it into map[string]string so a nested object 400s as 'invalid request body'.
//  - `user_id` is never sent: the handler defaults it to the authenticated admin
//    (:255-263), it grants nothing, and a valid-but-nonexistent UUID returns 500
//    from the users FK (:294-297) rather than a 400.
//  - `project` / `starts_at` / `ends_at` are OMITTED when blank rather than sent as
//    "" or null, so the stored row says what the admin actually supplied.
//  - Dates are full RFC3339 with an offset. A datetime-local input yields a
//    zone-less string that Go's time.Time decoder rejects, so the form converts with
//    new Date(localValue).toISOString().
export interface CreateReservationBody {
  name: string
  worker_ids: string[]
  project?: string
  starts_at?: string
  ends_at?: string
}

// 201 echoes the full row - the same shape the list returns, and nothing secret.
export function createReservation(body: CreateReservationBody): Promise<Reservation> {
  return apiFetch<Reservation>('/reservations', { method: 'POST', json: body })
}

// 204 with no body (internal/api/reservations.go:322); apiFetch returns undefined for
// 204 (web/src/lib/api.ts:57). Hard delete - there is no soft-delete column.
export function deleteReservation(id: string): Promise<void> {
  return apiFetch<void>(`/reservations/${id}`, { method: 'DELETE' })
}
