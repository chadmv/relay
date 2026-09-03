import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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

function taskRow(name: string) {
  return within(screen.getByRole('group', { name }))
}

test('a task row group is named by its task name and falls back to its position', async () => {
  renderBuilder()
  expect(screen.getByRole('group', { name: 'hello' })).toBeInTheDocument()
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
  expect(screen.getByRole('button', { name: 'Remove task build' })).toBeInTheDocument()
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
