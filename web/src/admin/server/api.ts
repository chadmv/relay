import { apiFetch } from '../../lib/api'
import type { ConfigResponse } from '../../lib/types'

// Mirrors handleHealth (internal/api/health.go:5-7), which writes
// map[string]string{"status": "ok"}. `status` is deliberately `string` and not a
// closed union: the Go handler's type admits any value, and the pill reports what
// the server said rather than asserting health from a 200.
//
// HEALTHY here means "the HTTP listener answered". handleHealth performs NO
// database check, so this must never be presented as a database probe.
export interface HealthResponse {
  status: string
}

export function getHealth(): Promise<HealthResponse> {
  return apiFetch<HealthResponse>('/health')
}

// ConfigResponse already exists in lib/types.ts:13-15 (RegisterScreen consumes it).
// Re-export the type so this module's consumers have one import site, but do NOT
// redeclare the shape.
export type { ConfigResponse }

// Public endpoint (internal/api/config.go:5-11): no bearer required. apiFetch
// attaches one anyway when a token is present, which the server ignores.
export function getServerConfig(): Promise<ConfigResponse> {
  return apiFetch<ConfigResponse>('/config')
}
