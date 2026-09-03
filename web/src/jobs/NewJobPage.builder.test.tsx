import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, delay } from 'msw'
import { expect, test } from 'vitest'
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { NewJobPage } from './NewJobPage'

function DetailStub() {
  const { id } = useParams()
  return <div>detail for {id ?? ''}</div>
}

function renderBuilder() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs/new']}>
        <Routes>
          <Route path="/jobs/new" element={<NewJobPage />} />
          <Route path="/jobs/:id" element={<DetailStub />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function preview(): unknown {
  return JSON.parse(screen.getByLabelText('Job spec preview').textContent ?? '')
}

function jsonEditor(): HTMLTextAreaElement {
  return screen.getByRole('textbox', { name: 'Job spec JSON' }) as HTMLTextAreaElement
}

test('the page opens in form mode, seeded from the starter template', () => {
  renderBuilder()
  expect(screen.getByRole('button', { name: 'Form' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('textbox', { name: 'Job name' })).toHaveValue('my-job')
  expect(screen.queryByRole('textbox', { name: 'Job spec JSON' })).not.toBeInTheDocument()
})

test('the preview renders exactly the object that will be posted', () => {
  renderBuilder()
  expect(preview()).toEqual({
    name: 'my-job',
    priority: 'normal',
    tasks: [{ name: 'hello', command: ['echo', 'hello world'] }],
  })
})

test('switching to JSON writes the object the form produces', async () => {
  renderBuilder()
  const name = screen.getByRole('textbox', { name: 'Job name' })
  await userEvent.clear(name)
  await userEvent.type(name, 'edited')
  await userEvent.click(screen.getByRole('button', { name: 'JSON' }))
  expect(JSON.parse(jsonEditor().value)).toEqual({
    name: 'edited',
    priority: 'normal',
    tasks: [{ name: 'hello', command: ['echo', 'hello world'] }],
  })
})

test('an unknown key refuses the switch back and leaves the text byte-identical', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'JSON' }))
  const typed = '{"name":"j","tasks":[{"name":"t","command":["echo"],"widget":1}]}'
  const editor = jsonEditor()
  await userEvent.clear(editor)
  await userEvent.paste(typed)
  await userEvent.click(screen.getByRole('button', { name: 'Form' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('tasks[0].widget')
  expect(screen.getByRole('button', { name: 'JSON' })).toHaveAttribute('aria-pressed', 'true')
  expect(jsonEditor().value).toBe(typed)
})

test('a JSON syntax error refuses the switch back and leaves the text byte-identical', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'JSON' }))
  const typed = '{ not json'
  const editor = jsonEditor()
  await userEvent.clear(editor)
  await userEvent.paste(typed)
  await userEvent.click(screen.getByRole('button', { name: 'Form' }))

  expect(await screen.findByRole('alert')).toHaveTextContent(/Invalid JSON/)
  expect(jsonEditor().value).toBe(typed)
})

test('a full mode cycle preserves a spec the form can model', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'JSON' }))
  const editor = jsonEditor()
  await userEvent.clear(editor)
  await userEvent.paste('{"name":"round","tasks":[{"name":"t","commands":[["echo","hi there"]]}]}')
  await userEvent.click(screen.getByRole('button', { name: 'Form' }))

  expect(screen.getByRole('button', { name: 'Form' })).toHaveAttribute('aria-pressed', 'true')
  expect(preview()).toEqual({ name: 'round', tasks: [{ name: 't', commands: [['echo', 'hi there']] }] })
})

test('priority is a segmented group preselecting normal', () => {
  renderBuilder()
  const group = within(screen.getByRole('group', { name: 'Priority' }))
  expect(group.getByRole('button', { name: 'normal' })).toHaveAttribute('aria-pressed', 'true')
  expect(group.getByRole('button', { name: 'low' })).toHaveAttribute('aria-pressed', 'false')
})

