# Holo Design Primitives (Slice 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the recurring Holo glass-panel / eyebrow / chip / KPI / status vocabulary into a shared, purely presentational primitive module under `web/src/components/holo/`, then adopt it in the already-shipped `WorkersPage` and `WorkersGrid` with no visual regression.

**Architecture:** Each primitive is one focused `.tsx` file that emits literal Tailwind class strings (mapped from the Holo prototype onto the existing `web/src/theme/tokens.css` tokens - no HSV machinery, no density switch, no inline styles except `ProgressBar`'s dynamic width). Primitives merge a caller `className` by string concatenation, matching the existing `Button`/`Input` pattern (there is no `clsx`/`cn` helper in this repo and this plan does not add one). `StatusDot` moves from `web/src/workers/` into the module (the one cross-file rename). Adoption swaps inline glass/eyebrow/chip markup in `WorkersPage` and `WorkersGrid` for the primitives, keeping their existing tests green.

**Tech Stack:** React 18 + TypeScript, Tailwind v4 (`@tailwindcss/vite`), Vitest + React Testing Library + jsdom. Tests run with `cd web && npm test` (which is `vitest run`).

---

## Slice independence

**This is a FRONTEND-ONLY slice.** It touches only `web/src/` - no Go, no `.sql`, no `.proto`, no `make generate`. There is no backend counterpart, so backend/frontend parallelism does not apply here: this whole plan runs as the frontend slice.

## Scope guardrails

- **Slice 1 only.** This plan builds the primitives and adopts them in `WorkersPage` + `WorkersGrid`. The worker-detail relayout (`WorkerDetailPage` body, KPI row, two-column body, placeholders, action-bar reposition) is **Slice 2**, a separate plan commissioned after this merges. Do NOT restyle `WorkerDetailPage`'s body here.
- **The one exception in `WorkerDetailPage`:** the `StatusDot` import path must change when the file moves (Task 9). That is a mechanical import-path edit only - no layout changes to that page in this slice.
- **`Spark` is deferred** (spec Open Decisions #3). Do NOT build it. The barrel does not export it.
- **`PillButton` is built as a primitive** (it is part of the module the spec defines) but it is NOT adopted anywhere in Slice 1 - the pages that use it (`WorkerActions`, detail header) are Slice 2. Slice 1 ships it with its own render test so Slice 2 can consume it. It is not dead code at the module level (it has a test and a barrel export); it just has no page consumer yet, by design.
- **No visual regression on adoption.** The single intentional visual change is that adopted glass surfaces gain the Holo gradient + inset/drop shadow (that is the fidelity upgrade baked into `GlassPanel`). Everything else - spacing, text, layout, hover, dim behavior - stays pixel-identical. Existing `WorkersPage.test.tsx` and `WorkersGrid.test.tsx` assertions must keep passing (adjust only import paths or structural queries if strictly forced).
- **Never edit** `*.sql`, `*.sql.go`, `models.go`, `*.proto` - none are in scope.

## Conventions to follow (verified in this codebase)

- **className merge = string concatenation.** `Button.tsx` and `Input.tsx` do `'<base> ' + (props.className ?? '')`. Follow that exactly. Do NOT add `clsx`/`tailwind-merge`.
- **Class strings are literals** so Tailwind v4's scanner includes them (see `liveness.ts` comment). Never build class names by interpolation of dynamic segments.
- **Test imports.** Even though `vitest` runs with `globals: true`, existing tests still `import { expect, test } from 'vitest'`. Match that. Component render tests import `render, screen` from `@testing-library/react`.
- **Token classes** already exist: `bg-bg text-fg text-fg-mute text-fg-dim border-border text-ok bg-ok text-warn bg-warn text-err bg-err text-accent bg-accent text-accent-b bg-accent-b rounded-card rounded-input font-mono`. Use them; do not add new tokens to `tokens.css`.

## File structure

New module (all new files):

```
web/src/components/holo/
  GlassPanel.tsx     purpose: translucent gradient glass surface (the base container)
  Eyebrow.tsx        purpose: mono uppercase micro-label
  ProgressBar.tsx    purpose: thin gradient fill bar (dynamic width)
  Chip.tsx           purpose: rounded pill for labels/tags/actions (tones + dashed)
  PillButton.tsx     purpose: compact pill action button (variants) - built, not yet adopted
  KpiStat.tsx        purpose: KPI stat block (label / value / optional progress / sub)
  Panel.tsx          purpose: GlassPanel + header row + optional footer endnote
  StatusDot.tsx      purpose: MOVED from web/src/workers/StatusDot.tsx (dot + mono status)
  index.ts           purpose: barrel re-export of all of the above (no Spark)
  <one .test.tsx per primitive above, except index.ts>
```

Files modified for the StatusDot move (Task 9):
- `web/src/workers/StatusDot.tsx` - deleted (content moves into holo/)
- `web/src/workers/WorkersGrid.tsx` - import path
- `web/src/workers/WorkersTable.tsx` - import path
- `web/src/workers/WorkerDetailPage.tsx` - import path

Files modified for adoption (Tasks 10-11):
- `web/src/workers/WorkersGrid.tsx` - consume `GlassPanel` (via `Link` as), `StatusDot`, `Chip`
- `web/src/workers/WorkersPage.tsx` - consume `Eyebrow`, `GlassPanel` for error/empty/skeleton boxes

### Import-collision sequencing

Every primitive is its own new file (Tasks 1-8), so they never collide. The one cross-file rename (`StatusDot` move + 3 import updates) is isolated in Task 9. Adoption edits to the two shipped worker files are the last two tasks (10-11), each touching one file, so they do not collide with each other or with the primitive tasks. Run tasks in numeric order.

---

## Task 1: GlassPanel primitive

**Files:**
- Create: `web/src/components/holo/GlassPanel.tsx`
- Test: `web/src/components/holo/GlassPanel.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/components/holo/GlassPanel.test.tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { GlassPanel } from './GlassPanel'

test('renders children inside a div by default with the glass base classes', () => {
  render(<GlassPanel>hello</GlassPanel>)
  const el = screen.getByText('hello')
  expect(el.tagName).toBe('DIV')
  expect(el).toHaveClass('rounded-card', 'border', 'border-border', 'backdrop-blur-[8px]')
})

test('merges a caller className after the base classes', () => {
  render(<GlassPanel className="bg-black/25">x</GlassPanel>)
  expect(screen.getByText('x')).toHaveClass('bg-black/25')
})

test('renders as the element named by `as`', () => {
  render(
    <GlassPanel as="section" className="p-4">
      s
    </GlassPanel>,
  )
  expect(screen.getByText('s').tagName).toBe('SECTION')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/GlassPanel.test.tsx`
Expected: FAIL - cannot resolve `./GlassPanel`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/holo/GlassPanel.tsx
import type { ElementType, ReactNode } from 'react'

// The fundamental Holo container: translucent gradient surface, 1px border, blur,
// 14px radius, inset + drop shadow. Maps the prototype's glassPanel(C) onto the
// app's tokens. The gradient + shadow are the fidelity upgrade over the old flat
// `bg-white/5`. Pass `className` to override (e.g. a subtler nested surface).
// Class strings are literals so Tailwind v4 includes them.
const BASE =
  'rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] ' +
  'backdrop-blur-[8px] shadow-[inset_0_1px_0_rgba(255,255,255,0.08),0_8px_32px_rgba(0,0,0,0.4)]'

interface GlassPanelProps {
  as?: ElementType
  className?: string
  children?: ReactNode
}

export function GlassPanel({ as, className, children }: GlassPanelProps) {
  const Tag = as ?? 'div'
  return <Tag className={`${BASE} ${className ?? ''}`}>{children}</Tag>
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/holo/GlassPanel.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/GlassPanel.tsx web/src/components/holo/GlassPanel.test.tsx
git commit -m "feat(web): add GlassPanel holo primitive"
```

---

## Task 2: Eyebrow primitive

**Files:**
- Create: `web/src/components/holo/Eyebrow.tsx`
- Test: `web/src/components/holo/Eyebrow.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/components/holo/Eyebrow.test.tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { Eyebrow } from './Eyebrow'

test('renders children with the mono uppercase eyebrow classes', () => {
  render(<Eyebrow>fleet</Eyebrow>)
  const el = screen.getByText('fleet')
  expect(el).toHaveClass('font-mono', 'uppercase', 'text-fg-mute', 'tracking-[0.18em]')
})

test('merges a caller className for the section-label variant', () => {
  render(<Eyebrow className="text-[10px] tracking-[0.16em]">labels</Eyebrow>)
  expect(screen.getByText('labels')).toHaveClass('text-[10px]', 'tracking-[0.16em]')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/Eyebrow.test.tsx`
Expected: FAIL - cannot resolve `./Eyebrow`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/holo/Eyebrow.tsx
import type { ReactNode } from 'react'

// The mono uppercase micro-label used above H1s (FLEET, RECURRING) and as section
// labels (LABELS, TELEMETRY). Uppercases via CSS, so callers pass normal-case text.
// Section-label variant: pass className="text-[10px] tracking-[0.16em]".
const BASE = 'font-mono text-[11px] uppercase tracking-[0.18em] text-fg-mute'

export function Eyebrow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={`${BASE} ${className ?? ''}`}>{children}</div>
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/holo/Eyebrow.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/Eyebrow.tsx web/src/components/holo/Eyebrow.test.tsx
git commit -m "feat(web): add Eyebrow holo primitive"
```

---

## Task 3: ProgressBar primitive

**Files:**
- Create: `web/src/components/holo/ProgressBar.tsx`
- Test: `web/src/components/holo/ProgressBar.test.tsx`

- [ ] **Step 1: Write the failing test**

`value/max` maps to a percent width applied as an inline style (dynamic data is the one allowed inline value; everything else is a class). Percent is clamped to `[0, 100]`.

```tsx
// web/src/components/holo/ProgressBar.test.tsx
import { render } from '@testing-library/react'
import { expect, test } from 'vitest'
import { ProgressBar } from './ProgressBar'

function fill(container: HTMLElement): HTMLElement {
  const el = container.querySelector('[data-testid="progress-fill"]')
  if (!(el instanceof HTMLElement)) throw new Error('fill not found')
  return el
}

test('sets the fill width from value/max as a percentage', () => {
  const { container } = render(<ProgressBar value={2} max={4} />)
  expect(fill(container).style.width).toBe('50%')
})

test('defaults max to 100 when omitted', () => {
  const { container } = render(<ProgressBar value={30} />)
  expect(fill(container).style.width).toBe('30%')
})

test('clamps out-of-range values to 0..100', () => {
  const { container: hi } = render(<ProgressBar value={9} max={4} />)
  expect(fill(hi).style.width).toBe('100%')
  const { container: lo } = render(<ProgressBar value={-1} max={4} />)
  expect(fill(lo).style.width).toBe('0%')
})

test('renders the accent gradient fill by default and a muted fill via tone', () => {
  const { container } = render(<ProgressBar value={1} max={4} tone="muted" />)
  expect(fill(container)).toHaveClass('bg-white/20')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/ProgressBar.test.tsx`
Expected: FAIL - cannot resolve `./ProgressBar`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/holo/ProgressBar.tsx
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/holo/ProgressBar.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/ProgressBar.tsx web/src/components/holo/ProgressBar.test.tsx
git commit -m "feat(web): add ProgressBar holo primitive"
```

---

## Task 4: Chip primitive

**Files:**
- Create: `web/src/components/holo/Chip.tsx`
- Test: `web/src/components/holo/Chip.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/components/holo/Chip.test.tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { Chip } from './Chip'

test('defaults to the accent tone', () => {
  render(<Chip>pool=render</Chip>)
  const el = screen.getByText('pool=render')
  expect(el).toHaveClass('rounded-full', 'border-accent/40', 'bg-accent/10', 'text-accent')
})

test('muted tone uses the border/muted palette', () => {
  render(<Chip tone="muted">HELD</Chip>)
  expect(screen.getByText('HELD')).toHaveClass('border-border', 'text-fg-mute')
})

test('warn tone uses the warn palette', () => {
  render(<Chip tone="warn">draining</Chip>)
  expect(screen.getByText('draining')).toHaveClass('border-warn/40', 'bg-warn/10', 'text-warn')
})

test('dashed renders a dashed transparent affordance', () => {
  render(<Chip dashed>+ add label</Chip>)
  expect(screen.getByText('+ add label')).toHaveClass('border-dashed', 'bg-transparent', 'cursor-pointer')
})

test('is a button when onClick is provided and fires it', async () => {
  const onClick = vi.fn()
  render(<Chip onClick={onClick}>EVICT</Chip>)
  const el = screen.getByRole('button', { name: 'EVICT' })
  await userEvent.click(el)
  expect(onClick).toHaveBeenCalledOnce()
})

test('is a span when no onClick is provided', () => {
  render(<Chip>label</Chip>)
  expect(screen.getByText('label').tagName).toBe('SPAN')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/Chip.test.tsx`
Expected: FAIL - cannot resolve `./Chip`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/holo/Chip.tsx
import type { ReactNode } from 'react'

// Rounded pill for labels, tags, reservation selectors, and action pills. Renders
// a <button> when onClick is set, else a <span>. `dashed` is the "+ add label"
// affordance (overrides the tone border/fill). Class strings are literals.
const BASE = 'rounded-full px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em]'

const TONES = {
  accent: 'border border-accent/40 bg-accent/10 text-accent',
  muted: 'border border-border bg-white/[0.04] text-fg-mute',
  warn: 'border border-warn/40 bg-warn/10 text-warn',
} as const

const DASHED = 'border border-dashed border-border bg-transparent text-fg-mute cursor-pointer'

interface ChipProps {
  children: ReactNode
  tone?: keyof typeof TONES
  dashed?: boolean
  onClick?: () => void
}

export function Chip({ children, tone = 'accent', dashed, onClick }: ChipProps) {
  const cls = `${BASE} ${dashed ? DASHED : TONES[tone]}`
  if (onClick) {
    return (
      <button type="button" onClick={onClick} className={cls}>
        {children}
      </button>
    )
  }
  return <span className={cls}>{children}</span>
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/holo/Chip.test.tsx`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/Chip.tsx web/src/components/holo/Chip.test.tsx
git commit -m "feat(web): add Chip holo primitive"
```

---

## Task 5: PillButton primitive

**Files:**
- Create: `web/src/components/holo/PillButton.tsx`
- Test: `web/src/components/holo/PillButton.test.tsx`

Note: `PillButton` is compact pill actions for toolbars/headers. It is DISTINCT from the full-width rectangular form `Button` (`web/src/components/Button.tsx`) - this plan does not merge them (spec Open Decisions #2). It spreads standard button attributes so callers can pass `onClick`, `disabled`, `aria-*`, etc.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/components/holo/PillButton.test.tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { PillButton } from './PillButton'

test('defaults to the ghost variant and pill base classes', () => {
  render(<PillButton>Drain</PillButton>)
  const el = screen.getByRole('button', { name: 'Drain' })
  expect(el).toHaveClass('rounded-full', 'border', 'border-border', 'bg-white/5', 'text-fg')
})

test('primary variant uses the accent gradient', () => {
  render(<PillButton variant="primary">Save</PillButton>)
  expect(screen.getByRole('button', { name: 'Save' })).toHaveClass('from-accent', 'to-accent-b', 'text-bg')
})

test('danger variant uses the err palette', () => {
  render(<PillButton variant="danger">Revoke</PillButton>)
  expect(screen.getByRole('button', { name: 'Revoke' })).toHaveClass('border-err/50', 'bg-err/10', 'text-err')
})

test('forwards standard button attributes (onClick, disabled)', async () => {
  const onClick = vi.fn()
  render(
    <PillButton onClick={onClick} disabled>
      Edit
    </PillButton>,
  )
  const el = screen.getByRole('button', { name: 'Edit' })
  expect(el).toBeDisabled()
  await userEvent.click(el)
  expect(onClick).not.toHaveBeenCalled()
})

test('merges a caller className', () => {
  render(<PillButton className="ml-2">X</PillButton>)
  expect(screen.getByRole('button', { name: 'X' })).toHaveClass('ml-2')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/PillButton.test.tsx`
Expected: FAIL - cannot resolve `./PillButton`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/holo/PillButton.tsx
import type { ButtonHTMLAttributes } from 'react'

// Compact pill action button for toolbars/headers (Enable/Disable/Drain/Edit/
// Rename/Revoke). Distinct from the full-width form Button; the two are not merged
// (they serve different roles). Class strings are literals.
const BASE = 'rounded-full px-4 py-2 text-[12px] tracking-[0.02em] backdrop-blur-[8px] disabled:opacity-40'

const VARIANTS = {
  primary: 'bg-gradient-to-r from-accent to-accent-b font-semibold text-bg',
  ghost: 'border border-border bg-white/5 text-fg',
  danger: 'border border-err/50 bg-err/10 text-err',
  muted: 'border border-fg-mute/50 bg-fg-mute/10 text-fg',
} as const

interface PillButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: keyof typeof VARIANTS
}

export function PillButton({ variant = 'ghost', className, ...rest }: PillButtonProps) {
  return <button type="button" {...rest} className={`${BASE} ${VARIANTS[variant]} ${className ?? ''}`} />
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/holo/PillButton.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/PillButton.tsx web/src/components/holo/PillButton.test.tsx
git commit -m "feat(web): add PillButton holo primitive"
```

---

## Task 6: KpiStat primitive

**Files:**
- Create: `web/src/components/holo/KpiStat.tsx`
- Test: `web/src/components/holo/KpiStat.test.tsx`

Depends on `GlassPanel` (Task 1), `Eyebrow` (Task 2), and `ProgressBar` (Task 3), all already built.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/components/holo/KpiStat.test.tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { KpiStat } from './KpiStat'

test('renders the label, value, and optional sub', () => {
  render(<KpiStat label="CPU · RAM" value="16c · 128G" sub="os: linux" />)
  expect(screen.getByText('CPU · RAM')).toBeInTheDocument()
  expect(screen.getByText('16c · 128G')).toHaveClass('font-mono', 'text-[22px]', 'text-fg')
  expect(screen.getByText('os: linux')).toHaveClass('font-mono', 'text-[10px]', 'text-fg-mute')
})

test('omits the sub line when not provided', () => {
  render(<KpiStat label="GPU" value="No GPU" />)
  expect(screen.getByText('GPU')).toBeInTheDocument()
  expect(screen.getByText('No GPU')).toBeInTheDocument()
})

test('renders a progress bar when progress is provided', () => {
  const { container } = render(<KpiStat label="Slots" value="2/4" progress={{ used: 2, max: 4 }} />)
  const fill = container.querySelector('[data-testid="progress-fill"]')
  expect(fill).not.toBeNull()
  expect((fill as HTMLElement).style.width).toBe('50%')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/KpiStat.test.tsx`
Expected: FAIL - cannot resolve `./KpiStat`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/holo/KpiStat.tsx
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/holo/KpiStat.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/KpiStat.tsx web/src/components/holo/KpiStat.test.tsx
git commit -m "feat(web): add KpiStat holo primitive"
```

---

## Task 7: Panel primitive

**Files:**
- Create: `web/src/components/holo/Panel.tsx`
- Test: `web/src/components/holo/Panel.test.tsx`

Depends on `GlassPanel` (Task 1). A glass panel with a header row (title left, mono `meta` right) and an optional footer endnote.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/components/holo/Panel.test.tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { Panel } from './Panel'

test('renders the title, optional meta, and body', () => {
  render(
    <Panel title="Source workspaces" meta="2 OF 4 SLOTS">
      <div>body content</div>
    </Panel>,
  )
  expect(screen.getByText('Source workspaces')).toHaveClass('text-[13px]', 'text-fg')
  expect(screen.getByText('2 OF 4 SLOTS')).toHaveClass('font-mono', 'text-[10px]', 'text-fg-mute')
  expect(screen.getByText('body content')).toBeInTheDocument()
})

test('omits the footer when not provided', () => {
  render(
    <Panel title="Labels">
      <div>b</div>
    </Panel>,
  )
  expect(screen.queryByText('endnote')).toBeNull()
})

test('renders a footer endnote when provided', () => {
  render(
    <Panel title="Utilization" footer={<span>endnote</span>}>
      <div>b</div>
    </Panel>,
  )
  expect(screen.getByText('endnote')).toBeInTheDocument()
})

test('applies bodyClassName to the body wrapper', () => {
  render(
    <Panel title="t" bodyClassName="p-4">
      <div>body</div>
    </Panel>,
  )
  expect(screen.getByText('body').parentElement).toHaveClass('p-4')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/Panel.test.tsx`
Expected: FAIL - cannot resolve `./Panel`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/holo/Panel.tsx
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/holo/Panel.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/Panel.tsx web/src/components/holo/Panel.test.tsx
git commit -m "feat(web): add Panel holo primitive"
```

---

## Task 8: Barrel index (no Spark, no StatusDot yet)

**Files:**
- Create: `web/src/components/holo/index.ts`
- Test: `web/src/components/holo/index.test.ts`

This barrel exports the seven primitives built so far. `StatusDot` is added to it in Task 9 (after the move), and `Spark` is intentionally excluded (deferred). A tiny test guards the export surface so a later refactor cannot silently drop a name.

- [ ] **Step 1: Write the failing test**

```ts
// web/src/components/holo/index.test.ts
import { expect, test } from 'vitest'
import * as holo from './index'

test('barrel re-exports the built primitives', () => {
  expect(typeof holo.GlassPanel).toBe('function')
  expect(typeof holo.Eyebrow).toBe('function')
  expect(typeof holo.ProgressBar).toBe('function')
  expect(typeof holo.Chip).toBe('function')
  expect(typeof holo.PillButton).toBe('function')
  expect(typeof holo.KpiStat).toBe('function')
  expect(typeof holo.Panel).toBe('function')
})

test('does not export the deferred Spark primitive', () => {
  expect('Spark' in holo).toBe(false)
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/index.test.ts`
Expected: FAIL - cannot resolve `./index`.

- [ ] **Step 3: Write minimal implementation**

```ts
// web/src/components/holo/index.ts
// Barrel for the Holo presentational primitives. Spark is deferred (not built).
// StatusDot is added here in the task that moves it into this module.
export { GlassPanel } from './GlassPanel'
export { Eyebrow } from './Eyebrow'
export { ProgressBar } from './ProgressBar'
export { Chip } from './Chip'
export { PillButton } from './PillButton'
export { KpiStat } from './KpiStat'
export { Panel } from './Panel'
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/holo/index.test.ts`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/holo/index.ts web/src/components/holo/index.test.ts
git commit -m "feat(web): add holo primitives barrel"
```

---

## Task 9: Move StatusDot into the holo module (the one cross-file rename)

**Files:**
- Create: `web/src/components/holo/StatusDot.tsx` (moved content, verbatim, with the relative import fixed)
- Modify: `web/src/components/holo/index.ts` - add the `StatusDot` export
- Delete: `web/src/workers/StatusDot.tsx`
- Modify: `web/src/workers/WorkersGrid.tsx:2` - import path
- Modify: `web/src/workers/WorkersTable.tsx:2` - import path
- Modify: `web/src/workers/WorkerDetailPage.tsx:6` - import path

This is the single cross-file rename in Slice 1. `StatusDot` keeps its `livenessView`-based API unchanged; only its location and its import of `liveness`/`api` change (those files live in `web/src/workers/`, so from `web/src/components/holo/` the path becomes `../../workers/...`). The three consumers update their import from `./StatusDot` to `../components/holo/StatusDot`.

There is no dedicated `StatusDot` test today (it is exercised via `WorkersGrid.test.tsx` and `WorkersTable.test.tsx`), so the safety net for this task is that those consumer tests plus the full suite stay green. Add one small dedicated render test to the new location while we are here.

- [ ] **Step 1: Write the failing test at the new location**

```tsx
// web/src/components/holo/StatusDot.test.tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { StatusDot } from './StatusDot'

test('renders the mono status label for a worker status', () => {
  render(<StatusDot status="online" />)
  expect(screen.getByText('ONLINE')).toHaveClass('font-mono', 'text-ok')
})

test('renders the offline label', () => {
  render(<StatusDot status="offline" />)
  expect(screen.getByText('OFFLINE')).toHaveClass('text-err')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/holo/StatusDot.test.tsx`
Expected: FAIL - cannot resolve `./StatusDot`.

- [ ] **Step 3: Create the moved file and update imports**

Create `web/src/components/holo/StatusDot.tsx` (content identical to the old file; only the relative import paths change, since `liveness` and `api` remain in `web/src/workers/`):

```tsx
// web/src/components/holo/StatusDot.tsx
import { livenessView } from '../../workers/liveness'
import type { WorkerStatus } from '../../workers/api'

// The dot + mono status label. Moved here from web/src/workers/ so non-worker
// pages can use it. Keeps the livenessView-based API (worker statuses are the only
// consumer today).
export function StatusDot({ status }: { status: WorkerStatus }) {
  const v = livenessView(status)
  return (
    <span className={`inline-flex items-center gap-1.5 font-mono text-[10px] tracking-wider ${v.textClass}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${v.dotClass}`} />
      {v.label}
    </span>
  )
}
```

Delete the old file:

```bash
git rm web/src/workers/StatusDot.tsx
```

Add the export to the barrel - append this line to `web/src/components/holo/index.ts`:

```ts
export { StatusDot } from './StatusDot'
```

Update `web/src/workers/WorkersGrid.tsx` line 2 - change:

```tsx
import { StatusDot } from './StatusDot'
```

to:

```tsx
import { StatusDot } from '../components/holo/StatusDot'
```

Update `web/src/workers/WorkersTable.tsx` line 2 - change:

```tsx
import { StatusDot } from './StatusDot'
```

to:

```tsx
import { StatusDot } from '../components/holo/StatusDot'
```

Update `web/src/workers/WorkerDetailPage.tsx` line 6 - change:

```tsx
import { StatusDot } from './StatusDot'
```

to:

```tsx
import { StatusDot } from '../components/holo/StatusDot'
```

- [ ] **Step 4: Run the moved test, the consumer tests, and typecheck**

Run: `cd web && npx vitest run src/components/holo/StatusDot.test.tsx src/workers/WorkersGrid.test.tsx src/workers/WorkersTable.test.tsx`
Expected: PASS (all).

Run: `cd web && npx tsc -b`
Expected: no errors (proves no stale `./StatusDot` import remains anywhere).

- [ ] **Step 5: Update the barrel-export test and confirm**

Append to `web/src/components/holo/index.test.ts`'s first test body (inside the same `test('barrel re-exports the built primitives', ...)`):

```ts
  expect(typeof holo.StatusDot).toBe('function')
```

Run: `cd web && npx vitest run src/components/holo/index.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/holo/StatusDot.tsx web/src/components/holo/StatusDot.test.tsx web/src/components/holo/index.ts web/src/components/holo/index.test.ts web/src/workers/WorkersGrid.tsx web/src/workers/WorkersTable.tsx web/src/workers/WorkerDetailPage.tsx
git rm web/src/workers/StatusDot.tsx
git commit -m "refactor(web): move StatusDot into holo primitives module"
```

---

## Task 10: Adopt primitives in WorkersGrid (no visual regression)

**Files:**
- Modify: `web/src/workers/WorkersGrid.tsx`
- Test (existing, must stay green): `web/src/workers/WorkersGrid.test.tsx`

Refactor `WorkersGrid` to consume `GlassPanel` (as the `Link` element via `as`) and `Chip` for the label chips. The `StatusDot` import already updated in Task 9. **No visual regression:** the card keeps its `p-4`, hover, and dim classes; the only intentional change is the glass gradient/shadow now coming from `GlassPanel`. The card previously used `bg-white/5 backdrop-blur`; `GlassPanel` supplies `rounded-card border border-border` + gradient + blur + shadow, so those are removed from the inline string and the card-specific classes (`block`, `p-4`, `transition`, hover, dim) are passed via `className`.

The existing tests assert text presence (`render-01`, `ONLINE`, `16c · 128GB · RTX 4090`, `4 slots`, `pool=render`), the dim class (`.opacity-\\[0\\.55\\]`), and the link href. All of these must keep passing: the label chip text `pool=render` still renders (now inside `Chip`), the dim class still applies (passed through `className`), and the `Link` still renders an `<a>` with the href (via `GlassPanel as={Link}`).

- [ ] **Step 1: Add a regression-guard test asserting the label chip renders as an accent Chip**

Add this test to `web/src/workers/WorkersGrid.test.tsx` (it locks the adoption in: the chip must carry the accent Chip class and the dim class must survive):

```tsx
test('label chips render as accent holo chips', () => {
  renderGrid([worker({})])
  expect(screen.getByText('pool=render')).toHaveClass('rounded-full', 'text-accent')
})

test('cards still dim offline workers after adopting GlassPanel', () => {
  const { container } = renderGrid([worker({ id: 'o', name: 'off-01', status: 'offline' })])
  expect(container.querySelector('.opacity-\\[0\\.55\\]')).not.toBeNull()
})
```

- [ ] **Step 2: Run the new tests to verify they fail (chip not yet accent-classed via Chip)**

Run: `cd web && npx vitest run src/workers/WorkersGrid.test.tsx -t "accent holo chips"`
Expected: FAIL - the current inline chip uses `text-[9.5px]` accent classes but the assertion `rounded-full` + `text-accent` should already pass on the OLD markup; to make this a true RED, note the old chip DOES have `rounded-full` and `text-accent`. So instead assert a property only the Chip primitive produces: its base font size `text-[10.5px]`.

Replace the first new test with:

```tsx
test('label chips render as accent holo chips', () => {
  renderGrid([worker({})])
  expect(screen.getByText('pool=render')).toHaveClass('rounded-full', 'text-accent', 'text-[10.5px]')
})
```

Run: `cd web && npx vitest run src/workers/WorkersGrid.test.tsx -t "accent holo chips"`
Expected: FAIL - the old inline chip uses `text-[9.5px]`, not the Chip primitive's `text-[10.5px]`.

- [ ] **Step 3: Refactor WorkersGrid to consume the primitives**

Replace the full contents of `web/src/workers/WorkersGrid.tsx` with:

```tsx
import { Link } from 'react-router-dom'
import { Chip, GlassPanel, StatusDot } from '../components/holo'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker } from './api'

export function WorkersGrid({ workers }: { workers: Worker[] }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
      {workers.map((w) => (
        <GlassPanel
          as={Link}
          key={w.id}
          to={`/workers/${w.id}`}
          className={`block p-4 transition hover:border-accent/50 ${livenessView(w.status).dimClass}`}
        >
          <div className="mb-2 flex items-baseline justify-between">
            <span className="font-mono text-[13px] text-fg">{w.name}</span>
            <StatusDot status={w.status} />
          </div>
          <div className="mb-2 font-mono text-[11px] text-fg-mute">{w.max_slots} slots</div>
          {labelChips(w.labels).length > 0 && (
            <div className="mb-2 flex flex-wrap gap-1">
              {labelChips(w.labels).map((c) => (
                <Chip key={c}>{c}</Chip>
              ))}
            </div>
          )}
          <div className="mt-2 flex justify-between border-t border-border pt-2 font-mono text-[10px] text-fg-mute">
            <span>{specLine(w)}</span>
            <span>{w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}</span>
          </div>
        </GlassPanel>
      ))}
    </div>
  )
}
```

Note: `GlassPanel`'s current signature (Task 1) accepts only `as`, `className`, `children`. To pass `to`/`key` through to the `Link`, `GlassPanel` must forward arbitrary props. Update `GlassPanel` to spread the rest - see Step 4.

- [ ] **Step 4: Extend GlassPanel to forward extra props (needed for `as={Link}` with `to`)**

Update `web/src/components/holo/GlassPanel.tsx` to spread remaining props onto the rendered element:

```tsx
// web/src/components/holo/GlassPanel.tsx
import type { ElementType, ReactNode } from 'react'

const BASE =
  'rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] ' +
  'backdrop-blur-[8px] shadow-[inset_0_1px_0_rgba(255,255,255,0.08),0_8px_32px_rgba(0,0,0,0.4)]'

interface GlassPanelProps {
  as?: ElementType
  className?: string
  children?: ReactNode
  [prop: string]: unknown
}

export function GlassPanel({ as, className, children, ...rest }: GlassPanelProps) {
  const Tag = as ?? 'div'
  return (
    <Tag className={`${BASE} ${className ?? ''}`} {...rest}>
      {children}
    </Tag>
  )
}
```

- [ ] **Step 5: Run the WorkersGrid suite, the GlassPanel suite, and typecheck**

Run: `cd web && npx vitest run src/workers/WorkersGrid.test.tsx src/components/holo/GlassPanel.test.tsx`
Expected: PASS (all - original grid tests, the two new adoption tests, and the GlassPanel tests including the `as`/props-forwarding path).

Run: `cd web && npx tsc -b`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/workers/WorkersGrid.tsx web/src/workers/WorkersGrid.test.tsx web/src/components/holo/GlassPanel.tsx
git commit -m "refactor(web): adopt holo primitives in WorkersGrid"
```

---

## Task 11: Adopt primitives in WorkersPage (no visual regression)

**Files:**
- Modify: `web/src/workers/WorkersPage.tsx`
- Test (existing, must stay green): `web/src/workers/WorkersPage.test.tsx`

Refactor `WorkersPage` to consume `Eyebrow` for the `FLEET` eyebrow (both the `header` block and the inline duplicate in the main return) and `GlassPanel` for the three inline glass boxes: the loading skeleton cards, the error card, and the empty-state card, plus the decommissioned-section error card. **No visual regression:** each box keeps its exact padding/margin/text classes; only the flat `bg-white/5` surface upgrades to the glass gradient via `GlassPanel`. The `FLEET` eyebrow text and the `2 workers`/error/empty copy are unchanged, so all existing `WorkersPage.test.tsx` assertions keep passing.

The eyebrow currently reads `font-mono text-[11px] tracking-widest text-fg-mute`. `Eyebrow`'s base is `font-mono text-[11px] uppercase tracking-[0.18em] text-fg-mute` and the text is already uppercase (`FLEET`), so switching is a no-visible-change swap (uppercase on already-uppercase text is a no-op; `tracking-widest` -> `tracking-[0.18em]` is the deliberate token unification).

- [ ] **Step 1: Add a regression-guard test asserting the FLEET eyebrow uses the Eyebrow primitive**

Add to `web/src/workers/WorkersPage.test.tsx`:

```tsx
test('the FLEET eyebrow renders via the holo Eyebrow primitive', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  renderPage()
  await screen.findByText('render-01')
  expect(screen.getByText('FLEET')).toHaveClass('uppercase', 'tracking-[0.18em]')
})
```

- [ ] **Step 2: Run the new test to verify it fails**

Run: `cd web && npx vitest run src/workers/WorkersPage.test.tsx -t "holo Eyebrow primitive"`
Expected: FAIL - the current eyebrow uses `tracking-widest`, not `uppercase tracking-[0.18em]`.

- [ ] **Step 3: Refactor WorkersPage to consume Eyebrow and GlassPanel**

In `web/src/workers/WorkersPage.tsx`:

Add the import near the top (after the `Button` import on line 2):

```tsx
import { Eyebrow, GlassPanel } from '../components/holo'
```

Replace the `header` eyebrow (lines 92-95 region) - change:

```tsx
      <div>
        <div className="font-mono text-[11px] tracking-widest text-fg-mute">FLEET</div>
        <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
      </div>
```

to:

```tsx
      <div>
        <Eyebrow>FLEET</Eyebrow>
        <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
      </div>
```

Replace the inline-duplicate eyebrow in the main return (lines 208-211 region) - change:

```tsx
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">FLEET</div>
          <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
        </div>
```

to:

```tsx
        <div>
          <Eyebrow>FLEET</Eyebrow>
          <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
        </div>
```

Replace the decommissioned error card (lines 116-121 region) - change:

```tsx
          <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
            <div className="mb-3 text-[13px] text-err">{(revoked.error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => revoked.refetch()}>
              Retry
            </Button>
          </div>
```

to:

```tsx
          <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
            <div className="mb-3 text-[13px] text-err">{(revoked.error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => revoked.refetch()}>
              Retry
            </Button>
          </GlassPanel>
```

Replace the loading skeleton cards (lines 159-163 region) - change:

```tsx
        <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-28 rounded-card border border-border bg-white/5" />
          ))}
        </div>
```

to:

```tsx
        <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <GlassPanel key={i} className="h-28" />
          ))}
        </div>
```

Replace the main error card (lines 172-177 region) - change:

```tsx
        <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
          <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
          <Button className="w-auto px-4" onClick={() => refetch()}>
            Retry
          </Button>
        </div>
```

to:

```tsx
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
          <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
          <Button className="w-auto px-4" onClick={() => refetch()}>
            Retry
          </Button>
        </GlassPanel>
```

Replace the empty-state card (lines 187-189 region) - change:

```tsx
        <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
          No workers enrolled yet.
        </div>
```

to:

```tsx
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No workers enrolled yet.
        </GlassPanel>
```

Leave everything else (the `sectionTabs`, the summary strip, the fetching indicator, the grid/table toggle, the pagination footer) exactly as-is - those are not glass panels and are out of scope for adoption.

- [ ] **Step 4: Run the WorkersPage suite and typecheck**

Run: `cd web && npx vitest run src/workers/WorkersPage.test.tsx`
Expected: PASS (all original tests plus the new eyebrow guard).

Run: `cd web && npx tsc -b`
Expected: no errors.

- [ ] **Step 5: Run the full web suite to confirm no regression anywhere**

Run: `cd web && npm test`
Expected: PASS - all suites, including the seven new primitive suites, the moved `StatusDot` suite, and every pre-existing worker suite.

- [ ] **Step 6: Commit**

```bash
git add web/src/workers/WorkersPage.tsx web/src/workers/WorkersPage.test.tsx
git commit -m "refactor(web): adopt holo primitives in WorkersPage"
```

---

## Self-review notes

**Spec coverage (Slice 1 sections):**
- File layout `web/src/components/holo/` with GlassPanel, Panel, Eyebrow, KpiStat, Chip, PillButton, ProgressBar, StatusDot, index.ts (no Spark) -> Tasks 1-9. Covered.
- Each primitive's exact Tailwind classes used verbatim from the spec -> Tasks 1-7 (classes copied from spec sections 1-7). Covered.
- Lightweight render test per primitive (renders children, applies variant class, merges className) -> Tasks 1-7 each have a `.test.tsx`. Covered.
- `Spark` deferred, not built, not exported -> Task 8 asserts `'Spark' in holo === false`. Covered.
- `StatusDot` moved + re-export + import updates in WorkersGrid/WorkersTable/WorkerDetailPage -> Task 9. Covered.
- Adoption in WorkersPage + WorkersGrid, no visual regression, existing tests green -> Tasks 10-11 with explicit regression-guard tests and full-suite run. Covered.
- Frontend-only, `cd web && npm test` -> declared at top; every task runs Vitest. Covered.

**Explicitly NOT done (matches spec "does NOT add" + Slice 2 boundary):** no density switch, no HSV machinery, no Spark, no table primitive, no DAGSVG/Donut/UserMenu/SortControl, no `WorkerDetailPage` body restyle (only the StatusDot import path), no `WorkerActions`/`WorkspacesPanel`/`WorkerEditForm` restyle (all Slice 2).

**Type consistency:** `GlassPanel` props (`as`, `className`, `children`, `...rest`) are consistent between Task 1 and the Task 10 extension. `Chip` tones `accent|muted|warn` and `dashed`/`onClick` consistent across Task 4 and its consumers. `Panel` props (`title`, `meta`, `footer`, `className`, `bodyClassName`, `children`) consistent. `KpiStat.progress` is `{ used, max }` in Task 6 and its test. `ProgressBar` `{ value, max?, className?, tone? }` consistent between Task 3 and its use inside `KpiStat` (Task 6). `StatusDot` keeps `{ status: WorkerStatus }` across the move (Task 9).

**Placeholder scan:** no TBD/TODO/"handle edge cases"/"similar to Task N" - every code step shows full literal code and every test step shows full test code.
