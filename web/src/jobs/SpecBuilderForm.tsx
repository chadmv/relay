import { Field } from '../components/Field'
import { Input } from '../components/Input'
import type { BuilderState } from './specBuilder'

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
export function SpecBuilderForm({ state, onChange }: SpecBuilderFormProps) {
  return (
    <div className="flex flex-col gap-3">
      <Field label="Job name" htmlFor="job-name">
        <Input id="job-name" value={state.name} onChange={(e) => onChange({ ...state, name: e.target.value })} />
      </Field>
    </div>
  )
}
