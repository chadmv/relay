# Task and command count bounds

Date: 2026-08-29
Status: proposed
Source item: `docs/backlog/bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers.md`
Predecessor slice: `docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md`

## Scope

Half 1 of the source item only: bound the counts in `jobspec.Validate`. Half 2 of that item
(rate-limiting `POST /v1/jobs`) is explicitly out of this slice and stays filed as its own concern.
The residual after this slice ships is stated at the end, in the same register the retry-bounds slice
used, and that statement is a requirement of this spec rather than a hedge.

Three new constants land beside `maxRetries` and `maxTimeoutSeconds` in `internal/jobspec/jobspec.go`,
each with a doc comment carrying the argument that produced its number and instructions aimed at the
next person who wants to change it. The Single job-spec pipeline invariant means all four ingest paths
(REST, CLI, MCP, schedrunner) inherit them with no further work.

## What this spec refutes in the source item

The item is substantially correct. Four things in it do not survive contact with the tree, and one
dismissal in it is too quick.

**1. `sendStepMarker` is not called "unconditionally".** `internal/agent/runner.go`'s command loop
calls it once per command *executed*, at the top of the loop body, after the `cl == nil ||
len(cl.Argv) == 0` guard. Two things stop the loop early and therefore stop the markers: that guard,
and any command whose `cmd.Start()` or `cmd.Wait()` fails, both of which `break`. The consequence is
not cosmetic. **An attacker must supply commands that actually run.** A `commands` array of
`["a"],["a"],...` where `a` is not on PATH costs one failed `exec` and one marker, not 175,000 of
each, because `cmd.Start()` fails on the first entry and breaks. The item's own worked example
(`["true"]` repeated, last entry failing) does hold, and that is the shape the numbers below use. It
is a smaller minimum entry that does not: the cheapest *runnable* entry is `["true"],` at 9 bytes,
not the ~8 the item assumed and not the ~6 that `["a"],` would suggest.

**2. Each marker does become one row, and the item's "nothing prunes `task_logs`" is confirmed by the
code's own comment.** `worker.handleTaskLog` calls `store.AppendTaskLog` once per chunk, one INSERT,
no batching; `internal/store/query/tasks.sql` states in `AppendTaskLog`'s own header that "nothing in
this repo prunes `task_logs`". Both halves of the item's cost claim stand. The fence can drop a
chunk, so the row count is an upper bound, not an identity.

**3. The `maxRetries` doc comment's enumeration of stored-spec paths is now stale, and this slice must
not copy it.** It says `fireOne` "reaches `Validate` only through `jobcreate.CreateJobFromSpec`". PR
#159 hoisted `jobspec.Validate` directly into `schedrunner.fireOne` (`internal/schedrunner/runner.go`,
above the overlap check), so `fireOne` now calls it itself. The same stale claim is repeated in
`internal/schedrunner/stored_spec_bounds_test.go`'s `makeOverBudgetSpecJSON` header. Two further
re-validating sites also postdate that comment: `schedrunner.ValidateStoredSpecsOnStartup` (the boot
sweep) and `api.handlePatchScheduledJob`'s clear-decision, both reaching `Validate` through
`schedrunner.ValidateStoredSchedule`. The complete list is in "Retroactivity" below. Correcting the
`maxRetries` comment is in scope for this slice, because the new constants' comments sit directly
beside it and would otherwise inherit a wrong enumeration by proximity.

**4. The README rows the acceptance criterion names do not exist.** There is no `tasks` row and no
`tasks[].commands` row in README's job-spec field table; there is only `tasks[].command`, the legacy
singular. The multi-command spelling `commands` - which `normalizeTaskCommands` treats as the
canonical form - is undocumented in that table entirely. So the acceptance criterion "the README rows
for `tasks` and `commands` state whatever bound is chosen" is an instruction to *add* two rows, not to
edit two. Separately, the existing `tasks[].retries` row contains the sentence "a job's `tasks` and a
task's `commands` are themselves unlimited", which this slice makes false and must rewrite.

