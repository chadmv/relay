import { render, screen } from '@testing-library/react'
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
