import { GlassPanel, PillButton } from '../components/holo'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { CommandsRepeater } from './CommandsRepeater'
import { KeyValueRepeater } from './KeyValueRepeater'
import type { TaskRow } from './specBuilder'

interface TaskRowFieldsProps {
  task: TaskRow
  index: number
  // Every task, so the dependency picker can offer the others by their CURRENT
  // name. The picker stores ids, never names.
  allTasks: TaskRow[]
  onChange: (next: TaskRow) => void
  onRemove: () => void
  announce: (message: string) => void
}

// The row's accessible name, and the noun its remove control is named with. A
// bare glyph makes a button list unnavigable, and a positional fallback keeps two
// unnamed rows distinguishable.
export function taskLabel(task: TaskRow, index: number): string {
  return task.name === '' ? `Task ${index + 1}` : task.name
}

export function TaskRowFields({ task, index, allTasks, onChange, onRemove, announce }: TaskRowFieldsProps) {
  const label = taskLabel(task, index)
  const removeName = task.name === '' ? `Remove task ${index + 1}` : `Remove task ${task.name}`
  const others = allTasks.filter((t) => t.id !== task.id)

  function toggleDep(id: string) {
    const next = task.dependsOn.includes(id)
      ? task.dependsOn.filter((d) => d !== id)
      : [...task.dependsOn, id]
    onChange({ ...task, dependsOn: next })
  }

  return (
    <GlassPanel role="group" aria-label={label} className="flex flex-col gap-2 p-3">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div className="min-w-[12rem] flex-1">
          <Field label="Task name" htmlFor={`task-${task.id}-name`}>
            <Input
              id={`task-${task.id}-name`}
              value={task.name}
              spellCheck={false}
              onChange={(e) => onChange({ ...task, name: e.target.value })}
            />
          </Field>
        </div>
        <PillButton id={`task-${task.id}-remove`} aria-label={removeName} onClick={onRemove}>
          Remove task
        </PillButton>
      </div>

      <CommandsRepeater task={task} onChange={onChange} announce={announce} />

      <div className="flex flex-wrap gap-2">
        {/* Plain text inputs. No min, no max, no step, no maxlength and no number
            type: every one of those would be a copy of a bound jobspec.Validate
            owns, and a copy makes this refuse a spec the server accepts on the
            first release that moves it. inputMode is a keyboard hint and
            constrains nothing. */}
        <div className="min-w-[9rem] flex-1">
          <Field label="Timeout seconds" htmlFor={`task-${task.id}-timeout`}>
            <Input
              id={`task-${task.id}-timeout`}
              inputMode="numeric"
              value={task.timeout}
              onChange={(e) => onChange({ ...task, timeout: e.target.value })}
            />
          </Field>
        </div>
        <div className="min-w-[9rem] flex-1">
          <Field label="Retries" htmlFor={`task-${task.id}-retries`}>
            <Input
              id={`task-${task.id}-retries`}
              inputMode="numeric"
              value={task.retries}
              onChange={(e) => onChange({ ...task, retries: e.target.value })}
            />
          </Field>
        </div>
      </div>

      <KeyValueRepeater
        idPrefix={`task-${task.id}-env`}
        groupLabel="Environment variables"
        itemNoun="environment variable"
        rows={task.env}
        onChange={(env) => onChange({ ...task, env })}
        announce={announce}
      />

      <KeyValueRepeater
        idPrefix={`task-${task.id}-requires`}
        groupLabel="Requires"
        itemNoun="requirement"
        rows={task.requires}
        onChange={(requires) => onChange({ ...task, requires })}
        announce={announce}
      />

      {others.length > 0 ? (
        <div className="flex flex-col gap-1.5">
          <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">Depends on</span>
          {/* Inline toggles, not a popover: a floating overlay would be a second
              modal surface, and DialogShell owns those. A task is not offered as
              its own dependency - an affordance, not a rule, since a self-edge
              reaches the server's own cycle detector. */}
          <div role="group" aria-label="Depends on" className="flex flex-wrap gap-1.5">
            {others.map((o) => (
              <button
                key={o.id}
                type="button"
                aria-pressed={task.dependsOn.includes(o.id)}
                onClick={() => toggleDep(o.id)}
                className={`rounded-md border px-2.5 py-1 font-mono text-[11px] ${
                  task.dependsOn.includes(o.id)
                    ? 'border-accent/50 bg-accent/15 text-fg'
                    : 'border-border bg-white/5 text-fg-mute'
                }`}
              >
                {taskLabel(o, allTasks.indexOf(o))}
              </button>
            ))}
          </div>
        </div>
      ) : null}
    </GlassPanel>
  )
}
