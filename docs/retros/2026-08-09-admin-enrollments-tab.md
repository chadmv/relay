---
date: 2026-08-09
topic: admin-enrollments-tab
branch: claude/pr-merging-session-0674dd
range: de3050e..HEAD
---

# Session Retro: 2026-08-09 - admin-enrollments-tab

**TL;DR:** Iteration 4 of the same 5-item unattended `/autopilot` batch, and the batch's smoothest.
The admin console got its second tab: create an agent-enrollment token and list the unconsumed,
unexpired ones, with the raw token revealed clear-text exactly once in a shared
`TokenRevealDialog`. Frontend-only, zero Go changes; web suite 530 -> 617 tests; review returned
**0 high** / 2 medium / 5 low. Most of this slice was a structural clone of the Users tab shipped
earlier the same day, and it shows: the spec and plan were both deliberately shorter than iteration
1's because most decisions were inherited rather than re-derived. Three genuinely new things: the
shared reveal dialog (the first surface in the SPA that displays a credential it can never retrieve
again), a shared `web/src/test/secretLeaks.ts`, and `useNow` + `formatTimeUntil`. Ships with no
revoke control, because the endpoint does not exist. The what and why are recorded thoroughly in the
closed item's Resolution
(`docs/backlog/closed/feature-2026-08-08-admin-enrollments-tab.md`); this retro records the four
things worth carrying forward.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-09-admin-enrollments-tab.md`, **plan**
  `docs/superpowers/plans/2026-08-09-admin-enrollments-tab.md` (10 sequential tasks, one engineer).
- `web/src/admin/enrollments/` - `api.ts`, `useAgentEnrollments.ts`, `useAgentEnrollmentActions.ts`,
  `EnrollmentsTab.tsx`, `EnrollmentsTable.tsx`, `CreateEnrollmentForm.tsx`, `enrollmentStatus.ts`,
  a structural mirror of `web/src/admin/users/`.
- `web/src/admin/TokenRevealDialog.tsx` - **shared**, at the admin module root. The only component in
  the app that renders a raw credential. Backdrop click deliberately does not dismiss (no `onClick`
  on the overlay, so there is no handler to reintroduce); Escape and `Done` do; clipboard is
  feature-detected because `navigator.clipboard` is `undefined` on plain-HTTP `:8080`.
- `web/src/test/secretLeaks.ts` - reusable leak matchers (console walker, DOM-including-input-values,
  storage), extracted so the invites tab does not rewrite them.
- `web/src/lib/useNow.ts` (60s local clock tick, no request) and `formatTimeUntil` in
  `web/src/lib/time.ts`; one new `ADMIN_TABS` entry.

## Key Decisions

Nearly all inherited. The spec has an explicit "what is inherited, not re-derived" table, and the
three decisions actually made here were:

- **Split the hi-fi's single `AdminTokenModal` in two.** The reveal half is shared because invites
  need the byte-identical surface and it carries the security invariants, which should have exactly
  one audited copy. The create half stays tab-local, because the hi-fi models the divergence with an
  `isInvite` boolean, which is the flag-driven component that rots.
- **`gcTime: 0` on the create mutation.** See Problems #2 - `reset()` is not enough.
- **No revoke control**, following the Users tab's no-role-change precedent one slice earlier:
  `DELETE /v1/agent-enrollments/{id}` does not exist and a guaranteed-405 button is a dead control.
  Blast radius stays bounded by single-use consumption, the TTL, and `DELETE /v1/workers/{id}/token`,
  and the footnote says so, so the absence is explained rather than merely missing.

## Problems Encountered

1. **The TPM corrected three errors in an item it had authored itself hours earlier.** Most
   consequentially, the item named "the shared `AdminTokenModal` (line 2340)" as the token-reveal
   surface. That component is the **create form** - a hint input, four TTL presets, Cancel/Enroll -
   and it never renders a token; its own copy defers to a "success toast" that exists nowhere in the
   hi-fi and nowhere in `web/src` (grep: zero `toast` matches). **The reveal surface, the one
   genuinely new and highest-consequence part of the slice, was undesigned.** The other two: the item
   omitted that `readJSON` is called unconditionally so a POST with no body 400s (a client must send
   at least `{}`), and it claimed two derivable states where three are observable. All three were
   caught at spec time by reading `internal/api/agent_enrollments.go`. **Lesson: "verify the proposal
   against the code" applies to your own proposals. Authorship is not evidence** - an item written
   from a design handoff encodes the handoff's assumptions, and writing it does not check them.
2. **A library API did not do what its name implies, and only a test caught it.**
   `mutation.reset()` sounds like it clears the mutation. It only detaches the observer: the
   underlying `Mutation`, with the raw token in its `state.data`, stays in
   `queryClient.getMutationCache()` for the default 5-minute `gcTime`. Found by the plan-required
   assertion that no mutation-cache entry contains the token after dismissal, then verified against
   library source during review (`reset()` -> `removeObserver` -> `scheduleGc()`, and
   `optionalRemove` removes only when `observers.length === 0` and the status is settled, so
   `gcTime: 0` is both necessary and sufficient and cannot evict while pending). **Lesson: on a
   secret-handling path, "I called the clear function" is not evidence. Assert the secret's absence
   from the actual store**, and when the answer turns on a library's retention semantics, read the
   library rather than its method name.
3. **Two medium findings that only adversarial reading catches.** Both are worth recording
   concretely because neither is visible in the happy path:
   - **The reveal dialog stole focus back every 60 seconds.** Its focus-and-select effect depended on
     `onDone`, which `EnrollmentsTab` passes as an inline `() => create.reset()` whose identity
     changes on every parent re-render - and the parent re-renders on the `useNow(60_000)` tick. So a
     keyboard admin who tabbed to `Done` and paused to read the warning was silently yanked back to
     the token input, got nothing on Enter, and the plausible next keystroke is **Escape, which
     permanently destroys the credential**. Fixed by splitting into a mount-only focus effect and a
     separate `onDone`-dependent keydown effect; re-attaching a listener has no visible side effect,
     unlike re-focusing.
   - **The shared leak checker had precisely the blind spot it was written to close.** It handled
     `Error` directly but fell through to `JSON.stringify` for plain objects, so
     `console.error({ err: new Error(token) })` - the shape both React and TanStack log internally -
     rendered as `{"err":{}}` and reported clean. Fixed with an object/array branch that recurses
     through `stringifyArg` itself, with a depth cap and a seen-set.
4. **A positive control that proved nothing.** The DOM secrecy control asserted
   `domContainsSecret` sees a value living only in an input property - but it built the case with a
   React-controlled `<input value={TOKEN}>`, and **React mirrors a controlled input's value into the
   DOM value attribute on mount**. So `document.body.innerHTML` already contained the token and the
   control passed with the input/textarea value loop deleted, proving nothing about the mechanism it
   existed to protect. Fixed by setting `.value` imperatively on a plain DOM node, which leaves the
   attribute unset, and by asserting `document.body.innerHTML` does **not** contain the token first.
   This is a direct hit on the standing lesson that a control must use the representation the real
   failure would take - and a reminder that the framework can quietly supply the representation you
   were trying to exclude.
5. **Process notes.** Phase 4 again substituted a direct `relay-code-reviewer` dispatch for the
   documented `relay-verify` workflow, which needs an opt-in an unattended batch cannot give. **This
   is the fourth consecutive iteration**, which makes the substitution the de facto pipeline; the
   playbook should either document it as the unattended path or make the workflow runnable
   unattended, because "documented pipeline, four-for-four deviation" is a docs defect at this point.
   On the positive side, the engineer **correctly did not commit** this time, after the previous
   iteration's engineer committed 15 times against a dispatch reserving git to the conductor.

## Findings Triage

- **0 high.** The only iteration in the batch with none other than iterations 1 and 2, and the
  cheapest to review.
- **2 medium, both fixed with tests** (Problems #3).
- **5 low**, triaged and either fixed or accepted as minor. Problem #4 is among the fixed set.

## Known Limitations

- **No revoke control.** `DELETE /v1/agent-enrollments/{id}` does not exist. A leaked unconsumed
  token cannot be killed from the UI; expiry or consumption are the only terminal states. Owned by
  `feature-2026-06-26-agent-enrollment-revocation`, which additionally carries an integration
  requirement this frontend-only slice could not satisfy (revoking must not disturb an
  already-enrolled worker).
- **No `TOKEN PREFIX`, no `CREATED BY`, and no `CONSUMED` status.** None is supportable by the list
  response: only `tokenhash.Hash(rawHex)` is stored so no prefix exists, `created_by` is a bare user
  UUID with no join to `users`, and every list and count query filters
  `consumed_at IS NULL AND expires_at > NOW()` so consumed and expired rows simply vanish rather than
  changing state. A `token_prefix` column (folded into the revocation item) and a `created_by_email`
  enricher were proposed, not filed.
- **`TokenRevealDialog` is the third consumer of the un-focus-trapped dialog primitive**, and the
  worst one: a focus escape from a dialog whose sole content is a credential means the credential can
  be tabbed past. `idea-2026-07-01-confirmdialog-focus-trap-hardening` was already past its stated
  "schedule before a third consumer" threshold at iteration 1; the Invites tab makes it four.
- **Escape destroys the credential with no confirmation.** Accepted deliberately, because trapping
  Escape would break the a11y baseline of every dialog in the app, but it is a real footgun: the
  dialog holds the only copy of an unrecoverable secret and one ordinary dismissal keystroke ends it.
- **Status derives from the browser clock**, so a badly skewed client mislabels a row. The server
  exposes no status to prefer instead.

## Improvement Goals

Carried forward from the three earlier iterations of this batch. Most were honored without incident,
so this is deliberately brief:

- **Treat the plan as an untrusted source of test design** - **honored, and notably quieter.** No
  count of broken plan tests to report this time, after 1 / 5 / 7 in the previous three. The plans
  are getting better at the things the previous retros named (the no-poll test ships with its
  positive control written into the plan; the invalidation test's active observer is explained
  inline). The residue moved: the plan-supplied *control* in Problem #4 was the weak artifact, not a
  plan-supplied assertion.
- **Pair every absence assertion with a positive control, in the representation the real failure
  would take** - **honored as a habit and, for once, caught in the analysis too.** Every absence
  assertion in the secrecy suite has a paired control, and five of them are now **permanent tests**
  of the matchers themselves rather than one-off mutations. Problem #4 is the one that slipped, and it
  slipped on exactly the axis the goal names.
- **Verify a backlog item's technical claims against the code during spec** - **honored, and
  sharpened.** Fourth iteration in a row where it paid; see Problem #1 for the new wrinkle.
- **Independently re-verify the tree and re-run the green gate** - **honored.** Suite and production
  build re-run by the conductor on the settled tree, and the flow verified live against a real
  backend.
- **Test invalidation with a real active observer** - **honored**, with the reason written into the
  test so it cannot be reverted to a `fetchQuery` seed, plus a mandatory plan step proving the
  mounted-list test goes RED when the key is broken.
- **An overlay owns its own error surface** - **honored.** Create errors render inside
  `CreateEnrollmentForm`, and a rejected clipboard write falls back to a hint inside the reveal
  dialog rather than logging or leaving a dead button.
- **Coverage shape: name the test for every rejection** - **honored.** The create 500 path, the
  clipboard rejection path, and the list error path all have tests.
- **Rewriting a shared test file is coverage-losing** - **honored.** `time.test.ts`,
  `AdminTabs.test.tsx`, and `AdminPage.test.tsx` were appended to and edited surgically, not
  replaced.
- **Confirm which design-fidelity layer is authoritative** - **honored**, and it is what exposed
  Problem #1: the hi-fi was read closely enough to discover the named component does not do what the
  item said.
- **Teardown ends the generation first / a per-event guarantee is not a bound / diagnose a red gate /
  a concurrency test must fail fast / a wrong contract in docs is a defect / bound error logging on a
  hot path** - **n/a.** No async lifecycle, no recovery loop, no gate went red, no Go, no
  client-facing contract edited.
- **Give the playbook an explicit unattended Phase 4 path** - **not honored, fourth time.** See
  Problem #5.

New goals from this iteration:

- **Verify your own proposals against the code; authorship is not evidence.** The existing durable
  note treats a backlog item as a hypothesis about the code. This iteration adds that the rule does
  not weaken when you wrote the item: an item authored from a design handoff carries the handoff's
  assumptions, and writing it is not checking it. **Candidate for a one-line amendment to
  [[feedback_backlog_proposal_not_contract]]**, not a new note.
- **On a secret-handling path, calling the clear function is not evidence - assert the secret's
  absence from the actual store.** And when the answer depends on a library's retention semantics,
  read the library rather than trusting its method name. **Candidate for durable memory**, framed
  narrowly around secrets, because that is where the cost of being wrong is asymmetric.
- **A framework can supply the representation your control was trying to exclude.** React mirroring a
  controlled input's `value` into the attribute made a value-property control pass with the
  value-property mechanism deleted. The habit: build a control with the *lowest-level* construction
  of the failure case available, not the ergonomic one. **Not a new memory candidate** - it belongs as
  a sentence in the already-promoted absence-needs-a-control-in-the-real-representation note.

## The Second-Instance Effect

Worth stating on its own, because it is the main positive result of the iteration. This slice was
materially cheaper and cleaner than the Users tab that established the pattern: a shorter spec (most
of it an explicit inherited-decisions table rather than argument), a shorter plan whose tasks say
"mirror `web/src/admin/users/X` at `file:line`, change the nouns", 0 high findings, and the whole new
risk concentrated in exactly one component that was not a clone. That is the shape the Holo relayout
program bet on - extract the primitive, then instances get cheap - and this is evidence it transfers
to a second domain, from presentational primitives to a whole feature-module pattern. The corollary
is that review attention should follow the novelty, not the diff size: 0 of the 7 findings landed in
the cloned files, and both mediums landed in `TokenRevealDialog.tsx` and `secretLeaks.ts`, the two
files with no precedent.

## Files Most Touched

- `web/src/admin/TokenRevealDialog.tsx` - the only genuinely new component, the only place a raw
  credential is rendered, and where both medium findings landed. Its comments carry the reasoning for
  each guard (why there is no backdrop `onClick`, why the focus effect must not depend on `onDone`,
  why the clipboard catch logs nothing) because every future edit here can lose an admin's only copy
  of a secret.
- `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx` - the DOM / cache / storage / URL /
  console absence assertions, each with a paired positive control, and the five permanent
  matcher-control tests. Problem #4 lived here.
- `web/src/test/secretLeaks.ts` - the extracted matchers, and the file with the blind spot it existed
  to close. Now recursing through `stringifyArg` for objects and arrays with a depth cap and a
  seen-set.
- `web/src/admin/enrollments/useAgentEnrollmentActions.ts` - one mutation, bare-prefix invalidation,
  and the `gcTime: 0` from Problem #2 with the library-source reasoning in a comment so it is not
  removed as redundant.
- `web/src/admin/enrollments/EnrollmentsTab.tsx` - the composition point: control row, create panel,
  table, footer, footnote, reveal dialog, and the three `create.reset()` call sites.
- `web/src/admin/enrollments/{api,useAgentEnrollments,EnrollmentsTable,CreateEnrollmentForm,enrollmentStatus}.ts(x)`
  - the cloned tier. Reviewed clean.
- `web/src/lib/useNow.ts` + `web/src/lib/time.ts` - the 60s local tick and `formatTimeUntil`, the two
  additions outside `web/src/admin/`. `formatTimeUntil` exists because `formatRelativeTime` clamps
  the future to zero and would render every expiry as `0s ago`, which a permanent positive control
  asserts.
- `web/src/admin/tabs.ts` + `AdminTabs.test.tsx` + `AdminPage.test.tsx` - the one-line registry entry
  and the two shipped test files it forced, edited additively.

## Verification

- Full web suite green: **617 tests, up from 530**. Production build green (`tsc -b && vite build`),
  with `git checkout -- web/dist/` before the change set was assembled.
- Both re-run by the conductor on the settled tree rather than trusted from the implementer's report.
- **Verified live against a real backend:** the token is absent from the DOM, both web storages, every
  request URL, and the console after the dialog closes.
- The plan's mandatory non-vacuity steps were performed, including breaking the invalidation key and
  confirming the mounted-list test goes RED.
- Code review: 0 high, 2 medium (both fixed), 5 low - by a direct `relay-code-reviewer` dispatch
  rather than the documented `relay-verify` fan-out (Problem #5).
- No Go files changed, so no `make test` / `make test-integration` run was required and none of the
  six Invariants was in play. The frontend analogues that were respected: every request goes through
  `apiFetch`, no component calls `fetch` directly, and the credential has exactly one render site and
  exactly one destruction point.
