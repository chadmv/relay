# Bound agent-driven log volume on the gRPC recv goroutine

- Date: 2026-08-15
- Backlog item: `docs/backlog/bug-2026-08-12-tasklog-err-limiter-attacker-keyed.md`
- Verified against: worktree `sad-mccarthy-6053a2`, branch `claude/pr-merge-main-2d2fc3`, even with `origin/main` @ `f6226d3`
- Gate mode: autonomous. Every call recorded in section 11.

---

## 1. Problem

`internal/worker/handler.go` has one rate limiter guarding one of four caller-driven `log.Printf`
sites on the agent ingest path, and that limiter is keyed on values the caller supplies. The other
three sites have no limiter at all. Every one of them runs synchronously on the `Connect` recv
goroutine and serializes on the `log` package's process-global mutex, which every other connection
and every HTTP handler also uses.

So a principal holding one valid agent token can drive one `log.Printf` per gRPC message,
indefinitely, and the cost lands on its own ingest path first and on every other worker's second.

This spec replaces the wire-keyed limiter with a **per-connection budget that is bounded by
construction**, and routes every caller-driven log line on the recv path through it. It separates
two jobs the current code conflates: **deduplication** (a diagnostics-quality concern, keyed on
whatever is useful) and **the bound** (a security control, keyed on nothing the caller supplies).

This slice touches no SQL, no migration and no generated file.

---

## 2. What the code actually does, verified at HEAD

Every claim in this section was read in the tree.

### 2.1 The limiter

`internal/worker/handler.go:705-752`:

```go
const taskLogErrLimiterMax = 1024

var taskLogErrs taskLogErrLimiter

type taskLogErrLimiter struct {
	mu       sync.Mutex
	reported map[string]int32 // task id -> the assignment epoch already logged
}

func (l *taskLogErrLimiter) shouldLog(taskID string, epoch int32) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.reported == nil {
		l.reported = make(map[string]int32)
	}
	if got, ok := l.reported[taskID]; ok && got == epoch {
		return false
	}
	if len(l.reported) >= taskLogErrLimiterMax {
		l.reported = make(map[string]int32)
	}
	l.reported[taskID] = epoch
	return true
}
```

One production call site, `handler.go:831`:

```go
if !errors.Is(err, pgx.ErrNoRows) && taskLogErrs.shouldLog(chunk.TaskId, int32(chunk.Epoch)) {
	log.Printf("worker: handleTaskLog AppendTaskLog %s: %v", chunk.TaskId, err)
}
```

`chunk.TaskId` and `chunk.Epoch` are proto fields off the wire. The only upstream validation is
`taskID.Scan(chunk.TaskId)` at `handler.go:774`, which checks that the string *parses* as a UUID and
returns silently otherwise. It does not check that the task exists.

Also true, and one detail the state is global: `taskLogErrs` is a package-level var behind a
`sync.Mutex` shared by every connection in the process, with a test hook
`ResetTaskLogErrLimiterForTest` (`export_test.go:39-43`).

### 2.2 The reachable error class, and why the fence does not stop it

The limiter is consulted only when `err` is **not** `pgx.ErrNoRows`. The item's central claim is that
a NUL byte or invalid UTF-8 in `chunk.Content` produces such an error, and produces it *before* the
fence is evaluated. **Confirmed, by three independent lines of evidence.**

**Mechanism.** `AppendTaskLog` (`internal/store/query/tasks.sql:266-278`) binds `content` as a
parameter:

```sql
WITH fence AS (
    SELECT t.job_id FROM tasks t
    WHERE t.id = sqlc.arg(task_id)
      AND t.assignment_epoch = sqlc.arg(assignment_epoch)
      AND t.worker_id = sqlc.arg(worker_id)
      AND (t.status IN ('pending', 'dispatched', 'running')
           OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
), ins AS (
    INSERT INTO task_logs (task_id, stream, content)
    SELECT sqlc.arg(task_id), sqlc.arg(stream), sqlc.arg(content) FROM fence
    RETURNING id, created_at
)
SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence;
```

Nothing in the repo sets a pgx `QueryExecMode` (grepped tree-wide: zero hits), so pgx runs its
default extended-protocol mode. In the extended protocol, PostgreSQL converts each text-format
parameter with its type's input function while processing the **Bind** message, before the portal is
executed. `text` input runs `pg_verify_mbstr`, which rejects an embedded NUL with SQLSTATE `22021`
and the message `invalid byte sequence for encoding "UTF8": 0x00`. The statement never executes, so
the fence CTE never runs, so the fictitious task id and the wrong assignee are both irrelevant.

**Prior empirical verification.** The 2026-08-12 review executed the generated statement against a
real Postgres and recorded the result in the item; the same finding is written into the test's own
scope note at `internal/worker/handler_tasklog_integration_test.go:345-352`.

**Positive evidence from the adjacent slice.** The 2026-08-14 trailing-window work added a *third*
predicate to that fence and `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` passed
with no edit at all - the fence predicate set is invisible to a NUL-bearing chunk.

Errors in this class are the only *caller-forceable* non-`ErrNoRows` errors on this path. Everything
else that can fail here (pool exhaustion, context cancellation, a lost connection) is an infra
condition. `stream` is derived server-side from a proto enum and `min_finished_at` from the server
clock, so neither carries caller bytes.

### 2.3 The flood needs neither a fresh task id nor the overflow reset

