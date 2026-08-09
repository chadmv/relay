import type { HealthResponse } from './api'

// HEALTHY means "the HTTP listener answered", nothing more: handleHealth
// (internal/api/health.go:5-7) performs NO database check. Do not derive this pill
// from the stat queries to make it "smarter" - a green pill next to two degraded
// stat sections is the CORRECT rendering of "server up, Postgres down", and
// ServerTab.test.tsx asserts exactly that.
//
// The error branch wins over stale data: a pill is a liveness claim, and a claim
// backed by a response from 30s ago that has since started failing is not one.
export function deriveHealthPill(
  data: HealthResponse | undefined,
  error: Error | null,
): { text: string; tone: string } {
  if (error) return { text: 'UNREACHABLE', tone: 'text-err' }
  if (!data) return { text: 'CHECKING', tone: 'text-fg-mute' }
  if (data.status === 'ok') return { text: 'HEALTHY', tone: 'text-ok' }
  // Unreachable in production today - handleHealth only ever writes "ok". It is one
  // ternary, and it means a future non-ok status shows up instead of being silently
  // rendered as HEALTHY.
  return { text: String(data.status).toUpperCase(), tone: 'text-warn' }
}

export function HealthPill({
  data,
  error,
}: {
  data: HealthResponse | undefined
  error: Error | null
}) {
  const { text, tone } = deriveHealthPill(data, error)
  return (
    <span className={`flex items-center gap-1.5 font-mono text-[10px] tracking-[0.14em] ${tone}`}>
      {/* The dot is its own node so the label is assertable as an exact string. */}
      <span aria-hidden="true">●</span>
      <span>{text}</span>
    </span>
  )
}
