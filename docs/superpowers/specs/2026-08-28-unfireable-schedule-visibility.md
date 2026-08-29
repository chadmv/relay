# A permanently un-fireable schedule must be visible

- **Date:** 2026-08-28
- **Type:** backend slice (Go + SQL + one migration) plus three independent client slices (SPA, CLI, Python SDK)
- **Closes:** `docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md`
- **Blocked on:** nothing. The half it used to be paired with (`bug-2026-08-12-retries-unvalidated-and-budget-only-in-go`) shipped in #158.
- **Phase:** 1 (design). Phase 2 writes the plan.

This spec was produced by a subagent, so every place the brainstorming flow would ask a human, one
of two things happened. Where the evidence in the tree decides the question, the call is made here
with the reasoning written down. Where a genuine product fork exists, it is a **GATE QUESTION** in
section 1 and is NOT silently resolved; the conductor puts those to the human at the spec gate.

---

## 1. GATE QUESTIONS

Five. Each has a recommendation, and each recommendation is argued in the section named.

### GATE DECISION (2026-08-28, conductor session, gateMode=gated)

**All five recommendations were put to the human at the spec gate and all five were accepted.**
The questions below are left in place as written, because the argument for each recommendation is
the reason the decision went the way it did, and a spec that deletes the question keeps only the
conclusion. Read each `Recommendation:` line as the decision.

- **G1 - accepted: no auto-disable**, now or additively. All six assertions in
  `TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard` therefore stay TRUE,
  `Enabled` included. That test's inversion is the removal of its hazard framing plus new
  assertions that the failure is recorded and cleared - it is NOT a flipped assertion.
- **G2 - accepted: no count column.** Two columns, `last_error` and `last_error_at`.
- **G3 - accepted: the startup validation sweep IS in scope**, sequenced as the last backend
  sub-slice so it can be dropped without unwinding anything else.
- **G4 - accepted: `relay schedules update` gains `--spec FILE`** in this slice. The SPA spec
  editor stays out.
- **G5 - accepted: the CI gap is recorded, not closed here.** The reason goes in the headline
  test's own comment, two default-lane siblings carry what CI can run, and an eighth instance is
  appended to `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`.

**G1. Auto-disable after N consecutive failures, instead of or in addition to a recorded error?**
`internal/schedrunner/stored_spec_bounds_test.go`'s header names this as a legitimate alternative
design and flags "the schedule is still Enabled" as the assertion to think hardest about.
**Recommendation: reject auto-disable, now and additively.** Argued in section 9.1. If the human
overrides this, section 9.1 also states what would have to change.

**G2. Is a consecutive-failure COUNT column in scope?** The alternative is `last_error` plus
`last_error_at` alone, reading duration-of-death off the gap between `last_run_at` and
`last_error_at`. **Recommendation: no count.** Argued in section 4.2.

**G3. Does a startup validation sweep ship in this slice?** Without one, a schedule broken by #158
stays invisible until its next scheduled fire, which for `@monthly` is up to a month after the fix
deploys - and schedules broken by a retroactive validation change are the entire population this
item exists for. With one, the whole surface is populated within seconds of the deploy that carries
it. Cost is roughly 30 lines, one new query, one test, and it is cleanly separable and droppable.
**Recommendation: in scope, sequenced as the LAST backend sub-slice so it can be dropped without
unwinding anything.** Argued in section 6.4.

**G4. Does `relay schedules update` gain a `--spec FILE` flag in this slice?** This is the
"verify a prescribed command exists" question, and the answer at HEAD is uncomfortable: the failure
this slice makes visible has a remedy (PATCH a corrected `job_spec`) that **neither the CLI nor the
SPA can perform**. `doSchedulesUpdate` has no spec flag; the SPA's Job spec panel is READ-ONLY and
`ScheduleTriggerForm` submits only `cron_expr`, `timezone` and `overlap_policy`. Only the Python SDK
and a raw HTTP call can do it. **Recommendation: yes, add `--spec FILE` to the CLI slice** (roughly
ten lines, mirroring `doSchedulesCreate` exactly). The SPA editor stays out: it has a filed enabler
with an extraction prerequisite (`idea-2026-08-12-schedule-job-spec-editor`). Argued in section 7.5.

**G5. CI will not run the headline regression test. Accept and record, or extend the lane?**
`.github/workflows/go-ci.yml` runs `go test -race ./...` untagged plus `make test-cli-integration`.
The end-to-end test must cross `internal/schedrunner` and `internal/api`, both integration-tagged,
so it runs in neither CI job. Extending the `services: postgres` job to `internal/api` is the
subject of `idea-2026-08-23-integration-only-guards-ci-never-runs`, which is separately in Now.
**Recommendation: accept, record the reason in the test's own comment (which is that item's own
stated acceptance form), and carry TWO default-lane siblings that do run in CI** - one over the
response mapping, one over the classification and sanitization helpers. Argued in section 8.

---

## 2. Verification of the backlog item

This project has a long record of items whose diagnosis was right and whose prescribed remedy was
wrong, and a longer one of prose that has drifted from the tree. Every load-bearing claim in the
item was checked at HEAD (`77a847a`). The diagnosis holds completely. Four things were refuted or
corrected, and two of them change the design.

### Confirmed