**5. The `DependsOn` dismissal is correct about the validator and silent about the writer.** The item
says the graph is cycle-checked in O(V+E) with "no algorithmic blowup". That is true:
`detectCycle` is linear, E is bounded by body size, and a few hundred thousand map operations is
milliseconds. But `jobcreate.CreateJobFromSpec` issues **one `CreateTaskDependency` round trip per
edge**, inside the caller's transaction. Edges are bounded by `min(body-derived count, V*(V-1))`; with
short task names a 1 MiB body expresses roughly 250,000 edges across roughly 500 tasks, so a single
request can hold one pool connection through a quarter of a million sequential INSERTs. That is
linear in body size rather than a multiplier - it is not amplified by retries, since dependencies are
inserted once - so it belongs in the item's third bucket alongside `Labels`/`Env` rather than in its
"unbounded multiplier" headline. The difference from that bucket is that each unit is a network round
trip rather than a byte copy.

**A `maxTasksPerJob` cap does not meaningfully bound it.** Edge count is quadratic in the task count,
so at the cap proposed below (5000) the `V*(V-1)` ceiling is far above what 1 MiB can express and the
body limit remains the only binding constraint. **Recommendation: this is a real concern, it is not
this slice, and it should be a new backlog item** against `jobcreate.CreateJobFromSpec`'s insert
strategy (batch the dependency inserts, or bound total edges). It is a different fix in a different
package from the one this spec touches, and folding it in would make the slice about two things.

## The bounds

Three constants. Names carry their scope explicitly, because two of them are per-job and one is
per-task and an unqualified `maxCommands` would be ambiguous at the call site. The existing
`maxRetries` and `maxTimeoutSeconds` are per-task and unqualified; **do not rename them in this
slice** - a rename is churn that would make the diff about naming instead of about bounds.

### `maxTasksPerJob = 5000`

A task in relay is a frame, a frame chunk, a build step, or one unit of a fan-out. The realistic high
end for a single submission is a full animation submitted one task per frame: a 1000 to 2000 frame
sequence. Chunking frames (the usual practice, because per-task dispatch and workspace-prep overhead
dominates for fast frames) puts the same sequence at a couple of hundred tasks. A build with a few
hundred steps and a parameter sweep of a few hundred units both land far below. 5000 is 2.5x to 5x
above that high end, so no submission a user plausibly wants is refused.

It still binds. At a realistic per-task JSON size - README's own example task is about 105 bytes with
a real Blender command line - the 1 MiB body already caps a request near 10,000 tasks, so 5000 binds
at half the realistic ceiling. Against minimal JSON (a 3-character unique name and a one-character
argv, about 34 bytes per task) the body permits roughly 30,000, so 5000 is a 6x reduction on the
worst case.

Two operational costs are what the number is actually protecting, and neither is mentioned in the
source item:

- `jobcreate.CreateJobFromSpec` inserts tasks one at a time, one round trip each, inside the caller's
  transaction. 5000 tasks is 5000 sequential round trips - a slow HTTP request, not a pathological
  one. 30,000 is not.
- `store.GetEligibleTasks` **has no `LIMIT`**, and `scheduler.Dispatcher.dispatch` runs it on every
  `Trigger()` and every 30 seconds. A large pending backlog is re-read in full on every tick until it
  drains. This is a fleet-wide property, not a per-request one, so a per-request cap only bounds one
  request's contribution to it; repetition still grows it, which is half 2's problem.

DO NOT RAISE THIS WITHOUT A REFUSED REAL SUBMISSION. If it is raised, the reason must be that a job
someone actually wanted to run was rejected, not that the number looks small. And before raising it,
look at the two costs above: `GetEligibleTasks`'s missing `LIMIT` and the per-task round trip are
what this number is standing in for, and fixing either of those is a better answer than a larger cap.

### `maxCommandsPerTask = 500`

