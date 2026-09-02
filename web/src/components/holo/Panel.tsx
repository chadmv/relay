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
    // data-panel-title is inert. It exists so a page-level test can walk a table up
    // to the panel that wraps it and compare the rendered title with the rendered
    // accessible name, which is the only comparison that can catch the two drifting
    // apart. Omitted for a node title, which has no single string to publish.
    <GlassPanel
      className={`flex flex-col ${className ?? ''}`}
      data-panel-title={typeof title === 'string' ? title : undefined}
    >
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