test('clicking the pressed priority clears it and emits no priority key', async () => {
  renderBuilder()
  const group = within(screen.getByRole('group', { name: 'Priority' }))
  await userEvent.click(group.getByRole('button', { name: 'normal' }))
  expect(group.getByRole('button', { name: 'normal' })).toHaveAttribute('aria-pressed', 'false')
  expect(preview()).not.toHaveProperty('priority')
})

test('choosing another priority emits it', async () => {
  renderBuilder()
  await userEvent.click(within(screen.getByRole('group', { name: 'Priority' })).getByRole('button', { name: 'high' }))
  expect(preview()).toHaveProperty('priority', 'high')
})

test('a label row is emitted under labels', async () => {
  renderBuilder()
  const labels = within(screen.getByRole('group', { name: 'Labels' }))
  await userEvent.click(labels.getByRole('button', { name: 'Add label' }))
  await userEvent.type(labels.getByRole('textbox', { name: 'Key 1' }), 'project')
  await userEvent.type(labels.getByRole('textbox', { name: 'Value 1' }), 'film-x')
  expect(preview()).toHaveProperty('labels', { project: 'film-x' })
})

test('two rows sharing a key render the last-one-wins note', async () => {
  renderBuilder()
  const labels = within(screen.getByRole('group', { name: 'Labels' }))
  await userEvent.click(labels.getByRole('button', { name: 'Add label' }))
  await userEvent.click(labels.getByRole('button', { name: 'Add label' }))
  await userEvent.type(labels.getByRole('textbox', { name: 'Key 1' }), 'dup')
  expect(labels.queryByText(/last row wins/i)).not.toBeInTheDocument()
  await userEvent.type(labels.getByRole('textbox', { name: 'Key 2' }), 'dup')
  expect(labels.getByText(/last row wins/i)).toBeInTheDocument()
})

// A task's accessible name is "Task N: name" once named, or the positional
// "Task N" fallback while blank; a remove control's follows the same shape
// with a "Remove task " prefix. A caller here either already knows the exact
// positional form (a freshly added, still-blank row) or only knows the name
// (a row this test named earlier) - recomputing N itself would duplicate the
// production label, so a named lookup matches the ": name" suffix instead.
function taskGroupName(nameOrPosition: string): RegExp | string {
  return /^Task \d+$/.test(nameOrPosition) ? nameOrPosition : new RegExp(`: ${nameOrPosition}$`)
}

function removeTaskName(nameOrPosition: string): RegExp | string {
  return /^\d+$/.test(nameOrPosition)
    ? `Remove task ${nameOrPosition}`
    : new RegExp(`^Remove task \\d+: ${nameOrPosition}$`)
}

function taskRow(name: string) {
  return within(screen.getByRole('group', { name: taskGroupName(name) }))
}

test('a task row group is named by its task name and falls back to its position', async () => {
  renderBuilder()
  expect(screen.getByRole('group', { name: taskGroupName('hello') })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  expect(screen.getByRole('group', { name: 'Task 2' })).toBeInTheDocument()
})

test('adding a task row adds an empty task to the emitted spec', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  expect(preview()).toHaveProperty('tasks', [
    { name: 'hello', command: ['echo', 'hello world'] },
    { name: '' },
  ])
})

test('removing a task row removes it from the emitted spec', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.click(screen.getByRole('button', { name: 'Remove task 2' }))
  expect(preview()).toHaveProperty('tasks', [{ name: 'hello', command: ['echo', 'hello world'] }])
})

test('the remove control is named for its task once the task has a name', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.type(taskRow('Task 2').getByRole('textbox', { name: 'Task name' }), 'build')
  expect(screen.getByRole('button', { name: removeTaskName('build') })).toBeInTheDocument()
})

test('timeout and retries carry no range, no step and no number type', () => {
  renderBuilder()
  const row = taskRow('hello')
  for (const label of ['Timeout seconds', 'Retries']) {
    const input = row.getByRole('textbox', { name: label })
    expect(input).not.toHaveAttribute('min')
    expect(input).not.toHaveAttribute('max')
    expect(input).not.toHaveAttribute('step')
    expect(input).not.toHaveAttribute('maxlength')
    expect(input.getAttribute('type')).not.toBe('number')
  }
})