`commands` exists so several steps share one prepared workspace and environment: sync, build, render,
publish, clean up. The realistic shape is single digits. The plausible high end is a task that
iterates a fixed list inside one prepared workspace - export N assets from a scene, bake N maps - which
is tens. 500 is roughly 20x that and about 230x below the ~116,000 runnable command entries a 1 MiB
body can express.

Past a few hundred, one task per unit is the better model anyway: separate tasks parallelize across
the fleet, retry independently, and report per-unit status, which is the entire point of a task graph.
Tasks sharing a `source` stream reuse the same workspace (`workspace_exclusive` defaults to false), so
splitting does not cost the workspace sharing that motivates `commands` in the first place. A user at
this bound is being told to use the better model, not being told no.

This is the concentration control: it bounds how much sequential work a single request can pin to a
single worker slot. At the bound, one task is 500 spawns per attempt and 5500 across a full retry
budget.

### `maxCommandsPerJob = 25000`

The total across all tasks, accumulated during validation. **This is the aggregate control and it is
the one that does the work.** See the next section for why the two per-axis caps do not produce it.

The legitimate high end for the total is set by the many-tasks shape, not the many-commands shape:
5000 tasks at 3 commands each is 15,000, and at a realistic size that spec is around 750 KB, which
only just fits the body limit. The few-tasks shape tops out lower - 20 tasks at the per-task cap is
10,000. So 25,000 is about 1.7x above the legitimate maximum and about 4.6x below the ~116,000 the
body permits with the cheapest runnable entry.

The window between "what a real job needs" and "what 1 MiB expresses" is narrow because a legitimate
command is a long string and an adversarial one is nine bytes. A count bound cannot tell them apart,
so it can only be placed inside that window, and the window is only about 8x wide. 25,000 is placed to
keep the legitimate side clear rather than to make the adversarial number small. That choice is
argued in "Rejected alternatives" and is the open question flagged at the end.

## Why three bounds and not two

Two independent per-axis caps whose product exceeds what the body limit already permits **reduce
nothing in aggregate**. They only change the shape of the worst case.

The aggregate cost that matters - subprocess spawns and `task_logs` rows - is
`total_commands x (1 + retries)`, and it does not care how the commands are distributed across tasks,
because the retry budget is per task and every task's commands re-run on every attempt. A job of one
task with 116,000 commands and a job of 232 tasks with 500 commands each cost the same 1.28 million
spawns at `retries: 10`. The distribution changes who pays (one worker slot versus the fleet) and
changes nothing about the total.

`maxTasksPerJob x maxCommandsPerTask` is 5000 x 500 = 2,500,000. That is about 21x more than a 1 MiB
body can express with the cheapest runnable entry, so with only the two per-axis caps in place the
binding constraint on total commands would remain `maxBodyBytes`, exactly as it is today. The slice
would ship two bounds, both correct, and the worst-case aggregate work per request would be
unchanged. "Each axis is bounded" would be true and would not be an answer.

So the third bound is warranted, and it is the only one of the three that moves the aggregate number:
116,000 to 25,000, a 4.6x reduction, and 1.28 million spawns to 275,000.

The per-axis caps are not redundant once it exists. `maxCommandsPerJob` implies `tasks <= 25000` (each
task needs at least one command), which is weaker than 5000; and it says nothing about concentration,
since 25,000 commands in one task satisfies it. Each of the three answers a different question:
how long is the transaction and how big is the dispatcher's backlog (tasks), how much can one request
pin to one worker slot (commands per task), how much total work can one request buy (commands per job).

## Not env-configurable

The `maxRetries` argument applies here identically, and it is now stronger than when it was written.

`Validate` runs on STORED `scheduled_jobs.spec` rows on five paths (enumerated below). An env-tunable
bound would make retroactive schedule invalidation environment-dependent: the same stored spec fires
on one replica's configuration and stops on another's, and lowering the knob would disable schedules
with no signal anywhere. A validation vocabulary shared by four ingest paths is a property of the
binary, exactly as the priority set is.

What has changed since that comment was written adds two new failure modes, both of which should be
named in the new constants' doc comments:

