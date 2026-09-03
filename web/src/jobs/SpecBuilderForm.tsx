import { memo, useCallback, useLayoutEffect, useMemo, useRef } from 'react'
import { PillButton } from '../components/holo'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { KeyValueRepeater } from './KeyValueRepeater'
import { TaskRowFields, taskLabel, type DepOption } from './TaskRowFields'
import { newTaskRow, type BuilderState, type Priority, type TaskRow } from './specBuilder'
import { useFocusAfterUpdate } from './useFocusAfterUpdate'

interface SpecBuilderFormProps {
  state: BuilderState
  onChange: (next: BuilderState) => void
  // The page owns the single polite live region; every repeater in this subtree
  // routes its announcements through here so two of them never race in two
  // regions.
  announce: (message: string) => void
}

// Memoized so a keystroke in one row leaves the other rows' own subtree
// (their CommandsRepeater, their two KeyValueRepeaters) untouched by React -
// see updateTask and removeTaskById below for the callback half of this, and
// depOptions for why a rename is the one edit that still fans out.
const MemoTaskRowFields = memo(TaskRowFields)

// No Field in this subtree is ever given an `error` prop. The server answers
// with one top-level string and no field map, so binding a message to a control
// would mean matching its text - a coupling whose failure mode is silent.
export function SpecBuilderForm({ state, onChange, announce }: SpecBuilderFormProps) {
  const focusAfterUpdate = useFocusAfterUpdate()

  // Always the latest state, read only inside the stable callbacks below -
  // never a render output or a hook dependency, so writing it cannot make a
  // callback's own identity change. useLayoutEffect (not a plain assignment
  // during render) keeps the write off the render pass itself: it lands after
  // DOM mutations and before the browser paints, ahead of any real click or
  // keystroke a stable callback could be reached by.
  const stateRef = useRef(state)
  useLayoutEffect(() => {
    stateRef.current = state
  })

  // One function serves every row: the row's id travels as an ARGUMENT rather
  // than being baked into a fresh closure per row per render (the array `.map`
  // below used to do exactly that), which is what keeps this reference
  // identical across a keystroke in a DIFFERENT row - the precondition for
  // MemoTaskRowFields to skip that row entirely.
  const updateTask = useCallback(
    (id: string, next: TaskRow) => {
      const current = stateRef.current
      onChange({ ...current, tasks: current.tasks.map((t) => (t.id === id ? next : t)) })
    },
    [onChange],
  )

  const removeTaskById = useCallback(
    (id: string) => {
      const current = stateRef.current
      const i = current.tasks.findIndex((t) => t.id === id)
      if (i === -1) return
      const gone = current.tasks[i]
      onChange({
        ...current,
        // Every dependent's selection is pruned with the row, so no reference
        // outlives its target.
        tasks: current.tasks
          .filter((t) => t.id !== id)
          .map((t) => ({ ...t, dependsOn: t.dependsOn.filter((d) => d !== id) })),
      })
      announce(`${taskLabel(gone, i)} removed`)
      const next = current.tasks[i + 1] ?? current.tasks[i - 1]
      focusAfterUpdate(next === undefined ? 'add-task' : `task-${next.id}-remove`)
    },
    [onChange, announce, focusAfterUpdate],
  )

  const setLabels = useCallback(
    (labels: BuilderState['labels']) => {
      onChange({ ...stateRef.current, labels })
    },
    [onChange],
  )

  function addTask() {
    const task = newTaskRow()
    onChange({ ...state, tasks: [...state.tasks, task] })
    announce(`Task ${state.tasks.length + 1} added`)
    focusAfterUpdate(`task-${task.id}-name`)
  }

  // Precomputed once per render and cached across renders that leave every
  // task's id, position and name untouched - the only three inputs taskLabel
  // reads. An edit to timeout, retries, env, requires or commands changes
  // none of them, so this key (and so depOptions itself) stays the SAME
  // reference, which is what lets MemoTaskRowFields bail out for every row
  // but the one actually edited. A rename does change it, on purpose: every
  // row's dependency picker can be showing the renamed task as an option.
  const depKey = state.tasks.map((t, i) => `${t.id}:${i}:${t.name}`).join('|')
  const depOptions = useMemo<DepOption[]>(
    () => state.tasks.map((t, i) => ({ id: t.id, label: taskLabel(t, i) })),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [depKey],
  )

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
        onChange={setLabels}
        announce={announce}
      />

      <div className="flex flex-col gap-2">
        <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">Tasks</span>
        {state.tasks.map((task, i) => (
          <MemoTaskRowFields
            key={task.id}
            task={task}
            index={i}
            depOptions={depOptions}
            announce={announce}
            onChange={updateTask}
            onRemove={removeTaskById}
          />
        ))}
        <div>
          <PillButton id="add-task" onClick={addTask}>
            Add task
          </PillButton>
        </div>
      </div>
    </div>
  )
}