| Claim | Evidence |
|---|---|
| `fireOne` re-validates the stored spec on every fire | `fireOne` calls `jobcreate.CreateJobFromSpec`, whose first statement is `jobspec.Validate(&spec)`. Confirmed. |
| The failure path logs one line and advances `next_run_at` | `TickOnce`'s `fireErr` branch: `sp.Rollback`, `log.Printf`, `r.advanceNextRun`. Confirmed. |
| No `last_error` column exists in any migration | `store.ScheduledJob` has exactly 13 fields; the highest migration is `000021_tasks_assigned_at`. Next number is `000022`. Confirmed. |
| No failure field on `scheduledJobResponse` | 13 JSON fields, none of them a failure surface. Confirmed. |
| The bound is live and retroactive | `jobspec.Validate` rejects `Retries` outside `[0,10]` and `TimeoutSeconds` outside `[0,604800]`, and `maxRetries`' own doc comment explains at length why it must not be env-configurable *because* it runs on stored scheduled-job specs. Confirmed. |
| `run-now` answers with 400 and the per-task message | `handleRunScheduledJobNow` calls `ValidateJobSpec(spec)` on the stored spec ahead of the transaction and writes `err.Error()` into a 400. Confirmed. |
| The read is owner-scoped or admin | `ownedScheduledJob` 404s a non-owner non-admin. Both list arms are owner-scoped for non-admins. Confirmed - this is what makes the stored text safe to serve; see section 5.3. |
| The stored spec is exactly what the client sent | `handleCreateScheduledJob` stores `req.JobSpec`, the raw bytes, and never re-marshals the validated struct. Confirmed, including the item's own 2026-08-28 correction to *why*. |

### Refuted or corrected

**R1. There is no PUT handler.** The item's Proposal and the surrounding conversation both speak of
"the PUT handler". The route is `PATCH /v1/scheduled-jobs/{id}` (`server.go`), served by
`handlePatchScheduledJob`, whose request struct is all pointers so an omitted key means "leave
alone". The *SQL statement* `UpdateScheduledJob` does rewrite every mutable column, which is where
the PUT impression comes from, and that distinction is load-bearing: the handler performs a
read-modify-write against a row it read without a lock. Section 6.3 designs around it rather than
joining it.

**R2. `AdvanceScheduledJob` is called on the SKIP path too, and it stamps `last_run_at` there.**
The item and the hazard test both reason as if `AdvanceScheduledJob` were the success statement.
`fireOne` calls `r.advance` from two places: after a successful `CreateJobFromSpec`, and in the
`overlap_policy == "skip"` branch when `active > 0`. On the skip path it passes a zero-value
`pgtype.UUID`, so `COALESCE($3, last_job_id)` preserves the old job id - but `last_run_at = NOW()`
fires unconditionally. So `last_run_at` already means "the last time the runner reached the end of a
fire attempt", not "the last time a job was produced". **This changes the design**: a naive
"clear the failure whenever `AdvanceScheduledJob` runs" would clear it on skip, and the skip branch
returns *before* `CreateJobFromSpec`, so it never reaches `jobspec.Validate` and is therefore no
evidence at all that the spec is valid. Section 6.2 splits the statement.

**R3. The Proposal's remedy is not reachable from relay's own clients.** See G4. This does not
refute the diagnosis; it means the surface this slice builds points at a fix that, today, requires
the Python SDK or curl. Recording it here so the slice does not ship a signal whose documented
remedy does not exist.

**R4. `advanceNextRun` is not the only writer of `next_run_at` on a non-firing path.**
`ReconcileOnStartup` calls `AdvanceScheduledJobNextRun` for every overdue enabled schedule at boot,
and logs-and-continues on its own `ParseSchedule` failure. Any design that repurposes
`AdvanceScheduledJobNextRun` to record failures would therefore change startup behaviour as a side
effect. Section 6.2 leaves that statement untouched for exactly this reason, and section 6.5 explains
why reconcile deliberately records nothing.

### One adjacent, in-scope prose correction

`web/src/schedules/api.ts`'s comment on `getSchedule` cites `internal/api/scheduled_jobs.go:147-169`
and `updateSchedule` cites `:32-43` of the SQL file. Those line numbers already point at different
code than they did when written. This slice edits the surrounding block, so converting those
citations to symbol names (`ownedScheduledJob`, `UpdateScheduledJob`) is in scope for the frontend
slice. Do not add new line-number citations.

---

## 3. What this closes, and the one thing it does not

The item's acceptance criteria are about `GET /v1/scheduled-jobs/{id}`. That is the DIAGNOSIS
surface, and it is not the interesting half: `run-now` already closes diagnosis, and section 2
confirms it does so well.

**The half that closes DISCOVERY is the LIST.** `toScheduledJobResponse` is shared by
`handleGetScheduledJob` and both arms of `handleListScheduledJobs`, so adding the fields there puts
them on `GET /v1/scheduled-jobs` for free - and an operator scanning `relay schedules` or the SPA's
schedules table can then see *which* schedule to suspect without suspecting anything first. That is
the whole difference between this fix and the `run-now` remedy it must not rebuild. Every surface
decision below is made in that light: the list-level marker is not a nice-to-have, it is the point.

**What this does NOT close:** a schedule with `overlap_policy: skip` whose previous job is wedged
forever also produces nothing while looking healthy, and it looks *healthier* than the case here,
because `last_run_at` keeps advancing on the skip path (R2). That is a different defect with a
different cause and it is not in scope. Propose it as a backlog item (section 11).

---

## 4. Storage shape

### 4.1 The recommendation

Two nullable columns on `scheduled_jobs`, migration `000022`:

```sql
ALTER TABLE scheduled_jobs ADD COLUMN last_error TEXT NULL;
ALTER TABLE scheduled_jobs ADD COLUMN last_error_at TIMESTAMPTZ NULL;
```

**This migration cannot refuse to boot, and that is a requirement, not a nicety.** Migrations are
embedded and run on startup, so a migration that can fail is a deployment that cannot start. These
two statements have no `NOT NULL`, no `DEFAULT`, no `CHECK` and no backfill, so there is no existing
row they can reject and no expression they can fail to evaluate. In Postgres a nullable
`ADD COLUMN` with no default is a catalog-only change: it takes a brief `ACCESS EXCLUSIVE` lock and
returns without rewriting the table, whatever its size. This is the same reasoning by which #158
declined a `CHECK` constraint on `tasks.retries`, and it is stated in the migration's own comment
rather than left implied.

