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

// Fetches a single worker. Throws ApiError(404) if the worker does not exist.
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

// Admin-only. Lists revoked (decommissioned) workers, newest revocation first.
// limit=50 matches listWorkers. Pass cursor='' for the first page.
export function listRevokedWorkers(cursor = ''): Promise<WorkersPage> {
  const q = new URLSearchParams({ limit: '50' })
  if (cursor) q.set('cursor', cursor)
  return apiFetch<WorkersPage>(`/workers/revoked?${q}`)
}

export interface DisableWorkerResponse extends Worker {
  requeued_tasks: number
}

export interface WorkerPatch {
  name?: string
  labels?: Record<string, string>
  max_slots?: number
}

// Admin-only. Rename / edit labels / set max_slots. Fields omitted keep their
// current value; `labels`, when present, is a full replace of the label map (the
// server marshals the whole map), not a per-key merge.
export function updateWorker(id: string, patch: WorkerPatch): Promise<Worker> {
  return apiFetch<Worker>(`/workers/${id}`, { method: 'PATCH', json: patch })
}

// Admin-only. Disable (pause) the worker. requeue=true is the "drain" concept:
// in-flight tasks are requeued to other workers and cancelled here.
export function disableWorker(id: string, requeue: boolean): Promise<DisableWorkerResponse> {
  const q = requeue ? '?requeue=true' : ''
  return apiFetch<DisableWorkerResponse>(`/workers/${id}/disable${q}`, { method: 'POST' })
}

// Admin-only. Re-enable a disabled worker.
export function enableWorker(id: string): Promise<Worker> {
  return apiFetch<Worker>(`/workers/${id}/enable`, { method: 'POST' })
}

// Admin-only. Revoke the agent token. TERMINAL: also sets the worker to
// `revoked`, which excludes it from every list/get endpoint. Returns 204 (no
// body). After success the caller must navigate away, not re-fetch the worker.
export function revokeWorkerToken(id: string): Promise<void> {
  return apiFetch<void>(`/workers/${id}/token`, { method: 'DELETE' })
}

// Admin-only. Request eviction of a source workspace. Best-effort/async: returns
// 202 (no body); the agent evicts on its stream and confirms later via an
// inventory update. A held workspace is refused by the agent, not this endpoint.
export function evictWorkspace(id: string, shortId: string): Promise<void> {
  return apiFetch<void>(`/workers/${id}/workspaces/${shortId}/evict`, { method: 'POST' })
}
