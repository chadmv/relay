import type { ElementType, ReactNode } from 'react'

// The fundamental Holo container: translucent gradient surface, 1px border, blur,
// 14px radius, inset + drop shadow. Maps the prototype's glassPanel(C) onto the
// app's tokens. The gradient + shadow are the fidelity upgrade over the old flat
// `bg-white/5`. Pass `className` to override (e.g. a subtler nested surface).
// Class strings are literals so Tailwind v4 includes them.
const BASE =
  'rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] ' +
  'backdrop-blur-[8px] shadow-[inset_0_1px_0_rgba(255,255,255,0.08),0_8px_32px_rgba(0,0,0,0.4)]'

interface GlassPanelProps {
  as?: ElementType
  className?: string
  children?: ReactNode
  [prop: string]: unknown
}

export function GlassPanel({ as, className, children, ...rest }: GlassPanelProps) {
  const Tag = as ?? 'div'
  return (
    <Tag className={`${BASE} ${className ?? ''}`} {...rest}>
      {children}
    </Tag>
  )
}