The down migration drops both columns.

### 4.2 Why not a consecutive-failure count (G2)

A count answers "how many times has this failed". An operator's actual question is "how long has
this been dead, and is it still being tried", and the tree already answers both:

- **How long.** `last_run_at` is the last time the runner completed a fire attempt without erroring.
  With clear-on-success semantics (section 6), a non-null `last_error` means nothing has succeeded
  since. So the interval `last_run_at -> now` IS the duration of death, readable directly, with no
  new column and no counter that a restart or a manual edit can desynchronize from reality.
- **Still being tried.** `last_error_at` moving is proof the scheduler is still evaluating the row.
  Without it you cannot distinguish "failing every hour" from "failed once in March and then the
  schedule was disabled".

A count buys a number nobody needs to make a decision, and it costs a third column on a row that
`TestScheduledJobRowStillCarriesNoFailureSurface` deliberately makes expensive to widen, a third
response field, a third thing for four clients to render, and a semantic question ("does a skip
reset it?") that has no good answer. Reject.

### 4.3 Is `last_error_at` itself pulling its weight

Weakest of the two, and worth stating honestly. Given reliable clearing, `last_error != NULL`
already means "currently failing", and `updated_at` moves on every advance so it cannot help.
`last_error_at` earns its place on the "still being tried" argument above and on one concrete
rendering need: the SPA and CLI want to say "last failure 4 minutes ago" beside "last run 22 days
ago", and that pair is the sentence an operator reads and understands immediately. Keep it. If the
human wants the absolute minimum, dropping `last_error_at` is survivable and nothing else in this
spec changes shape.

---

## 5. What text is stored

### 5.1 Which failures are recorded, and which are not

`fireOne` has four failure returns plus a `skip` path that returns nil. They are not the same kind of
event and must not be recorded the same way.

| Site | Error | Class | Recorded? |
|---|---|---|---|
| `json.Unmarshal(row.JobSpec, &spec)` | `invalid job_spec: ...` | Permanent. Operator-supplied data. Not reachable through POST or PATCH today (both unmarshal before validating), so it means the row was written by something else or by an earlier release. | **Yes** |
| `ParseSchedule(row.CronExpr, row.Timezone)` | `parse cron: ...` | Permanent. Operator-supplied data. | **Yes** |
| `jobspec.Validate` (see 5.2) | the per-task message | Permanent. Operator-supplied data. **This is the case the item is about.** | **Yes** |
| `q.CountActiveJobsForSchedule` | `count active jobs: ...` | Transient infrastructure fault. A wrapped pgx error. | **No** |
| `jobcreate.CreateJobFromSpec` after validation | `create job: ...` | Infrastructure fault, once validation has been split out ahead of it. | **No** |
| `tx.Begin` for the savepoint | savepoint begin failure | Infrastructure fault; the row is `continue`d before `fireOne` runs at all. | **No** |
| `overlap_policy: skip` with an active job | none, returns nil | Healthy no-op. | **No** (and does not clear either - section 6.2) |

The partition is justified twice over, and both arguments point the same way, which is why it is
worth making rather than recording everything uniformly.

- **Semantically.** A DB blip is not a fact about the schedule. Recording it would make the surface
  flicker with events that resolve themselves, and an operator who learns to ignore a noisy field
  has lost the field. The three recorded classes share the property that an identical attempt later
  gets an identical answer - the same partition `relayclient.ErrorIsTransient` documents and the same
  one `handleRunScheduledJobNow`'s comment reasons about when it chooses 400 over 500.
- **By disclosure.** The three recorded messages are derived from data the schedule's owner supplied.
  The three unrecorded ones are wrapped pgx errors, which can carry constraint names, column names,
  connection strings and host names. `internal/api` has a settled convention of not disclosing
  internals (`writeError(w, 500, "db error")`, never the pgx message). Storing a pgx error in a
  column that four clients render would sidestep that convention through the back door.

The unrecorded classes keep logging exactly as they do today. Nothing gets quieter.

### 5.2 Getting a clean classification: hoist `jobspec.Validate` in `fireOne`

Today the validation failure and the insert failure are the same return value
(`create job: %w` wrapping whatever `CreateJobFromSpec` produced), so they cannot be told apart at
the call site. `fireOne` must call `jobspec.Validate(&spec)` explicitly, immediately after the
unmarshal, before the overlap check and before `CreateJobFromSpec`.

Three things make this safe and consistent:

- **It is the precedent, not a new idea.** `handleRunScheduledJobNow` already does exactly this, for
  exactly this reason, and its comment says so at length: it validates the stored spec ahead of the
  transaction so a stored spec's failure is answered as a fact about the request rather than as a
  server fault.
- **It respects the single-job-spec-pipeline invariant.** It is the same `jobspec.Validate`, not a
  parallel check. `CreateJobFromSpec` still validates; the hoisted call is a second call to the same
  validator of record, which is what run-now already does.
- **It is idempotent.** `Validate` normalizes `Command` into `Commands` and clears `Command`. A
  second call sees `hasCommand == false, hasCommands == true` and falls through `normalizeTaskCommands`
  without error. Verified by reading `normalizeTaskCommands`'s switch.

Note the ordering consequence, and take it deliberately: hoisting `Validate` above the overlap check
means a poisoned spec now reports its validation error even when a previous run is still active,
where today it would have skipped silently. That is the correct order. A spec that cannot produce a
job is a fact about the schedule regardless of what else is running.

The classification itself should be a named, pure helper so it can be tested in the default lane
(section 8). Suggested shape, not mandated: `fireOne` wraps the three permanent errors in an
unexported type whose `Error()` delegates to the wrapped error (so no prefix text leaks into the
stored string), and `TickOnce` uses `errors.As` to decide. The product requirement is only that the
decision is a function of the error and is testable without a database.

### 5.3 Sanitization, truncation, and who reads it

**Sanitize at the single write site**, before the value reaches SQL. One place, four readers.

- **Strip control characters.** Replace every rune below U+0020 and U+007F with a space, except keep
  nothing special: newlines are not needed by any of the three recorded classes. This closes ANSI
  escape injection into `relay schedules show`'s terminal output. The text is operator-controlled
  (a task name flows verbatim into `task %s: retries must be between 0 and %d`), and while the CLI
  already prints operator-controlled job and task names, a new sink is the right moment to close the
  class rather than widen it.
- **Truncate to 1024 bytes on a rune boundary**, appending a marker such as `... (truncated)` when
  it bites. 1024 is comfortably above every fixed-format message `jobspec.Validate` emits; the only
  way to exceed it is an operator-chosen task name of ~1 KB. `last_error` is a `TEXT` column, and
  Postgres rejects invalid UTF-8 in `TEXT`, so a byte-slicing truncation is a genuine write failure,
  not a cosmetic bug. Truncate on runes.
- **Never store an empty string.** `omitempty` on the response field means an empty string is
  indistinguishable from absent, and absent must mean "no failure". If sanitization reduces the
  message to nothing, store a fixed fallback (`fire failed; message unavailable`).
- **Bound on growth.** The write is an UPDATE of one already-locked row, not an append, so a failing
  schedule costs at most 1 KB, once, forever. Total storage is 1 KB times the number of currently
  failing schedules. Write amplification is bounded by the schedule's own cadence, with one floor
  worth naming: the unmarshal and parse-cron classes return `time.Now().Add(time.Minute)` rather
  than a cron-derived time, so those two re-fail and re-write at most once a minute per schedule. At
  the batch ceiling that is 100 extra UPDATEs per 10-second tick, each on a row the outer transaction
  already holds `FOR UPDATE`. Not a load concern; recorded so nobody has to re-derive it.

**Who can read it.** Owner or admin, and nobody else: `ownedScheduledJob` 404s everyone else, and the
non-admin list arms are owner-scoped. The stored text is derived from the reader's own data in the
owner case. In the admin case it is derived from ANOTHER user's data, which is the only interesting
direction and is handled in 5.4.

### 5.4 Stating the signal's trustworthiness where it is READ

Applying the epoch-fence family's shape rather than its nouns. `last_error` is a signal an operator
reads and acts on, so the question is what a peer who can move it gains.

The answer is narrow but not empty. A schedule's owner controls part of the text, because the message
embeds a task name they chose. An admin reading someone else's schedule is therefore reading partly
attacker-chosen prose. There is no counter here to inflate, no remedy ladder that favours the writer,
and nothing an owner gains by breaking their own schedule. The one real risk is **display-layer
impersonation**: text crafted to read like relay's own chrome in an admin's console.

So the requirement is a copy and rendering one, and it lands where the signal is read, in the same
change that ships it:

- The SPA renders it inside a clearly labelled panel whose heading names its provenance, as a React
  text child, never as chrome and never through `dangerouslySetInnerHTML`. The existing Job spec
  panel's comment already states this rule for the same reason; follow it.
- The CLI prefixes the line with its provenance, e.g. `Last error (from the stored job_spec):`.
- README says, at the point where it documents the field, that the text is derived from the stored
  spec and is operator-supplied.

**And the remedy ladder must not contain "turn the control off" as a peer step.** Here that rule is
satisfied by construction and it is worth writing down why: the control in question is
`jobspec.Validate`'s bounds, and `maxRetries`' own doc comment refuses to make them env-configurable,
explaining that an env-tunable bound would make retroactive schedule invalidation
environment-dependent. There is no knob to list. The ladder in section 7.6 is diagnose, fix, or
disable the schedule, and "disable the schedule" is a statement about that one schedule, not about
the validator.

---

## 6. Write sites

### 6.1 The sharpest implementation constraint: the savepoint

`TickOnce` opens one outer transaction per tick and one nested transaction (a savepoint) per row.
`fireOne` runs against the savepoint's `*store.Queries`. On `fireErr` the savepoint is **rolled
back**, and `advanceNextRun` is then called on the **outer** transaction's `q`.

**Any write that records the failure must therefore go on the OUTER transaction, beside
`advanceNextRun`.** A write issued inside `fireOne` is discarded by the rollback that is the entire
point of the savepoint design, and it is discarded silently: the row would simply never carry an
error and the test would fail with no clue why. This is stated here, in the plan, and in a comment at
the write site.

Two corollaries:

- The error TEXT is a Go value returned from `fireOne`, unaffected by the rollback. Capture and
  classify it from `fireErr` after the rollback. No change needed, but do not be tempted to move the
  classification inside `fireOne`'s transaction scope.
- The row is held `FOR UPDATE` by the outer transaction for the whole tick, so the failure write
  cannot race a concurrent PATCH: the PATCH blocks on the row lock. Ordering between the two is
  serialized by the database, not by the application.

### 6.2 The statements

| Statement | Change | Called from |
|---|---|---|
| `AdvanceScheduledJob` | **Changed**: also sets `last_error = NULL, last_error_at = NULL`. Its `COALESCE($3, last_job_id)` is untouched. | `fireOne`'s success path only, after the split below. |
| `AdvanceScheduledJobSkipped` | **New**: `next_run_at`, `last_run_at = NOW()`, `updated_at = NOW()`. No `last_job_id`, no failure clear. | `fireOne`'s `skip` branch. |
| `AdvanceScheduledJobAfterFailure` | **New**: `next_run_at`, `last_error = $3`, `last_error_at = NOW()`, `updated_at = NOW()`. | `TickOnce`'s failure branch, on the OUTER tx, for the three recorded classes. |
| `AdvanceScheduledJobNextRun` | **Unchanged.** | `TickOnce`'s failure branch for the unrecorded classes, and `ReconcileOnStartup`. |
| `UpdateScheduledJob` | **Changed**: conditional clear, section 6.3. | `handlePatchScheduledJob`. |

**Why split rather than fold a `CASE` into `AdvanceScheduledJob`.** R2 established that the skip path
and the success path share one statement, distinguished only by whether `$3` carries a job id. A
`CASE WHEN $3 IS NULL` expression would work and would be one statement fewer, and it would also make
the correctness of the clearing rule depend on a parameter overload that a reader has to reconstruct.
Two statements whose names say what they mean is worth one extra query. After the split,
`AdvanceScheduledJob`'s `COALESCE` has exactly one caller which always passes a valid id, so it is
vestigial - **leave it**, removing it is an unrelated behaviour change and does not belong in this
slice.

`last_error_at` uses the database clock `NOW()`, matching `last_run_at` immediately beside it. Within
one transaction `NOW()` is the transaction start time, which for a 100-row tick is at most a few
seconds stale. Consistency with the field it sits next to beats that. Recorded so nobody later
"fixes" it to a Go clock in isolation.

### 6.3 Clearing semantics, stated for every path

| Event | `last_error` / `last_error_at` | Why |
|---|---|---|
| Successful fire (a job was created) | **CLEARED** | The spec validated and the insert succeeded. This is the only event that proves the schedule works. |
| `skip` (overlap policy, previous run active) | **PRESERVED** | The skip branch returns before `jobspec.Validate` runs, so it is no evidence the spec is valid. Clearing here would make a poisoned schedule with a long-running predecessor flicker between "failing" and "healthy". |
| Recorded failure | **OVERWRITTEN** with the new message and time | Latest failure wins. |
| Unrecorded failure (DB fault) | **PRESERVED** | A blip is not news about the schedule. |
| `PATCH` that changes `job_spec`, `cron_expr` or `timezone` **and leaves all three validating** | **CLEARED** | These are exactly the three inputs the three recorded classes are about. |
| `PATCH` that changes one of the three while another is still broken | **PRESERVED** | The handler validates PER KEY - `job_spec` only inside `if req.JobSpec != nil`, cron/tz only when one is supplied - so "stale by construction" was true of the values the request SUPPLIED and false of the row. The clear is gated on `schedrunner.ValidateStoredSchedule` over the effective post-patch values, the same unmarshal -> `ParseSchedule` -> `Validate` the writers use. The `PATCH` still returns 200: a two-step repair is legitimate. |
| `PATCH` that changes nothing else (`name`, `overlap_policy`, `enabled`) | **PRESERVED** | Renaming a schedule must not erase the only signal that it is broken. This matters concretely: on an `@monthly` schedule the record would not be rewritten for a month. |
| Enable, and disable | **PRESERVED** | Nothing about the spec changed. A re-enabled schedule that still carries its failure is showing the truth, and it is the most useful moment to see it. |
| `run-now`, success or failure | **UNTOUCHED** | `handleRunScheduledJobNow` writes neither `last_run_at` nor `last_job_id` today, because it is not "the schedule ran". The new fields join those two. This keeps the surface meaning exactly one thing: the last thing the SCHEDULER tried. |
| `ReconcileOnStartup` | **UNTOUCHED** | Section 6.5. |
| Delete | n/a | The row is gone. |

**Implementing the PATCH arm without a read-modify-write.** `handlePatchScheduledJob` reads the row
through `ownedScheduledJob` (no lock), builds every column value in Go, then calls
`UpdateScheduledJob`, which rewrites every mutable column. Adding `last_error` to that pattern -
reading the current value and writing it back - would let a PATCH carry a stale error forward over a
failure a tick recorded in between. Instead, `UpdateScheduledJob` takes a **boolean argument**:

```sql
last_error    = CASE WHEN sqlc.arg(clear_failure)::bool THEN NULL ELSE last_error END,
last_error_at = CASE WHEN sqlc.arg(clear_failure)::bool THEN NULL ELSE last_error_at END,
```

The handler sets it from `req.JobSpec != nil || req.CronExpr != nil || req.Timezone != nil`. The row's
own value is never read into Go and written back, so there is no window. This is the same read-
modify-write hazard `next_run_at` already has in that handler; this slice does not fix that one, and
it does not join it either.

### 6.4 The startup validation sweep (G3)

Without it, this slice has a hole aimed precisely at its own audience. `ReconcileOnStartup` advances
`next_run_at` past missed triggers (never-catch-up), so after the deploy that carries this fix, a
schedule broken by #158 records nothing until its next scheduled fire. For `@daily` that is up to a
day; for `@monthly`, up to a month. The population most likely to be broken right now is exactly the
population of long-cadence schedules nobody has looked at recently.

Recommended shape, if the gate says yes:

- A new query listing every **enabled** schedule (not just overdue ones; `ListOverdueScheduledJobsForCatchup`
  is the wrong set). Run once at startup, after migrations, beside `ReconcileOnStartup`.
- Per row: unmarshal, `ParseSchedule`, `jobspec.Validate`. On failure, write through
  `AdvanceScheduledJobAfterFailure`'s sibling that touches only the failure fields (`next_run_at`
  must NOT move at startup - never-catch-up already owns that).
