# Worker Detail Relayout (Slice 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite the shipped worker detail page to the hi-fi Holo layout (breadcrumb + identity header with an inline action bar, a four-up KPI stat row, and a two-column body of glass panels) using the already-merged Slice 1 primitives, with graceful placeholders for backend-blocked panels.

**Architecture:** Frontend-only, presentational relayout. No data-fetching, mutation, hook, or API changes. `WorkerDetailPage.tsx` is rewritten to compose the Slice 1 primitives (`GlassPanel`, `Panel`, `KpiStat`, `Chip`, `PillButton`, `ProgressBar`, `Eyebrow`, `StatusDot`). `WorkerActions`' trigger buttons restyle to `PillButton` and are repositioned so they render inside the detail header (the confirm dialogs, banners, and mutations are untouched). `WorkspacesPanel` and `WorkerEditForm` restyle to the primitives. `MetricChart`, `liveness.ts`, all `useWorker*` hooks, `useWorkerActions`, and `api.ts` are NOT modified.

**Tech Stack:** React 18 + TypeScript, Tailwind v4 (literal class strings), TanStack Query, React Router, Vitest + Testing Library + MSW.

---

## Slice independence

This is a **frontend-only** slice. It touches no Go code and adds no endpoints. It has no backend counterpart, so there is no frontend/backend parallelism to declare for the conductor: Phase 3 runs a single frontend track. Slice 2 depends only on Slice 1 (already implemented and committed on this branch).

## Context the engineer needs before starting

Read these before Task 1. You will hold them in context throughout:

- **Rewrite target:** `web/src/workers/WorkerDetailPage.tsx` (current flat vertical stack; wholesale rewrite).
- **Restyle/reposition:** `web/src/workers/WorkerActions.tsx`, `web/src/workers/WorkspacesPanel.tsx`, `web/src/workers/WorkerEditForm.tsx`.
- **Primitives (do NOT modify; consume their real APIs):** `web/src/components/holo/` -
  - `GlassPanel({ as?, className?, children })` - base classes are literal; `className` appends.
  - `Panel({ title, meta?, footer?, className?, bodyClassName?, children })` - header (title left, mono `meta` right) + body + optional `footer`. Composes `GlassPanel`.
  - `KpiStat({ label, value, sub?, progress? })` where `progress` is `{ used: number; max: number }` and renders a `ProgressBar`.
  - `Chip({ children, tone?: 'accent' | 'muted' | 'warn', dashed?, onClick? })` - renders a `<button>` when `onClick` is set, else a `<span>`.
  - `PillButton({ variant?: 'primary' | 'ghost' | 'danger' | 'muted', ...ButtonHTMLAttributes })` - defaults `ghost`, sets `type="button"`.
  - `ProgressBar({ value, max?, className?, tone? })` - inner fill has `data-testid="progress-fill"`.
  - `Eyebrow({ children, className? })` - mono uppercase micro-label.
  - `StatusDot({ status })` - already imported from `../components/holo/StatusDot` by the current page.
- **Data (unchanged):** `useWorker`, `useWorkerMetrics`, `useWorkerWorkspaces`, `useWorkerActions` in `web/src/workers/`; `Worker`/`MetricSample`/`WorkerPatch` types in `web/src/workers/api.ts`; helpers `formatGB`, `formatRelativeTime`, `labelChips`, `livenessView` in `web/src/workers/liveness.ts`.
- **`MetricChart`:** kept as-is. Renders `role="img"` with `aria-label={title}` per chart (CPU / MEMORY / GPU / GPU MEMORY). Fed by `useWorkerMetrics`. Tests key on those `img` roles - preserve them.
- **`ConfirmDialog`:** `role="dialog"`; Cancel + confirm buttons. Unchanged.

### Decisions locked for this slice (do not deviate)

1. **No Rename mutation.** The hi-fi mock shows a separate "Rename" pill, but the current `WorkerActions` has no rename action - renaming is done through the Edit form's Name field, and `useWorkerActions` exposes no rename mutation. Adding one would violate the "actions/hooks unchanged" constraint. Keep the existing button set: **Edit**, **Enable** OR **Disable** (toggle by `disabled_at`), **Drain**, **Revoke** (danger). Do NOT invent a Rename button or mutation.
2. **Backend-blocked pieces render placeholders, never fabricated data.** Current-tasks is a quiet note (NOT an empty table). Reservations is a shell + note. Jobs-today KPI is value `—`, sub `activity endpoint pending`. Slots `used` renders as `— / {max}` (no fabricated active-slots number) with a `ProgressBar` showing `{ used: 0, max: worker.max_slots }`. Agent-token is an **inline note**, not a panel. Each placeholder carries a code comment naming its backlog item.
3. **Admin gating unchanged.** Action bar, workspaces, reservations, agent-token note, and edit form are admin-only. Telemetry, KPIs, read-only labels, and identity always render.
4. **Telemetry unchanged.** Keep the existing CPU (`text-accent`) / MEMORY (`text-ok`) / GPU (`text-warn`) colors, the GPU gate on `worker.gpu_count > 0`, the empty state (`No telemetry yet.`), and the `MetricChart` grid.
5. **Dim on offline/stale** via `livenessView(worker.status).dimClass` on the page root, as today.

