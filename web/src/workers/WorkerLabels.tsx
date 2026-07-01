import { useState, type KeyboardEvent } from 'react'
import { useAuth } from '../auth/AuthProvider'
import { Chip } from '../components/holo'
import { Input } from '../components/Input'
import { useWorkerActions } from './useWorkerActions'
import type { Worker } from './api'

// Parses "key=value" (split on the first "=") or a bare "key" (empty value).
// Trims the key; returns null when the trimmed key is empty (caller no-ops).
function parseLabelInput(text: string): { key: string; value: string } | null {
  const eq = text.indexOf('=')
  const rawKey = eq === -1 ? text : text.slice(0, eq)
  const value = eq === -1 ? '' : text.slice(eq + 1)
  const key = rawKey.trim()
  if (!key) return null
  return { key, value }
}

// Inline label manager for the worker-detail Labels panel. Admins can remove a
// label from its chip and add new ones via an inline "+ add label" input; both
// paths PATCH the full label map (the server replaces the whole map, not a
// per-key merge). Non-admins see read-only chips.
export function WorkerLabels({ worker }: { worker: Worker }) {
  const { user } = useAuth()
  const isAdmin = Boolean(user?.is_admin)
  const { update } = useWorkerActions(worker.id)
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState('')

  const labels = worker.labels ?? {}
  const entries = Object.entries(labels)

  function submitPatch(nextLabels: Record<string, string>) {
    update.mutate({ labels: nextLabels })
  }

  function removeLabel(key: string) {
    const next = { ...labels }
    delete next[key]
    submitPatch(next)
  }

  function startAdding() {
    setText('')
    setAdding(true)
  }

  function cancelAdding() {
    setAdding(false)
    setText('')
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Escape') {
      cancelAdding()
      return
    }
    if (e.key !== 'Enter') return
    const parsed = parseLabelInput(text)
    if (!parsed) return
    submitPatch({ ...labels, [parsed.key]: parsed.value })
    setAdding(false)
    setText('')
  }

  return (
    <div className="flex flex-col gap-2 px-4 py-3">
      <div className="flex flex-wrap items-center gap-1.5">
        {entries.length === 0 && !isAdmin && (
          <span className="font-mono text-[11px] text-fg-dim">no labels</span>
        )}
        {entries.map(([key, value]) => (
          <span key={key} className="inline-flex items-center gap-1">
            <Chip tone="accent">{value ? `${key}=${value}` : key}</Chip>
            {isAdmin && (
              <button
                type="button"
                aria-label={`Remove ${key}`}
                onClick={() => removeLabel(key)}
                className="shrink-0 rounded-md border border-border px-1.5 py-0.5 text-[10px] text-fg-mute"
              >
                x
              </button>
            )}
          </span>
        ))}
        {isAdmin && !adding && (
          <Chip dashed onClick={startAdding}>
            + add label
          </Chip>
        )}
        {isAdmin && adding && (
          <Input
            autoFocus
            value={text}
            placeholder="key=value"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            onBlur={cancelAdding}
            className="w-40"
          />
        )}
      </div>

      {update.error ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {(update.error as Error).message}
        </div>
      ) : null}
    </div>
  )
}
