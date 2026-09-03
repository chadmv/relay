import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { GlassPanel, Eyebrow, PillButton } from '../components/holo'
import { useCreateJob } from './useCreateJob'
import { STARTER_TEMPLATE, validateSpecText } from './specTemplate'
import { SpecBuilderForm } from './SpecBuilderForm'
import { fromSpec, newBuilderState, toSpec, type BuilderState } from './specBuilder'

type Mode = 'form' | 'json'

// Dedicated /jobs/new page. Two modes over one submit path: the form, which is
// what the page opens in, and the raw JSON editor, which stays the authority of
// last resort for any spec the form cannot type. Only ONE direction is
// automatic - the form always renders into the JSON text - because a form that
// silently drops what it cannot model is unreportable: the server ignores
// unknown keys, so nothing anywhere would say the key was lost.
export function NewJobPage() {
  const navigate = useNavigate()
  const create = useCreateJob()
  const [mode, setMode] = useState<Mode>('form')
  const [builder, setBuilder] = useState<BuilderState>(newBuilderState)
  const [text, setText] = useState(STARTER_TEMPLATE)
  // Client-side parse/shape/import error. Server errors come from create.error.
  const [clientError, setClientError] = useState<string | null>(null)
  const [announcement, setAnnouncement] = useState('')

  function toJson() {
    if (mode === 'json') return
    setText(JSON.stringify(toSpec(builder), null, 2))
    setClientError(null)
    create.reset()
    setMode('json')
  }

  function toForm() {
    if (mode === 'form') return
    create.reset()
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch (e) {
      setClientError(`Invalid JSON: ${(e as Error).message}`)
      return
    }
    const result = fromSpec(parsed)
    if (!result.ok) {
      setClientError(result.error)
      return
    }
    setBuilder(result.state)
    setClientError(null)
    setMode('form')
  }

  function onSubmit() {
    // Clear a stale server error before re-validating (matches JobActions).
    create.reset()
    setClientError(null)

    if (mode === 'form') {
      // No pre-check at all. The form owns the ENCODING, not the rules: every
      // semantic answer - a cycle, a duplicate name, a range, a missing command
      // - comes from the server and is rendered verbatim.
      create.mutate(toSpec(builder), { onSuccess: (job) => navigate(`/jobs/${job.id}`) })
      return
    }

    const check = validateSpecText(text)
    if (!check.ok) {
      setClientError(check.error)
      return
    }
    create.mutate(check.value, { onSuccess: (job) => navigate(`/jobs/${job.id}`) })
  }

  // One banner slot for all three sources; the client error takes precedence
  // since it is set on the current action and a stale server error was just
  // reset.
  const bannerMessage = clientError ?? (create.error as Error | null)?.message ?? null

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <Link to="/jobs" className="text-[12px] text-fg-mute hover:text-fg">
          &larr; Jobs
        </Link>
        <Eyebrow>NEW</Eyebrow>
        <h1 className="text-[28px] font-normal tracking-tight">New job</h1>
        <p className="font-mono text-[11px] text-fg-mute">
          Build a job-spec with the form, or edit it as JSON (the same shape <code>relay submit</code>{' '}
          accepts). Fields: name, priority, labels, tasks[] (name + command/commands, env, requires,
          timeout_seconds, retries, depends_on, source).
        </p>
      </div>

      <div role="group" aria-label="Editor mode" className="flex gap-1.5">
        {(['form', 'json'] as const).map((m) => (
          <button
            key={m}
            type="button"
            aria-pressed={mode === m}
            onClick={m === 'form' ? toForm : toJson}
            className={`rounded-md border px-2.5 py-1 font-mono text-[11px] ${
              mode === m ? 'border-accent/50 bg-accent/15 text-fg' : 'border-border bg-white/5 text-fg-mute'
            }`}
          >
            {m === 'form' ? 'Form' : 'JSON'}
          </button>
        ))}
      </div>

      {mode === 'form' ? (
        <>
          <GlassPanel className="p-3">
            <SpecBuilderForm state={builder} onChange={setBuilder} announce={setAnnouncement} />
          </GlassPanel>
          <GlassPanel className="p-3">
            <Eyebrow className="mb-1 text-[10px] tracking-[0.16em]">JSON PREVIEW</Eyebrow>
            {/* A text child, never markup: this renders exactly what will be
                posted, and a spec's env values are where somebody puts a token. */}
            <pre
              aria-label="Job spec preview"
              className="max-h-[320px] overflow-auto font-mono text-[11px] leading-relaxed text-fg-mute"
            >
              {JSON.stringify(toSpec(builder), null, 2)}
            </pre>
          </GlassPanel>
        </>
      ) : (
        <GlassPanel className="p-3">
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            spellCheck={false}
            aria-label="Job spec JSON"
            className="min-h-[360px] w-full resize-y bg-transparent font-mono text-[12px] text-fg outline-none"
          />
        </GlassPanel>
      )}

      {/* One polite region for the whole page, shared by the task repeater and
          every nested repeater, so two announcements cannot race. */}
      <div role="status" aria-live="polite" className="font-mono text-[11px] text-fg-mute">
        {announcement}
      </div>

      {bannerMessage ? (
        <div role="alert" className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {bannerMessage}
        </div>
      ) : null}

      <div>
        <PillButton variant="primary" onClick={onSubmit} disabled={create.isPending}>
          Create job
        </PillButton>
      </div>
    </div>
  )
}
