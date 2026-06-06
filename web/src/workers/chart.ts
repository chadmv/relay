// Pure SVG path geometry for the telemetry charts. No React, no DOM - unit
// tested in isolation (mirrors liveness.ts).

export interface ChartPaths {
  line: string
  area: string
}

// Maps a series of values to an SVG line path and a filled area path inside a
// width x height box. y is inverted (0 at the top, height at the bottom). Values
// are clamped to [0, max]; a non-positive max maps everything to the baseline so
// the path never contains NaN. A single point is drawn as a flat line across the
// full width. An empty series yields empty strings so the caller can render an
// empty state instead.
export function chartPath(values: number[], width: number, height: number, max: number): ChartPaths {
  if (values.length === 0) return { line: '', area: '' }
  const pts = values.length === 1 ? [values[0], values[0]] : values
  const n = pts.length
  const dx = width / (n - 1)
  const y = (v: number): number => {
    if (max <= 0) return round(height)
    const clamped = Math.min(Math.max(v, 0), max)
    return round(height - (clamped / max) * height)
  }
  const coords = pts.map((v, i) => [round(i * dx), y(v)] as const)
  const line = coords.map(([x, yy], i) => `${i === 0 ? 'M' : 'L'}${x},${yy}`).join(' ')
  const first = coords[0][0]
  const lastX = coords[n - 1][0]
  const area = `${line} L${lastX},${round(height)} L${first},${round(height)} Z`
  return { line, area }
}

function round(n: number): number {
  return Math.round(n * 100) / 100
}