- `schedrunner.ValidateStoredSpecsOnStartup` writes `last_error` from the message `Validate` returned.
  With an env-tunable bound, the recorded failure text becomes a function of which replica happened to
  boot, and the number in a stored operator-facing string stops matching the binary that reads it.
- `api.handlePatchScheduledJob` clears the failure record if and only if
  `schedrunner.ValidateStoredSchedule` returns nil on the effective row. With a per-replica bound, a
  PATCH served by a lenient replica clears a record a strict replica will immediately re-write, and
  the operator sees the failure flicker.

DO NOT MAKE THESE ENV-CONFIGURABLE.

## Placement inside `Validate`

### The task-count check goes at the top, beside the zero check

Immediately after `if len(spec.Tasks) == 0`, before the priority switch:

```
at most %d tasks are allowed, got %d
```

Three reasons. The two checks are the two ends of one range on one field, and a future reader
changing either should see the other - the same adjacency argument that keeps the `retries` and
`timeout_seconds` bounds together. It is a job-level property, so it has no task name to interpolate
and does not belong in the per-task loop. And it refuses the spec before the work it bounds: before
`nameSet` is allocated at `len(spec.Tasks)` capacity and before 30,000 iterations of
`normalizeTaskCommands`.

**Precedence consequence, taken deliberately.** A spec that is both over the task count and carries an
invalid priority now reports the task count where today's code would report the priority. No test can
depend on the old precedence, since the bound is new, and nothing else reads the error text
positionally. The message wording deliberately mirrors "at least one task is required" so the pair
reads as one range.

### The per-task command check goes after `normalizeTaskCommands`, in the bounds block

Inside the existing per-task loop, in the block the current comment introduces with "Bounds last in
this loop body, so command-form and duplicate-name errors keep the precedence they have today", and
**first within that block**:

```
task %s: at most %d commands are allowed, got %d
```

It must come after `normalizeTaskCommands(ts)`. That function rewrites a legacy single `Command` into
a one-element `Commands` and clears `Command`, so a check placed after it covers both spellings by
construction, and a check placed before it would measure `len(ts.Commands) == 0` for every legacy
spec. The legacy form can only ever produce one command, so no legacy spec can exceed the bound - but
"the check is correct because the input cannot reach it" is a property that dies the moment the legacy
form gains a second element, and reading the normalized value costs nothing.

Placing it first within the bounds block is not arbitrary: the per-job total accumulates from the same
value on the next line, and checking the per-task bound first guarantees a task that is itself over
the per-task cap gets the specific, task-naming message rather than the job-level total message.

### The total accumulates in the same loop and fails as soon as it is exceeded

After the per-task check, `total += len(ts.Commands)`, then:

```
at most %d commands in total across all tasks are allowed
```

Checked inside the loop, not after it, so a 116,000-command spec is refused partway through traversal
rather than after a full pass. Job-level message, no task prefix: the budget is a property of the job,
and naming the task the accumulator happened to cross on would read as an accusation against a task
that may be entirely ordinary.

**No "got" clause, and that is a decision rather than an omission.** The other two messages report the
offending count because they know it. This one fires the moment the budget is exceeded and therefore
does not know the final total; printing the running count as if it were the total would be false, and
"got at least N" is honest but tells the operator nothing they can act on, since the actionable number
is the limit. The alternative - complete the pass, then report the exact total - trades the early
refusal for a nicer message and was not taken.

## Retroactivity

`jobspec.Validate` is retroactive over stored `scheduled_jobs.spec` rows. **A stored spec over any of
the three new bounds stops firing on the deploy that carries them.** This is the same hazard the
retry-bounds slice carried, and it must be stated in the PR.

### Every call site that re-validates STORED data

Found by grepping for `jobspec.Validate`, `ValidateJobSpec` and `ValidateStoredSchedule` and reading
each hit, including indirect ones. The retry-bounds slice's list of two is not complete for this
change.

