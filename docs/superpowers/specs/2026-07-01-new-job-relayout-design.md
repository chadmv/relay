# New Job Page Holo Relayout

- Date: 2026-07-01
- Status: Draft (design; awaiting review)
- Owner: relay-tpm
- Scope: web SPA only (`web/src/jobs/NewJobPage.tsx`). No backend, no Go, no shared-primitive changes.

## Problem

The shipped New Job page (`web/src/jobs/NewJobPage.tsx`, the `/jobs/new` JSON job-spec
editor) is a small, working page that predates the picked "Holo" hi-fi design and the
shared primitive set at `web/src/components/holo/`. It renders a flat inline
`bg-white/5 border-border` textarea, a hand-built header, and the generic full-width
`Button` for submit, duplicating vocabulary that now lives in reusable primitives (used by
the worker pages and the jobs-list relayout, which are the migration references).

This is a **pure restyle/relayout of an existing, working page**. Every data path, query
key, validation rule, error surface, and navigation target is preserved exactly. Only
structure and styling change, rebuilt from the shared primitives.

## Design authority and token mapping

Follows the same approach as the jobs-list relayout
(`docs/superpowers/specs/2026-07-01-jobs-list-holo-relayout-design.md`) and the worker
relayout (`docs/superpowers/specs/2026-07-01-holo-primitives-worker-detail-design.md`).

The **authoritative look** is the hi-fi Holo prototype (`design_handoff_relay_holo/hifi3-holo-pages.jsx`),
not the lo-fi `reference/screens/*` sketch. The app keeps its cyan accent and its fixed
`#050410` background. The prototype threads a `C` token bag (inline styles) and a density
switch `D` into its components. We **do not** port the `C` bag, the `makeTokens` machinery,
or the `D` density switch: `C.*` maps onto the existing `tokens.css` Tailwind classes, and
`D.*` collapses to fixed comfortable Tailwind values. Confirmed token mapping (all classes
verified present in `web/src/theme/tokens.css`):

| Prototype | App token / class |
| --- | --- |
| `C.bg` (`#050410`) | `bg-bg` |
| `C.fg` | `text-fg` |
| `C.fgMute` | `text-fg-mute` |
| `C.fgDim` | `text-fg-dim` |
| `C.accent` (`#3dd0f7`) | `text-accent` / `bg-accent` |
| `C.accentB` (`#6fe0fa`) | `text-accent-b` / `to-accent-b` |
| `C.err` (`#fb7185`) | `text-err` / `border-err` / `bg-err` |
| `C.border` | `border-border` |
| `glassPanel(C)` radius `14` | `rounded-card` |

The `web/src/components/holo/` primitives are already merged to main. This spec consumes
them; it does not add or modify any primitive.

## Hi-fi reference: is there a dedicated new-job mock?

**No.** The hi-fi handoff (`hifi3-holo-pages.jsx`) has **no dedicated new-job / job-submit
page**. The full component roster is `HoloAuth`, `HoloJobsList`, `HoloLanes`,
`HoloTimeline`, `HoloWorkers`, `HoloWorkerDetail`, `HoloSchedules`, `HoloScheduleDetail`,
`HoloAdmin`, `AdminUsers`. There is no `HoloNewJob` or submit screen, and the string
"New job" does not appear as a literal in the handoff. (The jobs-list spec's reference to a
"+ New job" button near line 469 in `HoloJobsList` was describing the **app's** current
entry-point button, not a handoff mock of the destination page.)

Because there is no mock, we **apply the Holo idiom directly**, grounded in the two closest
analogs the handoff does provide:

1. **`HoloScheduleDetail`'s spec-editor form** (`hifi3-holo-pages.jsx` ~line 1707-1791): a
   `glassPanel(C)` container holding form fields and a monospace-ish spec, with a
   primary/ghost **`pillBtn(C,'primary')` + `pillBtn(C,'ghost')`** button pair at the
   bottom (`Save changes` / `Cancel`). This is the authoritative model for a spec-editing
   surface with an action button pair.
2. **`HoloScheduleDetail`'s breadcrumb header** (~line 1710-1713): a leaf page under a list
   uses a back-link breadcrumb (`← Schedules` in `C.fgMute`, a `/` separator in `C.fgDim`,
   then the mono name). The app's `WorkerDetailPage` already ships this exact idiom
   (`← Workers / name`). The New Job page is a leaf under `/jobs`, so it follows the same
   breadcrumb pattern.

