import { memo, useCallback } from 'react'
import { GlassPanel, PillButton } from '../components/holo'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { CommandsRepeater } from './CommandsRepeater'
import { KeyValueRepeater } from './KeyValueRepeater'
import type { KvRow, TaskRow } from './specBuilder'

// One entry per task, precomputed once by the caller from the whole list, so
// that a row unrelated to an edit gets the SAME array back across renders
// (see SpecBuilderForm) - a `TaskRow[]` prop containing the caller's live
// objects would carry a fresh reference on every keystroke to ANY task,
// defeating memoization on every other row for no reason this row cares about.
export interface DepOption {
  id: string
  label: string
}

// Wrapped here rather than inside their own modules, so each stays a plain
// function its own tests can call or spy on directly.
const MemoCommandsRepeater = memo(CommandsRepeater)
const MemoKeyValueRepeater = memo(KeyValueRepeater)

interface TaskRowFieldsProps {
  task: TaskRow
  index: number
  depOptions: DepOption[]
  // Dispatched WITH the row's id, so one stable function serves every row -
  // see the identical note on SpecBuilderForm's updateTask/removeTaskById.
  // The updater form lets a field's own stable callback below build on the
  // task React is about to commit rather than closing over this render's
  // `task`, which is what keeps THAT callback stable across every render of
  // this row, not only across other rows.
  onChange: (id: string, next: TaskRow | ((prev: TaskRow) => TaskRow)) => void
  onRemove: (id: string) => void
  announce: (message: string) => void
}

// The row's accessible name, and the noun its remove control is named with. A
// bare glyph makes a button list unnavigable, and a positional fallback keeps two
// unnamed rows distinguishable. The index is folded into the NAMED form too,
// not only the blank one: two tasks can share a name (the duplicate-name
// battery's own fixture does exactly this), and the server - not this form -
// is what decides whether that is allowed. N is the row's current position,
// recomputed on every render from the array index; it is never stored on the
// row and never appears in a DOM id, both of which would go stale across an
// add or a remove elsewhere in the list.
export function taskLabel(task: TaskRow, index: number): string {
  return task.name === '' ? `Task ${index + 1}` : `Task ${index + 1}: ${task.name}`
}

export function TaskRowFields({ task, index, depOptions, onChange, onRemove, announce }: TaskRowFieldsProps) {
  const label = taskLabel(task, index)
  const removeName = task.name === '' ? `Remove task ${index + 1}` : `Remove task ${index + 1}: ${task.name}`
  const others = depOptions.filter((o) => o.id !== task.id)

  // Stable across every render of THIS row, not only across other rows: each
  // depends on task.id (fixed for the row's lifetime) and onChange (stable
  // from SpecBuilderForm), never on the task object itself - depending on
  // `task` directly would recreate all three on every keystroke to any field.
  // Stability alone is not enough for MemoCommandsRepeater and
  // MemoKeyValueRepeater to bail on a sibling-field edit: both also receive
  // NARROWED props below (commands/multiCommand, rows), not the whole task,
  // since the whole task is itself a fresh reference on every field edit.
  const onCommandsChange = useCallback(
    (commands: TaskRow['commands'], multiCommand: boolean) =>
      onChange(task.id, (prev) => ({ ...prev, commands, multiCommand })),
    [task.id, onChange],
  )
  const onEnvChange = useCallback(
    (env: KvRow[]) => onChange(task.id, (prev) => ({ ...prev, env })),
    [task.id, onChange],
  )
  const onRequiresChange = useCallback(
    (requires: KvRow[]) => onChange(task.id, (prev) => ({ ...prev, requires })),
    [task.id, onChange],
  )

  function toggleDep(id: string) {
    const next = task.dependsOn.includes(id)
      ? task.dependsOn.filter((d) => d !== id)
      : [...task.dependsOn, id]
    onChange(task.id, { ...task, dependsOn: next })
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
              onChange={(e) => onChange(task.id, { ...task, name: e.target.value })}
            />
          </Field>
        </div>
        <PillButton id={`task-${task.id}-remove`} aria-label={removeName} onClick={() => onRemove(task.id)}>
          Remove task
        </PillButton>
      </div>

      <MemoCommandsRepeater
        taskId={task.id}
        commands={task.commands}
        multiCommand={task.multiCommand}
        onChange={onCommandsChange}
        announce={announce}
      />

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
              onChange={(e) => onChange(task.id, { ...task, timeout: e.target.value })}
            />
          </Field>
        </div>
        <div className="min-w-[9rem] flex-1">
          <Field label="Retries" htmlFor={`task-${task.id}-retries`}>
            <Input
              id={`task-${task.id}-retries`}
              inputMode="numeric"
              value={task.retries}
              onChange={(e) => onChange(task.id, { ...task, retries: e.target.value })}
            />
          </Field>
        </div>
      </div>

      <MemoKeyValueRepeater
        idPrefix={`task-${task.id}-env`}
        groupLabel="Environment variables"
        itemNoun="environment variable"
        rows={task.env}
        onChange={onEnvChange}
        announce={announce}
      />

      <MemoKeyValueRepeater
        idPrefix={`task-${task.id}-requires`}
        groupLabel="Requires"
        itemNoun="requirement"
        rows={task.requires}
        onChange={onRequiresChange}
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
                {o.label}
              </button>
            ))}
          </div>
        </div>
      ) : null}
    </GlassPanel>
  )
}