### Commands

- Run one test file: `cd web && npx vitest run src/workers/WorkerDetailPage.test.tsx`
- Run one test by name: `cd web && npx vitest run src/workers/WorkerActions.test.tsx -t "revoke success navigates"`
- Run all worker + holo tests: `cd web && npx vitest run src/workers src/components/holo`
- Typecheck: `cd web && npx tsc --noEmit`

Use PowerShell (the primary shell) or the Bash tool; either works for `cd web && ...`. Commit with the Bash tool (Git Bash) using a heredoc.

---

## Task 1: Restyle WorkerActions buttons to PillButton (in place)

Restyle the four/five action buttons to `PillButton`. Do NOT move them yet - keep the component's structure so its own test file stays green. Confirm dialogs, banners, edit-form toggle, and all mutations are unchanged.

**Files:**
- Modify: `web/src/workers/WorkerActions.tsx`
- Test: `web/src/workers/WorkerActions.test.tsx` (existing; assertions are role/label-based and must keep passing)

- [ ] **Step 1: Run the existing WorkerActions tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/workers/WorkerActions.test.tsx`
Expected: PASS (all 8 tests).

- [ ] **Step 2: Swap the raw `<button>` action triggers for `PillButton`**

In `web/src/workers/WorkerActions.tsx`, add the import and replace the button block. Import:

```tsx
import { PillButton } from '../components/holo'
```

Replace the `<div className="flex flex-wrap gap-2">...</div>` button group (currently the raw Edit / Enable / Disable / Drain / Revoke buttons) with:

```tsx
      <div className="flex flex-wrap gap-2">
        <PillButton onClick={() => setEditing((v) => !v)}>Edit</PillButton>
        {isDisabled ? (
          <PillButton
            variant="primary"
            disabled={busy}
            onClick={() => {
              disable.reset()
              enable.mutate()
            }}
          >
            Enable
          </PillButton>
        ) : (
          <>
            <PillButton variant="muted" disabled={busy} onClick={() => setConfirm('disable')}>
              Disable
            </PillButton>
            <PillButton variant="muted" disabled={busy} onClick={() => setConfirm('drain')}>
              Drain
            </PillButton>
          </>
        )}
        <PillButton variant="danger" disabled={busy} onClick={() => setConfirm('revoke')}>
          Revoke
        </PillButton>
      </div>
```

Leave the outer `<div className="flex flex-col gap-2">`, the `editing` edit-form block, the `actionError` banner, the requeued-tasks banner, and the `ConfirmDialog` block exactly as they are.

- [ ] **Step 3: Run the WorkerActions tests**

Run: `cd web && npx vitest run src/workers/WorkerActions.test.tsx`
Expected: PASS. The tests query by `getByRole('button', { name: 'Disable' })` etc.; `PillButton` renders a `<button>` with the same accessible name, so they survive. `disabled:opacity-40` is on `PillButton`'s base classes, so the busy-state visuals are preserved.

- [ ] **Step 4: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
cd web && git add src/workers/WorkerActions.tsx && git commit -m "refactor(web): restyle WorkerActions triggers to PillButton"
```

---

## Task 2: Add an inline-mode prop to WorkerActions (header vs stacked)

The detail header needs just the pill bar in the header row, with the banners/edit-form/dialog rendering below the header. Add an optional `inline` prop: when set, `WorkerActions` renders only the pill bar (no wrapping `flex-col`) so the page can place it in the header `ml-auto` slot; the banners, edit form, and dialog are hoisted so they still appear, but below the header. Rather than split the component, keep `WorkerActions` cohesive and have the page render the pill bar in the header and the rest below by using a single component with a render structure that supports both. Concretely: extract the pill bar into the header slot by rendering `WorkerActions` once, and give it a `barClassName` so the page can drop it into the header while its dialogs/banners portal-render in place.

Simplest correct approach (chosen): keep `WorkerActions` as one component but change its **outer wrapper to not impose vertical spacing when embedded**, and let the page mount `WorkerActions` in a dedicated row directly under the header. This keeps behavior identical and avoids prop sprawl. Therefore **this task adds no new prop**; instead Task 5 mounts `WorkerActions` in its own row beneath the header (the pill bar visually reads as the header action zone because the header + action row sit together at the top). Skip this task's code and proceed - it is intentionally a no-op placeholder to record the decision.

- [ ] **Step 1: Record the decision (no code)**