test('timeout and retries emit numbers', async () => {
  renderBuilder()
  const row = taskRow('hello')
  await userEvent.type(row.getByRole('textbox', { name: 'Timeout seconds' }), '3600')
  await userEvent.type(row.getByRole('textbox', { name: 'Retries' }), '2')
  expect(preview()).toHaveProperty('tasks', [
    { name: 'hello', command: ['echo', 'hello world'], timeout_seconds: 3600, retries: 2 },
  ])
})

test('the starter task renders one argument input per argv element', () => {
  renderBuilder()
  const cmd = within(taskRow('hello').getByRole('group', { name: 'Command 1' }))
  expect(cmd.getByRole('textbox', { name: 'Argument 1' })).toHaveValue('echo')
  expect(cmd.getByRole('textbox', { name: 'Argument 2' })).toHaveValue('hello world')
})

test('an argument typed with an internal space is one argv element', async () => {
  renderBuilder()
  const cmd = within(taskRow('hello').getByRole('group', { name: 'Command 1' }))
  await userEvent.click(cmd.getByRole('button', { name: 'Add argument' }))
  await userEvent.type(cmd.getByRole('textbox', { name: 'Argument 3' }), 'a b c')
  expect(preview()).toHaveProperty('tasks', [
    { name: 'hello', command: ['echo', 'hello world', 'a b c'] },
  ])
})

test('a command renders a joined preview of its own arguments', () => {
  renderBuilder()
  const cmd = within(taskRow('hello').getByRole('group', { name: 'Command 1' }))
  expect(cmd.getByText('echo hello world')).toBeInTheDocument()
})

test('adding a command promotes the task to the commands spelling', async () => {
  renderBuilder()
  await userEvent.click(taskRow('hello').getByRole('button', { name: 'Add command' }))
  const second = within(taskRow('hello').getByRole('group', { name: 'Command 2' }))
  await userEvent.type(second.getByRole('textbox', { name: 'Argument 1' }), 'sleep')
  expect(preview()).toHaveProperty('tasks', [
    { name: 'hello', commands: [['echo', 'hello world'], ['sleep']] },
  ])
})

test('removing the promoted command leaves the commands spelling in place', async () => {
  renderBuilder()
  await userEvent.click(taskRow('hello').getByRole('button', { name: 'Add command' }))
  await userEvent.click(taskRow('hello').getByRole('button', { name: 'Remove command 2' }))
  // The flag exists to round-trip an imported spelling; a count-derived rule
  // would silently rewrite the user's own.
  expect(preview()).toHaveProperty('tasks', [{ name: 'hello', commands: [['echo', 'hello world']] }])
})

test('a single-command task offers no remove-command control', () => {
  renderBuilder()
  expect(taskRow('hello').queryByRole('button', { name: /^Remove command/ })).not.toBeInTheDocument()
})

test('an env row and a requires row land under their own keys', async () => {
  renderBuilder()
  const row = taskRow('hello')
  const env = within(row.getByRole('group', { name: 'Environment variables' }))
  await userEvent.click(env.getByRole('button', { name: 'Add environment variable' }))
  await userEvent.type(env.getByRole('textbox', { name: 'Key 1' }), 'SCENE')
  await userEvent.type(env.getByRole('textbox', { name: 'Value 1' }), 'a.blend')
  const req = within(row.getByRole('group', { name: 'Requires' }))
  await userEvent.click(req.getByRole('button', { name: 'Add requirement' }))
  await userEvent.type(req.getByRole('textbox', { name: 'Key 1' }), 'gpu')
  await userEvent.type(req.getByRole('textbox', { name: 'Value 1' }), 'true')
  expect(preview()).toHaveProperty('tasks', [
    {
      name: 'hello',
      command: ['echo', 'hello world'],
      env: { SCENE: 'a.blend' },
      requires: { gpu: 'true' },
    },
  ])
})