- **Record-only. It never clears.** A spec that validates at boot has not been proven to fire; the
  insert could still fail. Clearing on a boot-time pass would assert something the sweep did not
  observe, and the failure record is the more conservative state to leave standing. Clearing stays
  the exclusive job of a successful fire and of a PATCH.
- Cost: one pass over N enabled schedules at boot, no I/O per row beyond the read that lists them.

This needs its own statement (failure fields only, no `next_run_at`), so it is genuinely separable
and should be the last backend sub-slice.

### 6.5 Why `ReconcileOnStartup`'s own `ParseSchedule` failure records nothing

It looks like an obvious fourth recording site and it is not, for a reason worth writing down so
nobody adds it later. When `ReconcileOnStartup` fails to parse a schedule's cron it logs and
`continue`s **without advancing `next_run_at`**. The row therefore stays overdue, and
`ListEligibleScheduledJobs` (`enabled AND next_run_at <= NOW()`) picks it up on the very next tick,
at most 10 seconds later, where `fireOne`'s own `ParseSchedule` failure records it. A write here
would be redundant within ten seconds and would add a second code path to keep in step. Leave it
logging.

(Note this is a different question from G3, which is about schedules that are NOT overdue and so are
seen by neither loop for a full cron period.)

---

## 7. Surfaces

