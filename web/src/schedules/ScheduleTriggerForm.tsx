import { useState } from 'react'
import { Field } from '../components/Field'
import { PillButton } from '../components/holo'
import { Input } from '../components/Input'
import type { Schedule, SchedulePatch } from './api'

// The two values the server accepts (internal/api/scheduled_jobs.go:561-564). The
// hi-fi offers a third, `queue` (hifi3-holo-pages.jsx:1773); it always 400s, so it
// is not rendered. A queueing overlap policy is a scheduler product decision, not a
// UI gap, so no enabler is filed for it.
const OVERLAP_OPTIONS = ['skip', 'allow'] as const

// A NON-CONSTRAINING suggestion list. The server accepts any name time.LoadLocation
// resolves (internal/schedrunner/cron.go:33-36), so a fixed <select> like the hi-fi's
// six-entry dropdown (hifi3-holo-pages.jsx:1766) would make an existing schedule's
// zone unselectable and silently rewrite it on the next save.
// Intl.supportedValuesOf('timeZone') is deliberately NOT used: it was not verified
// against this repo's jsdom version, and a convenience list is not worth a runtime
// risk in the test environment.
const COMMON_TIMEZONES = [
  'UTC',
  'America/Los_Angeles',
  'America/New_York',
  'Europe/London',
  'Europe/Berlin',
  'Asia/Tokyo',
  'Australia/Sydney',
]

interface ScheduleTriggerFormProps {
  schedule: Schedule
  pending: boolean
  error?: string
  onSubmit: (patch: SchedulePatch) => void
}

// Inline edit surface for cron / timezone / overlap policy, on the page rather than
// in a dialog: the hi-fi puts it inline (hifi3-holo-pages.jsx:1745-1791) and the
// shipped precedent for editing a resource from its detail page is WorkerEditForm
// (web/src/workers/WorkerEditForm.tsx). `name`, `enabled` and `job_spec` are also
// PATCH-able and are deliberately NOT here: enabled has its own header button, and
// the other two are out of scope for this slice.
export function ScheduleTriggerForm({ schedule, pending, error, onSubmit }: ScheduleTriggerFormProps) {
  // Seeded ONCE from the schedule and never re-derived on re-render. The page polls
  // every 10s; a refetch landing mid-edit must not overwrite what the user typed.
  // The draft is reset only by an explicit Cancel.
  const [cron, setCron] = useState(schedule.cron_expr)
  const [tz, setTz] = useState(schedule.timezone)
  const [overlap, setOverlap] = useState(schedule.overlap_policy)

  function submit(e: React.FormEvent) {
    e.preventDefault()
    // CHANGED FIELDS ONLY, compared against the currently loaded row - not against a
    // "has been edited" flag. Sending an unchanged cron_expr or timezone is NOT a
    // harmless no-op: internal/api/scheduled_jobs.go:585 recomputes next_run_at from
    // time.Now() whenever the body merely CARRIES either key, so a re-sent unchanged
    // cron on an `@every 1h` schedule pushes the next fire out by up to an hour.
    // Same construction as WorkerEditForm.tsx:42-45.
    //
    // Values are sent exactly as typed, untrimmed: the server does not trim, and
    // trimming here would silently alter the user's input.
    const patch: SchedulePatch = {}
    if (cron !== schedule.cron_expr) patch.cron_expr = cron
    if (tz !== schedule.timezone) patch.timezone = tz
    if (overlap !== schedule.overlap_policy) patch.overlap_policy = overlap

    // Nothing changed: issue no request at all rather than an empty PATCH.
    if (Object.keys(patch).length === 0) return

    // No client-side cron or timezone validation, by design. A pre-check would be a
    // second implementation of robfig/cron/v3's grammar and IANA zone resolution
    // (internal/schedrunner/cron.go:14-16, :33-36) that can disagree with the server.
    // The server is the validator of record and the caller renders its 400 verbatim.
    onSubmit(patch)
  }

  function cancel() {
    setCron(schedule.cron_expr)
    setTz(schedule.timezone)
    setOverlap(schedule.overlap_policy)
  }

  return (
    <form onSubmit={submit} className="px-4 py-3">
      <Field label="Cron expression" htmlFor="schedule-cron" hint="5-field cron, @hourly / @daily, or @every <duration>.">
        <Input
          id="schedule-cron"
          value={cron}
          spellCheck={false}
          onChange={(e) => setCron(e.target.value)}
          className="font-mono"
        />
      </Field>

      <Field label="Timezone" htmlFor="schedule-tz" hint="Any IANA zone name the server can resolve.">
        <Input
          id="schedule-tz"
          list="schedule-tz-options"
          value={tz}
          spellCheck={false}
          onChange={(e) => setTz(e.target.value)}
          className="font-mono"
        />
      </Field>
      <datalist id="schedule-tz-options">
        {COMMON_TIMEZONES.map((z) => (
          <option key={z} value={z} />
        ))}
      </datalist>

      <div className="mb-3">
        <span className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">
          Overlap policy
        </span>
        <div role="group" aria-label="Overlap policy" className="flex gap-1.5">
          {OVERLAP_OPTIONS.map((o) => (
            <button
              key={o}
              type="button"
              aria-pressed={overlap === o}
              onClick={() => setOverlap(o)}
              className={`rounded-md border px-2.5 py-1 font-mono text-[11px] ${
                overlap === o ? 'border-accent/50 bg-accent/15 text-fg' : 'border-border bg-white/5 text-fg-mute'
              }`}
            >
              {o}
            </button>
          ))}
        </div>
      </div>

      {/* The server's message, verbatim, INSIDE the form next to the control that
          produced it. An error routed to a page-level banner can end up rendered
          somewhere the user is not looking. */}
      {error ? (
        <div
          role="alert"
          className="mb-3 rounded-card border border-err/40 bg-err/10 px-3 py-2 font-mono text-[11px] leading-relaxed text-err"
        >
          {error}
        </div>
      ) : null}

      <div className="flex justify-end gap-2">
        <PillButton onClick={cancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Save changes
        </PillButton>
      </div>
    </form>
  )
}
