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
  // (SchedulesPage.tsx's setTick effect); the shared hook is used here rather than adding a
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

  // keepPreviousData means a detail-to-detail route transition (no unmount, only
  // useParams().id changing) renders the PREVIOUS schedule's row while the new one
  // is in flight: isLoading is false and schedule is non-null throughout, so none
  // of the checks above catch it. Every id-bearing control below - the action bar,
  // the trigger form (which seeds its draft from `schedule` exactly once) and the
  // delete confirm dialog (whose copy names `schedule.name`) - must not render
  // against a schedule whose id does not match the route until the fresh row
  // arrives, or a clean Save / Delete / Run now / Disable can act on the WRONG id.
  if (schedule.id !== id) {
    return <GlassPanel className="h-40" />
  }

  // Symmetric in both directions: a Save in flight disables the header actions and
  // a header action in flight disables Save, since there is no version column and
  // no 409 handling (api.ts:80-83) - a concurrent PATCH from either surface is
  // last-writer-wins, so only one of these mutations should ever be in flight at a
  // time.
  const busy = runNow.isPending || setEnabled.isPending || remove.isPending || update.isPending
  const actionError = (runNow.error ?? setEnabled.error ?? remove.error) as Error | null

  // A settled mutation error stays on its object until something resets it
  // (react-query does not clear it on its own). The `??` chain above shows the
  // FIRST non-null error, so a stale one from an earlier action can both keep the
  // banner up after a later action succeeds and hide a later action's own failure.
  // Reset the OTHER two action mutations before firing a new one, so the banner can
  // only ever reflect the action that actually just ran.
  function resetOtherActionErrors(current: 'runNow' | 'setEnabled' | 'remove') {
    if (current !== 'runNow') runNow.reset()
    if (current !== 'setEnabled') setEnabled.reset()
    if (current !== 'remove') remove.reset()
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Breadcrumb + name + state pill + right-aligned action bar. */}
      <div className="flex flex-wrap items-center gap-2.5">
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
          <PillButton
            onClick={() => {
              resetOtherActionErrors('runNow')
              runNow.mutate(id)
            }}
            disabled={busy}
          >
            Run now
          </PillButton>
          <PillButton
            onClick={() => {
              resetOtherActionErrors('setEnabled')
              setEnabled.mutate({ id, enabled: !schedule.enabled })
            }}
            disabled={busy}
          >
            {schedule.enabled ? 'Disable' : 'Enable'}
          </PillButton>
          <PillButton
            variant="danger"
            onClick={() => {
              resetOtherActionErrors('remove')
              setConfirmDelete(true)
            }}
            disabled={busy}
          >
            Delete
          </PillButton>
        </div>
      </div>

      {/* Identity sub-line. handleGetScheduledJob now populates owner_email through
          fillOwnerEmails, same as both list arms, so the owner normally renders. The
          line stays conditional because fillOwnerEmails is best-effort: it logs and
          leaves the field "" when the owner lookup fails, and owner_email carries no
          omitempty. Omitting the label beats rendering a blank one, and falling back
          to owner_id would put 36 opaque characters on the identity line. */}
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
        {/* WITHOUT SCROLLING is the whole point of putting it here. The line above
            already reads created / updated / next fire / last run, and a dead
            schedule's tell is that "last run" stopped moving while "next fire"
            kept going - a pair the reader has to interpret. "last failure 4
            minutes ago" beside "last run 22 days ago" is the sentence an
            operator understands immediately.

            GATED ON BOTH FIELDS, not on the timestamp alone. last_error and
            last_error_at are separate nullable columns, so both one-sided states
            are reachable, and this marker's only job is to point at the Last
            failure panel below - which renders on last_error. A marker with no
            panel to point at would say a failure happened and then refuse to say
            what it was. The truthiness test also keeps absent, "" and present
            apart: an empty last_error is not a failure worth pointing at.

            No id guard needed here beyond the one the page already does: the
            `schedule.id !== id` check above returns before this renders, so
            keepPreviousData cannot paint a previous schedule's failure under this
            route's name. */}
        {schedule.last_error && schedule.last_error_at ? (
          <>
            {' '}
            · last failure{' '}
            <span data-testid="last-failure-rel" className="text-err">
              {formatRelativeTime(schedule.last_error_at)}
            </span>
          </>
        ) : null}
      </div>

      {actionError ? (
        <div role="alert" className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {/* Narrow-viewport convention: multi-column bodies stack below `md`, matching
          admin/server/ServerTab.tsx. See
          docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md. */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <div className="flex flex-col gap-3">
          <Panel title="Trigger" meta="PATCH /v1/scheduled-jobs">
            <ScheduleTriggerForm
              schedule={schedule}
              pending={busy}
              error={(update.error as Error | null)?.message}
              onDismissError={() => update.reset()}
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
          {/* TOP OF THE COLUMN, and conditional, so a healthy schedule's layout is
              unchanged. The heading names the text's PROVENANCE because the text
              is operator-supplied: it is derived from the schedule's stored
              configuration - its job_spec, or its cron_expr and timezone for a
              `parse cron:` failure - and it may quote prose the schedule's owner
              chose, such as a task name interpolated verbatim. Other messages are
              fixed server text; the meta label names job_spec because that is the
              common case and the strip is a few words wide, and the point it makes
              - these are not relay's words - holds for the cron case too. So an
              admin reading someone else's schedule is reading potentially
              attacker-chosen prose.
              There is no counter here to inflate and nothing an owner gains by
              breaking their own schedule; the one real risk is display-layer
              impersonation, text crafted to read like relay's own chrome. So it
              renders as a React TEXT CHILD in a preformatted block, never through
              dangerouslySetInnerHTML, and it goes into no URL, no title
              attribute and no log line. Same rule as the Job spec panel beside it.

              A TRUTHINESS TEST, matching the list's chip: the server omits
              last_error entirely for a healthy schedule and never stores "", and
              opening a panel headed "Last failure" over an empty string would show
              a heading with no reason under it.

              The time line is separately conditional because last_error and
              last_error_at are separate nullable columns and nothing in the
              database forces them to move together; reading last_error_at
              unconditionally would render "Invalid Date".

              The remedy copy names Run now first because it returns the
              UNTRUNCATED message: the stored value is capped at 1 KB server-side
              and this is the only place that says so. */}
          {schedule.last_error ? (
            <Panel title="Last failure" meta="FROM THE STORED JOB SPEC">
              <div className="flex flex-col gap-2 px-4 py-3">
                <pre
                  data-testid="last-error-text"
                  className="max-h-[180px] overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed text-err"
                >
                  {schedule.last_error}
                </pre>
                {schedule.last_error_at ? (
                  <span data-testid="last-error-when" className="font-mono text-[11px] text-fg-mute">
                    {formatRelativeTime(schedule.last_error_at)} · {formatStarted(schedule.last_error_at)}
                  </span>
                ) : null}
                {/* THE REMEDY NAMES A COMMAND BECAUSE THIS SURFACE CANNOT PERFORM
                    IT. "Repair the spec" pointed at nothing a reader of this panel
                    could do: the Job spec panel below is read only and the SPA has
                    no spec editor, so the only reachable half of that sentence was
                    the destructive one. The CLI closed exactly this gap for itself
                    by adding `relay schedules update --spec`; naming that command
                    here is what makes the sentence actionable from a browser.

                    RUN NOW STAYS FIRST. It is the only route to the UNTRUNCATED
                    message - the stored value is capped at 1 KB - so the ordering
                    is load bearing. README's "A schedule that has stopped firing"
                    runbook carries the same three steps in the same order. */}
                <span data-testid="last-error-remedy" className="text-[11px] text-fg-mute">
                  The scheduler re-validates the stored job spec on every fire. Use Run now to re-check and
                  see the message in full. The Job spec panel here is read only, so repair the spec with{' '}
                  <code className="font-mono">relay schedules update &lt;id&gt; --spec FILE</code>, or disable
                  the schedule if it should not run.
                </span>
              </div>
            </Panel>
          ) : null}

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
            resetOtherActionErrors('remove')
            remove.mutate(id, { onSuccess: () => navigate('/schedules') })
          }}
        />
      )}
    </div>
  )
}
