import type { ReactNode } from 'react'

// Rounded pill for labels, tags, reservation selectors, and action pills. Renders
// a <button> when onClick is set, else a <span>. `dashed` is the "+ add label"
// affordance (overrides the tone border/fill). Class strings are literals.
const BASE = 'rounded-full px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em]'

const TONES = {
  accent: 'border border-accent/40 bg-accent/10 text-accent',
  muted: 'border border-border bg-white/[0.04] text-fg-mute',
  warn: 'border border-warn/40 bg-warn/10 text-warn',
  // Fourth tone, added for the invites STATUS pill: four derivable states need
  // four tones (web/src/admin/invites/inviteStatus.ts). Collapsing EXPIRED and
  // REDEEMED into `muted` would discard information the hi-fi deliberately
  // encodes - it uses C.err for expired and C.fgMute for redeemed
  // (design_handoff_relay_holo/hifi3-holo-pages.jsx:2101). The class string is
  // the error idiom already used at seven call sites (LoginScreen.tsx:62,
  // JobActions.tsx:65, UsersTable.tsx:25, ...), and PillButton.tsx:11 carries the
  // sibling `danger` tone, so this introduces no new palette.
  err: 'border border-err/40 bg-err/10 text-err',
} as const

const DASHED = 'border border-dashed border-border bg-transparent text-fg-mute cursor-pointer'

interface ChipProps {
  children: ReactNode
  tone?: keyof typeof TONES
  dashed?: boolean
  onClick?: () => void
}

export function Chip({ children, tone = 'accent', dashed, onClick }: ChipProps) {
  const cls = `${BASE} ${dashed ? DASHED : TONES[tone]}`
  if (onClick) {
    return (
      <button type="button" onClick={onClick} className={cls}>
        {children}
      </button>
    )
  }
  return <span className={cls}>{children}</span>
}
