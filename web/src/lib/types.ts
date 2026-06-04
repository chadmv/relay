export interface User {
  id: string
  email: string
  name: string
  is_admin: boolean
}

export interface LoginResponse {
  token: string
  expires_at: string
}

export interface ConfigResponse {
  allow_self_register: boolean
}
