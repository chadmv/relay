import type { ReactNode } from 'react'

// The mono uppercase micro-label used above H1s (FLEET, RECURRING) and as section
// labels (LABELS, TELEMETRY). Uppercases via CSS, so callers pass normal-case text.
// Section-label variant: pass className="text-[10px] tracking-[0.16em]".
const BASE = 'font-mono text-[11px] uppercase tracking-[0.18em] text-fg-mute'

export function Eyebrow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={`${BASE} ${className ?? ''}`}>{children}</div>
}
