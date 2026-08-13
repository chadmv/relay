---
date: 2026-08-13
topic: admin-invites-tab
branch: claude/pr-merge-session-f5796e
range: origin/main..HEAD (green, not yet merged)
---

# Session Retro: 2026-08-13 - The Invites tab, and a shipped defect found by its own copy

**TL;DR:** Shipped the admin console's Invites tab: a create form with hour-denominated TTL presets,
a token-revealed-once dialog, and a paginated sortable list with a four-state pill. 12 tasks, one
frontend engineer, sequential. Web suite 973 -> 1049. `Chip` gained an `err` tone. **This is the
fifth and final admin tab to be built; it sits second in the tab bar**, between Users and Agent
enrolls (`web/src/admin/tabs.ts:21-25`) - a distinction the conductor repeatedly collapsed into
"last tab", which the spec's and plan's opening sentences inherited. Review found one medium and it
is the sharpest finding of the five-item batch: cancelling an in-flight create destroyed the only
plaintext copy of an unrecoverable credential, **inherited verbatim from the shipped Enrollments
tab**, so the fix landed in both. A second finding was about the evidence rather than the code: a
security-relevant assertion was believed proven RED by a path that never reaches it. **For the first
time in several iterations, no false comment shipped.**

## The medium, and why it is the best finding of the batch

Only the submit button was disabled while the create mutation was pending. **Cancel** and the
**`+ Create invite`** toggle both remained live, and both call `create.reset()`.

`MutationObserver.reset()` detaches the observer. It does not cancel `Mutation.execute`. So a click
on Cancel one tick after submit produced this sequence, with nothing in it that errors:

1. The POST is already in flight and is not cancelled.
2. The observer detaches, so the mutation now has zero observers.
3. The server mints and persists the invite and returns 201 with the raw token.
4. Success dispatches to zero observers, so `create.data` never lands anywhere.
5. `gcTime: 0` - the very control that exists to destroy the credential promptly - evicts the
   mutation on the next tick.
6. The reveal dialog never opens. The token is destroyed before it is ever rendered.

The end state is a **permanently unusable ACTIVE invite**: it exists in the database, it counts
against the list, it will sit there until its TTL runs out, and nobody can ever redeem it because
the only plaintext copy was garbage-collected. There is no revoke endpoint to clean it up
(`internal/api/server.go:142-143` is the entire route surface for invites), so the row is
unrecoverable in both directions.

**Two lanes proved it independently with probes rather than by reasoning.** One recorded
`posts === 1`, no dialog ever appearing, the token never present in the DOM, and the mutation cache
empty. That is the practice this batch converged on, and it is why the finding is a medium with
evidence rather than a "looks racy" note.

**It was inherited verbatim.** `EnrollmentsTab` had the identical shape, and the invites tab was a
deliberate structural clone of it, so the defect arrived by construction. The fix is now in both
files with the reasoning at each site
(`web/src/admin/invites/CreateInviteForm.tsx:95-104`,
`web/src/admin/enrollments/CreateEnrollmentForm.tsx:92-101`, and the toggle at
`InvitesTab.tsx:170-177` / `EnrollmentsTab.tsx:172-179`). The toggle comment also records a second
accident the same fix closes: without it, a mid-request click both reset the live mutation and
reopened a fresh form, so two clicks fired a duplicate create.

**Record the shape.** Cloning a shipped surface imports its defects at full strength, and the clone
gets more review attention than the original ever did, which is exactly when the original's defects
become findable. "This is a structural clone, re-review the source" is worth adding to the brief on
any slice that copies a shipped module.

## The second finding was about the evidence, not the code

The spec (`2026-08-13-admin-invites-tab.md:573-576`) claimed the mutation-cache-empty assertion was
proven RED by removing `create.reset()`. **It is not.** With `onDone={() => {}}` the run fails
earlier, at `expect(screen.queryByRole('dialog')).not.toBeInTheDocument()`, because the dialog is
driven by `create.data` and nothing ever clears it. The cache-empty line is never reached.

