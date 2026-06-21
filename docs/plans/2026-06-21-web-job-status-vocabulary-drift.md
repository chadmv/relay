# Web Job-Status Vocabulary Drift Fix Implementation Plan

> Conductor-authored plan (autopilot). REQUIRED SUB-SKILL for the implementer:
> superpowers:test-driven-development. Steps use `- [ ]` checkboxes.

**Goal:** Make the SPA's `JobStatus` model and the Jobs "Queued" filter match the real
`jobs.status` vocabulary (`pending`/`running`/`done`/`failed`/`cancelled`, constraint-locked by
migration 000019), removing the dead `queued`/`dispatched`/`timed_out` cases and making the
"Queued" filter actually match jobs.

**Root cause:** `jobs.status` never holds `queued`/`dispatched`/`timed_out` (those are partly
`tasks.status` values, deliberately distinct). The SPA still models them, and the "Queued" filter
sends `status=queued`, which can never match a job. The public `JobStats.queued` JSON field (counts
`pending` jobs) is a SEPARATE, intentional wire name and is OUT OF SCOPE.

**Fix:** narrow the `JobStatus` union; drop the dead `statusColor` switch cases and the dead
`JobsTable` `timed_out` branch; change the "Queued" filter to send `status=pending` (keeping the
"Queued" label, consistent with the stats strip that already labels the pending count "QUEUED").

**Slice:** Frontend-only (`web/src/jobs/*`). No backend/API change. `JobStats` and `getJobStats`
are untouched.

## Confirmed references (grep over web/src)

- `web/src/jobs/api.ts:3-11` - `JobStatus` union includes `'queued' | 'dispatched' | 'timed_out'`.
- `web/src/jobs/status.ts:11-27` - `statusColor` switch has `dispatched` (grouped with `running`),
  `queued` (grouped with `pending`), `timed_out` (grouped with `failed`); doc comment lines 8-10
  describe the old vocabulary.
- `web/src/jobs/JobsPage.tsx:12` - `{ key: 'queued', label: 'Queued', status: 'queued' }`.
- `web/src/jobs/JobsTable.tsx:50` - `... j.status === 'failed' || j.status === 'timed_out' ? 'bg-err' : 'bg-accent'`.
- `web/src/jobs/status.test.ts:7,8,11` - assertions for `statusColor('dispatched'|'queued'|'timed_out')`.

After narrowing the union, the `status.ts` cases, the `JobsTable` `timed_out` comparison, and the
three `status.test.ts` assertions become TypeScript "no overlap" / "not assignable" errors - so they
MUST all be removed together for `tsc` to pass. That is the safety net proving completeness.

---

## Task 1: Behavioral RED test - the "Queued" filter must request `status=pending`

**Files:** `web/src/jobs/JobsPage.test.tsx`

- [ ] **Step 1 (RED):** Add (or adapt the existing status-chip test) a test that clicks the
  "Queued" filter chip and asserts the resulting jobs request carries `status=pending` (mirror the
  existing "selecting a status chip re-requests with status" test, which checks
  `requests.some((q) => q.get('status') === 'running')`). Against current code this FAILS because the
  filter sends `status=queued`. Run `npm test -- JobsPage` (in `web/`), capture the RED, commit.

## Task 2: Narrow the model and the filter, drop dead cases (GREEN)

**Files:** `web/src/jobs/api.ts`, `web/src/jobs/status.ts`, `web/src/jobs/JobsPage.tsx`,
`web/src/jobs/JobsTable.tsx`, `web/src/jobs/status.test.ts`

- [ ] **Step 1:** `api.ts` - narrow the union to:
  ```ts
  export type JobStatus = 'pending' | 'running' | 'done' | 'failed' | 'cancelled'
  ```
- [ ] **Step 2:** `status.ts` - remove the `case 'dispatched':`, `case 'queued':`, `case 'timed_out':`
  lines (keep `running`→accent, `pending`→warn, `failed`→err, `done`→ok, `default`→fg-mute for
  `cancelled`/unknown). Update the doc comment (lines 8-10) to the real vocabulary - drop the
  `dispatched`/`queued`/`timed_out` mentions.
- [ ] **Step 3:** `JobsPage.tsx:12` - change the filter to `{ key: 'queued', label: 'Queued', status: 'pending' }`
  (keep key+label; only the sent `status` changes). Confirm this makes Task 1 GREEN.
- [ ] **Step 4:** `JobsTable.tsx:50` - drop the dead `|| j.status === 'timed_out'` so it reads
  `... j.status === 'failed' ? 'bg-err' : 'bg-accent'`.
- [ ] **Step 5:** `status.test.ts` - remove the three assertions for `'dispatched'`, `'queued'`,
  `'timed_out'` (lines 7,8,11). Keep `pending`→warn, `running`→accent, `done`→ok, `failed`→err,
  `cancelled`→fg-mute (add `running`/`done`/`failed` assertions if not already present so coverage of
  the real vocabulary stays complete).
- [ ] **Step 6:** Run `npm test` (whole web suite) and `tsc -b && vite build` (or the project's build
  script) - all green; `tsc` passing is the proof that no dead `queued`/`dispatched`/`timed_out`
  reference remains anywhere. Commit.

## Task 3: Verify

- [ ] `npm test` green; build/typecheck clean.
- [ ] Browser preview (best-effort): the Jobs "Queued" chip now returns pending jobs instead of an
  always-empty list; status dots still render for all real statuses.

## Notes / scope

- Do NOT touch `JobStats`/`getJobStats`/the stats strip - the public `queued` count (pending jobs) is
  an intentional wire name, explicitly out of scope per the backlog item.
- Keep it surgical: the union, the switch, the one filter value, the one table comparison, and the
  test. No relabeling of the "Queued" chip (it stays consistent with the "QUEUED" stats label).
- After any web build, `git checkout -- web/dist/` before assembling the PR (web/dist is a stale,
  unmaintained scaffold artifact - do not commit a rebuild).