### 7.1 The response (backend slice)

Two fields on `scheduledJobResponse`, following `LastRunAt` and `LastJobID`'s existing absent-not-zero
precedent in the same struct:

```go
LastError   string     `json:"last_error,omitempty"`
LastErrorAt *time.Time `json:"last_error_at,omitempty"`
```

`toScheduledJobResponse` populates them only when the `pgtype` values are `Valid`. Absent means no
failure; the keys are not present in the JSON at all, never `""` and never `null`. Section 5.3's
"never store an empty string" rule is what makes `omitempty` on a string safe here.

Both endpoints get them for free (section 3). No new endpoint, no new query parameter, no filter.
A `?failing=true` list filter is a plausible later refinement and is explicitly not in this slice:
the list already carries the field, and a filter is a sort/pagination question that touches
`ScheduledJobsSortSpec` and eight paging statements.

### 7.2 SPA detail page (frontend slice)

Two additions to `ScheduleDetailPage`, both conditional on `schedule.last_error` being present so a
healthy schedule's layout is byte-identical to today:

1. **The identity sub-line** already reads `created ... updated ... next fire ... last run ...`.
   Append `· last failure <relative>` when `last_error_at` is present, reusing `formatRelativeTime`.
   No new formatter. This is what makes the failure visible without scrolling.
