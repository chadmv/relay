# Task-Log Tail Paging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `GET /v1/tasks/{id}/logs` a descending mode (`?order=desc`, `?before_seq=`, `prev_seq`) and change the SPA log view to open at the tail with one request and walk earlier on demand.

**Architecture:** The endpoint keeps its hand-built `{items, next_seq, total}` envelope and gains a `prev_seq` key. Query-string validation moves into a pure `parseTaskLogQuery` in `package api` so the whole matrix is testable without Docker. Two new sqlc statements do a bounded backward index scan and re-order ascending in an outer select, so "items are always ascending" lives in the SQL and every caller shares one item-building path. The SPA's `useTaskLogStream` keeps its shape: only the FIRST history fetch changes (tail instead of forward-from-zero) and one user-driven continuation, `loadEarlier`, is added, fenced by the existing generation counter.

**Tech Stack:** Go 1.26, sqlc, pgx/v5, testify, testcontainers-go (integration lane). React 19 + TypeScript, vitest + jsdom, MSW, Testing Library.

---

## Slice independence declaration

**SEQUENTIAL. Backend first, then frontend. They are NOT independent and must NOT run in parallel in Phase 3.**

The frontend depends on the backend in two hard ways: `getTaskLogsDesc` calls a query parameter that does not exist until Task 3 ships, and `TaskLogPage.prev_seq` becomes a required field whose only producer is Task 3's handler. A frontend agent starting early would write MSW fixtures for a contract that is still being decided and would have nothing real to run against.

| Task | Owner | Lane |
|---|---|---|
| 1. `parseTaskLogQuery` and its validation matrix | `relay-backend-engineer` | Go default lane |
| 2. The two SQL statements plus `make generate` | `relay-backend-engineer` | build only |
| 3. Handler: branch selection and `prev_seq` | `relay-backend-engineer` | Go integration lane |
| 4. The rest of the wire battery (7 tests) | `relay-backend-engineer` | Go integration lane |
| 5. README | `relay-backend-engineer` | docs |
| 6. `api.ts`: `prev_seq` plus `getTaskLogsDesc` | `relay-frontend-engineer` | vitest |
| 7. `logBuffer`: `minSeq` | `relay-frontend-engineer` | vitest |
| 8. `logBuffer`: `prependEntries` | `relay-frontend-engineer` | vitest |
| 9. `logBuffer`: `preservedScrollTop` | `relay-frontend-engineer` | vitest |
| 10. Hook: open at the tail | `relay-frontend-engineer` | vitest |
| 11. Hook: `loadEarlier` and the generation fence | `relay-frontend-engineer` | vitest |
| 12. `LogView`: the control, the notice, the scroll anchor | `relay-frontend-engineer` | vitest |
| 13. Fixture sweep and the secrecy cycle | `relay-frontend-engineer` | vitest |
| 14. Gates | either | all |

Tasks 1 and 2 are independent of each other; everything else is a chain.

---

## What I refuted or changed while reading the spec against HEAD

Read the spec once for contradiction, then read every file it names. Findings, in the order they matter:

1. **The spec says the handler "runs the EXISTING item loop unchanged; only the cursor computation forks". That is not achievable as written, and following it literally will not compile.** `GetTaskLogsPage` returns `[]store.TaskLog` because it selects a table's columns directly. The two new statements select from a DERIVED TABLE (`FROM ( ... ) AS t`), and sqlc is not guaranteed to map a derived table's columns back onto the `TaskLog` model - it commonly emits a per-query `GetTaskLogsTailPageRow` struct instead. Three different slice types cannot share one `for i, l := range logs` loop. Task 3 therefore uses one `appendRow` closure called from three per-branch loops; the closure takes scalars, so it compiles whatever sqlc names the row struct. Do not "simplify" it back.
2. **That same change carries a silent envelope regression the spec's own criterion 11 would not catch.** Today `items := make([]logEntry, len(logs))` is non-nil, so an empty page serialises as `"items":[]`. An `items` built by `append` from a `var` declaration is nil and serialises as `"items":null`. A raw-key test that only checks key PRESENCE passes on `null`. Task 3 initialises `items := []logEntry{}` and Task 4's envelope test asserts the raw value is `[]`, not merely that the key exists.
3. **The spec's SQL uses positional `$1/$2/$3`. I changed it to `sqlc.arg()` named parameters with explicit casts.** Positional parameters inside a derived table give no guarantee about the Go field names sqlc will emit (`ID`? `BeforeSeq`? `Column2`?), and this plan has to show code that compiles. `ListOverdueAssignedTasks` in this same file already uses `sqlc.arg(max_rows)::int`, so the form is proven in this repo. The generated param structs are then deterministically `TaskID` / `BeforeSeq` / `RowLimit`.
4. **The brief asked for "the non-contiguous seq case" in the default-lane parser table test. A pure parser cannot observe seq contiguity** - it never sees a row. The parser test gets the closest thing it CAN pin (a large, non-offset-shaped `before_seq` such as `94312` is accepted unchanged, so nothing in the parser treats the cursor as an offset), and the real property is `TestTaskLogs_NonContiguousSeqTailIsExactlyTheNewestRowsOfThatTask` in the integration lane, which seeds two tasks interleaved. Both are in the plan.
5. **`canLoadEarlier` gains a fourth condition the spec did not list: `minSeq > 0`.** The spec gives `!earlierComplete && !evicted && lines.length < MAX_LINES`. Between an effect starting and its tail page landing, all three are true while the window is empty, so a "Load earlier" button renders over "Loading logs..." and a click would issue `before_seq=0` (a 400 by D7). `minSeq > 0` means "we actually hold a page whose start we could walk back from".
6. **`earlierComplete` must not be reset unconditionally when the effect re-runs.** A terminal transition and a manual reconnect re-run the effect for the SAME task with a carry, and blanket-resetting would re-enable "Load earlier" on a log the user had already walked to the beginning of. It is reset when `carried === null` (a genuinely fresh window) and in the disabled early return, and set from `prev_seq` on each tail page.
7. Everything else in the spec checked out against HEAD and is implemented as written. Specifically verified: `page[T]`/`buildPage` really are unused by this endpoint (`internal/api/tasks.go:132`, a `map[string]any`); `idx_task_logs_task_id_id` really does exist (`internal/store/migrations/000018_hot_path_indexes.up.sql:13`), so **no migration**; `internal/api/pagination_test.go` really is `package api` with no build tag while `tasks_integration_test.go` is `//go:build integration` in `package api_test`; every SPA log fixture really is a hand-written literal passed to `HttpResponse.json` and none is marshalled through `TaskLogPage`; `web/e2e` really contains no reference to the log surface (searched, zero files); `isAbortSignalRealmMismatch` really is documented in `web/src/lib/api.ts:118` with the fallback present in `apiStream` and absent in `apiFetch`, which is D17's whole evidence.

### The generation-ordering invariant, and the stale "Load earlier" response

CLAUDE.md: end the generation before releasing the resource. `useTaskLogStream` is the file where that rule was rediscovered as a frontend bug, so state the answer explicitly.

**`loadEarlier` releases no resource.** It creates no `AbortController`, aborts nothing, closes nothing and unregisters nothing (D17: `apiFetch` has no realm-mismatch fallback, so handing it a jsdom-constructed signal imports a known landmine into the exact lane this feature is tested in). There is therefore no acquire/release window inside it and no new abort ordering to get wrong. The existing `gen++`-then-`abort()` order in `recover`, in the backfill failure path and in the cleanup is unchanged, and Task 10 moves the backfill failure path into a `failHistory` helper that keeps `fatal = true; gen++; ...; controller.abort()` in that order, in one place, for both the tail branch and the forward branch.

**What happens to a response that lands after the generation moved on:** `loadEarlier` captures `myGen = gen` before issuing the request. Because nothing is aborted, the request always completes and its body is always parsed. After the await it checks, in this order:

- `cancelled` (the effect was torn down: unmount, task switch, tab exit) - return immediately, touching NO state. The new effect run owns `loadingEarlier` and has already set it false in its own body.
- `myGen !== gen` (a drop-recovery, a retry or a manual reconnect bumped the generation inside the SAME effect run) - clear `loadingEarlier` so the control returns to enabled, then return WITHOUT prepending. The click is visibly not lost; the user can click again against the current window.

Only when both checks pass does the page reach `prependEntries`. The failure mode being closed is quiet and specific: a stale earlier page prepended into a different task's window, joined at a seam that does not exist there.

**Pinned by two named tests, both in Task 11:** `'a stale earlier page is discarded'` (a task switch lands mid-flight; the new task's rows never contain the old task's line) and `'a discarded earlier page re-enables the control'` (a drop-recovery bumps the generation mid-flight; `loadingEarlier` is false and `canLoadEarlier` is true again afterwards).

---

## Critical files

Read these before touching anything:

- `internal/api/tasks.go:56-137` - `logEntry` and `handleGetTaskLogs`. The 404-before-400 precedence at :72-79 is deliberate and must survive.
- `internal/store/query/tasks.sql:722-731` - `GetTaskLogsPage` and `CountTaskLogs`. New statements go beside them.
- `internal/store/query/tasks.sql:283-286` - why a derived table needs an alias and qualified columns for sqlc's analyzer.
- `internal/api/pagination_test.go:1-80` - the default-lane test style for a pure helper in `package api`.
- `internal/api/tasks_integration_test.go` - the whole file. `submitTrivialJob`, `firstTaskID`, `seedLogRow`, `newTestServerWithPool` are the seams every new integration test uses.
- `web/src/jobs/useTaskLogStream.ts:112-420` - the effect. The generation discipline at :210-246 and :359-390 is the model.
- `web/src/jobs/logBuffer.ts:146-217, 278-305` - `collapseCR`, `appendEntries`, `markDropped` (its marker row is emitted with `stream: 'stdout'` regardless of the real stream - that is the seam-join trap), `shouldFollow`.
- `web/src/jobs/LogView.tsx:87-199` - the notice, the scroll box and the follow-tail state.
- `CLAUDE.md` - the CRLF procedure under `internal/store/`, the comment policy, and "Where a CLI test goes" (a fixture must never be encoded through the production response struct; hand-write the JSON).

## Files created or modified

| File | Change |
|---|---|
| `internal/api/task_log_query.go` | NEW. `taskLogOrder`, `taskLogQuery`, `parseTaskLogQuery`. |
| `internal/api/task_log_query_test.go` | NEW. `package api`, no build tag. The validation matrix. |
| `internal/store/query/tasks.sql` | Two statements added after `GetTaskLogsPage`. |
| `internal/store/tasks.sql.go` | REGENERATED by `make generate`. Never hand-edited. |
| `internal/api/tasks.go` | `handleGetTaskLogs` rewritten from :81 down. |
| `internal/api/tasks_integration_test.go` | Helpers plus 8 new tests. The two existing tests are untouched. |
| `README.md` | Tasks table row, a new "Task log paging" subsection, a tail variant in the Events backfill recipe. |
| `web/src/jobs/api.ts` | `TaskLogPage.prev_seq`, `getTaskLogsDesc`. |
| `web/src/jobs/logBuffer.ts` | `minSeq`, `prependEntries`, `preservedScrollTop`. |
| `web/src/jobs/useTaskLogStream.ts` | Tail-vs-forward branch, `failHistory`, `loadEarlier`, three new result fields. |
| `web/src/jobs/LogView.tsx` | Load earlier row, notice ordering, prepend scroll anchor. |
| `web/src/jobs/*.test.ts(x)` | New tests plus the `prev_seq` fixture sweep. |

Not touched, deliberately: any migration (D10 and 000018), `internal/cli`, `python/`, `internal/mcp`, `web/e2e`, `web/dist`.

---

## Task 1: `parseTaskLogQuery` (BACKEND)

**Files:**
- Create: `internal/api/task_log_query.go`
- Create: `internal/api/task_log_query_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/task_log_query_test.go`. Note the package: `package api`, NO build tag, so this runs in `make test` without Docker.