This is the sharpest finding in this spec and the item does not state it.

`shouldLog` stores **one entry per task id**, with the epoch as the *value*:
`l.reported[taskID] = epoch`. So with a single fixed task id and a varying `chunk.Epoch`:

- `got, ok := l.reported[taskID]` hits, `got != epoch`, so the early return is skipped;
- `len(l.reported)` is 1, so the capacity branch never fires;
- the entry is overwritten and `shouldLog` returns `true`.

One log line per message, forever, with a map of exactly one entry and `reset()` never called.

Two consequences the plan must carry:

1. **Repairing `reset()` alone repairs nothing.** The at-capacity behaviour is a real defect, but it
   is not *the* defect, and a patch that only changes reset-to-suppress leaves the vector fully open.
2. **A composite key is not optional.** Any fix that keeps `map[taskID]epoch` shape - including the
   item's own proposal keyed on `(workerID, taskID, epoch)` if `epoch` stays the map *value* - keeps
   this hole. The epoch must be part of the key, or the capacity cap is never reached and can never
   bind.

Memory is not the exposure here. The map is already bounded at 1024 small entries. The unbounded
resource is the process-global `log` mutex, which is why this is a cross-tenant degrade vector rather
than a memory problem.

### 2.4 Half B exists, in `handleTaskStatus`, exactly as described

`handler.go:464-475`:

```go
	var taskID pgtype.UUID
	if err := taskID.Scan(upd.TaskId); err != nil {
		log.Printf("worker: handleTaskStatus bad task id %q: %v", upd.TaskId, err)
		return
	}

	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		log.Printf("worker: handleTaskStatus GetTask %s: %v", upd.TaskId, err)
		return
	}
```

Both unconditional, both keyed on nothing, both ahead of the identity gate (`:542`) and the currency
gate (`:555`). A well-formed random UUID reaches the second one via `pgx.ErrNoRows` from `GetTask`.
Malformed bytes reach the first one without even a query.

The three *post*-gate log sites in the same function (`:604` `IncrementTaskRetryCount`, `:649`
`UpdateTaskStatus`, `:656` `FailDependentTasks`) are **not** caller-forceable: they are already
wrapped in `!errors.Is(err, pgx.ErrNoRows)`, they are reachable only by the task's real assignee at
the current epoch, and no caller-supplied bytes reach any of those three statements (`statusStr`
comes from a closed switch; the timestamps come from the server clock; `FailDependentTasks` takes
only a task id). They are left alone. Section 6.6 covers them anyway.

### 2.5 A third instance the item does not name, one line away

`Connect`, `handler.go:177-180`:

```go
		case *relayv1.AgentMessage_WorkspaceInventory:
			if err := h.applyInventoryUpdate(ctx, workerUUID, p.WorkspaceInventory); err != nil {
				log.Printf("worker: inventory update failed: %v", err)
			}
```

`applyInventoryUpdate` (`handler.go:994-1009`) binds `u.SourceType`, `u.SourceKey`, `u.ShortId` and
`u.BaselineHash` straight from the wire into `UpsertWorkerWorkspace` / `DeleteWorkerWorkspace`. A NUL
byte in any of them produces the same bind-time `22021`, so the same unbounded `log.Printf` per
message, with no limiter of any kind and no gate ahead of it.

The item's own Notes ask for exactly this grep ("worth a grep for other map keys and log arguments
derived from `chunk.*` or `upd.*`"). Including it is section 11's decision D3.

### 2.6 The full caller-driven log surface on the recv goroutine

Grepped `log.Printf` across `internal/worker` and classified:

| Site | Caller-forceable? | Disposition |
| --- | --- | --- |
| `:158` worker id unusable | No - registration only, once per connection | Unchanged |
| `:179` inventory update failed | **Yes** - NUL in any inventory string | Budgeted |
| `:344` auto-enrolled | No - once per connection | Unchanged |
| `:375` register inventory replace | No - registration only | Unchanged |
| `:467` handleTaskStatus bad task id | **Yes** - any unparseable string | Budgeted, deduped |
| `:473` handleTaskStatus GetTask | **Yes** - any random UUID | `ErrNoRows` dropped; other errors budgeted |
| `:604` IncrementTaskRetryCount | No (2.4) | Unchanged |
| `:649` UpdateTaskStatus | No (2.4) | Unchanged |
| `:656` FailDependentTasks | No (2.4) | Unchanged |
| `:832` handleTaskLog AppendTaskLog | **Yes** - limiter defeatable (2.3) | Budgeted, deduped |
| `:854` handleTaskLog marshal | No - `json.Marshal` of a struct of strings/ints cannot fail | Unchanged |

Four sites to fix. Three of them are one line each; the fourth is the limiter rewrite.

---

## 3. Discrepancies between the item and HEAD

Most important first.

1. **The item's own proposal fails open.** "Key on `(workerID, taskID, epoch)` and cap per worker" is
   correct only if `epoch` becomes part of the map *key*. Implemented as the current shape suggests -
   a per-worker `map[taskID]epoch` with a cap - a single worker varying `chunk.Epoch` on one task id
   never grows the map, never reaches the cap, and floods exactly as today (section 2.3). The item
   never states that the epoch is stored as a value rather than a key, and its proposal reads
   naturally as preserving that shape. This is the single most important correction here.