1. **`schedrunner.fireOne`** (`internal/schedrunner/runner.go`) calls `jobspec.Validate` **directly**,
   hoisted above the overlap check by PR #159, and then reaches it a second time inside
   `jobcreate.CreateJobFromSpec`. Consequence: the schedule stops producing jobs, and `TickOnce`'s
   failure branch records the message in `last_error` / `last_error_at`.
2. **`api.handleRunScheduledJobNow`** (`internal/api/scheduled_jobs.go`) calls `ValidateJobSpec`
   **directly** on the stored spec, ahead of the transaction, and then again inside
   `CreateJobFromSpec`. That direct call is what makes run-now answer **400 with the per-task or
   per-job message** instead of the 500 that `relayclient.ErrorIsTransient` reads as retryable. It is
   the operator's interactive remedy and the reason a stored spec's failure is explainable at all.
3. **`schedrunner.ValidateStoredSpecsOnStartup`** (`internal/schedrunner/startup_validation.go`) via
   `ValidateStoredSchedule`, at boot, over every ENABLED schedule. Consequence: on the deploy that
   carries these bounds, every stored schedule over any of them records its failure immediately rather
   than at its next scheduled fire. **This is the surface the retry-bounds slice did not have and had
   to build afterwards** - it means this slice ships with operator visibility already in place, which
   is a materially better retroactivity story than the predecessor's.
4. **`api.handlePatchScheduledJob`'s clear-decision** (`internal/api/scheduled_jobs.go`) via
   `schedrunner.ValidateStoredSchedule`, on the *effective* row. When the PATCH does not include
   `job_spec`, the stored spec is what gets re-validated. Consequence: a cron-only PATCH on a schedule
   whose stored spec is over a new bound correctly does NOT clear the failure record, because the row
   it leaves behind still does not validate.
5. **`jobcreate.CreateJobFromSpec`** itself, reached from 1 and 2 above with stored data. Its error
   collapses into one "create job: %w", which is exactly why 1 and 2 both validate ahead of it.

For completeness, the ingest paths that validate fresh data and inherit the bounds for free:
`api.handleCreateJob`, `api.handleCreateScheduledJob`, `api.handlePatchScheduledJob` when the PATCH
carries a `job_spec`, `mcp.submit`, and `mcp.schedules_write` (create and update). The CLI and the web
SPA post JSON and hold no parallel validation, so they inherit through the API with no change; the SPA
form deliberately does not reimplement `Validate` (see `docs/plans/2026-07-01-job-submit-form-plan.md`).

### How likely is a real stored spec to be over these bounds

Much less likely than `retries: 50` was. A stored schedule over 5000 tasks is a job_spec above 200 KB,
and one over 25,000 total commands is larger still. But it is not impossible, the boot sweep will name
any that exist, and the PR must say so plainly rather than assert nobody is affected.

## Rejected alternatives

**Tighter caps (2000 tasks / 200 commands per task / 10,000 total).** This produces a much better
protection story: 17x rather than 4.6x on the aggregate, and 275,000 spawns becomes 110,000. It was
rejected because a 2000-frame animation submitted one task per frame is a real thing a user does, and
a bound that a real submission lands exactly on generates support tickets rather than protection. The
asymmetry decides it: the cost of refusing a legitimate render is a broken workflow with no workaround
inside the product, while the benefit is a constant factor on an attack that repetition makes
unbounded anyway. **Neither 4.6x nor 17x is a defence against an unrate-limited route.** Once the caps
cannot be the DoS control, their job is to remove the absurd and bound the transaction and dispatcher
costs, and the generous set does that. If half 2 never ships, revisit this choice - but revisit it by
shipping half 2, not by tightening these.

**Two per-axis caps only, accepting the product.** Rejected for the reason argued above: the product
exceeds what the body limit expresses, so the aggregate worst case would be unchanged and the slice
would ship a bound that reads as protection and provides none.