```go
package api

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	require.NoError(t, err)
	return v
}

func TestParseTaskLogQuery_DefaultsToAscendingFromZero(t *testing.T) {
	// Every default here is the behaviour handleGetTaskLogs had before the
	// parse was extracted. An absent value and an explicitly empty one resolve
	// the same way, matching how limit and since_seq already treat "".
	for _, raw := range []string{"", "order=", "limit=", "since_seq="} {
		q, err := parseTaskLogQuery(mustQuery(t, raw))
		require.NoError(t, err, "raw=%q", raw)
		assert.Equal(t, taskLogOrderAsc, q.Order, "raw=%q", raw)
		assert.Equal(t, int32(50), q.Limit, "raw=%q", raw)
		assert.Equal(t, int64(0), q.SinceSeq, "raw=%q", raw)
		assert.Equal(t, int64(0), q.BeforeSeq, "raw=%q", raw)
	}
}

func TestParseTaskLogQuery_RejectsAnUnknownOrder(t *testing.T) {
	// An allow-list, not a deny-list: a deny-list fails OPEN on the next value
	// someone adds. The two accepted values travel the same call path in this
	// same table, so the rejections cannot pass by the parser rejecting
	// everything.
	for _, ok := range []struct {
		raw  string
		want taskLogOrder
	}{
		{"order=asc", taskLogOrderAsc},
		{"order=desc", taskLogOrderDesc},
	} {
		q, err := parseTaskLogQuery(mustQuery(t, ok.raw))
		require.NoError(t, err, "raw=%q", ok.raw)
		assert.Equal(t, ok.want, q.Order, "raw=%q", ok.raw)
	}
	for _, bad := range []string{"order=DESC", "order=Asc", "order=descending", "order=-id", "order=1", "order=%20desc"} {
		_, err := parseTaskLogQuery(mustQuery(t, bad))
		require.Error(t, err, "raw=%q", bad)
		assert.Equal(t, "order must be asc or desc", err.Error(), "raw=%q", bad)
	}
}

func TestParseTaskLogQuery_RejectsACursorFromTheWrongDirection(t *testing.T) {
	// Fail closed on a direction-confused client. Silently ignoring a cursor is
	// how a client loops over page 1 forever while believing it is paging.
	_, err := parseTaskLogQuery(mustQuery(t, "order=desc&since_seq=10"))
	require.Error(t, err)
	assert.Equal(t, "since_seq is not valid with order=desc; use before_seq", err.Error())

	// The direction conflict is reported ahead of the value parse, so a client
	// sending both mistakes at once is told the one that explains the other.
	_, err = parseTaskLogQuery(mustQuery(t, "order=desc&since_seq=abc"))
	require.Error(t, err)
	assert.Equal(t, "since_seq is not valid with order=desc; use before_seq", err.Error())

	_, err = parseTaskLogQuery(mustQuery(t, "before_seq=10"))
	require.Error(t, err)
	assert.Equal(t, "before_seq requires order=desc", err.Error())

	_, err = parseTaskLogQuery(mustQuery(t, "order=asc&before_seq=10"))
	require.Error(t, err)
	assert.Equal(t, "before_seq requires order=desc", err.Error())
}

func TestParseTaskLogQuery_RejectsMalformedBeforeSeq(t *testing.T) {
	for _, bad := range []string{"0", "-1", "abc", "1.5", "9223372036854775808"} {
		_, err := parseTaskLogQuery(mustQuery(t, "order=desc&before_seq="+bad))
		require.Error(t, err, "before_seq=%s", bad)
		assert.Equal(t, "before_seq must be a positive integer", err.Error(), "before_seq=%s", bad)
	}
	q, err := parseTaskLogQuery(mustQuery(t, "order=desc&before_seq=1"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), q.BeforeSeq)

	// A cursor is a row id, never an offset. task_logs.id is a table-wide
	// BIGSERIAL, so a large value is ordinary on a busy farm and nothing here
	// may clamp it against total or against the limit.
	q, err = parseTaskLogQuery(mustQuery(t, "order=desc&before_seq=94312&limit=200"))
	require.NoError(t, err)
	assert.Equal(t, int64(94312), q.BeforeSeq)
}

func TestParseTaskLogQuery_LimitClampIsTheSameInBothDirections(t *testing.T) {
	for _, order := range []string{"asc", "desc"} {
		for _, bad := range []string{"0", "201", "-1", "abc"} {
			_, err := parseTaskLogQuery(mustQuery(t, "order="+order+"&limit="+bad))
			require.Error(t, err, "order=%s limit=%s", order, bad)
			assert.Equal(t, "limit must be 1..200", err.Error(), "order=%s limit=%s", order, bad)
		}
		q, err := parseTaskLogQuery(mustQuery(t, "order="+order+"&limit=200"))
		require.NoError(t, err, "order=%s", order)
		assert.Equal(t, int32(200), q.Limit, "order=%s", order)
	}
}

func TestParseTaskLogQuery_DescWithNoCursorIsTheTailRequest(t *testing.T) {
	// The single most common call this feature exists for: "the newest page",
	// with no magic sentinel.
	q, err := parseTaskLogQuery(mustQuery(t, "order=desc&limit=200"))
	require.NoError(t, err)
	assert.Equal(t, taskLogOrderDesc, q.Order)
	assert.Equal(t, int64(0), q.BeforeSeq)
	assert.Equal(t, int64(0), q.SinceSeq)
}

func TestParseTaskLogQuery_AscendingSinceSeqIsUnchanged(t *testing.T) {
	q, err := parseTaskLogQuery(mustQuery(t, "since_seq=41&limit=200"))
	require.NoError(t, err)
	assert.Equal(t, int64(41), q.SinceSeq)
	assert.Equal(t, taskLogOrderAsc, q.Order)

	for _, bad := range []string{"-1", "abc"} {
		_, err := parseTaskLogQuery(mustQuery(t, "since_seq="+bad))
		require.Error(t, err, "since_seq=%s", bad)
		assert.Equal(t, "since_seq must be a non-negative integer", err.Error(), "since_seq=%s", bad)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail for the right reason**

```powershell
cd D:/dev/relay/.claude/worktrees/web-d-tasklog-tail
go test ./internal/api/... -run TestParseTaskLogQuery -v -timeout 60s
```

Expected: a COMPILE failure, `undefined: parseTaskLogQuery`, `undefined: taskLogOrderAsc`, `undefined: taskLogOrderDesc`. That is the right reason; do not proceed if it fails for any other one.

- [ ] **Step 3: Write the implementation**

Create `internal/api/task_log_query.go`:

```go
package api

import (
	"errors"
	"net/url"
	"strconv"
)

// taskLogOrder selects WHICH rows a log page contains, not the order of the
// items inside it: items are ascending by seq in both directions.
type taskLogOrder string

const (
	taskLogOrderAsc  taskLogOrder = "asc"
	taskLogOrderDesc taskLogOrder = "desc"
)

// taskLogQuery is the validated query string of GET /v1/tasks/{id}/logs.
// Exactly one cursor is ever populated: SinceSeq in the ascending direction,
// BeforeSeq in the descending one. BeforeSeq 0 means "no cursor", which in the
// descending direction is the newest page.
type taskLogQuery struct {
	Limit     int32
	Order     taskLogOrder
	SinceSeq  int64
	BeforeSeq int64
}

// parseTaskLogQuery validates the query string and returns a 400-worthy error
// whose message is written to the client verbatim. It is a pure function of the
// query values so the whole cross-parameter matrix is testable without a
// database; the handler calls it AFTER its existence check, preserving the
// 404-before-400 precedence the endpoint has always had.
//
// A cursor for the wrong direction is an error, never an ignored parameter:
// ignoring it leaves a client looping over one page while believing it is
// paging.
func parseTaskLogQuery(v url.Values) (taskLogQuery, error) {
	q := taskLogQuery{Limit: 50, Order: taskLogOrderAsc}

	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 200 {
			return taskLogQuery{}, errors.New("limit must be 1..200")
		}
		q.Limit = int32(n)
	}

	// Allow-list. A deny-list would fail open on the next value added.
	switch s := v.Get("order"); s {
	case "", string(taskLogOrderAsc):
		q.Order = taskLogOrderAsc
	case string(taskLogOrderDesc):
		q.Order = taskLogOrderDesc
	default:
		return taskLogQuery{}, errors.New("order must be asc or desc")
	}

	if s := v.Get("since_seq"); s != "" {
		if q.Order == taskLogOrderDesc {
			return taskLogQuery{}, errors.New("since_seq is not valid with order=desc; use before_seq")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return taskLogQuery{}, errors.New("since_seq must be a non-negative integer")
		}
		q.SinceSeq = n
	}

	if s := v.Get("before_seq"); s != "" {
		if q.Order != taskLogOrderDesc {
			return taskLogQuery{}, errors.New("before_seq requires order=desc")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		// 0 is rejected rather than served as an empty page: it is far more
		// likely an unset client variable than an intention, and the contract
		// says to stop when prev_seq is 0 rather than to send it back.
		if err != nil || n < 1 {
			return taskLogQuery{}, errors.New("before_seq must be a positive integer")
		}
		q.BeforeSeq = n
	}

	return q, nil
}
```

- [ ] **Step 4: Run the test and watch it pass**

```powershell
go test ./internal/api/... -run TestParseTaskLogQuery -v -timeout 60s
go vet ./internal/api/...
```

Expected: 7 tests PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/api/task_log_query.go internal/api/task_log_query_test.go
git commit -m "feat(api): pure parseTaskLogQuery for the task-log query string

Extracted so the cross-parameter matrix is testable in the default lane.
handleGetTaskLogs's first act is a GetTask round trip, so inline validation
can only be exercised through Docker.

No caller yet; the handler is wired in a later commit."
```

---

## Task 2: the two descending statements (BACKEND)

**Files:**
- Modify: `internal/store/query/tasks.sql` (insert after line 728, the end of `GetTaskLogsPage`)
- Regenerate: `internal/store/tasks.sql.go` (NEVER hand-edited)

- [ ] **Step 1: Add the statements**

Insert this immediately after `GetTaskLogsPage` and before `-- name: CountTaskLogs :one`:

```sql
-- name: GetTaskLogsTailPage :many
-- The newest row_limit rows for a task, returned ASCENDING. The inner
-- ORDER BY id DESC is what makes this a bounded backward scan of
-- idx_task_logs_task_id_id instead of a full read of the task's log; the outer
-- ORDER BY is the response contract, since items are ascending in both
-- directions. The subquery alias and the qualified columns are load-bearing for
-- sqlc's analyzer, exactly as in AppendTaskLog.
SELECT t.id, t.task_id, t.stream, t.content, t.created_at
FROM (
    SELECT l.id, l.task_id, l.stream, l.content, l.created_at
    FROM task_logs l
    WHERE l.task_id = sqlc.arg(task_id)
    ORDER BY l.id DESC
    LIMIT sqlc.arg(row_limit)::int
) AS t
ORDER BY t.id;

-- name: GetTaskLogsBeforePage :many
-- The row_limit rows immediately OLDER than before_seq (exclusive), returned
-- ASCENDING. Same backward index scan and same outer re-order as
-- GetTaskLogsTailPage.
SELECT t.id, t.task_id, t.stream, t.content, t.created_at
FROM (
    SELECT l.id, l.task_id, l.stream, l.content, l.created_at
    FROM task_logs l
    WHERE l.task_id = sqlc.arg(task_id) AND l.id < sqlc.arg(before_seq)::bigint
    ORDER BY l.id DESC
    LIMIT sqlc.arg(row_limit)::int
) AS t
ORDER BY t.id;
```

- [ ] **Step 2: Generate**

```powershell
cd D:/dev/relay/.claude/worktrees/web-d-tasklog-tail
make generate
```

If `make` is unavailable, run `sqlc generate` from the same directory. `buf generate` is a no-op here; no `.proto` changed.

- [ ] **Step 3: Do the CRLF revert procedure - this is not optional**

sqlc emits LF and this is a CRLF repo, so it rewrites line endings across every generated file. `git diff` and `git status` DISAGREE by design here (`core.autocrlf=true` makes `git diff` normalize the churn away while `git status` still lists the files). Never conclude "nothing to revert" from `git diff` alone.

```powershell
git status --short
git diff --ignore-all-space -- internal/store/
```

The ONLY real content change should be the two new `const getTaskLogsTailPage` / `getTaskLogsBeforePage` blocks, their param structs and their methods, in `internal/store/tasks.sql.go`. For every OTHER file `git status` lists under `internal/store/`, revert it:

```powershell
git checkout -- internal/store/<file-that-has-no-content-change>.sql.go
```

Then verify the touched paths and, critically, that the revert did not discard the file you actually meant to regenerate:

```powershell
git ls-files --eol internal/store/tasks.sql.go internal/store/query/tasks.sql
git diff --stat -- internal/store/
Select-String -Path internal/store/tasks.sql.go -Pattern "GetTaskLogsTailPage|GetTaskLogsBeforePage" | Select-Object -First 6
```

Expected: `i/lf` on both paths; a diffstat proportionate to two added queries (roughly 90 to 110 added lines in `tasks.sql.go`); and the `Select-String` finding both symbols. If the symbols are gone, the CRLF revert ate the regeneration - re-run `make generate` and revert more carefully.

- [ ] **Step 4: Read the generated signatures and confirm they compile**