test('toggling a dependency emits the other task name, and no task is offered to itself', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.type(taskRow('Task 2').getByRole('textbox', { name: 'Task name' }), 'second')
  const deps = within(taskRow('second').getByRole('group', { name: 'Depends on' }))
  expect(deps.queryByRole('button', { name: taskGroupName('second') })).not.toBeInTheDocument()
  await userEvent.click(deps.getByRole('button', { name: taskGroupName('hello') }))
  expect(preview()).toHaveProperty('tasks', [
    { name: 'hello', command: ['echo', 'hello world'] },
    { name: 'second', depends_on: ['hello'] },
  ])
})

test('adding a task moves focus to the new row name input and announces it', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  expect(taskRow('Task 2').getByRole('textbox', { name: 'Task name' })).toHaveFocus()
  expect(screen.getByRole('status')).toHaveTextContent('Task 2 added')
})

test('removing the middle of three rows moves focus to the next remove control and keeps every surviving label scoped', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.type(taskRow('Task 2').getByRole('textbox', { name: 'Task name' }), 'middle')
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.type(taskRow('Task 3').getByRole('textbox', { name: 'Task name' }), 'last')

  await userEvent.click(screen.getByRole('button', { name: removeTaskName('middle') }))

  expect(screen.getByRole('button', { name: removeTaskName('last') })).toHaveFocus()
  expect(screen.queryByRole('group', { name: taskGroupName('middle') })).not.toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent('middle removed')
  // The surviving rows are still reachable BY THEIR OWN LABEL, scoped to their
  // own group. Index-keyed control ids survive an add and a remove-the-last but
  // fail here: the label below a removed row re-associates and names a control
  // in a different row.
  expect(taskRow('hello').getByRole('textbox', { name: 'Task name' })).toHaveValue('hello')
  expect(taskRow('last').getByRole('textbox', { name: 'Task name' })).toHaveValue('last')
})

test('removing the last remaining row moves focus to the Add task control', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: removeTaskName('hello') }))
  expect(screen.getByRole('button', { name: 'Add task' })).toHaveFocus()
  expect(preview()).toHaveProperty('tasks', [])
})

test('adding and removing an environment row moves focus and announces', async () => {
  renderBuilder()
  const env = within(taskRow('hello').getByRole('group', { name: 'Environment variables' }))
  await userEvent.click(env.getByRole('button', { name: 'Add environment variable' }))
  expect(env.getByRole('textbox', { name: 'Key 1' })).toHaveFocus()
  await userEvent.click(env.getByRole('button', { name: 'Add environment variable' }))
  await userEvent.type(env.getByRole('textbox', { name: 'Key 1' }), 'A')
  await userEvent.click(env.getByRole('button', { name: 'Remove environment variable 1: A' }))
  expect(env.getByRole('button', { name: 'Remove environment variable 1' })).toHaveFocus()
})

test('adding an argument moves focus to the new argument input', async () => {
  renderBuilder()
  const cmd = within(taskRow('hello').getByRole('group', { name: 'Command 1' }))
  await userEvent.click(cmd.getByRole('button', { name: 'Add argument' }))
  expect(cmd.getByRole('textbox', { name: 'Argument 3' })).toHaveFocus()
})

test('the page has exactly one polite live region', () => {
  renderBuilder()
  const regions = screen.getAllByRole('status')
  expect(regions).toHaveLength(1)
  expect(regions[0]).toHaveAttribute('aria-live', 'polite')
})

test('a 400 body no jobspec rule produces is rendered verbatim and marks no control invalid', async () => {
  // THE DISCRIMINATING INPUT FIRST: a string that matches no Go format string in
  // jobspec. A message-to-field mapping would either swallow it or attach it to
  // an arbitrary control.
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ error: 'the flux capacitor is misaligned' }, { status: 400 }),
    ),
  )
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))

  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent('the flux capacitor is misaligned')
  expect(screen.getAllByRole('alert')).toHaveLength(1)
  expect(document.querySelectorAll('[aria-invalid]')).toHaveLength(0)
})

test('the alert carries the server string verbatim, whatever its status', async () => {
  server.use(http.post('/v1/jobs', () => HttpResponse.json({ error: 'request body too large' }, { status: 413 })))
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('request body too large')
})

