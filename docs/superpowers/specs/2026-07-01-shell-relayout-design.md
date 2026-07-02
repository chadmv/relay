# App Shell Holo Relayout (HoloShell nav chrome + UserMenu)

- Date: 2026-07-01
- Status: Draft (design; awaiting review)
- Owner: relay-tpm
- Scope: web SPA only. Two files: `web/src/shell/HoloShell.tsx` (the global nav/layout
  chrome that wraps every ProtectedRoute page) and `web/src/shell/UserMenu.tsx` (the
  account dropdown in the top-right of the shell). No backend, no Go, no router change,
  no `AuthProvider` change, no shared-primitive change.

## Problem

The shipped app shell (`web/src/shell/HoloShell.tsx`) is the global chrome rendered by
`ProtectedRoute` around every authenticated page (`<HoloShell><Outlet /></HoloShell>`).
It is a small, working top-bar layout: a `relay.` wordmark, a `NavLink` row for
`/jobs /workers /schedules /admin`, and the `UserMenu` account dropdown. It predates the
picked "Holo" hi-fi design and the shared primitive set at `web/src/components/holo/`.
The bar is a flat `border-b border-border` header with no glass surface; the active nav
item is a bare `text-accent` color swap (no underline accent bar); the wordmark is a
`font-sans` bold word, not the Holo mono/eyebrow logo lockup; and the `UserMenu` dropdown
is a hand-rolled `rgba(14,12,30,0.96)` panel with no gradient glass, no account header,
and a plain toggle chip. That Holo vocabulary now lives in reusable primitives
(`GlassPanel`, `Eyebrow`) already used by the worker pages, the jobs-list relayout, the
new-job relayout, and the auth relayout, which are the migration references.

This is a **pure restyle/relayout of the working shell**. Every nav link and its target,
the active-route highlighting logic, the `UserMenu` open/close behavior, the current-user
display, the logout action, and every dropdown link (including links to unshipped pages)
are preserved exactly. Only structure and styling change, rebuilt from the shared
primitives.

## The shell: exact files

The "app shell" is two files under `web/src/shell/`:

| File | Role |
| --- | --- |
| `web/src/shell/HoloShell.tsx` | The global layout chrome. Renders the top `<header>` (wordmark + nav + `UserMenu`) and a `<main>` that renders `children`. `ProtectedRoute` wraps every authenticated page in it. Owns the nav list, the active-route highlight (via `NavLink` `isActive`), and the `onLogout` handler (`logout()` then `navigate('/auth')`). |
| `web/src/shell/UserMenu.tsx` | The account dropdown, rendered by `HoloShell` in the top-right. Owns its own open/close state, outside-click + Escape close, the toggle chip (shows `email`), and the dropdown panel: three `Link`s (`/profile`, `/profile/password`, `/profile/sessions`) plus a `Log out` `<button>` that calls the `onLogout` prop. |

Consumers / adjacent (read for context, **not edited**):

- `web/src/app/ProtectedRoute.tsx` - the only consumer; wraps `<Outlet />` in `<HoloShell>`.
  Untouched.
- `web/src/app/router.tsx` - defines the routes the nav links target (`/jobs`, `/workers`,
  `/schedules`, `/admin`, and `/profile/*`). Untouched. Note `/admin` and `/profile/*`
  currently both route to `JobsPlaceholder` (they are placeholders), which is exactly why
  we must not touch nav targets - see Backend reality.
- `web/src/shell/UserMenu.test.tsx` - the only shell test file. Must keep passing (see
  Test impact). There is **no** `HoloShell.test.tsx`.

## Design authority and token mapping

Follows the same approach as the auth relayout
(`docs/superpowers/specs/2026-07-01-auth-relayout-design.md`), the new-job relayout
(`docs/superpowers/specs/2026-07-01-new-job-relayout-design.md`), the jobs-list relayout
(`docs/superpowers/specs/2026-07-01-jobs-list-holo-relayout-design.md`), and the worker
relayout (`docs/superpowers/specs/2026-07-01-holo-primitives-worker-detail-design.md`):
consume the merged primitives, **do not force a primitive where the existing control is
already reasonable**, **preserve all behavior**, and **omit backend-blocked chrome**.