```powershell
go build ./...
Select-String -Path internal/store/tasks.sql.go -Pattern "type GetTaskLogsTailPageParams|type GetTaskLogsBeforePageParams" -Context 0,6
```

Expected fields: `GetTaskLogsTailPageParams{TaskID pgtype.UUID; RowLimit int32}` and `GetTaskLogsBeforePageParams{TaskID pgtype.UUID; BeforeSeq int64; RowLimit int32}`. If sqlc named them differently, use the names it actually emitted in Task 3 and note the difference in the commit message. The row struct may be `[]store.TaskLog` or `[]store.GetTaskLogsTailPageRow` - Task 3's code works either way, so do not force it.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go
git commit -m "feat(store): descending task-log page statements

GetTaskLogsTailPage and GetTaskLogsBeforePage: bounded backward scans of
idx_task_logs_task_id_id, re-ordered ascending in an outer select so the
ascending-items response contract lives in one place rather than in each
caller.

No migration: idx_task_logs_task_id_id (000018) already covers the backwards
scan of an equality-prefixed range."
```

---

## Task 3: the handler branches and emits `prev_seq` (BACKEND)

**Files:**
- Modify: `internal/api/tasks.go:81-137`
- Modify: `internal/api/tasks_integration_test.go` (helpers plus two tests)

- [ ] **Step 1: Write the failing tests**

Add to the END of `internal/api/tasks_integration_test.go`. The response is decoded through a LOCALLY declared struct whose json tags are written by hand, deliberately independent of the handler's `map[string]any`, so this test can detect a renamed key in either direction.

```go
// logsPageResp is a hand-written decode of the logs envelope. Its json tags are
// deliberately independent of anything in package api: a fixture or decoder
// derived from the production type agrees with it by construction and can never
// detect drift.
type logsPageResp struct {
	Items []struct {
		Seq       int64  `json:"seq"`
		Stream    string `json:"stream"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	} `json:"items"`
	NextSeq int64 `json:"next_seq"`
	PrevSeq int64 `json:"prev_seq"`
	Total   int64 `json:"total"`
}

func getLogs(t *testing.T, srv *api.Server, token, taskID, query string) (*httptest.ResponseRecorder, logsPageResp) {
	t.Helper()
	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/tasks/%s/logs?%s", taskID, query), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var page logsPageResp
	if rr.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page), "body=%s", rr.Body.String())
	}
	return rr, page
}

func logContents(p logsPageResp) []string {
	out := make([]string, len(p.Items))
	for i, it := range p.Items {
		out[i] = it.Content
	}
	return out
}

func TestTaskLogs_DescendingTailReturnsTheNewestPageInAscendingOrder(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Carol", "carol@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	rr, page := getLogs(t, srv, token, taskID, "order=desc&limit=2")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	// The exact ordered slice, not a length: order=desc selects the NEWEST rows
	// and the items inside the page are ASCENDING. A page returned descending
	// would still be length 2 and would still contain the right rows.
	require.Equal(t, []string{"line 3", "line 4"}, logContents(page))
	require.Equal(t, int64(5), page.Total)
}

func TestTaskLogs_CursorsAreDirectionExclusive(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Dan", "dan@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	// Descending, full page: prev_seq is the page's LOWEST seq, next_seq is 0.
	_, desc := getLogs(t, srv, token, taskID, "order=desc&limit=2")
	require.Len(t, desc.Items, 2)
	require.Equal(t, desc.Items[0].Seq, desc.PrevSeq)
	require.Equal(t, int64(0), desc.NextSeq)

	// Descending, short page: the beginning of the log has been reached.
	_, drained := getLogs(t, srv, token, taskID, "order=desc&limit=200")
	require.Len(t, drained.Items, 5)
	require.Equal(t, int64(0), drained.PrevSeq)
	require.Equal(t, int64(0), drained.NextSeq)

	// Ascending is unchanged and zeroes the descending cursor.
	_, asc := getLogs(t, srv, token, taskID, "limit=2")
	require.Equal(t, []string{"line 0", "line 1"}, logContents(asc))
	require.Equal(t, asc.Items[1].Seq, asc.NextSeq)
	require.Equal(t, int64(0), asc.PrevSeq)
}
```

- [ ] **Step 2: Run the tests and watch them fail for the right reason**

Docker Desktop must be running.

```powershell
go test -tags integration -p 1 ./internal/api/... -run "TestTaskLogs_DescendingTailReturnsTheNewestPageInAscendingOrder|TestTaskLogs_CursorsAreDirectionExclusive" -v -timeout 300s
```

Expected: both FAIL. The tail test fails on `["line 0","line 1"] != ["line 3","line 4"]` (today `order=desc` is an ignored parameter, so the ascending page comes back), and the cursor test fails on `PrevSeq` being 0 where the page's lowest seq is expected (today there is no `prev_seq` key at all, so it decodes as 0). Both are the right reason.

- [ ] **Step 3: Write the implementation**

Replace `internal/api/tasks.go:81-137` (everything from `limit := int32(50)` to the end of the function) with:

```go
	q, qerr := parseTaskLogQuery(r.URL.Query())
	if qerr != nil {
		writeError(w, http.StatusBadRequest, qerr.Error())
		return
	}

	// Built by append from an empty non-nil slice: a nil slice marshals as null,
	// and four clients decode this envelope expecting a list.
	items := []logEntry{}
	appendRow := func(seq int64, stream, content string, at time.Time) {
		items = append(items, logEntry{Seq: seq, Stream: stream, Content: content, CreatedAt: at})
	}

	// One loop per branch because the three statements do not share a row type:
	// the two descending statements select from a derived table, so sqlc may
	// emit a per-query row struct rather than store.TaskLog.
	switch {
	case q.Order == taskLogOrderDesc && q.BeforeSeq > 0:
		rows, err := s.q.GetTaskLogsBeforePage(ctx, store.GetTaskLogsBeforePageParams{
			TaskID:    id,
			BeforeSeq: q.BeforeSeq,
			RowLimit:  q.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get task logs failed")
			return
		}
		for _, l := range rows {
			appendRow(l.ID, l.Stream, l.Content, l.CreatedAt.Time)
		}
	case q.Order == taskLogOrderDesc:
		rows, err := s.q.GetTaskLogsTailPage(ctx, store.GetTaskLogsTailPageParams{
			TaskID:   id,
			RowLimit: q.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get task logs failed")
			return
		}
		for _, l := range rows {
			appendRow(l.ID, l.Stream, l.Content, l.CreatedAt.Time)
		}
	default:
		rows, err := s.q.GetTaskLogsPage(ctx, store.GetTaskLogsPageParams{
			TaskID: id,
			ID:     q.SinceSeq,
			Limit:  q.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get task logs failed")
			return
		}
		for _, l := range rows {
			appendRow(l.ID, l.Stream, l.Content, l.CreatedAt.Time)
		}
	}

	total, err := s.q.CountTaskLogs(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count task logs failed")
		return
	}

	// Each direction populates exactly one cursor and zeroes the other, so a
	// direction-confused client stops immediately instead of looping. A short
	// page has drained that direction: 0 is never a valid seq.
	var nextSeq, prevSeq int64
	if int32(len(items)) >= q.Limit && len(items) > 0 {
		if q.Order == taskLogOrderDesc {
			prevSeq = items[0].Seq
		} else {
			nextSeq = items[len(items)-1].Seq
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"next_seq": nextSeq,
		"prev_seq": prevSeq,
		"total":    total,
	})
}
```

`strconv` is no longer used by `tasks.go`; remove it from the import block. `time` is still used by `logEntry` and by `appendRow`.

- [ ] **Step 4: Run the tests and watch them pass**

```powershell
go build ./...
go vet ./internal/api/...
go test -tags integration -p 1 ./internal/api/... -run "TestTaskLogs_" -v -timeout 300s
```

Expected: the two new tests PASS, and `TestTaskLogs_Pagination` and `TestTaskLogs_LimitClamping` PASS UNMODIFIED. A diff to either of those two is a signal that the change was not additive - stop and re-read the handler.

- [ ] **Step 5: Commit**

```bash
git add internal/api/tasks.go internal/api/tasks_integration_test.go
git commit -m "feat(api): order=desc and before_seq on the task-log endpoint

?order=desc selects the newest rows; ?before_seq= walks earlier from a
prev_seq. Items stay ASCENDING in both directions - order selects WHICH rows,
not their presentation - so every existing consumer's reassembly and dedupe
keeps working unchanged.

The envelope keeps its {items, next_seq, total} shape and gains prev_seq;
migrating it to page[T] would break the CLI, the Python SDK, the SPA and the
README recipe at once for no gain.

items is initialised non-nil: an appended nil slice marshals as null, and the
raw-key envelope test asserts [] rather than mere key presence."
```

---

## Task 4: the rest of the wire battery (BACKEND)

**Files:**
- Modify: `internal/api/tasks_integration_test.go`

These tests assert properties the code from Task 3 already has, so they will be GREEN on first run. That is not evidence. Each step below names the exact mutation that must turn it RED; apply the mutation, run the test, see it fail, restore the file FROM A COPY (never `git checkout --`, which would discard the uncommitted test under construction), and re-run to confirm green. Record the mutations in the commit message, not in comments.

- [ ] **Step 1: Write the tests**

Append to `internal/api/tasks_integration_test.go`:

```go
func TestTaskLogs_NonContiguousSeqTailIsExactlyTheNewestRowsOfThatTask(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Nina", "nina@logs-test.com", false)
	token := createTestToken(t, q, user.ID)

	jobA := submitTrivialJob(t, srv, token)
	taskA := firstTaskID(t, srv, token, jobA)
	jobB := submitTrivialJob(t, srv, token)
	taskB := firstTaskID(t, srv, token, jobB)

	// task_logs.id is a table-wide BIGSERIAL consumed by every task logging
	// concurrently. Interleaving a second task's rows makes A's ids
	// non-contiguous, which is the whole reason "give me the last N" cannot be
	// derived client-side from total or from arithmetic on seq.
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskA, "stdout", fmt.Sprintf("a-%d", i))
		seedLogRow(t, pool, taskB, "stdout", fmt.Sprintf("b-%d", i))
	}

	_, page := getLogs(t, srv, token, taskA, "order=desc&limit=2")
	require.Equal(t, []string{"a-3", "a-4"}, logContents(page))
	require.Equal(t, int64(5), page.Total)
	require.Equal(t, page.Items[0].Seq, page.PrevSeq)
	// Non-vacuity: the two returned ids are genuinely NOT adjacent, so no
	// offset arithmetic on seq or on total could have produced this page.
	require.Greater(t, page.Items[1].Seq-page.Items[0].Seq, int64(1))
}

func TestTaskLogs_DescendingWalkEqualsTheForwardWalkWithNoGapAndNoDuplicate(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Ed", "ed@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	// Forward walk, limit 2, until next_seq is 0.
	var forward []string
	since := int64(0)
	for i := 0; i < 10; i++ {
		_, p := getLogs(t, srv, token, taskID, fmt.Sprintf("limit=2&since_seq=%d", since))
		forward = append(forward, logContents(p)...)
		if p.NextSeq == 0 {
			break
		}
		since = p.NextSeq
	}

	// Backwards walk, limit 2, until prev_seq is 0. Each page is prepended, so
	// the assembled slice is in the same order the forward walk produced.
	var backward []string
	before := int64(0)
	for i := 0; i < 10; i++ {
		query := "order=desc&limit=2"
		if before > 0 {
			query = fmt.Sprintf("order=desc&limit=2&before_seq=%d", before)
		}
		_, p := getLogs(t, srv, token, taskID, query)
		backward = append(logContents(p), backward...)
		if p.PrevSeq == 0 {
			break
		}
		before = p.PrevSeq
	}

	require.Equal(t, []string{"line 0", "line 1", "line 2", "line 3", "line 4"}, forward)
	require.Equal(t, forward, backward)
}

func TestTaskLogs_EnvelopeCarriesAllFourKeysOnAnEmptyLog(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Fay", "fay@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)

	for _, query := range []string{"limit=10", "order=desc&limit=10"} {
		rr, _ := getLogs(t, srv, token, taskID, query)
		require.Equal(t, http.StatusOK, rr.Code, "q=%s", query)

		// The RAW key set, not a decoded struct: a decoded page cannot tell a
		// present-and-zero key from a missing one, which is the distinction
		// four clients depend on.
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &keys), "q=%s", query)
		got := make([]string, 0, len(keys))
		for k := range keys {
			got = append(got, k)
		}
		require.ElementsMatch(t, []string{"items", "next_seq", "prev_seq", "total"}, got, "q=%s", query)
		require.Equal(t, "[]", string(keys["items"]), "q=%s: an empty page is [], never null", query)
		require.Equal(t, "0", string(keys["next_seq"]), "q=%s", query)
		require.Equal(t, "0", string(keys["prev_seq"]), "q=%s", query)
		require.Equal(t, "0", string(keys["total"]), "q=%s", query)
	}
}

func TestTaskLogs_DescValidationOverTheWire(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Gus", "gus@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)

	cases := []struct{ query, wantMsg string }{
		{"order=desc&since_seq=10", "since_seq is not valid with order=desc; use before_seq"},
		{"before_seq=10", "before_seq requires order=desc"},
		{"order=desc&before_seq=0", "before_seq must be a positive integer"},
		{"order=desc&before_seq=-1", "before_seq must be a positive integer"},
		{"order=DESC", "order must be asc or desc"},
		{"order=descending", "order must be asc or desc"},
	}
	for _, c := range cases {
		rr, _ := getLogs(t, srv, token, taskID, c.query)
		require.Equal(t, http.StatusBadRequest, rr.Code, "q=%s: body=%s", c.query, rr.Body.String())
		require.Contains(t, rr.Body.String(), c.wantMsg, "q=%s", c.query)
	}

	// Paired positive control on the same call path: the accepted spellings do
	// not 400, so the rejections above are not the endpoint rejecting
	// everything.
	for _, ok := range []string{"order=asc&since_seq=1", "order=desc&before_seq=1", "order=desc"} {
		rr, _ := getLogs(t, srv, token, taskID, ok)
		require.Equal(t, http.StatusOK, rr.Code, "q=%s: body=%s", ok, rr.Body.String())
	}
}

func TestTaskLogs_UnknownTaskIs404AheadOfParameterValidation(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Hal", "hal@logs-test.com", false)
	token := createTestToken(t, q, user.ID)

	// A well-formed UUID that names no task, plus a malformed order. The
	// existence check runs first, so this is a 404 and not a 400 - preserved
	// deliberately, because it is what the endpoint has always done.
	unknown := "11111111-2222-4333-8444-555555555555"
	rr, _ := getLogs(t, srv, token, unknown, "order=DESC&before_seq=0")
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "task not found")
}

func TestTaskLogs_TailUsesTheSameAuthorizationAsTheForwardRead(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	owner := createTestUser(t, q, "Ivy", "ivy@logs-test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)
	jobID := submitTrivialJob(t, srv, ownerToken)
	taskID := firstTaskID(t, srv, ownerToken, jobID)
	seedLogRow(t, pool, taskID, "stdout", "secret-ish output")

	// No token: 401 in BOTH directions. The tail is not a new capability, and
	// it must not be a cheaper one either.
	for _, query := range []string{"limit=10", "order=desc&limit=10"} {
		req := httptest.NewRequest("GET", fmt.Sprintf("/v1/tasks/%s/logs?%s", taskID, query), nil)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code, "q=%s", query)
	}

	// An ordinary non-admin, non-owner user succeeds in both directions: this
	// endpoint has no ownership check today and this slice deliberately does
	// not add or remove one.
	other := createTestUser(t, q, "Jo", "jo@logs-test.com", false)
	otherToken := createTestToken(t, q, other.ID)
	for _, query := range []string{"limit=10", "order=desc&limit=10"} {
		rr, page := getLogs(t, srv, otherToken, taskID, query)
		require.Equal(t, http.StatusOK, rr.Code, "q=%s: body=%s", query, rr.Body.String())
		require.Equal(t, []string{"secret-ish output"}, logContents(page), "q=%s", query)
	}
}
```

- [ ] **Step 2: Run them green, then prove each one discriminates**

```powershell
go test -tags integration -p 1 ./internal/api/... -run "TestTaskLogs_" -v -timeout 600s
```

Expected: all 8 PASS. Now copy the two files aside so a mutation can be undone without touching the uncommitted tests:

```powershell
Copy-Item internal/api/tasks.go $env:TEMP/tasks.go.bak
Copy-Item internal/store/query/tasks.sql $env:TEMP/tasks.sql.bak
```

Apply each mutation, run ONLY the named test, confirm it fails, then `Copy-Item $env:TEMP/tasks.go.bak internal/api/tasks.go` (or the .sql equivalent plus `make generate` for the SQL mutation) and re-run to confirm green again.

| Mutation | Must turn RED |
|---|---|
| In `GetTaskLogsTailPage`, change the OUTER `ORDER BY t.id` to `ORDER BY t.id DESC`, then `make generate` | `..._DescendingTailReturnsTheNewestPageInAscendingOrder`, `..._NonContiguousSeqTailIsExactlyTheNewestRowsOfThatTask`, `..._DescendingWalkEqualsTheForwardWalk...` |
| In `GetTaskLogsBeforePage`, change `l.id < ...` to `l.id <= ...`, then `make generate` | `..._DescendingWalkEqualsTheForwardWalk...` (a duplicate row at every seam) |
| Delete the `int32(len(items)) >= q.Limit` guard so both cursors are always set | `..._CursorsAreDirectionExclusive`, `..._EnvelopeCarriesAllFourKeysOnAnEmptyLog`, and the walk test loops to its safety bound |
| Swap `prevSeq = items[0].Seq` to `items[len(items)-1].Seq` | `..._CursorsAreDirectionExclusive`, `..._DescendingWalkEqualsTheForwardWalk...` |
| Change `items := []logEntry{}` to `var items []logEntry` | `..._EnvelopeCarriesAllFourKeysOnAnEmptyLog` on the `[]` vs `null` assertion ONLY. Note which assertions do NOT move: this is the mutation the key-presence check alone cannot see. |
| Move the `parseTaskLogQuery` call above the `s.q.GetTask` existence check | `..._UnknownTaskIs404AheadOfParameterValidation` |
| In `parseTaskLogQuery`, accept any non-empty `order` (delete the `default:` arm) | `..._DescValidationOverTheWire` |

If a mutation reports "survived", first check that it actually applied and actually rebuilt: a SQL mutation needs `make generate` before it can change anything, and a silently unapplied mutation reports survival identically to a missing assertion.

- [ ] **Step 3: Confirm the whole package is green after every restore**

```powershell
git diff --stat -- internal/api/tasks.go internal/store/
go test -tags integration -p 1 ./internal/api/... -timeout 900s
```

`git diff --stat` must show NO change to `internal/api/tasks.go` or `internal/store/` beyond what Tasks 2 and 3 committed. If a mutation survived into the tree, the diff is where you catch it.

- [ ] **Step 4: Commit**

```bash
git add internal/api/tasks_integration_test.go
git commit -m "test(api): wire battery for descending task-log paging

Eight integration tests: the non-contiguous-seq tail (two tasks interleaved,
so no offset arithmetic could produce the page), backwards-walk equals
forward-walk with no gap and no duplicate at limit=2, direction-exclusive
cursors, all four envelope keys with items as [] rather than null, the
validation matrix over the wire with a positive control, 404 ahead of 400,
and identical authorization in both directions.

Every one was green when written, so each was proved discriminating by
mutation: outer ORDER BY reversed; before_seq made inclusive; the drained
guard deleted; prev_seq taken from the last item; items declared nil; the
parse hoisted above the existence check; the order allow-list opened. The
nil-items mutation is the one the key-presence assertion alone cannot see."
```

---

## Task 5: README (BACKEND)

**Files:**
- Modify: `README.md:1517` (the Tasks table row), a new subsection after the Tasks table, and `README.md:1755-1771` (the backfill recipe)

- [ ] **Step 1: Edit the Tasks table row**

Replace line 1517:

```
| `GET` | `/v1/tasks/{id}/logs` | Get task log entries |
```

with:

```
| `GET` | `/v1/tasks/{id}/logs` | Get task log entries. Paged by `seq`, forward or backward. See "Task log paging" below. |
```

- [ ] **Step 2: Add the subsection**

Insert immediately after the Tasks table (that is, after the new line 1517 and before `### Workers`):

```markdown
**Task log paging.** `GET /v1/tasks/{id}/logs` pages by `seq`, which is the log
row's id. It is ordered but **not contiguous**: it comes from a table-wide
sequence shared by every task, so neither `total` nor arithmetic on `seq` yields
an offset.

| Parameter | Default | Rule |
|---|---|---|
| `limit` | 50 | 1..200. Out of range or unparseable: 400 `limit must be 1..200`. |
| `order` | `asc` | `asc` or `desc`, nothing else. Absent or empty is `asc`. Anything else: 400 `order must be asc or desc`. |
| `since_seq` | 0 | Ascending only. **Exclusive**: returns rows with `seq > since_seq`. Negative or unparseable: 400. Sent with `order=desc`: 400. |
| `before_seq` | none | Descending only. **Exclusive**: returns rows with `seq < before_seq`. Absent with `order=desc` means "the newest page". Less than 1 or unparseable: 400. Sent without `order=desc`: 400. |

The response always carries all four keys, on every page including an empty one:

```json
{
  "items":    [ { "seq": 41, "stream": "stdout", "content": "...", "created_at": "..." } ],
  "next_seq": 0,
  "prev_seq": 41,
  "total":    94312
}
```

- **`items` is always ASCENDING by `seq`, in both directions.** `order` selects
  WHICH rows the page contains, not the order they appear in it. A descending
  page is the newest `limit` rows, listed oldest-first.
- `next_seq` is the ascending cursor: the last row's `seq`, or `0` when the page
  was short (drained). It is always `0` in a descending response.
- `prev_seq` is the descending cursor: the FIRST row's `seq` (the lowest), or `0`
  when the page was short (the beginning of the log has been reached). It is
  always `0` in an ascending response.
- `total` counts the task's log ENTRIES, not lines. An entry is an arbitrary
  chunk of output; one line can straddle two entries.

Stop when the cursor for your direction is `0`. Do not feed a `0` cursor back:
`before_seq=0` is a 400, not an empty page.

Read the end of a long log with one request, then walk earlier:

```
GET /v1/tasks/{id}/logs?order=desc&limit=200
  -> the newest 200 entries, ascending; prev_seq = the lowest seq in that page

GET /v1/tasks/{id}/logs?order=desc&before_seq=<prev_seq>&limit=200
  -> the 200 entries immediately older, ascending; repeat until prev_seq is 0
```

An unknown task is a `404` before any parameter is validated, so a malformed
`?order=` on a task that does not exist returns `404`, not `400`.
```

- [ ] **Step 3: Add the tail variant to the backfill recipe**

In the Events section, after the existing numbered recipe (currently ending at line 1765 with "Reversing steps 1 and 2 leaves a hole between the last page and the first event."), insert:

```markdown
**Opening at the tail instead.** Step 2 walks the whole history, which on a long
log is many requests for output the reader did not ask for. To show the END of
the log instead, keep step 1 exactly as it is - subscribe FIRST, the ordering is
the whole guarantee - and replace step 2 with a single
`GET /v1/tasks/{id}/logs?order=desc&limit=200`. The join stays gapless for the
same reason: the subscription is live from `T0`, the tail page is read at
`T1 > T0`, and every event with `seq <= maxSeq` is discarded, so a chunk written
in that window is either in the page or above `maxSeq`. What this does NOT cover
is everything OLDER than the page, which is the point - fetch it on demand with
`?order=desc&before_seq=`, and say so in the UI rather than implying the log is
complete. See "Task log paging" under the Tasks endpoints.
```

- [ ] **Step 4: Verify the edit did not corrupt the file**

README is the file this repo has damaged twice with programmatic edits, so check all three axes before committing:

```powershell
git diff --stat -- README.md
git ls-files --eol README.md
node -e "const b=require('fs').readFileSync('README.md');const s=b.toString('utf8');if(Buffer.from(s,'utf8').compare(b)!==0)throw new Error('README.md is not valid UTF-8');const i=b.findIndex(c=>c>127);console.log(i<0?'pure ASCII':'first non-ASCII byte at '+i)"
```

Expected: a diffstat proportionate to the three edits (roughly 60 to 70 added lines, 1 changed); `i/lf`; the UTF-8 assertion passing. The new text is intended to be pure ASCII - if the last command reports a non-ASCII byte inside a line you added, you introduced it and it must be removed.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document task-log paging, including the descending tail

The endpoint's parameters and envelope were documented nowhere in the REST
section - the table said only 'Get task log entries' - which is why every
client re-derived the rule from the handler. One subsection now owns the
contract and both recipes point at it.

States explicitly that items are ascending in BOTH directions, since
order=desc returning ascending items is the one genuinely confusing thing
here, and that a 0 cursor means stop rather than 'send it back'."
```

---

## Task 6: `api.ts` gains `prev_seq` and `getTaskLogsDesc` (FRONTEND)

**Files:**
- Modify: `web/src/jobs/api.ts:128-132, 155-168`
- Modify: `web/src/jobs/api.test.ts`
- Modify: `web/src/jobs/detailApi.test.ts:42-64`

- [ ] **Step 1: Write the failing tests**

In `web/src/jobs/api.test.ts`, extend the existing forward test and add the descending ones. Add `getTaskLogsDesc` to the import from `./api`.

```ts
test('getTaskLogs sends limit=200 and omits since_seq on the first page', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, prev_seq: 0, total: 0 })
    }),
  )
  await getTaskLogs('t1')
  expect(params?.get('limit')).toBe(String(BACKFILL_PAGE_SIZE))
  expect(BACKFILL_PAGE_SIZE).toBe(200) // the server's documented maximum
  expect(params?.has('since_seq')).toBe(false)
  // The forward reader must never acquire a direction: order=desc would make
  // this the newest page rather than the oldest, silently.
  expect(params?.has('order')).toBe(false)
})