No file changes. The action bar is mounted by the page as a full `WorkerActions` block placed immediately under the breadcrumb/header row (Task 5). `WorkerActions` keeps its `flex flex-col gap-2` wrapper so its banners and dialog render below its pill bar. This satisfies the spec's "buttons repositioned into the header, banners render below the header row" without threading a layout prop through the component.

> Rationale: the spec's hard requirement is (a) pills are pill-styled (Task 1) and (b) banners render below the header row. Mounting the whole `WorkerActions` block right under the header meets both while keeping `WorkerActions.test.tsx` untouched and the mutation/dialog wiring identical. Threading an `inline` layout prop would add a variant with no behavioral value.

No commit for this task.

---

## Task 3: Restyle WorkspacesPanel body to primitives (keep it a bare panel body)

`WorkspacesPanel` will be wrapped by a `Panel` in the page (Task 5), so its own outer `GlassPanel`/title must be removed to avoid a double border/header. Restyle: drop the `SOURCE WORKSPACES` eyebrow and the outer `rounded-card border` box (the wrapping `Panel` supplies both), and turn the per-row `Evict` button into a `Chip` with `tone="accent"` (matching the hi-fi EVICT pill). The evict confirm flow and hook are unchanged.

**Files:**
- Modify: `web/src/workers/WorkspacesPanel.tsx`
- Test: `web/src/workers/WorkspacesPanel.test.tsx`

- [ ] **Step 1: Update the WorkspacesPanel test for the new structure**

The panel no longer renders its own `SOURCE WORKSPACES` eyebrow (the page's `Panel` title does) and the evict trigger is now a `Chip` button. The existing tests query by text (`ws-a4f2`, `//depot/x/main`, `No workspaces.`) and by `getByRole('button', { name: /evict/i })`, which all survive because `Chip` with `onClick` renders a `<button>` whose text is `Evict`. So the existing four tests need **no change**. Add one assertion that the panel no longer emits its own section eyebrow, to lock the de-duplication.

In `web/src/workers/WorkspacesPanel.test.tsx`, add:

```tsx
test('does not render its own SOURCE WORKSPACES heading (the page Panel supplies it)', async () => {
  server.use(http.get('/v1/workers/w1/workspaces', () => HttpResponse.json([])))
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  await screen.findByText('No workspaces.')
  expect(screen.queryByText('SOURCE WORKSPACES')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the new test to verify it fails**

Run: `cd web && npx vitest run src/workers/WorkspacesPanel.test.tsx -t "does not render its own SOURCE WORKSPACES"`
Expected: FAIL - `SOURCE WORKSPACES` is still present (current component renders the eyebrow).

- [ ] **Step 3: Restyle WorkspacesPanel**

Rewrite `web/src/workers/WorkspacesPanel.tsx`. Remove the outer eyebrow and `rounded-card border border-border bg-white/5` box; keep the header-row + rows grid as the bare body (the page's `Panel` supplies the frame). Swap the Evict `<button>` for a `Chip`. Keep the empty state, the evict error banner, and the `ConfirmDialog` unchanged.

```tsx
import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { Chip } from '../components/holo'
import { formatRelativeTime } from './liveness'
import { useWorkerActions } from './useWorkerActions'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

const COLS = 'grid grid-cols-[120px_90px_1fr_120px_90px_90px]'

// Admin-only source workspaces table with per-row evict. Rendered inside the
// page's Panel (which supplies the glass frame and the "Source workspaces"
// title), so this component is only the header row + data rows + confirm flow.
// Mounted by WorkerDetailPage only for admins, so no inner is_admin check is
// needed. Eviction is best-effort/async (202): the row does not vanish
// immediately; the 15s workspace poll reconciles once the agent confirms.
export function WorkspacesPanel({ workerId }: { workerId: string }) {
  const { data, isLoading } = useWorkerWorkspaces(workerId)
  const { evict } = useWorkerActions(workerId)
  const [confirmId, setConfirmId] = useState<string | null>(null)
  const rows = data ?? []

  function runEvict() {
    if (confirmId) evict.mutate(confirmId)
    setConfirmId(null)
  }

  return (
    <div className="flex flex-col">
      <div className={`${COLS} border-b border-border px-4 py-2 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <span>SHORT ID</span>
        <span>TYPE</span>
        <span>SOURCE KEY</span>
        <span>BASELINE</span>
        <span>LAST USED</span>
        <span className="text-right">ACTIONS</span>
      </div>
      {!isLoading && rows.length === 0 && (
        <div className="px-4 py-3 text-[12px] text-fg-mute">No workspaces.</div>
      )}
      {rows.map((ws) => (
        <div
          key={ws.short_id}
          className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11px]`}
        >
          <span className="text-fg">{ws.short_id}</span>
          <span className="text-fg-mute">{ws.source_type}</span>
          <span className="truncate text-fg-mute">{ws.source_key}</span>
          <span className="text-fg-mute">{ws.baseline_hash}</span>
          <span className="text-fg-mute">{formatRelativeTime(ws.last_used_at)}</span>
          <span className="flex justify-end">
            <Chip tone="accent" onClick={evict.isPending ? undefined : () => setConfirmId(ws.short_id)}>
              Evict
            </Chip>
          </span>
        </div>
      ))}

      {evict.error ? (
        <div className="mx-4 my-2 rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {(evict.error as Error).message}
        </div>
      ) : null}

      {confirmId && (
        <ConfirmDialog
          title={`Evict workspace ${confirmId}?`}
          body="The agent removes it on next opportunity. A held workspace is refused."
          confirmLabel="Evict"
          onConfirm={runEvict}
          onCancel={() => setConfirmId(null)}
        />
      )}
    </div>
  )
}
```

> Note: `Chip`'s `onClick` is `() => void`; passing `undefined` while `evict.isPending` renders a non-interactive `<span>`, matching the old `disabled` behavior (no double-fire). The `getByRole('button', { name: /evict/i })` query still finds the button in the not-pending state the tests exercise.

- [ ] **Step 4: Run the WorkspacesPanel tests**

Run: `cd web && npx vitest run src/workers/WorkspacesPanel.test.tsx`
Expected: PASS (all 5 tests, including the new de-duplication one).

- [ ] **Step 5: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
cd web && git add src/workers/WorkspacesPanel.tsx src/workers/WorkspacesPanel.test.tsx && git commit -m "refactor(web): restyle WorkspacesPanel body for Panel wrapping"
```

