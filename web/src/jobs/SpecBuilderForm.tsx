import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { KeyValueRepeater } from './KeyValueRepeater'
import type { BuilderState, Priority } from './specBuilder'

interface SpecBuilderFormProps {
  state: BuilderState
  onChange: (next: BuilderState) => void
  // The page owns the single polite live region; every repeater in this subtree
  // routes its announcements through here so two of them never race in two
  // regions.
  announce: (message: string) => void
}

// No Field in this subtree is ever given an `error` prop. The server answers
// with one top-level string and no field map, so binding a message to a control
// would mean matching its text - a coupling whose failure mode is silent.
export function SpecBuilderForm({ state, onChange, announce }: SpecBuilderFormProps) {
  return (
    <div className="flex flex-col gap-3">
      <Field label="Job name" htmlFor="job-name">
        <Input id="job-name" value={state.name} onChange={(e) => onChange({ ...state, name: e.target.value })} />
      </Field>

      <div className="flex flex-col gap-1.5">
        <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">Priority</span>
        {/* The server's set is closed and this renders exactly it. Clicking the
            pressed value clears it, which is the fourth state (unset) and emits
            no key. */}
        <div role="group" aria-label="Priority" className="flex flex-wrap gap-1.5">
          {(['low', 'normal', 'high'] as const).map((p) => (
            <button
              key={p}
              type="button"
              aria-pressed={state.priority === p}
              onClick={() => onChange({ ...state, priority: (state.priority === p ? '' : p) as Priority })}
              className={`rounded-md border px-2.5 py-1 font-mono text-[11px] ${
                state.priority === p ? 'border-accent/50 bg-accent/15 text-fg' : 'border-border bg-white/5 text-fg-mute'
              }`}
            >
              {p}
            </button>
          ))}
        </div>
      </div>

      <KeyValueRepeater
        idPrefix="job-labels"
        groupLabel="Labels"
        itemNoun="label"
        rows={state.labels}
        onChange={(labels) => onChange({ ...state, labels })}
        announce={announce}
      />
    </div>
  )
}
