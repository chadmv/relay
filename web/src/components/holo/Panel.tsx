import type { ReactNode } from 'react'
import { GlassPanel } from './GlassPanel'

// A glass panel with a header row (title left, mono meta right) and an optional
// footer endnote. Composes GlassPanel. Used by Current tasks, Source workspaces,
// Utilization, etc. Class strings are literals.
interface PanelProps {
  title: ReactNode
  meta?: ReactNode
  footer?: ReactNode
  className?: string
  bodyClassName?: string
  children?: ReactNode
}

export function Panel({ title, meta, footer, className, bodyClassName, children }: PanelProps) {
  return (
    <GlassPanel className={`flex flex-col ${className ?? ''}`}>
      <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
        <span className="text-[13px] text-fg">{title}</span>
        {meta && <span className="font-mono text-[10px] tracking-[0.14em] text-fg-mute">{meta}</span>}
      </div>
      <div className={bodyClassName}>{children}</div>
      {footer && (
        <div className="mt-auto flex items-center justify-between border-t border-border px-4 py-2.5 font-mono text-[10px] tracking-[0.06em] text-fg-mute">
          {footer}
        </div>
      )}
    </GlassPanel>
  )
}
