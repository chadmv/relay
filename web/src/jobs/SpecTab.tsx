import type { TaskDetail } from './api'

// Renders the selected task's spec: commands, env, requires. No per-task source
// block (handleGetJob does not echo `source`); that is out of scope.
export function SpecTab({ task }: { task: TaskDetail | undefined }) {
  if (!task) {
    return <div className="p-4 text-[12px] text-fg-mute">Select a task to view its spec.</div>
  }
  const env = Object.entries(task.env)
  const requires = Object.entries(task.requires)
  return (
    <div className="flex flex-col gap-4 p-4">
      <section>
        <div className="mb-1 font-mono text-[10px] tracking-widest text-fg-mute">COMMANDS</div>
        <div className="flex flex-col gap-1 rounded-card border border-border bg-black/20 p-3 font-mono text-[11.5px] text-fg">
          {task.commands.length === 0 ? (
            <span className="text-fg-mute">(none)</span>
          ) : (
            task.commands.map((cmd, i) => <div key={i}>$ {cmd.join(' ')}</div>)
          )}
        </div>
      </section>
      <section>
        <div className="mb-1 font-mono text-[10px] tracking-widest text-fg-mute">ENV</div>
        <div className="rounded-card border border-border bg-white/5 p-3 font-mono text-[11.5px] text-fg-mute">
          {env.length === 0 ? '(none)' : env.map(([k, v]) => <div key={k}>{k}={v}</div>)}
        </div>
      </section>
      <section>
        <div className="mb-1 font-mono text-[10px] tracking-widest text-fg-mute">REQUIRES</div>
        <div className="rounded-card border border-border bg-white/5 p-3 font-mono text-[11.5px] text-fg-mute">
          {requires.length === 0 ? '(none)' : requires.map(([k, v]) => <div key={k}>{k}={v}</div>)}
        </div>
      </section>
    </div>
  )
}
