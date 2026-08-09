---
date: 2026-08-09
topic: autopilot-batch-admin-and-sse
branch: claude/pr-merging-session-0674dd
range: 5d492f9..HEAD
---

# Session Retro: 2026-08-09 - autopilot batch (admin console + live task logs)

**TL;DR:** One unattended `/autopilot 5` run: five items, five merges, five closed backlog items,
seven new ones filed. Two deliberate chains rather than five unrelated slices - a backend enabler and
then its first consumer (SSE task-log publishing, then the SPA's live log view), and an admin console
shell and then two tabs on its registry (Users, then Agent enrollments, then Reservations). The web
suite doubled, 355 -> 710 tests. Review across the batch: **2 high, 13 medium, 37 low**; both highs in
one iteration, the SSE consumer, and both found by adversarial probing rather than by reading.
Per-topic detail lives in the five topic retros written along the way; this is the session-level
synthesis.

## What Was Built

Five slices, each a full `spec -> plan -> implement -> verify -> integrate -> retro` pass:

- **Admin console shell + Users tab** - `/admin/:tab` replacing the placeholder, a registry-driven
  tab bar, an `is_admin` route guard and nav filter, and a fully wired Users tab (list, sort, cursor
  pagination, email filter, create, rename, archive/unarchive, admin password reset). The omnibus
  five-tab backlog item was closed as decomposed after this first slice.
  Retro: `2026-08-09-admin-console-shell-users-tab.md`.
- **SSE task-log publishing (backend enabler)** - `GET /v1/events?task_id=` plus a `task_log` event
  per persisted chunk, routed through a second task-keyed broker index so a log publish never becomes
  a cluster-wide firehose. `AppendTaskLog` went `:exec` -> `:one` so the epoch fence's outcome is
  observable and the publish can be gated on it.
  Retro: `2026-08-09-sse-task-log-publishing.md`.
- **SPA task-log view + live tailing (the enabler's consumer)** - the app's **first SSE client**:
  `fetch` + `ReadableStream` + a hand-rolled frame parser, subscribe-before-backfill with `seq`
  dedupe on the join, client-side line reassembly, a bounded retry ladder, a permanent drop marker,
  and a new full-screen route. `useTaskLogs` deleted.
  Retro: `2026-08-09-task-log-view-sse-tailing.md`.
- **Admin Agent enrollments tab** - create an enrollment token and list the live ones, with the raw
  token revealed clear-text exactly once in a shared `TokenRevealDialog` - the first surface in the
  SPA that displays a credential it can never retrieve again.
  Retro: `2026-08-09-admin-enrollments-tab.md`.
- **Admin Reservations tab** - list, create and delete worker reservations, with a worker picker and
  the console's first real destructive control, and with copy corrected against what a reservation
  actually does.
  Retro: `2026-08-09-admin-reservations-tab.md`.

Closed: `feature-2026-06-26-admin-console-pages` (as decomposed),
`feature-2026-06-26-sse-task-log-publishing`, `feature-2026-06-26-task-log-view-sse-tailing`,
`feature-2026-08-08-admin-enrollments-tab`, `feature-2026-08-08-admin-reservations-tab`.

## Key Decisions

- **Sequence the enabler before its consumer, in the same batch.** The enabler settled the transport
  shape (`?task_id=` on `/v1/events` rather than `?follow=1` on the logs endpoint) on an argument that
  is a property of the existing broker rather than a preference, and the consumer inherited it with no
  renegotiation. The payoff shows up twice: the consumer's spec spent its budget on the join ordering
  and the retry bound instead of the transport, and the enabler's corrected README `seq` contract
  arrived as a **test** on the consumer side one iteration later.
- **Build the shell before the tabs, and keep unbuilt tabs out of the registry.** Four "coming soon"
  panels would have been four dead tabs; the registry made adding a real one a one-line change.
- **Honest omission over fabrication, throughout.** No role-change control (no endpoint mutates
  `is_admin`), no revoke control (no `DELETE /v1/agent-enrollments/{id}`), no `SESSIONS` / `LAST LOGIN`
  / `service` role, no owner column on either token-bearing table, no `INFO`/`DEBUG` log columns, no
  download button, and no reservation copy implying an affinity the scheduler does not implement. Each
  absence is explained in a footnote or a filed enabler item rather than merely missing. This continues
  the rule established by the Holo relayout program and it held in all five iterations.
- **Where the transport choice was security-relevant, it was decided on the threat model.** A token in
  a query parameter was rejected outright for live tailing (relay's bearer token is long-lived,
  unscoped and the only credential, so `?access_token=` puts it in proxy logs, history and `Referer`),
  which is what forced the hand-rolled `fetch` client over `EventSource`.
- **Match the existing authorization model rather than tightening it mid-slice.** Both `/v1/events`
  and `/v1/tasks/{id}/logs` are `auth(...)`-only with no ownership check; a live view of bytes the
  same token already polls is no escalation, and gating only the live path would accomplish nothing.
  Cross-tenant reads were filed, not partially fixed.

## Problems Encountered

### 1. Two chains, and the second-instance effect is real

Both chains are evidence for the same bet the Holo relayout program made: build the shared thing, then
instances get cheap. The second and third admin tabs each had a **shorter spec** (mostly an explicit
"what is inherited, not re-derived" table rather than argument), a **shorter plan** (tasks that read
"mirror `web/src/admin/users/X` at `file:line`, change the nouns"), and **fewer findings** than the
first: 4 medium / 8 low, then 2 / 5, then 2 / 7. The corollary is the more useful half: **findings
track novelty, not diff size.** In iteration 4, zero of seven findings landed in the cloned files and
both mediums landed in the two files with no precedent; in iteration 5 the same held. Review attention
should be aimed at the non-clone.

### 2. Verification found things nothing else did

Honest totals from the topic retros: **2 high, 13 medium, 37 low.** Both highs were in the SSE
consumer, both were structural async-lifecycle defects in the interaction between a failing path and a
concurrent one, and both were **empirically reproduced with probes** before being fixed (six
connections and six backfill requests per viewer for a deleted task; 26 connections from 25 drop
cycles) and then proven RED by mutation by the conductor afterwards. Neither is visible by reading
either path alone, and the happy path and every single-event path were correct and well tested in both
cases.

Worse than a weak test: in the same iteration a **whole class of failure had no test at all** - there
was no test anywhere for a failing `/logs` response, and the batch's most severe defect lived exactly
there. That is a coverage-*shape* problem rather than a coverage-count one: a plan enumerates
behaviours, and "the request fails" is not a behaviour anyone writes down. The mechanical check is
cheap, and cheaper than the probe-driven review that actually found it.

### 3. Plan-supplied code and tests were wrong repeatedly - now a pattern, not incidents

One vacuous plan-supplied test in iteration 1 (a timing-based absence assertion at 4% of the interval
it claimed to catch), **five** broken or vacuous ones in iteration 2, **seven** in iteration 3, and in
iteration 5 a **logic bug in the plan's own reference implementation** that its own test contradicted.
The count rising as the discipline improved is explained by the plans getting more test-heavy: more
guesses per plan even at a better hit rate. The durable form was promoted mid-batch, and the batch
then earned the promotion twelve more times. The one widening this batch supports: it is not only test
bodies. **Any code a plan hands the engineer is a draft**, including reference implementations that
look authoritative because they carry comments.

### 4. Upstream docs were wrong in every single iteration

Every one of the five backlog items misdescribed the code, and in ways that would have shaped the
build: an `:email`-keyed PATCH and a role-change control that no endpoint supports; a bound that
`Broker.Publish` already had, which would have consumed a task while the real risks went unaddressed;
a `/jobs/:id/tasks/:n` route that is unimplementable because a job's task order has no tiebreaker;
a named token-reveal component that is actually the create form, leaving the highest-consequence part
of that slice undesigned; and a Reservations item that asked only whether the `selector` footnote was
true while `user_id` and `project` are equally inert. **Two of the five were items the TPM had authored
itself hours earlier in this same batch.** That is the sharpest available statement of the lesson:
authorship is not evidence. An item written from a design handoff encodes the handoff's assumptions,
and writing it does not check them.

The same class showed up in the other direction too: the README's `seq`-gap sentence was a **wrong
contract in prose sitting next to correct code comments**, and nothing in a Go toolchain checks prose.
A client implementing it would have re-paged on nearly every frame on a busy farm.

### 5. The same structural bug appeared on both sides of the stack

The backend epoch fence exists because tearing down an assignment without bumping the generation
leaves the stale connection able to write. High (a) in iteration 3 was a React effect calling
`controller.abort()` on a still-open SSE stream **without** bumping its generation, so the dying
connection's own settled promise found its generation current and re-entered recovery - overwriting a
fatal error with `reconnecting`, inserting a drop marker for lines that were never missed, and
retrying a non-transient 400. The fix is the fence's rule arrived at independently: end the generation
*before* releasing the resource.

**Worth asking as a project question rather than only recording as a bug:** should the Invariants in
CLAUDE.md name the general pattern - "teardown ends the generation before it releases the resource" -
rather than only its backend instances? Today "Epoch fence" and "Identity-checked teardown" are phrased
entirely in terms of `tasks.status`, `assignment_epoch` and gRPC senders, so a frontend engineer
reading CLAUDE.md would not recognise that this defect was already codified. This batch is evidence
that the rule transfers, and the generalization is cheap to state.

### 6. A verification gate had been silently failing for every change

`make test-integration` ran at a 300s bound against an `internal/api` integration package that takes
~320-340s, so it was failing on a package timeout regardless of the diff, and nobody had noticed. The
iteration diagnosed it rather than absorbing it, and measured the baseline **both ways** (338.8s with
the new test removed, 321.6s with it present) to establish that the red was pre-existing rather than a
regression the change was masking. Raised to 900s with the reason in the Makefile. A gate that has
quietly been failing is worse than no gate: its red is indistinguishable from a real red, and the habit
of absorbing it is precisely what makes a real failure invisible.

### 7. Process deviations, recorded plainly

- **Phase 4 substituted a direct `relay-code-reviewer` dispatch for the documented `relay-verify`
  workflow in all five iterations.** The workflow needs an explicit opt-in an unattended batch cannot
  give. Each substitution was defensible; five-for-five is not a deviation any more, it is the de facto
  pipeline, and **the playbook now arguably documents something the pipeline does not do.** The cost is
  concrete rather than theoretical: the one iteration with real highs is the one that got a
  single-reviewer pass instead of a parallel fan-out across dimensions. Fix is a documented unattended
  Phase 4 path in `docs/agent-team/README.md`, not per-run improvisation.
- **The reviewer could not invoke the skills its own definition names.** `relay-code-reviewer.md`
  instructs it to call `/code-review` and `/security-review` via the Skill tool, and its frontmatter
  grants no Skill tool, so every review ever dispatched to it has silently been an ad-hoc pass. Filed.
  The planner hit the same class - no `Edit` tool, so its plan arrived as two files the conductor had
  to consolidate. **An agent's `tools:` grant and the work its role requires are two documents nobody
  diffs.**
- **One engineer committed despite being told not to.** The conductor owns git in this pipeline and the
  dispatch said so; 15 per-task commits landed anyway, almost certainly driven by the plan's own
  per-task "Commit" steps. Kept, because the branch and scope were right and the project squash-merges.
  The plan template should not instruct an engineer to commit when the dispatch reserves git to the
  conductor: when the two disagree, the plan is the document being executed line by line. The next
  iteration's engineer correctly did not commit.
- **One agent was killed mid-run by an API connection error and resumed from its transcript with no
  loss.** Task 2 of 9; nothing was redone, no file was left half-edited, the remaining tasks proceeded
  in order. Recorded as a positive: long sequential iterations are exactly where a mid-run kill is
  likely, and knowing transcript resume is a real recovery path changes how to handle it next time.

## Known Limitations

Session-level; per-slice detail is in the topic retros.

- **Live tailing degrades silently behind more than one replica.** The broker is in-process, so a
  `task_log` event only reaches clients connected to the process owning that agent's stream. The UI
  shows `LIVE` and no new lines while polling stays correct. Cross-process fan-out is its own design
  (NOTIFY payloads cap at 8000 bytes, so log content needs a re-read by `seq`); proposed, not filed.
- **The log view holds the *oldest* lines of a huge log**, because the polling endpoint pages only
  forward from `since_seq` and `seq` is non-contiguous, so `total` cannot become an offset. The
  design's weakest point, mitigated by an explicit notice and by drop-oldest converging to a true tail
  once live lines arrive. Real fix is a descending or `?before_seq=` mode; filed.
- **Two pre-existing security items surfaced and amplified by the SSE work remain open:**
  `bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero` (the log append fences on epoch but never
  checks sender identity, and `assignment_epoch` defaults to 0) and
  `idea-2026-08-09-sse-revoked-token-keeps-streaming` (bearer auth checked once at connect). Neither is
  introduced by this batch; both are more visible because of it.
- **The admin console is three tabs of five.** Server overview is a quick win over existing stats; the
  Invites *list* half stays blocked on a `GET /v1/invites` that does not exist.
- **No role-change control and no enrollment revoke**, because neither endpoint exists. A leaked
  unconsumed enrollment token cannot be killed from the UI; expiry or consumption are its only terminal
  states.
- **`ConfirmDialog` has no focus trap and now has four consumers**, including one whose sole content is
  an unrecoverable credential that a single Escape destroys.
  `idea-2026-07-01-confirmdialog-focus-trap-hardening` passed its own "schedule before a third
  consumer" threshold inside this batch.
- **No web e2e harness.** Two defects in this batch were found only by running the feature against a
  real backend (the manual-reconnect path that wiped the drop marker and re-paged from `seq 0`), which
  a `renderHook` suite structurally cannot reach. `idea-2026-06-03-web-e2e-harness` has now cost
  something on a second surface.
- **`step_index` / `step_total` still unexposed**, so no step grouping in the log view.
- **The Vite dev proxy hangs on a long-lived SSE response**, so live tailing behaves differently in
  `npm run dev` than in the embedded production build. Dev tooling, not product, but it will read as
  "the feature is broken" to the next person who tries it.

## Improvement Goals

Carried forward from `2026-07-01-autopilot-and-web-relayout`:

- **Independently re-verify the working tree and re-run the green gate after every code subagent**
  ([[feedback_verify_tree_not_subagent_claims]]) - **honored in all five, and the batch's most
  load-bearing habit.** The conductor re-ran suites and production builds itself on settled trees, and
  went further three times: RED-proving a fixed vacuous test by injecting `refetchInterval: 3000`,
  proving both High fixes RED by mutation, and measuring the integration-timeout baseline both ways.
  Trusting an engineer's "all green" would have shipped on top of a gate nobody could pass.
- **Confirm which design-fidelity layer is authoritative before analyzing a gap**
  ([[reference_holo_handoff_two_layers]]) - **honored in all five**, and twice it was reading the hi-fi
  closely that exposed the iteration's biggest problem: that a named component does not do what the
  item said, and that the hi-fi's own framing is wrong about the system.
- **Test invalidation/refetch with a real active observer, not a `fetchQuery` seed**
  ([[reference_tanstack_invalidation_test_needs_active_observer]]) - **honored** in all four frontend
  slices that use TanStack mutations, with the reason written inline so it cannot be reverted to a
  seed; n/a in the log view, which deliberately keeps live state out of Query entirely.
- **Large-migration playbook (primitives-first, one PR per page, don't force the primitive,
  omit-unbacked-not-fake)** - **honored where applicable.** Not a migration, but omit-unbacked-not-fake
  applied in every iteration, "don't force the primitive" ruled out several Holo primitives as
  semantically empty here, and no shared primitive was modified to serve a caller. The batch also
  extends the playbook's bet from presentational primitives to a whole feature-module pattern (see
  Problems #1).

New session-level goals:

- **The conductor should treat plan-supplied *code* as a draft, not only plan-supplied tests.** Already
  promoted for tests; this batch adds a reference implementation whose own test contradicted it.
  **Candidate for a one-line amendment** to the untrusted-plan-tests note rather than a new note.
- **Give the playbook an explicit unattended Phase 4 path.** Five-for-five deviation means the
  documented pipeline and the real one have diverged, which by the project's own standard
  ([[feedback_docs_contract_is_a_defect]]) is a docs defect. Either document the substitution as the
  unattended path or make `relay-verify` runnable unattended. **Not a memory candidate** - it is a doc
  change in `docs/agent-team/README.md`, and it is now the oldest unhonored goal in the sequence.
- **Consider generalizing the Invariants to name patterns, not only their backend instances.** See
  Problems #5. A CLAUDE.md amendment, and worth putting to the human as a question rather than filing.
- **Audit each agent definition's prose against its `tools:` grant.** Two agents in this batch could
  not do what their own definitions instruct, and both failures were silent by construction. One is
  filed; the general check belongs wherever `.claude/agents/` is reviewed.
- **Do not let the plan template instruct an engineer to commit when the dispatch reserves git.** A
  cheap, mechanical fix to the plan template that removes a whole class of process deviation.
- **Schedule the e2e harness.** Two defects in this batch were reachable only by running the thing.
  This is the second batch making the same argument; the item is the fix, not a habit.

## Files Most Touched

- `internal/events/broker.go` (+ `broker_test.go`) - the structural heart of the enabler: `Filter`, the
  two subscription indexes, and `removeLocked` as the single close-point, because one channel can live
  in two maps and must be closed exactly once on four exit paths.
- `internal/worker/handler.go` - `handleTaskLog` rewritten around the fenced insert's returned row,
  plus the bounded persist-failure limiter. The only file in the batch where a bug reaches a live agent
  connection.
- `internal/api/events.go`, `internal/store/query/tasks.sql`, `internal/relayclient/client.go`,
  `README.md`, `Makefile` - `?task_id=` validation ahead of the response headers, the `fence`/`ins`
  CTE, the scanner buffer, the corrected events contract, and the integration timeout.
- `web/src/jobs/useTaskLogStream.ts` (+ its test) - the SPA's only live-stream state machine and the
  only frontend file where a bug becomes a load generator. Both High findings landed here.
- `web/src/lib/sse.ts`, `web/src/lib/api.ts`, `web/src/jobs/logBuffer.ts`, `web/src/test/sseStream.ts` -
  the pure parser, `apiStream` beside `apiFetch` (one place attaches the token and fires the 401
  notifier), the pure buffer, and the injected-`fetchImpl` harness.
- `web/src/admin/{AdminPage,AdminTabs}.tsx` + `tabs.ts` + `web/src/app/AdminRoute.tsx` +
  `web/src/shell/HoloShell.tsx` - the shell, the registry every later tab entered through in one line,
  the `is_admin` guard and the nav filter.
- `web/src/admin/users/*` - the pattern-setting tab: the widest surface, the most findings, and the
  template the next two mirrored.
- `web/src/admin/TokenRevealDialog.tsx` + `web/src/test/secretLeaks.ts` - the only place in the app
  that renders a raw credential, and the extracted leak matchers.
- `web/src/admin/{enrollments,reservations}/*` - the two cloned modules; their novel files
  (`TokenRevealDialog`, `WorkerPicker`, the status derivations) are where every medium finding landed.
- `web/src/lib/{time,useNow,useDebouncedValue}.ts` - the small shared additions the tabs forced,
  deliberately in `lib/` rather than `admin/`.
