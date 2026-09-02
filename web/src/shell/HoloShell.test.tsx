import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { HoloShell } from './HoloShell'

function renderShell(isAdmin: boolean) {
  setToken('tok')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: isAdmin }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs']}>
        <AuthProvider>
          <HoloShell>
            <div>page body</div>
          </HoloShell>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

// The breakpoint prefixes are spelled as a constant plus a bare suffix, never as
// one literal string, and that is load-bearing rather than stylistic. Tailwind v4
// scans every file under web/ for class-shaped substrings - test files included -
// and emits a rule for each one it finds. A literal here would put these rules in
// the production bundle on its own, so the post-build check that is supposed to
// attribute them to HoloShell.tsx would pass with the classes deleted from the
// component. The bare suffixes still emit their own unprefixed utilities, which is
// harmless dead CSS.
const NARROW = 'max-md:'
const WIDE = 'md:'

test('always shows the non-admin nav entries', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByText('page body')).toBeInTheDocument())
  for (const label of ['Jobs', 'Workers', 'Schedules']) {
    expect(screen.getByRole('link', { name: label })).toBeInTheDocument()
  }
})

test('hides the Admin nav entry from non-admins', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByRole('link', { name: 'Jobs' })).toBeInTheDocument())
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
})

test('shows the Admin nav entry to admins', async () => {
  renderShell(true)
  expect(await screen.findByRole('link', { name: 'Admin' })).toHaveAttribute('href', '/admin')
})

// The header owns the profile dropdown, which hangs down over <main>. <main> is
// a later sibling and its panels create stacking contexts of their own (every
// GlassPanel carries backdrop-blur), so with both at z-auto the page content
// paints over an open dropdown. A z-index on the dropdown alone cannot fix that:
// the header's own backdrop-blur makes it a stacking context, which confines any
// z-index inside it. The order has to be declared out here, between the siblings.
//
// jsdom does no layout and no hit-testing, so this can only pin the classes;
// the ordering they produce was measured in Chrome by hit-testing 275 points
// across the open dropdown (see HoloShell.tsx for the numbers). z-0 on <main>
// is asserted for the same reason it is written: it is the guard that keeps a
// future page-level z-index from climbing back over the header.
test('stacks the header above the page content', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByText('page body')).toBeInTheDocument())
  expect(screen.getByRole('banner')).toHaveClass('relative', 'z-10')
  expect(screen.getByRole('main')).toHaveClass('relative', 'z-0')
})

// Cause 0 of docs/backlog/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md.
// A real browser measured HEADER at 523px against a 375px viewport on EVERY shell
// page, including two that render no table at all - the header, not the tables, is
// the floor every page inherits.
//
// jsdom does no layout, so the first three assertions are REGRESSION PINS, not
// proof: they pin the classes whose effect was measured in Chrome (Task 7 of
// docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md). Same honesty as
// the stacking test above, which can only pin z-10/z-0.
//
// The FOURTH assertion is not a pin, it is a real guard. The scroll container must
// be the <nav> and NEVER the <header>: an overflow on the header establishes a
// scroll container that CLIPS the UserMenu dropdown, which deliberately hangs out
// of the header over <main> - the behaviour established by the 275-point hit test
// recorded in HoloShell.tsx. "Just put overflow-x-auto on the header" is the
// tempting wrong fix, and this line is what reddens for it.
test('the nav is the only shrinkable scroll container in the header', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })

  // The scroll container MOVED and the rule did not. The links now live in an
  // always-mounted panel inside the <nav>, so the two pins that described the
  // <nav> describe the panel, and the scroll is scoped to md and up because below
  // it the panel is the dropdown and must not scroll. The whole shrink chain is
  // asserted, not just the leaf: a flex item's automatic minimum size is its
  // content, so panel, <nav> and the group holding the wordmark all need min-w-0
  // or the header gets a content floor back.
  const panel = screen.getByTestId('header-nav-panel')
  expect(panel).toHaveClass('min-w-0', WIDE + 'overflow-x-auto')
  const nav = panel.parentElement as HTMLElement
  expect(nav).toHaveAttribute('aria-label', 'Main')
  expect(nav).toHaveClass('min-w-0')
  expect(nav.parentElement).toHaveClass('min-w-0')

  const header = screen.getByRole('banner')
  expect(header.className).not.toMatch(/\boverflow-/)
})

// AC1. The collapsed nav is an ARIA disclosure, and its panel is ALWAYS mounted -
// which is why aria-controls is present in both states here and only while open in
// UserMenu, whose panel is conditionally mounted. An IDREF to a node that does not
// exist is an authoring error; an IDREF to a node that is merely display:none is
// not. A reviewer "fixing" this into agreement with UserMenu would be wrong.
test('the nav toggle exposes disclosure semantics in both states', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  const panel = screen.getByTestId('header-nav-panel')

  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(toggle).toHaveAttribute('aria-controls', panel.id)
  expect(panel.id).toBeTruthy()

  await userEvent.click(toggle)

  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(toggle).toHaveAttribute('aria-controls', panel.id)
})

// REGRESSION PIN: the collapsed nav is a full-bleed opaque panel below md and
// inline above it.
//
// A PIN, NOT A GUARD. jsdom applies no CSS and does no layout, so nothing here
// evaluates a breakpoint, a position or a fill - every one of these classes was
// chosen for an effect only a browser can show. Its whole job is to make a silent
// deletion visible in this lane. The behaviour is header-nav.spec.ts's.
//
// Full bleed rather than a fixed-width panel anchored at the nav: a 224px panel
// starting past the wordmark at a 320px viewport reaches beyond the viewport edge
// and re-creates the document overflow the previous narrow-viewport slice closed.
// left-0 with right-0 cannot overflow by construction. The <header> is the
// positioned ancestor it anchors to.
//
// The bg fill is load-bearing for the same reason it is in UserMenu: GlassPanel
// and the header set no background-color at all, so a panel floating over live
// content without its own fill reads straight through.
test('REGRESSION PIN: the collapsed nav is a full-bleed opaque panel below md and inline above it', async () => {
  renderShell(true)
  await screen.findByRole('link', { name: 'Admin' })
  const toggle = screen.getByRole('button', { name: /menu/i })
  const panel = screen.getByTestId('header-nav-panel')

  expect(toggle).toHaveClass(WIDE + 'hidden')
  expect(panel).toHaveClass(WIDE + 'flex')
  expect(panel).toHaveClass(
    NARROW + 'absolute',
    NARROW + 'left-0',
    NARROW + 'right-0',
    NARROW + 'top-full',
    NARROW + 'z-50',
    NARROW + 'flex-col',
    NARROW + 'bg-popover',
  )

  // The state switch itself, which is the entire collapse mechanism and the one
  // part of it jsdom can see.
  expect(panel).toHaveClass('hidden')
  expect(panel).not.toHaveClass('flex')
  await userEvent.click(toggle)
  expect(panel).toHaveClass('flex')
  expect(panel).not.toHaveClass('hidden')
})