2. **The item frames `reset()` as "the specific defect"; it is not sufficient and not necessary.**
   Not sufficient: the epoch-varying flood needs no overflow at all. Not necessary as a *memory*
   concern: 1024 entries were already bounded. `reset()` is a real defect but only as a
   re-arming mechanism, and it becomes harmless the moment something else is the actual bound
   (section 6.3).

3. **One acceptance criterion cannot be met inside the stated scope.** The item requires "a bounded
   number of `GetTask` round trips, proven by a test that is RED against today's handler". There is
   no way to know a well-formed UUID names no task without querying, the item forbids a new query or
   a cache, and it puts a general recv-loop rate limiter out of scope (deferring to the tasklog
   spec's section 10). Dropping status messages once a budget is exhausted is not an option: it would
   strand real tasks. Section 4 argues the query cost is *already* bounded at one in-flight query per
   connection, which is the same bound legitimate traffic has, so the criterion is also not load-
   bearing. **Proposed amendment to the item: strike that criterion and record that the query cost
   belongs to the recv-loop limiter item, not this one.**

4. **A third instance exists that the item does not enumerate** (section 2.5, the inventory line),
   though the item's Notes ask for the grep that finds it.

5. **The item's claim that half B is "strictly more expensive per message than half A"** is right on
   the round trip and wrong on the whole cost. Half A's chunks also cost a round trip (the failed
   `AppendTaskLog` Bind), so the two are equal on queries; half B is cheaper for the *attacker*
   because it needs no content trick, which is the practically relevant asymmetry.

Everything else in the item is accurate as filed: the keys are wire-supplied, overflow resets rather
than suppresses, the limiter is global with a test reset hook, both half-B lines exist and sit ahead
of both gates, `%q` and `%v`-not-`Detail` are the correct injection and secret-leak defences, and the
2026-08-14 amendment's claims about bind-time decode and about the two slices' independence are
correct.

---

## 4. Threat model and honest exposure

**Principal.** Anything with a valid long-lived agent token, or anything at all when
`RELAY_ALLOW_AUTO_ENROLL` is on. A compromised or buggy agent is the realistic case; a malicious
insider with one token is the adversarial one.

**What is unbounded.** Log lines, and through them the process-global `log` mutex. `log.Printf`
serializes every writer in the process - every other agent connection's error paths and every HTTP
handler that logs. That is the cross-tenant part and it is the whole vulnerability.

**What is already bounded, and should not be claimed as part of the fix.**

- *Queries.* Each connection's recv loop is synchronous, so one connection has at most one in-flight
  statement. A flooding agent consumes one pool connection, which is the same footprint a legitimate
  agent has. The aggregate is bounded by connection count, not message rate.
- *Memory.* 1024 map entries today; section 6.3's design is smaller.
- *Disk.* Log volume is bounded by whatever ships the process's stderr, which is outside this repo.

**What this fix does not close.** A flooding connection still starves *its own* status, inventory and
telemetry ingest, because those messages queue behind the flood on its recv goroutine. That is
self-inflicted and correct. Bounding total message rate per connection is the recv-loop limiter item.

**Severity, honestly.** Medium. It requires a credential, it degrades rather than corrupts, and it
leaves an obvious trace. It is worth fixing because the defence is cheap and because the shape -
a rate limiter keyed on the rate-limited party's own input - recurs.

---

## 5. Scope decision: this spec covers ONE item, not two

`ROADMAP.md:32` says: "**Coordinate with fence-rejection-is-unobservable** - both touch the same
silent `ErrNoRows` branch and must agree on one mechanism."

**Decision: two slices. This spec covers the limiter item only, both halves A and B (plus the
inventory line). The counter item ships separately.** I checked the roadmap's rationale rather than
deferring to it, because the last roadmap pairing recommendation of exactly this form was wrong on
both halves.

**The rationale is wrong on the first half and vacuous on the second.**

- *"The same silent `ErrNoRows` branch"* - they touch **complementary arms of the same `if`**, not the
  same branch. The counter goes where `errors.Is(err, pgx.ErrNoRows)` is **true**; everything in this
  spec lives where it is **false**. The two code paths are disjoint by construction: no input can
  execute both. They share one line of source (`handler.go:831`) only because that line is a compound
  condition, and section 6.2 splits it, after which they share nothing at all.
- *"Must agree on one mechanism"* - there is no mechanism to agree on. The counter item's own
  proposal says "explicitly NOT a log line"; this item is entirely about log lines. The only genuine
  coupling is a design question - "if counters exist, should a parse failure be a counter rather than
  a budgeted log line?" - and section 11 D2 settles it in a way that stays correct either way.

**Positive reasons to split, beyond refuting the pairing.**

- **The counter item carries an unbounded dependency.** It verified at `ee88de0` that there is *no
  coordinator-level counters endpoint*, so its read surface is either an extension of
  `GET /v1/workers/stats` or a whole new endpoint with its own authorization question
  (`feature-2026-08-09-server-info-allowlist-endpoint`). Merging would block a security fix behind an
  API design decision.
- **Different maturity.** This is a `bug` with a settled threat model. That is an `idea` whose own
  text says its shape is "to be argued at spec time rather than adopted as written" and lists three
  unsettled sub-decisions (where the counter lives, whether it is per-worker, where it is read).
- **Test strategies interfere in one specific, avoidable way.** Both slices' tests are counting
  assertions over the same handler. This slice's tests count *log lines*; that slice's count
  *rejections*. Combined, a reviewer confirming a RED has two candidate causes for every number, and
  this slice's whole value is that its REDs are unambiguous.
