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
  const trimmed = raw.trim()
  if (trimmed === '') return undefined
  // An integer-shaped string is recognized before JSON.parse gets a turn:
  // JSON's own grammar forbids a leading zero ahead of more digits, so "07"
  // is not valid JSON and would otherwise fall through to the verbatim
  // string branch below - a shape mismatch the server reports as an opaque
  // decode failure rather than the field-specific message it would give a
  // plain integer. This widens the ACCEPTED SHAPE only: "99" still emits 99
  // exactly as before, for the server to refuse on range.
  if (/^-?\d+$/.test(trimmed)) return Number(trimmed)
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

// The keys the form has controls for. Anything else stops the import instead of
// disappearing from it, which is the property that keeps this safe as
// jobspec.TaskSpec grows. `source` is deliberately absent: a spec carrying one
// is refused by name until the source builder exists.
const JOB_KEYS = ['name', 'priority', 'labels', 'tasks']
const TASK_KEYS = [
  'name',
  'command',
  'commands',
  'env',
  'requires',
  'timeout_seconds',
  'retries',
  'depends_on',
]

function refuse(what: string): ImportResult {
  return { ok: false, error: `The form cannot edit this spec: ${what}. Edit it as JSON instead.` }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isStringArray(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((x) => typeof x === 'string')
}

function kvRowsFrom(v: unknown): KvRow[] | null {
  if (!isObject(v)) return null
  const rows: KvRow[] = []
  for (const [key, value] of Object.entries(v)) {
    if (typeof value !== 'string') return null
    rows.push({ id: newRowId(), key, value })
  }
  return rows
}

type TaskImport = { row: TaskRow; deps: string[] } | { error: string }

function taskFrom(raw: unknown, i: number): TaskImport {
  const p = `tasks[${i}]`
  if (!isObject(raw)) return { error: `${p} is not an object` }
  for (const k of Object.keys(raw)) {
    if (!TASK_KEYS.includes(k)) return { error: `${p}.${k} is a field the form does not know` }
  }
  if (raw.name !== undefined && typeof raw.name !== 'string') return { error: `${p}.name is not a string` }

  const hasCommand = raw.command !== undefined
  const hasCommands = raw.commands !== undefined
  if (hasCommand && hasCommands) return { error: `${p} sets both command and commands` }
  let commands: CommandRow[]
  let multiCommand: boolean
  if (hasCommands) {
    if (!Array.isArray(raw.commands) || !raw.commands.every(isStringArray)) {
      return { error: `${p}.commands is not an array of string arrays` }
    }
    commands = raw.commands.map((argv) => newCommandRow(argv))
    multiCommand = true
  } else if (hasCommand) {
    if (!isStringArray(raw.command)) return { error: `${p}.command is not an array of strings` }
    commands = [newCommandRow(raw.command)]
    multiCommand = false
  } else {
    commands = [newCommandRow()]
    multiCommand = false
  }

  let env: KvRow[] = []
  if (raw.env !== undefined) {
    const rows = kvRowsFrom(raw.env)
    if (rows === null) return { error: `${p}.env is not an object of strings` }
    env = rows
  }
  let requires: KvRow[] = []
  if (raw.requires !== undefined) {
    const rows = kvRowsFrom(raw.requires)
    if (rows === null) return { error: `${p}.requires is not an object of strings` }
    requires = rows
  }

  // A numeric field holds text, but only a JSON number can be imported into it:
  // accepting a string here would re-emit it as a number and silently change the
  // type the user wrote. This refuses to MODEL, it does not validate - the server
  // refuses the same input.
  if (raw.timeout_seconds !== undefined && typeof raw.timeout_seconds !== 'number') {
    return { error: `${p}.timeout_seconds is not a number` }
  }
  if (raw.retries !== undefined && typeof raw.retries !== 'number') {
    return { error: `${p}.retries is not a number` }
  }
  if (raw.depends_on !== undefined && !isStringArray(raw.depends_on)) {
    return { error: `${p}.depends_on is not an array of strings` }
  }

  return {
    row: {
      id: newRowId(),
      name: raw.name === undefined ? '' : (raw.name as string),
      commands,
      multiCommand,
      env,
      requires,
      timeout: raw.timeout_seconds === undefined ? '' : String(raw.timeout_seconds),
      retries: raw.retries === undefined ? '' : String(raw.retries),
      dependsOn: [],
    },
    deps: raw.depends_on === undefined ? [] : (raw.depends_on as string[]),
  }
}

// Models every key or refuses, naming the first offending path. It never models
// the keys it knows and ignores the rest: the server accepts unknown keys
// silently, so a partial import is a loss with no event anywhere.
export function fromSpec(value: unknown): ImportResult {
  if (!isObject(value)) return refuse('the spec is not a JSON object')
  for (const k of Object.keys(value)) {
    if (!JOB_KEYS.includes(k)) return refuse(`${k} is a field the form does not know`)
  }
  if (value.name !== undefined && typeof value.name !== 'string') return refuse('name is not a string')

  let priority: Priority = ''
  if (value.priority !== undefined) {
    const p = value.priority
    if (p !== '' && p !== 'low' && p !== 'normal' && p !== 'high') {
      return refuse('priority is not low, normal or high')
    }
    priority = p
  }

  let labels: KvRow[] = []
  if (value.labels !== undefined) {
    const rows = kvRowsFrom(value.labels)
    if (rows === null) return refuse('labels is not an object of strings')
    labels = rows
  }

  if (!Array.isArray(value.tasks)) return refuse('tasks is not an array')
  const rows: TaskRow[] = []
  const depNames: string[][] = []
  for (let i = 0; i < value.tasks.length; i++) {
    const result = taskFrom(value.tasks[i], i)
    if ('error' in result) return refuse(result.error)
    rows.push(result.row)
    depNames.push(result.deps)
  }

  // Names resolve to ids in a second pass, so a forward reference works. A name
  // matching no task, or a task's own name, has no control in the picker to
  // represent it - refused rather than dropped.
  for (let i = 0; i < rows.length; i++) {
    const ids: string[] = []
    for (let j = 0; j < depNames[i].length; j++) {
      const target = rows.find((t) => t.name === depNames[i][j])
      if (target === undefined) return refuse(`tasks[${i}].depends_on[${j}] names no task in this spec`)
      if (target.id === rows[i].id) return refuse(`tasks[${i}].depends_on[${j}] names its own task`)
      ids.push(target.id)
    }
    rows[i].dependsOn = ids
  }

  return { ok: true, state: { name: value.name === undefined ? '' : value.name, priority, labels, tasks: rows } }
}
