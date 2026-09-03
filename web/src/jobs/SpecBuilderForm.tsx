import { memo, useCallback, useMemo, type Dispatch, type SetStateAction } from 'react'
import { PillButton } from '../components/holo'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import { KeyValueRepeater } from './KeyValueRepeater'
import { TaskRowFields, taskLabel, type DepOption } from './TaskRowFields'
import { newTaskRow, type BuilderState, type Priority, type TaskRow } from './specBuilder'
import { useFocusAfterUpdate } from './useFocusAfterUpdate'

interface SpecBuilderFormProps {
  state: BuilderState
  onChange: Dispatch<SetStateAction<BuilderState>>
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

  // Precomputed once per render and cached across renders that leave every
  // task's id, position and name untouched. An edit to timeout, retries, env,
  // requires or commands changes none of them, so this key (and so
  // depOptions itself) stays the SAME reference, which is what lets
  // MemoTaskRowFields bail out for every row but the one actually edited. A
  // rename does change it, on purpose: every row's dependency picker can be
  // showing the renamed task as an option.
  //
  // id is in the key for a reason position and name alone do not cover: a
  // remove followed by an add at the same position can leave position and
  // name unchanged (both blank), while the row underneath is a different one
  // entirely. Without id, depOptions would keep the removed row's id at that
  // position instead of picking up the new row's.
  const depKey = state.tasks.map((t, i) => `${t.id}:${i}:${t.name}`).join('|')
  const depOptions = useMemo<DepOption[]>(
    () => state.tasks.map((t, i) => ({ id: t.id, label: taskLabel(t, i) })),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [depKey],
  )

  // One function serves every row: the row's id travels as an ARGUMENT rather
  // than being baked into a fresh closure per row per render (the array `.map`
  // below used to do exactly that), which is what keeps this reference
  // identical across a keystroke in a DIFFERENT row - the precondition for
  // MemoTaskRowFields to skip that row entirely. The functional updater form
  // reads the state React is actually about to commit rather than a snapshot
  // taken at call time, so two dispatches to two different rows inside one
  // batching window both apply instead of the second silently overwriting
  // the first.
  const updateTask = useCallback(
    (id: string, next: TaskRow) => {
      onChange((prev) => ({ ...prev, tasks: prev.tasks.map((t) => (t.id === id ? next : t)) }))
    },
    [onChange],
  )

  const removeTaskById = useCallback(
    (id: string) => {
      // depOptions, not the state prop: it is what the row asking for its own
      // removal was rendered against, and it holds everything the side
      // effects below need (the removed row's label, its neighbor's id) with
      // no separate snapshot of state to keep in sync.
      const i = depOptions.findIndex((o) => o.id === id)
      if (i === -1) return
      const label = depOptions[i].label
      onChange((prev) => ({
        ...prev,
        // Every dependent's selection is pruned with the row, so no reference
        // outlives its target.
        tasks: prev.tasks
          .filter((t) => t.id !== id)
          .map((t) => ({ ...t, dependsOn: t.dependsOn.filter((d) => d !== id) })),
      }))
      announce(`${label} removed`)
      const next = depOptions[i + 1] ?? depOptions[i - 1]
      focusAfterUpdate(next === undefined ? 'add-task' : `task-${next.id}-remove`)
    },
    [depOptions, onChange, announce, focusAfterUpdate],
  )

  const setLabels = useCallback(
    (labels: BuilderState['labels']) => {
      onChange((prev) => ({ ...prev, labels }))
    },
    [onChange],
  )

  function addTask() {
    const task = newTaskRow()
    onChange((prev) => ({ ...prev, tasks: [...prev.tasks, task] }))
    announce(`Task ${state.tasks.length + 1} added`)
    focusAfterUpdate(`task-${task.id}-name`)
  }

  return (
    <div className="flex flex-col gap-3">
      <Field label="Job name" htmlFor="job-name">
        <Input
          id="job-name"
          value={state.name}
          onChange={(e) => {
            const name = e.target.value
            onChange((prev) => ({ ...prev, name }))
          }}
        />
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
              onClick={() => onChange((prev) => ({ ...prev, priority: (prev.priority === p ? '' : p) as Priority }))}
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
