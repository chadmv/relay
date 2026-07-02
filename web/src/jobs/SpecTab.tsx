import type { TaskDetail } from './api'
import { Eyebrow, GlassPanel } from '../components/holo'

// Renders the selected task's spec: commands, env, requires. No per-task source
// block (handleGetJob does not echo `source`); the hi-fi image/runtime/cluster/
// source rows are also omitted - JobDetail/TaskDetail return none of them (mock
// inventions, not deferred features).
export function SpecTab({ task }: { task: TaskDetail | undefined }) {
  if (!task) {
    return <div className="p-4 text-[12px] text-fg-mute">Select a task to view its spec.</div>
  }
  // env/requires/commands are nullable on the wire (see TaskDetail); coerce to
  // empty so an omitted field renders "(none)" instead of throwing. DO NOT REMOVE
  // these guards - PR #96; a regression re-blanks the whole job-detail page.
  const env = Object.entries(task.env ?? {})
  const requires = Object.entries(task.requires ?? {})
  const commands = task.commands ?? []
  return (
    <div className="flex flex-col gap-4 p-4">
      <section>
        <Eyebrow className="mb-1 text-[10px] tracking-[0.16em]">COMMANDS</Eyebrow>
        <GlassPanel className="flex flex-col gap-1 p-3 font-mono text-[11.5px] text-fg">
          {commands.length === 0 ? (
            <span className="text-fg-mute">(none)</span>
          ) : (
            commands.map((cmd, i) => (
              <div key={i}>
                <span className="text-accent">$</span> {cmd.join(' ')}
              </div>
            ))
          )}
        </GlassPanel>
      </section>
      <section>
        <Eyebrow className="mb-1 text-[10px] tracking-[0.16em]">ENV</Eyebrow>
        <GlassPanel className="p-3 font-mono text-[11.5px] text-fg-mute">
          {env.length === 0 ? '(none)' : env.map(([k, v]) => <div key={k}>{k}={v}</div>)}
        </GlassPanel>
      </section>
      <section>
        <Eyebrow className="mb-1 text-[10px] tracking-[0.16em]">REQUIRES</Eyebrow>
        <GlassPanel className="p-3 font-mono text-[11.5px] text-fg-mute">
          {requires.length === 0 ? '(none)' : requires.map(([k, v]) => <div key={k}>{k}={v}</div>)}
        </GlassPanel>
      </section>
    </div>
  )
}
