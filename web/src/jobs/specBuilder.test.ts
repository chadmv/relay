import { expect, test } from 'vitest'
import { fromSpec, newBuilderState, newCommandRow, newKvRow, newTaskRow, toSpec } from './specBuilder'
import { STARTER_TEMPLATE } from './specTemplate'

// A minimal state built by hand rather than through newBuilderState, so these
// tests keep meaning if the starter template changes.
function oneTask() {
  const t = newTaskRow()
  t.name = 'hello'
  t.commands = [newCommandRow(['echo'])]
  return { name: 'my-job', priority: 'normal' as const, labels: [], tasks: [t] }
}

test('an untouched optional emits no key at the job level', () => {
  expect(Object.keys(toSpec(oneTask())).sort()).toEqual(['name', 'priority', 'tasks'])
})

test('an untouched optional emits no key at the task level', () => {
  expect(Object.keys(toSpec(oneTask()).tasks[0]).sort()).toEqual(['command', 'name'])
})

test('a blank argv token is dropped and does not become an empty string', () => {
  const s = oneTask()
  // The blank goes FIRST: an implementation that stops at the first empty token
  // rather than filtering every one of them cannot pass by never reaching it.
  s.tasks[0].commands = [newCommandRow(['', 'echo', '', 'hi'])]
  expect(toSpec(s).tasks[0].command).toEqual(['echo', 'hi'])
})

test('a command left with no tokens emits neither command nor commands', () => {
  const s = oneTask()
  s.tasks[0].commands = [newCommandRow([''])]
  const task = toSpec(s).tasks[0]
  expect(task.command).toBeUndefined()
  expect(task.commands).toBeUndefined()
  expect(Object.keys(task)).toEqual(['name'])
})

test('an argument containing a space is one argv element', () => {
  const s = oneTask()
  s.tasks[0].commands = [newCommandRow(['echo', 'hello world'])]
  expect(toSpec(s).tasks[0].command).toEqual(['echo', 'hello world'])
})

test('newBuilderState models the starter template', () => {
  expect(newBuilderState().tasks).toHaveLength(1)
  expect(newBuilderState().name).toBe('my-job')
})

test('numeric text that parses as a number emits a number', () => {
  const s = oneTask()
  s.tasks[0].timeout = '3600'
  s.tasks[0].retries = '2'
  const task = toSpec(s).tasks[0]
  expect(task.timeout_seconds).toBe(3600)
  expect(task.retries).toBe(2)
})

test('an empty numeric field emits no key, and whitespace counts as empty', () => {
  const s = oneTask()
  s.tasks[0].timeout = '   '
  s.tasks[0].retries = ''
  const task = toSpec(s).tasks[0]
  expect('timeout_seconds' in task).toBe(false)
  expect('retries' in task).toBe(false)
})

test('text that is not a JSON number is emitted verbatim for the server to refuse', () => {
  const s = oneTask()
  s.tasks[0].retries = 'soon'
  expect(toSpec(s).tasks[0].retries).toBe('soon')
})

test('an out-of-range number is emitted as typed, with no client-side clamp', () => {
  // 99 is above the server's own retries bound. The client must not know that.
  const s = oneTask()
  s.tasks[0].retries = '99'
  expect(toSpec(s).tasks[0].retries).toBe(99)
})

test('a key-value row with a blank key is dropped and the last duplicate wins', () => {
  const s = oneTask()
  const blank = newKvRow()
  const first = { ...newKvRow(), key: 'SCENE', value: 'a.blend' }
  const second = { ...newKvRow(), key: 'SCENE', value: 'b.blend' }
  // The blank row FIRST, so an implementation that stops at the first offender
  // rather than filtering all of them cannot pass by never reaching it.
  s.tasks[0].env = [blank, first, second]
  expect(toSpec(s).tasks[0].env).toEqual({ SCENE: 'b.blend' })
})

test('an all-blank key-value repeater emits no key at all', () => {
  const s = oneTask()
  s.tasks[0].env = [newKvRow(), newKvRow()]
  expect('env' in toSpec(s).tasks[0]).toBe(false)
})

