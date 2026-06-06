import { apiFetch } from '../lib/api'

export type WorkerStatus = 'online' | 'stale' | 'offline' | 'disabled' | 'revoked'

export interface WorkerStats {
  online: number
  stale: number
  offline: number
  disabled: number
  total: number
}

export interface Worker {
  id: string
  name: string
  hostname: string
  cpu_cores: number
  ram_gb: number
  gpu_count: number
  gpu_model: string
  os: string
  max_slots: number
  labels: Record<string, string> | null
  status: WorkerStatus
  last_seen_at?: string
  last_sample_at?: string
  disabled_at?: string
  revoked_at?: string
}

export interface WorkersPage {
  items: Worker[]
  next_cursor: string
  total: number
}

export type WorkerSort =
  | '-created_at'
  | 'created_at'
  | 'name'
  | '-name'
  | 'status'
  | '-status'
  | 'last_seen_at'
  | '-last_seen_at'

// First page only. limit=50 is the server default, passed explicitly so the
// client's page size is self-documenting and decoupled from server changes.
export function listWorkers(sort: WorkerSort): Promise<WorkersPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  return apiFetch<WorkersPage>(`/workers?${q}`)
}

// Fleet-wide worker counts for the summary strip. Buckets sum to total; revoked
// workers are excluded server-side.
export function getWorkerStats(): Promise<WorkerStats> {
  return apiFetch<WorkerStats>('/workers/stats')
}

// Admin-only. Lists revoked (decommissioned) workers, newest revocation first.
// First page only; limit=50 matches listWorkers.
export function listRevokedWorkers(): Promise<WorkersPage> {
  const q = new URLSearchParams({ limit: '50' })
  return apiFetch<WorkersPage>(`/workers/revoked?${q}`)
}
