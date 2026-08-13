export interface User {
  id: string
  email: string
  name: string
  is_admin: boolean
  // ALWAYS present. userResponse has no omitempty on CreatedAt
  // (internal/api/users.go:22-29) and users.created_at is NOT NULL DEFAULT NOW()
  // (internal/store/migrations/000001_initial.up.sql:9). RFC3339 with
  // nanoseconds. Required rather than optional-with-a-fallback: no file in
  // web/src constructs a User-annotated literal (the only typed uses are
  // apiFetch<User> and user: User | null, AuthProvider.tsx:18,56,70), so making
  // it required costs nothing and a fallback would only hide a broken fixture.
  //
  // archived_at is deliberately NOT modelled: an archived user's token cannot
  // authenticate at all - GetTokenWithUser joins AND u.archived_at IS NULL
  // (internal/store/query/tokens.sql:20) - so on the endpoints that produce this
  // type the field can only ever be null.
  created_at: string
}

export interface LoginResponse {
  token: string
  expires_at: string
}

export interface ConfigResponse {
  allow_self_register: boolean
}
