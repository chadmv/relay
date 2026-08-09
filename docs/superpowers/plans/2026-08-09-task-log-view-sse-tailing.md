# Task Log View + Live SSE Tailing (SPA) - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **14 tasks, sequential.** The front matter below (slice declaration, file structure, conventions, non-vacuity doctrine, scope guard) governs all of them.

**Goal:** Make the SPA tail a task's log live - `GET /v1/events?task_id=<uuid>` consumed over `fetch` + `ReadableStream` with a bearer header - in the job-detail Log tab and in a new full-screen view at `/jobs/:id/tasks/:taskId`, with a gapless subscribe-then-backfill join, a bounded retry, bounded memory, and no connection leak.

**Architecture:** Four layers, bottom-up. (1) `web/src/lib/sse.ts` is a pure incremental SSE frame parser - no network, no auth. (2) `apiStream` in `web/src/lib/api.ts` is the single authenticated streaming entry point (bearer header, 401 notifier, `ApiError` on a non-ok response) with an injectable `fetchImpl` seam. (3) `web/src/jobs/logBuffer.ts` is pure state logic: dedupe by `seq`, reassemble entries into lines, collapse `\r`, strip ANSI, cap at 2000 lines. (4) `web/src/jobs/useTaskLogStream.ts` is the one stateful hook: it subscribes, then pages history, then replays the buffered frames through the same dedupe, and owns recovery. `web/src/jobs/LogView.tsx` is the shared presentational body used by both surfaces. No log line ever enters the TanStack cache.

**Tech Stack:** React 18, TypeScript, TanStack Query v5 (untouched by the log path), react-router-dom v7, Tailwind v4 (Holo tokens), Vitest 2.1.8 + jsdom 29 + MSW 2.7.

**Spec:** `docs/superpowers/specs/2026-08-09-task-log-view-sse-tailing.md`

---

## Slice independence declaration

- **Backend slice: none. This plan changes ZERO Go files.** Confirmed against the spec's Decision 1 ("this design requires zero Go changes") and against the code: `GET /v1/events?task_id=` already validates, subscribes and streams (`internal/api/events.go:14-95`), `GET /v1/tasks/{id}/logs` already pages `?limit=1..200&since_seq=` and returns `{items, next_seq, total}` (`internal/api/tasks.go:63-137`), and both are already `auth(...)`-registered (`internal/api/server.go:124,171`). No `.sql` edit, so **no `make generate`**, no CRLF-revert dance, no migration. No backend Invariant (epoch fence, single job-spec pipeline, one bounded sender per gRPC stream, identity-checked teardown, no interior pointers across locks, single JSON entry point) is in play. The SPA-side analogue of the last one **is** in play and Task 5 carries it: exactly one place attaches the bearer token and fires the 401 notifier, and `apiStream` goes **in that file** rather than opening a second authenticated transport path.
- **Frontend slice: one, and it is SEQUENTIAL.** Do **not** split these tasks across two engineers.
  - Tasks 2, 3 and 4 all write `web/src/jobs/logBuffer.ts` and its test file; Task 3 depends on Task 2's types.
  - Tasks 7, 8 and 9 all write `web/src/jobs/useTaskLogStream.ts` and its test file, each extending the previous effect body.
  - Tasks 10-13 all write into `web/src/jobs/`; Tasks 12 and 13 both touch `JobDetailPage.tsx` / `router.tsx`.
  - Task 6 (`web/src/jobs/api.ts`) is imported by Tasks 7-9; Task 5 (`apiStream`) is imported by Task 6.
  - The project has been burned **twice** by concurrent writers on shared frontend files. One engineer, tasks in order.
- **On the "pure modules are independent" question, answered honestly: they are NOT a usable parallel cut.** Tasks 1-4 are independent *of the UI wiring* in dependency terms, but they land in `web/src/lib/` and `web/src/jobs/` - the same trees Tasks 5-13 edit - and Tasks 2-4 share one file with each other. Splitting them off would buy one engineer's worth of latency at the cost of the exact collision class this project has already paid for twice. **Sequential.**
- **Parallelism the conductor can use:** none within this plan. If the batch contains other unrelated items, they can run alongside this whole plan.

---

## Ordering rationale: pure modules first, transport second, hook third

**Do not reorder Tasks 1-5.** The spec names one dominant risk (Risks, first bullet): whether MSW 2.7 + undici under jsdom 29 delivers a `ReadableStream` body **incrementally** is unverified. An interception layer that buffered the whole body before resolving would make the transport test silently vacuous - the parser would still look green, the hook would still look green, and live tailing would be broken in the browser only.

1. **Tasks 1-4 need no network at all.** `sse.ts` and `logBuffer.ts` carry the interesting behaviour (frame splitting, dedupe, line reassembly, cap) and are tested with plain function calls. If the transport spike goes badly, all of this is still green and still correct.
2. **Task 5 is an explicit spike** with the outcome recorded in the test file: build `apiStream` with the `fetchImpl` seam, then *empirically determine* whether MSW streams incrementally. Whatever the answer, the "first frame observed **before** the stream closes" assertion **ships**. If MSW buffers, that assertion lives only in the seam-based test and the MSW tests keep the auth/status-code coverage. **The seam is not deleted even if MSW works** (spec, Testing 12).
3. **Tasks 7-9 build the hook on a transport already proven to stream**, and use the seam (`fakeSseServer`) for every frame-delivery assertion, so hook behaviour is never hostage to MSW's streaming semantics. MSW still serves the `/logs` backfill pages, which are ordinary JSON.

The second load-bearing ordering decision is inside Task 7: **subscribe first, then page.** That is one line's worth of ordering (`README.md:1334-1344`) whose inversion leaves a small, intermittent hole. Task 7 has a mandatory RED proof: swapping the two statements must fail the test.

---

## Non-vacuity doctrine (read before writing any test)

Two standing project lessons, both paid for in this batch:

1. **A plan's test bodies are guesses, not verified guards.** Five plan-supplied tests in the immediately preceding iteration were vacuous or broken. Every test below has an explicit "prove it RED" step naming the exact mutation and the exact failure to observe. **If a mutation does not turn the test red, the test is wrong - fix the test before continuing, do not proceed.**
2. **Every absence assertion needs a positive control on the same code path.** "No duplicate lines", "no marker", "no reconnect after the cap", "connection closed on unmount", "nothing logged to console" all fail open: if the probe is broken they pass silently. Each such assertion below is paired with a positive assertion driven through the same call.

**The five tests most likely to be vacuous here** - treat their RED proofs as mandatory:

| Test | Task | Why it can pass for the wrong reason |
|---|---|---|
| Subscribe-before-backfill ordering | 7 | "The stream opened first" is trivially true if the stream request is the only request the test makes. The RED proof (swap the two statements) is the only thing that makes it a guard. |
| `seq <= maxSeq` dedupe | 2 | A test feeding only distinct seqs passes with the dedupe deleted. Must feed one below and one above `maxSeq` in the same call. |
| Line reassembly across entry boundaries | 3 | Asserting "the text appears" passes when it is split across two rows. Must assert the exact **row count**. |
| Bounded retry cap | 8 | "It retried" passes for an unbounded loop. Must assert the attempt count stops growing after 5, over a long timer advance. |
| Teardown on unmount / task switch | 9 | "No frame arrived after unmount" passes if the transport was broken all along. Must assert exact open counts, exact abort counts, and a positive-control frame before the teardown. |

Incremental streaming (Task 5, `delivers the first frame before the stream closes`) is a sixth: it is the one the spec forbids weakening.

---

## Scope guard: do NOT build the spec's proposed-not-filed follow-ups

The spec's Omissions table proposes six follow-ups it did **not** file. The conductor files them. If you find yourself writing any of these, stop:

- **Descending / `?before_seq=` log paging.** Backend change. The oldest-2000-lines corner (Decision 7) is mitigated with a notice, not fixed.
- **Log row virtualization.** `MAX_LINES = 2000` is what makes deferring it safe. **Do not add any dependency to `web/package.json` in this plan.**
- **`↧ Download` / copy-to-clipboard.** The hi-fi shows one (`hifi3-holo-pages.jsx:2732`); it is deliberately omitted.
- **ANSI colour rendering.** Sequences are *stripped*, not parsed into spans.
- **A task-list ordering tiebreaker / stable ordinal.** Backend change. This plan routes around it with UUID routes.
- **In-log search / filter / stderr-only toggle.**

Also out of scope: `step_index`/`step_total` display (the backend exposes neither field - `internal/api/tasks.go:56-61`), live status via `?job_id=` on the same connection, `Last-Event-ID` resume, **client-side gap detection on `seq`** (actively harmful - never add it), pausing on tab hide, and any ownership/authorization change on `/v1/events`.

**Nothing outside `web/src/` and `docs/` changes.**

---

## File Structure

**New files**

| File | Responsibility | Task |
|---|---|---|
| `web/src/lib/sse.ts` | Pure incremental SSE frame parser. No network, no auth, no React. | 1 |
| `web/src/lib/sse.test.ts` | Chunk-boundary matrix, multi-line `data:`, CRLF, comments, unknown event types. | 1 |
| `web/src/jobs/logBuffer.ts` | Pure log state: dedupe by `seq`, line reassembly, `\r` collapse, ANSI strip, `MAX_LINES` cap, drop marker, `shouldFollow`. | 2, 3, 4 |
| `web/src/jobs/logBuffer.test.ts` | All of the above, each with a RED proof. | 2, 3, 4 |
| `web/src/test/sseStream.ts` | Test harness: `fakeSseServer()` (an injectable `fetchImpl` handing out controllable streams, recording opens and aborts) and `openSseResponse()` (an MSW body that stays open until aborted). | 5 |
| `web/src/lib/api.stream.test.ts` | `apiStream`: auth header, `/v1` prefix, 401 notifier, 404 -> `ApiError`, abort, **incremental delivery**. | 5 |
| `web/src/jobs/api.test.ts` | `getTaskLogs` query params and `streamTaskLog` frame routing. | 6 |
| `web/src/jobs/useTaskLogStream.ts` | The one stateful hook: subscribe -> backfill -> replay -> recover. Owns all timers, refs and bounds. | 7, 8, 9 |
| `web/src/jobs/useTaskLogStream.test.tsx` | Ordering, dedupe, paging, cap, drop, bounded retry, reset rule, terminal task, leak counts, coalescing. | 7, 8, 9 |
| `web/src/jobs/LogView.tsx` | Shared presentational log body: status strip, rows, notices, drop marker, follow-tail pill. | 10 |
| `web/src/jobs/LogView.test.tsx` | Rows, states, notices, badges, follow-tail decision, no-HTML rendering. | 10 |
| `web/src/jobs/TaskLogPage.tsx` | Full-screen view at `/jobs/:id/tasks/:taskId`. | 13 |
| `web/src/jobs/TaskLogPage.test.tsx` | Route resolution, header from `useJob`, task-not-in-job state. | 13 |
| `web/src/jobs/logSecrecy.test.tsx` | No `console` method ever receives log content, across mount-stream-drop-unmount. | 14 |

**Modified files**

| File | Change | Task |
|---|---|---|
| `web/src/lib/api.ts:1-58` | Add `apiStream(path, {signal, onEvent, onOpen, fetchImpl})` below `apiFetch`. Nothing existing changes. | 5 |
| `web/src/jobs/api.ts:103-127` | Add `BACKFILL_PAGE_SIZE`, `TaskLogEvent`; widen `getTaskLogs(taskId, sinceSeq = 0, limit = BACKFILL_PAGE_SIZE)`; add `streamTaskLog()`. | 6 |
| `web/src/jobs/taskStatus.ts:1-26` | Add `isTerminalTask(status)`. | 6 |
| `web/src/jobs/LogTab.tsx` (whole file, 50 lines) | Becomes a thin wrapper: `LogView` + a "Full screen" link. Props change from `items/isLoading/isError/onRetry` to `jobId/taskId/stream`. | 11 |
| `web/src/jobs/LogTab.test.tsx` (whole file) | Rewritten for the new props. The `never a LIVE badge` case at `:29-37` is **replaced** by its inverse. | 11 |
| `web/src/jobs/JobDetailPage.tsx:14,46,179-185` | Swap `useTaskLogs` for `useTaskLogStream`; compute `live` from the selected task's status; pass the result to `LogTab`. | 12 |
| `web/src/jobs/JobDetailPage.test.tsx:93-126,192-209` | Add a `/v1/events` handler; replace the static-marker case with a live one; assert no `/v1/events` request while the Spec tab is active. | 12 |
| `web/src/app/router.tsx:6,26` | Add `<Route path="/jobs/:id/tasks/:taskId" element={<TaskLogPage />} />` inside `ProtectedRoute`. | 13 |

**Deleted files**

| File | Why | Task |
|---|---|---|
| `web/src/jobs/useTaskLogs.ts` | Superseded. Acceptance criterion 12: no `useQuery` holds log lines. | 12 |
| `web/src/jobs/useTaskLogs.test.tsx` | Tests a deleted hook. | 12 |

**Reused, not rebuilt** (read these before writing anything):

