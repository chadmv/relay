# Holo Design Primitives + Worker Detail Relayout

- Date: 2026-07-01
- Status: Draft (design; awaiting review)
- Owner: relay-tpm
- Scope: web SPA only (`web/src/`). No backend or Go changes.

## Problem

The shipped worker detail page (`web/src/workers/WorkerDetailPage.tsx`) does not match the
picked "Holo" hi-fi design (`design_handoff_relay_holo/hifi3-holo-pages.jsx`,
`HoloWorkerDetail` ~line 1245). The current page is a flat vertical stack of
`bg-white/5 border-border` boxes with a partial header; the hi-fi target is a
breadcrumb + identity header with an inline action bar, a four-up KPI stat row, and a
two-column body of glass panels (current tasks, source workspaces, labels,
reservations, agent token, utilization telemetry).

Separately, the glass-panel / eyebrow / chip / KPI vocabulary is re-inlined on every
page (`WorkersPage`, `WorkersGrid`, `WorkerDetailPage`, `MetricChart`, `WorkspacesPanel`
each repeat `bg-white/5 backdrop-blur border-border` and mono eyebrow strings). Before
the surge of new pages this should be a shared primitive set. This realizes backlog
item `docs/backlog/idea-2026-06-26-shared-holo-design-primitives.md`.

The user chose a two-slice fix: Slice 1 extracts a shared Holo primitive set; Slice 2
rewrites the worker detail page to the hi-fi layout using them. This spec covers both.

## Design authority and token mapping

The **authoritative look** is the hi-fi Holo prototype, not the lo-fi sketch in
`reference/screens/*` (which uses cursive fonts and an orange accent; it is a
structural second-source only). The app's cyan accent stays.

The prototype threads a `C` token bag (inline style values) into every component and
derives it from an HSV-parameterized accent via `makeTokens`. The app already bakes the
same tokens as CSS custom properties in `web/src/theme/tokens.css` with the default cyan
accent. **The primitives map the prototype's `C.*` inline values onto the existing
Tailwind token classes; we do not port the HSV machinery, the `C` bag, the density
switch (`D`), or inline styles.** Mapping:

| Prototype `C.*` (inline) | App token (tokens.css) | Tailwind class |
| --- | --- | --- |
| `C.bg` `#080612`* | `--color-bg` `#050410` | `bg-bg` |
| `C.fg` `#EDE9FE` | `--color-fg` `#ede9fe` | `text-fg` |
| `C.fgMute` | `--color-fg-mute` | `text-fg-mute` |
| `C.fgDim` | `--color-fg-dim` | `text-fg-dim` |
| `C.border` `rgba(255,255,255,0.08)` | `--color-border` | `border-border` |
| `C.ok` | `--color-ok` `#34d399` | `text-ok` / `bg-ok` |
| `C.warn` | `--color-warn` `#fbbf24` | `text-warn` / `bg-warn` |
| `C.err` | `--color-err` `#fb7185` | `text-err` / `bg-err` |
| `C.accent` | `--color-accent` `#3dd0f7` | `text-accent` / `bg-accent` |
| `C.accentB` | `--color-accent-b` `#6fe0fa` | `text-accent-b` / `bg-accent-b` |
| glass panel radius `14` | `--radius-card` `14px` | `rounded-card` |
| input/button radius `6-8` | `--radius-input` `8px` | `rounded-input` |

\* The app background is fixed at `#050410` in `tokens.css`; keep the app value. The
prototype's slightly different `#080612` is not authoritative here.