The **authoritative look** is `HoloShell` in the hi-fi Holo prototype
(`design_handoff_relay_holo/hifi3-holo-pages.jsx`, ~line 303 for the shell/nav; the
prototype's `UserMenu` is ~line 200-301), **not** the lo-fi `reference/screens/*` sketch.
The app keeps its cyan accent and its `#050410` background. The prototype threads a `C`
token bag (inline styles) into its components; we **do not** port the `C` bag or the
`makeTokens` machinery. `C.*` maps onto the existing `tokens.css` Tailwind classes.
Confirmed token mapping (classes verified present in `web/src/theme/tokens.css`):

| Prototype | App token / class |
| --- | --- |
| `C.bg` (`#050410`) | `bg-bg` |
| `C.fg` | `text-fg` |
| `C.fgMute` | `text-fg-mute` |
| `C.fgDim` | `text-fg-dim` (verified present) |
| `C.accent` (`#3dd0f7`) | `text-accent` / `bg-accent` / `border-accent` |
| `C.accentB` (`#6fe0fa`) | `text-accent-b` / `to-accent-b` (verified present) |
| `C.err` (`#fb7185`) | `text-err` |
| `C.border` | `border-border` |
| `C.mono` | `font-mono` (verified present) |
| `glassPanel(C)` radius `14` | `GlassPanel` / `rounded-card` |

The `web/src/components/holo/` primitives are already merged to main. This spec consumes
them; it does not add or modify any primitive.

## Backend reality: what `HoloShell`/`UserMenu` show that is NOT backed

The prototype shell and its `UserMenu` are marketing-fidelity mocks and render several
affordances the relay app does not have backing for. Per the "don't render backend-blocked
bits" stance, **these are out of scope and must NOT be rendered**:

| Prototype element | Reality | Decision |
| --- | --- | --- |
| `2.4.1` version string next to the RELAY wordmark (~line 332) | No version string is exposed to the client; there is no version field on the app surface. | **Omit** (same call the auth spec made for the `RELAY · 2.4.1` badge). |
| `SYNC OK` status dot in the top-right (~line 346) | No global "sync"/coordinator-health signal is fetched by the shell; net-new data. | **Omit** (ambient chrome; no backing data). |
| `ADMIN · relay.studio.dev` sub-line under the email in the dropdown account header (~line 264) | No server-name is exposed, and role (`ADMIN`) is on `user` but the shell does not currently render it and adding it is net-new. | **Omit the server-name.** The account header shows the email only (which the app already has via the `email` prop). See Open Decisions re: whether to surface role. |
| Per-item mono `hint` text in the dropdown: `name, avatar`, `PUT /users/me/password`, `3 active · 30-day TTL` (~line 217-219) | These are illustrative API/marketing hints, not app data (e.g. no live session count is fetched). | **Omit the hints.** The dropdown items keep their plain labels only. |
| `DELETE /auth/token` hint next to Log out (~line 295) | Same: illustrative API annotation, not an affordance. | **Omit.** |
| Per-item leading icons (`◐`, `⌥`, `≡`, `⎋`, ~line 217-219, 293) | Decorative glyphs from the mock. | **Omit** (net-new decorative content; the current menu has no icons and the a11y item is separately tracked - do not introduce new markup that would interact with it). |
| Nav items for `/admin` etc. | See next section - the app's nav set is preserved exactly; this is not a nav change. | Keep current nav (see below). |

The `R` gradient logo tile (~line 324) is decorative but small and is part of the brand
lockup the task calls out ("the brand/logo"). See Target layout / Open Decisions - the
recommendation is to adopt a lightweight version of the mono `RELAY` wordmark treatment
**without** the version string, and to treat the gradient `R` tile as optional (Open
Decision 1), since the current shell uses a `relay.` text wordmark and the tile is a
net-new decorative element.

## Nav set: preserved exactly (not a nav change)

The prototype's `NAV` is `[['jobs','Jobs'],['workers','Workers'],['schedules','Schedules'],
['admin','Admin']]` - identical labels/targets to the app's current
`NAV = [{to:'/jobs'},{to:'/workers'},{to:'/schedules'},{to:'/admin'}]`. So there is no
nav-set delta to reconcile here. **The app keeps its current four nav links exactly**
(same order, same labels, same targets). Even though `/admin` currently routes to a
placeholder, it is an existing nav destination and stays. This spec does **not** add,
remove, or re-target any nav link. It only restyles how the row and the active item look.

The `UserMenu` dropdown links (`/profile`, `/profile/password`, `/profile/sessions`) also
stay exactly as they are, **including their targets**, even though `/profile/*` currently
routes to a placeholder. The task is explicit: do not change what the menu links to. These
are the app's honest current links; a future Profile ship will back them.

## Target layout

Rebuilt from `HoloShell` (prototype ~line 303) and its `UserMenu` (~line 200), using the
shared primitives, keeping the app's existing DOM structure and all behavior.

### The top bar (`HoloShell.tsx`)

Today the `<header>` is:
`<header className="flex items-center justify-between border-b border-border px-5 py-3">`
with an inner left cluster (`gap-6`: wordmark + nav) and the `UserMenu` on the right.

Restyle to the prototype's glass top bar while keeping the same flex structure and the
`<main className="p-5">{children}</main>` unchanged:

- **Glass bar surface.** The prototype nav bar is a translucent blurred strip
  (`background:'rgba(255,255,255,0.025)', backdropFilter:'blur(10px)'`,
  `borderBottom:1px ${C.border}`, ~line 316-322). Apply a subtle glass treatment to the
  `<header>` so the bar reads as a Holo surface over the page. Because the bar is a
  full-width strip (not a card), use inline glass classes rather than the `GlassPanel`
  primitive here: add `bg-white/[0.025] backdrop-blur-[10px]` to the existing
  `border-b border-border`. Keep `flex items-center justify-between`; bump padding to the
  prototype rhythm `px-[22px] py-3` (from `px-5 py-3`; cosmetic, matches
  `padding:'12px 22px'`). Rationale for not using `GlassPanel`: `GlassPanel`'s `BASE`
  includes `rounded-card` and a drop shadow + gradient meant for a floating card; a
  full-bleed top bar wants only the flat blur + bottom border, so hand-classing the strip
  is the right, simpler fit (the "don't force a primitive" stance). The dropdown, which
  *is* a floating card, does use `GlassPanel` (see below).

- **Brand lockup (mono `RELAY` wordmark).** Replace the current
  `<span className="font-sans text-[18px] font-bold">relay<span className="text-accent">.
  </span></span>` with the prototype's mono wordmark treatment (~line 323-333),
  **minus the `2.4.1` version string** (omitted, backend-blocked):
  a small gradient `R` tile (Open Decision 1) followed by
  `<span className="font-mono text-[11px] font-semibold uppercase tracking-[0.18em] text-accent">RELAY</span>`.
  The mono/uppercase/tracked treatment is exactly the `Eyebrow` primitive's typography, but
  `Eyebrow` hardcodes `text-fg-mute`; the brand wants `text-accent`. Two options
  (Open Decision 2): (a) use a plain `<span>` with the mono classes and `text-accent` (no
  primitive; simplest, keeps `Eyebrow` untouched), or (b) use
  `<Eyebrow className="text-accent">RELAY</Eyebrow>` (the trailing `text-accent` in
  `className` wins over `Eyebrow`'s `text-fg-mute` since it is appended after). Recommend
  (b) - reuses the primitive and the tracking/size are already correct - but confirm.
  Keep the wordmark as a plain non-link `<span>` (the current wordmark is not a link;
  do not add net-new navigation).

- **Nav row + active-item accent treatment.** Keep the `NAV.map` over `NavLink` exactly
  (same keys, targets, labels). Change only the class function. The prototype's active
  item is `color:C.fg` with `borderBottom:2px solid ${C.accent}` and inactive is
  `color:C.fgMute` with a transparent 2px bottom border (~line 336-341), padded
  `7px 14px`. Translate the `NavLink` `className` callback to:
  - active: `text-fg border-b-2 border-accent`
  - inactive: `text-fg-mute border-b-2 border-transparent hover:text-fg`
  - shared on the link: `px-[14px] py-[7px] text-[13px] tracking-[0.02em] transition-colors`
  This is the one real visual upgrade: the active route now gets the **accent underline
  bar** from `HoloShell`, not just a color swap. The `NavLink`/`isActive` mechanism is
  unchanged - React Router still computes `isActive`; we only change what classes it maps
  to. The nav container becomes `flex gap-0.5` (prototype `gap:2`) instead of `gap-4`,
  since items now carry their own horizontal padding.

- **Right cluster.** Today: just `<UserMenu ... />`. Prototype: a `SYNC OK` dot + the
  `UserMenu`, right-aligned via `marginLeft:'auto'`. We **omit the `SYNC OK` dot**
  (backend-blocked), so the right cluster stays exactly `<UserMenu email={...}
  onLogout={onLogout} />`, right-aligned by the header's existing `justify-between`
  (the left cluster holds wordmark + nav; the `UserMenu` is the sole right child). No
  structural change to the right side beyond restyling `UserMenu` itself.

- **Layout wrapper.** Keep `<div className="min-h-screen bg-bg text-fg">` and
  `<main className="p-5">{children}</main>` unchanged. Do **not** adopt the prototype's
  radial-gradient ambient background (`radial-gradient(...)` stack, ~line 308-312) - that
  is decorative net-new chrome and the app's flat `bg-bg` is the established convention
  across every other relaid-out page. (Open Decision 3.)

### The account chip + dropdown (`UserMenu.tsx`)

Keep the component's entire behavior contract (state, effects, props, links, button)
and restyle the two surfaces: the toggle chip and the dropdown panel.

**Toggle chip.** Today:
`<button ... aria-haspopup="menu" aria-expanded={open} className="rounded-full border
border-border bg-white/5 px-3 py-1 text-[12px] text-fg">{email}</button>`.
Restyle toward the prototype chip (~line 224-241): a rounded-full accent-tinted pill with
the email in a mono micro-label, and (Open Decision 4) an optional leading initials avatar
+ trailing caret. Concretely, **preserve all attributes** (`onClick`, `aria-haspopup`,
`aria-expanded`) and change only the class + inner content:
  - class: `flex items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[10px]
    uppercase tracking-[0.12em] transition-colors` plus an open-aware accent tint:
    `open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'`. This
    reproduces the prototype's `open`-state border/background swap.
  - inner: keep `{email}` as the visible text (do not lose it - the tests match the toggle
    by its accessible name `/ada@studio.dev/i`, which comes from the button text). Wrap it
    as `<span className="text-fg normal-case tracking-normal">{email}</span>` so the email
    itself is not uppercased/tracked (emails are case-sensitive; the mono micro-label
    framing is on the chip, the email stays readable). Optionally prepend the initials
    avatar and append a caret (Open Decision 4); both are decorative and must sit **outside**
    the text node so the accessible name stays exactly the email.

**Dropdown panel.** Today:
`<div className="absolute right-0 mt-2 w-44 rounded-card border border-border p-1
text-[12px] shadow-xl" style={{ background: 'rgba(14,12,30,0.96)' }}>` containing three
`Link`s and the `Log out` `<button>`. Restyle to the prototype glass dropdown
(~line 243-298) using the `GlassPanel` primitive (the dropdown *is* a floating card, so
the primitive fits, unlike the top bar):

- Replace the panel `<div>` with
  `<GlassPanel className="absolute right-0 mt-2 w-56 p-1.5 text-[12px]">`. `GlassPanel`
  supplies `rounded-card`, `border-border`, the gradient, `backdrop-blur`, and a drop
  shadow (replacing the ad-hoc `rgba(14,12,30,0.96)` + `shadow-xl`). Widen from `w-44`
  to `w-56` to match the prototype's roomier `280px` panel (cosmetic; Open Decision 5).
  Keep `absolute right-0 mt-2` (the prototype uses `top:calc(100%+8px); right:0`, which
  `mt-2` approximates). **Keep the panel a plain `<div>`/`GlassPanel` with no `role="menu"`**
  - the menu/menuitem-roles + arrow-key work is a separately tracked backlog item
  (`docs/backlog/feature-2026-06-05-usermenu-panel-menu-roles.md`) and is explicitly
  **out of scope here** (see Non-goals). This restyle must not add or change `role`
  attributes on the panel or items.
- **Account header (new, backed).** Prepend a small account header row inside the panel,
  matching the prototype (~line 251-267) but **email-only** (omit the `ADMIN ·
  server-name` sub-line, backend-blocked): a bordered-bottom row with the email. Optionally
  an initials avatar (Open Decision 4). Example:
  `<div className="flex items-center gap-2.5 border-b border-border px-2.5 pb-2.5 pt-2
  mb-1.5"><span className="truncate text-[12.5px] text-fg">{email}</span></div>`. This uses
  the `email` prop the component already receives - no new data. (If the initials avatar is
  adopted, it precedes the email span.) Rationale: the prototype's dropdown leads with an
  account identity block; showing the email there is honest and uses data on hand.
- **Menu items (preserved links).** Keep the three `Link`s exactly - same `to` targets
  (`/profile`, `/profile/password`, `/profile/sessions`) and same visible labels
  (`Profile`, `Password`, `Sessions`). **Do not** rename to the prototype's `Change
  password`/etc. or add the mono `hint` columns or leading icons (all omitted per Backend
  reality). Restyle each `Link`'s class from
  `block rounded px-3 py-2 hover:bg-white/5` to the prototype's item rhythm, e.g.
  `block rounded-md px-2.5 py-2 text-fg hover:bg-white/5` (cosmetic tightening only;
  keep `hover:bg-white/5`). Targets, labels, and the `Link` element are unchanged.
- **Divider + Log out.** Prepend a thin divider before Log out (prototype ~line 285:
  `<div className="my-1.5 h-px bg-border" />`) and keep the `Log out` `<button>` exactly:
  same `onClick={onLogout}`, same visible text `Log out`, restyled from
  `block w-full rounded px-3 py-2 text-left text-err hover:bg-white/5` to
  `block w-full rounded-md px-2.5 py-2 text-left text-err hover:bg-white/5` (cosmetic).
  **The `onClick={onLogout}` and the text `Log out` are load-bearing** (a test clicks
  `getByText('Log out')` and asserts `onLogout` fired). Do not wrap the handler or change
  the label. (The prototype wraps logout as `()=>{setOpen(false); onLogout()}`; the app's
  current button calls `onLogout` directly and the outside-click/route change already
  unmounts or closes the menu, so **do not** add a `setOpen(false)` - it is net-new
  behavior and unnecessary; keep `onClick={onLogout}` verbatim.)

## Primitives used vs deliberately not used

**Used:**

- **`GlassPanel`** - the `UserMenu` dropdown panel (a floating card), replacing the
  ad-hoc `rgba(14,12,30,0.96)` + `shadow-xl` div with the gradient-glass surface used by
  every other Holo card.
- **`Eyebrow`** (recommended, Open Decision 2) - the mono/uppercase/tracked `RELAY`
  brand wordmark, via `<Eyebrow className="text-accent">RELAY</Eyebrow>` (the appended
  `text-accent` overrides `Eyebrow`'s default `text-fg-mute`). Alternative: a plain mono
  `<span>` with the same classes (no primitive).

**Deliberately not used:**

- **`GlassPanel` for the top bar** - **not used.** The bar is a full-bleed strip that
  wants only flat blur + a bottom border, not `rounded-card` + drop shadow + gradient. It
  is hand-classed (`bg-white/[0.025] backdrop-blur-[10px] border-b border-border`). This is
  the "don't force a primitive where the plain treatment is right" stance.
- **`PillButton`** - **not used.** The nav items are `NavLink`s (router navigation with
  `isActive` styling), not action pills; forcing them into `PillButton` would drop the
  router integration and the active-underline treatment. The `UserMenu` toggle and the
  `Log out` button are stateful/handler buttons with bespoke chip/menu styling, not the
  primary/ghost action pills `PillButton` models. All stay as their current elements,
  restyled inline.
- **`Chip` / `StatusDot` / `KpiStat` / `ProgressBar` / `Panel`** - none apply. There is no
  tag, status, KPI, progress, or titled-data-panel content in the shell. (`StatusDot`'s
  only would-be use is the mock's `SYNC OK` dot, which is backend-blocked and omitted.)
- **The `2.4.1` version string, `SYNC OK` dot, server-name sub-line, per-item hints,
  API-annotation hints, per-item icons, and the radial ambient background** - all omitted
  as backend-blocked or net-new decorative chrome (see Backend reality).

## Preserved vs changed

**Preserved exactly (behavior, contracts, test-relevant):**

- **Nav links + targets.** `NAV = [{/jobs,Jobs},{/workers,Workers},{/schedules,Schedules},
  {/admin,Admin}]` - same order, labels, and targets. No add/remove/re-target.
- **Active-route highlight logic.** Still driven by `NavLink`'s `isActive` callback; only
  the class strings it maps to change (now an accent underline instead of a bare color
  swap). React Router's route matching is untouched.
- **`HoloShell` structure + `onLogout`.** `useAuth()` (`user`, `logout`), `useNavigate`,
  the `onLogout` async handler (`await logout(); navigate('/auth')`), and the
  `email={user?.email ?? ''}` prop into `UserMenu` are all unchanged. The `<main>` and the
  outer `min-h-screen bg-bg text-fg` wrapper are unchanged.
- **`UserMenu` behavior contract (all preserved):** `open`/`setOpen` state; the
  `useEffect` outside-click (`mousedown` on `document`, `ref.current.contains`) + Escape
  (`keydown`) close, with its add/remove-listener cleanup keyed on `[open]`; the toggle's
  `onClick={() => setOpen(v => !v)}`; the `aria-haspopup="menu"` and `aria-expanded={open}`
  attributes; the three `Link`s and their `to` targets; and the `Log out` button's
  `onClick={onLogout}`. The toggle's accessible name stays the `email` (tests match
  `/ada@studio.dev/i`). The `Log out` text stays `Log out` (a test clicks it by text).
- **Responsive behavior.** The shell has no special responsive logic today (it is a single
  flex row that wraps by default); this restyle adds none and removes none. No breakpoints
  are introduced (Open Decision 6 notes the nav row could overflow on narrow widths, same
  as today; out of scope to fix here).

**Changed (structure/styling only):**

- Top bar: flat `border-b` header -> glass strip (`bg-white/[0.025] backdrop-blur-[10px]`)
  with prototype padding rhythm.
- Brand: `font-sans` bold `relay.` wordmark -> mono/uppercase/tracked `RELAY` accent
  wordmark (via `Eyebrow`), version string omitted; optional gradient `R` tile
  (Open Decision 1).
- Nav active item: bare `text-accent` color swap -> `text-fg` + accent underline bar
  (`border-b-2 border-accent`); inactive gets a transparent underline + hover; items gain
  their own padding and the row gap tightens.
- `UserMenu` toggle: flat `bg-white/5` pill -> accent-tinted mono chip with open-state
  border/bg swap (email preserved as the accessible name).
- `UserMenu` dropdown: ad-hoc dark div -> `GlassPanel` glass card with an email-only
  account header, tightened item rows, and a divider before Log out. Links, labels,
  targets, and the logout handler unchanged.
- No functional change anywhere; `AuthProvider`, the router, and `ProtectedRoute` are
  untouched.

## Test impact

Vitest (`cd web && npm test`). The only shell test file is
`web/src/shell/UserMenu.test.tsx` (four tests). **There is no `HoloShell.test.tsx`**, so
`HoloShell` is exercised only indirectly (e.g. via any page-level render that mounts the
shell). No Go tests are affected. Every existing `UserMenu` assertion is designed to
survive because it queries by role/name/text, not by container classes:

| Existing test (`UserMenu.test.tsx`) | Survives? | Why |
| --- | --- | --- |
| `opens and closes on outside click` | yes | Toggle matched by `getByRole('button', { name: /ada@studio.dev/i })` - the email stays the button's accessible name (kept as a plain text node inside the chip). `getByText('Log out')` still resolves (label preserved). Outside-click close logic untouched. |
| `closes on Escape` | yes | Same toggle selector; the Escape `keydown` handler is unchanged. |
| `exposes menu semantics and reflects open state via aria attributes` | yes | `aria-haspopup="menu"` and `aria-expanded` are preserved on the toggle verbatim; only the toggle's `className`/inner-content changes, not its attributes. **This test is the guard that the a11y scope line is respected** - it asserts the toggle's existing ARIA, which we keep, while not asserting `role="menu"` on the panel (which stays out of scope). |
| `calls onLogout when Log out is clicked` | yes | `getByText('Log out')` resolves (label kept) and the button's `onClick={onLogout}` is preserved verbatim (no `setOpen(false)` wrapper added), so `onLogout` fires exactly once. |

**Load-bearing preservation risks (called out in the steps):**

1. **The toggle's accessible name must remain the raw email.** If the initials avatar or
   caret (Open Decision 4) are added, they must be non-text decorative spans **outside** the
   email text node, or the accessible name could change and break the `/ada@studio.dev/i`
   selector on all four tests. Keep the email as a clean text child.
2. **The `Log out` label and `onClick={onLogout}` must be verbatim.** Renaming to the
   prototype's wording or wrapping the handler would break the last two tests.
3. **Do not add `role="menu"`/`role="menuitem"`.** That is the separately tracked backlog
   item; adding it here would both exceed scope and risk changing how the tests resolve
   items.

**Class-based assertions:** none of the current tests key on container classes, so no
assertion needs updating. If any does after implementation, update the class expectation
only (never relax a behavior assertion).

**New tests:** none required. No new component and no new behavior; `GlassPanel` and
`Eyebrow` are already independently tested. The restyle is covered by the existing four
`UserMenu` tests (behavior) plus the web lint/typecheck gate.

## Non-goals / out of scope

- **The UserMenu menu/menuitem-roles + keyboard-nav a11y work
  (`docs/backlog/feature-2026-06-05-usermenu-panel-menu-roles.md`).** Explicitly out of
  scope. This restyle does **not** add `role="menu"`/`role="menuitem"`, `aria-controls`,
  roving tabindex, or arrow-key handling. It leaves the panel a plain `GlassPanel` of
  `Link`/`button` elements. That backlog item stays open and is done on its own.
- **No nav change.** No nav link added, removed, or re-targeted; `/admin` and `/profile/*`
  keep their current placeholder targets. This is a restyle, not an information-architecture
  change.
- **No backend, Go, router, `ProtectedRoute`, `AuthProvider`, or primitive change.** No
  version string, sync/health indicator, server-name, session count, role display, or
  radial ambient background (all backend-blocked or net-new).
- **No responsive/overflow rework.** The nav-row overflow behavior on narrow viewports is
  unchanged from today (see Open Decision 6).

## Implementation task breakdown

Both files change independently; do `UserMenu.tsx` first (it has the test guard), then
`HoloShell.tsx`. Steps are bite-sized and ordered; each names its verification. This is
small enough to implement directly from this spec without a separate plan doc.

### UserMenu.tsx

1. **Imports.** Add `import { GlassPanel } from '../components/holo'`. Keep
   `useEffect`/`useRef`/`useState`, `Link`, and the `UserMenuProps` interface. (Add
   `Eyebrow` only if the initials/brand treatment ends up needing it - it does not; skip.)
   - Verify: no unused-import lint error.

2. **State/effect/props unchanged.** Do **not** touch the `open` state, the outside-click +
   Escape `useEffect`, the `ref`, or the `UserMenuProps` (`email`, `onLogout`). Confirm they
   are byte-for-byte the same after editing.
   - Verify: the "outside click" and "Escape" tests still pass.

3. **Toggle chip restyle.** Keep the `<button onClick={() => setOpen(v => !v)}
   aria-haspopup="menu" aria-expanded={open}>` attributes verbatim. Change only its
   `className` to the accent-tinted mono chip:
   `` `flex items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}` ``
   and wrap the email as
   `<span className="text-fg normal-case tracking-normal">{email}</span>` so the email is
   the clean accessible-name text (not uppercased). Do **not** add an avatar/caret unless
   Open Decision 4 is accepted; if added, they are decorative spans **outside** the email
   span.
   - Verify: `getByRole('button', { name: /ada@studio.dev/i })` still resolves;
     `aria-haspopup`/`aria-expanded` unchanged (the "aria attributes" test passes).

4. **Dropdown panel -> GlassPanel.** Replace the panel
   `<div className="absolute right-0 mt-2 w-44 rounded-card border border-border p-1
   text-[12px] shadow-xl" style={{background:'rgba(14,12,30,0.96)'}}>` with
   `<GlassPanel className="absolute right-0 mt-2 w-56 p-1.5 text-[12px]">` and the matching
   closing `</div>` with `</GlassPanel>`. Remove the inline `style` (the glass comes from
   the primitive). **Do not add any `role` attribute.**
   - Verify: dropdown renders as a glass card when `open`; no `role="menu"` introduced.

5. **Account header (email-only).** As the first child inside the panel, add a bordered
   header row showing the email:
   `<div className="mb-1.5 flex items-center gap-2.5 border-b border-border px-2.5 pb-2.5
   pt-2"><span className="truncate text-[12.5px] text-fg">{email}</span></div>`. **Omit** the
   `ADMIN · server-name` sub-line (backend-blocked). (Initials avatar only if Open Decision 4
   accepted, placed before the email span.)
   - Verify: the email appears in the open panel; no server-name/role text.

6. **Menu links restyle (targets/labels preserved).** Keep the three `Link`s exactly -
   `to="/profile"` "Profile", `to="/profile/password"` "Password", `to="/profile/sessions"`
   "Sessions". Change only each `className` from `block rounded px-3 py-2 hover:bg-white/5`
   to `block rounded-md px-2.5 py-2 text-fg hover:bg-white/5`. Do **not** rename labels,
   change targets, add hints, or add icons.
   - Verify: the three links resolve with their current text and `to` targets.

7. **Divider + Log out (handler/label preserved).** Before the `Log out` button add
   `<div className="my-1.5 h-px bg-border" />`. Keep the button's `onClick={onLogout}` and
   text `Log out` verbatim; change only its `className` to
   `block w-full rounded-md px-2.5 py-2 text-left text-err hover:bg-white/5`. Do **not** add
   a `setOpen(false)` wrapper.
   - Verify: `getByText('Log out')` resolves and clicking it calls `onLogout` once (the last
     test passes).

### HoloShell.tsx

8. **Imports.** Add `import { Eyebrow } from '../components/holo'` (if Open Decision 2(b) is
   accepted; otherwise skip and use a plain span). Keep `NavLink`, `useNavigate`, `useAuth`,
   `UserMenu`, and `ReactNode`. Do not touch the `NAV` constant, `useAuth`/`useNavigate`
   wiring, or the `onLogout` handler.
   - Verify: no unused-import lint error; `NAV` unchanged.

9. **Header -> glass strip.** Change the `<header>` class from
   `flex items-center justify-between border-b border-border px-5 py-3` to
   `flex items-center justify-between border-b border-border bg-white/[0.025]
   backdrop-blur-[10px] px-[22px] py-3`. Keep the outer
   `<div className="min-h-screen bg-bg text-fg">` and `<main className="p-5">{children}
   </main>` unchanged. Do **not** add the radial ambient background.
   - Verify: bar renders as a blurred glass strip with a bottom border; page background
     unchanged.

10. **Brand wordmark.** Replace the `<span className="font-sans text-[18px] font-bold">relay
    <span className="text-accent">.</span></span>` with the mono `RELAY` wordmark:
    `<Eyebrow className="text-accent">RELAY</Eyebrow>` (Open Decision 2(b)) or an equivalent
    plain `<span className="font-mono text-[11px] font-semibold uppercase tracking-[0.18em]
    text-accent">RELAY</span>` (2(a)). **Omit** the `2.4.1` version string. Optional gradient
    `R` tile before it only if Open Decision 1 accepted:
    `<span className="grid h-5 w-5 place-items-center rounded-[5px] bg-gradient-to-br
    from-accent to-accent-b text-[11px] font-bold text-white">R</span>` (wrap the tile +
    wordmark in a `flex items-center gap-2.5` span). Keep the wordmark a non-link `<span>`.
    - Verify: `RELAY` wordmark renders in accent mono; no version string; wordmark is not a
      link.

11. **Nav active-item treatment.** Keep the `NAV.map(n => <NavLink key={n.to} to={n.to} ...>
    {n.label}</NavLink>)` structure verbatim. Change the container from
    `<nav className="flex gap-4 text-[12px]">` to `<nav className="flex gap-0.5">`, and the
    `NavLink` `className` callback to:
    `` ({ isActive }) => `px-[14px] py-[7px] text-[13px] tracking-[0.02em] transition-colors border-b-2 ${isActive ? 'text-fg border-accent' : 'text-fg-mute border-transparent hover:text-fg'}` ``.
    Do not change `key`, `to`, or `{n.label}`.
    - Verify: the active route's link shows the accent underline + `text-fg`; inactive links
      are muted with a transparent underline and hover to `text-fg`; nav targets unchanged.

12. **Right cluster unchanged (minus omitted dot).** Confirm the right side is exactly
    `<UserMenu email={user?.email ?? ''} onLogout={onLogout} />`. Do **not** add the
    `SYNC OK` dot (backend-blocked). The `justify-between` on the header keeps it
    right-aligned.
    - Verify: `UserMenu` renders on the right; no sync/status dot present.

### Whole-workstream verification

13. **Run tests.** `cd web && npm test` (or scoped to `web/src/shell/UserMenu.test.tsx`).
    - Verify: all four `UserMenu` tests pass unchanged.

14. **Lint / typecheck.** Run the web lint/typecheck gate.
    - Verify: clean; no unused imports; no type errors on `GlassPanel`/`Eyebrow` usage or the
      `NavLink` className callback.

15. **web/dist hygiene.** A frontend build dirties the tracked `web/dist`. `git checkout --
    web/dist/` before assembling the PR (per project convention; `web/dist` is not maintained
    per-PR).
    - Verify: `web/dist` not in the staged diff.

## Open decisions

1. **Gradient `R` logo tile.** Recommendation (baked in as optional): include a small
   gradient `R` tile before the `RELAY` wordmark, matching the prototype brand lockup. It is
   decorative and net-new (the current shell has only the `relay.` text wordmark), so it is
   flagged. Alternative: wordmark only, no tile (simplest, fully avoids net-new decoration).
   Confirm which. Cosmetic either way.
2. **Brand wordmark via `Eyebrow` vs plain span.** Recommendation: `<Eyebrow
   className="text-accent">RELAY</Eyebrow>` - reuses the primitive; the appended
   `text-accent` overrides its default `text-fg-mute`. Alternative: a plain mono `<span>`
   with the same classes (no primitive dependency). Confirm.
3. **Radial ambient background.** Recommendation (baked in): **omit** it; keep the app's flat
   `bg-bg`, consistent with every other relaid-out page. Alternative: port the prototype's
   `radial-gradient(...)` ambient stack onto the shell wrapper. Confirm the omission.
4. **UserMenu initials avatar + caret.** Recommendation: **omit** both for the smallest,
   lowest-risk restyle (they are decorative and the avatar/caret risk touching the toggle's
   accessible name if done carelessly). Alternative: add a 22px gradient initials avatar
   (`email.split('@')[0].slice(0,2).toUpperCase()`) and a caret to the toggle, and the 32px
   avatar to the dropdown account header - all as decorative spans outside the email text
   node. Confirm; if added, the test-safety note in Test impact #1 governs.
5. **Dropdown width.** Recommendation (baked in): widen `w-44` -> `w-56` toward the
   prototype's roomier panel. Alternative: keep `w-44`. Cosmetic; confirm.
6. **Nav overflow on narrow viewports.** Recommendation (baked in): **out of scope** - keep
   today's behavior (single flex row, no responsive collapse). The shell has no responsive
   logic today and adding a mobile nav/hamburger is net-new feature work, not a restyle.
   Flag as a possible future backlog item if narrow-viewport support is wanted; do not
   address here. Confirm.
