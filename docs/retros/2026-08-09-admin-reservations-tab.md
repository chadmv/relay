---
date: 2026-08-09
topic: admin-reservations-tab
branch: claude/pr-merging-session-0674dd
range: f350a78..HEAD
---

# Session Retro: 2026-08-09 - admin-reservations-tab

**TL;DR:** Iteration 5 and the last of the 5-item unattended `/autopilot` batch. The admin console got
its third tab: list, create and delete worker reservations, with `DELETE /v1/reservations/{id}` being
the first sibling endpoint that actually exists, so this is also the console's first real destructive
control. Frontend-only, zero Go changes; web suite 617 -> 710 tests; review returned **0 high** /
2 medium / 7 low. Third instance of an established pattern, so the spec and plan were mostly pointers
into `web/src/admin/enrollments/`, and the residual risk concentrated in the two files with no
precedent (`WorkerPicker.tsx`, the delete-dialog copy branch in `ReservationsTab.tsx`). What and why
are recorded thoroughly in the closed item's Resolution
(`docs/backlog/closed/feature-2026-08-08-admin-reservations-tab.md`); this retro records the five
things worth carrying forward.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-09-admin-reservations-tab.md`, **plan**
  `docs/superpowers/plans/2026-08-09-admin-reservations-tab.md` (10 sequential tasks, one engineer).
- `web/src/admin/reservations/` - `api.ts`, `useReservations.ts`, `useReservationActions.ts`,
  `useWorkerOptions.ts`, `ReservationsTab.tsx`, `ReservationsTable.tsx`,
  `CreateReservationForm.tsx`, `WorkerPicker.tsx`, `reservationStatus.ts`. A structural mirror of the
  enrollments module plus the two genuinely new surfaces.
- `web/src/workers/api.ts` - one optional `limit = 50` parameter on `listWorkers`, default preserved,
  the shipped `limit=50` test left untouched as the regression guard.
- `web/src/lib/time.ts` - `formatDateTime`, built from `Date` getters rather than `toLocaleString` so
  its output is one fixed shape in every runner locale.
- One `ADMIN_TABS` entry, plus the two shipped shell test files that entry forces.

## Key Decisions

Nearly all inherited from the two sibling tabs. The ones actually made here:

- **A worker picker over one page of 200, with the ceiling stated in the UI.** Creating a reservation
  needs worker UUIDs, which appear nowhere else in the SPA's text, so a free-text field would be
  unverifiable. The `total > loaded` note is what keeps a truncated list from reading as complete.
- **Client validation stricter than the server** (name required, at least one worker, `ends_at` after
  `starts_at`), because the handler validates almost nothing and would happily persist dead rows.
- **`selector` and `user_id` omitted from the create form.** Both are inert; an input with no effect
  is worse than a missing one.
- **No secrecy machinery.** No `TokenRevealDialog`, no `gcTime: 0`, no `secretLeaks` import - stated
  as a scope guard precisely because the immediately preceding slice needed all of it.

## Problems Encountered

1. **The domain discovery mattered more than the UI.** A reservation is a **pure exclusion** from the
   dispatch pool: `ListActiveReservations`' `worker_ids` become a `reservedIDs` set and those workers
   are skipped for *every* task. `user_id`, `project` and `selector` are stored and never consulted.
   The hi-fi's "reserve for X" framing therefore implies an affinity the scheduler does not implement,
   and repeating it would have led admins to make reservations that do the opposite of what they
   intend. What made this stick was making "the copy must be honest" **a task with assertions rather
   than a note**: an absence sweep for `/reserved for/`, `/dedicated/`, `/priority/`, `/exclusive/`,
   `/assigned to/` with paired positive controls. That is what caught the M2 finding below - a note in
   a spec has no failure mode, and an assertion does.
2. **The plan's own reference `deriveStatus` had a logic bug, and the plan's own test contradicted it.**
   It compared each bound to `now` and never to each other, so a window whose `ends_at` precedes its
   `starts_at` but is still in the future read `SCHEDULED` - while the plan's own inverted-window test
   asserted `ENDED`. The shipped module compares the bounds to each other first (and treats a NaN
   `ends_at` as `ENDED` rather than falling through to `ACTIVE`). This is the **third** time in this
   batch that plan-supplied code or tests were wrong, and it is already a promoted lesson, so record
   it as confirmation rather than discovery: a plan is written before the code exists, and its code
   blocks are drafts on exactly the same footing as its test bodies.
3. **A state-divergence bug only adversarial review found, with two opposite outcomes from one state
   (M1).** `WorkerPicker` emitted its selection by projecting through the loaded page
   (`workers.filter(w => next.has(w.id))`). `staleTime: 30_000` only delays a refetch; it does not
   disable `refetchOnWindowFocus`. So if a window-focus refetch dropped a revoked worker while the
   panel sat open, pressing Reserve **submitted the vanished id** (there is no FK on `worker_ids`, so
   it persisted into a reservation that reserves a worker that no longer exists) - unless the admin
   had toggled any other checkbox first, in which case the projection **silently dropped it** instead.
   One state, two contradictory failures, neither visible on the happy path. Fixed by mutating `value`
   itself for the one id the call is about, and surfacing any stale ids in a warn box with a `remove`
   control so the admin decides rather than the code guessing.
4. **The absence sweep was non-vacuous but representationally blind (L4).** Every negative matcher had
   a paired positive control on the same instrument, which is the habit working - but it matched
   `container.textContent`, and `textContent` excludes attribute values entirely. A regression adding
   `aria-label="Reserved for ada@studio.dev"`, or an affinity claim in a `title` or `placeholder`,
   would have passed the whole sweep. Switched to `innerHTML`, and a second sweep added for the
   confirm dialog, which the first never covered. Direct hit on the already-promoted lesson that a
   control must exercise the representation the real failure would take - here the gap was not the
   control but the **reach of the probe**.
5. **M2: the delete dialog asserted a dispatch effect that does not exist for most rows.** The body
   was unconditional - "returns its N worker(s) to the general dispatch pool" - which is false for a
   `SCHEDULED` or `ENDED` reservation whose workers were never withheld, and reads absurdly as
   "returns its 0 worker(s)" for a CLI-created `worker_ids: []` row. The tab already computes
   `deriveStatus` for the `STATUS` column, so the dialog now reuses it and says deletion changes
   nothing about dispatch when that is the truth. Exactly the overstatement class Problem #1's
   assertions exist to catch, found in the one piece of copy the sweep did not reach.
6. **Good practice worth naming: the reviewer re-ran the time-sensitive tests under two non-UTC
   timezones** to confirm `formatDateTime` and `deriveStatus` were not passing only because CI runs at
   UTC+0. The spec had prescribed a TZ-independent-by-construction test shape; the reviewer verified
   the construction actually delivered it instead of trusting the comment saying so.
7. **Process note.** Phase 4 again substituted a direct `relay-code-reviewer` dispatch for the
   documented `relay-verify` workflow, which needs an opt-in an unattended batch cannot give. **Fifth
   consecutive iteration** - see the batch retro; at five-for-five the playbook documents something the
   pipeline does not do.

## Findings Triage

- **0 high.** Third iteration of the batch with none, and the pattern holds that findings track novelty
  rather than diff size: both mediums landed in the two files without precedent.
- **2 medium, both fixed with tests** (Problems #3 and #5).
- **7 low**, triaged and either fixed or accepted as minor. Problem #4 is among the fixed set.

## Known Limitations

- **No owner column.** `user_id` is a bare UUID with no join to `users`, it grants nothing, and the
  one endpoint that could resolve it 500s for a well-formed but nonexistent id. Rendering 36 opaque
  characters would be real but useless. A `user_email` enricher was proposed, not filed, folded into
  `feature-2026-06-26-web-enabler-backend-endpoints`; the same item would supply worker names for the
  truncated-UUID chips.
- **`CREATED` renders the UTC date while `STARTS` / `ENDS` render local wall-clock.** `created_at`
  is sliced to its ISO date for consistency with the Users and Enrollments tables, while the window
  columns are local so they read back what the admin typed into the `datetime-local` inputs. Two
  timezones in one row is a real inconsistency, accepted to avoid making this tab the odd one out of
  three.
- **The backend validation gap is filed, not fixed:**
  `bug-2026-08-09-create-reservation-500-on-client-error` - `POST /v1/reservations` funnels every
  `CreateReservation` error to a 500, so a well-formed but nonexistent `user_id` is a 500 rather than
  a 400, and there is no worker-existence check and no inverted-window rejection. The tab sidesteps it
  by never sending `user_id` and by validating client-side, which makes the client validation
  load-bearing rather than decorative.
- **Effect is delayed to the next dispatch tick.** No reservation write emits a NOTIFY or calls
  `Trigger()`, so create and delete land within roughly one 30s poll and never preempt a running task.
  Stated in the footnote and the confirm dialog rather than hidden.
- **The picker's 200-row ceiling** is a single request at the server's `maxLimit` with no cursor. It
  fails visibly, but a fleet above 200 workers needs a paginated or server-filtered picker in its own
  item.
- **Status derives from the browser clock**, so a badly skewed client mislabels a row. The server
  exposes no status to prefer.
- **`ConfirmDialog` still has no focus trap.** `idea-2026-07-01-confirmdialog-focus-trap-hardening`
  passed its stated "schedule before a third consumer" threshold earlier in this batch.

## Improvement Goals

Carried forward from the four earlier iterations of this batch, briefly:

- **Treat the plan as an untrusted source of test design** - **honored, and widened.** No count of
  broken plan tests this time, but the plan's *reference implementation* was wrong (Problem #2). The
  goal should be read as "untrusted source of plan-supplied code", not only tests.
- **Pair every absence assertion with a positive control, in the representation the real failure would
  take** - **honored on the control, missed on the reach** (Problem #4). Every negative had a paired
  positive on the same instrument; the instrument itself could not see attributes.
- **Verify a backlog item's technical claims against the code during spec** - **honored, fifth time,
  and second time against an item the TPM wrote itself.** The item asked only whether the `selector`
  footnote was true; the bigger truth is that `user_id` and `project` are equally inert. It was also
  silent on the only hard design problem (how an admin obtains worker UUIDs) and on the near-total
  absence of server-side validation.
- **Independently re-verify the tree and re-run the green gate** - **honored.** Suite and production
  build re-run by the conductor on the settled tree.
- **Test invalidation with a real active observer** - **honored**, both mutations, with the reason
  written into the test.
- **An overlay owns its own error surface** - **honored.** Create errors render in the create panel,
  delete errors in the shared `actionError` box above the table.
- **Coverage shape: name the test for every rejection** - **honored.** List error with Retry, create
  500, delete 404 that still refetches.
- **Rewriting a shared test file is coverage-losing** - **honored.** `time.test.ts`,
  `workers/api.test.ts`, `AdminTabs.test.tsx` and `AdminPage.test.tsx` were all appended to.
- **Confirm which design-fidelity layer is authoritative** - **honored**, and it is what surfaced
  Problem #1: the hi-fi was read closely enough to find that its framing is wrong about the system.
- **Teardown ends the generation first / a per-event guarantee is not a bound / diagnose a red gate /
  a concurrency test must fail fast / a wrong contract in docs is a defect / bound error logging on a
  hot path / calling the clear function is not evidence** - **n/a.** No async lifecycle, no recovery
  loop, no gate went red, no Go, no client-facing contract, no secret.
- **Give the playbook an explicit unattended Phase 4 path** - **not honored, fifth time.**

New from this iteration:

- **Make an honesty requirement a test, not a note.** When the risk is that the UI claims something
  the system does not do, the mitigation has to be an assertion with a paired positive control, not a
  sentence in a spec. **Candidate for durable memory**, and it is the general form of the
  omit-unbacked-not-fake rule: an absence sweep over the copy is the only version of that rule with a
  failure mode.
- **A probe's reach is part of its non-vacuity.** A `textContent` sweep with perfect positive controls
  is still blind to every attribute. **Not a new note** - it belongs as a sentence in the already
  promoted absence-needs-a-control-in-the-real-representation memory, beside the React-controlled-input
  instance from the previous iteration.
- **Derived UI state must be derived from the same value the copy depends on.** M2 and M1 are the same
  mistake in two places: a projection (through the loaded page, through an unconditional sentence)
  that is right for the common case and silently wrong for the rest. When a component already computes
  the authoritative value, every dependent surface should read it rather than re-assume it.

## Files Most Touched

- `web/src/admin/reservations/ReservationsTab.tsx` - the composition point and the home of the
  status-aware delete-dialog body (Problem #5) plus the honest footnote. Its comments carry the
  reasoning for the branch so it is not simplified back into one unconditional sentence.
- `web/src/admin/reservations/WorkerPicker.tsx` - the one genuinely new input, where M1 lived. Now
  mutates `value` directly and surfaces stale selections rather than silently keeping or dropping
  them; the comment names the previous projection so it is not reintroduced.
- `web/src/admin/reservations/ReservationsTab.test.tsx` - the honesty sweeps (both of them), the
  three no-dispatch-effect dialog cases with their ACTIVE positive control, the pager, the 60s tick,
  and the per-row delete-identity test.
- `web/src/admin/reservations/reservationStatus.ts` + `reservationStatus.test.ts` - the transcription
  of `ListActiveReservations`, the bound-versus-bound check from Problem #2, the NaN degradation, and
  the matrix test that checks ACTIVE-vs-not against an independently written predicate.
- `web/src/admin/reservations/api.ts` + `api.test.ts` - the nullability contract that is the easiest
  thing here to get wrong: `selector` is present-and-`null`, while `project` / `starts_at` / `ends_at`
  keys are **absent**.
- `web/src/admin/reservations/{useReservations,useReservationActions,useWorkerOptions}.ts` - the query
  tier. No polling anywhere, bare-prefix invalidation, no `gcTime` override.
- `web/src/admin/reservations/{ReservationsTable,CreateReservationForm}.tsx` - the cloned tier plus
  the client validation the missing server validation makes load-bearing.
- `web/src/lib/time.ts` + `web/src/workers/api.ts` - the two additions outside `web/src/admin/`:
  `formatDateTime`, and one optional parameter with its default preserved.
- `web/src/admin/tabs.ts` + `AdminTabs.test.tsx` + `AdminPage.test.tsx` - the one-line registry entry
  and the two shipped test files it forces, edited additively.

## Verification

- Full web suite green: **710 tests, up from 617**. Production build green (`tsc -b && vite build`),
  with `git checkout -- web/dist/` before the change set was assembled.
- Both re-run by the conductor on the settled tree rather than trusted from the implementer's report.
- The time-sensitive units re-run under two non-UTC timezones by the reviewer (Problem #6).
- Code review: 0 high, 2 medium (both fixed), 7 low - by a direct `relay-code-reviewer` dispatch
  rather than the documented `relay-verify` fan-out (Problem #7).
- No Go files changed, so no `make test` / `make test-integration` run was required and none of the six
  Invariants was in play. The frontend analogues respected: every request goes through `apiFetch`, no
  component calls `fetch` directly, and no shared primitive was modified (a chip becomes a link by
  being wrapped in one).
