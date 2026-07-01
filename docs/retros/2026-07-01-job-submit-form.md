---
date: 2026-07-01
topic: job-submit-form
branch: claude/stoic-cannon-15b269
pr: "2026-07-01 job-submit-form (autopilot iteration 4)"
---

# Session Retro: 2026-07-01 - job-submit-form

**TL;DR:** Iteration 4, the last of this autopilot batch, shipped the "New Job submit form
(+ New job)" item: a `/jobs/new` route with a JSON job-spec textarea editor prefilled with a minimal
valid starter spec, plus a "+ New job" entry point on the jobs list. Frontend-only, consuming the
existing `POST /v1/jobs` endpoint (auth-only, NOT admin-gated). A single `createJob(spec)` client, a
`useCreateJob` hook invalidating `['jobs']` + `['job-stats']`, and a `NewJobPage` that keeps client
validation deliberately minimal (valid JSON + non-empty name + non-empty tasks array) and defers all
task-level rules to the server's `jobspec.Validate` - so no parallel validation path drifts from the
single job-spec pipeline invariant. Spec and plan committed first; feature landed in commit 23eadae.
Full web suite (290 tests, 59 files) and the production build were green, re-run independently by the
conductor. Code review came back CLEAN (no findings) with both load-bearing tests mutation-checked.
As in iteration 3, the engineer caught several real defects in the plan's literal test code during
TDD and fixed each properly.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-07-01-job-submit-form-design.md`.
- **Plan** `docs/plans/2026-07-01-job-submit-form-plan.md`.
- **Feature** commit 23eadae:
  - A `/jobs/new` route (`NewJobPage`) with a JSON job-spec textarea editor prefilled with a minimal
    valid starter spec, registered adjacent to `/jobs/:id` with a route-collision guard so `new` is
    not swallowed by the `:id` param route.
  - A "+ New job" entry point `Link` on `JobsPage`. It is NOT admin-gated, because `POST /v1/jobs` is
    auth-only, not admin-only.
  - `createJob(spec)` client calling `POST /v1/jobs`.
  - `useCreateJob` hook invalidating `['jobs']` + `['job-stats']` on success. It deliberately does NOT
    invalidate `['job', id]`, because the job is brand new and has no prior detail cache entry;
    navigation to the created job is handled by the page, not the hook.
  - Client validation deliberately minimal: valid JSON, non-empty `name`, non-empty `tasks` array.
    Everything else (task-level rules, dependency shape, source-spec semantics) defers to the server's
    `jobspec.Validate` rather than being reimplemented client-side.
  - Backend `{"error": msg}` surfaced inline in a `role="alert"` banner. Editor text is preserved on
    error; submit is disabled while pending; the error resets on resubmit; a 201 navigates to
    `/jobs/:id`.

## What Went Well

- **Spec and plan committed before any code.** Same discipline as iterations 1 through 3: the design
  and plan docs landed at the phase boundary first, so implementation executed against an approved,
  file-listed target rather than design-as-you-go.
- **The single job-spec pipeline invariant held by construction.** Rather than reimplement task-level
  validation client-side, the form validates only the minimum needed for a coherent submit and lets
  the server's `jobspec.Validate` own semantics. There is no second validation path to drift, which is
  exactly what the invariant is meant to prevent.
- **The entry point is correctly gated (i.e., not over-gated).** `POST /v1/jobs` is auth-only, so the
  "+ New job" link is shown to any authenticated user, not just admins - matching the endpoint's
  actual authorization rather than copying an admin gate by reflex.
- **The route-collision guard was designed in, not patched on.** Registering `/jobs/new` next to
  `/jobs/:id` with an explicit guard (and a test that fails if the guard is removed) means the
  ordering hazard is provably handled rather than working by accident of registration order.

## Notable

- **Several real plan-test defects caught during TDD and fixed properly.** Continuing the iteration-3
  pattern, working the plan test-first surfaced genuine problems the plan's literal test code had
  baked in:
  - **A hook called inside JSX passed to `element={}`** produced "Invalid hook call". Fixed by
    extracting a proper stub component rather than calling the hook in render-prop position.
  - **`userEvent.type` needing `[` escaped as well as `{`.** Both `{` and `[` are special in the
    userEvent keyboard parser, so typing a JSON array literal mangled the input until both were
    escaped. Without this the typed job spec was silently corrupted.
  - **Error-text collisions with the page's helper hint.** A plain-text error assertion also matched
    the form's helper copy. Fixed by adding `role="alert"` to the banner (also an a11y improvement)
    and scoping assertions to `findByRole('alert')`.
- **The reviewer mutation-tested the load-bearing assertions.** Code review confirmed the tests are
  non-vacuous: removing the `['job-stats']` invalidation turns the stats test RED, and removing the
  `/jobs/new` route turns the collision-guard test RED. This is the "a green test can be vacuous;
  assert a property only the fix produces" discipline in action, and it validates the conductor's
  standing practice of independent re-verification plus adversarial review as the thing that keeps
  quality honest across an unattended batch.

## Findings Triage

- **No findings.** Code review came back fully CLEAN.

## Deferred (to its own item)

- **Structured/visual job-spec form-builder** -> `docs/backlog/idea-2026-07-01-job-spec-form-builder.md`
  (priority medium). This slice ships a raw JSON textarea. A structured form-builder (per-task rows,
  dependency picker, Perforce source-spec builder) is a larger surface deferred deliberately from the
  first slice. Any client-side structural validation it adds must still defer semantic validation to
  the server to avoid drifting from the single job-spec pipeline (`jobspec.Validate`).

## Verification

- Full web suite: 290 tests passing across 59 files.
- Production build green (`tsc -b && vite build`).
- Both re-run independently by the conductor on the final stable tree rather than trusted from the
  implementer's claim.
- Code review: no findings (CLEAN), with the two load-bearing tests mutation-checked as non-vacuous.

## Batch Context

This was the 4th and final iteration of the batch. The batch shipped worker-detail admin mutations
(iteration 1), the job detail page (iteration 2), job cancel / force-cancel (iteration 3), and this
New Job form (iteration 4). The jobs UI is now substantially operational: list -> detail -> cancel ->
create.