test('one click issues exactly one POST and the submit control is disabled while pending', async () => {
  let posted = 0
  server.use(
    http.post('/v1/jobs', async () => {
      posted++
      await delay(50)
      return HttpResponse.json({ id: 'job-1' }, { status: 201 })
    }),
  )
  renderBuilder()
  const btn = screen.getByRole('button', { name: 'Create job' })
  await userEvent.click(btn)
  await waitFor(() => expect(btn).toBeDisabled())
  await userEvent.click(btn)
  expect(posted).toBe(1)
})

test('navigation happens only after a 201', async () => {
  server.use(http.post('/v1/jobs', () => HttpResponse.json({ error: 'name is required' }, { status: 400 })))
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('name is required')
  expect(screen.queryByText(/^detail for/)).not.toBeInTheDocument()
})

test('a stale server error is cleared on the next submit', async () => {
  let call = 0
  server.use(
    http.post('/v1/jobs', () => {
      call++
      if (call === 1) return HttpResponse.json({ error: 'name is required' }, { status: 400 })
      return HttpResponse.json({ id: 'job-9' }, { status: 201 })
    }),
  )
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('name is required')
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))
  expect(await screen.findByText('detail for job-9')).toBeInTheDocument()
  expect(screen.queryByText(/name is required/)).not.toBeInTheDocument()
})

// Captures the body and answers with the server's own message for that input.
function respondWith(error: string, seen: { body?: unknown }) {
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      seen.body = await request.json()
      return HttpResponse.json({ error }, { status: 400 })
    }),
  )
}

test('a dependency cycle is posted, and the server names the tasks', async () => {
  const seen: { body?: unknown } = {}
  respondWith('dependency cycle detected involving tasks: hello, second', seen)
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.type(taskRow('Task 2').getByRole('textbox', { name: 'Task name' }), 'second')
  await userEvent.click(
    within(taskRow('second').getByRole('group', { name: 'Depends on' })).getByRole('button', {
      name: taskGroupName('hello'),
    }),
  )
  await userEvent.click(
    within(taskRow('hello').getByRole('group', { name: 'Depends on' })).getByRole('button', {
      name: taskGroupName('second'),
    }),
  )

  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'dependency cycle detected involving tasks: hello, second',
  )
  expect(seen.body).toHaveProperty('tasks', [
    { name: 'hello', command: ['echo', 'hello world'], depends_on: ['second'] },
    { name: 'second', depends_on: ['hello'] },
  ])
})

test('two tasks with the same name are posted, and the server refuses', async () => {
  const seen: { body?: unknown } = {}
  respondWith('duplicate task name: hello', seen)
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.type(taskRow('Task 2').getByRole('textbox', { name: 'Task name' }), 'hello')
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('duplicate task name: hello')
  expect(seen.body).toBeDefined()
})

test('retries typed as 99 is posted as 99', async () => {
  const seen: { body?: unknown } = {}
  respondWith('task hello: retries must be between 0 and 10', seen)
  renderBuilder()
  await userEvent.type(taskRow('hello').getByRole('textbox', { name: 'Retries' }), '99')
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('task hello: retries must be between 0 and 10')
  expect(seen.body).toHaveProperty('tasks', [
    { name: 'hello', command: ['echo', 'hello world'], retries: 99 },
  ])
})

test('a task with every argument blank is posted with no command key', async () => {
  const seen: { body?: unknown } = {}
  respondWith('task hello: commands is required', seen)
  renderBuilder()
  const cmd = within(taskRow('hello').getByRole('group', { name: 'Command 1' }))
  await userEvent.clear(cmd.getByRole('textbox', { name: 'Argument 1' }))
  await userEvent.clear(cmd.getByRole('textbox', { name: 'Argument 2' }))
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('task hello: commands is required')
  expect(seen.body).toHaveProperty('tasks', [{ name: 'hello' }])
})

