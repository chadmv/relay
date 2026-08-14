import { Eyebrow, KpiStat } from '../../components/holo'
import { formatRelativeTime } from '../../lib/time'
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
  label: string
  // Query's dataUpdatedAt (ms epoch), so the stale line can say how old the
  // numbers on screen are rather than publishing an indefinitely-old value with
  // no indication of age.
  dataUpdatedAt: number
  onRetry: () => void
}

// A bare Eyebrow caption above a grid of KpiStats - NOT a Panel wrapping KpiStats.
// Both Panel and KpiStat wrap GlassPanel, and glass inside glass reads as a
// rendering bug in this palette. This is the WorkerDetailPage treatment
// (WorkerDetailPage.tsx:98-111), so the console inherits a look the app ships.
export function StatSection({
  caption,
  cells,
  error,
  label,
  dataUpdatedAt,
  onRetry,
}: StatSectionProps) {
  const hasData = cells.some((c) => c.value !== null)
  return (
    <div className="flex flex-col gap-2">
      <Eyebrow className="border-b border-border pb-1.5 text-[10px] tracking-[0.18em]">
        {caption}
      </Eyebrow>
      {error && !hasData ? (
        <ErrorStrip message={error.message} label={label} onRetry={onRetry} />
      ) : (
        <>
          {error && (
            // A poll failed after a good load. Keep the numbers, mark them stale -
            // with a 10s poll a single dropped request is the common case, and
            // blanking correct numbers for it is the worse failure. Announced via
            // aria-live so the fresh -> stale transition reaches assistive tech.
            <div
              role="status"
              aria-live="polite"
              className="font-mono text-[10px] tracking-[0.04em] text-warn"
            >
              stale · last update failed · {formatRelativeTime(new Date(dataUpdatedAt).toISOString())}
            </div>
          )}
          {/* Stacks below `md`, matching the ServerTab grid that lays these out. */}
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {cells.map((c) => (
              /* The wide cell's two-column span is gated behind the SAME
                 breakpoint as the container, not applied unconditionally: below
                 md the explicit grid has only ONE track, so an unconditional
                 span would force an implicit second track plus a gap-3 gutter,
                 rendering this card ~12px wider than its siblings - the ragged
                 layout Task 2 of
                 docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md
                 was meant to remove. From md up there are two tracks, where the
                 gated span correctly covers both, matching the original
                 desktop-only layout. Enforced by responsive.guard.test.ts. */
              <div key={c.label} className={c.wide ? 'md:col-span-2' : undefined}>
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
