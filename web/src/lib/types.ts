export interface User {
  id: string
  email: string
  name: string
  role: string
}

export interface LoginResponse {
  token: string
  expires: string
  user: User
}

export interface ConfigResponse {
  allow_self_register: boolean
}