- **`metrics.Store` is a per-worker sample store.** Adding a global counter to it is a shape change
  in a package this slice does not otherwise touch.
- **Attribution.** This slice must be reviewable as a security fix in isolation.

**What this spec owes the counter item.** One thing, and it costs nothing: section 6.2 splits
`handler.go:831` into two explicit arms and leaves the `ErrNoRows` arm as a named, commented,
side-effect-free branch with a `return`. The counter item then becomes a one-line insertion into a
branch that already exists, with no re-litigation of the condition. The comment on that arm states
that it must never gain a log line, and cites this spec.

**Sequencing recommendation.** This slice first, counter second, unchanged from the roadmap's
ordering intent.

---

## 6. Design

### 6.1 Two layers, because the current code conflates two jobs

The existing limiter is one map doing two jobs:

- **Deduplication** - "a realistic persist failure repeats for every chunk of a task, so collapse
  them to one line". This is a diagnostics-quality concern. Keying it on wire values is *fine*,
  because a caller that varies the key only makes its own diagnostics noisier.
- **Bounding** - "no caller may consume unbounded log budget". This is a security control. Keying it
  on wire values is *fatal*.

Today the dedupe map is also the bound, which is why one wire field defeats both. The design keeps
both jobs and separates them:

```
                 dedupe (map, wire-keyed, best-effort)
   log line ->   |  seen before? -> drop, spend nothing
                 |  new key?     -> ask the budget
                 v
                 budget (token bucket, per connection, keyed on nothing)
                 |  token available? -> spend one, record the key, LOG
                 |  empty?           -> drop, do NOT record the key
```

The order is load-bearing. Dedupe **before** the budget means the honest repeating failure spends
exactly one token no matter how many chunks it produces. Not recording a key that was suppressed for
lack of a token means the diagnostic reappears when tokens refill, rather than being permanently
swallowed.

### 6.2 The call site, split into two arms

`handleTaskLog`'s error handling becomes:

```go
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// <existing three-meanings comment block, unchanged>
			//
			// This arm is deliberately side-effect-free and MUST stay silent: a log
			// line here would be caller-driven volume on the recv goroutine, and it
			// would fire on the legitimate late-flush case. Observability for this
			// arm is idea-2026-08-14-tasklog-fence-rejection-is-unobservable, whose
			// answer is a counter, not a log line. Nothing here may publish.
			return
		}
		if lim.allow(logKey{kind: kindTaskLogPersist, id: chunk.TaskId, epoch: chunk.Epoch}) {
			// <existing never-log-chunk.Content comment block, unchanged>
			log.Printf("worker: handleTaskLog AppendTaskLog %s: %v", chunk.TaskId, err)
		}
		return
	}
```

Semantically identical to today's compound condition. Justified on its own readability grounds - the
current single condition conflates "is this the fence rejecting" with "should we log" - and it is
what section 5 owes the counter item.

Note `epoch: chunk.Epoch` at **full int64 width**, not `int32(chunk.Epoch)`. Free, one fewer
truncation site. The fence parameter's `int32(chunk.Epoch)` narrowing at `handler.go:796` is
**untouched**; that is `bug-2026-08-12-tasklog-epoch-int32-truncation` and stays open (section 10).

### 6.3 The limiter

```go
// ingestLogLimiter bounds caller-driven log volume for ONE agent connection.
//
// It is two things stacked, and the split is the whole point. `seen` is a
// DEDUPLICATOR: it collapses a repeating failure to one line, and it is keyed on
// wire-supplied values on purpose, because a caller that varies the key only
// makes its own diagnostics noisier. `tokens` is the BOUND: it is keyed on
// nothing, so no wire value can enlarge it.
//
// The predecessor (taskLogErrs, removed 2026-08-15) used the dedupe map AS the
// bound, so one wire field defeated both. Do not merge these back together, and
// in particular do not delete the bucket on the grounds that the map already
// limits things - the map deliberately does not.
//
// Owned by one goroutine: allocated in Connect and used only from that
// connection's recv loop. No mutex, by design - adding one would be the second
// lock on this path after the log package's own. If a caller ever appears off
// that goroutine, that caller is the finding, not the missing lock.
type ingestLogLimiter struct {
	seen   map[logKey]struct{}
	tokens int
	last   time.Time
	now    func() time.Time // injectable for the refill test only
}

type logKey struct {
	kind  uint8
	id    string // populated only by kindTaskLogPersist
	epoch int64  // populated only by kindTaskLogPersist
}
```

`allow(k logKey) bool`, in order:

1. **Refill.** `n := int(l.now().Sub(l.last) / ingestLogRefill); if n > 0 { l.tokens = min(l.tokens+n, ingestLogBurst); l.last = l.last.Add(time.Duration(n) * ingestLogRefill) }`.
   Advancing `last` by *whole consumed intervals* rather than to `now` is load-bearing: setting
   `last = now` on a call that added zero tokens means a connection called more often than the
   refill interval never refills at all. That is the most likely way to get this wrong.
2. **Dedupe.** `if _, ok := l.seen[k]; ok { return false }`.
3. **Spend.** `if l.tokens == 0 { return false }` - and return *without* touching `seen`.
4. **Capacity.** `if len(l.seen) >= ingestLogSeenMax { clear(l.seen) }`.
5. `l.tokens--; l.seen[k] = struct{}{}; return true`.