test('getTaskLogsDesc asks for the tail with no cursor, and for a page before one when given', async () => {
  let search = ''
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ items: [], next_seq: 0, prev_seq: 0, total: 0 })
    }),
  )
  // The exact query string, not a subset: a leftover since_seq or a missing
  // order would still satisfy a per-key assertion.
  await getTaskLogsDesc('t1')
  expect(search).toBe('?order=desc&limit=200')

  await getTaskLogsDesc('t1', 93913)
  expect(search).toBe('?order=desc&limit=200&before_seq=93913')

  // 0 means "no cursor", and the server 400s before_seq=0, so it is never sent.
  await getTaskLogsDesc('t1', 0, 50)
  expect(search).toBe('?order=desc&limit=50')
})

test('getTaskLogsDesc reads prev_seq off the envelope', async () => {
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      // Hand-written, never marshalled through TaskLogPage: a fixture built
      // from the production type agrees with the decoder by construction.
      HttpResponse.json({
        items: [{ seq: 41, stream: 'stdout', content: 'x\n', created_at: '2026-09-01T00:00:00Z' }],
        next_seq: 0,
        prev_seq: 41,
        total: 94312,
      }),
    ),
  )
  const page = await getTaskLogsDesc('t1')
  expect(page.prev_seq).toBe(41)
  expect(page.total).toBe(94312)
})
```

- [ ] **Step 2: Run and watch it fail**

```powershell
cd D:/dev/relay/.claude/worktrees/web-d-tasklog-tail/web
npm test -- src/jobs/api.test.ts
```

Expected: a TypeScript/ESM resolution failure on the import, `does not provide an export named 'getTaskLogsDesc'`. Right reason.

- [ ] **Step 3: Implement**

In `web/src/jobs/api.ts`, change `TaskLogPage`:

```ts
export interface TaskLogPage {
  items: LogEntry[]
  next_seq: number
  prev_seq: number
  total: number
}
```

and add, immediately after `getTaskLogs`:

```ts
/**
 * One page of a task's log history walking BACKWARD. With no cursor this is the
 * newest page, which is how a log view opens at the end in one request rather
 * than paging forward through everything ahead of it.
 *
 * items are ASCENDING inside the page in both directions, so appendEntries and
 * the SSE dedupe consume this identically to a forward page. beforeSeq 0 means
 * "no cursor" and is omitted: the server 400s before_seq=0 rather than serving
 * an empty page, so a prev_seq of 0 means stop, not "ask again".
 */
