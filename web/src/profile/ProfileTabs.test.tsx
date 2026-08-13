import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { DEFAULT_PROFILE_TAB, PROFILE_TABS, findProfileTab } from './tabs'
import { ProfileTabs } from './ProfileTabs'

function renderTabs(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ProfileTabs />
    </MemoryRouter>,
  )
}

test('the registry holds exactly the three built tabs, defaulting to identity', () => {
  expect(PROFILE_TABS.map((t) => t.slug)).toEqual(['identity', 'password', 'sessions'])
  expect(DEFAULT_PROFILE_TAB).toBe('identity')
})

test('findProfileTab resolves a known slug and rejects everything else', () => {
  expect(findProfileTab('identity')?.label).toBe('Identity')
  expect(findProfileTab('password')?.label).toBe('Password')
  expect(findProfileTab('sessions')?.label).toBe('Sessions')
  // The hi-fi's first slug is 'profile' (hifi3-holo-pages.jsx:2817), which would
  // make the URL /profile/profile. It must NOT resolve; the shell redirects it to
  // /profile/identity like any other unknown segment.
  expect(findProfileTab('profile')).toBeUndefined()
  expect(findProfileTab('bogus')).toBeUndefined()
  expect(findProfileTab(undefined)).toBeUndefined()
})

test('renders one link per registry entry, pointing at /profile/<slug>', () => {
  renderTabs('/profile/identity')
  expect(screen.getByRole('link', { name: 'Identity' })).toHaveAttribute(
    'href',
    '/profile/identity',
  )
  expect(screen.getByRole('link', { name: 'Password' })).toHaveAttribute(
    'href',
    '/profile/password',
  )
  expect(screen.getByRole('link', { name: 'Sessions' })).toHaveAttribute(
    'href',
    '/profile/sessions',
  )
  expect(screen.getAllByRole('link')).toHaveLength(3)
})

test('the current tab carries aria-current="page" and the others do not', () => {
  renderTabs('/profile/password')
  expect(screen.getByRole('link', { name: 'Password' })).toHaveAttribute('aria-current', 'page')
  expect(screen.getByRole('link', { name: 'Identity' })).not.toHaveAttribute('aria-current')
  expect(screen.getByRole('link', { name: 'Sessions' })).not.toHaveAttribute('aria-current')
})

test('the sessions tab is marked current on its own route', () => {
  renderTabs('/profile/sessions')
  expect(screen.getByRole('link', { name: 'Sessions' })).toHaveAttribute('aria-current', 'page')
})

test('no count badge is rendered on Sessions', () => {
  renderTabs('/profile/sessions')
  // The hi-fi puts a session count on this pill (hifi3-holo-pages.jsx:2819). We
  // could not supply one: GET /v1/auth/tokens does not exist. AdminTabs omits
  // badges for a related reason (AdminTabs.tsx:6-8). Both directions: the label
  // is exactly 'Sessions', with no digits anywhere in the bar.
  expect(screen.getByRole('link', { name: 'Sessions' })).toHaveTextContent(/^Sessions$/)
  expect(screen.getByRole('link', { name: 'Sessions' }).textContent).not.toMatch(/\d/)
})
