import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Button } from '../components/Button'
import { useCreateJob } from './useCreateJob'
import { STARTER_TEMPLATE, validateSpecText } from './specTemplate'

// Dedicated /jobs/new page: a JSON job-spec editor that POSTs to /v1/jobs and,
// on 201, navigates to the created job's detail page. Creation is auth-only, so
// this is available to every logged-in user (no admin gate).
export function NewJobPage() {
  const navigate = useNavigate()
  const create = useCreateJob()
  const [text, setText] = useState(STARTER_TEMPLATE)
  // Client-side parse/shape error. Server errors come from create.error.
  const [clientError, setClientError] = useState<string | null>(null)

  function onSubmit() {
    // Clear a stale server error before re-validating (matches JobActions).
    create.reset()
    setClientError(null)

    const check = validateSpecText(text)
    if (!check.ok) {
      setClientError(check.error)
      return
    }
    create.mutate(check.value, {
      onSuccess: (job) => navigate(`/jobs/${job.id}`),
    })
  }

  // One banner slot for both sources; client error takes precedence since it is
  // set on the current submit and a stale server error was just reset.
  const bannerMessage = clientError ?? (create.error as Error | null)?.message ?? null

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <Link to="/jobs" className="font-mono text-[11px] text-fg-mute hover:text-fg">
          &larr; Jobs
        </Link>
        <h1 className="text-[28px] font-normal tracking-tight">New job</h1>
        <p className="font-mono text-[11px] text-fg-mute">
          Author a job-spec as JSON (the same shape <code>relay submit</code> accepts).
          Fields: name, priority, labels, tasks[] (name + command/commands, env,
          requires, timeout_seconds, retries, depends_on, source).
        </p>
      </div>

      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        spellCheck={false}
        aria-label="Job spec JSON"
        className="min-h-[360px] w-full rounded-card border border-border bg-white/5 p-3 font-mono text-[12px] text-fg"
      />

      {bannerMessage ? (
        <div role="alert" className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {bannerMessage}
        </div>
      ) : null}

      <div>
        <Button className="w-auto px-4" onClick={onSubmit} disabled={create.isPending}>
          Create job
        </Button>
      </div>
    </div>
  )
}