Constants, all per connection, all untunable:

| Constant | Value | Why |
| --- | --- | --- |
| `ingestLogSeenMax` | 128 | Dedupe capacity. Far above any realistic count of distinct concurrently-failing tasks on one agent, so the honest case never clears. |
| `ingestLogBurst` | 16 | Tokens at connection start. Covers a burst of several tasks failing at once at connection start without waiting on refill. |
| `ingestLogRefill` | 10s | Steady state 6 lines/min/connection. A genuine repeating infra failure still reports continuously; a flood is 6 lines/min instead of one per message. |

**No env knob.** An operator raising it re-opens the vector, and no value here has an operational
reason to move. State that in the comment so nobody adds one.

**At capacity, the map is cleared, and that is safe here.** Clearing re-arms every key, which is
exactly the 2026-08-12 defect - but only when the map is the bound. With the bucket as the bound,
re-arming can at worst produce `ingestLogBurst` extra lines and then 6/min. The alternative,
permanent suppression, is bounded too but has **no time-based recovery**: a connection that once
tripped 128 distinct failures loses the diagnostic for its whole lifetime, and this project has
already recorded that a recovery bound must be time-based. The comment must say that the clear is
safe *because of* the bucket, so that removing the bucket is visibly the thing that re-opens the bug.

### 6.4 Key granularity, per site

| Site | Key | What it trades |
| --- | --- | --- |
| `handleTaskLog` persist failure | `{kindTaskLogPersist, chunk.TaskId, chunk.Epoch}` | Preserves exactly today's diagnostic - one line per task per generation - because the dedupe layer is no longer the bound. This is what keeps the existing test's `1` then `2` assertions byte-identical. |
| bad task id (**both** handlers) | `{kindBadTaskID}` - no wire value | One line per connection per dedupe generation, showing the *first* offending id (`%q`). The content of the second malformed id adds nothing an operator can act on; the fact that this agent sends malformed ids is the whole signal. Trades per-id detail for a key the caller cannot vary. |
| `GetTask` non-`ErrNoRows` | `{kindStatusGetTask}` - no wire value | A pool or context failure is not per-task; keying on the task id would multiply one infra event by the task count. Trades per-task attribution for one honest line per infra episode. |
| inventory update failure | `{kindInventory}` - no wire value | Same reasoning. |

**Why not key on the authenticated worker, as the item proposes.** Per-connection state makes the
worker id redundant: the limiter is reachable only from that connection's recv goroutine, so it is
*already* per worker, and per *connection* is strictly tighter (two connections claiming the same
worker get separate budgets). Keying a global map on the worker id instead would keep the global
mutex, need eviction on disconnect, and still need the composite-key fix of section 2.3. And keying
on the worker *alone* - one line for all of a worker's tasks - would hide a genuine multi-task
persist failure, which is precisely the diagnostic the existing comment exists to protect.

### 6.5 The `GetTask` `ErrNoRows` line is deleted, not budgeted

A status update naming a task that does not exist is dropped **silently**, with no budget spend and
no line:

```go
	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		// pgx.ErrNoRows here means the named task does not exist. That is
		// indistinguishable from a forged message, carries nothing an operator can
		// act on, and is the cheapest message an attacker can send - so it is
		// dropped silently, exactly as handleTaskLog drops an unresolvable chunk and
		// exactly as both gates below drop a rejected one. It also has one
		// legitimate cause: DeleteJob cascades to tasks, so a task row can vanish
		// under a running agent, and there is nothing to do about it either.
		// Any other error is real infrastructure and logs under the connection's
		// budget.
		if !errors.Is(err, pgx.ErrNoRows) && lim.allow(logKey{kind: kindStatusGetTask}) {
			log.Printf("worker: handleTaskStatus GetTask %s: %v", upd.TaskId, err)
		}
		return
	}
```

This matches the shape the retry-resurrect slice already applied to both *write*-error sites in this
same function, so the function ends up internally consistent: every `ErrNoRows` in `handleTaskStatus`
is silent, every real error is budgeted.

`DeleteJob` and `tasks.job_id ... ON DELETE CASCADE` were both verified in the tree
(`internal/store/query/jobs.sql:109`, `internal/store/migrations/000001_initial.up.sql:53`), so the
legitimate cause in that comment is real, not hypothetical.

### 6.6 The asymmetry the item asks to settle: both handlers log a bad task id, under one key

Today `handleTaskStatus` logs an unparseable task id and `handleTaskLog` returns silently. The item
requires picking one behaviour for both and saying why.

**Decision: both log, once per connection, sharing one key.** `handleTaskLog` gains:

```go
	var taskID pgtype.UUID
	if err := taskID.Scan(chunk.TaskId); err != nil {
		// Deliberately symmetric with handleTaskStatus's identical guard, and they
		// share ONE budget key (kindBadTaskID) so an agent malforming ids on both
		// streams costs one line, not two. Logging here is safe only because of the
		// per-connection budget: before it existed this was silent precisely because
		// an unbounded line on this path is a flood vector. An agent sending
		// unparseable ids on the log path loses 100% of that task's output with no
		// other signal anywhere, which is worth one line per connection.
		if lim.allow(logKey{kind: kindBadTaskID}) {
			log.Printf("worker: handleTaskLog bad task id %q: %v", chunk.TaskId, err)
		}
		return
	}
```

