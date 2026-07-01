import type { ReactNode } from 'react'
import { GlassPanel } from './GlassPanel'
import { Eyebrow } from './Eyebrow'
import { ProgressBar } from './ProgressBar'

// A KPI/stat block for the four-up row: eyebrow label, large mono value, optional
// inline progress bar, optional mono sub-line. Wraps GlassPanel. Class strings are
// literals.
interface KpiStatProps {
  label: ReactNode
  value: ReactNode
  sub?: ReactNode
  progress?: { used: number; max: number }
}

export function KpiStat({ label, value, sub, progress }: KpiStatProps) {
  return (
    <GlassPanel className="flex flex-col gap-1 p-3.5">
      <Eyebrow className="text-[10px] tracking-[0.16em]">{label}</Eyebrow>
      <div className="font-mono text-[22px] font-light tracking-[-0.01em] text-fg">{value}</div>
      {progress && <ProgressBar value={progress.used} max={progress.max} />}
      {sub && <div className="font-mono text-[10px] tracking-[0.04em] text-fg-mute">{sub}</div>}
    </GlassPanel>
  )
}
