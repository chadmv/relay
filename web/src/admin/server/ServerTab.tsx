import { Chip, Panel } from '../../components/holo'
import { useJobStats } from '../../jobs/useJobStats'
import { useWorkerStats } from '../../workers/useWorkerStats'
import { ErrorStrip } from './ErrorStrip'
import { HealthPill } from './HealthPill'
import { StatSection, type StatCell } from './StatSection'
import { useServerConfig } from './useServerConfig'
import { useServerHealth } from './useServerHealth'

// Passed EXPLICITLY, never defaulted: useJobStats and useWorkerStats default to
// 3000ms for the jobs and workers dashboards, and a change to that default must not
// silently change this tab. 10s is strictly less load than the shipped dashboards,
// so this tab introduces no new worst case for either stats endpoint.
const POLL_MS = 10_000

export function ServerTab() {
  // Reused across module boundaries on purpose: these hooks already own
  // ['job-stats'] and ['workers','stats'], so mounting this tab creates an OBSERVER
  // on the existing cache entries rather than a second client for the same endpoint.
  const jobs = useJobStats(POLL_MS)
  const fleet = useWorkerStats(POLL_MS)
  const health = useServerHealth()
  const config = useServerConfig()

  const jobCells: StatCell[] = [
    { label: 'RUNNING', value: jobs.data?.running ?? null },
    // jobs.status = 'pending' - a JOB count, not "tasks waiting for a slot".
    { label: 'QUEUED', value: jobs.data?.queued ?? null, sub: 'status = pending' },
    { label: 'DONE · 24H', value: jobs.data?.done_24h ?? null },
    { label: 'FAILED OR CANCELLED · 24H', value: jobs.data?.failed_24h ?? null },
  ]

  const fleetCells: StatCell[] = [
    { label: 'ONLINE', value: fleet.data?.online ?? null },
    { label: 'STALE', value: fleet.data?.stale ?? null },
    { label: 'OFFLINE', value: fleet.data?.offline ?? null },
    { label: 'DISABLED', value: fleet.data?.disabled ?? null },
    {
      label: 'TOTAL',
      value: fleet.data?.total ?? null,
      // Every bucket excludes revoked workers, matching GET /v1/workers. Stated on
      // the cell so an admin never reconciles it against the decommissioned list.
      sub: 'revoked workers excluded',
      wide: true,
    },
  ]

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-col">
          <span className="text-[15px] text-fg">Server overview</span>
          <span className="font-mono text-[10.5px] tracking-[0.06em] text-fg-mute">
            Read-only · live aggregates
          </span>
        </div>
        <div className="ml-auto">
          <HealthPill data={health.data} error={health.error as Error | null} />
        </div>
      </div>

      {/* Four independent queries. No query's failure may unmount another's data. */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <StatSection
          caption="JOBS · GET /v1/jobs/stats"
          cells={jobCells}
          error={jobs.error as Error | null}
          onRetry={() => jobs.refetch()}
        />
        <StatSection
          caption="FLEET · GET /v1/workers/stats"
          cells={fleetCells}
          error={fleet.error as Error | null}
          onRetry={() => fleet.refetch()}
        />
      </div>

      <Panel title="Access" meta="GET /v1/config" bodyClassName="flex flex-col gap-2 px-4 py-3">
        {config.error && !config.data ? (
          // NEVER render a default here. A fabricated "DISABLED" would misreport the
          // registration policy, which is a security-relevant claim; a page that
          // reports nothing is strictly better than one that reports a guess.
          <ErrorStrip
            message={(config.error as Error).message}
            onRetry={() => config.refetch()}
          />
        ) : (
          <>
            <div className="flex items-center gap-3 text-[13px] text-fg">
              <span>Self-registration</span>
              {config.data ? (
                <Chip tone={config.data.allow_self_register ? 'accent' : 'muted'}>
                  {config.data.allow_self_register ? 'ENABLED' : 'DISABLED'}
                </Chip>
              ) : (
                <span className="font-mono text-[11px] text-fg-mute">—</span>
              )}
            </div>
            {config.data && (
              <div className="font-mono text-[10px] tracking-[0.04em] text-fg-mute">
                {config.data.allow_self_register
                  ? 'POST /v1/auth/register creates a non-admin account without an invite.'
                  : 'POST /v1/auth/register requires an invite_token.'}
              </div>
            )}
          </>
        )}
      </Panel>

      {/* Plain text, no nested spans, so the wording is assertable as one string. */}
      <div
        data-testid="server-footnote"
        className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim"
      >
        ▸ Every value here comes from a named response field of GET /v1/jobs/stats, GET
        /v1/workers/stats or GET /v1/config. Version, build, uptime, database and environment
        values are deliberately absent: no endpoint returns them, and an endpoint that did would
        need a hand-written allowlist of non-secret keys rather than a redacted env dump. The 24h
        buckets are windowed on jobs.updated_at, a finish-time proxy rather than a real finish
        timestamp, so treat them as indicative and not as an audit source. FAILED OR CANCELLED
        counts both statuses. All worker buckets, including TOTAL, exclude revoked workers. The
        health pill reflects HTTP reachability only - GET /v1/health does not check the database,
        so HEALTHY means the listener answered and nothing more.
      </div>
    </div>
  )
}
