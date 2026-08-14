import { Button } from '../../components/Button'
import { GlassPanel } from '../../components/holo'

// The in-place degraded body, shared by the two stat sections and the access panel.
// One component because the copy and the affordance must be identical in all three:
// a region that fails shows what failed and offers a way to try again, and NEVER a
// fabricated value. Same shape as the sibling tabs' error state
// (ReservationsTab.tsx's `error && !data` branch), scoped to a region rather than the page.
export function ErrorStrip({
  message,
  label,
  onRetry,
}: {
  message: string
  label: string
  onRetry: () => void
}) {
  return (
    <GlassPanel role="alert" className="flex flex-col items-start gap-2 p-4">
      <div className="text-[12px] text-err">{message}</div>
      <Button
        aria-label={`Retry ${label}`}
        className="w-auto px-3 py-1 text-[12px]"
        onClick={onRetry}
      >
        Retry
      </Button>
    </GlassPanel>
  )
}
