import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'
import { clearToken } from './lib/token'
import { App } from './App'

afterEach(() => clearToken())

test('anonymous user landing on / is sent to the sign-in screen', async () => {
  render(<App />)
  await waitFor(() =>
    expect(screen.getByText(/sign in to the coordinator/i)).toBeInTheDocument(),
  )
})