2. **A "Last failure" panel** at the TOP of the right-hand column, above "Next fire", rendered only
   when `last_error` is present. Contents: the message as a React text child in a monospace block,
   the absolute and relative time, and one line of remedy copy pointing at Run now and at editing the
   spec. Its heading names the text's provenance (section 5.4).

The `Schedule` interface in `web/src/schedules/api.ts` gains `last_error?: string` and
`last_error_at?: string`.

Deliberately NOT done: changing the ENABLED/PAUSED chip, or adding a third state to it. The schedule
IS enabled; the chip is telling the truth and it is the operator's own setting. Failure is a separate
axis and gets a separate element.

### 7.3 SPA schedules list (frontend slice)

**No new column.** `SchedulesTable` already carries nine columns and a 1040px minimum width, the
widest in the app, and its comment says so. Instead, put a small `FAILING` chip in the NAME cell,
which is already `flex min-w-0 items-center gap-2` and already holds the status dot. Text, not a
colour change to the dot: a bare colour is not accessible and the dot's two states are already spoken
for by `enabled`.

**This is the one place where the frontend slice needs a browser, not jsdom.** A chip added inside a
`1.4fr` track in a nine-column grid at a 1040px floor is exactly the horizontal-overflow class that
890 green jsdom unit tests were once silent about. The frontend slice runs the Playwright lane
against the schedules list at a narrow viewport with at least one failing row present, and measures
the populated state, not an empty table.

### 7.4 CLI (CLI slice)

`scheduleResp` in `internal/cli/schedules.go` gains `LastError *string` and `LastErrorAt *time.Time`.
Note this struct is ALREADY a lossy view: it carries no `last_job_id` and `schedules show` does not
even print `last_run_at`. This slice does not fix that; it adds the failure fields and prints them.

- `schedules show`: print `Last error:` and its timestamp, only when present, with the provenance
  prefix from section 5.4. While there, print `Last run:` too - it is one line, it is already in the
  struct, and its absence is what makes a failing schedule look identical to a healthy one in the
  one command an operator runs to inspect a schedule.
- `schedules list`: no new column (the table is already six columns of tabwriter output). Append a
  `FAILING` marker to the `NEXT` cell, or add a seventh short `STATE` column - the planner may choose,
  but it must be TEXT and it must be visible without `--json`.

### 7.5 The remedy gap (G4)

`doSchedulesUpdate` accepts `--cron`, `--tz`, `--enable`, `--disable`, `--overlap`. There is no
`--spec`. So the CLI can create a schedule from a spec file and cannot repair one. The SPA cannot
either: the Job spec panel is read-only and `ScheduleTriggerForm` submits three keys, none of them
`job_spec`. `PATCH` accepts `job_spec` and the Python SDK's `update_scheduled_job` sends it.

Recommendation: add `--spec FILE` to `schedules update`, mirroring `doSchedulesCreate`'s existing
read-parse-send exactly (read the file, `json.Unmarshal` into `map[string]any` to confirm it parses,
put it on the body). The server remains the validator of record and its 400 renders verbatim, the same
contract `ScheduleTriggerForm`'s comment describes for cron. Ten lines, and it turns the remedy from
"use curl" into "use relay".

The SPA editor stays out of scope: `idea-2026-08-12-schedule-job-spec-editor` requires extracting
`NewJobPage`'s spec editor first so the app does not grow a second one, and that extraction is its own
slice with its own byte-identical-test gate.

### 7.6 README (backend slice)

README documents the scheduled-jobs endpoints but not a single response field, so there is nothing to
correct - only a short subsection to add, under the scheduled-jobs API block:

- The two fields, their absent-not-zero semantics, and that the list carries them too.
- That the text is derived from the stored `job_spec` and is operator-supplied (section 5.4).
- That it is truncated, and that `run-now` returns the untruncated message.
- **The remedy ladder**, in order: (1) `POST /v1/scheduled-jobs/{id}/run-now`, or `relay schedules
  run-now`, or the SPA's Run now, to re-check interactively and get the current message in full;
  (2) repair the spec with `PATCH {"job_spec": ...}` (naming the CLI flag if G4 lands, and the
  delete-and-recreate fallback if it does not); (3) disable the schedule if it should not run.
  There is no fourth step, and in particular there is no "relax the validator" step - the bounds are
  not configurable by design (section 5.4).