export function getTaskLogsDesc(
  taskId: string,
  beforeSeq = 0,
  limit = BACKFILL_PAGE_SIZE,
): Promise<TaskLogPage> {
  const q = new URLSearchParams({ order: 'desc', limit: String(limit) })
  if (beforeSeq > 0) q.set('before_seq', String(beforeSeq))
  return apiFetch<TaskLogPage>(`/tasks/${taskId}/logs?${q}`)
}
```

Also update the doc comment on `getTaskLogs` so it names the direction: change its first sentence to `One page of a task's log history walking FORWARD from sinceSeq.`

- [ ] **Step 4: Run and watch it pass**

```powershell
npm test -- src/jobs/api.test.ts src/jobs/detailApi.test.ts
npx tsc -b
```

`detailApi.test.ts` will still pass (its fixtures decode `prev_seq` as `undefined`), but add `prev_seq: 0` to its two fixtures at lines 47 and 59 now, so the sweep in Task 13 has less to find. `tsc -b` should be clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/api.ts web/src/jobs/api.test.ts web/src/jobs/detailApi.test.ts
git commit -m "feat(web): getTaskLogsDesc and prev_seq on TaskLogPage

The tail reader. getTaskLogs keeps its exact query string and gains a test
pinning that it still sends no order - a forward reader that silently acquired
a direction would return the newest page where the oldest is expected."
```

---

## Task 7: `logBuffer` tracks `minSeq` (FRONTEND)

**Files:**
- Modify: `web/src/jobs/logBuffer.ts:49-71, 177-217`
- Modify: `web/src/jobs/logBuffer.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/logBuffer.test.ts`:

```ts
test('minSeq records the lowest seq ever accepted and never rises', () => {
  let s = createLogState()
  expect(s.minSeq).toBe(0)

  s = appendEntries(s, [chunk(10, 'a\n'), chunk(40, 'b\n')])
  // The FIRST accepted entry, not the last, and not the smallest of a later
  // batch: this is the cursor a backwards fetch continues from.
  expect(s.minSeq).toBe(10)
  expect(s.maxSeq).toBe(40)

  s = appendEntries(s, [chunk(41, 'c\n')])
  expect(s.minSeq).toBe(10)
  expect(s.maxSeq).toBe(41)

  // A duplicate below maxSeq is discarded before it can move anything.
  s = appendEntries(s, [chunk(5, 'old\n')])
  expect(s.minSeq).toBe(10)
})
```

- [ ] **Step 2: Run and watch it fail**

```powershell
cd D:/dev/relay/.claude/worktrees/web-d-tasklog-tail/web
npm test -- src/jobs/logBuffer.test.ts
```

Expected: `expected undefined to be 0` on the first assertion. Right reason.

- [ ] **Step 3: Implement**

In `LogState`, after the `maxSeq` field:

```ts
  /** Lowest seq accepted; 0 when the window is empty. The backwards cursor. */
  minSeq: number
```

In `createLogState`, add `minSeq: 0,` after `maxSeq: 0,`.

In `appendEntries`, add `let minSeq = state.minSeq` beside the other locals, set it inside the accept branch right after `maxSeq = e.seq`:

```ts
    if (minSeq === 0) minSeq = e.seq
```

and add `minSeq,` to the returned object.

- [ ] **Step 4: Run and watch it pass**

```powershell
npm test -- src/jobs/logBuffer.test.ts
npx tsc -b
```

`tsc -b` may now flag `createLogState`-shaped object literals elsewhere; there should be none (`createLogState()` is the only constructor). If it flags a test literal, add `minSeq: 0` there.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/logBuffer.ts web/src/jobs/logBuffer.test.ts
git commit -m "feat(web): LogState tracks minSeq

The cursor a backwards fetch continues from. Set once, on the first accepted
entry, and never raised by later appends."
```

---

## Task 8: `prependEntries` with an exact seam join (FRONTEND)

**Files:**
- Modify: `web/src/jobs/logBuffer.ts` (new export after `appendEntries`)
- Modify: `web/src/jobs/logBuffer.test.ts`

- [ ] **Step 1: Write the failing tests**

Add `prependEntries` to the import list, then append:

```ts
test('prependEntries joins a line that straddles the seam', () => {
  // The window starts mid-line: the tail page began at 'World\n', so its first
  // row is the text from the window's start to that stream's first newline.
  let s = createLogState()
  s = appendEntries(s, [chunk(10, 'World\nsecond\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['World', 'second'])

  // The earlier page ends mid-line with 'Hello ' dangling.
  s = prependEntries(s, [chunk(8, 'zero\n'), chunk(9, 'one\nHello ')])

  // Exact text AND exact count: an implementation that appends the fragment as
  // its own row produces four rows that read almost the same.
  expect(s.lines.map((l) => l.text)).toEqual(['zero', 'one', 'Hello World', 'second'])
  expect(s.lines).toHaveLength(4)
})

test('prependEntries keeps the two streams seams apart', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(20, 'out-tail\n'), chunk(21, 'err-tail\n', 'stderr')])
  s = prependEntries(s, [chunk(10, 'out-head ', 'stdout'), chunk(11, 'err-head ', 'stderr')])

  const out = s.lines.filter((l) => l.stream === 'stdout').map((l) => l.text)
  const err = s.lines.filter((l) => l.stream === 'stderr').map((l) => l.text)
  expect(out).toEqual(['out-head out-tail'])
  expect(err).toEqual(['err-head err-tail'])
})

test('prependEntries does not join into a marker row', () => {
  // The reachable shape: a drop happens before any line arrives, so markDropped
  // puts a marker at index 0 and the tail page's lines follow it. markDropped
  // emits its marker with stream 'stdout' REGARDLESS of which stream dropped,
  // so a naive "first row of this stream" scan joins into the marker text.
  let s = createLogState()
  s = markDropped(s)
  s = appendEntries(s, [chunk(20, 'World\n')])
  expect(s.lines[0].kind).toBe('marker')

  s = prependEntries(s, [chunk(10, 'Hello ')])

  expect(s.lines[0].kind).toBe('marker')
  expect(s.lines[0].text).toBe(DROP_MARKER_TEXT)
  expect(s.lines.map((l) => l.text)).toEqual([DROP_MARKER_TEXT, 'Hello World'])
})

test('prependEntries refuses after eviction', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'x\n'.repeat(MAX_LINES + 1))])
  expect(s.evicted).toBe(true)
  const before = s
  s = prependEntries(s, [chunk(1, 'earlier\n')])
  // Reference equality: once drop-oldest has evicted the front of the window,
  // the first row is no longer the continuation of minSeq, so there is no seam
  // to join to and the control that produced this call is disabled anyway.
  expect(s).toBe(before)
})

test('prependEntries that overflows the cap keeps the newest lines', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1000, 'keep-me\n')])
  const batch = Array.from({ length: MAX_LINES + 5 }, (_, i) => chunk(i + 1, `old-${i}\n`))
  s = prependEntries(s, batch)

  expect(s.lines).toHaveLength(MAX_LINES)
  expect(s.evicted).toBe(true)
  // The FIRST retained line, not the length: keeping the OLDEST lines would
  // also produce MAX_LINES rows and would drop the live tail.
  expect(s.lines[0].text).toBe('old-6')
  expect(s.lines[s.lines.length - 1].text).toBe('keep-me')
})

test('prependEntries lowers minSeq only', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(100, 'a\n')])
  s = prependEntries(s, [chunk(40, 'b\n'), chunk(50, 'c\n')])
  expect(s.minSeq).toBe(40)
  expect(s.maxSeq).toBe(100)
})

test('prependEntries assigns fresh keys that collide with nothing', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(100, 'a\nb\n')])
  s = prependEntries(s, [chunk(40, 'c\nd\n')])
  const keys = s.lines.map((l) => l.key)
  expect(new Set(keys).size).toBe(keys.length)
})
```


- [ ] **Step 2: Run and watch it fail**

```powershell
npm test -- src/jobs/logBuffer.test.ts
```

Expected: `prependEntries is not a function` / no such export. Right reason.

- [ ] **Step 3: Implement**

Add after `appendEntries` in `web/src/jobs/logBuffer.ts`:

```ts
/**
 * Prepends an older page. Entries are ascending and every seq is below
 * state.minSeq, so the batch is contiguous with the window by construction -
 * which is what makes the seam join exact: the window's first COMPLETED line of
 * a given stream is the text from the window's start to that stream's first
 * newline, so the batch's dangling fragment for that stream is precisely its
 * missing prefix.
 *
 * Refuses once evicted is set. Drop-oldest has then removed the front of the
 * window, so its first row is no longer the continuation of minSeq and there is
 * no seam to join to.
 *
 * The join skips marker rows: markDropped emits its marker with stream 'stdout'
 * whichever stream dropped, so matching on stream alone would concatenate a
 * fragment onto the drop notice. Guard: 'prependEntries does not join into a
 * marker row'.
 */
export function prependEntries(state: LogState, entries: LogChunk[]): LogState {
  if (state.evicted || entries.length === 0) return state

  // Reuse the tested reassembly rather than forking it.
  const batch = appendEntries(createLogState(), entries)

  const lines = state.lines.slice()
  const partials = { ...state.partials }
  const streams: LogStream[] = ['stdout', 'stderr']
  for (const s of streams) {
    const dangling = batch.partials[s]
    if (dangling === null) continue
    const i = lines.findIndex((r) => r.kind === 'line' && r.stream === s)
    if (i === -1) {
      // No completed line for this stream in the window: the fragment is the
      // prefix of whatever partial is still open there, or is itself the only
      // text that stream has.
      const open = partials[s]
      partials[s] = { text: dangling.text + (open?.text ?? ''), time: open?.time ?? dangling.time }
      continue
    }
    lines[i] = { ...lines[i], text: collapseCR(dangling.text + lines[i].text) }
  }

  let nextKey = state.nextKey
  const prepended = batch.lines.map((r) => ({ ...r, key: nextKey++ }))
  const capped = capLines([...prepended, ...lines])
  return {
    ...state,
    lines: capped.lines,
    partials,
    nextKey,
    minSeq: state.minSeq === 0 ? entries[0].seq : Math.min(state.minSeq, entries[0].seq),
    evicted: state.evicted || capped.evicted,
  }
}
```


