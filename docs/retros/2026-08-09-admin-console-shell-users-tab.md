---
date: 2026-08-09
topic: admin-console-shell-users-tab
branch: claude/pr-merging-session-0674dd
range: 5d492f9..HEAD
---

# Session Retro: 2026-08-09 - admin-console-shell-users-tab

**TL;DR:** Iteration 1 of a 5-item unattended `/autopilot` batch replaced the `/admin`
`JobsPlaceholder` stub with an admin-gated `/admin/:tab` shell (registry-driven pill tab bar,
`/admin` and unknown tabs redirecting to `/admin/users`, Admin nav entry filtered on `is_admin`)
plus a fully wired Users tab: list with sort, cursor pagination, `?include_archived=true`, and a
debounced exact-match `?email=` filter; create; rename; archive/unarchive behind `ConfirmDialog`;
and admin password reset. Frontend-only, zero Go changes. Web suite grew from 355 to 445 tests and
the production build was clean, both re-run by the conductor on the settled tree. The omnibus
five-tab backlog item was closed as decomposed after this first slice, with the four remaining tabs
carved into their own items and deliberately absent from the registry. Review returned 0 high /
4 medium / 8 low; all four mediums and four lows were fixed, including a vacuous test the plan
itself specified and a password reset that failed completely silently.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`.
- **Plan** `docs/superpowers/plans/2026-08-08-admin-console-shell-users-tab.md`.
- **Feature** plus two review-fix commits:
  - `web/src/admin/tabs.ts` - the `ADMIN_TABS` registry with exactly one entry, `DEFAULT_ADMIN_TAB`,
    and `findAdminTab`. Unbuilt tabs are absent, so an unknown segment redirects rather than
    rendering an empty panel.
  - `web/src/admin/AdminPage.tsx` / `AdminTabs.tsx` - the shell: Holo header (eyebrow + 32px title),
    pill-group tab bar from the registry, panel switch on `useParams().tab`.
  - `web/src/app/AdminRoute.tsx` - `is_admin` route guard nested inside `ProtectedRoute`; non-admins
    hitting `/admin/*` land on `/jobs`.
  - `web/src/shell/HoloShell.tsx` - `NAV` entries carry `adminOnly` and are filtered
    (`NAV.filter((n) => !n.adminOnly || user?.is_admin)`), so non-admins see no Admin tab.
  - `web/src/admin/users/api.ts` - six typed clients written to the **code**, not to the backlog
    item's description (see Problems #1).
  - `web/src/admin/users/useAdminUsers.ts` - the list query, `keepPreviousData`, deliberately no
    `refetchInterval`.
  - `web/src/admin/users/useAdminUserActions.ts` - five mutations, each invalidating the bare
    `['users']` prefix, no optimistic updates (nothing polls, so there is no lag to hide).
  - `web/src/admin/users/{UsersTab,UsersTable,CreateUserForm,ResetPasswordDialog}.tsx` - the tab
    composition, table with inline rename and per-row actions, inline create panel, and the
    password-reset form dialog.
  - `web/src/lib/useDebouncedValue.ts` - generic debounce hook in `lib/` rather than `admin/`,
    since it is not admin-specific.

## Key Decisions

- **Decompose the omnibus item, close it after slice one.** `feature-2026-06-26-admin-console-pages`
  specified five tabs and its own text said "slice per tab - each is independently shippable"; one
  tab (the Invites list) is backend-blocked on a `GET /v1/invites` that does not exist. It was closed
  as decomposed with the four remaining tabs carved into their own items, following the precedent set
  by `feature-2026-06-26-job-actions-submit-cancel-retry`.
- **Unbuilt tabs are absent from the registry, not stubbed.** The alternative - four "coming soon"
  panels - would have shipped four dead tabs. The registry shape makes adding a tab later a one-line
  change, so there is no cost to leaving them out.
- **`/admin/:tab` path segments over `?tab=`.** The backlog item offered either. Path segments won
  because there is **no `useSearchParams` usage anywhere in `web/src`** and the router already uses
  path params everywhere (`/jobs/:id`, `/workers/:id`, and a reserved `/profile/*`). Adopting a
  query-string idiom for one screen would have introduced a second competing state-in-URL convention.
- **Omit unbacked hi-fi elements rather than fake them.** No `SESSIONS` count (no per-user token-count
  endpoint), no `LAST LOGIN` (no column in `users`), no `service` role (mock fiction - relay's model
  is one `is_admin` boolean), no header `VERSION`/`BUILD`/`DB`/`UPTIME` strip (deferred to the
  server-overview tab item). Continues the standing rule from the Holo relayout program.
- **No Archive control on the acting admin's own row.** The server 400s `cannot archive yourself`, so
  rendering the button would be a guaranteed-failing dead control. The unpre-emptable guards
  (last-active-admin, already-archived, unknown id) surface as inline errors instead.

## Problems Encountered

1. **The backlog item's endpoint description was wrong in three places, and the code won.** The item
   said rename/role was `PATCH /v1/users/:email`; it is `PATCH /v1/users/{id}`, **UUID-keyed and
   name-only**. The item implied role could be changed; **no endpoint mutates `is_admin` after
   creation at all** - `updateUserRequest` has a single `Name` field, so role is set once at
   `POST /v1/users`. And admin password reset is not a per-user path: it is
   `POST /v1/users/password-reset` keyed by email in the body. All three were caught during spec by
   reading `internal/api/users.go` and `internal/api/auth.go`, which is the only reason the API
   clients were correct on the first write. **Lesson: verify a backlog item's technical claims
   against the code during the spec phase, not during implementation** - had these gone unchecked, a
   role-change control and an `:email`-keyed PATCH would have been planned, built, and then
   discovered broken at test time. This echoes the durable lesson that
   [[feedback_backlog_proposal_not_contract]]: an item's Proposal section is a hypothesis about the
   code, not a description of it.
2. **A vacuous test shipped from the plan itself.** The plan's no-poll test asserted "no second
   request fired" after `await new Promise((r) => setTimeout(r, 120))`, aimed at catching an
   accidental `refetchInterval: 3000` copy-pasted from `useWorkers`. 120ms is **4% of the interval it
   claims to catch**, so adding the interval left the test green. Review caught it; the conductor then
   independently proved it RED by injecting `refetchInterval: 3000` into the hook. The fix is
   deterministic - assert the resolved query's own options rather than racing a wall clock:
   `expect(options?.refetchInterval).toBeUndefined()`. **Lesson: the plan is not a safe source of test
   design.** A timing-based absence assertion must have its timescale checked against the thing it
   claims to catch, and the deterministic form (assert the resolved option) is strictly better than any
   wall-clock wait.
3. **The re-review found the fixed test was still weak.** `expect(options?.refetchInterval)
   .toBeUndefined()` asserts only a property's *absence*, so if TanStack ever stopped merging observer
   options onto `Query.options` the probe would break and the test would silently re-green - passing
   while proving nothing. Fixed with a positive control through the identical code path:
   `expect(options?.placeholderData).toBeDefined()`, since `placeholderData: keepPreviousData` is set
   on this hook and lands via the same merge. **Lesson: an absence assertion needs a paired positive
   control proving the probe still works.** This is the generalization of the existing "a green test
   can be vacuous" lesson to negative assertions specifically - absence tests fail open by default.
4. **A password reset failed completely silently.** The mutation's error rendered only in `UsersTab`'s
   page-level error box, which sits **behind** `ResetPasswordDialog`'s own `fixed inset-0 z-50` scrim,
   and the submit button re-enabled on settle. Net behaviour: the admin clicks Reset password, nothing
   visible happens, clicks again, nothing happens. Not theoretical - reachable via a password over
   72 bytes (routine password-manager output), which hits bcrypt's `ErrPasswordTooLong` and returns an
   opaque 500. Fixed on both layers: the dialog now takes an `error` prop and renders it inside itself,
   and validates the 72-byte limit client-side. **Lesson: error state must live in, or above, the layer
   that owns the interaction.** A modal that renders over a page cannot rely on that page's error
   surface; any new overlay needs its own error slot wired at the moment it is introduced.
5. **The `relay-code-reviewer` agent cannot run the skills its own definition tells it to run.**
   `.claude/agents/relay-code-reviewer.md` instructs "Invoke the /code-review skill via the Skill
   tool" and "/security-review skill via the Skill tool", but its frontmatter grants only
   `tools: Read, Grep, Glob, Bash` - no Skill tool. Every review ever dispatched to that agent has
   silently been an ad-hoc manual pass, with no failure signal, because a subagent that cannot call a
   skill just proceeds without it. **This is a process finding, not a code one: the playbook documented
   one pipeline and the pipeline ran another, undetected.** Filed as
   `docs/backlog/bug-2026-08-09-code-reviewer-agent-missing-skill-tool.md`. Worth generalizing: agent
   definitions that name a tool in their prose should be checked against the `tools:` grant, since the
   mismatch is silent by construction.
6. **Phase 4 did not run the documented `relay-verify` workflow - recorded as a deviation, not a clean
   pass.** `.claude/workflows/relay-verify.js` requires an explicit user opt-in to run a Workflow, which
   an unattended batch does not have. The conductor substituted a direct `relay-code-reviewer` dispatch
   and skipped the integration-tester lane on the rationale that a zero-Go diff gives it no surface.
   Both substitutions are defensible, but the honest statement is that this slice got a single-reviewer
   pass rather than the documented parallel fan-out across dimensions - and, per Problem #5, that
   single reviewer was running without its skills. The playbook needs a documented unattended path for
   Phase 4 rather than leaving each autopilot run to improvise one.

## Findings Triage

- **0 high.**
- **4 medium, all fixed with tests.** The two named above (the vacuous no-poll test, the silently
  failing password reset with its 72-byte client-side guard) plus two others fixed in the same pass.
- **8 low: 4 fixed, 4 accepted as minor.** The re-review's weak-absence-assertion finding
  (Problems #3) is among the fixed set.

## Known Limitations

- **Four of five admin tabs are unbuilt.** Invites, Agent enrollments, Reservations, and Server
  overview are absent from `ADMIN_TABS` by design and tracked as
  `feature-2026-08-08-admin-{invites,enrollments,reservations,server-overview}-tab`. The Invites
  *list* half stays blocked on `GET /v1/invites`
  (`feature-2026-06-26-web-enabler-backend-endpoints`).
- **No role-change control.** An existing user cannot be promoted or demoted from the UI because no
  endpoint mutates `is_admin` after creation. `is_admin` is settable only at
  `POST /v1/users`. Filed as `docs/backlog/feature-2026-08-09-user-role-change-endpoint.md`.
- **`ResetPasswordDialog` inherits the un-trapped modal baseline.** It matches `ConfirmDialog`'s
  a11y level (`role="dialog"`, `aria-modal`, labelled by title, Escape dismisses, first field
  autofocused) but has no focus trap and uses a document-global Escape listener. This slice makes it
  a **third** consumer of that baseline, which is one more than the "schedule this before a third
  consumer appears" threshold set when
  `idea-2026-07-01-confirmdialog-focus-trap-hardening` was filed. It should be scheduled now.
- **`AuthProvider`'s cached user is not refreshed when an admin renames themselves.** Harmless today
  because the header `UserMenu` shows the email, not the name. Called out in the spec so it is not
  rediscovered as a bug.

## Improvement Goals

Carried forward from `2026-07-01-autopilot-and-web-relayout`:

- **Independently re-verify the working tree and re-run the green gate after every code subagent**
  ([[feedback_verify_tree_not_subagent_claims]]) - **honored.** The conductor re-ran the full web
  suite and the production build itself on the settled tree, and went further: it personally proved
  the Problem #2 test RED by injecting `refetchInterval: 3000`, rather than accepting the fix on the
  engineer's word.
- **Test invalidation/refetch with a real active observer, not a `fetchQuery` seed**
  ([[reference_tanstack_invalidation_test_needs_active_observer]]) - **honored.** The
  `useAdminUserActions` invalidation tests mount the list query via `renderHook`, and the plan
  called out the reason inline so the engineer could not accidentally revert to a seed.
- **Confirm which design-fidelity layer is authoritative before analyzing a gap**
  ([[reference_holo_handoff_two_layers]]) - **honored.** The spec names `hifi3-holo-pages.jsx`
  (`HoloAdmin`) as authoritative and `reference/screens/admin.js` as structure-only, and states that
  the hi-fi wins on visual disagreements.
- **Large-migration playbook (primitives-first, one PR per page, don't force the primitive,
  omit-unbacked-not-fake)** - **partly applicable, honored where it was.** Not a migration, but
  omit-unbacked-not-fake and "don't force the primitive" both applied: `ProgressBar`, `KpiStat`,
  `StatusDot`, and `Panel` were explicitly ruled out as having no semantics here, and row mini-actions
  used literal classes instead of bending `PillButton` (two competing padding utilities on one element
  resolve by stylesheet order, not class-attribute order).

New goals from this iteration:

- **Verify a backlog item's technical claims against the code during spec, not implementation.** An
  item's Proposal is a hypothesis about the code. Read the handler, the request struct, and the route
  registration before writing the client. **Candidate for durable memory** - or better, an amendment to
  the existing [[feedback_backlog_proposal_not_contract]] note, which currently says "parts may be
  already done, dead, or duplicative" but does not cover "the described API does not exist as
  described."
- **Treat the plan as an untrusted source of test design.** Plans are written before the code exists,
  so their test bodies are guesses. Every plan-supplied assertion needs its non-vacuity checked
  independently; a timing-based assertion needs its timescale compared against the interval it claims
  to catch. **Strong candidate for durable memory** - this is the third iteration in a row where a
  vacuous test came from the plan.
- **Pair every absence assertion with a positive control on the same code path.** `toBeUndefined()`
  and `not.toBeInTheDocument()` fail open: if the probe breaks, they pass. **Strong candidate for
  durable memory**, as a companion to the existing
  [[feedback_regression_test_must_distinguish_fix]] note, which covers RED-proving but not the
  probe-still-works half.
- **An overlay owns its own error surface.** When adding a modal, dialog, or any `fixed inset-0`
  layer, wire an error slot into it at introduction time; never let its failures render into the page
  behind its scrim. **Candidate for durable memory** - narrower than the others, but the failure mode
  (a button that does visibly nothing) is severe and easy to reintroduce.
- **Check an agent definition's prose against its `tools:` grant.** A subagent told to invoke a skill
  it has no Skill tool for fails silently. Filed as a bug; the general check belongs in whatever
  reviews `.claude/agents/`. **Candidate for durable memory**, framed as a tooling invariant.
- **Give the playbook an explicit unattended Phase 4 path.** `relay-verify` needs an opt-in autopilot
  cannot give, so every unattended run improvises. Not yet a memory candidate - the right fix is a doc
  change in `docs/agent-team/README.md`, not a habit.

## Files Most Touched

- `web/src/admin/users/UsersTab.tsx` - the composition point: control row, loading/error/empty triad,
  table, footer, and all four dialogs and forms. The largest new file and the one that owns the
  page-level error box implicated in Problem #4.
- `web/src/admin/users/UsersTable.tsx` - table, sortable headers with `aria-sort`, role pill, archived
  dimming, inline rename, and the own-row Archive guard.
- `web/src/admin/users/api.ts` - the six clients, and the file where Problem #1's three corrections
  landed (UUID-keyed name-only PATCH, body-keyed password reset, no role mutation).
- `web/src/admin/users/useAdminUsers.ts` + `useAdminUsers.test.tsx` - the no-poll decision and the
  test that was vacuous (Problem #2) then weak (Problem #3) before it was sound.
- `web/src/admin/users/ResetPasswordDialog.tsx` - the silent-failure fix (Problem #4): an `error` prop
  rendered inside the dialog plus client-side 72-byte validation.
- `web/src/admin/users/useAdminUserActions.ts` - five mutations on the bare `['users']` prefix.
- `web/src/admin/{AdminPage,AdminTabs}.tsx` + `tabs.ts` - the registry-plus-switch shell.
- `web/src/app/{router.tsx,AdminRoute.tsx}` - the `/admin` + `/admin/:tab` pair replacing the stub,
  behind the `is_admin` guard.
- `web/src/shell/HoloShell.tsx` - `adminOnly` nav filtering.
- `web/src/lib/useDebouncedValue.ts` - the one addition outside `web/src/admin/` and `web/src/app/`.

## Verification

- Full web suite green: 445 tests, up from 355 before this slice.
- Production build green (`tsc -b && vite build`).
- Both re-run by the conductor on the settled tree rather than trusted from the implementer's report.
- The Problem #2 fix was RED-proven by the conductor independently, by injecting
  `refetchInterval: 3000` into `useAdminUsers` and confirming the test fails.
- Code review: 0 high, 4 medium (all fixed), 8 low (4 fixed, 4 accepted as minor) - delivered by a
  direct `relay-code-reviewer` dispatch rather than the documented `relay-verify` fan-out, and by a
  reviewer that could not invoke its own `/code-review` and `/security-review` skills. See Problems
  #5 and #6.
- No Go files changed, so no `make test` / `make test-integration` run was required and no Invariant
  (epoch fence, single job-spec pipeline, bounded sender, identity-checked teardown, interior
  pointers, single JSON entry point) was in play. The frontend analogue that was respected: every
  request goes through `apiFetch`; no component calls `fetch` directly.
