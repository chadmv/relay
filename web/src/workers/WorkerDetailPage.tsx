import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { useAuth } from '../auth/AuthProvider'
import { Button } from '../components/Button'
import { Chip, GlassPanel, KpiStat, Panel, StatusDot } from '../components/holo'
import { MetricChart } from './MetricChart'
import { WorkerActions } from './WorkerActions'
import { WorkerLabels } from './WorkerLabels'
import { WorkspacesPanel } from './WorkspacesPanel'
import { formatGB, formatRelativeTime, livenessView } from './liveness'
import { useWorker } from './useWorker'
import { useWorkerMetrics } from './useWorkerMetrics'
import type { MetricSample } from './api'

function pct(n: number): string {
  return `${Math.round(n)}%`
}

function last<T>(arr: T[]): T | undefined {
  return arr[arr.length - 1]
}

export function WorkerDetailPage() {
  const { id = '' } = useParams()
  const { user } = useAuth()
  const isAdmin = Boolean(user?.is_admin)
  const { data: worker, error, isLoading, refetch } = useWorker(id)
  const { data: metrics } = useWorkerMetrics(id)

  if (isLoading && !worker) {
    return <GlassPanel className="h-40" />
  }

  if (error && !worker) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        {notFound ? (
          <div className="text-[13px] text-fg-mute">Worker not found.</div>
        ) : (
          <>
            <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => refetch()}>
              Retry
            </Button>
          </>
        )}
        <div className="mt-4">
          <Link to="/workers" className="font-mono text-[11px] text-accent">
            &larr; Workers
          </Link>
        </div>
      </GlassPanel>
    )
  }

  if (!worker) return null

  const samples: MetricSample[] = metrics?.samples ?? []
  // Gate GPU charts on the hardware-stable gpu_count, not the per-sample `gpu`
  // flag (a transient nvidia-smi success), so the charts do not flicker away on
  // a single failed reading.
  const hasGpu = worker.gpu_count > 0
  const latest = last(samples)
  const memTotal = latest?.mem_total ?? 0
  const gpuMemTotal = latest?.gpu_mem_total ?? 0
  const view = livenessView(worker.status)
  const isStale = worker.status === 'stale'

  return (
    <div className={`flex flex-col gap-4 ${view.dimClass}`}>
      {/* Breadcrumb + header row: back link, name, inline status chip; action bar (admin, ml-auto). */}
      <div className="flex items-center gap-2.5">
        <Link to="/workers" className="text-[12px] text-fg-mute hover:text-fg">
          &larr; Workers
        </Link>
        <span className="text-fg-dim">/</span>
        <span className="font-mono text-[14px] tracking-[0.04em] text-fg">{worker.name}</span>
        {worker.status === 'disabled' && <Chip tone="muted">{view.label}</Chip>}
        <span className="ml-auto">
          <StatusDot status={worker.status} />
        </span>
      </div>

      {/* Identity sub-line. Last-seen turns warn when stale. */}
      <div className="font-mono text-[11px] tracking-[0.04em] text-fg-mute">
        id <span className="text-fg">{worker.id.slice(0, 8)}</span> · hostname{' '}
        <span className="text-fg">{worker.hostname}</span> · os{' '}
        <span className="text-fg">{worker.os}</span> · last seen{' '}
        <span className={isStale ? 'text-warn' : 'text-fg'}>
          {worker.last_seen_at ? formatRelativeTime(worker.last_seen_at) : 'never'}
        </span>
      </div>

      {/* Admin action bar (repositioned WorkerActions; banners + edit form render below the header). */}
      {isAdmin && <WorkerActions worker={worker} />}

      {/* KPI stat row. */}
      <div className="grid grid-cols-4 gap-3">
        <KpiStat label="CPU · RAM" value={`${worker.cpu_cores}c · ${worker.ram_gb}G`} sub={`os: ${worker.os}`} />
        <KpiStat
          label="GPU"
          value={hasGpu ? `${worker.gpu_count} × ${worker.gpu_model}` : 'No GPU'}
        />
        {/* `used` (active slots) is not on the Worker type yet: render "— / max" with an
            empty progress bar until feature-2026-06-05-worker-detail-activity-panel lands. */}
        <KpiStat label="Slots" value={`— / ${worker.max_slots}`} progress={{ used: 0, max: worker.max_slots }} />
        {/* Backend-blocked: no per-worker activity aggregate exists yet.
            Enabler: feature-2026-06-05-worker-detail-activity-panel. */}
        <KpiStat label="Jobs today" value="—" sub="activity endpoint pending" />
      </div>

      {/* Two-column body. */}
      <div className="grid grid-cols-2 gap-3">
        {/* Left column. */}
        <div className="flex flex-col gap-3">
          {/* Backend-blocked: no per-worker task feed endpoint exists yet.
              Enabler: feature-2026-06-05-worker-detail-activity-panel. */}
          <Panel title="Current tasks" meta="ACTIVITY ENDPOINT PENDING">
            <div className="px-4 py-6 font-mono text-[11px] tracking-[0.04em] text-fg-dim">
              no per-worker task feed yet
            </div>
          </Panel>

          {isAdmin && (
            <Panel title="Source workspaces" meta="/v1/workers/.../workspaces">
              <WorkspacesPanel workerId={id} />
            </Panel>
          )}
        </div>

        {/* Right column. */}
        <div className="flex flex-col gap-3">
          <Panel title="Labels" meta={isAdmin ? 'PATCH /v1/workers' : undefined}>
            <WorkerLabels worker={worker} />
          </Panel>

          {isAdmin && (
            <>
              {/* Backend-blocked: /v1/reservations is global admin with no worker filter.
                  Enabler: feature-2026-06-05-worker-detail-reservations-panel. */}
              <Panel title="Reservations" meta="RESERVATIONS ENDPOINT PENDING">
                <div className="flex flex-col gap-2 px-4 py-3">
                  <div className="font-mono text-[11px] tracking-[0.04em] text-fg-dim">
                    no per-worker reservation lookup yet
                  </div>
                  <div className="font-mono text-[10px] tracking-[0.04em] text-fg-dim">
                    selectors are informational in v1 · only worker_ids are enforced.
                  </div>
                </div>
              </Panel>

              {/* Agent token: the value is never exposed over HTTP (hash-only by design,
                  internal/tokenhash). Revoke already lives in the header action bar, so
                  this is an inline explanatory note, not a panel with a second Revoke. */}
              <div className="rounded-input border border-border bg-black/25 px-4 py-3 font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
                Long-lived agent token. Revoking (in the action bar above) forces the agent to exit and re-enroll with a fresh token.
              </div>
            </>
          )}

          {/* Utilization telemetry: REAL, fed by useWorkerMetrics. Empty/stale/offline preserved. */}
          <Panel
            title="Utilization · last 30m"
            meta="GET /v1/workers/{id}/metrics"
            footer={<span>last 30 min · 10s samples</span>}
          >
            {samples.length === 0 ? (
              <div className="px-4 py-6 text-center text-[12px] text-fg-mute">No telemetry yet.</div>
            ) : (
              <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3 p-3">
                <MetricChart
                  title="CPU"
                  values={samples.map((s) => s.cpu_pct)}
                  max={100}
                  current={latest ? pct(latest.cpu_pct) : '-'}
                  colorClass="text-accent"
                />
                <MetricChart
                  title="MEMORY"
                  values={samples.map((s) => s.mem_used)}
                  max={memTotal}
                  current={latest ? `${formatGB(latest.mem_used)} / ${formatGB(latest.mem_total)}` : '-'}
                  colorClass="text-ok"
                />
                {hasGpu && (
                  <>
                    <MetricChart
                      title="GPU"
                      values={samples.map((s) => s.gpu_util_pct)}
                      max={100}
                      current={latest ? pct(latest.gpu_util_pct) : '-'}
                      colorClass="text-warn"
                    />
                    <MetricChart
                      title="GPU MEMORY"
                      values={samples.map((s) => s.gpu_mem_used)}
                      max={gpuMemTotal}
                      current={latest ? `${formatGB(latest.gpu_mem_used)} / ${formatGB(latest.gpu_mem_total)}` : '-'}
                      colorClass="text-warn"
                    />
                  </>
                )}
              </div>
            )}
          </Panel>
        </div>
      </div>
    </div>
  )
}