- [ ] **Step 4: Run and watch it pass**

```powershell
npm test -- src/jobs/logBuffer.test.ts
npx tsc -b
```

Expected: all logBuffer tests PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/logBuffer.ts web/src/jobs/logBuffer.test.ts
git commit -m "feat(web): prependEntries joins an older page at an exact seam

A line straddling the seam renders as ONE line, which is a property the log
view deliberately bought and would otherwise lose once per click per stream.
The join skips marker rows because markDropped emits its marker as stdout
whichever stream dropped, and it refuses outright once eviction has removed
the front of the window - the one case where the first row is no longer the
continuation of minSeq."
```

---

## Task 9: `preservedScrollTop` (FRONTEND)

**Files:**
- Modify: `web/src/jobs/logBuffer.ts` (new export beside `shouldFollow`)
- Modify: `web/src/jobs/logBuffer.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
test('preservedScrollTop keeps the viewport anchored', () => {
  // Content added ABOVE the viewport: the same content stays under the cursor
  // only if scrollTop moves by the height delta.
  expect(preservedScrollTop(100, 500, 900)).toBe(500)
  // Nothing changed.
  expect(preservedScrollTop(100, 500, 500)).toBe(100)
  // A shrinking container (drop-oldest evicted rows above): never negative.
  expect(preservedScrollTop(100, 900, 500)).toBe(0)
  expect(preservedScrollTop(700, 900, 500)).toBe(300)
})
```

- [ ] **Step 2: Run and watch it fail**

```powershell
npm test -- src/jobs/logBuffer.test.ts
```

Expected: `preservedScrollTop is not a function`.

- [ ] **Step 3: Implement**

```ts
/**
 * Where the scroll cursor must move to keep the same content under the
 * viewport when rows are added above it (or removed above it, which is the
 * negative case). Pure for the same reason shouldFollow is: jsdom reports every
 * geometry as 0, so a pixel assertion in a component test is vacuously green.
 */