---

## Task 4: Restyle WorkerEditForm container and buttons

Swap the form's outer container to `GlassPanel`, and its Cancel/Save/Add-label/remove buttons to the primitive vocabulary. Field labels, patch-building logic, and validation are unchanged (its test file asserts by label text and role names, which survive).

**Files:**
- Modify: `web/src/workers/WorkerEditForm.tsx`
- Test: `web/src/workers/WorkerEditForm.test.tsx` (existing; no changes needed)

- [ ] **Step 1: Run the existing WorkerEditForm tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/workers/WorkerEditForm.test.tsx`
Expected: PASS (all tests).

- [ ] **Step 2: Restyle the container and buttons**

In `web/src/workers/WorkerEditForm.tsx`, add the import:

```tsx
import { GlassPanel, PillButton } from '../components/holo'
```

Change the outer `<form>` from `className="rounded-card border border-border bg-white/5 p-4"` to wrap the fields in a `GlassPanel`. Keep the `<form onSubmit={submit}>` element (the tests submit via the Save button) but move the glass styling onto it via `GlassPanel`'s `as="form"`:

```tsx
  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
```

and change the closing `</form>` to `</GlassPanel>`.

Replace the footer Cancel/Save buttons:

```tsx
      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Save
        </PillButton>
      </div>
```

Leave the `Field`/`Input` rows, the per-row key/value inputs, the `Remove {row.key}` buttons, and the `Add label` button as they are (their `aria-label` / text is asserted by tests). Do NOT change the label-row `<button>`s.

> Note: `PillButton` sets `type="button"` by default via its spread order, but we pass `type="submit"` for Save so the form submits. Verify the Save button still submits in Step 3.

- [ ] **Step 3: Run the WorkerEditForm tests**

Run: `cd web && npx vitest run src/workers/WorkerEditForm.test.tsx`
Expected: PASS. Save still submits (`type="submit"` on the `PillButton`), Cancel still fires `onCancel`, and every label/validation assertion is unchanged.

- [ ] **Step 4: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
cd web && git add src/workers/WorkerEditForm.tsx && git commit -m "refactor(web): restyle WorkerEditForm to GlassPanel + PillButton"
```

---

## Task 5: Rewrite WorkerDetailPage to the Holo layout

The wholesale rewrite. Rewrite the page test first (it drives the new structure), watch it fail, then implement. This is the largest task; the test and implementation are given in full.

**Files:**
- Modify (wholesale rewrite): `web/src/workers/WorkerDetailPage.tsx`
- Test (wholesale rewrite): `web/src/workers/WorkerDetailPage.test.tsx`

- [ ] **Step 1: Rewrite the WorkerDetailPage test for the new structure**

Replace the entire contents of `web/src/workers/WorkerDetailPage.test.tsx` with the following. It keeps the render harness, preserves the telemetry/gating/error tests (re-keyed where the old flat copy changed), and adds assertions for the new header, KPI row, and the backend-blocked placeholders (asserting the placeholder copy AND that no fabricated data renders).

