import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { GlassPanel, PillButton } from '../components/holo'
import { signOutEverywhere } from './api'

// The Sessions tab: ONE action, NO list.
//
// GET /v1/auth/tokens exists (internal/api/server.go:103, handleListTokens,
// shipped in PR #125) and returns id, created_at, is_current and expires_at
// per token (internal/api/tokens.go:51-61). It does NOT return the hi-fi's kind
// / agent / IP / location / last-active columns (hifi3-holo-pages.jsx:3054-3113):
// api_tokens has no columns for those (internal/store/migrations/000001_initial.up.sql:13-19).
// So the gap here is not a missing endpoint - it is that this tab does not yet
// render the list the endpoint can already supply, and even that list would be
// a bare id/created-at/expires-at table, not the hi-fi's richer one. The house
// rule is: omit what the backend cannot supply, and file the enabler for what
// it now can (EnrollmentsTab.tsx's footnote documenting the missing revoke
// endpoint, AdminPage.tsx:6-14).
//
// The ACTION, though, works: DELETE /v1/auth/tokens is a live, auth-gated,
// idempotent 204 (internal/api/auth.go:350-357). Applied faithfully the rule
// drops the list and keeps the control - dropping a working capability while its
// list has not been built yet would be over-applying it. And because this tab
// holds no query, there is no active observer to fire against a destroyed token
// when the session is torn down below; a Sessions LIST would have had to solve
// that ordering problem too.
export function SessionsTab() {
  const { clearSession } = useAuth()
  const navigate = useNavigate()
  const [confirming, setConfirming] = useState(false)

  const signOut = useMutation({
    mutationFn: () => signOutEverywhere(),
    // CLAUDE.md Invariant 1 - "end the generation before releasing the resource" -
    // read forwards. The resource is ALREADY gone by the time this runs: the
    // server has deleted every bearer token for this user (DeleteTokensForUser,
    // internal/store/query/tokens.sql:40-41). A 204 fires NO listener -
    // onUnauthorized is 401-only (the onUnauthorized notifier in lib/api.ts) - so
    // until we act, the SPA still holds a token in localStorage and still renders
    // as authenticated against a credential that no longer exists. So end the
    // generation that still believes in it before anything can observe it.
    //
    // THERE IS DELIBERATELY NO invalidateQueries HERE, and the omission is the
    // point. The house pattern for a mutation is
    // `onSuccess: () => qc.invalidateQueries({ queryKey: [...] })`
    // (useScheduleActions.ts:11). Here that would refetch every mounted query
    // against the destroyed credential - a burst of guaranteed 401s whose
    // onUnauthorized would race the teardown already in flight - and it would run
    // BEFORE anything unmounts, because a hook-level onSuccess resolves at
    // query-core mutation.js:123, ahead of the success dispatch and of any
    // mutate-level callback. clearSession() is the whole cleanup: it drops the
    // cache outright and flips status to 'anonymous', which makes ProtectedRoute
    // render <Navigate to="/auth" replace/> in the same commit and unmount every
    // active observer.
    //
    // logout() is deliberately NOT reused: it would first issue
    // DELETE /v1/auth/token (singular) against a token that no longer exists - a
    // guaranteed 401 whose onUnauthorized would race this same teardown.
    // SessionsTab.teardown.test.tsx asserts that request is never made.
    //
    // The explicit navigate is belt and braces: ProtectedRoute already redirects
    // on 'anonymous', but stating the destination keeps the intent readable and
    // keeps this component correct on its own.
    onSuccess: () => {
      clearSession()
      navigate('/auth')
    },
  })

  return (
    <div className="flex max-w-[720px] flex-col gap-3">
      <GlassPanel className="p-6">
        <div className="mb-4 flex items-baseline justify-between">
          <span className="text-[13px] text-fg">Active sessions</span>
          <span className="font-mono text-[10px] tracking-[0.06em] text-fg-dim">
            DELETE /v1/auth/tokens
          </span>
        </div>

        {/* The verified blast radius. DeleteTokensForUser is
            `DELETE FROM api_tokens WHERE user_id = $1` with no `id <> $2`
            (internal/store/query/tokens.sql:40-41), so this browser goes too. A
            control that understates its own blast radius is worse than a missing
            one. */}
        <p
          data-testid="sessions-blast-radius"
          className="mb-4 text-[12.5px] leading-relaxed text-fg-mute"
        >
          Signing out everywhere revokes <b>every</b> bearer token on your account,{' '}
          <b>including this browser</b>. You will be returned to sign-in here, and any{' '}
          <span className="font-mono">relay</span> CLI login will need{' '}
          <span className="font-mono">relay login</span> again.
        </p>

        {signOut.error && (
          <div role="alert" className="mb-3 text-[11px] text-err">
            {signOut.error.message}
          </div>
        )}

        <PillButton
          variant="danger"
          disabled={signOut.isPending}
          onClick={() => setConfirming(true)}
        >
          Sign out everywhere
        </PillButton>
      </GlassPanel>

      <div
        data-testid="sessions-omission-note"
        className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim"
      >
        ▸ There is no per-session list here yet. The server does expose{' '}
        <span className="text-fg-mute">GET /v1/auth/tokens</span>, but this tab does not render it
        - and the <span className="text-fg-mute">api_tokens</span> table still has no last-used,
        agent or IP column, so even a built list could not show which device is which. Until a list
        is built, signing out everywhere is the only session control, and it is all-or-nothing.
      </div>

      {confirming && (
        <ConfirmDialog
          title="Sign out everywhere?"
          body={
            'This revokes every bearer token on your account, including this browser - you will be returned to sign-in immediately. Any relay CLI login will need relay login again. Nothing else is deleted.'
          }
          confirmLabel="Sign out everywhere"
          destructive
          onConfirm={() => {
            // Close the dialog BEFORE firing. A mutation error rendered on the
            // page while a modal is open sits behind that modal's fixed inset-0
            // z-50 scrim, so the button would appear to do nothing. On success
            // this component is unmounted by the redirect anyway.
            setConfirming(false)
            signOut.mutate()
          }}
          onCancel={() => setConfirming(false)}
        />
      )}
    </div>
  )
}
