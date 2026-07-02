# Auth Screens Holo Relayout (Login + Register)

- Date: 2026-07-01
- Status: Draft (design; awaiting review)
- Owner: relay-tpm
- Scope: web SPA only (`web/src/auth/LoginScreen.tsx`, `web/src/auth/RegisterScreen.tsx`).
  No backend, no Go, no `AuthProvider` change, no shared-primitive change.

## Problem

The shipped auth screens (`web/src/auth/LoginScreen.tsx`, `web/src/auth/RegisterScreen.tsx`)
are small, working forms that predate the picked "Holo" hi-fi design and the shared primitive
set at `web/src/components/holo/`. Each hand-builds its own card shell as a flat
`rounded-card border border-border bg-white/5 p-6 backdrop-blur` `<form>`, a hand-rolled
header, and the generic full-width `Button` for submit. That vocabulary now lives in reusable
primitives (used by the worker pages, the jobs-list relayout, and the new-job relayout, which
are the migration references). The two screens duplicate the same card/header/submit structure,
so the restyle must be applied to both consistently.

This is a **pure restyle/relayout of two existing, working forms**. Every data path, mutation,
validation rule, error surface, config gate, redirect, and navigation link is preserved exactly.
Only structure and styling change, rebuilt from the shared primitives.

## Design authority and token mapping

Follows the same approach as the new-job relayout
(`docs/superpowers/specs/2026-07-01-new-job-relayout-design.md`), the jobs-list relayout
(`docs/superpowers/specs/2026-07-01-jobs-list-holo-relayout-design.md`), and the worker relayout
(`docs/superpowers/specs/2026-07-01-holo-primitives-worker-detail-design.md`): consume the merged
primitives, do not force a primitive where the existing control is already reasonable, and
preserve all behavior.

The **authoritative look** is `HoloAuth` in the hi-fi Holo prototype
(`design_handoff_relay_holo/hifi3-holo-pages.jsx`, ~line 366), **not** the lo-fi
`reference/screens/*` sketch. The app keeps its cyan accent and its fixed `#050410` background.
The prototype threads a `C` token bag (inline styles) into its components; we **do not** port the
`C` bag or the `makeTokens` machinery. `C.*` maps onto the existing `tokens.css` Tailwind classes.
Confirmed token mapping (classes verified present in `web/src/theme/tokens.css`):

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
| `glassPanel(C)` radius `14` | `GlassPanel` / `rounded-card` |

The `web/src/components/holo/` primitives are already merged to main. This spec consumes them; it
does not add or modify any primitive.

## Backend reality: what `HoloAuth` shows that is NOT backed by the real API

`HoloAuth` is a marketing-fidelity mock and renders several affordances the relay auth API does
not implement. Per the "don't render backend-blocked bits" stance, **these are out of scope and
must NOT be rendered** in the restyled screens:

| `HoloAuth` element (`hifi3-holo-pages.jsx`) | Backend reality | Decision |
| --- | --- | --- |
| `forgot?` link next to the PASSWORD label (~line 402) | No public/unauthenticated forgot-password endpoint exists. `PUT /v1/users/me/password` is an authenticated self-change (you must already be logged in); `POST /v1/users/password-reset` is **admin-only**. Neither is reachable from the sign-in screen. | **Omit.** Do not render a "forgot?" link. |
| `COORDINATOR · relay.studio.dev` server indicator (top-left, ~line 371) | The SPA is served by the same `relay-server` it talks to; there is no server-picker and no exposed server-name/version field on the public surface. | **Omit** (ambient chrome; net-new, no backing data). |
| `RELAY · 2.4.1` version badge (top-right, ~line 377) | No version string is exposed to the anonymous client (`GET /v1/config` returns only `allow_self_register`). | **Omit.** |
| `manage in Profile → Sessions` session note (~line 421) | No Profile page or Sessions UI ships yet (Profile is pending per project status). Linking there would dangle. | **Omit the "manage in Profile" wording.** The plain "30-day token" helper copy the app already shows is fine and is preserved (see below). |
| `terminal user? $ relay login → writes ~/.relay/config.json` CLI hint (bottom, ~line 442) | Accurate as prose, but it is ambient marketing chrome, not an app affordance. | **Omit** (net-new content on a pure restyle; not required). |
| `OPEN` badge next to the register link when self-register is on (~line 430) | This maps to a real signal: `GET /v1/config`'s `allow_self_register`. But that signal already drives the **register screen's** subtitle copy, and the login screen does not currently fetch `/config`. Adding a `/config` fetch to the login screen to render an `OPEN` badge is net-new behavior/data-fetching. | **Omit on login.** Do not add a `/config` fetch to `LoginScreen`. (See Open Decisions.) |