Note on per-metric hues: the prototype derives `cCpu/cMem/cGpu/cGpuMem` by hue-shifting
the accent. The app has no such tokens today; `MetricChart` uses `text-accent`,
`text-ok`, `text-warn`. Slice 2 keeps the existing per-metric color assignment (see
Open Decisions #4); we do not add derived-hue tokens in this pass.

## Backend reality (confirmed against `internal/api/`)

The hi-fi `HoloWorkerDetail` shows several panels fed by mock data. Verified which are
real today:

| Panel | Endpoint | Status |
| --- | --- | --- |
| Utilization / telemetry sparklines | `GET /v1/workers/{id}/metrics` | **REAL** - already rendered by `MetricChart`. Keep. |
| Source workspaces + evict | `GET /v1/workers/{id}/workspaces`, `POST .../evict` | **REAL** - `WorkspacesPanel` works. Keep, restyle. |
| Labels | `PATCH /v1/workers/{id}` | **REAL** - `WorkerEditForm` handles it. Keep. |
| Enable/Disable/Drain/Rename/Revoke | disable/enable/PATCH/DELETE token | **REAL** - `WorkerActions` handles them. Keep, reposition. |
| Current tasks table | `GET /v1/workers/{id}/tasks` | **MISSING** - no per-worker task endpoint. Backend-blocked. |
| "Jobs today" KPI (count / failures / avg) | (none) | **MISSING** - no per-worker activity aggregate. Backend-blocked. |
| Reservations targeting this worker | `GET /v1/reservations` is global admin, no worker filter | **MISSING** - Backend-blocked. |
| Agent token value box | (none - tokens are hash-only, never returned over HTTP) | **NOT EXPOSABLE** - by design (`internal/tokenhash`). Only metadata + the existing Revoke action. |

Backend enablers already tracked:
- `docs/backlog/feature-2026-06-05-worker-detail-activity-panel.md` (current tasks + jobs-today)
- `docs/backlog/feature-2026-06-05-worker-detail-reservations-panel.md` (per-worker reservations)

Slice 2 must ship without these endpoints. It renders graceful placeholders (or omits
panels) for the blocked ones and references the backlog items in code comments.

---

## Slice 1 - Shared Holo primitives

### Goal

A small, purely presentational primitive module under `web/src/components/holo/` that
captures the recurring Holo vocabulary. Minimal and driven by what `HoloWorkerDetail`
needs plus the clearest cross-page repeats (glass panel, eyebrow label, panel-with-header
shell, KPI stat, chip, status dot, spark, pill button, progress bar). No density-mode
switching (handoff says one default is fine). No new behavior; these replace inline
Tailwind repetition, not logic.

### File layout

```
web/src/components/holo/
  GlassPanel.tsx
  Panel.tsx          (header + optional endnote wrapper around GlassPanel)
  Eyebrow.tsx
  KpiStat.tsx
  Chip.tsx
  PillButton.tsx
  ProgressBar.tsx
  Spark.tsx
  StatusDot.tsx      (moved from web/src/workers/StatusDot.tsx)
  index.ts           (barrel re-export)
```

`Spark` and `StatusDot` reconcile with existing code (below) rather than duplicating.

### Primitives

Each primitive lists: purpose, props, exact Tailwind classes (mapped from the
prototype), and what it replaces. Classes are literal strings so Tailwind v4 includes
them (the codebase already relies on this - see `liveness.ts`).

#### 1. `GlassPanel`

- **Purpose:** the fundamental Holo container - translucent gradient surface, 1px border,
  blur, 14px radius, inset+drop shadow. Prototype's `glassPanel(C)`.
- **Props:** `{ className?, children, as? }` (default `div`). `className` merges/overrides.
- **Base classes:**
  `rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px] shadow-[inset_0_1px_0_rgba(255,255,255,0.08),0_8px_32px_rgba(0,0,0,0.4)]`
- **Replaces:** every `rounded-card border border-border bg-white/5` (+ `backdrop-blur`)
  usage in `WorkersGrid`, `WorkerDetailPage`, `MetricChart`, `WorkspacesPanel`,
  `WorkerEditForm`, and the list/error/skeleton boxes. The gradient + shadow are the
  fidelity upgrade over the current flat `bg-white/5`.
- **Note:** a plain flat variant is available by passing `className` to override the
  gradient where a subtler surface is wanted (e.g. nested inner rows use `bg-black/25`,
  not a full glass panel - those stay inline, they are not panels).

#### 2. `Panel`

- **Purpose:** the recurring "glass panel with a header row and an optional footer/endnote"
  used by Current tasks, Source workspaces, Utilization, etc. Composes `GlassPanel`.
- **Props:** `{ title, meta?, footer?, className?, bodyClassName?, children }`.
  - `title`: left side of the header (sans, `text-[13px]`).
  - `meta`: right side of the header, mono uppercase micro-label (e.g. `2 OF 4 SLOTS`,
    or an endpoint hint). Rendered via the `Eyebrow`-style token but inline in the header.
  - `footer`: optional endnote row (mono `text-[10px] text-fg-mute`, top border).
- **Header classes:** container `flex items-center justify-between border-b border-border
  px-4 py-2.5`; title `text-[13px] text-fg`; meta `font-mono text-[10px] tracking-[0.14em]
  text-fg-mute`.
- **Footer classes:** `mt-auto border-t border-border px-4 py-2.5 font-mono text-[10px]
  tracking-[0.06em] text-fg-mute flex items-center justify-between`.
- **Replaces:** the hand-built header rows in `WorkspacesPanel` and the ad-hoc
  section-title-plus-hint patterns; the framing of `MetricChart`'s container header.

#### 3. `Eyebrow`

- **Purpose:** the mono uppercase micro-label used above H1s (`FLEET`, `RECURRING`) and as
  section labels (`LABELS`, `TELEMETRY`).
- **Props:** `{ children, className? }`. Uppercases via CSS, not JS, so callers pass
  normal text.
- **Classes:** `font-mono text-[11px] uppercase tracking-[0.18em] text-fg-mute`
  (section-label variant `text-[10px] tracking-[0.16em]` via `className`).
- **Replaces:** the repeated `font-mono text-[11px] tracking-widest text-fg-mute` eyebrow
  strings in `WorkersPage` (`FLEET`), `WorkerDetailPage` (`TELEMETRY`, `LABELS`),
  `WorkspacesPanel` (`SOURCE WORKSPACES`), `Field.tsx`'s label styling (Field keeps its
  own label element but the token string is unified).

