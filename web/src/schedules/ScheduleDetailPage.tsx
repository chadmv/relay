import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Button } from '../components/Button'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { Chip, GlassPanel, Panel, PillButton } from '../components/holo'
import { ApiError } from '../lib/api'
import { useNow } from '../lib/useNow'
import { formatStarted } from '../jobs/status'
import type { SchedulePatch } from './api'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'
import { ScheduleRunsPanel } from './ScheduleRunsPanel'
import { ScheduleTriggerForm } from './ScheduleTriggerForm'
import { useSchedule } from './useSchedule'
import { useScheduleActions } from './useScheduleActions'
import { useScheduleRuns } from './useScheduleRuns'

export function ScheduleDetailPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { data: schedule, error, isLoading, refetch } = useSchedule(id)
  const { data: runs } = useScheduleRuns(id)
  const { runNow, setEnabled, update, remove } = useScheduleActions()
  const [confirmDelete, setConfirmDelete] = useState(false)
  // Local 1s clock so the relative countdown stays fresh between 10s polls. It issues
  // NO request (lib/useNow.ts:8-15). SchedulesPage rolls its own setTick
  // (SchedulesPage.tsx:43-47); the shared hook is used here rather than adding a
  // second local-timer idiom to the codebase.
  const now = useNow(1000)

  // THIRD CONSUMER of this triad, shipped deliberately. The identical block lives in
  // web/src/workers/WorkerDetailPage.tsx:30-55 and web/src/jobs/JobDetailPage.tsx:57-78.
  // Extracting a shared primitive would have to migrate both shipped pages behind a
  // byte-identical-test refactor gate, which is its own slice.
  // Enabler: idea-2026-08-12-detail-page-state-triad-primitive (to be filed).
  if (isLoading && !schedule) {
    return <GlassPanel className="h-40" />
  }

  if (error && !schedule) {
    // ownedScheduledJob 404s a non-owner non-admin exactly as it 404s a missing row
    // (internal/api/scheduled_jobs.go:147-169): the resource is hidden, not refused.
    // That server check is the ENTIRE access-control story - the SPA adds no gate and
    // must not. A 404 is not transient, so it gets no Retry.
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        {notFound ? (
          <div className="text-[13px] text-fg-mute">Schedule not found.</div>
        ) : (
          <>
            <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => refetch()}>
              Retry
            </Button>
          </>
        )}
        <div className="mt-4">
          <Link to="/schedules" className="font-mono text-[11px] text-accent">
            &larr; Schedules
          </Link>
        </div>
      </GlassPanel>
    )
  }

  if (!schedule) return null

  const busy = runNow.isPending || setEnabled.isPending || remove.isPending
  const actionError = (runNow.error ?? setEnabled.error ?? remove.error) as Error | null

  return (
    <div className="flex flex-col gap-4">
      {/* Breadcrumb + name + state pill + right-aligned action bar. */}
      <div className="flex items-center gap-2.5">
        <Link to="/schedules" className="text-[12px] text-fg-mute hover:text-fg">
          &larr; Schedules
        </Link>
        <span className="text-fg-dim">/</span>
        <span className="font-mono text-[14px] tracking-[0.04em] text-fg">{schedule.name}</span>
        <Chip tone={schedule.enabled ? 'accent' : 'muted'}>{schedule.enabled ? 'ENABLED' : 'PAUSED'}</Chip>
        <div className="ml-auto flex gap-2">
          {/* All three are owner-or-admin server-side, including run-now
              (internal/api/scheduled_jobs.go:642), contrary to the hi-fi's
              admin-only footnote. No client-side role gate. */}
          <PillButton onClick={() => runNow.mutate(id)} disabled={busy}>
            Run now
          </PillButton>
          <PillButton onClick={() => setEnabled.mutate({ id, enabled: !schedule.enabled })} disabled={busy}>
            {schedule.enabled ? 'Disable' : 'Enable'}
          </PillButton>
          <PillButton variant="danger" onClick={() => setConfirmDelete(true)} disabled={busy}>
            Delete
          </PillButton>
        </div>
      </div>

      {/* Identity sub-line. The OWNER is deliberately conditional and therefore today
          always absent: GET /v1/scheduled-jobs/{id} never calls fillOwnerEmails
          (internal/api/scheduled_jobs.go:508-519, unlike both list arms at :371 and
          :504) and owner_email has no omitempty (:25), so it is always "". Falling
          back to owner_id would render 36 opaque characters, and carrying the value
          over from the cached list row would make a deep link behave differently from
          a click-through.
          Enabler: bug-2026-08-12-scheduled-job-detail-missing-owner-email (to be filed). */}
      <div className="font-mono text-[11px] tracking-[0.04em] text-fg-mute">
        {schedule.owner_email ? (
          <>
            owner <span className="text-fg">{schedule.owner_email}</span> ·{' '}
          </>
        ) : null}
        created <span className="text-fg">{formatStarted(schedule.created_at)}</span> · updated{' '}
        <span className="text-fg">{formatRelativeTime(schedule.updated_at)}</span> · next fire{' '}
        <span className="text-fg">
          {schedule.enabled ? nextRunDisplay(schedule.next_run_at, now) : '-'}
        </span>{' '}
        · last run{' '}
        <span className="text-fg">
          {schedule.last_run_at ? formatRelativeTime(schedule.last_run_at) : '-'}
        </span>
        {schedule.last_job_id ? (
          <>
            {' '}
            · last job{' '}
            <Link to={`/jobs/${schedule.last_job_id}`} className="text-accent">
              {shortId(schedule.last_job_id)}
            </Link>
          </>
        ) : null}
      </div>

      {actionError ? (
        <div role="alert" className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-3">
          <Panel title="Trigger" meta="PATCH /v1/scheduled-jobs">
            <ScheduleTriggerForm
              schedule={schedule}
              pending={update.isPending}
              error={(update.error as Error | null)?.message}
              onSubmit={(patch: SchedulePatch) => {
                // Clear a stale server error before re-submitting, matching the
                // NewJobPage/JobActions convention.
                update.reset()
                // The patch is already a diff; it is forwarded verbatim. The settled
                // mutation writes nothing back into the form's draft state - the
                // fresh row arrives through the invalidated refetch.
                update.mutate({ id, patch })
              }}
            />
          </Panel>

          {/* Read-only. The stored value is JSON (scheduledJobResponse.JobSpec is a
              json.RawMessage, internal/api/scheduled_jobs.go:26); web/ has no YAML
              serializer, and the app's only spec editor is already a JSON textarea
              (jobs/NewJobPage.tsx:51-59). Rendered as a React TEXT CHILD in a <pre>:
              never dangerouslySetInnerHTML, and nothing from job_spec goes into a
              URL, a title attribute or a log line - it can carry env values a user
              chose to store.
              Enabler: idea-2026-08-12-schedule-job-spec-editor (to be filed). */}
          <Panel title="Job spec" meta="READ-ONLY">
            <pre className="max-h-[360px] overflow-auto px-4 py-3 font-mono text-[11px] leading-relaxed text-fg-mute">
              {JSON.stringify(schedule.job_spec, null, 2)}
            </pre>
          </Panel>
        </div>

        <div className="flex flex-col gap-3">
          {/* ONE entry, the server's own next_run_at. The hi-fi previews five
              (hifi3-holo-pages.jsx:1814-1828), which needs a cron parser: web/ has
              none (package.json:13-20), so a preview would be a second implementation
              of @every / @hourly / IANA-zone semantics that has to agree with
              robfig/cron/v3 (internal/schedrunner/cron.go:14-16), and a preview that
              silently disagrees is worse than one honest value. This is not a
              degraded placeholder: PATCH recomputes next_run_at server-side and
              returns it (scheduled_jobs.go:585-596), so after a cron save this shows
              the authoritative first fire of the edit just made.
              Enabler: idea-2026-08-12-schedule-next-fires-preview (to be filed). */}
          <Panel title="Next fire" meta="NEXT_RUN_AT">
            {schedule.enabled ? (
              <div className="flex flex-col gap-1 px-4 py-3">
                <span data-testid="next-fire-rel" className="font-mono text-[13px] text-fg">
                  {nextRunDisplay(schedule.next_run_at, now)}
                </span>
                <span data-testid="next-fire-abs" className="font-mono text-[11px] text-fg-mute">
                  {formatStarted(schedule.next_run_at)}
                </span>
              </div>
            ) : (
              <div className="px-4 py-3 font-mono text-[11px] text-fg-dim">paused - no fires queued</div>
            )}
          </Panel>

          <ScheduleRunsPanel runs={runs?.items ?? []} total={runs?.total ?? 0} />
        </div>
      </div>

      {/* ConfirmDialog is reused UNMODIFIED; it composes DialogShell/dialogStack,
          which own the portal, focus handling, scroll lock and scoped Escape. Do not
          hand-roll a modal here. */}
      {confirmDelete && (
        <ConfirmDialog
          title="Delete schedule"
          body={`Delete "${schedule.name}"? Jobs it already produced are kept, but they are unlinked from this schedule, so its run history becomes unreachable. A run already in flight is not cancelled. This cannot be undone.`}
          confirmLabel="Delete"
          destructive
          onCancel={() => setConfirmDelete(false)}
          onConfirm={() => {
            setConfirmDelete(false)
            remove.mutate(id, { onSuccess: () => navigate('/schedules') })
          }}
        />
      )}
    </div>
  )
}