What IS backed and preserved: email + password login, the register fields (name, email, password,
and invite token when self-register is off), the `GET /v1/config` self-register gate on register,
the 30-day-token helper note, and the login<->register cross-links. See Preserved vs changed.

## What `HoloAuth` covers vs what the app has (login vs register)

`HoloAuth` is a **single component that renders the sign-in form only**; it does not include a
separate register mock. Its register affordance is just the "No account? Create an account →" /
"Register with invite →" link (whose label flips on `allowSelfRegister`). The app has two distinct
routed screens, `LoginScreen` (`/auth`) and `RegisterScreen` (`/register`), that already share the
same card/header/submit structure.

Therefore we **derive the register screen's Holo styling from the same idiom as login** (same
GlassPanel card, same eyebrow-over-H1 header, same Field/Input body, same PillButton submit, same
Holo error banner), preserving the register-specific fields and the self-register subtitle. The
handoff sanctions the login card directly; the register card reuses that card's structure with
register's own content. This keeps the two screens visually consistent, which is the point of the
task.

## Target layout

Both screens share the same outer shell and card structure, built from the shared primitives.

### Shared outer shell (both screens)

Keep the current centering wrapper on both screens:
`<div className="flex min-h-screen items-center justify-center bg-bg"> ... </div>`. Inside, the
`<form onSubmit={onSubmit}>` becomes a `GlassPanel` rendered as the form element (see next
section). The register screen's existing loading placeholder (the empty centered div shown while
`selfRegister === null`) is **preserved unchanged**.

### The auth card: GlassPanel as the `<form>`

Replace the hand-built card class on each `<form>`
(`w-[320px]`/`w-[360px]` + `rounded-card border border-border bg-white/5 p-6 backdrop-blur`) with
the `GlassPanel` primitive rendered **as the form** via its `as` prop, so the glass gradient +
inset/drop shadow replace the flat `bg-white/5`, matching every other Holo surface:

```
<GlassPanel as="form" onSubmit={onSubmit} className="w-[360px] p-6">
  ...
</GlassPanel>
```

- `GlassPanel` supplies `rounded-card`, `border-border`, the gradient, `backdrop-blur`, and the
  shadow. The `p-6` reproduces the current padding.
- `onSubmit={onSubmit}` is forwarded through `GlassPanel`'s `...rest` spread onto the underlying
  `<form>` element, so native form submission is preserved (see Submit below - this is why the
  submit button must remain a real submit).
