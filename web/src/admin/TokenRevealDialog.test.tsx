import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'
import { TokenRevealDialog } from './TokenRevealDialog'

const TOKEN = 'f00dcafe'.repeat(8) // 64 hex chars, like the real token

// jsdom implements no Clipboard API, so navigator.clipboard is undefined by
// default - which is also the real shape on http://host:8080, where the API is
// withheld outside a secure context. Tests that need the API install it.
let restoreClipboard: (() => void) | null = null

function installClipboard(writeText: (t: string) => Promise<void>) {
  const original = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  restoreClipboard = () => {
    if (original) Object.defineProperty(navigator, 'clipboard', original)
    else delete (navigator as { clipboard?: unknown }).clipboard
    restoreClipboard = null
  }
}

afterEach(() => restoreClipboard?.())

function renderDialog(over: Partial<Parameters<typeof TokenRevealDialog>[0]> = {}) {
  const props = {
    token: TOKEN,
    title: 'Agent enrollment created',
    endpoint: 'POST /v1/agent-enrollments',
    onDone: vi.fn(),
    ...over,
  }
  return { props, ...render(<TokenRevealDialog {...props} />) }
}

test('shows the endpoint, title, the one-time warning, and the token', () => {
  renderDialog()
  expect(screen.getByText('POST /v1/agent-enrollments')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 2, name: 'Agent enrollment created' })).toBeInTheDocument()
  expect(screen.getByText(/shown once/i)).toBeInTheDocument()
  expect(screen.getByText(/cannot be retrieved again/i)).toBeInTheDocument()
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)
})

test('matches the inherited dialog a11y baseline', () => {
  renderDialog()
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toHaveAccessibleName('Agent enrollment created')
})

test('the token field is readonly, focused, and pre-selected', () => {
  renderDialog()
  const input = screen.getByLabelText('Token') as HTMLInputElement
  expect(input).toHaveAttribute('readonly')
  // NOT type="password": the entire purpose of this dialog is to display it.
  expect(input.type).toBe('text')
  expect(input).toHaveFocus()
  expect(input.selectionStart).toBe(0)
  expect(input.selectionEnd).toBe(TOKEN.length)
})

test('a backdrop click does NOT dismiss it, but Escape does (paired positive control)', async () => {
  const { props } = renderDialog()
  const backdrop = screen.getByRole('dialog').parentElement as HTMLElement

  await userEvent.click(backdrop)
  // A stray click must never destroy the only copy of the credential.
  expect(props.onDone).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)

  // Positive control: something CAN close it, so the assertion above is about the
  // backdrop and not about a dialog that is impossible to dismiss.
  await userEvent.keyboard('{Escape}')
  expect(props.onDone).toHaveBeenCalledTimes(1)
})

test('Done calls onDone exactly once', async () => {
  const { props } = renderDialog()
  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  expect(props.onDone).toHaveBeenCalledTimes(1)
})

test('Copy writes exactly the token and flips the label', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  installClipboard(writeText)
  renderDialog()

  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(writeText).toHaveBeenCalledTimes(1)
  expect(writeText).toHaveBeenCalledWith(TOKEN)
  expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument()
})

test('with no clipboard API the Copy button is ABSENT and a manual hint replaces it', () => {
  // Default jsdom state: no navigator.clipboard, exactly like plain-HTTP relay.
  renderDialog()
  expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument()
  expect(screen.getByText(/needs HTTPS/i)).toBeInTheDocument()
  // The token is still selected, so the insecure path still works.
  expect(screen.getByLabelText('Token')).toHaveFocus()
})

test('positive control: the Copy button IS present when the API exists', () => {
  installClipboard(vi.fn().mockResolvedValue(undefined))
  renderDialog()
  // Without this, the absence assertion above could pass on a typo'd query.
  expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument()
  expect(screen.queryByText(/needs HTTPS/i)).not.toBeInTheDocument()
})

test('a rejected clipboard write falls back to the manual hint and logs nothing', async () => {
  const spies = (['log', 'info', 'warn', 'error', 'debug', 'trace'] as const).map((m) =>
    vi.spyOn(console, m).mockImplementation(() => {}),
  )
  installClipboard(vi.fn().mockRejectedValue(new Error(`clipboard denied for ${TOKEN}`)))
  renderDialog()

  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(await screen.findByText(/needs HTTPS/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument()
  // The rejection is swallowed, not logged: a caught error can carry the argument
  // that caused it, and console output is captured by extensions and screen-shared.
  for (const s of spies) expect(s).not.toHaveBeenCalled()
  spies.forEach((s) => s.mockRestore())
})