So the New Job page is treated as a **detail/leaf page**: back-link breadcrumb + eyebrow
micro-label header, a glass-wrapped editor, an error banner, and a Holo pill-button action
row. This matches the worker/jobs surfaces without inventing anything the handoff does not
sanction.

## Target layout

Top to bottom, built from the shared primitives. The page keeps its outer
`flex flex-col gap-4` container.

### 1. Header block (back-link breadcrumb + eyebrow + title + helper hint)

Restyle the current header `<div className="flex flex-col gap-1">` block to the Holo leaf
idiom:

- **Back-link breadcrumb.** The current `<Link to="/jobs">&larr; Jobs</Link>` is
  **preserved exactly** (same target, same `&larr; Jobs` copy). Its class shifts from the
  current `font-mono text-[11px] text-fg-mute hover:text-fg` to the worker-detail
  breadcrumb styling `text-[12px] text-fg-mute hover:text-fg` (drop the `font-mono` so the
  back-link matches `WorkerDetailPage`'s `← Workers`). Purely cosmetic; the `Link` element,
  `to`, and text are unchanged.
- **Eyebrow.** Add the `Eyebrow` primitive above the title with `NEW` (or `CREATE`; see
  Open Decisions). This is the mono uppercase micro-label the jobs-list header uses
  (`OVERVIEW`) and the worker/fleet pages use (`FLEET`, `RECURRING`). It gives the page the
  same Holo eyebrow-over-H1 rhythm.
- **Title.** Keep `<h1 className="text-[28px] font-normal tracking-tight">New job</h1>`
  unchanged. (The jobs-list H1 is `text-[32px]`; the New Job H1 stays `text-[28px]` since it
  is a leaf page, not a top-level overview - matching the leaf/detail scale is fine and
  not worth churning.)
- **Helper hint.** Keep the existing helper `<p className="font-mono text-[11px]
  text-fg-mute">` paragraph verbatim (the "Author a job-spec as JSON ..." copy that lists
  the fields, including the nested `<code>relay submit</code>`). Its text mentions "name"
  and "tasks", which a test relies on being distinct from the alert banner (see Test
  impact) - do **not** change this copy.

### 2. Editor (JSON textarea inside a GlassPanel)

The current bare textarea:

```
className="min-h-[360px] w-full rounded-card border border-border bg-white/5 p-3 font-mono text-[12px] text-fg"
```

Wrap the textarea in a `GlassPanel` so it sits on the proper Holo glass surface (gradient +
inset/drop shadow) instead of the flat `bg-white/5`, matching the schedule-detail spec
editor and the worker/jobs surfaces. The panel is the container; the textarea inside becomes
a transparent, borderless editing field so the glass reads as one surface:

- **Container:** `<GlassPanel className="p-3">` wrapping the textarea. `GlassPanel` supplies
  `rounded-card`, the `border-border`, the gradient, blur, and shadow; the `p-3` reproduces
  the current padding.
- **Textarea inside:** keep the `<textarea>` element with **all its behavior attributes
  unchanged** - `value={text}`, `onChange`, `spellCheck={false}`, and critically
  `aria-label="Job spec JSON"` (the tests select the editor via `getByRole('textbox')`,
  which resolves to this textarea; the aria-label must remain). Restyle its className from
  the bordered flat box to a transparent field that fills the panel:
  `min-h-[360px] w-full resize-y bg-transparent font-mono text-[12px] text-fg outline-none`.
  (Drop `rounded-card border border-border bg-white/5 p-3` - those now live on the
  `GlassPanel`. `resize-y` and `outline-none` are minor polish; `min-h-[360px]` and the mono
  `text-[12px] text-fg` are preserved so the editor keeps its height and look.)

The `aria-label` and `value`/`onChange` wiring are load-bearing for both behavior and tests;
only the visual container and the textarea's own border/background classes change.

### 3. Error banner (role="alert")

The inline error banner is **preserved exactly** in structure and semantics - it is the
single source for both the client validation error (`clientError`) and the server error
(`create.error`), joined by the existing `bannerMessage` expression with client precedence.

- Keep the conditional `{bannerMessage ? (<div role="alert" ...>{bannerMessage}</div>) :
  null}`. The `role="alert"` and the `{bannerMessage}` content are unchanged (a test asserts
  `findByRole('alert')` has the error text).
- Class stays the Holo error-banner form the jobs-list/worker pages use:
  `rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err`. This already
  matches the Holo idiom; **no change needed** (the `err` token is confirmed). Keep as-is.

The banner sits between the editor `GlassPanel` and the action row, exactly as today.

### 4. Action row (submit via PillButton primary)

The current submit uses the generic full-width `Button` (`../components/Button`) forced to
auto width:

```
<div><Button className="w-auto px-4" onClick={onSubmit} disabled={create.isPending}>Create job</Button></div>
```

Replace with the Holo `PillButton` primitive, `variant="primary"`, matching the
schedule-detail `Save changes` primary pill and the `AdminUsers` `+ Create user` primary
pill:

```
<div>
  <PillButton variant="primary" onClick={onSubmit} disabled={create.isPending}>
    Create job
  </PillButton>
</div>
```

- **Copy unchanged:** `Create job` (a test matches `getByRole('button', { name: /create
  job/i })`; `PillButton` renders a real `<button>`, so the accessible name is preserved).
- **`onClick={onSubmit}` unchanged** - same handler, same validate-then-mutate-then-navigate
  flow.
- **`disabled={create.isPending}` unchanged** - `PillButton`'s base class includes
  `disabled:opacity-40`, so the disabled-while-pending visual works; the test only checks
  `toBeDisabled()`, which holds for the underlying `<button>`.
- **`type`:** `PillButton` hardcodes `type="button"`, which is correct here (this is a
  click-handler submit, not a native form submit - there is no `<form>` element). No
  regression: the current generic `Button` has no explicit type, defaulting to `submit`, but
  since it is not inside a `<form>` the default never mattered. `PillButton`'s explicit
  `type="button"` is strictly safer and does not change behavior.

**Cancel / back affordance.** The current page has **no** separate Cancel button - the back
affordance is the `← Jobs` breadcrumb link in the header (section 1). The hi-fi
schedule-detail form does show a `Cancel` ghost pill next to `Save changes`, but that form
edits an existing resource in place; the New Job page is a create flow whose "cancel" is
simply navigating away via the breadcrumb. **Decision: do not add a Cancel button** - it
would be net-new behavior (this is a pure restyle) and the `← Jobs` breadcrumb already
serves the exit. (See Open Decisions if a ghost `Cancel` pill is wanted for symmetry.)

## Primitives used vs deliberately not used

**Used:**

- **`GlassPanel`** - wraps the JSON editor, replacing the flat `bg-white/5` box. The one
  intentional visual upgrade (gradient glass + shadow), applied to match every other Holo
  surface.
- **`Eyebrow`** - the `NEW` micro-label above the H1, matching the header rhythm of the
  jobs-list, worker, and fleet pages.
- **`PillButton` (`variant="primary"`)** - the `Create job` action, replacing the generic
  full-width `Button`.

**Deliberately not used:**

- **Generic `Button` (`../components/Button`)** - **removed from this page.** It is the
  full-width form button (used on the auth/login form and any true `<form>` submit); the
  Holo header/toolbar action idiom is `PillButton`. After this change `NewJobPage` no longer
  imports `Button`; other consumers keep it. Remove the now-unused `Button` import.
- **`Input` (`../components/Input`)** - not applicable; the editor is a multi-line
  `<textarea>`, not a single-line `<input>`. No change.
- **`Panel`** - not used. `Panel`'s built-in header/meta/footer frame is for titled data
  panels (worker-detail sub-panels, tables); the editor is a single free-form surface where
  the title lives in the page header, so a bare `GlassPanel` is the right, simpler fit
  (same choice the jobs-list spec made for its table container).
- **`Chip` / `StatusDot` / `ProgressBar` / `KpiStat`** - no status, tags, progress, or KPI
  content on this page; none apply.
- **A ghost `PillButton` Cancel** - deliberately omitted (see section 4); the breadcrumb is
  the exit.

## Preserved vs changed

**Preserved exactly (behavior, contracts, test-relevant):**

- `useCreateJob()` - the mutation and its dual invalidation of `['jobs']` (bare prefix, so
  every list view refetches) **and** `['job-stats']` (decoupled key; must be explicit).
  Untouched (it is a separate hook file; this page does not edit it).
- `validateSpecText(text)` client pre-check (valid JSON + non-empty string `name` +
  non-empty `tasks` array) and its precedence over the server error. Untouched.
- `STARTER_TEMPLATE` starter spec prefilled into the editor. Untouched.
- `onSubmit` flow: `create.reset()` -> `setClientError(null)` -> validate -> on failure set
  `clientError` and return (no POST) -> on success `create.mutate(value, { onSuccess: (job)
  => navigate(`/jobs/${job.id}`) })`. Untouched.
- `bannerMessage = clientError ?? (create.error as Error | null)?.message ?? null` - the
  single banner slot with client precedence. Untouched.
- The `role="alert"` error banner surfacing the server/client message. Structure/role
  preserved; classes already Holo-correct (no change).
- Submit-disabled-while-pending (`disabled={create.isPending}`). Preserved (moved onto
  `PillButton`).
- Navigation to `/jobs/:id` on 201 success. Untouched.
- The `/jobs/new` route (registered in `web/src/app/router.tsx`) and its precedence over
  `/jobs/:id`. Untouched (no routing change).
- The editor's `aria-label="Job spec JSON"`, `value`, `onChange`, `spellCheck={false}`.
  Preserved.
- The `← Jobs` back-link `<Link to="/jobs">` (target + copy). Preserved.
- The helper hint paragraph copy (mentions "name"/"tasks"; a test relies on it being
  distinct from the banner). Preserved.

**Changed (structure/styling only):**

- Add `Eyebrow` (`NEW`) above the H1.
- Back-link class: `font-mono text-[11px]` -> `text-[12px]` (drop mono, match
  worker-detail breadcrumb). Same element/target/text.
- Editor: flat `bg-white/5` bordered textarea -> textarea inside a `GlassPanel` (gradient
  glass); textarea's own border/background/padding classes moved to the panel; textarea
  becomes `bg-transparent ... outline-none` with `resize-y`.
- Submit: generic full-width `Button` -> `PillButton variant="primary"`; remove the now-
  unused `Button` import.
- No functional change anywhere.

## Test impact

Vitest (`cd web && npm test`, `web/src/jobs/NewJobPage.test.tsx`). No Go tests affected. All
existing behavior assertions are designed to survive because they query by role/text, not by
container classes:

| Existing test | Survives? | Why |
| --- | --- | --- |
| renders editor prefilled with starter template | yes | `getByRole('textbox')` still resolves to the textarea (aria-label + element preserved); `STARTER_TEMPLATE` unchanged. |
| submitting unedited template POSTs that body | yes | Button name `create job` and `onClick={onSubmit}` preserved on `PillButton`. |
| happy path: POST body, 201, navigate to `/jobs/:id` | yes | `onSubmit` -> `navigate` flow unchanged; button name preserved. |
| local parse error shows banner, NO POST | yes | `validateSpecText` + `clientError` + `role="alert"` preserved. |
| local shape error - missing name - banner, NO POST | yes | Banner is `role="alert"`; the test scopes to the alert (not the helper hint), and both the alert and the helper hint copy are preserved. |
| local shape error - empty tasks - banner, NO POST | yes | Same as above; helper hint mentioning "task" is preserved and stays distinct from the scoped alert. |
| server 400 surfaces inline, no nav, text preserved | yes | `bannerMessage` server path + `create.error` preserved; editor `value` binding preserved so text survives. |
| 413 oversize surfaces inline (same banner path) | yes | Same banner path. |
| submit button disabled while pending | yes | `disabled={create.isPending}` moved onto `PillButton` (a real `<button>`); `toBeDisabled()` holds. |
| a stale server error clears on next submit | yes | `create.reset()` + `setClientError(null)` in `onSubmit` preserved. |
| `/jobs/new` route renders form, NO GET `/v1/jobs/new` | yes | No routing change; `getByRole('textbox')` still finds the editor. |

**Class-based assertions:** none of the current tests key on container classes, so no
assertion needs updating. If any does after implementation, update the class expectation
only (never relax a behavior assertion).

**New tests:** none required. No new component, no new behavior; the primitives
(`GlassPanel`, `Eyebrow`, `PillButton`) are already independently tested. Optional (low
value): a light assertion that the submit is a Holo pill (e.g. the accessible name is
unchanged) - not necessary since the existing button-name test already covers it.

## Non-goals / out of scope

- No backend, Go, `router.tsx`, `api.ts`, `useCreateJob.ts`, or `specTemplate.ts` change.
- No new validation, no schema hints/linting in the editor, no field-level form (still a
  single JSON textarea - the server `jobspec.Validate` remains the validator of record, per
  the single job-spec pipeline invariant).
- No Cancel button, no split primary/ghost action row (breadcrumb is the exit).
- No modification to any `web/src/components/holo/` primitive.

## Implementation task breakdown

This page is small enough to implement directly from this spec without a separate plan doc.
Steps are bite-sized and ordered; each names its verification.

1. **Imports.** In `web/src/jobs/NewJobPage.tsx`: remove
   `import { Button } from '../components/Button'`; add
   `import { GlassPanel, Eyebrow, PillButton } from '../components/holo'`. Keep the `Link`,
   `useNavigate`, `useCreateJob`, and `specTemplate` imports.
   - Verify: no unused-import lint error; `Button` no longer referenced in this file.

2. **Header block.** Above the `<h1>`, add `<Eyebrow>NEW</Eyebrow>`. Change the back-link
   `<Link to="/jobs">` className from `font-mono text-[11px] text-fg-mute hover:text-fg` to
   `text-[12px] text-fg-mute hover:text-fg`. Leave the `&larr; Jobs` text, the `<h1>`, and
   the helper `<p>` copy unchanged.
   - Verify: page renders eyebrow + title + hint; back-link still targets `/jobs`.

3. **Editor GlassPanel.** Wrap the `<textarea>` in `<GlassPanel className="p-3"> ...
   </GlassPanel>`. Change the textarea className to
   `min-h-[360px] w-full resize-y bg-transparent font-mono text-[12px] text-fg outline-none`.
   Keep `value`, `onChange`, `spellCheck={false}`, and `aria-label="Job spec JSON"`.
   - Verify: `getByRole('textbox')` still resolves; editor renders on the glass surface;
     prefilled starter template visible.

4. **Error banner.** Leave the `{bannerMessage ? (<div role="alert" className="rounded-card
   border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">{bannerMessage}</div>) :
   null}` block exactly as-is (already Holo-correct).
   - Verify: no change; banner still `role="alert"`.

5. **Submit PillButton.** Replace
   `<Button className="w-auto px-4" onClick={onSubmit} disabled={create.isPending}>Create
   job</Button>` with
   `<PillButton variant="primary" onClick={onSubmit} disabled={create.isPending}>Create
   job</PillButton>` inside the same wrapping `<div>`.
   - Verify: `getByRole('button', { name: /create job/i })` resolves; disabled toggles with
     `create.isPending`.

6. **Run tests.** `cd web && npm test` (or scoped: the `NewJobPage` file).
   - Verify: all existing `NewJobPage.test.tsx` assertions pass unchanged.

7. **Lint / typecheck.** Run the web lint/typecheck gate.
   - Verify: clean; no unused `Button` import, no type errors on `PillButton`/`GlassPanel`
     props.

## Open decisions

1. **Eyebrow label.** Recommendation (baked in): `NEW`. Alternative: `CREATE` or
   `NEW JOB`. Confirm the word; trivial to change.
2. **Cancel affordance.** Recommendation (baked in): no separate Cancel button - the
   `← Jobs` breadcrumb is the exit, and adding a button is net-new behavior on a pure
   restyle. Alternative: add a ghost `PillButton` "Cancel" beside the primary submit
   (calling `navigate('/jobs')`) for visual symmetry with the schedule-detail form; this
   would be a small behavior addition and would want its own test. Confirm the omission.
3. **H1 scale.** Recommendation (baked in): keep `text-[28px]` (leaf-page scale).
   Alternative: bump to `text-[32px]` to match the jobs-list overview H1. Confirm; cosmetic.