### 7.7 Python SDK (SDK slice)

`ScheduledJob` in `python/src/relay/models.py` gains `last_error: Optional[str] = None` and
`last_error_at: Optional[datetime] = None`, matching `last_run_at`'s existing shape. Two lines and a
model test. Note the model is `extra="ignore"`, so it does not break without this - it just cannot see
the fields. Small enough that it can ride with the CLI slice or go alone.

---

## 8. Testing, and what CI actually runs

### 8.1 The two tests that already exist and are addressed to this slice

Both must be updated in the same commit as the column. Neither is optional cleanup.

**`internal/schedrunner/scheduled_job_surface_test.go`.** `scheduledJobFields` gains `LastError` and
`LastErrorAt`. Its header instructs: invert the hazard test, do NOT add an exemption. Follow it
exactly. This test is untagged and runs in CI, so it will be the first thing to redden.

**`internal/schedrunner/stored_spec_bounds_test.go`**,
`TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard`. Its header enumerates six
assertions with a per-assertion instruction. Under the recommended design, **all six stay true and
none of them inverts**, including "the schedule is still Enabled" - because this design does not
auto-disable (G1). What changes is the framing and the additions:

- Rename off the `_DocumentedHazard` suffix; the hazard is closed.
- Rewrite the header: the DISCOVERY gap it documents is now filled, and the six assertions are now
  positive statements of correct behaviour rather than a pinned defect.
- Rewrite the `Enabled` assertion's message from "THE HAZARD, STATED POSITIVELY" to a deliberate
  statement that relay does not auto-disable a failing schedule, citing this spec's section 9.1 for
  why.
- **Add** the assertions that make the test's new name true: `row.LastError` is set and carries the
  bound's message, and `row.LastErrorAt` is set. Then fire the healthy control's spec through a
  second tick, or PATCH the poisoned one, and assert the clear.

If the human overrides G1 and chooses auto-disable, the `Enabled` assertion does invert, exactly as
its header predicts.

### 8.2 The headline regression test, and where it lives

The acceptance criterion requires driving a stored-then-invalidated spec through a tick and asserting
the error is visible **via the API**. That crosses two packages whose meaningful tests are both
integration-tagged, so placement is decided by which package can hold both halves:

**`internal/api`, integration-tagged.** `internal/api` already imports `internal/schedrunner`
(`ValidateMinInterval`, `ParseSchedule`), and `schedrunner` does not import `internal/api`, so there
is no cycle. An `internal/api` integration test has the httptest server, a real pool, and can
construct `schedrunner.NewRunner(pool, q)` and call `TickOnce`. `internal/schedrunner`'s harness
cannot do the reverse: it has no server.

Shape: store an over-bound spec directly through `CreateScheduledJob` (as `makeOverBudgetSpecJSON`
already does, and for the same reason - POST would refuse it), plus a healthy control in the same
tick; `TickOnce`; `GET /v1/scheduled-jobs/{id}` through the real handler and assert the body carries
`last_error`; `GET /v1/scheduled-jobs` and assert the LIST carries it too (that is the discovery
half, and a test that only checks the detail endpoint would not cover the thing this slice is for);
assert the healthy control's body has **neither key present**; then PATCH a valid `job_spec` and
assert both keys are gone.

It is RED at HEAD in the strongest available sense: it does not compile, because the field does not
exist.

**Assert on the JSON keys, not on the response struct.** Decode into `map[string]any` and check key
presence and absence. An assertion routed through `scheduledJobResponse` agrees with itself by
construction on both the key names and the omitempty behaviour, which is the vacuous-fixture defect
CLAUDE.md counts 51 instances of, and a deep-equal against a fixture cannot see an absent optional
field at all.

### 8.3 CI, honestly (G5)

`.github/workflows/go-ci.yml` runs `go test -race ./...` untagged and `make test-cli-integration`.
The test in 8.2 is in neither. **It will not run in CI.** Do not describe it as a gate.

Extending the `services: postgres` job to `internal/api` is the right long-term answer and is
`idea-2026-08-23-integration-only-guards-ci-never-runs`'s own subject; that item is separately in Now
and already names `internal/api` as its sharpest instance. Doing it inside this slice means moving
`newIntegrationDSN` out of `internal/cli`'s test files into a shared location and converting
`internal/api`'s harness to honour `RELAY_TEST_DATABASE_URL`, which is a refactor of another item's
scope, on the critical path of this one.

So: accept, and pay for it two ways.

1. **Record the reason in the test's own comment** - which is precisely the form that item's
   acceptance criteria allow ("a written decision in the test's own comment saying why not"), naming
   the two default-lane siblings below and what each covers.
2. **Carry two default-lane tests that DO run in CI**, each pinning a real property rather than
   standing in decoratively:
   - `internal/api`, untagged (the package already has untagged files: `cors_test.go`,
     `pagination_test.go`, `ratelimit_test.go`). Call `toScheduledJobResponse` on a hand-built
     `store.ScheduledJob`, marshal, and assert key presence and absence against **hand-written JSON
     or a locally declared struct with independent tags** - never against `scheduledJobResponse`
     itself. This pins the wire contract, the field names and the absent-not-zero rule in CI, with no
     database.
   - `internal/schedrunner`, untagged. The classification helper (which of the failure classes is
     recordable) and the sanitize-and-truncate helper: a control character is stripped, a 2 KB message
     is truncated on a rune boundary with the marker, a message that sanitizes to empty becomes the
     fallback, a `count active jobs` error is NOT recordable, a validation error IS. Both helpers are
     pure. The poisoned input goes FIRST in any table, not last, so an early-exit mutation cannot
     survive it.

