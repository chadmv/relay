import { useEffect, useId, useRef, useState } from 'react'
import { PillButton } from '../components/holo'

interface TokenRevealDialogProps {
  // The raw credential. MUST be passed straight from the mutation's data
  // (create.data.token) and never copied into caller state, so there is exactly
  // one retention site and the caller's reset() destroys it.
  token: string
  title: string
  // The endpoint that minted it, e.g. "POST /v1/agent-enrollments". Display only.
  endpoint: string
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
//  4. Backdrop click does NOT dismiss - there is deliberately no onClick on the
//     overlay (the hi-fi's AdminTokenModal has one at
//     design_handoff_relay_holo/hifi3-holo-pages.jsx:2345, which is fine for a
//     form and catastrophic for a secret). Escape DOES dismiss, preserving the
//     baseline of the two shipped dialogs.
//  5. a11y baseline copied from web/src/components/ConfirmDialog.tsx:36-46:
//     role="dialog", aria-modal, aria-labelledby the title, first field focused.
//     NO focus trap, same as ConfirmDialog and ResetPasswordDialog. This is the
//     THIRD un-trapped consumer and the worst one - the credential can be tabbed
//     past - so docs/backlog/idea-2026-07-01-confirmdialog-focus-trap-hardening.md
//     should land before a fourth.
export function TokenRevealDialog({
  token,
  title,
  endpoint,
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

  useEffect(() => {
    // Focus + select the token: satisfies "first field focused" and gives keyboard
    // users select-all for free, which is the manual copy path when the clipboard
    // API is unavailable.
    inputRef.current?.focus()
    inputRef.current?.select()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onDone()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onDone])

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
    // No onClick here. See invariant 4.
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="w-full max-w-lg rounded-card border border-border bg-bg p-5 shadow-xl"
      >
        <div className="font-mono text-[10px] tracking-[0.18em] text-fg-mute">{endpoint}</div>
        <h2 id={titleId} className="mt-1 text-[17px] font-medium text-fg">
          {title}
        </h2>

        <div className="mt-4 rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[10.5px] leading-relaxed tracking-[0.04em] text-warn">
          ⚠ {warning ?? DEFAULT_WARNING}
        </div>

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
      </div>
    </div>
  )
}
