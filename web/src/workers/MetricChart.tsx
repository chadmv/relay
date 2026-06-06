import { chartPath } from './chart'

const W = 300
const H = 60

// A small hand-rolled area+line chart. colorClass sets the line/fill color via
// currentColor so it stays on the Holo palette (e.g. "text-accent", "text-ok").
// An empty series renders just the frame; the caller decides whether to show a
// chart at all.
export function MetricChart({
  title,
  values,
  max,
  current,
  colorClass,
}: {
  title: string
  values: number[]
  max: number
  current: string
  colorClass: string
}) {
  const { line, area } = chartPath(values, W, H, max)
  return (
    <div className="rounded-card border border-border bg-white/5 p-3">
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-[10px] tracking-wider text-fg-mute">{title}</span>
        <span className="font-mono text-[12px] text-fg">{current}</span>
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className={`mt-2 h-16 w-full ${colorClass}`}
        role="img"
        aria-label={title}
      >
        {area && <path d={area} fill="currentColor" fillOpacity={0.15} />}
        {line && <path d={line} fill="none" stroke="currentColor" strokeWidth={1.5} />}
      </svg>
    </div>
  )
}
