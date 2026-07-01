import type { ReactNode } from 'react'

// Rounded pill for labels, tags, reservation selectors, and action pills. Renders
// a <button> when onClick is set, else a <span>. `dashed` is the "+ add label"
// affordance (overrides the tone border/fill). Class strings are literals.
const BASE = 'rounded-full px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em]'

const TONES = {
  accent: 'border border-accent/40 bg-accent/10 text-accent',
  muted: 'border border-border bg-white/[0.04] text-fg-mute',
  warn: 'border border-warn/40 bg-warn/10 text-warn',
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