test('env keys are posted verbatim, including a reserved name and one with an equals sign', async () => {
  let body: unknown = null
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'job-1' }, { status: 201 })
    }),
  )
  renderBuilder()
  const env = within(taskRow('hello').getByRole('group', { name: 'Environment variables' }))
  // The equals-sign key FIRST: it is the one a key regex would reject before
  // ever reaching the reserved name.
  await userEvent.click(env.getByRole('button', { name: 'Add environment variable' }))
  await userEvent.type(env.getByRole('textbox', { name: 'Key 1' }), 'A=B')
  await userEvent.type(env.getByRole('textbox', { name: 'Value 1' }), 'one')
  await userEvent.click(env.getByRole('button', { name: 'Add environment variable' }))
  await userEvent.type(env.getByRole('textbox', { name: 'Key 2' }), 'RELAY_JOB_ID')
  await userEvent.type(env.getByRole('textbox', { name: 'Value 2' }), 'two')

  expect(env.queryByRole('alert')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))
  await waitFor(() => expect(body).not.toBeNull())
  expect(body).toHaveProperty('tasks', [
    {
      name: 'hello',
      command: ['echo', 'hello world'],
      env: { 'A=B': 'one', RELAY_JOB_ID: 'two' },
    },
  ])
})

test('an argument with an internal space reaches the POST body as one element', async () => {
  let body: unknown = null
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'job-1' }, { status: 201 })
    }),
  )
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Create job' }))
  await waitFor(() => expect(body).not.toBeNull())
  expect(body).toHaveProperty('tasks', [{ name: 'hello', command: ['echo', 'hello world'] }])
})

test('two tasks sharing a name get distinct group, remove-control and dependency-toggle names', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.type(taskRow('Task 2').getByRole('textbox', { name: 'Task name' }), 'hello')
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.type(taskRow('Task 3').getByRole('textbox', { name: 'Task name' }), 'third')

  const groups = screen.getAllByRole('group', { name: /^Task \d+: hello$/ })
  expect(groups).toHaveLength(2)
  expect(new Set(groups.map((g) => g.getAttribute('aria-label'))).size).toBe(2)

  const removeButtons = screen.getAllByRole('button', { name: /^Remove task \d+: hello$/ })
  expect(removeButtons).toHaveLength(2)
  expect(new Set(removeButtons.map((b) => b.getAttribute('aria-label'))).size).toBe(2)

  const deps = within(taskRow('third').getByRole('group', { name: 'Depends on' }))
  const depButtons = deps.getAllByRole('button', { name: /^Task \d+: hello$/ })
  expect(depButtons).toHaveLength(2)
  expect(new Set(depButtons.map((b) => b.textContent))).toEqual(new Set(['Task 1: hello', 'Task 2: hello']))
})

test('two key-value rows sharing a key get distinct remove-control names', async () => {
  renderBuilder()
  const env = within(taskRow('hello').getByRole('group', { name: 'Environment variables' }))
  await userEvent.click(env.getByRole('button', { name: 'Add environment variable' }))
  await userEvent.click(env.getByRole('button', { name: 'Add environment variable' }))
  await userEvent.type(env.getByRole('textbox', { name: 'Key 1' }), 'dup')
  await userEvent.type(env.getByRole('textbox', { name: 'Key 2' }), 'dup')

  const removeButtons = env.getAllByRole('button', { name: /^Remove environment variable \d+: dup$/ })
  expect(removeButtons).toHaveLength(2)
  expect(new Set(removeButtons.map((b) => b.getAttribute('aria-label'))).size).toBe(2)
})

test('removing a still-unnamed task announces its positional fallback', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  await userEvent.click(screen.getByRole('button', { name: removeTaskName('2') }))
  expect(screen.getByRole('status')).toHaveTextContent('Task 2 removed')
})

test('removing a blank-key row announces its position', async () => {
  renderBuilder()
  const env = within(taskRow('hello').getByRole('group', { name: 'Environment variables' }))
  await userEvent.click(env.getByRole('button', { name: 'Add environment variable' }))
  await userEvent.click(env.getByRole('button', { name: 'Remove environment variable 1' }))
  expect(screen.getByRole('status')).toHaveTextContent('environment variable 1 removed')
})
