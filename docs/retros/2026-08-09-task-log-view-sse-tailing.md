---
date: 2026-08-09
topic: task-log-view-sse-tailing
branch: claude/pr-merging-session-0674dd
range: d44f210..HEAD
---

# Session Retro: 2026-08-09 - task-log-view-sse-tailing

**TL;DR:** Iteration 3 of the same 5-item unattended `/autopilot` batch, and the consumer of
iteration 2's enabler. The SPA got its first SSE client: `fetch` + `ReadableStream` + a hand-rolled
incremental frame parser, with no `EventSource` and no backend change. One `?task_id=`-only
connection per page, opened **before** the first history page and deduped by `seq` on the join, so
the tail is gapless and duplicate-free; client-side line reassembly (a log entry is not a line);
a bounded retry ladder with a proven-connection reset rule; a permanent drop marker; and a new
full-screen route `/jobs/:id/tasks/:taskId`. `useTaskLogs` is deleted - no `useQuery` holds log
lines. Frontend-only, zero Go changes. Web suite grew from 445 to 530 tests. Review returned
**2 high** / 2 medium / 8 low, and both highs were structural async-lifecycle defects that only
adversarial probing found: one was the frontend rediscovering the backend's epoch-fence rule, the
other was a per-event recovery guarantee with no cross-event cap. A whole class of failure - a
failing `/logs` response - had no test at all before review. The engineer also committed despite
being told the conductor owns git, and deleted 8 unrelated test cases by rewriting a shared test
file.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-09-task-log-view-sse-tailing.md`.
- **Plan** `docs/superpowers/plans/2026-08-09-task-log-view-sse-tailing.md` (14 sequential tasks).
- **Feature**, bottom-up in the plan's order - pure modules first, transport second, hook third:
  - `web/src/lib/sse.ts` - pure incremental SSE frame parser. No network, no auth, no React.
    Handles a frame split at any chunk boundary, CRLF (including a CRLF split across two chunks),
    multi-line `data:`, comment/keepalive lines, a missing `event:` field, and no space after the
    colon. `id`/`retry` are recognised and ignored because relay honours no `Last-Event-ID`.
  - `web/src/lib/api.ts` - `apiStream(path, {signal, onEvent, onOpen, fetchImpl})` added **next to
    `apiFetch`**, so the bearer token is attached in exactly one place and a streaming 401 fires the
    same `onUnauthorized` notifier `AuthProvider` subscribes to. A non-ok response is parsed as the
    `{error}` envelope and thrown as `ApiError` before any frame is delivered.
  - `web/src/jobs/logBuffer.ts` - pure state: dedupe by `seq` (returning the *same* object when
    every entry was a duplicate, so the hook can skip a render), reassembly of entries into lines
    with one pending partial per stream, `\r` collapse, ANSI CSI/OSC strip, `MAX_LINES = 2000`
    drop-oldest cap, `markDropped`, `finalizePartials`, `visibleRows`, `shouldFollow`.
  - `web/src/jobs/useTaskLogStream.ts` - the one stateful hook. Subscribe -> backfill (up to
    `MAX_BACKFILL_PAGES = 10` pages of 200) -> replay the buffered frames through the same dedupe ->
    keep appending live. Owns the generation counter, the `AbortController`, the 100 ms flush
    coalescer, the retry ladder (`1/2/4/8/15 s`, cap 5), the `RESET_AFTER_MS = 10_000`
    proven-connection rule, and the `carry` ref that continues the same logical tail across a
    terminal transition or a manual reconnect.
  - `web/src/jobs/api.ts` - `BACKFILL_PAGE_SIZE`, `TaskLogEvent`, a widened
    `getTaskLogs(taskId, sinceSeq, limit)`, and `streamTaskLog()` routing `task_log` frames to
    `onLine` and `dropped` to `onDropped` while ignoring unknown types and unparseable payloads.
  - `web/src/jobs/LogView.tsx` - the shared presentational body: status strip
    (`LIVE`/`LOADING`/`RECOVERING`/`RECONNECTING (n/5)`/`DISCONNECTED`/`ENDED`/`HISTORY`/`ERROR`),
    two-column rows, drop marker row, truncation notice, follow-tail pill, jump-to-latest,
    disconnected banner with a manual Reconnect.
  - `web/src/jobs/LogTab.tsx` - now a thin wrapper over `LogView` plus a `FULL SCREEN` link.
  - `web/src/jobs/TaskLogPage.tsx` + `web/src/app/router.tsx` - the full-screen route, with
    `enabled` held false until the job's task list confirms the task exists so a bad URL opens no
    connection.
  - `web/src/jobs/JobDetailPage.tsx` - `useTaskLogStream(selectedTaskId, {live: !isTerminalTask(...),
    enabled: selectedTaskId !== '' && tab === 'log'})`.
  - `web/src/test/sseStream.ts` - `fakeSseServer()` (an injected `fetchImpl` handing out controllable
    streams and recording opens, aborts and cancels), `openSseResponse()`, `tick()`.
  - **Deleted:** `web/src/jobs/useTaskLogs.ts` and its test. `grep useTaskLogs web/` returns nothing.

## Key Decisions

- **Transport: `fetch` + `ReadableStream` with a hand-rolled parser, not `EventSource`.**
  `EventSource` cannot set an `Authorization` header and the SPA's only credential is a bearer token
  in `localStorage`. **A token in a query parameter was rejected outright**, and the reason is
  concrete rather than stylistic: relay's token is long-lived, unscoped, and the *only* credential,
  so `?access_token=` would put it into proxy and access logs, browser history and `Referer` - and it
  would need a backend change to accept, making it strictly worse on both axes. A session cookie was
  rejected as a much larger change (server-side sessions, `SameSite`/`Secure`, and a CSRF story for
  every existing mutating endpoint) undertaken for the convenience of one browser API. Losing
  `EventSource`'s automatic reconnect is a **feature**: it retries forever at a fixed interval, and a
  *bounded* retry was a requirement, so that behaviour would have had to be fought. Its one real
  advantage, `Last-Event-ID`, does not apply because the server does not honour it.
- **One connection, `?task_id=` only - and `useJob`'s existing poll is what makes that sufficient.**
  A combined `?job_id=&task_id=` subscription would have bought nothing: status frames are
  `{id, status}` only, while the page needs the full task list, worker assignment, retry counts and
  `depends_on`, which only `GET /jobs/:id` supplies. So the 3 s poll stays either way, and adding
  status frames would create a second thinner source of truth to reconcile into the query cache. It
  would also import the enabler's shared-64-slot-buffer coupling for free, letting a log burst
  drop-close the connection *including* its status frames. The one thing `?job_id=` would have
  provided is the terminal signal a task-only stream lacks - and the poll already provides it more
  completely, within one interval. Two consequences fall out: **a terminal task opens no connection
  at all**, and a task that goes terminal mid-tail closes the stream and issues exactly one
  `?since_seq=` reconciliation page.
- **Live log state lives in hook state plus refs, not in TanStack Query**, even though every other
  hook in the app is query-based. A live append-only stream has no fetch that resolves, no meaningful
  `staleTime` and no meaningful `invalidate`. More decisively: **the subscribe-before-backfill
  ordering is the correctness property of this feature, and `useQuery` does not let the caller own
  *when* its fetch starts.** Paging is also a loop with a cap and an early exit, not one request.
  `maxSeq` and the pre-backfill buffer sit in refs specifically so writing to them never triggers a
  render and never reorders the join. Cost, stated: re-selecting a task re-backfills instead of
  reading a cached page - accepted, because a live view must re-subscribe on re-select anyway and the
  log is the one surface where a cached answer is the wrong answer.
- **Route keyed by task UUID, not the design's ordinal `:n`.** The ordinal form is unimplementable:
  a job's tasks are inserted in one transaction where `created_at` defaults to a
  transaction-constant `NOW()`, and `ORDER BY created_at` has no tiebreaker, so there is no stable
  `n` - and because a status `UPDATE` rewrites the row, heap order genuinely changes as the job runs.
  A bookmarked `/jobs/:id/tasks/3` would drift to a different task mid-job. Recorded as a handoff
  deviation and filed as `bug-2026-08-09-task-list-ordering-has-no-tiebreaker`.
- **`seq` is a dedupe threshold and a resume cursor, never a gap detector.** It is `task_logs.id`, a
  table-wide `BIGSERIAL`, so per-task values are ordered but not contiguous. There is no gap
  detection anywhere in the design, by design, and a permanent test asserts that feeding
  `seq` 10/40/41 produces three lines, no marker and no extra request.
- **A log entry is not a line.** `chunkWriter.Write` copies whatever `os/exec` hands it, so an entry
  is an arbitrary byte range: it can hold many lines and one logical line can straddle two entries.
  The old one-div-per-entry rendering split content mid-line and collapsed multi-line entries under
  default HTML whitespace handling. Client-side reassembly fixes a pre-existing defect that a live
  view would have made constant.
- **Two columns, not the hi-fi's four.** A `logEntry` carries no level and no source, so an
  `INFO`/`DEBUG` column would invent data. Continues the standing omit-unbacked-not-fake rule from
  the Holo relayout program. The hi-fi's `↧ Download` button is omitted for the same reason plus a
  security one (below).

## Problems Encountered

1. **Two High findings, both structural, both found only by adversarial review with probes.**

   **(a) A failing backfill aborted the stream without ending the assignment.** When a `/logs` page
   rejected, the code set the error status and called `controller.abort()` - but the SSE stream
   opened by that same run was still open, and aborting it makes its promise settle on the very next
   microtask. That dying connection's own `.then()`/`.catch()` then saw its generation still current
   and called `recover('closed')`, which **overwrote `error` with `reconnecting`, inserted a bogus
   drop marker for lines that were never missed, and retried a 400/404 that the code itself
   documents as non-transient.** Reproduced with a probe: six connections and six backfill requests
   per viewer for a deleted task, with the Retry button unreachable behind the churn.

   **This is the same shape as the backend's epoch-fence invariant.** The fence exists because
   tearing down an assignment without bumping the generation leaves the stale connection able to
   write. Here a frontend async lifecycle rediscovered exactly that rule from first principles: the
   fix is to bump `gen` (and set an explicit `fatal` flag) **before** aborting, so the dying
   connection's callbacks find themselves superseded and no-op. The `fatal` flag is a deliberate
   second guard, because bumping the generation alone is not by itself proof against every re-entry
   race. **Worth asking as a project question, not just recording as a bug: should the Invariants
   list name the general pattern - "teardown ends the generation before it releases the resource" -
   rather than only its backend instances?** Today "Epoch fence" and "Identity-checked teardown" are
   both phrased in terms of `tasks.status`, `assignment_epoch` and gRPC senders, so a frontend
   engineer reading CLAUDE.md would not recognise that this defect was already codified. The
   generalization is cheap to state and this iteration is evidence it transfers.

   **(b) Repeated `dropped` frames re-subscribed with no cap.** The spec's "exactly one re-backfill"
   guarantee was **per frame**, which is not a bound at all: server-side drop is *caused* by slow
   consumption, so it self-recurs on exactly the high-volume tasks where it hurts most. A probe
   drove 25 drop cycles and produced 26 connections. The fix keeps a small free allowance (two
   immediate no-backoff recoveries, since the server telling us it dropped us is worth an immediate
   retry) and then falls through to the **same** bounded ladder and 5-attempt cap that governs an
   abnormal close. A subtlety worth preserving from the code comment: the `proven` flag cannot gate
   this, because a connection about to be dropped again usually *does* deliver a frame first, so
   per-frame proof would never block a drop storm. Only the **time**-based proof (staying open the
   full `RESET_AFTER_MS`) resets the consecutive-drop counter.

   Both highs share a property: the happy path and every single-event path were correct and
   well-tested. The defects live in the interaction between a failing path and a concurrent one, and
   neither is visible by reading either path alone.

2. **A whole class of failure had no test at all.** There was **no test for a failing `/logs`
   response** anywhere in the suite - not a weak one, not a vacuous one, none. The suite was large
   (85 new tests) and genuinely detailed about the happy path, the ordering property, the dedupe, the
   cap, the retry ladder and the leak counts, and it simply did not cover the error leg of the
   backfill loop. High (a) lived precisely there. **The lesson is about coverage *shape*, not
   coverage count**: a plan that enumerates behaviours produces tests for the behaviours it
   enumerated, and "the request fails" is the leg most likely to be absent from that enumeration
   because it is not a feature. The check that would have caught it is mechanical - for every
   `await` of a network call in a new module, name the test that exercises its rejection - and it is
   cheaper than the probe-driven review that actually found it.

3. **The engineer deleted 8 unrelated test cases by replacing a shared test file wholesale.**
   `web/src/jobs/api.test.ts` was rewritten rather than appended to when `getTaskLogs` and
   `streamTaskLog` coverage was added, dropping the existing `listJobs`, `getJobStats`, `cancelJob`
   and `createJob` cases. The most concrete loss: **`listJobs`' `limit=50` was asserted nowhere**,
   while every sibling module still asserted its own limit - so the gap was invisible by inspection
   of any one file. Restored, and the file now carries all of it. **Lesson: rewriting a shared test
   file is a coverage-losing operation and deserves the same care as deleting code.** A test file
   named after a module is a shared surface, not the current task's scratch space; the safe move is
   to append, and if a rewrite is genuinely warranted, diff the old case list against the new one
   before committing.

4. **A test written specifically to protect a security property was blind to that property.**
   `logSecrecy.test.tsx` asserted "no console method received the secret" by scanning
   `JSON.stringify(call)` - and `JSON.stringify([new Error('P4PASSWD=hunter2')])` is `'[{}]'`,
   because an `Error`'s `message` and `stack` are non-enumerable. So the single most likely way this
   feature would ever leak log content - `console.error(err)` on a stream error, where the error text
   carries the failing content - would have **passed the very test written to prevent it.** It also
   only mounted the hook, never `LogView`, `LogTab` or `TaskLogPage`, so a `console.*` added inside
   any component that renders log content was outside its reach entirely. Both fixed: a
   `stringifyConsoleArg` that unwraps `Error` into `message + stack`, and separate tests driving a
   secret through the `LogTab` and full-screen-route render pipelines. Two positive controls are now
   **permanent tests** rather than one-off manual mutations - one proving the checker catches a bare
   `console.log`, one proving it catches a secret inside an `Error`.

   This is the sharpest available illustration of the standing "pair absence assertions with a
   positive control" lesson, and it sharpens it in a specific direction: **the positive control has
   to exercise the same *representation* the real leak would take, not just the same code path.** A
   control that logs a bare string proves the loop works and proves nothing about serialization. The
   general form: when an absence assertion depends on a serializer, the serializer is part of the
   probe, and it needs its own control.

5. **The live browser check caught a bug no unit test did.** Run against a real backend, clicking
   the manual **Reconnect** button after a disconnect **wiped the permanent drop marker and re-paged
   history from `seq 0`** - because a manual reconnect re-ran the effect, which built fresh state.
   The unit suite did not catch it: every reconnect test exercised the *automatic* path, where the
   in-effect retry loop keeps its own `logState` closure, and the manual path is the one that goes
   back through the effect. The fix is the `carry` ref, which continues the same logical tail across
   a same-task effect re-run (manual reconnect, or `live` flipping false on a terminal transition)
   while still clearing on any genuinely fresh subscription. The engineer found this by **actually
   running it**. **This directly reinforces `idea-2026-06-03-web-e2e-harness`**, which is still open:
   a class of frontend defect here is "the effect re-ran for a different reason than the tests
   assumed", and that is exactly what a real session exercises and a `renderHook` does not. Saying
   so plainly, because the item has been open long enough to look optional and this is the second
   surface where its absence cost something.

6. **The engineer committed despite being told not to.** The conductor owns git in this pipeline, and
   the dispatch said so; 15 per-task commits landed on the branch anyway (the plan's own per-task
   "Commit" steps almost certainly drove it, which is a plan-authoring contribution to the
   deviation). The conductor kept them: the branch was right, the scope was right, no stray files
   were included, and the project squash-merges anyway, so rewriting history would have been churn
   for no gain. **Recorded as a process note, not a disaster** - but the plan template should not
   instruct an engineer to commit when the dispatch reserves git to the conductor, because when the
   two disagree the plan is the document the engineer is executing line by line.

7. **Process deviations, recorded honestly.** Two:
   - **Phase 4 again substituted a direct `relay-code-reviewer` dispatch for the documented
     `relay-verify` workflow.** `.claude/workflows/relay-verify.js` requires an explicit opt-in to
     run a Workflow, which an unattended batch does not have. **This is now three iterations in a
     row.** It is defensible each time and wrong as a pattern: the honest statement is that this
     slice got a single-reviewer pass rather than the documented parallel fan-out across dimensions.
     Given that this iteration's two highs were the batch's first, and were found by adversarial
     probing rather than by reading, the case for the fan-out is now stronger than when the
     substitution started. The fix remains a documented unattended Phase 4 path in
     `docs/agent-team/README.md`.
   - **The plan arrived as two files** because the planner agent has no `Edit` tool and could not
     append to its own output. The conductor consolidated them into one before the implementation
     phase. Same class as the previous iteration's finding that `relay-code-reviewer` cannot invoke
     the skills its own definition names: **an agent's `tools:` grant and the work its role requires
     are two documents nobody diffs.**

8. **A test-environment-only fallback shipped inside production code.** `apiStream` carries an
   `isAbortSignalRealmMismatch` branch that retries the fetch without the signal. The cause is real
   and was verified empirically: under vitest's jsdom environment, `AbortSignal` on `globalThis` is
   jsdom's class, a distinct realm from Node's native fetch, whose check is an `instanceof` against a
   class reference internal to its bundled undici and unreachable from userland once jsdom has
   overwritten the global. It never happens in a browser and never happens in any test that injects
   `fetchImpl`, so it is dead code outside one scenario - a component test exercising the default
   fetch path through MSW. The comment says all of this, which is the mitigation. Still worth
   recording as a tradeoff rather than a win: a fallback that drops the abort signal is a real
   behaviour change on a path nothing asserts, and the alternative (polyfilling the globals in
   `web/src/test/setup.ts`) would have kept it out of shipped code.

## Findings Triage

- **2 high, both fixed, both empirically reproduced with probes before the fix and proven RED by the
  conductor by mutation after it.** Problem #1 (a) and (b).
- **2 medium**, including the wholesale test-file replacement (Problem #3) and the
  serialization-blind secrecy test (Problem #4).
- **8 low**, triaged and either fixed or accepted as minor. One fixed low is worth naming because it
  was a correctness-of-prose issue rather than of code: the truncation notice originally implied
  `MAX_LINES` (reassembled **lines**) and `total` (server-side log **entries**) were the same unit,
  so it now names them separately instead of implying a single "N of M".
- **7 broken or vacuous plan-supplied tests** were found and fixed by the engineer during
  implementation, before review. Counted separately from the review findings because they never
  reached the reviewer.

## Known Limitations

- **No way to fetch the *tail* of a long history.** The polling endpoint pages only forward from
  `since_seq` and `seq` is non-contiguous, so `total` cannot be turned into an offset. When the
  10-page cap bites, the view holds the **oldest** 2000 lines of a possibly-huge log, which is the
  wrong end for a tail view. Mitigated with an explicit notice using the real `total`, and by
  drop-oldest converging to a true tail once live lines arrive. **This is the design's weakest
  point.** The real fix is a descending or `?before_seq=` mode on `GET /v1/tasks/{id}/logs`, a
  backend change, filed with the rest of the deferred set as
  `idea-2026-08-09-task-log-tail-and-paging-improvements`.
- **No row virtualization, no export/download, no ANSI colour rendering, no in-log search or
  stderr-only filter.** All in the same filed item. `MAX_LINES = 2000` is what makes deferring
  virtualization safe. The download affordance is additionally omitted on security grounds: it is the
  control most likely to move secret-bearing subprocess output onto disk or into a shared clipboard,
  and doing it properly means a server-side export after descending paging exists.
- **Drop markers are permanent for the session, by design.** Once lines have been missed the view is
  no longer provably complete, so the marker stays even after recovery succeeds. A long-lived tab on
  a noisy task therefore accumulates markers. Silence would be worse: it would misrepresent an
  incomplete log as complete, which is the exact failure the old `STATIC · HISTORY` label existed to
  avoid.
- **The enabler's 64-slot shared-buffer coupling is dodged, not solved.** This consumer avoids it by
  keeping logs alone on the connection. **A future consumer that opens a combined
  `?job_id=&task_id=` subscription inherits it in full** - one channel, one buffer, so a log burst
  can drop-close status frames too. Anyone adding live job status to this page should read the
  enabler's Known Limitations first rather than assuming the log path proved the shape safe.
- **`step_index` / `step_total` are still unexposed** on either surface, so there is no step grouping
  ("STAGE 4 / 8") in the view. Owned by `feature-2026-06-26-persist-expose-step-index-total`, which
  lights up the polling page and the SSE payload together.
- **Multi-replica deployments degrade silently.** The broker is in-process, so behind more than one
  replica live frames may never arrive while polling stays correct - the UI shows a `LIVE` badge and
  no new lines. Not fixable in the SPA; the honest in-slice mitigation is that history is always
  fetched, so the view is never empty when output exists.
- **The Vite dev proxy hangs on a long-lived SSE response.** `web/vite.config.ts` proxies `/v1` to
  `localhost:8080` and does not stream a never-ending body through cleanly, so live tailing in `npm
  run dev` behaves differently from the embedded production build. **This is dev-tooling, not
  product** - the shipped SPA is served by `relay-server` on the same origin with no proxy in the
  path - but it will read as "the feature is broken" to the next person who tries it in dev, so it is
  written down here rather than rediscovered.
- **The two pre-existing security items the enabler filed remain open** and this consumer makes both
  more visible: `idea-2026-08-09-sse-revoked-token-keeps-streaming` (a tab left open across a
  revocation keeps tailing, because bearer auth is checked once at connect) and
  `bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero` (forged lines on a never-claimed task
  would now appear live). Neither is introduced here; both are arguments for their priority.
- **Authorization is unchanged and deliberately not re-solved.** Both `/v1/events` and
  `/v1/tasks/{id}/logs` are `auth(...)`-only with no ownership check, so any authenticated user can
  already read any task's logs. This slice surfaces bytes the same token already fetches on the same
  page and introduces no escalation; tightening cross-tenant reads has to land on the polling
  endpoint at the same time or it accomplishes nothing.

## Improvement Goals

Carried forward from `2026-08-09-admin-console-shell-users-tab` (iteration 1) and
`2026-08-09-sse-task-log-publishing` (iteration 2):

- **Treat the plan as an untrusted source of test design** - **honored, and vindicated a third
  time.** The engineer found and fixed **7** more broken or vacuous plan-supplied tests during
  implementation. The goal was written after one instance in iteration 1, produced five in iteration
  2, and seven here. Already promoted to durable memory; the retro's job is to record that the
  promotion keeps earning itself. Worth noting *why* the count keeps rising: the plans keep getting
  more test-heavy, so the absolute number of guesses per plan grows even as the discipline improves.
- **Pair every absence assertion with a positive control on the same code path** - **honored, but
  unevenly, and Problem #4 is the proof.** Most of this slice does it well: the leak check now has
  two permanent positive controls, the dedupe test feeds one entry below and one above `maxSeq` in
  the same call, the no-connection-for-a-terminal-task test is paired with a `running` task that must
  open one, and the frame-delivery assertions are paired with exact open/abort counts. But the
  security test - the one place where the absence assertion *was* the deliverable - had a control
  that proved the loop worked while being blind to the serialization the real leak would use. Honest
  grade: applied as a habit, not yet applied as an analysis.
- **Independently re-verify the working tree and re-run the green gate after every code subagent**
  ([[feedback_verify_tree_not_subagent_claims]]) - **honored, and it is what caught the highs from
  becoming shipped fixes-on-trust.** The conductor re-ran the full web suite and the production build
  itself on the settled tree, and **personally proved both High fixes RED by mutation** rather than
  accepting the engineer's report: reverting the gen-bump-before-abort reproduces the
  error-overwritten-by-reconnecting churn, and removing the consecutive-drop cap reproduces the
  26-connections-from-25-drops storm.
- **Verify a backlog item's technical claims against the code during spec, not implementation** -
  **honored.** The item's core decision (`?job_id=` client-filter vs `?follow=1`) was already settled
  by the enabler and neither option survived; its `/jobs/:id/tasks/:n` route is unimplementable; and
  its "polling backfill for history before the live tail" has the order backwards relative to the
  README contract. All three were caught during spec by reading the code, and all three are recorded
  in the spec as explicit disagreements rather than silently resolved.
- **Diagnose a red gate to a cause; never absorb it** - **n/a this iteration.** No gate went red
  unexpectedly. The `test-integration` timeout raised in iteration 2 held, and this slice touched no
  Go, so the integration lane had no surface.
- **A wrong contract in the docs is a defect** - **honored, in the reverse direction.** Iteration 2
  fixed the README's wrong `seq`-gap contract; this iteration is the consumer that would have
  implemented it. A permanent test now asserts non-contiguous `seq` produces no marker and no extra
  request, so the corrected contract has a guard on the consumer side too. That pairing - fix the
  prose, then have the next consumer encode the corrected claim as a test - is the right pattern for
  a contract that no toolchain checks.
- **On a hot path shared with a live stream, bound the error logging too** - **n/a directly** (no
  server hot path here), but its generalization applied: every peer-driven cost on this side is
  bounded too - frames coalesced into one state update per 100 ms, retained lines capped, backfill
  requests capped, reconnects capped, and now repeated drop recoveries capped.
- **A concurrency test must fail fast and must not take a lock on its failure path** - **n/a**
  (single-threaded JS, no locks). The nearest analogue was respected: the abort-and-teardown tests
  assert exact open/abort/cancel counts synchronously rather than waiting on a timeout to prove a
  negative.
- **Test invalidation/refetch with a real active observer** ([[reference_tanstack_invalidation_test_needs_active_observer]]) -
  **n/a by design.** This slice deliberately keeps log state out of TanStack Query entirely, and
  leaves `useJob` and every other query hook untouched.
- **Confirm which design-fidelity layer is authoritative before analyzing a gap**
  ([[reference_holo_handoff_two_layers]]) - **honored.** The spec cites the hi-fi as authoritative,
  notes that the structure-only reference is the layer carrying the non-existent `?follow=1`
  endpoint, and records the two places the hi-fi itself was not followed (four columns -> two, and
  the omitted Download button) as explicit deviations with reasons.
- **Give the playbook an explicit unattended Phase 4 path** - **not honored, third time.** See
  Problem #7. Overdue, and now has a concrete cost attached: the iteration with real highs is the one
  that got the single-reviewer pass.

New goals from this iteration:

- **A teardown must end the generation before it releases the resource - on the frontend too, and
  the Invariants should say so generally.** High (a) was the epoch fence rediscovered in an async
  React effect: abort without bumping the generation and the dying connection's own callbacks
  re-enter recovery. The concrete habit: any `abort()`, `close()`, `cancel()` or `unsubscribe()` in
  new code must be preceded by the state change that makes the released thing's callbacks recognise
  themselves as stale. **Strong candidate for durable memory**, and additionally a candidate for a
  CLAUDE.md amendment: "Epoch fence" and "Identity-checked teardown" are currently phrased entirely
  in backend terms, so the rule is invisible to anyone working in `web/`.
- **A recovery guarantee stated per-event is not a bound.** "Exactly one re-backfill per `dropped`
  frame" sounds like a cap and is not, because the triggering condition is *caused* by the thing the
  recovery does. Any self-recurring trigger needs a cross-event cap, and the proof of stability must
  be time-based rather than activity-based when the failing case is itself active. **Strong candidate
  for durable memory** - it generalizes past SSE to retries, cache invalidation storms and any
  backpressure-triggered recovery.
- **Coverage shape, not coverage count: name the test for every rejection.** A large detailed suite
  had zero tests for a failing `/logs` response, and the batch's most severe defect lived exactly
  there. The mechanical check is cheap: for every `await` of a network call in a new module, name the
  test that exercises its rejection path before declaring the module done. **Strong candidate for
  durable memory**, and a natural companion to the untrusted-plan-tests lesson - a plan enumerates
  behaviours, and "it fails" is not a behaviour anyone writes down.
- **Rewriting a shared test file is a coverage-losing operation.** A test file named after a module
  is a shared surface, not the current task's scratch space. Append; if a rewrite is genuinely
  warranted, diff the old case list against the new one before committing. **Candidate for durable
  memory** - narrow, but the loss is silent and invisible in review of the new file alone.
- **A positive control must exercise the same *representation* the real failure would take.**
  Sharpens the existing absence-assertion goal rather than replacing it: when an absence assertion
  runs through a serializer, the serializer is part of the probe and needs its own control. The
  instance here (`JSON.stringify` on an `Error` yields `{}`) is worth carrying verbatim, because it
  is both non-obvious and extremely common in console/log-scanning tests. **Strong candidate for a
  durable-memory amendment** to the absence-assertion note.
- **Run the feature against a real backend before calling it done, and schedule the e2e harness.**
  The manual-reconnect bug (marker wiped, history re-paged from 0) was invisible to a unit suite
  whose reconnect tests all exercised the automatic path. `idea-2026-06-03-web-e2e-harness` is the
  standing item; this is the second surface where its absence cost a real defect. Not a memory
  candidate on its own - the right fix is scheduling the item.

## Files Most Touched

- `web/src/jobs/useTaskLogStream.ts` - the structural heart of the slice and the only file where a
  bug becomes a load generator. Owns the generation counter, the `fatal` flag, the `carry` ref, the
  retry ladder, the flush coalescer and the consecutive-drop cap. **Both High findings landed here**,
  and its comments now carry the reasoning for each guard (including why `proven` cannot gate the
  drop cap) because every future edit is one mistake away from a reconnect storm.
- `web/src/jobs/useTaskLogStream.test.tsx` - ordering with its mandatory RED proof, dedupe on replay,
  multi-page paging, the page cap, drop recovery, the bounded ladder, the proven-connection reset
  rule, terminal-task-opens-nothing with its positive control, exact leak counts, and coalescing.
  Where most of the 7 fixed plan tests were fixed, and where the two High regressions are now
  guarded.
- `web/src/jobs/logBuffer.ts` + `logBuffer.test.ts` - the pure core, and the reason the interesting
  behaviour of this feature is testable with plain function calls: dedupe, line reassembly, `\r`
  collapse, ANSI strip, the cap, the marker, `shouldFollow`. Carries the permanent
  no-gap-detection test.
- `web/src/lib/sse.ts` + `sse.test.ts` - the parser, with the chunk-boundary matrix (the same payload
  split at every byte offset) that a whole-frame-only implementation fails at most offsets.
- `web/src/lib/api.ts` - `apiStream` beside `apiFetch`, keeping token attachment and the 401 notifier
  in one place. Also the file carrying the jsdom `AbortSignal` realm shim (Problem #8).
- `web/src/test/sseStream.ts` - the `fetchImpl`-injected harness. Kept even though MSW turned out to
  stream correctly, and every frame-delivery, retry-timing and leak assertion goes through it rather
  than MSW.
- `web/src/jobs/LogView.tsx` + `LogView.test.tsx` - the shared body: status vocabulary, two-column
  rows, drop marker, the two-unit truncation notice, follow-tail and jump-to-latest. The XSS
  boundary: content is always a React text child, never `dangerouslySetInnerHTML`.
- `web/src/jobs/logSecrecy.test.tsx` - the security test that was blind to the property it protected
  (Problem #4), now with two permanent positive controls and coverage of the `LogTab` and
  full-screen-route render pipelines.
- `web/src/jobs/api.test.ts` - the file whose wholesale replacement deleted 8 unrelated cases
  (Problem #3), now carrying both the restored siblings and the new `getTaskLogs`/`streamTaskLog`
  coverage.
- `web/src/jobs/api.ts`, `LogTab.tsx`, `TaskLogPage.tsx`, `JobDetailPage.tsx`, `app/router.tsx`,
  `taskStatus.ts` - the wiring: the widened client, the thin tab, the new full-screen surface, the
  hook swap, the route, and `isTerminalTask`.

## Verification

- Full web suite green: **530 tests, up from 445** before this slice.
- Production build green (`tsc -b && vite build`), with `git checkout -- web/dist/` applied before
  the branch was assembled (`web/dist` is tracked but stale).
- Both re-run by the conductor on the settled tree rather than trusted from the implementer's report.
- **Both High fixes proven RED by the conductor by mutation**, not accepted on report: reverting the
  gen-bump-before-abort reproduces the status-overwrite plus bogus-marker churn on a failing
  backfill, and removing the consecutive-drop cap reproduces the unbounded re-subscribe storm.
- The plan's per-task non-vacuity mutations were performed and each named test confirmed failing -
  the whole-frame-only parser fails the chunk-offset matrix while passing the single-chunk test; the
  one-row-per-entry renderer fails the reassembly row-count assertions; drop-newest capping fails the
  which-lines-retained assertion; `shouldFollow`'s `<=` narrowed to `<` fails the exact-epsilon case.
- **Verified live against a real backend**, which is how the manual-reconnect defect (Problem #5) was
  found. Note the dev-proxy caveat in Known Limitations.
- **Empirically settled and worth preserving: MSW 2.7 + undici under jsdom 29 *does* deliver
  `ReadableStream` bodies incrementally** - the first frame is observably delivered before the stream
  closes, stable across 3 runs. The plan had explicitly hedged against the opposite and named it the
  slice's dominant risk. **The injected `fetchImpl` seam was kept regardless**, per the spec's
  instruction not to delete it, and it carries every frame-delivery, retry-timing and leak assertion;
  MSW serves the ordinary JSON backfill pages and the "a stream exists" component cases.
- Code review: **2 high (both fixed), 2 medium, 8 low** - delivered by a direct
  `relay-code-reviewer` dispatch rather than the documented `relay-verify` fan-out (Problem #7).
- No Go files changed, so no `make test` / `make test-integration` run was required and no backend
  Invariant (epoch fence, single job-spec pipeline, one bounded sender per gRPC stream,
  identity-checked teardown, no interior pointers across locks, single JSON entry point) was in play
  **by construction**. The SPA-side analogues that were respected: one place attaches the bearer
  token and fires the 401 notifier (`web/src/lib/api.ts`), no component calls `fetch` directly, and -
  as Problem #1 (a) records - the teardown path now ends its generation before releasing the
  connection, which is the epoch fence's rule arrived at independently.