#### 4. `KpiStat`

- **Purpose:** a KPI/stat block in the four-up row: eyebrow label, large mono value,
  small mono sub-line, optional inline progress bar. Prototype's KPI cards in the
  `HoloWorkerDetail` KPI grid.
- **Props:** `{ label, value, sub?, progress? }` where `progress` is `{ used, max }`
  (renders a `ProgressBar` between value and sub).
- **Classes:** wraps `GlassPanel` with `p-3.5 flex flex-col gap-1`; label via `Eyebrow`
  section variant; value `font-mono text-[22px] font-light tracking-[-0.01em] text-fg`;
  sub `font-mono text-[10px] tracking-[0.04em] text-fg-mute`.
- **Replaces:** the three hand-built stat boxes currently in `WorkerDetailPage`
  (`CPU · RAM`, `GPU`, `MAX SLOTS`).

#### 5. `Chip`

- **Purpose:** rounded pill for labels, tags, reservation selectors. Two tones.
- **Props:** `{ children, tone?: 'accent' | 'muted' | 'warn', dashed?, onClick? }`.
  `dashed` renders the "+ add label" affordance (transparent, dashed border).
- **Classes:** base `rounded-full px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em]`;
  accent `border border-accent/40 bg-accent/10 text-accent`; muted
  `border border-border bg-white/[0.04] text-fg-mute`; warn
  `border border-warn/40 bg-warn/10 text-warn` (for `draining` and similar); dashed
  `border border-dashed border-border bg-transparent text-fg-mute cursor-pointer`.
- **Replaces:** the label chip spans in `WorkersGrid`, `WorkerDetailPage` (labels block),
  and the HELD/EVICT action pills in `WorkspacesPanel` (EVICT = accent, HELD = muted).

#### 6. `PillButton`