The `%q` is mandatory at both sites and is the injection defence; it is unchanged on the status side.

The alternative - make both silent - is defensible and cheaper, and section 11 D2 records why it was
not taken: the budget is exactly the thing that makes the "log it" direction safe, and this is the
one failure mode on the log path that produces total, silent data loss.

### 6.7 Where the state lives, and the test seam

**Production.** `Connect` allocates one limiter after `workerUUID` is resolved and before the message
loop, and passes it into the three call sites:

```go
	lim := newIngestLogLimiter()

	for {
		...
		switch p := msg.Payload.(type) {
		case *relayv1.AgentMessage_TaskStatus:
			h.handleTaskStatus(ctx, workerUUID, lim, p.TaskStatus)
		case *relayv1.AgentMessage_TaskLog:
			h.handleTaskLog(ctx, workerUUID, lim, p.TaskLog)
		case *relayv1.AgentMessage_WorkspaceInventory:
			h.handleInventoryUpdate(ctx, workerUUID, lim, p.WorkspaceInventory)
		...
```

The inventory case becomes a thin `handleInventoryUpdate` wrapper (call `applyInventoryUpdate`, then
the budgeted log) purely so the behaviour is testable at the same layer as the other three. It adds
no logic.

The limiter's lifetime is the connection's, so it is garbage-collected on disconnect with no eviction
path to get wrong, and one connection can neither consume nor suppress another's budget - by
construction, not by a key.

**Package-level `taskLogErrs`, `taskLogErrLimiter`, `taskLogErrLimiterMax` and
`ResetTaskLogErrLimiterForTest` are deleted.** Net removal of global mutable state and one test hook.

**Test seam, designed to avoid 43 mechanical edits.** `HandleTaskLog` and `HandleTaskStatus` in
`export_test.go` keep their current signatures and allocate a **throwaway limiter per call**, so the
~43 existing call sites across four test files compile and behave unchanged. Two new wrappers,
`HandleTaskLogWithLimiter` / `HandleTaskStatusWithLimiter`, take an opaque `*LimiterHandle` (the
`SenderHandle` pattern already in this file) for the tests that assert on log volume.

The throwaway seam is fail-open by construction, so `export_test.go` must say so in as many words:
*"these wrappers exercise NO limiting; anything asserting on log-line counts must use the
`WithLimiter` form"*. That warning is the price of not touching 43 call sites, and it is worth
paying, because a 43-site mechanical diff would bury the assertion changes that actually matter.

---

## 7. Alternatives considered and rejected

- **Fix `reset()` to suppress instead of clearing.** The item's headline framing. Rejected: section
  2.3 shows it closes nothing - the epoch-varying flood never reaches capacity.
- **Key on the authenticated worker only.** The item's Proposal A. Rejected as the sole mechanism: it
  collapses every task on a worker to one line, hiding a real multi-task persist failure, and it
  still needs an at-capacity rule and a reset-vs-suppress decision. Section 6.4.
- **A pure token bucket with no dedupe.** Simplest possible, bounded by construction, no map at all.
  Rejected: it breaks the diagnostic the current limiter exists for. Eight chunks of one failing task
  would produce eight lines (up to burst), and
  `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` would go RED on its `1` assertion -
  which the item names as a contract.
- **Keep the limiter global, fix the key to `(workerID, taskID, epoch)` with a per-worker cap.**
  Smallest diff, no signature changes. Rejected: keeps a process-global mutex on the recv path, needs
  an eviction policy on disconnect, needs the same composite-key correction anyway, and preserves the
  shape where one shared structure's capacity behaviour is a security control.
- **A `sync.Mutex` on the per-connection limiter.** Cheap insurance against a future off-goroutine
  caller. Rejected: it is a second lock on a path whose whole complaint is lock contention, and the
  single-goroutine ownership is a property worth stating structurally rather than defending
  defensively. Section 8.5 pins it with a race-detector run.
- **An env knob for the budget.** Rejected. Section 6.3.
- **Rate-limiting the recv loop itself.** The general form, and the only thing that could bound the
  `GetTask` round trips. Out of scope here, already out of scope in the 2026-08-12 tasklog spec's
  section 10, and tracked as its own eventual item. Section 3, discrepancy 3.

---

## 8. Test strategy

All handler-layer, `//go:build integration`, in `internal/worker`, using the fixtures the
trailing-window slice left behind (`seedClaimedTask`, `captureLog`, `countLines`).

### 8.1 The two floods, and the one that discriminates

1. `TestHandleTaskLog_DistinctWireTaskIdsCannotFloodTheLog` - one `LimiterHandle`, 64 chunks with a
   freshly generated random UUID each and NUL-bearing content. Assert log lines `<= ingestLogBurst`.
   RED today: 64.
2. `TestHandleTaskLog_VaryingWireEpochOnOneTaskCannotFloodTheLog` - **the discriminating test.** One
   `LimiterHandle`, one fixed task id, 64 chunks at epochs 1..64, NUL content. Assert lines
   `<= ingestLogBurst`. RED today: 64, with the limiter map holding exactly one entry and `reset()`
   never called. This test is the permanent record of section 2.3 and must survive into the tree -
   it is the only test that stays RED against a fix that only repairs `reset()`.

### 8.2 The status path

3. `TestHandleTaskStatus_UnknownTaskIdsAreSilent` - 64 well-formed random UUIDs. Assert **zero**
   lines. RED today: 64.
