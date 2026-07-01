import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { useAuth } from '../auth/AuthProvider'
import { Button } from '../components/Button'
import { MetricChart } from './MetricChart'
import { StatusDot } from '../components/holo/StatusDot'
import { WorkerActions } from './WorkerActions'
import { WorkspacesPanel } from './WorkspacesPanel'
import { formatGB, formatRelativeTime, labelChips, livenessView } from './liveness'
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
  const { data: worker, error, isLoading, refetch } = useWorker(id)
  const { data: metrics } = useWorkerMetrics(id)

  if (isLoading && !worker) {
    return <div className="h-40 rounded-card border border-border bg-white/5" />
  }

  if (error && !worker) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
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
      </div>
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
  const chips = labelChips(worker.labels)

  return (
    <div className={`flex flex-col gap-5 ${livenessView(worker.status).dimClass}`}>
      <div className="flex flex-col gap-1">
        <Link to="/workers" className="font-mono text-[11px] text-fg-mute hover:text-fg">
          &larr; Workers
        </Link>
        <div className="flex items-center gap-3">
          <h1 className="text-[28px] font-normal tracking-tight">{worker.name}</h1>
          <StatusDot status={worker.status} />
        </div>
        <div className="font-mono text-[11px] text-fg-mute">
          id {worker.id.slice(0, 8)} · {worker.hostname} · {worker.os} ·{' '}
          {worker.last_seen_at ? `last seen ${formatRelativeTime(worker.last_seen_at)}` : 'never seen'}
          {worker.last_sample_at ? ` · sampled ${formatRelativeTime(worker.last_sample_at)}` : ''}
        </div>
      </div>

      {user?.is_admin && <WorkerActions worker={worker} />}

      <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-3">
        <div className="rounded-card border border-border bg-white/5 p-4">
          <div className="font-mono text-[10px] tracking-wider text-fg-mute">CPU &middot; RAM</div>
          <div className="mt-1 text-[20px]">{worker.cpu_cores}c · {worker.ram_gb}GB</div>
          <div className="font-mono text-[10px] text-fg-mute">os: {worker.os}</div>
        </div>
        <div className="rounded-card border border-border bg-white/5 p-4">
          <div className="font-mono text-[10px] tracking-wider text-fg-mute">GPU</div>
          <div className="mt-1 text-[20px]">
            {hasGpu ? `${worker.gpu_count} × ${worker.gpu_model}` : 'No GPU'}
          </div>
        </div>
        <div className="rounded-card border border-border bg-white/5 p-4">
          <div className="font-mono text-[10px] tracking-wider text-fg-mute">MAX SLOTS</div>
          <div className="mt-1 text-[20px]">{worker.max_slots}</div>
          <div className="font-mono text-[10px] text-fg-mute">capacity</div>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-baseline justify-between">
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">TELEMETRY</div>
          <div className="font-mono text-[10px] text-fg-dim">last 30 min &middot; 10s samples</div>
        </div>
        {samples.length === 0 ? (
          <div className="rounded-card border border-border bg-white/5 p-6 text-center text-[12px] text-fg-mute">
            No telemetry yet.
          </div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
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
      </div>

      {chips.length > 0 && (
        <div className="flex flex-col gap-2">
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">LABELS</div>
          <div className="flex flex-wrap gap-1">
            {chips.map((c) => (
              <span
                key={c}
                className="rounded-full border border-accent/40 bg-accent/10 px-2 py-0.5 font-mono text-[10px] text-accent"
              >
                {c}
              </span>
            ))}
          </div>
        </div>
      )}

      {user?.is_admin && <WorkspacesPanel workerId={id} />}
    </div>
  )
}
