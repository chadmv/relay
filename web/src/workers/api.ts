import { apiFetch } from '../lib/api'

export type WorkerStatus = 'online' | 'stale' | 'offline' | 'disabled'

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

export interface MetricSample {
  t: string
  cpu_pct: number
  mem_used: number
  mem_total: number
  gpu: boolean
  gpu_util_pct: number
  gpu_mem_used: number
  gpu_mem_total: number
}

export interface WorkerMetrics {
  worker_id: string
  sample_interval_seconds: number
  samples: MetricSample[]
}

export interface Workspace {
  source_type: string
  source_key: string
  short_id: string
  baseline_hash: string
  last_used_at: string
}

export function getWorker(id: string): Promise<Worker> {
  return apiFetch<Worker>(`/workers/${id}`)
}

// Short-term utilization history. samples is always present (empty for an
// offline / never-sampled worker).
export function getWorkerMetrics(id: string): Promise<WorkerMetrics> {
  return apiFetch<WorkerMetrics>(`/workers/${id}/metrics`)
}

// Admin-only. Source workspaces resident on the worker.
export function listWorkerWorkspaces(id: string): Promise<Workspace[]> {
  return apiFetch<Workspace[]>(`/workers/${id}/workspaces`)
}