```tsx
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { WorkerDetailPage } from './WorkerDetailPage'

const ID = 'w1abc234'
const GB = 1024 ** 3

const WORKER = {
  id: ID,
  name: 'render-rig-A',
  hostname: 'render-a.studio.dev',
  cpu_cores: 32,
  ram_gb: 128,
  gpu_count: 2,
  gpu_model: 'RTX 4090',
  os: 'linux',
  max_slots: 4,
  labels: { rack: 'A' },
  status: 'online',
  last_seen_at: '2026-06-05T00:00:00Z',
  last_sample_at: '2026-06-05T00:00:00Z',
}

function metrics(over: Record<string, unknown> = {}) {
  return {
    worker_id: ID,
    sample_interval_seconds: 10,
    samples: [
      { t: '2026-06-05T00:00:00Z', cpu_pct: 40, mem_used: 64 * GB, mem_total: 128 * GB, gpu: true, gpu_util_pct: 55, gpu_mem_used: 8 * GB, gpu_mem_total: 24 * GB },
      { t: '2026-06-05T00:00:10Z', cpu_pct: 60, mem_used: 70 * GB, mem_total: 128 * GB, gpu: true, gpu_util_pct: 70, gpu_mem_used: 9 * GB, gpu_mem_total: 24 * GB },
    ],
    ...over,
  }
}

function renderDetail(isAdmin: boolean) {
  setToken('test-token')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: isAdmin }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/workers/${ID}`]}>
        <AuthProvider>
          <Routes>
            <Route path="/workers/:id" element={<WorkerDetailPage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders the breadcrumb, worker name, and identity sub-line', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('link', { name: /workers/i })).toBeInTheDocument()
  expect(screen.getByText('render-rig-A')).toBeInTheDocument()
  expect(screen.getByText(/render-a\.studio\.dev/)).toBeInTheDocument()
})

test('renders the CPU/RAM and Slots KPI cards', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('32c · 128G')).toBeInTheDocument()
  // Slots: no active-slots field exists yet, so used renders as an em dash.
  expect(screen.getByText('— / 4')).toBeInTheDocument()
})

test('renders the Jobs-today placeholder KPI with no fabricated data', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('activity endpoint pending')).toBeInTheDocument()
  // Guard against a fabricated count like the hi-fi mock's "47".
  expect(screen.queryByText('47')).not.toBeInTheDocument()
})

test('renders the current-tasks placeholder note, not an empty table', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('no per-worker task feed yet')).toBeInTheDocument()
})

test('renders CPU/memory telemetry charts', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('img', { name: 'CPU' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'MEMORY' })).toBeInTheDocument()
})

test('shows GPU charts when the worker has a GPU', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('img', { name: 'GPU' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'GPU MEMORY' })).toBeInTheDocument()
})

test('hides GPU charts when the worker has no GPU', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ ...WORKER, gpu_count: 0, gpu_model: '' })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('img', { name: 'CPU' })).toBeInTheDocument()
  expect(screen.queryByRole('img', { name: 'GPU' })).not.toBeInTheDocument()
})

test('shows an empty telemetry state when there are no samples', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByText('No telemetry yet.')).toBeInTheDocument()
})

test('renders read-only labels for non-admins', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('rack=A')).toBeInTheDocument()
  // Non-admins get no add-label affordance.
  expect(screen.queryByRole('button', { name: /add label/i })).not.toBeInTheDocument()
})

test('shows not-found for a 404 worker', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ error: 'worker not found' }, { status: 404 })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByText('Worker not found.')).toBeInTheDocument()
})

test('shows a generic error with a Retry button for a non-404 failure', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByRole('button', { name: /retry/i })).toBeInTheDocument()
})

