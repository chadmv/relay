import { Eyebrow, KpiStat } from '../../components/holo'
import { ErrorStrip } from './ErrorStrip'

// value === null means "no data yet" (first paint) - the cell renders an em dash so
// the fixed grid keeps its final size and does not reflow when data lands.
export interface StatCell {
  label: string
  value: number | null
  sub?: string
  wide?: boolean
}

interface StatSectionProps {
  caption: string
  cells: StatCell[]
  error: Error | null
  onRetry: () => void
}

// A bare Eyebrow caption above a grid of KpiStats - NOT a Panel wrapping KpiStats.
// Both Panel and KpiStat wrap GlassPanel, and glass inside glass reads as a
// rendering bug in this palette. This is the WorkerDetailPage treatment
// (WorkerDetailPage.tsx:98-111), so the console inherits a look the app ships.
export function StatSection({ caption, cells, error, onRetry }: StatSectionProps) {
  const hasData = cells.some((c) => c.value !== null)
  return (
    <div className="flex flex-col gap-2">
      <Eyebrow className="border-b border-border pb-1.5 text-[10px] tracking-[0.18em]">
        {caption}
      </Eyebrow>
      {error && !hasData ? (
        <ErrorStrip message={error.message} onRetry={onRetry} />
      ) : (
        <>
          {error && (
            // A poll failed after a good load. Keep the numbers, mark them stale -
            // with a 10s poll a single dropped request is the common case, and
            // blanking correct numbers for it is the worse failure.
            <div className="font-mono text-[10px] tracking-[0.04em] text-warn">
              stale · last update failed
            </div>
          )}
          <div className="grid grid-cols-2 gap-3">
            {cells.map((c) => (
              <div key={c.label} className={c.wide ? 'col-span-2' : undefined}>
                <KpiStat
                  label={c.label}
                  value={c.value === null ? '—' : c.value.toLocaleString()}
                  sub={c.sub}
                />
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
