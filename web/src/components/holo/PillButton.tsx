import type { ButtonHTMLAttributes } from 'react'

// Compact pill action button for toolbars/headers (Enable/Disable/Drain/Edit/
// Rename/Revoke). Distinct from the full-width form Button; the two are not merged
// (they serve different roles). Class strings are literals.
const BASE = 'rounded-full px-4 py-2 text-[12px] tracking-[0.02em] backdrop-blur-[8px] disabled:opacity-40'

const VARIANTS = {
  primary: 'bg-gradient-to-r from-accent to-accent-b font-semibold text-bg',
  ghost: 'border border-border bg-white/5 text-fg',
  danger: 'border border-err/50 bg-err/10 text-err',
  muted: 'border border-fg-mute/50 bg-fg-mute/10 text-fg',
} as const

interface PillButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: keyof typeof VARIANTS
}

export function PillButton({ variant = 'ghost', className, ...rest }: PillButtonProps) {
  return <button type="button" {...rest} className={`${BASE} ${VARIANTS[variant]} ${className ?? ''}`} />
}