- Width: standardize both cards to `w-[360px]` (register's current width). Login is currently
  `w-[320px]`; widening it 40px to match register makes the two screens consistent and gives the
  Holo card a bit more breathing room. (Cosmetic; see Open Decisions if login should stay 320.)

### Header block (eyebrow + H1 + subtitle)

Both screens replace their hand-rolled header text with the Holo eyebrow-over-H1 rhythm the other
pages use. `HoloAuth`'s header is a centered logo tile + `<h1>Sign in</h1>` + a muted subtitle
(~line 380-390). We keep the app's centered-card feel and adopt the eyebrow + H1 + subtitle
structure:

- **Login:**
  - `<Eyebrow>` with `COORDINATOR` (matches `HoloAuth`'s COORDINATOR framing without the
    server-name that we omit). Replaces the current `relay.` wordmark line as the micro-label.
  - `<h1 className="text-[28px] font-normal tracking-tight">Sign in</h1>` (mirrors `HoloAuth`'s
    `fontSize:28, fontWeight:400`). Replaces the current 32px `relay.` wordmark as the title.
  - Subtitle: keep the app's `Sign in to the coordinator` line, restyled to
    `text-[13px] text-fg-mute` (matching `HoloAuth`'s subtitle scale). (Copy stays "Sign in to
    the coordinator"; no need to adopt the mock's "same credentials as relay login" wording, which
    references CLI chrome we are omitting - see Open Decisions.)
- **Register:**
  - `<Eyebrow>REGISTER</Eyebrow>`.
  - `<h1 className="text-[28px] font-normal tracking-tight">Create your relay account</h1>`
    (adopts the same H1 scale as login; replaces the current 18px header). Copy preserved.
  - Subtitle: **preserve the existing conditional copy verbatim** -
    `{selfRegister ? 'Open registration is enabled.' : 'You need an invite to register.'}` -
    restyled to `text-[13px] text-fg-mute`. This is the self-register signal, kept as-is.

The header block on both is left-aligned (the app's existing convention; `HoloAuth`'s
`textAlign:'center'` logo-tile treatment is not adopted, since the app cards are left-aligned form
cards and the logo tile is decorative chrome). Keep the accent-dot wordmark spirit only via the
eyebrow; do **not** add the decorative `R` gradient logo tile (net-new decorative element).

### Form body (Field + Input, unchanged controls)

The field controls stay exactly as they are - `Field` + `Input` are already the correct,
reasonable Holo form primitives (the `Field` label is already the mono-uppercase-tracked micro-label
that `HoloAuth` uses for `EMAIL`/`PASSWORD`, and `Input` already has the dark bordered treatment).
**Do not** replace `Field`/`Input` with raw inputs from the mock.

- **Login body (preserved):** `Field label="Email"` + `Input id="email" type="email"
  autoComplete="username"`; `Field label="Password"` + `Input id="password" type="password"
  autoComplete="current-password"`. Both `value`/`onChange` bindings preserved. The `htmlFor`/`id`
  and label text are load-bearing for the tests (`getByLabelText('Email')`, `'Password'`) - keep
  them exactly.
- **Register body (preserved):** `Field label="Display name"` + `Input id="name"`;
  `Field label="Email"`; the conditional `Field label="Invite token"` (only when `!selfRegister`,
  with the `error` prop wiring and the mono/accent invite Input styling) ; `Field label="Password"
  hint="min 8 characters"` with its conditional `error` prop. All field labels, ids, the
  `!selfRegister` conditional, and the `error`-prop wiring are preserved exactly (the tests key on
  `getByLabelText('Display name')`, `'Email'`, `/invite token/i`, `'Password'`, and on the invite
  field appearing/disappearing with the config gate).

### Error surface (Holo err styling)

Both screens surface an inline error. Today login uses `<div className="mb-3 text-[12px]
text-err">{error}</div>` and register uses the same for the email-exists message, plus
`Field`-level `error` props for the invite/password validation errors. The task calls for adopting
the Holo error-banner styling.

- **Login error banner:** restyle the `{error && ...}` block from the bare `text-[12px] text-err`
  line to the Holo error-banner form the jobs-list/worker/new-job pages use, and give it
  `role="alert"` for accessibility parity with those pages:
  `<div role="alert" className="mb-3 rounded-card border border-err/40 bg-err/10 px-4 py-2
  text-[12px] text-err">{error}</div>`. The `{error}` content and the `error` state logic
  (429 -> "Too many attempts..."; else -> "Invalid email or password.") are **unchanged**; only the
  container class + the added `role` change. The tests locate the message via `findByText`, which
  still matches inside the banner.
- **Register email-exists banner:** same restyle for the `{emailExists && ...}` block ->
  `<div role="alert" className="mb-3 rounded-card border border-err/40 bg-err/10 px-4 py-2
  text-[12px] text-err">That email is already registered.</div>`. Copy and the `emailExists`
  condition unchanged; the "sign in" link that must accompany it lives in the footer link (see
  below) and is preserved.
- **Register Field-level errors (invite/password):** **leave the `Field` `error` prop wiring
  exactly as-is.** These are field-scoped validation/server errors (the client "Password must be
  at least 8 characters." message and the server `err.code` routed to either the invite or
  password field depending on `selfRegister`). `Field` already renders them in `text-err`; they are
  not banners and should stay attached to their field. No change.

### Submit (PillButton primary, kept as a form submit)

Replace the generic full-width `Button` (`type` defaults to `submit`) with the Holo `PillButton`
primitive, `variant="primary"`, matching the primary pills on the other pages:

- **Login:** `<PillButton variant="primary" type="submit" disabled={busy}>Sign in →</PillButton>`.
- **Register:** `<PillButton variant="primary" type="submit" disabled={busy}>Create account
  →</PillButton>`.

**Critical: `type="submit"` must be passed explicitly.** Both screens rely on **native form
submission** - the `<form onSubmit={onSubmit}>` fires when the submit button is clicked, and the
tests trigger the flow via `userEvent.click(screen.getByRole('button', { name: /sign in/i }))` /
`{ name: /create account/i }`. `PillButton` hardcodes `type="button"` in its JSX, but it spreads
`{...rest}` **after** the hardcoded `type`, so a caller-supplied `type="submit"` overrides it (verified
against `web/src/components/holo/PillButton.tsx`: `<button type="button" {...rest} .../>`). Passing
`type="submit"` is therefore required and sufficient; **omitting it would silently break login and
register submission** (the click would not submit the form) and fail every submit test. This is the
single most important preservation detail in this spec.

- Copy unchanged: `Sign in →` / `Create account →` (tests match `name: /sign in/i` and
  `/create account/i`; `PillButton` renders a real `<button>`, so the accessible name is preserved).
- `disabled={busy}` unchanged - `PillButton`'s base class includes `disabled:opacity-40`, so the
  pending visual works; behavior identical.
- Width: `PillButton` is an inline pill (`rounded-full px-4 py-2`), not full-width. The current
  `Button` is `w-full`. To keep the submit visually prominent as the card's primary action,
  wrap it so it renders full-width within the card: add `className="w-full justify-center"` on the
  `PillButton` (it is a `<button>`, so `w-full` stretches it; `text-center` is inherent to a block
  button but add `justify-center`/`text-center` defensively). This preserves the "big primary
  submit spanning the card" look the current full-width `Button` gives. (Cosmetic; if a compact
  pill is preferred, see Open Decisions.)