- `web/src/lib/api.ts:3-12` `ApiError`, `:14-21` `onUnauthorized` + the module-private `unauthorizedListeners` set, `:28-58` `apiFetch` - the shape `apiStream` mirrors: `/v1` prefix at `:37`, bearer at `:31-32`, 401 notify at `:43-45`, envelope parse at `:47-53`.
- `web/src/lib/token.ts:3-5` `getToken` (localStorage-backed; the SPA's only credential).
- `web/src/auth/AuthProvider.tsx:39-49` - the `onUnauthorized` subscriber that performs logout-and-redirect. This is why a streaming 401 must go through `apiStream`, not a bare `fetch`.
- `web/src/jobs/api.ts:103-114` `LogEntry`/`TaskLogPage`, `:66` `TaskStatus`, `:70-85` `TaskDetail`, `:117-119` `getJob`, `:122-127` today's `getTaskLogs`.
- `web/src/jobs/useJob.ts:7-14` - the 3000 ms poll that supplies the task list **and the terminal signal**. Untouched.
- `web/src/jobs/taskStatus.ts:11-26` `taskStatusColor` (task vocabulary; **not** `status.ts`, which is the job vocabulary).
- `web/src/jobs/TasksTable.tsx:13-21,43-51` - rows are selection controls (`aria-selected` + `onSelect`), not data owners. Never add a hook call there.
- `web/src/jobs/LogTab.tsx:21-47` - today's loading / error-with-retry / empty / row markup, ported into `LogView`.
- Holo primitives: barrel `web/src/components/holo/index.ts:3-10`; `GlassPanel.tsx:19` (`as`/`className` passthrough), `PillButton.tsx:19` (`variant` of `primary|ghost|danger|muted`), `Chip.tsx:23` (`tone` of `accent|muted|warn`), `Eyebrow.tsx:8`. `web/src/components/Button.tsx` is the full-width form button used by the error state.
- Test harness: `web/src/test/msw.ts:4` `server`, re-exported by `web/src/test/setup-helpers.ts:1` - **import `server` from `../test/setup-helpers`**, the house convention. `web/src/test/setup.ts:5` sets `onUnhandledRequest: 'error'`. `web/src/test/renderWithQuery.tsx:7` `renderWithQuery` (fresh `QueryClient`, `retry: false`). `web/vite.config.ts:13-18` - jsdom env, globals on.
- Test patterns to copy: `renderHook` + a local `wrapper` making a fresh `QueryClient` - `web/src/jobs/useJob.test.tsx:9-12`; a full page mount with `MemoryRouter` + `AuthProvider` + `setToken` - `web/src/jobs/JobDetailPage.test.tsx:38-55`; a request-count non-vacuity assertion - `web/src/jobs/queryKeyDecoupling.test.tsx:26-82`; a stdout/stderr class assertion - `web/src/jobs/LogTab.test.tsx:11-16`.

**Backend contract** (read-only reference, do **not** edit):

- `internal/api/events.go:30-48` - `?task_id=` is validated: 400 on a malformed UUID, 404 on an unknown task, both written as JSON **before** the headers switch to `text/event-stream`. `:59-70` - Subscribe-then-Flush, so a 200 means the subscription is already live. `:78-86` - `event: dropped\ndata: {"reason":"slow_consumer"}` as the final frame. `:90-92` - framing is `event: <type>\ndata: <json>\n\n`, flushed per frame.
- `internal/api/tasks.go:56-61` `logEntry` (`seq`/`stream`/`content`/`created_at`), `:81-89` `?limit=` 1..200 default 50, `:91-99` `?since_seq=`, `:128-130` `next_seq` is 0 when drained.
- `README.md:1322` the `task_log` payload key set. `:1334-1344` the subscribe-then-backfill contract. `:1357-1360` **`seq` is ordered but not contiguous - a gap is not a drop signal.** `:1362-1366` the validation asymmetry.
- `internal/agent/runner.go:285-309` `chunkWriter.Write` - why an entry is not a line.
- `design_handoff_relay_holo/hifi3-holo-pages.jsx:2716-2745` - the full-screen chrome (breadcrumb, ids, status pill, endpoint caption, `LIVE` badge, follow-tail). `:2732` is the `↧ Download` button that is **omitted**. `:2754-2759` is the four-column row layout **replaced by two columns** (no level and no source exist on the wire).

---

## Conventions for every task

- **All frontend commands run from `D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web`.** Use this worktree path, not `D:/dev/relay`.
- Single test file: `npx vitest run src/<path>`. Single test by name: `npx vitest run src/lib/sse.test.ts -t 'parses a frame split'`. Full suite: `npm test`. Type check + build: `npm run build` (= `tsc -b && vite build`).
- TDD, every task: write the failing test, run it and watch it fail, implement, run it and watch it pass, **prove the test non-vacuous where the task says so**, commit.
- MSW is `onUnhandledRequest: 'error'` (`web/src/test/setup.ts:5`). Every endpoint a test's component touches needs a handler - including `/v1/users/me` whenever `AuthProvider` is mounted, and `/v1/events` whenever the log hook can become live.
- **Never `console.log`/`debug`/`error`/`warn` a frame, a payload, or a log line** anywhere in this feature. Error paths surface a status code and a message through React state and log nothing. Log content is raw subprocess output and can contain secrets (spec, Security).
- **Never `dangerouslySetInnerHTML`.** Content is always React text children.
- House rule: never use em dashes or en dashes. Use hyphens.
- Never reformat or "tidy" code you were not asked to change.
- Commit at the end of every task. Use bash heredocs for multi-line commit messages (the Bash tool is Git Bash), not PowerShell here-strings.
- **`web/dist` is tracked but stale.** Do not run `npm run build` until Task 14, and when you do, `git checkout -- web/dist/` afterwards. Never commit `web/dist`.

---

## Task 1: `web/src/lib/sse.ts` - pure incremental SSE frame parser

The parser is the only place that knows SSE framing. It must handle a frame split across two reader chunks, CRLF, multi-line `data:` and comment lines, because those are properties of the transport, not of relay's server (`internal/api/events.go:90-92` happens to write one clean frame per flush, but a `ReadableStream` chunk boundary can land anywhere).

**Files:**
- Create: `web/src/lib/sse.ts`
- Test: `web/src/lib/sse.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/lib/sse.test.ts`:

```ts
import { expect, test } from 'vitest'
import { createSseParser, type SseFrame } from './sse'

const TWO_FRAMES =
  'event: task_log\ndata: {"seq":1,"content":"a"}\n\n' +
  'event: dropped\ndata: {"reason":"slow_consumer"}\n\n'

const EXPECTED: SseFrame[] = [
  { event: 'task_log', data: '{"seq":1,"content":"a"}' },
  { event: 'dropped', data: '{"reason":"slow_consumer"}' },
]

test('parses whole frames from a single chunk', () => {
  const p = createSseParser()
  expect(p.push(TWO_FRAMES)).toEqual(EXPECTED)
})

// Non-vacuity: split the SAME payload at EVERY byte offset and demand the same
// result from all of them. A parser that only handles whole frames passes the
// test above and fails at most of the 90-odd offsets here.
test('parses the same two frames no matter where the chunk boundary falls', () => {
  for (let i = 1; i < TWO_FRAMES.length; i++) {
    const p = createSseParser()
    const frames = [...p.push(TWO_FRAMES.slice(0, i)), ...p.push(TWO_FRAMES.slice(i))]
    expect(frames, `split at offset ${i}`).toEqual(EXPECTED)
  }
})

test('emits nothing until a frame is terminated by a blank line', () => {
  const p = createSseParser()
  expect(p.push('event: task_log\ndata: {"seq":1}\n')).toEqual([])
  expect(p.push('\n')).toEqual([{ event: 'task_log', data: '{"seq":1}' }])
})

test('joins multi-line data with a newline', () => {
  const p = createSseParser()
  expect(p.push('event: x\ndata: one\ndata: two\n\n')).toEqual([{ event: 'x', data: 'one\ntwo' }])
})

test('parses CRLF line endings, including a CRLF split across chunks', () => {
  const p = createSseParser()
  expect(p.push('event: x\r\ndata: hi\r')).toEqual([])
  expect(p.push('\n\r\n')).toEqual([{ event: 'x', data: 'hi' }])
})

test('ignores comment (keepalive) lines without emitting a frame', () => {
  const p = createSseParser()
  expect(p.push(':keepalive\n\n')).toEqual([])
  expect(p.push('event: x\ndata: 1\n\n')).toEqual([{ event: 'x', data: '1' }])
})

test('surfaces an unknown event type rather than dropping it', () => {
  const p = createSseParser()
  expect(p.push('event: brand_new\ndata: {}\n\n')).toEqual([{ event: 'brand_new', data: '{}' }])
})

test('defaults a frame with no event field to "message"', () => {
  const p = createSseParser()
  expect(p.push('data: bare\n\n')).toEqual([{ event: 'message', data: 'bare' }])
})

test('accepts data with no space after the colon', () => {
  const p = createSseParser()
  expect(p.push('event:x\ndata:{"a":1}\n\n')).toEqual([{ event: 'x', data: '{"a":1}' }])
})

test('keeps a colon inside a data value', () => {
  const p = createSseParser()
  expect(p.push('data: {"url":"http://x/y"}\n\n')).toEqual([
    { event: 'message', data: '{"url":"http://x/y"}' },
  ])
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/lib/sse.test.ts`
Expected: FAIL - `Failed to resolve import "./sse"`. A resolution failure is the correct RED: the module does not exist.

- [ ] **Step 3: Write the implementation**

Create `web/src/lib/sse.ts`:

```ts
// Pure, incremental Server-Sent Events frame parser. No network, no auth, no
// React: it is fed strings and returns frames. Auth and the 401 notifier live in
// api.ts (apiStream) so the bearer token stays in exactly one place.
//
// relay's server writes one clean `event: <type>\ndata: <json>\n\n` per flush
// (internal/api/events.go:90-92) and json.Marshal escapes newlines, so in
// practice every frame is two lines. The parser still handles a frame split
// across two reader chunks, CRLF, multi-line `data:` and comment lines, because
// a ReadableStream chunk boundary is a property of the transport, not of the
// server.

export interface SseFrame {
  /** The `event:` field value; 'message' when the frame carried none (SSE default). */
  event: string
  /** The `data:` field value; multiple data lines are joined with '\n'. */
  data: string
}

export interface SseParser {
  /** Feeds a decoded chunk and returns every frame completed by it (possibly none). */
  push(chunk: string): SseFrame[]
}

export function createSseParser(): SseParser {
  // Everything after the last '\n' seen so far. A lone trailing '\r' stays here
  // too, so a CRLF split across two chunks is joined rather than mis-parsed.
  let buf = ''
  let eventType = ''
  let dataLines: string[] = []

  return {
    push(chunk: string): SseFrame[] {
      const frames: SseFrame[] = []
      buf += chunk

      let nl = buf.indexOf('\n')
      while (nl !== -1) {
        let line = buf.slice(0, nl)
        buf = buf.slice(nl + 1)
        if (line.endsWith('\r')) line = line.slice(0, -1)

        if (line === '') {
          // Blank line = end of frame. A blank line with nothing pending (which
          // is what a `:keepalive` comment leaves behind) emits nothing.
          if (dataLines.length > 0 || eventType !== '') {
            frames.push({ event: eventType || 'message', data: dataLines.join('\n') })
          }
          eventType = ''
          dataLines = []
        } else if (!line.startsWith(':')) {
          const colon = line.indexOf(':')
          const field = colon === -1 ? line : line.slice(0, colon)
          let value = colon === -1 ? '' : line.slice(colon + 1)
          if (value.startsWith(' ')) value = value.slice(1)
          if (field === 'event') eventType = value
          else if (field === 'data') dataLines.push(value)
          // `id` and `retry` are recognised and ignored: relay sends neither and
          // does not honour Last-Event-ID (README.md:1349-1350).
        }

        nl = buf.indexOf('\n')
      }

      return frames
    },
  }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/lib/sse.test.ts`
Expected: PASS, 10 tests.

- [ ] **Step 5: Prove the chunk-boundary test is not vacuous**

Temporarily replace the body of `push` with a whole-payload-only implementation:

```ts
    push(chunk: string): SseFrame[] {
      return chunk
        .split('\n\n')
        .filter((f) => f.trim() !== '')
        .map((f) => {
          const lines = f.split('\n')
          return {
            event: lines[0].replace(/^event:\s?/, ''),
            data: lines.slice(1).map((l) => l.replace(/^data:\s?/, '')).join('\n'),
          }
        })
    },
```

Run: `npx vitest run src/lib/sse.test.ts`
Expected: `parses whole frames from a single chunk` PASSES while `parses the same two frames no matter where the chunk boundary falls` FAILS with a `split at offset N` message. That contrast is the point: the naive parser is exactly the mistake the offset matrix exists to catch. If the offset test also passes, it is not exercising the exposure - fix it before continuing.

Restore the Step 3 implementation and re-run: PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/lib/sse.ts web/src/lib/sse.test.ts
git commit -m "feat(web): pure incremental SSE frame parser"
```

---

## Task 2: `logBuffer.ts` - state shape and `seq` dedupe

The dedupe is step 3 of the README join (`README.md:1341-1342`) and the single place a duplicate can slip through, because both the buffered replay and the live path go through it. This task also locks the negative requirement: **a `seq` gap is not a drop signal** (`README.md:1357-1360`).

**Files:**
- Create: `web/src/jobs/logBuffer.ts`
- Test: `web/src/jobs/logBuffer.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/logBuffer.test.ts`:

```ts
import { expect, test } from 'vitest'
import { appendEntries, createLogState, type LogChunk } from './logBuffer'

function chunk(seq: number, content: string, stream: 'stdout' | 'stderr' = 'stdout'): LogChunk {
  return { seq, stream, content, created_at: '2026-08-09T14:36:25.000Z' }
}

test('a fresh state is empty with maxSeq 0', () => {
  const s = createLogState()
  expect(s.lines).toEqual([])
  expect(s.maxSeq).toBe(0)
  expect(s.dropped).toBe(false)
  expect(s.evicted).toBe(false)
})

// Paired positive control on the same call path: one entry below maxSeq and one
// above, in the SAME appendEntries call. Feeding only distinct seqs would pass
// with the dedupe deleted.
test('discards entries at or below maxSeq and accepts those above, advancing maxSeq', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(10, 'first\n')])
  expect(s.maxSeq).toBe(10)

  s = appendEntries(s, [chunk(10, 'duplicate\n'), chunk(9, 'older\n'), chunk(11, 'newer\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['first', 'newer'])
  expect(s.maxSeq).toBe(11)
})

test('returns the identical state object when every entry is a duplicate', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(5, 'a\n')])
  const before = s
  s = appendEntries(s, [chunk(5, 'a\n'), chunk(1, 'b\n')])
  // Reference equality, not deep equality: the hook relies on this to skip a
  // render when a replayed frame turns out to be a duplicate.
  expect(s).toBe(before)
})

// THE test that protects against the README's old, wrong contract. seq comes from
// a table-wide BIGSERIAL, so gaps are normal on a busy farm
// (README.md:1357-1360); any gap detection would re-backfill on nearly every
// frame.
test('non-contiguous seq is NOT a drop signal', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(10, 'a\n'), chunk(40, 'b\n'), chunk(41, 'c\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['a', 'b', 'c'])
  expect(s.dropped).toBe(false)
  expect(s.lines.every((l) => l.kind === 'line')).toBe(true)
  expect(s.maxSeq).toBe(41)
})

test('assigns a unique increasing render key to every row', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'a\nb\n'), chunk(2, 'c\n')])
  const keys = s.lines.map((l) => l.key)
  // Task 3 tightens this to an exact 3 once entries are reassembled into lines.
  expect(new Set(keys).size).toBe(s.lines.length)
  expect(keys).toEqual([...keys].sort((x, y) => x - y))
})