export function preservedScrollTop(
  scrollTop: number,
  prevHeight: number,
  nextHeight: number,
): number {
  return Math.max(0, scrollTop + (nextHeight - prevHeight))
}
```

- [ ] **Step 4: Run and watch it pass**

```powershell
npm test -- src/jobs/logBuffer.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/logBuffer.ts web/src/jobs/logBuffer.test.ts
git commit -m "feat(web): preservedScrollTop for content added above the viewport"
```

---

## Task 10: the hook opens at the tail (FRONTEND)

**Files:**
- Modify: `web/src/jobs/useTaskLogStream.ts:318-408`
- Modify: `web/src/jobs/useTaskLogStream.test.tsx:67-111` (two existing tests must change)

- [ ] **Step 1: Write the failing test**

Add to `web/src/jobs/useTaskLogStream.test.tsx`:

```ts
test('opens at the tail with one request', async () => {
  const fake = fakeSseServer()
  const searches: string[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      searches.push(new URL(request.url).search)
      // A server with more history than one page: prev_seq is non-zero, and
      // next_seq is 0 in every descending response.
      return HttpResponse.json({ items: [entry(93_913), entry(94_312)], next_seq: 0, prev_seq: 93_913, total: 94_312 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))

  // The exact count and the exact query string: a leftover forward walk shows
  // up as a second request, and a forward FIRST request shows up here as a
  // missing order=desc.
  expect(searches).toEqual(['?order=desc&limit=200'])
  expect(result.current.rows.map((r) => r.text)).toEqual(['line-93913', 'line-94312'])
  expect(result.current.total).toBe(94_312)
  expect(result.current.historyTruncated).toBe(false)
})

test('a short tail page means the log is complete', async () => {
  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({ items: [entry(1)], next_seq: 0, prev_seq: 0, total: 1 }),
    ),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(result.current.earlierComplete).toBe(true)
  expect(result.current.canLoadEarlier).toBe(false)
})
```

Then REWRITE the two existing tests that entered the forward loop through a fresh mount. A fresh mount no longer walks forward, so as written they now assert nothing about the loop. Replace `'pumps since_seq from next_seq until next_seq is 0'` (currently lines 67 to 87) and `'stops at MAX_BACKFILL_PAGES, flags truncation, and still applies live frames'` (89 to 111) with:

```ts
// The forward loop is now reached only by a RECOVERY: a fresh open takes the
// tail. Entering through a dropped frame is the shortest honest route to it.
test('a recovery pumps since_seq from next_seq until next_seq is 0', async () => {
  const fake = fakeSseServer()
  const searches: string[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const url = new URL(request.url)
      searches.push(url.search)
      const since = url.searchParams.get('since_seq')
      if (since === null) return HttpResponse.json({ items: [entry(10)], next_seq: 0, prev_seq: 0, total: 3 })
      if (since === '10') return HttpResponse.json({ items: [entry(20)], next_seq: 20, prev_seq: 0, total: 3 })
      return HttpResponse.json({ items: [entry(30)], next_seq: 0, prev_seq: 0, total: 3 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(searches).toEqual(['?order=desc&limit=200'])

  fake.latest().emit('dropped', { reason: 'slow_consumer' })
  await waitFor(() => expect(result.current.status).toBe('live'))

  // Direction is decided by what we hold: maxSeq is 10, so the recovery pages
  // FORWARD and pumps until next_seq is 0. No order parameter on any of them.
  expect(searches).toEqual([
    '?order=desc&limit=200',
    '?limit=200&since_seq=10',
    '?limit=200&since_seq=20',
  ])
  expect(result.current.rows.filter((r) => r.kind === 'line').map((r) => r.text)).toEqual([
    'line-10',
    'line-20',
    'line-30',
  ])
  expect(result.current.historyTruncated).toBe(false)
  expect(result.current.total).toBe(3)
})

test('a recovery stops at MAX_BACKFILL_PAGES, flags truncation, and still applies live frames', async () => {
  const fake = fakeSseServer()
  let forwardRequests = 0
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const since = new URL(request.url).searchParams.get('since_seq')
      if (since === null) {
        return HttpResponse.json({ items: [entry(1)], next_seq: 0, prev_seq: 0, total: 94_312 })
      }
      forwardRequests++
      // A server that never drains: next_seq is always non-zero.
      return HttpResponse.json({
        items: [entry(forwardRequests + 1)],
        next_seq: forwardRequests + 1,
        prev_seq: 0,
        total: 94_312,
      })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(result.current.historyTruncated).toBe(false) // a tail open never truncates

  fake.latest().emit('dropped', { reason: 'slow_consumer' })
  await waitFor(() => expect(result.current.historyTruncated).toBe(true))
  // Exact count, not "several": an off-by-one or a missing cap is a request loop.
  expect(forwardRequests).toBe(MAX_BACKFILL_PAGES)
  expect(result.current.total).toBe(94_312)

  fake.latest().emit('task_log', logEvent(5000, 'after-cap\n'))
  await waitFor(() => expect(result.current.rows.some((r) => r.text === 'after-cap')).toBe(true))
})
```

- [ ] **Step 2: Run and watch it fail**

```powershell
npm test -- src/jobs/useTaskLogStream.test.tsx
```

Expected: `'opens at the tail with one request'` fails on `searches` being `['?limit=200']`; `'a short tail page means the log is complete'` fails on `earlierComplete` being `undefined`; the two rewritten recovery tests fail on their `searches` arrays. All the right reason.

- [ ] **Step 3: Implement**

In `web/src/jobs/useTaskLogStream.ts`:

1. Import `getTaskLogsDesc` alongside `getTaskLogs`.
2. Add to `TaskLogStreamResult`:

```ts
  /** prev_seq was 0: the window reaches the beginning of the log. */
  earlierComplete: boolean
```

3. Add the state hook beside the others:

```ts
  const [earlierComplete, setEarlierComplete] = useState(false)
```

4. In the disabled early return, add `setEarlierComplete(false)` beside `setView(createLogState())`.
5. In the effect body, after `setErrorMessage('')`, add:

```ts
    // Only for a genuinely fresh window. A same-task re-run with a carry (a
    // terminal transition, a manual reconnect) keeps whatever the user has
    // already walked back to; blanket-resetting would re-offer "Load earlier"
    // on a log that has none.
    if (carried === null) setEarlierComplete(false)
```

6. Extract the failure path so both history branches share one ordering. Add above `run`:

```ts
    // Ends the generation BEFORE releasing the resource. The SSE stream this
    // run opened is still open, and aborting makes its promise settle on the
    // next microtask; without bumping gen (and setting fatal) first, that dying
    // connection's own handler still passes the staleness guard and overwrites
    // 'error' with 'reconnecting'. Guard: 'a failing backfill page settles to
    // error and the dying stream cannot resurrect it'.
    function failHistory(err: unknown) {
      fatal = true
      gen++
      setErrorMessage(err instanceof Error ? err.message : 'failed to load logs')
      setStatus('error')
      controller.abort()
    }
```

7. Replace the paging block in `run` (currently `let since = sinceSeq` through the end of the `for (;;)` loop) with:

```ts
      if (logState.maxSeq === 0) {
        // We hold nothing, so open at the END. One request, and the rows the
        // reader came for. Paging forward from 0 returns the OLDEST page, which
        // on a long log is the wrong end and costs up to MAX_BACKFILL_PAGES
        // requests to still not reach the tail.
        let page: TaskLogPage
        try {
          page = await getTaskLogsDesc(taskId, 0, BACKFILL_PAGE_SIZE)
        } catch (err) {
          if (cancelled || myGen !== gen) return
          failHistory(err)
          return
        }
        if (cancelled || myGen !== gen) return
        ingest(page.items)
        setTotal(page.total)
        setEarlierComplete(page.prev_seq === 0)
      } else {
        // We hold something: continue FORWARD from it. This is the recovery,
        // the reconciliation and the manual reconnect, all unchanged.
        let since = sinceSeq
        let pages = 0
        for (;;) {
          let page: TaskLogPage
          try {
            page = await getTaskLogs(taskId, since, BACKFILL_PAGE_SIZE)
          } catch (err) {
            if (cancelled || myGen !== gen) return
            failHistory(err)
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
      }
```

8. Add `earlierComplete,` to the returned object.

- [ ] **Step 4: Run and watch it pass**

```powershell
npm test -- src/jobs/useTaskLogStream.test.tsx
```

Expected: everything in the file PASSES, including `'subscribes to the stream BEFORE it requests the first history page'` (still the ordering guard - re-prove it RED once by swapping the `await openStream(myGen)` statement below the history block, then restore) and the H1 regression test, which now exercises `failHistory` through the tail branch.

Several tests in this file will still be reading fixtures without `prev_seq`; that is Task 13. If one fails NOW because of it, add `prev_seq: 0` to that fixture immediately rather than deferring.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/useTaskLogStream.ts web/src/jobs/useTaskLogStream.test.tsx
git commit -m "feat(web): the log view opens at the tail, in one request

maxSeq === 0 means we hold nothing, so fetch the newest page; otherwise page
forward from what we hold. One predicate covers all four entries into run: a
fresh mount, a drop-recovery, the terminal reconciliation, and a drop that
happened before any line arrived (maxSeq still 0, so the tail - which is the
correct answer there, not an accident).

The forward loop, its MAX_BACKFILL_PAGES bound and historyTruncated are
unchanged on that path, so the two tests that used to reach the loop through a
fresh mount now reach it through a recovery.

failHistory keeps the bump-then-abort ordering in one place for both branches."
```

---

## Task 11: `loadEarlier` and the generation fence (FRONTEND)

**Files:**
- Modify: `web/src/jobs/useTaskLogStream.ts`
- Modify: `web/src/jobs/useTaskLogStream.test.tsx`

- [ ] **Step 1: Write the failing tests**

```ts
test('load earlier fetches one page per click', async () => {
  const fake = fakeSseServer()
  const searches: string[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const url = new URL(request.url)
      searches.push(url.search)
      if (url.searchParams.get('before_seq') === '10') {
        return HttpResponse.json({ items: [entry(8), entry(9)], next_seq: 0, prev_seq: 8, total: 12 })
      }
      return HttpResponse.json({ items: [entry(10), entry(11)], next_seq: 0, prev_seq: 10, total: 12 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(result.current.canLoadEarlier).toBe(true)

  await act(async () => {
    result.current.loadEarlier()
    // A second click while one is in flight must be a no-op, not a second page.
    result.current.loadEarlier()
  })
  await waitFor(() => expect(result.current.loadingEarlier).toBe(false))

  expect(searches).toEqual(['?order=desc&limit=200', '?order=desc&limit=200&before_seq=10'])
  expect(result.current.rows.map((r) => r.text)).toEqual([
    'line-8',
    'line-9',
    'line-10',
    'line-11',
  ])
})

test('canLoadEarlier is false in each of the terminal cases', async () => {
  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({ items: [entry(5)], next_seq: 0, prev_seq: 0, total: 1 }),
    ),
  )
  const drained = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(drained.result.current.status).toBe('live'))
  // prev_seq was 0: the beginning of the log is already on screen.
  expect(drained.result.current.canLoadEarlier).toBe(false)
  drained.unmount()

  // Nothing held yet: no cursor exists, so there is nothing to walk back from.
  const fake2 = fakeSseServer()
  let release: () => void = () => {}
  const gate = new Promise<void>((r) => {
    release = r
  })
  server.use(
    http.get('/v1/tasks/t1/logs', async () => {
      await gate
      return HttpResponse.json({ items: [entry(5)], next_seq: 0, prev_seq: 4, total: 9 })
    }),
  )
  const pending = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake2.fetchImpl }),
  )
  await fake2.waitForConnection()
  expect(pending.result.current.canLoadEarlier).toBe(false)
  release()
  await waitFor(() => expect(pending.result.current.canLoadEarlier).toBe(true))

  // Evicted: the front of the window is gone, so there is no seam to join to.
  const conn = fake2.latest()
  conn.emit('task_log', logEvent(6, 'x\n'.repeat(MAX_LINES + 1)))
  await waitFor(() => expect(pending.result.current.evicted).toBe(true))
  expect(pending.result.current.canLoadEarlier).toBe(false)
  pending.unmount()
})

// The generation fence. loadEarlier carries no AbortSignal (apiFetch has no
// realm-mismatch fallback, and jsdom + a native fetch is exactly where that
// bites), so the response always arrives and the fence is the only control.
test('a stale earlier page is discarded', async () => {
  const fake = fakeSseServer()
  let release: () => void = () => {}
  const gate = new Promise<void>((r) => {
    release = r
  })
  server.use(
    http.get('/v1/tasks/:tid/logs', async ({ request, params }) => {
      const url = new URL(request.url)
      if (url.searchParams.has('before_seq')) {
        await gate
        return HttpResponse.json({ items: [entry(1, 'STALE-EARLIER\n')], next_seq: 0, prev_seq: 0, total: 9 })
      }
      const seq = params.tid === 't1' ? 10 : 20
      return HttpResponse.json({ items: [entry(seq)], next_seq: 0, prev_seq: seq - 1, total: 9 })
    }),
  )
  const { result, rerender } = renderHook(
    ({ id }: { id: string }) =>
      useTaskLogStream(id, { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
    { initialProps: { id: 't1' } },
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  act(() => {
    result.current.loadEarlier()
  })

  // Switch tasks while the earlier page is still in flight, then let it land.
  rerender({ id: 't2' })
  await waitFor(() => expect(result.current.rows.map((r) => r.text)).toEqual(['line-20']))
  release()
  await tick()
  await tick()

  // The stale page never reaches the new task's window - which would otherwise
  // be a prepend at a seam that does not exist there.
  expect(result.current.rows.map((r) => r.text)).toEqual(['line-20'])
})

test('a discarded earlier page re-enables the control', async () => {
  const fake = fakeSseServer()
  let release: () => void = () => {}
  const gate = new Promise<void>((r) => {
    release = r
  })
  server.use(
    http.get('/v1/tasks/t1/logs', async ({ request }) => {
      const url = new URL(request.url)
      if (url.searchParams.has('before_seq')) {
        await gate
        return HttpResponse.json({ items: [entry(1, 'STALE\n')], next_seq: 0, prev_seq: 0, total: 9 })
      }
      if (url.searchParams.has('since_seq')) {
        return HttpResponse.json({ items: [entry(11)], next_seq: 0, prev_seq: 0, total: 9 })
      }
      return HttpResponse.json({ items: [entry(10)], next_seq: 0, prev_seq: 9, total: 9 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  act(() => {
    result.current.loadEarlier()
  })
  expect(result.current.loadingEarlier).toBe(true)

  // A drop-recovery bumps the generation while the earlier page is in flight.
  fake.latest().emit('dropped', { reason: 'slow_consumer' })
  await waitFor(() => expect(result.current.status).toBe('live'))
  release()
  await waitFor(() => expect(result.current.loadingEarlier).toBe(false))

  // Discarded, not applied - and the control is usable again, so the click is
  // visibly refused rather than silently swallowed.
  expect(result.current.rows.some((r) => r.text === 'STALE')).toBe(false)
  expect(result.current.canLoadEarlier).toBe(true)
})
```

Add `MAX_LINES` to the `./logBuffer` import in this test file.

- [ ] **Step 2: Run and watch it fail**

```powershell
npm test -- src/jobs/useTaskLogStream.test.tsx
```

Expected: `result.current.loadEarlier is not a function`, plus `canLoadEarlier`/`loadingEarlier` undefined.

- [ ] **Step 3: Implement**

1. Import `prependEntries` from `./logBuffer` and `MAX_LINES` (already exported).
2. Extend `TaskLogStreamResult`:

```ts
  /** A click will fetch one page of older history. */
  canLoadEarlier: boolean
  loadingEarlier: boolean
  loadEarlier: () => void
```

3. Add state and the stable callback beside `reconnect`:

```ts
  const [loadingEarlier, setLoadingEarlier] = useState(false)
  // Holds the CURRENT effect run's loader. A click always drives the run that
  // owns the window on screen; a torn-down run's loader is never reachable.
  const loadEarlierRef = useRef<(() => void) | null>(null)
  const loadEarlier = useCallback(() => {
    loadEarlierRef.current?.()
  }, [])
```

4. In the disabled early return, add `setLoadingEarlier(false)`.
5. In the effect body, beside `setErrorMessage('')`, add `setLoadingEarlier(false)`.
6. Inside the effect, after `flushNow`, add the loader and register it:

```ts
    // In-flight guard. A local, not React state: two clicks in one tick must
    // not both fetch, and a state update is not visible until the next render.
    let earlierInFlight = false

    async function loadEarlier() {
      const myGen = gen
      const before = logState.minSeq
      // minSeq 0 means the window is empty: there is no cursor to walk back
      // from, and before_seq=0 is a 400 by contract rather than an empty page.
      if (earlierInFlight || before <= 0 || logState.evicted) return
      earlierInFlight = true
      setLoadingEarlier(true)

      let page: TaskLogPage
      try {
        page = await getTaskLogsDesc(taskId, before, BACKFILL_PAGE_SIZE)
      } catch {
        earlierInFlight = false
        if (!cancelled) setLoadingEarlier(false)
        return
      }
      earlierInFlight = false
      // No AbortSignal on this request (apiFetch has no realm-mismatch
      // fallback), so the response always arrives and this fence is the only
      // control. Cancelled: the next run owns the state, touch nothing.
      // Superseded: clear the flag so the control re-enables, then discard -
      // prepending here would join a page onto a window it is not contiguous
      // with.
      if (cancelled) return
      setLoadingEarlier(false)
      if (myGen !== gen) return

      setLogState(prependEntries(logState, page.items))
      if (page.prev_seq === 0) setEarlierComplete(true)
      setTotal(page.total)
      flushNow()
    }
    loadEarlierRef.current = loadEarlier
```

7. In the cleanup, add `loadEarlierRef.current = null` beside `cancelled = true`.
8. Compute and return the new fields:

```ts
  const canLoadEarlier =
    !earlierComplete && !view.evicted && view.minSeq > 0 && view.lines.length < MAX_LINES

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
    canLoadEarlier,
    loadingEarlier,
    earlierComplete,
    loadEarlier,
  }
```

- [ ] **Step 4: Run and watch it pass**

```powershell
npm test -- src/jobs/useTaskLogStream.test.tsx
npx tsc -b
```

`tsc -b` will now fail in `LogView.test.tsx` and `LogTab.test.tsx`, whose `streamOf` helpers construct a full `TaskLogStreamResult`. Add the four new fields to both helpers now:

```ts
    canLoadEarlier: false,
    loadingEarlier: false,
    earlierComplete: false,
    loadEarlier: () => {},
```

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/useTaskLogStream.ts web/src/jobs/useTaskLogStream.test.tsx web/src/jobs/LogView.test.tsx web/src/jobs/LogTab.test.tsx
git commit -m "feat(web): Load earlier walks one page of older history per click

Never automatic and never a loop. The request carries no AbortSignal: apiFetch
has no realm-mismatch fallback, and handing a jsdom AbortSignal to Node's fetch
throws - the documented reason apiStream carries one. The generation fence is
the control instead, so a page superseded by a task switch or a drop-recovery
is discarded and the control returns to enabled rather than the click being
silently lost.

canLoadEarlier is false while the window is empty, once prev_seq came back 0,
once eviction removed the front of the window, and at MAX_LINES."
```

---

## Task 12: `LogView` renders the control, the notice and the anchor (FRONTEND)

**Files:**
- Modify: `web/src/jobs/LogView.tsx`
- Modify: `web/src/jobs/LogView.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
test('renders Load earlier only when the stream says a page is available', async () => {
  const loadEarlier = vi.fn()
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(1, 'a')], canLoadEarlier: true, loadEarlier })} />,
  )
  await userEvent.click(screen.getByRole('button', { name: /load earlier/i }))
  expect(loadEarlier).toHaveBeenCalledTimes(1)

  // A complete log must not grow a control that implies missing history.
  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a')], canLoadEarlier: false, earlierComplete: true })} />,
  )
  expect(screen.queryByRole('button', { name: /load earlier/i })).toBeNull()
  expect(screen.queryByText(/loading earlier/i)).toBeNull()
})

test('shows a loading state instead of the button while a page is in flight', () => {
  render(
    <LogView stream={streamOf({ rows: [row(1, 'a')], canLoadEarlier: true, loadingEarlier: true })} />,
  )
  expect(screen.getByText(/loading earlier/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /load earlier/i })).toBeNull()
})

test('the tail notice keeps lines and entries as separate units, and eviction wins', () => {
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(1, 'a'), row(2, 'b')], earlierComplete: false, total: 94312 })} />,
  )
  expect(
    screen.getByText(`Showing the most recent 2 lines of ${(94312).toLocaleString('en-US')} log entries.`),
  ).toBeInTheDocument()

  // Eviction resolves first: it is the stronger statement, and both can be true.
  rerender(
    <LogView stream={streamOf({ rows: [row(1, 'a')], earlierComplete: false, evicted: true, total: 94312 })} />,
  )
  expect(screen.getByText('Earlier output not shown.')).toBeInTheDocument()

  // A complete log shows no notice and no control at all.
  rerender(<LogView stream={streamOf({ rows: [row(1, 'a')], earlierComplete: true, total: 1 })} />)
  expect(screen.queryByText(/showing the most recent/i)).toBeNull()
  expect(screen.queryByText(/earlier output not shown/i)).toBeNull()
})

test('anchors the viewport when rows are added above it', () => {
  const onPrependAdjust = vi.fn()
  // jsdom reports every geometry as 0, so the pixel is untestable here and only
  // the DECISION is asserted; preservedScrollTop owns the arithmetic.
  const { rerender } = render(
    <LogView stream={streamOf({ rows: [row(10, 'tail')] })} onPrependAdjust={onPrependAdjust} />,
  )
  // Follow is on by default, so an append must not adjust anything.
  rerender(
    <LogView stream={streamOf({ rows: [row(10, 'tail'), row(11, 'more')] })} onPrependAdjust={onPrependAdjust} />,
  )
  expect(onPrependAdjust).not.toHaveBeenCalled()

  const box = screen.getByTestId('log-body')
  Object.defineProperty(box, 'scrollHeight', { value: 2000, configurable: true })
  Object.defineProperty(box, 'clientHeight', { value: 1000, configurable: true })
  box.scrollTop = 0
  act(() => {
    box.dispatchEvent(new Event('scroll', { bubbles: true }))
  })
  // Prepended rows carry FRESH keys, so the first row's key changes; an append
  // leaves it alone. That is the signal a prepend happened.
  rerender(
    <LogView
      stream={streamOf({ rows: [row(12, 'earlier'), row(10, 'tail'), row(11, 'more')] })}
      onPrependAdjust={onPrependAdjust}
    />,
  )
  expect(onPrependAdjust).toHaveBeenCalledTimes(1)
})
```

- [ ] **Step 2: Run and watch it fail**

```powershell
npm test -- src/jobs/LogView.test.tsx
```

Expected: no `Load earlier` button; the notice assertions fail (today's notice is `historyTruncated`-only); `onPrependAdjust` is not a prop.

- [ ] **Step 3: Implement**

In `web/src/jobs/LogView.tsx`:

1. Imports: add `useLayoutEffect` from react, and `preservedScrollTop` from `./logBuffer`.
2. Add to `LogViewProps`:

```ts
  /** Test seam: called with the scrollTop applied after content was added above. */
  onPrependAdjust?: (scrollTop: number) => void
