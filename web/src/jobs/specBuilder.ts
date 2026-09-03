import { STARTER_TEMPLATE } from './specTemplate'

// Stable per-row identity, never derived from a position. The dependency picker
// stores row ids rather than task names, so a rename cannot strand a reference:
// toSpec resolves an id to the row's CURRENT name at emission time. Ids never
// reach the wire.
let rowSeq = 0
export function newRowId(): string {
  rowSeq += 1
  return `r${rowSeq}`
}

export interface KvRow {
  id: string
  key: string
  value: string
}

export interface TokenRow {
  id: string
  text: string
}

export interface CommandRow {
  id: string
  tokens: TokenRow[]
}

export type Priority = '' | 'low' | 'normal' | 'high'

export interface TaskRow {
  id: string
  name: string
  commands: CommandRow[]
  // The command SPELLING, not a count. A task imported as `commands` holding one
  // argv must be re-emitted as `commands`; a rule derived from the number of
  // commands would silently rewrite what the user wrote. Adding a second command
  // sets this; removing one does not clear it.
  multiCommand: boolean
  env: KvRow[]
  requires: KvRow[]
  // Raw text, never a number. A half-typed value is not an integer, and the
  // state has to be able to hold what was typed without inventing a value.
  timeout: string
  retries: string
  // Row ids, resolved to names at emission time.
  dependsOn: string[]
}

export interface BuilderState {
  name: string
  priority: Priority
  labels: KvRow[]
  tasks: TaskRow[]
}

export function newKvRow(): KvRow {
  return { id: newRowId(), key: '', value: '' }
}

export function newTokenRow(text = ''): TokenRow {
  return { id: newRowId(), text }
}

export function newCommandRow(tokens: string[] = ['']): CommandRow {
  return { id: newRowId(), tokens: tokens.map((t) => newTokenRow(t)) }
}

export function newTaskRow(): TaskRow {
  return {
    id: newRowId(),
    name: '',
    commands: [newCommandRow()],
    multiCommand: false,
    env: [],
    requires: [],
    timeout: '',
    retries: '',
    dependsOn: [],
  }
}

// The object that is POSTed. `timeout_seconds` and `retries` widen to string
// because unparseable text is emitted verbatim for the server to refuse, rather
// than the client inventing a number - see numberOf.
export interface TaskSpecJson {
  name: string
  command?: string[]
  commands?: string[][]
  env?: Record<string, string>
  requires?: Record<string, string>
  timeout_seconds?: number | string
  retries?: number | string
  depends_on?: string[]
}

export interface JobSpecJson {
  name: string
  priority?: string
  labels?: Record<string, string>
  tasks: TaskSpecJson[]
}

// Blank is the EMPTY string exactly, never a trimmed one: a single space is a
// legitimate argv element and trimming here would rewrite it.
function argvOf(cmd: CommandRow): string[] {
  return cmd.tokens.map((t) => t.text).filter((t) => t !== '')
}

// A row with an empty key is dropped. Two rows with the same key collapse and
// the LAST one wins, which is what building a JSON object does; the form renders
// a note wherever that can happen.
function mapOf(rows: KvRow[]): Record<string, string> | undefined {
  const out: Record<string, string> = {}
  let any = false
  for (const r of rows) {
    if (r.key === '') continue
    out[r.key] = r.value
    any = true
  }
  return any ? out : undefined
}

// Text that parses as a JSON number emits that number; empty or whitespace-only
// emits no key; anything else is emitted VERBATIM so the server refuses it
// rather than the client inventing a value. No range is applied here: the bounds
// belong to jobspec.Validate and a copy of one would make this refuse a spec the
// server accepts on the first release that moves it.
function numberOf(raw: string): number | string | undefined {
  if (raw.trim() === '') return undefined
  try {
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed === 'number') return parsed
  } catch {
    // Not JSON at all; fall through to the verbatim emission.
  }
  return raw
}

function taskToSpec(t: TaskRow, nameById: Map<string, string>): TaskSpecJson {
  const argvs = t.commands.map(argvOf).filter((a) => a.length > 0)
  const commandKeys: Pick<TaskSpecJson, 'command' | 'commands'> =
    argvs.length === 0 ? {} : t.multiCommand ? { commands: argvs } : { command: argvs[0] }
  const env = mapOf(t.env)
  const requires = mapOf(t.requires)
  const timeout = numberOf(t.timeout)
  const retries = numberOf(t.retries)
  const deps: string[] = []
  for (const id of t.dependsOn) {
    const name = nameById.get(id)
    // A row removed while it was still selected simply stops being emitted; the
    // remove handler prunes the selection, and this is the backstop.
    if (name !== undefined) deps.push(name)
  }
  return {
    name: t.name,
    ...commandKeys,
    ...(env === undefined ? {} : { env }),
    ...(requires === undefined ? {} : { requires }),
    ...(timeout === undefined ? {} : { timeout_seconds: timeout }),
    ...(retries === undefined ? {} : { retries }),
    ...(deps.length === 0 ? {} : { depends_on: deps }),
  }
}

// The object that will be POSTed, exactly. The read-only preview renders this,
// so a dropped blank token is visible before submit rather than after it.
export function toSpec(state: BuilderState): JobSpecJson {
  const nameById = new Map(state.tasks.map((t) => [t.id, t.name]))
  const labels = mapOf(state.labels)
  return {
    name: state.name,
    ...(state.priority === '' ? {} : { priority: state.priority }),
    ...(labels === undefined ? {} : { labels }),
    tasks: state.tasks.map((t) => taskToSpec(t, nameById)),
  }
}

// The form starts from the same spec the JSON editor starts from, so switching
// modes on an untouched page produces the starter template's own object rather
// than a second, drifting default. The fallback is unreachable while the
// template is modellable, which "newBuilderState models the starter template"
// pins.
export function newBuilderState(): BuilderState {
  const result = fromSpec(JSON.parse(STARTER_TEMPLATE) as unknown)
  if (result.ok) return result.state
  return { name: '', priority: 'normal', labels: [], tasks: [newTaskRow()] }
}

export type ImportResult = { ok: true; state: BuilderState } | { ok: false; error: string }

export function fromSpec(_value: unknown): ImportResult {
  return { ok: false, error: 'not implemented' }
}
