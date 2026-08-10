import { useEffect, useId, useRef, useState } from 'react'
import { PillButton } from '../components/holo'
import { DialogShell } from '../components/dialog/DialogShell'
import { formatTimeUntil } from '../lib/time'

interface TokenRevealDialogProps {
  // The raw credential. MUST be passed straight from the mutation's data
  // (create.data.token) and never copied into caller state, so there is exactly
  // one retention site and the caller's reset() destroys it.
  token: string
  title: string
  // The endpoint that minted it, e.g. "POST /v1/agent-enrollments". Display only.
  endpoint: string
  // ISO expiry of the credential (e.g. CreateEnrollmentResponse.expires_at,
  // api.ts). Optional and display-only - not every future consumer of this
  // shared dialog necessarily has one. Rendered once from `now` at mount; there
  // is no live tick here (unlike EnrollmentsTable's useNow(60_000)) because the
  // dialog is short-lived and this is not live data - no new fetch either way.
  expiresAt?: string
  warning?: string
  // Called on Done AND on Escape. The caller MUST reset() the mutation here: that
  // is what actually drops the token. Unmounting this component alone does not,
  // because TanStack retains a mutation's data and variables until reset.
  onDone: () => void
}

const DEFAULT_WARNING =
  'This token is shown once. It cannot be retrieved again - copy it now, or create a replacement.'

// Shared reveal surface for a one-time credential: agent enrollments today,
// invites later. It replaces the hi-fi's success toast, which does not exist and
// would be the wrong primitive anyway - auto-dismissal turns a glance away from
// the screen into permanent data loss.
//
// SECURITY INVARIANTS, structural rather than incidental:
//  1. The token is rendered from the `token` prop and nowhere else. This component
//     holds NO state containing it (`copied` and `canCopy` are booleans).
//  2. Nothing here calls console.*. The clipboard catch swallows its rejection
//     rather than logging an error that could carry the argument.
//  3. The token never enters a URL, a route, a query param, or a query key. This
//     dialog is not linkable or bookmarkable by construction, so the credential
//     cannot leak into history, a Referer header, or a proxy log.
//  4. NEITHER a backdrop click NOR Escape dismisses. There is deliberately no
//     onClick on the overlay (the hi-fi's AdminTokenModal has one at
//     design_handoff_relay_holo/hifi3-holo-pages.jsx:2345, which is fine for a
//     form and catastrophic for a secret), and DialogShell is passed
//     dismissOnEscape={false}. Escape is the same class of input as a stray
//     click - single, low-intent, no target, frequently reflexive - and here
//     dismissal IS the destructive act: onDone is what calls create.reset(), so
//     there is nothing to revert to and no cancel affordance to preserve. WAI-
//     ARIA APG requires Escape so a keyboard user is never trapped; they are not
//     trapped, because the Done button is inside the focus trap and one Tab away,
//     and it is the only exit BY DESIGN. This is the documented irreversible-
//     dismissal exception, not an oversight. A confirm-before-discarding dialog
//     was considered and rejected: it stacks a second modal on the credential
//     modal to guard against a keystroke the user can simply not press, and ends
//     in a dialog asking whether you meant to close the dialog that says do not
//     close me.
//  5. a11y and modal behavior come from web/src/components/dialog/DialogShell.tsx,
//     which every dialog in the app composes: the labelled modal role, the portal,
//     the focus trap, the inert + aria-hidden background, the scroll lock, and the
//     scoped Escape. The credential can no longer be tabbed past - verified true
//     even through a scrim click, which used to blur focus to <body> and hand
//     the Tab trap nothing to intercept (code review, 2026-08-09; the scrim's
//     onMouseDown in DialogShell.tsx now prevents that blur outright, pinned by
//     DialogShell.test.tsx's "a scrim mousedown does not blur the panel at all"
//     test, which asserts the trap specifically).
export function TokenRevealDialog({
  token,
  title,
  endpoint,
  expiresAt,
  warning,
  onDone,
}: TokenRevealDialogProps) {
  const titleId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const [copied, setCopied] = useState(false)
  // navigator.clipboard is undefined outside a secure context, and relay-server
  // serves plain HTTP on :8080 by default, so on a LAN-hosted http://host:8080
  // there is no clipboard API at all. Feature-detected rather than assumed:
  // rendering a Copy button that can only fail is worse than not offering one.
  // document.execCommand('copy') is not used as a fallback - deprecated, and it
  // buys nothing over the already-selected input.
  const [canCopy, setCanCopy] = useState(
    () => typeof navigator.clipboard?.writeText === 'function',
  )

  // Focus + select the token ONCE, on mount only: satisfies "first field
  // focused" and gives keyboard users select-all for free, which is the manual
  // copy path when the clipboard API is unavailable. This must NOT depend on
  // onDone: EnrollmentsTab passes an inline `onDone={() => create.reset()}`
  // whose identity changes on every parent re-render (the 60s useNow tick, or
  // the list refetch after invalidation), and an effect keyed on that identity
  // re-fires focus()+select() on every one of those re-renders - yanking focus
  // away from wherever the admin has tabbed to (e.g. the Done button) back onto
  // the token input. A keyboard admin who pauses more than 60s and then presses
  // Enter on what they believe is Done gets nothing, and a plausible next
  // keystroke is Escape, which destroys the only copy of the credential.
  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // The 2s "Copied" timer must be cleared on unmount: Done unmounts this dialog,
  // and a pending setState on an unmounted component warns through console.error -
  // which the secrecy suite spies on.
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(t)
  }, [copied])

  async function copy() {
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
    } catch {
      // A denied permission is not worth logging, and logging the rejection risks
      // logging the argument that caused it. Fall back to the manual hint so the
      // admin is never left with a silently dead button.
      setCanCopy(false)
    }
  }

  return (
    <DialogShell
      titleId={titleId}
      onDismiss={onDone}
      dismissOnEscape={false}
      panelClassName="max-w-lg"
    >
      <div className="font-mono text-[10px] tracking-[0.18em] text-fg-mute">{endpoint}</div>
      <h2 id={titleId} className="mt-1 text-[17px] font-medium text-fg">
        {title}
      </h2>

      <div className="mt-4 rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[10.5px] leading-relaxed tracking-[0.04em] text-warn">
        ⚠ {warning ?? DEFAULT_WARNING}
      </div>

      {expiresAt && (
        <div className="mt-2 font-mono text-[10.5px] text-fg-dim">
          Expires {formatTimeUntil(expiresAt)}
        </div>
      )}

      <label
        htmlFor="reveal-token"
        className="mb-1 mt-4 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute"
      >
        Token
      </label>
      <input
        id="reveal-token"
        ref={inputRef}
        type="text"
        readOnly
        value={token}
        spellCheck={false}
        autoComplete="off"
        onFocus={(e) => e.currentTarget.select()}
        className="w-full rounded-[8px] border border-border bg-black/40 px-3 py-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
      />

      {canCopy ? (
        <div className="mt-2">
          <PillButton onClick={copy}>{copied ? 'Copied' : 'Copy'}</PillButton>
        </div>
      ) : (
        <div className="mt-2 text-[11px] text-fg-dim">
          Clipboard access needs HTTPS, so select the field above and copy it manually. The text
          is already selected.
        </div>
      )}

      <div className="mt-5 flex justify-end">
        <PillButton variant="primary" onClick={onDone}>
          Done - I have copied it
        </PillButton>
      </div>
    </DialogShell>
  )
}