**A database CHECK constraint instead of, or as well as, a validator bound.** Rejected. `tasks` rows
carry no job-level count, so a per-job cap is not expressible as a row CHECK without a trigger or a
counter column, and `store.CreateTask` is already guarded against callers that bypass
`jobcreate.CreateJobFromSpec` (`internal/store/createtask_guard_test.go`). The Single job-spec pipeline
invariant puts the bound at the validator.

**Bounding total dependency edges in the same slice.** Rejected as scope. See refutation 5; it is a
different package and a different fix, and it wants its own item.

## Testing

The predecessor slice's test topology is the model: `internal/jobspec/jobspec_bounds_test.go` for the
unit-level bounds, `internal/api/jobs_spec_bounds_integration_test.go` and
`internal/cli/jobs_spec_bounds_integration_test.go` for the entry-point proofs. New cases join those
files rather than creating parallel ones.

Required:

- **Exactly at the bound is ACCEPTED, one over is REJECTED, on all three axes.** Both directions, six
  cases. The at-the-bound cases are the ones that catch an off-by-one written as `>=`, and they are
  the acceptance criterion the source item states explicitly.
- **The per-task message names the task**, on a task that is not the first in the list, so an
  implementation that reports index 0 regardless is caught. The predecessor's
  `TestValidate_AnOutOfRangeTaskIsRejectedAtAnyIndex` is the pattern.
- **The per-task command bound applies to the legacy `command` spelling too.** A spec using
  `command` cannot exceed the bound, so the assertion is that a legacy spec at one command is still
  accepted after the check is added - a guard against a check placed before `normalizeTaskCommands`
  that would read `len(Commands) == 0` and, if written as a range check, reject every legacy spec.
- **The per-task bound wins over the total bound** for a task that violates both. Assert the message
  names the task, not the job. This pins the ordering decision above.
- **The total bound fires on a spec where no single task and no single count is over its own bound.**
  Many tasks, each modest. This is the case two per-axis caps cannot catch and is the whole argument
  for the third constant; without it the third bound could be deleted and every test would stay green.
- **At least one real entry point**, per the source item's acceptance criterion: `POST /v1/jobs`
  answers 400 with the message.
- **The retroactivity path.** A stored schedule whose spec is over a new bound records its failure and
  stays enabled, and `POST /v1/scheduled-jobs/{id}/run` answers 400 with the message rather than 500.
  `internal/schedrunner/stored_spec_bounds_test.go` and the run-now bounds integration test already
  exercise this shape for `retries: 50`; the cheapest honest version is to extend them, not to
  duplicate them.
- **RED against today's code** for every one of the above before the constants exist.

## Advertisement surfaces

The bound must be stated everywhere the predecessor stated its own, and the predecessor's own claims
must be corrected:

- **README job-spec field table.** Add a `tasks` row (job-level count, max 5000) and a
  `tasks[].commands` row - the plural form is currently undocumented in that table entirely, which
  must be fixed in the same pass since this slice advertises a bound on a field the table does not
  list. State the total in whichever row reads best, and state the retroactivity consequence for
  stored schedules the way the `retries` and `timeout_seconds` rows already do.
- **README `tasks[].retries` row**, which says "a job's `tasks` and a task's `commands` are themselves
  unlimited". This slice makes that false. Rewrite it to name the three new bounds and to keep the
  honest part: the caps bound one request, not repetition.
- **`internal/jobspec/jobspec.go`'s `maxRetries` doc comment**, whose enumeration of stored-spec paths
  predates PR #159 and is now wrong (refutation 3). Correct it to the five-site list above. The same
  stale claim in `internal/schedrunner/stored_spec_bounds_test.go`'s `makeOverBudgetSpecJSON` header
  should be corrected in the same pass.
- **`docs/backlog/idea-2026-08-28-mcp-tool-schema-does-not-advertise-the-job-spec-bounds.md`** is an
  open item about `retries` and `timeout_seconds` not appearing in the MCP tool schema. This slice
  adds three more unadvertised bounds to exactly that gap. **Do not fix it here** - the item's own
  unresolved question (shared-type tags versus an MCP mirror, against the Single job-spec pipeline
  invariant) is a real design decision. Widen the item's scope to cover all five bounds and note in
  the PR that it was widened.