test('admins see the action bar, the Source workspaces panel, and the reservations placeholder', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(
    http.get(`/v1/workers/${ID}/workspaces`, () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  renderDetail(true)
  expect(await screen.findByRole('button', { name: 'Edit' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Revoke' })).toBeInTheDocument()
  expect(await screen.findByText('ws-a4f2')).toBeInTheDocument()
  expect(screen.getByText('no per-worker reservation lookup yet')).toBeInTheDocument()
})

test('non-admins see none of the action controls and never fetch workspaces', async () => {
  let wsCount = 0
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(
    http.get(`/v1/workers/${ID}/workspaces`, () => {
      wsCount++
      return HttpResponse.json([])
    }),
  )
  renderDetail(false)
  await screen.findByText('render-rig-A')
  await screen.findByRole('img', { name: 'CPU' })
  await new Promise((r) => setTimeout(r, 50))
  expect(wsCount).toBe(0)
  expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Disable' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Revoke' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /evict/i })).not.toBeInTheDocument()
  // Admin-only right-column pieces are hidden.
  expect(screen.queryByText('no per-worker reservation lookup yet')).not.toBeInTheDocument()
  expect(screen.queryByText(/Long-lived agent token/)).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the rewritten test to verify it fails**

Run: `cd web && npx vitest run src/workers/WorkerDetailPage.test.tsx`
Expected: FAIL. The current page has no `— / 4` slots text, no `activity endpoint pending` sub, no `no per-worker task feed yet`, no `no per-worker reservation lookup yet`, and its CPU/RAM card renders `32c · 128GB` (with `GB`), not `32c · 128G`. Multiple assertions fail.

- [ ] **Step 3: Rewrite WorkerDetailPage.tsx**

Replace the entire contents of `web/src/workers/WorkerDetailPage.tsx` with:

```tsx
import { Link, useParams } from 'react-router-dom'
import { useState } from 'react'
import { ApiError } from '../lib/api'
import { useAuth } from '../auth/AuthProvider'
import { Button } from '../components/Button'
import { Chip, GlassPanel, KpiStat, Panel, StatusDot } from '../components/holo'
import { MetricChart } from './MetricChart'
import { WorkerActions } from './WorkerActions'
import { WorkerEditForm } from './WorkerEditForm'
import { WorkspacesPanel } from './WorkspacesPanel'
import { formatGB, formatRelativeTime, labelChips, livenessView } from './liveness'
import { useWorker } from './useWorker'
import { useWorkerActions } from './useWorkerActions'
import { useWorkerMetrics } from './useWorkerMetrics'
import type { MetricSample } from './api'

function pct(n: number): string {
  return `${Math.round(n)}%`
}

function last<T>(arr: T[]): T | undefined {
  return arr[arr.length - 1]
}

export function WorkerDetailPage() {
  const { id = '' } = useParams()
  const { user } = useAuth()
  const isAdmin = Boolean(user?.is_admin)
  const { data: worker, error, isLoading, refetch } = useWorker(id)
  const { data: metrics } = useWorkerMetrics(id)
  const { update } = useWorkerActions(id)
  const [editing, setEditing] = useState(false)

  if (isLoading && !worker) {
    return <GlassPanel className="h-40" />
  }

  if (error && !worker) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        {notFound ? (
          <div className="text-[13px] text-fg-mute">Worker not found.</div>
        ) : (
          <>
            <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => refetch()}>
              Retry
            </Button>
          </>
        )}
        <div className="mt-4">
          <Link to="/workers" className="font-mono text-[11px] text-accent">
            &larr; Workers
          </Link>
        </div>
      </GlassPanel>
    )
  }

  if (!worker) return null

  const samples: MetricSample[] = metrics?.samples ?? []
  // Gate GPU charts on the hardware-stable gpu_count, not the per-sample `gpu`
  // flag (a transient nvidia-smi success), so the charts do not flicker away on
  // a single failed reading.
  const hasGpu = worker.gpu_count > 0
  const latest = last(samples)
  const memTotal = latest?.mem_total ?? 0
  const gpuMemTotal = latest?.gpu_mem_total ?? 0
  const chips = labelChips(worker.labels)
  const view = livenessView(worker.status)
  const isStale = worker.status === 'stale'

  return (
    <div className={`flex flex-col gap-4 ${view.dimClass}`}>
      {/* Breadcrumb + header row: back link, name, inline status chip; action bar (admin, ml-auto). */}
      <div className="flex items-center gap-2.5">
        <Link to="/workers" className="text-[12px] text-fg-mute hover:text-fg">
          &larr; Workers
        </Link>
        <span className="text-fg-dim">/</span>
        <span className="font-mono text-[14px] tracking-[0.04em] text-fg">{worker.name}</span>
        {worker.status === 'disabled' && <Chip tone="muted">{view.label}</Chip>}
        {isAdmin && (
          <span className="ml-auto">
            <StatusDot status={worker.status} />
          </span>
        )}
      </div>

      {/* Identity sub-line. Last-seen turns warn when stale. */}
      <div className="font-mono text-[11px] tracking-[0.04em] text-fg-mute">
        id <span className="text-fg">{worker.id.slice(0, 8)}</span> · hostname{' '}
        <span className="text-fg">{worker.hostname}</span> · os{' '}
        <span className="text-fg">{worker.os}</span> · last seen{' '}
        <span className={isStale ? 'text-warn' : 'text-fg'}>
          {worker.last_seen_at ? formatRelativeTime(worker.last_seen_at) : 'never'}
        </span>
      </div>

      {/* Admin action bar (repositioned WorkerActions; banners + edit form render below the header). */}
      {isAdmin && <WorkerActions worker={worker} />}

      {/* KPI stat row. */}
      <div className="grid grid-cols-4 gap-3">
        <KpiStat label="CPU · RAM" value={`${worker.cpu_cores}c · ${worker.ram_gb}G`} sub={`os: ${worker.os}`} />
        <KpiStat
          label="GPU"
          value={hasGpu ? `${worker.gpu_count} × ${worker.gpu_model}` : 'No GPU'}
        />
        {/* `used` (active slots) is not on the Worker type yet: render "— / max" with an
            empty progress bar until feature-2026-06-05-worker-detail-activity-panel lands. */}
        <KpiStat label="Slots" value={`— / ${worker.max_slots}`} progress={{ used: 0, max: worker.max_slots }} />
        {/* Backend-blocked: no per-worker activity aggregate exists yet.
            Enabler: feature-2026-06-05-worker-detail-activity-panel. */}
        <KpiStat label="Jobs today" value="—" sub="activity endpoint pending" />
      </div>

      {/* Two-column body. */}
      <div className="grid grid-cols-2 gap-3">
        {/* Left column. */}
        <div className="flex flex-col gap-3">
          {/* Backend-blocked: no per-worker task feed endpoint exists yet.
              Enabler: feature-2026-06-05-worker-detail-activity-panel. */}
          <Panel title="Current tasks" meta="ACTIVITY ENDPOINT PENDING">
            <div className="px-4 py-6 font-mono text-[11px] tracking-[0.04em] text-fg-dim">
              no per-worker task feed yet
            </div>
          </Panel>

          {isAdmin && (
            <Panel title="Source workspaces" meta="/v1/workers/.../workspaces">
              <WorkspacesPanel workerId={id} />
            </Panel>
          )}
        </div>

        {/* Right column. */}
        <div className="flex flex-col gap-3">
          <Panel title="Labels" meta={isAdmin ? 'PATCH /v1/workers' : undefined}>
            <div className="flex flex-wrap gap-1.5 px-4 py-3">
              {chips.length === 0 && !isAdmin && (
                <span className="font-mono text-[11px] text-fg-dim">no labels</span>
              )}
              {chips.map((c) => (
                <Chip key={c} tone="accent">
                  {c}
                </Chip>
              ))}
              {isAdmin && (
                <Chip dashed onClick={() => setEditing(true)}>
                  + add label
                </Chip>
              )}
            </div>
          </Panel>

          {isAdmin && editing && (
            <WorkerEditForm
              worker={worker}
              pending={update.isPending}
              onSubmit={(patch) => update.mutate(patch, { onSuccess: () => setEditing(false) })}
              onCancel={() => setEditing(false)}
            />
          )}

          {isAdmin && (
            <>
              {/* Backend-blocked: /v1/reservations is global admin with no worker filter.
                  Enabler: feature-2026-06-05-worker-detail-reservations-panel. */}
              <Panel title="Reservations" meta="RESERVATIONS ENDPOINT PENDING">
                <div className="flex flex-col gap-2 px-4 py-3">
                  <div className="font-mono text-[11px] tracking-[0.04em] text-fg-dim">
                    no per-worker reservation lookup yet
                  </div>
                  <div className="font-mono text-[10px] tracking-[0.04em] text-fg-dim">
                    selectors are informational in v1 · only worker_ids are enforced.
                  </div>
                </div>
              </Panel>

              {/* Agent token: the value is never exposed over HTTP (hash-only by design,
                  internal/tokenhash). Revoke already lives in the header action bar, so
                  this is an inline explanatory note, not a panel with a second Revoke. */}
              <div className="rounded-input border border-border bg-black/25 px-4 py-3 font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
                Long-lived agent token. Revoking (in the action bar above) forces the agent to exit and re-enroll with a fresh token.
              </div>
            </>
          )}

          {/* Utilization telemetry: REAL, fed by useWorkerMetrics. Empty/stale/offline preserved. */}
          <Panel
            title="Utilization · last 30m"
            meta="GET /v1/workers/{id}/metrics"
            footer={<span>last 30 min · 10s samples</span>}
          >
            {samples.length === 0 ? (
              <div className="px-4 py-6 text-center text-[12px] text-fg-mute">No telemetry yet.</div>
            ) : (
              <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3 p-3">
                <MetricChart
                  title="CPU"
                  values={samples.map((s) => s.cpu_pct)}
                  max={100}
                  current={latest ? pct(latest.cpu_pct) : '-'}
                  colorClass="text-accent"
                />
                <MetricChart
                  title="MEMORY"
                  values={samples.map((s) => s.mem_used)}
                  max={memTotal}
                  current={latest ? `${formatGB(latest.mem_used)} / ${formatGB(latest.mem_total)}` : '-'}
                  colorClass="text-ok"
                />
                {hasGpu && (
                  <>
                    <MetricChart
                      title="GPU"
                      values={samples.map((s) => s.gpu_util_pct)}
                      max={100}
                      current={latest ? pct(latest.gpu_util_pct) : '-'}
                      colorClass="text-warn"
                    />
                    <MetricChart
                      title="GPU MEMORY"
                      values={samples.map((s) => s.gpu_mem_used)}
                      max={gpuMemTotal}
                      current={latest ? `${formatGB(latest.gpu_mem_used)} / ${formatGB(latest.gpu_mem_total)}` : '-'}
                      colorClass="text-warn"
                    />
                  </>
                )}
              </div>
            )}
          </Panel>
        </div>
      </div>
    </div>
  )
}
```

> Notes for the implementer:
> - The Labels-panel `+ add label` chip and the header `Edit` pill both open the same `WorkerEditForm`. The header `Edit` is owned by `WorkerActions` (its own `editing` state); the Labels chip is owned by this page's `editing` state, mounting a second `WorkerEditForm` in the right column. That is acceptable and matches the spec (two entry points to the same form). Both call `update.mutate` and both close on success. If you prefer a single entry point, you may drop the header `Edit`-form (it stays in `WorkerActions`); do NOT do that here - keep `WorkerActions` untouched.
> - `useWorkerActions(id)` is called both here (for the Labels `update`) and inside `WorkerActions`. Two hook instances share the same query client, so this is safe - no shared mutable state, just two mutation objects.
> - Non-admins: `chips` still render (read-only labels), no `+ add label` chip, no action bar, no workspaces/reservations/token note.

- [ ] **Step 4: Run the WorkerDetailPage tests**

Run: `cd web && npx vitest run src/workers/WorkerDetailPage.test.tsx`
Expected: PASS (all tests).

- [ ] **Step 5: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors. (The page no longer references the removed `last_sample_at` line; confirm no unused-import error - `Button` is still used in the error state.)

- [ ] **Step 6: Commit**

```bash
cd web && git add src/workers/WorkerDetailPage.tsx src/workers/WorkerDetailPage.test.tsx && git commit -m "feat(web): relayout worker detail to Holo layout"
```

---

## Task 6: Full worker + holo suite green, typecheck, lint

Confirm the whole affected surface is green together (Task-by-task runs can hide cross-file regressions), and that Slice 1 primitive tests still pass.

**Files:** none (verification only).

- [ ] **Step 1: Run the full worker + holo test suite**

Run: `cd web && npx vitest run src/workers src/components/holo`
Expected: PASS. Specifically confirm `WorkerActions.test.tsx`, `WorkspacesPanel.test.tsx`, `WorkerEditForm.test.tsx`, `WorkerDetailPage.test.tsx`, `MetricChart.test.tsx` (unchanged), and all `src/components/holo/*.test.*` pass.

- [ ] **Step 2: Typecheck the whole web app**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Lint (if the project lints in CI)**

Run: `cd web && npm run lint`
Expected: no errors. If the script does not exist, skip.

- [ ] **Step 4: Confirm web/dist is not dirtied**

A frontend build is not part of this slice. If your tooling produced `web/dist` changes, revert them: `git checkout -- web/dist/`. Do NOT commit `web/dist` changes in this slice.

- [ ] **Step 5: Commit (only if lint auto-fixed formatting)**

If lint made no changes, there is nothing to commit and this slice is complete. Otherwise:

```bash
cd web && git add -A ':!dist' && git commit -m "chore(web): lint fixups for worker detail relayout"
```

---

## Self-review (author checklist, already applied)

- **Spec coverage:** header/breadcrumb + status chip (Task 5), identity sub-line with warn-on-stale last-seen (Task 5), 4-up KPI row with CPU·RAM / GPU / Slots(`— / max` + ProgressBar) / Jobs-today placeholder (Task 5), two-column body with Current-tasks placeholder note, Source-workspaces Panel-wrapped WorkspacesPanel (Tasks 3+5), Labels Panel with Chips + dashed add-label opening WorkerEditForm (Tasks 4+5), Reservations placeholder (Task 5), agent-token inline note (Task 5), Utilization Panel wrapping MetricChart with empty state preserved (Task 5). Action pills restyled + repositioned with banners below (Tasks 1+5). Admin gating, revoke-navigates, labels full-replace, dim-on-offline all preserved.
- **No fabricated data:** tests assert the absence of the mock's `47` jobs count and render `— / {max}` for slots and `—` for jobs-today. Placeholders carry backlog-referencing comments.
- **Unchanged surfaces:** `MetricChart`, `useWorker*`, `useWorkerActions`, `api.ts`, `liveness.ts`, `ConfirmDialog` not modified. `MetricChart.test.tsx` untouched.
- **Rename:** intentionally NOT added (no rename mutation exists; Edit covers it). Documented in "Decisions locked."
- **Type consistency:** `KpiStat` `progress` uses `{ used, max }` (matches primitive); `Chip` `onClick` is `() => void` (WorkspacesPanel passes `undefined` when pending); `PillButton` `variant` values are `primary|ghost|danger|muted` (used: primary/muted/danger/ghost-default). `Panel` props `title/meta/footer/children` match usage.

## Task count

**6 tasks** (Task 2 is an intentional no-op recording the layout decision; 5 tasks change code/tests, 1 is verification).