Nothing about the code was wrong. What was wrong was the belief that a security-relevant property
had been exercised. The fix is a genuinely independent control at
`useInviteActions.test.tsx:139-149`: it never calls `reset()` at all, reaches the cache assertion
directly, and asserts the settled mutation is **still** in the cache - which is precisely the
premise the emptiness claim depends on. Its comment records the false claim and why it was false, so
the next reader does not re-derive it.

This is the previous iteration's "a mutation that reddens is not a mutation that discriminates the
claim" lesson, arriving one layer up: the same `require`-dies-upstream failure mode, but in a spec's
prose rather than in a Go test, and about an assertion that guards a credential.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-13-admin-invites-tab.md`, **plan**
  `docs/superpowers/plans/2026-08-13-admin-invites-tab.md` (12 tasks, one `relay-frontend-engineer`,
  strictly sequential - the dependency chain is linear and Tasks 1 and 10 both write into shipped
  shared modules).
- **Fifteen new files** under `web/src/admin/invites/`: `api.ts`, `inviteStatus.ts`, `useInvites.ts`,
  `useInviteActions.ts`, `CreateInviteForm.tsx`, `InvitesTable.tsx`, `InvitesTab.tsx`, and a test
  file for each plus `inviteTokenSecrecy.test.tsx`.
- **One additive `Chip` tone.** `err: 'border border-err/40 bg-err/10 text-err'`
  (`web/src/components/holo/Chip.tsx:12-20`), with the reasoning at the key. Four derivable states
  need four tones; no existing consumer changed.
- **One `ADMIN_TABS` entry**, `{slug: 'invites', label: 'Invites', Panel: InvitesTab}` at
  `web/src/admin/tabs.ts:22`, **second in the array**. The stale comment claiming the tab "stays
  blocked on a GET /v1/invites that does not exist" was corrected in the same edit (`:14-19`); a
  wrong contract in a comment is a defect.
- **The four-state derivation** (`inviteStatus.ts`), order REDEEMED, EXPIRED, EXPIRING, ACTIVE, with
  the precedence pinned by a dedicated test: a redeemed invite that is also past its expiry reads
  REDEEMED.
- **The fix to the Enrollments tab**, shipped alongside its copy.
- **Zero Go files.** The whole slice is `web/`.

## The spec found two facts nobody had recorded

Both were verified independently by the conductor before they reached a task brief.

1. **`time.ParseDuration` has no day unit.** The backlog item proposed presets labelled
   `24h / 72h / 7d / 30d` (`feature-2026-08-08-admin-invites-tab.md:44-45`). Sending the literal
   `"7d"` 400s every time with `unknown unit "d"`. The labels stay human and the **wire values** are
   `24h` / `72h` / `168h` / `720h` (`web/src/admin/invites/api.ts`, `TTL_PRESETS`). This is the
   failure that passes any naive "there are four presets" test and only fails in production, so
   `api.test.ts` asserts `/^\d+h$/` on every value plus an explicit `not.toMatch(/[dwy]$/)`.
2. **`readJSON` runs unconditionally on `POST /v1/invites`** (`internal/api/invites.go:27`), so a
   body is mandatory even though every field in it is optional. The minimum legal body is
   `{expires_in: "72h"}`. A client that "helpfully" omits the body when there is nothing to send
   gets a 400.

Neither is stated in the item, the README, or the preceding slice's spec. Both are exactly the class
of fact that a spec phase exists to find: cheap to discover by reading, expensive to discover in
production, invisible to a reviewer looking at correct-looking TypeScript.

## The plan refuted the spec, and was right

The spec said the two shipped admin test files needed **additive edits only**. The plan checked and
found that **five shipped assertions actively encode this tab's absence** - a tab-count, a label
list, a redirect case, and so on. They cannot be added around; they have to move.

The plan enumerated them individually, and exactly those moved. None was weakened. That is the
correct handling of the standing rule "never edit a shipped test's assertions to make new code pass":
the rule has an exception for assertions that encode the absence of the thing being added, and the
exception is only safe when the exceptions are listed by name **before** the code is written, so the
diff can be checked against a closed list rather than judged case by case afterwards.

Confirmed at HEAD: `AdminTabs.test.tsx` now asserts the order explicitly, including
`'the invites tab sits between Users and Agent enrolls, matching the hi-fi order'` (`:72-81`).

## No false comment shipped

A review lens checked **every** library and Go line citation in the new comments: query-core
`mutation.js:47/123/144`, `mutationObserver.js:50-55`, and twelve `invites.go` line references. All
checked out.

Worth recording as a positive, because the previous several iterations each shipped at least one
comment that misdescribed the code it sat next to, and the batch's dominant defect class is exactly
that. The countermeasure that produced this outcome was not "write fewer comments"; the comments
here are long and dense. It was a lens whose entire brief was **open the cited file at the cited
line**, mechanically, for every citation in the diff. That is cheap and it is now the only technique
that has produced a clean prose result.

## Real-browser lane

All pass, and it is the lane that turned several arguments into observations:

- The tab renders for an admin and is correctly hidden from a non-admin.
- **All five seeded states render distinctly**, including the redeemed-and-expired row, which reads
  `REDEEMED`. That is the precedence decision observed rather than argued.
- The token is absent from the DOM, `localStorage`, `sessionStorage` and the URL after dismissal,
  **and after navigating away and back**. The second half is the part a unit test would not have
  asked.
- A **4-point hit test** proved the reveal dialog paints above the scrim despite `GlassPanel`
  creating its own stacking context. The `z-index` question that shipped a bug in #118 is now
  answered by pixels rather than by reading Tailwind classes.
- **The TTL presets were observed on the wire as `168h` and `720h`.** The single most important
  finding of the spec, confirmed at the only layer that can confirm it.

It also noted a nuance and correctly declined to call it a defect: `<input type="email">` native
constraint validation blocks clearly-malformed addresses before the app sees them, so the app's own
400 path is exercised in a real browser only for addresses that pass browser syntax and fail
`mail.ParseAddress`. See Deferred Findings for why this is not filed.

The 375px horizontal overflow it observed matched the already-filed
`bug-2026-08-12-web-narrow-viewport-horizontal-overflow` and was attributed there rather than
refiled.

## Key Decisions

- **The four states derive client-side, in the order REDEEMED, EXPIRED, EXPIRING, ACTIVE.** The
  server ships facts and no `status` field, by design (`internal/api/invites.go:112-121`). A
  server-asserted "expired" is stale the instant the row is on screen.
- **A redeemed invite that is also past its expiry reads REDEEMED.** Redemption is terminal and
  one-way (`MarkInviteUsed` carries `AND used_at IS NULL`), so expiry of an already-spent credential
  is a non-event. Pinned by the one test that an expiry-first implementation fails and every other
  test in the file passes.
- **The `EXPIRING` window is 1h, cited not invented** (`README.md:1300-1303`,
  `enrollmentStatus.ts:5`). Boundaries `<= 0` for expired and strict `<` for expiring, so the pill
  and the `EXPIRES` cell flip at the same instant.
- **`Chip` gains a fourth tone rather than collapsing EXPIRED and REDEEMED into `muted`.** The hi-fi
  encodes them differently on purpose. Colour is never the only channel: pill text differs and both
  terminal states dim their row.
- **`ACTIONS` is renamed `NOTE`.** No revoke, delete or resend endpoint exists, and the hi-fi asks
  for none - its cell renders prose. A header promising actions while delivering a sentence is
  itself a dead affordance. Asserted negatively with a positive control proving the query would find
  such a button if one were rendered.
- **The create form is tab-local, not shared with `CreateEnrollmentForm`.** The decision was already
  recorded at `CreateEnrollmentForm.tsx:22-25`; the hi-fi's `isInvite` boolean is the flag-driven
  component that rots. Only the reveal dialog is shared, and it took no new prop.
- **Email is validated by `type="email"` plus the server's 400.** Two parsers disagreeing is worse
  than one round trip, and the error renders in the form's own error slot rather than a page-level
  box.
- **No polling.** Pill freshness comes from `useNow(60_000)` (`InvitesTab.tsx:42`), which issues no
  request; row-set freshness comes from bare-prefix `['invites']` invalidation.
- **`gcTime: 0` and `reset()` are both required and neither is sufficient**, with `reset()` never
  inside `onSuccess` - query-core dispatches success only after awaiting it (`mutation.js:123` vs
  `:144`), so a `reset()` there would detach the observer before the notification and the reveal
  dialog would silently stop opening. Guarded by its own test.

## Problems Encountered

1. **The pending-disable gap (the medium).** Covered in full above. Fixed in both tabs.
2. **The overstated RED claim in the spec.** Covered in full above. Fixed with an independent
   control.
3. **The conductor asserted "last tab" repeatedly, and two documents inherited it.** It is the fifth
   and final tab to be **built**; it is **second** in the bar. The spec's opening sentence (`:8`) and
   the plan's Goal (`:5`) both read "the fifth and last admin-console tab", which reads as an
   ordinal position and is wrong as one. Both documents state the correct position correctly
   elsewhere (spec `:357`, `:563`; plan's modified-files table), so the error is confined to the
   opening sentence of each and never reached the code: `tabs.ts:18-19` carries the right comment and
   `AdminTabs.test.tsx:72` asserts the right order. **The backlog item does not repeat the error** -
   its own staleness is different, a title that still says the list half is blocked. Small, but it is
   the fourth consecutive iteration in which a claim travelled from the conductor's brief into a
   committed document without being checked, and this one had a one-line check available
   (`ADMIN_TABS`).
4. **`toggleSort` is now duplicated in five files, not four.** The spec and plan both say "four of
   those", which was true when they were written and stopped being true when this slice landed. The
   live set is `UsersTab.tsx:17`, `InvitesTab.tsx:22`, `EnrollmentsTab.tsx:16`,
   `ReservationsTab.tsx:21`, `WorkersPage.tsx:22`. A count in a plan is a measurement with a
   timestamp, and this one aged out inside its own slice.

## Findings Triage

- **1 medium (fixed, in two files), 0 high. 1 evidence finding (fixed). 1 nuance observed and
  rejected. 1 pre-existing item attributed rather than refiled.**
- **The medium was found by two independent lanes, each with its own probe**, and neither reasoned
  its way there from the code. `posts === 1`, no dialog, no token in the DOM, empty mutation cache -
  four observations, one conclusion.
- **Every citation in every new comment was opened and checked.** Zero false citations.
- **The five moved test assertions were enumerated before the code was written** and exactly those
  moved. No assertion was weakened.
- **The seventh copy of the cursor pager was confirmed character-for-character faithful** to its
  siblings. That is the only thing that makes shipping it defensible: seven identical copies are a
  mechanical extraction, and seven near-copies are a design exercise.

## Deferred Findings

Filed this pass, **proposed** for human accept rather than treated as accepted:

1. `idea-2026-08-13-cursor-pager-hook.md` (**idea/medium**) - the cursor-pager block is now at
   **seven** consumers against a project rule that says extract before the third. The item carries
   the two things that make the extraction safe: the **zero-line-diff gate** on all seven existing
   test files, and the requirement that **`statusTone` stays per-module** (invites maps EXPIRED to
   `err`, enrollments maps it to `muted`, so a naive merge would flatten a deliberate difference).
   The `formatExpiryLabel` / `EXPIRING_WINDOW_MS` duplication is included as the smaller, separable
   half.

Considered and **not** filed, with reasons:

- **The `<input type="email">` gating nuance. Rejected.** The observation is correct - the browser
  rejects clearly-malformed addresses before the app sees them, so the app's own 400 path is
  under-exercised **in a real browser**. But there is no defect and no work item behind it. The path
  is covered where coverage belongs: `api.test.ts` asserts a 400 surfaces as an `ApiError` carrying
  the status and the server sentence, and jsdom does not enforce native constraint validation, so
  the unit tests exercise it directly. The only two "fixes" available are removing `type="email"`
  (losing a free first pass and a mobile keyboard) or reimplementing `mail.ParseAddress` in
  TypeScript, which spec decision 14 explicitly rejects because two parsers disagreeing is worse than
  one round trip. An item whose every proposed resolution is already-rejected is an item nobody can
  close. The genuine residue - that `mail.ParseAddress` accepts display-name forms like
  `"Name" <a@b.com>` which `type="email"` rejects, so the browser is stricter in one direction and
  looser in another - is a two-line UX curiosity with no reported user, and filing it would put a
  speculative item in front of a human who never asked for it. Recorded here so it is a lookup rather
  than a rediscovery.
- **The 375px overflow.** Already covered by
  `bug-2026-08-12-web-narrow-viewport-horizontal-overflow`; attributed there, not refiled.
- **A revoke endpoint for invites.** Out of scope by spec ruling and by the hi-fi's own footnote,
  which states there is no revoke endpoint in v1. It is a product decision, not a UI gap. Note that
  the medium above makes the **absence** of one materially worse - a stranded ACTIVE invite has no
  cleanup path - but the medium is fixed, so the argument for a revoke endpoint is back to where it
  was.

## Known Limitations

- **`EXPIRING` and `EXPIRED` read the browser clock**, which makes the browser a **third** clock
  after the app host and the database (`bug-2026-08-13-token-expiry-two-clocks`). A badly skewed
  client mislabels a row and no server field exists to prefer. `REDEEMED` is immune, because it
  derives from a server-written timestamp's presence rather than from a comparison.
- **`total` is the unfiltered count over an unreaped table.** Nothing reaps `invites`
  (`idea-2026-08-13-reap-expired-invites-and-tokens`), so the list grows monotonically. Small-number
  territory at farm scale.
- **Invite creation leaves no audit trail.** A privileged action with no record; covered by
  `feature-2026-06-26-audit-log-admin-console-actions`, which names invite creation explicitly.
- **The seventh pager copy shipped.** Deliberate and recorded, but this slice is the event that
  starts the countdown running: at seven, the duplication is arguably the codebase's convention
  rather than a deferral.
- **No `?status=` filter and no search box.** Adding either makes the sort-plus-filter 400 rule at
  `internal/api/jobs.go:417-422` live for this endpoint.
- **The suite counts here were reported by the implementing lane, not re-run in this pass.** This
  pass had no shell.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, eighth
  iteration running, and it produced the two highest-value facts of the slice (no day unit,
  unconditional `readJSON`).
- **A backlog proposal is not a contract** - eight for eight. The item proposed a two-step ship with
  an honest empty state; step one was dead scope the moment `GET /v1/invites` landed, and the item's
  own preset labels would have 400ed on the wire.
- **Stage the work so RED is behavioral** - honored, and this is the iteration where the goal got its
  sharpest counter-example: a claimed RED that reddened for the wrong reason. See the new goal below.
- **Re-running the implementer's own proofs is cheap and should stay standard** - honored, and it is
  what caught both findings.
- **A wide RED is not a strong RED** / **a mutation that reddens is not a mutation that discriminates
  the claim** - both directly exercised. The spec's `reset()` claim is the worked example, one layer
  up from a test.
- **An overlay owns its own error surface** - honored; the create form's 400 renders in the form's
  own slot, never a page-level box behind its own scrim.
- **A TanStack invalidation test needs an active observer** - honored explicitly, with a comment at
  the site saying why a `fetchQuery` seed would pass vacuously.
- **Calling the clear function is not evidence** - honored in its strongest form. The assertion is
  that the mutation **cache is empty**, not that one field of one entry stringifies clean, because a
  `mutationFn` closure holds the invitee email where no state inspection can reach it.
- **Backlog housekeeping is required scope** - the `/backlog close` on
  `feature-2026-08-08-admin-invites-tab` is the conductor's step and had not run when this was
  written. **Its stale title must be corrected in the same commit**: it still reads "(create now,
  list blocked on GET /v1/invites)", which would leave a closed item asserting a block that was
  lifted before the work started.
- **When the Go diff is empty, spend the integration lane on a real browser** - honored, and it is
  the strongest instance yet: five states, a 4-point hit test, and the wire values observed.

New from this iteration:

- **A structural clone re-reviews its source.** The medium was inherited verbatim from a tab that
  shipped four days earlier and had already passed review. The clone got fresh attention the original
  never will, and that is the moment the original's defects become findable. When a plan says "mirror
  X at file:line", the review brief should say "and re-review X".
- **A claimed RED must name the assertion it reddens at.** "Removing X makes the suite red" is not
  evidence that the assertion you care about is live. Both the previous iteration's Go finding and
  this iteration's spec finding are the same failure: something upstream aborts first. State the
  assertion by name, or the claim is about liveness, not coverage.
- **Check the citation, do not read the comment.** The one lens that opened every cited file at every
  cited line is the reason this is the first clean-prose iteration in the batch. It is mechanical,
  it is cheap, and nothing else has worked.
- **A count in a plan is a measurement with a timestamp.** "Duplicated in four of those" was true
  when written and false when the slice it described landed. Counts of consumers, call sites and
  copies should be written as "N as of <commit>", or re-measured at Phase 6 rather than quoted.
- **`ADMIN_TABS` is one line and the conductor still got the order wrong four times.** When a brief
  makes an ordering or positional claim, the check is usually a single grep. Positional claims are
  the cheapest class of claim to verify and this batch verified none of them.

## The batch arc, and what it was actually about

This closes a five-item autopilot batch: schedule detail, profile pages, UserMenu roles, the two
web-enabler list endpoints, and this tab. Four frontend slices and one backend slice, and the
through-line is unmistakable.

**The dominant defect class across all five was not wrong code. It was wrong prose about correct
code.** In order: a self-falsifying comment; a mechanism that batching means can never happen; an
overlap guarantee refuted in both of its halves; an overstated mutation claim; and here, a RED
credited to an assertion it never reaches. Not one of these was a logic error. Every one of them was
a sentence - in a comment, a spec, a plan, or a conductor's brief - that described the code as
something other than what it is, and every one of them would have been believed by the next reader.

That class is uniquely expensive because it is invisible to the ordinary gates. A test suite cannot
go red over a comment. A type checker cannot see a spec. The only thing that catches it is a reader
who treats prose as a claim rather than as documentation, and that habit has to be assigned to
somebody or it does not happen.

**The counter-practice was equally consistent, and it is two rules:**

1. **Probe rather than reason.** Every finding that held up in this batch came with an instrument
   reading - a request counter, a `pg_locks` query, a hit test, a mutation-cache length. Every claim
   that got refuted came from reading code and concluding.
2. **Grep the claim's literal wording rather than chase it from memory.** Repeatedly, the thing that
   settled an argument was finding the exact sentence and reading what it actually said, not what
   everyone remembered it saying.

This iteration was the first to ship clean prose, and it did so because a lens was assigned the
mechanical citation check as its whole job. That is the batch's most transferable output: the defect
class is known, and there is now one technique with a track record against it.

## Files Most Touched

- `web/src/admin/invites/useInviteActions.ts` - 48 lines, of which the top 31 are the security
  comment block: why `gcTime: 0` and `reset()` are both required and neither sufficient, why
  `reset()` must never move into `onSuccess`, and why the guarding test asserts an empty cache rather
  than a clean field. The most important comment written this iteration and the one the citation lens
  checked hardest.
- `web/src/admin/invites/useInviteActions.test.tsx` - carries both halves of the evidence finding:
  the positive control taken **while an observer is attached** (`:103-110`), and the independent
  no-`reset()` counterpart at `:139-149` whose comment records the false claim it replaces.
- `web/src/admin/invites/CreateInviteForm.tsx` - the fix site. The Cancel button's comment
  (`:95-100`) is the clearest single statement of the medium.
- `web/src/admin/enrollments/CreateEnrollmentForm.tsx` and `EnrollmentsTab.tsx` - **not new code and
  not in the original scope.** The shipped defect, fixed alongside its copy.
- `web/src/admin/invites/api.ts` - `TTL_PRESETS` and the day-unit hazard comment. The label/value
  divergence is pinned by three separate assertions because a `"7d"` preset passes any naive test.
- `web/src/admin/invites/inviteStatus.ts` - the derivation, the load-bearing order, and the two
  bodies duplicated from `enrollmentStatus.ts` with `web/src/lib/expiry.ts` named as the destination
  for consumer three.
- `web/src/components/holo/Chip.tsx:12-20` - one key, eight lines of reasoning.
- `web/src/admin/tabs.ts:14-25` - the entry, and the corrected comment.
- `web/src/admin/AdminTabs.test.tsx`, `web/src/admin/AdminPage.test.tsx` - the five enumerated
  assertion moves.

## Verification

- **This pass had no shell.** Nothing was executed. Every claim below that could be checked by
  reading was checked against the worktree.
- **Reported by the implementing and verifying lanes, not re-run here:** web suite 973 -> 1049,
  green; `tsc -b && vite build` clean; the real-browser lane's five-state render, the 4-point hit
  test, the wire-value observation, and the storage/URL absence checks; the two independent probes of
  the medium; the RED for the independent cache control.
- **Verified by reading:** `ADMIN_TABS` order and the corrected comment (`tabs.ts:14-25`); the
  `AdminTabs.test.tsx` order assertion (`:72-81`); the `err` tone and its comment (`Chip.tsx:12-20`);
  `statusTone` mapping EXPIRED to `err` in invites (`inviteStatus.ts:61`) versus `muted` in
  enrollments (`enrollmentStatus.ts:30`); the duplicated `EXPIRING_WINDOW_MS` and `formatExpiryLabel`
  in both modules; the pending-disable on Cancel and the toggle in **both** tabs
  (`CreateInviteForm.tsx:101-104`, `CreateEnrollmentForm.tsx:98-101`, `InvitesTab.tsx:177`,
  `EnrollmentsTab.tsx:179`); the independent no-`reset()` control (`useInviteActions.test.tsx:139-149`)
  and the positive control taken before reset (`:103-110`); `gcTime: 0` and the bare-prefix
  invalidation (`useInviteActions.ts:38-45`); `useNow(60_000)` and the absence of any
  `refetchInterval` (`InvitesTab.tsx:42`); the footer and footnote copy including "all states"
  (`:136, :204-208`); the full fifteen-file set under `web/src/admin/invites/`; the **seven**
  `computePageRange` consumers and the **five** `toggleSort` copies; and the absence of any pager item
  in `docs/backlog/`.
- **Not verified:** all test results, the browser lane's observations, the suite counts, and anything
  requiring execution. Each is attributed above to the lane that reported it.
- **The backlog item is still open in `docs/backlog/` and was not edited to look closed.** The
  `/backlog close feature-2026-08-08-admin-invites-tab` is the conductor's required scope, and its
  stale title must be corrected in the same commit.