test('normalises an unexpected stream value to stdout', () => {
  let s = createLogState()
  s = appendEntries(s, [{ seq: 1, stream: 'weird', content: 'a\n', created_at: '' }])
  expect(s.lines[0].stream).toBe('stdout')
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/jobs/logBuffer.test.ts`
Expected: FAIL - `Failed to resolve import "./logBuffer"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/jobs/logBuffer.ts`:

```ts
// Pure log-state logic for the live task-log view. No React, no network, no
// timers - which is why the interesting behaviour of this feature (dedupe, line
// reassembly, the memory cap) is testable with plain function calls.
//
// Log state deliberately does NOT live in the TanStack cache: a live append-only
// stream has no fetch that resolves, no meaningful staleTime and no meaningful
// invalidate (spec Decision 3).

/** Retained line cap, drop-oldest. Postgres already holds the whole log. */
export const MAX_LINES = 2000

/** Pixels off the bottom at which follow-tail switches off. */
export const FOLLOW_EPSILON = 24

/** The in-stream marker inserted when lines may have been missed. */
export const DROP_MARKER_TEXT = 'lines may be missing here'

/**
 * The minimal shape appendEntries needs. Structurally satisfied by BOTH the
 * polling endpoint's LogEntry (web/src/jobs/api.ts:103-108) and the SSE task_log
 * payload (TaskLogEvent). That field-for-field symmetry is a backend guarantee
 * (README.md:1330-1332) and is why one client type covers both surfaces.
 */
export interface LogChunk {
  seq: number
  stream: string
  content: string
  created_at: string
}

export type LogStream = 'stdout' | 'stderr'

export interface LogRow {
  /** Stable React key. Positive for retained rows; negative for provisional partials. */
  key: number
  kind: 'line' | 'marker' | 'partial'
  stream: LogStream
  text: string
  /** created_at of the entry that terminated this line. '' on marker rows. */
  time: string
}

interface PendingPartial {
  text: string
  time: string
}

export interface LogState {
  lines: LogRow[]
  /** Highest seq accepted. The dedupe key of README.md:1341-1342. */
  maxSeq: number
  /** One in-progress trailing fragment per stream, because an entry is not a line. */
  partials: Record<LogStream, PendingPartial | null>
  nextKey: number
  /** The MAX_LINES cap has evicted at least one line. */
  evicted: boolean
  /** A `dropped` frame or an unexpected close happened; the view is no longer provably complete. */
  dropped: boolean
}

export function createLogState(): LogState {
  return {
    lines: [],
    maxSeq: 0,
    partials: { stdout: null, stderr: null },
    nextKey: 1,
    evicted: false,
    dropped: false,
  }
}

function normalizeStream(s: string): LogStream {
  return s === 'stderr' ? 'stderr' : 'stdout'
}

/**
 * Appends entries, discarding any whose seq is at or below maxSeq. Returns the
 * SAME object when nothing was accepted, so the caller can skip a render.
 *
 * There is deliberately NO gap detection: seq comes from a table-wide BIGSERIAL
 * shared by every task, so a gap is normal and acting on one would re-backfill on
 * nearly every frame (README.md:1357-1360). The only drop signals are the
 * `dropped` frame and an unexpected stream close.
 */
export function appendEntries(state: LogState, entries: LogChunk[]): LogState {
  let lines = state.lines
  let maxSeq = state.maxSeq
  let nextKey = state.nextKey
  let changed = false

  for (const e of entries) {
    if (e.seq <= maxSeq) continue
    maxSeq = e.seq
    if (!changed) {
      lines = lines.slice()
      changed = true
    }
    lines.push({
      key: nextKey++,
      kind: 'line',
      stream: normalizeStream(e.stream),
      text: e.content,
      time: e.created_at,
    })
  }

  if (!changed) return state
  return { ...state, lines, maxSeq, nextKey }
}
```

This stores one row per entry, which is today's behaviour (`LogTab.tsx:41-47`). **Task 3 replaces that with real line reassembly**; keeping them separate is what makes Task 3's row-count assertions provably RED.

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/jobs/logBuffer.test.ts`
Expected: PASS, 6 tests.

- [ ] **Step 5: Prove the dedupe and gap tests are not vacuous**

Two temporary mutations. After each, run the file, confirm the named test **fails**, then revert before the next.

1. **Delete the dedupe.** Remove `if (e.seq <= maxSeq) continue`.
   Expected: `discards entries at or below maxSeq...` FAILS with `['first','duplicate','older','newer']`, and `returns the identical state object...` FAILS on the reference-equality assertion. If either still passes, the test is not feeding a duplicate through the same call - fix it.

2. **Add gap detection** (the harmful implementation the spec forbids). Inside the loop, before `maxSeq = e.seq`, insert `if (maxSeq !== 0 && e.seq !== maxSeq + 1) state = { ...state, dropped: true }`.
   Expected: `non-contiguous seq is NOT a drop signal` FAILS on `expect(s.dropped).toBe(false)`. This proves the test would catch a future refactor that "helpfully" adds gap detection.

Restore and re-run: PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/logBuffer.ts web/src/jobs/logBuffer.test.ts
git commit -m "feat(web): log buffer state with seq dedupe and no gap detection"
```

---

## Task 3: `logBuffer.ts` - a log entry is not a line

`chunkWriter.Write` (`internal/agent/runner.go:285-309`) copies whatever `os/exec` hands it, so an entry is an arbitrary byte range: it can hold many lines, and one logical line can straddle two entries. Today's renderer shows one `<div>` per entry (`LogTab.tsx:41-47`), so a straddling line renders as two rows and multi-line content collapses under default HTML whitespace handling. A live view makes that constant.

**Files:**
- Modify: `web/src/jobs/logBuffer.ts` (replace `appendEntries`, add `visibleRows`, `finalizePartials`)
- Test: `web/src/jobs/logBuffer.test.ts` (tighten one case, append nine)

- [ ] **Step 1: Write the failing test**

First tighten the key test relaxed in Task 2 - change its two assertion lines to:

```ts
  expect(new Set(keys).size).toBe(3)
  expect(keys).toEqual([...keys].sort((x, y) => x - y))
```

and delete the `// Task 3 tightens this...` comment. Then append to `web/src/jobs/logBuffer.test.ts`, adding `finalizePartials` and `visibleRows` to the import from `./logBuffer`:

```ts
test('one entry containing three newlines yields three lines', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'one\ntwo\nthree\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['one', 'two', 'three'])
})

// The reassembly test. Asserting only "the text appears" would pass against an
// implementation that renders one row per ENTRY, which is today's behaviour and
// exactly the defect being fixed - so assert the exact row COUNT.
test('a line split across two entries renders as ONE line', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'abc'), chunk(2, 'def\n')])
  expect(s.lines).toHaveLength(1)
  expect(s.lines[0].text).toBe('abcdef')
})

test('a dangling partial is a provisional row, not a completed line', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'done\nprompt> ')])
  expect(s.lines.map((l) => l.text)).toEqual(['done'])

  const rows = visibleRows(s)
  expect(rows).toHaveLength(2)
  expect(rows[1]).toMatchObject({ kind: 'partial', text: 'prompt> ' })
  // Provisional rows use negative keys so they can never collide with the
  // positive keys of retained lines.
  expect(rows[1].key).toBeLessThan(0)
})

test('finalizePartials flushes a dangling partial into a real line', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'no trailing newline')])
  expect(s.lines).toHaveLength(0)

  s = finalizePartials(s)
  expect(s.lines.map((l) => l.text)).toEqual(['no trailing newline'])
  expect(visibleRows(s).every((r) => r.kind === 'line')).toBe(true)
  // Idempotent: a second call must not duplicate the line.
  expect(finalizePartials(s)).toBe(s)
})

test('a carriage-return run collapses to the segment after the final CR', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'frame 1/100\rframe 2/100\rframe 3/100\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['frame 3/100'])
})

test('ANSI SGR escape sequences are stripped', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, '\u001B[32mgreen\u001B[0m and \u001B[1;31mred\u001B[0m\n')])
  expect(s.lines[0].text).toBe('green and red')
  expect(s.lines[0].text).not.toContain('\u001B')
  expect(s.lines[0].text).not.toContain('[32m')
})

test('an ANSI erase-line sequence is stripped too', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, '\u001B[2Kprogress\n')])
  expect(s.lines[0].text).toBe('progress')
})

test('stdout and stderr partials do not corrupt each other', () => {
  let s = createLogState()
  s = appendEntries(s, [
    chunk(1, 'out-a', 'stdout'),
    chunk(2, 'err-a', 'stderr'),
    chunk(3, 'out-b\n', 'stdout'),
    chunk(4, 'err-b\n', 'stderr'),
  ])
  expect(s.lines.map((l) => [l.stream, l.text])).toEqual([
    ['stdout', 'out-aout-b'],
    ['stderr', 'err-aerr-b'],
  ])
})

test('a completed line carries the created_at of the entry that terminated it', () => {
  let s = createLogState()
  s = appendEntries(s, [
    { seq: 1, stream: 'stdout', content: 'half', created_at: '2026-08-09T00:00:01.000Z' },
    { seq: 2, stream: 'stdout', content: 'done\n', created_at: '2026-08-09T00:00:02.000Z' },
  ])
  expect(s.lines[0].time).toBe('2026-08-09T00:00:02.000Z')
})

test('visibleRows returns the lines array itself when there is no partial', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'a\n')])
  expect(visibleRows(s)).toBe(s.lines)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/logBuffer.test.ts`
Expected: FAIL - the import of `visibleRows`/`finalizePartials` fails to resolve as exports; once you have added stubs, `a line split across two entries renders as ONE line` fails with `expected length 1, received 2`.

- [ ] **Step 3: Write the implementation**

In `web/src/jobs/logBuffer.ts`, add below `normalizeStream`:

```ts
// CSI sequences (ESC [ ... final byte) plus OSC (ESC ] ... BEL or ST). Covers SGR
// colour codes, cursor moves and erase-line, which is what a progress bar emits.
// Rendering the colours is a separate proposed follow-up; leaving the raw bytes in
// would show `[32m` litter that reads as corruption.
const ANSI_RE = /\u001B(?:\[[0-?]*[ -/]*[@-~]|\][^\u0007\u001B]*(?:\u0007|\u001B\\))/g

function stripAnsi(s: string): string {
  return s.replace(ANSI_RE, '')
}

// Within one emitted line only the segment after the final carriage return is
// kept, so `\rframe 12/100` progress output renders as one updating line instead
// of a wall of concatenated garbage.
function collapseCR(s: string): string {
  const i = s.lastIndexOf('\r')
  return i === -1 ? s : s.slice(i + 1)
}

function capLines(lines: LogRow[]): { lines: LogRow[]; evicted: boolean } {
  if (lines.length <= MAX_LINES) return { lines, evicted: false }
  return { lines: lines.slice(lines.length - MAX_LINES), evicted: true }
}
```

Then replace `appendEntries` in full (extending its doc comment) and add the two new exports:

```ts
/**
 * Appends entries, discarding any whose seq is at or below maxSeq, then
 * reassembles their content into LINES. Returns the SAME object when nothing was
 * accepted, so the caller can skip a render.
 *
 * A log entry is NOT a line: chunkWriter.Write copies whatever os/exec hands it
 * (internal/agent/runner.go:285-309), so an entry can hold many lines and one
 * logical line can straddle two entries. One pending-partial buffer per stream
 * handles both cases; every '\n' emits a completed line in the order its
 * terminating newline arrived, which is what a terminal shows for merged output.
 *
 * Dedupe happens BEFORE reassembly, so replaying a buffered frame can never
 * duplicate a partial line.
 *
 * There is deliberately NO gap detection: seq comes from a table-wide BIGSERIAL
 * shared by every task, so a gap is normal and acting on one would re-backfill on
 * nearly every frame (README.md:1357-1360). The only drop signals are the
 * `dropped` frame and an unexpected stream close.
 */
export function appendEntries(state: LogState, entries: LogChunk[]): LogState {
  let lines = state.lines
  let partials = state.partials
  let maxSeq = state.maxSeq
  let nextKey = state.nextKey
  let changed = false

  for (const e of entries) {
    if (e.seq <= maxSeq) continue
    maxSeq = e.seq
    if (!changed) {
      lines = lines.slice()
      partials = { ...partials }
      changed = true
    }

    const stream = normalizeStream(e.stream)
    // Strip before splitting: an escape sequence never contains a newline.
    let buf = (partials[stream]?.text ?? '') + stripAnsi(e.content)

    let nl = buf.indexOf('\n')
    while (nl !== -1) {
      const raw = buf.slice(0, nl)
      buf = buf.slice(nl + 1)
      lines.push({ key: nextKey++, kind: 'line', stream, text: collapseCR(raw), time: e.created_at })
      nl = buf.indexOf('\n')
    }
    partials[stream] = buf === '' ? null : { text: buf, time: e.created_at }
  }

  if (!changed) return state
  const capped = capLines(lines)
  return {
    ...state,
    lines: capped.lines,
    partials,
    maxSeq,
    nextKey,
    evicted: state.evicted || capped.evicted,
  }
}

/**
 * Rows to render: the retained lines plus one provisional row per dangling
 * partial. A task that prints a prompt with no trailing newline must not look
 * silent. Provisional rows use fixed NEGATIVE keys so they can never collide with
 * the positive keys of retained lines and React keeps them stable across renders.
 */
export function visibleRows(state: LogState): LogRow[] {
  const { stdout, stderr } = state.partials
  if (stdout === null && stderr === null) return state.lines
  const rows = state.lines.slice()
  if (stdout !== null) {
    rows.push({ key: -1, kind: 'partial', stream: 'stdout', text: collapseCR(stdout.text), time: stdout.time })
  }
  if (stderr !== null) {
    rows.push({ key: -2, kind: 'partial', stream: 'stderr', text: collapseCR(stderr.text), time: stderr.time })
  }
  return rows
}

/**
 * Flushes any dangling partials into real lines. Called when the task reaches a
 * terminal status: there will be no further output, so a partial is final.
 * Returns the same object when there is nothing pending, so it is idempotent.
 */
export function finalizePartials(state: LogState): LogState {
  const streams: LogStream[] = ['stdout', 'stderr']
  const pending = streams.filter((s) => state.partials[s] !== null)
  if (pending.length === 0) return state

  let nextKey = state.nextKey
  const lines = state.lines.slice()
  for (const s of pending) {
    const p = state.partials[s]!
    lines.push({ key: nextKey++, kind: 'line', stream: s, text: collapseCR(p.text), time: p.time })
  }
  const capped = capLines(lines)
  return {
    ...state,
    lines: capped.lines,
    partials: { stdout: null, stderr: null },
    nextKey,
    evicted: state.evicted || capped.evicted,
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/logBuffer.test.ts`
Expected: PASS, 16 tests.

- [ ] **Step 5: Prove the reassembly tests are not vacuous**

Three temporary mutations. After each, run the file, confirm the named test **fails**, revert.

1. **One row per entry** (today's defect). Replace the `while (nl !== -1)` loop and the `partials[stream] = ...` line with a single `lines.push({ key: nextKey++, kind: 'line', stream, text: stripAnsi(e.content), time: e.created_at })`.
   Expected: `a line split across two entries renders as ONE line` FAILS with `expected length 1, received 2`, and `one entry containing three newlines yields three lines` FAILS with one row. If the split test still passes, it is asserting text presence instead of row count - fix it.

2. **Drop the CR collapse.** Change `collapseCR(raw)` to `raw`.
   Expected: `a carriage-return run collapses...` FAILS with `'frame 1/100\rframe 2/100\rframe 3/100'`.

3. **Drop the ANSI strip.** Change `stripAnsi(e.content)` to `e.content`.
   Expected: both ANSI tests FAIL, the first with a literal `[32m` in the text.

Restore and re-run: PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/logBuffer.ts web/src/jobs/logBuffer.test.ts
git commit -m "feat(web): reassemble log entries into lines, collapse CR, strip ANSI"
```

---

## Task 4: `logBuffer.ts` - line cap, drop marker, follow-tail decision

Three bounds that fail differently. The cap is what makes deferring virtualization safe.

**Files:**
- Modify: `web/src/jobs/logBuffer.ts` (add `markDropped`, `shouldFollow`)
- Test: `web/src/jobs/logBuffer.test.ts` (append)

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/logBuffer.test.ts`, adding `MAX_LINES`, `FOLLOW_EPSILON`, `DROP_MARKER_TEXT`, `markDropped`, `shouldFollow` to the import:

```ts
// Non-vacuity: assert WHICH lines were retained, not just how many. A cap that
// kept the OLDEST MAX_LINES would pass a length-only assertion.
test('the line cap retains the newest MAX_LINES and flags eviction', () => {
  let s = createLogState()
  const entries: LogChunk[] = []
  for (let i = 1; i <= MAX_LINES + 50; i++) entries.push(chunk(i, `line-${i}\n`))
  s = appendEntries(s, entries)

  expect(s.lines).toHaveLength(MAX_LINES)
  expect(s.evicted).toBe(true)
  expect(s.lines[0].text).toBe('line-51')
  expect(s.lines[s.lines.length - 1].text).toBe(`line-${MAX_LINES + 50}`)
})

test('exactly MAX_LINES does not set the eviction flag', () => {
  let s = createLogState()
  const entries: LogChunk[] = []
  for (let i = 1; i <= MAX_LINES; i++) entries.push(chunk(i, `line-${i}\n`))
  s = appendEntries(s, entries)
  expect(s.lines).toHaveLength(MAX_LINES)
  expect(s.evicted).toBe(false)
})

test('markDropped appends a marker row and sets the dropped flag', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'before\n')])
  s = markDropped(s)
  s = appendEntries(s, [chunk(2, 'after\n')])

  expect(s.dropped).toBe(true)
  expect(s.lines.map((l) => [l.kind, l.text])).toEqual([
    ['line', 'before'],
    ['marker', DROP_MARKER_TEXT],
    ['line', 'after'],
  ])
})

// The marker is permanent for the session: once lines have been missed the view
// is no longer provably complete, so silence would misrepresent an incomplete log
// as complete.
test('markDropped twice leaves two markers and stays dropped', () => {
  const s = markDropped(markDropped(createLogState()))
  expect(s.lines.filter((l) => l.kind === 'marker')).toHaveLength(2)
  expect(s.dropped).toBe(true)
})

test('shouldFollow is true at the bottom and just inside the epsilon', () => {
  expect(shouldFollow(1000, 2000, 1000)).toBe(true) // exactly at the bottom
  expect(shouldFollow(1000 - FOLLOW_EPSILON, 2000, 1000)).toBe(true) // exactly at epsilon
  expect(shouldFollow(1000 - (FOLLOW_EPSILON - 1), 2000, 1000)).toBe(true)
})

test('shouldFollow is false once scrolled further than the epsilon off the bottom', () => {
  expect(shouldFollow(1000 - (FOLLOW_EPSILON + 1), 2000, 1000)).toBe(false)
  expect(shouldFollow(0, 2000, 1000)).toBe(false)
})

test('shouldFollow is true for a container smaller than its viewport', () => {
  // jsdom reports 0/0/0 for an unlaid-out element; that must not read as
  // "scrolled away", or follow-tail would switch itself off on mount.
  expect(shouldFollow(0, 0, 0)).toBe(true)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/logBuffer.test.ts`
Expected: FAIL - `markDropped is not a function` / `shouldFollow is not a function`.

- [ ] **Step 3: Write the implementation**

Append to `web/src/jobs/logBuffer.ts`:

```ts
/**
 * Records that lines may have been missed: appends a permanent in-stream marker
 * row and sets the dropped flag. The marker stays for the session even after
 * recovery succeeds, because the view is no longer provably complete - silence
 * here would misrepresent an incomplete log as complete, which is the exact
 * failure today's STATIC/HISTORY label exists to avoid.
 */
export function markDropped(state: LogState): LogState {
  const capped = capLines([
    ...state.lines,
    { key: state.nextKey, kind: 'marker', stream: 'stdout', text: DROP_MARKER_TEXT, time: '' },
  ])
  return {
    ...state,
    lines: capped.lines,
    nextKey: state.nextKey + 1,
    evicted: state.evicted || capped.evicted,
    dropped: true,
  }
}

/**
 * Whether follow-tail should stay on given a scroll container's geometry. The
 * whole threshold decision is extracted as a pure function because the pixel
 * effect cannot be honestly asserted in jsdom (scrollTop/scrollHeight are 0
 * there, so a test asserting scrollTop === scrollHeight would be vacuously
 * green).
 */
export function shouldFollow(scrollTop: number, scrollHeight: number, clientHeight: number): boolean {
  return scrollHeight - scrollTop - clientHeight <= FOLLOW_EPSILON
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/logBuffer.test.ts`
Expected: PASS, 23 tests.

- [ ] **Step 5: Prove the cap and threshold tests are not vacuous**

1. In `capLines`, change the drop-oldest slice to drop-newest: `return { lines: lines.slice(0, MAX_LINES), evicted: true }`.
   Expected: `the line cap retains the newest MAX_LINES and flags eviction` FAILS on `expect(s.lines[0].text).toBe('line-51')` (it will be `line-1`). A length-only test would have passed. Revert.
2. In `shouldFollow`, change `<=` to `<`.
   Expected: `shouldFollow is true at the bottom and just inside the epsilon` FAILS on the exact-epsilon case, proving the boundary is asserted rather than approximated. Revert.

Run: `npx vitest run src/jobs/logBuffer.test.ts` - PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/logBuffer.ts web/src/jobs/logBuffer.test.ts
git commit -m "feat(web): bounded log buffer, drop marker, follow-tail threshold"
```

---

## Task 5: `apiStream` + the streaming test seam (THE SPIKE)

The riskiest task. Build the authenticated streaming transport in the **same file** as `apiFetch`, so the bearer token and the 401 notifier stay in one place, and build the `fetchImpl`-injected test harness that makes frame timing deterministic. Then **empirically determine** whether MSW streams incrementally, and record the answer in the test file.

**Files:**
- Modify: `web/src/lib/api.ts` (append `apiStream`; nothing existing changes)
- Create: `web/src/test/sseStream.ts`
- Test: `web/src/lib/api.stream.test.ts`

- [ ] **Step 1: Write the test harness**

Create `web/src/test/sseStream.ts`. This is production-quality test infrastructure, not scaffolding - Tasks 7-9 and 13-14 all depend on it.

```ts
// Transport test harness for the SSE client. It exists because MSW is an
// interception layer, not a socket: whether it delivers a ReadableStream body
// INCREMENTALLY under jsdom is not guaranteed, and a buffering layer would make
// an incremental-delivery assertion silently vacuous. fakeSseServer() is injected
// through apiStream's `fetchImpl` seam (the SPA analogue of internal/cli's
// saveConfigFn override convention), so frame timing is fully under test control.

export interface FakeSseConnection {
  url: string
  headers: Headers
  /** True once the caller's AbortSignal fired. The leak assertions read this. */
  aborted: boolean
  /** True once the consumer cancelled the body. */
  cancelled: boolean
  /** Pushes raw SSE text into the body. */
  send(text: string): void
  /** Pushes one well-formed frame: `event: <type>\ndata: <json>\n\n`. */
  emit(event: string, data: unknown): void
  /** Ends the body cleanly. relay's server never does this, which is why it is abnormal. */
  close(): void
  /** Errors the body, simulating network loss or a server restart. */
  fail(err?: unknown): void
}

export interface FakeSseServer {
  fetchImpl: typeof fetch
  /** Every connection ever opened, in order. Length = streams opened. */
  connections: FakeSseConnection[]
  /** Status for the NEXT connection. 200 streams; anything else is a JSON error body. */
  status: number
  /** Body for a non-200 response, matching writeError's {error} envelope. */
  errorBody: unknown
  latest(): FakeSseConnection
  abortedCount(): number
  /** Resolves once at least n connections exist. Microtask-based, so it works under fake timers. */
  waitForConnection(n?: number): Promise<FakeSseConnection>
}

export function fakeSseServer(): FakeSseServer {
  const connections: FakeSseConnection[] = []
  const enc = new TextEncoder()

  const server: FakeSseServer = {
    connections,
    status: 200,
    errorBody: { error: 'error' },
    fetchImpl: (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : String(input)
      const headers = new Headers(init?.headers)

      if (server.status !== 200) {
        // A non-ok response arrives as JSON BEFORE the headers switch to
        // text/event-stream, exactly as internal/api/events.go:34-43 writes it.
        return new Response(JSON.stringify(server.errorBody), {
          status: server.status,
          headers: { 'Content-Type': 'application/json' },
        })
      }

      let ctl!: ReadableStreamDefaultController<Uint8Array>
      const conn: FakeSseConnection = {
        url,
        headers,
        aborted: false,
        cancelled: false,
        send(text) {
          try {
            ctl.enqueue(enc.encode(text))
          } catch {
            /* already closed or errored */
          }
        },
        emit(event, data) {
          conn.send(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`)
        },
        close() {
          try {
            ctl.close()
          } catch {
            /* already closed */
          }
        },
        fail(err = new TypeError('network error')) {
          try {
            ctl.error(err)
          } catch {
            /* already closed */
          }
        },
      }
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          ctl = c
        },
        cancel() {
          conn.cancelled = true
        },
      })
      init?.signal?.addEventListener('abort', () => {
        conn.aborted = true
        conn.fail(new DOMException('The operation was aborted.', 'AbortError'))
      })
      connections.push(conn)
      return new Response(body, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
      })
    }) as unknown as typeof fetch,
    latest: () => connections[connections.length - 1],
    abortedCount: () => connections.filter((c) => c.aborted).length,
    async waitForConnection(n = 1) {
      for (let i = 0; i < 500 && connections.length < n; i++) await Promise.resolve()
      if (connections.length < n) {
        throw new Error(`expected ${n} connection(s), only ${connections.length} opened`)
      }
      return connections[n - 1]
    },
  }
  return server
}

/**
 * An MSW resolver body for an SSE endpoint that stays open until the request is
 * aborted. Used by COMPONENT tests that only need "a stream exists" - frame
 * delivery is asserted through the fetchImpl seam above, never through MSW.
 */
export function openSseResponse(): Response {
  const body = new ReadableStream<Uint8Array>({
    start() {
      /* deliberately never closed */
    },
  })
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

/** Lets the stream reader loop make progress. One macrotask tick, not a bare microtask drain. */
export function tick(): Promise<void> {
  return new Promise((r) => setTimeout(r, 0))
}
```

If `Response` or `ReadableStream` turns out to be undefined under the jsdom environment, do **not** weaken the tests: add `globalThis.Response`/`ReadableStream` from `node:stream/web` and `undici` at the top of `web/src/test/setup.ts` instead, and note it in a comment. MSW 2.7 already requires these globals, so this is unlikely.

- [ ] **Step 2: Write the failing test**

Create `web/src/lib/api.stream.test.ts`:

```ts
import { afterEach, expect, test, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setup-helpers'
import { fakeSseServer, tick } from '../test/sseStream'
import { ApiError, apiStream, onUnauthorized } from './api'
import { clearToken, setToken } from './token'
import type { SseFrame } from './sse'

afterEach(() => clearToken())

function frameOf(seq: number) {
  return { seq, stream: 'stdout', content: `line-${seq}\n`, created_at: '2026-08-09T00:00:00Z' }
}

test('sends the bearer token and the /v1 prefix', async () => {
  const fake = fakeSseServer()
  setToken('tok-123')
  const ac = new AbortController()
  const p = apiStream('/events?task_id=t1', {
    signal: ac.signal,
    onEvent: () => {},
    fetchImpl: fake.fetchImpl,
  })
  const conn = await fake.waitForConnection()
  expect(conn.url).toBe('/v1/events?task_id=t1')
  expect(conn.headers.get('Authorization')).toBe('Bearer tok-123')
  expect(conn.headers.get('Accept')).toBe('text/event-stream')
  ac.abort()
  await expect(p).rejects.toThrow()
})

test('a 401 fires the onUnauthorized listeners and does not retry', async () => {
  const fake = fakeSseServer()
  fake.status = 401
  fake.errorBody = { error: 'unauthorized' }
  const seen = vi.fn()
  const off = onUnauthorized(seen)
  await expect(
    apiStream('/events?task_id=t1', {
      signal: new AbortController().signal,
      onEvent: () => {},
      fetchImpl: fake.fetchImpl,
    }),
  ).rejects.toBeInstanceOf(ApiError)
  // Without this, a revoked token becomes a silently empty log instead of a
  // redirect to sign-in (AuthProvider.tsx:39-49 is the subscriber).
  expect(seen).toHaveBeenCalledTimes(1)
  // apiStream never retries: recovery policy belongs to the hook.
  expect(fake.connections).toHaveLength(1)
  off()
})

test('a 404 throws ApiError(404, "task not found") before any frame is delivered', async () => {
  const fake = fakeSseServer()
  fake.status = 404
  fake.errorBody = { error: 'task not found' }
  const frames: SseFrame[] = []
  await expect(
    apiStream('/events?task_id=nope', {
      signal: new AbortController().signal,
      onEvent: (f) => frames.push(f),
      fetchImpl: fake.fetchImpl,
    }),
  ).rejects.toMatchObject({ status: 404, code: 'task not found' })
  // A 404 must be distinguishable from an empty log.
  expect(frames).toHaveLength(0)
})

// THE assertion the spec forbids weakening. A buffering transport passes a naive
// "both frames arrived at the end" test and fails this one.
test('delivers the first frame BEFORE the stream closes', async () => {
  const fake = fakeSseServer()
  const frames: SseFrame[] = []
  let opened = false
  const p = apiStream('/events?task_id=t1', {
    signal: new AbortController().signal,
    onOpen: () => {
      opened = true
    },
    onEvent: (f) => frames.push(f),
    fetchImpl: fake.fetchImpl,
  })
  const conn = await fake.waitForConnection()
  await tick()
  // onOpen fires on the 200, which is when handleEvents has already Subscribe()d
  // and flushed (internal/api/events.go:59-70).
  expect(opened).toBe(true)

  conn.emit('task_log', frameOf(1))
  await tick()
  expect(frames).toHaveLength(1)
  expect(frames[0].event).toBe('task_log')
  expect(JSON.parse(frames[0].data).seq).toBe(1)

  conn.emit('task_log', frameOf(2))
  conn.close()
  await p
  expect(frames).toHaveLength(2)
})

test('abort stops delivery and the transport sees the abort', async () => {
  const fake = fakeSseServer()
  const frames: SseFrame[] = []
  const ac = new AbortController()
  const p = apiStream('/events?task_id=t1', {
    signal: ac.signal,
    onEvent: (f) => frames.push(f),
    fetchImpl: fake.fetchImpl,
  })
  const conn = await fake.waitForConnection()
  conn.emit('task_log', frameOf(1))
  await tick()
  expect(frames).toHaveLength(1) // positive control on the same path

  ac.abort()
  await expect(p).rejects.toThrow()
  // The real leak property: the signal reached the transport.
  expect(conn.aborted).toBe(true)

  conn.emit('task_log', frameOf(2))
  await tick()
  expect(frames).toHaveLength(1) // nothing after abort
})

// The default fetchImpl path, through MSW, so the seam cannot hide a broken
// default. These two do not depend on incremental delivery.
test('through the real global fetch (MSW): the bearer header is attached', async () => {
  setToken('tok-msw')
  let auth: string | null = null
  server.use(
    http.get('/v1/events', ({ request }) => {
      auth = request.headers.get('Authorization')
      return HttpResponse.json({ error: 'stop here' }, { status: 400 })
    }),
  )
  await expect(
    apiStream('/events?task_id=t1', { signal: new AbortController().signal, onEvent: () => {} }),
  ).rejects.toThrow()
  expect(auth).toBe('Bearer tok-msw')
})

test('through the real global fetch (MSW): a 404 envelope becomes ApiError', async () => {
  server.use(http.get('/v1/events', () => HttpResponse.json({ error: 'task not found' }, { status: 404 })))
  await expect(
    apiStream('/events?task_id=nope', { signal: new AbortController().signal, onEvent: () => {} }),
  ).rejects.toMatchObject({ status: 404, code: 'task not found' })
})
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `npx vitest run src/lib/api.stream.test.ts`
Expected: FAIL - `apiStream is not a function` (it is not exported from `./api` yet).

- [ ] **Step 4: Implement `apiStream`**

In `web/src/lib/api.ts`, add to the top import block:

```ts
import { createSseParser, type SseFrame } from './sse'
```

and append at the end of the file:

```ts
export interface StreamOptions {
  signal: AbortSignal
  onEvent: (frame: SseFrame) => void
  /** Called once the response is 200, i.e. once the subscription is live. */
  onOpen?: () => void
  /** Test seam; defaults to globalThis.fetch. See the note below - do not delete it. */
  fetchImpl?: typeof fetch
}

/**
 * Opens an authenticated Server-Sent Events stream, calling onEvent for every
 * frame. Resolves when the stream ends; rejects on a non-ok response, a transport
 * error, or an abort.
 *
 * It lives HERE, next to apiFetch, on purpose: the bearer token (token.ts:3-5) is
 * attached in exactly one place, and a streaming 401 fires the same
 * onUnauthorized notifier AuthProvider subscribes to (AuthProvider.tsx:39-49).
 * Otherwise a revoked token would turn into a silently empty log instead of a
 * redirect to sign-in. sse.ts holds framing only and knows nothing about auth.
 *
 * EventSource is deliberately not used: it cannot set an Authorization header,
 * and putting the token in a query parameter would leak the SPA's only credential
 * into proxy logs, browser history and Referer. Losing EventSource's automatic
 * reconnect is a feature - the retry here must be bounded, which EventSource
 * cannot do.
 *
 * `fetchImpl` is a test seam mirroring the project's package-var-override
 * convention (internal/cli's saveConfigFn and friends). It exists because MSW is
 * an interception layer, not a socket, so incremental delivery through it is not
 * guaranteed. DO NOT DELETE IT even if MSW happens to stream correctly.
 */
export async function apiStream(path: string, opts: StreamOptions): Promise<void> {
  const { signal, onEvent, onOpen, fetchImpl } = opts
  const doFetch = fetchImpl ?? globalThis.fetch

  const headers = new Headers({ Accept: 'text/event-stream' })
  const token = getToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)

  const res = await doFetch(`/v1${path}`, { headers, signal })

  if (res.status === 401) {
    unauthorizedListeners.forEach((fn) => fn())
  }

  // A non-ok response arrives as JSON BEFORE the headers switch to
  // text/event-stream (internal/api/events.go:34-43), so a 404 "task not found"
  // is distinguishable from an empty log. Same envelope handling as apiFetch.
  if (!res.ok) {
    const code = await res
      .json()
      .then((b) => (b as { error?: string }).error ?? 'error')
      .catch(() => 'error')
    throw new ApiError(res.status, code, `${res.status} ${code}`)
  }
  if (!res.body) {
    throw new ApiError(res.status, 'no stream body', `${res.status} no stream body`)
  }

  // A 200 means handleEvents has already Subscribe()d and flushed
  // (internal/api/events.go:59-70), so the subscription is live. That is what the
  // caller waits on before it starts backfilling - never a timer.
  onOpen?.()

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  const parser = createSseParser()
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) return
      // Never log a frame: content is raw subprocess output and can carry secrets
      // a job's own script echoed.
      for (const frame of parser.push(decoder.decode(value, { stream: true }))) onEvent(frame)
    }
  } finally {
    // cancel() closes the stream and releases the lock in one step, so an aborted
    // or abandoned stream never leaves a dangling reader.
    await reader.cancel().catch(() => {})
  }
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `npx vitest run src/lib/api.stream.test.ts`
Expected: PASS, 7 tests.

- [ ] **Step 6: THE SPIKE - determine empirically whether MSW streams incrementally**

Append this test to `web/src/lib/api.stream.test.ts` and run the file:

```ts
// EMPIRICAL SPIKE (see the plan, Task 5 Step 6). Does MSW 2.7 + undici under
// jsdom 29 deliver a ReadableStream body incrementally? The seam-based test above
// is the authoritative incremental assertion either way; this one only tells us
// whether the default path can also be observed frame-by-frame.
test('through the real global fetch (MSW): the first frame arrives before close', async () => {
  let ctl!: ReadableStreamDefaultController<Uint8Array>
  const enc = new TextEncoder()
  const body = new ReadableStream<Uint8Array>({ start: (c) => { ctl = c } })
  server.use(
    http.get('/v1/events', () =>
      new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }),
    ),
  )
  const frames: SseFrame[] = []
  const p = apiStream('/events?task_id=t1', {
    signal: new AbortController().signal,
    onEvent: (f) => frames.push(f),
  })
  await tick()
  ctl.enqueue(enc.encode(`event: task_log\ndata: ${JSON.stringify(frameOf(1))}\n\n`))
  await tick()
  expect(frames).toHaveLength(1) // observed while the stream is still open
  ctl.close()
  await p
  expect(frames).toHaveLength(1)
})
```

**Decide and record:**

- **If it PASSES:** keep it. Replace its leading comment's question with `// EMPIRICAL, verified 2026-08-09: MSW 2.7 + undici under jsdom 29 DOES deliver a ReadableStream body incrementally.` **Keep the `fetchImpl` seam and the seam-based test regardless** - the spec is explicit about this (Testing 12).
- **If it FAILS** (frames is empty, or both frames arrive only after `ctl.close()`): **delete this test** and replace it with a comment in the same place:
  ```ts
  // EMPIRICAL, determined 2026-08-09: MSW 2.7 + undici under jsdom 29 does NOT
  // deliver a ReadableStream body incrementally - it buffers until close. The
  // incremental assertion therefore lives only in the fetchImpl-seam test
  // 'delivers the first frame BEFORE the stream closes' above, which is why the
  // seam exists. Do not "fix" this by asserting only that both frames eventually
  // arrived: that assertion passes for a buffering transport and is exactly the
  // vacuity the spec forbids.
  ```
  Then use `fakeSseServer` (never MSW) for every frame-delivery assertion in Tasks 7-9.
- **Never** weaken the seam-based `delivers the first frame BEFORE the stream closes` assertion in either branch.

- [ ] **Step 7: Prove the transport tests are not vacuous**

Three temporary mutations. After each, run the file, confirm the named test **fails**, revert.

1. **Remove the 401 notify.** Delete the `if (res.status === 401) { ... }` block.
   Expected: `a 401 fires the onUnauthorized listeners and does not retry` FAILS on `expect(seen).toHaveBeenCalledTimes(1)`.
2. **Buffer the body.** Replace the read loop with a single `const text = await new Response(res.body).text(); for (const f of parser.push(text)) onEvent(f)`.
   Expected: `delivers the first frame BEFORE the stream closes` FAILS with `expected length 1, received 0` (the first frame is not seen until close). **If it still passes, the test is vacuous and must be fixed before continuing** - this is the single most important RED proof in the plan.
3. **Ignore the signal.** In `sseStream.ts`, comment out the `init?.signal?.addEventListener('abort', ...)` block.
   Expected: `abort stops delivery and the transport sees the abort` FAILS on `expect(conn.aborted).toBe(true)`.

Restore and re-run: PASS.

- [ ] **Step 8: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/lib/api.ts web/src/lib/api.stream.test.ts web/src/test/sseStream.ts
git commit -m "feat(web): apiStream authenticated SSE transport with a fetchImpl test seam"
```

---

## Task 6: typed task-log clients and the terminal-status helper

**Files:**
- Modify: `web/src/jobs/api.ts:103-127`
- Modify: `web/src/jobs/taskStatus.ts`
- Test: `web/src/jobs/api.test.ts` (create)

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/api.test.ts`:

```ts
import { expect, test, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setup-helpers'
import { fakeSseServer, tick } from '../test/sseStream'
import { BACKFILL_PAGE_SIZE, getTaskLogs, streamTaskLog, type TaskLogEvent } from './api'
import { isTerminalTask } from './taskStatus'

test('getTaskLogs sends limit=200 and omits since_seq on the first page', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  await getTaskLogs('t1')
  expect(params?.get('limit')).toBe(String(BACKFILL_PAGE_SIZE))
  expect(BACKFILL_PAGE_SIZE).toBe(200) // the server's documented maximum
  expect(params?.has('since_seq')).toBe(false)
})

test('getTaskLogs sends since_seq when paging forward', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, total: 7 })
    }),
  )
  const page = await getTaskLogs('t1', 41, 200)
  expect(params?.get('since_seq')).toBe('41')
  expect(page.total).toBe(7)
})

test('streamTaskLog routes task_log frames to onLine and dropped frames to onDropped', async () => {
  const fake = fakeSseServer()
  const lines: TaskLogEvent[] = []
  const dropped = vi.fn()
  const p = streamTaskLog('t1', {
    signal: new AbortController().signal,
    onLine: (e) => lines.push(e),
    onDropped: dropped,
    fetchImpl: fake.fetchImpl,
  })
  const conn = await fake.waitForConnection()
  expect(conn.url).toBe('/v1/events?task_id=t1')

  conn.emit('task_log', { task_id: 't1', job_id: 'j1', seq: 9, stream: 'stderr', content: 'boom\n', created_at: '2026-08-09T00:00:00Z' })
  // A status frame can never reach a ?task_id=-only subscription
  // (README.md:1312-1313), and an unknown type is additive - both must be ignored
  // without throwing.
  conn.emit('task', { id: 't1', status: 'running' })
  conn.emit('brand_new', { x: 1 })
  conn.send('event: task_log\ndata: {not json}\n\n')
  await tick()
  expect(lines).toHaveLength(1)
  expect(lines[0]).toMatchObject({ seq: 9, stream: 'stderr', content: 'boom\n' })
  expect(dropped).not.toHaveBeenCalled()

  conn.emit('dropped', { reason: 'slow_consumer' })
  await tick()
  expect(dropped).toHaveBeenCalledTimes(1)

  conn.close()
  await p
})

test('isTerminalTask covers exactly done, failed and timed_out', () => {
  expect(isTerminalTask('done')).toBe(true)
  expect(isTerminalTask('failed')).toBe(true)
  expect(isTerminalTask('timed_out')).toBe(true)
  expect(isTerminalTask('pending')).toBe(false)
  expect(isTerminalTask('dispatched')).toBe(false)
  expect(isTerminalTask('running')).toBe(false)
  expect(isTerminalTask(undefined)).toBe(false)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/jobs/api.test.ts`
Expected: FAIL - `BACKFILL_PAGE_SIZE`/`streamTaskLog` are not exported from `./api`, `isTerminalTask` is not exported from `./taskStatus`.

- [ ] **Step 3: Implement**

In `web/src/jobs/api.ts`, add `apiStream` to the existing import from `../lib/api`, then replace the `getTaskLogs` block at `:121-127` with:

```ts
/**
 * Backfill page size. The server caps ?limit= at 200 (internal/api/tasks.go:84),
 * and 200 is used so a full history costs the fewest requests.
 */
export const BACKFILL_PAGE_SIZE = 200

/**
 * The SSE task_log payload. seq/stream/content/created_at are field-identical to
 * LogEntry above, which is a backend guarantee (README.md:1330-1332), so one
 * client-side type covers both the live and the polled surface.
 */
export interface TaskLogEvent extends LogEntry {
  task_id: string
  job_id: string
}

/**
 * One page of a task's log history, forward-only from sinceSeq. next_seq is 0
 * when drained (internal/api/tasks.go:128-130). Always sends an explicit limit so
 * the caller is never silently truncated to the server default of 50.
 */
export function getTaskLogs(
  taskId: string,
  sinceSeq = 0,
  limit = BACKFILL_PAGE_SIZE,
): Promise<TaskLogPage> {
  const q = new URLSearchParams({ limit: String(limit) })
  if (sinceSeq > 0) q.set('since_seq', String(sinceSeq))
  return apiFetch<TaskLogPage>(`/tasks/${taskId}/logs?${q}`)
}

export interface TaskLogStreamOptions {
  signal: AbortSignal
  onLine: (entry: TaskLogEvent) => void
  onDropped: () => void
  onOpen?: () => void
  fetchImpl?: typeof fetch
}

/**
 * Subscribes to one task's live log lines. Resolves when the stream ENDS - which
 * is abnormal, because the server never ends a stream on its own
 * (README.md:1310-1313), so the caller treats a resolve as a failure for backoff
 * purposes rather than as an end of data. Rejects on a non-ok response, a
 * transport error, or an abort.
 *
 * Only ?task_id= is sent. Adding ?job_id= would put status frames on the same
 * 64-slot buffer, so a log burst could drop-close the connection including its
 * status frames (README.md:1352-1355); job/task status comes from useJob's poll
 * instead (spec Decision 2).
 */
export function streamTaskLog(taskId: string, opts: TaskLogStreamOptions): Promise<void> {
  return apiStream(`/events?task_id=${encodeURIComponent(taskId)}`, {
    signal: opts.signal,
    fetchImpl: opts.fetchImpl,
    onOpen: opts.onOpen,
    onEvent: (frame) => {
      if (frame.event === 'task_log') {
        try {
          opts.onLine(JSON.parse(frame.data) as TaskLogEvent)
        } catch {
          // A malformed frame is dropped silently. Never log frame.data: it is
          // raw subprocess output and can carry secrets.
        }
        return
      }
      if (frame.event === 'dropped') {
        opts.onDropped()
      }
      // Anything else is ignored: a ?task_id=-only subscription receives no status
      // frames (README.md:1312-1313), and unknown event types are additive.
    },
  })
}
```

Append to `web/src/jobs/taskStatus.ts`:

```ts
// The terminal task statuses. A ?task_id= subscription has no terminal signal of
// its own (README.md:1310-1313), so this is what useJob's 3 s poll turns into
// "stop tailing" - which is what makes one log-only connection sufficient.
const TERMINAL: TaskStatus[] = ['done', 'failed', 'timed_out']

export function isTerminalTask(status: TaskStatus | undefined): boolean {
  return status !== undefined && TERMINAL.includes(status)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/api.test.ts`
Expected: PASS, 4 tests.

Run: `npx vitest run src/jobs`
Expected: PASS. `useTaskLogs.test.tsx` still passes - the widened `getTaskLogs` signature is backward compatible, and its handlers ignore query params.

- [ ] **Step 5: Prove the frame-routing test is not vacuous**

Temporary mutation: in `streamTaskLog`, change `if (frame.event === 'task_log')` to `if (frame.event !== 'dropped')`.
Expected: `streamTaskLog routes task_log frames...` FAILS with `expected length 1, received 3` (the status frame, the unknown frame and the malformed frame all leak into `onLine`). This proves the negative assertion has teeth. Revert and re-run: PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/api.ts web/src/jobs/api.test.ts web/src/jobs/taskStatus.ts
git commit -m "feat(web): typed task-log page + SSE clients and isTerminalTask"
```

---


---

## Task 7: `useTaskLogStream` - subscribe, then backfill, then replay

The correctness core. `README.md:1334-1344` requires the subscription to be open **before** the first history page; reversing it leaves a hole between the last page and the first frame. That ordering, the forward paging pump, the page cap, and the buffered replay through the same dedupe all land here. Recovery is deliberately stubbed and Task 8 replaces it.

**Files:**
- Create: `web/src/jobs/useTaskLogStream.ts`
- Test: `web/src/jobs/useTaskLogStream.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/useTaskLogStream.test.tsx`. The hook uses no TanStack query, so `renderHook` needs **no** `QueryClientProvider` wrapper.

```tsx
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { fakeSseServer, tick } from '../test/sseStream'
import { MAX_BACKFILL_PAGES, useTaskLogStream } from './useTaskLogStream'

function entry(seq: number, content = `line-${seq}\n`) {
  return { seq, stream: 'stdout' as const, content, created_at: '2026-08-09T00:00:00Z' }
}
function logEvent(seq: number, content = `line-${seq}\n`) {
  return { task_id: 't1', job_id: 'j1', ...entry(seq, content) }
}

// The ordering guard. Both events must be recorded, so a run that made only one
// request cannot pass. Prove it RED by swapping the two statements in the hook.
test('subscribes to the stream BEFORE it requests the first history page', async () => {
  const fake = fakeSseServer()
  const order: string[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      order.push('logs')
      return HttpResponse.json({ items: [entry(1)], next_seq: 0, total: 1 })
    }),
  )
  const wrapped = ((input: RequestInfo | URL, init?: RequestInit) => {
    order.push('stream')
    return fake.fetchImpl(input, init)
  }) as typeof fetch

  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: wrapped }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(order).toEqual(['stream', 'logs'])
})

test('applies frames buffered during backfill, deduping any also present in a page', async () => {
  const fake = fakeSseServer()
  let release: () => void = () => {}
  const gate = new Promise<void>((r) => {
    release = r
  })
  server.use(
    http.get('/v1/tasks/t1/logs', async () => {
      await gate
      return HttpResponse.json({ items: [entry(1), entry(2)], next_seq: 0, total: 2 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  const conn = await fake.waitForConnection()
  // Arriving DURING the backfill. seq 2 is also in the page, so it must appear
  // once; seq 3 is above maxSeq, the paired positive control, so it must appear.
  conn.emit('task_log', logEvent(2))
  conn.emit('task_log', logEvent(3))
  await tick()
  release()

  await waitFor(() => expect(result.current.status).toBe('live'))
  await waitFor(() =>
    expect(result.current.rows.map((r) => r.text)).toEqual(['line-1', 'line-2', 'line-3']),
  )
})

test('pumps since_seq from next_seq until next_seq is 0', async () => {
  const fake = fakeSseServer()
  const seen: (string | null)[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const since = new URL(request.url).searchParams.get('since_seq')
      seen.push(since)
      if (since === null) return HttpResponse.json({ items: [entry(10)], next_seq: 10, total: 3 })
      if (since === '10') return HttpResponse.json({ items: [entry(20)], next_seq: 20, total: 3 })
      return HttpResponse.json({ items: [entry(30)], next_seq: 0, total: 3 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(seen).toEqual([null, '10', '20'])
  expect(result.current.rows.map((r) => r.text)).toEqual(['line-10', 'line-20', 'line-30'])
  expect(result.current.historyTruncated).toBe(false)
  expect(result.current.total).toBe(3)
})

test('stops at MAX_BACKFILL_PAGES, flags truncation, and still applies live frames', async () => {
  const fake = fakeSseServer()
  let requests = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      requests++
      // A server that never drains: next_seq is always non-zero.
      return HttpResponse.json({ items: [entry(requests)], next_seq: requests, total: 94312 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  // Exact count, not "several": an off-by-one or a missing cap is a request loop.
  expect(requests).toBe(MAX_BACKFILL_PAGES)
  expect(result.current.historyTruncated).toBe(true)
  expect(result.current.total).toBe(94312)

  // Truncated history must not stop live tailing.
  fake.latest().emit('task_log', logEvent(5000, 'after-cap\n'))
  await waitFor(() => expect(result.current.rows.some((r) => r.text === 'after-cap')).toBe(true))
})

test('a 404 on the stream is a terminal error with no retry', async () => {
  const fake = fakeSseServer()
  fake.status = 404
  fake.errorBody = { error: 'task not found' }
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('error'))
  expect(result.current.errorMessage).toContain('task not found')
  // A deleted task or a bad id is not transient: exactly one attempt, and no
  // history request either.
  await tick()
  expect(fake.connections).toHaveLength(1)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/useTaskLogStream.test.tsx`
Expected: FAIL - `Failed to resolve import "./useTaskLogStream"`.

- [ ] **Step 3: Write the implementation**

Create `web/src/jobs/useTaskLogStream.ts`:

```ts
import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError } from '../lib/api'
import {
  BACKFILL_PAGE_SIZE,
  getTaskLogs,
  streamTaskLog,
  type TaskLogEvent,
  type TaskLogPage,
} from './api'
import {
  appendEntries,
  createLogState,
  visibleRows,
  type LogChunk,
  type LogRow,
  type LogState,
} from './logBuffer'

/** History costs at most 10 requests of 200 lines. */
export const MAX_BACKFILL_PAGES = 10

/** Frames are coalesced into one state update per window. */
export const FLUSH_MS = 100

export type LogStreamStatus =
  | 'idle'
  | 'loading'
  | 'live'
  | 'recovering'
  | 'reconnecting'
  | 'disconnected'
  | 'ended'
  | 'history'
  | 'error'

export interface TaskLogStreamResult {
  rows: LogRow[]
  status: LogStreamStatus
  /** Current reconnect attempt, 1..5, shown as "reconnecting (n/5)". */
  attempt: number
  dropped: boolean
  evicted: boolean
  historyTruncated: boolean
  /** `total` from the last page, for the honest "showing N of T" notice. */
  total: number
  errorMessage: string
  reconnect: () => void
}

export interface UseTaskLogStreamOptions {
  /** False when the task is terminal: a terminal task opens no connection at all. */
  live: boolean
  /** False when the Log tab is not showing or no task is selected. */
  enabled: boolean
  /** Test seam forwarded to streamTaskLog. Must be referentially stable. */
  fetchImpl?: typeof fetch
}

/**
 * Tails one task's log: subscribe, then backfill, then replay the buffered
 * frames, then keep appending live ones.
 *
 * State lives HERE and not in the TanStack cache. A live append-only stream has
 * no fetch that resolves, no meaningful staleTime and no meaningful invalidate;
 * the subscribe-before-backfill ordering is imperative sequencing that useQuery
 * cannot express; and paging is a loop with a cap and an early exit rather than
 * one request (spec Decision 3). Log content is therefore never written to the
 * query cache, localStorage or sessionStorage - it is component-lifetime memory,
 * discarded on unmount.
 */
export function useTaskLogStream(
  taskId: string,
  { live, enabled, fetchImpl }: UseTaskLogStreamOptions,
): TaskLogStreamResult {
  const [view, setView] = useState<LogState>(createLogState)
  const [status, setStatus] = useState<LogStreamStatus>('idle')
  const [attempt, setAttempt] = useState(0)
  const [total, setTotal] = useState(0)
  const [historyTruncated, setHistoryTruncated] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')
  const [manualRetry, setManualRetry] = useState(0)

  const reconnect = useCallback(() => {
    setAttempt(0)
    setManualRetry((n) => n + 1)
  }, [])

  useEffect(() => {
    if (!enabled || taskId === '') {
      setView(createLogState())
      setStatus('idle')
      return
    }

    // Every mutable per-connection value is local to this effect run, so a stale
    // run can never write into the current one. `cancelled` plus the `gen`
    // generation counter are the identity check: a callback whose generation is
    // no longer current returns immediately, which is the SPA analogue of the
    // codebase's identity-checked-teardown rule.
    let cancelled = false
    let gen = 0
    let controller = new AbortController()
    let flushTimer: ReturnType<typeof setTimeout> | null = null
    let logState = createLogState()
    // maxSeq and the pre-backfill buffer live here, NOT in React state: writing
    // to them must never trigger a render and never reorder the join.
    let buffering = true
    let pending: TaskLogEvent[] = []

    setView(logState)
    setStatus('loading')
    setHistoryTruncated(false)
    setErrorMessage('')

    function publish() {
      flushTimer = null
      if (cancelled) return
      setView(logState)
    }

    // Coalesce to one setState per FLUSH_MS. This is not only a render
    // optimization: a browser that stops draining the socket fills the server's
    // 64-slot buffer and gets drop-closed (README.md:1346-1348), so less React
    // work per frame directly reduces server-side drops.
    function ingest(entries: LogChunk[]) {
      const next = appendEntries(logState, entries)
      if (next === logState) return // everything was a duplicate
      logState = next
      if (flushTimer === null) flushTimer = setTimeout(publish, FLUSH_MS)
    }

    function flushNow() {
      if (flushTimer !== null) {
        clearTimeout(flushTimer)
        flushTimer = null
      }
      publish()
    }

    // Task 8 replaces this with markDropped plus a bounded retry.
    function endConnection(myGen: number) {
      if (cancelled || myGen !== gen) return
      gen++
      controller.abort()
      setStatus('disconnected')
    }

    // Resolves once the response is 200, which means handleEvents has already
    // Subscribe()d and flushed (internal/api/events.go:59-70) - so the
    // subscription is provably live. Never a sleep: a timer barrier here is
    // exactly the broken test pattern the enabler's retro caught.
    function openStream(myGen: number): Promise<void> {
      return new Promise<void>((resolveOpen, rejectOpen) => {
        let opened = false
        streamTaskLog(taskId, {
          signal: controller.signal,
          fetchImpl,
          onOpen: () => {
            opened = true
            resolveOpen()
          },
          onLine: (e) => {
            if (myGen !== gen) return
            if (buffering) pending.push(e)
            else ingest([e])
          },
          onDropped: () => endConnection(myGen),
        })
          .then(() => {
            // The server never ends a stream on its own (README.md:1310-1313),
            // so a resolve is abnormal.
            if (opened) endConnection(myGen)
          })
          .catch((err: unknown) => {
            if (cancelled || myGen !== gen) return
            if (opened) endConnection(myGen)
            else rejectOpen(err)
          })
      })
    }

    async function run(sinceSeq: number) {
      const myGen = ++gen
      buffering = true
      pending = []
      controller = new AbortController()

      if (live) {
        try {
          // ORDER IS LOAD-BEARING (README.md:1334-1344): the subscription must be
          // open before the first history page, or the window between the last
          // page and the first frame is lost. Do NOT move this below the paging
          // loop. Guard: useTaskLogStream.test.tsx 'subscribes to the stream
          // BEFORE it requests the first history page'.
          await openStream(myGen)
        } catch (err) {
          if (cancelled || myGen !== gen) return
          if (err instanceof ApiError) {
            // 401 already fired onUnauthorized inside apiStream and
            // AuthProvider redirects (AuthProvider.tsx:39-49). 400 and 404 are
            // not transient: a bad id or a deleted task. No retry for any of
            // them. Never log the error object.
            if (err.status === 400 || err.status === 401 || err.status === 404) {
              setErrorMessage(err.message)
              setStatus('error')
              return
            }
          }
          // Task 8 replaces this with the bounded backoff.
          setErrorMessage(err instanceof Error ? err.message : 'stream failed')
          setStatus('disconnected')
          return
        }
        if (cancelled || myGen !== gen) return
      }

      let since = sinceSeq
      let pages = 0
      for (;;) {
        let page: TaskLogPage
        try {
          page = await getTaskLogs(taskId, since, BACKFILL_PAGE_SIZE)
        } catch (err) {
          if (cancelled || myGen !== gen) return
          setErrorMessage(err instanceof Error ? err.message : 'failed to load logs')
          setStatus('error')
          controller.abort()
          return
        }
        if (cancelled || myGen !== gen) return
        ingest(page.items)
        setTotal(page.total)
        pages++
        if (page.next_seq === 0) break
        if (pages >= MAX_BACKFILL_PAGES) {
          setHistoryTruncated(true)
          break
        }
        since = page.next_seq
      }

      // Step 3 of the README join: apply what arrived while we were paging.
      // appendEntries drops anything with seq <= maxSeq, so this is
      // duplicate-free, and both paths share one dedupe rule.
      buffering = false
      const replay = pending
      pending = []
      ingest(replay)
      flushNow()
      setStatus(live ? 'live' : 'history')
    }

    void run(0)

    return () => {
      cancelled = true
      gen++
      controller.abort()
      if (flushTimer !== null) clearTimeout(flushTimer)
    }
  }, [taskId, live, enabled, fetchImpl, manualRetry])

  const rows = useMemo(() => visibleRows(view), [view])

  return {
    rows,
    status,
    attempt,
    dropped: view.dropped,
    evicted: view.evicted,
    historyTruncated,
    total,
    errorMessage,
    reconnect,
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/useTaskLogStream.test.tsx`
Expected: PASS, 5 tests.

- [ ] **Step 5: Prove the ordering and dedupe tests are not vacuous (MANDATORY)**

Three temporary mutations. After each, run the file, confirm the named test **fails**, revert.

1. **Invert the join order.** Move the whole `if (live) { ... }` block (the `openStream` call and its catch) from above the paging loop to immediately below it, before `buffering = false`.
   Expected: `subscribes to the stream BEFORE it requests the first history page` FAILS with `['logs','stream']`. **If it still passes, the test is vacuous and must be fixed before continuing** - this is the ordering regression the spec calls out as invisible without its RED proof.
2. **Skip the replay dedupe.** In `appendEntries` (`logBuffer.ts`), remove `if (e.seq <= maxSeq) continue`.
   Expected: `applies frames buffered during backfill...` FAILS with `['line-1','line-2','line-2','line-3']`.
3. **Remove the page cap.** Delete the `if (pages >= MAX_BACKFILL_PAGES) { ... }` block.
   Expected: `stops at MAX_BACKFILL_PAGES...` fails or the test times out on an unbounded request loop. Either is acceptable RED; revert immediately.

Restore and re-run: PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/useTaskLogStream.ts web/src/jobs/useTaskLogStream.test.tsx
git commit -m "feat(web): useTaskLogStream - subscribe before backfill, paged history, deduped replay"
```

---

## Task 8: recovery - drop marker, one immediate re-backfill, bounded retry with a proven-connection reset

Every bound here exists because the unbounded version is the natural one to write. The reset rule is the specific trap relay already fell into once on the agent side (`docs/retros/2026-06-20-reconnect-backoff-never-resets.md`).

**Files:**
- Modify: `web/src/jobs/useTaskLogStream.ts` (replace `endConnection`, add `recover`, `scheduleRetry`, `markProven`)
- Test: `web/src/jobs/useTaskLogStream.test.tsx` (append)

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/useTaskLogStream.test.tsx`, adding `act` to the `@testing-library/react` import and `vi` to the `vitest` import:

```tsx
// Pure fake timers, no shouldAdvanceTime: real time leaking in would make the
// "did not fire early" assertions flaky by a few milliseconds. Every advance is
// wrapped in act() because it flushes React state updates.
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

test('an event: dropped frame produces exactly ONE re-backfill plus a permanent marker', async () => {
  const fake = fakeSseServer()
  let requests = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      requests++
      return HttpResponse.json({ items: [entry(requests)], next_seq: 0, total: 1 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(requests).toBe(1)

  const first = fake.latest()
  first.emit('dropped', { reason: 'slow_consumer' })
  // The server closes the stream immediately after the dropped frame
  // (internal/api/events.go:84-86). That close must NOT trigger a SECOND
  // recovery - hence the exact counts below rather than "at least".
  first.close()

  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(requests).toBe(2)
  expect(fake.connections).toHaveLength(2)
  expect(first.aborted).toBe(true)
  expect(result.current.dropped).toBe(true)
  expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(true)
  // No backoff delay for a dropped frame: the server told us, so one immediate
  // recovery is correct and the attempt counter is untouched.
  expect(result.current.attempt).toBe(0)

  // The marker is permanent for the session even though recovery succeeded.
  fake.latest().emit('task_log', logEvent(500, 'recovered\n'))
  await waitFor(() => expect(result.current.rows.some((r) => r.text === 'recovered')).toBe(true))
  expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(true)
})

test('reconnects at 1/2/4/8/15 s, stops after 5 attempts, and the manual control resets', async () => {
  vi.useFakeTimers()
  try {
    const fake = fakeSseServer()
    // A 500 fails before the stream ever opens, so this test needs no /logs
    // handler and never leaves an unproven connection ambiguous.
    fake.status = 500
    fake.errorBody = { error: 'boom' }
    const { result } = renderHook(() =>
      useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
    )
    await advance(0)
    expect(fake.connections).toHaveLength(1)
    expect(result.current.status).toBe('reconnecting')
    expect(result.current.attempt).toBe(1)

    const delays = [1000, 2000, 4000, 8000, 15000]
    for (let i = 0; i < delays.length; i++) {
      await advance(delays[i] - 1)
      expect(fake.connections, `retry ${i + 1} fired early`).toHaveLength(i + 1)
      await advance(1)
      expect(fake.connections, `retry ${i + 1} did not fire`).toHaveLength(i + 2)
    }

    // Non-vacuity: the count must STOP growing. A test that only asserted "it
    // retried" passes for an unbounded loop. 50 open tabs against a restarted
    // server must not become a reconnect storm.
    expect(result.current.status).toBe('disconnected')
    await advance(300_000)
    expect(fake.connections).toHaveLength(6) // initial attempt plus exactly 5 retries

    await act(async () => {
      result.current.reconnect()
    })
    await advance(0)
    expect(fake.connections).toHaveLength(7)
    expect(result.current.attempt).toBe(1)
  } finally {
    vi.useRealTimers()
  }
})

test('the backoff counter resets only for a connection that PROVED itself', async () => {
  vi.useFakeTimers()
  try {
    const fake = fakeSseServer()
    server.use(http.get('/v1/tasks/t1/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })))

    // Direction A: opens and closes immediately, never delivering a frame. This
    // is the 2026-06-20-reconnect-backoff-never-resets bug class - resetting on
    // open alone turns it into an unbounded tight loop.
    const { result, unmount } = renderHook(() =>
      useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
    )
    for (let i = 0; i < 6; i++) {
      const conn = await fake.waitForConnection(i + 1)
      conn.close()
      await advance(20_000)
    }
    expect(result.current.status).toBe('disconnected')
    expect(fake.connections).toHaveLength(6)
    await advance(300_000)
    expect(fake.connections).toHaveLength(6)
    unmount()

    // Direction B: same flapping, but each connection delivers a frame first, so
    // each one has proven itself and the counter resets every cycle. Ten cycles
    // must never reach 'disconnected', and every delay must be the first one.
    const fake2 = fakeSseServer()
    const r2 = renderHook(() =>
      useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake2.fetchImpl }),
    )
    for (let i = 0; i < 10; i++) {
      const conn = await fake2.waitForConnection(i + 1)
      conn.emit('task_log', logEvent(i + 1))
      await advance(0)
      conn.close()
      await advance(999)
      expect(fake2.connections, `cycle ${i} retried early`).toHaveLength(i + 1)
      await advance(1)
      expect(fake2.connections, `cycle ${i} did not retry at 1s`).toHaveLength(i + 2)
      expect(r2.result.current.status).not.toBe('disconnected')
    }
    r2.unmount()
  } finally {
    vi.useRealTimers()
  }
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/useTaskLogStream.test.tsx`
Expected: FAIL - `an event: dropped frame...` fails because `status` becomes `'disconnected'` and `requests` stays 1 (the Task 7 stub does not recover); the two backoff tests fail because there is no retry at all.

- [ ] **Step 3: Write the implementation**

In `web/src/jobs/useTaskLogStream.ts`, add `markDropped` to the `./logBuffer` import and add these constants below `FLUSH_MS`:

```ts
/** Delays for consecutive failed reconnects, last value repeated. */
export const RETRY_DELAYS_MS = [1000, 2000, 4000, 8000, 15000]

/** Consecutive failed attempts before a human click is required. */
export const MAX_RECONNECT_ATTEMPTS = 5

/** A connection that stays open this long has proven itself. */
export const RESET_AFTER_MS = 10_000
```

Inside the effect, add two more locals next to `flushTimer`:

```ts
    let retryTimer: ReturnType<typeof setTimeout> | null = null
    let openTimer: ReturnType<typeof setTimeout> | null = null
    let attempts = 0
    let proven = false
```

Replace `endConnection` with:

```ts
    // A connection earns a backoff-counter reset only by PROVING itself: staying
    // open past RESET_AFTER_MS or delivering at least one frame. Resetting on
    // open alone is exactly the bug relay already shipped once on the agent side
    // (docs/retros/2026-06-20-reconnect-backoff-never-resets.md), where a
    // connection that opens and immediately fails becomes an unbounded tight
    // loop.
    function markProven() {
      proven = true
      if (attempts !== 0) {
        attempts = 0
        setAttempt(0)
      }
    }

    function recover(myGen: number, reason: 'dropped' | 'closed') {
      if (cancelled || myGen !== gen) return
      // Bump the generation FIRST so the dying connection's remaining callbacks
      // cannot trigger a second recovery: the server writes `dropped` and then
      // closes, so both fire for one event.
      gen++
      controller.abort()
      if (openTimer !== null) {
        clearTimeout(openTimer)
        openTimer = null
      }
      // Lines may have been missed either way, so the permanent marker goes in
      // for both reasons.
      logState = markDropped(logState)
      flushNow()

      if (reason === 'dropped') {
        // The server told us it dropped us (README.md:1346-1348). One immediate
        // recovery is correct: no backoff, attempt counter untouched.
        setStatus('recovering')
        void run(logState.maxSeq)
        return
      }
      // A clean close is abnormal (README.md:1310-1313), so it is treated as a
      // failure for backoff purposes, not as an end of data.
      scheduleRetry(logState.maxSeq)
    }

    function scheduleRetry(sinceSeq: number) {
      if (proven) attempts = 0
      if (attempts >= MAX_RECONNECT_ATTEMPTS) {
        setStatus('disconnected')
        return
      }
      const delay = RETRY_DELAYS_MS[Math.min(attempts, RETRY_DELAYS_MS.length - 1)]
      attempts++
      setAttempt(attempts)
      setStatus('reconnecting')
      retryTimer = setTimeout(() => {
        retryTimer = null
        if (!cancelled) void run(sinceSeq)
      }, delay)
    }
```

In `openStream`, replace the three callbacks that referenced `endConnection`:

```ts
          onOpen: () => {
            opened = true
            openTimer = setTimeout(() => {
              openTimer = null
              if (myGen === gen) markProven()
            }, RESET_AFTER_MS)
            resolveOpen()
          },
          onLine: (e) => {
            if (myGen !== gen) return
            markProven() // a delivered frame proves the connection
            if (buffering) pending.push(e)
            else ingest([e])
          },
          onDropped: () => recover(myGen, 'dropped'),
```

and in the `.then`/`.catch`, replace both `endConnection(myGen)` calls with `recover(myGen, 'closed')`.

In `run`, replace the non-`ApiError`-terminal branch of the `openStream` catch:

```ts
          scheduleRetry(sinceSeq)
          return
```

and add `proven = false` immediately after `const myGen = ++gen`.

Finally, extend the cleanup:

```ts
    return () => {
      cancelled = true
      gen++
      controller.abort()
      if (flushTimer !== null) clearTimeout(flushTimer)
      if (retryTimer !== null) clearTimeout(retryTimer)
      if (openTimer !== null) clearTimeout(openTimer)
    }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/useTaskLogStream.test.tsx`
Expected: PASS, 8 tests.

- [ ] **Step 5: Prove the recovery tests are not vacuous (MANDATORY)**

Four temporary mutations. After each, run the file, confirm the named test **fails**, revert.

1. **Unbounded retry.** Delete the `if (attempts >= MAX_RECONNECT_ATTEMPTS) { setStatus('disconnected'); return }` block.
   Expected: `reconnects at 1/2/4/8/15 s...` FAILS on `expect(fake.connections).toHaveLength(6)` after the 300 s advance - it will be much larger. **If it still passes, the test is only asserting "it retried" and must be fixed.**
2. **Reset on open instead of on proof.** Move `markProven()` from the `onLine` callback and the `openTimer` into `onOpen` directly.
   Expected: `the backoff counter resets only for a connection that PROVED itself` FAILS in direction A - `status` never reaches `'disconnected'` and the connection count keeps growing.
3. **Double recovery on a dropped frame.** Remove the `gen++` from the top of `recover`.
   Expected: `an event: dropped frame produces exactly ONE re-backfill...` FAILS with `requests` at 3 and 3 connections, because the close that follows the dropped frame recovers again.
4. **No marker.** Remove the `logState = markDropped(logState)` line.
   Expected: the same test FAILS on `expect(result.current.dropped).toBe(true)` - a silent drop would misrepresent an incomplete log as complete.

Restore and re-run: PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/useTaskLogStream.ts web/src/jobs/useTaskLogStream.test.tsx
git commit -m "feat(web): task-log recovery - drop marker, one re-backfill, bounded retry with proven reset"
```

---

## Task 9: terminal tasks, the final reconciliation page, and the connection-count guarantee

A `?task_id=` subscription has no terminal signal (`README.md:1310-1313`); `useJob`'s 3 s poll supplies it. That is what makes one log-only connection sufficient, and it is what removes the "connection open forever on a finished task" failure entirely.

**Files:**
- Modify: `web/src/jobs/useTaskLogStream.ts` (add the `carry` ref, the non-live settle, `finalizePartials`)
- Test: `web/src/jobs/useTaskLogStream.test.tsx` (append)

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/useTaskLogStream.test.tsx`:

```tsx
// Paired positive control in one test: the same harness, live true vs false.
test('a terminal task opens no stream at all, while a live one opens exactly one', async () => {
  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({ items: [entry(1), entry(2)], next_seq: 0, total: 2 }),
    ),
  )
  const terminal = renderHook(() =>
    useTaskLogStream('t1', { live: false, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(terminal.result.current.status).toBe('history'))
  expect(terminal.result.current.rows.map((r) => r.text)).toEqual(['line-1', 'line-2'])
  await tick()
  expect(fake.connections).toHaveLength(0)
  terminal.unmount()

  const running = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(running.result.current.status).toBe('live'))
  expect(fake.connections).toHaveLength(1)
  running.unmount()
})

test('a task that becomes terminal mid-tail closes the stream and reconciles once', async () => {
  const fake = fakeSseServer()
  const sinceParams: (string | null)[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const since = new URL(request.url).searchParams.get('since_seq')
      sinceParams.push(since)
      if (since === null) return HttpResponse.json({ items: [entry(10)], next_seq: 0, total: 1 })
      return HttpResponse.json({ items: [entry(30, 'tail\n')], next_seq: 0, total: 3 })
    }),
  )
  const { result, rerender } = renderHook(
    ({ live }: { live: boolean }) =>
      useTaskLogStream('t1', { live, enabled: true, fetchImpl: fake.fetchImpl }),
    { initialProps: { live: true } },
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  const conn = fake.latest()
  // A partial with no trailing newline, plus a live line, before the task ends.
  conn.emit('task_log', logEvent(20, 'mid\nno-newline-yet'))
  await waitFor(() => expect(result.current.rows.some((r) => r.text === 'mid')).toBe(true))

  rerender({ live: false })
  await waitFor(() => expect(result.current.status).toBe('ended'))
  expect(conn.aborted).toBe(true)
  expect(fake.connections).toHaveLength(1)
  // Exactly ONE reconciliation page, and it pages from the last seq seen rather
  // than re-fetching the whole history.
  expect(sinceParams).toEqual([null, '20'])
  // The dangling partial is flushed as a final line, and earlier lines survive.
  const texts = result.current.rows.map((r) => r.text)
  expect(texts).toEqual(['line-10', 'mid', 'tail', 'no-newline-yet'])
  expect(result.current.rows.every((r) => r.kind !== 'partial')).toBe(true)
})

test('switching tasks opens exactly one stream each and leaves none open', async () => {
  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/:tid/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })),
  )
  const { result, rerender, unmount } = renderHook(
    ({ id, enabled }: { id: string; enabled: boolean }) =>
      useTaskLogStream(id, { live: true, enabled, fetchImpl: fake.fetchImpl }),
    { initialProps: { id: 't1', enabled: true } },
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(fake.connections).toHaveLength(1)

  rerender({ id: 't2', enabled: true })
  await waitFor(() => expect(fake.connections).toHaveLength(2))
  rerender({ id: 't3', enabled: true })
  await waitFor(() => expect(fake.connections).toHaveLength(3))

  // Exact counts, not "at least one": three opened, the first two aborted, and
  // the URLs prove each stream really was for the right task (the positive
  // control that makes the abort assertions meaningful).
  expect(fake.abortedCount()).toBe(2)
  expect(fake.connections[2].aborted).toBe(false)
  expect(fake.connections.map((c) => c.url)).toEqual([
    '/v1/events?task_id=t1',
    '/v1/events?task_id=t2',
    '/v1/events?task_id=t3',
  ])

  // Leaving the Log tab: enabled goes false, the connection closes, none opens.
  rerender({ id: 't3', enabled: false })
  await waitFor(() => expect(fake.abortedCount()).toBe(3))
  expect(fake.connections).toHaveLength(3)
  await waitFor(() => expect(result.current.status).toBe('idle'))
  expect(result.current.rows).toEqual([])

  // Returning re-subscribes exactly once; unmount aborts the last one.
  rerender({ id: 't3', enabled: true })
  await waitFor(() => expect(fake.connections).toHaveLength(4))
  unmount()
  await waitFor(() => expect(fake.abortedCount()).toBe(4))
})

test('coalesces a burst of 50 frames into far fewer than 50 renders', async () => {
  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/t1/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })),
  )
  let renders = 0
  const { result } = renderHook(() => {
    renders++
    return useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl })
  })
  await waitFor(() => expect(result.current.status).toBe('live'))
  const before = renders

  const conn = fake.latest()
  for (let i = 1; i <= 50; i++) conn.emit('task_log', logEvent(i))
  // Positive control: all 50 lines really arrived, so a broken transport cannot
  // make the render-count assertion pass.
  await waitFor(() => expect(result.current.rows).toHaveLength(50))
  expect(renders - before).toBeLessThanOrEqual(5)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/useTaskLogStream.test.tsx`
Expected: FAIL - `a task that becomes terminal mid-tail...` fails because the re-run pages from `since_seq=null` (a full re-backfill) and settles to `'history'` rather than `'ended'`, and the partial is not flushed.

- [ ] **Step 3: Write the implementation**

In `web/src/jobs/useTaskLogStream.ts`:

Add `useRef` to the React import and `finalizePartials` to the `./logBuffer` import. Add above the effect:

```ts
  // Carries log state across the effect re-run caused by `live` flipping to false
  // (the task reached a terminal status), so the final pass is ONE
  // ?since_seq=<maxSeq> reconciliation page instead of a full re-backfill. That
  // closes the "did we get the tail" question without depending on frame
  // delivery, and costs one request per completed task view. Keyed on taskId, so
  // a task switch or a tab exit always starts clean.
  const carry = useRef<{ taskId: string; state: LogState } | null>(null)
```

In the disabled early-return branch, clear it:

```ts
    if (!enabled || taskId === '') {
      carry.current = null
      setView(createLogState())
      setStatus('idle')
      return
    }
```

Replace the `let logState = createLogState()` line with:

```ts
    const carried = !live && carry.current?.taskId === taskId ? carry.current.state : null
    carry.current = null
    let logState = carried ?? createLogState()
```

Add a single writer for `logState` right above `publish`, and route every assignment through it:

```ts
    function setLogState(next: LogState) {
      logState = next
      carry.current = { taskId, state: next }
    }
```

- in `ingest`, replace `logState = next` with `setLogState(next)`
- in `recover`, replace `logState = markDropped(logState)` with `setLogState(markDropped(logState))`

Replace the final `setStatus(live ? 'live' : 'history')` in `run` with:

```ts
      if (!live) {
        // No further output is possible, so a dangling partial is final.
        setLogState(finalizePartials(logState))
        flushNow()
        setStatus(carried ? 'ended' : 'history')
        return
      }
      setStatus('live')
```

and replace the initial call with:

```ts
    void run(carried ? carried.maxSeq : 0)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/useTaskLogStream.test.tsx`
Expected: PASS, 12 tests.

Run: `npx vitest run src/jobs src/lib`
Expected: PASS.

- [ ] **Step 5: Prove the lifecycle tests are not vacuous (MANDATORY)**

Four temporary mutations. After each, run the file, confirm the named test **fails**, revert.

1. **Subscribe even when terminal.** Change `if (live) {` around the `openStream` block to `if (true) {`.
   Expected: `a terminal task opens no stream at all...` FAILS on `expect(fake.connections).toHaveLength(0)`.
2. **Do not abort on cleanup.** Remove `controller.abort()` from the effect's cleanup return.
   Expected: `switching tasks opens exactly one stream each...` FAILS on `expect(fake.abortedCount()).toBe(2)` (it will be 0). **This is the leak test's only real teeth** - if it still passes, the harness is not observing aborts and must be fixed.
3. **Drop the carry.** Change `const carried = ...` to `const carried = null`.
   Expected: `a task that becomes terminal mid-tail...` FAILS on `expect(sinceParams).toEqual([null, '20'])` (it will be `[null, null]`) and on `'ended'` vs `'history'`.
4. **Publish per frame.** In `ingest`, replace the `if (flushTimer === null) ...` line with a direct `publish()`.
   Expected: `coalesces a burst of 50 frames...` FAILS with a render delta near 50.

Restore and re-run: PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/useTaskLogStream.ts web/src/jobs/useTaskLogStream.test.tsx
git commit -m "feat(web): terminal-task reconciliation and the one-connection-per-page guarantee"
```

---

## Task 10: `LogView` - the shared presentational log body

Two columns (time, content), not the hi-fi's four: a `logEntry` carries no level and no source (`internal/api/tasks.go:56-61`), so rendering an `INFO`/`DEBUG` column would invent data.

**Files:**
- Create: `web/src/jobs/LogView.tsx`
- Test: `web/src/jobs/LogView.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/LogView.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { LogView } from './LogView'
import { DROP_MARKER_TEXT, MAX_LINES, type LogRow } from './logBuffer'
import type { TaskLogStreamResult } from './useTaskLogStream'

function row(key: number, text: string, over: Partial<LogRow> = {}): LogRow {
  return { key, kind: 'line', stream: 'stdout', text, time: '2026-08-09T14:36:25.000Z', ...over }
}

function streamOf(over: Partial<TaskLogStreamResult> = {}): TaskLogStreamResult {
  return {
    rows: [],
    status: 'live',
    attempt: 0,
    dropped: false,
    evicted: false,
    historyTruncated: false,
    total: 0,
    errorMessage: '',
    reconnect: () => {},
    ...over,
  }
}

test('renders log lines with a stdout/stderr distinction and a UTC time column', () => {
  render(
    <LogView
      stream={streamOf({
        rows: [row(1, 'building'), row(2, 'warning: x', { stream: 'stderr' })],
      })}
    />,
  )
  expect(screen.getByText('building')).toBeInTheDocument()
  expect(screen.getByText('warning: x').className).toMatch(/text-err/)
  expect(screen.getAllByText('14:36:25').length).toBeGreaterThan(0)
})

test('shows LIVE with a green dot only while the stream is actually open', () => {
  const { rerender } = render(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'live' })} />)
  expect(screen.getByText('LIVE')).toBeInTheDocument()
  // The inverse of the old LogTab.test.tsx:29-37 case: a LIVE badge on a
  // non-streaming view would imply output we are not receiving.
  rerender(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'history' })} />)
  expect(screen.queryByText('LIVE')).toBeNull()
  expect(screen.getByText('HISTORY')).toBeInTheDocument()
  rerender(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'ended' })} />)
  expect(screen.queryByText('LIVE')).toBeNull()
  expect(screen.getByText('ENDED')).toBeInTheDocument()
})

test('shows the reconnect attempt count while reconnecting', () => {
  render(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'reconnecting', attempt: 3 })} />)
  expect(screen.getByText(/RECONNECTING \(3\/5\)/)).toBeInTheDocument()
})

test('offers a manual Reconnect control when disconnected', async () => {
  const reconnect = vi.fn()
  render(<LogView stream={streamOf({ rows: [row(1, 'a')], status: 'disconnected', reconnect })} />)
  await userEvent.click(screen.getByRole('button', { name: /reconnect/i }))
  expect(reconnect).toHaveBeenCalledTimes(1)
})

test('renders the drop marker as a distinct in-stream row', () => {
  render(
    <LogView
      stream={streamOf({
        rows: [row(1, 'before'), row(2, DROP_MARKER_TEXT, { kind: 'marker', time: '' }), row(3, 'after')],
        dropped: true,
      })}
    />,
  )
  expect(screen.getByText(new RegExp(DROP_MARKER_TEXT))).toBeInTheDocument()
})

test('shows the truncation notice with real counts, then the eviction notice', () => {
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(1, 'a')], historyTruncated: true, total: 94312 })} />,
  )
  expect(
    screen.getByText(
      `Showing the first ${MAX_LINES.toLocaleString('en-US')} of 94,312 lines. Live output continues below.`,
    ),
  ).toBeInTheDocument()

  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a')], historyTruncated: true, evicted: true, total: 94312 })} />,
  )
  expect(screen.getByText('Earlier output not shown.')).toBeInTheDocument()
})

test('shows loading, empty and error states', async () => {
  const reconnect = vi.fn()
  const { rerender } = render(<LogView stream={streamOf({ status: 'loading' })} />)
  expect(screen.getByText(/loading logs/i)).toBeInTheDocument()

  rerender(<LogView stream={streamOf({ status: 'history' })} />)
  expect(screen.getByText(/no log output/i)).toBeInTheDocument()

  rerender(<LogView stream={streamOf({ status: 'error', errorMessage: '404 task not found', reconnect })} />)
  expect(screen.getByText(/failed to load logs/i)).toBeInTheDocument()
  expect(screen.getByText(/404 task not found/)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  expect(reconnect).toHaveBeenCalledTimes(1)
})

test('renders untrusted content as text, never as HTML', () => {
  render(<LogView stream={streamOf({ rows: [row(1, '<img src=x onerror=alert(1)>')] })} />)
  // A job that prints markup must render as characters. This is the XSS boundary.
  expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
  expect(document.querySelector('img')).toBeNull()
})

test('scrolls to the bottom on new rows while following, and stops when follow is off', async () => {
  const onScrolledToBottom = vi.fn()
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(1, 'a')] })} onScrolledToBottom={onScrolledToBottom} />,
  )
  const before = onScrolledToBottom.mock.calls.length
  expect(before).toBeGreaterThan(0)

  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a'), row(2, 'b')] })} onScrolledToBottom={onScrolledToBottom} />,
  )
  expect(onScrolledToBottom.mock.calls.length).toBeGreaterThan(before)

  // Asserting a pixel value in jsdom would be vacuously green (scrollTop and
  // scrollHeight are 0 there), so the geometry is set explicitly and only the
  // DECISION is asserted, via shouldFollow.
  await userEvent.click(screen.getByRole('button', { name: /follow tail/i }))
  const after = onScrolledToBottom.mock.calls.length
  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a'), row(2, 'b'), row(3, 'c')] })} onScrolledToBottom={onScrolledToBottom} />,
  )
  expect(onScrolledToBottom.mock.calls.length).toBe(after)
  expect(screen.getByRole('button', { name: /jump to latest/i })).toBeInTheDocument()
})

test('a scroll away from the bottom turns follow off and reveals Jump to latest', () => {
  const { container } = render(<LogView stream={streamOf({ rows: [row(1, 'a')] })} />)
  expect(screen.queryByRole('button', { name: /jump to latest/i })).toBeNull()

  const box = container.querySelector('[data-testid="log-body"]') as HTMLElement
  Object.defineProperty(box, 'scrollHeight', { value: 2000, configurable: true })
  Object.defineProperty(box, 'clientHeight', { value: 1000, configurable: true })
  box.scrollTop = 0
  box.dispatchEvent(new Event('scroll', { bubbles: true }))

  expect(screen.getByRole('button', { name: /jump to latest/i })).toBeInTheDocument()
})

test('renders the endpoint caption and extra header content when given', () => {
  render(
    <LogView
      stream={streamOf({ rows: [row(1, 'a')] })}
      endpointCaption="/v1/events?task_id=t1 · single-task stream"
      headerExtra={<span>EXTRA</span>}
    />,
  )
  expect(screen.getByText('/v1/events?task_id=t1 · single-task stream')).toBeInTheDocument()
  expect(screen.getByText('EXTRA')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/LogView.test.tsx`
Expected: FAIL - `Failed to resolve import "./LogView"`.

- [ ] **Step 3: Write the implementation**

Create `web/src/jobs/LogView.tsx`:

```tsx
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Button } from '../components/Button'
import { PillButton } from '../components/holo'
import { MAX_LINES, shouldFollow, type LogRow } from './logBuffer'
import type { LogStreamStatus, TaskLogStreamResult } from './useTaskLogStream'

// Status vocabulary for the header strip, replacing LogTab's old
// `STATIC · HISTORY` / `live tailing pending` (LogTab.tsx:37-40). LIVE appears
// ONLY while a stream is actually open - a badge on a non-streaming view would
// imply output we are not receiving.
function statusLabel(status: LogStreamStatus, attempt: number): string {
  switch (status) {
    case 'live':
      return 'LIVE'
    case 'loading':
      return 'LOADING'
    case 'recovering':
      return 'RECOVERING'
    case 'reconnecting':
      return `RECONNECTING (${attempt}/5)`
    case 'disconnected':
      return 'DISCONNECTED'
    case 'ended':
      return 'ENDED'
    case 'history':
      return 'HISTORY'
    case 'error':
      return 'ERROR'
    default:
      return 'IDLE'
  }
}

// UTC HH:MM:SS. Deliberately locale-independent, so the mono column is a fixed
// width and tests are deterministic.
function logTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().slice(11, 19)
}

function LogRowView({ row }: { row: LogRow }) {
  if (row.kind === 'marker') {
    return (
      <div data-kind="marker" className="my-1 border-y border-warn/40 py-0.5 text-warn">
        --- {row.text} ---
      </div>
    )
  }
  // Two columns, not the hi-fi's four (hifi3-holo-pages.jsx:2754-2759): a
  // logEntry carries no level and no source (internal/api/tasks.go:56-61), so an
  // INFO/DEBUG column would invent data.
  return (
    <div data-kind={row.kind} className="grid grid-cols-[62px_1fr] gap-3">
      <span className="text-fg-dim">{logTime(row.time)}</span>
      {/* whitespace-pre-wrap keeps indentation. Content is always a React text
          child, which escapes it: this is the XSS boundary, and a job printing
          <img onerror> must render as characters. NEVER dangerouslySetInnerHTML -
          this is untrusted subprocess output. */}
      <span
        className={`whitespace-pre-wrap break-all ${row.stream === 'stderr' ? 'text-err' : 'text-fg'}`}
      >
        {row.text}
      </span>
    </div>
  )
}

export interface LogViewProps {
  stream: TaskLogStreamResult
  /** e.g. `/v1/events?task_id=<id> · single-task stream` on the full-screen view. */
  endpointCaption?: string
  /** Extra header content, e.g. the job-detail tab's "Full screen" link. */
  headerExtra?: ReactNode
  bodyClassName?: string
  /** Test seam: called whenever the view scrolls itself to the bottom. */
  onScrolledToBottom?: () => void
}

export function LogView({
  stream,
  endpointCaption,
  headerExtra,
  bodyClassName,
  onScrolledToBottom,
}: LogViewProps) {
  const { rows, status, attempt, evicted, historyTruncated, total, errorMessage } = stream
  const [follow, setFollow] = useState(true)
  const boxRef = useRef<HTMLDivElement>(null)

  const scrollToBottom = useCallback(() => {
    const el = boxRef.current
    if (el) el.scrollTop = el.scrollHeight
    onScrolledToBottom?.()
  }, [onScrolledToBottom])

  useEffect(() => {
    if (follow) scrollToBottom()
  }, [rows, follow, scrollToBottom])

  function handleScroll() {
    const el = boxRef.current
    if (el) setFollow(shouldFollow(el.scrollTop, el.scrollHeight, el.clientHeight))
  }

  const live = status === 'live'

  // The page cap holds the OLDEST lines of a long history, which is the wrong end
  // for a tail view (spec Decision 7). The notice says so with the real total,
  // and switches once drop-oldest has converged the view to a true tail.
  const notice = evicted
    ? 'Earlier output not shown.'
    : historyTruncated
      ? `Showing the first ${MAX_LINES.toLocaleString('en-US')} of ${total.toLocaleString('en-US')} lines. Live output continues below.`
      : null

  let body: ReactNode
  if (status === 'error') {
    body = (
      <div className="flex flex-col items-start gap-2 p-1">
        <div className="text-[12px] text-err">
          Failed to load logs.{errorMessage ? ` ${errorMessage}` : ''}
        </div>
        <Button className="w-auto px-4" onClick={stream.reconnect}>
          Retry
        </Button>
      </div>
    )
  } else if (status === 'loading' && rows.length === 0) {
    body = <div className="p-1 text-[12px] text-fg-mute">Loading logs...</div>
  } else if (rows.length === 0) {
    body = <div className="p-1 text-[12px] text-fg-mute">No log output.</div>
  } else {
    body = rows.map((r) => <LogRowView key={r.key} row={r} />)
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-3 py-2 font-mono text-[10px] tracking-[0.14em] text-fg-mute">
        <span className={`flex items-center gap-1.5 ${live ? 'text-ok' : 'text-fg-dim'}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${live ? 'bg-ok' : 'bg-fg-mute'}`} />
          {statusLabel(status, attempt)}
        </span>
        {endpointCaption && (
          <span className="truncate tracking-[0.06em]">{endpointCaption}</span>
        )}
        <span className="ml-auto flex items-center gap-2">
          {!follow && (
            <PillButton
              className="!px-3 !py-1 !text-[10px]"
              onClick={() => {
                setFollow(true)
                scrollToBottom()
              }}
            >
              Jump to latest
            </PillButton>
          )}
          <PillButton
            aria-pressed={follow}
            variant={follow ? 'primary' : 'ghost'}
            className="!px-3 !py-1 !text-[10px]"
            onClick={() => {
              const next = !follow
              setFollow(next)
              if (next) scrollToBottom()
            }}
          >
            Follow tail
          </PillButton>
          {headerExtra}
        </span>
      </div>

      {status === 'disconnected' && (
        <div className="flex flex-wrap items-center gap-3 border-b border-border bg-warn/5 px-3 py-2 text-[11px] text-warn">
          <span>Disconnected after 5 attempts.</span>
          <PillButton className="!px-3 !py-1 !text-[10px]" onClick={stream.reconnect}>
            Reconnect
          </PillButton>
        </div>
      )}

      {notice && (
        <div className="border-b border-border px-3 py-2 text-[11px] text-fg-mute">{notice}</div>
      )}

      <div
        ref={boxRef}
        data-testid="log-body"
        onScroll={handleScroll}
        className={`flex flex-col gap-0.5 bg-black/25 p-3 font-mono text-[11px] ${
          bodyClassName ?? 'max-h-[420px] overflow-auto'
        }`}
      >
        {body}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/LogView.test.tsx`
Expected: PASS, 11 tests.

- [ ] **Step 5: Prove the follow-tail and no-HTML tests are not vacuous**

1. In `handleScroll`, replace the `setFollow(...)` call with `setFollow(true)`.
   Expected: `a scroll away from the bottom turns follow off...` FAILS - no Jump to latest appears. Revert.
2. In `LogRowView`, temporarily render the content as `<span dangerouslySetInnerHTML={{ __html: row.text }} />`.
   Expected: `renders untrusted content as text, never as HTML` FAILS on `expect(document.querySelector('img')).toBeNull()`. **Revert immediately** - this mutation is the XSS the assertion exists to prevent.
3. In the `useEffect`, remove the `if (follow)` guard.
   Expected: `scrolls to the bottom on new rows while following...` FAILS on the "follow off means no scroll" assertion. Revert.

Run: `npx vitest run src/jobs/LogView.test.tsx` - PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/LogView.tsx web/src/jobs/LogView.test.tsx
git commit -m "feat(web): LogView - live status strip, reassembled rows, notices, follow tail"
```

---

## Task 11: `LogTab` becomes a thin wrapper over `LogView`

**Files:**
- Modify: `web/src/jobs/LogTab.tsx` (whole file)
- Modify: `web/src/jobs/LogTab.test.tsx` (whole file)

- [ ] **Step 1: Write the failing test**

Replace `web/src/jobs/LogTab.test.tsx` in full:

```tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { LogTab } from './LogTab'
import type { LogRow } from './logBuffer'
import type { TaskLogStreamResult } from './useTaskLogStream'

function row(key: number, text: string, over: Partial<LogRow> = {}): LogRow {
  return { key, kind: 'line', stream: 'stdout', text, time: '2026-07-01T00:00:00Z', ...over }
}

function streamOf(over: Partial<TaskLogStreamResult> = {}): TaskLogStreamResult {
  return {
    rows: [row(1, 'building'), row(2, 'warning: x', { stream: 'stderr' })],
    status: 'live',
    attempt: 0,
    dropped: false,
    evicted: false,
    historyTruncated: false,
    total: 2,
    errorMessage: '',
    reconnect: () => {},
    ...over,
  }
}

function renderTab(stream = streamOf(), taskId = 't2') {
  return render(
    <MemoryRouter>
      <LogTab jobId="j1" taskId={taskId} stream={stream} />
    </MemoryRouter>,
  )
}

test('renders log lines with a stdout/stderr distinction', () => {
  renderTab()
  expect(screen.getByText('building')).toBeInTheDocument()
  expect(screen.getByText('warning: x').className).toMatch(/text-err/)
})

// Replaces the old 'shows a STATIC history marker ... never a LIVE badge' case:
// live tailing has shipped, so the honest signal is now the inverse.
test('shows a LIVE badge while the stream is open and no STATIC marker', () => {
  renderTab()
  expect(screen.getByText('LIVE')).toBeInTheDocument()
  expect(screen.queryByText(/static/i)).toBeNull()
  expect(screen.queryByText(/live tailing pending/i)).toBeNull()
})

test('does not show LIVE for a terminal task', () => {
  renderTab(streamOf({ status: 'history' }))
  expect(screen.queryByText('LIVE')).toBeNull()
  expect(screen.getByText('HISTORY')).toBeInTheDocument()
})

test('shows the empty state when there is no output', () => {
  renderTab(streamOf({ rows: [], status: 'history' }))
  expect(screen.getByText(/no log output/i)).toBeInTheDocument()
})

test('shows a retry control on error', () => {
  renderTab(streamOf({ rows: [], status: 'error', errorMessage: 'boom' }))
  expect(screen.getByText(/failed to load logs/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
})

test('links to the full-screen view for the selected task', () => {
  renderTab()
  expect(screen.getByRole('link', { name: /full screen/i })).toHaveAttribute(
    'href',
    '/jobs/j1/tasks/t2',
  )
})

test('omits the full-screen link when no task is selected', () => {
  renderTab(streamOf({ rows: [], status: 'idle' }), '')
  expect(screen.queryByRole('link', { name: /full screen/i })).toBeNull()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/LogTab.test.tsx`
Expected: FAIL to type/render - `LogTab` still expects `items`/`isLoading`/`isError`/`onRetry`, so nothing renders and `getByText('building')` throws.

- [ ] **Step 3: Write the implementation**

Replace `web/src/jobs/LogTab.tsx` in full:

```tsx
import { Link } from 'react-router-dom'
import { LogView } from './LogView'
import type { TaskLogStreamResult } from './useTaskLogStream'

// The job-detail Log pane: LogView plus a link to the full-screen view. All log
// state lives in useTaskLogStream, which JobDetailPage mounts - this component is
// purely presentational, and that is what keeps the "exactly one SSE connection
// per page" guarantee structural rather than a convention (spec Decision 8).
export function LogTab({
  jobId,
  taskId,
  stream,
}: {
  jobId: string
  taskId: string
  stream: TaskLogStreamResult
}) {
  return (
    <LogView
      stream={stream}
      headerExtra={
        taskId ? (
          <Link
            to={`/jobs/${jobId}/tasks/${taskId}`}
            className="tracking-[0.14em] text-accent hover:underline"
          >
            FULL SCREEN
          </Link>
        ) : null
      }
    />
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/LogTab.test.tsx`
Expected: PASS, 7 tests. `npx vitest run src/jobs/JobDetailPage.test.tsx` will now FAIL to compile - that is expected and Task 12 fixes it.

- [ ] **Step 5: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/LogTab.tsx web/src/jobs/LogTab.test.tsx
git commit -m "feat(web): LogTab renders the live LogView and links to the full-screen log"
```

---

## Task 12: wire `JobDetailPage` and delete `useTaskLogs`

The change most likely to produce an unrelated-looking diff, so it gets its own commit (spec, Risks).

**Files:**
- Modify: `web/src/jobs/JobDetailPage.tsx:14,44-46,179-185`
- Modify: `web/src/jobs/JobDetailPage.test.tsx`
- Delete: `web/src/jobs/useTaskLogs.ts`, `web/src/jobs/useTaskLogs.test.tsx`

- [ ] **Step 1: Write the failing test**

In `web/src/jobs/JobDetailPage.test.tsx`, add `openSseResponse` to the imports:

```tsx
import { openSseResponse } from '../test/sseStream'
```

Replace the `does NOT hit the log endpoint while the Spec tab is active` test (`:93-106`) with:

```tsx
test('does NOT hit the log endpoint or open a stream while the Spec tab is active', async () => {
  let logCount = 0
  let streamCount = 0
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/:tid/logs', () => {
      logCount++
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  server.use(
    http.get('/v1/events', () => {
      streamCount++
      return openSseResponse()
    }),
  )
  renderDetail()
  await screen.findByText('shot-042 render')
  await new Promise((r) => setTimeout(r, 60))
  expect(logCount).toBe(0)
  expect(streamCount).toBe(0)
})
```

Replace `switching to the Log tab fetches once and renders lines` (`:108-126`) with:

```tsx
test('switching to the Log tab subscribes once, backfills once, and renders lines', async () => {
  let logCount = 0
  let streamUrl = ''
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/t2/logs', () => {
      logCount++
      return HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'rendering\n', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      })
    }),
  )
  server.use(
    http.get('/v1/events', ({ request }) => {
      streamUrl = request.url
      return openSseResponse()
    }),
  )
  renderDetail()
  await screen.findByText('shot-042 render')
  await userEvent.click(screen.getByRole('tab', { name: /log/i }))
  expect(await screen.findByText('rendering')).toBeInTheDocument()
  expect(logCount).toBe(1)
  // t2 is the default selected task (the first running one) and is not terminal,
  // so exactly one ?task_id= subscription is opened - and no token in the URL.
  expect(streamUrl).toContain('task_id=t2')
  expect(streamUrl).not.toMatch(/token|access_token/)
})
```

Replace `the Log tab shows a static/history marker, not a LIVE badge` (`:192-209`) with:

```tsx
test('the Log tab shows LIVE for a running task and HISTORY for a terminal one', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/:tid/logs', () =>
      HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'rendering\n', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      }),
    ),
  )
  server.use(http.get('/v1/events', () => openSseResponse()))
  renderDetail()
  await screen.findByText('shot-042 render')
  await userEvent.click(screen.getByRole('tab', { name: /log/i }))
  // t2 (running) tails live.
  expect(await screen.findByText('LIVE')).toBeInTheDocument()
  expect(screen.queryByText(/static/i)).toBeNull()

  // t1 is `done`: selecting it must open NO stream and settle to HISTORY.
  const frameRow = screen
    .getAllByRole('row')
    .find((r) => r.textContent?.startsWith('frame-001'))!
  await userEvent.click(frameRow)
  expect(await screen.findByText('HISTORY')).toBeInTheDocument()
  expect(screen.queryByText('LIVE')).toBeNull()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/JobDetailPage.test.tsx`
Expected: FAIL - `JobDetailPage` still passes `items`/`isLoading`/`isError`/`onRetry` to `LogTab`, so it does not compile.

- [ ] **Step 3: Write the implementation**

In `web/src/jobs/JobDetailPage.tsx`:

1. Replace the import at `:14` with:
   ```tsx
   import { useTaskLogStream } from './useTaskLogStream'
   ```
2. Add `isTerminalTask` to the `./taskStatus` imports (add the import line if the file does not import from it yet):
   ```tsx
   import { isTerminalTask } from './taskStatus'
   ```
3. Replace the comment and hook call at `:44-46` with:
   ```tsx
   // Log state lives in the hook, not the query cache (spec Decision 3), so a job
   // poll can never disturb it and no log line ever enters TanStack. `live` comes
   // from useJob's poll: a ?task_id= subscription has no terminal signal of its
   // own (README.md:1310-1313), and the selected task's status reaching a terminal
   // value within one poll interval is what tells us to stop tailing. A terminal
   // task therefore opens no connection at all, and leaving the Log tab flips
   // `enabled` false, which tears the connection down.
   const logStream = useTaskLogStream(selectedTaskId, {
     live: !isTerminalTask(selectedTask?.status),
     enabled: selectedTaskId !== '' && tab === 'log',
   })
   ```
4. Replace the `<LogTab ... />` element at `:179-185` with:
   ```tsx
   <LogTab jobId={job.id} taskId={selectedTaskId} stream={logStream} />
   ```

Then delete the superseded hook and its test:

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git rm web/src/jobs/useTaskLogs.ts web/src/jobs/useTaskLogs.test.tsx
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/JobDetailPage.test.tsx`
Expected: PASS. All pre-existing cases in that file still pass unchanged.

Run: `npm test`
Expected: PASS, whole suite.

Run: `npx tsc -b`
Expected: no output. This is what proves nothing still imports the deleted `useTaskLogs`.

- [ ] **Step 5: Prove the gating test is not vacuous**

Temporary mutation: change the hook's `enabled` argument to `selectedTaskId !== ''` (dropping the `tab === 'log'` clause).
Expected: `does NOT hit the log endpoint or open a stream while the Spec tab is active` FAILS with `logCount` 1 and `streamCount` 1. Revert.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/JobDetailPage.tsx web/src/jobs/JobDetailPage.test.tsx
git commit -m "feat(web): job detail Log tab tails live; delete useTaskLogs"
```

---

## Task 13: `TaskLogPage` at `/jobs/:id/tasks/:taskId`

The route is keyed by task **UUID**, not an index. There is no task ordinal column, every task of a job shares one `created_at`, and `ORDER BY created_at` has no tiebreaker, so a bookmarked `/jobs/:id/tasks/3` would drift to a different task mid-job (spec, finding 2).

**Files:**
- Create: `web/src/jobs/TaskLogPage.tsx`
- Modify: `web/src/app/router.tsx:6,26`
- Test: `web/src/jobs/TaskLogPage.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/TaskLogPage.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { openSseResponse } from '../test/sseStream'
import { clearToken, setToken } from '../lib/token'
import { AppRoutes } from '../app/router'
import { AuthProvider } from '../auth/AuthProvider'

const JOB = {
  id: 'j1',
  name: 'shot-042 render',
  priority: 'high',
  status: 'running',
  submitted_by: 'u1',
  labels: null,
  created_at: '2026-08-09T00:00:00Z',
  updated_at: '2026-08-09T00:00:00Z',
  tasks: [
    {
      id: 't1', name: 'frame-001', status: 'running',
      commands: [['blender', '-b']], env: null, requires: null,
      timeout_seconds: null, retries: 1, retry_count: 0, worker_id: 'w1abcdef',
    },
  ],
}

function renderRoute(path: string) {
  setToken('test-token')
  server.use(http.get('/v1/users/me', () => HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: false })))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('the /jobs/:id/tasks/:taskId route renders the header and tails the task', async () => {
  server.use(http.get('/v1/jobs/j1', () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'rendering\n', created_at: '2026-08-09T14:36:25Z' }],
        next_seq: 0,
        total: 1,
      }),
    ),
  )
  server.use(http.get('/v1/events', () => openSseResponse()))

  renderRoute('/jobs/j1/tasks/t1')
  expect(await screen.findByText('frame-001')).toBeInTheDocument()
  expect(await screen.findByText('rendering')).toBeInTheDocument()
  // Hi-fi chrome (hifi3-holo-pages.jsx:2716-2745): breadcrumb, status pill,
  // worker, endpoint caption, LIVE badge, follow-tail.
  expect(screen.getByRole('link', { name: /job detail/i })).toHaveAttribute('href', '/jobs/j1')
  expect(screen.getByText('running')).toBeInTheDocument()
  expect(screen.getByText(/w1abcd/)).toBeInTheDocument()
  expect(screen.getByText('/v1/events?task_id=t1 · single-task stream')).toBeInTheDocument()
  expect(screen.getByText('LIVE')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /follow tail/i })).toBeInTheDocument()
  // The Download button of the hi-fi is deliberately omitted (spec, Omissions).
  expect(screen.queryByRole('button', { name: /download/i })).toBeNull()
})

test('a task id that is not in the job renders a not-found panel and opens no stream', async () => {
  let streamCount = 0
  server.use(http.get('/v1/jobs/j1', () => HttpResponse.json(JOB)))
  server.use(http.get('/v1/events', () => { streamCount++; return openSseResponse() }))
  server.use(http.get('/v1/tasks/:tid/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })))

  renderRoute('/jobs/j1/tasks/does-not-exist')
  expect(await screen.findByText(/task not found in this job/i)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /job detail/i })).toBeInTheDocument()
  await new Promise((r) => setTimeout(r, 60))
  expect(streamCount).toBe(0)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/jobs/TaskLogPage.test.tsx`
Expected: FAIL - the route does not exist, so `AppRoutes`'s `path="*"` catch-all redirects to `/jobs` and `findByText('frame-001')` times out.

- [ ] **Step 3: Write the implementation**

Create `web/src/jobs/TaskLogPage.tsx`:

```tsx
import { Link, useParams } from 'react-router-dom'
import { GlassPanel } from '../components/holo'
import { LogView } from './LogView'
import { useJob } from './useJob'
import { useTaskLogStream } from './useTaskLogStream'
import { isTerminalTask, taskStatusColor } from './taskStatus'

// Full-screen single-task log. The route is keyed by task UUID, not by an index:
// there is no task ordinal column, every task of a job shares one created_at
// (internal/api/jobs.go:202-209 inserts them in one transaction where NOW() is
// constant) and ORDER BY created_at has no tiebreaker, so a bookmarked
// /jobs/:id/tasks/3 would drift to a different task mid-job.
//
// It reuses useJob for the header (which also supplies the terminal signal) plus
// useTaskLogStream and LogView unchanged, so there is exactly one hook instance
// on this page. The hi-fi's Download button is deliberately omitted (spec,
// Omissions): it needs a full history the page cap does not fetch, and it is the
// affordance most likely to move secret-bearing output onto disk.
export function TaskLogPage() {
  const { id = '', taskId = '' } = useParams()
  const { data: job, isLoading } = useJob(id)
  const task = job?.tasks.find((t) => t.id === taskId)

  // enabled stays false until the job's task list confirms the task exists, so a
  // bad URL never opens a connection.
  const stream = useTaskLogStream(taskId, {
    live: task !== undefined && !isTerminalTask(task.status),
    enabled: task !== undefined,
  })

  if (isLoading && !job) return <GlassPanel className="h-40" />

  if (!task) {
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        <div className="text-[13px] text-fg-mute">Task not found in this job.</div>
        <div className="mt-4">
          <Link to={`/jobs/${id}`} className="font-mono text-[11px] text-accent">
            &larr; Job detail
          </Link>
        </div>
      </GlassPanel>
    )
  }

  const c = taskStatusColor(task.status)

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2.5">
        <Link to={`/jobs/${id}`} className="font-mono text-[11px] text-fg-mute hover:text-fg">
          &larr; Job detail
        </Link>
        <span className="text-fg-dim">/</span>
        <span className="font-mono text-[12px] text-fg-mute">{id.slice(0, 8)}</span>
        <span className="text-fg-dim">/</span>
        <span className="font-mono text-[12px] text-accent">{taskId.slice(0, 8)}</span>
        <h1 className="text-[16px] font-normal tracking-tight">{task.name}</h1>
        <span className={`flex items-center gap-2 font-mono text-[12px] ${c.text}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
          {task.status}
        </span>
        <span className="font-mono text-[11px] text-fg-mute">
          worker {task.worker_id ? task.worker_id.slice(0, 6) : '-'} · retry {task.retry_count}/{task.retries}
        </span>
      </div>

      <GlassPanel className="flex min-h-0 flex-1 flex-col">
        <LogView
          stream={stream}
          endpointCaption={`/v1/events?task_id=${taskId} · single-task stream`}
          bodyClassName="min-h-0 flex-1 overflow-auto"
        />
      </GlassPanel>
    </div>
  )
}
```

Add `isTerminalTask` and `taskStatusColor` are both in `web/src/jobs/taskStatus.ts`, so the single import above is correct.

In `web/src/app/router.tsx`, add the import after line 6:

```tsx
import { TaskLogPage } from '../jobs/TaskLogPage'
```

and the route immediately after the `/jobs/:id` route (line 26):

```tsx
        <Route path="/jobs/:id/tasks/:taskId" element={<TaskLogPage />} />
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/jobs/TaskLogPage.test.tsx`
Expected: PASS, 2 tests.

Run: `npm test`
Expected: PASS, whole suite.

- [ ] **Step 5: Prove the not-found test is not vacuous**

Temporary mutation: change the hook's `enabled` argument to `taskId !== ''`.
Expected: `a task id that is not in the job renders a not-found panel and opens no stream` FAILS on `expect(streamCount).toBe(0)`. Revert.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/TaskLogPage.tsx web/src/jobs/TaskLogPage.test.tsx web/src/app/router.tsx
git commit -m "feat(web): full-screen task log view at /jobs/:id/tasks/:taskId"
```

---

## Task 14: log content never reaches a console, and the whole-plan verification gate

**Files:**
- Create: `web/src/jobs/logSecrecy.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/logSecrecy.test.tsx`:

```tsx
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { fakeSseServer } from '../test/sseStream'
import { useTaskLogStream } from './useTaskLogStream'

// Log content is raw subprocess stdout/stderr: P4 paths, hostnames, env-derived
// values, and anything a user's own script echoed, including credentials. Browser
// consoles are captured by extensions and screen-shared, so no console method may
// ever receive it. Error paths log nothing at all.
test('no console method ever receives log content, across mount-stream-drop-unmount', async () => {
  const SECRET = 'P4PASSWD=hunter2-never-log-me'
  const methods = ['log', 'info', 'warn', 'error', 'debug', 'trace'] as const
  const spies = methods.map((m) => vi.spyOn(console, m).mockImplementation(() => {}))

  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/t1/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })),
  )
  const { result, unmount } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))

  const conn = fake.latest()
  conn.emit('task_log', {
    task_id: 't1',
    job_id: 'j1',
    seq: 1,
    stream: 'stdout',
    content: `${SECRET}\n`,
    created_at: '2026-08-09T00:00:00Z',
  })
  // Positive control: the content really did flow through the code path under
  // test. Without this, a broken transport would make the absence assertion pass.
  await waitFor(() => expect(result.current.rows.some((r) => r.text === SECRET)).toBe(true))

  conn.emit('dropped', { reason: 'slow_consumer' })
  conn.close()
  await waitFor(() => expect(result.current.dropped).toBe(true))
  unmount()

  for (const spy of spies) {
    for (const call of spy.mock.calls) {
      expect(JSON.stringify(call)).not.toContain('hunter2')
    }
  }
  spies.forEach((s) => s.mockRestore())
})
```

- [ ] **Step 2: Run the test to verify it fails, then passes**

First prove it can fail. Temporarily add `console.debug(frame.data)` inside `streamTaskLog`'s `task_log` branch in `web/src/jobs/api.ts`.

Run: `npx vitest run src/jobs/logSecrecy.test.tsx`
Expected: FAIL on `expect(JSON.stringify(call)).not.toContain('hunter2')`. **If it passes with that line in place, the test is vacuous and must be fixed.**

Remove the `console.debug` line and re-run: PASS.

- [ ] **Step 3: Verify no `useQuery` holds log lines (acceptance criterion 12)**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web
grep -rn "task-logs\|useTaskLogs" src/ || echo "clean"
grep -rn "dangerouslySetInnerHTML" src/ || echo "clean"
grep -rn "EventSource\|access_token" src/ || echo "clean"
```
Expected: `clean` for all three. Any hit is a defect: the first means a stale reference to the deleted query, the second is an XSS hole, the third is a rejected transport or a credential in a URL.

- [ ] **Step 4: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git add web/src/jobs/logSecrecy.test.tsx
git commit -m "test(web): assert log content never reaches a console method"
```

---

## Whole-plan verification gate

Run all of this before declaring the plan done. **This is not optional and not delegable to the unit suite alone** - a green Vitest run cannot tell you whether a real browser tails a real backend.

- [ ] **1. Full unit suite green**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web
npm test
```
Expected: all files pass, zero skipped. Confirm the new files are in the run: `sse.test.ts`, `logBuffer.test.ts`, `api.stream.test.ts`, `api.test.ts`, `useTaskLogStream.test.tsx`, `LogView.test.tsx`, `LogTab.test.tsx`, `TaskLogPage.test.tsx`, `logSecrecy.test.tsx`, `JobDetailPage.test.tsx`. Confirm `useTaskLogs.test.tsx` is gone.

- [ ] **2. Type check and production build clean**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web
npm run build
```
Expected: `tsc -b` silent, `vite build` succeeds.

- [ ] **3. Revert the build output immediately**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git checkout -- web/dist/
git status --porcelain
```
Expected: `web/dist` clean. **`web/dist` is tracked but stale from the scaffold; a build dirties it and it must never be committed from this plan.**

- [ ] **4. Browser check against a real backend**

This is the only step that exercises the real transport end to end - MSW is an interception layer and cannot prove that a real `text/event-stream` response streams.

```bash
# Terminal 1: Postgres
docker run --rm -p 5432:5432 -e POSTGRES_PASSWORD=relay -e POSTGRES_DB=relay postgres:16

# Terminal 2: server (from the worktree root)
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
make build
./bin/relay-server

# Terminal 3: agent
./bin/relay-agent

# Terminal 4: the SPA dev server (it proxies /v1 to :8080 - web/vite.config.ts:8-12)
cd web && npm run dev
```

Then, in a browser:
1. Sign in, submit a job whose task emits output slowly and in more than one chunk - for example a task whose command loops `echo` with a sleep, or any long-running build.
2. Open the job, switch to the **Log** tab. Confirm: the badge reads `LIVE` with a green dot; **new lines appear without a reload**; the time column is populated; a line printed without a trailing newline shows as a provisional row and then merges when the newline arrives.
3. Open DevTools -> Network -> the `events?task_id=` request. Confirm it stays open with `Content-Type: text/event-stream`, that the `Authorization: Bearer` header is present on the **request**, and that **no token appears anywhere in the URL**.
4. Switch the selected task in the Tasks table. Confirm in Network that the old `events` request terminates and exactly one new one opens.
5. Switch to the **Spec** tab. Confirm the `events` request terminates and no new one opens.
6. Open the full-screen view via the `FULL SCREEN` link. Confirm the URL is `/jobs/<uuid>/tasks/<uuid>`, the breadcrumb, status pill, worker and endpoint caption render, and the tail continues.
7. Scroll up in the log body. Confirm `Follow tail` switches off and `Jump to latest` appears; click it and confirm the view returns to the bottom and resumes following.
8. Kill `relay-server` (Ctrl-C). Confirm the badge goes to `RECONNECTING (1/5)`, then climbs, and after 5 attempts settles on `DISCONNECTED` with a working `Reconnect` button. Restart the server and click `Reconnect`; confirm history is re-fetched and the drop marker row remains.
9. Let the task finish. Confirm the badge settles to `ENDED`, the `events` request in Network has terminated, and exactly one final `/logs` request fired.
10. Confirm the DevTools **Console** contains no log-line content at any point in the above.

- [ ] **5. Confirm the changed file set**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f
git status --porcelain
git diff --stat main...HEAD -- . ':(exclude)web/src' ':(exclude)docs'
```
Expected: the second command prints **nothing**. Acceptance criterion 14: no file outside `web/src/` and `docs/` changes. In particular: zero `.go` files, zero `.sql` files, no `internal/` change, no `README.md` change, no `web/package.json` change (no new dependency), no `web/dist` change.

---

## Self-review: spec coverage

| Spec requirement | Where |
|---|---|
| Acceptance 1 - Log tab tails live, `LIVE` only while a stream is open | Tasks 7-9 (hook), 10 (`statusLabel`), 11-12 (wiring + tests) |
| Acceptance 2 - full-screen view at `/jobs/:id/tasks/:taskId` reusing the hook and `LogView` | Task 13 |
| Acceptance 3 - bearer header via `fetch` + `ReadableStream`, no token in a URL, no Go change | Task 5; grep in Task 14 Step 3; gate step 5 |
| Acceptance 4 - gapless, duplicate-free join, ordering proven RED | Task 7 Steps 1 and 5 |
| Acceptance 5 - nothing reacts to a `seq` gap | Task 2 (test + RED proof mutation 2) |
| Acceptance 6 - `dropped` and close both produce a permanent marker and one re-backfill | Tasks 4 (`markDropped`), 8 (`recover`, RED proofs 3 and 4) |
| Acceptance 7 - bounded retry 1/2/4/8/15 s, max 5, proven-connection reset | Task 8 |
| Acceptance 8 - one connection per page, N switches open N, terminal opens none, teardown on tab/unmount | Task 9; Task 12's Spec-tab test; Task 13's not-found test |
| Acceptance 9 - 10 pages of 200, 2000 lines drop-oldest, notice with real counts | Tasks 3-4 (cap), 7 (page cap), 10 (notices) |
| Acceptance 10 - multi-line entry, straddling line, dangling partial, ANSI | Task 3 |
| Acceptance 11 - no console, no storage, no HTML | Task 14; Task 10 RED proof 2; the hook's doc comment |
| Acceptance 12 - `useTaskLogs` deleted, no `useQuery` holds log lines | Task 12; Task 14 Step 3 |
| Acceptance 13 - `npm test`, `tsc -b`, `git checkout -- web/dist/` | Gate steps 1-3 |
| Acceptance 14 - nothing outside `web/src/` and `docs/` | Gate step 5 |
| Decision 1 transport, `onUnauthorized` on a streaming 401, non-ok before body | Task 5 |
| Decision 2 one `?task_id=` connection, terminal signal from `useJob` | Task 6 (`streamTaskLog`, `isTerminalTask`), 9, 12 |
| Decision 3 state in the hook, not the cache | Task 7 |
| Decision 4 the gapless join, `maxSeq` and the buffer in refs | Task 7 |
| Decision 5 line reassembly, CR collapse, ANSI strip, no `dangerouslySetInnerHTML` | Tasks 3, 10 |
| Decision 6 recovery table, bounded retry, permanent marker, status vocabulary | Tasks 8, 10 |
| Decision 7 three bounds, the oldest-2000 notice text verbatim, no virtualization | Tasks 4, 7, 10; scope guard |
| Decision 8 file map, connection-count guarantee | File Structure; Tasks 9, 11, 13 |
| Omissions - all six proposed follow-ups excluded | Scope guard in the main plan file |

**Calls made where the spec was underspecified** (all recorded rather than silently resolved):

1. **`LogTab`/`LogView` prop shape.** The spec names both components but not their interfaces. `LogView` takes the whole `TaskLogStreamResult` as one `stream` prop rather than 10 spread props, so adding a field to the hook cannot drift the view's signature, and `LogTab` becomes a 20-line wrapper.
2. **Where the hook is mounted.** The spec's Decision 8 says `JobDetailPage` and `TaskLogPage`. Mounting it inside `LogTab` would work identically and would be shorter, but the plan follows the spec so the connection-count guarantee stays legible in the page component, and so `enabled` keeps mirroring the existing gate at `JobDetailPage.tsx:46`.
3. **The `historyTruncated` vs `evicted` notice precedence.** The spec gives both notice texts but not what happens when both are true. `evicted` wins, because "Earlier output not shown." is the more accurate statement once drop-oldest has bitten. Noted in a code comment.
4. **The terminal-transition seed.** The spec asks for one `?since_seq=<maxSeq>` reconciliation page when a live task ends, but the effect necessarily re-runs when `live` flips. A `carry` ref keyed on `taskId` seeds that re-run, which is the only way to get one page instead of a full re-backfill without moving `live` out of the deps.
5. **Timestamp formatting.** The spec says the row shows the `created_at` time; it does not say which zone. UTC `HH:MM:SS` via `toISOString`, so the mono column is fixed-width and the tests are deterministic.
6. **Backoff jitter.** The spec says the planner *may* add it. Not added: 5 attempts capped at 15 s is already bounded, and one fewer moving part is worth more than smoothing a thundering herd that is capped at six requests per tab.