- **Purpose:** the rounded action buttons in the detail header
  (Enable/Disable/Drain/Edit/Rename/Revoke). Prototype's `pillBtn(C, kind)`.
- **Props:** standard `ButtonHTMLAttributes` plus `variant?: 'primary' | 'ghost' |
  'danger' | 'muted'`. Defaults `ghost`.
- **Classes:** base `rounded-full px-4 py-2 text-[12px] tracking-[0.02em] backdrop-blur-[8px]
  disabled:opacity-40`; primary `bg-gradient-to-r from-accent to-accent-b font-semibold
  text-bg`; ghost `border border-border bg-white/5 text-fg`; danger
  `border border-err/50 bg-err/10 text-err`; muted (the disabled-state toggle look)
  `border border-fg-mute/50 bg-fg-mute/10 text-fg`.
- **Distinct from existing `Button`:** `web/src/components/Button.tsx` is the full-width
  rectangular form-submit button (auth, retry, forms). It stays. `PillButton` is the
  compact pill action used on toolbars/headers. This spec does not merge them; they serve
  different roles (see Open Decisions #2).
- **Replaces:** the hand-built `rounded-md border ... px-3 py-1.5` action buttons in
  `WorkerActions.tsx`.

#### 7. `ProgressBar`

- **Purpose:** the thin gradient fill bar used for slots, task progress, and telemetry
  utilization rows.
- **Props:** `{ value, max?, className?, tone? }` (value/max -> percent; `tone` allows a
  muted/stale fill).
- **Classes:** track `relative h-1 overflow-hidden rounded-[2px] bg-white/[0.08]`; fill
  `absolute inset-0 rounded-[2px] bg-gradient-to-r from-accent to-accent-b` with inline
  `width: pct%` (width is dynamic data, allowed as a style; every other value is a class).
- **Replaces:** the inline slot bar in `WorkerDetailPage`'s MAX SLOTS card and the ad-hoc
  bars the hi-fi uses in tasks/telemetry rows.

#### 8. `Spark`

- **Purpose:** small sparkline (area + line) for inline telemetry. Prototype's `Spark`.
- **Reconciliation:** the app already has `web/src/workers/chart.ts` (`chartPath`) and
  `MetricChart.tsx`. `MetricChart` is the large 300x60 titled chart used on the detail
  page and is **kept as-is** (it already matches the Holo telemetry panel well). A new
  small `Spark` (e.g. 70x18, no title) is only needed if Slice 2's layout uses inline
  mini-sparks in table rows. **Decision:** Slice 2 reuses `MetricChart` for the
  Utilization panel and does **not** introduce inline row-sparks (the current-tasks table
  that used them is backend-blocked). Therefore `Spark` is **deferred** - not built in
  this pass. If a later page needs it, build it on `chartPath` to avoid geometry
  duplication. (Listed here for completeness; see Open Decisions #3.)

#### 9. `StatusDot`

- **Purpose:** the dot + mono status label. Already exists at
  `web/src/workers/StatusDot.tsx`, driven by `livenessView`.
- **Reconciliation:** **move** it to `web/src/components/holo/StatusDot.tsx` and re-export
  so non-worker pages can use it. Keep the `livenessView`-based API (worker statuses are
  the only consumer today). Do **not** duplicate the prototype's generic-status version.
  Update imports in `WorkersGrid`, `WorkersTable`, `WorkerDetailPage`.

### What Slice 1 explicitly does NOT add

- No density (`D`) switching - one comfortable default.
- No HSV/theme-tweak machinery.
- No `Spark` (deferred, see above).
- No table primitive - that is a separate tracked idea
  (`idea-2026-06-05-shared-accessible-table-primitive`); this spec does not absorb it.
- No `DAGSVG`, `Donut`, `UserMenu`, `SortControl` - not needed by worker detail.

### Slice 1 adoption / regression guard

Slice 1 lands the primitives **and** refactors the already-shipped pages that repeat the
vocabulary (`WorkersPage`, `WorkersGrid`) to consume them, with **no visual regression**
(the gradient/shadow upgrade to glass panels is the one intentional visual change, applied
uniformly). Existing tests for those pages must still pass (see Test impact).

---

## Slice 2 - Worker detail relayout

Rewrite `WorkerDetailPage.tsx` to the `HoloWorkerDetail` layout using Slice 1 primitives.
Data still comes from `useWorker`, `useWorkerMetrics`, `useWorkerWorkspaces` and the
existing action hooks - no data-fetching changes.

### Target layout (top to bottom)

1. **Breadcrumb + header row** (single flex row):
   - Left: `← Workers` back link (mute -> fg on hover), a `/` divider, the worker
     **name** in mono `text-[14px] tracking-[0.04em]`. When the worker is `disabled`,
     an inline status `Chip` (tone by status) follows the name.
   - Right (`ml-auto`), admin only: the action `PillButton` bar -
     Enable **or** Disable (toggle by `disabled_at`), Drain, Edit (labels), Rename,
     Revoke (danger). These are the current `WorkerActions` buttons repositioned into the
     header and restyled as pills. Non-admins see no action bar.

2. **Identity sub-line** (mono `text-[11px] text-fg-mute`): `id {id.slice(0,8)} ·
   hostname {hostname} · os {os} · last seen {relative}`; the last-seen segment turns
   `text-warn` when status is `stale`. Uses real `worker` fields.

3. **KPI stat row** - `grid grid-cols-4 gap-3` of `KpiStat`:
   - `CPU · RAM` -> `{cpu_cores}c · {ram_gb}G`, sub `os: {os}`.
   - `GPU` -> `{gpu_count} × {gpu_model}` or `No GPU`, sub from hardware (no fabricated
     `nvidia-smi · cuda 12.3` string unless a real field exists; otherwise omit sub).
   - `Slots` -> `{used}/{max}` with a `ProgressBar`. **Caveat:** `used` (active slots) is
     not currently on the `Worker` type. Render `max` alone (`— / {max}` for used) until
     the activity endpoint lands, OR drop the used portion. See Open Decisions #1.
   - **`Jobs today`** -> **backend-blocked**. Render a placeholder KPI: value `—`, sub
     `activity endpoint pending`, with a code comment referencing
     `feature-2026-06-05-worker-detail-activity-panel`. (Alternative: omit the fourth card
     and use a 3-up grid - see Open Decisions #1.)

4. **Two-column body** - `grid grid-cols-2 gap-3`, each column a `flex flex-col gap-3`:

   **Left column:**
   - **Current tasks** `Panel` - **backend-blocked**. Render the panel shell with a
     placeholder body: `no per-worker task feed yet` in mono `text-fg-dim`, meta hint
     removed or set to the pending note. Code comment references the activity-panel
     backlog item. (Alternative: omit entirely - Open Decisions #1.)
   - **Source workspaces** `Panel` wrapping the restyled `WorkspacesPanel` **(admin
     only)**. The workspaces table moves inside a `Panel` (title `Source workspaces`,
     meta the endpoint hint). Row chips (HELD/EVICT) use `Chip`. The evict confirm flow
     and hook are unchanged.

   **Right column:**
   - **Labels** `Panel` (or `GlassPanel`) - the worker's labels as `Chip`s, plus a
     `+ add label` dashed `Chip` (admin) that opens the existing `WorkerEditForm` (see
     "Edit form" below). Uses real `worker.labels`.
   - **Reservations** `Panel` - **backend-blocked**. Render a placeholder:
     `no per-worker reservation lookup yet` + the informational note that selectors are
     advisory in v1. Admin-gated. Code comment references
     `feature-2026-06-05-worker-detail-reservations-panel`. (Alternative: omit - Open
     Decisions #1.)
   - **Agent token** `Panel` (admin only) - **token value is never exposed over HTTP**
     (hash-only by design). Render **metadata only**: a masked/placeholder token display
     (e.g. `tok_••••` static text, clearly not a real value) is **out**; instead show the
     explanatory copy ("Long-lived agent token. Revoking forces the agent to re-enroll.")
     and the **Revoke** action. Since Revoke already lives in the header action bar, this
     panel is primarily explanatory; **Decision:** fold agent-token context into a small
     note rather than a dedicated panel to avoid a redundant Revoke button (Open
     Decisions #5).
   - **Utilization · last 30m** `Panel` wrapping the existing telemetry - **REAL**. Keep
     the current `MetricChart` grid (CPU, MEMORY, GPU, GPU MEMORY) fed by
     `useWorkerMetrics`. Empty/stale/offline states preserved (empty -> "No telemetry
     yet"; stale -> dim + warning endnote). The panel footer shows the sampling note.

### Edit form integration

`WorkerEditForm` (name / max_slots / labels) is kept. It is invoked from the header
`Edit` `PillButton` (as today via `WorkerActions`' toggle) and/or the Labels panel's
`+ add label` chip. It restyles by swapping its container to `GlassPanel` and its
buttons to `PillButton`; `Field`/`Input` stay. It renders inline below the header (or as
the existing toggle) - not a modal, matching current behavior.

### Actions integration

`WorkerActions`' buttons move into the header bar as `PillButton`s. The confirm dialogs
(`ConfirmDialog`), the disable/drain/revoke/enable hooks, the requeued-tasks success
banner, and the inline error banner are all **unchanged** - only the trigger buttons
restyle and reposition. The success/error banners render below the header row.

### States

- **Loading:** `GlassPanel` skeletons in the KPI/body grid (upgrade current flat box).
- **Error / 404:** keep the existing centered error card, restyled to `GlassPanel`; 404
  copy unchanged.
- **Non-admin:** header action bar, workspaces, reservations, agent-token, and edit form
  are all hidden (admin-gated exactly as today). Telemetry, KPIs, labels (read-only),
  identity remain.
- **Offline / stale worker:** dim via `livenessView().dimClass` (as today); telemetry
  panel shows the offline/stale endnote; last-seen segment colors by status.

### Placeholders summary (backend-blocked)

| Panel | Slice 2 treatment | Backlog ref |
| --- | --- | --- |
| Current tasks | Panel shell + "no per-worker task feed yet" placeholder | activity-panel |
| Jobs today KPI | `—` value + "activity endpoint pending" sub (or omit) | activity-panel |
| Slots `used` | `— / {max}` until active-slots field exists (or show `max` only) | activity-panel |
| Reservations | Panel shell + "no per-worker reservation lookup yet" + v1 note | reservations-panel |
| Agent token value | Never shown (hash-only); context note + existing Revoke only | n/a (by design) |

Each placeholder carries a code comment naming the backlog item so the enabler is
discoverable when the endpoint lands.

---

## Test impact

Worker tests will need updating. Notable:

- **`StatusDot`** moves to `web/src/components/holo/`; update imports in
  `WorkersGrid`, `WorkersTable`, `WorkerDetailPage`. No existing dedicated `StatusDot`
  test file (it is exercised via grid/table tests), so those consumer tests just need the
  import path.
- **`WorkerDetailPage.test.tsx`** - rewrites: header/breadcrumb structure, KPI row,
  two-column body, the new backend-blocked placeholders (assert the placeholder copy and
  that no fabricated data renders), admin vs non-admin gating, telemetry still renders
  from metrics. Assertions keyed on the old flat layout will break and must be re-authored.
- **`WorkerActions.test.tsx`** - buttons restyle to `PillButton` and move into the header;
  role/label-based queries (getByRole('button', { name: ... })) should mostly survive, but
  any class/structure assertions need updating. Confirm-dialog and hook behavior assertions
  are unchanged.
- **`WorkspacesPanel.test.tsx`** - now wrapped in a `Panel`; HELD/EVICT become `Chip`s.
  Query by text/role should survive; structural/class queries update.
- **`WorkerEditForm.test.tsx`** - container/buttons restyle; field labels unchanged.
  Behavior assertions (patch building, validation) unchanged.
- **`MetricChart.test.tsx`** - unchanged (component kept as-is).
- **New primitive tests** - lightweight render tests for `KpiStat`, `Chip`, `PillButton`,
  `ProgressBar`, `Panel`, `Eyebrow`, `GlassPanel` (render children, apply variant classes,
  merge `className`). Keep these minimal; they are presentational.
- **`WorkersPage.test.tsx` / `WorkersGrid.test.tsx`** - Slice 1 adoption must not regress;
  these should pass with at most import/structure tweaks.

Run: `cd web && npm test` (Vitest). No Go tests are affected.

---

## Gate decisions (resolved 2026-07-01)

Signed off by the user:

1. **Backend-blocked panels -> graceful placeholders.** Render Current tasks (as a quiet
   "activity endpoint pending" note, NOT an empty table), Reservations (shell + pending
   note), and the Jobs-today KPI (`—` + pending sub) as placeholders so the layout is
   hi-fi-faithful and lights up when the enablers land. Do not omit them.
2. **Sequencing -> two PRs.** Slice 1 (primitives + adopt in `WorkersPage`/`WorkersGrid`,
   no visual regression) ships first; Slice 2 (worker-detail relayout) builds on the
   merged primitives.
3. Accepted the recommended defaults: keep `PillButton` and `Button` separate (#2);
   defer `Spark` (#3); keep the current CPU/MEM/GPU telemetry colors (#4); render the
   agent-token as an inline note, not a dedicated panel (#5).

The section below is the original rationale for each.

## Open decisions (for the gate)

1. **Backend-blocked panels: placeholder vs omit.** The spec's default is to render the
   Current tasks and Reservations panels and the Jobs-today KPI as **graceful
   placeholders** (shell + "pending" copy) so the hi-fi layout is structurally faithful
   and the panels light up when endpoints land. The alternative is to **omit** them now
   (3-up KPI grid, single-panel-per-column body) and add them with the backend work,
   yielding a cleaner but less hi-fi-faithful page. Which do you prefer? (Recommendation:
   placeholders for KPI + reservations; **omit** the current-tasks table since an empty
   table reads as broken, and instead show a small "current tasks: activity endpoint
   pending" note. Confirm.)

2. **`PillButton` vs existing `Button`.** Keep them as two separate components (rectangular
   form button vs compact pill action)? Recommendation: yes, keep both; they are visually
   and semantically distinct and merging adds a variant matrix for no gain.

3. **`Spark` primitive - build now or defer?** Recommendation: defer; the only Slice 2
   consumer (inline row sparks in the current-tasks table) is backend-blocked, and
   `MetricChart` covers the utilization panel. Build `Spark` on `chartPath` when a page
   actually needs it. Confirm.

4. **Per-metric telemetry colors.** Keep the current assignment (CPU accent, MEM ok, GPU
   warn) rather than porting the prototype's hue-shifted `cCpu/cMem/cGpu/cGpuMem` derived
   tokens? Recommendation: keep current; derived-hue tokens are a separate polish item and
   not needed for layout fidelity.

5. **Agent-token panel: dedicated panel or inline note?** Since the token value cannot be
   shown and Revoke already lives in the header, a dedicated panel would only repeat the
   Revoke button. Recommendation: render agent-token context as a small explanatory note
   (rotation metadata if available, plus the re-enroll caveat), not a full panel with a
   second Revoke. Confirm - or keep a dedicated panel for visual balance in the right
   column (its slot could instead be filled by the telemetry panel growing).

6. **Slice sequencing / PRs.** Two PRs (Slice 1 primitives + adoption, then Slice 2
   relayout) or one? Recommendation: two PRs - Slice 1 is independently reviewable and
   de-risks Slice 2. Slice 1 must adopt primitives in the already-shipped pages so it is
   not dead code.