4. `TestHandleTaskStatus_MalformedTaskIdsAreLoggedOncePerConnection` - 64 unparseable ids. Assert
   exactly 1 line, and that it contains the first id `%q`-quoted. RED today: 64.
5. `TestHandleTaskLog_MalformedTaskIdSharesTheBadIdBudget` - a malformed id on each handler with one
   shared `LimiterHandle` produces exactly 1 line total. Pins section 6.6's shared key.

### 8.3 The inventory line

6. `TestHandleInventoryUpdate_PersistFailureIsBounded` - 64 updates carrying a NUL in `SourceKey`.
   Assert lines `<= ingestLogBurst`. RED today: 64.

### 8.4 The limiter's own contract

7. `TestIngestLogLimiter_PerConnectionBudgetsDoNotInterfere` - two `LimiterHandle`s; exhausting one
   leaves the other at full burst.
8. `TestIngestLogLimiter_DedupeDoesNotSpendRepeatedTokens` - repeat one key 64 times, then present
   `ingestLogBurst - 1` fresh keys and assert every one of them logs. **This is the test that catches
   spend-before-dedupe**, which would otherwise pass tests 1-6 while burning a token per repeated
   chunk.
9. `TestIngestLogLimiter_RefillsOverTime` - drive `now` on the injected clock. Exhaust the bucket,
   advance by `3 * ingestLogRefill`, assert exactly 3 more lines. Prove RED by hardcoding a literal
   interval at the refill site; asserting the exported constant alone would prove nothing about the
   code consuming it.
10. `TestIngestLogLimiter_RefillDoesNotStallUnderFrequentCalls` - call `allow` 1000 times with the
    clock advancing 1ms per call (total 1s, well under one refill interval), then advance past one
    interval and assert a token arrives. RED against the `last = now` spelling in section 6.3 step 1.

### 8.5 Ownership