```

and destructure it in the component signature.

3. Destructure the new stream fields at the top of the component:

```ts
  const { rows, status, attempt, evicted, historyTruncated, total, errorMessage } = stream
  const { canLoadEarlier, loadingEarlier, earlierComplete } = stream
```

4. Replace the `notice` expression with the four-way resolution, first match wins:

```tsx
  // MAX_LINES counts reassembled LINES; total counts server-side log ENTRIES.
  // Two different units, so the notice names them separately rather than
  // implying a single "N of M" count of the same thing.
  let notice: string | null = null
  if (evicted) {
    notice = 'Earlier output not shown.'
  } else if (!earlierComplete && rows.length > 0) {
    notice = `Showing the most recent ${rows.length.toLocaleString('en-US')} lines of ${total.toLocaleString('en-US')} log entries.`
  } else if (historyTruncated) {
    notice = `Showing the first ${MAX_LINES.toLocaleString('en-US')} of ${total.toLocaleString('en-US')} log entries. Live output continues below.`
  }
```

5. Add the scroll anchor, after the existing follow effect:

```tsx
  const prevFirstKey = useRef<number | undefined>(undefined)
  const prevHeight = useRef(0)

  useLayoutEffect(() => {
    const firstKey = rows[0]?.key
    const changedAtTop = prevFirstKey.current !== undefined && firstKey !== prevFirstKey.current
    prevFirstKey.current = firstKey
    const el = boxRef.current
    if (!el) return
    const before = prevHeight.current
    prevHeight.current = el.scrollHeight
    // Only when content changed ABOVE the viewport and the user is reading
    // history rather than following the tail. A prepend gives its rows fresh
    // keys, so the first row's key moving is the signal; an append never
    // touches it.
    if (!changedAtTop || follow) return
    el.scrollTop = preservedScrollTop(el.scrollTop, before, el.scrollHeight)
    onPrependAdjust?.(el.scrollTop)
  }, [rows, follow, onPrependAdjust])
```

6. Render the control as the FIRST child inside the scroll box. Replace the `<div ref={boxRef} ...>{body}</div>` children with:

```tsx
        {loadingEarlier ? (
          <div className="pb-1 text-[11px] text-fg-mute">Loading earlier...</div>
        ) : canLoadEarlier ? (
          <div className="pb-1">
            <PillButton className="!px-3 !py-1 !text-[10px]" onClick={stream.loadEarlier}>
              Load earlier
            </PillButton>
          </div>
        ) : null}
        {body}
```

- [ ] **Step 4: Run and watch it pass**

```powershell
npm test -- src/jobs/LogView.test.tsx src/jobs/LogTab.test.tsx src/jobs/TaskLogPage.test.tsx
npx tsc -b
```

Expected: all PASS. The existing `'shows the truncation notice with real counts, then the eviction notice'` test needs `earlierComplete: true` added to its `streamOf` calls, because `historyTruncated` is now the THIRD branch and the tail notice would otherwise win; make that edit and say why in the commit.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/LogView.tsx web/src/jobs/LogView.test.tsx
git commit -m "feat(web): Load earlier control, tail notice, and a prepend scroll anchor

The notice resolves eviction first, then the tail case, then the forward-walk
case, which can now only appear after a recovery that hit MAX_BACKFILL_PAGES.
Retained LINES and server-side ENTRIES stay named as separate units.

The viewport anchor keys off the first row's key changing, since a prepend
assigns fresh keys and an append does not. jsdom reports every geometry as 0,
so the component test asserts the DECISION through an injected seam and
preservedScrollTop owns the arithmetic."
```

---

## Task 13: the `prev_seq` fixture sweep and the secrecy cycle (FRONTEND)

**Files:**
- Modify: `web/src/jobs/JobDetailPage.test.tsx`, `logSecrecy.test.tsx`, `TaskLogPage.test.tsx`, `useTaskLogStream.test.tsx`, `api.test.ts`, `detailApi.test.ts`

`prev_seq` is now required on `TaskLogPage`, but every SPA fixture is a hand-written literal passed to `HttpResponse.json`, so the compiler will not find a missing one. A missing `prev_seq` decodes as `undefined`, which is not `0`, which leaves `earlierComplete` false and "Load earlier" enabled. Sweep deliberately rather than waiting for a failure to point at one.

- [ ] **Step 1: Find every fixture**

```powershell
cd D:/dev/relay/.claude/worktrees/web-d-tasklog-tail/web
npx rg -n "next_seq" src
```

Expected: roughly 37 raw hits, of which the object literals served to `/logs` are the ones that matter (a test title and comments also match). Every hit inside an object literal served to `/logs` must also carry `prev_seq`; reconcile the list by hand.

- [ ] **Step 2: Add `prev_seq` to each**

Default to `prev_seq: 0` (a complete log, no control) everywhere EXCEPT where a test deliberately wants more history, which is only the tests written in Tasks 10 to 12. Keep the fixtures hand-written; do not introduce a helper that marshals through `TaskLogPage`, because a fixture built from the production type agrees with the decoder by construction and can never detect drift in either direction.

- [ ] **Step 3: Extend the secrecy cycle with a prepend**

In `web/src/jobs/logSecrecy.test.tsx`, in `'no console method ever receives log content, across mount-stream-drop-unmount'`: serve a tail page with a non-zero `prev_seq` so the control is live, and drive one `loadEarlier` whose page carries the secret, before the drop. Add to the handler and the body:

```tsx
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      if (new URL(request.url).searchParams.has('before_seq')) {
        return HttpResponse.json({
          items: [{ seq: 1, stream: 'stdout', content: `${SECRET}\n`, created_at: '2026-08-09T00:00:00Z' }],
          next_seq: 0,
          prev_seq: 0,
          total: 2,
        })
      }
      return HttpResponse.json({
        items: [{ seq: 9, stream: 'stdout', content: 'tail\n', created_at: '2026-08-09T00:00:00Z' }],
        next_seq: 0,
        prev_seq: 9,
        total: 2,
      })
    }),
  )
```

and after the hook reaches `'live'`:

```tsx
  await act(async () => {
    result.current.loadEarlier()
  })
  // Positive control: the prepended content really did flow through the code
  // path under test.
  await waitFor(() => expect(result.current.rows.some((r) => r.text === SECRET)).toBe(true))
```

Import `act` from `@testing-library/react` if it is not already imported there.

- [ ] **Step 4: Run the whole suite**

```powershell
npm test
npx tsc -b
```

Expected: everything green. Then re-run the sweep check and confirm every `/logs` fixture carries both keys:

```powershell
npx rg -n "next_seq" src --stats
npx rg -n "prev_seq" src --stats
```

The two counts should now be equal except for `api.ts` (where only `next_seq` appears in a doc sentence) - reconcile any other difference by hand rather than by assuming.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs
git commit -m "test(web): prev_seq fixture sweep and a prepend in the secrecy cycle

prev_seq is required on TaskLogPage but every fixture is a hand-written
literal, so nothing would have caught a missing one: it decodes as undefined,
which is not 0, which leaves Load earlier enabled and adds a request the test
did not expect.

The secrecy cycle now covers mount, tail, PREPEND, drop and unmount - a
console call added on the prepend path would otherwise be invisible to it."
```

---

## Task 14: gates before the PR (EITHER)

- [ ] **Step 1: Go gates**

```powershell
cd D:/dev/relay/.claude/worktrees/web-d-tasklog-tail
make test
go vet ./...
go test -tags integration -p 1 ./internal/api/... -timeout 900s
go test -tags integration -p 1 ./internal/store/... -timeout 900s
```

All must be green. The lane split hides a class of half-finished change here: EVERY wire-level behaviour of this endpoint is behind `//go:build integration`, so a signature or envelope change can be fully green in the default lane. Actually run the integration lane; do not infer it from `make test`.

- [ ] **Step 2: Web gates**

```powershell
cd D:/dev/relay/.claude/worktrees/web-d-tasklog-tail/web
npm test
npx tsc -b
npm run build
```

- [ ] **Step 3: Race lane**

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

Run from the worktree root under Git Bash. If the lane is genuinely unavailable, SAY SO in the PR body rather than substituting `-count=N`, which re-runs under the ordinary scheduler and cannot observe an unsynchronised access. (This slice adds no new concurrency, so the expected result is a clean pass that says nothing new.)

- [ ] **Step 4: Reset `web/dist`**

`web/dist` is tracked but stale, and `npm run build` dirties it. It is never maintained per-PR.

```powershell
cd D:/dev/relay/.claude/worktrees/web-d-tasklog-tail
git checkout -- web/dist/
git status --short
```

Expected: `git status` clean apart from anything still uncommitted. `web/dist` must NEVER be staged; never use `git add -A`.

- [ ] **Step 5: Final tree check**

```powershell
git log --oneline origin/main..HEAD
git diff --stat origin/main..HEAD
git ls-files --eol internal/store/tasks.sql.go internal/store/query/tasks.sql README.md
```

Expected: 12 or 13 commits, no `web/dist` in the diffstat, `i/lf` on every listed path.

---

## Notes for the conductor, not for the engineer

- **The engineer must NOT run `/backlog close`.** The conductor closes `docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md` with `/backlog close`, which does the `git mv` into `docs/backlog/closed/` and stamps the frontmatter. Flipping `status` by hand leaves a malformed open item.
- Per spec D22, **the two required follow-up items must be filed BEFORE the close lands**, or content is lost in the move: `idea-2026-09-01-task-log-row-virtualization` and `idea-2026-09-01-task-log-export-endpoint`. The export item must carry the byte-exactness foreclosure verbatim (the agent normalises CRLF to LF in `chunkWriter` before a chunk is sent), because after the close it can no longer refer back to the open file. Two optional items are also proposed in the spec: `idea-2026-09-01-tail-mode-for-the-cli-python-sdk-and-mcp` and `idea-2026-09-01-in-log-search`.
- **This plan is ONE unit of work: one branch, one PR, one session.** It is not staged and needs no `/backlog phases` run.
- Phase 4 verification lanes worth naming when the fan-out is dispatched: the invariants lens should check the generation ordering in `loadEarlier` against the acquire direction as well as the release direction; the correctness lens should check the seam join against a batch whose dangling fragment belongs to a stream with no completed line in the window; the security lens should confirm D11 (authorization unchanged, not tightened and not loosened) and that no log content reaches a `console` method on the new prepend path.

---

## Self-review

**Spec coverage.** Criteria 1 to 6 -> Task 1. 7 and 10 -> Task 3. 8, 9, 11, 12, 13 -> Task 4. 14 (existing tests unmodified) -> Task 3 Step 4 and Task 4 Step 3. 15 to 19 -> Tasks 8 and 9. 20 to 23 -> Task 10. 24 to 27 -> Tasks 10 and 11. 28 -> Task 6. 29 -> Task 12. 30 -> Task 13. 31 -> Task 5. 32 -> Task 14. D19's injected callback -> Task 12. D22's item disposition -> conductor notes.

**Type consistency.** `taskLogQuery` / `taskLogOrderAsc` / `taskLogOrderDesc` are used identically in Tasks 1, 3 and 4. `GetTaskLogsTailPageParams{TaskID, RowLimit}` and `GetTaskLogsBeforePageParams{TaskID, BeforeSeq, RowLimit}` are named by the `sqlc.arg()` names chosen in Task 2 and read back in Task 3, with a Step 4 check that reads the generated struct rather than assuming it. `prependEntries`, `preservedScrollTop`, `minSeq` are defined in Tasks 7 to 9 and consumed under those exact names in Tasks 11 and 12. `canLoadEarlier`, `loadingEarlier`, `earlierComplete`, `loadEarlier` are added to `TaskLogStreamResult` in Tasks 10 and 11 and consumed in Task 12, and both `streamOf` helpers are updated in Task 11 Step 4 where `tsc` first flags them.