### Footer links (cross-navigation, preserved)

Both screens keep their existing footer cross-link block exactly (targets, copy, and the
`react-router-dom` `Link` element are load-bearing):

- **Login:** `New here? <Link to="/register" className="text-accent">Create an account</Link>`
  plus the `Tokens last 30 days.` micro-note. Both preserved (the token note is the app's honest
  version of `HoloAuth`'s 30-day-token line, minus the Profile→Sessions chrome we omit). Restyle is
  cosmetic only; the `to="/register"` target and text are unchanged.
- **Register:** `Already have an account? <Link to="/auth" className="text-accent">Sign
  in</Link>`. Preserved exactly - the `to="/auth"` target and the "Sign in" link text are
  load-bearing: a test asserts `getByRole('link', { name: /sign in/i })` is present alongside the
  email-exists error. Do not change the link text or target.

## Primitives used vs deliberately not used

**Used:**

- **`GlassPanel` (`as="form"`)** - the auth card on both screens, replacing the flat `bg-white/5`
  form. The one intentional visual upgrade (gradient glass + shadow), applied to match every other
  Holo surface. `onSubmit` is forwarded to the underlying `<form>`.
- **`Eyebrow`** - the `COORDINATOR` (login) / `REGISTER` (register) micro-label above the H1,
  matching the header rhythm of the jobs-list, worker, fleet, and new-job pages.
- **`PillButton` (`variant="primary"`, `type="submit"`)** - the submit on both screens, replacing
  the generic full-width `Button`.

**Deliberately not used:**

- **Generic `Button` (`../components/Button`)** - **removed from both auth screens.** After this
  change neither screen imports `Button`. Remove the now-unused import from each file. (`Button`
  keeps other consumers elsewhere; do not delete the component.)
- **`Field` / `Input`** - **kept, not replaced.** These are already the correct Holo form controls
  (the task explicitly says they stay). We do NOT swap in the mock's raw `<input>` elements. This is
  the "don't force a primitive where the existing control is reasonable" stance from the new-job
  spec.
- **`Panel`** - not used. `Panel`'s built-in header/meta/footer frame is for titled data panels;
  the auth card is a single free-form form surface where the header lives inline, so a bare
  `GlassPanel` is the right, simpler fit.
- **`Chip` / `StatusDot` / `ProgressBar` / `KpiStat`** - no status, tags, progress, or KPI content
  on these screens; none apply. (`StatusDot`'s only would-be use is the mock's `OPEN`/`SYNC OK`
  dots, which are backend-blocked chrome and omitted.)
- **The decorative `R` logo tile, server/version badges, `forgot?` link, CLI hint, Profile→Sessions
  note, and login-side `OPEN` badge** - all omitted as net-new or backend-blocked (see Backend
  reality).

## Preserved vs changed

**Preserved exactly (behavior, contracts, test-relevant):**

- `useAuth()` `login(email, password)` and `register(input)` mutations and the whole
  `AuthProvider` (untouched - not edited by this spec). The submit handlers `onSubmit` on both
  screens are unchanged: login's try/catch (429 -> rate-limit copy; else -> "Invalid email or
  password."), register's `password.length < 8` client guard, `emailExists` on 409, `err.code` on
  other `ApiError`, "Something went wrong." fallback, and the `busy` pending toggle.
- The `GET /v1/config` self-register gate on register: the `useEffect` fetching `/config`,
  `setSelfRegister`, the `selfRegister === null` loading placeholder, the `!selfRegister` invite
  field, and the conditional subtitle. All untouched.
- All field ids, `htmlFor`/label text, `type`, `autoComplete`, and `value`/`onChange` bindings on
  both screens (load-bearing for `getByLabelText` and for autofill semantics).
- The redirect-on-success: `applyAuth` in `AuthProvider` flips status to `authenticated`, and the
  route guard redirects. Untouched (lives in `AuthProvider`/router, not these screens).
- The login<->register `Link`s (`/register` from login, `/auth` from register), their copy, and the
  register "Sign in" link that a test asserts. Preserved.
- The login `Tokens last 30 days.` note and the register invite-field mono/accent styling.
  Preserved.
- The submit accessible names (`Sign in →`, `Create account →`) and the disabled-while-`busy`
  behavior (moved onto `PillButton` with `type="submit"`).

**Changed (structure/styling only):**

- Card: hand-built `bg-white/5` `<form>` -> `GlassPanel as="form"` (gradient glass); login card
  width `w-[320px]` -> `w-[360px]` to match register.
- Header: hand-rolled wordmark/title text -> `Eyebrow` + `h1 text-[28px]` + `text-[13px]` subtitle
  (register H1 up from 18px; login title text changes from the `relay.` wordmark to `Sign in`).
- Error surface: bare `text-err` line -> Holo error **banner** (`rounded-card border border-err/40
  bg-err/10 px-4 py-2`) with `role="alert"` on both the login error and the register email-exists
  message. `Field`-level errors (invite/password) unchanged.
- Submit: full-width `Button` -> `PillButton variant="primary" type="submit"` (with `w-full` to
  keep the spanning look); remove the now-unused `Button` import from each file.
- No functional change anywhere; `AuthProvider` and the mutations are untouched.

## Test impact

Vitest (`cd web && npm test`; `web/src/auth/LoginScreen.test.tsx`,
`web/src/auth/RegisterScreen.test.tsx`, `web/src/auth/AuthProvider.test.tsx`). No Go tests
affected. All existing behavior assertions are designed to survive because they query by
role/label/text, not by container classes:

| Existing test | Survives? | Why |
| --- | --- | --- |
| login: generic message on 401 | yes | `getByLabelText('Email'/'Password')` still resolve (labels/ids preserved); `getByRole('button', { name: /sign in/i })` resolves to the `PillButton` (real `<button>`, name preserved); `type="submit"` preserves form submission so `onSubmit` runs; `findByText(/invalid email or password/i)` matches inside the restyled banner. |
| login: rate-limit hint on 429 | yes | Same selectors; `error` state logic unchanged; `/too many attempts/i` matches inside the banner. |
| register: hides invite field when self-register on | yes | `/config` fetch, `selfRegister` gate, and the `!selfRegister` conditional unchanged; `getByLabelText('Email')` present, `queryByLabelText(/invite token/i)` absent. |
| register: shows invite field when self-register off | yes | Same gate; `findByLabelText(/invite token/i)` present (conditional `Field` preserved). |
| register: inline invite error on 400 | yes | `Field`-level `error` prop wiring on the invite field is preserved; `err.code` (`invite_expired`) routes to that field; `findByText(/invite_expired/i)` matches. Submit runs because `type="submit"` is set. |
| register: email-exists + sign-in link on 409 | yes | `emailExists` banner copy ("already registered") preserved; `role="alert"` added but `findByText(/already registered/i)` still matches text; `getByRole('link', { name: /sign in/i })` (the `/auth` footer link) preserved. Submit runs via `type="submit"`. |
| AuthProvider tests | yes | `AuthProvider` is untouched by this spec. |

**Load-bearing regression risk:** the one way this restyle could break tests is forgetting
`type="submit"` on `PillButton` (defaults to `type="button"`, which would not submit the form). The
implementation steps call this out explicitly and it is covered by every submit-triggering test
above; no new test is required to guard it, but see Open Decisions if a belt-and-suspenders
"submit posts credentials" assertion is wanted.

**Class-based assertions:** none of the current tests key on container classes, so no assertion
needs updating. If any does after implementation, update the class expectation only (never relax a
behavior assertion).

**New tests:** none required. No new component, no new behavior; the primitives (`GlassPanel`,
`Eyebrow`, `PillButton`) are already independently tested.

## Non-goals / out of scope

- No backend, Go, `AuthProvider.tsx`, `router.tsx`, `api.ts`, `types.ts`, or `token.ts` change.
- No new auth behavior: no forgot-password, no OAuth/SSO, no server picker, no version badge, no
  Profile/Sessions link, no CLI hint, no login-side `/config` fetch or `OPEN` badge. (All are
  backend-blocked or net-new; enumerated in Backend reality.)
- No change to `Field`/`Input`/`Button` components, and no change to any `web/src/components/holo/`
  primitive.
- No layout change to the centered full-screen shell or the register loading placeholder.

## Implementation task breakdown

Both files change in the same way; do login first, then mirror on register. Steps are bite-sized
and ordered; each names its verification. This is small enough to implement directly from this spec
without a separate plan doc.

### LoginScreen.tsx

1. **Imports.** Remove `import { Button } from '../components/Button'`; add
   `import { GlassPanel, Eyebrow, PillButton } from '../components/holo'`. Keep `Field`, `Input`,
   `Link`, `useAuth`, `ApiError`, and the `useState`/`FormEvent` imports.
   - Verify: no unused-import lint error; `Button` no longer referenced in this file.

2. **Card shell.** Replace the `<form onSubmit={onSubmit} className="w-[320px] rounded-card border
   border-border bg-white/5 p-6 backdrop-blur">` opening tag with
   `<GlassPanel as="form" onSubmit={onSubmit} className="w-[360px] p-6">` and change the matching
   closing `</form>` to `</GlassPanel>`. Leave the outer centering `<div ... bg-bg>` unchanged.
   - Verify: renders as a glass card; the form still submits (checked in step 5's tests).

3. **Header.** Replace the `relay.` wordmark div + `Sign in to the coordinator` div with:
   `<Eyebrow>COORDINATOR</Eyebrow>`, then
   `<h1 className="text-[28px] font-normal tracking-tight">Sign in</h1>`, then
   `<div className="mb-5 text-[13px] text-fg-mute">Sign in to the coordinator</div>`.
   - Verify: eyebrow + title + subtitle render; no leftover `relay.`/accent-dot wordmark.

4. **Error banner.** Change the `{error && <div className="mb-3 text-[12px] text-err">{error}</div>}`
   to `{error && <div role="alert" className="mb-3 rounded-card border border-err/40 bg-err/10 px-4
   py-2 text-[12px] text-err">{error}</div>}`. Do not touch the `error` state logic in `onSubmit`.
   - Verify: banner renders with the Holo err styling and `role="alert"`; message text unchanged.

5. **Submit.** Replace `<Button type="submit" disabled={busy}>Sign in →</Button>` with
   `<PillButton variant="primary" type="submit" disabled={busy} className="w-full justify-center">Sign
   in →</PillButton>`. **`type="submit"` is required** (see Submit section) so the form still
   submits on click.
   - Verify: `getByRole('button', { name: /sign in/i })` resolves; clicking it submits the form and
     runs `onSubmit`; `disabled` toggles with `busy`.

6. **Footer links.** Leave the `New here? / Create an account` (`to="/register"`) block and the
   `Tokens last 30 days.` note as-is (structure/targets unchanged; restyle only if a class is
   inconsistent, otherwise leave).
   - Verify: `/register` link present and targets `/register`.

### RegisterScreen.tsx

7. **Imports.** Same swap as step 1: remove `Button`, add
   `{ GlassPanel, Eyebrow, PillButton }`. Keep `Field`, `Input`, `Link`, `useAuth`, `ApiError`,
   `apiFetch`, `ConfigResponse`, and the `useEffect`/`useState`/`FormEvent` imports.
   - Verify: no unused-import lint error.

8. **Loading placeholder.** Leave the `selfRegister === null` early return
   (`<div className="flex min-h-screen items-center justify-center bg-bg" />`) unchanged.
   - Verify: no change.

9. **Card shell.** Replace the `<form ... className="w-[360px] rounded-card border border-border
   bg-white/5 p-6 backdrop-blur">` opening tag with `<GlassPanel as="form" onSubmit={onSubmit}
   className="w-[360px] p-6">` and the closing `</form>` with `</GlassPanel>`.
   - Verify: glass card renders; form still submits (checked in step 12).

10. **Header.** Replace the `Create your relay account` (18px) div + the conditional subtitle div
    with: `<Eyebrow>REGISTER</Eyebrow>`, then `<h1 className="text-[28px] font-normal
    tracking-tight">Create your relay account</h1>`, then
    `<div className="mb-5 text-[13px] text-fg-mute">{selfRegister ? 'Open registration is enabled.'
    : 'You need an invite to register.'}</div>`. Preserve the conditional expression verbatim.
    - Verify: eyebrow + title render; subtitle flips with `selfRegister`.

11. **Fields + Field-level errors.** Leave the `Display name`, `Email`, conditional `Invite token`
    (with its `error` prop and mono/accent Input styling), and `Password` (`hint="min 8
    characters"`, conditional `error` prop) `Field`/`Input` blocks **exactly as-is**. Do not change
    ids, labels, the `!selfRegister` conditional, or the `error`-prop wiring.
    - Verify: `getByLabelText` for all fields resolves; invite field appears/disappears with the
      config gate; invite/password errors still render on their fields.

12. **Email-exists banner + submit.** Restyle the `{emailExists && <div className="mb-3 text-[12px]
    text-err">That email is already registered.</div>}` to `{emailExists && <div role="alert"
    className="mb-3 rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">That
    email is already registered.</div>}`. Replace `<Button type="submit" disabled={busy}>Create
    account →</Button>` with `<PillButton variant="primary" type="submit" disabled={busy}
    className="w-full justify-center">Create account →</PillButton>` (**`type="submit"` required**).
    - Verify: banner renders with Holo err styling + `role="alert"`; `getByRole('button', { name:
      /create account/i })` resolves and submitting runs `onSubmit`.

13. **Footer link.** Leave `Already have an account? / Sign in` (`to="/auth"`) unchanged (the "Sign
    in" link text and `/auth` target are load-bearing for a test).
    - Verify: `getByRole('link', { name: /sign in/i })` present and targets `/auth`.

### Whole-workstream verification

14. **Run tests.** `cd web && npm test` (or scoped to the two auth test files).
    - Verify: all `LoginScreen.test.tsx`, `RegisterScreen.test.tsx`, `AuthProvider.test.tsx`
      assertions pass unchanged.

15. **Lint / typecheck.** Run the web lint/typecheck gate.
    - Verify: clean; no unused `Button` imports; no type errors on `GlassPanel as="form"`,
      `PillButton type="submit"`, or `Eyebrow` props.

16. **web/dist hygiene.** A frontend build dirties the tracked `web/dist`. `git checkout --
    web/dist/` before assembling the PR (per project convention; `web/dist` is not maintained
    per-PR).
    - Verify: `web/dist` not in the staged diff.

## Open decisions

1. **Login eyebrow label.** Recommendation (baked in): `COORDINATOR` (mirrors `HoloAuth`'s
   COORDINATOR framing without the omitted server-name). Alternatives: `RELAY`, `SIGN IN`. Confirm;
   trivial to change.
2. **Login card width.** Recommendation (baked in): widen login `w-[320px]` -> `w-[360px]` to match
   register for a consistent card size. Alternative: keep login at `w-[320px]`. Confirm; cosmetic.
3. **Submit width.** Recommendation (baked in): make the primary `PillButton` `w-full` so it spans
   the card like the current full-width `Button`. Alternative: a compact inline pill (default
   `PillButton` width). Confirm; cosmetic. (Either way `type="submit"` is required.)
4. **Login-side self-register `OPEN` badge / `/config` fetch.** Recommendation (baked in): **omit**
   - do not add a `/config` fetch to `LoginScreen` just to render the `OPEN` badge; it is net-new
   data-fetching on a pure restyle, and the register screen already surfaces the self-register
   state. Alternative: fetch `/config` on login too and show the `OPEN` badge next to the register
   link. Confirm the omission.
5. **Belt-and-suspenders submit test.** Recommendation (baked in): none required - the existing
   submit-triggering tests already exercise `type="submit"` end to end (they fail if it regresses to
   `type="button"`). Alternative: add one explicit "clicking Sign in POSTs to /v1/auth/login"
   assertion as a guard. Confirm; optional, low value.