11. Run the existing `Connect`-level integration tests under `-race` (MSYS2 mingw64 gcc; see the
    project's race-detector note) as the check that the mutex-free limiter is genuinely
    single-goroutine. Not a new test - a required gate on existing ones.

### 8.6 Existing tests: what may change and what is a finding

- `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` **must keep its assertions
  byte-identical**: `1`, then `2`, plus the `secret` non-containment assertion and the positive
  control. Permitted edits, and nothing else: swap `h.HandleTaskLog` for
  `h.HandleTaskLogWithLimiter` with one handle threaded through, and delete the
  `worker.ResetTaskLogErrLimiterForTest()` line (the symbol no longer exists). **Any assertion that
  needs adjusting is a finding to report, not to fix** - it would mean the design changed the
  diagnostic contract.
- Any other test whose log-count assertions move is likewise a finding. The throwaway-limiter seam
  means `HandleTaskLog` now logs *every* failure where the global limiter previously collapsed them
  across tests, so a test that was silently relying on cross-test limiter state will surface here.
  That is information, not breakage.

### 8.7 Mutation matrix

| Mutation | Must go RED |
| --- | --- |
| Delete the token check in `allow` | 1, 2, 4, 6 |
| Restore `reset()`-as-the-bound (dedupe only, no bucket) | **2** |
| Move `epoch` from the key back to the map value | **2** |
| Drop `epoch` from the key entirely | existing `PersistFailure` test's `2` |
| Spend a token before the dedupe check | **8** |
| `last = now` instead of `last.Add(n*interval)` | **10** |
| Budget the `GetTask` `ErrNoRows` line instead of deleting it | 3 |
| Give each handler its own key kind for a bad id | 5 |
| Share one limiter across connections | 7 |

---

## 9. Constraint checks

- **Epoch fence (CLAUDE.md).** This slice adds no write to `tasks.status` or `task_logs`, changes no
  predicate, and changes no fence argument. `AppendTaskLog`'s status allow-list and its three
  predicates are untouched. The `pgx.ErrNoRows` drop stays silent and stays **before** the publish -
  section 6.2's split moves the `return` into its own arm without reordering anything relative to
  `h.broker.Publish`.
- **One bounded sender per gRPC stream.** Untouched; no sends added.
- **Identity-checked teardown.** Untouched; the limiter has no teardown (it dies with the frame).
- **No interior pointers across locks.** The limiter has no lock and never escapes its goroutine.
- **Single job-spec pipeline / single JSON entry point.** Not applicable.
- **One DB round trip on `handleTaskLog`.** Preserved exactly - no query, goroutine, queue or lock is
  added anywhere. `handleTaskStatus` gains no round trip. The limiter's hot path is one map lookup
  plus one integer compare.
- **`chunk.Content` never logged.** Unchanged, and the comment block explaining why `%v` on a
  `pgconn.PgError` is safe (severity + message + SQLSTATE, never `Detail`) moves verbatim into the
  new arm. `pgErr.Detail` appears nowhere.
- **`%q` on unparsed ids.** Preserved on the status side, and required on the new log-side line.
- **`internal/tokenhash.Hash`.** Not touched; no hashing in this slice.
- **Generated code.** No `.sql` change, so no `make generate`, no CRLF revert dance, no
  `*.sql.go`/`models.go` edit. If a plan step proposes one, that step is wrong.

---

## 10. Scope

**In.** `internal/worker/handler.go` (the limiter type and its constants, `Connect`'s three call
sites plus the new `handleInventoryUpdate` wrapper, `handleTaskLog`'s two error arms and its parse
guard, `handleTaskStatus`'s two pre-gate lines), `internal/worker/export_test.go` (two new wrappers,
one opaque handle, delete `ResetTaskLogErrLimiterForTest`), and the handler-layer tests in section 8.

**Out, explicitly.**

- The counter on the `ErrNoRows` arm - `idea-2026-08-14-tasklog-fence-rejection-is-unobservable`.
  Section 5. This slice leaves the branch shaped for it.
- `int32(chunk.Epoch)` at the fence parameter - `bug-2026-08-12-tasklog-epoch-int32-truncation`. The
  limiter key uses the full int64, the fence argument is untouched.
- Any bound on message *rate* or on `GetTask` round trips - the recv-loop limiter. Section 3,
  discrepancy 3.
- The three post-gate log sites in `handleTaskStatus` and the three registration-time sites. Section
  2.6 records why each is unreachable by a caller.
- `task_logs` volume caps and retention - separate items.

---

## 11. Decisions taken autonomously

Gate mode is autonomous; these would otherwise have been questions.

- **D1. Two slices, not one** (this item; the counter separately). Section 5. Would have escalated,
  because it contradicts a standing ROADMAP instruction. Called: split, because the two live on
  complementary arms of one `if` and the counter carries an unresolved endpoint dependency.
- **D2. A bad task id logs on BOTH handlers, under one shared key**, rather than both going silent.
  Section 6.6. Would have escalated: it *adds* a log line on the flood path in the slice that closes
  a flood vector. Called: log it, because the budget is what makes it safe and because a malformed id
  on the log path silently destroys 100% of a task's output.
- **D3. The inventory line is in scope**, though the item names only two halves. Section 2.5. Would
  have escalated as a scope extension. Called: include - it is one line in `Connect` plus a thin
  wrapper, it is the same defect the item's own Notes ask for a grep to find, and closing two of
  three doors is not closing the vector.
- **D4. The `GetTask` `ErrNoRows` line is deleted, not budgeted.** Section 6.5. Would have escalated:
  it removes an existing diagnostic. Called: delete, because its only legitimate cause (a cascaded
  job delete) is not actionable and its dominant cause is forged traffic.
- **D5. The item's acceptance criterion about bounded `GetTask` round trips is struck**, and the item
  should be amended to say why. Section 3, discrepancy 3. Would have escalated: it changes the item's
  own Done-When. Called: strike, because it is unachievable under the item's own constraints and the
  query cost is already bounded per connection.
- **D6. Per-connection, mutex-free state; the existing exported test wrappers keep their signatures
  with a throwaway limiter.** Sections 6.7 and the alternatives list. Trades a documented fail-open
  test seam against a 43-site mechanical diff that would bury the assertion changes that matter.
- **D7. Constants, not env knobs**, at 128 / 16 / 10s. Section 6.3.
- **D8. An injected `now func() time.Time`** on the limiter, so the refill test is deterministic
  rather than sleeping. Three lines, and without it the refill is untested.
- **D9. The `int32` epoch truncation is NOT folded in**, despite ROADMAP's "one-line guard whenever
  the path is next touched". The limiter key uses int64 (free); the fence argument stays. Called:
  leave it, because folding it in would put a fence-semantics change inside a logging slice, which is
  exactly the attribution problem section 5 is avoiding.

---

## 12. Acceptance criteria

1. A single connection emitting `TaskLogChunk`s with **distinct random task ids** and NUL-bearing
   content produces at most `ingestLogBurst` log lines, proven by a test RED against today's limiter.
2. A single connection emitting `TaskLogChunk`s with **one task id and varying epochs** produces at
   most `ingestLogBurst` log lines, proven by a test RED against today's limiter **and** RED against
   a fix that only changes `reset()` to suppress.
3. A single connection emitting `TaskStatusUpdate`s naming distinct random well-formed task ids
   produces **zero** log lines, proven by a test RED against today's handler.
4. A single connection emitting `TaskStatusUpdate`s whose ids do not parse produces exactly one log
   line, containing the first id `%q`-quoted, proven by a test RED against today's handler.
5. A single connection emitting inventory updates with NUL-bearing strings produces at most
   `ingestLogBurst` log lines, proven by a test RED against today's handler.
6. A malformed task id on each of the two handlers, on one connection, produces one line in total.
7. One connection cannot consume or suppress another connection's budget, proven by a test.
8. The budget refills over time, proven with an injected clock, and does not stall under calls more
   frequent than the refill interval.
9. Deduplication happens before the token spend, proven by a test in which a repeated key leaves the
   budget available for fresh keys.
10. `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` passes with **every assertion
    byte-identical**; only the wrapper call and the deleted reset hook may change.
11. `handleTaskLog` still performs exactly one DB round trip and one statement; `handleTaskStatus`
    gains none; no query, goroutine, queue or lock is added to the recv path.
12. Chunk content never reaches the log; `pgErr.Detail` appears nowhere; both bad-id lines use `%q`.
13. The `pgx.ErrNoRows` arm of `handleTaskLog` is silent, side-effect-free, publishes nothing, and
    carries a comment naming the counter item as the mechanism that belongs there.
14. `taskLogErrs`, `taskLogErrLimiter`, `taskLogErrLimiterMax` and `ResetTaskLogErrLimiterForTest`
    are gone from the tree.
15. `make test` and the `internal/worker` integration suite are green, and the `Connect`-level
    integration tests are green under `-race`.
