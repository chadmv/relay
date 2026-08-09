import { apiFetch } from '../../lib/api'

// Mirrors internal/api/users.go:22-29 (userResponse). archived_at is nullable AND
// is only meaningful when include_archived=true: usersListRowToResponse passes a
// zero timestamp for the active-only query family (internal/api/users.go:111-132),
// so never infer "archived" from archived_at unless the toggle is on.
export interface AdminUser {
  id: string
  email: string
  name: string
  is_admin: boolean
  created_at: string
  archived_at: string | null
}

export interface AdminUsersPage {
  items: AdminUser[]
  next_cursor: string
  total: number
}

// The three sortable keys accepted by UsersSortSpec (internal/api/users.go:69-76).
export type UserSortField = 'created_at' | 'name' | 'email'

export type UserSort =
  | '-created_at'
  | 'created_at'
  | 'name'
  | '-name'
  | 'email'
  | '-email'

export interface ListUsersParams {
  sort: UserSort
  includeArchived: boolean
  cursor: string
  email: string
}

// limit=50 is the server default, passed explicitly so the client's page size is
// self-documenting (same as listWorkers). The server short-circuits the ?email=
// branch before pagination, returning the same envelope with 0 or 1 items.
export function listUsers({ sort, includeArchived, cursor, email }: ListUsersParams): Promise<AdminUsersPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (includeArchived) q.set('include_archived', 'true')
  if (cursor) q.set('cursor', cursor)
  if (email) q.set('email', email)
  return apiFetch<AdminUsersPage>(`/users?${q}`)
}

// Mirrors createUserRequest (internal/api/users.go:569-575). This is the ONLY
// place is_admin can be set; no endpoint mutates it afterwards. A blank name
// defaults to the email server-side. 409 on a duplicate email.
export interface CreateUserBody {
  email: string
  name: string
  password: string
  is_admin: boolean
}

export function createUser(body: CreateUserBody): Promise<AdminUser> {
  return apiFetch<AdminUser>('/users', { method: 'POST', json: body })
}

// {id} is the user UUID, not the email. The body accepts ONLY name
// (updateUserRequest, internal/api/users.go:49-51).
export function renameUser(id: string, name: string): Promise<AdminUser> {
  return apiFetch<AdminUser>(`/users/${id}`, { method: 'PATCH', json: { name } })
}

// Transactional server-side: archives, deletes the target's API tokens, disables
// their scheduled jobs. 400 on self or last-active-admin, 409 if already archived.
export function archiveUser(id: string): Promise<AdminUser> {
  return apiFetch<AdminUser>(`/users/${id}/archive`, { method: 'POST' })
}

export function unarchiveUser(id: string): Promise<AdminUser> {
  return apiFetch<AdminUser>(`/users/${id}/unarchive`, { method: 'POST' })
}

// Keyed by email in the BODY, not by a path id. Returns 204 (no body) and deletes
// every one of the target's tokens - including yours if you target yourself.
export function resetUserPassword(email: string, newPassword: string): Promise<void> {
  return apiFetch<void>('/users/password-reset', {
    method: 'POST',
    json: { email, new_password: newPassword },
  })
}