test('a dependency follows a rename', () => {
  const s = oneTask()
  const b = newTaskRow()
  b.name = 'b'
  b.commands = [newCommandRow(['echo'])]
  b.dependsOn = [s.tasks[0].id]
  s.tasks = [s.tasks[0], b]
  expect(toSpec(s).tasks[1].depends_on).toEqual(['hello'])

  s.tasks[0] = { ...s.tasks[0], name: 'renamed' }
  expect(toSpec(s).tasks[1].depends_on).toEqual(['renamed'])
})

test('a dependency on a task that is no longer in the state is not emitted', () => {
  const s = oneTask()
  const b = newTaskRow()
  b.name = 'b'
  b.commands = [newCommandRow(['echo'])]
  b.dependsOn = ['r-gone']
  s.tasks = [b]
  expect('depends_on' in toSpec(s).tasks[0]).toBe(false)
})

test('the multi-command flag emits commands even for a single command', () => {
  const s = oneTask()
  s.tasks[0].multiCommand = true
  expect(toSpec(s).tasks[0].commands).toEqual([['echo']])
  expect('command' in toSpec(s).tasks[0]).toBe(false)
})

test('two commands under the multi-command flag emit both, in order', () => {
  const s = oneTask()
  s.tasks[0].multiCommand = true
  s.tasks[0].commands = [newCommandRow(['a']), newCommandRow(['b'])]
  expect(toSpec(s).tasks[0].commands).toEqual([['a'], ['b']])
})

// The server's own examples, not invented fixtures. This is the README `relay
// submit` job file, verbatim.
const README_JOB = {
  name: 'my-render',
  priority: 'normal',
  labels: { project: 'film-x' },
  tasks: [
    {
      name: 'frame-001',
      command: ['blender', '-b', 'scene.blend', '-f', '1'],
      env: { SCENE: 'scene.blend' },
      requires: { gpu: 'true' },
      timeout_seconds: 3600,
      retries: 2,
    },
    {
      name: 'frame-002',
      command: ['blender', '-b', 'scene.blend', '-f', '2'],
      depends_on: ['frame-001'],
    },
  ],
}

function imported(spec: unknown) {
  const result = fromSpec(spec)
  if (!result.ok) throw new Error(`expected an accepted import, got: ${result.error}`)
  return result.state
}

test('the README job file round-trips through the form state', () => {
  expect(toSpec(imported(README_JOB))).toEqual(README_JOB)
})

test('the starter template round-trips through the form state', () => {
  const parsed: unknown = JSON.parse(STARTER_TEMPLATE)
  expect(toSpec(imported(parsed))).toEqual(parsed)
})

test('a task imported with a one-entry commands re-emits as commands', () => {
  const spec = { name: 'j', tasks: [{ name: 't', commands: [['echo', 'hi']] }] }
  expect(toSpec(imported(spec))).toEqual(spec)
})

test('a task imported with command re-emits as command', () => {
  const spec = { name: 'j', tasks: [{ name: 't', command: ['echo', 'hi'] }] }
  expect(toSpec(imported(spec))).toEqual(spec)
})

test('an imported argument containing a space stays one argv element', () => {
  const spec = { name: 'j', tasks: [{ name: 't', command: ['echo', 'hello world'] }] }
  expect(imported(spec).tasks[0].commands[0].tokens.map((t) => t.text)).toEqual(['echo', 'hello world'])
})

test('a depends_on naming a duplicated task name resolves to the first of them', () => {
  const spec = {
    name: 'j',
    tasks: [
      { name: 'dup', command: ['a'] },
      { name: 'dup', command: ['b'] },
      { name: 'c', command: ['c'], depends_on: ['dup'] },
    ],
  }
  const state = imported(spec)
  expect(state.tasks[2].dependsOn).toEqual([state.tasks[0].id])
})

test('every imported row carries a distinct identity', () => {
  const state = imported(README_JOB)
  expect(new Set(state.tasks.map((t) => t.id)).size).toBe(2)
})
