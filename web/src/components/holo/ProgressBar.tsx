// Thin gradient fill bar for slots, task progress, telemetry utilization. Width is
// dynamic data, so it is the one inline style; every other value is a class literal.
const TRACK = 'relative h-1 overflow-hidden rounded-[2px] bg-white/[0.08]'
const FILL_BASE = 'absolute inset-0 rounded-[2px]'
const FILL_ACCENT = 'bg-gradient-to-r from-accent to-accent-b'
const FILL_MUTED = 'bg-white/20'

interface ProgressBarProps {
  value: number
  max?: number
  className?: string
  tone?: 'accent' | 'muted'
}

export function ProgressBar({ value, max = 100, className, tone = 'accent' }: ProgressBarProps) {
  const raw = max > 0 ? (value / max) * 100 : 0
  const pct = Math.min(Math.max(raw, 0), 100)
  const fillTone = tone === 'muted' ? FILL_MUTED : FILL_ACCENT
  return (
    <div className={`${TRACK} ${className ?? ''}`}>
      <div data-testid="progress-fill" className={`${FILL_BASE} ${fillTone}`} style={{ width: `${pct}%` }} />
    </div>
  )
}