Propose (do not auto-file) an amendment to the CI-gap item recording this as its next instance.

### 8.4 Frontend

Unit tests for the conditional panel and the list chip, plus the absence case (a healthy schedule
renders neither, and the detail page's layout is unchanged). Fixtures are hand-written JSON shapes,
not built from the `Schedule` interface's own type in a way that makes a key rename invisible. Plus
the browser check from section 7.3.

---

## 9. Rejected alternatives

### 9.1 Auto-disable after N consecutive failures (G1)

Named by the hazard test's header as a reasonable alternative. Rejected as the primary mechanism, and
rejected as an addition on top. Five reasons, in descending order of weight:

1. **It destroys operator intent in response to a change the SERVER made.** The failure mode this
   whole item exists for is "a release retroactively invalidated stored data". Answering that by
   turning the operator's schedule off compounds a server-driven change to user data with a
   server-driven change to user configuration.
2. **It does not even save the column.** Once `enabled` can be flipped by the server, nobody can tell
   whether a disabled schedule was disabled by its owner or by the runner, so you need a column
   recording that - and it would be worth less than `last_error`, because it says that something went
   wrong without saying what.
3. **It provides no diagnosis, only a state change.** The operator still has to run `run-now` to
   learn why, so it does not close the item's acceptance criteria; it would have to ship *alongside*
   the columns anyway.
4. **It is not self-reversing, and the counter it needs is drivable by the wrong events.** Once
   disabled, the row leaves `ListEligibleScheduledJobs` forever. A counter that includes transient DB
   faults (and section 5.1 shows the failure classes are genuinely mixed) can therefore retire a
   perfectly good schedule after a database restart, which is the same discovery problem one layer
   down and strictly harder to notice.
5. **It is the shape CLAUDE.md warns about.** A control the server switches off automatically, on the
   strength of an internally-generated signal, is "turn the control off" promoted from a remedy step
   to an automatic action.

If the human overrides this, the minimum honest version is: keep both columns exactly as specified,
add a counter, disable only on the PERMANENT classes (never on a DB fault), record that the runner
was the one who disabled it, and invert the hazard test's `Enabled` assertion. It is a strictly
larger slice, not a cheaper one.

### 9.2 Recording every failure class uniformly

Simpler code, one branch instead of two. Rejected in section 5.1 on both the noise argument and the
disclosure argument. The disclosure half is the harder constraint: it would put pgx error text into a
field four clients render, sidestepping `internal/api`'s no-internals convention.

### 9.3 A structured failure code instead of free text

For example `last_error_kind` in `{spec_invalid, cron_invalid, spec_undecodable}` plus a message. It
would make the SPA able to render tailored remedy copy per kind. Rejected for this slice: three kinds
whose remedies are all "fix the stored spec or the cron and re-check with run-now" do not earn a
third column and a vocabulary that then needs its own lockstep guard, in the shape of
`TestTasksStatusVocabularyIsExactly`. The message already names which is which. Revisit only if a
fourth genuinely different kind appears.

### 9.4 An events/SSE frame on a fire failure

Live notification instead of, or beside, a stored field. Rejected: SSE has no history, so a schedule
that broke while nobody was watching stays invisible, which is the exact defect. A stored field is
the right primitive; a frame could be added later on top of it.

---

## 10. Slices, and what depends on what

**Four slices. Backend and frontend are independent** in the sense the planner needs: they can be
implemented in parallel by different agents, because the only thing crossing between them is the
response shape, and this spec freezes it (`last_error`, `last_error_at`, both `omitempty`, absent
means healthy). Every frontend test in `web/src/schedules/` is fixture-driven, so the frontend needs
no running server. Merge order is backend-first for coherence only; a frontend panel keyed on an
absent field renders nothing, so the reverse order is safe too, just pointless.

- **Slice A (backend, Go + SQL + migration `000022`).** The migration; the four statements in section
  6.2 plus the `UpdateScheduledJob` change; the `jobspec.Validate` hoist and classification in
  `fireOne`/`TickOnce`; sanitize and truncate; the response fields; README. Updates both existing
  tests (8.1) and adds the tests in 8.2 and 8.3. **This is the slice that closes the backlog item's
  acceptance criteria.**
- **Slice A2 (backend, optional, G3).** The startup validation sweep. Depends on A. Droppable.
- **Slice B (frontend, zero Go diff).** Sections 7.2 and 7.3, plus the R4 comment-citation cleanup in
  `web/src/schedules/api.ts`. Independent of A.
- **Slice C (CLI).** Section 7.4, plus `--spec` if G4 says yes. Independent of A and B. Note the
  `internal/cli` fixture rule: a default-lane fixture must not be marshalled through `scheduleResp`.
- **Slice D (Python SDK).** Section 7.7. Two lines; may ride with C.

---

## 11. Follow-ups to PROPOSE (not to file)

Per the backlog rule, these are proposals for the human, not filings.

1. **A `skip`-policy schedule with a wedged predecessor is invisible too**, and looks healthier than
   the case this slice fixes, because `AdvanceScheduledJob` stamps `last_run_at` on the skip path
   (R2). Different cause, same class of invisibility.
2. **`last_run_at` means "the runner finished an attempt", not "a job was produced"** (R2). That is
   arguably a defect in its own right, and it is what makes the skip case above invisible. Fixing it
   is a behaviour change to a shipped field, so it needs its own decision.
3. **Amend `idea-2026-08-23-integration-only-guards-ci-never-runs`** with this slice's instance
   (section 8.3): `internal/api` plus `internal/schedrunner` is the pairing needed to run this item's
   headline test, and it is the same `services: postgres` mechanism.
4. **`GET /v1/scheduled-jobs?failing=true`**, once the field exists and someone has enough schedules
   for scanning the list to be the wrong tool.