## Residual after this slice

Stated in the same register the retry-bounds slice used: **the counts are bounded, not that unbounded
work is impossible.**

With `maxCommandsPerJob = 25000` and `maxRetries = 10`, the worst case a single authenticated 1 MiB
`POST /v1/jobs` still buys is:

- **275,000 subprocess spawns** (25,000 commands x 11 attempts), spread over at least 50 tasks
  (25,000 / 500) and therefore over as many worker slots as the fleet has.
- **275,000 `task_logs` rows** from step markers alone, before any command output, and nothing in the
  repo prunes `task_logs`.
- **5000 sequential INSERT round trips** inside one transaction at job creation, plus up to roughly
  250,000 more if the spec is dense in `depends_on` edges, which this slice does not bound.
- **A pending backlog of up to 5000 tasks re-read in full by `GetEligibleTasks` on every dispatcher
  tick** until it drains, because that query has no `LIMIT`.

Down from roughly 116,000 commands, 1.28 million spawns and 1.28 million log rows before this slice: a
4.6x reduction on the aggregate and a 6x reduction on the task count.

**`POST /v1/jobs` remains unrate-limited.** `internal/api/server.go` wraps only
`POST /v1/auth/register` and `POST /v1/auth/login` in `RateLimit`, so every number above is a
per-request figure that an authenticated caller may repeat at whatever rate the network allows. There
is likewise no per-user quota on `POST /v1/scheduled-jobs`. The multiplier is smaller after this slice;
the total remains bounded only by how fast a caller can send requests. Half 2 of the source item is
the control for that, and this slice does not substitute for it.

## Open questions for the human

1. **The generous-versus-tight cap choice** (5000 / 500 / 25,000 versus 2000 / 200 / 10,000). The
   argument for generous is in "Rejected alternatives" and rests on the claim that these caps cannot
   be the DoS control, so refusing a real render is the worse error. If the intent is that this slice
   ships *instead of* rate limiting rather than *before* it, the tight set is the better call and the
   spec should be revised.
2. **Whether `maxCommandsPerJob` should report a running total** in its message ("got at least N")
   rather than naming only the limit. The spec picks the limit-only form; the alternative is
   defensible.
3. **Confirmation that the dependency-edge round-trip finding (refutation 5) becomes a new backlog
   item** rather than being folded in here.

## Gate decision (conductor, autonomous mode, 2026-08-29)

The spec gate auto-passes. Rulings on the three open questions:

1. **Generous set adopted: `maxTasksPerJob = 5000`, `maxCommandsPerTask = 500`,
   `maxCommandsPerJob = 25000`.** The premise the spec conditioned on holds: half 2 (rate-limiting
   `POST /v1/jobs`) is DEFERRED, not abandoned. The human's scope call was "caps only for this
   slice", and a fresh backlog item for the rate limit is filed before the source item is closed. So
   this ships *before* rate limiting, not *instead of* it, and the spec's own argument - that a cap
   low enough to be the DoS control is a cap that refuses real renders - decides it.
2. **Limit-only message form adopted.** The check fires mid-loop and does not know the final total;
   a "got at least N" figure that varies with task ordering for the same spec is worse than no
   figure.
3. **Confirmed: the dependency-edge round-trip finding (refutation 5) becomes its own backlog item**,
   not part of this slice. It is quadratic in task count, unbounded by `maxTasksPerJob`, and lives in
   `jobcreate`'s insert strategy rather than in validation - a different fix in a different package.

One correction carried into implementation: the phrase "shared by four ingest paths", inherited from
the `maxRetries` comment, is itself stale - there are six (`handleCreateJob`,
`handleCreateScheduledJob`, `handlePatchScheduledJob` with a body `job_spec`, `mcp.submit`, and
`mcp.schedules_write` on both create and update). The new constants' comments must not repeat the
count without re-deriving it.
