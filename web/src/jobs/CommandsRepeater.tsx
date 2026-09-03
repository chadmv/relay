import { PillButton } from '../components/holo'
import { Input } from '../components/Input'
import { newCommandRow, newTokenRow, type TaskRow } from './specBuilder'
import { useFocusAfterUpdate } from './useFocusAfterUpdate'

interface CommandsRepeaterProps {
  task: TaskRow
  onChange: (next: TaskRow) => void
  announce: (message: string) => void
}

// A command with no non-blank token - whether that is zero rows or every row
// left blank - emits no argv (toSpec's argvOf filters blanks the same way)
// and so drops silently out of the emitted command/commands array while its
// own panel keeps rendering, exactly like two key-value rows sharing a key.
function isEmptyCommand(cmd: TaskRow['commands'][number]): boolean {
  return cmd.tokens.every((t) => t.text === '')
}

// One input per argv TOKEN, never one input per command line. Splitting a line
// on whitespace is a correctness bug the first time anyone types a path with a
// space in it, and a silent one: the spec serializes, dispatches and fails on the
// agent. A quoting grammar would make this module the owner of a rule relay's Go
// has nowhere, with no server-side counterpart to pin it.
//
// Wrapped in memo() at each import site, not here, so this stays a plain
// function a test can call or spy on directly.
export function CommandsRepeater({ task, onChange, announce }: CommandsRepeaterProps) {
  const focusAfterUpdate = useFocusAfterUpdate()
  const addCommandId = `task-${task.id}-add-command`

  function setCommands(commands: TaskRow['commands'], multiCommand = task.multiCommand) {
    onChange({ ...task, commands, multiCommand })
  }

  function addCommand() {
    const cmd = newCommandRow()
    // The promotion is recorded here and is NOT reversed by removing the command
    // again: the flag exists to round-trip an imported spelling.
    setCommands([...task.commands, cmd], true)
    announce(`Command ${task.commands.length + 1} added`)
    focusAfterUpdate(`task-${task.id}-cmd-${cmd.id}-arg-${cmd.tokens[0].id}`)
  }

  function removeCommand(ci: number) {
    const gone = task.commands[ci]
    setCommands(task.commands.filter((c) => c.id !== gone.id))
    announce(`Command ${ci + 1} removed`)
    const next = task.commands[ci + 1] ?? task.commands[ci - 1]
    focusAfterUpdate(next === undefined ? addCommandId : `task-${task.id}-cmd-${next.id}-remove`)
  }

  function addToken(ci: number) {
    const token = newTokenRow()
    const cmd = task.commands[ci]
    setCommands(task.commands.map((c, j) => (j === ci ? { ...c, tokens: [...c.tokens, token] } : c)))
    announce(`Argument ${cmd.tokens.length + 1} added`)
    focusAfterUpdate(`task-${task.id}-cmd-${cmd.id}-arg-${token.id}`)
  }

  function removeToken(ci: number, ti: number) {
    const cmd = task.commands[ci]
    const gone = cmd.tokens[ti]
    setCommands(
      task.commands.map((c, j) => (j === ci ? { ...c, tokens: c.tokens.filter((t) => t.id !== gone.id) } : c)),
    )
    announce(`Argument ${ti + 1} removed`)
    const next = cmd.tokens[ti + 1] ?? cmd.tokens[ti - 1]
    focusAfterUpdate(
      next === undefined
        ? `task-${task.id}-cmd-${cmd.id}-add-arg`
        : `task-${task.id}-cmd-${cmd.id}-arg-${next.id}-remove`,
    )
  }

  function editToken(ci: number, ti: number, text: string) {
    setCommands(
      task.commands.map((c, j) =>
        j === ci ? { ...c, tokens: c.tokens.map((t, k) => (k === ti ? { ...t, text } : t)) } : c,
      ),
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">Commands</span>
      {task.commands.map((cmd, ci) => (
        <div
          key={cmd.id}
          role="group"
          aria-label={`Command ${ci + 1}`}
          className="flex flex-col gap-1.5 rounded-[8px] border border-border p-2"
        >
          {cmd.tokens.map((token, ti) => (
            <div key={token.id} className="flex flex-wrap items-center gap-1.5">
              <Input
                id={`task-${task.id}-cmd-${cmd.id}-arg-${token.id}`}
                aria-label={`Argument ${ti + 1}`}
                value={token.text}
                spellCheck={false}
                onChange={(e) => editToken(ci, ti, e.target.value)}
                className="min-w-[8rem] flex-1 font-mono"
              />
              <PillButton
                id={`task-${task.id}-cmd-${cmd.id}-arg-${token.id}-remove`}
                aria-label={`Remove argument ${ti + 1} from command ${ci + 1}`}
                onClick={() => removeToken(ci, ti)}
              >
                Remove
              </PillButton>
            </div>
          ))}
          {isEmptyCommand(cmd) ? (
            <p className="text-[11px] text-fg-dim">This command has no arguments and will not be submitted.</p>
          ) : (
            // The same joined rendering the task-detail spec panel uses, so the
            // reading and the writing surfaces agree. It is a preview, not the
            // value: each argument is its own element on the wire.
            <span className="font-mono text-[11px] text-fg-dim">
              {cmd.tokens.map((t) => t.text).filter((t) => t !== '').join(' ')}
            </span>
          )}
          <div className="flex flex-wrap gap-1.5">
            <PillButton id={`task-${task.id}-cmd-${cmd.id}-add-arg`} onClick={() => addToken(ci)}>
              Add argument
            </PillButton>
            {task.commands.length > 1 ? (
              <PillButton
                id={`task-${task.id}-cmd-${cmd.id}-remove`}
                aria-label={`Remove command ${ci + 1}`}
                onClick={() => removeCommand(ci)}
              >
                Remove command
              </PillButton>
            ) : null}
          </div>
        </div>
      ))}
      <div>
        <PillButton id={addCommandId} onClick={addCommand}>
          Add command
        </PillButton>
      </div>
    </div>
  )
}
