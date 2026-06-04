import { setupServer } from 'msw/node'

// Shared MSW server. Per-test handlers are added with server.use(...).
export const server = setupServer()
